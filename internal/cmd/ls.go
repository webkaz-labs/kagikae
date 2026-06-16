package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// lsReport is the JSON contract of `kae ls`: the one view of captured accounts
// and defined profiles, today split across `kae accounts` and `kae status`.
// It reuses the existing accountItem / profileStatus row shapes (docs/CLI.md);
// read-only, no new state.
type lsReport struct {
	SchemaVersion int             `json:"schema_version"`
	Accounts      []accountItem   `json:"accounts"`
	Profiles      []profileStatus `json:"profiles"`
}

// CmdLs lists every captured account and every defined profile in one view
// (alias-free; docs/RELEASE.md §C). Read-only.
func CmdLs(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("ls", flags, false, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s ls [--json]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runLs(ctx, app, opts)
}

func runLs(_ context.Context, app *App, opts commonOpts) int {
	report, err := buildLs(app)
	if err != nil {
		return finish(opts, err)
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	printLsReport(report)
	return constants.ExitOK
}

func buildLs(app *App) (*lsReport, error) {
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
	return &lsReport{
		SchemaVersion: constants.SchemaVersion,
		Accounts:      accountItems(st, captured),
		Profiles:      app.profileStatuses(app.activeProfileName(st)),
	}, nil
}

func printLsReport(report *lsReport) {
	if len(report.Accounts) == 0 {
		fmt.Println("Accounts: (none — register one with: kae add <tool>)")
	} else {
		fmt.Println("Accounts:")
		rows := [][]string{}
		for _, item := range report.Accounts {
			active := ""
			if item.Active {
				active = "*"
			}
			rows = append(rows, []string{item.Tool, item.Account, active, item.Driver})
		}
		printTable([]string{"Tool", "Account", "Active", "Driver"}, rows)
	}
	fmt.Println()
	if len(report.Profiles) == 0 {
		fmt.Println("Profiles: (none defined — add them with: kae edit)")
		return
	}
	fmt.Println("Profiles:")
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
