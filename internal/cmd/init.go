package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
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
	opts, _, ok := parseCommon("init", flags, false)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s init [--json]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runInit(ctx, app, opts)
}

func runInit(_ context.Context, app *App, opts commonOpts) int {
	for _, dir := range []string{app.Paths.ConfigDir, app.Paths.DataDir, app.Paths.StateDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return finish(opts, fmt.Errorf("create %s: %w", dir, err))
		}
	}
	report := initReport{
		SchemaVersion: constants.SchemaVersion,
		OK:            true,
		ConfigPath:    app.displayPath(app.ConfigPath),
	}
	if _, err := os.Stat(app.ConfigPath); os.IsNotExist(err) {
		content := config.InitialContent("")
		if err := patch.WriteFileAtomic(app.ConfigPath, []byte(content), 0o600); err != nil {
			return finish(opts, fmt.Errorf("write config: %w", err))
		}
		report.Created = true
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	if report.Created {
		fmt.Printf("Created %s\n", report.ConfigPath)
	} else {
		fmt.Printf("Config already exists: %s\n", report.ConfigPath)
	}
	fmt.Println("\nNext steps:")
	fmt.Println("  kae doctor                     # check the environment")
	fmt.Println("  kae capture <tool> <account>   # snapshot the current login")
	return constants.ExitOK
}
