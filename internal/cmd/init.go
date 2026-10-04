package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/patch"
)

type initReport struct {
	SchemaVersion int    `json:"schema_version"`
	OK            bool   `json:"ok"`
	Created       bool   `json:"created"`
	ConfigPath    string `json:"config_path"`
}

func CmdInit(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("init", flags, false, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageLine(toolName + " init [--json]")
	}
	app := newApp(opts.ConfigPath)
	return runInit(ctx, app, opts)
}

func runInit(_ context.Context, app *App, opts commonOpts) int {
	l, err := app.acquireConfigLock()
	if err != nil {
		return finish(opts, err)
	}
	defer l.Release()

	info, statErr := os.Lstat(app.ConfigPath)
	if statErr != nil && !os.IsNotExist(statErr) {
		return finish(opts, l10n.Errorf("inspect config: %w", statErr))
	}
	if statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			info, err = os.Stat(app.ConfigPath)
			if err != nil {
				return finish(opts, l10n.Errorf("inspect config target: %w", err))
			}
		}
		if !info.Mode().IsRegular() {
			return finish(opts, errf(constants.ExitUnsafeRefused, "config is not a regular file: %s", app.displayPath(app.ConfigPath)))
		}
		if _, _, err := config.Load(app.ConfigPath); err != nil {
			return finish(opts, errInvalidConfig(app.displayPath(app.ConfigPath), err))
		}
	}
	for _, dir := range []string{app.Paths.ConfigDir, app.Paths.DataDir, app.Paths.StateDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return finish(opts, l10n.Errorf("create %s: %w", dir, err))
		}
	}
	report := initReport{
		SchemaVersion: constants.SchemaVersion,
		OK:            true,
		ConfigPath:    app.displayPath(app.ConfigPath),
	}
	if os.IsNotExist(statErr) {
		content := config.InitialContent("")
		if err := patch.WriteFileAtomic(app.ConfigPath, []byte(content), 0o600); err != nil {
			return finish(opts, l10n.Errorf("write config: %w", err))
		}
		report.Created = true
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	if report.Created {
		reportf("Created %s", report.ConfigPath)
	} else {
		reportf("Config already exists: %s", report.ConfigPath)
	}
	fmt.Println()
	reportf("Next steps:")
	reportf("  kae doctor                             # check the environment")
	reportf("  kae add --no-login <tool> <account>    # snapshot the current login")
	return constants.ExitOK
}
