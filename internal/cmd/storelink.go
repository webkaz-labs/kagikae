package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// storeLinkRelPath is where a bound directory carries the convenience symlink to
// one tool's store, relative to that directory: ./.config/<tool>.
//
// The store itself stays in kae's data dir (docs/SECURITY.md § Store links in a
// bound directory says why it is not moved here); this is a pointer, so the
// directory a user is working in can answer "where is this project's claude
// config?" without hashing its own path. Under .config/ because the fragment
// already put a kae-owned entry there, and named after the tool, which is why it
// cannot collide with the fragment's own path
// (TestStoreLinkPathsCannotCollideWithTheFragment).
func storeLinkRelPath(tool string) string { return filepath.Join(".config", tool) }

// linkableTools lists the tools a store link can exist for, in a fixed order so
// the report and the exclude entries do not reorder between runs. A tool with no
// isolation env var has no per-directory store to point at.
func linkableTools() []string {
	out := make([]string, 0, len(constants.Tools))
	for _, tool := range constants.Tools {
		if isolationEnvVar(tool) != "" {
			out = append(out, tool)
		}
	}
	return out
}

// syncStoreLinks converges the store links of tools on want (tool → the store
// its isolation env var points at). A tool in tools with no entry in want has
// its kae-made link removed, which is what makes this converge rather than only
// grow: a tool dropped from the profile, and `--no-link` (an empty want), would
// otherwise leave a link aimed at a store the directory no longer binds.
//
// It returns the link paths it created or refreshed and the ones it removed, for
// the caller to report and to record ignore rules for. Nothing here fails the
// command; warnStoreLink says why.
func (app *App) syncStoreLinks(tools []string, want map[string]string) (linked, removed []string) {
	for _, tool := range tools {
		path := storeLinkRelPath(tool)
		target := want[tool]
		if target == "" {
			if app.removeStoreLink(path) {
				removed = append(removed, path)
			}
			continue
		}
		if app.ensureStoreLink(path, target) {
			linked = append(linked, path)
		}
	}
	return linked, removed
}

// wantedStoreLinks is the store link set a whole bind asks for: every tool the
// bind actually points somewhere, whose store is a directory on disk when the
// links are made.
//
// A warning entry is left out because it has no store at all — that tool keeps
// its real home, so there is nothing here to point at.
//
// The dirExists check re-reads the disk rather than trusting the plan, because
// the link's contract is what the fragment's [env] block promises: the path it
// names is an existing store directory. The materializers create that directory
// before they write anything into it (prepareBond, preparePinConfig), so a
// prepared tool normally has one — including when its credential could not be
// written, which only warns (warnUnisolatableCredential). This is the guard that
// keeps a link from naming a directory anyway, whatever left it missing.
func wantedStoreLinks(entries []isolationEntry) map[string]string {
	want := map[string]string{}
	for _, entry := range entries {
		if entry.Warning != "" || entry.Dir == "" {
			continue
		}
		if dirExists(entry.Dir) {
			want[entry.Tool] = entry.Dir
		}
	}
	return want
}

// linkState is what is sitting at a store link's path. A named type rather than
// a bool pair or a bare int: the answer is a three-way classification, and
// ensureStoreLink reads as one because of it.
type linkState int

const (
	storeLinkAbsent  linkState = iota // nothing there; kae may create the link
	storeLinkKae                      // a symlink into kae's isolation root: kae's to re-aim or remove
	storeLinkForeign                  // anything else, including an unreadable path: the user's
)

// storeLinkState classifies path without following it. Lstat and Readlink rather
// than Stat on purpose: a kae link whose store was deleted is broken, and Stat
// would report it as absent — kae would then try to create a link over a name
// that already exists, and leave the stale one behind when it failed.
//
// A path kae cannot even Lstat is classified foreign, not absent: an unreadable
// path is one kae has established nothing about, and the safe reading of that is
// "not mine".
func (app *App) storeLinkState(path string) (target string, state linkState) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", storeLinkAbsent
		}
		return "", storeLinkForeign
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", storeLinkForeign
	}
	target, err = os.Readlink(path)
	if err != nil || !app.isKaeManagedHome(target) {
		return "", storeLinkForeign
	}
	return target, storeLinkKae
}

// ensureStoreLink points path at target, reporting whether the link is now in
// place. target must be absolute: the link is read by the user, and by
// kaeManagedHomeKind on the next run, neither of which resolves it against the
// directory it sits in.
//
// It refuses to replace anything kae did not put there — a real directory, a real
// file, or a symlink aimed outside kae's isolation root — because that is the
// user's own data and a convenience pointer is never worth destroying it. The
// refusal is a stderr warning and a false return, not an error (warnStoreLink);
// docs/CLI.md § kae pin states that a conflict skips the link and leaves the
// bind successful.
func (app *App) ensureStoreLink(path, target string) bool {
	switch current, state := app.storeLinkState(path); state {
	case storeLinkAbsent:
	case storeLinkKae:
		if current == target {
			return true
		}
		// Re-aimed rather than left: this is the re-pin to another account, where
		// the old target is a store the directory no longer binds.
		if err := os.Remove(path); err != nil {
			return warnStoreLink(path, err)
		}
	default: // storeLinkForeign
		fmt.Fprintf(os.Stderr,
			"kae: warning: %s is not a kae link; leaving it alone. This directory's %s store is %s\n",
			path, filepath.Base(path), app.displayPath(target))
		return false
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return warnStoreLink(path, err)
	}
	if err := os.Symlink(target, path); err != nil {
		return warnStoreLink(path, err)
	}
	return true
}

// removeStoreLink deletes a kae-made store link, reporting whether it removed
// one. Anything else at that path — including a symlink pointing somewhere kae
// does not own — is left where it is, for the same reason ensureStoreLink will
// not overwrite it.
func (app *App) removeStoreLink(path string) bool {
	if _, state := app.storeLinkState(path); state != storeLinkKae {
		return false
	}
	if err := os.Remove(path); err != nil {
		return warnStoreLink(path, err)
	}
	return true
}

// warnStoreLink reports a store link kae could not write or remove and returns
// false, so every caller stays one line.
//
// **Nothing in the links step fails the command.** By the time it runs the stores
// are materialized, the credential is written and the fragment is in place, so
// the directory is bound either way — the same reason ensureGitExcluded only
// warns. Every problem here goes to stderr and leaves the exit code alone
// (docs/CLI.md § Output Rules).
func warnStoreLink(path string, err error) bool {
	fmt.Fprintf(os.Stderr, "kae: warning: could not update the store link %s: %v\n", path, err)
	return false
}

// reportStoreLinks prints what the links step did. excludeFile is where the
// caller recorded the ignore rules for linked (empty when it recorded none), so
// the report can name it; recording is the caller's step because one call covers
// the fragment and the links together (ensureGitExcluded).
//
// Removals need no rule, and an entry left in the exclude file is harmless —
// `kae unpin` leaves the fragment's own entry there for the same reason.
func (app *App) reportStoreLinks(linked, removed []string, excludeFile string) {
	if len(linked) > 0 {
		noun := "store"
		if len(linked) > 1 {
			noun = "stores"
		}
		if excludeFile != "" {
			fmt.Printf("Linked %s to this directory's %s (ignored via %s).\n",
				strings.Join(linked, ", "), noun, app.displayPath(excludeFile))
		} else {
			fmt.Printf("Linked %s to this directory's %s.\n", strings.Join(linked, ", "), noun)
		}
	}
	if len(removed) > 0 {
		noun := "link"
		if len(removed) > 1 {
			noun = "links"
		}
		fmt.Printf("Removed the store %s %s.\n", noun, strings.Join(removed, ", "))
	}
}
