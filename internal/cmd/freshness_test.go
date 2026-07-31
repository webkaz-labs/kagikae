package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
)

// seedClaudeOAuth writes a claude credential with an explicit oauth object so
// freshness fields (expiresAt, refreshToken) can be exercised.
func seedClaudeOAuth(t *testing.T, app *App, oauthObject string) {
	t.Helper()
	writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"),
		`{"claudeAiOauth":`+oauthObject+`}`)
}

func claudeCreds(t *testing.T, app *App) string {
	t.Helper()
	return readFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"))
}

// §A: kae use A -> B -> A re-applies the token that was live when A was
// switched away (recaptured), not the original captured token.
func TestSwitchAwayRecapturesRefreshedToken(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main-uuid")
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
		t.Fatalf("capture main: %s", out)
	}
	seedClaude(t, app, sideToken, "side-uuid")
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "side") }); code != constants.ExitOK {
		t.Fatalf("capture side: %s", out)
	}

	// Switch to main, then simulate claude rotating its token in-tool.
	if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
		t.Fatalf("switch to main: %s", out)
	}
	const refreshed = "sk-ant-oat01-WORK-REFRESHED-cccc"
	seedClaude(t, app, refreshed, "main-uuid")

	// Switch away: §A must recapture main's live (refreshed) token first.
	if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "side") }); code != constants.ExitOK {
		t.Fatalf("switch to side: %s", out)
	}
	// Switch back: the refreshed token must come back, not the original.
	if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
		t.Fatalf("switch back to main: %s", out)
	}
	creds := claudeCreds(t, app)
	if !strings.Contains(creds, refreshed) {
		t.Fatalf("switch-back did not apply the recaptured token: %s", creds)
	}
	if strings.Contains(creds, mainToken) {
		t.Fatalf("stale original token re-applied: %s", creds)
	}
}

// §A: when the live store still matches the snapshot, a switch away leaves the
// snapshot untouched (the token round-trips unchanged through A->B->A).
func TestSwitchAwaySkipsRecaptureWhenMatching(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main-uuid")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	seedClaude(t, app, sideToken, "side-uuid")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "side") })

	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	// No in-tool change: live still equals main's snapshot.
	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "side") })
	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })

	creds := claudeCreds(t, app)
	if !strings.Contains(creds, mainToken) {
		t.Fatalf("matching round-trip corrupted the token: %s", creds)
	}
}

// §B: switching to an account whose snapshot is expired with no refresh token
// warns and names kae add, but still proceeds.
func TestSwitchToExpiredSnapshotWarns(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	// expiresAt 2020-01-01 (past app.Now of 2026), no refresh token.
	seedClaudeOAuth(t, app, `{"accessToken":"old","refreshToken":"","expiresAt":1577836800000}`)
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "stale") })
	// A fresh current account so the switch actually moves away from it.
	seedClaude(t, app, sideToken, "side-uuid")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "current") })
	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "current") })

	report, err := buildSwitch(ctx, app, opts, "claude", "stale")
	if err != nil {
		t.Fatalf("switch to stale must proceed, got error: %v", err)
	}
	warnings := strings.Join(report.Results[0].Warnings, " | ")
	if !strings.Contains(warnings, "expired") || !strings.Contains(warnings, "kae add") {
		t.Fatalf("expected stale warning naming kae add, got: %q", warnings)
	}
}

// The shared §B/§D predicate. The load-bearing rows are the last two: a refresh
// token that has itself expired, and a credential the tool tombstoned, both used
// to silence the warning completely.
func TestNeedsRelogin(t *testing.T) {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	cases := []struct {
		name string
		info freshness.Info
		want bool
	}{
		{"not datable", freshness.Info{}, false},
		{"still valid", freshness.Info{Known: true, ExpiresAt: future}, false},
		{"undated", freshness.Info{Known: true}, false},
		{"expired, refresh with no published expiry", freshness.Info{Known: true, ExpiresAt: past, HasRefresh: true}, false},
		{"expired, refresh still valid", freshness.Info{Known: true, ExpiresAt: past, HasRefresh: true, RefreshExpiresAt: future}, false},
		{"expired, no refresh", freshness.Info{Known: true, ExpiresAt: past}, true},
		{"expired, refresh expired too", freshness.Info{Known: true, ExpiresAt: past, HasRefresh: true, RefreshExpiresAt: past}, true},
		{"tombstoned by the tool", freshness.Info{Known: true, Revoked: true}, true},
		// Both sides of the decision must treat "exactly now" the same way, or a
		// credential expiring on the tick reads as usable while its refresh token
		// reads as dead.
		{"access expires exactly now, no refresh", freshness.Info{Known: true, ExpiresAt: now}, true},
		{"refresh expires exactly now", freshness.Info{Known: true, ExpiresAt: past, HasRefresh: true, RefreshExpiresAt: now}, true},
	}
	for _, c := range cases {
		if got := needsRelogin(c.info, now); got != c.want {
			t.Errorf("%s: needsRelogin = %v, want %v", c.name, got, c.want)
		}
	}
}

// §B: an expired refresh token no longer counts as a recovery path, and the
// warning must name the tool's own login flow — re-capturing a dead credential
// only writes it back into the snapshot.
func TestSwitchToExpiredRefreshTokenWarns(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	// access expired 2020, refresh token expired 2021 (app.Now is 2026).
	seedClaudeOAuth(t, app,
		`{"accessToken":"old","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":1609459200000}`)
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "stale") })
	seedClaude(t, app, sideToken, "side-uuid")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "current") })
	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "current") })

	report, err := buildSwitch(ctx, app, opts, "claude", "stale")
	if err != nil {
		t.Fatalf("switch to stale must proceed, got error: %v", err)
	}
	warnings := strings.Join(report.Results[0].Warnings, " | ")
	for _, want := range []string{"refresh token expired", "claude /login", "kae add --no-login claude stale"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("stale warning should contain %q, got: %q", want, warnings)
		}
	}
}

// §B: a credential the tool itself emptied (the tombstone a failed refresh
// leaves) reads as "no expiry recorded", so it used to be the one state kae never
// warned about.
func TestSwitchToTombstonedSnapshotWarns(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaudeOAuth(t, app, `{"accessToken":"","refreshToken":"","expiresAt":0}`)
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "dead") })
	seedClaude(t, app, sideToken, "side-uuid")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "current") })
	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "current") })

	report, err := buildSwitch(ctx, app, opts, "claude", "dead")
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(report.Results[0].Warnings, " | ")
	if !strings.Contains(warnings, "failed token refresh") || !strings.Contains(warnings, "claude /login") {
		t.Fatalf("expected a tombstone warning naming the login flow, got: %q", warnings)
	}
	if strings.Contains(warnings, "0001-01-01") {
		t.Fatalf("a tombstone has no meaningful expiry to print: %q", warnings)
	}
}

// §B: an expired snapshot that still carries a refresh token proceeds with no
// warning — the tool self-refreshes.
func TestSwitchToExpiredWithRefreshNoWarning(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaudeOAuth(t, app, `{"accessToken":"old","refreshToken":"r","expiresAt":1577836800000}`)
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "refreshable") })
	seedClaude(t, app, sideToken, "side-uuid")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "current") })
	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "current") })

	report, err := buildSwitch(ctx, app, opts, "claude", "refreshable")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range report.Results[0].Warnings {
		if strings.Contains(w, "expired") {
			t.Fatalf("refreshable account must not warn, got: %q", w)
		}
	}
}

// captureBoth runs fn with both streams captured, so a switch's stdout report and
// its stderr warnings can be asserted separately.
func captureBoth(t *testing.T, run func() int) (code int, stdout, stderr string) {
	t.Helper()
	_, stderr = captureStderr(t, func() int {
		code, stdout = captureStdout(t, run)
		return code
	})
	return code, stdout, stderr
}

// §A: `claude /login` outside kae rewrites accountUuid/emailAddress
// unconditionally, so afterwards the live identity names the *new* account while
// kae's active account is still the old one. Recapturing there would file the new
// credential under the old account's name and identity — and nothing offline can
// find that afterwards, because the access token is opaque.
func TestSwitchAwaySkipsRecaptureAfterOutsideLogin(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main-uuid")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	seedClaude(t, app, sideToken, "side-uuid")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "side") })
	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })

	// Someone ran the tool's own login flow and landed on the other account.
	const outside = "sk-ant-oat01-OUTSIDE-LOGIN-dddd"
	seedClaude(t, app, outside, "side-uuid")

	code, out, stderr := captureBoth(t, func() int { return runSwitch(ctx, app, opts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(stderr, "outside kae") {
		t.Errorf("expected a warning that the live login is not kae's: %q", stderr)
	}
	for _, pii := range []string{"side-uuid", "main-uuid", "@example.com", outside} {
		if strings.Contains(stderr, pii) {
			t.Errorf("identity/credential value %q must not reach stderr: %q", pii, stderr)
		}
	}

	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	creds := claudeCreds(t, app)
	if !strings.Contains(creds, mainToken) || strings.Contains(creds, outside) {
		t.Fatalf("an outside login was filed under the previously active account: %s", creds)
	}
}

// §A: a live credential can *exist* and still be unusable — claude writes a
// blanked payload over it when a refresh fails — so the logged-out guard passes
// and recapture would overwrite a snapshot that was still good, irrecoverably
// (the switch's backup reads the same dead live store).
func TestSwitchAwaySkipsRecaptureWhenLiveCredentialIsDead(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	// expiresAt 2027-01-15, well after app.Now (2026-06-11).
	seedClaudeOAuth(t, app, `{"accessToken":"main-live","refreshToken":"r","expiresAt":1800000000000}`)
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	seedClaudeOAuth(t, app, `{"accessToken":"side-live","refreshToken":"r","expiresAt":1800000000000}`)
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "side") })
	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })

	// A refresh failed: claude tombstoned the live credential in place.
	seedClaudeOAuth(t, app, `{"accessToken":"","refreshToken":"","expiresAt":0}`)

	code, out, stderr := captureBoth(t, func() int { return runSwitch(ctx, app, opts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(stderr, "needs a re-login") {
		t.Errorf("expected a warning that the live credential is dead: %q", stderr)
	}

	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	creds := claudeCreds(t, app)
	if !strings.Contains(creds, "main-live") {
		t.Fatalf("a dead live credential overwrote a usable snapshot: %s", creds)
	}
}

// §B delivery: the warning has to arrive on stderr and before the apply. On
// stdout after the fact it vanished through a pipe and, under bare
// `kae use --quiet`, was never printed at all.
func TestStaleWarningGoesToStderrNotStdout(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaudeOAuth(t, app, `{"accessToken":"old","refreshToken":"","expiresAt":1577836800000}`)
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "stale") })
	seedClaude(t, app, sideToken, "side-uuid")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "current") })
	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "current") })

	code, stdout, stderr := captureBoth(t, func() int { return runSwitch(ctx, app, opts, "claude", "stale") })
	mustExit(t, constants.ExitOK, code, stdout)
	if !strings.Contains(stderr, "kae: warning: claude: snapshot credential is stale") {
		t.Errorf("stale warning missing from stderr: %q", stderr)
	}
	if strings.Contains(stdout, "warning") {
		t.Errorf("the warning must not also ride the stdout report: %q", stdout)
	}
	if !strings.Contains(stdout, "Switched claude -> stale") {
		t.Errorf("success report lost: %q", stdout)
	}
}

// §B delivery: --quiet suppresses the success report, never a warning — and this
// is the form `kae mise init --auto` puts in the enter hook, so it was the case
// where the warning was completely invisible. The exit code stays 0.
func TestQuietBareUseStillWarns(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	app.Config.Profiles = map[string]config.Profile{
		"stale":   {Accounts: map[string]string{constants.ToolClaude: "stale"}},
		"current": {Accounts: map[string]string{constants.ToolClaude: "current"}},
	}

	seedClaudeOAuth(t, app, `{"accessToken":"old","refreshToken":"","expiresAt":1577836800000}`)
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "stale") })
	seedClaude(t, app, sideToken, "side-uuid")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "current") })
	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "current") })

	code, stdout, stderr := captureBoth(t, func() int {
		return runUseBare(ctx, app, opts, false, "stale", true)
	})
	mustExit(t, constants.ExitOK, code, stdout)
	if stdout != "" {
		t.Errorf("--quiet must stay silent on stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "snapshot credential is stale") {
		t.Errorf("--quiet dropped the warning: %q", stderr)
	}
}

// §B delivery: a profile switch fans out over several tools, so the per-tool
// lines close with one roll-up naming them.
func TestWarnBeforeApplyRollsUpMultipleTools(t *testing.T) {
	results := []switchResult{
		{Tool: "claude", Warnings: []string{"snapshot credential is stale: reason"}},
		{Tool: "codex", Warnings: []string{"snapshot credential is stale: reason"}},
	}
	_, stderr := captureStderr(t, func() int {
		warnBeforeApply(results, []string{"claude", "codex"})
		return 0
	})
	for _, want := range []string{
		"kae: warning: claude: snapshot credential is stale",
		"kae: warning: codex: snapshot credential is stale",
		"2 tools need a re-login before use: claude, codex",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in:\n%s", want, stderr)
		}
	}

	// One stale tool already said it once; no roll-up.
	_, single := captureStderr(t, func() int {
		warnBeforeApply(results[:1], []string{"claude"})
		return 0
	})
	if strings.Contains(single, "need a re-login before use") {
		t.Errorf("single-tool switch must not repeat itself: %q", single)
	}
}

// The lead-time band: reloginDueWithin must fire only while the deadline is
// still ahead, and only for a deadline kae can actually know. The load-bearing
// rows are the two "unknown" ones — a refresh token whose expiry the payload
// does not publish (codex, opencode), and a not-datable payload — where a zero
// RefreshExpiresAt read as "never expires" would put every such account
// permanently in the band or permanently out of it by accident.
func TestReloginDueWithin(t *testing.T) {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)
	const lead = 7 * 24 * time.Hour
	in := now.Add(5 * 24 * time.Hour)   // inside the band
	out := now.Add(30 * 24 * time.Hour) // beyond it
	edge := now.Add(lead)               // exactly on the boundary
	cases := []struct {
		name string
		info freshness.Info
		want bool
	}{
		{"not datable", freshness.Info{}, false},
		{"no expiry recorded", freshness.Info{Known: true}, false},
		{"no refresh, expiring inside the band", freshness.Info{Known: true, ExpiresAt: in}, true},
		{"no refresh, expiring beyond the band", freshness.Info{Known: true, ExpiresAt: out}, false},
		{"no refresh, exactly on the boundary", freshness.Info{Known: true, ExpiresAt: edge}, true},
		{"already past the deadline", freshness.Info{Known: true, ExpiresAt: now.Add(-time.Hour)}, false},
		{"deadline exactly now", freshness.Info{Known: true, ExpiresAt: now}, false},
		{"tombstoned", freshness.Info{Known: true, Revoked: true}, false},
		// The deadline is the refresh token's, not the access token's: an access
		// token expiring in an hour is the normal state of a healthy credential.
		{"access expires soon, refresh far away", freshness.Info{
			Known: true, ExpiresAt: now.Add(time.Hour), HasRefresh: true, RefreshExpiresAt: out,
		}, false},
		{"access expired, refresh inside the band", freshness.Info{
			Known: true, ExpiresAt: now.Add(-time.Hour), HasRefresh: true, RefreshExpiresAt: in,
		}, true},
		// A refresh token with no published expiry: unknowable, so silent. Reading
		// the zero as "far future" or as "expired" would both be inventions.
		{"refresh with no published expiry, access soon", freshness.Info{
			Known: true, ExpiresAt: in, HasRefresh: true,
		}, false},
		{"refresh with no published expiry, access past", freshness.Info{
			Known: true, ExpiresAt: now.Add(-time.Hour), HasRefresh: true,
		}, false},
	}
	for _, c := range cases {
		deadline, got := reloginDueWithin(c.info, now, lead)
		if got != c.want {
			t.Errorf("%s: reloginDueWithin ok = %v, want %v", c.name, got, c.want)
		}
		if got && deadline.IsZero() {
			t.Errorf("%s: a reported deadline must carry its timestamp", c.name)
		}
	}
	// The two predicates read one deadline, so they can never both be true.
	for _, c := range cases {
		if _, soon := reloginDueWithin(c.info, now, lead); soon && needsRelogin(c.info, now) {
			t.Errorf("%s: expiring and stale must be mutually exclusive", c.name)
		}
	}
}

// §B lead time: switching to an account whose re-login deadline is days away must
// say so, name `kae add --restore` (the one command that refreshes it without
// disturbing the login that is live now), and — unlike the stale warning — must
// not be counted among the tools that "need a re-login before use", because this
// switch works today.
func TestSwitchToExpiringSnapshotWarnsWithLeadTime(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	// Access token long gone, refresh token good for 3 more days (app.Now is
	// 2026-06-11T01:23:45Z).
	refreshExp := app.Now().Add(3 * 24 * time.Hour).UnixMilli()
	seedClaudeOAuth(t, app, fmt.Sprintf(
		`{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":%d}`, refreshExp,
	))
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "soon") })
	seedClaude(t, app, sideToken, "side-uuid")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "current") })
	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "current") })

	code, stdout, stderr := captureBoth(t, func() int { return runSwitch(ctx, app, opts, "claude", "soon") })
	mustExit(t, constants.ExitOK, code, stdout)
	if !strings.Contains(stderr, "needs an interactive re-login in 3 day(s)") {
		t.Errorf("lead-time notice missing or not counting days: %q", stderr)
	}
	if !strings.Contains(stderr, "kae add --restore claude soon") {
		t.Errorf("lead-time notice must name the proactive refresh command: %q", stderr)
	}
	if strings.Contains(stderr, "is stale") {
		t.Errorf("a credential that still works must not be called stale: %q", stderr)
	}
	if strings.Contains(stderr, "need a re-login before use") {
		t.Errorf("an expiring account must stay out of the stale roll-up: %q", stderr)
	}
}

// The band has an outer edge: a credential with a month left must stay silent, or
// the notice is on for most of every credential's life and stops being read.
func TestSwitchToHealthySnapshotHasNoLeadTimeNotice(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	refreshExp := app.Now().Add(30 * 24 * time.Hour).UnixMilli()
	seedClaudeOAuth(t, app, fmt.Sprintf(
		`{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":%d}`, refreshExp,
	))
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "healthy") })
	seedClaude(t, app, sideToken, "side-uuid")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "current") })
	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "current") })

	_, _, stderr := captureBoth(t, func() int { return runSwitch(ctx, app, opts, "claude", "healthy") })
	if strings.Contains(stderr, "re-login") {
		t.Errorf("a credential with a month left must be silent: %q", stderr)
	}
}
