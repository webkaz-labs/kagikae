package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/state"
)

type preservedSelection struct {
	Tool    string `json:"tool"`
	Account string `json:"account"`
}

func runUseAuto(ctx context.Context, app *App, opts commonOpts, profile string, quiet bool) int {
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	name, err := app.resolveBareUseProfile(profile)
	if err != nil {
		return finish(opts, err)
	}
	report, err := buildUseAuto(ctx, app, opts, name)
	if err != nil {
		return finish(opts, err)
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	if !quiet {
		if len(report.Preserved) == 0 {
			printBareUseReport(report)
		} else {
			for _, p := range report.Preserved {
				fmt.Printf("Preserved global isolated %s -> %s (unchanged)\n", p.Tool, p.Account)
			}
			if len(report.Results) != 0 {
				printSwitchReport(&switchReport{DryRun: opts.DryRun, BackupID: report.BackupID, Results: report.Results})
			} else {
				fmt.Println("No shared changes")
			}
		}
	}
	return constants.ExitOK
}

func buildUseAuto(ctx context.Context, app *App, opts commonOpts, profile string) (*bareUseReport, error) {
	app.pinnedGlobalScope()
	targets, _, err := app.resolveTargets("all", profile)
	if err != nil {
		return nil, err
	}
	st, err := app.loadState()
	if err != nil {
		return nil, err
	}
	report := &bareUseReport{DryRun: opts.DryRun, SchemaVersion: constants.SchemaVersion, OK: true, Profile: &profile, Results: []switchResult{}, Preserved: []preservedSelection{}}
	// Preserve the ordinary shared no-op's lock-free behavior. It linearizes at
	// this state observation; no credential or binding is changed afterwards.
	consistent, fragmentErr := app.globalFragmentConsistent(st.Synced)
	if len(st.Synced) == 0 && consistent && fragmentErr == nil && recordedMatch(st.Active, targets) {
		return report, nil
	}
	if !opts.DryRun {
		locks, err := app.acquireIsolationLifecycleReaders(runTargetTools(targets))
		if err != nil {
			return nil, err
		}
		defer releaseLocks(locks)
	}
	selected := []runTarget{}
	inspect := func(current *state.State) error {
		st = current
		consistent, err := app.globalFragmentConsistent(st.Synced)
		if err != nil || !consistent {
			return errf(constants.ExitUnsafeRefused, "global isolation state and fragment disagree; inspect kae doctor, then explicitly select kae use -i or kae use -s")
		}
		for _, tgt := range targets {
			acct, ok := st.Synced[tgt.Tool]
			if !ok {
				selected = append(selected, tgt)
				continue
			}
			if !config.ValidFileName(acct) || isolationEnvVar(tgt.Tool) == "" {
				return errf(constants.ExitUnsafeRefused, "global isolated selection is invalid; inspect kae doctor and explicitly select an account")
			}
			if meta, found, err := account.Load(app.Paths.AccountDir(tgt.Tool, acct)); err != nil || !found || meta.Tool != tgt.Tool || meta.Name != acct {
				return errf(constants.ExitUnsafeRefused, "global isolated selection for %s is unavailable; inspect kae doctor and explicitly select an account", tgt.Tool)
			}
			dirs := []string{app.Paths.GlobalIsolatedHomeDir(tgt.Tool, acct)}
			if entry, ok := app.credentialEntry(tgt.Tool, acct); ok {
				dirs = append(dirs, entry.dir)
			}
			for _, dir := range dirs {
				info, err := os.Stat(dir)
				if err != nil || !info.IsDir() {
					return errf(constants.ExitUnsafeRefused, "global isolated store for %s is unavailable; inspect kae doctor and explicitly select an account", tgt.Tool)
				}
			}
			report.Preserved = append(report.Preserved, preservedSelection{Tool: tgt.Tool, Account: acct})
		}
		return nil
	}
	if opts.DryRun {
		err = inspect(st)
	} else {
		err = app.inspectState(inspect)
	}
	if err != nil {
		return nil, err
	}
	if recordedMatch(st.Active, selected) {
		return report, nil
	}
	appliedProfile := profile
	if len(report.Preserved) != 0 {
		appliedProfile = ""
	}
	sw, err := buildSwitchTargets(ctx, app, opts, selected, appliedProfile)
	if err != nil {
		return nil, err
	}
	report.Changed = true
	report.BackupID, report.Results = sw.BackupID, sw.Results
	return report, nil
}
