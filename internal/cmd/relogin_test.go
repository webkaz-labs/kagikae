package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
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
		dir, credDir := "", ""
		for _, entry := range extraEnv {
			if rest, ok := strings.CutPrefix(entry, isolationEnvVar(tool)+"="); ok {
				dir = rest
			}
			if credVar := credentialEnvVar(tool); credVar != "" {
				if rest, ok := strings.CutPrefix(entry, credVar+"="); ok {
					credDir = rest
				}
			}
		}
		if dir == "" {
			return 1, nil // no isolation variable: the login would hit the real home
		}
		// The two halves land where the tool puts them, which is the split this fake
		// has to reproduce: the credential follows the credential variable and the
		// identity follows the home. A fake that wrote both into the home would report
		// success for a kae that exported only one of the two.
		if credDir == "" {
			credDir = dir
		}
		writeFile(t, filepath.Join(credDir, ".credentials.json"), claudeOAuthPayload(token, expiresAt))
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
	_, storeDir, credFile := boundStoreForClaudeMain(t, app)

	const refreshed = "sk-ant-oat01-RELOGGED-aaaa"
	seen := []string{}
	withInteractive(t, loginInto(t, constants.ToolClaude, refreshed, "main-uuid", now.Add(8*time.Hour), &seen))

	code, out, stderr := captureBoth(t, func() int {
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
	// The strong wording, which kae may print only where it observed all three of: the
	// store changed, what is there now is not a tombstone, and the harvest attributed
	// it to this account. Without this assertion every gate below could be inverted
	// into always printing the weak line and nothing would fail.
	if !strings.Contains(out, "Logged claude in for claude/main in this directory") {
		t.Errorf("a login kae observed end to end must be reported as one: %q", out)
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
	_, _, credFile := boundStoreForClaudeMain(t, app)

	const foreign = "sk-ant-oat01-SIDE-bbbb"
	seen := []string{}
	withInteractive(t, loginInto(t, constants.ToolClaude, foreign, "side-uuid", now.Add(8*time.Hour), &seen))

	code, out, stderr := captureBoth(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	// The stdout line is the one a skimming reader keeps, so it must not name the
	// account the warning has just said this login is *not*. Two lines disagreeing is
	// worse than one weak line.
	if strings.Contains(out, "claude/main") {
		t.Errorf("the success line must not name an account kae could not attribute: %q", out)
	}
	if !strings.Contains(out, "Ran the claude login flow") {
		t.Errorf("the success line must still say what kae did: %q", out)
	}

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
	_, _, credFile := boundStoreForClaudeMain(t, app)
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
	_, _, credFile := boundStoreForClaudeMain(t, app)
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

// H1's shape: kae could not read the store, so it cannot tell whether the flow
// changed anything — and the one thing it must not do then is fall through to a
// line claiming a login. Reporting a login that did not happen sends the user away
// believing a stale directory is fixed, which is the whole reason they ran this.
func TestReloginSaysSoWhenItCannotTellWhetherAnythingChanged(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	_, _, credFile := boundStoreForClaudeMain(t, app)
	// A credential path that is a *directory* reads as an error rather than as absent,
	// which is the distinction the comparison turns on: absent-then-present is a
	// change, unreadable-then-unreadable is kae not knowing.
	if err := os.Remove(credFile); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(credFile, 0o700); err != nil {
		t.Fatal(err)
	}

	ran := false
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		ran = true
		return 0, nil
	})
	code, out, stderr := captureBoth(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	if !ran {
		t.Fatal("the login flow must still be launched")
	}
	if code == constants.ExitAuthUnchanged {
		t.Fatalf("two failed reads are not proof that nothing changed: %s", stderr)
	}
	if !strings.Contains(stderr, "cannot tell whether the login flow changed anything") {
		t.Errorf("kae must say it could not compare: %q / %q", stderr, out)
	}
	// And the stdout line must not contradict the warning two lines above it. The
	// warning alone is not the fix: it was added first and the success line still
	// claimed a login underneath it.
	if strings.Contains(out, "Logged claude in") {
		t.Errorf("no login may be claimed on a comparison that never happened: %q", out)
	}
}

// The store path is recomputed from a hash of this directory's current path, while
// what the tool reads there is the literal value in the fragment mise exports. A
// directory that moved keeps its fragment and gets a different pin id, so logging in
// would create a store nothing reads — and report success. `kae pin` always
// materializes the store, so its absence is the signal that the two have diverged.
func TestReloginRefusesWhenTheBoundStoreIsNotThere(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	_, storeDir, _ := boundStoreForClaudeMain(t, app)
	if err := os.RemoveAll(storeDir); err != nil {
		t.Fatal(err)
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
		t.Fatalf("a store that is not there must refuse before launching: exit=%d ran=%v %s", code, ran, stderr)
	}
	if !strings.Contains(stderr, "kae pin claude main") {
		t.Errorf("the remedy is a re-bind at the current path: %q", stderr)
	}
}

// The state a stale bound directory actually reaches, in both of its shapes — and
// they are one state to the classifier that already owns the question
// (readLiveCredential's liveNothing is "absent, **or** present with nothing left to
// authenticate or refresh with"). The first version of this gate tested `Revoked`
// alone, which is false for an absent payload, so an emptied store printed a login
// with no warning at all.
func TestReloginDoesNotCallAnEmptiedStoreALogin(t *testing.T) {
	tombstone := func(now time.Time) string {
		return fmt.Sprintf(
			`{"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":0,"refreshTokenExpiresAt":%d}}`,
			now.Add(27*24*time.Hour).UnixMilli(),
		)
	}
	for _, tc := range []struct {
		name, wantSaid string
		leave          func(t *testing.T, now time.Time, credFile string)
	}{
		// Blanked in place: claude tries to refresh on startup, gets invalid_grant and
		// overwrites the credential with empty tokens, before the user reaches a prompt.
		{
			"tombstone", "read no usable claude token",
			func(t *testing.T, now time.Time, credFile string) { writeFile(t, credFile, tombstone(now)) },
		},
		// Removed. Reachable with no failure at all: under the file driver a successful
		// `claude /login` writes the keychain item and deletes the plaintext file kae is
		// reading, so kae's own spec resolves to something that is now absent.
		{
			"absent", "found no claude credential",
			func(t *testing.T, _ time.Time, credFile string) {
				if err := os.Remove(credFile); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := overlayTestApp(t)
			ctx := context.Background()
			captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
			boundStoreForClaudeMain(t, app)

			withInteractive(t, func(_ context.Context, extraEnv []string, _ string, _ ...string) (int, error) {
				// Keyed on the *credential* variable: that is where the login flow writes,
				// so it is the copy an aborted or emptied flow leaves behind. Reading the
				// home variable here would leave a file nothing looks at and let this
				// assert about a store kae never read.
				for _, entry := range extraEnv {
					if dir, ok := strings.CutPrefix(entry, credentialEnvVar(constants.ToolClaude)+"="); ok {
						tc.leave(t, app.Now(), filepath.Join(dir, ".credentials.json"))
					}
				}
				return 1, nil
			})

			code, out, stderr := captureBoth(t, func() int {
				return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
			})
			mustExit(t, constants.ExitOK, code, stderr)

			// The bytes differ, so this is not the auth_unchanged path — which is exactly
			// why this needs a gate of its own.
			if code == constants.ExitAuthUnchanged {
				t.Fatalf("the store did change; this is a different failure: %s", stderr)
			}
			if strings.Contains(out, "Logged claude in") {
				t.Errorf("a store with nothing usable in it is not a login: %q", out)
			}
			if !strings.Contains(stderr, tc.wantSaid) {
				t.Errorf("kae must say what it read in the store: %q", stderr)
			}
			// And it may not claim more than it read. `Revoked` is derived from fields that
			// are empty *or absent*, so an upstream rename of the token keys reads the same
			// as a tombstone — "the login failed, run it again" would loop forever against a
			// login that is fine, on the day kae's parser is the stale thing.
			//
			// Scoped to the warning lines and matched on word boundaries: the pre-login
			// banner contains "against", and a bare substring test for "again" fails on it
			// — the same shape as asserting a path prefix and matching a sibling.
			//
			// The list below is **illustrative, not a fence**. It pins the three phrasings
			// that were actually wrong once; a reworded regression ("re-run the flow to
			// finish it") matches none of them. The real property — no imperative, and no
			// claim about what the tool can do — is not mechanically testable, so a green
			// run here is not the property holding. The line filter has the same character:
			// a warning that ever wrapped would hide its continuation from it.
			warnings := ""
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "kae: warning:") {
					warnings += line + "\n"
				}
			}
			if warnings == "" {
				t.Fatalf("no warning line to check: %q", stderr)
			}
			for _, forbidden := range []string{`\bagain\b`, `did not complete`, `cannot log in`} {
				if regexp.MustCompile(forbidden).MatchString(warnings) {
					t.Errorf("the warning may not claim more than kae read (%s): %q", forbidden, warnings)
				}
			}
			be := testBackend(t, app)
			if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, mainToken) {
				t.Fatalf("an unusable payload must never be harvested: %s", got)
			}
		})
	}
}

// A tool whose rotation is unmeasured harvests nothing, so nothing checked whose
// login landed in the store either — and "Logged codex in for codex/main" would be
// asserting an attribution kae never made. Absence of a refusal is not confirmation,
// which is the same rule dirIdentityConfirms follows one level down.
func TestReloginDoesNotClaimAnAccountItNeverAttributed(t *testing.T) {
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

	withInteractive(t, func(_ context.Context, extraEnv []string, _ string, _ ...string) (int, error) {
		for _, entry := range extraEnv {
			if dir, ok := strings.CutPrefix(entry, isolationEnvVar(constants.ToolCodex)+"="); ok {
				writeFile(t, filepath.Join(dir, "auth.json"),
					`{"tokens":{"access_token":"codex-relogged"}}`)
			}
		}
		return 0, nil
	})
	code, stdout, stderr := captureBoth(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, constants.ToolCodex)
	})
	mustExit(t, constants.ExitOK, code, stderr)

	if strings.Contains(stdout, "codex/main") {
		t.Errorf("kae never attributed this login, so it may not name the account: %q", stdout)
	}
	if !strings.Contains(stdout, "Ran the codex login flow") {
		t.Errorf("kae must still say what it did: %q", stdout)
	}
	// Positive control for the fixture: the login really did land in the bound store,
	// so the weak wording above is the attribution rule and not a flow that never ran.
	storeDir, bound := app.boundStoreDir(paths.PinID(mustCwdAbs(t)), constants.ToolCodex, mustFragment(t))
	if !bound {
		t.Fatal("the directory must bind codex for this test to mean anything")
	}
	if got := readFile(t, filepath.Join(storeDir, "auth.json")); !strings.Contains(got, "codex-relogged") {
		t.Fatalf("the login did not land in the bound codex store: %q", got)
	}
}

// mustFragment is the read the assertion above needs to name the store the way
// runRelogin does, rather than rebuilding the path by hand. Its sibling is
// mustCwdAbs (ls_test.go).
func mustFragment(t *testing.T) fragmentInfo {
	t.Helper()
	info, exists, err := readDirFragment()
	if err != nil || !exists {
		t.Fatalf("read fragment: exists=%v err=%v", exists, err)
	}
	return info
}

// The two reads are separate observations and each can fail alone, which the
// existing un-comparable test cannot show: it makes the credential path a directory,
// so *both* reads fail and either flag alone still evaluates false. An asymmetric
// fixture is what distinguishes them — and the harm the pair prevents is the
// contradicting-lines defect: stderr saying kae cannot tell, stdout claiming a login.
func TestReloginWillNotClaimALoginItCouldNotCompareAgainst(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	_, _, credFile := boundStoreForClaudeMain(t, app)
	// Unreadable *before*: a path that is a directory errors rather than reading as
	// absent, so kae has no pre-flow bytes to compare against.
	if err := os.Remove(credFile); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(credFile, 0o700); err != nil {
		t.Fatal(err)
	}
	// …and a perfectly good login after it. Everything except the comparison succeeds:
	// the harvest reads the new copy, orders it ahead of the snapshot and attributes
	// it, so `attributed` is true and only the missing before-read holds the wording
	// back. Drop `comparable` from that gate and this prints "Logged claude in".
	withInteractive(t, func(_ context.Context, extraEnv []string, _ string, _ ...string) (int, error) {
		for _, entry := range extraEnv {
			if dir, ok := strings.CutPrefix(entry, credentialEnvVar(constants.ToolClaude)+"="); ok {
				cred := filepath.Join(dir, ".credentials.json")
				if err := os.RemoveAll(cred); err != nil {
					t.Fatal(err)
				}
				writeFile(t, cred, claudeOAuthPayload("sk-ant-oat01-AFTERONLY-mmmm", app.Now().Add(8*time.Hour)))
				writeFile(t, filepath.Join(dir, ".claude.json"), claudeIdentityFile("main-uuid"))
			}
		}
		return 0, nil
	})

	code, out, stderr := captureBoth(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	if strings.Contains(out, "Logged claude in") {
		t.Errorf("kae never read the store before the flow, so it cannot say the flow changed it: %q", out)
	}
	if !strings.Contains(stderr, "cannot tell whether the login flow changed anything") {
		t.Errorf("kae must say which observation it is missing: %q", stderr)
	}
	// The positive control that keeps this from passing for the wrong reason: the
	// login itself worked and *was* harvested, so the weak wording is the missing
	// before-read and not a flow that failed.
	if !strings.Contains(stderr, "harvested") {
		t.Fatalf("the capture back must still have happened: %q", stderr)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, "AFTERONLY-mmmm") {
		t.Fatalf("the new login must reach the snapshot even when kae cannot compare: %s", got)
	}
}

// The invariant `kae relogin`'s reasoning rests on and nothing guarded: for every
// tool kae materializes a per-directory credential for, a successful `Artifacts`
// returns a spec under exactly the name `credentialArtifactName` gives.
//
// Two places lean on it. `captureBackAfterRelogin` refuses when `specs == nil`, which
// is only equivalent to "kae has no credential spec here" while this holds — otherwise
// a spec set without its credential reaches `harvestDirCredential`, whose `!ok` arm
// returns an empty refusal and so reports the login as attributed. And
// `reloginCredentialSpec` reports `haveSpec=false` for a resolution that *succeeded*,
// which would then be a silent no-comparison rather than the impossible state it is
// meant to be.
//
// A real guard rather than a test that cannot fail: it fails the day an adapter grows
// a spec set without its credential, or a name here drifts from the adapter's own
// `Spec.Name`.
func TestCredentialArtifactNameMatchesEveryAdapter(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	checked := []string{}
	for _, tool := range constants.Tools {
		artName := credentialArtifactName(tool)
		if artName == "" {
			continue // kae materializes no per-directory credential for this tool
		}
		adp, err := adapter.ForTool(tool)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		specs, err := adp.Artifacts(ctx, app.Env)
		if err != nil {
			// A refusal is a legitimate answer (an unsupported store mode, a platform kae
			// cannot read); it is a spec set *without* the credential that breaks the
			// invariant, not the absence of one.
			continue
		}
		checked = append(checked, tool)
		if _, ok := specByName(specs, artName); !ok {
			names := []string{}
			for _, sp := range specs {
				names = append(names, sp.Name)
			}
			t.Errorf("%s: credentialArtifactName is %q but Artifacts returned %v", tool, artName, names)
		}
	}
	// The guard on the guard, and it has to be the **exact set**, not a non-empty one.
	// Both `continue`s above are per tool, so an emptiness test passes with coverage
	// silently halved when one adapter refuses — and a real drift on the refusing side
	// goes with it (measured: claude's Artifacts made to refuse *and* its name drifted
	// leaves this green). The realistic trigger is not exotic — claude's Artifacts
	// opens with `driver(env)`, so any environment where the driver refuses drops it.
	//
	// A refusal is a legitimate answer for an adapter, but not for this test's
	// environment: testApp hands both tools a temp HOME with nothing to refuse over.
	// If a platform-conditional adapter ever does refuse here, this assertion is meant
	// to be the alarm and to be updated deliberately.
	want := []string{}
	for _, tool := range constants.Tools {
		if credentialArtifactName(tool) != "" {
			want = append(want, tool)
		}
	}
	if !slices.Equal(checked, want) {
		t.Fatalf("only %v of %v reached the assertion; a tool whose Artifacts refuses is silently uncovered",
			checked, want)
	}
}

// The refusal's frame may claim no more than the reason it interpolates. It used to say
// kae "cannot attribute" the login and that the snapshot holds "the **older** copy" — an
// ordering claim, next to a reason that says kae could not read or date the payload at all.
//
// Reaching this arm needs the payload written where relogin actually resolves the store: a
// first attempt wrote it elsewhere, read back absent, and landed on the silent-success arm,
// which is what made the arm look unreachable.
func TestReloginRefusalDoesNotClaimAnOrderingItCannotMake(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))

	credDir := t.TempDir()
	// Known to claude's parser (expiresAt present), not a tombstone, and undated.
	writeFile(t, filepath.Join(credDir, ".credentials.json"),
		fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-LIVE","refreshToken":"r1","expiresAt":%q}}`,
			fmt.Sprint(now.Add(8*time.Hour).UnixMilli())))
	writeFile(t, filepath.Join(credDir, ".claude.json"), claudeIdentityFile("main-uuid"))

	dirs := bindDirs{Config: credDir}
	specs, err := app.dirSpecs(ctx, constants.ToolClaude, dirs)
	if err != nil {
		t.Fatal(err)
	}
	var captured bool
	_, stderr := captureStderr(t, func() int {
		// wroteIdentity=true, which is the harder side: the override it licenses is about
		// *whose* login a readable copy is, and must not rescue one kae cannot read at all.
		captured = app.captureBackAfterRelogin(ctx, testBackend(t, app), specs,
			constants.ToolClaude, "main", dirs, true)
		return 0
	})
	if captured {
		t.Fatalf("a copy kae cannot judge must not be captured back: %q", stderr)
	}
	if !strings.Contains(stderr, "cannot confirm the claude login") {
		t.Errorf("the frame must claim only that kae cannot confirm whose login it is: %q", stderr)
	}
	for _, forbidden := range []string{"the older copy", "that older one"} {
		if strings.Contains(stderr, forbidden) {
			t.Errorf("kae could not order the two, so it must not call either the older: %q", stderr)
		}
	}
}

// The login flow is a write kae does not perform, so the copy already in the store is
// gone the moment the tool finishes — and since the credential split that copy belongs
// to the *account*, not to this directory. `kae pin` declines to overwrite one it
// cannot attribute in order to preserve it, and then names this command as the remedy;
// following that remedy destroyed the copy the refusal had kept, with no warning and no
// copy anywhere afterwards (measured 2026-08-08, end to end).
//
// So kae harvests before the flow, and says so when it could not. It may claim only
// what it observed: the harvest's own reason, never that the copy is another account's,
// which on this arm is exactly what kae could not establish.
func TestReloginSaysWhatTheLoginFlowIsAboutToReplace(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))

	// A sibling bound to the same account, which confirms — without it this directory
	// would be the only reader that disagrees, which is the `Conflicting` arm that
	// overwrites rather than the keep this test is about.
	_, _, siblingCred := boundStoreForClaudeMain(t, app)
	_, storeDir, credFile := boundStoreForClaudeMain(t, app)
	// Positive control on the fixture itself: the two directories must be reading one
	// copy, or the disagreement below is about a store nothing shares and the refusal
	// under test is never reached.
	if siblingCred != credFile {
		t.Fatalf("the two bindings must share one account credential store: %s vs %s", siblingCred, credFile)
	}

	// Somebody ran the tool's own /login here as another account: the identity lands in
	// this directory's config dir and the credential in the account's shared store.
	const foreign = "sk-ant-oat01-SIDE-cccc"
	writeFile(t, filepath.Join(storeDir, ".claude.json"), claudeIdentityFile("side-uuid"))
	writeFile(t, credFile, claudeOAuthPayload(foreign, now.Add(8*time.Hour)))

	withInteractive(t, loginInto(t, constants.ToolClaude, "sk-ant-oat01-MAIN-eeee", "main-uuid",
		now.Add(9*time.Hour), &[]string{}))
	code, stderr := captureStderr(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	warned := strings.Index(stderr, "completing the login flow replaces it")
	if warned < 0 {
		t.Fatalf("the flow must not replace a copy kae could not keep without saying so: %q", stderr)
	}
	// Before the write it warns about, which for this one means before the flow is
	// launched — a warning printed afterwards describes a loss that has already
	// happened (AGENTS.md).
	if launched := strings.Index(stderr, "complete the claude login flow"); launched < 0 || warned > launched {
		t.Errorf("the warning must precede the flow (warned=%d launched=%d): %q", warned, launched, stderr)
	}
	if !strings.Contains(stderr, "disagree about whose login it is") {
		t.Errorf("it must carry the harvest's own reason: %q", stderr)
	}
	// The ordered frame, which is the half the un-orderable test cannot pin. Here the
	// harvest refused *past* the supersedes gate, so kae did establish the copy is newer
	// and saying so is the most useful thing it knows. Without this assertion the flag
	// that picks the frame can be tied to Conflicting — which is false for every
	// unattributable refusal — and nothing fails (measured 2026-08-08).
	if !strings.Contains(stderr, "is newer than snapshot claude/main") {
		t.Errorf("kae established the ordering here, so the frame must carry it: %q", stderr)
	}
	// What kae did *not* establish, it may not say. On this arm the readers disagree,
	// so kae has no verdict about whose the copy is.
	if strings.Contains(stderr, "belongs to an account other than") {
		t.Errorf("kae did not attribute this copy, so it must not name an owner: %q", stderr)
	}
	// The refusal is real: the foreign copy was not filed under this account either.
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, foreign) {
		t.Fatalf("a copy kae could not attribute must not reach this snapshot: %s", got)
	}

	// Positive control, both directions: with the disagreement resolved the same
	// fixture harvests silently, so the warning above is the refusal and not a line
	// this path always prints.
	writeFile(t, filepath.Join(storeDir, ".claude.json"), claudeIdentityFile("main-uuid"))
	// Three distinct deadlines, in the order a real machine produces them: the copy in
	// the store is later than what the first run's login left in the snapshot (or the
	// harvest returns at its "nothing newer" arm and this control never reaches the
	// attribution it is controlling for), and the login is later again. Dating the store
	// copy *past* the login instead makes captureBackAfterRelogin short-circuit at
	// `!supersedes` — a state no real login reaches, since deadlines advance — and this
	// control would then pass while exercising it.
	const kept = "sk-ant-oat01-KEPT-hhhh"
	writeFile(t, credFile, claudeOAuthPayload(kept, now.Add(10*time.Hour)))
	const last = "sk-ant-oat01-LAST-iiii"
	withInteractive(t, loginInto(t, constants.ToolClaude, last, "main-uuid", now.Add(11*time.Hour), &[]string{}))
	code, stderr = captureStderr(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)
	if strings.Contains(stderr, "completing the login flow replaces it") {
		t.Errorf("an attributable copy is harvested, not warned about: %q", stderr)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, last) {
		t.Fatalf("the login is the newest copy here, so the capture back must have run: %s", got)
	}
	// Harvested rather than merely not-warned-about, and **before** the flow: the
	// post-flow capture back would report the same line about the login's own copy, so
	// only the ordering distinguishes the pass this test is about from the one that was
	// already there. The snapshot cannot carry this assertion — by the time the command
	// returns it holds what the login wrote, which supersedes both.
	harvested := strings.Index(stderr, "harvested the newer claude credential")
	launched := strings.Index(stderr, "complete the claude login flow")
	if harvested < 0 || launched < 0 || harvested > launched {
		t.Fatalf("the copy the flow replaces must be harvested before it (harvested=%d launched=%d): %q",
			harvested, launched, stderr)
	}
}

// A message may claim no more than kae observed, and this one's reason is sometimes
// kae saying it **cannot order** the two copies. So the frame must not call the copy
// newer: that contradicts the reason it interpolates, which is the fold
// docs/CLI.md § `kae rollback --json` is normative against and which
// captureBackAfterRelogin was corrected for once already.
func TestReloginPreFlightDoesNotClaimAnOrderingItCannotMake(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, _, credFile := boundStoreForClaudeMain(t, app)

	// Known (expiresAt present) but undated (a non-numeric value parses to the zero
	// time) with the tokens intact, so it is not the measured tombstone: readLiveCredential
	// classifies it liveUnreadable, which is the arm that cannot be ordered at all.
	writeFile(t, credFile,
		`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-ODD-ffff","refreshToken":"rt-odd",`+
			`"expiresAt":"soon","refreshTokenExpiresAt":1830384000000}}`)

	withInteractive(t, loginInto(t, constants.ToolClaude, "sk-ant-oat01-NEW-gggg", "main-uuid",
		now.Add(9*time.Hour), &[]string{}))
	code, stderr := captureStderr(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	// Positive control first: without it, a run that never reached this arm would pass
	// the negative assertion below for free.
	if !strings.Contains(stderr, "cannot read or date the copy already there") {
		t.Fatalf("the fixture must reach the un-orderable arm: %q", stderr)
	}
	if !strings.Contains(stderr, "kae is not harvesting the claude credential already in") {
		t.Errorf("the pre-flight must still say it did not keep the copy: %q", stderr)
	}
	if strings.Contains(stderr, "is newer than snapshot") {
		t.Errorf("kae could not order these two copies, so it may not call one newer: %q", stderr)
	}
}

// "kae could not look" and "there was nothing worth keeping" are the same output and
// opposite facts, and the only thing the user can still do about the second — not
// complete the flow — is available *before* it starts. So the pre-flight says so on
// the routes where it cannot even read the snapshot to compare against, rather than
// deferring to the post-flow report, which describes the loss after it happened.
func TestReloginSaysWhenItCannotCheckWhatTheFlowWillReplace(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, _, credFile := boundStoreForClaudeMain(t, app)
	// Something worth losing is in the store, so this is not the "nothing there" case.
	writeFile(t, credFile, claudeOAuthPayload("sk-ant-oat01-ATRISK-jjjj", now.Add(8*time.Hour)))

	// The binding still names claude/main; the snapshot it would compare against is gone
	// (an `kae account rm` between the bind and the login). The fragment is what relogin
	// reads, so the command still runs.
	if err := os.RemoveAll(app.Paths.AccountDir(constants.ToolClaude, "main")); err != nil {
		t.Fatalf("remove the snapshot: %v", err)
	}

	withInteractive(t, loginInto(t, constants.ToolClaude, "sk-ant-oat01-NEW-kkkk", "main-uuid",
		now.Add(9*time.Hour), &[]string{}))
	code, stderr := captureStderr(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	said := strings.Index(stderr, "cannot tell what the login flow is about to replace")
	if said < 0 {
		t.Fatalf("a read kae could not make must be said before the flow, not left silent: %q", stderr)
	}
	if launched := strings.Index(stderr, "complete the claude login flow"); launched < 0 || said > launched {
		t.Errorf("and before it (said=%d launched=%d): %q", said, launched, stderr)
	}
	// No remedy: kae genuinely has none here, and inventing one would send the user at a
	// command that cannot help.
	if strings.Contains(stderr, "kae pin claude") {
		t.Errorf("kae has no remedy for a snapshot it could not read: %q", stderr)
	}
}

// A new output path is a new place a secret can leak, and this one interpolates an
// error from the secret backend. AGENTS.md requires a redaction test for each.
func TestReloginPreFlightWarningsCarryNoSecret(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, storeDir, credFile := boundStoreForClaudeMain(t, app)

	// One reader disagreeing, so the pre-flight reaches the refusal that names the
	// store and a reason — the wordiest of the three lines.
	const atRisk = "sk-ant-oat01-ATRISK-llll"
	_, siblingStore, _ := boundStoreForClaudeMain(t, app)
	writeFile(t, filepath.Join(siblingStore, ".claude.json"), claudeIdentityFile("side-uuid"))
	writeFile(t, credFile, claudeOAuthPayload(atRisk, now.Add(8*time.Hour)))
	_ = storeDir

	const fresh = "sk-ant-oat01-FRESH-mmmm"
	withInteractive(t, loginInto(t, constants.ToolClaude, fresh, "main-uuid", now.Add(9*time.Hour), &[]string{}))
	code, out, stderr := captureBoth(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	// Positive control: the pre-flight really spoke, so the absences below are about
	// a line that exists rather than about a run that printed nothing.
	if !strings.Contains(stderr, "completing the login flow replaces it") {
		t.Fatalf("the fixture must reach the pre-flight refusal: %q", stderr)
	}
	for _, secret := range []string{atRisk, fresh, mainToken} {
		if strings.Contains(stderr, secret) || strings.Contains(out, secret) {
			t.Errorf("a credential reached the output: %q / %q", stderr, out)
		}
	}
}

// A login kae ran itself is captured back even though a *sibling* worktree bound to the
// same account carries an unresolved `identity_drift` — the override attributionSource
// .WatchedLogin licenses, measured as a refusal on 2026-08-16 before it existed.
//
// The reader set is the mechanism: sharedStoreAttribution asks every directory that
// reads `credstore/claude/main`, the sibling names somebody else, and a confirming
// reader beside a conflicting one is the `disagree` outcome. That outcome is right for a
// *bind*, where no reader's cache is any fresher than another's; here kae exported the
// store's variables, ran the tool's own login flow against them, and watched the tool
// write both the credential and its own label in this directory. Without the override
// the account snapshot kept the copy this very login had already invalidated (claude's
// refresh token rotates single-use), with no way to update it until the sibling's drift
// was resolved somewhere else.
//
// The label the flow writes here names the **same account the bind already labelled the
// directory with**, which is the ordinary case and the one that decided the evidence's
// shape: byte-identical through claude's `/oauthAccount` pointer, so only the write time
// separates a tool that rewrote it from a tool that wrote nothing (dirIdentityWrittenAt).
//
// The drift is applied *after* the sibling's own bind so the fixture reaches this
// outcome and not an earlier one: a directory bound while a sibling disagrees takes
// the keep branch and writes no identity of its own, which would make every assertion
// below hold for missing evidence instead.
func TestReloginCapturesALoginItWatchedWhenASiblingHasDrifted(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	dir, _, credFile := boundStoreForClaudeMain(t, app)
	_, siblingStore := bindClaudeHere(t, app, "main")
	writeFile(t, filepath.Join(siblingStore, ".claude.json"), claudeIdentityFile("side-uuid"))
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	const refreshed = "sk-ant-oat01-RELOGGED-aaaa"
	seen := []string{}
	withInteractive(t, loginInto(t, constants.ToolClaude, refreshed, "main-uuid", now.Add(8*time.Hour), &seen))

	code, out, stderr := captureBoth(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	// Positive controls for the two halves kae observed, so the capture below rests on
	// what this test arranged rather than on a flow that never happened: the login
	// landed in the store this directory reads, and it named this account there.
	if got := readFile(t, credFile); !strings.Contains(got, refreshed) {
		t.Fatalf("the login must land in the bound store for this to be about attribution: %s", got)
	}
	if got := readFile(t, filepath.Join(app.Paths.SharedDir(paths.PinID(dir), constants.ToolClaude),
		".claude.json")); !strings.Contains(got, "main-uuid") {
		t.Fatalf("the flow must leave this directory naming this account: %q", got)
	}
	// And the sibling still disagrees, so the capture below is the override rather than
	// a drift the fixture lost along the way.
	if got := readFile(t, filepath.Join(siblingStore, ".claude.json")); !strings.Contains(got, "side-uuid") {
		t.Fatalf("the sibling must still name another account: %q", got)
	}

	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
		t.Fatalf("a login kae ran itself must reach the snapshot: %s", got)
	}
	if !strings.Contains(stderr, "harvested") {
		t.Errorf("the capture back must be reported: %q", stderr)
	}
	if strings.Contains(stderr, "disagree about whose login it is") {
		t.Errorf("the sibling's disagreement must not refuse this one: %q", stderr)
	}
	if !strings.Contains(out, "Logged claude in for claude/main in this directory") {
		t.Errorf("a login kae observed end to end must be reported as one: %q", out)
	}
	if strings.Contains(stderr, refreshed) || strings.Contains(out, refreshed) {
		t.Errorf("a credential must never reach a message: %q / %q", stderr, out)
	}
}

// The same override with no sibling worktree at all, because a **globally isolated
// home** reads the account's credential store too — `prepareGlobalIsolatedHome`
// writes one per `kae use -i` / `kae run -i`, nothing removes it, and the reader walk
// reads those from disk with no liveness gate. So one stale `kae use -i` home was
// enough to veto a login kae watched happen in a bound directory, and the entry's
// "two worktrees" is one shape of the mechanism rather than its extent.
//
// Measured as a refusal 2026-08-16, alongside the test above and by the same fix.
func TestReloginCapturesALoginItWatchedWhenAnIsolatedHomeHasDrifted(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, _, credFile := boundStoreForClaudeMain(t, app)

	opts := commonOpts{Format: formatText}
	if code, out := captureStdout(t, func() int {
		return runUseIsolated(ctx, app, opts, constants.ToolClaude, "main")
	}); code != constants.ExitOK {
		t.Fatalf("use -i exit %d: %s", code, out)
	}
	home := app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "main")
	if got := readFile(t, filepath.Join(home, ".claude.json")); !strings.Contains(got, "main-uuid") {
		t.Fatalf("the fixture needs the home to start out naming this account: %q", got)
	}
	// Somebody logged in as side inside that home. It has no fragment and no pin
	// record; it is a reader because the store path composes from the account name.
	writeFile(t, filepath.Join(home, ".claude.json"), claudeIdentityFile("side-uuid"))

	const refreshed = "sk-ant-oat01-RELOGGED-bbbb"
	seen := []string{}
	withInteractive(t, loginInto(t, constants.ToolClaude, refreshed, "main-uuid", now.Add(8*time.Hour), &seen))

	code, out, stderr := captureBoth(t, func() int {
		return runRelogin(ctx, app, opts, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	if got := readFile(t, credFile); !strings.Contains(got, refreshed) {
		t.Fatalf("the login must land in the bound store for this to be about attribution: %s", got)
	}
	if got := readFile(t, filepath.Join(home, ".claude.json")); !strings.Contains(got, "side-uuid") {
		t.Fatalf("the home must still name another account: %q", got)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
		t.Fatalf("a login kae ran itself must reach the snapshot: %s", got)
	}
	if strings.Contains(stderr, "disagree about whose login it is") {
		t.Errorf("the home's disagreement must not refuse this one: %q", stderr)
	}
	if !strings.Contains(out, "Logged claude in for claude/main in this directory") {
		t.Errorf("a login kae observed end to end must be reported as one: %q", out)
	}
}

// The override's first conjunct, and the case that decided it may not rest on the
// acting directory's label alone: the flow leaves nothing of its own — an abort, or a
// failure — while the **sibling** refreshes the shared store in place, which is a
// perfectly ordinary thing for its own claude to do during an interactive login. The
// store changed, so relogin does not take its unchanged branch and the harvest runs;
// the copy there is the sibling's, and this directory's label is the one `kae pin`
// planted. Capturing it would file another account's token under this name, which is
// the one mistake nothing offline can detect afterwards.
//
// So the mtime pair is what separates this from the test above: same readers, same
// disagreement, same confirming label here — and no write by the tool in this
// directory. Measured on today's code before the override existed, as the case its
// refusal was already protecting (2026-08-16).
func TestReloginDoesNotCaptureWhenTheFlowWroteNoLabelHere(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	dir, _, credFile := boundStoreForClaudeMain(t, app)
	_, siblingStore := bindClaudeHere(t, app, "main")
	writeFile(t, filepath.Join(siblingStore, ".claude.json"), claudeIdentityFile("side-uuid"))
	// Somebody logged in as side inside the sibling, so the shared store holds side's copy.
	writeFile(t, credFile, claudeOAuthPayload("sk-ant-oat01-SIDE-OLD-dddd", now.Add(2*time.Hour)))
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	const sideRefreshed = "sk-ant-oat01-SIDE-REFRESHED-eeee"
	withInteractive(t, func(_ context.Context, extraEnv []string, _ string, _ ...string) (int, error) {
		credDir := ""
		for _, entry := range extraEnv {
			if rest, ok := strings.CutPrefix(entry, credentialEnvVar(constants.ToolClaude)+"="); ok {
				credDir = rest
			}
		}
		// The sibling's claude refreshes side's token while the user abandons the flow.
		// Nothing is written in *this* directory.
		writeFile(t, filepath.Join(credDir, ".credentials.json"),
			claudeOAuthPayload(sideRefreshed, now.Add(8*time.Hour)))
		return 1, nil
	})

	code, out, stderr := captureBoth(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	// Positive control: the store did change, so the harvest ran rather than being
	// short-circuited by relogin's unchanged branch, which returns before it.
	if got := readFile(t, credFile); !strings.Contains(got, sideRefreshed) {
		t.Fatalf("the fixture must leave the sibling's refreshed copy in the store: %s", got)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, sideRefreshed) {
		t.Fatalf("another account's token must not be filed under this one: %s", got)
	}
	if !strings.Contains(stderr, "disagree about whose login it is") {
		t.Errorf("the refusal must still be the reader disagreement: %q", stderr)
	}
	if strings.Contains(out, "Logged claude in") {
		t.Errorf("the success line may not claim an account kae did not attribute: %q", out)
	}
}

// The override's second conjunct. kae watched the tool write a label here, so the first
// one holds — but the label names **another account**, because that is who the user
// logged in as, while an honest sibling still reads this store as this account's. The
// override is about *this* directory's own reading winning; a directory that reads the
// store as somebody else's has not read it as this account's, and nothing about having
// watched the write changes what the write said.
//
// Without the conjunct the flow's own foreign login would be captured under this
// account's name, which is the mis-filing docs/ROADMAP.md § Attribution reads a label
// kae may have written itself exists about, reopened from the caller's side.
func TestReloginDoesNotCaptureAForeignLoginItWatched(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	dir, _, credFile := boundStoreForClaudeMain(t, app)
	// An honest sibling: bound to the same account and still reading the store as its own.
	_, siblingStore := bindClaudeHere(t, app, "main")
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	const foreign = "sk-ant-oat01-SIDE-ffff"
	seen := []string{}
	withInteractive(t, loginInto(t, constants.ToolClaude, foreign, "side-uuid", now.Add(8*time.Hour), &seen))

	code, out, stderr := captureBoth(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	if got := readFile(t, filepath.Join(siblingStore, ".claude.json")); !strings.Contains(got, "main-uuid") {
		t.Fatalf("the sibling must be the confirming reader for this to be the disagree outcome: %q", got)
	}
	if got := readFile(t, credFile); !strings.Contains(got, foreign) {
		t.Fatalf("the login still belongs in the store it was made in: %s", got)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, mainToken) {
		t.Fatalf("another account's login must not reach this snapshot: %s", got)
	}
	if strings.Contains(out, "Logged claude in") {
		t.Errorf("the success line may not claim an account kae did not attribute: %q", out)
	}
}
