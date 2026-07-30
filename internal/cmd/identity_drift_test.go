package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/state"
)

// identityDriftApp captures claude/main from a seeded login (credential plus the
// /oauthAccount identity cache) and leaves it active, so any later rewrite of the
// live ~/.claude.json is drift against what kae applied.
func identityDriftApp(t *testing.T) *App {
	t.Helper()
	app := testApp(t, nil)
	seedClaude(t, app, mainToken, "main-uuid")
	code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main")
	})
	mustExit(t, constants.ExitOK, code, out)
	return app
}

// claudeJSON overwrites the live mixed-state identity file.
func claudeJSON(t *testing.T, app *App, content string) {
	t.Helper()
	writeFile(t, filepath.Join(app.Env.Home, ".claude.json"), content)
}

func TestDoctorIdentityDriftAbsentWhenLiveMatches(t *testing.T) {
	app := identityDriftApp(t)
	report := buildDoctor(context.Background(), app, "", false)
	if msg, ok := findCheck(report, constants.CheckIdentityDrift); ok {
		t.Fatalf("live identity matches the applied snapshot; unexpected drift: %q", msg)
	}
}

// The motivating failure: the live identity names a different account than the
// credential kae applied. The message must be actionable without leaking the
// identity itself (PII on both sides of the comparison).
func TestDoctorIdentityDriftDetected(t *testing.T) {
	app := identityDriftApp(t)
	claudeJSON(t, app,
		`{"oauthAccount":{"accountUuid":"side-uuid","emailAddress":"side@example.com"},"projects":{}}`)

	report := buildDoctor(context.Background(), app, "", false)
	msg, ok := findCheck(report, constants.CheckIdentityDrift)
	if !ok {
		t.Fatalf("expected identity_drift when the live identity was rewritten: %+v", report.Checks)
	}
	for _, want := range []string{"main", "oauth_account", "kae use claude main", "VALIDATION.md"} {
		if !strings.Contains(msg, want) {
			t.Errorf("drift message should name %q: %q", want, msg)
		}
	}
	for _, secretish := range []string{"side@example.com", "side-uuid", "main-uuid"} {
		if strings.Contains(msg, secretish) {
			t.Errorf("identity payload %q must never reach doctor output: %q", secretish, msg)
		}
	}
	for _, c := range report.Checks {
		if c.Code == constants.CheckIdentityDrift {
			if c.Status != constants.StatusWarn {
				t.Errorf("identity_drift must be warn-level, got %q", c.Status)
			}
			if c.Tool != constants.ToolClaude {
				t.Errorf("identity_drift must name the tool, got %q", c.Tool)
			}
		}
	}
}

// The live identity vanished (the tool cleared it, or a reset). Stated as a
// possibly-transient absence, not as a wrong account.
func TestDoctorIdentityDriftLiveAbsent(t *testing.T) {
	app := identityDriftApp(t)
	claudeJSON(t, app, `{"projects":{}}`)

	report := buildDoctor(context.Background(), app, "", false)
	msg, ok := findCheck(report, constants.CheckIdentityDrift)
	if !ok {
		t.Fatalf("expected identity_drift when the live identity disappeared: %+v", report.Checks)
	}
	if !strings.Contains(msg, "gone") || !strings.Contains(msg, "rebuild") {
		t.Errorf("absent message should read as possibly-transient, not a wrong account: %q", msg)
	}
}

// A snapshot captured while the tool had no identity records it absent. kae never
// applied one, so the live value that appears later is not drift.
func TestDoctorIdentityDriftReportsUntrackedSnapshot(t *testing.T) {
	app := testApp(t, nil)
	seedClaudeOAuth(t, app, `{"accessToken":"`+mainToken+`"}`)
	claudeJSON(t, app, `{"projects":{}}`)
	code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main")
	})
	mustExit(t, constants.ExitOK, code, out)

	claudeJSON(t, app, `{"oauthAccount":{"emailAddress":"you@example.com"}}`)
	report := buildDoctor(context.Background(), app, "", false)
	msg, ok := findCheck(report, constants.CheckIdentityDrift)
	if !ok {
		t.Fatal("an untracked identity must be reported, not silently skipped")
	}
	// Not a warning: the display still ends up right (the tool refetches), so this
	// must not read as a problem. It says that, then names the one-time step.
	for _, c := range report.Checks {
		if c.Code == constants.CheckIdentityDrift && c.Status != constants.StatusOK {
			t.Fatalf("an untracked identity must be status ok, got %q", c.Status)
		}
	}
	for _, want := range []string{"no oauth_account identity recorded yet", "refetches it", "kae add --no-login claude main"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "you@example.com") {
		t.Fatalf("an identity is PII and must not reach the output: %s", msg)
	}
}

// Inside a kae-owned isolated home the live identity was never applied by kae
// (the per-directory materializers copy only the credential — the ROADMAP
// attribution gap) and state.Active names the global account, so comparing the
// two would warn in every pinned directory.
func TestDoctorIdentityDriftSkipsInsideKaeIsolation(t *testing.T) {
	app := identityDriftApp(t)
	isolated := app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "main")
	app.Env.Getenv = func(key string) string {
		if key == "CLAUDE_CONFIG_DIR" {
			return isolated
		}
		return ""
	}
	// The isolated home carries a different identity than the global snapshot.
	writeFile(t, filepath.Join(isolated, ".claude.json"),
		`{"oauthAccount":{"emailAddress":"side@example.com"}}`)

	report := buildDoctor(context.Background(), app, "", false)
	if msg, ok := findCheck(report, constants.CheckIdentityDrift); ok {
		t.Fatalf("a kae-owned isolated home must not be compared to the global snapshot: %q", msg)
	}
}

func TestDoctorIdentityDriftSkipsWithoutActiveAccount(t *testing.T) {
	app := identityDriftApp(t)
	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	delete(st.Active, constants.ToolClaude)
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}
	claudeJSON(t, app, `{"oauthAccount":{"emailAddress":"side@example.com"}}`)

	report := buildDoctor(context.Background(), app, "", false)
	if msg, ok := findCheck(report, constants.CheckIdentityDrift); ok {
		t.Fatalf("with no active account kae applied nothing to compare against: %q", msg)
	}
}

// seedClaudeIdentity writes the live mixed-state file with a full /oauthAccount:
// the identifying keys plus the bookkeeping claude renews on a profile refetch.
func seedClaudeIdentity(t *testing.T, app *App, accountUUID, email string, fetchedAt int64, plan string) {
	t.Helper()
	writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"),
		`{"claudeAiOauth":{"accessToken":"`+mainToken+`","subscriptionType":"max"}}`)
	claudeJSON(t, app, fmt.Sprintf(
		`{"oauthAccount":{"accountUuid":%q,"emailAddress":%q,"organizationUuid":"org-1",`+
			`"profileFetchedAt":%d,"organizationRole":%q},"projects":{}}`,
		accountUUID, email, fetchedAt, plan,
	))
}

// The regression this check nearly caused itself: claude's identity self-heal is
// TTL-gated, so once the applied (capture-time) profileFetchedAt is over 24h old
// it refetches and rewrites that timestamp and the plan fields. Comparing the
// whole payload therefore warned about a *correctly* switched account a day
// later, and its remedy — re-apply the same bytes — could not fix it.
func TestDoctorIdentityDriftIgnoresProfileRefetch(t *testing.T) {
	app := testApp(t, nil)
	seedClaudeIdentity(t, app, "main-uuid", "you@example.com", 1749000000000, "admin")
	code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main")
	})
	mustExit(t, constants.ExitOK, code, out)

	// Same account; claude refetched the profile and rewrote its bookkeeping.
	seedClaudeIdentity(t, app, "main-uuid", "you@example.com", 1750000000000, "member")

	report := buildDoctor(context.Background(), app, "", false)
	if msg, ok := findCheck(report, constants.CheckIdentityDrift); ok {
		t.Fatalf("a profile refetch of the same account is not drift: %q", msg)
	}
}

// The identifying keys still drift: the account uuid alone changing is a
// different account, even with the email untouched.
func TestDoctorIdentityDriftDetectsAccountUUIDOnly(t *testing.T) {
	app := testApp(t, nil)
	seedClaudeIdentity(t, app, "main-uuid", "you@example.com", 1749000000000, "admin")
	code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main")
	})
	mustExit(t, constants.ExitOK, code, out)

	seedClaudeIdentity(t, app, "other-uuid", "you@example.com", 1749000000000, "admin")

	report := buildDoctor(context.Background(), app, "", false)
	if _, ok := findCheck(report, constants.CheckIdentityDrift); !ok {
		t.Fatalf("a changed accountUuid must still drift: %+v", report.Checks)
	}
}

// identityDiffers keys the comparison; anything it cannot key on falls back to the
// strict byte comparison rather than being declared equal.
func TestIdentityDiffers(t *testing.T) {
	keyed := artifact.Spec{IdentityKeys: []string{"accountUuid", "emailAddress"}}
	cases := []struct {
		name          string
		sp            artifact.Spec
		stored, live  string
		wantDifferent bool
	}{
		{
			"volatile field only", keyed,
			`{"accountUuid":"a","emailAddress":"you@example.com","profileFetchedAt":1}`,
			`{"accountUuid":"a","emailAddress":"you@example.com","profileFetchedAt":2}`, false,
		},
		{
			"email differs", keyed,
			`{"accountUuid":"a","emailAddress":"you@example.com"}`,
			`{"accountUuid":"a","emailAddress":"other@example.com"}`, true,
		},
		{
			"identity key dropped live", keyed,
			`{"accountUuid":"a","emailAddress":"you@example.com"}`,
			`{"accountUuid":"a"}`, true,
		},
		{
			"key order and spacing", keyed,
			`{"accountUuid":"a","emailAddress":"you@example.com"}`,
			`{"emailAddress":"you@example.com", "accountUuid":"a"}`, false,
		},
		{
			"no keys declared: byte comparison",
			artifact.Spec{},
			`{"accountUuid":"a","profileFetchedAt":1}`,
			`{"accountUuid":"a","profileFetchedAt":2}`, true,
		},
		{
			"live not an object: byte comparison", keyed,
			`{"accountUuid":"a"}`, `"a"`, true,
		},
		{"both unparseable but identical", keyed, `nonsense`, `nonsense`, false},
		{"both unparseable and different", keyed, `nonsense`, `other`, true},
	}
	for _, c := range cases {
		if got := identityDiffers(c.sp, []byte(c.stored), []byte(c.live)); got != c.wantDifferent {
			t.Errorf("%s: identityDiffers = %v, want %v", c.name, got, c.wantDifferent)
		}
	}
}

// TestIdentityDriftNeverPrintsTheIdentity is the redaction assertion AGENTS.md
// requires of a new output path. Both sides of this comparison are PII — the
// account uuid and the login email — and the whole payload is in memory when the
// message is built, so "it happens not to be interpolated today" is exactly the
// property a test has to hold in place.
func TestIdentityDriftNeverPrintsTheIdentity(t *testing.T) {
	const liveEmail = "side@example.com"
	const liveUUID = "side-uuid"
	app := identityDriftApp(t)
	claudeJSON(t, app,
		`{"oauthAccount":{"accountUuid":"`+liveUUID+`","emailAddress":"`+liveEmail+`"},"projects":{}}`)

	report := buildDoctor(context.Background(), app, "", false)
	msg, ok := findCheck(report, constants.CheckIdentityDrift)
	if !ok {
		t.Fatal("expected identity drift to be reported")
	}
	for _, secret := range []string{liveEmail, liveUUID, "main-uuid", "main@example.com"} {
		if strings.Contains(msg, secret) {
			t.Errorf("the identity %q reached the doctor message: %q", secret, msg)
		}
	}
}
