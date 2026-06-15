package cmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// pinnedEnvApp simulates a directory pinned in overlay mode: KAE_PROFILE
// plus CLAUDE_CONFIG_DIR pointing into kae's overlays root, layered over
// the sync test fixtures (claude work/personal captured, personal active).
func pinnedEnvApp(t *testing.T) *App {
	t.Helper()
	app := syncTestApp(t, nil)
	overlayDir := app.Paths.OverlayDir(constants.ToolClaude, "work")
	app.Env.Getenv = func(key string) string {
		switch key {
		case constants.EnvKaeProfile:
			return "personal"
		case "CLAUDE_CONFIG_DIR":
			return overlayDir
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
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "work") })
	mustExit(t, constants.ExitOK, code, out)
	creds := readFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"))
	if !strings.Contains(creds, workToken) {
		t.Fatalf("use inside a pinned dir must write the real home: %s", creds)
	}

	// apply (the idempotent global form) behaves the same.
	app = pinnedEnvApp(t)
	code, out = captureStdout(t, func() int { return runSync(ctx, app, opts, "work", false) })
	mustExit(t, constants.ExitOK, code, out)

	// The auth-mode pin exports only KAE_PROFILE; its tasks and enter hook
	// run use/apply inside the directory and must keep working (no warning).
	app = pinnedEnvApp(t)
	app.Env.Getenv = func(key string) string {
		if key == constants.EnvKaeProfile {
			return "personal"
		}
		return ""
	}
	code, out = captureStdout(t, func() int { return runSync(ctx, app, opts, "personal", false) })
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
