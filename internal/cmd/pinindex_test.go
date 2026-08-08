package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	runner.With(&pinFixtureRunner{}, func() {
		code, out = captureStdout(t, func() int {
			return runPin(context.Background(), app, commonOpts{Format: formatText}, profile, mode)
		})
	})
	mustExit(t, constants.ExitOK, code, out)
	return cwd
}

// pinFixtureRunner emulates the `security` CLI in memory and hands every other
// command to the real runner, because a pin also asks git where the repository
// root is and that answer is part of what the fixtures set up.
//
// It exists because pinHereAs used to call runPin with **no runner installed at
// all**, and a pin whose adapter resolves to the keychain driver then ran the real
// `security`. That is not hypothetical in either direction. On darwin it wrote a
// genuine login-keychain item per pin: 956 `Claude Code-credentials-*` items under
// claude's fallback account were found on the operator's machine on 2026-08-09,
// 448 and 508 of them created on the two days this branch was developed — from
// `mise run check`, the gate AGENTS.md says must never touch the real environment.
// On linux there is no `security`, so the same call made the pin exit 1 and two
// tests failed — which is how CI found it, on this branch's first ever CI run.
//
// Keyed by service **and** account rather than holding one item, because a pin
// writes one item per bound directory and these fixtures bind more than one; a
// single-item fake would let one directory's write answer another's read.
type pinFixtureRunner struct {
	payload map[string]string
	account map[string]string
}

func (p *pinFixtureRunner) Run(ctx context.Context, name string, args ...string) (string, string, int) {
	if name != "security" || len(args) == 0 {
		return runner.OSRunner{}.Run(ctx, name, args...)
	}
	if p.payload == nil {
		p.payload, p.account = map[string]string{}, map[string]string{}
	}
	svc := valueAfter(args, "-s")
	notFound := "security: " + keychain.NotFoundMarker
	switch args[0] {
	case "find-generic-password":
		stored, ok := p.account[svc]
		if !ok {
			return "", notFound, 44
		}
		// An account-scoped read sees the item only when the accounts agree: service
		// plus account is how a service holding more than one legitimate item is
		// addressed, and kae's own guard requires every keychain spec to be scoped.
		if want := valueAfter(args, "-a"); want != "" && stored != "" && want != stored {
			return "", notFound, 44
		}
		if slices.Contains(args, "-w") {
			return p.payload[svc], "", 0
		}
		return fmt.Sprintf("keychain: \"login\"\nattributes:\n    \"acct\"<blob>=\"%s\"\n", stored), "", 0
	case "add-generic-password":
		p.payload[svc] = valueAfter(args, "-w")
		p.account[svc] = valueAfter(args, "-a")
		return "", "", 0
	case "delete-generic-password":
		stored, ok := p.account[svc]
		if !ok {
			return "", notFound, 44
		}
		if want := valueAfter(args, "-a"); want != "" && stored != "" && want != stored {
			return "", notFound, 44
		}
		delete(p.payload, svc)
		delete(p.account, svc)
		return "", "", 0
	}
	return "", "", 0
}

func (p *pinFixtureRunner) RunInput(ctx context.Context, _ string, name string, args ...string) (string, string, int) {
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
