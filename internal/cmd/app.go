package cmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
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
			GOOS:     runtime.GOOS,
			Home:     home,
			Getenv:   claudeDriverGetenv(os.Getenv, cfg),
			LookPath: exec.LookPath,
		},
		Now: time.Now,
	}
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
	if app.backendForTest != nil {
		return app.backendForTest, nil
	}
	be, err := secret.Resolve(app.Config.Security.SecretBackend, app.Env.GOOS,
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
		l, err := lock.Acquire(app.Paths.LocksDir(), tool)
		if err != nil {
			releaseLocks(locks)
			if errors.Is(err, lock.ErrBusy) {
				return nil, errf(constants.ExitLockBusy, "another kae process is switching %s; retry shortly", tool)
			}
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

// acquireConfigLock takes the shared config lock so config.toml edits do not
// race other kae processes. Released by the caller.
func (app *App) acquireConfigLock() (*lock.Lock, error) {
	l, err := lock.Acquire(app.Paths.LocksDir(), "config")
	if err != nil {
		if errors.Is(err, lock.ErrBusy) {
			return nil, errf(constants.ExitLockBusy, "another kae process is editing the config; retry shortly")
		}
		return nil, err
	}
	return l, nil
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
