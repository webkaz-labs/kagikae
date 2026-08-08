package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/keychain"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// pinHere binds a temp cwd with overlayTestApp's profile (claude/main, never
// captured) and returns the bound directory.
func pinHere(t *testing.T, app *App, mode string) string {
	t.Helper()
	return pinHereAs(t, app, "main", mode)
}

// pinHereAs is pinHere for a fixture that needs a second directory bound to a
// *different* profile — two directories reading two different accounts' credential
// stores is the state the shared-store attribution turns on.
func pinHereAs(t *testing.T, app *App, profile, mode string) string {
	t.Helper()
	chdirTemp(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var code int
	var out string
	pin := func() {
		code, out = captureStdout(t, func() int {
			return runPin(context.Background(), app, commonOpts{Format: formatText}, profile, mode)
		})
	}
	// Only when the caller has installed nothing. 8 of this helper's call sites run
	// inside their own `runner.With`, and shadowing it is not neutral: their fake is
	// what the test then asserts on, so a pin whose `add-generic-password` lands in a
	// throwaway leaves the caller's simulator holding no account — which turns its
	// account-scoping arm into an unconditional match and stops it killing a mutation
	// of the account kae writes. Measured: `DeleteItemForAccount`'s account mutated,
	// killed by TestUnpinPurgeRemovesACredentialNothingElseBinds before shadowing and
	// survived after. Shadowing also un-fakes every *other* command for those sites,
	// which sent `ensureGitExcluded` to real git.
	if _, none := runner.Default.(runner.OSRunner); none {
		runner.With(pinFixtureRunner{}, pin)
	} else {
		pin()
	}
	mustExit(t, constants.ExitOK, code, out)
	return cwd
}

// pinFixtureRunner answers the `security` CLI as an empty keychain — every lookup
// misses, every write and delete succeeds — and hands every other command to the
// real runner.
//
// It exists because pinHereAs used to call runPin with **no runner installed at
// all**, and a pin whose adapter resolves to the keychain driver then ran the real
// `security`. Not hypothetical in either direction. On darwin it wrote a genuine
// login-keychain item per pin: 956 `Claude Code-credentials-*` items under claude's
// fallback account were on the operator's machine on 2026-08-09, 448 and 508 of them
// created on the two days this branch was developed — by `mise run check`, the gate
// AGENTS.md says must never touch the real environment. On linux there is no
// `security`, so the same call made the pin exit 1 and two tests failed, which is how
// CI found it on this branch's first ever CI run.
//
// **Deliberately stateless, and the reason is a measurement rather than taste.** An
// earlier version stored what it was told and replayed it. Eleven mutations of that
// version — always-miss, store-nothing, drop the `-a` scoping either way, empty
// payload, empty account, key by service+account, a shared instance — every one of
// them survived the whole package. Nothing here is a subject; no caller asserts on a
// value this fixture produced, so a store is state no test can observe, and the two
// rationales the earlier comment gave for its shape (that a pin's git answer is
// needed, that the keying must be service+account) were both refuted by their own
// mutants. Per AGENTS.md that reason belongs in a comment rather than in a test that
// cannot fail. What the callers do need is only that the pin **succeeds**: a write
// that reports 0 and a lookup that is honest about an empty keychain.
//
// The pass-through is not decoration either: `ensureGitExcluded` asks git for the
// common dir and the prefix, and answering that from here would be inventing a repo
// layout. It goes to the same git the parent commit used.
type pinFixtureRunner struct{}

func (p pinFixtureRunner) Run(ctx context.Context, name string, args ...string) (string, string, int) {
	if name != "security" || len(args) == 0 {
		return runner.OSRunner{}.Run(ctx, name, args...)
	}
	switch args[0] {
	case "add-generic-password", "delete-generic-password":
		return "", "", 0
	}
	return "", "security: " + keychain.NotFoundMarker, 44
}

// RunInput forwards stdin, because the pass-through reaches real programs that read
// it — `secret-tool store` takes the secret that way, so dropping it here would run a
// real credential helper against an empty payload.
func (p pinFixtureRunner) RunInput(ctx context.Context, stdin, name string, args ...string) (string, string, int) {
	if name != "security" || len(args) == 0 {
		return runner.OSRunner{}.RunInput(ctx, stdin, name, args...)
	}
	return p.Run(ctx, name, args...)
}

// TestPinRecordsTheBoundDirectory pins that a bound directory is findable from
// outside it. The fragment that names the store lives in the directory and the
// store is named by a hash of the path, so without the breadcrumb no command
// anywhere else can answer "which directories bind this".
func TestPinRecordsTheBoundDirectory(t *testing.T) {
	app := overlayTestApp(t)
	cwd := pinHere(t, app, modeShared)

	pins, err := app.pinnedDirs()
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 1 {
		t.Fatalf("pinnedDirs() = %v, want exactly the directory just bound", pins)
	}
	if pins[0].Dir != cwd || pins[0].PinID != paths.PinID(cwd) {
		t.Fatalf("pinnedDirs() = %+v, want {PinID: %s, Dir: %s}", pins[0], paths.PinID(cwd), cwd)
	}

	// The record is what makes the directory reachable by what it binds.
	matched := app.pinnedDirsMatching(func(info fragmentInfo) bool {
		return info.Accounts[constants.ToolClaude] == "main"
	})
	if len(matched) != 1 || matched[0] != cwd {
		t.Fatalf("pinnedDirsMatching = %v, want [%s]", matched, cwd)
	}
	// A binding it does not have must not match, or every warning is noise.
	if other := app.pinnedDirsMatching(func(info fragmentInfo) bool {
		return info.Accounts[constants.ToolClaude] == "side"
	}); len(other) != 0 {
		t.Fatalf("pinnedDirsMatching matched an unbound account: %v", other)
	}
}

// TestPinChecksReportStaleBindings covers both ways a binding stops being
// honorable: the account it names is gone (what `kae account rm`/`rename` and
// `kae profile rm` used to do in silence), and the directory itself is gone
// (which strands the store forever, since only the breadcrumb can still name it).
func TestPinChecksReportStaleBindings(t *testing.T) {
	app := overlayTestApp(t)
	cwd := pinHere(t, app, modeShared)

	// A directory that was bound and then deleted or moved.
	gone := filepath.Join(t.TempDir(), "moved-away")
	if err := app.recordPinnedDir("00112233445566778899aabbccddeeff", gone); err != nil {
		t.Fatal(err)
	}

	checks := app.pinChecks()
	if len(checks) != 2 {
		t.Fatalf("pinChecks() = %+v, want one dangling-account and one orphaned-store check", checks)
	}
	var dangling, orphan string
	for _, c := range checks {
		if c.Code != constants.CheckPinStale || c.Status != constants.StatusWarn {
			t.Fatalf("unexpected check %+v", c)
		}
		if strings.Contains(c.Message, gone) {
			orphan = c.Message
		}
		if strings.Contains(c.Message, cwd) {
			dangling = c.Message
		}
	}
	if !strings.Contains(dangling, "claude/main") || !strings.Contains(dangling, "kae pin claude") {
		t.Fatalf("the dangling-account check must name the binding and the fix: %q", dangling)
	}
	if !strings.Contains(orphan, "orphaned") {
		t.Fatalf("the deleted directory's store must be reported as orphaned: %q", orphan)
	}

	// Capturing the account it binds silences the dangling half, so the check
	// tracks the binding rather than merely the existence of a pin.
	seedAccountMeta(t, app, constants.ToolClaude, "main")
	for _, c := range app.pinChecks() {
		if strings.Contains(c.Message, cwd) {
			t.Fatalf("a captured account must not warn: %q", c.Message)
		}
	}
}

// TestUnpinnedDirectoryIsNotReportedStale pins the deliberate asymmetry:
// `kae unpin` keeps the store so a re-pin restores its sessions, so a directory
// that still exists but is no longer pinned is not a finding.
func TestUnpinnedDirectoryIsNotReportedStale(t *testing.T) {
	app := overlayTestApp(t)
	pinHere(t, app, modeShared)
	if code, out := captureStdout(t, func() int {
		return runUnpin(context.Background(), app, commonOpts{Format: formatText}, false)
	}); code != constants.ExitOK {
		t.Fatalf("unpin exit %d (%s)", code, out)
	}
	if checks := app.pinChecks(); len(checks) != 0 {
		t.Fatalf("an unpinned directory must not warn: %+v", checks)
	}
}

// TestPinCheckDoesNotCallALiveDirectoryOrphaned pins the distinction the
// orphaned branch depends on. It tells the user to delete a per-directory store
// — sessions, settings and a credential — so it must fire only when the bound
// directory is confirmed gone, never when the fragment inside a live directory
// merely could not be read (a permission change, an I/O error on a network
// mount).
func TestPinCheckDoesNotCallALiveDirectoryOrphaned(t *testing.T) {
	app := overlayTestApp(t)
	cwd := pinHere(t, app, modeShared)
	// A fragment that exists but cannot be read: a directory in its place makes
	// ReadFile fail with something other than "not exist".
	if err := os.Remove(fragmentRelPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(fragmentRelPath, 0o700); err != nil {
		t.Fatal(err)
	}

	checks := app.pinChecks()
	if len(checks) != 1 {
		t.Fatalf("pinChecks() = %+v, want one unreadable-fragment check", checks)
	}
	if strings.Contains(checks[0].Message, "orphaned") {
		t.Fatalf("a live directory must never be reported as orphaned: %q", checks[0].Message)
	}
	if !strings.Contains(checks[0].Message, cwd) || !strings.Contains(checks[0].Message, "could not be read") {
		t.Fatalf("the check must name the directory and say the fragment was unreadable: %q", checks[0].Message)
	}
}
