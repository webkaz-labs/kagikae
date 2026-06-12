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

func TestOverlayExtraSharedValidation(t *testing.T) {
	valid := "version = 1\n[tools.claude]\noverlay_extra_shared = [\"output-styles\", \"statusline.json\"]\n"
	cfg, err := loadFromString(t, valid)
	if err != nil {
		t.Fatalf("valid extra-shared list rejected: %v", err)
	}
	got := cfg.OverlayExtraShared("claude")
	if len(got) != 2 || got[0] != "output-styles" {
		t.Fatalf("accessor: %v", got)
	}
	if cfg.OverlayExtraShared("codex") != nil {
		t.Fatal("unset tool must return nil")
	}

	for name, content := range map[string]string{
		"path separator": "version = 1\n[tools.claude]\noverlay_extra_shared = [\"a/b\"]\n",
		"dot-dot":        "version = 1\n[tools.claude]\noverlay_extra_shared = [\"..\"]\n",
		"credentials":    "version = 1\n[tools.claude]\noverlay_extra_shared = [\".credentials.json\"]\n",
		"identity":       "version = 1\n[tools.claude]\noverlay_extra_shared = [\".claude.json\"]\n",
		"codex auth":     "version = 1\n[tools.codex]\noverlay_extra_shared = [\"auth.json\"]\n",
	} {
		if _, err := loadFromString(t, content); err == nil {
			t.Fatalf("%s must be rejected", name)
		} else if name == "credentials" && !strings.Contains(err.Error(), "auth/identity") {
			t.Fatalf("refusal message: %v", err)
		}
	}
}
