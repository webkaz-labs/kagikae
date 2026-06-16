package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadFromString(t *testing.T, content string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	_ = cfg
	return cfg, err
}

func TestIsolatedSharedItemsValidation(t *testing.T) {
	valid := "version = 1\n[tools.claude]\nisolated_shared_items = [\"output-styles\", \"statusline.json\"]\n"
	cfg, err := loadFromString(t, valid)
	if err != nil {
		t.Fatalf("valid isolated-shared list rejected: %v", err)
	}
	got := cfg.IsolatedSharedItems("claude")
	if len(got) != 2 || got[0] != "output-styles" {
		t.Fatalf("accessor: %v", got)
	}
	if cfg.IsolatedSharedItems("codex") != nil {
		t.Fatal("unset tool must return nil")
	}

	for name, content := range map[string]string{
		"path separator": "version = 1\n[tools.claude]\nisolated_shared_items = [\"a/b\"]\n",
		"dot-dot":        "version = 1\n[tools.claude]\nisolated_shared_items = [\"..\"]\n",
		"credentials":    "version = 1\n[tools.claude]\nisolated_shared_items = [\".credentials.json\"]\n",
		"codex auth":     "version = 1\n[tools.codex]\nisolated_shared_items = [\"auth.json\"]\n",
	} {
		if _, err := loadFromString(t, content); err == nil {
			t.Fatalf("%s must be rejected", name)
		} else if name == "credentials" && !strings.Contains(err.Error(), "auth") {
			t.Fatalf("refusal message: %v", err)
		}
	}

	// .claude.json is not an auth artifact (it is a token-derived cache that
	// claude self-heals), so it may be listed in isolated_shared_items.
	allowedIdentity := "version = 1\n[tools.claude]\nisolated_shared_items = [\".claude.json\"]\n"
	if _, err := loadFromString(t, allowedIdentity); err != nil {
		t.Fatalf(".claude.json must be allowed in isolated_shared_items: %v", err)
	}
}
