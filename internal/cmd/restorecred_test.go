package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
	"github.com/webkaz-labs/kagikae/internal/state"
)

const (
	refreshedToken  = "sk-ant-oat01-REFRESHED-cccc"
	rolledBackToken = "sk-ant-oat01-ROLLED-BACK-dddd"
)

// expiryIn derives a credential deadline from the app's own clock rather than
// restating it, and every test below takes **three** distinct deadlines from it. Two
// copies given one value compare equal, which stops before the ordering guard the
// test means to exercise — the false pass measured on 2026-08-04.
func expiryIn(app *App, d time.Duration) time.Time { return app.Now().Add(d) }

// codexAuthAt renders a codex auth.json whose access token carries a known expiry.
// codex dates a credential from its JWT `exp`, not from a JSON field, which is why
// this cannot reuse claudeOAuthPayload's shape — the two derivations are per-tool and
// must never be ported across.
func codexAuthAt(app *App, d time.Duration) string {
	return `{"tokens":{"access_token":"` + jwtWithExp(expiryIn(app, d)) +
		`","refresh_token":"codex-rt"}}`
}

// codexDeadAuthAt is the same with **no** refresh token, which is what makes a past
// deadline mean "needs a re-login": with a refresh token and no published refresh
// expiry, needsRelogin has nothing to judge and answers false however old the access
// token is. Tests that need to get past that early return must use this one.
func codexDeadAuthAt(app *App, d time.Duration) string {
	return `{"tokens":{"access_token":"` + jwtWithExp(expiryIn(app, d)) + `","refresh_token":""}}`
}

// writeSnapshotPayload replaces what an account's snapshot holds for its credential,
// which is what a bound-directory harvest or a switch-away recapture leaves there.
// The mechanism that put it there is not what these tests are about, so they state
// the resulting copy directly.
func writeSnapshotPayload(t *testing.T, app *App, tool, accountName, payload string) {
	t.Helper()
	be := testBackend(t, app)
	acc, found, err := account.Load(app.Paths.AccountDir(tool, accountName))
	if err != nil || !found {
		t.Fatalf("load snapshot %s/%s: found=%v err=%v", tool, accountName, found, err)
	}
	art := acc.Artifacts[credentialArtifactName(tool)]
	if !art.Present {
		t.Fatalf("snapshot %s/%s records no credential", tool, accountName)
	}
	if err := be.Set(context.Background(), art.SecretRef, []byte(payload)); err != nil {
		t.Fatal(err)
	}
}

func TestSupersedesOrdersByExpiryAlone(t *testing.T) {
	early := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	usable := func(at time.Time) freshness.Info {
		return freshness.Info{Known: true, ExpiresAt: at, HasRefresh: true}
	}
	cases := []struct {
		name string
		a, b freshness.Info
		want bool
	}{
		{"later beats earlier", usable(late), usable(early), true},
		{"earlier loses to later", usable(early), usable(late), false},
		// Equal deadlines are the same copy as far as kae can tell, and "not newer"
		// is what keeps a no-op restore from being treated as destructive.
		{"equal is not later", usable(early), usable(early), false},
		// b degrading to the zero cutoff: a copy with no comparable deadline loses to
		// any usable one.
		{"unreadable b loses", usable(early), freshness.Info{}, true},
		{"tombstoned b loses", usable(early), freshness.Info{Known: true, Revoked: true, ExpiresAt: late}, true},
		{"undated b loses", usable(early), freshness.Info{Known: true}, true},
		// The a-side guard. Only the `Revoked` row is killable, and that is a property
		// of the arithmetic rather than a gap: the cutoff floor *is* the zero time, so a
		// zero `ExpiresAt` is never `After` it and the `Known`/`IsZero` rows answer false
		// with or without their guards (measured 2026-08-05, the same reason the `IsZero`
		// half is documented as unobservable on readLiveCredential). They are kept as a
		// statement of the contract, not as assertions that can fail. A tombstone is a
		// fully-formed payload, so presence proves nothing — that row does the work.
		{"unreadable a never wins", freshness.Info{}, freshness.Info{}, false},
		{"tombstoned a never wins", freshness.Info{Known: true, Revoked: true, ExpiresAt: late}, usable(early), false},
		{"undated a never wins", freshness.Info{Known: true}, freshness.Info{}, false},
	}
	for _, c := range cases {
		if got := supersedes(c.a, c.b); got != c.want {
			t.Errorf("%s: supersedes = %v, want %v", c.name, got, c.want)
		}
	}
}

// The defect: `kae run -s claude <the account that is already active>` restored the
// pre-child copy of that same login over the one the child had just refreshed.
// claude's refresh token rotates single-use, so that is a logout of the real home
// reported as "previous auth state restored".
func TestRunSharedKeepsTheCredentialItsChildRefreshed(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	// runCapture records the account as active, so main is both the target and what
	// the backup will name in active_before.
	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 2*time.Hour))

	refreshed := claudeOAuthPayload(refreshedToken, expiryIn(app, 3*time.Hour))
	withInteractive(t, func(_ context.Context, _ []string, name string, _ ...string) (int, error) {
		writeFile(t, credsPath, refreshed)
		return 0, nil
	})
	code, _, stderr := captureBoth(t, func() int {
		return runRun(ctx, app, opts, runModeShared, "claude", "main", []string{"claude"})
	})
	if code != 0 {
		t.Fatalf("run failed: %d (%s)", code, stderr)
	}
	// Positive first: the copy the child left is the one still live. Asserting only
	// the absence of the old token would also pass if the file had gone missing.
	if live := readFile(t, credsPath); !strings.Contains(live, refreshedToken) {
		t.Fatalf("the refreshed credential was not kept: %s", live)
	}
	if live := readFile(t, credsPath); strings.Contains(live, mainToken) {
		t.Fatalf("the pre-child copy was restored over it: %s", live)
	}
	if !strings.Contains(stderr, "was already the active account") {
		t.Fatalf("the skip must be reported: %q", stderr)
	}
	if strings.Contains(stderr, "previous auth state restored") {
		t.Fatalf("nothing was restored, so it must not be claimed: %q", stderr)
	}
	// The account is unchanged: run -s never records a switch, and skipping the
	// restore must not turn the temporary application into a permanent one.
	st, err := app.loadState()
	if err != nil || st.Active[constants.ToolClaude] != "main" {
		t.Fatalf("active account changed: %+v %v", st, err)
	}
}

// The gate is "the target was already active". With another account active the two
// copies are different chains and putting the previous one back is exactly what
// run -s promises — so the restore still has to happen.
func TestRunSharedRestoresADifferentAccount(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 2*time.Hour))
	// Captured last, so side is the active account and the live store holds it.
	captureClaudeAt(t, app, "side", sideToken, expiryIn(app, 2*time.Hour))

	withInteractive(t, func(_ context.Context, _ []string, _ string, _ ...string) (int, error) {
		writeFile(t, credsPath, claudeOAuthPayload(refreshedToken, expiryIn(app, 3*time.Hour)))
		return 0, nil
	})
	code, _, stderr := captureBoth(t, func() int {
		return runRun(ctx, app, opts, runModeShared, "claude", "main", []string{"claude"})
	})
	if code != 0 {
		t.Fatalf("run failed: %d (%s)", code, stderr)
	}
	if live := readFile(t, credsPath); !strings.Contains(live, sideToken) {
		t.Fatalf("the previously active account was not restored: %s", live)
	}
	if !strings.Contains(stderr, "previous auth state restored") {
		t.Fatalf("the restore must be reported: %q", stderr)
	}
}

// Attribution, in the global frame: a child that logs in as somebody else leaves a
// credential with a later deadline that is not this account's chain at all. Keeping
// it would leave the live store holding one account while kae records another — so
// the ordering alone must not decide.
func TestRunSharedRestoresWhenTheChildLoggedInAsAnotherAccount(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 2*time.Hour))

	withInteractive(t, func(_ context.Context, _ []string, _ string, _ ...string) (int, error) {
		writeFile(t, credsPath, claudeOAuthPayload(refreshedToken, expiryIn(app, 3*time.Hour)))
		// `claude /login` rewrites the identity cache unconditionally, which is the
		// only offline evidence that the token belongs to another account.
		claudeJSON(t, app, `{"oauthAccount":`+claudeOAuthAccount("side-uuid", "side-uuid@example.com")+
			`,"projects":{}}`)
		return 0, nil
	})
	code, _, stderr := captureBoth(t, func() int {
		return runRun(ctx, app, opts, runModeShared, "claude", "main", []string{"claude"})
	})
	if code != 0 {
		t.Fatalf("run failed: %d (%s)", code, stderr)
	}
	if live := readFile(t, credsPath); !strings.Contains(live, mainToken) {
		t.Fatalf("an unattributable newer copy must not be kept: %s", live)
	}
	if strings.Contains(stderr, "was already the active account") {
		t.Fatalf("the skip must not be reported: %q", stderr)
	}
}

// A child that refreshed nothing leaves a copy with the same deadline, which is not
// a later one — the restore is then a no-op and stays unconditional.
func TestRunSharedRestoresWhenNothingWasRefreshed(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 2*time.Hour))
	ran := false
	withInteractive(t, func(_ context.Context, _ []string, _ string, _ ...string) (int, error) {
		ran = true
		return 0, nil
	})
	code, _, stderr := captureBoth(t, func() int {
		return runRun(ctx, app, opts, runModeShared, "claude", "main", []string{"claude"})
	})
	if code != 0 || !ran {
		t.Fatalf("run failed: %d ran=%v (%s)", code, ran, stderr)
	}
	if live := readFile(t, credsPath); !strings.Contains(live, mainToken) {
		t.Fatalf("the account must still be live: %s", live)
	}
	if !strings.Contains(stderr, "previous auth state restored") {
		t.Fatalf("the restore must be reported: %q", stderr)
	}
}

// The laundering path: `kae rollback` deliberately puts an *older* copy in the live
// store, and the switch-away recapture used to file it over the snapshot — which by
// then held the only copy that could still refresh. recaptureWouldDowngrade's
// usability test cannot see this: both copies are usable, they differ only in order.
func TestSwitchAwayKeepsASnapshotNewerThanTheLiveStore(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	captureClaudeAt(t, app, "side", sideToken, expiryIn(app, 2*time.Hour))
	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 3*time.Hour))
	// What a rollback leaves: an older copy of main's own login in the live store,
	// with main's identity cache still beside it (so attribution confirms and only
	// the ordering separates the two).
	writeFile(t, credsPath, claudeOAuthPayload(rolledBackToken, expiryIn(app, 1*time.Hour)))

	code, _, stderr := captureBoth(t, func() int { return runSwitch(ctx, app, opts, "claude", "side") })
	if code != constants.ExitOK {
		t.Fatalf("switch failed: %d (%s)", code, stderr)
	}
	snap := snapshotPayload(t, app, testBackend(t, app), constants.ToolClaude, "main")
	if !strings.Contains(snap, mainToken) {
		t.Fatalf("the later snapshot copy was overwritten: %s", snap)
	}
	if strings.Contains(snap, rolledBackToken) {
		t.Fatalf("the older live copy was recaptured over it: %s", snap)
	}
	if !strings.Contains(stderr, "holds a later claude credential than the live store") {
		t.Fatalf("the refusal must be reported: %q", stderr)
	}
}

// `kae rollback` restores a credential kae recorded earlier. When something has
// refreshed that account since — and been harvested back into its snapshot — the
// recorded copy is the one that can no longer refresh, so "Rolled back to" alone
// would be a success report for a rejected token.
func TestRollbackWarnsWhenTheSnapshotHoldsALaterCopy(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	// side is deliberately *later* than what the backup will record for main. Giving the
	// two the same deadline made the live candidate lose on a tie, so this test proved
	// nothing about attribution and the snapshot branch would have been chosen either way
	// (review finding, measured 2026-08-05).
	captureClaudeAt(t, app, "side", sideToken, expiryIn(app, 4*time.Hour))
	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 2*time.Hour))
	// Backs up the live main login and switches away, so active_before names main.
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, out)
	// A bound directory refreshed main and the harvest brought that copy back.
	writeSnapshotPayload(t, app, constants.ToolClaude, "main",
		claudeOAuthPayload(refreshedToken, expiryIn(app, 3*time.Hour)))

	code, _, stderr := captureBoth(t, func() int { return runRollback(ctx, app, opts, "") })
	if code != constants.ExitOK {
		t.Fatalf("rollback must still happen: %d (%s)", code, stderr)
	}
	if !strings.Contains(stderr, "than the one in snapshot claude/main") {
		t.Fatalf("the superseded restore must be reported: %q", stderr)
	}
	// Branch-specific remedy: the newer copy survives the rollback in the snapshot,
	// so applying it is one switch away — naming the pre-rollback backup here would
	// send the user to a copy that is not the newest.
	if !strings.Contains(stderr, "kae use claude main") {
		t.Fatalf("the snapshot remedy must be named: %q", stderr)
	}
}

// The other source: main stayed active and the tool refreshed in place with nothing
// harvesting it, so the newer copy is in the live store the rollback overwrites.
func TestRollbackWarnsWhenTheLiveStoreHoldsALaterCopy(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 2*time.Hour))
	// A re-apply of the same account: the backup it takes records main both as the
	// active account and as the credential.
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	writeFile(t, credsPath, claudeOAuthPayload(refreshedToken, expiryIn(app, 3*time.Hour)))

	code, _, stderr := captureBoth(t, func() int { return runRollback(ctx, app, opts, "") })
	if code != constants.ExitOK {
		t.Fatalf("rollback must still happen: %d (%s)", code, stderr)
	}
	if !strings.Contains(stderr, "than the one in the live store") {
		t.Fatalf("the superseded restore must be reported: %q", stderr)
	}
	if !strings.Contains(stderr, "the newer copy is left only in backup") {
		t.Fatalf("the pre-rollback backup must be named: %q", stderr)
	}
	// Not the snapshot branch: the snapshot holds the same copy the backup recorded,
	// so telling the user to apply it would achieve nothing.
	if strings.Contains(stderr, "kae use claude main") {
		t.Fatalf("the wrong remedy was offered: %q", stderr)
	}
}

// Both places can hold a later copy at once, and then the remedy has to name the
// live store: that is the copy the rollback overwrites, so afterwards only the
// pre-rollback backup still has it. Naming the snapshot would send the user to a copy
// that is not the newest.
func TestRollbackPrefersTheLiveStoreWhenItIsTheNewestCopy(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 1*time.Hour))
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	// A bound directory's copy was harvested back, and the real home then refreshed
	// past it — three distinct deadlines, so the two candidates cannot tie.
	writeSnapshotPayload(t, app, constants.ToolClaude, "main",
		claudeOAuthPayload(rolledBackToken, expiryIn(app, 2*time.Hour)))
	writeFile(t, credsPath, claudeOAuthPayload(refreshedToken, expiryIn(app, 3*time.Hour)))

	code, _, stderr := captureBoth(t, func() int { return runRollback(ctx, app, opts, "") })
	if code != constants.ExitOK {
		t.Fatalf("rollback must still happen: %d (%s)", code, stderr)
	}
	if !strings.Contains(stderr, "than the one in the live store") {
		t.Fatalf("the live store holds the newest copy and must be named: %q", stderr)
	}
	if strings.Contains(stderr, "kae use claude main") {
		t.Fatalf("the snapshot remedy names a copy that is not the newest: %q", stderr)
	}
}

// The other direction, which is what makes the two candidates a comparison rather
// than a priority list: the live store is newer than the backup but the snapshot is
// newer still, so the newest copy survives the rollback and `kae use` is the remedy.
// Naming the pre-rollback backup here would point at a copy that is not the newest.
func TestRollbackPrefersTheSnapshotWhenItIsTheNewestCopy(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 1*time.Hour))
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	writeFile(t, credsPath, claudeOAuthPayload(refreshedToken, expiryIn(app, 2*time.Hour)))
	writeSnapshotPayload(t, app, constants.ToolClaude, "main",
		claudeOAuthPayload(rolledBackToken, expiryIn(app, 3*time.Hour)))

	code, _, stderr := captureBoth(t, func() int { return runRollback(ctx, app, opts, "") })
	if code != constants.ExitOK {
		t.Fatalf("rollback must still happen: %d (%s)", code, stderr)
	}
	if !strings.Contains(stderr, "than the one in snapshot claude/main") {
		t.Fatalf("the snapshot holds the newest copy and must be named: %q", stderr)
	}
	if strings.Contains(stderr, "the newer copy is left only in backup") {
		t.Fatalf("the newest copy survives the rollback, so it is not backup-only: %q", stderr)
	}
}

// Attribution needs two account *records*. A live identity that is well-formed JSON
// but not an object names no account, so it can neither confirm nor deny — and
// identityDiffers falls back to a byte comparison for exactly those, which would let
// two unreadable sides "agree". Measured on the harvest path 2026-08-05; the same gate
// has to hold here or a child's login is kept on the strength of no evidence.
// A fresh app per shape: run -s recaptures whatever the child left into the snapshot,
// so a second command on the same app would start from an identity this test itself
// poisoned rather than from the state it means to set up.
func TestRunSharedRestoresWhenTheLiveIdentityNamesNoAccount(t *testing.T) {
	for _, shape := range []string{"null", `"main-uuid"`, "[]", "{}"} {
		t.Run(shape, func(t *testing.T) {
			app := testApp(t, nil)
			ctx := context.Background()
			opts := commonOpts{Format: formatText}
			credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

			captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 2*time.Hour))
			withInteractive(t, func(_ context.Context, _ []string, _ string, _ ...string) (int, error) {
				writeFile(t, credsPath, claudeOAuthPayload(refreshedToken, expiryIn(app, 3*time.Hour)))
				claudeJSON(t, app, `{"oauthAccount":`+shape+`,"projects":{}}`)
				return 0, nil
			})
			code, _, stderr := captureBoth(t, func() int {
				return runRun(ctx, app, opts, runModeShared, "claude", "main", []string{"claude"})
			})
			if code != 0 {
				t.Fatalf("run failed: %d (%s)", code, stderr)
			}
			if live := readFile(t, credsPath); !strings.Contains(live, mainToken) {
				t.Fatalf("an unattributable copy must not be kept: %s", live)
			}
		})
	}
}

// The shape the decodability gate actually exists for, which the cases above do not
// reach: when **both** identities are non-records *and* byte-identical, identityDiffers
// falls back to a byte comparison and calls them equal — two sides agreeing about
// nothing would then attribute the child's credential to this account. Reachable
// because `/oauthAccount: null` is recorded by capture and propagated to every store
// of that account (docs/ROADMAP.md), so the backup records it too.
func TestRunSharedRestoresWhenNeitherIdentityNamesAnAccount(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	seedClaude(t, app, mainToken, "main-uuid")
	claudeJSON(t, app, `{"oauthAccount":null,"projects":{}}`)
	writeFile(t, credsPath, claudeOAuthPayload(mainToken, expiryIn(app, 2*time.Hour)))
	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, opts, constants.ToolClaude, "main")
	})
	mustExit(t, constants.ExitOK, code, out)

	withInteractive(t, func(_ context.Context, _ []string, _ string, _ ...string) (int, error) {
		writeFile(t, credsPath, claudeOAuthPayload(refreshedToken, expiryIn(app, 3*time.Hour)))
		return 0, nil
	})
	code, _, stderr := captureBoth(t, func() int {
		return runRun(ctx, app, opts, runModeShared, "claude", "main", []string{"claude"})
	})
	if code != 0 {
		t.Fatalf("run failed: %d (%s)", code, stderr)
	}
	if live := readFile(t, credsPath); !strings.Contains(live, mainToken) {
		t.Fatalf("two identical non-records are not attribution: %s", live)
	}
}

// The other refusal recaptureWouldDowngrade carries, which had no test in the suite
// at all (measured by mutation, 2026-08-05): claude's tombstone is a fully-formed
// payload with a future deadline, so only the usability test separates it from a
// healthy copy, and recapturing it would overwrite a snapshot that still works.
func TestSwitchAwayKeepsAUsableSnapshotOverATombstone(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()

	captureClaudeAt(t, app, "side", sideToken, expiryIn(app, 2*time.Hour))
	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 2*time.Hour))
	// Blank tokens with both deadlines already past: usable-looking structure, nothing
	// left to authenticate or refresh with.
	writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"), fmt.Sprintf(
		`{"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":%d,"refreshTokenExpiresAt":%d}}`,
		now.Add(-2*time.Hour).UnixMilli(), now.Add(-time.Hour).UnixMilli(),
	))

	code, _, stderr := captureBoth(t, func() int { return runSwitch(ctx, app, opts, "claude", "side") })
	if code != constants.ExitOK {
		t.Fatalf("switch failed: %d (%s)", code, stderr)
	}
	snap := snapshotPayload(t, app, testBackend(t, app), constants.ToolClaude, "main")
	if !strings.Contains(snap, mainToken) {
		t.Fatalf("a usable snapshot was overwritten by a dead live copy: %s", snap)
	}
	if !strings.Contains(stderr, "needs a re-login while snapshot claude/main still holds a usable one") {
		t.Fatalf("the refusal must be reported: %q", stderr)
	}
}

// A backup that recorded no *usable* credential is restored verbatim, whatever shape
// the uselessness takes. The restore's job there is to put the real home back the way
// it was, and keeping the child's login instead would leave run -s having applied an
// account permanently — the outcome an earlier version produced for two of these three
// shapes, because a copy with no comparable deadline is "superseded" by any live login
// (review finding, measured 2026-08-05).
//
// All three shapes, not the one that reads worst: the guard is a property of the
// payload, and measuring only the absent case is what let the other two through.
// A fresh app per shape, because run -s recaptures whatever the child left into the
// snapshot and a second command would start from that instead of this setup.
func TestRunSharedRestoresAStoreWithNoUsableCredential(t *testing.T) {
	shapes := []struct {
		name string
		// live returns the pre-child payload, or "" to delete the credential outright.
		live func(app *App) string
	}{
		{"absent", func(*App) string { return "" }},
		// Blank tokens: a fully-formed payload with nothing left to authenticate with,
		// which is what a failed refresh leaves in place.
		{"tombstone", func(app *App) string {
			now := app.Now()
			return fmt.Sprintf(
				`{"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":%d,"refreshTokenExpiresAt":%d}}`,
				now.Add(-2*time.Hour).UnixMilli(), now.Add(-time.Hour).UnixMilli(),
			)
		}},
		// A shape the parser does not recognize, which is what an upstream format change
		// looks like. It carries no deadline, so it must not read as "older than live".
		{"unparseable", func(*App) string { return `{"claudeAiOauth":{"unrecognized":true}}` }},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			app := testApp(t, nil)
			ctx := context.Background()
			opts := commonOpts{Format: formatText}
			credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

			captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 2*time.Hour))
			// main stays the recorded active account while its live credential is useless.
			if payload := shape.live(app); payload != "" {
				writeFile(t, credsPath, payload)
			} else if err := os.Remove(credsPath); err != nil {
				t.Fatal(err)
			}
			withInteractive(t, func(_ context.Context, _ []string, _ string, _ ...string) (int, error) {
				writeFile(t, credsPath, claudeOAuthPayload(refreshedToken, expiryIn(app, 3*time.Hour)))
				return 0, nil
			})
			code, _, stderr := captureBoth(t, func() int {
				return runRun(ctx, app, opts, runModeShared, "claude", "main", []string{"claude"})
			})
			if code != 0 {
				t.Fatalf("run failed: %d (%s)", code, stderr)
			}
			if !strings.Contains(stderr, "previous auth state restored") {
				t.Fatalf("the restore must be reported: %q", stderr)
			}
			// Positive assertion first would need a shape-specific expectation; what every
			// shape shares is that the child's token must be gone. The credential is a JSON
			// pointer, so restoring it as absent removes the key and leaves the file.
			live := readFile(t, credsPath)
			if strings.Contains(live, refreshedToken) {
				t.Fatalf("the child's login was left applied permanently: %s", live)
			}
			if shape.name == "absent" && strings.Contains(live, "claudeAiOauth") {
				t.Fatalf("the logged-out state was not restored: %s", live)
			}
		})
	}
}

// The backend-read arm of the same guard, which no command-level test can reach: a
// backend that errors rather than reporting the payload absent. Left unguarded, an
// errored read would hand the comparison a zero cutoff and skip the restore.
func TestBackupCredentialFreshnessRefusesAnErroringBackend(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 2*time.Hour))
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	meta, found, err := backup.Latest(app.Paths.BackupsDir())
	if err != nil || !found {
		t.Fatalf("no backup was taken: found=%v err=%v", found, err)
	}

	// Positive first: through a working backend the same backup reads as usable, so a
	// false below cannot come from the fixture being wrong.
	if _, usable := backupCredentialFreshness(ctx, testBackend(t, app), meta, constants.ToolClaude); !usable {
		t.Fatal("the backup must record a usable credential for this test to mean anything")
	}
	if _, usable := backupCredentialFreshness(ctx, erroringBackend{}, meta, constants.ToolClaude); usable {
		t.Fatal("a payload kae could not read is not a credential worth comparing")
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

	seedCodex(t, app, "codex-main-token")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "main") })
	mustExit(t, constants.ExitOK, code, out)
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "codex", "main") })
	mustExit(t, constants.ExitOK, code, out)
	// A codex credential whose expiry is later than the one the backup recorded. codex
	// is datable, so the comparison itself would succeed; only the tool gate stops it.
	writeSnapshotPayload(t, app, constants.ToolCodex, "main", codexAuthAt(app, 3*time.Hour))

	code, _, stderr := captureBoth(t, func() int { return runRollback(ctx, app, opts, "") })
	if code != constants.ExitOK {
		t.Fatalf("rollback failed: %d (%s)", code, stderr)
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
	if strings.Contains(stderr, "refresh token rotates single-use") {
		t.Fatalf("a rollback with nothing newer anywhere must be quiet: %q", stderr)
	}
}
