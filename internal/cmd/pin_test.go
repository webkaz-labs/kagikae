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
			constants.ToolGemini: "work",
		}},
	}
	// Shared items must exist in the real home to be linked.
	writeFile(t, filepath.Join(app.Env.Home, ".claude", "settings.json"), `{"theme":"dark"}`)
	if err := os.MkdirAll(filepath.Join(app.Env.Home, ".claude", "skills", "demo"), 0o700); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestMiseInitOverlayWriteLinksAndRefreshes(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	chdirTemp(t)

	// Preview renders the env entry and warnings without touching disk.
	code, out := captureStdout(t, func() int {
		return runMiseInit(ctx, app, opts, "work", modeOverlay, false, false)
	})
	mustExit(t, constants.ExitOK, code, out)
	overlayDir := app.Paths.OverlayDir(constants.ToolClaude, "work")
	if !strings.Contains(out, `CLAUDE_CONFIG_DIR = "`+overlayDir+`"`) {
		t.Fatalf("missing overlay env entry: %s", out)
	}
	if !strings.Contains(out, "gemini has no stable home-isolation env var") {
		t.Fatalf("gemini must keep the real home with a warning: %s", out)
	}
	if _, err := os.Stat(overlayDir); !os.IsNotExist(err) {
		t.Fatal("preview must not create overlay dirs")
	}

	// Write prepares the overlay: private dir plus shared-item symlinks.
	code, out = captureStdout(t, func() int {
		return runMiseInit(ctx, app, opts, "work", modeOverlay, false, true)
	})
	mustExit(t, constants.ExitOK, code, out)
	link, err := os.Readlink(filepath.Join(overlayDir, "settings.json"))
	if err != nil || link != filepath.Join(app.Env.Home, ".claude", "settings.json") {
		t.Fatalf("settings symlink: %q %v", link, err)
	}
	if !strings.Contains(readFile(t, ".mise.toml"), miseBlockStart) {
		t.Fatal(".mise.toml not written")
	}

	// A shared item added to the real home later is linked on re-run.
	writeFile(t, filepath.Join(app.Env.Home, ".claude", "CLAUDE.md"), "# memo")
	code, out = captureStdout(t, func() int {
		return runMiseInit(ctx, app, opts, "work", modeOverlay, false, true)
	})
	mustExit(t, constants.ExitOK, code, out)
	if _, err := os.Readlink(filepath.Join(overlayDir, "CLAUDE.md")); err != nil {
		t.Fatalf("re-run must link new shared items: %v", err)
	}
}

func TestMiseInitOverlayDisabledToolWarns(t *testing.T) {
	app := overlayTestApp(t)
	disabled := false
	app.Config.Tools[constants.ToolClaude] = config.Tool{OverlayModeEnabled: &disabled}
	ctx := context.Background()
	chdirTemp(t)

	code, out := captureStdout(t, func() int {
		return runMiseInit(ctx, app, commonOpts{Format: formatText}, "work", modeOverlay, false, false)
	})
	mustExit(t, constants.ExitOK, code, out)
	if strings.Contains(out, "CLAUDE_CONFIG_DIR =") || !strings.Contains(out, "overlay mode is disabled for claude") {
		t.Fatalf("disabled claude must keep the real home with a warning: %s", out)
	}
}

func TestUnpinRemovesOnlyTheBlock(t *testing.T) {
	chdirTemp(t)
	writeFile(t, ".mise.toml",
		"[tasks.custom]\nrun = \"echo hi\"\n"+miseBlockStart+"\n[env]\nKAE_PROFILE = \"work\"\n"+miseBlockEnd+"\ntail = 1\n")
	if err := removeMiseBlock(".mise.toml"); err != nil {
		t.Fatal(err)
	}
	rest := readFile(t, ".mise.toml")
	if strings.Contains(rest, miseBlockStart) || strings.Contains(rest, "KAE_PROFILE") {
		t.Fatalf("block not removed: %s", rest)
	}
	if !strings.Contains(rest, "tasks.custom") || !strings.Contains(rest, "tail = 1") {
		t.Fatalf("unpin must keep everything else: %s", rest)
	}

	// Without a block (or file) unpin is a not_found error.
	if err := removeMiseBlock(".mise.toml"); exitOf(err) != constants.ExitNotFound {
		t.Fatalf("expected not_found, got %v", err)
	}
	if err := os.Remove(".mise.toml"); err != nil {
		t.Fatal(err)
	}
	if err := removeMiseBlock(".mise.toml"); exitOf(err) != constants.ExitNotFound {
		t.Fatalf("expected not_found for a missing file, got %v", err)
	}
}

// TestPrepareOverlayInsidePinnedDir reproduces the v0.5.0 acceptance bug:
// inside a pinned directory the isolation env var points at the overlay
// itself, and a re-run used to create self-referential symlinks (ELOOP).
// The env var must be ignored as the "real" home, and an existing self-loop
// must be repaired on re-run.
func TestPrepareOverlayInsidePinnedDir(t *testing.T) {
	app := testApp(t, nil)
	overlayDir := app.Paths.OverlayDir(constants.ToolClaude, "work")
	app.Env.Getenv = func(key string) string {
		if key == "CLAUDE_CONFIG_DIR" {
			return overlayDir
		}
		return ""
	}
	realSettings := filepath.Join(app.Env.Home, ".claude", "settings.json")
	writeFile(t, realSettings, `{"theme":"dark"}`)

	// Seed the broken state a pre-fix re-run left behind: a self-loop link.
	if err := os.MkdirAll(overlayDir, 0o700); err != nil {
		t.Fatal(err)
	}
	loop := filepath.Join(overlayDir, "settings.json")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}

	if home := app.realToolHome(constants.ToolClaude); home != filepath.Join(app.Env.Home, ".claude") {
		t.Fatalf("kae-managed env dir must be ignored as the real home, got %q", home)
	}
	if _, err := app.prepareOverlay(constants.ToolClaude, "work"); err != nil {
		t.Fatalf("prepareOverlay must repair a self-loop: %v", err)
	}
	if link, err := os.Readlink(loop); err != nil || link != realSettings {
		t.Fatalf("self-loop not repaired: %q %v", link, err)
	}
}

func TestPinAndUnpinUsage(t *testing.T) {
	// Argument validation happens before any environment access.
	if code := CmdPin(context.Background(), []string{"a", "b"}); code != constants.ExitUsage {
		t.Fatalf("pin with two positionals must be a usage error, got %d", code)
	}
	if code := CmdUnpin(context.Background(), []string{"x"}); code != constants.ExitUsage {
		t.Fatalf("unpin with a positional must be a usage error, got %d", code)
	}
}

func TestRemovedCommandsPointAtReplacements(t *testing.T) {
	for _, name := range []string{"switch", "s", "login", "capture", "current"} {
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
