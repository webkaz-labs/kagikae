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
default_profile = "personal"

[security]
secret_backend = "file"
backup_keep = 5

[tools.claude]
enabled = false

[profiles.work]
label = "Work"
[profiles.work.accounts]
claude = "work"
codex = "work"

[profiles.personal]
[profiles.personal.accounts]
claude = "personal"
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
	if cfg.Profiles["work"].Accounts["codex"] != "work" {
		t.Fatalf("profile mapping lost: %+v", cfg.Profiles)
	}
	if got := cfg.ProfileNames(); len(got) != 2 || got[0] != "personal" || got[1] != "work" {
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
	}
	for name, content := range cases {
		if _, _, err := Load(writeConfig(t, content)); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestMatchProfile(t *testing.T) {
	cfg := Default()
	cfg.Profiles["work"] = Profile{Accounts: map[string]string{"claude": "work", "codex": "work"}}
	cfg.Profiles["personal"] = Profile{Accounts: map[string]string{"claude": "personal"}}
	if got := cfg.MatchProfile(map[string]string{"claude": "work", "codex": "work"}); got != "work" {
		t.Fatalf("expected work, got %q", got)
	}
	if got := cfg.MatchProfile(map[string]string{"claude": "personal", "codex": "work"}); got != "personal" {
		t.Fatalf("expected personal (subset match), got %q", got)
	}
	if got := cfg.MatchProfile(map[string]string{"claude": "other"}); got != "" {
		t.Fatalf("expected no match, got %q", got)
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"work", "personal-2", "a.b_c"} {
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
