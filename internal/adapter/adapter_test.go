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
	"github.com/webkaz-labs/kagikae/internal/adapter/gemini"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

var (
	claudeAdapter = claude.Claude{}
	codexAdapter  = codex.Codex{}
	geminiAdapter = gemini.Gemini{}
	agyAdapter    = agy.Agy{}
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
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs: %+v", specs)
	}
	if specs[0].Kind != constants.KindJSONPointer ||
		specs[0].Target != filepath.Join(env.Home, ".claude", ".credentials.json") ||
		specs[0].Pointer != "/claudeAiOauth" {
		t.Fatalf("unexpected credentials spec: %+v", specs[0])
	}
	if specs[1].Target != filepath.Join(env.Home, ".claude.json") || specs[1].Pointer != "/oauthAccount" {
		t.Fatalf("unexpected identity spec: %+v", specs[1])
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
	if specs[1].Target != filepath.Join(configDir, ".claude.json") {
		t.Fatalf("identity file should follow CLAUDE_CONFIG_DIR: %+v", specs[1])
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

func TestGeminiArtifactsAndDoctor(t *testing.T) {
	env := testEnv(t, "linux", nil)
	env.WarnAntigravityTransition = true
	specs, err := geminiAdapter.Artifacts(context.Background(), env)
	if err != nil || len(specs) != 2 {
		t.Fatalf("unexpected: %+v %v", specs, err)
	}
	write(t, filepath.Join(env.Home, ".gemini", "oauth_creds.json"), `{"access_token":"x"}`)
	write(t, filepath.Join(env.Home, ".gemini", "settings.json"),
		`{"security":{"auth":{"selectedType":"oauth-personal"}}}`)
	info, err := geminiAdapter.Detect(context.Background(), env)
	if err != nil || !info.AuthPresent {
		t.Fatalf("unexpected: %+v %v", info, err)
	}
	checks := geminiAdapter.Doctor(context.Background(), env)
	var hasTransition, hasAuthType bool
	for _, check := range checks {
		if check.Code == constants.CheckTransitionNotice {
			hasTransition = true
		}
		if check.Code == constants.CheckCredentialStore && strings.Contains(check.Message, "oauth-personal") {
			hasAuthType = true
		}
	}
	if !hasTransition || !hasAuthType {
		t.Fatalf("missing doctor checks: %+v", checks)
	}

	env.WarnAntigravityTransition = false
	for _, check := range geminiAdapter.Doctor(context.Background(), env) {
		if check.Code == constants.CheckTransitionNotice {
			t.Fatal("transition notice should be suppressed")
		}
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
