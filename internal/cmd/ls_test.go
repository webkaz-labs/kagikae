package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
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

// `kae ls --pins` answers "what is bound right now", from anywhere — the question
// `kae status` cannot answer when every worktree is a separate binding. The two
// negative cases are the whole risk: pinnedDirs deliberately keeps a store that
// nothing points at any more, so listing a store as a bound directory would name
// a directory that is not bound and a re-bind that lands where nothing reads.
func TestLsPinsListsOnlyLiveBindings(t *testing.T) {
	app := overlayTestApp(t)
	root := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })

	bound := filepath.Join(root, "bound")
	unpinned := filepath.Join(root, "unpinned") // store kept by `kae unpin`; no fragment
	gone := filepath.Join(root, "gone")         // deleted or moved after being bound
	if err := os.MkdirAll(bound, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(unpinned, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(bound); err != nil {
		t.Fatal(err)
	}
	boundAbs, err := cwdAbs()
	if err != nil {
		t.Fatal(err)
	}
	if code, out := captureStdout(t, func() int {
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeIsolated)
	}); code != constants.ExitOK {
		t.Fatalf("runPin: %s", out)
	}
	for _, dir := range []string{unpinned, gone} {
		if err := app.recordPinnedDir(paths.PinID(dir), dir); err != nil {
			t.Fatal(err)
		}
	}

	code, out := captureStdout(t, func() int { return runLsPins(app, commonOpts{Format: formatJSON}) })
	mustExit(t, constants.ExitOK, code, out)
	var report pinsReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid ls --pins JSON: %v: %s", err, out)
	}
	if report.SchemaVersion != constants.SchemaVersion {
		t.Fatalf("schema_version = %d, want %d", report.SchemaVersion, constants.SchemaVersion)
	}
	if len(report.BoundDirectories) != 1 {
		t.Fatalf("expected only the live binding, got %d: %s", len(report.BoundDirectories), out)
	}
	got := report.BoundDirectories[0]
	if got.Directory != boundAbs {
		t.Fatalf("directory = %q, want %q", got.Directory, boundAbs)
	}
	if got.Profile != "main" || got.Mode != constants.ModeIsolated {
		t.Fatalf("profile/mode = %q/%q, want main/%s", got.Profile, got.Mode, constants.ModeIsolated)
	}
	if got.Accounts[constants.ToolClaude] != "main" {
		t.Fatalf("accounts = %v, want claude:main", got.Accounts)
	}
	if !got.Current {
		t.Fatalf("the cwd's own binding must be marked current: %s", out)
	}

	// Text view, and the current marker from outside every bound directory.
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	code, text := captureStdout(t, func() int { return runLsPins(app, commonOpts{Format: formatText}) })
	mustExit(t, constants.ExitOK, code, text)
	for _, want := range []string{"Bound directories:", "Directory", "claude:main", constants.ModeIsolated} {
		if !strings.Contains(text, want) {
			t.Fatalf("ls --pins text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, unpinned) || strings.Contains(text, gone) {
		t.Fatalf("a store without a live binding must not be listed:\n%s", text)
	}
	code, outside := captureStdout(t, func() int { return runLsPins(app, commonOpts{Format: formatJSON}) })
	mustExit(t, constants.ExitOK, code, outside)
	var elsewhere pinsReport
	if err := json.Unmarshal([]byte(outside), &elsewhere); err != nil {
		t.Fatal(err)
	}
	if elsewhere.BoundDirectories[0].Current {
		t.Fatalf("nothing is current outside every bound directory: %s", outside)
	}
}

// An unreadable fragment is not an unbound directory. Collapsing the two into one
// silent skip makes a live, genuinely bound directory vanish from the only command
// that says which account it runs — so the row goes, but a warning names it and the
// exit code does not move. Without this test nothing fails if the two branches are
// merged back together, which is the whole point of the fix.
//
// The fragment path is made a *directory* rather than chmod'd: ReadFile gets EISDIR
// deterministically, including as root.
func TestLsPinsWarnsOnAnUnreadableFragment(t *testing.T) {
	app := overlayTestApp(t)
	broken := filepath.Join(t.TempDir(), "broken")
	if err := os.MkdirAll(filepath.Join(broken, fragmentRelPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := app.recordPinnedDir(paths.PinID(broken), broken); err != nil {
		t.Fatal(err)
	}

	code, stderr := captureStderr(t, func() int { return runLsPins(app, commonOpts{Format: formatJSON}) })
	mustExit(t, constants.ExitOK, code, stderr)
	if !strings.Contains(stderr, "is bound but its fragment could not be read") {
		t.Fatalf("an unreadable fragment must be reported, not silently dropped:\n%s", stderr)
	}
	if !strings.Contains(stderr, broken) {
		t.Fatalf("the warning must name the directory:\n%s", stderr)
	}
	_, out := captureStdout(t, func() int { return runLsPins(app, commonOpts{Format: formatJSON}) })
	var report pinsReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid ls --pins JSON: %v: %s", err, out)
	}
	if len(report.BoundDirectories) != 0 {
		t.Fatalf("a directory kae could not read must not be reported as bound: %s", out)
	}
}

// No bindings lists nothing without error and keeps the [] JSON array.
func TestLsPinsEmpty(t *testing.T) {
	app := testApp(t, nil)
	code, out := captureStdout(t, func() int { return runLsPins(app, commonOpts{Format: formatJSON}) })
	mustExit(t, constants.ExitOK, code, out)
	var report pinsReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid ls --pins JSON: %v: %s", err, out)
	}
	if report.BoundDirectories == nil {
		t.Fatalf("bound_directories must be [] not null: %s", out)
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

	// Three bands against the login deadline: dying (past it), soon (3 days out, no
	// refresh token so the access expiry is that deadline), healthy (a month out).
	seedClaudeOAuth(t, app, refreshBackedClaudeCred(app.Now(), -time.Hour))
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "dying") })
	seedClaudeOAuth(t, app, endOfLifeClaudeCred(app.Now(), 3*24*time.Hour, "a"))
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "soon") })
	seedClaudeOAuth(t, app, refreshBackedClaudeCred(app.Now(), 30*24*time.Hour))
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

	// No refresh token, so this really classifies as expiring — the state whose
	// rendering (the day count in the Credential column) is the one being canaried.
	// A refresh-backed payload would read ok and cover nothing.
	seedClaudeOAuth(t, app, endOfLifeClaudeCred(app.Now(), 2*24*time.Hour, canary))
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "canary") })
	_, probe := captureStdout(t, func() int { return runLs(ctx, app, commonOpts{Format: formatJSON}) })
	var probed lsReport
	if err := json.Unmarshal([]byte(probe), &probed); err != nil {
		t.Fatalf("invalid ls JSON: %v: %s", err, probe)
	}
	// Matched on the row's own field, not as a substring of the whole document: the
	// token would also match a future field or an account named for it.
	if len(probed.Accounts) != 1 || probed.Accounts[0].Credential != constants.CredentialExpiring {
		t.Fatalf("fixture no longer reaches the expiring state; this canary would pass vacuously: %s", probe)
	}

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
