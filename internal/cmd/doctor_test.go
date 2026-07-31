package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/envprofile"
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

	report := buildDoctor(ctx, app, "claude", false)
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

// §D: the credential Claude Code tombstones after a failed refresh (blank tokens,
// expiresAt 0) reads literally as "no expiry recorded" — kae's most harmless
// state — so it used to be the one snapshot doctor stayed silent about.
func TestDoctorReportsTombstonedSnapshot(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaudeOAuth(t, app, `{"accessToken":"","refreshToken":"","expiresAt":0}`)
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "dead") })

	msg, ok := findCheck(buildDoctor(ctx, app, "claude", false), constants.CheckCredentialStale)
	if !ok {
		t.Fatal("a tombstoned snapshot must be reported stale")
	}
	if !strings.Contains(msg, "failed token refresh") || !strings.Contains(msg, "claude /login") {
		t.Fatalf("stale message should explain the tombstone and name the login flow: %q", msg)
	}
}

// §D: an expired snapshot that still carries a refresh token is not flagged.
func TestDoctorIgnoresRefreshableSnapshot(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaudeOAuth(t, app, `{"accessToken":"old","refreshToken":"r","expiresAt":1577836800000}`)
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "refreshable") })

	report := buildDoctor(ctx, app, "claude", false)
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
	report := buildDoctor(ctx, app, "claude", false)
	msg, ok := findCheck(report, constants.CheckSecretOrphan)
	if !ok {
		t.Fatalf("expected a secret_orphan check, got %+v", report.Checks)
	}
	if !strings.Contains(msg, "ghost") || !strings.Contains(msg, "kae account rm") {
		t.Fatalf("orphan message should name the account and kae account rm: %q", msg)
	}
}

// §D: keys of the prefixed namespaces have no snapshot dir by design, so they
// are not orphans. Reading them as <tool>/<account> warned forever on every
// companion binding and env-profile variable, with a remediation
// (`kae account rm companion <profile>`) naming a tool that does not exist.
func TestDoctorIgnoresNonAccountSecretNamespaces(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()

	be, err := app.secretBackend()
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{
		companion.SecretRef("main", "git", "email"),
		envprofile.SecretRef("claude", "main", "API_KEY"),
		backup.SecretRef("20260101T000000Z", "claude", "claude_ai_oauth"),
	}
	for _, key := range keys {
		if err := be.Set(ctx, key, []byte("payload")); err != nil {
			t.Fatal(err)
		}
	}
	report := buildDoctor(ctx, app, "", false)
	if msg, ok := findCheck(report, constants.CheckSecretOrphan); ok {
		t.Fatalf("no namespace but the account one can be orphaned: %q", msg)
	}
}

// §D lead time: a snapshot that still works but whose re-login deadline is inside
// the lead window is reported under its own code, at warn level, naming the
// proactive refresh. credential_stale must stay silent — a consumer filtering on
// that code to find broken accounts must not start matching healthy ones.
func TestDoctorReportsExpiringSnapshot(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	// No refresh token, so the access-token expiry is the whole deadline — the shape a
	// lead-time notice is for (cursor's). A refresh-backed deadline is a shelf life
	// that renews, so it is silent by design (leadTimeApplies).
	seedClaudeOAuth(t, app, fmt.Sprintf(
		`{"accessToken":"a","refreshToken":"","expiresAt":%d}`, app.Now().Add(4*24*time.Hour).UnixMilli(),
	))
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "soon") })

	report := buildDoctor(ctx, app, "claude", false)
	if report.SchemaVersion != constants.SchemaVersion {
		t.Fatalf("schema_version changed: %d", report.SchemaVersion)
	}
	msg, ok := findCheck(report, constants.CheckCredentialExpiring)
	if !ok {
		t.Fatalf("expected a credential_expiring check, got %+v", report.Checks)
	}
	if !strings.Contains(msg, "4 day(s)") || !strings.Contains(msg, "kae add --restore claude soon") {
		t.Fatalf("expiring message should name the lead time and the refresh command: %q", msg)
	}
	if _, ok := findCheck(report, constants.CheckCredentialStale); ok {
		t.Fatal("a credential that still works must not also be reported stale")
	}
	for _, c := range report.Checks {
		if c.Code == constants.CheckCredentialExpiring && c.Status != constants.StatusWarn {
			t.Fatalf("credential_expiring must be warn-level, got %q", c.Status)
		}
	}
	// A doctor whose only findings are warnings still exits 0.
	if !report.OK {
		t.Fatal("a warn-level check must not fail the report")
	}
}

// A snapshot with a month left is not the lead window's business.
func TestDoctorIgnoresHealthySnapshot(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	refreshExp := app.Now().Add(30 * 24 * time.Hour).UnixMilli()
	seedClaudeOAuth(t, app, fmt.Sprintf(
		`{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":%d}`, refreshExp,
	))
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "healthy") })

	if _, ok := findCheck(buildDoctor(ctx, app, "claude", false), constants.CheckCredentialExpiring); ok {
		t.Fatal("a credential with a month left must not be reported as expiring")
	}
}

// Both freshness messages are built from a parsed credential, so both are new
// paths for a token to reach stdout, --json and stderr. AGENTS.md requires a
// redaction test for every new output path; the last one found a real leak.
func TestCredentialFreshnessMessagesNeverCarryTheToken(t *testing.T) {
	const secretToken = "sk-ant-oat01-LEAK-CANARY-ffff"
	const secretRefresh = "refresh-LEAK-CANARY-gggg"
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	for _, tc := range []struct {
		name, oauth string
	}{
		{"expiring", `{"accessToken":"` + secretToken + `","refreshToken":"` + secretRefresh +
			`","expiresAt":1577836800000,"refreshTokenExpiresAt":%d}`},
		{"stale", `{"accessToken":"` + secretToken + `","refreshToken":"` + secretRefresh +
			`","expiresAt":1577836800000,"refreshTokenExpiresAt":1577836800000}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testApp(t, nil)
			payload := tc.oauth
			if strings.Contains(payload, "%d") {
				payload = fmt.Sprintf(payload, app.Now().Add(2*24*time.Hour).UnixMilli())
			}
			seedClaudeOAuth(t, app, payload)
			captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "canary") })

			// The human report and the JSON contract, both streams.
			for _, format := range []string{formatText, formatJSON} {
				_, stdout, stderr := captureBoth(t, func() int {
					return runDoctor(ctx, app, commonOpts{Format: format}, "claude")
				})
				for stream, out := range map[string]string{"stdout": stdout, "stderr": stderr} {
					if strings.Contains(out, secretToken) || strings.Contains(out, secretRefresh) {
						t.Fatalf("%s (%s) leaked a credential value: %q", stream, format, out)
					}
				}
			}
			// And the switch-time warning, which is the other consumer.
			seedClaude(t, app, sideToken, "side-uuid")
			captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "current") })
			captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "current") })
			_, stdout, stderr := captureBoth(t, func() int { return runSwitch(ctx, app, opts, "claude", "canary") })
			if strings.Contains(stdout+stderr, secretToken) || strings.Contains(stdout+stderr, secretRefresh) {
				t.Fatalf("the switch-time warning leaked a credential value: %q / %q", stdout, stderr)
			}
		})
	}
}
