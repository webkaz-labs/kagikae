package cmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
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
