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
		"work":     {Accounts: map[string]string{constants.ToolClaude: "work", constants.ToolCodex: "work"}},
		"personal": {Accounts: map[string]string{constants.ToolClaude: "personal"}},
	}
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	// Capture two claude accounts; work is active.
	seedClaude(t, app, workToken, "work-uuid")
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "work") }); code != constants.ExitOK {
		t.Fatalf("capture work: %s", out)
	}
	seedClaude(t, app, personalToken, "personal-uuid")
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "personal") }); code != constants.ExitOK {
		t.Fatalf("capture personal: %s", out)
	}
	// Record work as the active profile.
	st := state.New()
	st.Active[constants.ToolClaude] = "work"
	st.ActiveProfile = "work"
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
		if a.Account == "personal" && a.Active {
			t.Fatalf("personal must not be active: %s", out)
		}
		if a.Account == "work" && a.Active {
			activeAccount = true
			if a.Identity != "work-uuid@example.com" { // §D: raw identity carried in --json
				t.Fatalf("work identity = %q, want work-uuid@example.com: %s", a.Identity, out)
			}
		}
	}
	if !activeAccount {
		t.Fatalf("claude/work must be marked active: %s", out)
	}
	if len(report.Profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d: %s", len(report.Profiles), out)
	}
	for _, p := range report.Profiles {
		if p.Name == "work" && p.Active {
			activeProfile = true
		}
	}
	if !activeProfile {
		t.Fatalf("profile work must be marked active: %s", out)
	}

	// Text view shows both sections with active markers.
	code, out = captureStdout(t, func() int { return runLs(ctx, app, commonOpts{Format: formatText}) })
	mustExit(t, constants.ExitOK, code, out)
	for _, want := range []string{"Accounts:", "Profiles:", "claude:work codex:work", "(active)"} {
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
