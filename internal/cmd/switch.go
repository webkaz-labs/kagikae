package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/constants"
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
//	kae use [--global] <profile>           every enabled tool in the profile
//	kae use [--global] <tool> <account>    one tool
//
// It always applies, even when the recorded state already matches
// (kae sync is the idempotent variant). Inside a pinned directory it
// refuses unless --global is given (pinnedIsolationGuard).
func CmdUse(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	var global bool
	opts, ok := parseCommon("use", flags, true, func(fs *flag.FlagSet) {
		fs.BoolVar(&global, "global", false, "act on the real home, ignoring this directory's pin")
	})
	if !ok {
		return constants.ExitUsage
	}
	opts.Global = global
	if len(positionals) != 1 && len(positionals) != 2 {
		return usageError("usage: %s use <profile> | %s use <tool> <account>", toolName, toolName)
	}
	app := newApp(opts.ConfigPath)
	if len(positionals) == 1 {
		return runSwitch(ctx, app, opts, "all", positionals[0])
	}
	return runSwitch(ctx, app, opts, positionals[0], positionals[1])
}

func runSwitch(ctx context.Context, app *App, opts commonOpts, target, name string) int {
	report, err := buildSwitch(ctx, app, opts, target, name)
	if err != nil {
		return finish(opts, err)
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	printSwitchReport(app, report)
	return constants.ExitOK
}

func buildSwitch(ctx context.Context, app *App, opts commonOpts, target, name string) (*switchReport, error) {
	if err := app.requireConfig(); err != nil {
		return nil, err
	}
	if err := app.pinnedIsolationGuard(opts.Global); err != nil {
		return nil, err
	}

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

func printSwitchReport(app *App, report *switchReport) {
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
