package cmd

import (
	"context"
	"fmt"
	"sort"
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

type statusReport struct {
	SchemaVersion int          `json:"schema_version"`
	OK            bool         `json:"ok"`
	ActiveProfile *string      `json:"active_profile"`
	Mode          string       `json:"mode"`
	Tools         []toolStatus `json:"tools"`
}

func CmdStatus(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, _, ok := parseCommon("status", flags, false)
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
		SchemaVersion: constants.SchemaVersion,
		OK:            true,
		Mode:          constants.ModeAuth,
		Tools:         []toolStatus{},
	}
	if profile := app.Config.MatchProfile(st.Active); profile != "" {
		report.ActiveProfile = &profile
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

func printStatusReport(app *App, report *statusReport, opts commonOpts) {
	color := colorEnabled(opts.NoColor)
	if report.ActiveProfile != nil {
		fmt.Printf("Active profile: %s\n\n", *report.ActiveProfile)
	} else {
		fmt.Print("Active profile: (none)\n\n")
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
	for _, ts := range report.Tools {
		for _, warning := range ts.Warnings {
			fmt.Printf("\n%s: %s", ts.Tool, paint(constants.StatusWarn, warning, color))
		}
	}
	for _, ts := range report.Tools {
		if len(ts.Warnings) > 0 {
			fmt.Println()
			break
		}
	}
}

type currentReport struct {
	SchemaVersion int               `json:"schema_version"`
	ActiveProfile *string           `json:"active_profile"`
	Active        map[string]string `json:"active"`
}

func CmdCurrent(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, _, ok := parseCommon("current", flags, false)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s current [--json]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runCurrent(ctx, app, opts)
}

func runCurrent(_ context.Context, app *App, opts commonOpts) int {
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	st, err := app.loadState()
	if err != nil {
		return finish(opts, err)
	}
	report := currentReport{
		SchemaVersion: constants.SchemaVersion,
		Active:        st.Active,
	}
	if profile := app.Config.MatchProfile(st.Active); profile != "" {
		report.ActiveProfile = &profile
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	if report.ActiveProfile != nil {
		fmt.Printf("profile: %s\n", *report.ActiveProfile)
	}
	printed := false
	for _, tool := range constants.Tools {
		if accountName, ok := st.Active[tool]; ok {
			fmt.Printf("%s: %s\n", tool, accountName)
			printed = true
		}
	}
	if !printed {
		fmt.Println("no active accounts recorded (run: kae capture <tool> <account>)")
	}
	return constants.ExitOK
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
	opts, _, ok := parseCommon("accounts", flags, false)
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
		fmt.Println("no captured accounts (run: kae capture <tool> <account>)")
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
