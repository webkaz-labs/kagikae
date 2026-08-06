package cmd

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// loginInto returns a RunInteractive stand-in that behaves like the real login
// flow: it writes a credential (and the identity that names its account) into the
// directory the **environment it was handed** points at, and records the argv and
// env it saw.
//
// Honoring the env rather than a path captured from the test is the point. kae's
// whole reason for exporting the isolation variable here is that the login must land
// in the bound store; a fake that wrote to a directory the test chose would pass just
// as happily if kae passed the wrong value, or none.
func loginInto(t *testing.T, tool, token, uuid string, expiresAt time.Time, seen *[]string) func(context.Context, []string, string, ...string) (int, error) {
	t.Helper()
	return func(_ context.Context, extraEnv []string, name string, args ...string) (int, error) {
		*seen = append(append([]string{name}, args...), extraEnv...)
		dir := ""
		for _, entry := range extraEnv {
			if rest, ok := strings.CutPrefix(entry, isolationEnvVar(tool)+"="); ok {
				dir = rest
			}
		}
		if dir == "" {
			return 1, nil // no isolation variable: the login would hit the real home
		}
		writeFile(t, filepath.Join(dir, ".credentials.json"), claudeOAuthPayload(token, expiresAt))
		writeFile(t, filepath.Join(dir, ".claude.json"), claudeIdentityFile(uuid))
		return 0, nil
	}
}

// The command's reason to exist, both halves in one run: the login is driven into
// the directory's own store (so it cannot land in the real home when the pin is not
// active in this shell — mise is never active under test, which is exactly the
// hazard), and the result is captured back into the account snapshot at the moment
// it is the newest copy.
func TestReloginLogsIntoTheBoundStoreAndCapturesItBack(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, credFile := boundStoreForClaudeMain(t, app)
	storeDir := filepath.Dir(credFile)

	const refreshed = "sk-ant-oat01-RELOGGED-aaaa"
	seen := []string{}
	withInteractive(t, loginInto(t, constants.ToolClaude, refreshed, "main-uuid", now.Add(8*time.Hour), &seen))

	code, stderr := captureStderr(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	// The argv, not just the effect: a test that only checked the outcome would keep
	// passing if kae stopped running the tool's own login flow.
	if len(seen) < 2 || seen[0] != "claude" || seen[1] != "/login" {
		t.Fatalf("kae must launch the tool's own login flow: %v", seen)
	}
	if want := isolationEnvVar(constants.ToolClaude) + "=" + storeDir; !slices.Contains(seen, want) {
		t.Fatalf("the login must be pointed at the bound store (%s): %v", want, seen)
	}
	if got := readFile(t, credFile); !strings.Contains(got, refreshed) {
		t.Fatalf("the login did not land in the bound store: %s", got)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
		t.Fatalf("the new login was not captured back into the snapshot: %s", got)
	}
	if !strings.Contains(stderr, "harvested") {
		t.Errorf("the capture back must be reported: %q", stderr)
	}
	if strings.Contains(stderr, refreshed) {
		t.Fatalf("a credential must never reach a message: %q", stderr)
	}
}

// The one thing that must be undetectable-proof: a login as somebody else. The
// store is now that account's, and filing its token under this directory's account
// name would be invisible afterwards — the token is opaque, so live, snapshot and
// doctor would all agree on a label that is simply wrong.
func TestReloginDoesNotFileAnotherAccountsLoginUnderThisAccount(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, credFile := boundStoreForClaudeMain(t, app)

	const foreign = "sk-ant-oat01-SIDE-bbbb"
	seen := []string{}
	withInteractive(t, loginInto(t, constants.ToolClaude, foreign, "side-uuid", now.Add(8*time.Hour), &seen))

	code, stderr := captureStderr(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	if got := readFile(t, credFile); !strings.Contains(got, foreign) {
		t.Fatalf("the login still belongs in the store it was made in: %s", got)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, mainToken) {
		t.Fatalf("another account's login must not reach this snapshot: %s", got)
	}
	if !strings.Contains(stderr, "belongs to an account other than claude/main") {
		t.Errorf("the refusal must say why: %q", stderr)
	}
	// Never a login remedy for a conflict: logging in again would mint a fresh chain
	// and invalidate the copy kae just left in place. The re-bind is the fix.
	if !strings.Contains(stderr, "kae pin claude") {
		t.Errorf("the remedy for a conflict is a re-bind: %q", stderr)
	}
	if strings.Contains(stderr, "relogin") {
		t.Errorf("a conflict must not be answered with another login: %q", stderr)
	}
}

// Outside a binding there is no store to log into, and driving the tool's login
// against the real home is `kae add`'s job — with a backup and a restore, which this
// command has neither of.
func TestReloginRefusesOutsideABoundDirectory(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ran := false
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		ran = true
		return 0, nil
	})
	code, stderr := captureStderr(t, func() int {
		return runRelogin(context.Background(), app, commonOpts{Format: formatText}, "")
	})
	if code != constants.ExitNotFound {
		t.Fatalf("exit = %d, want %d: %s", code, constants.ExitNotFound, stderr)
	}
	if ran {
		t.Fatal("no login may be launched when there is no store to launch it into")
	}
	if !strings.Contains(stderr, "add --restore") {
		t.Errorf("the refusal must name the global path: %q", stderr)
	}
}

// A tool the directory does not bind has no store here either, and running its login
// with no isolation variable set is precisely the accident this command exists to
// prevent: it would refresh the real home's login instead.
func TestReloginRefusesAToolThisDirectoryDoesNotBind(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	boundStoreForClaudeMain(t, app)
	ran := false
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		ran = true
		return 0, nil
	})

	code, stderr := captureStderr(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, constants.ToolCodex)
	})
	if code != constants.ExitNotFound {
		t.Fatalf("exit = %d, want %d: %s", code, constants.ExitNotFound, stderr)
	}
	if ran {
		t.Fatal("no login may be launched for a tool this directory does not bind")
	}
	if !strings.Contains(stderr, "it binds claude") {
		t.Errorf("the refusal must say what the directory does bind: %q", stderr)
	}
}

// The store a login is driven into is the one the *fragment* describes, and the
// fragment is what mise exports — so a directory that was unpinned has no store to
// speak of even though its store tree survives on purpose (a re-pin restores its
// sessions). Without the fragment gate this would log in to a directory nothing
// points at.
func TestReloginRefusesAfterUnpin(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	_, credFile := boundStoreForClaudeMain(t, app)
	code, out := captureStdout(t, func() int {
		return runUnpin(ctx, app, commonOpts{Format: formatText}, false)
	})
	mustExit(t, constants.ExitOK, code, out)
	// The store survives an unpin; only the binding is gone. Without this the test
	// would be asserting the refusal for the wrong reason.
	if _, err := os.Stat(credFile); err != nil {
		t.Fatalf("unpin must keep the store: %v", err)
	}

	ran := false
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		ran = true
		return 0, nil
	})
	code, stderr := captureStderr(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	if code != constants.ExitNotFound || ran {
		t.Fatalf("an unpinned directory has nothing to log in to: exit=%d ran=%v %s", code, ran, stderr)
	}
}

// A directory can bind several tools kae could log in, and two interactive login
// flows from one word is not a thing to do by default. Taking the first candidate
// instead would silently log in the tool the user did not mean.
func TestReloginRefusesToChooseBetweenTwoBoundTools(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	app.Config.Profiles["main"] = config.Profile{Accounts: map[string]string{
		constants.ToolClaude: "main", constants.ToolCodex: "main",
	}}
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	seedCodex(t, app, "codex-token")
	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText}, constants.ToolCodex, "main")
	})
	mustExit(t, constants.ExitOK, code, out)
	boundStoreForClaudeMain(t, app)

	ran := false
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		ran = true
		return 0, nil
	})
	code, stderr := captureStderr(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	if code != constants.ExitUsage || ran {
		t.Fatalf("an ambiguous directory must refuse: exit=%d ran=%v %s", code, ran, stderr)
	}
	for _, want := range []string{"claude", "codex", "relogin <tool>"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal must name the choice (%q): %q", want, stderr)
		}
	}
	// And naming one of them works, so the refusal is the ambiguity and not a
	// directory this command cannot handle at all.
	withInteractive(t, loginInto(t, constants.ToolClaude, "sk-ant-oat01-PICKED-cccc", "main-uuid",
		app.Now().Add(8*time.Hour), &[]string{}))
	code, stderr = captureStderr(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, constants.ToolClaude)
	})
	mustExit(t, constants.ExitOK, code, stderr)
}

// A flow the user aborted leaves the store exactly as it was, and the one claim
// this command must never make is that a login happened. The whole reason to run it
// is that the directory was already stale, so "logged in" here would send the user
// away believing it is fixed.
func TestReloginRefusesWhenTheFlowChangedNothing(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	_, credFile := boundStoreForClaudeMain(t, app)
	unchanged := readFile(t, credFile)

	ran := false
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		ran = true
		return 1, nil // the tool exited non-zero and wrote nothing
	})
	code, stderr := captureStderr(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	if !ran {
		t.Fatal("the login flow must still be launched")
	}
	if code != constants.ExitAuthUnchanged {
		t.Fatalf("exit = %d, want %d: %s", code, constants.ExitAuthUnchanged, stderr)
	}
	if !strings.Contains(stderr, "left this directory's credential unchanged") {
		t.Errorf("the refusal must say the flow changed nothing: %q", stderr)
	}
	if strings.Contains(stderr, "Logged claude in") {
		t.Errorf("no login may be claimed: %q", stderr)
	}
	if readFile(t, credFile) != unchanged {
		t.Fatal("nothing may have touched the store")
	}
	// Positive control: the same fixture with a flow that does write reports the
	// login, so the refusal above is the comparison and not a path that always
	// refuses.
	withInteractive(t, loginInto(t, constants.ToolClaude, "sk-ant-oat01-WROTE-dddd", "main-uuid",
		app.Now().Add(8*time.Hour), &[]string{}))
	code, out := captureStdout(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, out)
}
