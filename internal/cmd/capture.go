package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

type captureResult struct {
	Tool     string   `json:"tool"`
	Account  string   `json:"account"`
	Driver   string   `json:"driver"`
	Captured bool     `json:"captured"`
	Actions  []action `json:"actions"`
	Warnings []string `json:"warnings"`
}

type captureReport struct {
	SchemaVersion int             `json:"schema_version"`
	OK            bool            `json:"ok"`
	DryRun        bool            `json:"dry_run"`
	Results       []captureResult `json:"results"`
}

// runCapture snapshots the current live auth state into an account; the
// CLI surface is kae add --no-login (CmdAdd).
func runCapture(ctx context.Context, app *App, opts commonOpts, tool, accountName string) int {
	report, err := buildCapture(ctx, app, opts, tool, accountName)
	if err != nil {
		return finish(opts, err)
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	printCaptureReport(app, report)
	return constants.ExitOK
}

func buildCapture(ctx context.Context, app *App, opts commonOpts, tool, accountName string) (*captureReport, error) {
	if err := validateToolAccount(tool, accountName, "account"); err != nil {
		return nil, err
	}
	if err := app.requireConfig(); err != nil {
		return nil, err
	}
	plan, err := app.planTool(ctx, tool, accountName)
	if err != nil {
		return nil, err
	}
	report := &captureReport{
		SchemaVersion: constants.SchemaVersion,
		OK:            true,
		DryRun:        opts.DryRun,
		Results: []captureResult{{
			Tool: tool, Account: accountName, Driver: plan.Driver,
			Captured: !opts.DryRun, Actions: app.actionsOf(plan.Specs),
			Warnings: plan.Warnings,
		}},
	}
	if opts.DryRun {
		return report, nil
	}
	be, err := app.secretBackend()
	if err != nil {
		return nil, err
	}
	locks, err := app.acquireLocks([]string{tool})
	if err != nil {
		return nil, err
	}
	defer releaseLocks(locks)

	if err := app.captureSnapshot(ctx, be, plan); err != nil {
		return nil, err
	}
	st, err := app.loadState()
	if err != nil {
		return nil, err
	}
	if err := app.saveActive(st, map[string]string{tool: accountName}, ""); err != nil {
		return nil, err
	}
	return report, nil
}

// captureSnapshot reads the live auth artifacts of plan's tool and persists
// them as the plan's account snapshot. Callers hold the tool lock and update
// state themselves.
func (app *App) captureSnapshot(ctx context.Context, be secret.Backend, plan toolPlan) error {
	values := make([]artifact.Value, len(plan.Specs))
	anyPresent := false
	for i, sp := range plan.Specs {
		value, err := artifact.ReadLive(ctx, sp)
		if err != nil {
			return err
		}
		values[i] = value
		if value.Present {
			anyPresent = true
		}
	}
	if !anyPresent {
		message := fmt.Sprintf("no live %s auth state found; log in with the official CLI first", plan.Tool)
		if len(plan.Warnings) > 0 {
			message += " (" + strings.Join(plan.Warnings, "; ") + ")"
		}
		return errf(constants.ExitAuthMissing, "%s", message)
	}

	acc := account.Account{
		Version:    1,
		Tool:       plan.Tool,
		Name:       plan.Account,
		Driver:     plan.Driver,
		CapturedAt: app.Now().UTC(),
		Artifacts:  map[string]account.Artifact{},
	}
	for i, sp := range plan.Specs {
		ref := account.SecretRef(plan.Tool, plan.Account, sp.Name)
		if values[i].Present {
			if err := be.Set(ctx, ref, values[i].Data); err != nil {
				return fmt.Errorf("store captured payload: %w", err)
			}
		} else if err := be.Delete(ctx, ref); err != nil {
			return fmt.Errorf("clear stale payload: %w", err)
		}
		acc.Artifacts[sp.Name] = account.Artifact{
			Kind: sp.Kind, Target: sp.Target, Pointer: sp.Pointer,
			SecretRef: ref, Present: values[i].Present,
		}
	}
	return account.Save(app.Paths.AccountDir(plan.Tool, plan.Account), acc)
}

func printCaptureReport(app *App, report *captureReport) {
	for _, result := range report.Results {
		verb := "Captured"
		if report.DryRun {
			verb = "Would capture"
		}
		fmt.Printf("%s %s/%s (driver: %s)\n", verb, result.Tool, result.Account, result.Driver)
		for _, act := range result.Actions {
			if act.Pointer != "" {
				fmt.Printf("  %s %s %s\n", act.Kind, act.Target, act.Pointer)
			} else {
				fmt.Printf("  %s %s\n", act.Kind, act.Target)
			}
		}
		for _, warning := range result.Warnings {
			fmt.Printf("  warning: %s\n", warning)
		}
	}
}
