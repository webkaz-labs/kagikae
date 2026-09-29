package cmd

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// lsFlags is every flag `kae ls` takes beyond the common ones.
type lsFlags struct {
	pins, current              bool
	at                         atFlag
	project, below, home, root bool
	shared, isolated           bool
}

// atFlag is `--at N`, which must tell "not given" from any value given.
type atFlag struct {
	n   int
	set bool
}

func (a *atFlag) String() string {
	if a == nil || !a.set {
		return ""
	}
	return strconv.Itoa(a.n)
}

func (a *atFlag) Set(value string) error {
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return fmt.Errorf("--at takes a place number from 1, got %q", value)
	}
	a.n, a.set = n, true
	return nil
}

// lsRequest is a parsed `kae ls` command line.
type lsRequest struct {
	target   string // "" for bare ls; a PlaceGroup word or a tool
	explicit *explicitUserLevel
	current  bool
	at       int    // 0 when --at was not given
	level    string // PlaceKindProject / PlaceKindBelow / PlaceKindHome, or ""
	root     bool
}

const lsUsage = "usage: kae ls [account|pin|repo|kae|<tool> | -s <tool> | -i <tool> <account>] [--current [--project|--below|--home] [--root] | --at N] [--json]"

// resolveLsTarget matches a target word: the exact words first, then a prefix
// of a tool name (and only a tool name).
func resolveLsTarget(word string) (string, int) {
	if slices.Contains(constants.PlaceGroups, word) || constants.IsTool(word) {
		return word, constants.ExitOK
	}
	matches := toolPrefixMatches(word)
	switch len(matches) {
	case 1:
		return matches[0], constants.ExitOK
	case 0:
		candidates := append(append([]string{}, constants.PlaceGroups...), constants.Tools...)
		return "", usageError("unknown ls target: %s (targets: %s, or a tool: %s)%s", word,
			strings.Join(constants.PlaceGroups, ", "), strings.Join(constants.Tools, ", "), didYouMean(word, candidates))
	default:
		return "", usageError("ambiguous ls target %q: matches %s", word, strings.Join(matches, ", "))
	}
}

// parseLsRequest validates the command line against docs/CLI.md § kae ls
// Semantics. Every refusal is a usage error, printed here.
func parseLsRequest(f lsFlags, positionals []string) (lsRequest, int) {
	req := lsRequest{current: f.current, root: f.root}
	if f.at.set {
		req.at = f.at.n
	}
	if f.shared && f.isolated {
		return req, usageError("-s and -i are mutually exclusive")
	}
	switch {
	case f.isolated:
		if len(positionals) < 2 {
			return req, usageError("-i names an account's isolated home: kae ls -i <tool> <account>")
		}
		if len(positionals) > 2 {
			return req, usageError("%s", lsUsage)
		}
	case f.shared:
		if len(positionals) == 2 {
			return req, usageError("-s names the real home, which holds whichever account is active; name an account with -i: kae ls -i %s %s", positionals[0], positionals[1])
		}
		if len(positionals) != 1 {
			return req, usageError("-s names a tool's real home: kae ls -s <tool>")
		}
	default:
		if len(positionals) == 2 {
			return req, usageError("a shared home holds whichever account is active, so an account needs -i: kae ls -i %s %s", positionals[0], positionals[1])
		}
		if len(positionals) > 2 {
			return req, usageError("%s", lsUsage)
		}
	}
	if len(positionals) > 0 {
		target, code := resolveLsTarget(positionals[0])
		if code != constants.ExitOK {
			return req, code
		}
		req.target = target
	}
	if f.shared || f.isolated {
		if !constants.IsTool(req.target) {
			return req, usageError("-s and -i resolve a tool's user level; %q is not a tool", req.target)
		}
		req.explicit = &explicitUserLevel{tool: req.target, isolated: f.isolated}
		if f.isolated {
			account := positionals[1]
			if !config.ValidFileName(account) {
				return req, usageError("invalid account name: %q", account)
			}
			req.explicit.account = account
		}
	}
	if f.pins {
		if req.target != "" && req.target != constants.PlaceGroupPin {
			return req, usageError("--pins is kae ls pin; it takes no other target")
		}
		req.target = constants.PlaceGroupPin
	}

	levels := 0
	for _, l := range []struct {
		set  bool
		kind string
	}{{f.project, constants.PlaceKindProject}, {f.below, constants.PlaceKindBelow}, {f.home, constants.PlaceKindHome}} {
		if l.set {
			levels++
			req.level = l.kind
		}
	}
	if levels > 1 {
		return req, usageError("--project, --below and --home each choose one level; give one")
	}
	if req.current && req.at != 0 {
		return req, usageError("--current and --at each choose one place; give one")
	}
	if req.level != "" && !req.current {
		return req, usageError("--project, --below and --home select a place with --current")
	}
	if req.root && !req.current && req.at == 0 {
		return req, usageError("--root selects the directory holding a project level, with --current or --at")
	}
	if req.root && req.current && req.level != constants.PlaceKindProject && req.level != constants.PlaceKindBelow {
		return req, usageError("--root applies to a project level: add --project or --below")
	}
	if req.level != "" && !constants.IsTool(req.target) {
		return req, usageError("a level selector needs a tool target: kae ls <tool> --current %s", "--"+req.level)
	}
	if req.explicit != nil && req.explicit.isolated && req.level == constants.PlaceKindHome {
		return req, usageError("-i resolves the user level and --home selects the real home; give one")
	}
	if req.current || req.at != 0 {
		switch req.target {
		case "":
			return req, usageError("--current and --at need a target: kae ls <target> --current")
		case constants.PlaceGroupAccount:
			return req, usageError("account rows are not places; choose pin, repo, kae or a tool")
		}
	}
	return req, constants.ExitOK
}

func runLsRequest(ctx context.Context, app *App, opts commonOpts, req lsRequest) int {
	switch {
	case req.current || req.at != 0:
		return runLsPick(ctx, app, opts, req)
	case req.target == "":
		return runLs(ctx, app, opts)
	case req.target == constants.PlaceGroupAccount:
		return runLsAccount(ctx, app, opts)
	case req.target == constants.PlaceGroupPin:
		return runLsPins(app, opts)
	case constants.IsTool(req.target):
		return runLsTool(ctx, app, opts, req)
	default:
		return runLsGroup(ctx, app, opts, req.target)
	}
}

// bareLsReport is bare `kae ls --json`: the account group's keys, with every
// other group's places beside them.
type bareLsReport struct {
	SchemaVersion int             `json:"schema_version"`
	Accounts      []accountItem   `json:"accounts"`
	Profiles      []profileStatus `json:"profiles"`
	Places        []placeRow      `json:"places"`
}

// toolLsReport is `kae ls <tool> --json`: that tool's accounts and places.
type toolLsReport struct {
	SchemaVersion int           `json:"schema_version"`
	Tool          string        `json:"tool"`
	Accounts      []accountItem `json:"accounts"`
	Places        []placeRow    `json:"places"`
}

// groupLsReport is `kae ls repo|kae --json`.
type groupLsReport struct {
	SchemaVersion int        `json:"schema_version"`
	Group         string     `json:"group"`
	Places        []placeRow `json:"places"`
}

// placeReport is `--current` / `--at` with --json: the path printed, and the
// place it came from (path differs from place.path only with --root).
type placeReport struct {
	SchemaVersion int      `json:"schema_version"`
	Path          string   `json:"path"`
	Place         placeRow `json:"place"`
}

// runLsAccount is `kae ls account`: the accounts and profiles view.
func runLsAccount(ctx context.Context, app *App, opts commonOpts) int {
	report, err := buildLs(ctx, app)
	if err != nil {
		return finish(opts, err)
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	printLsReport(app, report, opts)
	return constants.ExitOK
}

// firstExit keeps the first failing exit code.
func firstExit(code, next int) int {
	if code != constants.ExitOK {
		return code
	}
	return next
}

// reportGroupError prints a group's error in human output and returns its exit
// code; the other groups are still shown.
func reportGroupError(err error) int {
	fmt.Fprintln(os.Stderr, "kae:", err)
	return exitOf(err)
}

func governingDir(b *governingBinding) string {
	if b == nil {
		return ""
	}
	return b.dir
}

// runLs is bare `kae ls`: the account group, the pin group, each relevant tool's
// places, the repository root and kae's directories.
//
// The account group needs config, and its failure keeps today's contract: in
// human output the error goes to stderr, the other groups are still shown and
// the exit code is the error's; with --json it is the JSON error object. Any
// other group that fails is left out with a warning on stderr, in both formats,
// and — a warning — does not change the exit code.
func runLs(ctx context.Context, app *App, opts commonOpts) int {
	loaded := app.readState()
	report, accountErr := buildLsWith(ctx, app, loaded)
	if accountErr != nil && opts.Format == formatJSON {
		return finish(opts, accountErr)
	}
	// warned keeps one message from printing twice: an unreadable state.json
	// fails both the account rows and every tool's user level.
	warned := map[string]bool{}
	if accountErr != nil {
		warned[accountErr.Error()] = true
	}
	warnGroup := func(group string, err error) {
		if !warned[err.Error()] {
			warned[err.Error()] = true
			fmt.Fprintf(os.Stderr, "kae: warning: the %s group is not listed: %v\n", group, err)
		}
	}
	var (
		pins     *pinsReport
		tools    []string
		toolRows = map[string][]placeRow{}
		repo     []placeRow
	)
	if pc, err := app.newPlaceContextWith(ctx, nil, loaded); err != nil {
		warnGroup("place", err)
	} else {
		if pins, err = buildLsPins(app, governingDir(pc.binding)); err != nil {
			warnGroup(constants.PlaceGroupPin, err)
			pins = nil
		}
		for _, tool := range placeTools() {
			if !pc.toolRelevant(tool) {
				continue
			}
			rows, err := app.toolPlaces(ctx, pc, tool)
			if err != nil {
				warnGroup(tool, err)
				continue
			}
			tools = append(tools, tool)
			toolRows[tool] = rows
		}
		repo = pc.repoPlaces()
	}
	kae := app.kaePlaces()

	if opts.Format == formatJSON {
		places := []placeRow{}
		if pins != nil {
			places = append(places, pinPlaces(pins.BoundDirectories)...)
		}
		for _, tool := range tools {
			places = append(places, toolRows[tool]...)
		}
		places = append(append(places, repo...), kae...)
		return encodeJSON(bareLsReport{
			SchemaVersion: constants.SchemaVersion,
			Accounts:      report.Accounts,
			Profiles:      report.Profiles,
			Places:        places,
		})
	}
	code := constants.ExitOK
	color := colorEnabled(opts.NoColor)
	var groups []func()
	if accountErr != nil {
		code = reportGroupError(accountErr)
	} else {
		groups = append(groups, func() { printLsReport(app, report, opts) })
	}
	if pins != nil {
		groups = append(groups, func() { printPinsReport(app, pins, color) })
	}
	for _, tool := range tools {
		groups = append(groups, func() { printToolPlaces(app, tool, toolRows[tool], color) })
	}
	if repo != nil {
		groups = append(groups, func() { printRepoPlaces(app, repo, color) })
	}
	groups = append(groups, func() { printKaePlaces(app, kae, color) })
	for i, group := range groups {
		if i > 0 {
			fmt.Println()
		}
		group()
	}
	return code
}

// runLsTool is `kae ls <tool>`: the tool's accounts, which need config, and its
// places, which do not.
func runLsTool(ctx context.Context, app *App, opts commonOpts, req lsRequest) int {
	tool := req.target
	loaded := app.readState()
	items, _, accountErr := buildAccountItemsWith(ctx, app, tool, loaded)
	if accountErr != nil && opts.Format == formatJSON {
		return finish(opts, accountErr)
	}
	pc, err := app.newPlaceContextWith(ctx, req.explicit, loaded)
	if err != nil {
		return finish(opts, err)
	}
	rows, placeErr := app.toolPlaces(ctx, pc, tool)
	if opts.Format == formatJSON {
		if placeErr != nil {
			return finish(opts, placeErr)
		}
		return encodeJSON(toolLsReport{SchemaVersion: constants.SchemaVersion, Tool: tool, Accounts: items, Places: rows})
	}
	code := constants.ExitOK
	if accountErr != nil {
		code = reportGroupError(accountErr)
	} else {
		printAccountItems(app, items, "kae add "+tool, opts)
		fmt.Println()
	}
	if placeErr != nil {
		if accountErr == nil || accountErr.Error() != placeErr.Error() {
			return firstExit(code, reportGroupError(placeErr))
		}
		return code
	}
	printToolPlaces(app, tool, rows, colorEnabled(opts.NoColor))
	return code
}

// runLsGroup is `kae ls repo` and `kae ls kae`, neither of which reads config
// or state.
func runLsGroup(ctx context.Context, app *App, opts commonOpts, group string) int {
	rows, err := app.groupPlaces(ctx, group, nil)
	if err != nil {
		return finish(opts, err)
	}
	if opts.Format == formatJSON {
		return encodeJSON(groupLsReport{SchemaVersion: constants.SchemaVersion, Group: group, Places: rows})
	}
	color := colorEnabled(opts.NoColor)
	if group == constants.PlaceGroupRepo {
		printRepoPlaces(app, rows, color)
	} else {
		printKaePlaces(app, rows, color)
	}
	return constants.ExitOK
}

// pinGroupPlaces is the pin group's rows, marked against an already-found
// governing binding (so its walk, and any warning it prints, happens once).
func (app *App) pinGroupPlaces(governing *governingBinding) ([]placeRow, error) {
	pins, err := buildLsPins(app, governingDir(governing))
	if err != nil {
		return nil, err
	}
	return pinPlaces(pins.BoundDirectories), nil
}

// groupPlaces lists one group's places exactly as `kae ls <group>` numbers them.
func (app *App) groupPlaces(ctx context.Context, group string, explicit *explicitUserLevel) ([]placeRow, error) {
	switch group {
	case constants.PlaceGroupKae:
		return app.kaePlaces(), nil
	case constants.PlaceGroupPin:
		return app.pinGroupPlaces(app.governingBindingNow())
	}
	pc, err := app.newPlaceContext(ctx, explicit)
	if err != nil {
		return nil, err
	}
	if group == constants.PlaceGroupRepo {
		return pc.repoPlaces(), nil
	}
	return app.toolPlaces(ctx, pc, group)
}

// runLsPick is `--current` and `--at N`: one place's path on stdout.
func runLsPick(ctx context.Context, app *App, opts commonOpts, req lsRequest) int {
	row, code := app.pickPlace(ctx, opts, req)
	if code != constants.ExitOK {
		return code
	}
	path := row.Path
	if req.root {
		if row.Root == "" {
			return usageError("--root applies to a project level; place %d of kae ls %s is a %s place", row.Number, req.target, row.Kind)
		}
		path = row.Root
	}
	if opts.Format == formatJSON {
		return encodeJSON(placeReport{SchemaVersion: constants.SchemaVersion, Path: path, Place: row})
	}
	fmt.Println(path)
	return constants.ExitOK
}

// pickPlace chooses the place --current or --at names. A failure has already
// been reported; its exit code is returned.
func (app *App) pickPlace(ctx context.Context, opts commonOpts, req lsRequest) (placeRow, int) {
	if req.target == constants.PlaceGroupPin && req.current {
		cwd, err := cwdAbs()
		if err != nil {
			return placeRow{}, finish(opts, err)
		}
		binding := app.governingBindingAt(cwd)
		if binding == nil {
			return placeRow{}, finish(opts, errf(constants.ExitNotFound, "no bound directory governs %s", cwd))
		}
		rows, err := app.pinGroupPlaces(binding)
		if err != nil {
			return placeRow{}, finish(opts, err)
		}
		for _, row := range rows {
			if samePath(row.Path, binding.dir) {
				return row, constants.ExitOK
			}
		}
		// Bound by a kae older than the breadcrumb, so not in the listing: no number.
		return placeRow{
			Group: constants.PlaceGroupPin, Kind: constants.PlaceKindBoundDirectory, Path: binding.dir,
			Exists: true, InEffect: true, Source: constants.PlaceSourcePin, Mode: binding.info.Mode,
		}, constants.ExitOK
	}
	rows, err := app.groupPlaces(ctx, req.target, req.explicit)
	if err != nil {
		return placeRow{}, finish(opts, err)
	}
	if req.at != 0 {
		if req.at > len(rows) {
			return placeRow{}, usageError("kae ls %s lists %d place(s); there is no place %d", req.target, len(rows), req.at)
		}
		return rows[req.at-1], constants.ExitOK
	}
	var matches []placeRow
	switch {
	case constants.IsTool(req.target) && projectLevelName(req.target) == "":
		return placeRow{}, finish(opts, errf(constants.ExitNotFound, "kae resolves no places for %s", req.target))
	case constants.IsTool(req.target) && req.level == "":
		matches = rows[:1] // the effective user level is always first
	case req.level == constants.PlaceKindProject:
		// The nearest effective ancestor level: the first in list order.
		for _, row := range rows {
			if row.Kind == constants.PlaceKindProject {
				matches = []placeRow{row}
				break
			}
		}
	case req.level == constants.PlaceKindBelow:
		for _, row := range rows {
			if row.Kind == constants.PlaceKindBelow {
				matches = append(matches, row)
			}
		}
	case req.level == constants.PlaceKindHome:
		home := app.realToolHome(req.target)
		for _, row := range rows {
			if (row.Kind == constants.PlaceKindHome || row.Kind == constants.PlaceKindUser) && samePath(row.Path, home) {
				matches = []placeRow{row}
				break
			}
		}
	default: // repo, kae
		matches = rows
	}
	switch len(matches) {
	case 0:
		what := req.target
		if req.level != "" {
			what += " --" + req.level
		}
		return placeRow{}, finish(opts, errf(constants.ExitNotFound, "no current place for kae ls %s here", what))
	case 1:
		return matches[0], constants.ExitOK
	}
	names := make([]string, 0, len(matches))
	for _, row := range matches {
		names = append(names, fmt.Sprintf("%d %s", row.Number, row.Path))
	}
	return placeRow{}, usageError("kae ls %s --current matches %d places; choose one with --at N: %s",
		req.target, len(matches), strings.Join(names, ", "))
}

// placePathCell is a place's path for the human tables, marked when absent.
func (app *App) placePathCell(row placeRow) string {
	path := app.displayPath(row.Path)
	if !row.Exists {
		path += " (missing)"
	}
	return path
}

func printToolPlaces(app *App, tool string, rows []placeRow, color bool) {
	if len(rows) == 0 {
		fmt.Printf("%s places: (none — kae resolves places for %s only)\n", tool, strings.Join(placeTools(), " and "))
		return
	}
	fmt.Printf("%s places:\n", tool)
	table := [][]string{}
	for _, row := range rows {
		table = append(table, []string{
			strconv.Itoa(row.Number), row.Kind, app.placePathCell(row), activeMark(row.InEffect, color),
			orDash(row.Source), orDash(row.Mode), orDash(row.Account), orDash(strings.Join(row.Applies, ",")),
		})
	}
	printTable([]string{"#", "Level", "Path", "In effect", "Source", "Mode", "Account", "Applies"}, table, color)
}

func printRepoPlaces(app *App, rows []placeRow, color bool) {
	if len(rows) == 0 {
		fmt.Println("Repository: (none — the current directory is not in a Git repository)")
		return
	}
	fmt.Println("Repository:")
	table := [][]string{}
	for _, row := range rows {
		table = append(table, []string{strconv.Itoa(row.Number), app.placePathCell(row)})
	}
	printTable([]string{"#", "Root"}, table, color)
}

func printKaePlaces(app *App, rows []placeRow, color bool) {
	fmt.Println("kae directories:")
	table := [][]string{}
	for _, row := range rows {
		table = append(table, []string{strconv.Itoa(row.Number), row.Kind, app.placePathCell(row)})
	}
	printTable([]string{"#", "Kind", "Path"}, table, color)
}
