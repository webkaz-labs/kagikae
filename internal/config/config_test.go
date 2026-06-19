package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissingFileYieldsDefaults(t *testing.T) {
	cfg, warnings, err := Load(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || len(warnings) != 0 {
		t.Fatalf("unexpected: %v %v", warnings, err)
	}
	if cfg.Security.SecretBackend != "auto" || cfg.Security.BackupKeep != DefaultBackupKeep {
		t.Fatalf("unexpected defaults: %+v", cfg.Security)
	}
	if !cfg.ToolEnabled("claude") {
		t.Fatal("defaults should enable tools")
	}
}

func TestLoadFullConfig(t *testing.T) {
	path := writeConfig(t, `
version = 1
default_profile = "side"

[security]
secret_backend = "file"
backup_keep = 5

[tools.claude]
enabled = false

[profiles.main]
label = "Main"
[profiles.main.accounts]
claude = "main"
codex = "main"

[profiles.side]
[profiles.side.accounts]
claude = "side"
`)
	cfg, warnings, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if cfg.ToolEnabled("claude") {
		t.Fatal("claude should be disabled")
	}
	if cfg.ToolEnabled("codex") == false {
		t.Fatal("codex should default to enabled")
	}
	if cfg.Profiles["main"].Accounts["codex"] != "main" {
		t.Fatalf("profile mapping lost: %+v", cfg.Profiles)
	}
	if got := cfg.ProfileNames(); len(got) != 2 || got[0] != "main" || got[1] != "side" {
		t.Fatalf("unexpected profile names: %v", got)
	}
}

func TestLoadUnknownKeyWarns(t *testing.T) {
	path := writeConfig(t, "version = 1\nbogus_key = true\n")
	_, warnings, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "bogus_key") {
		t.Fatalf("expected unknown-key warning, got %v", warnings)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]string{
		"newer version":      "version = 2\n",
		"bad backup_keep":    "[security]\nbackup_keep = 0\n",
		"unknown tool":       "[tools.vscode]\nenabled = true\n",
		"bad profile name":   "[profiles.\"a b\"]\nlabel = \"x\"\n",
		"unknown tool map":   "[profiles.p.accounts]\nvscode = \"x\"\n",
		"bad account name":   "[profiles.p.accounts]\nclaude = \"a b\"\n",
		"bad default":        "default_profile = \"nope\"\n",
		"invalid toml":       "version = [\n",
		"driver wrong value": "[tools.claude]\ndriver = \"keychain\"\n",
		"driver wrong tool":  "[tools.codex]\ndriver = \"file\"\n",
	}
	for name, content := range cases {
		if _, _, err := Load(writeConfig(t, content)); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestMatchProfile(t *testing.T) {
	cfg := Default()
	cfg.Profiles["main"] = Profile{Accounts: map[string]string{"claude": "main", "codex": "main"}}
	cfg.Profiles["side"] = Profile{Accounts: map[string]string{"claude": "side"}}
	if got := cfg.MatchProfile(map[string]string{"claude": "main", "codex": "main"}); got != "main" {
		t.Fatalf("expected main, got %q", got)
	}
	if got := cfg.MatchProfile(map[string]string{"claude": "side", "codex": "main"}); got != "side" {
		t.Fatalf("expected side (subset match), got %q", got)
	}
	if got := cfg.MatchProfile(map[string]string{"claude": "other"}); got != "" {
		t.Fatalf("expected no match, got %q", got)
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"main", "side-2", "a.b_c"} {
		if !ValidName(ok) {
			t.Fatalf("expected valid: %q", ok)
		}
	}
	for _, bad := range []string{"", "a b", "a/b", strings.Repeat("x", 65), "日本語"} {
		if ValidName(bad) {
			t.Fatalf("expected invalid: %q", bad)
		}
	}
}

func TestInitialContentParses(t *testing.T) {
	path := writeConfig(t, InitialContent(""))
	cfg, warnings, err := Load(path)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("initial content invalid: %v %v", warnings, err)
	}
	if cfg.Security.SecretBackend != "auto" {
		t.Fatalf("unexpected: %+v", cfg.Security)
	}
}

func TestClaudeDriverConfigOption(t *testing.T) {
	path := writeConfig(t, "[tools.claude]\ndriver = \"file\"\n")
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("valid driver option: %v", err)
	}
	if cfg.Tools["claude"].Driver != "file" {
		t.Fatalf("driver option not loaded: %+v", cfg.Tools["claude"])
	}
}

func TestSharedDenylistExtraValidation(t *testing.T) {
	// Valid extra items are accepted.
	path := writeConfig(t, "[tools.claude]\nshared_denylist_extra = [\"custom-session.json\"]\n")
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("valid shared_denylist_extra: %v", err)
	}
	if got := cfg.SharedDenylistExtra("claude"); len(got) != 1 || got[0] != "custom-session.json" {
		t.Fatalf("unexpected SharedDenylistExtra: %v", got)
	}

	// Hard-coded auth artifacts must be rejected.
	for _, bad := range []string{".credentials.json", "auth.json"} {
		content := "[tools.claude]\nshared_denylist_extra = [\"" + bad + "\"]\n"
		if _, _, err := Load(writeConfig(t, content)); err == nil {
			t.Fatalf("expected error for hard-coded artifact %q", bad)
		}
	}

	// Invalid file names must be rejected.
	badContent := "[tools.claude]\nshared_denylist_extra = [\"a/b\"]\n"
	if _, _, err := Load(writeConfig(t, badContent)); err == nil {
		t.Fatal("expected error for non-bare file name")
	}
}

// TestRenamedConfigKeysFailAtLoad asserts the v0.8.0 hard break: the renamed
// per-tool keys error at load naming their replacement, and the removed
// overlay/home keys error too (docs/RELEASE.md). Pre-1.0, no silent acceptance.
func TestRenamedConfigKeysFailAtLoad(t *testing.T) {
	cases := map[string]string{
		"shared_denylist_extra": "[tools.claude]\nbond_denylist_extra = [\"x\"]\n",
		"isolated_shared_items": "[tools.claude]\npin_shared_items = [\"x\"]\n",
	}
	for newKey, content := range cases {
		_, _, err := Load(writeConfig(t, content))
		if err == nil || !strings.Contains(err.Error(), newKey) {
			t.Fatalf("renamed key must fail at load naming %q, got %v", newKey, err)
		}
	}
	for _, removed := range []string{
		"[tools.claude]\noverlay_extra_shared = [\"x\"]\n",
		"[tools.claude]\noverlay_mode_enabled = false\n",
		"[tools.claude]\nhome_mode_enabled = false\n",
	} {
		if _, _, err := Load(writeConfig(t, removed)); err == nil {
			t.Fatalf("removed key must fail at load: %s", removed)
		}
	}
}
