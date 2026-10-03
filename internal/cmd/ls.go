package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/state"
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
	Mode      string `json:"mode"`    // shared | isolated | tree
	// Accounts is every tool the directory binds, in either mode (fragmentInfo).
	Accounts map[string]string `json:"accounts"`
	// Stores maps each bound tool to the config store this directory's binding
	// names for it, so a reader can reach it without deriving a pin-id from the
	// path. JSON only (docs/CLI.md § `kae ls --pins --json`) — the human table
	// keeps its columns, and a machine-specific path is not one a reader of it
	// asked for.
	Stores  map[string]string `json:"stores,omitempty"`
	Current bool              `json:"current"` // this is the current directory
	// Governing marks the binding that applies at the current directory: the
	// nearest ancestor bound directory, which Current (an exact match) misses from
	// a subdirectory.
	Governing bool `json:"governing"`
}

// pinsReport is the JSON contract of `kae ls --pins`: every directory bound
// right now, from anywhere. `kae status` answers for the current directory only,
// which is the wrong question with one worktree per agent.
type pinsReport struct {
	SchemaVersion    int        `json:"schema_version"`
	BoundDirectories []boundDir `json:"bound_directories"`
}

// CmdLs lists places and accounts (docs/CLI.md § kae ls Semantics): bare, every
// group; with a target, one group; with --current or --at, one place's path.
// Read-only.
func CmdLs(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args, "--at")
	var f lsFlags
	opts, ok := parseCommon("ls", flags, false, func(fs *flag.FlagSet) {
		registerLsFlags(fs, &f)
	})
	if !ok {
		return constants.ExitUsage
	}
	opts.Full = f.full
	req, code := parseLsRequest(f, positionals)
	if code != constants.ExitOK {
		return code
	}
	return runLsRequest(ctx, newApp(opts.ConfigPath), opts, req)
}

func runLsPins(app *App, opts commonOpts) int {
	report, err := buildLsPins(app, governingDir(app.governingBindingNow()))
	if err != nil {
		return finish(opts, err)
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	printPinsReport(app, report, colorEnabled(opts.NoColor))
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
//
// governing is the directory whose binding applies at the current directory (the
// nearest ancestor bound directory, "" for none); its row is marked governing.
func buildLsPins(app *App, governing string) (*pinsReport, error) {
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
			Stores:    app.bindingConfigStores(pin.PinID, info),
			Current:   cwd != "" && pin.Dir == cwd,
			Governing: governing != "" && samePath(pin.Dir, governing),
		})
	}
	// The breadcrumb index is ordered by pin-id (a path hash), which is meaningless to a
	// reader; sibling worktrees sort next to each other by path.
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Directory < dirs[j].Directory })
	return &pinsReport{SchemaVersion: constants.SchemaVersion, BoundDirectories: dirs}, nil
}

// bindingConfigStores resolves the config store one binding names for each tool
// it binds, for the listing's JSON. The store is named after a hash of the
// directory's absolute path, so a reader that has not reimplemented that hash
// cannot get there from a row; the paths are already kae's to publish
// (`kae status --json` carries the globally isolated homes the same way).
//
// It stays the fragment's reading, like every other field of the row: a path
// here is what the binding points at, not a directory this walk observed —
// nothing in buildLsPins stats a store. `boundDirStores` is the walk for a
// caller that needs the store to be there, and it requires exactly that.
//
// Over the fragment's own account map rather than constants.Tools, so a tool an
// older kae bound and this one has retired keeps the store its row already
// names. A mode kae does not recognize leaves the tool out instead of naming a
// guessed path, which is boundStoreDir's contract.
func (app *App) bindingConfigStores(pinID string, info fragmentInfo) map[string]string {
	stores := make(map[string]string, len(info.Accounts))
	for tool := range info.Accounts {
		if dir, bound := app.boundStoreDir(pinID, tool, info); bound {
			stores[tool] = dir
		}
	}
	return stores
}

func printPinsReport(app *App, report *pinsReport, color bool) {
	if len(report.BoundDirectories) == 0 {
		fmt.Println("Bound directories: (none); run: kae pin <profile>")
		return
	}
	fmt.Println("Bound directories:")
	rows := [][]string{}
	for i, dir := range report.BoundDirectories {
		current := activeMark(dir.Current, color)
		profile := dir.Profile
		if profile == "" {
			profile = "(ad-hoc)"
		}
		rows = append(rows, []string{
			strconv.Itoa(i + 1), app.displayPath(dir.Directory), current, profile, dir.Mode,
			toolAccountList(dir.Accounts),
		})
	}
	printTable([]string{"#", "Directory", "Current", "Profile", "Mode", "Accounts"}, rows, color)
}

func buildLs(ctx context.Context, app *App) (*lsReport, error) {
	return buildLsWith(ctx, app, app.readState())
}

// buildLsWith is buildLs over a state.json read the caller shares with the
// place groups.
func buildLsWith(ctx context.Context, app *App, loaded loadedState) (*lsReport, error) {
	items, st, err := buildAccountItemsWith(ctx, app, "", loaded)
	if err != nil {
		return nil, err
	}
	return &lsReport{
		SchemaVersion: constants.SchemaVersion,
		Accounts:      items,
		Profiles:      app.profileStatuses(app.activeProfileName(st)),
	}, nil
}

// buildAccountItems is the account rows of the account group, or of one tool's
// group when tool is set. It needs config, which is what makes a config error
// reach these rows and not the places beside them.
func buildAccountItems(ctx context.Context, app *App, tool string) ([]accountItem, *state.State, error) {
	return buildAccountItemsWith(ctx, app, tool, app.readState())
}

// loadedState is one read of state.json, shared by the groups of one `kae ls`
// so the file is read once; err is the read's error, surfaced by each consumer
// that needs the state.
type loadedState struct {
	st  *state.State
	err error
}

func (app *App) readState() loadedState {
	st, err := app.loadState()
	return loadedState{st: st, err: err}
}

// buildAccountItemsWith is buildAccountItems over a shared state read. The
// config error still comes first, as it did when this read state itself.
func buildAccountItemsWith(ctx context.Context, app *App, tool string, loaded loadedState) ([]accountItem, *state.State, error) {
	if err := app.requireConfig(); err != nil {
		return nil, nil, err
	}
	st, err := loaded.st, loaded.err
	if err != nil {
		return nil, nil, err
	}
	var captured []account.Account
	if tool == "" {
		captured, err = account.List(app.Paths.AccountsDir())
	} else {
		captured, err = account.ListForTool(app.Paths.AccountsDir(), tool)
	}
	if err != nil {
		return nil, nil, err
	}
	states := app.capturedCredentialStates(ctx, captured)
	usages := app.accountUsages(ctx, captured, st)
	return accountItems(st, captured, states, usages), st, nil
}

func printLsReport(app *App, report *lsReport, opts commonOpts) {
	printAccountItems(app, report.Accounts, "kae add <tool>", opts)
	fmt.Println()
	printProfileList(report.Profiles)
}

// printProfileList is the Profiles block of the ls and status reports.
func printProfileList(profiles []profileStatus) {
	if len(profiles) == 0 {
		fmt.Println("Profiles: (none defined); run: kae edit")
		return
	}
	fmt.Println("Profiles:")
	for _, profile := range profiles {
		marker := ""
		if profile.Active {
			marker = "  (active)"
		}
		fmt.Printf("  %-14s %s%s\n", profile.Name, toolAccountList(profile.Accounts), marker)
	}
}

// printAccountItems is the Accounts table, shared by the account group and a tool
// group; addHint is the command the empty case suggests.
func printAccountItems(app *App, items []accountItem, addHint string, opts commonOpts) {
	if len(items) == 0 {
		fmt.Printf("Accounts: (none); run: %s\n", addHint)
		return
	}
	fmt.Println("Accounts:")
	now := app.Now()
	color := colorEnabled(opts.NoColor)
	rows := [][]string{}
	for _, item := range items {
		active := activeMark(item.Active, color)
		rows = append(rows, []string{
			item.Tool, item.Account, item.Identity, active, item.Driver,
			credentialCell(item.Credential, item.ReloginBy, now),
			limitCell(item.Usage, now, color),
		})
	}
	printAccountTable([]string{"Tool", "Account", columnIdentity, "Active", columnDriver, "Credential", "Limit"}, rows, opts.Full, color)
}
