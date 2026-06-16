package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/state"
)

type switchResult struct {
	Tool     string   `json:"tool"`
	Account  string   `json:"account"`
	Driver   string   `json:"driver"`
	Applied  bool     `json:"applied"`
	Actions  []action `json:"actions"`
	Warnings []string `json:"warnings"`
}

type switchReport struct {
	SchemaVersion int            `json:"schema_version"`
	OK            bool           `json:"ok"`
	DryRun        bool           `json:"dry_run"`
	Profile       *string        `json:"profile"`
	BackupID      string         `json:"backup_id,omitempty"`
	Results       []switchResult `json:"results"`
}

// CmdUse switches now, in global scope (alias: kae u):
//
//	kae use [-s|-i] [-P <profile>]      bare: resolve the profile, apply it
//	                                    idempotently (the folded `apply`)
//	kae use [-s|-i] <profile>           every enabled tool in the profile
//	kae use [-s|-i] <tool> <account>    one tool
//
// With an explicit positional it always applies, even when the recorded state
// already matches; bare use (no positional) resolves the profile and is
// idempotent (the folded `apply`, with --quiet for hooks). use is inherently
// global, so it always acts on the real home; inside a pinned directory it
// warns that it is changing global state (pinnedGlobalScope). --shared/-s (the
// default) switches the real home in place; --isolated/-i points every terminal
// at a per-account private home via a kae-owned global mise fragment.
func CmdUse(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args, "--profile", "P")
	var shared, isolated, quiet bool
	var profileFlag string
	opts, ok := parseCommon("use", flags, true, func(fs *flag.FlagSet) {
		registerScopeFlags(fs, &shared, &isolated)
		fs.BoolVar(&quiet, "quiet", false, "suppress the success report (for hooks; bare use)")
		registerProfileFlag(fs, &profileFlag)
	})
	if !ok {
		return constants.ExitUsage
	}
	isolatedMode, ok := resolveScope(shared, isolated)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) > 2 {
		return usageError("usage: %s use [-s|-i] [-P <profile>] | %s use [-s|-i] <profile> | %s use [-s|-i] <tool> <account>", toolName, toolName, toolName)
	}
	app := newApp(opts.ConfigPath)
	if len(positionals) == 0 {
		return runUseBare(ctx, app, opts, isolatedMode, profileFlag, quiet)
	}
	if profileFlag != "" {
		return usageError("-P <profile> selects a profile for bare `kae use`; drop the positional or the flag")
	}
	target, name := "all", positionals[0]
	if len(positionals) == 2 {
		target, name = positionals[0], positionals[1]
	}
	if isolatedMode {
		return runUseIsolated(ctx, app, opts, target, name)
	}
	return runSwitch(ctx, app, opts, target, name)
}

func runSwitch(ctx context.Context, app *App, opts commonOpts, target, name string) int {
	report, err := buildSwitch(ctx, app, opts, target, name)
	if err != nil {
		return finish(opts, err)
	}
	// kae use -s is the documented teardown of kae use -i: after switching the
	// real home in place, drop the switched tools from state.synced and
	// regenerate (or delete) the global mise fragment. A no-op when no switched
	// tool is globally isolated, so the plain shared switch is unaffected.
	if !report.DryRun {
		tools := make([]string, 0, len(report.Results))
		for _, r := range report.Results {
			tools = append(tools, r.Tool)
		}
		if err := app.teardownSynced(tools); err != nil {
			return finish(opts, err)
		}
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	printSwitchReport(report)
	return constants.ExitOK
}

func buildSwitch(ctx context.Context, app *App, opts commonOpts, target, name string) (*switchReport, error) {
	if err := app.requireConfig(); err != nil {
		return nil, err
	}
	app.pinnedGlobalScope()

	targets, profileName, err := app.resolveTargets(target, name)
	if err != nil {
		return nil, err
	}
	// Plans include each captured snapshot; uncaptured targets fail before
	// anything is written.
	plans, err := app.loadPlansWithSnapshots(ctx, targets)
	if err != nil {
		return nil, err
	}

	report := &switchReport{
		SchemaVersion: constants.SchemaVersion,
		OK:            true,
		DryRun:        opts.DryRun,
		Results:       []switchResult{},
	}
	if profileName != "" {
		report.Profile = &profileName
	}
	for _, plan := range plans {
		report.Results = append(report.Results, switchResult{
			Tool: plan.Tool, Account: plan.Account, Driver: plan.Driver,
			Applied: !opts.DryRun, Actions: app.actionsOf(plan.Specs),
			Warnings: plan.Warnings,
		})
	}
	if opts.DryRun {
		return report, nil
	}

	be, err := app.secretBackend()
	if err != nil {
		return nil, err
	}
	tools := make([]string, len(plans))
	for i, plan := range plans {
		tools[i] = plan.Tool
	}
	locks, err := app.acquireLocks(tools)
	if err != nil {
		return nil, err
	}
	defer releaseLocks(locks)

	st, err := app.loadState()
	if err != nil {
		return nil, err
	}
	meta, err := app.createBackup(ctx, be, plans, st, "switch")
	if err != nil {
		return nil, err
	}
	report.BackupID = meta.ID

	appliedTools := map[string]bool{}
	for _, plan := range plans {
		if err := applySnapshot(ctx, be, plan); err != nil {
			appliedTools[plan.Tool] = true // partially-applied tool needs restore too
			if restoreErr := applyBackup(ctx, be, meta, appliedTools); restoreErr != nil {
				return nil, doubleFailure("switch "+plan.Tool, err, restoreErr, meta.ID)
			}
			return nil, errf(exitOf(err),
				"switch %s failed, previous state restored from backup %s: %v",
				plan.Tool, meta.ID, err)
		}
		appliedTools[plan.Tool] = true
	}

	updates := map[string]string{}
	for _, plan := range plans {
		updates[plan.Tool] = plan.Account
	}
	if err := app.saveActive(st, updates, profileName); err != nil {
		// live state changed but the record failed: restore so state.json and
		// reality cannot diverge.
		if restoreErr := applyBackup(ctx, be, meta, nil); restoreErr != nil {
			return nil, doubleFailure("recording state", err, restoreErr, meta.ID)
		}
		return nil, errf(constants.ExitError,
			"recording state failed, live state restored from backup %s: %v", meta.ID, err)
	}
	if _, err := backup.Prune(ctx, be, app.Paths.BackupsDir(), app.Config.Security.BackupKeep); err != nil {
		fmt.Fprintf(os.Stderr, "kae: warning: backup pruning failed: %v\n", err)
	}
	return report, nil
}

func printSwitchReport(report *switchReport) {
	if report.DryRun {
		if report.Profile != nil {
			fmt.Printf("Would switch profile to %s\n", *report.Profile)
		}
		for _, result := range report.Results {
			fmt.Printf("\n%s -> %s (driver: %s)\n", result.Tool, result.Account, result.Driver)
			for _, act := range result.Actions {
				if act.Pointer != "" {
					fmt.Printf("  patch %s %s\n", act.Target, act.Pointer)
				} else {
					fmt.Printf("  replace %s\n", act.Target)
				}
			}
			fmt.Println("  preserve all other keys, settings, skills, hooks, history")
			for _, warning := range result.Warnings {
				fmt.Printf("  warning: %s\n", warning)
			}
		}
		return
	}
	for _, result := range report.Results {
		fmt.Printf("Switched %s -> %s\n", result.Tool, result.Account)
		for _, warning := range result.Warnings {
			fmt.Printf("  warning: %s\n", warning)
		}
	}
	if report.Profile != nil {
		fmt.Printf("Active profile: %s\n", *report.Profile)
	}
	if report.BackupID != "" {
		fmt.Printf("Backup: %s (undo: kae rollback)\n", report.BackupID)
	}
}

// globalIsolateResult is one tool's row of the kae use -i report.
type globalIsolateResult struct {
	Tool    string `json:"tool"`
	Account string `json:"account"`
	Home    string `json:"home"`
}

// globalIsolateReport is the JSON contract of kae use --isolated.
type globalIsolateReport struct {
	SchemaVersion int                   `json:"schema_version"`
	OK            bool                  `json:"ok"`
	DryRun        bool                  `json:"dry_run"`
	Fragment      string                `json:"fragment"`
	Results       []globalIsolateResult `json:"results"`
}

// runUseIsolated performs the global isolated switch (kae use -i): for each
// target it prepares a full per-account private home under
// isolation/global/<tool>/<account>/ (materializing the captured credential),
// records the binding in state.synced, and regenerates the kae-owned global
// mise fragment so every globally activated terminal points the tool there on
// its next prompt. The real ~/.<tool> is never touched. claude and codex only
// (a tool with no stable home-isolation env var exits 5). When mise activation
// is not detected it prints the export fallback for the current shell.
//
// Unlike the shared switch it takes no per-tool locks and writes no backup: it
// mutates no live credential store, only kae's own data dirs and state.json
// (mirroring kae pin, which is also lock-free).
func runUseIsolated(ctx context.Context, app *App, opts commonOpts, target, name string) int {
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	// Inside a per-directory pin this warns that global state is changing; in a
	// globally-isolated terminal it stays silent (use -i is the global path).
	app.pinnedGlobalScope()
	targets, _, err := app.resolveTargets(target, name)
	if err != nil {
		return finish(opts, err)
	}
	for _, tgt := range targets {
		if isolationEnvVar(tgt.Tool) == "" {
			return finish(opts, errf(constants.ExitUnsupported,
				"%s has no home-isolation env var; global isolated mode (kae use -i) supports claude and codex only",
				tgt.Tool))
		}
	}

	report := globalIsolateReport{
		SchemaVersion: constants.SchemaVersion,
		OK:            true,
		DryRun:        opts.DryRun,
		Fragment:      app.Paths.MiseGlobalFragmentFile(),
		Results:       []globalIsolateResult{},
	}

	for _, tgt := range targets {
		report.Results = append(report.Results, globalIsolateResult{
			Tool: tgt.Tool, Account: tgt.Account,
			Home: app.Paths.GlobalIsolatedHomeDir(tgt.Tool, tgt.Account),
		})
	}

	if opts.DryRun {
		if opts.Format == formatJSON {
			return encodeJSON(report)
		}
		for _, r := range report.Results {
			fmt.Printf("Would globally isolate %s -> %s\n  home: %s\n", r.Tool, r.Account, r.Home)
		}
		fmt.Printf("Would write %s\n", report.Fragment)
		return constants.ExitOK
	}

	be, err := app.secretBackend()
	if err != nil {
		return finish(opts, err)
	}
	st, err := app.loadState()
	if err != nil {
		return finish(opts, err)
	}
	if st.Synced == nil {
		st.Synced = map[string]string{}
	}
	for _, r := range report.Results {
		if err := os.MkdirAll(r.Home, 0o700); err != nil {
			return finish(opts, fmt.Errorf("create global isolated home for %s: %w", r.Tool, err))
		}
		if err := app.swapDirCredential(ctx, be, r.Tool, r.Account, r.Home); err != nil {
			return finish(opts, fmt.Errorf("materialize credential for %s/%s: %w", r.Tool, r.Account, err))
		}
		st.Synced[r.Tool] = r.Account
	}
	st.UpdatedAt = app.Now().UTC()
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		return finish(opts, err)
	}
	if err := app.regenGlobalFragment(st.Synced); err != nil {
		return finish(opts, err)
	}

	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	for _, r := range report.Results {
		fmt.Printf("Globally isolated %s -> %s (private home; real ~/.%s untouched)\n", r.Tool, r.Account, r.Tool)
	}
	fmt.Printf("Wrote %s (regenerated from kae state).\n", report.Fragment)
	if app.miseActivated() {
		fmt.Println("mise applies it on the next prompt (or run `mise env`).")
	} else {
		fmt.Fprintln(os.Stderr, "kae: warning: mise activation not detected; the binding takes effect once mise is active.")
		fmt.Fprintln(os.Stderr, "kae: to apply it in the current shell now, run:")
		fmt.Fprint(os.Stderr, app.globalExportFallback(st.Synced))
	}
	return constants.ExitOK
}
