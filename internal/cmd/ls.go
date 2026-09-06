package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"

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

// boundDir is one directory `kae pin` has bound, as `kae ls --pins` reports it.
// Every field but Directory comes from that directory's mise fragment, which is
// the binding itself; the store under isolation/<pin-id> only says a binding once
// existed there.
type boundDir struct {
	Directory string `json:"directory"`
	Profile   string `json:"profile"` // empty for an ad-hoc account set
	Mode      string `json:"mode"`    // shared | isolated
	// Accounts is every tool the directory binds, in either mode (fragmentInfo).
	Accounts map[string]string `json:"accounts"`
	Current  bool              `json:"current"` // this is the current directory
}

// pinsReport is the JSON contract of `kae ls --pins`: every directory bound
// right now, from anywhere. `kae status` answers for the current directory only,
// which is the wrong question with one worktree per agent.
type pinsReport struct {
	SchemaVersion    int        `json:"schema_version"`
	BoundDirectories []boundDir `json:"bound_directories"`
}

// CmdLs lists every captured account and every defined profile in one view
// (alias-free), or with --pins every bound directory.
// Read-only.
func CmdLs(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	var pins bool
	opts, ok := parseCommon("ls", flags, false, func(fs *flag.FlagSet) {
		registerLsFlags(fs, &pins)
	})
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s ls [--pins] [--json]", toolName)
	}
	app := newApp(opts.ConfigPath)
	if pins {
		return runLsPins(app, opts)
	}
	return runLs(ctx, app, opts)
}

func runLs(ctx context.Context, app *App, opts commonOpts) int {
	report, err := buildLs(ctx, app)
	if err != nil {
		return finish(opts, err)
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	printLsReport(app, report)
	return constants.ExitOK
}

func runLsPins(app *App, opts commonOpts) int {
	report, err := buildLsPins(app)
	if err != nil {
		return finish(opts, err)
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	printPinsReport(app, report)
	return constants.ExitOK
}

// buildLsPins reports what is bound *now*, which is a different question from
// what the breadcrumb index answers. That walk deliberately returns stores nothing points
// at any more — `kae unpin` keeps one so a re-pin restores its sessions, and a
// single-tool re-bind leaves the previously bound tools' stores behind — so a
// directory is listed only when it still has a fragment to read (AGENTS.md; the
// leftovers are pinChecks' business). A directory whose recorded path is gone is
// skipped for the same reason, and `kae doctor` reports that it may have been deleted
// or moved while leaving its store untouched.
//
// It reads no config, and so deliberately skips requireConfig: the bindings live
// in the data dir and the fragments live in the directories, so a malformed
// config.toml is not a reason to refuse the one command that says which accounts
// the directories are currently running.
func buildLsPins(app *App) (*pinsReport, error) {
	index := app.boundDirectoryIndex()
	if index.err != nil {
		return nil, index.err
	}
	// A failure to learn the cwd only costs the "current" marker, so it must not
	// fail the listing — which is most useful from outside every bound directory.
	cwd, _ := cwdAbs()
	dirs := []boundDir{}
	for _, pin := range index.directories {
		info, exists, ferr := pin.readFragment()
		// An unreadable fragment is not an unbound directory, and the two must not
		// collapse into the same silent skip — pinChecks splits them for the same
		// reason. A live, genuinely bound directory vanishing from the one command
		// that says which account it runs is worse than a noisy row, so say why on
		// stderr and keep going (a warning never changes the exit code).
		if ferr != nil {
			fmt.Fprintf(os.Stderr, "kae: warning: %s is bound but its fragment could not be read (%v), so it is not listed\n",
				pin.Dir, ferr)
			continue
		}
		if !exists {
			continue
		}
		dirs = append(dirs, boundDir{
			Directory: pin.Dir,
			Profile:   info.Profile,
			Mode:      info.Mode,
			Accounts:  info.Accounts,
			Current:   cwd != "" && pin.Dir == cwd,
		})
	}
	// The breadcrumb index is ordered by pin-id (a path hash), which is meaningless to a
	// reader; sibling worktrees sort next to each other by path.
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Directory < dirs[j].Directory })
	return &pinsReport{SchemaVersion: constants.SchemaVersion, BoundDirectories: dirs}, nil
}

func printPinsReport(app *App, report *pinsReport) {
	if len(report.BoundDirectories) == 0 {
		fmt.Println("Bound directories: (none — bind one with: kae pin <profile>)")
		return
	}
	fmt.Println("Bound directories:")
	rows := [][]string{}
	for _, dir := range report.BoundDirectories {
		current := ""
		if dir.Current {
			current = "*"
		}
		profile := dir.Profile
		if profile == "" {
			profile = "(ad-hoc)"
		}
		rows = append(rows, []string{
			app.displayPath(dir.Directory), current, profile, dir.Mode,
			toolAccountList(dir.Accounts),
		})
	}
	printTable([]string{"Directory", "Current", "Profile", "Mode", "Accounts"}, rows)
}

func buildLs(ctx context.Context, app *App) (*lsReport, error) {
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
		Accounts:      accountItems(st, captured, app.capturedCredentialStates(ctx, captured)),
		Profiles:      app.profileStatuses(app.activeProfileName(st)),
	}, nil
}

func printLsReport(app *App, report *lsReport) {
	if len(report.Accounts) == 0 {
		fmt.Println("Accounts: (none — register one with: kae add <tool>)")
	} else {
		fmt.Println("Accounts:")
		now := app.Now()
		rows := [][]string{}
		for _, item := range report.Accounts {
			active := ""
			if item.Active {
				active = "*"
			}
			rows = append(rows, []string{
				item.Tool, item.Account, item.Identity, active, item.Driver,
				credentialCell(item.Credential, item.ReloginBy, now),
			})
		}
		printTable([]string{"Tool", "Account", "Identity", "Active", "Driver", "Credential"}, rows)
	}
	fmt.Println()
	if len(report.Profiles) == 0 {
		fmt.Println("Profiles: (none defined — add them with: kae edit)")
		return
	}
	fmt.Println("Profiles:")
	for _, profile := range report.Profiles {
		marker := ""
		if profile.Active {
			marker = "  (active)"
		}
		fmt.Printf("  %-14s %s%s\n", profile.Name, toolAccountList(profile.Accounts), marker)
	}
}
