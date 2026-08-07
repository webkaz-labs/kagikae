package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/envprofile"
	"github.com/webkaz-labs/kagikae/internal/keychain"
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
// home shared with kae use -i (no lock, and no mutation of the live store — it can
// still write the account snapshot, when materializing that home harvests a newer
// credential out of it); --env injects the env-profile vars only. On success the child's exit code is returned verbatim;
// kae's own exit codes apply only to failures before the child starts (and to a
// failed restore afterwards). The child owns stdio, so --json affects only kae's
// error reports.
func CmdRun(ctx context.Context, args []string) int {
	kaeArgs, childCmd := splitAtDashDash(args)
	// childCmd may be empty: for a single-tool target it defaults to that tool's
	// binary (resolved in runRun once the target is known); a profile/all target
	// or a binary-less tool still requires an explicit -- <cmd>.
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
		registerRunFlags(fs, &shared, &isolated, &envMode, &profileFlag)
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

// defaultChildCmd resolves the child command when kae run was given no
// -- <cmd>: a single-tool target runs that tool's upstream binary
// (claude→claude, cursor→cursor-agent, agy→agy). A profile/all target runs no
// single binary, and a tool with no launchable binary cannot be defaulted —
// both require an explicit -- <cmd>, named in the error.
func defaultChildCmd(targets []runTarget, profileName string) ([]string, error) {
	if profileName != "" || len(targets) != 1 {
		return nil, errf(constants.ExitUsage,
			"a profile target runs no single binary; name the command explicitly: %s run [-s|-i|--env] <tool|all> <name> -- <cmd...>", toolName)
	}
	tool := targets[0].Tool
	adp, err := adapter.ForTool(tool)
	if err != nil {
		return nil, err
	}
	bin := adp.Binary()
	if bin == "" {
		return nil, errf(constants.ExitUsage,
			"%s has no launchable binary; name the command explicitly: %s run %s %s -- <cmd...>",
			tool, toolName, tool, targets[0].Account)
	}
	return []string{bin}, nil
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
	if len(childCmd) == 0 {
		childCmd, err = defaultChildCmd(targets, profileName)
		if err != nil {
			return finish(opts, err)
		}
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
// no lock and no mutation of the live store, so a concurrent kae use in another
// shell is never blocked and never seen by the isolated process (the harvest inside
// writeDirCredential does write the account snapshot, which is not the live store). A tool with no
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
		home, err := app.prepareGlobalIsolatedHome(ctx, be, tgt.Tool, tgt.Account, fromProfile)
		if err != nil {
			return finish(opts, fmt.Errorf("prepare isolated home for %s/%s: %w", tgt.Tool, tgt.Account, err))
		}
		extraEnv = append(extraEnv, isolationEnvVar(tgt.Tool)+"="+home)
		// The credential variable too, and pointed at the account's own store rather
		// than at this home: the home is already per-account, but the *credential* has
		// to be the same copy every other binding of that account reads, or the child's
		// first refresh invalidates all of them. Without this line the child would look
		// for a credential under the home's own name and find none.
		if credVar := credentialEnvVar(tgt.Tool); credVar != "" {
			if credDir := app.credStoreDir(tgt.Tool, tgt.Account); credDir != "" {
				extraEnv = append(extraEnv, credVar+"="+credDir)
			}
		}
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
// global live credential store are never touched.
//
// The home becomes the tool's isolation env var (global_fragment.go), so it is
// a bound directory in exactly the sense writeDirCredential means: on a keychain
// platform the credential belongs in this home's own keychain item, not in a
// file the tool stops reading.
//
// fromProfile carries the same profile-vs-explicit split isolatableTargets
// applies to a tool with no isolation env var, and it lives here rather than in
// the callers so a third one cannot get it wrong: a tool resolved from a profile
// whose credential store cannot be scoped to a directory is warned about and
// still gets its home for everything else, while a tool the user named is the
// whole request and the error stands. The home is returned in the tolerated case
// too — it exists by then, and returning "" would set the isolation env var to
// the empty string.
func (app *App) prepareGlobalIsolatedHome(ctx context.Context, be secret.Backend, tool, account string, fromProfile bool) (string, error) {
	home := app.Paths.GlobalIsolatedHomeDir(tool, account)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", fmt.Errorf("create global isolated home: %w", err)
	}
	err := app.writeDirCredential(ctx, be, tool, account, home)
	if err != nil && fromProfile && warnUnisolatableCredential(err, tool, account) {
		err = nil
	}
	return home, err
}

// toolsToRestore decides, per tool, whether run -s's post-child restore should put the
// pre-child copy of its credential back, and warns about each tool it leaves alone.
//
// The restore normally puts back exactly what was live before the child. When the target
// was already the active account, though, that copy is the *same* login the child just
// refreshed, and for a tool whose refresh token rotates single-use writing it back is a
// logout rather than a regression (restoreWouldKillNewerLogin). Those tools are left as
// the child left them: the live store already holds this account's newest copy, which is
// what "restore the previous state" means there.
//
// The result covers every tool that should be restored, which for an untouched run is all
// of them — meta was created from these same plans, so that is the same thing passing nil
// to applyBackup used to mean.
//
// Extracted from the caller so a test can assert the decision for a **set** of tools
// without depending on the order `constants.Tools` happens to put them in. Measured
// 2026-08-05: at the call site, a `continue` mistakenly written as `break` is only
// observable while the skipped tool precedes a restored one, so the command-level test
// pinned it by accident of ordering and a reorder would have reopened it silently.
func (app *App) toolsToRestore(ctx context.Context, be secret.Backend,
	meta backup.Meta, plans []toolPlan,
) map[string]bool {
	restore := make(map[string]bool, len(plans))
	for _, plan := range plans {
		if app.restoreWouldKillNewerLogin(ctx, be, meta, plan) {
			// Warned before the write it replaces, as every warning on this path is.
			fmt.Fprintf(os.Stderr,
				"kae: warning: %s refreshed its credential during the run and %s/%s was already the active "+
					"account, so restoring backup %s would put back a copy %s can no longer refresh; leaving "+
					"the live %s credential as the child left it\n",
				plan.Tool, plan.Tool, plan.Account, meta.ID, plan.Tool, plan.Tool)
			continue
		}
		restore[plan.Tool] = true
	}
	return restore
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
				"profile %q is not defined in %s%s", name, app.ConfigPath, didYouMean(name, app.Config.ProfileNames()))
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
			if restoreErr := app.applyBackup(ctx, be, meta, appliedTools, false); restoreErr != nil {
				return 0, doubleFailure("apply "+plan.Tool, err, restoreErr, meta.ID)
			}
			return 0, errf(exitOf(err),
				"apply %s failed, previous state restored from backup %s: %v", plan.Tool, meta.ID, err)
		}
		appliedTools[plan.Tool] = true
	}

	childCode, runErr := runner.RunInteractive(ctx, nil, childCmd[0], childCmd[1:]...)

	// Coalesce the live reads that follow — the recapture, the restore decision and its
	// attribution all read the same credential and identity, which on darwin is a
	// `security` subprocess each (measured: three post-child reads collapse to one).
	// Opened **after** the child and never around it: the whole reason no cache spans a
	// child run is that the child rotates tokens under kae (docs/ARCHITECTURE.md
	// § Caching). By this line it has exited and the per-tool locks are still held, so
	// nothing can change the store while these reads agree; the writes below invalidate
	// their own entries.
	//
	// Moving this line above the child is **unobservable today**, which is worth knowing
	// before someone "tidies" it there: applySnapshot writes the credential immediately
	// before the child, and that write invalidates the service, so no entry survives into
	// the child window and there is no read in between. The placement is the invariant,
	// not a filter — it starts mattering the moment anything reads between the apply and
	// the child (a verification read, a second tool's probe). Measured 2026-08-06.
	ctx = keychain.WithReadCache(ctx)

	// The child may have moved the credential to the tool's other store (codex
	// under `auto` writes its keychain item and deletes auth.json on its first
	// save), so re-resolve before reading or writing anything: both the recapture
	// below and the restore after it must act on the store the tool reads now
	// (refreshPlan).
	for i := range plans {
		plans[i] = app.refreshPlan(ctx, plans[i])
	}

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

	restore := app.toolsToRestore(ctx, be, meta, plans)
	if err := app.applyBackup(ctx, be, meta, restore, false); err != nil {
		return 0, errf(exitOf(err),
			"child finished but restoring the previous auth state failed: %v; run: kae rollback --to %s",
			err, meta.ID)
	}
	app.pruneBackups(ctx, be)
	if len(restore) > 0 {
		fmt.Fprintf(os.Stderr, "kae: previous auth state restored (backup %s)\n", meta.ID)
	}

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return childCode, fmt.Errorf("run %s: %w", childCmd[0], runErr)
	}
	return childCode, nil
}
