package cmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

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
func TestDoctorIdentityDriftSkipsUntrackedSnapshot(t *testing.T) {
	app := testApp(t, nil)
	seedClaudeOAuth(t, app, `{"accessToken":"`+mainToken+`"}`)
	claudeJSON(t, app, `{"projects":{}}`)
	code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main")
	})
	mustExit(t, constants.ExitOK, code, out)

	claudeJSON(t, app, `{"oauthAccount":{"emailAddress":"you@example.com"}}`)
	report := buildDoctor(context.Background(), app, "", false)
	if msg, ok := findCheck(report, constants.CheckIdentityDrift); ok {
		t.Fatalf("an identity the snapshot never recorded must not drift: %q", msg)
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
