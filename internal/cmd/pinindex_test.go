package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/keychain"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

func TestPinIndexIncompleteChecks(t *testing.T) {
	for _, shape := range []string{"missing", "empty", "unreadable", "root-unreadable"} {
		t.Run(shape, func(t *testing.T) {
			app := testApp(t, nil)
			if checks := app.pinChecks(""); len(checks) != 0 {
				t.Fatalf("an absent index is complete: %+v", checks)
			}
			if shape == "root-unreadable" {
				writeFile(t, app.Paths.IsolationDir(), "not a directory")
			} else {
				// A valid record remains diagnosable alongside both broken records.
				if err := app.recordPinnedDir("main", filepath.Join(t.TempDir(), "gone")); err != nil {
					t.Fatal(err)
				}
				for _, id := range []string{"side", "alt"} {
					record := app.Paths.PinRecordFile(id)
					if err := os.MkdirAll(app.Paths.PinDir(id), 0o700); err != nil {
						t.Fatal(err)
					}
					switch shape {
					case "empty":
						writeFile(t, record, " \n\t")
					case "unreadable":
						// A directory yields a deterministic read error, even as root.
						if err := os.Mkdir(record, 0o700); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			for _, filter := range []string{"", constants.ToolClaude, constants.ToolCodex} {
				checks := app.pinChecks(filter)
				incomplete, stale := 0, 0
				for _, check := range checks {
					switch check.Code {
					case constants.CheckPinIndexIncomplete:
						incomplete++
						if check.Tool != "" || check.Status != constants.StatusWarn {
							t.Fatalf("expected machine-wide warn: %+v", check)
						}
					case constants.CheckPinStale:
						stale++
					}
				}
				wantStale := 0
				if filter == "" && shape != "root-unreadable" {
					wantStale = 1
				}
				if incomplete != 1 || stale != wantStale || len(checks) != 1+wantStale {
					t.Fatalf("filter %q: incomplete=%d stale=%d checks=%+v", filter, incomplete, stale, checks)
				}
			}
		})
	}
}

func TestDoctorPinIndexIncompleteOutput(t *testing.T) {
	app := testApp(t, nil)
	seedClaude(t, app, mainToken, "main-uuid")
	for _, id := range []string{"side", "alt"} {
		if err := os.MkdirAll(app.Paths.PinDir(id), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, filter := range []string{"", constants.ToolClaude} {
		report := buildDoctor(context.Background(), app, filter, false)
		if msgs := findChecks(report, constants.CheckPinIndexIncomplete); len(msgs) != 1 {
			t.Fatalf("filter %q must retain exactly one index finding: %+v", filter, report.Checks)
		}
	}
	for _, format := range []string{formatText, formatJSON} {
		code, out := captureStdout(t, func() int {
			return runDoctor(context.Background(), app, commonOpts{Format: format, NoColor: true}, constants.ToolClaude)
		})
		mustExit(t, constants.ExitOK, code, out)
		if !strings.Contains(out, "pin index could not be read completely") {
			t.Fatalf("%s output omitted the finding: %s", format, out)
		}
		for _, sensitive := range []string{mainToken, "main-uuid", "you@example.com"} {
			if strings.Contains(out, sensitive) {
				t.Fatalf("%s output leaked %q: %s", format, sensitive, out)
			}
		}
		if format == formatJSON {
			var report doctorReport
			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatal(err)
			}
			if report.SchemaVersion != 1 || !report.OK {
				t.Fatalf("warning changed the report contract: %+v", report)
			}
		}
	}
}

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
	// what the test then asserts on, and a pin whose write lands in a throwaway leaves
	// that simulator holding no account. pinFixtureRunner's doc carries the measurement
	// and why `next` does not make this branch redundant; do not restate it here.
	// Shadowing also un-fakes every *other* command for those sites, which sent
	// `ensureGitExcluded` to real git.
	// Both spellings, because the value and the pointer are different types to a type
	// switch and a stray `&` would silently send the pin back to the real keychain —
	// the exact direction this fixture exists to prevent. A nil `Default` is left to
	// panic: that is a broken setup, and installing a fixture over it would hide it.
	switch runner.Default.(type) {
	case runner.OSRunner, *runner.OSRunner, cmdTestRunnerGuard, *cmdTestRunnerGuard:
		runner.With(pinFixtureRunner{next: runner.Default}, pin)
	default:
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
// layout. It goes to `next`, the runner this one replaced — which restores the
// *other* commands for a caller that had installed its own. It does **not** make the
// branch at the call site redundant, and that is worth being exact about: with the
// branch removed and `next` still in place, a caller's simulator still never sees the
// pin's write, and the account mutation at `artifact.go:325` goes from killed to
// survived while the whole package stays green. The branch is what keeps the caller's
// simulator authoritative; `next` only keeps git honest.
//
// **Both credential programs are intercepted, not just `security`**, and each gets the
// reply *its own* reader treats as an empty store — they do not share a convention.
// `internal/keychain` needs a non-zero exit carrying `NotFoundMarker` on stderr;
// `internal/secret`'s libsecret backend discriminates on **empty** stderr instead
// (`libsecret.go`: "a non-zero exit with empty stderr means no items are stored"), so
// the keychain's wording handed to it would read as a hard error rather than a miss.
// `RunInput` matters for the same half: `secret-tool store` takes its secret on stdin,
// and passing that through would hand a live credential to a real keyring.
//
// No test reaches the `secret-tool` half today — measured, by planting a panic here
// and running the package — because `testApp` pins the file backend, and
// `secret.Resolve` returns it without consulting `LookPath` at all.
type pinFixtureRunner struct{ next runner.Runner }

func (p pinFixtureRunner) Run(ctx context.Context, name string, args ...string) (string, string, int) {
	if len(args) == 0 {
		return p.next.Run(ctx, name, args...)
	}
	switch name {
	case "security":
		switch args[0] {
		case "add-generic-password", "delete-generic-password":
			return "", "", 0
		}
		return "", "security: " + keychain.NotFoundMarker, 44
	case "secret-tool":
		switch args[0] {
		case "store", "clear":
			return "", "", 0
		}
		return "", "", 1
	}
	return p.next.Run(ctx, name, args...)
}

func (p pinFixtureRunner) RunInput(ctx context.Context, stdin, name string, args ...string) (string, string, int) {
	// The zero-arg case delegates from here rather than through Run, which would drop
	// stdin on the way. Unreachable — no credential call is argument-less — but this is
	// the method whose whole reason is not losing a secret on the way past.
	if len(args) == 0 || (name != "security" && name != "secret-tool") {
		return p.next.RunInput(ctx, stdin, name, args...)
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
// `kae profile rm` used to do in silence), and the recorded directory path is
// gone (which can mean either deletion or a move).
func TestPinChecksReportStaleBindings(t *testing.T) {
	app := overlayTestApp(t)
	cwd := pinHere(t, app, modeShared)

	// A directory that was bound and then deleted or moved.
	gone := filepath.Join(t.TempDir(), "moved-away")
	if err := app.recordPinnedDir("00112233445566778899aabbccddeeff", gone); err != nil {
		t.Fatal(err)
	}

	checks := app.pinChecks("")
	if len(checks) != 2 {
		t.Fatalf("pinChecks() = %+v, want one dangling-account and one absent-path check", checks)
	}
	var dangling, absent string
	for _, c := range checks {
		if c.Code != constants.CheckPinStale || c.Status != constants.StatusWarn {
			t.Fatalf("unexpected check %+v", c)
		}
		if strings.Contains(c.Message, gone) {
			absent = c.Message
		}
		if strings.Contains(c.Message, cwd) {
			dangling = c.Message
		}
	}
	if !strings.Contains(dangling, "claude/main") || !strings.Contains(dangling, "kae pin claude") {
		t.Fatalf("the dangling-account check must name the binding and the fix: %q", dangling)
	}
	wantAbsent := fmt.Sprintf(
		"%s was bound with kae pin but its recorded path is gone; it may have been deleted or moved, so kae left its per-directory store untouched",
		gone,
	)
	if absent != wantAbsent {
		t.Fatalf("the absent path check must report only the ambiguity and preservation decision:\n got %q\nwant %q", absent, wantAbsent)
	}

	// Capturing the account it binds silences the dangling half, so the check
	// tracks the binding rather than merely the existence of a pin.
	seedAccountMeta(t, app, constants.ToolClaude, "main")
	for _, c := range app.pinChecks("") {
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
	if checks := app.pinChecks(""); len(checks) != 0 {
		t.Fatalf("an unpinned directory must not warn: %+v", checks)
	}
}

// TestPinCheckDoesNotCallALiveDirectoryAbsent pins the distinction the
// absent-path branch depends on. It must fire only when the recorded directory
// path is confirmed gone, never when the fragment inside a live directory merely
// could not be read (a permission change, an I/O error on a network mount).
func TestPinCheckDoesNotCallALiveDirectoryAbsent(t *testing.T) {
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

	checks := app.pinChecks("")
	if len(checks) != 1 {
		t.Fatalf("pinChecks() = %+v, want one unreadable-fragment check", checks)
	}
	if strings.Contains(checks[0].Message, "recorded path is gone") {
		t.Fatalf("a live directory must never be reported as absent: %q", checks[0].Message)
	}
	if !strings.Contains(checks[0].Message, cwd) || !strings.Contains(checks[0].Message, "could not be read") {
		t.Fatalf("the check must name the directory and say the fragment was unreadable: %q", checks[0].Message)
	}
}
