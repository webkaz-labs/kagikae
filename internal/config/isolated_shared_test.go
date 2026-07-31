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
		// .claude.json is refused on a different axis, and getting the axis wrong is
		// how it used to be *allowed* here: it is not an auth artifact (it is a
		// token-derived cache claude refetches once its 24h TTL lapses), so a rule
		// about credentials let it through. The reason it must stay private is
		// attribution — linked back to the real home, every isolated directory
		// displays whatever the real home displays whichever account it is logged in
		// as, which is exactly the gap v0.16.0 closed for the shared bind.
		"identity cache": "version = 1\n[tools.claude]\nisolated_shared_items = [\".claude.json\"]\n",
	} {
		if _, err := loadFromString(t, content); err == nil {
			t.Fatalf("%s must be rejected", name)
		} else if name == "credentials" && !strings.Contains(err.Error(), "auth") {
			t.Fatalf("refusal message: %v", err)
		}
	}

	// The two fields refuse the same set, and must keep doing so: the shared bind
	// denies these by denylist and the isolated bind by refusing the opt-in, so a
	// name added to one and forgotten in the other reopens the gap in that mode only.
	for item := range refusedIsolatedShare {
		if !refusedSharedDenylistExtra[item] {
			t.Errorf("%q is refused for isolated_shared_items but not for shared_denylist_extra", item)
		}
	}
	for item := range refusedSharedDenylistExtra {
		if _, ok := refusedIsolatedShare[item]; !ok {
			t.Errorf("%q is refused for shared_denylist_extra but not for isolated_shared_items", item)
		}
	}
}
