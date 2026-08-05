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
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
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
		// The a-side guard, in all three forms it can degrade in. A tombstone is a
		// fully-formed payload, so presence proves nothing.
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

// A backup that recorded a logged-out store is restored as logged out, even though
// the child's login is strictly "newer": the restore's job there is to take the
// credential away again, and keeping it would leave run -s having applied an account
// permanently.
func TestRunSharedRestoresALoggedOutStore(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	captureClaudeAt(t, app, "main", mainToken, expiryIn(app, 2*time.Hour))
	// main stays the recorded active account while its live credential is gone —
	// deleted by hand, or by the tool logging out.
	if err := os.Remove(credsPath); err != nil {
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
	// The credential is a JSON pointer, so restoring it as absent removes the key and
	// leaves the file — which is why this asserts on the key rather than the file.
	live := readFile(t, credsPath)
	if strings.Contains(live, "claudeAiOauth") || strings.Contains(live, refreshedToken) {
		t.Fatalf("the logged-out state was not restored: %s", live)
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

	captureClaudeAt(t, app, "side", sideToken, expiryIn(app, 2*time.Hour))
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
