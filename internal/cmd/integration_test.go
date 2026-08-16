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

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/backup"
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
			GOOS:   "linux",
			Home:   home,
			Getenv: getenv,
			// Injected because production does (internal/cmd/app.go), and because
			// leaving it nil makes `Env.IsSet` degrade to a non-empty test on Getenv —
			// which silently hides every defect that lives in the difference between
			// "absent" and "set to empty". One shipped that way: the global-scope
			// masking covered Getenv only, and no fixture here could reach it.
			LookupEnv: func(key string) (string, bool) { value, ok := envVars[key]; return value, ok },
			LookPath:  func(string) (string, error) { return "", errors.New("not found") },
		},
		Now: func() time.Time { return time.Date(2026, 6, 11, 1, 23, 45, 0, time.UTC) },
	}
}

// dirCredFile is where a per-directory bind puts a tool's credential file:
// the account's own credential store for a tool that can separate the two
// (claude), the store directory itself otherwise.
//
// Tests go through it rather than joining a path, so the layout lives in one
// place — and so a test cannot keep asserting a location production has stopped
// writing to. It applies production's own rule (credStoreDir), which is what
// makes it safe to seed *and* assert through: seeding somewhere the tool does not
// read is exactly the mistake this file's fixtures are prone to.
func dirCredFile(app *App, tool, account, storeDir string) string {
	if dir := app.credStoreDir(tool, account); dir != "" {
		return filepath.Join(dir, ".credentials.json")
	}
	return filepath.Join(storeDir, ".credentials.json")
}

// makePreSplit rewrites an existing binding into the shape a kae from before the
// credential split left behind: no credential entry in the fragment, and the
// credential inside the store directory. It moves the file rather than copying it,
// so nothing is left in the new location to make an assertion pass by accident.
//
// It exists because this state cannot be produced by running kae any more, and it
// is the state every migration path has to keep working against — a directory bound
// by an older release keeps its per-directory credential until it is re-pinned, and
// the sweeps must still find *that* one rather than the account's shared store.
func makePreSplit(t *testing.T, app *App, tool, account, boundDir, storeDir string) {
	t.Helper()
	fragment := filepath.Join(boundDir, fragmentRelPath)
	data, err := os.ReadFile(fragment)
	if err != nil {
		t.Fatal(err)
	}
	kept := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, credentialEnvVar(tool)+" = ") {
			kept = append(kept, line)
		}
	}
	if len(kept) == len(strings.Split(string(data), "\n")) {
		t.Fatalf("fragment already has no %s entry; this fixture would assert nothing", credentialEnvVar(tool))
	}
	if err := os.WriteFile(fragment, []byte(strings.Join(kept, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	from := dirCredFile(app, tool, account, storeDir)
	if from == filepath.Join(storeDir, ".credentials.json") {
		return // this tool never split them
	}
	if payload, err := os.ReadFile(from); err == nil {
		writeFile(t, filepath.Join(storeDir, ".credentials.json"), string(payload))
		if err := os.Remove(from); err != nil {
			t.Fatal(err)
		}
	}
}

func seedClaude(t *testing.T, app *App, token, accountUUID string) {
	t.Helper()
	writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"),
		`{"claudeAiOauth":{"accessToken":"`+token+`","subscriptionType":"max"}}`)
	// The /oauthAccount object comes from claudeOAuthAccount, so this live cache and
	// every fixture a test compares it against carry the same keys. The rest of the
	// file is deliberately richer than the artifact kae patches — the point of the
	// mixed-state file is that a pointer write leaves it alone.
	writeFile(t, filepath.Join(app.Env.Home, ".claude.json"),
		`{"oauthAccount":`+claudeOAuthAccount(accountUUID, accountUUID+"@example.com")+`,`+
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
	// /oauthAccount travels with the credential: claude's self-heal of it is
	// TTL-gated (no profile refetch while the cache is under 24h old), so the
	// last-seeded side-uuid must be replaced by main's. Only that pointer moves;
	// every other key of the mixed-state file survives.
	identity := readFile(t, filepath.Join(app.Env.Home, ".claude.json"))
	if !strings.Contains(identity, "main-uuid") || strings.Contains(identity, "side-uuid") {
		t.Fatalf("/oauthAccount not switched: %s", identity)
	}
	for _, preserved := range []string{`"projects"`, `"/repo"`, `"firstStartTime"`} {
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
	if identity := readFile(t, filepath.Join(app.Env.Home, ".claude.json")); !strings.Contains(identity, "side-uuid") {
		t.Fatalf("rollback did not restore /oauthAccount: %s", identity)
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

// A backup's active_before keeps the name it had at capture time, so a rollback
// across a `kae account rm` used to record an account that is gone: state named a
// snapshot nothing could load (doctor's active_orphan) and the next `kae use
// claude` failed with "is not captured yet". The pointer is dropped instead — and
// the credential rollback, which is the part that was never broken, still happens.
func TestRollbackDropsActivePointerWhoseSnapshotIsGone(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, sideToken, "side-uuid")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, out)
	seedClaude(t, app, mainToken, "main-uuid")
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	// Make side active, then switch away: the backup that switch takes is the one
	// recording side as the account to restore.
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, out)
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	if _, err := buildAccountRm(ctx, app, opts, "claude", "side", false); err != nil {
		t.Fatalf("account rm side: %v", err)
	}

	code, stderr := captureStderr(t, func() int {
		code, _ := captureStdout(t, func() int { return runRollback(ctx, app, opts, "") })
		return code
	})
	mustExit(t, constants.ExitOK, code, stderr)
	if !strings.Contains(stderr, "side") || !strings.Contains(stderr, "no active account") {
		t.Fatalf("rollback must say which pointer it dropped and that it dropped it: %q", stderr)
	}

	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	if name, ok := st.Active["claude"]; ok {
		t.Fatalf("state kept an active account whose snapshot is gone: %q", name)
	}
	if creds := readFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json")); !strings.Contains(creds, sideToken) {
		t.Fatalf("rollback did not restore the credential: %s", creds)
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
	// The claude file driver switches the credential and the identity cache;
	// both are pointer patches and both report a home-relative target.
	actions := result["actions"].([]any)
	if len(actions) != 2 {
		t.Fatalf("expected 2 actions: %s", out)
	}
	for _, raw := range actions {
		action := raw.(map[string]any)
		if action["kind"] != "json-pointer" || !strings.HasPrefix(action["target"].(string), "~/") {
			t.Fatalf("unexpected action: %s", out)
		}
	}
	if pointer := actions[1].(map[string]any)["pointer"]; pointer != "/oauthAccount" {
		t.Fatalf("identity cache missing from actions: %s", out)
	}
	if result["warnings"] == nil {
		t.Fatalf("warnings must be [], not null: %s", out)
	}
}

// A switch-away recapture refreshes the credential of the account being left, but
// must never import the live identity cache: claude maintains it only lazily (24h
// TTL), so it can name a different account than the live credential — the very
// drift this artifact exists to correct. Importing it would mislabel the account
// for good. A live identity that names another account is stronger evidence still
// (a login outside kae), and there the recapture is skipped altogether
// (TestSwitchAwaySkipsRecaptureAfterOutsideLogin); either way the snapshot keeps
// the identity it recorded.
func TestRecaptureKeepsSnapshotIdentity(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main-uuid")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	seedClaude(t, app, sideToken, "side-uuid")
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, out)
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, out)

	// side is active. Its live identity still names side — so this is the keep
	// path, not the outside-login skip — but claude has refetched the profile and
	// renewed the volatile fields, and the credential rotated in-tool.
	const rotated = "sk-ant-oat01-SIDE-ROTATED-eeee"
	writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"),
		`{"claudeAiOauth":{"accessToken":"`+rotated+`","subscriptionType":"max"}}`)
	// The identifying keys come from the same template the snapshot's did, or this is
	// the outside-login *skip* path instead of the keep path this test is about; only
	// the volatile field claude renews on its own is added.
	liveIdentity := strings.TrimSuffix(claudeOAuthAccount("side-uuid", "side-uuid@example.com"), "}") +
		`,"profileFetchedAt":9999}`
	writeFile(t, filepath.Join(app.Env.Home, ".claude.json"),
		`{"oauthAccount":`+liveIdentity+`,"projects":{}}`)

	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, out)

	// The rotated credential was recaptured (the point of switching away)...
	if creds := claudeCreds(t, app); !strings.Contains(creds, rotated) {
		t.Fatalf("recapture did not refresh the credential: %s", creds)
	}
	// ...while the identity stayed the one the snapshot recorded: same account, and
	// none of the volatile fields claude renewed on its own.
	identity := readFile(t, filepath.Join(app.Env.Home, ".claude.json"))
	if !strings.Contains(identity, "side-uuid") {
		t.Fatalf("recapture lost the recorded identity: %s", identity)
	}
	if strings.Contains(identity, "profileFetchedAt") {
		t.Fatalf("recapture imported the volatile live identity: %s", identity)
	}
}

// An identity-only artifact whose payload has gone missing from the secret store must
// not block the credential recorded beside it: the switch proceeds and clears the
// stale cache, which claude then refetches.
func TestSwitchSurvivesMissingIdentityPayload(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main-uuid")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	// Capture side too, so side is the active account: switching away from main
	// would otherwise recapture main's snapshot from the live store first and
	// mask the missing payload.
	seedClaude(t, app, sideToken, "side-uuid")
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, out)

	be, err := app.secretBackend()
	if err != nil {
		t.Fatal(err)
	}
	if err := be.Delete(ctx, account.SecretRef("claude", "main", "oauth_account")); err != nil {
		t.Fatal(err)
	}

	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	if creds := readFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json")); !strings.Contains(creds, mainToken) {
		t.Fatalf("credential not applied: %s", creds)
	}
	if identity := readFile(t, filepath.Join(app.Env.Home, ".claude.json")); strings.Contains(identity, "oauthAccount") {
		t.Fatalf("stale identity cache survived: %s", identity)
	}
}

// A backup taken before the identity artifact existed has no record of it. The
// rollback must clear it, or the restored credential would stay labelled with the
// account the rollback just left.
func TestRollbackClearsUnrecordedIdentityArtifact(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main-uuid")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	seedClaude(t, app, sideToken, "side-uuid")
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, out)

	// Switching to main backs up the live (side) state.
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	// Rewrite that backup the way an older kae wrote it: credential only.
	meta, found, err := backup.Latest(app.Paths.BackupsDir())
	if err != nil || !found {
		t.Fatalf("latest backup: %v %v", found, err)
	}
	kept := meta.Artifacts[:0]
	for _, rec := range meta.Artifacts {
		if rec.Name != "oauth_account" {
			kept = append(kept, rec)
		}
	}
	meta.Artifacts = kept
	if err := backup.Save(app.Paths.BackupsDir(), meta); err != nil {
		t.Fatal(err)
	}

	code, out = captureStdout(t, func() int { return runRollback(ctx, app, opts, "") })
	mustExit(t, constants.ExitOK, code, out)

	if creds := readFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json")); !strings.Contains(creds, sideToken) {
		t.Fatalf("rollback did not restore the credential: %s", creds)
	}
	if identity := readFile(t, filepath.Join(app.Env.Home, ".claude.json")); strings.Contains(identity, "oauthAccount") {
		t.Fatalf("rollback left an unrestorable identity cache in place: %s", identity)
	}
}

// Rollback restores global state, so a kae-managed CLAUDE_CONFIG_DIR must not
// steer it. Today's adapter specs (used for the pre-rollback backup and to clear
// an artifact the backup never recorded) would otherwise resolve into the
// isolation tree, leaving the real home's identity cache stale.
func TestRollbackIgnoresPinnedConfigDir(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main-uuid")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	seedClaude(t, app, sideToken, "side-uuid")
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, out)
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	// Rewrite the backup as an older kae did (credential only), then run the
	// rollback as if the shell were inside a pinned directory.
	meta, found, err := backup.Latest(app.Paths.BackupsDir())
	if err != nil || !found {
		t.Fatalf("latest backup: %v %v", found, err)
	}
	kept := meta.Artifacts[:0]
	for _, rec := range meta.Artifacts {
		if rec.Name != "oauth_account" {
			kept = append(kept, rec)
		}
	}
	meta.Artifacts = kept
	if err := backup.Save(app.Paths.BackupsDir(), meta); err != nil {
		t.Fatal(err)
	}
	// A fresh App, as CmdRollback builds per process: the switch above already
	// normalized this app's scope, which would mask the env.
	pinnedConfig := app.Paths.SharedDir("pin-id", "claude")
	pinned := *app
	pinned.globalScope = false
	pinned.Env.Getenv = func(key string) string {
		if key == "CLAUDE_CONFIG_DIR" {
			return pinnedConfig
		}
		return ""
	}

	code, out = captureStdout(t, func() int { return runRollback(ctx, &pinned, opts, "") })
	mustExit(t, constants.ExitOK, code, out)

	if identity := readFile(t, filepath.Join(app.Env.Home, ".claude.json")); strings.Contains(identity, "oauthAccount") {
		t.Fatalf("the real home's identity cache was not cleared: %s", identity)
	}
	if _, err := os.Stat(filepath.Join(pinnedConfig, ".claude.json")); !os.IsNotExist(err) {
		t.Fatalf("rollback wrote into the isolation tree: %v", err)
	}
}

// claude rewrites /oauthAccount (at minimum its profileFetchedAt) on any login,
// so an identity-only difference must not read as "the login changed auth" —
// that would defeat the auth_unchanged guard for a re-login to the same account.
func TestLoginChangedAuthIgnoresIdentityArtifact(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()

	seedClaude(t, app, mainToken, "main-uuid")
	be, err := app.secretBackend()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := app.planTool(ctx, "claude", "main")
	if err != nil {
		t.Fatal(err)
	}
	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	meta, err := app.createBackup(ctx, be, []toolPlan{plan}, st, "login")
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(app.Env.Home, ".claude.json"),
		`{"oauthAccount":{"accountUuid":"main-uuid","profileFetchedAt":999},"projects":{}}`)

	changed, err := loginChangedAuth(ctx, be, meta, plan)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("an identity-only change must not count as an auth change")
	}
}

// The identity cache outlives a logout, so a ~/.claude.json carrying
// /oauthAccount with no credential is not a capturable login: capture must
// still refuse, or the snapshot would log the user out when applied.
func TestCaptureRefusesIdentityCacheWithoutCredential(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	writeFile(t, filepath.Join(app.Env.Home, ".claude.json"),
		`{"oauthAccount":{"emailAddress":"you@example.com"},"projects":{}}`)
	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText}, "claude", "main")
	})
	mustExit(t, constants.ExitAuthMissing, code, out)
}

// A snapshot captured before the oauth_account artifact existed still applies:
// the artifact is identity-only, so the switch removes the stale identity cache
// instead of failing, and Claude Code refetches the profile from the applied
// token. Removal must not disturb the other keys of the mixed-state file.
func TestSwitchAppliesSnapshotWithoutIdentityArtifact(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main-uuid")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	dir := app.Paths.AccountDir("claude", "main")
	acc, found, err := account.Load(dir)
	if err != nil || !found {
		t.Fatalf("load snapshot: %v %v", found, err)
	}
	delete(acc.Artifacts, "oauth_account")
	if err := account.Save(dir, acc); err != nil {
		t.Fatal(err)
	}

	seedClaude(t, app, sideToken, "side-uuid")
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	identity := readFile(t, filepath.Join(app.Env.Home, ".claude.json"))
	if strings.Contains(identity, "oauthAccount") {
		t.Fatalf("stale identity cache survived the switch: %s", identity)
	}
	if !strings.Contains(identity, `"projects"`) || !strings.Contains(identity, `"firstStartTime"`) {
		t.Fatalf("mixed-state keys lost: %s", identity)
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

// With the keyring store, capture/use round-trip through the single
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

		// codex logged in as main in this codex home: one keychain item, under the
		// account codex derives from CODEX_HOME. The sim answers any account here so
		// the derivation stays pinned in the codex package's golden test; what this
		// test pins is that capture and apply agree on one item.
		sim.present = true
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
		// The account the apply must use is the one the adapter derives for *this*
		// codex home, resolved the same way production resolves it (the derivation
		// itself is pinned in the codex package's golden test).
		derived := codexItemAccount(t, ctx, app)
		if !strings.HasPrefix(derived, "cli|") {
			t.Fatalf("adapter derived no keychain account: %q", derived)
		}
		// A re-login as side rewrote the item's payload. Its account did not change:
		// one codex home has one item, whatever account is logged in.
		sim.payload = `{"tokens":{"access_token":"side-access","refresh_token":"side-refresh"}}`
		if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "side") }); code != constants.ExitOK {
			t.Fatalf("capture side: %s", out)
		}
		// Switch back to main: upsert this home's item, and nothing else.
		sim.ops = nil // isolate the apply's keychain mutations
		if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "codex", "main") }); code != constants.ExitOK {
			t.Fatalf("switch to main: %s", out)
		}
		if !sim.present {
			t.Fatal("no Codex Auth item after switch")
		}
		// A single add, no delete: `Codex Auth` holds one item per CODEX_HOME, so a
		// delete on this path removed another codex home's login (shipped in v0.12.0).
		if len(sim.ops) != 1 || sim.ops[0] != "add" {
			t.Fatalf("expected a single add on apply, got %v", sim.ops)
		}
		if sim.account != derived {
			t.Fatalf("item account = %q, want the derived %q", sim.account, derived)
		}
		if !strings.Contains(sim.payload, "main-access") || strings.Contains(sim.payload, "side-access") {
			t.Fatalf("item payload not restored to main verbatim: %s", sim.payload)
		}
	})
}

// codexItemAccount returns the account attribute codex's adapter derives for the
// codex home app's environment names — the value an apply must address the
// `Codex Auth` item by.
func codexItemAccount(t *testing.T, ctx context.Context, app *App) string {
	t.Helper()
	adp, err := adapter.ForTool(constants.ToolCodex)
	if err != nil {
		t.Fatal(err)
	}
	specs, err := adp.Artifacts(ctx, app.Env)
	if err != nil {
		t.Fatal(err)
	}
	for _, sp := range specs {
		if sp.Kind == constants.KindKeychain {
			return sp.KeychainAccount
		}
	}
	t.Fatal("codex declared no keychain artifact")
	return ""
}

// A `Codex Auth` item belonging to *another* CODEX_HOME is not this home's
// credential: capture must report none rather than storing a stranger's login
// under this home's account. kae used to read the service's first item, so a
// second codex home's credential was capturable — and then written back here.
func TestCodexKeyringForeignHomeItemNotCaptured(t *testing.T) {
	sim := &keychainSim{
		present: true,
		account: "cli|0000000000000000", // some other CODEX_HOME's item
		payload: `{"tokens":{"access_token":"other-home-access"}}`,
	}
	runner.With(sim, func() {
		app := testApp(t, nil)
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		writeFile(t, filepath.Join(app.Env.Home, ".codex", "config.toml"),
			"cli_auth_credentials_store = \"keyring\"\n")
		code, out := captureStdout(t, func() int { return runCapture(ctx, app, commonOpts{Format: formatText}, "codex", "main") })
		if code == constants.ExitOK {
			t.Fatalf("expected capture to find no credential for this codex home: %s", out)
		}
		if strings.Contains(out, "other-home-access") {
			t.Fatalf("another home's payload reached the output: %s", out)
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

// On macOS, agy capture/use round-trip through the gemini/antigravity
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

// An empty keychain payload is refused at capture (structure guard:
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
