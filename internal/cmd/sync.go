package cmd

import (
	"context"
	"flag"
	"fmt"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// syncReport is the JSON contract of kae sync: the switch report plus a
// changed marker so hooks can tell a no-op from an apply.
type syncReport struct {
	SchemaVersion int            `json:"schema_version"`
	OK            bool           `json:"ok"`
	Changed       bool           `json:"changed"`
	Profile       *string        `json:"profile"`
	BackupID      string         `json:"backup_id,omitempty"`
	Results       []switchResult `json:"results"`
}

// CmdSync idempotently applies a profile, for hooks and scripts:
//
//	kae sync [--profile P] [--quiet]
//
// Profile resolution: --profile, then $KAE_PROFILE, then default_profile.
// When kae's recorded active state already matches the profile it exits 0
// with "changed": false, taking no locks and writing no backups. The match
// compares kae's belief (state.json), not upstream truth; external drift is
// neither verified nor repaired — kae use forces an apply.
func CmdSync(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args, "--profile")
	var profileName string
	quiet := false
	opts, ok := parseCommon("sync", flags, false, func(fs *flag.FlagSet) {
		fs.StringVar(&profileName, "profile", "",
			"profile to apply (default: $KAE_PROFILE, then config default_profile)")
		fs.BoolVar(&quiet, "quiet", false, "suppress the success report (for hooks)")
	})
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s sync [--profile P] [--quiet]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runSync(ctx, app, opts, profileName, quiet)
}

func runSync(ctx context.Context, app *App, opts commonOpts, profileName string, quiet bool) int {
	report, err := buildSync(ctx, app, opts, profileName)
	if err != nil {
		return finish(opts, err)
	}
	if quiet {
		return constants.ExitOK
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	printSyncReport(app, report)
	return constants.ExitOK
}

func buildSync(ctx context.Context, app *App, opts commonOpts, profileName string) (*syncReport, error) {
	if err := app.requireConfig(); err != nil {
		return nil, err
	}
	profileName, err := app.resolveSyncProfile(profileName)
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
	report := &syncReport{
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

// resolveSyncProfile resolves the profile to sync: explicit flag, then
// $KAE_PROFILE, then config default_profile.
func (app *App) resolveSyncProfile(explicit string) (string, error) {
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

func printSyncReport(app *App, report *syncReport) {
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
