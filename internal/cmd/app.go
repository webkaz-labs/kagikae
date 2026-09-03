package cmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/patch"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/secret"
	"github.com/webkaz-labs/kagikae/internal/state"
)

// App bundles the resolved environment every command needs. Tests construct
// it directly with temp homes and a fixed clock.
type App struct {
	Paths          paths.Paths
	Config         *config.Config
	ConfigPath     string
	ConfigWarnings []string
	ConfigErr      error
	Env            adapter.Env
	Now            func() time.Time

	// globalScope records that applyGlobalScope already wrapped Env.Getenv.
	// Set by pinnedGlobalScope (modes.go) on the first global-scope command.
	globalScope bool

	// backendForTest overrides the resolved secret backend when set. It is a
	// test seam (App is constructed directly in tests; see app.go newApp doc);
	// nil in production, so secretBackend resolves from config as usual.
	backendForTest secret.Backend
	// Test seams for failures and pre-lock races that cannot be scheduled
	// deterministically around non-blocking flock acquisition. Both are nil in
	// production.
	regenGlobalFragmentForTest        func(map[string]string) error
	beforeAccountMutationLocksForTest func()
	// refusalReported holds the per-directory stores whose un-harvested credential the
	// pin-level pass has already reported, so writeDirCredential's backstop does not say
	// it twice (markRefusalReported; docs/CLI.md § kae pin). Scoped to **one bind**: the
	// pass clears it when it starts, because an App outliving two binds would otherwise
	// carry a mark that silences the second one.
	refusalReported map[string]bool
}

// newApp resolves the live environment and loads config. A config problem is
// recorded in ConfigErr; commands other than doctor fail on it.
func newApp(configPath string) *App {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	p := paths.Resolve(os.Getenv, home)
	if configPath == "" {
		configPath = p.ConfigFile()
	}
	cfg, warnings, cfgErr := config.Load(configPath)
	if cfg == nil {
		cfg = config.Default()
	}
	return &App{
		Paths:          p,
		Config:         cfg,
		ConfigPath:     configPath,
		ConfigWarnings: warnings,
		ConfigErr:      cfgErr,
		Env: adapter.Env{
			GOOS:      runtime.GOOS,
			Home:      home,
			Getenv:    claudeDriverGetenv(os.Getenv, cfg),
			Username:  osUsername(),
			LookupEnv: os.LookupEnv,
			LookPath:  exec.LookPath,
		},
		Now: time.Now,
	}
}

// osUsername resolves the OS account name for adapter.Env.Username. It is only
// a fallback for tools that fall back to it themselves when $USER is unset
// (claude's keychain account attribute), so a lookup failure yields "" and the
// adapter applies its own default rather than kae inventing one.
func osUsername() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
}

// claudeDriverGetenv wraps a Getenv so the persisted [tools.claude] driver
// option acts as a fallback for KAE_CLAUDE_DRIVER. The real env var always
// wins: the config value is read only when the variable is unset, keeping the
// ephemeral override the primary surface. A nil config leaves Getenv untouched.
func claudeDriverGetenv(inner func(string) string, cfg *config.Config) func(string) string {
	if cfg == nil {
		return inner
	}
	configured := cfg.Tools[constants.ToolClaude].Driver
	if configured == "" {
		return inner
	}
	return func(key string) string {
		if key == constants.EnvKaeClaudeDriver {
			if v := inner(key); v != "" {
				return v
			}
			return configured
		}
		return inner(key)
	}
}

// requireConfig converts a deferred config error into a command error.
func (app *App) requireConfig() error {
	if app.ConfigErr != nil {
		return errf(constants.ExitInvalidConfig, "invalid config %s: %v", app.ConfigPath, app.ConfigErr)
	}
	return nil
}

// requireConfigFile is requireConfig plus a check that config.toml exists on
// disk, so config mutations never materialize a file from the editor on empty
// content. Fails with a kae init pointer when absent.
func (app *App) requireConfigFile() error {
	if err := app.requireConfig(); err != nil {
		return err
	}
	if _, err := os.Stat(app.ConfigPath); os.IsNotExist(err) {
		return errf(constants.ExitNotFound, "config %s does not exist yet (run: kae init)", app.displayPath(app.ConfigPath))
	}
	return nil
}

// secretBackend resolves the configured secret backend.
func (app *App) secretBackend() (secret.Backend, error) {
	return app.secretBackendForConfig(app.Config)
}

func (app *App) secretBackendForConfig(cfg *config.Config) (secret.Backend, error) {
	if app.backendForTest != nil {
		return app.backendForTest, nil
	}
	be, err := secret.Resolve(cfg.Security.SecretBackend, app.Env.GOOS,
		app.Paths.SecretsDir(), app.Env.LookPath)
	if err != nil {
		return nil, err
	}
	return be, nil
}

// enabledTools returns the canonical-order tools enabled in config.
func (app *App) enabledTools() []string {
	tools := []string{}
	for _, tool := range constants.Tools {
		if app.Config.ToolEnabled(tool) {
			tools = append(tools, tool)
		}
	}
	return tools
}

// Lock names that are not tool ids. They live in the same directory as the
// per-tool locks, so a tool id equal to one of these would silently share that
// tool's lock with an unrelated critical section — guarded by
// TestNonToolLockNamesDoNotCollideWithTools.
const (
	lockNameConfig          = "config"
	lockNameState           = "state"
	lockNameIsolationPrefix = "isolation-"
)

// acquireLocks takes per-tool locks in canonical order; on failure it
// releases everything taken so far.
func (app *App) acquireLocks(tools []string) ([]*lock.Lock, error) {
	wanted := map[string]bool{}
	for _, tool := range tools {
		wanted[tool] = true
	}
	locks := []*lock.Lock{}
	for _, tool := range constants.Tools {
		if !wanted[tool] {
			continue
		}
		l, err := app.acquireNamedLock(tool,
			fmt.Sprintf("another kae process is switching %s; retry shortly", tool))
		if err != nil {
			releaseLocks(locks)
			return nil, err
		}
		locks = append(locks, l)
	}
	return locks, nil
}

func releaseLocks(locks []*lock.Lock) {
	for _, l := range locks {
		l.Release()
	}
}

// acquireNamedLock takes one advisory lock under the runtime lock dir, turning
// a busy lock into the shared lock_busy exit code with the caller's wording.
func (app *App) acquireNamedLock(name, busy string) (*lock.Lock, error) {
	l, err := lock.Acquire(app.Paths.LocksDir(), name)
	if err != nil {
		if errors.Is(err, lock.ErrBusy) {
			return nil, errf(constants.ExitLockBusy, "%s", busy)
		}
		return nil, err
	}
	return l, nil
}

// acquireNamedSharedLock is the shared-holder counterpart to acquireNamedLock.
// It is used only for isolation lifecycle readers: several isolated children may
// use their account-keyed homes at once, while a rename takes the exclusive side.
func (app *App) acquireNamedSharedLock(name, busy string) (*lock.Lock, error) {
	l, err := lock.AcquireShared(app.Paths.LocksDir(), name)
	if err != nil {
		if errors.Is(err, lock.ErrBusy) {
			return nil, errf(constants.ExitLockBusy, "%s", busy)
		}
		return nil, err
	}
	return l, nil
}

func isolationLifecycleLockName(tool string) string {
	return lockNameIsolationPrefix + tool
}

// acquireIsolationLifecycleReaders takes the shared side in canonical tool order.
// `run -i` holds it for the child lifetime. `use -i` and account rename take the
// exclusive side, while several isolated children may keep running concurrently.
func (app *App) acquireIsolationLifecycleReaders(tools []string) ([]*lock.Lock, error) {
	return app.acquireIsolationLifecycleLocks(tools, true)
}

func (app *App) acquireIsolationLifecycleWriters(tools []string) ([]*lock.Lock, error) {
	return app.acquireIsolationLifecycleLocks(tools, false)
}

func (app *App) acquireIsolationLifecycleLocks(tools []string, shared bool) ([]*lock.Lock, error) {
	wanted := map[string]bool{}
	for _, tool := range tools {
		wanted[tool] = true
	}
	locks := []*lock.Lock{}
	for _, tool := range constants.Tools {
		if !wanted[tool] {
			continue
		}
		var (
			l   *lock.Lock
			err error
		)
		if shared {
			l, err = app.acquireNamedSharedLock(isolationLifecycleLockName(tool),
				fmt.Sprintf("another kae process is changing %s isolated account paths; retry shortly", tool))
		} else {
			l, err = app.acquireNamedLock(isolationLifecycleLockName(tool),
				fmt.Sprintf("another kae process is using or changing %s isolated account paths; stop it or retry after it exits", tool))
		}
		if err != nil {
			releaseLocks(locks)
			return nil, err
		}
		locks = append(locks, l)
	}
	return locks, nil
}

// acquireConfigLock takes the shared config lock so config.toml edits do not
// race other kae processes. Released by the caller.
func (app *App) acquireConfigLock() (*lock.Lock, error) {
	return app.acquireNamedLock(lockNameConfig, "another kae process is editing the config; retry shortly")
}

// mutateState is the single seam for state.json writes: take the state lock,
// re-read the file, apply mutate, save. It returns the state as written, so a
// caller can act on the values it just recorded.
//
// It exists because the per-tool locks do not cover this file, so a copy loaded
// earlier in the command can already be stale — see docs/ARCHITECTURE.md
// ("Locking") for the lost update it closes. Two rules follow for callers: a
// *decision* about the state must be made inside mutate, not from a copy read
// before the lock; and a busy lock fails loudly rather than retrying, which is
// safe here because the critical section is one read plus one atomic write and
// a switch that reaches it has a backup to restore from.
func (app *App) mutateState(mutate func(*state.State)) (*state.State, error) {
	l, err := app.acquireNamedLock(lockNameState, "another kae process is recording state; retry shortly")
	if err != nil {
		return nil, err
	}
	defer l.Release()
	st, err := app.loadState()
	if err != nil {
		return nil, err
	}
	mutate(st)
	st.UpdatedAt = app.Now().UTC()
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		return nil, err
	}
	return st, nil
}

// inspectState runs a read-only decision while holding the state lock. A caller
// that acts on the answer afterwards must keep the outer lock that excludes every
// writer of the field it inspected; account rename does that with the tool and
// isolation lifecycle locks. Unlike mutateState this never writes, including on
// a refusal path.
func (app *App) inspectState(inspect func(*state.State) error) error {
	l, err := app.acquireNamedLock(lockNameState, "another kae process is recording state; retry shortly")
	if err != nil {
		return err
	}
	defer l.Release()
	st, err := app.loadState()
	if err != nil {
		return err
	}
	return inspect(st)
}

// mutateSyncedAndFragment keeps preparation, the state record, and the fragment
// generated from it in one state critical section. prepare may be nil. Holding
// the lock before prepare ensures a busy state writer cannot be discovered only
// after prepare has materialized an isolated home or refreshed a snapshot.
//
// State and fragment cannot be one filesystem transaction. If fragment
// regeneration returns an error, this restores the pre-mutation state while the
// same lock is held; both individual file writes are atomic. A process crash
// between the two writes remains detectable as a derived-fragment mismatch.
func (app *App) mutateSyncedAndFragment(prepare func() error, mutate func(*state.State) bool) (*state.State, error) {
	l, err := app.acquireNamedLock(lockNameState, "another kae process is recording state; retry shortly")
	if err != nil {
		return nil, err
	}
	defer l.Release()
	previous, err := app.loadState()
	if err != nil {
		return nil, err
	}
	if prepare != nil {
		if err := prepare(); err != nil {
			return nil, err
		}
	}
	st := cloneState(previous)
	regen := app.regenGlobalFragment
	if app.regenGlobalFragmentForTest != nil {
		regen = app.regenGlobalFragmentForTest
	}
	changed := mutate(st)
	if !changed {
		consistent, _ := app.globalFragmentConsistent(st.Synced)
		if consistent {
			return st, nil
		}
		if err := regen(st.Synced); err != nil {
			return nil, fmt.Errorf("reconcile global mise fragment: %w", err)
		}
		return st, nil
	}
	st.UpdatedAt = app.Now().UTC()
	if err := saveSyncedStateAndFragment(previous, st,
		func(value *state.State) error { return state.Save(app.Paths.StateFile(), value) }, regen); err != nil {
		return nil, err
	}
	return st, nil
}

func cloneState(src *state.State) *state.State {
	dst := *src
	dst.Active = make(map[string]string, len(src.Active))
	for tool, accountName := range src.Active {
		dst.Active[tool] = accountName
	}
	dst.Synced = make(map[string]string, len(src.Synced))
	for tool, accountName := range src.Synced {
		dst.Synced[tool] = accountName
	}
	return &dst
}

// saveSyncedStateAndFragment is split out so the double-failure branch is
// deterministic to test. The production caller supplies atomic state and
// fragment writers and holds state.lock for the whole call.
func saveSyncedStateAndFragment(previous, next *state.State,
	save func(*state.State) error, regenerate func(map[string]string) error,
) error {
	if err := save(next); err != nil {
		return err
	}
	if err := regenerate(next.Synced); err != nil {
		if restoreErr := save(previous); restoreErr != nil {
			return fmt.Errorf("regenerate global mise fragment: %w; restoring previous state also failed: %v", err, restoreErr)
		}
		return fmt.Errorf("regenerate global mise fragment (previous state restored): %w", err)
	}
	return nil
}

// editConfig applies mutate to config.toml through the comment-preserving
// editor, writes it back atomically, and reloads app.Config. The caller holds
// the config lock. This is the single config-mutation seam shared by
// account rm/rename and the kae profile commands.
func (app *App) editConfig(mutate func(*config.Editor)) error {
	data, err := os.ReadFile(app.ConfigPath)
	if err != nil {
		return fmt.Errorf("read config for edit: %w", err)
	}
	ed, err := config.NewEditor(data)
	if err != nil {
		return err
	}
	mutate(ed)
	out, err := ed.Bytes()
	if err != nil {
		return err
	}
	if err := patch.WriteFileAtomic(app.ConfigPath, out, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	cfg, _, err := config.Load(app.ConfigPath)
	if err != nil {
		return fmt.Errorf("reload config after edit: %w", err)
	}
	app.Config = cfg
	return nil
}

// cmdError carries a deterministic exit code with its message.
type cmdError struct {
	exit    int
	message string
}

func (e *cmdError) Error() string { return e.message }

func errf(exit int, format string, args ...any) *cmdError {
	return &cmdError{exit: exit, message: fmt.Sprintf(format, args...)}
}

// exitOf maps an error to its deterministic exit code.
func exitOf(err error) int {
	var ce *cmdError
	switch {
	case err == nil:
		return constants.ExitOK
	case errors.As(err, &ce):
		return ce.exit
	case errors.Is(err, artifact.ErrUnsafe):
		return constants.ExitUnsafeRefused
	case errors.Is(err, adapter.ErrUnsupported):
		return constants.ExitUnsupported
	case errors.Is(err, secret.ErrUnavailable):
		return constants.ExitSecretStore
	case errors.Is(err, lock.ErrBusy):
		return constants.ExitLockBusy
	case errors.Is(err, os.ErrPermission):
		return constants.ExitPermission
	default:
		return constants.ExitError
	}
}

// errorReport is the JSON error contract.
type errorReport struct {
	SchemaVersion int    `json:"schema_version"`
	OK            bool   `json:"ok"`
	ErrorCode     string `json:"error_code"`
	Message       string `json:"message"`
}

// finish reports err in the requested format and returns the exit code.
func finish(opts commonOpts, err error) int {
	if err == nil {
		return constants.ExitOK
	}
	exit := exitOf(err)
	if opts.Format == formatJSON {
		encodeJSON(errorReport{
			SchemaVersion: constants.SchemaVersion,
			OK:            false,
			ErrorCode:     constants.ErrorCode(exit),
			Message:       err.Error(),
		})
		return exit
	}
	fmt.Fprintln(os.Stderr, "kae:", err)
	return exit
}

// commonOpts are flags shared by every structured command.
type commonOpts struct {
	Format     string
	DryRun     bool
	Yes        bool
	NoColor    bool
	ConfigPath string
	// IdentityOverride carries `kae add --identity <value>`: the login identity
	// to record when auto-detection is unavailable. Empty for every other command.
	IdentityOverride string
}

// parseCommon parses the flag portion of a command line (positionals are
// separated beforehand by splitArgs) and normalizes --json into Format.
// extra, when non-nil, registers command-specific flags on the same set.
func parseCommon(name string, args []string, withDryRun bool, extra func(*flag.FlagSet)) (commonOpts, bool) {
	var opts commonOpts
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonFlag := registerCommonFlags(fs, &opts, withDryRun)
	if extra != nil {
		extra(fs)
	}
	if err := fs.Parse(args); err != nil {
		return opts, false
	}
	if *jsonFlag {
		opts.Format = formatJSON
	}
	if opts.Format != formatText && opts.Format != formatJSON {
		usageError("unsupported format: %s", opts.Format)
		return opts, false
	}
	return opts, true
}

// registerCommonFlags registers the flags every command accepts (format/json/
// yes/no-color/config, plus dry-run when withDryRun) on fs and returns the
// --json shorthand pointer. Shared by parseCommon (the real parse) and
// flagSetFor (the completion-backend flag enumerator), so the flag set listed by
// `kae __complete flags` never drifts from what the parser accepts.
func registerCommonFlags(fs *flag.FlagSet, opts *commonOpts, withDryRun bool) *bool {
	fs.StringVar(&opts.Format, "format", formatText, "output format: text or json")
	jsonFlag := fs.Bool("json", false, "shorthand for --format json")
	fs.BoolVar(&opts.Yes, "yes", false, "non-interactive confirmation (reserved)")
	fs.BoolVar(&opts.NoColor, "no-color", false, "disable color in human text output")
	fs.StringVar(&opts.ConfigPath, "config", "", "explicit config file path")
	if withDryRun {
		fs.BoolVar(&opts.DryRun, "dry-run", false, "print planned actions without writing")
	}
	return jsonFlag
}

// registerScopeFlags registers the environment selector flags shared by
// `kae use` and `kae pin`: -s/--shared (the default) and -i/--isolated. The
// help text is generic because it only surfaces on a flag parse error; the
// hand-written help in printHelp documents the per-verb meaning.
func registerScopeFlags(fs *flag.FlagSet, shared, isolated *bool) {
	fs.BoolVar(shared, "shared", false, "shared environment (default)")
	fs.BoolVar(shared, "s", false, "alias for --shared")
	fs.BoolVar(isolated, "isolated", false, "isolated environment")
	fs.BoolVar(isolated, "i", false, "alias for --isolated")
}

// registerProfileFlag registers the --profile flag and its -P short form,
// shared by bare `kae use`, `kae run`, and `kae mise init`.
func registerProfileFlag(fs *flag.FlagSet, p *string) {
	fs.StringVar(p, "profile", "", "profile to resolve (default: $KAE_PROFILE, then config default_profile)")
	fs.StringVar(p, "P", "", "alias for --profile")
}

// resolveScope validates the mutually-exclusive scope flags and reports the
// selected environment. ok is false (and a usage error already emitted) when
// both are set; shared is the default, so isolatedMode echoes isolated.
func resolveScope(shared, isolated bool) (isolatedMode, ok bool) {
	if shared && isolated {
		usageError("--shared and --isolated are mutually exclusive")
		return false, false
	}
	return isolated, true
}
