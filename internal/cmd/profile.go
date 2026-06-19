package cmd

import (
	"context"
	"flag"
	"fmt"
	"sort"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// CmdProfile manages [profiles] entries without hand-editing TOML:
//
//	kae profile save <name>                 snapshot the active accounts
//	kae profile set <name> <tool> <account> set one tool mapping
//	kae profile unset <name> <tool>         drop one tool mapping
//	kae profile rm <name> [--force]         delete the whole profile
//	kae profile default [<name>|--clear]    show or set default_profile
func CmdProfile(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return usageError("usage: %s profile save|set|unset|rm|default ...", toolName)
	}
	switch args[0] {
	case "save":
		return cmdProfileSave(ctx, args[1:])
	case "set":
		return cmdProfileSet(ctx, args[1:])
	case "unset":
		return cmdProfileUnset(ctx, args[1:])
	case "rm", "remove":
		return cmdProfileRm(ctx, args[1:])
	case "default":
		return cmdProfileDefault(ctx, args[1:])
	default:
		return usageError("unknown profile subcommand %q (save, set, unset, rm, default)", args[0])
	}
}

type profileReport struct {
	SchemaVersion  int               `json:"schema_version"`
	OK             bool              `json:"ok"`
	DryRun         bool              `json:"dry_run"`
	Action         string            `json:"action"`
	Profile        string            `json:"profile,omitempty"`
	Accounts       map[string]string `json:"accounts,omitempty"`
	DefaultProfile string            `json:"default_profile,omitempty"`
}

func finishProfile(opts commonOpts, report *profileReport, err error, printer func(*profileReport)) int {
	if err != nil {
		return finish(opts, err)
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	printer(report)
	return constants.ExitOK
}

func cmdProfileSave(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("profile save", flags, true, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 1 {
		return usageError("usage: %s profile save <name>", toolName)
	}
	app := newApp(opts.ConfigPath)
	report, err := buildProfileSave(ctx, app, opts, positionals[0])
	return finishProfile(opts, report, err, printProfileSave)
}

func buildProfileSave(_ context.Context, app *App, opts commonOpts, name string) (*profileReport, error) {
	if !config.ValidName(name) {
		return nil, errf(constants.ExitUsage, "invalid profile name %q", name)
	}
	if err := app.requireConfigFile(); err != nil {
		return nil, err
	}
	st, err := app.loadState()
	if err != nil {
		return nil, err
	}
	accounts := map[string]string{}
	for tool, acc := range st.Active {
		accounts[tool] = acc
	}
	if len(accounts) == 0 {
		return nil, errf(constants.ExitNotFound, "no active accounts to save (switch first, e.g. kae use <tool> <account>)")
	}
	report := &profileReport{
		SchemaVersion: constants.SchemaVersion, OK: true, DryRun: opts.DryRun,
		Action: "save", Profile: name, Accounts: accounts,
	}
	if opts.DryRun {
		return report, nil
	}
	cfgLock, err := app.acquireConfigLock()
	if err != nil {
		return nil, err
	}
	defer cfgLock.Release()
	if err := app.editConfig(func(e *config.Editor) {
		e.ClearProfileAccounts(name)
		for tool, acc := range accounts {
			e.SetProfileAccount(name, tool, acc)
		}
	}); err != nil {
		return nil, err
	}
	return report, nil
}

func printProfileSave(r *profileReport) {
	verb := "Saved"
	if r.DryRun {
		verb = "Would save"
	}
	fmt.Printf("%s profile %s from the active accounts:\n", verb, r.Profile)
	tools := make([]string, 0, len(r.Accounts))
	for tool := range r.Accounts {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	for _, tool := range tools {
		fmt.Printf("  %s = %s\n", tool, r.Accounts[tool])
	}
}

func cmdProfileSet(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("profile set", flags, true, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 3 {
		return usageError("usage: %s profile set <name> <tool> <account>", toolName)
	}
	app := newApp(opts.ConfigPath)
	report, err := buildProfileSet(ctx, app, opts, positionals[0], positionals[1], positionals[2])
	return finishProfile(opts, report, err, printProfileSet)
}

func buildProfileSet(_ context.Context, app *App, opts commonOpts, name, tool, accountName string) (*profileReport, error) {
	if !config.ValidName(name) {
		return nil, errf(constants.ExitUsage, "invalid profile name %q", name)
	}
	if err := validateToolAccount(tool, accountName, "account"); err != nil {
		return nil, err
	}
	if err := app.requireConfigFile(); err != nil {
		return nil, err
	}
	if _, found, err := account.Load(app.Paths.AccountDir(tool, accountName)); err != nil {
		return nil, err
	} else if !found {
		return nil, errf(constants.ExitNotFound,
			"account %s/%s is not captured (run: kae add %s %s)", tool, accountName, tool, accountName)
	}
	report := &profileReport{
		SchemaVersion: constants.SchemaVersion, OK: true, DryRun: opts.DryRun,
		Action: "set", Profile: name, Accounts: map[string]string{tool: accountName},
	}
	if opts.DryRun {
		return report, nil
	}
	cfgLock, err := app.acquireConfigLock()
	if err != nil {
		return nil, err
	}
	defer cfgLock.Release()
	if err := app.editConfig(func(e *config.Editor) {
		e.SetProfileAccount(name, tool, accountName)
	}); err != nil {
		return nil, err
	}
	return report, nil
}

func cmdProfileUnset(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("profile unset", flags, true, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 2 {
		return usageError("usage: %s profile unset <name> <tool>", toolName)
	}
	app := newApp(opts.ConfigPath)
	report, err := buildProfileUnset(ctx, app, opts, positionals[0], positionals[1])
	return finishProfile(opts, report, err, printProfileUnset)
}

func buildProfileUnset(_ context.Context, app *App, opts commonOpts, name, tool string) (*profileReport, error) {
	if !constants.IsTool(tool) {
		return nil, errf(constants.ExitUsage, "unknown tool %q", tool)
	}
	if err := app.requireConfigFile(); err != nil {
		return nil, err
	}
	profile, ok := app.Config.Profiles[name]
	if !ok {
		return nil, errf(constants.ExitNotFound, "profile %q is not defined", name)
	}
	if _, ok := profile.Accounts[tool]; !ok {
		return nil, errf(constants.ExitNotFound, "profile %q does not map %s", name, tool)
	}
	// Unsetting the last mapping leaves an empty profile, so remove it whole.
	// If that profile is the default, clear default_profile in the same edit;
	// otherwise the reload validation rejects the dangling reference and the
	// file is left invalid.
	lastMapping := len(profile.Accounts) == 1
	clearsDefault := lastMapping && app.Config.DefaultProfile == name
	report := &profileReport{
		SchemaVersion: constants.SchemaVersion, OK: true, DryRun: opts.DryRun,
		Action: "unset", Profile: name, Accounts: map[string]string{tool: profile.Accounts[tool]},
	}
	if opts.DryRun {
		return report, nil
	}
	cfgLock, err := app.acquireConfigLock()
	if err != nil {
		return nil, err
	}
	defer cfgLock.Release()
	if err := app.editConfig(func(e *config.Editor) {
		if lastMapping {
			e.RemoveProfile(name)
			if clearsDefault {
				e.SetDefaultProfile("")
			}
		} else {
			e.RemoveProfileAccount(name, tool)
		}
	}); err != nil {
		return nil, err
	}
	return report, nil
}

func printProfileSet(r *profileReport) {
	verb := "Set"
	if r.DryRun {
		verb = "Would set"
	}
	for tool, acc := range r.Accounts {
		fmt.Printf("%s %s = %s in profile %s\n", verb, tool, acc, r.Profile)
	}
}

func printProfileUnset(r *profileReport) {
	verb := "Unset"
	if r.DryRun {
		verb = "Would unset"
	}
	for tool := range r.Accounts {
		fmt.Printf("%s %s from profile %s\n", verb, tool, r.Profile)
	}
}

func cmdProfileRm(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	force := false
	opts, ok := parseCommon("profile rm", flags, true, func(fs *flag.FlagSet) {
		registerProfileRmFlags(fs, &force)
	})
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 1 {
		return usageError("usage: %s profile rm <name> [--force]", toolName)
	}
	app := newApp(opts.ConfigPath)
	report, err := buildProfileRm(ctx, app, opts, positionals[0], force)
	return finishProfile(opts, report, err, printProfileRm)
}

func buildProfileRm(_ context.Context, app *App, opts commonOpts, name string, force bool) (*profileReport, error) {
	if err := app.requireConfigFile(); err != nil {
		return nil, err
	}
	if _, ok := app.Config.Profiles[name]; !ok {
		return nil, errf(constants.ExitNotFound, "profile %q is not defined", name)
	}
	clearsDefault := app.Config.DefaultProfile == name
	if clearsDefault && !force {
		return nil, errf(constants.ExitUnsafeRefused,
			"profile %q is the default_profile; rerun with --force to remove it and clear the default", name)
	}
	report := &profileReport{
		SchemaVersion: constants.SchemaVersion, OK: true, DryRun: opts.DryRun,
		Action: "rm", Profile: name,
	}
	if opts.DryRun {
		return report, nil
	}
	cfgLock, err := app.acquireConfigLock()
	if err != nil {
		return nil, err
	}
	defer cfgLock.Release()
	if err := app.editConfig(func(e *config.Editor) {
		e.RemoveProfile(name)
		if clearsDefault {
			e.SetDefaultProfile("")
		}
	}); err != nil {
		return nil, err
	}
	return report, nil
}

func printProfileRm(r *profileReport) {
	verb := "Removed"
	if r.DryRun {
		verb = "Would remove"
	}
	fmt.Printf("%s profile %s\n", verb, r.Profile)
}

func cmdProfileDefault(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	clear := false
	opts, ok := parseCommon("profile default", flags, true, func(fs *flag.FlagSet) {
		registerProfileDefaultFlags(fs, &clear)
	})
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) > 1 || (clear && len(positionals) == 1) {
		return usageError("usage: %s profile default [<name>|--clear]", toolName)
	}
	app := newApp(opts.ConfigPath)
	name := ""
	if len(positionals) == 1 {
		name = positionals[0]
	}
	report, err := buildProfileDefault(ctx, app, opts, name, clear)
	return finishProfile(opts, report, err, printProfileDefault)
}

func buildProfileDefault(_ context.Context, app *App, opts commonOpts, name string, clear bool) (*profileReport, error) {
	if err := app.requireConfigFile(); err != nil {
		return nil, err
	}
	// Bare "kae profile default" is a read: report the current default.
	if name == "" && !clear {
		return &profileReport{
			SchemaVersion: constants.SchemaVersion, OK: true,
			Action: "default", DefaultProfile: app.Config.DefaultProfile,
		}, nil
	}
	if !clear {
		if _, ok := app.Config.Profiles[name]; !ok {
			return nil, errf(constants.ExitNotFound, "profile %q is not defined", name)
		}
	}
	report := &profileReport{
		SchemaVersion: constants.SchemaVersion, OK: true, DryRun: opts.DryRun,
		Action: "default", DefaultProfile: name,
	}
	if opts.DryRun {
		return report, nil
	}
	cfgLock, err := app.acquireConfigLock()
	if err != nil {
		return nil, err
	}
	defer cfgLock.Release()
	if err := app.editConfig(func(e *config.Editor) {
		e.SetDefaultProfile(name)
	}); err != nil {
		return nil, err
	}
	return report, nil
}

func printProfileDefault(r *profileReport) {
	if r.DryRun {
		if r.DefaultProfile == "" {
			fmt.Println("Would clear default_profile")
		} else {
			fmt.Printf("Would set default_profile to %s\n", r.DefaultProfile)
		}
		return
	}
	if r.DefaultProfile == "" {
		fmt.Println("default_profile: (none)")
		return
	}
	fmt.Printf("default_profile: %s\n", r.DefaultProfile)
}
