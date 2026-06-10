package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/webkaz-labs/kagikae/internal/account"
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

func CmdSwitch(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("switch", flags, true, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 2 {
		return usageError("usage: %s switch <tool|all> <account-or-profile>", toolName)
	}
	app := newApp(opts.ConfigPath)
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

	targets, profileName, err := app.resolveTargets(target, name)
	if err != nil {
		return nil, err
	}
	plans := make([]toolPlan, 0, len(targets))
	for _, tgt := range targets {
		plan, err := app.planTool(ctx, tgt.Tool, tgt.Account)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", tgt.Tool, err)
		}
		plans = append(plans, plan)
	}

	// Every target account must be captured before anything is written.
	for i := range plans {
		acc, found, err := account.Load(app.Paths.AccountDir(plans[i].Tool, plans[i].Account))
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, errf(constants.ExitNotFound,
				"account %s/%s is not captured yet (run: kae capture %s %s)",
				plans[i].Tool, plans[i].Account, plans[i].Tool, plans[i].Account)
		}
		plans[i].Meta = acc
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
				return nil, errf(exitOf(err),
					"switch %s failed (%v) and rollback also failed (%v); run: kae rollback --to %s",
					plan.Tool, err, restoreErr, meta.ID)
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
			return nil, errf(constants.ExitError,
				"recording state failed (%v) and restore also failed (%v); run: kae rollback --to %s",
				err, restoreErr, meta.ID)
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
