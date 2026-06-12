package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// CmdEdit opens the config file in the user's editor and re-validates it:
//
//	kae edit
//
// Editor resolution: $VISUAL, then $EDITOR, then vi. A missing config is
// pointed at kae init instead of editing an empty file; an invalid result
// exits 2 with the parse error so the mistake is visible immediately.
func CmdEdit(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("edit", flags, false, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s edit", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runEdit(ctx, app, opts)
}

func runEdit(ctx context.Context, app *App, opts commonOpts) int {
	if _, err := os.Stat(app.ConfigPath); os.IsNotExist(err) {
		return finish(opts, errf(constants.ExitNotFound,
			"config %s does not exist yet (run: kae init)", app.displayPath(app.ConfigPath)))
	}
	editor := app.Env.Getenv("VISUAL")
	if editor == "" {
		editor = app.Env.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	// $VISUAL/$EDITOR may carry arguments ("code --wait"); split on spaces.
	parts := strings.Fields(editor)
	code, err := runner.RunInteractive(ctx, nil, parts[0], append(parts[1:], app.ConfigPath)...)
	if err != nil {
		return finish(opts, fmt.Errorf("launch editor %s: %w", parts[0], err))
	}
	if code != 0 {
		return finish(opts, errf(constants.ExitError,
			"editor %s exited with %d; the config is left as last saved", parts[0], code))
	}
	if _, warnings, err := config.Load(app.ConfigPath); err != nil {
		return finish(opts, errf(constants.ExitInvalidConfig,
			"config %s is invalid after editing: %v (run kae edit again)",
			app.displayPath(app.ConfigPath), err))
	} else {
		for _, warning := range warnings {
			fmt.Fprintf(os.Stderr, "kae: warning: %s\n", warning)
		}
	}
	fmt.Printf("Config OK: %s\n", app.displayPath(app.ConfigPath))
	return constants.ExitOK
}
