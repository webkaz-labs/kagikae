package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// Switch modes accepted by kae run --mode.
const (
	modeAuth    = constants.ModeAuth
	modeEnv     = "env"
	modeHome    = constants.ModeHome
	modeOverlay = "overlay"
)

func validMode(mode string) bool {
	switch mode {
	case modeAuth, modeEnv, modeHome, modeOverlay:
		return true
	}
	return false
}

// isolationEnvVar returns the env var that points a tool at an alternate
// home directory, or "" when the tool has no stable isolation mechanism.
// Consumers: homeModeEnv/overlayModeEnv (kae run, refuse with exit 5) and
// miseHomeBlock (kae mise init --mode home, skip with a warning comment);
// docs/ADAPTERS.md "Isolation" is the normative table — update together.
func isolationEnvVar(tool string) string {
	switch tool {
	case constants.ToolClaude:
		return "CLAUDE_CONFIG_DIR"
	case constants.ToolCodex:
		return "CODEX_HOME"
	default:
		return ""
	}
}

// homeModeEnv prepares the isolated home for one tool/account and returns
// the KEY=VALUE entries for the child process.
func (app *App) homeModeEnv(tool, accountName string) ([]string, error) {
	if !app.Config.HomeModeEnabled(tool) {
		return nil, errf(constants.ExitUnsupported,
			"home mode is disabled for %s (tools.%s.home_mode_enabled = false)", tool, tool)
	}
	envVar := isolationEnvVar(tool)
	if envVar == "" {
		return nil, errf(constants.ExitUnsupported,
			"%s has no stable home-isolation mechanism yet (see docs/ROADMAP.md)", tool)
	}
	dir := app.Paths.HomeModeDir(tool, accountName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create home-mode dir: %w", err)
	}
	return []string{envVar + "=" + dir}, nil
}

// overlaySharedItems lists the real-home entries shared (symlinked) into an
// overlay home. Everything else, notably credentials, sessions, history, and
// the mixed-state identity file, stays private to the overlay.
// docs/ADAPTERS.md "Isolation" is the normative source for this table (as
// for isolationEnvVar and realToolHome); update both together.
func overlaySharedItems(tool string) []string {
	switch tool {
	case constants.ToolClaude:
		return []string{"settings.json", "CLAUDE.md", "skills", "agents", "commands", "plugins"}
	case constants.ToolCodex:
		return []string{"config.toml", "AGENTS.md", "hooks.json", "prompts", "skills"}
	default:
		return nil
	}
}

// realToolHome resolves the tool's live home directory for overlay sharing.
func (app *App) realToolHome(tool string) string {
	switch tool {
	case constants.ToolClaude:
		if dir := app.Env.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
			return dir
		}
		return filepath.Join(app.Env.Home, ".claude")
	case constants.ToolCodex:
		if dir := app.Env.Getenv("CODEX_HOME"); dir != "" {
			return dir
		}
		return filepath.Join(app.Env.Home, ".codex")
	default:
		return ""
	}
}

// overlayModeEnv prepares the overlay home (private dir + shared symlinks)
// and returns the child env entries. Overlay mode is experimental and
// requires explicit opt-in per tool.
func (app *App) overlayModeEnv(tool, accountName string) ([]string, error) {
	if !app.Config.OverlayModeEnabled(tool) {
		return nil, errf(constants.ExitUnsupported,
			"overlay mode is experimental; enable it with tools.%s.overlay_mode_enabled = true", tool)
	}
	envVar := isolationEnvVar(tool)
	if envVar == "" {
		return nil, errf(constants.ExitUnsupported,
			"%s has no stable home-isolation mechanism yet (see docs/ROADMAP.md)", tool)
	}
	overlayDir := app.Paths.OverlayDir(tool, accountName)
	if err := os.MkdirAll(overlayDir, 0o700); err != nil {
		return nil, fmt.Errorf("create overlay dir: %w", err)
	}
	// A symlinked overlay dir would redirect the link maintenance below to
	// an arbitrary location; refuse it.
	if info, err := os.Lstat(overlayDir); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errf(constants.ExitUnsafeRefused,
			"overlay dir %s is not a real directory; remove it and retry", overlayDir)
	}
	realHome := app.realToolHome(tool)
	for _, item := range overlaySharedItems(tool) {
		source := filepath.Join(realHome, item)
		target := filepath.Join(overlayDir, item)
		if _, err := os.Stat(source); err != nil {
			continue // share only what exists in the real home
		}
		if info, err := os.Lstat(target); err == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				// A real file/dir in the overlay would be silently shadowed
				// by a link; refuse instead of destroying overlay-local data.
				return nil, errf(constants.ExitUnsafeRefused,
					"overlay item %s exists as a real file/directory; move it aside or delete the overlay dir %s",
					target, overlayDir)
			}
			if current, readErr := os.Readlink(target); readErr == nil && current == source {
				continue // already linked correctly
			}
			if err := os.Remove(target); err != nil {
				return nil, fmt.Errorf("refresh overlay link %s: %w", target, err)
			}
		}
		if err := os.Symlink(source, target); err != nil {
			return nil, fmt.Errorf("link overlay item %s: %w", target, err)
		}
	}
	return []string{envVar + "=" + overlayDir}, nil
}
