package cmd

import (
	"context"
	"fmt"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// bareUseReport is the JSON contract of bare `kae use` (the folded `apply`):
// the switch report plus a changed marker so hooks can tell a no-op from an
// applied switch.
type bareUseReport struct {
	SchemaVersion int            `json:"schema_version"`
	OK            bool           `json:"ok"`
	Changed       bool           `json:"changed"`
	Profile       *string        `json:"profile"`
	BackupID      string         `json:"backup_id,omitempty"`
	Results       []switchResult `json:"results"`
}

// CmdApply is a removed-command pointer: `apply` folded into bare `kae use` in
// v0.8.0 (docs/RELEASE.md). Exit 64 names the replacement for one release.
func CmdApply(_ context.Context, _ []string) int {
	return removedCommand("apply", "v0.8.0", "kae use [--quiet] (bare use resolves the profile)")
}

// runUseBare is bare `kae use`: resolve the profile (--profile/-P, then
// $KAE_PROFILE, then default_profile) and apply it. Shared (the default) is
// idempotent — a no-op (exit 0, no lock, no backup) when state.json `active`
// already matches — so it is safe as a mise enter hook. Isolated regenerates
// the global mise fragment for the resolved profile.
func runUseBare(ctx context.Context, app *App, opts commonOpts, isolated bool, profileName string, quiet bool) int {
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	profileName, err := app.resolveBareUseProfile(profileName)
	if err != nil {
		return finish(opts, err)
	}
	if isolated {
		return runUseIsolated(ctx, app, opts, "all", profileName)
	}
	report, err := buildUseBare(ctx, app, opts, profileName)
	if err != nil {
		return finish(opts, err)
	}
	// --quiet suppresses the human success report (for enter hooks); the JSON
	// report still emits so a script can read `changed` (docs/RELEASE.md).
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	if quiet {
		return constants.ExitOK
	}
	printBareUseReport(report)
	return constants.ExitOK
}

func buildUseBare(ctx context.Context, app *App, opts commonOpts, profileName string) (*bareUseReport, error) {
	app.pinnedGlobalScope()
	targets, _, err := app.resolveTargets("all", profileName)
	if err != nil {
		return nil, err
	}
	st, err := app.loadState()
	if err != nil {
		return nil, err
	}
	report := &bareUseReport{
		SchemaVersion: constants.SchemaVersion,
		OK:            true,
		Profile:       &profileName,
		Results:       []switchResult{},
	}
	if recordedMatch(st.Active, targets) {
		return report, nil
	}
	sw, err := buildSwitch(ctx, app, opts, "all", profileName)
	if err != nil {
		return nil, err
	}
	// Bare use is the documented teardown of kae use -i (docs/RELEASE.md): after
	// switching the real home in place, drop the switched tools from
	// state.synced and regenerate/delete the global fragment, mirroring
	// runSwitch. A no-op when no switched tool is globally isolated.
	if !opts.DryRun {
		tools := make([]string, 0, len(sw.Results))
		for _, r := range sw.Results {
			tools = append(tools, r.Tool)
		}
		if err := app.teardownSynced(tools); err != nil {
			return nil, err
		}
	}
	report.Changed = true
	report.Profile = sw.Profile
	report.BackupID = sw.BackupID
	report.Results = sw.Results
	return report, nil
}

// resolveBareUseProfile resolves the profile for bare `kae use`: explicit
// --profile/-P, then $KAE_PROFILE, then config default_profile.
func (app *App) resolveBareUseProfile(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if env := app.Env.Getenv(constants.EnvKaeProfile); env != "" {
		return env, nil
	}
	if app.Config.DefaultProfile != "" {
		return app.Config.DefaultProfile, nil
	}
	return "", errf(constants.ExitUsage,
		"no profile given: pass -P <name>, set %s, or set default_profile in config",
		constants.EnvKaeProfile)
}

// recordedMatch reports whether kae's recorded active accounts already cover
// every target (kae's belief, not upstream truth — docs/DATA-MODEL.md).
func recordedMatch(active map[string]string, targets []runTarget) bool {
	for _, tgt := range targets {
		if active[tgt.Tool] != tgt.Account {
			return false
		}
	}
	return true
}

func printBareUseReport(report *bareUseReport) {
	if !report.Changed {
		fmt.Printf("Profile %s already active (no changes)\n", *report.Profile)
		return
	}
	printSwitchReport(&switchReport{
		Profile:  report.Profile,
		BackupID: report.BackupID,
		Results:  report.Results,
	})
}
