package config

import (
	"strings"
	"testing"
)

const writerSample = `# kagikae config
version = 1
default_profile = "main"

[security]
# keep this comment
secret_backend = "auto"
backup_keep = 30

[tools.claude]
enabled = true

[profiles.main]
label = "Main"
[profiles.main.accounts]
claude = "main"   # trailing comment
codex = "main"

[profiles.side.accounts]
claude = "side"
`

func loadString(t *testing.T, content string) *Config {
	t.Helper()
	cfg, _, err := Load(writeConfig(t, content))
	if err != nil {
		t.Fatalf("reload edited config: %v\n%s", err, content)
	}
	return cfg
}

func edit(t *testing.T, src string, mutate func(*Editor)) string {
	t.Helper()
	e, err := NewEditor([]byte(src))
	if err != nil {
		t.Fatalf("NewEditor: %v", err)
	}
	mutate(e)
	out, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	return string(out)
}

func TestEditorPreservesCommentsAndUnrelatedKeys(t *testing.T) {
	out := edit(t, writerSample, func(e *Editor) {
		e.SetProfileAccount("main", "claude", "side")
	})
	for _, want := range []string{
		"# kagikae config",    // leading comment survives
		"# keep this comment", // unrelated section comment survives
		"[tools.claude]",      // unrelated table survives
		"enabled = true",      // unrelated key survives
		"# trailing comment",  // trailing comment survives
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("edit dropped %q:\n%s", want, out)
		}
	}
	// Re-loading the edited content must reflect the change and stay valid.
	cfg := loadString(t, out)
	if got := cfg.Profiles["main"].Accounts["claude"]; got != "side" {
		t.Fatalf("claude account not updated: %q", got)
	}
	if cfg.Profiles["main"].Accounts["codex"] != "main" {
		t.Fatalf("sibling key lost: %+v", cfg.Profiles["main"])
	}
}

func TestEditorCreatesProfileAccountTable(t *testing.T) {
	out := edit(t, writerSample, func(e *Editor) {
		e.SetProfileAccount("ci", "claude", "alt")
	})
	cfg := loadString(t, out)
	if got := cfg.Profiles["ci"].Accounts["claude"]; got != "alt" {
		t.Fatalf("new profile not created: %+v", cfg.Profiles)
	}
}

func TestEditorRemoveProfileAccount(t *testing.T) {
	out := edit(t, writerSample, func(e *Editor) {
		if !e.RemoveProfileAccount("main", "codex") {
			t.Fatal("RemoveProfileAccount reported no change")
		}
	})
	cfg := loadString(t, out)
	if _, ok := cfg.Profiles["main"].Accounts["codex"]; ok {
		t.Fatalf("codex mapping not removed: %+v", cfg.Profiles["main"])
	}
	if cfg.Profiles["main"].Accounts["claude"] != "main" {
		t.Fatalf("sibling key lost: %+v", cfg.Profiles["main"])
	}
}

func TestEditorRemoveProfile(t *testing.T) {
	// Remove the non-default profile so the document stays valid (clearing a
	// dangling default_profile is the command layer's job, not the editor's).
	out := edit(t, writerSample, func(e *Editor) {
		if !e.RemoveProfile("side") {
			t.Fatal("RemoveProfile reported no change")
		}
	})
	if strings.Contains(out, "[profiles.side") {
		t.Fatalf("profile section survived:\n%s", out)
	}
	cfg := loadString(t, out)
	if _, ok := cfg.Profiles["side"]; ok {
		t.Fatalf("side profile not removed: %+v", cfg.Profiles)
	}
	if _, ok := cfg.Profiles["main"]; !ok {
		t.Fatalf("unrelated profile lost: %+v", cfg.Profiles)
	}
}

func TestEditorSetAndClearDefaultProfile(t *testing.T) {
	set := edit(t, writerSample, func(e *Editor) { e.SetDefaultProfile("side") })
	if cfg := loadString(t, set); cfg.DefaultProfile != "side" {
		t.Fatalf("default not set: %q", cfg.DefaultProfile)
	}
	clear := edit(t, writerSample, func(e *Editor) { e.SetDefaultProfile("") })
	if cfg := loadString(t, clear); cfg.DefaultProfile != "" {
		t.Fatalf("default not cleared: %q", cfg.DefaultProfile)
	}
	// Setting a default when none exists adds the key.
	noDefault := "version = 1\n[profiles.main.accounts]\nclaude = \"main\"\n"
	added := edit(t, noDefault, func(e *Editor) { e.SetDefaultProfile("main") })
	if cfg := loadString(t, added); cfg.DefaultProfile != "main" {
		t.Fatalf("default not added: %q", cfg.DefaultProfile)
	}
}
