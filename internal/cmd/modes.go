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
	modeEnv     = constants.ModeEnv
	modeHome    = constants.ModeHome
	modeOverlay = constants.ModeOverlay
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
	dir, err := app.prepareHome(tool, accountName)
	if err != nil {
		return nil, err
	}
	return []string{envVar + "=" + dir}, nil
}

// prepareHome creates the fully separate home-mode directory for one
// tool/account. Shared by kae run --mode home and the pin / mise init
// write path; like prepareOverlay it does not check the per-tool gate.
func (app *App) prepareHome(tool, accountName string) (string, error) {
	dir := app.Paths.HomeModeDir(tool, accountName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create home-mode dir: %w", err)
	}
	return dir, nil
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

// overlayModeEnv prepares the overlay home and returns the child env
// entries (per-tool opt-out via overlay_mode_enabled).
func (app *App) overlayModeEnv(tool, accountName string) ([]string, error) {
	if !app.Config.OverlayModeEnabled(tool) {
		return nil, errf(constants.ExitUnsupported,
			"overlay mode is disabled for %s (tools.%s.overlay_mode_enabled = false)", tool, tool)
	}
	envVar := isolationEnvVar(tool)
	if envVar == "" {
		return nil, errf(constants.ExitUnsupported,
			"%s has no stable home-isolation mechanism yet (see docs/ROADMAP.md)", tool)
	}
	overlayDir, err := app.prepareOverlay(tool, accountName)
	if err != nil {
		return nil, err
	}
	return []string{envVar + "=" + overlayDir}, nil
}

// prepareOverlay creates the overlay home (private dir + shared-item
// symlinks) for one tool/account and refreshes stale links. Shared by
// kae run --mode overlay, kae mise init --mode overlay --write, and
// kae pin; it does not check the overlay_mode_enabled gate.
func (app *App) prepareOverlay(tool, accountName string) (string, error) {
	overlayDir := app.Paths.OverlayDir(tool, accountName)
	if err := os.MkdirAll(overlayDir, 0o700); err != nil {
		return "", fmt.Errorf("create overlay dir: %w", err)
	}
	// A symlinked overlay dir would redirect the link maintenance below to
	// an arbitrary location; refuse it.
	if info, err := os.Lstat(overlayDir); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errf(constants.ExitUnsafeRefused,
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
				return "", errf(constants.ExitUnsafeRefused,
					"overlay item %s exists as a real file/directory; move it aside or delete the overlay dir %s",
					target, overlayDir)
			}
			if current, readErr := os.Readlink(target); readErr == nil && current == source {
				continue // already linked correctly
			}
			if err := os.Remove(target); err != nil {
				return "", fmt.Errorf("refresh overlay link %s: %w", target, err)
			}
		}
		if err := os.Symlink(source, target); err != nil {
			return "", fmt.Errorf("link overlay item %s: %w", target, err)
		}
	}
	return overlayDir, nil
}
