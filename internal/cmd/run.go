package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/envprofile"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// CmdRun executes a child command with a temporarily applied account:
//
//	kae run [--mode auth|env|home|overlay|bond|pin] <tool|all> <name> -- <cmd...>
//
// On success the child's exit code is returned verbatim; kae's own exit
// codes apply only to failures before the child starts (and to a failed
// restore afterwards). The child owns stdio, so --json affects only kae's
// error reports.
func CmdRun(ctx context.Context, args []string) int {
	kaeArgs, childCmd := splitAtDashDash(args)
	if len(childCmd) == 0 {
		return usageError("usage: %s run [--mode auth|env|home|overlay|bond|pin] <tool|all> <name> -- <cmd...>", toolName)
	}
	flags, positionals := splitArgs(kaeArgs, "--mode")
	mode := modeAuth
	opts, ok := parseCommon("run", flags, false, func(fs *flag.FlagSet) {
		fs.StringVar(&mode, "mode", modeAuth, "switch mode: auth, env, home, overlay, or bond")
	})
	if !ok {
		return constants.ExitUsage
	}
	if !validMode(mode) {
		return usageError("unsupported mode %q (modes: auth, env, home, overlay, bond)", mode)
	}
	if len(positionals) != 2 {
		return usageError("usage: %s run [--mode auth|env|home|overlay|bond|pin] <tool|all> <name> -- <cmd...>", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runRun(ctx, app, opts, mode, positionals[0], positionals[1], childCmd)
}

// splitAtDashDash separates kae's own arguments from the child command.
func splitAtDashDash(args []string) (kaeArgs, childCmd []string) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

func runRun(ctx context.Context, app *App, opts commonOpts, mode, target, name string, childCmd []string) int {
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	targets, _, err := app.resolveTargets(target, name)
	if err != nil {
		return finish(opts, err)
	}
	if mode == modeAuth {
		code, err := app.runAuthTransaction(ctx, targets, childCmd)
		if err != nil {
			return finish(opts, err)
		}
		return code
	}
	// env/home/overlay never mutate live state; they only build child env.
	var be secret.Backend
	if mode == modeEnv {
		if be, err = app.secretBackend(); err != nil {
			return finish(opts, err)
		}
	}
	var extraEnv []string
	for _, tgt := range targets {
		var entries []string
		switch mode {
		case modeEnv:
			entries, err = app.envModeEnv(ctx, be, tgt.Tool, tgt.Account)
		case modeHome:
			entries, err = app.homeModeEnv(tgt.Tool, tgt.Account)
		case modeOverlay:
			entries, err = app.overlayModeEnv(tgt.Tool, tgt.Account)
		case modeBond:
			entries, err = app.bondModeEnv(ctx, tgt.Tool, tgt.Account)
		case modePin:
			entries, err = app.pinModeEnv(ctx, tgt.Tool, tgt.Account)
		}
		if err != nil {
			return finish(opts, fmt.Errorf("%s: %w", tgt.Tool, err))
		}
		extraEnv = append(extraEnv, entries...)
	}
	code, err := runner.RunInteractive(ctx, extraEnv, childCmd[0], childCmd[1:]...)
	if err != nil {
		return finish(opts, fmt.Errorf("run %s: %w", childCmd[0], err))
	}
	return code
}

// runTarget is one tool/account pair resolved from CLI arguments.
type runTarget struct {
	Tool    string
	Account string
}

// resolveTargets expands <tool|all> <name> into concrete tool/account pairs.
func (app *App) resolveTargets(target, name string) ([]runTarget, string, error) {
	if target == "all" {
		profile, ok := app.Config.Profiles[name]
		if !ok {
			return nil, "", errf(constants.ExitNotFound,
				"profile %q is not defined in %s", name, app.ConfigPath)
		}
		targets := []runTarget{}
		for _, tool := range app.enabledTools() {
			if accountName, mapped := profile.Accounts[tool]; mapped {
				targets = append(targets, runTarget{Tool: tool, Account: accountName})
			}
		}
		if len(targets) == 0 {
			return nil, "", errf(constants.ExitNotFound, "profile %q maps no enabled tools", name)
		}
		return targets, name, nil
	}
	if err := validateToolAccount(target, name, "account"); err != nil {
		return nil, "", err
	}
	return []runTarget{{Tool: target, Account: name}}, "", nil
}

// envModeEnv resolves one tool/account env profile into KEY=VALUE entries.
func (app *App) envModeEnv(ctx context.Context, be secret.Backend, tool, accountName string) ([]string, error) {
	profile, found, err := envprofile.Load(app.Paths.EnvProfileDir(tool, accountName))
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errf(constants.ExitNotFound,
			"env profile %s/%s does not exist (create it with: kae env set %s %s KEY=VALUE)",
			tool, accountName, tool, accountName)
	}
	return envprofile.EnvStrings(ctx, be, profile)
}

// runAuthTransaction is the auth-mode kae run: lock, backup, apply the
// target accounts, run the child, recapture refreshed credentials into the
// account snapshots, then restore the previous live state. It shares the
// lock-backup-apply-restore skeleton with buildSwitch (docs/ARCHITECTURE.md
// "Run Transaction") but diverges after apply, so the two stay separate.
func (app *App) runAuthTransaction(ctx context.Context, targets []runTarget, childCmd []string) (int, error) {
	plans, err := app.loadPlansWithSnapshots(ctx, targets)
	if err != nil {
		return 0, err
	}

	be, err := app.secretBackend()
	if err != nil {
		return 0, err
	}
	tools := make([]string, len(plans))
	for i, plan := range plans {
		tools[i] = plan.Tool
	}
	// The lock is held for the entire child run: auth mode mutates the live
	// credential store, so a concurrent switch would corrupt the restore.
	locks, err := app.acquireLocks(tools)
	if err != nil {
		return 0, err
	}
	defer releaseLocks(locks)

	st, err := app.loadState()
	if err != nil {
		return 0, err
	}
	meta, err := app.createBackup(ctx, be, plans, st, "run")
	if err != nil {
		return 0, err
	}

	appliedTools := map[string]bool{}
	for _, plan := range plans {
		if err := applySnapshot(ctx, be, plan); err != nil {
			appliedTools[plan.Tool] = true
			if restoreErr := applyBackup(ctx, be, meta, appliedTools); restoreErr != nil {
				return 0, doubleFailure("apply "+plan.Tool, err, restoreErr, meta.ID)
			}
			return 0, errf(exitOf(err),
				"apply %s failed, previous state restored from backup %s: %v", plan.Tool, meta.ID, err)
		}
		appliedTools[plan.Tool] = true
	}

	childCode, runErr := runner.RunInteractive(ctx, nil, childCmd[0], childCmd[1:]...)

	// Recapture: the child may have refreshed OAuth tokens; persist them into
	// the account snapshots so the next switch applies fresh credentials.
	for _, plan := range plans {
		if err := app.captureSnapshot(ctx, be, plan); err != nil {
			if exitOf(err) == constants.ExitAuthMissing {
				fmt.Fprintf(os.Stderr, "kae: warning: %s logged out during the run; snapshot %s/%s left unchanged\n",
					plan.Tool, plan.Tool, plan.Account)
				continue
			}
			fmt.Fprintf(os.Stderr, "kae: warning: recapture of %s/%s failed: %v\n", plan.Tool, plan.Account, err)
		}
	}

	if err := applyBackup(ctx, be, meta, nil); err != nil {
		return 0, errf(exitOf(err),
			"child finished but restoring the previous auth state failed: %v; run: kae rollback --to %s",
			err, meta.ID)
	}
	if _, err := backup.Prune(ctx, be, app.Paths.BackupsDir(), app.Config.Security.BackupKeep); err != nil {
		fmt.Fprintf(os.Stderr, "kae: warning: backup pruning failed: %v\n", err)
	}
	fmt.Fprintf(os.Stderr, "kae: previous auth state restored (backup %s)\n", meta.ID)

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return childCode, fmt.Errorf("run %s: %w", childCmd[0], runErr)
	}
	return childCode, nil
}
