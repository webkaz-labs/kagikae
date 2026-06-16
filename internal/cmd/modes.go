package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
)

// Per-directory bind kinds, unified on the user-facing shared/isolated
// vocabulary (docs/RELEASE.md v0.8.0). They equal the on-disk path segments
// (paths.SharedSegment / paths.IsolatedSegment) and the -s/-i flags.
const (
	modeShared   = constants.ModeShared
	modeIsolated = constants.ModeIsolated
)

// isolationEnvVar returns the env var that points a tool at an alternate
// home directory, or "" when the tool has no stable isolation mechanism.
// Consumers: the isolation-mode planners (kae pin / kae use -i / kae run -i,
// which skip or refuse a tool with no var) and miseinit; docs/ADAPTERS.md
// "Isolation" is the normative table — update together.
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

// realToolHome resolves the tool's live home directory for per-directory shared
// linking. An isolation env var pointing into kae's own isolation data dirs is
// ignored: that is kae's own redirection (e.g. exported by a pinned directory's
// mise fragment), and treating it as the real home would make a shared bind link
// from itself — self-referential symlinks, ELOOP at runtime (found in v0.5.0
// real-machine acceptance).
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

// isKaeManagedHome reports whether dir lies inside kae's isolation data root.
func (app *App) isKaeManagedHome(dir string) bool {
	return app.kaeManagedHomeKind(dir) != ""
}

// kaeManagedHomeKind classifies dir against kae's isolation data root. Returns
// a mode constant (modeShared / modeIsolated / sync) or "" for anything outside
// the isolation root. The path segments after isolation/ decide:
//
//	isolation/global/<tool>/<account>/      → sync (global isolated, kae use -i)
//	isolation/<pin-id>/<tool>/shared/       → shared (per-dir, kae pin -s)
//	isolation/<pin-id>/<tool>/isolated/…    → isolated (per-dir, kae pin -i)
//
// A pin-id is 16 hex chars, so it never collides with the "global" prefix.
func (app *App) kaeManagedHomeKind(dir string) string {
	if !pathWithin(dir, app.Paths.IsolationDir()) {
		return ""
	}
	rel, err := filepath.Rel(app.Paths.IsolationDir(), filepath.Clean(dir))
	if err != nil {
		return modeShared
	}
	parts := strings.SplitN(rel, string(filepath.Separator), 4)
	if len(parts) >= 1 && parts[0] == paths.GlobalSegment {
		return constants.ModeSync
	}
	if len(parts) >= 3 && parts[2] == paths.IsolatedSegment {
		return modeIsolated
	}
	return modeShared
}

// pinnedStatus reports the binding a pinned mise fragment exports into this
// directory's environment: KAE_PROFILE plus the bind kind inferred from which
// kae data segment the tools' isolation env vars point into. No isolation env
// var means the auth-mode tasks rendering; a pin is a single kind, so the first
// tool that resolves decides.
func (app *App) pinnedStatus() *pinnedStatus {
	profile := app.Env.Getenv(constants.EnvKaeProfile)
	if profile == "" {
		return nil
	}
	mode := constants.ModeAuth
	if kind := app.firstKaeManagedIsolation(); kind != "" {
		// kind is already the user-facing label (shared/isolated/sync).
		mode = kind
	}
	return &pinnedStatus{Profile: profile, Mode: mode}
}

// firstKaeManagedIsolation returns the bind kind the directory's environment
// redirects any tool into, or "" when no isolation env var points into kae's
// data root. A pin is a single kind, so the first tool that resolves decides.
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

// pinnedGlobalScope puts the global-scope commands (use / add) on the real home:
// they are inherently global, so kae-managed isolation env values are hidden
// (applyGlobalScope) and the adapters resolve the real base paths; genuinely
// user-set custom homes stay honored. Inside a kae-pinned directory it first
// warns that global state is changing and this directory will not see it —
// re-bind with `kae pin`. Idempotent (one warning per command path): the warning
// detection must run before applyGlobalScope hides the env values, and bare use
// delegates to buildSwitch, so both reach this.
func (app *App) pinnedGlobalScope() {
	if app.globalScope {
		return
	}
	// Warn only inside a per-directory pin (shared/isolated), where the change
	// really is invisible to the directory. A terminal activated by the global
	// mise fragment (kind == sync) is not "pinned": `kae use -s`/`-i` is the
	// sanctioned global path there, so it must not print a misleading warning.
	switch kind := app.firstKaeManagedIsolation(); kind {
	case modeShared, modeIsolated:
		fmt.Fprintf(os.Stderr,
			"kae: warning: this directory is pinned (%s); you are changing GLOBAL state, "+
				"which this directory will not see — re-bind with `kae pin`\n", kind)
	}
	app.applyGlobalScope()
}

// applyGlobalScope hides kae-managed isolation env values from everything
// resolved through app.Env. Idempotent: the guard runs once per command
// path but may be reached twice (bare use delegates to buildSwitch).
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
