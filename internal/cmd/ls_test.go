package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/state"
)

func TestLsListsAccountsAndProfiles(t *testing.T) {
	app := testApp(t, nil)
	app.Config.Profiles = map[string]config.Profile{
		"main": {Accounts: map[string]string{constants.ToolClaude: "main", constants.ToolCodex: "main"}},
		"side": {Accounts: map[string]string{constants.ToolClaude: "side"}},
	}
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	// Capture two claude accounts; main is active.
	seedClaude(t, app, mainToken, "main-uuid")
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
		t.Fatalf("capture main: %s", out)
	}
	seedClaude(t, app, sideToken, "side-uuid")
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "side") }); code != constants.ExitOK {
		t.Fatalf("capture side: %s", out)
	}
	// Record main as the active profile.
	st := state.New()
	st.Active[constants.ToolClaude] = "main"
	st.ActiveProfile = "main"
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}

	// JSON contract.
	code, out := captureStdout(t, func() int { return runLs(ctx, app, commonOpts{Format: formatJSON}) })
	mustExit(t, constants.ExitOK, code, out)
	var report lsReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid ls JSON: %v: %s", err, out)
	}
	if report.SchemaVersion != constants.SchemaVersion {
		t.Fatalf("schema_version = %d, want %d", report.SchemaVersion, constants.SchemaVersion)
	}
	if len(report.Accounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d: %s", len(report.Accounts), out)
	}
	var activeAccount, activeProfile bool
	for _, a := range report.Accounts {
		if a.Account == "side" && a.Active {
			t.Fatalf("side must not be active: %s", out)
		}
		if a.Account == "main" && a.Active {
			activeAccount = true
			if a.Identity != "main-uuid@example.com" { // §D: raw identity carried in --json
				t.Fatalf("main identity = %q, want main-uuid@example.com: %s", a.Identity, out)
			}
		}
	}
	if !activeAccount {
		t.Fatalf("claude/main must be marked active: %s", out)
	}
	if len(report.Profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d: %s", len(report.Profiles), out)
	}
	for _, p := range report.Profiles {
		if p.Name == "main" && p.Active {
			activeProfile = true
		}
	}
	if !activeProfile {
		t.Fatalf("profile main must be marked active: %s", out)
	}

	// Text view shows both sections with active markers.
	code, out = captureStdout(t, func() int { return runLs(ctx, app, commonOpts{Format: formatText}) })
	mustExit(t, constants.ExitOK, code, out)
	for _, want := range []string{"Accounts:", "Profiles:", "claude:main codex:main", "(active)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ls text missing %q: %s", want, out)
		}
	}
}

// Empty state lists nothing without error and keeps the [] JSON arrays.
func TestLsEmpty(t *testing.T) {
	app := testApp(t, nil)
	code, out := captureStdout(t, func() int { return runLs(context.Background(), app, commonOpts{Format: formatJSON}) })
	mustExit(t, constants.ExitOK, code, out)
	var report lsReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid ls JSON: %v: %s", err, out)
	}
	if report.Accounts == nil || report.Profiles == nil {
		t.Fatalf("accounts/profiles must be [] not null: %s", out)
	}
}
