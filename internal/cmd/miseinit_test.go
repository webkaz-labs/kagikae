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

func chdirTemp(t *testing.T) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
}

func TestMiseInitAutoRendersEnterHook(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	chdirTemp(t)

	code, out := captureStdout(t, func() int {
		return runMiseInit(ctx, app, opts, "work", constants.ModeAuth, true, false)
	})
	mustExit(t, constants.ExitOK, code, out)
	for _, want := range []string{"[hooks.enter]", `script = "kae sync --quiet"`, "[tasks.ai-use]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("auto block missing %q: %s", want, out)
		}
	}

	// Without --auto no hook is rendered.
	code, out = captureStdout(t, func() int {
		return runMiseInit(ctx, app, opts, "work", constants.ModeAuth, false, false)
	})
	mustExit(t, constants.ExitOK, code, out)
	if strings.Contains(out, "[hooks") {
		t.Fatalf("hook rendered without --auto: %s", out)
	}
}

func TestMiseInitHomeMode(t *testing.T) {
	app := testApp(t, nil)
	disabled := false
	app.Config.Tools[constants.ToolCodex] = config.Tool{HomeModeEnabled: &disabled}
	app.Config.Profiles = map[string]config.Profile{
		"work": {Accounts: map[string]string{
			constants.ToolClaude: "work",
			constants.ToolCodex:  "work",
			constants.ToolGemini: "work",
		}},
	}
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	chdirTemp(t)

	code, out := captureStdout(t, func() int {
		return runMiseInit(ctx, app, opts, "work", modeHome, false, false)
	})
	mustExit(t, constants.ExitOK, code, out)
	claudeHome := app.Paths.HomeModeDir(constants.ToolClaude, "work")
	if !strings.Contains(out, `CLAUDE_CONFIG_DIR = "`+claudeHome+`"`) {
		t.Fatalf("missing claude home entry: %s", out)
	}
	if strings.Contains(out, "CODEX_HOME =") || !strings.Contains(out, "home mode is disabled for codex") {
		t.Fatalf("disabled codex must keep the real home with a warning: %s", out)
	}
	if !strings.Contains(out, "gemini has no stable home-isolation env var") {
		t.Fatalf("gemini must be omitted with a warning: %s", out)
	}
	if strings.Contains(out, "[tasks.") || strings.Contains(out, "[hooks") {
		t.Fatalf("home mode must render no auth hooks/tasks: %s", out)
	}

	// Preview must not create directories; --write creates the rendered homes.
	if _, err := os.Stat(claudeHome); !os.IsNotExist(err) {
		t.Fatal("preview must not create home dirs")
	}
	code, out = captureStdout(t, func() int {
		return runMiseInit(ctx, app, opts, "work", modeHome, false, true)
	})
	mustExit(t, constants.ExitOK, code, out)
	if info, err := os.Stat(claudeHome); err != nil || !info.IsDir() {
		t.Fatalf("write must create the claude home dir: %v", err)
	}
	if !strings.Contains(readFile(t, ".mise.toml"), miseBlockStart) {
		t.Fatal(".mise.toml not written")
	}
}

func TestMiseInitHomeModeDirFailureLeavesTomlUntouched(t *testing.T) {
	app := testApp(t, nil)
	app.Config.Profiles = map[string]config.Profile{
		"work": {Accounts: map[string]string{constants.ToolClaude: "work"}},
	}
	chdirTemp(t)
	// Occupy the homes root with a file so MkdirAll fails; the block must
	// not be written when its directories cannot exist.
	writeFile(t, filepath.Join(app.Paths.DataDir, "homes"), "not a dir")
	code, out := captureStdout(t, func() int {
		return runMiseInit(context.Background(), app, commonOpts{Format: formatText}, "work", modeHome, false, true)
	})
	if code == constants.ExitOK {
		t.Fatalf("expected failure, got ok: %s", out)
	}
	if _, err := os.Stat(".mise.toml"); !os.IsNotExist(err) {
		t.Fatal(".mise.toml must not be written when home dirs cannot be created")
	}
}

func TestMiseInitHomeModeUnknownProfile(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	code, out := captureStdout(t, func() int {
		return runMiseInit(ctx, app, opts, "nope", modeHome, false, false)
	})
	mustExit(t, constants.ExitNotFound, code, out)
}

func TestMiseInitFlagValidation(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	code, out := captureStdout(t, func() int {
		return runMiseInit(ctx, app, opts, "work", "overlay", false, false)
	})
	mustExit(t, constants.ExitUsage, code, out)
	code, out = captureStdout(t, func() int {
		return runMiseInit(ctx, app, opts, "work", modeHome, true, false)
	})
	mustExit(t, constants.ExitUsage, code, out)
}
