package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// Per-directory bind kinds, unified on the user-facing shared/isolated/tree
// vocabulary (docs/CONTEXT.md § Mechanism terms). They equal the on-disk path segments
// (paths.SharedSegment / paths.IsolatedSegment / paths.TreeSegment) and the -s/-i/-t flags.
const (
	modeShared   = constants.ModeShared
	modeIsolated = constants.ModeIsolated
	modeTree     = constants.ModeTree
)

// bindMode is one per-directory bind mechanism: every fact kae derives from "which
// mode is this directory bound in", in one row. Call sites ask the row instead of
// comparing the mode against the constants, so a new mechanism is one new row in
// bindModes plus its CLI wiring, and a caller the row does not reach gets the
// unrecognized-mode answer rather than silently taking another mode's branch.
type bindMode struct {
	// name is the mode token (constants.Mode*): the fragment's mode, the status label
	// and the -s/-i/-t vocabulary.
	name string
	// flag is the `kae pin` short flag that selects the mode, for messages that tell
	// the user how to come back to it.
	flag string
	// segment is the store's path segment under isolation/<pin-id>/<tool>/
	// (paths.*Segment), which is how kaeManagedHomeKind recognizes a store by its
	// path and dirCredentialStores finds one on disk. It equals name; both are kept
	// so TestBindModeConstantsAgreeAcrossPackages can say so.
	segment string
	// perAccount reports whether the store path is composed from the account
	// (…/<segment>/<account>/config/) rather than being one store per pin×tool.
	// labelStaleOnAccountChange and configMovesWithAccount follow from it.
	perAccount bool
	// tools is the set of tools the mode binds; nil binds every tool that has an
	// isolation variable. A tool outside it keeps the real home with a warning entry,
	// as a tool with no isolation variable does (modeIsolationEntries), and
	// `kae pin <tool> <account>` refuses it (runRebind).
	tools []string
	// storeDir is the config dir the tool's isolation variable points at. An
	// account-agnostic mode ignores account.
	storeDir func(p paths.Paths, pinID, tool, account string) string
	// prepare materializes the store for one tool and account and returns its dir:
	// it links what the mode shares from the real home (shared: the real-home
	// listing minus the denylist; isolated and tree: isolated_shared_items) and writes
	// the credential.
	prepare func(app *App, ctx context.Context, be secret.Backend, tool, account, pinID string, staleLabel bool) (string, error)
	// rebindFailure names what a failed prepare was doing, for the error of
	// `kae pin <tool> <account>`.
	rebindFailure func(tool, account string) string
}

// bindModes is the table of per-directory bind mechanisms, in the order the store
// walk (dirCredentialStores) lists a pin's stores. A function rather than a package
// variable because the preparers reach modeStoreDir, which reads the table: a
// variable would be an initialization cycle.
func bindModes() []bindMode {
	return []bindMode{
		{
			name:       modeShared,
			flag:       "-s",
			segment:    paths.SharedSegment,
			perAccount: false, // one store per pin×tool
			storeDir: func(p paths.Paths, pinID, tool, _ string) string {
				return p.SharedDir(pinID, tool)
			},
			prepare: (*App).prepareBond,
			rebindFailure: func(tool, _ string) string {
				return "swap shared credential for " + tool
			},
		},
		{
			name:       modeIsolated,
			flag:       "-i",
			segment:    paths.IsolatedSegment,
			perAccount: true,
			storeDir:   paths.Paths.IsolatedConfigDir,
			prepare:    (*App).preparePinConfig,
			rebindFailure: func(tool, account string) string {
				return "prepare isolated config for " + tool + "/" + account
			},
		},
		{
			name:       modeTree,
			flag:       "-t",
			segment:    paths.TreeSegment,
			perAccount: false, // one store per pin×tool, kept across account switches
			// claude only: its credential lives in the per-account credential store, so
			// the tree store holds none. codex keeps its credential inside CODEX_HOME,
			// so it is not bound here (docs/ADAPTERS.md § Per-directory tree bind
			// (`kae pin -t`)).
			tools: []string{constants.ToolClaude},
			storeDir: func(p paths.Paths, pinID, tool, _ string) string {
				return p.TreeDir(pinID, tool)
			},
			prepare: (*App).prepareTree,
			rebindFailure: func(tool, account string) string {
				return "prepare tree store for " + tool + "/" + account
			},
		},
	}
}

// bindModeFor returns the row for a mode token, and false for a token no row
// names: a hand-edited fragment, one from a newer kae, or a global mode (auth, sync).
func bindModeFor(mode string) (bindMode, bool) {
	for _, m := range bindModes() {
		if m.name == mode {
			return m, true
		}
	}
	return bindMode{}, false
}

// bindModeForSegment returns the row whose stores live under the path segment.
func bindModeForSegment(segment string) (bindMode, bool) {
	for _, m := range bindModes() {
		if m.segment == segment {
			return m, true
		}
	}
	return bindMode{}, false
}

// bindsTool reports whether the mode binds tool at all (bindMode.tools).
func (m bindMode) bindsTool(tool string) bool {
	return m.tools == nil || slices.Contains(m.tools, tool)
}

// labelStaleOnAccountChange is modeLabelStale's polarity for this mode: an
// account-agnostic store keeps the label of whichever account was bound before, so a
// change of account makes it a leftover; a per-account store's label was written
// under this same account, so it is evidence.
func (m bindMode) labelStaleOnAccountChange() bool { return !m.perAccount }

// configMovesWithAccount reports whether re-binding a tool to another account moves
// its isolation variable (the fragment's config line) to a different store. An
// account-agnostic store stays where it is; only the credential entry moves.
func (m bindMode) configMovesWithAccount() bool { return m.perAccount }

// modeStoreIsPerAccount reports whether a store of mode names its account by its
// path; false for an account-agnostic or unrecognized mode.
func modeStoreIsPerAccount(mode string) bool {
	m, ok := bindModeFor(mode)
	return ok && m.perAccount
}

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
// ignored: that is kae's own redirection (e.g. exported by a bound directory's
// mise fragment), and treating it as the real home would make a shared bind link
// from itself — self-referential symlinks, ELOOP at runtime (found in v0.5.0
// real-machine acceptance).
func (app *App) realToolHome(tool string) string {
	envHome := func(def string) string {
		if dir, ok := app.userToolHomeEnv(tool); ok {
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

// userToolHomeEnv is the tool's isolation variable when the user set it to a
// directory of their own, that is, not one kae's own binding exported.
func (app *App) userToolHomeEnv(tool string) (string, bool) {
	dir := app.Env.Getenv(isolationEnvVar(tool))
	return dir, dir != "" && !app.isKaeManagedHome(dir)
}

// isKaeManagedHome reports whether dir lies inside kae's isolation data root. Any
// path there is kae's, including one kaeManagedHomeKind cannot classify: that is
// what keeps a kae-set value from being taken for the user's own home
// (realToolHome) and a kae link from being taken for the user's (storelink).
func (app *App) isKaeManagedHome(dir string) bool {
	return pathWithin(dir, app.Paths.IsolationDir())
}

// kaeManagedHomeKind classifies dir against kae's isolation data root. Returns
// a mode constant (a bindModes name, or sync), or "" for anything outside the
// isolation root or not recognizable as one of kae's homes. The path segments
// after isolation/ decide:
//
//	isolation/global/<tool>/<account>/      → sync (global isolated, kae use -i)
//	isolation/<pin-id>/<tool>/shared/       → shared (per-dir, kae pin -s)
//	isolation/<pin-id>/<tool>/isolated/…    → isolated (per-dir, kae pin -i)
//	isolation/<pin-id>/<tool>/tree/         → tree (per-dir, kae pin -t)
//
// A pin-id is 16 hex chars, so it never collides with the "global" prefix.
//
// A store segment no bindModes row names answers "" rather than a guess, so a newer
// kae's mechanism is not labelled shared in `kae status` or in pinnedGlobalScope's
// warning. Nothing safety-relevant reads the kind: whether
// the path is kae's is isKaeManagedHome's question, and that does not depend on it.
func (app *App) kaeManagedHomeKind(dir string) string {
	if !app.isKaeManagedHome(dir) {
		return ""
	}
	rel, err := filepath.Rel(app.Paths.IsolationDir(), filepath.Clean(dir))
	if err != nil {
		return ""
	}
	parts := strings.SplitN(rel, string(filepath.Separator), 4)
	if len(parts) >= 1 && parts[0] == paths.GlobalSegment {
		return constants.ModeSync
	}
	if len(parts) < 3 {
		return ""
	}
	if m, ok := bindModeForSegment(parts[2]); ok {
		return m.name
	}
	return ""
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
		// kind is already the user-facing label (shared/isolated/tree/sync).
		mode = kind
	}
	return &pinnedStatus{Profile: profile, Mode: mode}
}

// firstKaeManagedIsolation returns the bind kind the directory's environment
// redirects any tool into, or "" when no isolation env var points into kae's
// data root. "" from the classifier also means a path inside the root that it
// cannot classify; the loop then moves on to the next tool. A pin is a single
// kind, so the first tool that resolves decides.
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

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// pinnedGlobalScope puts the global-scope commands (use / add) on the real home:
// they are inherently global, so kae-managed isolation env values are hidden
// (applyGlobalScope) and the adapters resolve the real base paths; genuinely
// user-set custom homes stay honored. Inside a kae-bound directory it first
// warns that global state is changing and this directory will not see it —
// re-bind with `kae pin`. Idempotent (one warning per command path): the warning
// detection must run before applyGlobalScope hides the env values, and bare use
// delegates to buildSwitch, so both reach this.
func (app *App) pinnedGlobalScope() {
	if app.globalScope {
		return
	}
	// Warn only inside a per-directory pin (shared/isolated/tree), where the change
	// really is invisible to the directory. A terminal activated by the global
	// mise fragment (kind == sync) is not "pinned": `kae use -s`/`-i` is the
	// sanctioned global path there, so it must not print a misleading warning.
	kind := app.firstKaeManagedIsolation()
	if _, perDirectory := bindModeFor(kind); perDirectory {
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
// The polarity is the row's (bindMode.labelStaleOnAccountChange), derived from whether its
// store is per account, so a new mechanism decides it by stating that one fact. A mode kae
// does not recognize answers *not stale*, which is right for an account-keyed mechanism and
// the "keep then destroy on the next run" defect for an account-agnostic one — which is why
// the answer comes from the row and not from a comparison against one mode. Both
// polarities are pinned per mode by TestModeLabelStalePolarityPerMode.
func modeLabelStale(mode, prevAccount, account string) bool {
	m, ok := bindModeFor(mode)
	return ok && m.labelStaleOnAccountChange() && prevAccount != account
}

// modeStoreDir answers "which directory does a per-directory bind in mode point
// tool at" — the row's storeDir, read by the bind planners (which materialize the
// store), the re-bind and the doctor sweep that reads a bound directory's
// credential. ok is false for a mode kae does not recognize, so a caller reading a
// hand-edited or future fragment gets nothing rather than a guessed path.
func (app *App) modeStoreDir(mode, pinID, tool, account string) (dir string, ok bool) {
	m, ok := bindModeFor(mode)
	if !ok {
		return "", false
	}
	return m.storeDir(app.Paths, pinID, tool, account), true
}
