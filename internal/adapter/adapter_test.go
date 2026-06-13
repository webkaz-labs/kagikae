package adapter_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/adapter/agy"
	"github.com/webkaz-labs/kagikae/internal/adapter/claude"
	"github.com/webkaz-labs/kagikae/internal/adapter/codex"
	"github.com/webkaz-labs/kagikae/internal/adapter/copilot"
	"github.com/webkaz-labs/kagikae/internal/adapter/cursor"
	"github.com/webkaz-labs/kagikae/internal/adapter/opencode"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

var (
	claudeAdapter   = claude.Claude{}
	codexAdapter    = codex.Codex{}
	agyAdapter      = agy.Agy{}
	opencodeAdapter = opencode.Opencode{}
	cursorAdapter   = cursor.Cursor{}
	copilotAdapter  = copilot.Copilot{}
)

func testEnv(t *testing.T, goos string, vars map[string]string) adapter.Env {
	t.Helper()
	home := t.TempDir()
	return adapter.Env{
		GOOS: goos,
		Home: home,
		Getenv: func(key string) string {
			return vars[key]
		},
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryHasAllTools(t *testing.T) {
	for _, tool := range constants.Tools {
		a, err := adapter.ForTool(tool)
		if err != nil || a.ID() != tool {
			t.Fatalf("adapter for %s: %v", tool, err)
		}
	}
	if _, err := adapter.ForTool("vscode"); err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestClaudeArtifactsLinux(t *testing.T) {
	env := testEnv(t, "linux", nil)
	specs, err := claudeAdapter.Artifacts(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec: %+v", specs)
	}
	if specs[0].Kind != constants.KindJSONPointer ||
		specs[0].Target != filepath.Join(env.Home, ".claude", ".credentials.json") ||
		specs[0].Pointer != "/claudeAiOauth" {
		t.Fatalf("unexpected credentials spec: %+v", specs[0])
	}
}

func TestClaudeArtifactsDarwin(t *testing.T) {
	env := testEnv(t, "darwin", map[string]string{"USER": "alice"})
	specs, err := claudeAdapter.Artifacts(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].Kind != constants.KindKeychain || specs[0].Target != claude.KeychainService {
		t.Fatalf("unexpected keychain spec: %+v", specs[0])
	}
	if specs[0].KeychainAccount != "alice" {
		t.Fatalf("fallback account not propagated: %+v", specs[0])
	}
}

func TestClaudeArtifactsWindowsUnsupported(t *testing.T) {
	env := testEnv(t, "windows", nil)
	if _, err := claudeAdapter.Artifacts(context.Background(), env); !errors.Is(err, adapter.ErrUnsupported) {
		t.Fatalf("expected unsupported: %v", err)
	}
}

func TestClaudeHonorsConfigDir(t *testing.T) {
	configDir := t.TempDir()
	env := testEnv(t, "linux", map[string]string{"CLAUDE_CONFIG_DIR": configDir})
	specs, err := claudeAdapter.Artifacts(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].Target != filepath.Join(configDir, ".credentials.json") {
		t.Fatalf("CLAUDE_CONFIG_DIR not honored: %+v", specs[0])
	}
}

func TestClaudeDetectLinux(t *testing.T) {
	env := testEnv(t, "linux", map[string]string{"ANTHROPIC_API_KEY": "sk-x"})
	write(t, filepath.Join(env.Home, ".claude", ".credentials.json"),
		`{"claudeAiOauth":{"accessToken":"tok"}}`)
	info, err := claudeAdapter.Detect(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if !info.AuthPresent || info.Driver != constants.DriverClaudeFilePatch {
		t.Fatalf("unexpected info: %+v", info)
	}
	if len(info.Warnings) != 1 || !strings.Contains(info.Warnings[0], "ANTHROPIC_API_KEY") {
		t.Fatalf("expected env conflict warning: %+v", info.Warnings)
	}
}

func TestCodexArtifactsAndKeyringRefusal(t *testing.T) {
	env := testEnv(t, "linux", nil)
	specs, err := codexAdapter.Artifacts(context.Background(), env)
	if err != nil || len(specs) != 1 || specs[0].Kind != constants.KindFile {
		t.Fatalf("unexpected: %+v %v", specs, err)
	}
	write(t, filepath.Join(env.Home, ".codex", "config.toml"),
		"cli_auth_credentials_store = \"keyring\"\n")
	if _, err := codexAdapter.Artifacts(context.Background(), env); !errors.Is(err, artifact.ErrUnsafe) {
		t.Fatalf("expected keyring refusal: %v", err)
	}
	checks := codexAdapter.Doctor(context.Background(), env)
	foundError := false
	for _, check := range checks {
		if check.Code == constants.CheckCredentialStore && check.Status == constants.StatusError {
			foundError = true
		}
	}
	if !foundError {
		t.Fatalf("doctor should flag keyring store: %+v", checks)
	}
}

func TestCodexHonorsCodexHome(t *testing.T) {
	codexHome := t.TempDir()
	env := testEnv(t, "linux", map[string]string{"CODEX_HOME": codexHome})
	write(t, filepath.Join(codexHome, "auth.json"), `{"tokens":{}}`)
	info, err := codexAdapter.Detect(context.Background(), env)
	if err != nil || !info.AuthPresent {
		t.Fatalf("CODEX_HOME not honored: %+v %v", info, err)
	}
}

func TestCodexDetectMissingAuthWarnsAboutKeyring(t *testing.T) {
	env := testEnv(t, "linux", nil)
	info, err := codexAdapter.Detect(context.Background(), env)
	if err != nil || info.AuthPresent {
		t.Fatalf("unexpected: %+v %v", info, err)
	}
	if len(info.Warnings) != 1 || !strings.Contains(info.Warnings[0], "keyring") {
		t.Fatalf("expected keyring-possibility warning: %+v", info.Warnings)
	}
}

func TestOpencodeArtifactsAndXDGDataHome(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	specs, err := opencodeAdapter.Artifacts(context.Background(), env)
	if err != nil || len(specs) != 1 {
		t.Fatalf("unexpected specs: %+v %v", specs, err)
	}
	if specs[0].Kind != constants.KindJSONPointer ||
		specs[0].Target != filepath.Join(env.Home, ".local", "share", "opencode", "auth.json") ||
		specs[0].Pointer != "/openai" {
		t.Fatalf("unexpected auth spec: %+v", specs[0])
	}

	dataHome := t.TempDir()
	env = testEnv(t, "darwin", map[string]string{"XDG_DATA_HOME": dataHome})
	specs, err = opencodeAdapter.Artifacts(context.Background(), env)
	if err != nil || specs[0].Target != filepath.Join(dataHome, "opencode", "auth.json") {
		t.Fatalf("XDG_DATA_HOME not honored: %+v %v", specs, err)
	}

	// A relative value is ignored per the XDG spec (paths.XDGDataHome).
	env = testEnv(t, "darwin", map[string]string{"XDG_DATA_HOME": "relative/data"})
	specs, err = opencodeAdapter.Artifacts(context.Background(), env)
	if err != nil || specs[0].Target != filepath.Join(env.Home, ".local", "share", "opencode", "auth.json") {
		t.Fatalf("relative XDG_DATA_HOME must fall back to the default: %+v %v", specs, err)
	}
}

func TestOpencodeDetect(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	info, err := opencodeAdapter.Detect(context.Background(), env)
	if err != nil || info.AuthPresent {
		t.Fatalf("expected no auth without auth.json: %+v %v", info, err)
	}
	if len(info.Warnings) != 0 {
		t.Fatalf("missing auth.json must not warn: %+v", info.Warnings)
	}

	authPath := filepath.Join(env.Home, ".local", "share", "opencode", "auth.json")

	// API-key-only auth.json: no subscription login, explanatory warning.
	write(t, authPath, `{"openrouter":{"type":"api","key":"sk-x"}}`)
	info, err = opencodeAdapter.Detect(context.Background(), env)
	if err != nil || info.AuthPresent {
		t.Fatalf("expected no auth without an openai entry: %+v %v", info, err)
	}
	if len(info.Warnings) != 1 || !strings.Contains(info.Warnings[0], "openai") {
		t.Fatalf("expected missing-openai warning: %+v", info.Warnings)
	}

	write(t, authPath, `{"openai":{"type":"oauth","refresh":"r","access":"a"},"openrouter":{"type":"api","key":"sk-x"}}`)
	info, err = opencodeAdapter.Detect(context.Background(), env)
	if err != nil || !info.AuthPresent || info.Driver != constants.DriverOpencodeFilePatch {
		t.Fatalf("unexpected: %+v %v", info, err)
	}
}

func TestOpencodeRefusesUnrecognizedAuthJSON(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	specs, err := opencodeAdapter.Artifacts(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	authPath := specs[0].Target

	// Malformed auth.json: reading refuses instead of misparsing.
	write(t, authPath, `not json`)
	if _, err := artifact.ReadLive(context.Background(), specs[0]); !errors.Is(err, artifact.ErrUnsafe) {
		t.Fatalf("expected structure-guard refusal: %v", err)
	}
	checks := opencodeAdapter.Doctor(context.Background(), env)
	foundError := false
	for _, check := range checks {
		if check.Code == constants.CheckAuthPresent && check.Status == constants.StatusError {
			foundError = true
		}
	}
	if !foundError {
		t.Fatalf("doctor should flag the unrecognized auth.json: %+v", checks)
	}

	// Non-object root: applying refuses instead of replacing the file.
	write(t, authPath, `["not","an","object"]`)
	err = artifact.ApplyLive(context.Background(), specs[0],
		artifact.Value{Data: []byte(`{"type":"oauth"}`), Present: true})
	if !errors.Is(err, artifact.ErrUnsafe) {
		t.Fatalf("expected apply refusal on non-object root: %v", err)
	}
}

func TestCursorArtifactsDarwinOpaqueKeychain(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	specs, err := cursorAdapter.Artifacts(context.Background(), env)
	if err != nil || len(specs) != 1 {
		t.Fatalf("unexpected specs: %+v %v", specs, err)
	}
	if specs[0].Kind != constants.KindKeychain || specs[0].Target != cursor.KeychainService {
		t.Fatalf("unexpected keychain spec: %+v", specs[0])
	}
	// An empty pointer marks the opaque (raw-JWT) payload.
	if specs[0].Pointer != "" || specs[0].KeychainAccount != cursor.KeychainAccount {
		t.Fatalf("opaque spec must carry an empty pointer and the cursor-user account: %+v", specs[0])
	}
}

func TestCursorUnsupportedOffDarwin(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		env := testEnv(t, goos, nil)
		if _, err := cursorAdapter.Artifacts(context.Background(), env); !errors.Is(err, adapter.ErrUnsupported) {
			t.Fatalf("%s: expected unsupported: %v", goos, err)
		}
		checks := cursorAdapter.Doctor(context.Background(), env)
		if len(checks) != 1 || checks[0].Code != constants.CheckUnsupported || checks[0].Status != constants.StatusError {
			t.Fatalf("%s: doctor must report a single unsupported error: %+v", goos, checks)
		}
	}
}

const copilotConfigFixture = `// User settings belong in settings.json.
// This file is managed automatically.
{
  "trustedFolders": ["/workspaces"],
  "lastLoggedInUser": {"host":"https://github.com","login":"webkaz"},
  "loggedInUsers": [{"host":"https://github.com","login":"webkaz"}]
}
`

func TestCopilotArtifactsJSONCPointer(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	specs, err := copilotAdapter.Artifacts(context.Background(), env)
	if err != nil || len(specs) != 1 {
		t.Fatalf("unexpected specs: %+v %v", specs, err)
	}
	if specs[0].Kind != constants.KindJSONPointer || specs[0].Pointer != "/lastLoggedInUser" || !specs[0].JSONC {
		t.Fatalf("expected a JSONC json-pointer spec: %+v", specs[0])
	}
	if specs[0].Target != filepath.Join(env.Home, ".copilot", "config.json") {
		t.Fatalf("unexpected target: %+v", specs[0])
	}
}

func TestCopilotDetect(t *testing.T) {
	env := testEnv(t, "linux", nil)
	info, err := copilotAdapter.Detect(context.Background(), env)
	if err != nil || info.AuthPresent {
		t.Fatalf("no config.json should mean no auth: %+v %v", info, err)
	}

	cfg := filepath.Join(env.Home, ".copilot", "config.json")
	write(t, cfg, copilotConfigFixture)
	info, err = copilotAdapter.Detect(context.Background(), env)
	if err != nil || !info.AuthPresent || info.Driver != constants.DriverCopilotConfigPointer {
		t.Fatalf("unexpected: %+v %v", info, err)
	}

	// env override is warned about.
	env = testEnv(t, "linux", map[string]string{"GH_TOKEN": "x"})
	write(t, filepath.Join(env.Home, ".copilot", "config.json"), copilotConfigFixture)
	info, _ = copilotAdapter.Detect(context.Background(), env)
	warned := false
	for _, w := range info.Warnings {
		if strings.Contains(w, "GH_TOKEN") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("expected GH_TOKEN warning: %+v", info.Warnings)
	}
}

func TestCopilotRefusesBrokenConfig(t *testing.T) {
	env := testEnv(t, "linux", nil)
	write(t, filepath.Join(env.Home, ".copilot", "config.json"), `// c`+"\n"+`{not json`)
	checks := copilotAdapter.Doctor(context.Background(), env)
	foundError := false
	for _, check := range checks {
		if check.Code == constants.CheckAuthPresent && check.Status == constants.StatusError {
			foundError = true
		}
	}
	if !foundError {
		t.Fatalf("doctor should flag the unparseable config: %+v", checks)
	}
}

func TestAgyExperimentalFileSnapshot(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	specs, err := agyAdapter.Artifacts(context.Background(), env)
	if err != nil || len(specs) != 3 {
		t.Fatalf("unexpected specs: %+v %v", specs, err)
	}
	if specs[0].Kind != constants.KindFile ||
		specs[0].Target != filepath.Join(env.Home, ".gemini", "antigravity-cli", "credentials.enc") {
		t.Fatalf("unexpected spec: %+v", specs[0])
	}

	info, err := agyAdapter.Detect(context.Background(), env)
	if err != nil || info.AuthPresent {
		t.Fatalf("expected no auth: %+v %v", info, err)
	}

	// keyring-likely warning when the CLI dir exists without credential files
	write(t, filepath.Join(env.Home, ".gemini", "antigravity-cli", "settings.json"), `{}`)
	info, _ = agyAdapter.Detect(context.Background(), env)
	keyringWarned := false
	for _, warning := range info.Warnings {
		if strings.Contains(warning, "keyring") {
			keyringWarned = true
		}
	}
	if !keyringWarned {
		t.Fatalf("expected keyring warning: %+v", info.Warnings)
	}

	write(t, filepath.Join(env.Home, ".gemini", "antigravity-cli", "credentials.enc"), "opaque")
	info, _ = agyAdapter.Detect(context.Background(), env)
	if !info.AuthPresent || info.Driver != constants.DriverAgyFileSnapshot {
		t.Fatalf("unexpected: %+v", info)
	}
}
