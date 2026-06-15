package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
)

// Switch modes accepted by kae run --mode.
const (
	modeAuth    = constants.ModeAuth
	modeEnv     = constants.ModeEnv
	modeHome    = constants.ModeHome
	modeOverlay = constants.ModeOverlay
	modeBond    = constants.ModeBond
	modePin     = constants.ModePin
)

func validMode(mode string) bool {
	switch mode {
	case modeAuth, modeEnv, modeHome, modeOverlay, modeBond, modePin:
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
// overlay home: the built-in allowlist plus the user-configured
// tools.<tool>.overlay_extra_shared. Everything else, notably credentials,
// sessions, history, and the mixed-state identity file, stays private to
// the overlay — unknown new upstream files must fail safe (private).
// docs/ADAPTERS.md "Isolation" is the normative source for this table (as
// for isolationEnvVar and realToolHome); update both together.
func (app *App) overlaySharedItems(tool string) []string {
	var base []string
	switch tool {
	case constants.ToolClaude:
		base = []string{"settings.json", "CLAUDE.md", "skills", "agents", "commands", "plugins"}
	case constants.ToolCodex:
		base = []string{"config.toml", "AGENTS.md", "hooks.json", "prompts", "skills"}
	default:
		return nil
	}
	return append(base, app.Config.OverlayExtraShared(tool)...)
}

// realToolHome resolves the tool's live home directory for overlay sharing.
// An isolation env var pointing into kae's own homes/overlays data dirs is
// ignored: that is kae's own redirection (e.g. exported by a pinned
// directory's .mise.toml), and treating it as the real home would make an
// overlay share from itself — self-referential symlinks, ELOOP at runtime
// (found in v0.5.0 real-machine acceptance).
func (app *App) realToolHome(tool string) string {
	envVar := isolationEnvVar(tool)
	envHome := func(def string) string {
		dir := app.Env.Getenv(envVar)
		if dir != "" && !app.isKaeManagedHome(dir) {
			return dir
		}
		return def
	}
	switch tool {
	case constants.ToolClaude:
		return envHome(filepath.Join(app.Env.Home, ".claude"))
	case constants.ToolCodex:
		return envHome(filepath.Join(app.Env.Home, ".codex"))
	default:
		return ""
	}
}

// isKaeManagedHome reports whether dir lies inside kae's home-mode or
// overlay data roots.
func (app *App) isKaeManagedHome(dir string) bool {
	return app.kaeManagedHomeKind(dir) != ""
}

// kaeManagedHomeKind classifies dir against kae's isolation data roots.
// Returns a mode constant ("overlay", "home", "bond", "pin", "sync") or ""
// for anything outside all kae-managed roots.
func (app *App) kaeManagedHomeKind(dir string) string {
	switch {
	case pathWithin(dir, app.Paths.OverlaysDir()):
		return modeOverlay
	case pathWithin(dir, app.Paths.HomesDir()):
		return modeHome
	case pathWithin(dir, app.Paths.IsolationDir()):
		// isolation/<pin-id>/<tool>/shared/    → bond (per-dir shared)
		// isolation/<pin-id>/<tool>/isolated/… → pin  (per-dir isolated)
		// Inspect the third path segment after the isolation root. modePin
		// ("pin") is the pre-v0.7.2 isolated segment: a directory still pinned
		// by an old .mise.toml (not yet re-pinned to a fragment) must still be
		// reported as isolated, not misclassified as shared.
		rel, err := filepath.Rel(app.Paths.IsolationDir(), filepath.Clean(dir))
		if err == nil {
			parts := strings.SplitN(rel, string(filepath.Separator), 4)
			if len(parts) >= 3 && (parts[2] == paths.IsolatedSegment || parts[2] == modePin) {
				return modePin
			}
		}
		return modeBond
	case pathWithin(dir, app.Paths.SyncHomesDir()):
		return constants.ModeSync
	default:
		return ""
	}
}

// pinnedStatus reports the binding a pinned .mise.toml exports into this
// directory's environment: KAE_PROFILE plus the isolation mode inferred
// from which kae data root the tools' isolation env vars point into.
// Neither pointing anywhere means the auth-mode tasks rendering; a pin is
// a single mode, so the first tool that resolves decides.
func (app *App) pinnedStatus() *pinnedStatus {
	profile := app.Env.Getenv(constants.EnvKaeProfile)
	if profile == "" {
		return nil
	}
	mode := constants.ModeAuth
	switch kind := app.firstKaeManagedIsolation(); kind {
	case "":
		// auth-mode pin (only KAE_PROFILE exported); mode stays auth.
	case modeBond:
		mode = paths.SharedSegment // user-facing: shared
	case modePin:
		mode = paths.IsolatedSegment // user-facing: isolated
	default:
		mode = kind // legacy overlay/home (and sync); reported as-is
	}
	return &pinnedStatus{Profile: profile, Mode: mode}
}

// firstKaeManagedIsolation returns the isolation kind (modeOverlay /
// modeHome) the directory's environment redirects any tool into, or ""
// when no isolation env var points into kae's data roots. A pin is a
// single mode, so the first tool that resolves decides.
func (app *App) firstKaeManagedIsolation() string {
	for _, tool := range constants.Tools {
		envVar := isolationEnvVar(tool)
		if envVar == "" {
			continue
		}
		if kind := app.kaeManagedHomeKind(app.Env.Getenv(envVar)); kind != "" {
			return kind
		}
	}
	return ""
}

// pathWithin reports whether dir lies inside root (lexical; symlinks are
// not resolved).
func pathWithin(dir, root string) bool {
	rel, err := filepath.Rel(root, filepath.Clean(dir))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// pinnedGlobalScope puts the global-scope commands (use / add / apply) on the
// real home: they are inherently global, so kae-managed isolation env values
// are hidden (applyGlobalScope) and the adapters resolve the real base paths;
// genuinely user-set custom homes stay honored. Inside a kae-pinned directory
// it first warns that global state is changing and this directory will not see
// it — re-bind with `kae pin`. Idempotent (one warning per command path): the
// warning detection must run before applyGlobalScope hides the env values, and
// buildSync delegates to buildSwitch, so both reach this.
func (app *App) pinnedGlobalScope() {
	if app.globalScope {
		return
	}
	if kind := app.firstKaeManagedIsolation(); kind != "" {
		fmt.Fprintf(os.Stderr,
			"kae: warning: this directory is pinned (%s); you are changing GLOBAL state, "+
				"which this directory will not see — re-bind with `kae pin`\n", kind)
	}
	app.applyGlobalScope()
}

// applyGlobalScope hides kae-managed isolation env values from everything
// resolved through app.Env. Idempotent: the guard runs once per command
// path but may be reached twice (buildSync delegates to buildSwitch).
func (app *App) applyGlobalScope() {
	if app.globalScope {
		return
	}
	app.globalScope = true
	isolated := map[string]bool{}
	for _, tool := range constants.Tools {
		if envVar := isolationEnvVar(tool); envVar != "" {
			isolated[envVar] = true
		}
	}
	inner := app.Env.Getenv
	app.Env.Getenv = func(key string) string {
		value := inner(key)
		if isolated[key] && app.isKaeManagedHome(value) {
			return ""
		}
		return value
	}
}

// bondModeEnv prepares the bond directory for the current working directory
// and returns the child env entry pointing the tool at it.
func (app *App) bondModeEnv(ctx context.Context, tool, accountName string) ([]string, error) {
	envVar := isolationEnvVar(tool)
	if envVar == "" {
		return nil, errf(constants.ExitUnsupported,
			"%s has no stable home-isolation mechanism yet (see docs/ROADMAP.md)", tool)
	}
	absDir, err := cwdAbs()
	if err != nil {
		return nil, err
	}
	pinID := paths.PinID(absDir)
	bondDir, err := app.prepareBond(ctx, tool, accountName, pinID)
	if err != nil {
		return nil, err
	}
	return []string{envVar + "=" + bondDir}, nil
}

// pinModeEnv prepares the pin config directory for the current working directory
// and returns the child env entry pointing the tool at it.
func (app *App) pinModeEnv(ctx context.Context, tool, accountName string) ([]string, error) {
	envVar := isolationEnvVar(tool)
	if envVar == "" {
		return nil, errf(constants.ExitUnsupported,
			"%s has no stable home-isolation mechanism yet (see docs/ROADMAP.md)", tool)
	}
	absDir, err := cwdAbs()
	if err != nil {
		return nil, err
	}
	pinID := paths.PinID(absDir)
	configDir, err := app.preparePinConfig(ctx, tool, accountName, pinID)
	if err != nil {
		return nil, err
	}
	return []string{envVar + "=" + configDir}, nil
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
	// Belt and braces for the self-share case realToolHome already filters:
	// linking an overlay to itself would create symlink cycles (ELOOP).
	if filepath.Clean(realHome) == filepath.Clean(overlayDir) {
		return "", errf(constants.ExitUnsafeRefused,
			"the real %s home resolves to the overlay itself; unset %s and retry",
			tool, isolationEnvVar(tool))
	}
	for _, item := range app.overlaySharedItems(tool) {
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
