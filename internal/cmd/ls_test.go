package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
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

// The inventory commands are where a user looks to see what needs attention, and
// they showed no freshness at all — `kae doctor` was the only way to learn that an
// account had died. Every listing surface now carries the same state, and the
// text cell carries the number that decides whether to act.
func TestInventoryCommandsReportCredentialFreshness(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	// dying: past its deadline (refresh-backed, shelf life spent). soon: 3 days left
	// with no refresh token, so its access expiry is a real end of life and earns the
	// lead-time band. healthy: refresh-backed with a month of shelf life -> ok.
	seedClaudeOAuth(t, app,
		`{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":1609459200000}`)
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "dying") })
	seedClaudeOAuth(t, app, fmt.Sprintf(
		`{"accessToken":"a","refreshToken":"","expiresAt":%d}`,
		app.Now().Add(3*24*time.Hour).UnixMilli(),
	))
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "soon") })
	seedClaudeOAuth(t, app, fmt.Sprintf(
		`{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":%d}`,
		app.Now().Add(30*24*time.Hour).UnixMilli(),
	))
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "healthy") })

	code, out := captureStdout(t, func() int { return runLs(ctx, app, commonOpts{Format: formatJSON}) })
	mustExit(t, constants.ExitOK, code, out)
	var report lsReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid ls JSON: %v: %s", err, out)
	}
	// Additive fields only: the contract version does not move.
	if report.SchemaVersion != constants.SchemaVersion {
		t.Fatalf("schema_version = %d, want %d", report.SchemaVersion, constants.SchemaVersion)
	}
	want := map[string]string{
		"dying":   constants.CredentialStale,
		"soon":    constants.CredentialExpiring,
		"healthy": constants.CredentialOK,
	}
	for _, a := range report.Accounts {
		if got := a.Credential; got != want[a.Account] {
			t.Errorf("%s: credential = %q, want %q", a.Account, got, want[a.Account])
		}
		if a.ReloginBy == "" {
			t.Errorf("%s: a judged account must publish the deadline it was judged against", a.Account)
		} else if _, err := time.Parse(time.RFC3339, a.ReloginBy); err != nil {
			t.Errorf("%s: relogin_by is not RFC3339: %q", a.Account, a.ReloginBy)
		}
	}

	// The human table: a column, and the lead time spelled out rather than a state
	// word the user has to translate.
	_, text := captureStdout(t, func() int { return runLs(ctx, app, opts) })
	for _, want := range []string{"Credential", "re-login now", "3 day(s) left", constants.CredentialOK} {
		if !strings.Contains(text, want) {
			t.Errorf("ls table missing %q:\n%s", want, text)
		}
	}
	// `kae accounts` shares the row shape, so it must show the same thing.
	_, accountsText := captureStdout(t, func() int { return runAccounts(ctx, app, opts) })
	if !strings.Contains(accountsText, "Credential") || !strings.Contains(accountsText, "3 day(s) left") {
		t.Errorf("kae accounts lost the credential column:\n%s", accountsText)
	}
}

// A payload kae parses but that records no deadline it can trust must be reported
// as *unknown*, never as "ok": codex stores a refresh token without publishing its
// expiry, and an auth.json holding only an API key has no expiry at all. Claiming
// those are fine is the failure mode that makes a freshness column worse than none.
func TestInventoryLeavesUndatableCredentialsUnjudged(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	// A refresh token with no published expiry: the deadline is unknowable.
	seedClaudeOAuth(t, app, `{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000}`)
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "undatable") })

	_, out := captureStdout(t, func() int { return runLs(ctx, app, commonOpts{Format: formatJSON}) })
	var report lsReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	for _, a := range report.Accounts {
		if a.Credential != "" || a.ReloginBy != "" {
			t.Fatalf("an unknowable deadline must leave both fields absent, got %q/%q", a.Credential, a.ReloginBy)
		}
	}
	// omitempty, so the keys are gone rather than present-and-empty.
	if strings.Contains(out, "credential") || strings.Contains(out, "relogin_by") {
		t.Fatalf("unjudged rows must omit the fields entirely: %s", out)
	}
	_, text := captureStdout(t, func() int { return runLs(ctx, app, opts) })
	if !strings.Contains(text, "Credential") {
		t.Fatalf("the column stays even when every row is unknown:\n%s", text)
	}
}

// A nil state map is what an unavailable secret backend produces. The listing
// commands answer from metadata and must keep working: they are what a user runs
// when something is already wrong, so a freshness column must never become a
// reason they fail.
func TestAccountItemsToleratesNoCredentialStates(t *testing.T) {
	st := state.New()
	st.Active[constants.ToolClaude] = "main"
	items := accountItems(st, []account.Account{
		{Tool: constants.ToolClaude, Name: "main", Driver: constants.DriverClaudeFilePatch},
	}, nil)
	if len(items) != 1 {
		t.Fatalf("expected the row to survive, got %d", len(items))
	}
	if items[0].Credential != "" || items[0].ReloginBy != "" {
		t.Fatalf("no state map must mean no claim, got %q/%q", items[0].Credential, items[0].ReloginBy)
	}
	if !items[0].Active {
		t.Fatal("the rest of the row must be unaffected")
	}
	if got := credentialCell("", "", time.Now()); got != "-" {
		t.Fatalf("an unknown cell should read as unset, got %q", got)
	}
}

// The freshness column is built from a parsed credential, so it is a new output
// path for a token. AGENTS.md requires a redaction test for each one.
func TestInventoryFreshnessNeverCarriesTheToken(t *testing.T) {
	const canary = "sk-ant-oat01-LS-CANARY-hhhh"
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaudeOAuth(t, app, fmt.Sprintf(
		`{"accessToken":"%s","refreshToken":"%s-r","expiresAt":1577836800000,"refreshTokenExpiresAt":%d}`,
		canary, canary, app.Now().Add(2*24*time.Hour).UnixMilli(),
	))
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "canary") })

	for _, format := range []string{formatText, formatJSON} {
		for name, run := range map[string]func() int{
			"ls":       func() int { return runLs(ctx, app, commonOpts{Format: format}) },
			"accounts": func() int { return runAccounts(ctx, app, commonOpts{Format: format}) },
			"status":   func() int { return runStatus(ctx, app, commonOpts{Format: format}) },
		} {
			_, stdout, stderr := captureBoth(t, run)
			if strings.Contains(stdout+stderr, canary) {
				t.Fatalf("%s (%s) leaked a credential value:\n%s\n%s", name, format, stdout, stderr)
			}
		}
	}
}
