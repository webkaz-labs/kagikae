package cmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// pinnedEnvApp simulates a directory pinned in isolated mode: KAE_PROFILE plus
// CLAUDE_CONFIG_DIR pointing into kae's isolation root, layered over the test
// fixtures (claude main/side captured, side active).
func pinnedEnvApp(t *testing.T) *App {
	t.Helper()
	app := applyTestApp(t, nil)
	isoDir := app.Paths.IsolatedConfigDir("abcdef0123456789", constants.ToolClaude, "main")
	app.Env.Getenv = func(key string) string {
		switch key {
		case constants.EnvKaeProfile:
			return "side"
		case "CLAUDE_CONFIG_DIR":
			return isoDir
		}
		return ""
	}
	return app
}

func TestGlobalCommandsActOnRealHomeInsidePinnedDir(t *testing.T) {
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	// use is inherently global: inside a kae-pinned directory it no longer
	// refuses. It warns (on stderr) that global state is changing, hides the
	// kae-managed isolation env, and switches the real home instead.
	app := pinnedEnvApp(t)
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	creds := readFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"))
	if !strings.Contains(creds, mainToken) {
		t.Fatalf("use inside a pinned dir must write the real home: %s", creds)
	}

	// bare use (the idempotent global form) behaves the same.
	app = pinnedEnvApp(t)
	code, out = captureStdout(t, func() int { return runUseBare(ctx, app, opts, false, "main", false) })
	mustExit(t, constants.ExitOK, code, out)

	// The auth-mode pin exports only KAE_PROFILE; its tasks and enter hook
	// run use inside the directory and must keep working (no warning).
	app = pinnedEnvApp(t)
	app.Env.Getenv = func(key string) string {
		if key == constants.EnvKaeProfile {
			return "side"
		}
		return ""
	}
	code, out = captureStdout(t, func() int { return runUseBare(ctx, app, opts, false, "side", false) })
	mustExit(t, constants.ExitOK, code, out)
}

func TestGlobalScopeKeepsCustomHomes(t *testing.T) {
	app := testApp(t, nil)
	custom := filepath.Join(app.Env.Home, "custom-claude")
	app.Env.Getenv = func(key string) string {
		if key == "CLAUDE_CONFIG_DIR" {
			return custom
		}
		return ""
	}
	// A genuinely user-set custom home is outside kae's data roots, so the
	// global-scope commands leave it honored and emit no warning.
	app.pinnedGlobalScope()
	if got := app.Env.Getenv("CLAUDE_CONFIG_DIR"); got != custom {
		t.Fatalf("user-set custom home must stay honored, got %q", got)
	}
}
