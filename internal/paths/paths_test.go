package paths

import (
	"path/filepath"
	"strings"
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

func TestPinIDStability(t *testing.T) {
	id := PinID("/Users/alice/projects/myapp")
	if len(id) != 16 {
		t.Fatalf("PinID must be 16 hex chars, got %d: %q", len(id), id)
	}
	if strings.TrimLeft(id, "0123456789abcdef") != "" {
		t.Fatalf("PinID must be lowercase hex: %q", id)
	}
}

func TestPinIDCollisionResistance(t *testing.T) {
	// Two different paths must not collide (SHA-256 prefix; near-zero
	// probability but worth asserting for the test suite to catch bugs).
	if PinID("/a/b") == PinID("/a/c") {
		t.Fatal("PinID collision on /a/b vs /a/c")
	}
	if PinID("/home/u/proj") == PinID("/home/u/proj2") {
		t.Fatal("PinID collision on similar paths")
	}
}

func TestIsolationPaths(t *testing.T) {
	p := Resolve(func(string) string { return "" }, "/home/u")
	iso := "/home/u/.local/share/kagikae/isolation"
	sync := "/home/u/.local/share/kagikae/synchomes"
	pin := "abcdef0123456789"
	tool := "claude"
	acct := "work"
	pre := iso + "/" + pin + "/" + tool

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"IsolationDir", p.IsolationDir(), iso},
		{"SharedDir", p.SharedDir(pin, tool), pre + "/shared"},
		{"IsolatedConfigDir", p.IsolatedConfigDir(pin, tool, acct), pre + "/isolated/" + acct + "/config"},
		{"SyncHomesDir", p.SyncHomesDir(), sync},
		{"SyncHomeDir", p.SyncHomeDir(tool, acct), sync + "/" + tool + "/" + acct},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}
