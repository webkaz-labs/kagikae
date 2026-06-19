package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

const (
	mainToken = "sk-ant-oat01-MAIN-TOKEN-aaaa"
	sideToken = "sk-ant-oat01-SIDE-TOKEN-bbbb"
)

// testApp builds an isolated App with a temp home, the file secret backend,
// a linux Claude driver, and a fixed clock.
func testApp(t *testing.T, envVars map[string]string) *App {
	t.Helper()
	home := t.TempDir()
	getenv := func(key string) string { return envVars[key] }
	p := paths.Resolve(getenv, home)
	cfg := config.Default()
	cfg.Security.SecretBackend = secret.BackendFile
	return &App{
		Paths:      p,
		Config:     cfg,
		ConfigPath: p.ConfigFile(),
		Env: adapter.Env{
			GOOS:     "linux",
			Home:     home,
			Getenv:   getenv,
			LookPath: func(string) (string, error) { return "", errors.New("not found") },
		},
		Now: func() time.Time { return time.Date(2026, 6, 11, 1, 23, 45, 0, time.UTC) },
	}
}

func seedClaude(t *testing.T, app *App, token, accountUUID string) {
	t.Helper()
	writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"),
		`{"claudeAiOauth":{"accessToken":"`+token+`","subscriptionType":"max"}}`)
	writeFile(t, filepath.Join(app.Env.Home, ".claude.json"),
		`{"oauthAccount":{"accountUuid":"`+accountUUID+`","emailAddress":"`+accountUUID+`@example.com"},`+
			`"projects":{"/repo":{"allowedTools":["Bash"]}},"firstStartTime":"2024-01-01T00:00:00Z"}`)
	writeFile(t, filepath.Join(app.Env.Home, ".claude", "settings.json"), `{"theme":"dark"}`)
}

func seedCodex(t *testing.T, app *App, token string) {
	t.Helper()
	writeFile(t, filepath.Join(app.Env.Home, ".codex", "auth.json"),
		`{"tokens":{"access_token":"`+token+`"}}`)
	writeFile(t, filepath.Join(app.Env.Home, ".codex", "config.toml"), "model = \"gpt-5.4\"\n")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mustExit(t *testing.T, want, got int, output string) {
	t.Helper()
	if got != want {
		t.Fatalf("expected exit %d, got %d (output: %s)", want, got, output)
	}
}

func TestCaptureSwitchRollbackClaude(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main-uuid")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	seedClaude(t, app, sideToken, "side-uuid")
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, out)

	settingsBefore := readFile(t, filepath.Join(app.Env.Home, ".claude", "settings.json"))

	// switch back to main
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	creds := readFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"))
	if !strings.Contains(creds, mainToken) || strings.Contains(creds, sideToken) {
		t.Fatalf("credentials not switched: %s", creds)
	}
	// .claude.json is not switched: /oauthAccount is a token-derived cache that
	// claude self-heals; kae touches only the credential. The last-seeded
	// side-uuid must survive the switch back to main credentials.
	identity := readFile(t, filepath.Join(app.Env.Home, ".claude.json"))
	if strings.Contains(identity, "main-uuid") {
		t.Fatalf(".claude.json must not be patched by switch: %s", identity)
	}
	for _, preserved := range []string{`"projects"`, `"/repo"`, `"firstStartTime"`, "side-uuid"} {
		if !strings.Contains(identity, preserved) {
			t.Fatalf("mixed-state key lost: %s missing in %s", preserved, identity)
		}
	}
	if got := readFile(t, filepath.Join(app.Env.Home, ".claude", "settings.json")); got != settingsBefore {
		t.Fatalf("settings.json must be untouched: %s", got)
	}
	info, err := os.Stat(filepath.Join(app.Env.Home, ".claude", ".credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials mode: %v", info.Mode())
	}

	// state reflects the switch
	st, err := app.loadState()
	if err != nil || st.Active["claude"] != "main" {
		t.Fatalf("state not updated: %+v %v", st, err)
	}

	// backups exist and rollback restores the side login
	code, out = captureStdout(t, func() int { return runBackupList(ctx, app, opts) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "20260611T012345Z") {
		t.Fatalf("backup list missing entry: %s", out)
	}
	code, out = captureStdout(t, func() int { return runRollback(ctx, app, opts, "") })
	mustExit(t, constants.ExitOK, code, out)
	creds = readFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"))
	if !strings.Contains(creds, sideToken) {
		t.Fatalf("rollback did not restore: %s", creds)
	}
	st, _ = app.loadState()
	if st.Active["claude"] != "side" {
		t.Fatalf("rollback did not restore state: %+v", st)
	}

	// rollback is itself reversible: it created a "rollback" backup of the
	// pre-rollback (main) state, so rolling back again returns to main.
	code, out = captureStdout(t, func() int { return runBackupList(ctx, app, opts) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "rollback") {
		t.Fatalf("expected a rollback-reason backup: %s", out)
	}
	code, out = captureStdout(t, func() int { return runRollback(ctx, app, opts, "") })
	mustExit(t, constants.ExitOK, code, out)
	creds = readFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"))
	if !strings.Contains(creds, mainToken) {
		t.Fatalf("rollback of rollback did not restore main state: %s", creds)
	}
}

func TestSwitchAllProfileAndDivergence(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	app.Config.Profiles["main"] = config.Profile{Accounts: map[string]string{"claude": "main", "codex": "main"}}
	app.Config.Profiles["side"] = config.Profile{Accounts: map[string]string{"claude": "side", "codex": "side"}}

	seedClaude(t, app, mainToken, "main-uuid")
	seedCodex(t, app, "codex-main-token")
	for _, args := range [][]string{{"claude", "main"}, {"codex", "main"}} {
		code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, args[0], args[1]) })
		mustExit(t, constants.ExitOK, code, out)
	}
	seedClaude(t, app, sideToken, "side-uuid")
	seedCodex(t, app, "codex-side-token")
	for _, args := range [][]string{{"claude", "side"}, {"codex", "side"}} {
		code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, args[0], args[1]) })
		mustExit(t, constants.ExitOK, code, out)
	}

	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "all", "main") })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "Active profile: main") {
		t.Fatalf("profile not reported: %s", out)
	}
	if got := readFile(t, filepath.Join(app.Env.Home, ".codex", "auth.json")); !strings.Contains(got, "codex-main-token") {
		t.Fatalf("codex not switched: %s", got)
	}
	if got := readFile(t, filepath.Join(app.Env.Home, ".codex", "config.toml")); !strings.Contains(got, "gpt-5.4") {
		t.Fatalf("codex config must be preserved: %s", got)
	}
	st, _ := app.loadState()
	if st.ActiveProfile != "main" {
		t.Fatalf("active profile not set: %+v", st)
	}

	// single-tool divergence clears the profile match
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, out)
	report, err := buildStatus(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	if report.ActiveProfile != nil {
		t.Fatalf("diverged active set should match no profile: %+v", report.ActiveProfile)
	}
}

func TestSwitchUnknownAccountAndProfile(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "nope") })
	mustExit(t, constants.ExitNotFound, code, out)
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "all", "nope") })
	mustExit(t, constants.ExitNotFound, code, out)
}

func TestSwitchDryRunWritesNothing(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	seedClaude(t, app, mainToken, "main-uuid")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	seedClaude(t, app, sideToken, "side-uuid")

	dryOpts := commonOpts{Format: formatText, DryRun: true}
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, dryOpts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "claude -> main") || !strings.Contains(out, "patch") {
		t.Fatalf("dry-run plan missing: %s", out)
	}
	if got := readFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json")); !strings.Contains(got, sideToken) {
		t.Fatal("dry-run must not write")
	}
	code, out = captureStdout(t, func() int { return runBackupList(ctx, app, opts) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "no backups yet") {
		t.Fatalf("dry-run must not create backups: %s", out)
	}
}

func TestSecretsNeverInOutputOrMetadata(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	jsonOpts := commonOpts{Format: formatJSON}

	seedClaude(t, app, mainToken, "main-uuid")
	code, captureOut := captureStdout(t, func() int { return runCapture(ctx, app, jsonOpts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, captureOut)
	seedClaude(t, app, sideToken, "side-uuid")
	code, _ = captureStdout(t, func() int { return runCapture(ctx, app, jsonOpts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, "")

	code, switchOut := captureStdout(t, func() int { return runSwitch(ctx, app, jsonOpts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, switchOut)
	code, statusOut := captureStdout(t, func() int { return runStatus(ctx, app, jsonOpts) })
	mustExit(t, constants.ExitOK, code, statusOut)
	code, rollbackOut := captureStdout(t, func() int { return runRollback(ctx, app, jsonOpts, "") })
	mustExit(t, constants.ExitOK, code, rollbackOut)

	for name, output := range map[string]string{
		"capture": captureOut, "switch": switchOut, "status": statusOut, "rollback": rollbackOut,
	} {
		for _, tok := range []string{mainToken, sideToken} {
			if strings.Contains(output, tok) {
				t.Fatalf("secret leaked in %s output: %s", name, output)
			}
		}
	}
	// metadata files must not contain secrets either
	metaData := readFile(t, filepath.Join(app.Paths.AccountDir("claude", "main"), "account.toml"))
	if strings.Contains(metaData, mainToken) {
		t.Fatalf("secret leaked into account.toml: %s", metaData)
	}
	entries, err := os.ReadDir(app.Paths.BackupsDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		content := readFile(t, filepath.Join(app.Paths.BackupsDir(), entry.Name()))
		for _, tok := range []string{mainToken, sideToken} {
			if strings.Contains(content, tok) {
				t.Fatalf("secret leaked into backup metadata %s", entry.Name())
			}
		}
	}
}

func TestSwitchJSONReportShape(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	jsonOpts := commonOpts{Format: formatJSON}
	seedClaude(t, app, mainToken, "main-uuid")
	code, _ := captureStdout(t, func() int { return runCapture(ctx, app, jsonOpts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, "")
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, jsonOpts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out)
	}
	if report["schema_version"].(float64) != 1 || report["ok"] != true || report["dry_run"] != false {
		t.Fatalf("unexpected report: %s", out)
	}
	if report["profile"] != nil {
		t.Fatalf("single-tool switch must have null profile: %s", out)
	}
	results := report["results"].([]any)
	result := results[0].(map[string]any)
	if result["tool"] != "claude" || result["applied"] != true || result["driver"] != "claude-file-patch" {
		t.Fatalf("unexpected result: %s", out)
	}
	actions := result["actions"].([]any)
	if len(actions) != 1 {
		t.Fatalf("expected 1 action: %s", out)
	}
	first := actions[0].(map[string]any)
	if first["kind"] != "json-pointer" || !strings.HasPrefix(first["target"].(string), "~/") {
		t.Fatalf("unexpected action: %s", out)
	}
	if result["warnings"] == nil {
		t.Fatalf("warnings must be [], not null: %s", out)
	}
}

func TestStatusAccountsCurrentJSON(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	jsonOpts := commonOpts{Format: formatJSON}
	code, out := captureStdout(t, func() int { return runStatus(ctx, app, jsonOpts) })
	mustExit(t, constants.ExitOK, code, out)
	var status map[string]any
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatal(err)
	}
	if status["active_profile"] != nil || status["mode"] != "auth" {
		t.Fatalf("unexpected status: %s", out)
	}
	tools := status["tools"].([]any)
	if len(tools) != len(constants.Tools) {
		t.Fatalf("expected %d tools: %s", len(constants.Tools), out)
	}
	first := tools[0].(map[string]any)
	if first["tool"] != "claude" || first["account"] != nil || first["accounts"] == nil {
		t.Fatalf("unexpected tool entry: %s", out)
	}

	code, out = captureStdout(t, func() int { return runAccounts(ctx, app, jsonOpts) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, `"accounts": []`) {
		t.Fatalf("accounts must encode []: %s", out)
	}
}

func TestDoctorReportsConfigError(t *testing.T) {
	app := testApp(t, nil)
	app.ConfigErr = errors.New("boom")
	ctx := context.Background()
	jsonOpts := commonOpts{Format: formatJSON}
	code, out := captureStdout(t, func() int { return runDoctor(ctx, app, jsonOpts, "") })
	mustExit(t, constants.ExitError, code, out)
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report["ok"] != false {
		t.Fatalf("doctor should fail on config error: %s", out)
	}
}

func TestDoctorHealthy(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	code, out := captureStdout(t, func() int { return runDoctor(ctx, app, commonOpts{Format: formatText}, "claude") })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "claude") || !strings.Contains(out, "no blocking problems") {
		t.Fatalf("unexpected doctor output: %s", out)
	}
}

// §C: with the keyring store, capture/use round-trip through the single
// `Codex Auth` keychain item. The per-login opaque account is captured verbatim
// and apply deletes the prior item before writing the target's, so exactly one
// item — with the target account's id and payload — remains. The keychainSim
// (a stateful `security` double) keeps the test off the real keychain.
func TestCodexKeyringRoundTrip(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, nil)
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		opts := commonOpts{Format: formatText}
		writeFile(t, filepath.Join(app.Env.Home, ".codex", "config.toml"),
			"cli_auth_credentials_store = \"keyring\"\n")

		// codex logged in as main: one keychain item under its opaque account.
		sim.present = true
		sim.account = "cli|opaqueMAIN"
		sim.payload = `{"tokens":{"access_token":"main-access","refresh_token":"main-refresh"}}`
		captureCode, captureOut := captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "main") })
		if captureCode != constants.ExitOK {
			t.Fatalf("capture main: %s", captureOut)
		}
		// Redaction: the keyring payload is a credential and must never reach
		// stdout or the snapshot metadata (only the opaque account id is stored).
		if strings.Contains(captureOut, "main-access") {
			t.Fatalf("keyring token leaked to capture output: %s", captureOut)
		}
		meta := readFile(t, filepath.Join(app.Paths.AccountDir("codex", "main"), "account.toml"))
		if strings.Contains(meta, "main-access") {
			t.Fatalf("keyring token leaked into account.toml: %s", meta)
		}
		if !strings.Contains(meta, "cli|opaqueMAIN") {
			t.Fatalf("captured keychain account missing from account.toml: %s", meta)
		}
		// A re-login as side replaced the live item (new opaque account).
		sim.account = "cli|opaqueSIDE"
		sim.payload = `{"tokens":{"access_token":"side-access","refresh_token":"side-refresh"}}`
		if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "side") }); code != constants.ExitOK {
			t.Fatalf("capture side: %s", out)
		}
		// Switch back to main: delete the side item, recreate main's verbatim.
		sim.ops = nil // isolate the apply's keychain mutations
		if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "codex", "main") }); code != constants.ExitOK {
			t.Fatalf("switch to main: %s", out)
		}
		if !sim.present {
			t.Fatal("no Codex Auth item after switch")
		}
		// Apply must delete the prior item before writing the target's, so a
		// single item remains (robust to service-only or service+account match).
		if len(sim.ops) < 2 || sim.ops[0] != "delete" || sim.ops[1] != "add" {
			t.Fatalf("expected delete-then-add on apply, got %v", sim.ops)
		}
		if sim.account != "cli|opaqueMAIN" {
			t.Fatalf("item account = %q, want cli|opaqueMAIN (captured verbatim)", sim.account)
		}
		if !strings.Contains(sim.payload, "main-access") || strings.Contains(sim.payload, "side-access") {
			t.Fatalf("item payload not restored to main verbatim: %s", sim.payload)
		}
	})
}

// §C: a keyring item present but with an empty account attribute is refused at
// capture rather than stored as an unusable snapshot (which apply could not
// recreate correctly).
func TestCodexKeyringEmptyAccountRefused(t *testing.T) {
	sim := &keychainSim{present: true, account: "", payload: `{"tokens":{"access_token":"x"}}`}
	runner.With(sim, func() {
		app := testApp(t, nil)
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		writeFile(t, filepath.Join(app.Env.Home, ".codex", "config.toml"),
			"cli_auth_credentials_store = \"keyring\"\n")
		code, out := captureStdout(t, func() int { return runCapture(ctx, app, commonOpts{Format: formatText}, "codex", "main") })
		if code == constants.ExitOK {
			t.Fatalf("expected capture to refuse an item with an empty account: %s", out)
		}
	})
}

// geminiSim is a stateful security double keyed by service+account, so the agy
// keychain driver's service+account matching can be exercised without touching
// the real keychain and a sibling gemini item (a different account) is visible.
type geminiSim struct {
	items map[string]string // key: service "\x00" account
}

func (k *geminiSim) key(service, account string) string { return service + "\x00" + account }

func (k *geminiSim) Run(_ context.Context, _ string, args ...string) (string, string, int) {
	if len(args) == 0 {
		return "", "", 0
	}
	service, account := valueAfter(args, "-s"), valueAfter(args, "-a")
	switch args[0] {
	case "find-generic-password":
		if account == "" {
			return "", "test: agy read must be account-scoped (-a)", 1
		}
		payload, ok := k.items[k.key(service, account)]
		if !ok {
			return "", "security: could not be found", 44
		}
		if slices.Contains(args, "-w") {
			return payload, "", 0
		}
		return fmt.Sprintf("    \"acct\"<blob>=\"%s\"\n", account), "", 0
	case "add-generic-password":
		k.items[k.key(service, account)] = valueAfter(args, "-w")
		return "", "", 0
	case "delete-generic-password":
		delete(k.items, k.key(service, account))
		return "", "", 0
	}
	return "", "", 0
}

func (k *geminiSim) RunInput(ctx context.Context, _ string, name string, args ...string) (string, string, int) {
	return k.Run(ctx, name, args...)
}

// §A: on macOS, agy capture/use round-trip through the gemini/antigravity
// keychain item, matched by service AND account so a sibling gemini item (the
// Gemini ecosystem's, under a different account) is never read or written. The
// opaque token is stored verbatim and never leaks to stdout or the snapshot.
func TestAgyKeychainRoundTrip(t *testing.T) {
	const sibling = "gemini\x00gemini-cli-user"
	sim := &geminiSim{items: map[string]string{
		"gemini\x00antigravity": "agy-main-token",
		sibling:                 "gemini-cli-secret",
	}}
	runner.With(sim, func() {
		app := testApp(t, nil)
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		opts := commonOpts{Format: formatText}

		// agy logged in as main: capture stores the token verbatim, never leaking it.
		captureCode, captureOut := captureStdout(t, func() int { return runCapture(ctx, app, opts, "agy", "main") })
		if captureCode != constants.ExitOK {
			t.Fatalf("capture main: %s", captureOut)
		}
		if strings.Contains(captureOut, "agy-main-token") {
			t.Fatalf("agy token leaked to capture output: %s", captureOut)
		}
		meta := readFile(t, filepath.Join(app.Paths.AccountDir("agy", "main"), "account.toml"))
		if strings.Contains(meta, "agy-main-token") {
			t.Fatalf("agy token leaked into account.toml: %s", meta)
		}

		// A re-login as side replaced the live antigravity item.
		sim.items["gemini\x00antigravity"] = "agy-side-token"
		if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "agy", "side") }); code != constants.ExitOK {
			t.Fatalf("capture side: %s", out)
		}

		// Switch back to main: the antigravity item is restored verbatim.
		if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "agy", "main") }); code != constants.ExitOK {
			t.Fatalf("switch to main: %s", out)
		}
		if got := sim.items["gemini\x00antigravity"]; got != "agy-main-token" {
			t.Fatalf("antigravity item not switched to main verbatim: %q", got)
		}
		// The sibling gemini item must never be touched by any agy operation.
		if sim.items[sibling] != "gemini-cli-secret" {
			t.Fatalf("sibling gemini item was modified: %q", sim.items[sibling])
		}
	})
}

// §A: an empty keychain payload is refused at capture (structure guard:
// non-empty, single-line), not stored as an unusable snapshot.
func TestAgyKeychainEmptyPayloadRefused(t *testing.T) {
	sim := &geminiSim{items: map[string]string{"gemini\x00antigravity": ""}}
	runner.With(sim, func() {
		app := testApp(t, nil)
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		code, out := captureStdout(t, func() int { return runCapture(ctx, app, commonOpts{Format: formatText}, "agy", "main") })
		if code == constants.ExitOK {
			t.Fatalf("expected capture to refuse an empty keychain payload: %s", out)
		}
	})
}

func TestAgyCaptureSwitchFileSnapshot(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	// without a credential file, capture reports missing auth
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "agy", "main") })
	mustExit(t, constants.ExitAuthMissing, code, out)

	credPath := filepath.Join(app.Env.Home, ".gemini", "antigravity-cli", "credentials.enc")
	writeFile(t, credPath, "opaque-main-blob")
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "agy", "main") })
	mustExit(t, constants.ExitOK, code, out)

	writeFile(t, credPath, "opaque-side-blob")
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "agy", "side") })
	mustExit(t, constants.ExitOK, code, out)

	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "agy", "main") })
	mustExit(t, constants.ExitOK, code, out)
	if got := readFile(t, credPath); got != "opaque-main-blob" {
		t.Fatalf("agy credential not switched: %s", got)
	}
}

func TestOpencodeCaptureSwitchPreservesSiblingProviders(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	authPath := filepath.Join(app.Env.Home, ".local", "share", "opencode", "auth.json")

	// without an openai entry, capture reports missing auth (sibling
	// API-key providers do not count as a subscription login)
	writeFile(t, authPath, `{"openrouter":{"type":"api","key":"sk-other"}}`)
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "opencode", "main") })
	mustExit(t, constants.ExitAuthMissing, code, out)

	writeFile(t, authPath,
		`{"openai":{"type":"oauth","refresh":"r-main","access":"a-main"},"openrouter":{"type":"api","key":"sk-other"}}`)
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "opencode", "main") })
	mustExit(t, constants.ExitOK, code, out)

	writeFile(t, authPath,
		`{"openai":{"type":"oauth","refresh":"r-side","access":"a-side"},"openrouter":{"type":"api","key":"sk-other"}}`)
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "opencode", "side") })
	mustExit(t, constants.ExitOK, code, out)

	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "opencode", "main") })
	mustExit(t, constants.ExitOK, code, out)
	got := readFile(t, authPath)
	if !strings.Contains(got, `"r-main"`) || strings.Contains(got, `"r-side"`) {
		t.Fatalf("openai entry not switched: %s", got)
	}
	if !strings.Contains(got, `"sk-other"`) {
		t.Fatalf("sibling provider key must survive the switch: %s", got)
	}
}

func TestCaptureWithoutLiveAuth(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitAuthMissing, code, out)
}

func TestSwitchLockBusy(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	seedClaude(t, app, mainToken, "main-uuid")
	code, _ := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, "")

	held, err := lock.Acquire(app.Paths.LocksDir(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitLockBusy, code, out)
}

func TestJSONErrorReport(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	jsonOpts := commonOpts{Format: formatJSON}
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, jsonOpts, "claude", "nope") })
	mustExit(t, constants.ExitNotFound, code, out)
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("error report must be json: %v\n%s", err, out)
	}
	if report["ok"] != false || report["error_code"] != "not_found" {
		t.Fatalf("unexpected error report: %s", out)
	}
}

func TestInitCreatesConfigIdempotently(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	code, out := captureStdout(t, func() int { return runInit(ctx, app, opts) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "Created") {
		t.Fatalf("unexpected: %s", out)
	}
	marker := "# user marker"
	writeFile(t, app.ConfigPath, "version = 1\n"+marker+"\n")
	code, out = captureStdout(t, func() int { return runInit(ctx, app, opts) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "already exists") {
		t.Fatalf("unexpected: %s", out)
	}
	if !strings.Contains(readFile(t, app.ConfigPath), marker) {
		t.Fatal("init must not overwrite an existing config")
	}
}

func TestRollbackUnknownID(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	code, out := captureStdout(t, func() int { return runRollback(ctx, app, opts, "20000101T000000Z") })
	mustExit(t, constants.ExitNotFound, code, out)
}

func TestBackupPruneRetention(t *testing.T) {
	app := testApp(t, nil)
	app.Config.Security.BackupKeep = 1
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	seedClaude(t, app, mainToken, "main-uuid")
	code, _ := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, "")
	for i := 0; i < 3; i++ {
		code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
		mustExit(t, constants.ExitOK, code, out)
	}
	entries, err := os.ReadDir(app.Paths.BackupsDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected retention to keep 1 backup, got %d", len(entries))
	}
}
