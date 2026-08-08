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

// credentialEnvVar returns the env var that points a tool at an alternate
// *credential* store without moving its home, or "" when the tool has no way to
// separate the two. Only claude has one; docs/ADAPTERS.md § "Credential storage
// resolution" is the normative description of what it displaces — update
// together, and keep the literal in step with the adapter's own constant
// (claude.EnvSecureStorageDir), which this deliberately does not import for the
// same reason isolationEnvVar spells CLAUDE_CONFIG_DIR out above.
//
// A tool with no such variable keeps its credential inside the config dir, which
// is what an empty answer means to every caller: one directory, both roles.
func credentialEnvVar(tool string) string {
	switch tool {
	case constants.ToolClaude:
		return "CLAUDE_SECURESTORAGE_CONFIG_DIR"
	default:
		return ""
	}
}

// credStoreDir is the per-account credential store a bound directory points
// tool's credential variable at, or "" for a tool that cannot separate its
// credential from its home.
//
// Per *account*, not per directory: that is the whole point of the split. Two
// directories bound to one account share this one copy, so the tool's refresh
// rotates a single credential instead of invalidating the copies in every other
// bound directory (docs/ROADMAP.md § One credential per account).
func (app *App) credStoreDir(tool, account string) string {
	// The `account == ""` half is a statement of intent, not a live guard: every
	// caller resolves the account from a binding or a plan first, so a mutation that
	// removes it cannot be killed (measured 2026-08-07). It stays because composing a
	// store path from an empty account would put every unattributed store at one
	// shared path — write the reason rather than a test that cannot fail.
	if credentialEnvVar(tool) == "" || account == "" {
		return ""
	}
	return app.Paths.CredStoreDir(tool, account)
}

// isKaeManagedCredStore reports whether dir lies inside kae's per-account
// credential store root. The sibling of isKaeManagedHome, for the second
// variable: applyGlobalScope has to hide a kae-set credential dir exactly as it
// hides a kae-set config dir, or a global command run inside a bound directory
// resolves the *directory's* credential and switches the account there instead
// of in the real home.
func (app *App) isKaeManagedCredStore(dir string) bool {
	return dir != "" && pathWithin(dir, app.Paths.CredStoreRoot())
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

// toolIsolated reports whether this shell's environment redirects one tool into
// a kae-owned isolated home (kae pin, kae use -i). It is the per-tool half of
// firstKaeManagedIsolation, for callers that care about a single tool rather than
// the directory's bind kind. A tool with no isolation env var reads an empty
// value, which isKaeManagedHome already rejects.
func (app *App) toolIsolated(tool string) bool {
	return app.isKaeManagedHome(app.Env.Getenv(isolationEnvVar(tool)))
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
	credential := map[string]bool{}
	for _, tool := range constants.Tools {
		if envVar := isolationEnvVar(tool); envVar != "" {
			isolated[envVar] = true
		}
		// The second variable needs its own masking and its own test for what
		// counts as kae-managed: its value points into the per-account credential
		// store, which is not a tool home and so is not what isKaeManagedHome
		// recognizes. Masking one of the pair and not the other is worse than
		// masking neither — a global `kae use` would then write claude's credential
		// into the bound directory's shared store while reading and reporting the
		// real home.
		if envVar := credentialEnvVar(tool); envVar != "" {
			credential[envVar] = true
		}
	}
	// masked reports whether this key holds a value kae itself set, which is the one
	// thing a global command must not see.
	//
	// This wraps the same two seams `dirSpecs` wraps, and the two are deliberately not
	// one helper: they answer opposite questions. `dirSpecs` *asserts* a value, so its
	// LookupEnv forces `ok=true` for an overridden key; this one *hides* one, so it
	// must force `ok=false` — an absent key, not an empty one, because empty is a value
	// claude refuses. A shared wrapper would take that difference as a parameter and
	// bury the reason for it.
	inner, innerLookup := app.Env.Getenv, app.Env.LookupEnv
	masked := func(key, value string) bool {
		return (isolated[key] && app.isKaeManagedHome(value)) ||
			(credential[key] && app.isKaeManagedCredStore(value))
	}
	app.Env.Getenv = func(key string) string {
		if value := inner(key); !masked(key, value) {
			return value
		}
		return ""
	}
	// **Both** seams, because an adapter that asks `Env.IsSet` reads this one and not
	// the one above. That used to be safe on the stated grounds that every variable
	// reached through IsSet is user-set by definition — which stopped being true the
	// moment kae started setting a credential variable itself. Masking only Getenv
	// leaves `IsSet(SSCD) && Getenv(SSCD) == ""` true for every bound directory, which
	// is claude's refusal for the one value kae never writes: every global command run
	// inside a bound directory would report the tool unsupported, including the mise
	// enter hook. dirSpecs overrides both seams for the same reason.
	app.Env.LookupEnv = func(key string) (string, bool) {
		// Dead in both production (app.go injects os.LookupEnv) and the test fixture,
		// and kept as the degraded answer rather than a panic for an App built by hand
		// — a mutation of it cannot be killed (measured 2026-08-07), so this comment is
		// the guard instead of a test that would assert nothing.
		if innerLookup == nil {
			value := inner(key)
			return value, value != "" && !masked(key, value)
		}
		value, ok := innerLookup(key)
		if ok && masked(key, value) {
			return "", false
		}
		return value, ok
	}
}

// modeLabelStale answers the fourth question the bind mechanism decides: is the identity
// label in the config dir a bind materializes into kae's own leftover, or evidence?
//
// A **shared** config dir is one per pin×tool, so a label in it was written under whichever
// account was bound then — a change of account makes it a leftover, and leaving it there is
// how a keep destroys what it kept (the next run's fragment names the new account, so the
// directory is one of the store's readers and that label is its only reading). An
// **isolated** dir, and the globally isolated home, are keyed by the account: every label in
// one was written while bound to that same account, so a disagreement there is a login as
// somebody else, and retracting it deletes the only record of whose the credential is. Both
// destroyed a login before this was stated (measured 2026-08-08).
//
// It lives beside modeStoreDir because it is the same kind of fact — what the mechanism
// implies about its store — and because assembling it by hand at each bind path is how the
// two paths came to switch on different constant families for one question
// (TestBindModeConstantsAgreeAcrossPackages). A mode kae does not recognize answers *not
// stale*, which is right for an account-keyed mechanism and the "keep then destroy on the
// next run" defect for an account-agnostic one — so a third mechanism has to decide here,
// as the lockstep note below says.
func modeLabelStale(mode, prevAccount, account string) bool {
	return mode == modeShared && prevAccount != account
}

// modeStoreDir answers "which directory does a per-directory bind in mode point
// tool at" — the one place that maps the bind mechanism to its store layout. ok is
// false for a mode kae does not recognize, so a caller reading a hand-edited or
// future fragment gets nothing rather than a guessed path.
//
// It exists because that mapping was written three times: the two bind planners
// (which materialize the store) and the doctor sweep that reads a bound directory's
// credential. A third per-directory mechanism has to be added to `dirCredentialStores`
// in lockstep already (AGENTS.md); three switches to keep in step instead of one is
// how the new one ends up half-added, silently pointing some caller at a store that
// does not exist.
//
// A **fourth** switch on the mode is `modeLabelStale` above, which a new mechanism has to
// decide in the same way and for a consequence just as sharp — its own doc says which.
func (app *App) modeStoreDir(mode, pinID, tool, account string) (dir string, ok bool) {
	switch mode {
	case modeShared:
		// Account-agnostic: one shared store per pinID×tool.
		return app.Paths.SharedDir(pinID, tool), true
	case modeIsolated:
		return app.Paths.IsolatedConfigDir(pinID, tool, account), true
	}
	return "", false
}
