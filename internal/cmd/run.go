package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/envprofile"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// Run environments selected by kae run's -s / -i / --env flags.
const (
	runModeShared   = "shared"   // real home (default; the former auth mode)
	runModeIsolated = "isolated" // the global isolated home, shared with kae use -i
	runModeEnv      = "env"      // inject env-profile vars only
)

// CmdRun executes a child command with a temporarily applied account:
//
//	kae run [-s|-i|--env] [-P <profile>] <tool|all> <name> -- <cmd...>
//
// -s (default) runs against the real home (backup → apply → run → recapture →
// restore, lock held the whole run); -i runs in the per-account global isolated
// home shared with kae use -i (no lock, no live mutation); --env injects the
// env-profile vars only. On success the child's exit code is returned verbatim;
// kae's own exit codes apply only to failures before the child starts (and to a
// failed restore afterwards). The child owns stdio, so --json affects only kae's
// error reports.
func CmdRun(ctx context.Context, args []string) int {
	kaeArgs, childCmd := splitAtDashDash(args)
	if len(childCmd) == 0 {
		return usageError("usage: %s run [-s|-i|--env] [-P <profile>] <tool|all> <name> -- <cmd...>", toolName)
	}
	// --mode was removed in v0.8.0 (hard break); give a targeted pointer rather
	// than the flag package's "not defined" dump.
	for _, a := range kaeArgs {
		if a == "--mode" || a == "-mode" || strings.HasPrefix(a, "--mode=") || strings.HasPrefix(a, "-mode=") {
			return usageError("kae run --mode was removed in v0.8.0; use -s (real home), -i (isolated home), or --env")
		}
	}
	flags, positionals := splitArgs(kaeArgs, "--profile", "P")
	var shared, isolated, envMode bool
	var profileFlag string
	opts, ok := parseCommon("run", flags, false, func(fs *flag.FlagSet) {
		registerScopeFlags(fs, &shared, &isolated)
		fs.BoolVar(&envMode, "env", false, "inject the env-profile vars only (no home redirect, no lock)")
		registerProfileFlag(fs, &profileFlag)
	})
	if !ok {
		return constants.ExitUsage
	}
	runMode, ok := resolveRunMode(shared, isolated, envMode)
	if !ok {
		return constants.ExitUsage
	}
	target, name, ok := runTargetArgs(profileFlag, positionals)
	if !ok {
		return usageError("usage: %s run [-s|-i|--env] [-P <profile>] <tool|all> <name> -- <cmd...>", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runRun(ctx, app, opts, runMode, target, name, childCmd)
}

// resolveRunMode validates the mutually-exclusive run-environment flags and
// reports the selected mode. ok is false (and a usage error already emitted)
// when more than one is set; shared is the default.
func resolveRunMode(shared, isolated, envMode bool) (string, bool) {
	set := 0
	for _, v := range []bool{shared, isolated, envMode} {
		if v {
			set++
		}
	}
	if set > 1 {
		usageError("at most one of -s/--shared, -i/--isolated, --env may be set")
		return "", false
	}
	switch {
	case isolated:
		return runModeIsolated, true
	case envMode:
		return runModeEnv, true
	default:
		return runModeShared, true
	}
}

// runTargetArgs resolves the (target, name) pair from the -P profile flag or the
// positional <tool|all> <name>. -P is sugar for `all <profile>` and takes no
// positional; otherwise exactly two positionals are required.
func runTargetArgs(profileFlag string, positionals []string) (target, name string, ok bool) {
	if profileFlag != "" {
		if len(positionals) != 0 {
			return "", "", false
		}
		return "all", profileFlag, true
	}
	if len(positionals) != 2 {
		return "", "", false
	}
	return positionals[0], positionals[1], true
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

func runRun(ctx context.Context, app *App, opts commonOpts, runMode, target, name string, childCmd []string) int {
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	targets, profileName, err := app.resolveTargets(target, name)
	if err != nil {
		return finish(opts, err)
	}
	switch runMode {
	case runModeShared:
		code, err := app.runAuthTransaction(ctx, targets, childCmd)
		if err != nil {
			return finish(opts, err)
		}
		return code
	case runModeIsolated:
		return app.runIsolatedChild(ctx, opts, targets, profileName != "", childCmd)
	default: // runModeEnv
		return app.runEnvChild(ctx, opts, targets, childCmd)
	}
}

// runEnvChild injects each target's env-profile vars and runs the child. No
// home redirect and no lock — the live credential store is never touched.
func (app *App) runEnvChild(ctx context.Context, opts commonOpts, targets []runTarget, childCmd []string) int {
	be, err := app.secretBackend()
	if err != nil {
		return finish(opts, err)
	}
	var extraEnv []string
	for _, tgt := range targets {
		entries, err := app.envModeEnv(ctx, be, tgt.Tool, tgt.Account)
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

// runIsolatedChild runs the child with each target pointed at its global
// isolated home (isolation/global/<tool>/<account>/, shared with kae use -i):
// no lock and no live mutation, so a concurrent kae use in another shell is
// never blocked and never seen by the isolated process. A tool with no
// home-isolation env var is skipped with a warning when it came from a profile
// (claude/codex stay isolated), or exits 5 for a single explicit tool.
func (app *App) runIsolatedChild(ctx context.Context, opts commonOpts, targets []runTarget, fromProfile bool, childCmd []string) int {
	supported, err := isolatableTargets(targets, fromProfile, "run -i (isolated home)", "run -i")
	if err != nil {
		return finish(opts, err)
	}
	be, err := app.secretBackend()
	if err != nil {
		return finish(opts, err)
	}
	var extraEnv []string
	type homeRow struct{ tool, account, home string }
	var rows []homeRow
	for _, tgt := range supported {
		home, err := app.prepareGlobalIsolatedHome(ctx, be, tgt.Tool, tgt.Account)
		if err != nil {
			return finish(opts, fmt.Errorf("prepare isolated home for %s/%s: %w", tgt.Tool, tgt.Account, err))
		}
		extraEnv = append(extraEnv, isolationEnvVar(tgt.Tool)+"="+home)
		rows = append(rows, homeRow{tgt.Tool, tgt.Account, home})
	}
	// Confusion guard: name the shared home so it is never invisible that
	// run -i and kae use -i share one store per account (docs/RELEASE.md §B).
	for _, r := range rows {
		fmt.Fprintf(os.Stderr,
			"kae: run -i: %s runs in %s\n  (shared with `kae use -i %s`; concurrent `kae use` in other shells is not blocked)\n",
			r.tool, r.home, r.account)
	}
	code, err := runner.RunInteractive(ctx, extraEnv, childCmd[0], childCmd[1:]...)
	if err != nil {
		return finish(opts, fmt.Errorf("run %s: %w", childCmd[0], err))
	}
	return code
}

// isolatableTargets splits targets into those with a home-isolation env var
// (returned) and those without. A tool without one is skipped with a warning
// when it came from a profile (fromProfile = true; claude/codex stay isolated),
// or returns an exit-5 error for a single explicit tool. modeDesc names the mode
// in the exit-5 message; flagName names the surface in the skip warning. An
// empty result with no error cannot occur — a profile of only unsupported tools
// returns exit 5.
func isolatableTargets(targets []runTarget, fromProfile bool, modeDesc, flagName string) ([]runTarget, error) {
	var supported []runTarget
	for _, tgt := range targets {
		if isolationEnvVar(tgt.Tool) != "" {
			supported = append(supported, tgt)
			continue
		}
		if !fromProfile {
			return nil, errf(constants.ExitUnsupported,
				"%s has no home-isolation env var; %s supports claude and codex only", tgt.Tool, modeDesc)
		}
		fmt.Fprintf(os.Stderr,
			"kae: warning: %s has no home-isolation env var; it keeps the real home (%s isolates claude and codex only)\n",
			tgt.Tool, flagName)
	}
	if len(supported) == 0 {
		return nil, errf(constants.ExitUnsupported,
			"no tool in this profile supports home isolation; nothing to isolate")
	}
	return supported, nil
}

// prepareGlobalIsolatedHome materializes the per-account private home under
// isolation/global/<tool>/<account>/ (the captured credential written into it)
// and returns its path. Shared by kae use -i (runUseIsolated) and kae run -i so
// both point at the same home for a given account. The real ~/.<tool> and the
// live credential store are never touched.
func (app *App) prepareGlobalIsolatedHome(ctx context.Context, be secret.Backend, tool, account string) (string, error) {
	home := app.Paths.GlobalIsolatedHomeDir(tool, account)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", fmt.Errorf("create global isolated home: %w", err)
	}
	if err := app.swapDirCredential(ctx, be, tool, account, home); err != nil {
		return "", err
	}
	return home, nil
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
	tool, err := canonicalToolAccount(target, name, "account")
	if err != nil {
		return nil, "", err
	}
	return []runTarget{{Tool: tool, Account: name}}, "", nil
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

// runAuthTransaction is the shared-mode kae run (-s): lock, backup, apply the
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
	// The lock is held for the entire child run: shared mode mutates the live
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
