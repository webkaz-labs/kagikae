package paths

import (
	"path/filepath"
	"testing"
)

func TestResolveDefaults(t *testing.T) {
	p := Resolve(func(string) string { return "" }, "/home/u")
	if p.ConfigDir != "/home/u/.config/kagikae" ||
		p.DataDir != "/home/u/.local/share/kagikae" ||
		p.StateDir != "/home/u/.local/state/kagikae" {
		t.Fatalf("unexpected defaults: %+v", p)
	}
	if p.RuntimeDir != p.StateDir {
		t.Fatalf("runtime dir should fall back to state dir: %+v", p)
	}
	if p.ConfigFile() != "/home/u/.config/kagikae/config.toml" {
		t.Fatalf("config file: %s", p.ConfigFile())
	}
	if p.AccountDir("claude", "work") != "/home/u/.local/share/kagikae/accounts/claude/work" {
		t.Fatalf("account dir: %s", p.AccountDir("claude", "work"))
	}
}

func TestResolveXDGOverrides(t *testing.T) {
	env := map[string]string{
		"XDG_CONFIG_HOME": "/x/cfg",
		"XDG_DATA_HOME":   "/x/data",
		"XDG_STATE_HOME":  "/x/state",
		"XDG_RUNTIME_DIR": "/run/u",
	}
	p := Resolve(func(key string) string { return env[key] }, "/home/u")
	if p.ConfigDir != "/x/cfg/kagikae" || p.RuntimeDir != "/run/u/kagikae" {
		t.Fatalf("XDG not honored: %+v", p)
	}
	if p.LocksDir() != filepath.Join("/run/u/kagikae", "locks") {
		t.Fatalf("locks dir: %s", p.LocksDir())
	}
}

func TestResolveIgnoresRelativeXDG(t *testing.T) {
	env := map[string]string{"XDG_CONFIG_HOME": "relative/path"}
	p := Resolve(func(key string) string { return env[key] }, "/home/u")
	if p.ConfigDir != "/home/u/.config/kagikae" {
		t.Fatalf("relative XDG must be ignored per spec: %+v", p)
	}
}
