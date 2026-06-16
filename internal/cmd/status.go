package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

type toolStatus struct {
	Tool        string   `json:"tool"`
	Enabled     bool     `json:"enabled"`
	Account     *string  `json:"account"`
	Driver      string   `json:"driver"`
	AuthPresent bool     `json:"auth_present"`
	Accounts    []string `json:"accounts"`
	Warnings    []string `json:"warnings"`
}

// pinnedStatus is the directory binding a pinned .mise.toml exports.
type pinnedStatus struct {
	Profile string `json:"profile"`
	Mode    string `json:"mode"`
}

// profileStatus is one defined profile with its mapping.
type profileStatus struct {
	Name     string            `json:"name"`
	Label    string            `json:"label,omitempty"`
	Accounts map[string]string `json:"accounts"`
	Active   bool              `json:"active"`
}

// globalIsolatedStatus is one tool whose global mise fragment points it at a
// private home (kae use -i / run -i share this store). docs/RELEASE.md §B/§D:
// surfacing it keeps the shared isolated state from being invisible.
type globalIsolatedStatus struct {
	Tool    string `json:"tool"`
	Account string `json:"account"`
	Home    string `json:"home"`
}

type statusReport struct {
	SchemaVersion  int                    `json:"schema_version"`
	OK             bool                   `json:"ok"`
	Pinned         *pinnedStatus          `json:"pinned"`
	ActiveProfile  *string                `json:"active_profile"`
	Mode           string                 `json:"mode"`
	GlobalIsolated []globalIsolatedStatus `json:"global_isolated"`
	Tools          []toolStatus           `json:"tools"`
	Profiles       []profileStatus        `json:"profiles"`
}

func CmdStatus(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("status", flags, false, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s status [--json]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runStatus(ctx, app, opts)
}

func runStatus(ctx context.Context, app *App, opts commonOpts) int {
	report, err := buildStatus(ctx, app)
	if err != nil {
		return finish(opts, err)
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	printStatusReport(app, report, opts)
	return constants.ExitOK
}

func buildStatus(ctx context.Context, app *App) (*statusReport, error) {
	if err := app.requireConfig(); err != nil {
		return nil, err
	}
	st, err := app.loadState()
	if err != nil {
		return nil, err
	}
	captured, err := account.List(app.Paths.AccountsDir())
	if err != nil {
		return nil, err
	}
	capturedByTool := map[string][]string{}
	for _, acc := range captured {
		capturedByTool[acc.Tool] = append(capturedByTool[acc.Tool], acc.Name)
	}
	report := &statusReport{
		SchemaVersion:  constants.SchemaVersion,
		OK:             true,
		Pinned:         app.pinnedStatus(),
		Mode:           constants.ModeAuth,
		GlobalIsolated: globalIsolatedStatuses(app, st.Synced),
		Tools:          []toolStatus{},
		Profiles:       []profileStatus{},
	}
	// Inside a pinned directory the real per-tool account is the one the
	// kae-owned fragment bound (it may diverge from the global state and from
	// the KAE_PROFILE label after a single-tool re-bind); the fragment is the
	// source of truth. Tools it does not bind keep their global account.
	var pinnedAccounts map[string]string
	if report.Pinned != nil {
		if info, ok, ferr := readDirFragment(); ferr == nil && ok {
			pinnedAccounts = info.Accounts
		}
	}
	// Prefer the recorded profile (set by a profile-wide apply); fall back
	// to matching the per-tool map so older state files still resolve.
	activeProfile := st.ActiveProfile
	if activeProfile == "" {
		activeProfile = app.Config.MatchProfile(st.Active)
	}
	if activeProfile != "" {
		report.ActiveProfile = &activeProfile
	}
	for _, name := range app.Config.ProfileNames() { // ascending, stable order
		profile := app.Config.Profiles[name]
		accounts := profile.Accounts
		if accounts == nil {
			// A [profiles.X] section without accounts parses to a nil map;
			// keep the JSON contract at {} rather than null.
			accounts = map[string]string{}
		}
		report.Profiles = append(report.Profiles, profileStatus{
			Name: name, Label: profile.Label, Accounts: accounts,
			Active: name == activeProfile,
		})
	}
	for _, tool := range app.enabledTools() {
		ts := toolStatus{Tool: tool, Enabled: true, Warnings: []string{}, Accounts: []string{}}
		if names, ok := capturedByTool[tool]; ok {
			sort.Strings(names)
			ts.Accounts = names
		}
		if active, ok := st.Active[tool]; ok {
			activeCopy := active
			ts.Account = &activeCopy
		}
		if bound, ok := pinnedAccounts[tool]; ok {
			boundCopy := bound
			ts.Account = &boundCopy
		}
		ad, err := adapter.ForTool(tool)
		if err != nil {
			return nil, err
		}
		info, err := ad.Detect(ctx, app.Env)
		if err != nil {
			ts.Warnings = append(ts.Warnings, err.Error())
		} else {
			ts.Driver = info.Driver
			ts.AuthPresent = info.AuthPresent
			ts.Warnings = append(ts.Warnings, info.Warnings...)
		}
		report.Tools = append(report.Tools, ts)
	}
	return report, nil
}

// globalIsolatedStatuses resolves state.synced into per-tool private homes in
// canonical tool order (the [] contract is preserved by the caller's struct).
func globalIsolatedStatuses(app *App, synced map[string]string) []globalIsolatedStatus {
	out := []globalIsolatedStatus{}
	for _, tool := range constants.Tools {
		account, ok := synced[tool]
		if !ok {
			continue
		}
		out = append(out, globalIsolatedStatus{
			Tool: tool, Account: account,
			Home: app.Paths.GlobalIsolatedHomeDir(tool, account),
		})
	}
	return out
}

func printStatusReport(app *App, report *statusReport, opts commonOpts) {
	color := colorEnabled(opts.NoColor)
	if report.Pinned != nil {
		fmt.Printf("This directory: profile %s (pinned, %s)\n\n", report.Pinned.Profile, report.Pinned.Mode)
	}
	if len(report.GlobalIsolated) > 0 {
		fmt.Println("Global isolated homes (kae use -i / run -i share these):")
		for _, gi := range report.GlobalIsolated {
			fmt.Printf("  %s -> %s\n    %s\n", gi.Tool, gi.Account, gi.Home)
		}
		fmt.Println()
	}
	if report.ActiveProfile != nil {
		fmt.Printf("Global active profile: %s\n\n", *report.ActiveProfile)
	} else {
		fmt.Print("Global active profile: (none)\n\n")
	}
	rows := [][]string{}
	for _, ts := range report.Tools {
		accountName := "-"
		if ts.Account != nil {
			accountName = *ts.Account
		}
		auth := paint(constants.StatusWarn, "absent", color)
		if ts.AuthPresent {
			auth = paint(constants.StatusOK, "present", color)
		}
		notes := ""
		if len(ts.Warnings) > 0 {
			notes = paint(constants.StatusWarn, fmt.Sprintf("%d warning(s)", len(ts.Warnings)), color)
		}
		rows = append(rows, []string{ts.Tool, accountName, ts.Driver, auth, notes})
	}
	printTable([]string{"Tool", "Account", "Driver", "Auth", "Notes"}, rows)
	warned := false
	for _, ts := range report.Tools {
		for _, warning := range ts.Warnings {
			fmt.Printf("\n%s: %s", ts.Tool, paint(constants.StatusWarn, warning, color))
			warned = true
		}
	}
	if warned {
		fmt.Println()
	}
	if len(report.Profiles) == 0 {
		fmt.Println("\nProfiles: (none defined — add them with: kae edit)")
		return
	}
	fmt.Println("\nProfiles:")
	for _, profile := range report.Profiles {
		mapping := []string{}
		for _, tool := range constants.Tools {
			if accountName, ok := profile.Accounts[tool]; ok {
				mapping = append(mapping, tool+":"+accountName)
			}
		}
		marker := ""
		if profile.Active {
			marker = "  (active)"
		}
		fmt.Printf("  %-14s %s%s\n", profile.Name, strings.Join(mapping, " "), marker)
	}
}

type accountItem struct {
	Tool       string `json:"tool"`
	Account    string `json:"account"`
	Driver     string `json:"driver"`
	Active     bool   `json:"active"`
	CapturedAt string `json:"captured_at"`
}

type accountsReport struct {
	SchemaVersion int           `json:"schema_version"`
	Accounts      []accountItem `json:"accounts"`
}

func CmdAccounts(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("accounts", flags, false, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s accounts [--json]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runAccounts(ctx, app, opts)
}

func runAccounts(_ context.Context, app *App, opts commonOpts) int {
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	st, err := app.loadState()
	if err != nil {
		return finish(opts, err)
	}
	captured, err := account.List(app.Paths.AccountsDir())
	if err != nil {
		return finish(opts, err)
	}
	report := accountsReport{SchemaVersion: constants.SchemaVersion, Accounts: []accountItem{}}
	for _, acc := range captured {
		report.Accounts = append(report.Accounts, accountItem{
			Tool:       acc.Tool,
			Account:    acc.Name,
			Driver:     acc.Driver,
			Active:     st.Active[acc.Tool] == acc.Name,
			CapturedAt: acc.CapturedAt.UTC().Format(time.RFC3339),
		})
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	if len(report.Accounts) == 0 {
		fmt.Println("no captured accounts (run: kae add <tool> <account>)")
		return constants.ExitOK
	}
	rows := [][]string{}
	for _, item := range report.Accounts {
		active := ""
		if item.Active {
			active = "*"
		}
		rows = append(rows, []string{item.Tool, item.Account, active, item.Driver, item.CapturedAt})
	}
	printTable([]string{"Tool", "Account", "Active", "Driver", "Captured"}, rows)
	return constants.ExitOK
}
