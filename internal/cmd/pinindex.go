package cmd

import (
	"bytes"
	"fmt"
	"os"
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
// It holds a path, never a secret. Its location is paths.PinRecordFile.

// acquirePinLock serializes the commands that touch one directory's binding, so
// a `kae pin` and a `kae unpin` in the same directory cannot interleave into a
// fragment pointing at a store the other re-keyed. Per bound directory, and
// fail-fast: see docs/ARCHITECTURE.md § Locking. A half-done bind is re-runnable,
// which is why nothing has to be rolled back.
func (app *App) acquirePinLock(absDir string) (*lock.Lock, error) {
	return app.acquireNamedLock("pin-"+paths.PinID(absDir),
		"another kae process is binding this directory; retry shortly")
}

// beginBind is what a command that *binds* a directory calls instead of
// acquirePinLock: it takes the same lock and records the breadcrumb in one step.
// One seam, so a future binding command cannot take the lock and forget the
// record — which is the failure this whole file exists to prevent. `kae unpin`
// uses the bare lock: it is removing a binding, not making one.
func (app *App) beginBind(absDir string) (*lock.Lock, error) {
	l, err := app.acquirePinLock(absDir)
	if err != nil {
		return nil, err
	}
	if err := app.recordPinnedDir(paths.PinID(absDir), absDir); err != nil {
		l.Release()
		return nil, err
	}
	return l, nil
}

// pinnedDir is one bound directory kae has materialized a per-directory store
// for.
type pinnedDir struct {
	PinID string
	Dir   string // the absolute path recorded when the directory was bound
}

// recordPinnedDir writes the breadcrumb naming absDir inside that directory's
// own store root. Idempotent; called by every path that binds a directory.
//
// Re-pinning an already-recorded directory is the common case, and the atomic
// write is a temp file plus an fsync plus a rename — so read first and skip it
// when the bytes already match.
func (app *App) recordPinnedDir(pinID, absDir string) error {
	record := app.Paths.PinRecordFile(pinID)
	want := []byte(absDir + "\n")
	if have, err := os.ReadFile(record); err == nil && bytes.Equal(have, want) {
		return nil
	}
	if err := os.MkdirAll(app.Paths.PinDir(pinID), 0o700); err != nil {
		return fmt.Errorf("create per-directory store root: %w", err)
	}
	return patch.WriteFileAtomic(record, want, 0o600)
}

// pinnedDirs lists the bound directories kae has a store for. A store without
// a breadcrumb is skipped rather than reported: it was bound by a kae older
// than the record, and inventing a path for it would be a guess.
//
// complete is false when any store was skipped that way. Most callers report on the
// bindings they can name and do not care; a caller that acts on the *absence* of a
// binding does, because a skipped store is "kae could not look", not "nothing is
// there" — credStoreRefs is the one that must tell those apart before deleting a
// credential its siblings read.
func (app *App) pinnedDirs() ([]pinnedDir, error) {
	pins, _, err := app.pinnedDirsComplete()
	return pins, err
}

func (app *App) pinnedDirsComplete() ([]pinnedDir, bool, error) {
	entries, err := os.ReadDir(app.Paths.IsolationDir())
	if os.IsNotExist(err) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("list per-directory stores: %w", err)
	}
	pins, complete := []pinnedDir{}, true
	for _, entry := range entries {
		// isolation/global holds the global-isolated homes (kae use -i), which
		// belong to no directory.
		if !entry.IsDir() || entry.Name() == paths.GlobalSegment {
			continue
		}
		data, err := os.ReadFile(app.Paths.PinRecordFile(entry.Name()))
		if err != nil {
			complete = false // a store kae cannot name is not a store that is not there
			continue
		}
		dir := strings.TrimSpace(string(data))
		if dir == "" {
			complete = false
			continue
		}
		pins = append(pins, pinnedDir{PinID: entry.Name(), Dir: dir})
	}
	return pins, complete, nil
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
// tool/accountName, which the caller has just removed or renamed. replacement is
// the account to re-bind to — the new name after a rename, empty after a removal,
// where kae has none to suggest.
func (app *App) warnPinnedAccountGone(tool, accountName, replacement string) {
	if replacement == "" {
		replacement = "<account>"
	}
	app.warnPinnedDirs(
		func(info fragmentInfo) bool { return info.Accounts[tool] == accountName },
		func(dir string) string {
			return fmt.Sprintf("%s is still pinned to %s/%s, which no longer exists; re-bind it with: cd %s && kae pin %s %s",
				dir, tool, accountName, dir, tool, replacement)
		},
	)
}

// warnPinnedDirs prints one stderr warning per bound directory the caller's edit
// just invalidated. The loop is shared so the stream, the prefix and any future
// suppression stay in one place.
func (app *App) warnPinnedDirs(match func(fragmentInfo) bool, message func(dir string) string) {
	for _, dir := range app.pinnedDirsMatching(match) {
		fmt.Fprintf(os.Stderr, "kae: warning: %s\n", message(dir))
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
		// "Gone" is decided by the directory, never by a failed read of the
		// fragment inside it: this branch tells the user to delete a store, so a
		// permission error or an I/O blip on a live directory must not reach it.
		if !dirExists(pin.Dir) {
			// Deleted or moved. Nothing can reach its store again — the store is
			// named by a hash of the path, and the breadcrumb is the only thing
			// left that can still name it.
			checks = append(checks, adapter.Check{
				Code: constants.CheckPinStale, Status: constants.StatusWarn,
				Message: fmt.Sprintf(
					"%s was bound with kae pin but is gone; its per-directory store is orphaned (remove %s to reclaim it)",
					pin.Dir, app.displayPath(app.Paths.PinDir(pin.PinID)),
				),
			})
			continue
		}
		info, exists, ferr := readFragmentAt(pin.Dir)
		if ferr != nil {
			checks = append(checks, adapter.Check{
				Code: constants.CheckPinStale, Status: constants.StatusWarn,
				Message: fmt.Sprintf(
					"%s is bound with kae pin but its fragment could not be read (%v), so its binding was not checked",
					pin.Dir, ferr,
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
