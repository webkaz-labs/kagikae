package cmd

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// loginCommand returns the interactive official login invocation per tool.
// kae never reimplements a login flow; it launches the upstream one.
// docs/ADAPTERS.md "Login Commands" is the normative source for this table.
func loginCommand(tool string) []string {
	switch tool {
	case constants.ToolClaude:
		return []string{"claude", "/login"}
	case constants.ToolCodex:
		return []string{"codex", "login"}
	case constants.ToolOpencode:
		return []string{"opencode", "auth", "login"}
	case constants.ToolCursor:
		return []string{"cursor-agent", "login"}
	default:
		return nil
	}
}

// toolBinary is the executable name for a tool's CLI, used in the generated
// mise run tasks. It matches the tool id for every tool except cursor, whose
// binary is cursor-agent (keep in sync with loginCommand and each adapter's
// LookPath probe).
func toolBinary(tool string) string {
	if tool == constants.ToolCursor {
		return "cursor-agent"
	}
	return tool
}

// CmdAdd registers an account:
//
//	kae add <tool> <account> [--restore]      official login flow + snapshot
//	kae add --no-login <tool> <account>       snapshot the current live state
//
// The default backs up the current auth state, launches the official login
// flow, captures the result into the account, and (with --restore) puts the
// previous login back. --no-login skips the flow and snapshots whatever is
// live now (it supports --dry-run; the login flow does not).
func CmdAdd(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	restore, noLogin, global := false, false, false
	opts, ok := parseCommon("add", flags, true, func(fs *flag.FlagSet) {
		fs.BoolVar(&restore, "restore", false, "restore the previous login after capturing (login flow only)")
		fs.BoolVar(&noLogin, "no-login", false, "snapshot the current live auth state without launching a login flow")
		fs.BoolVar(&global, "global", false, "act on the real home, ignoring this directory's pin")
	})
	if !ok {
		return constants.ExitUsage
	}
	opts.Global = global
	if len(positionals) != 2 {
		return usageError("usage: %s add [--no-login] <tool> <account> [--restore]", toolName)
	}
	if noLogin && restore {
		return usageError("--restore needs the login flow; it cannot be combined with --no-login")
	}
	if !noLogin && opts.DryRun {
		return usageError("--dry-run applies to --no-login snapshots only")
	}
	app := newApp(opts.ConfigPath)
	if err := app.pinnedIsolationGuard(opts.Global); err != nil {
		return finish(opts, err)
	}
	if noLogin {
		return runCapture(ctx, app, opts, positionals[0], positionals[1])
	}
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
			"the kae add login flow does not support %s yet (see docs/ROADMAP.md)", tool))
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

	if changed, err := loginChangedAuth(ctx, be, meta, plan); err != nil {
		return finishLoginFailure(ctx, opts, be, meta, restore, "compare auth after login", err)
	} else if !changed {
		// The live state is still the pre-login state, so there is nothing
		// to capture and (with --restore) nothing to put back.
		return finish(opts, errf(constants.ExitAuthUnchanged,
			"%s login flow exited without changing auth; nothing captured (to snapshot the current login as %s/%s, run: kae add --no-login %s %s)",
			tool, tool, accountName, tool, accountName))
	}

	if err := app.captureSnapshot(ctx, be, plan); err != nil {
		return finishLoginFailure(ctx, opts, be, meta, restore, "capture after login", err)
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

// loginChangedAuth reports whether any live artifact differs from the
// pre-login backup, i.e. whether the login flow actually changed auth.
// A missing backup payload counts as changed so an internal inconsistency
// never blocks a legitimate capture.
func loginChangedAuth(ctx context.Context, be secret.Backend, meta backup.Meta, plan toolPlan) (bool, error) {
	records := map[string]backup.ArtifactRecord{}
	for _, rec := range meta.Artifacts {
		if rec.Tool == plan.Tool {
			records[rec.Name] = rec
		}
	}
	for _, sp := range plan.Specs {
		live, err := artifact.ReadLive(ctx, sp)
		if err != nil {
			return false, fmt.Errorf("read live %s/%s: %w", plan.Tool, sp.Name, err)
		}
		rec, ok := records[sp.Name]
		if !ok || live.Present != rec.Present {
			return true, nil
		}
		if !live.Present {
			continue
		}
		prev, found, err := be.Get(ctx, rec.SecretRef)
		if err != nil {
			return false, fmt.Errorf("read backup payload %s: %w", rec.SecretRef, err)
		}
		if !found || !bytes.Equal(live.Data, prev) {
			return true, nil
		}
	}
	return false, nil
}

// finishLoginFailure reports a failed post-login step. With --restore the
// user asked to end up on the previous login no matter what; put it back
// even when the failed step leaves auth in the post-login state.
func finishLoginFailure(ctx context.Context, opts commonOpts, be secret.Backend, meta backup.Meta, restore bool, op string, err error) int {
	if restore {
		if restoreErr := applyBackup(ctx, be, meta, nil); restoreErr != nil {
			return finish(opts, doubleFailure(op, err, restoreErr, meta.ID))
		}
		return finish(opts, errf(exitOf(err),
			"%s failed, previous login restored from backup %s: %v", op, meta.ID, err))
	}
	return finish(opts, fmt.Errorf("%s failed (previous state is in backup %s): %w", op, meta.ID, err))
}
