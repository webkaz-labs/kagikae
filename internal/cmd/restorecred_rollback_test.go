package cmd

// Rollback and restore tests over a recorded credential that is newer, older
// or cannot be ordered against the live one.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/state"
)

// The rollback half of the same fact, and the case a single shared "usable" gate went
// silent on — which is the *worse* direction: a tombstoned recorded copy written over a
// working login of the same account destroys it for certain, not merely probably, and
// afterwards only the pre-rollback backup holds it. The wording differs from the
// superseded case because a dead copy is not older, it never worked.
func TestRollbackWarnsWhenTheRecordedCredentialCannotLogIn(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")
	now := app.Now()

	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 1*time.Hour))
	// The backup records a tombstone: blank tokens, so nothing to authenticate with.
	writeFile(t, credsPath, fmt.Sprintf(
		`{"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":%d,"refreshTokenExpiresAt":%d}}`,
		now.Add(-2*time.Hour).UnixMilli(), now.Add(-time.Hour).UnixMilli(),
	))
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	// Then the user logs in again, in place, with nothing harvesting it.
	writeFile(t, credsPath, claudeOAuthPayload(refreshedToken, expiryIn(app, 3*time.Hour)))

	code, _, stderr := captureBoth(t, func() int { return runRollback(ctx, app, opts, "") })
	if code != constants.ExitOK {
		t.Fatalf("rollback must still happen: %d (%s)", code, stderr)
	}
	if !strings.Contains(stderr, "that carries no usable token, while the live store holds one") {
		t.Fatalf("a dead recorded copy over a working login must be reported: %q", stderr)
	}
	// Not the ordering wording: the recorded copy is not "older", it never worked.
	if strings.Contains(stderr, "recorded an older") {
		t.Fatalf("a tombstone does not support an ordering claim: %q", stderr)
	}
	// Nor the cannot-compare wording: a tombstone is provably dead, so kae says so.
	if strings.Contains(stderr, "cannot compare") {
		t.Fatalf("a tombstone is not merely unreadable: %q", stderr)
	}
	if !strings.Contains(stderr, "the newer copy is left only in backup") {
		t.Fatalf("the remedy must name the pre-rollback backup: %q", stderr)
	}
}

// The other reason a recorded copy cannot be ordered, and it licenses a **weaker**
// statement: a payload kae cannot parse may well be a working login in a shape kae has
// not been taught (the same thing liveUnreadable means, and what AGENTS.md says about it).
// Claiming it "cannot log in" told the user to undo a rollback that had just restored a
// credential which was probably fine — a review finding.
//
// Both sub-shapes of that branch, because they arrive by different arms of the parser and
// only one of them was covered: a deadline that no longer decodes leaves the reading
// Known-but-undated, while a missing `expiresAt` key leaves it not Known at all. Widening
// the strong wording to `|| !Known` survived the suite while only the first was tested.
func TestRollbackSaysItCannotCompareARecordedCredentialItCannotOrder(t *testing.T) {
	shapes := []struct{ name, payload string }{
		// A live access token whose deadline no longer decodes: not a tombstone — there is
		// something to authenticate with — but nothing kae can order it by.
		{"undated", `{"claudeAiOauth":{"accessToken":"` + mainToken + `","refreshToken":"rt-x","expiresAt":"1798761600000"}}`},
		// No deadline field at all, which is what claude's parser refuses outright.
		{"unknown", `{"claudeAiOauth":{"accessToken":"` + mainToken + `","refreshToken":"rt-x"}}`},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			app := testApp(t, nil)
			ctx := context.Background()
			opts := commonOpts{Format: formatText}
			credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

			captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 1*time.Hour))
			writeFile(t, credsPath, shape.payload)
			// Positive control on the fixture: it must be un-orderable without being a
			// tombstone, or the test would be exercising the branch next door.
			info := freshnessOf(constants.ToolClaude, []byte(shape.payload))
			if info.Revoked || orderable(info) {
				t.Fatalf("fixture must be un-orderable and not revoked: %+v", info)
			}
			code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
			mustExit(t, constants.ExitOK, code, out)
			writeFile(t, credsPath, claudeOAuthPayload(refreshedToken, expiryIn(app, 3*time.Hour)))

			code, _, stderr := captureBoth(t, func() int { return runRollback(ctx, app, opts, "") })
			if code != constants.ExitOK {
				t.Fatalf("rollback must still happen: %d (%s)", code, stderr)
			}
			if !strings.Contains(stderr, "that kae cannot compare with the one in the live store") {
				t.Fatalf("an un-orderable copy must be reported as incomparable: %q", stderr)
			}
			if !strings.Contains(stderr, "so kae cannot tell which of the two claude can still refresh") {
				t.Fatalf("the consequence must not be stated as a certainty: %q", stderr)
			}
			// kae must claim neither that the copy is dead nor that it is older.
			if strings.Contains(stderr, "carries no usable token") || strings.Contains(stderr, "recorded an older") {
				t.Fatalf("kae cannot order this copy, so it knows neither of those: %q", stderr)
			}
		})
	}
}

// A backup that recorded no credential at all is silent, which docs/CLI.md states
// outright. Without the guard the message claims something about a credential that does
// not exist and points the remedy the wrong way; measured 2026-08-05, dropping it
// survived the whole suite, so the documented silence was unpinned.
func TestRollbackIsSilentWhenTheBackupRecordedNoCredential(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 1*time.Hour))
	// main stays the recorded active account while its live credential is gone, so the
	// backup taken by the switch records the artifact as absent.
	if err := os.Remove(credsPath); err != nil {
		t.Fatal(err)
	}
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	// Then a login, in place, with nothing harvesting it — the state that makes every
	// other branch of this warning fire.
	writeFile(t, credsPath, claudeOAuthPayload(refreshedToken, expiryIn(app, 3*time.Hour)))

	code, _, stderr := captureBoth(t, func() int { return runRollback(ctx, app, opts, "") })
	if code != constants.ExitOK {
		t.Fatalf("rollback failed: %d (%s)", code, stderr)
	}
	// Positive control: the rollback ran and took the credential away, which is what the
	// backup says. So the silence below is a decision, not a function that never ran.
	live := readFile(t, credsPath)
	if strings.Contains(live, "claudeAiOauth") {
		t.Fatalf("the rollback did not remove the credential, so its silence proves nothing: %s", live)
	}
	if strings.Contains(stderr, "rotates single-use") {
		t.Fatalf("nothing was handed back, so nothing may be claimed about it: %q", stderr)
	}
}

// The skip is per tool, and every other test drives `restore` with a single entry —
// so nothing pinned that a skipped claude leaves codex's own restore alone. Measured:
// turning the loop's `continue` into a `break` survived the whole suite without this.
func TestRunSharedSkipsOnlyTheToolWhoseCredentialWasSuperseded(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	claudeCreds := filepath.Join(app.Env.Home, ".claude", ".credentials.json")
	codexAuth := filepath.Join(app.Env.Home, ".codex", "auth.json")
	app.Config.Profiles["main"] = config.Profile{Accounts: map[string]string{"claude": "main", "codex": "main"}}

	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 2*time.Hour))
	seedCodex(t, app, "codex-main-token")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "main") })
	mustExit(t, constants.ExitOK, code, out)
	// Both tools are now captured under `main`; codex was captured last, so re-assert
	// claude as active too — the profile switch below is what makes both active.
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "all", "main") })
	mustExit(t, constants.ExitOK, code, out)
	// codex's pre-child state, which the restore must put back untouched.
	writeFile(t, codexAuth, `{"tokens":{"access_token":"codex-main-token"}}`)

	withInteractive(t, func(_ context.Context, _ []string, _ string, _ ...string) (int, error) {
		// Only claude refreshes. codex is left as the child found it.
		writeFile(t, claudeCreds, claudeOAuthPayload(refreshedToken, expiryIn(app, 3*time.Hour)))
		writeFile(t, codexAuth, `{"tokens":{"access_token":"codex-CHILD-token"}}`)
		return 0, nil
	})
	code, _, stderr := captureBoth(t, func() int {
		return runRun(ctx, app, opts, runModeShared, "all", "main", []string{"claude"})
	})
	if code != 0 {
		t.Fatalf("run failed: %d (%s)", code, stderr)
	}
	if live := readFile(t, claudeCreds); !strings.Contains(live, refreshedToken) {
		t.Fatalf("claude's refreshed copy was not kept: %s", live)
	}
	if got := readFile(t, codexAuth); !strings.Contains(got, "codex-main-token") {
		t.Fatalf("codex was not restored while claude was skipped: %s", got)
	}
	// Something *was* restored, so the line is earned — and it must not be suppressed
	// just because another tool was skipped.
	if !strings.Contains(stderr, "previous auth state restored") {
		t.Fatalf("codex's restore must be reported: %q", stderr)
	}
}

// The decision as a **set**, asserted for both orderings of the same two tools. The
// command-level test above can only ever present them in the order constants.Tools puts
// them in, and a `continue` mistakenly written as `break` is invisible whenever the
// skipped tool happens to come last — so that test pinned the loop by accident of
// ordering, and a reorder would have reopened it silently (measured 2026-08-05).
func TestToolsToRestoreSkipsTheSupersededToolInEitherOrder(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 2*time.Hour))
	seedCodex(t, app, "codex-main-token")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "main") })
	mustExit(t, constants.ExitOK, code, out)

	be := testBackend(t, app)
	claudePlan, err := app.planTool(ctx, constants.ToolClaude, "main")
	if err != nil {
		t.Fatal(err)
	}
	codexPlan, err := app.planTool(ctx, constants.ToolCodex, "main")
	if err != nil {
		t.Fatal(err)
	}
	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	st.Active[constants.ToolClaude] = "main"
	st.Active[constants.ToolCodex] = "main"
	meta, err := app.createBackup(ctx, be, []toolPlan{claudePlan, codexPlan}, st, "run")
	if err != nil {
		t.Fatal(err)
	}
	// Only claude's live copy moves past what the backup recorded, so only claude may be
	// skipped — whichever position it holds in the slice.
	writeFile(t, credsPath, claudeOAuthPayload(refreshedToken, expiryIn(app, 3*time.Hour)))

	for _, order := range [][]toolPlan{
		{claudePlan, codexPlan},
		{codexPlan, claudePlan},
	} {
		name := order[0].Tool + "-first"
		t.Run(name, func(t *testing.T) {
			var restore map[string]bool
			_, stderr := captureStderr(t, func() int {
				restore = app.toolsToRestore(ctx, be, meta, order)
				return 0
			})
			if restore[constants.ToolClaude] {
				t.Fatalf("claude's superseded restore must be skipped: %v (%s)", restore, stderr)
			}
			if !restore[constants.ToolCodex] {
				t.Fatalf("codex must still be restored: %v (%s)", restore, stderr)
			}
			if len(restore) != 1 {
				t.Fatalf("exactly one tool should be restored: %v", restore)
			}
		})
	}
}

// The rollback warning's live branch needs attribution just as much as run -s's skip:
// a *later* copy in the live store may be a different account's, and naming it as
// "newer than what this backup recorded for claude/main" would point the user at
// somebody else's token. Measured: dropping the attribution call survived the suite.
func TestRollbackIgnoresANewerCopyOfAnotherAccount(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 1*time.Hour))
	// side is strictly later than what the backup will record for main, so only
	// attribution — not the ordering — can disqualify it.
	captureClaudeAt(t, app, "side", sideToken, expiryIn(app, 3*time.Hour))
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	// Now switch away so the backup being rolled back names main while side is live.
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, out)

	code, _, stderr := captureBoth(t, func() int { return runRollback(ctx, app, opts, "") })
	if code != constants.ExitOK {
		t.Fatalf("rollback failed: %d (%s)", code, stderr)
	}
	assertRolledBack(t, app, mainToken)
	if strings.Contains(stderr, "than the one in the live store") {
		t.Fatalf("another account's copy is not evidence about this one: %q", stderr)
	}
}

// The remedy names the *pre-rollback* backup, and only that one: the id of the backup
// being restored would tell the user to re-run the rollback that just overwrote the
// copy. Measured: swapping the two ids survived every assertion in the suite.
func TestRollbackRemedyNamesThePreRollbackBackup(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 2*time.Hour))
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	restored, found, err := backup.Latest(app.Paths.BackupsDir())
	if err != nil || !found {
		t.Fatalf("no backup to roll back to: found=%v err=%v", found, err)
	}
	writeFile(t, credsPath, claudeOAuthPayload(refreshedToken, expiryIn(app, 3*time.Hour)))

	code, _, stderr := captureBoth(t, func() int { return runRollback(ctx, app, opts, restored.ID) })
	if code != constants.ExitOK {
		t.Fatalf("rollback failed: %d (%s)", code, stderr)
	}
	// The rollback took its own backup of the live state; that is where the newer copy
	// now is, and its id is the one the remedy has to carry.
	pre, found, err := backup.Latest(app.Paths.BackupsDir())
	if err != nil || !found || pre.ID == restored.ID {
		t.Fatalf("no pre-rollback backup was taken: %+v (restored %s)", pre, restored.ID)
	}
	// Matched with the closing paren, because a backup id is a *prefix* of the
	// collision-suffixed ids minted in the same second: without the delimiter the
	// negative assertion below matches `…Z-2` while looking for `…Z` and fails a
	// correct message.
	if !strings.Contains(stderr, "kae rollback --to "+pre.ID+")") {
		t.Fatalf("the remedy must name the pre-rollback backup %s: %q", pre.ID, stderr)
	}
	if strings.Contains(stderr, "kae rollback --to "+restored.ID+")") {
		t.Fatalf("naming %s would tell the user to re-run this rollback: %q", restored.ID, stderr)
	}
}

// Both halves of the live branch carry weight. The mirror half (live later than the
// snapshot) is pinned by the two "prefers" tests; this pins the other, where the
// backup's own copy is the newest of the three and there is nothing to warn about.
// Reachable by rolling back to an old backup and then to a newer one.
func TestRollbackIsQuietWhenTheBackupHoldsTheNewestCopy(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 3*time.Hour))
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	// recorded (3h) is later than both the live store (2h) and the snapshot (1h).
	writeFile(t, credsPath, claudeOAuthPayload(refreshedToken, expiryIn(app, 2*time.Hour)))
	writeSnapshotPayload(t, app, constants.ToolClaude, "main",
		claudeOAuthPayload(rolledBackToken, expiryIn(app, 1*time.Hour)))

	code, _, stderr := captureBoth(t, func() int { return runRollback(ctx, app, opts, "") })
	if code != constants.ExitOK {
		t.Fatalf("rollback failed: %d (%s)", code, stderr)
	}
	assertRolledBack(t, app, mainToken)
	if strings.Contains(stderr, "refresh token rotates single-use") {
		t.Fatalf("the backup holds the newest copy; there is nothing to warn about: %q", stderr)
	}
}

// The claim "<tool>'s refresh token rotates single-use" is a measurement, and only
// claude's has been made. Both restore surfaces are gated on rotatesSingleUse, and the
// rollback warning's snapshot branch needs no attribution — so without that gate a
// codex rollback would state an unmeasured fact about codex. Measured: dropping it
// survived the suite.
func TestRollbackNeverClaimsRotationForAnUnmeasuredTool(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	codexAuth := filepath.Join(app.Env.Home, ".codex", "auth.json")
	seedCodex(t, app, "codex-main-token")
	// A JWT deadline on the copy the backup will record, so the recorded copy is
	// *orderable*: seedCodex's plain token dates to nothing, and a non-orderable recorded
	// copy is declined before the tool gate is ever consulted — which would leave this
	// test passing for a reason that has nothing to do with what it names.
	recorded := codexAuthAt(app, 1*time.Hour)
	writeFile(t, codexAuth, recorded)
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "main") })
	mustExit(t, constants.ExitOK, code, out)
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "codex", "main") })
	mustExit(t, constants.ExitOK, code, out)
	// A codex credential whose expiry is later than the one the backup recorded. codex
	// is datable, so the comparison itself would succeed; only the tool gate stops it.
	writeSnapshotPayload(t, app, constants.ToolCodex, "main", codexAuthAt(app, 3*time.Hour))
	// Distinct from the recorded copy, or the positive control below cannot tell a
	// restore that happened from one that did not.
	writeFile(t, codexAuth, codexAuthAt(app, 2*time.Hour))

	code, _, stderr := captureBoth(t, func() int { return runRollback(ctx, app, opts, "") })
	if code != constants.ExitOK {
		t.Fatalf("rollback failed: %d (%s)", code, stderr)
	}
	// Positive control: the rollback ran and put codex's recorded credential back, so the
	// silence below is a decision rather than a function that never executed.
	if got := readFile(t, codexAuth); got != recorded {
		t.Fatalf("the rollback did not restore codex, so its silence proves nothing: %s", got)
	}
	if strings.Contains(stderr, "rotates single-use") {
		t.Fatalf("codex's rotation has not been measured; kae must not claim it: %q", stderr)
	}
}

// The same rule on the recapture side, and reaching its ordering gate takes more than
// two stale copies. That gate sits behind an early return taken whenever the live copy
// does **not** need a re-login, and a codex payload that still carries a refresh token
// never does, however old its access token is (needsRelogin has no published refresh
// expiry to judge it by). The first version of this test seeded exactly that and so
// asserted an absence it could never have observed — measured, the mutation survived it.
// Both copies must therefore have **no refresh token** and a past deadline.
func TestSwitchAwayNeverClaimsRotationForAnUnmeasuredTool(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	codexAuth := filepath.Join(app.Env.Home, ".codex", "auth.json")

	seedCodex(t, app, "codex-side-token")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "side") })
	mustExit(t, constants.ExitOK, code, out)
	seedCodex(t, app, "codex-main-token")
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "main") })
	mustExit(t, constants.ExitOK, code, out)
	// The snapshot's deadline is later than the live store's, both already past, neither
	// refreshable — so the usability refusal cannot fire and only the ordering separates
	// the two, which is the state the tool gate has to answer for.
	writeSnapshotPayload(t, app, constants.ToolCodex, "main", codexDeadAuthAt(app, -1*time.Hour))
	writeFile(t, codexAuth, codexDeadAuthAt(app, -3*time.Hour))

	code, _, stderr := captureBoth(t, func() int { return runSwitch(ctx, app, opts, "codex", "side") })
	if code != constants.ExitOK {
		t.Fatalf("switch failed: %d (%s)", code, stderr)
	}
	// Positive: the recapture ran to completion for codex, which is what proves the
	// ordering refusal was *reached and declined* rather than never visited. With the
	// tool gate dropped it refuses here instead, leaving the snapshot untouched.
	snap := snapshotPayload(t, app, testBackend(t, app), constants.ToolCodex, "main")
	if snap != codexDeadAuthAt(app, -3*time.Hour) {
		t.Fatalf("codex's snapshot was not recaptured from the live store: %s", snap)
	}
	if strings.Contains(stderr, "rotates single-use") {
		t.Fatalf("codex's rotation has not been measured; kae must not claim it: %q", stderr)
	}
}

// A backup taken while nothing was active records a credential but no account to
// attribute it to, and then there is no chain to compare against: `active_before` is
// what names it. Without the guard the warning would read "for claude/" and the
// snapshot lookup would be asked for an account named "". Reachable through the login
// flow on a fresh state, which backs the live store up before anything is captured;
// built here with createBackup directly, because that is the fact under test.
func TestRollbackSaysNothingAboutABackupWithNoActiveAccount(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 1*time.Hour))
	plan, err := app.planTool(ctx, constants.ToolClaude, "main")
	if err != nil {
		t.Fatal(err)
	}
	// The state a login flow on a fresh install backs up against: a live credential, and
	// nothing recorded as active.
	meta, err := app.createBackup(ctx, testBackend(t, app), []toolPlan{plan}, state.New(), "login")
	if err != nil {
		t.Fatal(err)
	}
	if meta.ActiveBefore[constants.ToolClaude] != "" {
		t.Fatalf("this test needs a backup with no active account: %+v", meta.ActiveBefore)
	}
	// A live copy strictly later than the recorded one, so only the missing account name
	// keeps the comparison from having a sound side.
	writeFile(t, credsPath, claudeOAuthPayload(refreshedToken, expiryIn(app, 3*time.Hour)))

	code, _, stderr := captureBoth(t, func() int { return runRollback(ctx, app, opts, meta.ID) })
	if code != constants.ExitOK {
		t.Fatalf("rollback failed: %d (%s)", code, stderr)
	}
	assertRolledBack(t, app, mainToken)
	if strings.Contains(stderr, "rotates single-use") {
		t.Fatalf("nothing names the chain being restored, so nothing may be claimed: %q", stderr)
	}
}

// A healthy rollback stays quiet. The check exists because an unconditional version
// of it would fire on every rollback, which is the wallpaper v0.15.0/v0.15.1 made
// twice in both directions.
func TestRollbackIsQuietWhenNothingSupersededTheBackup(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 2*time.Hour))
	captureClaudeAt(t, app, "side", sideToken, expiryIn(app, 3*time.Hour))
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	code, _, stderr := captureBoth(t, func() int { return runRollback(ctx, app, opts, "") })
	if code != constants.ExitOK {
		t.Fatalf("rollback failed: %d (%s)", code, stderr)
	}
	assertRolledBack(t, app, sideToken)
	if strings.Contains(stderr, "refresh token rotates single-use") {
		t.Fatalf("a rollback with nothing newer anywhere must be quiet: %q", stderr)
	}
}
