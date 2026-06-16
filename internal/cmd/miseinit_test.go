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
		return runMiseInit(ctx, app, opts, "work", constants.ModeAuth, true, false)
	})
	mustExit(t, constants.ExitOK, code, out)
	for _, want := range []string{"[hooks.enter]", `script = "kae use --quiet"`, "[tasks.ai-use]"} {
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

func TestMiseInitRejectsNonAuthModes(t *testing.T) {
	// mise init renders auth mode only since v0.8.0; the former isolation modes
	// (and a stray --mode value) are rejected. Bind directories with kae pin.
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	for _, mode := range []string{"env", "home", "overlay", modeShared, modeIsolated} {
		code, out := captureStdout(t, func() int {
			return runMiseInit(ctx, app, opts, "work", mode, false, false)
		})
		mustExit(t, constants.ExitUsage, code, out)
	}
}
