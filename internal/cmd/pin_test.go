package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

func overlayTestApp(t *testing.T) *App {
	t.Helper()
	app := testApp(t, nil)
	app.Config.Profiles = map[string]config.Profile{
		"work": {Accounts: map[string]string{
			constants.ToolClaude: "work",
			constants.ToolAgy:    "work",
		}},
	}
	// Shared items must exist in the real home to be linked.
	writeFile(t, filepath.Join(app.Env.Home, ".claude", "settings.json"), `{"theme":"dark"}`)
	if err := os.MkdirAll(filepath.Join(app.Env.Home, ".claude", "skills", "demo"), 0o700); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestRemoveLegacyMiseBlockKeepsTheRest(t *testing.T) {
	chdirTemp(t)
	writeFile(t, ".mise.toml",
		"[tasks.custom]\nrun = \"echo hi\"\n"+miseBlockStart+"\n[env]\nKAE_PROFILE = \"work\"\n"+miseBlockEnd+"\ntail = 1\n")
	removed, err := removeLegacyMiseBlock(".mise.toml")
	if err != nil || !removed {
		t.Fatalf("removeLegacyMiseBlock: removed=%v err=%v", removed, err)
	}
	rest := readFile(t, ".mise.toml")
	if strings.Contains(rest, miseBlockStart) || strings.Contains(rest, "KAE_PROFILE") {
		t.Fatalf("block not removed: %s", rest)
	}
	if !strings.Contains(rest, "tasks.custom") || !strings.Contains(rest, "tail = 1") {
		t.Fatalf("must keep everything else: %s", rest)
	}

	// Without a block (or file) it is a no-op, not an error (the fragment is
	// now the primary binding).
	if removed, err := removeLegacyMiseBlock(".mise.toml"); err != nil || removed {
		t.Fatalf("absent block must be a no-op: removed=%v err=%v", removed, err)
	}
	if err := os.Remove(".mise.toml"); err != nil {
		t.Fatal(err)
	}
	if removed, err := removeLegacyMiseBlock(".mise.toml"); err != nil || removed {
		t.Fatalf("missing file must be a no-op: removed=%v err=%v", removed, err)
	}
}

// TestRealToolHomeIgnoresKaeManagedEnv reproduces the v0.5.0 acceptance bug:
// inside a pinned directory the isolation env var points into kae's own store,
// and treating it as the real home would create self-referential symlinks
// (ELOOP). realToolHome must ignore a kae-managed isolation env value.
func TestRealToolHomeIgnoresKaeManagedEnv(t *testing.T) {
	app := testApp(t, nil)
	sharedDir := app.Paths.SharedDir("abcdef0123456789", constants.ToolClaude)
	app.Env.Getenv = func(key string) string {
		if key == "CLAUDE_CONFIG_DIR" {
			return sharedDir
		}
		return ""
	}
	if home := app.realToolHome(constants.ToolClaude); home != filepath.Join(app.Env.Home, ".claude") {
		t.Fatalf("kae-managed env dir must be ignored as the real home, got %q", home)
	}

	// A genuinely user-set custom home (outside kae's store) is honored.
	custom := filepath.Join(app.Env.Home, "custom-claude")
	app.Env.Getenv = func(key string) string {
		if key == "CLAUDE_CONFIG_DIR" {
			return custom
		}
		return ""
	}
	if home := app.realToolHome(constants.ToolClaude); home != custom {
		t.Fatalf("user-set custom home must be honored, got %q", home)
	}
}

func TestPreparePinConfigSharesOptInItems(t *testing.T) {
	app := testApp(t, nil)
	app.Config.Tools[constants.ToolClaude] = config.Tool{IsolatedSharedItems: []string{"output-styles"}}
	writeFile(t, filepath.Join(app.Env.Home, ".claude", "output-styles", "x.json"), "{}")
	pinID := "abcdef0123456789"

	if _, err := app.preparePinConfig(context.Background(), constants.ToolClaude, "work", pinID); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "work"), "output-styles")
	if link, err := os.Readlink(target); err != nil || link != filepath.Join(app.Env.Home, ".claude", "output-styles") {
		t.Fatalf("opt-in shared item not linked: %q %v", link, err)
	}
}

func TestPinAndUnpinUsage(t *testing.T) {
	// Two positionals = re-bind <tool> <account>; an unknown tool is a usage
	// error, validated before any environment access. ("zz" matches no tool
	// prefix, so it stays unknown.)
	if code := CmdPin(context.Background(), []string{"zz", "b"}); code != constants.ExitUsage {
		t.Fatalf("pin with an unknown tool must be a usage error, got %d", code)
	}
	// --shared and --isolated are mutually exclusive.
	if code := CmdPin(context.Background(), []string{"-s", "-i"}); code != constants.ExitUsage {
		t.Fatalf("pin -s -i must be a usage error, got %d", code)
	}
	// Scope flags cannot be honored on a re-bind (mechanism is the directory's);
	// they are rejected, not silently dropped — checked before any env access.
	if code := CmdPin(context.Background(), []string{"-i", "claude", "work"}); code != constants.ExitUsage {
		t.Fatalf("pin -i <tool> <account> must be a usage error, got %d", code)
	}
	if code := CmdUnpin(context.Background(), []string{"x"}); code != constants.ExitUsage {
		t.Fatalf("unpin with a positional must be a usage error, got %d", code)
	}
}

func TestUseFlagValidation(t *testing.T) {
	ctx := context.Background()
	// --shared and --isolated are mutually exclusive (checked before env access).
	if code := CmdUse(ctx, []string{"-s", "-i", "work"}); code != constants.ExitUsage {
		t.Fatalf("use -s -i must be a usage error, got %d", code)
	}
	// More than two positionals is a usage error (checked before env access).
	// Bare use (zero positionals) is now valid — it folds apply — so it is
	// exercised in usebare_test.go against a temp HOME, not here.
	if code := CmdUse(ctx, []string{"a", "b", "c"}); code != constants.ExitUsage {
		t.Fatalf("use with three positionals must be a usage error, got %d", code)
	}
}

func TestRemovedCommandsPointAtReplacements(t *testing.T) {
	// bond/as folded into the pin surface in v0.7.2; the older removals stay.
	// `s` is no longer here — it is the status alias since v0.7.2.
	for _, name := range []string{"bond", "as", "switch", "login", "capture", "current"} {
		if code := Root([]string{name}); code != constants.ExitUsage {
			t.Fatalf("removed command %s must exit %d", name, constants.ExitUsage)
		}
	}
}

func TestAddFlagValidation(t *testing.T) {
	ctx := context.Background()
	if code := CmdAdd(ctx, []string{"claude"}); code != constants.ExitUsage {
		t.Fatalf("add with one positional must be a usage error, got %d", code)
	}
	if code := CmdAdd(ctx, []string{"--no-login", "--restore", "claude", "work"}); code != constants.ExitUsage {
		t.Fatalf("--no-login with --restore must be a usage error, got %d", code)
	}
	if code := CmdAdd(ctx, []string{"--dry-run", "claude", "work"}); code != constants.ExitUsage {
		t.Fatalf("--dry-run without --no-login must be a usage error, got %d", code)
	}
}
