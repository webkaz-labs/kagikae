package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/envprofile"
	"github.com/webkaz-labs/kagikae/internal/secret"
	"github.com/webkaz-labs/kagikae/internal/state"
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

	// No refresh token, so the access-token expiry is the login deadline itself
	// (cursor's shape). A refresh-backed one is covered by
	// TestRefreshBackedLoginDeadlineInTheBandWarns.
	seedClaudeOAuth(t, app, endOfLifeClaudeCred(app.Now(), 4*24*time.Hour, "a"))
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

	seedClaudeOAuth(t, app, refreshBackedClaudeCred(app.Now(), 30*24*time.Hour))
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
		name, oauth, wantCode string
	}{
		// No refresh token, so the access-token expiry is the deadline and this really
		// does classify as expiring. With a refresh token it would read ok, and this
		// subtest would silently stop covering the path it is named for — which is
		// exactly what happened once, so wantCode below is asserted rather than assumed.
		{
			"expiring", `{"accessToken":"` + secretToken + `","refreshToken":"","expiresAt":%d}`,
			constants.CheckCredentialExpiring,
		},
		{
			"stale", `{"accessToken":"` + secretToken + `","refreshToken":"` + secretRefresh +
				`","expiresAt":1577836800000,"refreshTokenExpiresAt":1577836800000}`,
			constants.CheckCredentialStale,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testApp(t, nil)
			payload := tc.oauth
			if strings.Contains(payload, "%d") {
				payload = fmt.Sprintf(payload, app.Now().Add(2*24*time.Hour).UnixMilli())
			}
			seedClaudeOAuth(t, app, payload)
			captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "canary") })

			// The fixture must actually reach the state this subtest is named for, or
			// the redaction assertions below prove nothing about that path.
			if _, ok := findCheck(buildDoctor(ctx, app, "claude", false), tc.wantCode); !ok {
				t.Fatalf("fixture no longer produces %s; this canary would pass vacuously", tc.wantCode)
			}

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

// state.json naming an account that has no snapshot: kae believes it applied
// something that does not exist. Two causes (see activeOrphanChecks) — an
// interrupted `kae account rename`, and a writer outside kae. This test covers the
// second, which is what a smoke run with an un-isolated XDG_STATE_HOME did on
// 2026-07-31, while doctor reported no problems and `kae status` displayed the
// phantom account.
func TestDoctorReportsAnActiveAccountWithNoSnapshot(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main-uuid")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })

	// Write the state a foreign writer would leave behind.
	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	st.Active[constants.ToolClaude] = "ghost"
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}

	report := buildDoctor(ctx, app, "", false)
	msg, ok := findCheck(report, constants.CheckActiveOrphan)
	if !ok {
		t.Fatalf("a dangling active account must be reported, got %+v", report.Checks)
	}
	for _, want := range []string{"claude/ghost", "kae use claude"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %q", want, msg)
		}
	}
	for _, c := range report.Checks {
		if c.Code == constants.CheckActiveOrphan && c.Status != constants.StatusWarn {
			t.Fatalf("active_orphan must be warn-level, got %q", c.Status)
		}
	}
	// A real active account is silent, and so is a tool with none recorded.
	st.Active[constants.ToolClaude] = "main"
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}
	if _, ok := findCheck(buildDoctor(ctx, app, "", false), constants.CheckActiveOrphan); ok {
		t.Fatal("a captured active account must not be reported")
	}
	delete(st.Active, constants.ToolClaude)
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}
	if _, ok := findCheck(buildDoctor(ctx, app, "", false), constants.CheckActiveOrphan); ok {
		t.Fatal("no recorded active account is not a finding")
	}
}

// The check needs no secret backend, and an unavailable one is exactly when a user
// is diagnosing — so it must not be wired in behind the backend gate that skips the
// credential-health checks. It was, in its first draft.
func TestActiveOrphanIsReportedWithNoSecretBackend(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main-uuid")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	st.Active[constants.ToolClaude] = "ghost"
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}

	// keychain on linux is unavailable, so secretBackend() fails and every
	// backend-dependent check is skipped.
	app.Config.Security.SecretBackend = secret.BackendKeychain
	if _, err := app.secretBackend(); err == nil {
		t.Fatal("this test needs an unavailable backend to be meaningful")
	}

	report := buildDoctor(ctx, app, "", false)
	if _, ok := findCheck(report, constants.CheckActiveOrphan); !ok {
		t.Fatalf("active_orphan must survive an unavailable secret backend, got %+v", report.Checks)
	}
	// And the backend itself is still reported, so the two findings coexist.
	if _, ok := findCheck(report, constants.CheckSecretBackend); !ok {
		t.Fatal("the backend failure must still be reported")
	}
}

// The two ways kae can fail to *read* what it needs, both of which returned
// silently in the first draft — an unreadable state file with no other check
// covering it, and an active account whose own metadata will not parse.
func TestActiveOrphanReportsUnreadableStateAndSnapshot(t *testing.T) {
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	t.Run("unreadable state file", func(t *testing.T) {
		app := testApp(t, nil)
		// Invalid JSON: state.Load surfaces a parse error, which nothing else in
		// doctor looks at (config_valid reflects config.toml only).
		writeFile(t, app.Paths.StateFile(), "{not json")
		msg, ok := findCheck(buildDoctor(ctx, app, "", false), constants.CheckActiveOrphan)
		if !ok {
			t.Fatal("an unreadable state.json must be reported by something")
		}
		if !strings.Contains(msg, "could not read") {
			t.Fatalf("message should say what failed: %q", msg)
		}
	})

	t.Run("unreadable active snapshot", func(t *testing.T) {
		app := testApp(t, nil)
		seedClaude(t, app, mainToken, "main-uuid")
		captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
		// The snapshot dir exists and is named by state, but its metadata is corrupt:
		// "found" is false and the error is real, which must not read as "fine".
		writeFile(t, filepath.Join(app.Paths.AccountDir(constants.ToolClaude, "main"), "account.toml"),
			"this is not toml = = =")
		msg, ok := findCheck(buildDoctor(ctx, app, "", false), constants.CheckActiveOrphan)
		if !ok {
			t.Fatal("an unreadable active snapshot must be reported, not skipped")
		}
		if !strings.Contains(msg, "claude/main") || !strings.Contains(msg, "could not be read") {
			t.Fatalf("message should name the account and the failure: %q", msg)
		}
	})
}
