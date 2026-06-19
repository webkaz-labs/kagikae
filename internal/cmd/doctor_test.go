package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// findCheck returns the first check with the given code, or false.
func findCheck(report *doctorReport, code string) (string, bool) {
	for _, c := range report.Checks {
		if c.Code == code {
			return c.Message, true
		}
	}
	return "", false
}

// §D: an expired snapshot with no refresh token produces a credential_stale
// warn-level check; the report keeps schema_version 1.
func TestDoctorReportsStaleSnapshot(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaudeOAuth(t, app, `{"accessToken":"old","refreshToken":"","expiresAt":1577836800000}`)
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "stale") })

	report := buildDoctor(ctx, app, "claude")
	if report.SchemaVersion != constants.SchemaVersion {
		t.Fatalf("schema_version changed: %d", report.SchemaVersion)
	}
	msg, ok := findCheck(report, constants.CheckCredentialStale)
	if !ok {
		t.Fatalf("expected a credential_stale check, got %+v", report.Checks)
	}
	if !strings.Contains(msg, "stale") || !strings.Contains(msg, "kae add") {
		t.Fatalf("stale message should name the account and kae add: %q", msg)
	}
	for _, c := range report.Checks {
		if c.Code == constants.CheckCredentialStale && c.Status != constants.StatusWarn {
			t.Fatalf("credential_stale must be warn-level, got %q", c.Status)
		}
	}
}

// §D: an expired snapshot that still carries a refresh token is not flagged.
func TestDoctorIgnoresRefreshableSnapshot(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaudeOAuth(t, app, `{"accessToken":"old","refreshToken":"r","expiresAt":1577836800000}`)
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "refreshable") })

	report := buildDoctor(ctx, app, "claude")
	if _, ok := findCheck(report, constants.CheckCredentialStale); ok {
		t.Fatal("refreshable snapshot must not be flagged stale")
	}
}

// §D: a stored secret item with no snapshot dir is reported as an orphan
// (file backend enumerates).
func TestDoctorReportsSecretOrphan(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()

	be, err := app.secretBackend()
	if err != nil {
		t.Fatal(err)
	}
	if err := be.Set(ctx, "claude/ghost/claude_ai_oauth", []byte("orphaned")); err != nil {
		t.Fatal(err)
	}
	report := buildDoctor(ctx, app, "claude")
	msg, ok := findCheck(report, constants.CheckSecretOrphan)
	if !ok {
		t.Fatalf("expected a secret_orphan check, got %+v", report.Checks)
	}
	if !strings.Contains(msg, "ghost") || !strings.Contains(msg, "kae account rm") {
		t.Fatalf("orphan message should name the account and kae account rm: %q", msg)
	}
}
