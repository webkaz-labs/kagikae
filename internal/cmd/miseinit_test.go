package cmd

import (
	"context"
	"os"
	"strings"
	"testing"

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
		return runMiseInit(ctx, app, opts, "main", constants.ModeAuth, true, false)
	})
	mustExit(t, constants.ExitOK, code, out)
	for _, want := range []string{"[hooks.enter]", `run = "kae use --auto --quiet"`, "[tasks.ai-use]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("auto block missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, `script = "kae use --auto --quiet"`) {
		t.Fatalf("auto hook must use mise's spawned run form, not deprecated script: %s", out)
	}

	// Without --auto no hook is rendered.
	code, out = captureStdout(t, func() int {
		return runMiseInit(ctx, app, opts, "main", constants.ModeAuth, false, false)
	})
	mustExit(t, constants.ExitOK, code, out)
	if strings.Contains(out, "[hooks") {
		t.Fatalf("hook rendered without --auto: %s", out)
	}
}

func TestMiseInitRejectsNonAuthModes(t *testing.T) {
	// mise init renders auth mode only since v0.8.0; the former isolation modes
	// (and a stray --mode value) are rejected. Bind directories with kae pin.
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	for _, mode := range []string{"env", "home", "overlay", modeShared, modeIsolated} {
		code, out := captureStdout(t, func() int {
			return runMiseInit(ctx, app, opts, "main", mode, false, false)
		})
		mustExit(t, constants.ExitUsage, code, out)
	}
}

func TestMiseInitMigratesOwnedAutoHook(t *testing.T) {
	app := testApp(t, nil)
	chdirTemp(t)
	old := strings.ReplaceAll(app.miseBlock("main", true), "kae use --auto --quiet", "kae use --quiet")
	writeFile(t, ".mise.toml", "# user prefix\n"+old+"\n# user suffix\n")
	code, out := captureStdout(t, func() int {
		return runMiseInit(context.Background(), app, commonOpts{Format: formatText}, "main", constants.ModeAuth, true, true)
	})
	mustExit(t, constants.ExitOK, code, out)
	content := readFile(t, ".mise.toml")
	if strings.Contains(content, `run = "kae use --quiet"`) || !strings.Contains(content, `run = "kae use --auto --quiet"`) || !strings.HasPrefix(content, "# user prefix\n") || !strings.HasSuffix(content, "# user suffix\n") {
		t.Fatalf("incorrect migration: %s", content)
	}
}
