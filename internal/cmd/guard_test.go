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

func TestGlobalCommandsRefuseInsidePinnedIsolation(t *testing.T) {
	app := pinnedEnvApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "all", "work") })
	mustExit(t, constants.ExitUnsupported, code, out)
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "work") })
	mustExit(t, constants.ExitUnsupported, code, out)
	code, out = captureStdout(t, func() int { return runSync(ctx, app, opts, "work", false) })
	mustExit(t, constants.ExitUnsupported, code, out)

	// The auth-mode pin exports only KAE_PROFILE; its tasks and enter hook
	// run use/sync inside the directory and must keep working.
	app.Env.Getenv = func(key string) string {
		if key == constants.EnvKaeProfile {
			return "personal"
		}
		return ""
	}
	code, out = captureStdout(t, func() int { return runSync(ctx, app, opts, "personal", false) })
	mustExit(t, constants.ExitOK, code, out)
}

func TestGlobalFlagActsOnTheRealHome(t *testing.T) {
	app := pinnedEnvApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText, Global: true}

	// With --global the kae-managed env value is hidden, the adapter
	// resolves the real home, and the switch lands there.
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "work") })
	mustExit(t, constants.ExitOK, code, out)
	creds := readFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"))
	if !strings.Contains(creds, workToken) {
		t.Fatalf("global switch must write the real home: %s", creds)
	}
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
	app.applyGlobalScope()
	if got := app.Env.Getenv("CLAUDE_CONFIG_DIR"); got != custom {
		t.Fatalf("user-set custom home must stay honored under --global, got %q", got)
	}
	if err := app.pinnedIsolationGuard(false); err != nil {
		t.Fatalf("custom home outside kae roots must not trip the guard: %v", err)
	}
}
