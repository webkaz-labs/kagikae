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

	// globalScope records that applyGlobalScope already wrapped Env.Getenv
	// (set by --global; modes.go).
	globalScope bool
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

// secretBackend resolves the configured secret backend.
func (app *App) secretBackend() (secret.Backend, error) {
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

// commonOpts are flags shared by every structured command. Global is set
// only by the commands that register --global (use / add / sync).
type commonOpts struct {
	Format     string
	DryRun     bool
	Yes        bool
	NoColor    bool
	ConfigPath string
	Global     bool
}

// parseCommon parses the flag portion of a command line (positionals are
// separated beforehand by splitArgs) and normalizes --json into Format.
// extra, when non-nil, registers command-specific flags on the same set.
func parseCommon(name string, args []string, withDryRun bool, extra func(*flag.FlagSet)) (commonOpts, bool) {
	var opts commonOpts
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.Format, "format", formatText, "output format: text or json")
	jsonFlag := fs.Bool("json", false, "shorthand for --format json")
	fs.BoolVar(&opts.Yes, "yes", false, "non-interactive confirmation (reserved)")
	fs.BoolVar(&opts.NoColor, "no-color", false, "disable color in human text output")
	fs.StringVar(&opts.ConfigPath, "config", "", "explicit config file path")
	if withDryRun {
		fs.BoolVar(&opts.DryRun, "dry-run", false, "print planned actions without writing")
	}
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
