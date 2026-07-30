package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/patch"
	"github.com/webkaz-labs/kagikae/internal/paths"
)

// A bound directory used to leave no trace kae could find from anywhere else.
// The fragment that points at the store lives *in* that directory, and the
// store is named by a hash of the path, so nothing outside could answer "which
// directories bind this account" — `kae account rm`, `kae account rename` and
// `kae profile rm` removed the thing a directory was bound to and said nothing,
// and a bound directory that was deleted or moved left a store no command could
// ever name again.
//
// The record is a breadcrumb *inside* the store, not a registry beside it. A
// registry would be a second source of truth that drifts from the directory
// tree the stores actually live in, and would need a repair path of its own;
// here the store's own existence is the record, so the two cannot disagree.
// It holds a path, never a secret.
const pinDirRecord = "dir"

// acquirePinLock serializes the commands that bind one directory. `kae pin`
// writes the credential, then the companion files, then the fragment, with no
// step atomic across the others, so two of them at once in the same directory
// could interleave into a fragment that points at a store the other command
// re-keyed. The lock is per bound directory (its pin id), not global: binding
// two different directories at once is fine, and pinning is interactive enough
// that a busy lock is better reported than queued.
//
// There is deliberately no backup of the previous per-directory credential —
// `kae rollback` has no pin state and does not need one. That credential is a
// copy of the account snapshot (writeDirCredential reads the snapshot, never the
// live store), so `kae pin <tool> <account>` reproduces it exactly; a half-done
// bind is re-runnable, not lost data.
func (app *App) acquirePinLock(absDir string) (*lock.Lock, error) {
	return app.acquireNamedLock("pin-"+paths.PinID(absDir),
		"another kae process is binding this directory; retry shortly")
}

// pinnedDir is one bound directory kae has materialized a per-directory store
// for.
type pinnedDir struct {
	PinID string
	Dir   string // the absolute path recorded when the directory was bound
}

// recordPinnedDir writes the breadcrumb naming absDir inside that directory's
// own store root. Idempotent; called by every path that binds a directory.
func (app *App) recordPinnedDir(pinID, absDir string) error {
	root := app.Paths.PinDir(pinID)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create per-directory store root: %w", err)
	}
	return patch.WriteFileAtomic(filepath.Join(root, pinDirRecord), []byte(absDir+"\n"), 0o600)
}

// pinnedDirs lists the bound directories kae has a store for. A store without
// a breadcrumb is skipped rather than reported: it was bound by a kae older
// than the record, and inventing a path for it would be a guess.
func (app *App) pinnedDirs() ([]pinnedDir, error) {
	entries, err := os.ReadDir(app.Paths.IsolationDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list per-directory stores: %w", err)
	}
	pins := []pinnedDir{}
	for _, entry := range entries {
		// isolation/global holds the global-isolated homes (kae use -i), which
		// belong to no directory.
		if !entry.IsDir() || entry.Name() == paths.GlobalSegment {
			continue
		}
		data, err := os.ReadFile(filepath.Join(app.Paths.IsolationDir(), entry.Name(), pinDirRecord))
		if err != nil {
			continue
		}
		if dir := strings.TrimSpace(string(data)); dir != "" {
			pins = append(pins, pinnedDir{PinID: entry.Name(), Dir: dir})
		}
	}
	return pins, nil
}

// pinnedDirsMatching returns the bound directories still pinned whose fragment
// satisfies match, for warning about a binding an edit just invalidated. A
// directory that is gone, or that was unpinned, matches nothing: `kae unpin`
// keeps the store on purpose, so its leftovers are doctor's business
// (pinChecks), not this warning's.
func (app *App) pinnedDirsMatching(match func(fragmentInfo) bool) []string {
	pins, err := app.pinnedDirs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "kae: warning: %v\n", err)
		return nil
	}
	matched := []string{}
	for _, pin := range pins {
		info, exists, err := readFragmentAt(pin.Dir)
		if err != nil || !exists {
			continue
		}
		if match(info) {
			matched = append(matched, pin.Dir)
		}
	}
	return matched
}

// warnPinnedAccountGone warns for every bound directory still pinned to
// tool/accountName, which the caller has just removed or renamed. fix is the
// `kae pin` invocation that re-binds the directory.
func (app *App) warnPinnedAccountGone(tool, accountName, fix string) {
	for _, dir := range app.pinnedDirsMatching(func(info fragmentInfo) bool {
		return info.Accounts[tool] == accountName
	}) {
		fmt.Fprintf(os.Stderr,
			"kae: warning: %s is still pinned to %s/%s, which no longer exists; re-bind it with: cd %s && %s\n",
			dir, tool, accountName, dir, fix)
	}
}

// pinChecks reports bound directories whose binding kae can no longer honor.
// Offline: it reads the breadcrumbs, the fragments they name, and the account
// snapshots on disk.
func (app *App) pinChecks() []adapter.Check {
	pins, err := app.pinnedDirs()
	if err != nil {
		return []adapter.Check{{
			Code: constants.CheckPinStale, Status: constants.StatusWarn, Message: err.Error(),
		}}
	}
	checks := []adapter.Check{}
	for _, pin := range pins {
		info, exists, ferr := readFragmentAt(pin.Dir)
		if ferr != nil || !dirExists(pin.Dir) {
			// The directory was deleted or moved. Nothing can reach its store
			// again — the breadcrumb is the only thing that can still name it.
			checks = append(checks, adapter.Check{
				Code: constants.CheckPinStale, Status: constants.StatusWarn,
				Message: fmt.Sprintf(
					"%s was bound with kae pin but is gone; its per-directory store is orphaned (remove %s to reclaim it)",
					pin.Dir, app.displayPath(app.Paths.PinDir(pin.PinID)),
				),
			})
			continue
		}
		if !exists {
			continue // unpinned; the store is kept on purpose so a re-pin restores it
		}
		for tool, accountName := range info.Accounts {
			if _, found, aerr := account.Load(app.Paths.AccountDir(tool, accountName)); aerr == nil && found {
				continue
			}
			checks = append(checks, adapter.Check{
				Tool: tool,
				Code: constants.CheckPinStale, Status: constants.StatusWarn,
				Message: fmt.Sprintf(
					"%s is pinned to %s/%s, which is not captured; re-bind it with: cd %s && kae pin %s <account>",
					pin.Dir, tool, accountName, pin.Dir, tool,
				),
			})
		}
	}
	return checks
}
