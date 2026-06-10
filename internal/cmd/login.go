package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// loginCommand returns the interactive official login invocation per tool.
// kae never reimplements a login flow; it launches the upstream one.
func loginCommand(tool string) []string {
	switch tool {
	case constants.ToolClaude:
		return []string{"claude", "/login"}
	case constants.ToolCodex:
		return []string{"codex", "login"}
	case constants.ToolGemini:
		// Gemini CLI has no login subcommand; the auth flow runs on startup.
		return []string{"gemini"}
	default:
		return nil
	}
}

// CmdLogin backs up the current auth state, launches the official login
// flow, captures the result into the account, and (with --restore) puts the
// previous login back.
func CmdLogin(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	restore := false
	opts, ok := parseCommon("login", flags, false, func(fs *flag.FlagSet) {
		fs.BoolVar(&restore, "restore", false, "restore the previous login after capturing")
	})
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 2 {
		return usageError("usage: %s login <tool> <account> [--restore]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runLogin(ctx, app, opts, positionals[0], positionals[1], restore)
}

func runLogin(ctx context.Context, app *App, opts commonOpts, tool, accountName string, restore bool) int {
	if err := validateToolAccount(tool, accountName, "account"); err != nil {
		return finish(opts, err)
	}
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	command := loginCommand(tool)
	if command == nil {
		return finish(opts, errf(constants.ExitUnsupported,
			"kae login does not support %s yet (see docs/ROADMAP.md)", tool))
	}
	plan, err := app.planTool(ctx, tool, accountName)
	if err != nil {
		return finish(opts, err)
	}
	be, err := app.secretBackend()
	if err != nil {
		return finish(opts, err)
	}
	locks, err := app.acquireLocks([]string{tool})
	if err != nil {
		return finish(opts, err)
	}
	defer releaseLocks(locks)

	st, err := app.loadState()
	if err != nil {
		return finish(opts, err)
	}
	meta, err := app.createBackup(ctx, be, []toolPlan{plan}, st, "login")
	if err != nil {
		return finish(opts, err)
	}

	fmt.Fprintf(os.Stderr, "kae: complete the %s login flow; the result is captured as %s/%s when it exits (previous state backed up as %s)\n",
		tool, tool, accountName, meta.ID)
	if code, err := runner.RunInteractive(ctx, nil, command[0], command[1:]...); err != nil {
		return finish(opts, fmt.Errorf("launch %s login: %w", tool, err))
	} else if code != 0 {
		fmt.Fprintf(os.Stderr, "kae: %s exited with %d; capturing whatever auth state is live now\n", command[0], code)
	}

	if err := app.captureSnapshot(ctx, be, plan); err != nil {
		return finish(opts, fmt.Errorf("capture after login failed (previous state is in backup %s): %w", meta.ID, err))
	}

	if restore {
		if err := applyBackup(ctx, be, meta, nil); err != nil {
			return finish(opts, errf(exitOf(err),
				"captured %s/%s but restoring the previous login failed: %v; run: kae rollback --to %s",
				tool, accountName, err, meta.ID))
		}
		fmt.Printf("Captured %s/%s and restored the previous login\n", tool, accountName)
		return constants.ExitOK
	}
	if err := app.saveActive(st, map[string]string{tool: accountName}, ""); err != nil {
		return finish(opts, err)
	}
	fmt.Printf("Captured %s/%s (now active)\n", tool, accountName)
	return constants.ExitOK
}
