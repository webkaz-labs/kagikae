package cmd

import (
	"context"
	"flag"
	"fmt"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// applyReport is the JSON contract of kae apply: the switch report plus a
// changed marker so hooks can tell a no-op from an apply.
type applyReport struct {
	SchemaVersion int            `json:"schema_version"`
	OK            bool           `json:"ok"`
	Changed       bool           `json:"changed"`
	Profile       *string        `json:"profile"`
	BackupID      string         `json:"backup_id,omitempty"`
	Results       []switchResult `json:"results"`
}

// CmdApply idempotently applies a profile, for hooks and scripts:
//
//	kae apply [--profile P] [--quiet]
//
// Profile resolution: --profile, then $KAE_PROFILE, then default_profile.
// When kae's recorded active state already matches the profile it exits 0
// with "changed": false, taking no locks and writing no backups. The match
// compares kae's belief (state.json), not upstream truth; external drift is
// neither verified nor repaired — kae use forces an apply.
func CmdApply(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args, "--profile")
	var profileName string
	quiet := false
	opts, ok := parseCommon("apply", flags, false, func(fs *flag.FlagSet) {
		fs.StringVar(&profileName, "profile", "",
			"profile to apply (default: $KAE_PROFILE, then config default_profile)")
		fs.BoolVar(&quiet, "quiet", false, "suppress the success report (for hooks)")
	})
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s apply [--profile P] [--quiet]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runApply(ctx, app, opts, profileName, quiet)
}

// CmdSync was renamed to CmdApply in v0.7.0 (docs/SCOPE-MODEL.md §8); exit
// 64 guides users to the new name. Kept for one release.
func CmdSync(_ context.Context, _ []string) int {
	return removedCommand("sync", "v0.7.0", "kae apply [--profile P] [--quiet]")
}

func runApply(ctx context.Context, app *App, opts commonOpts, profileName string, quiet bool) int {
	report, err := buildApply(ctx, app, opts, profileName)
	if err != nil {
		return finish(opts, err)
	}
	if quiet {
		return constants.ExitOK
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	printApplyReport(app, report)
	return constants.ExitOK
}

func buildApply(ctx context.Context, app *App, opts commonOpts, profileName string) (*applyReport, error) {
	if err := app.requireConfig(); err != nil {
		return nil, err
	}
	app.pinnedGlobalScope()
	profileName, err := app.resolveApplyProfile(profileName)
	if err != nil {
		return nil, err
	}
	targets, _, err := app.resolveTargets("all", profileName)
	if err != nil {
		return nil, err
	}
	st, err := app.loadState()
	if err != nil {
		return nil, err
	}
	report := &applyReport{
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
	report.Changed = true
	report.Profile = sw.Profile
	report.BackupID = sw.BackupID
	report.Results = sw.Results
	return report, nil
}

// resolveApplyProfile resolves the profile to apply: explicit flag, then
// $KAE_PROFILE, then config default_profile.
func (app *App) resolveApplyProfile(explicit string) (string, error) {
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
		"no profile given: use --profile <name>, set %s, or set default_profile in config",
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

func printApplyReport(app *App, report *applyReport) {
	if !report.Changed {
		fmt.Printf("Profile %s already active (no changes)\n", *report.Profile)
		return
	}
	printSwitchReport(app, &switchReport{
		Profile:  report.Profile,
		BackupID: report.BackupID,
		Results:  report.Results,
	})
}
