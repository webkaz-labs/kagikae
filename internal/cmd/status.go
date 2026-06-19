package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/state"
)

type toolStatus struct {
	Tool    string  `json:"tool"`
	Enabled bool    `json:"enabled"`
	Account *string `json:"account"`
	// Identity is the active account's recorded login identity (§D), additive and
	// omitempty so the JSON contract stays schema_version 1; blank for a
	// pre-identity snapshot or a tool/account with no readable identity.
	Identity    string   `json:"identity,omitempty"`
	Driver      string   `json:"driver"`
	AuthPresent bool     `json:"auth_present"`
	Accounts    []string `json:"accounts"`
	Warnings    []string `json:"warnings"`
}

// toolAccount is a (tool, account) map key for the captured-identity lookup.
type toolAccount struct{ tool, account string }

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
	// identityByAccount maps a (tool, account) pair to its recorded login identity,
	// so status can show the active account's identity (§D) without a second
	// snapshot read.
	identityByAccount := map[toolAccount]string{}
	for _, acc := range captured {
		capturedByTool[acc.Tool] = append(capturedByTool[acc.Tool], acc.Name)
		if acc.Identity != "" {
			identityByAccount[toolAccount{acc.Tool, acc.Name}] = acc.Identity
		}
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
	activeProfile := app.activeProfileName(st)
	if activeProfile != "" {
		report.ActiveProfile = &activeProfile
	}
	report.Profiles = app.profileStatuses(activeProfile)
	tools := app.enabledTools()
	// status is the most-run command; on macOS each tool's Detect is a live
	// `security` probe, so a sequential loop pays the sum. Run them concurrently
	// and assemble below in canonical (tools) order, so the output is unchanged.
	detections, err := detectTools(ctx, app.Env, tools)
	if err != nil {
		return nil, err
	}
	for i, tool := range tools {
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
		if ts.Account != nil {
			ts.Identity = identityByAccount[toolAccount{tool, *ts.Account}]
		}
		if det := detections[i]; det.err != nil {
			ts.Warnings = append(ts.Warnings, det.err.Error())
		} else {
			ts.Driver = det.info.Driver
			ts.AuthPresent = det.info.AuthPresent
			ts.Warnings = append(ts.Warnings, det.info.Warnings...)
		}
		report.Tools = append(report.Tools, ts)
	}
	return report, nil
}

// detection is one tool's concurrent Detect result.
type detection struct {
	info adapter.Info
	err  error
}

// detectTools runs each tool's Detect concurrently and returns the results in
// the same order as tools (so callers assemble in canonical order). A per-tool
// Detect failure is captured in its detection (non-fatal, surfaced as a tool
// warning, as the sequential version did); only an unknown adapter id aborts.
// Each goroutine writes its own index, so the results slice needs no lock; the
// shared adapter.Env is read-only (its Getenv/LookPath and the runner seam are
// safe for concurrent use).
func detectTools(ctx context.Context, env adapter.Env, tools []string) ([]detection, error) {
	results := make([]detection, len(tools))
	var wg sync.WaitGroup
	for i, tool := range tools {
		ad, err := adapter.ForTool(tool)
		if err != nil {
			return nil, err
		}
		wg.Add(1)
		go func(i int, ad adapter.Adapter) {
			defer wg.Done()
			info, derr := ad.Detect(ctx, env)
			results[i] = detection{info: info, err: derr}
		}(i, ad)
	}
	wg.Wait()
	return results, nil
}

// activeProfileName resolves the active profile: the recorded name wins (set by
// a profile-wide apply), else the per-tool mapping match so older state files
// still resolve. "" when none applies. Shared by status and ls.
func (app *App) activeProfileName(st *state.State) string {
	if st.ActiveProfile != "" {
		return st.ActiveProfile
	}
	return app.Config.MatchProfile(st.Active)
}

// profileStatuses builds the profile listing rows (ascending, stable order),
// marking activeProfile. Shared by `kae status` and `kae ls`.
func (app *App) profileStatuses(activeProfile string) []profileStatus {
	profiles := []profileStatus{}
	for _, name := range app.Config.ProfileNames() {
		profile := app.Config.Profiles[name]
		accounts := profile.Accounts
		if accounts == nil {
			// A [profiles.X] section without accounts parses to a nil map;
			// keep the JSON contract at {} rather than null.
			accounts = map[string]string{}
		}
		profiles = append(profiles, profileStatus{
			Name: name, Label: profile.Label, Accounts: accounts,
			Active: name == activeProfile,
		})
	}
	return profiles
}

// accountItems builds the captured-account listing rows shared by
// `kae accounts` and `kae ls`, marking the active account per tool.
func accountItems(st *state.State, captured []account.Account) []accountItem {
	items := []accountItem{}
	for _, acc := range captured {
		items = append(items, accountItem{
			Tool:       acc.Tool,
			Account:    acc.Name,
			Identity:   acc.Identity,
			Driver:     acc.Driver,
			Active:     st.Active[acc.Tool] == acc.Name,
			CapturedAt: acc.CapturedAt.UTC().Format(time.RFC3339),
		})
	}
	return items
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
		rows = append(rows, []string{ts.Tool, accountName, ts.Identity, ts.Driver, auth, notes})
	}
	printTable([]string{"Tool", "Account", "Identity", "Driver", "Auth", "Notes"}, rows)
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
	Tool    string `json:"tool"`
	Account string `json:"account"`
	// Identity is the raw login identity detected at capture (§D), additive and
	// omitempty so the JSON contract stays schema_version 1; blank for
	// pre-v0.8.3 snapshots and tools with no readable identity.
	Identity   string `json:"identity,omitempty"`
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
	report := accountsReport{SchemaVersion: constants.SchemaVersion, Accounts: accountItems(st, captured)}
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
		rows = append(rows, []string{item.Tool, item.Account, item.Identity, active, item.Driver, item.CapturedAt})
	}
	printTable([]string{"Tool", "Account", "Identity", "Active", "Driver", "Captured"}, rows)
	return constants.ExitOK
}
