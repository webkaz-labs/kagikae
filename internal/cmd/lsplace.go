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

// lsFlags is every flag `kae ls` takes beyond the common ones; `kae open` and
// `kae cd` take all of them but pins and current (registerPlaceFlags), and add pick.
type lsFlags struct {
	pins, current              bool
	full                       bool
	at                         atFlag
	project, below, home, root bool
	shared, isolated           bool
	pick                       bool
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
	verb     string // the command parsed for: ls, open or cd
	target   string // "" for bare ls; a PlaceGroup word or a tool
	explicit *explicitUserLevel
	current  bool
	at       int    // 0 when --at was not given
	level    string // PlaceKindProject / PlaceKindBelow / PlaceKindHome, or ""
	root     bool
	pick     bool // open and cd only: choose among the target's places in the picker
}

// errNoPlaces is the error for a tool target that has no project-level places.
func errNoPlaces(target string) *cmdError {
	return errf(constants.ExitNotFound, "kae resolves no places for %s", target)
}

const lsUsage = "usage: kae ls [account|pin|repo|kae|<tool> | -s <tool> | -i <tool> <account>] [--current [--project|--below|--home] [--root] | --at N] [-f|--full] [--json]"

// resolveLsTarget matches a target word: the exact words first, then a prefix
// of a tool name (and only a tool name). verb names the command in the error.
func resolveLsTarget(verb, word string) (string, int) {
	if slices.Contains(constants.PlaceGroups, word) || constants.IsTool(word) {
		return word, constants.ExitOK
	}
	matches := toolPrefixMatches(word)
	switch len(matches) {
	case 1:
		return matches[0], constants.ExitOK
	case 0:
		groups := targetGroups(verb)
		candidates := append(append([]string{}, groups...), constants.Tools...)
		return "", usageError("unknown %s target: %s (targets: %s, or a tool: %s)%s", verb, word,
			strings.Join(groups, ", "), strings.Join(constants.Tools, ", "), didYouMean(word, candidates))
	default:
		return "", usageError("ambiguous %s target %q: matches %s", verb, word, strings.Join(matches, ", "))
	}
}

// targetGroups is the fixed target words verb offers: every group for ls, and
// all but account for open and cd, which refuse it.
func targetGroups(verb string) []string {
	if verb == "ls" {
		return constants.PlaceGroups
	}
	return slices.DeleteFunc(slices.Clone(constants.PlaceGroups), func(g string) bool { return g == constants.PlaceGroupAccount })
}

// parseLsRequest validates the command line against docs/CLI.md § kae ls
// Semantics. Every refusal is a usage error, printed here.
func parseLsRequest(f lsFlags, positionals []string) (lsRequest, int) {
	return parsePlaceArgs("ls", f, positionals)
}

// parsePlaceArgs is the target, explicit-resolution and selector grammar that
// `kae ls`, `kae open` and `kae cd` share (docs/CLI.md § kae ls Semantics and
// § kae open and kae cd Semantics). verb is the command it parses for. `ls`
// chooses one place with --current or --at; `open` and `cd` always choose one,
// so they take no --current (f.current is always false for them) and their
// request is --current unless --at is given. For them a missing target and a
// level selector without a tool are not usage errors here: resolution decides.
func parsePlaceArgs(verb string, f lsFlags, positionals []string) (lsRequest, int) {
	navigate := verb != "ls"
	usage := lsUsage
	if navigate {
		usage = placeUsage(verb)
	}
	req := lsRequest{verb: verb, current: f.current, root: f.root, pick: navigate && f.pick}
	if f.at.set {
		req.at = f.at.n
	}
	if req.pick && f.at.set {
		return req, usageError("--pick chooses a place in the picker and --at names one; give one")
	}
	if navigate && !f.at.set {
		req.current = true
	}
	if f.shared && f.isolated {
		return req, usageError("-s and -i are mutually exclusive")
	}
	switch {
	case f.isolated:
		if len(positionals) < 2 {
			return req, usageError("-i names an account's isolated home: kae %s -i <tool> <account>", verb)
		}
		if len(positionals) > 2 {
			return req, usageError("%s", usage)
		}
	case f.shared:
		if len(positionals) == 2 {
			return req, usageError("-s names the real home, which holds whichever account is active; name an account with -i: kae %s -i %s %s", verb, positionals[0], positionals[1])
		}
		if len(positionals) != 1 {
			return req, usageError("-s names a tool's real home: kae %s -s <tool>", verb)
		}
	default:
		if len(positionals) == 2 {
			return req, usageError("a shared home holds whichever account is active, so an account needs -i: kae %s -i %s %s", verb, positionals[0], positionals[1])
		}
		if len(positionals) > 2 {
			return req, usageError("%s", usage)
		}
	}
	if len(positionals) > 0 {
		target, code := resolveLsTarget(verb, positionals[0])
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
		if navigate {
			return req, usageError("--project, --below and --home choose a level and --at a place number; give one")
		}
		return req, usageError("--project, --below and --home select a place with --current")
	}
	if req.root && !req.current && req.at == 0 {
		return req, usageError("--root selects the directory holding a project level, with --current or --at")
	}
	// --pick alone waives the level requirement, and only where a level could
	// follow: no target or a tool. --home, repo, kae and pin stay refused.
	pickMayOmitLevel := req.pick && req.level == "" && (req.target == "" || constants.IsTool(req.target))
	if req.root && req.current && !pickMayOmitLevel && req.level != constants.PlaceKindProject && req.level != constants.PlaceKindBelow {
		return req, usageError("--root applies to a project level: add --project or --below")
	}
	if req.level != "" && !constants.IsTool(req.target) && (!navigate || req.target != "") {
		if navigate {
			return req, usageError("a level selector needs a tool target: kae %s <tool> %s", verb, "--"+req.level)
		}
		return req, usageError("a level selector needs a tool target; run: kae ls <tool> --current %s", "--"+req.level)
	}
	if req.explicit != nil && req.explicit.isolated && req.level == constants.PlaceKindHome {
		return req, usageError("-i resolves the user level and --home selects the real home; give one")
	}
	if req.current || req.at != 0 {
		switch {
		case req.target == "" && !navigate:
			return req, usageError("--current and --at need a target; run: kae ls <target> --current")
		case req.target == "" && req.at != 0:
			return req, usageError("--at is the number kae ls <target> shows, so it needs a target: kae %s <target> --at N", verb)
		case req.target == constants.PlaceGroupAccount:
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
	// An unreadable state.json fails both the account rows and every tool's user
	// level; the account error is already reported, so it is not warned again.
	g := app.collectPlaceGroups(ctx, loaded, warnGroupOnce(accountErr))

	if opts.Format == formatJSON {
		return encodeJSON(bareLsReport{
			SchemaVersion: constants.SchemaVersion,
			Accounts:      report.Accounts,
			Profiles:      report.Profiles,
			Places:        g.places(),
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
	if g.pins != nil {
		groups = append(groups, func() { printPinsReport(app, g.pins, color) })
	}
	for _, tool := range g.tools {
		groups = append(groups, func() { printToolPlaces(app, tool, g.toolRows[tool], color) })
	}
	if g.repo != nil {
		groups = append(groups, func() { printRepoPlaces(app, g.repo, color) })
	}
	groups = append(groups, func() { printKaePlaces(app, g.kae, color) })
	for i, group := range groups {
		if i > 0 {
			fmt.Println()
		}
		group()
	}
	return code
}

// placeGroups is every place group bare `kae ls` shows, beside the account
// group: pin, each relevant tool, repo and kae. A group that failed is absent
// (pins nil, a tool left out of tools, repo nil); its warning has been given.
type placeGroups struct {
	pins     *pinsReport
	tools    []string
	toolRows map[string][]placeRow
	repo     []placeRow
	kae      []placeRow
}

// collectPlaceGroups resolves the place groups once, reporting each group that
// fails through warnGroup and leaving it out.
func (app *App) collectPlaceGroups(ctx context.Context, loaded loadedState, warnGroup func(group string, err error)) placeGroups {
	g := placeGroups{toolRows: map[string][]placeRow{}}
	if pc, err := app.newPlaceContextWith(ctx, nil, loaded); err != nil {
		warnGroup("place", err)
	} else {
		if g.pins, err = buildLsPins(app, governingDir(pc.binding)); err != nil {
			warnGroup(constants.PlaceGroupPin, err)
			g.pins = nil
		}
		for _, tool := range placeTools() {
			if !pc.toolRelevant(tool) && !app.hasSession(pc, tool) {
				continue
			}
			rows, err := app.toolPlaces(ctx, pc, tool)
			if err != nil {
				warnGroup(tool, err)
				continue
			}
			g.tools = append(g.tools, tool)
			g.toolRows[tool] = rows
		}
		g.repo = pc.repoPlaces()
	}
	g.kae = app.kaePlaces()
	return g
}

// warnGroupOnce warns that a group is not listed, once per distinct error;
// seen errors (already reported) are never warned about.
func warnGroupOnce(seen ...error) func(group string, err error) {
	warned := map[string]bool{}
	for _, err := range seen {
		if err != nil {
			warned[err.Error()] = true
		}
	}
	return func(group string, err error) {
		if !warned[err.Error()] {
			warned[err.Error()] = true
			fmt.Fprintf(os.Stderr, "kae: warning: the %s group is not listed: %v\n", group, err)
		}
	}
}

// groups is the place groups in bare `kae ls` order, the one owner of that order.
// Relevant marks a group holding a place that bears on the current directory: a
// governing binding, a tool `kae ls` shows, the repository.
func (g placeGroups) groups() []candidateGroup {
	var groups []candidateGroup
	if g.pins != nil {
		rows := pinPlaces(g.pins.BoundDirectories)
		groups = append(groups, candidateGroup{
			Heading: constants.PlaceGroupPin, Rows: rows,
			Relevant: slices.ContainsFunc(rows, func(row placeRow) bool { return row.InEffect }),
		})
	}
	for _, tool := range g.tools {
		groups = append(groups, candidateGroup{Heading: tool, Rows: g.toolRows[tool], Relevant: true})
	}
	return append(groups,
		candidateGroup{Heading: constants.PlaceGroupRepo, Rows: g.repo, Relevant: true},
		candidateGroup{Heading: constants.PlaceGroupKae, Rows: g.kae})
}

// places is the groups' rows in bare `kae ls` order.
func (g placeGroups) places() []placeRow {
	places := []placeRow{}
	for _, group := range g.groups() {
		places = append(places, group.Rows...)
	}
	return places
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
	choice, code := app.pickPlace(ctx, opts, req)
	if code != constants.ExitOK {
		return code
	}
	switch choice.kind {
	case choiceNone:
		return finish(opts, choice.noneError("ls", req))
	case choiceSeveral:
		names := make([]string, 0, len(choice.candidates))
		for _, row := range choice.candidates {
			names = append(names, fmt.Sprintf("%d %s", row.Number, row.Path))
		}
		return usageError("kae ls %s --current matches %d places; choose one with --at N: %s",
			req.target, len(choice.candidates), strings.Join(names, ", "))
	}
	row := choice.row
	path, code := placePath(row, req)
	if code != constants.ExitOK {
		return code
	}
	if opts.Format == formatJSON {
		return encodeJSON(placeReport{SchemaVersion: constants.SchemaVersion, Path: path, Place: row})
	}
	fmt.Println(path)
	return constants.ExitOK
}

// placePath is the path a chosen place prints: the place itself, or with --root
// the directory holding its project level.
func placePath(row placeRow, req lsRequest) (string, int) {
	if !req.root {
		return row.Path, constants.ExitOK
	}
	if row.Root == "" {
		return "", usageError("--root applies to a project level; place %d of kae ls %s is a %s place", row.Number, req.target, row.Kind)
	}
	return row.Root, constants.ExitOK
}

// choiceKind says what a place request resolved to.
type choiceKind int

const (
	choiceOne     choiceKind = iota // one place: placeChoice.row
	choiceSeveral                   // several places match: placeChoice.candidates
	choiceNone                      // no current place: candidates are the target's places (not pin's; currentPinPlace)
)

// placeChoice is what --current or --at resolved to. ls reports several and
// none itself; open and cd turn them into a candidate set for the picker or the
// list (candidates.go).
type placeChoice struct {
	kind       choiceKind
	row        placeRow
	candidates []placeRow
	// governs is set for a pin request with no governing binding: the directory
	// the not_found message names.
	governs string
}

// noneError is the not_found a choiceNone reports, as verb.
func (c placeChoice) noneError(verb string, req lsRequest) error {
	if c.governs != "" {
		return errf(constants.ExitNotFound, "no bound directory governs %s", c.governs)
	}
	what := req.target
	if req.level != "" {
		what += " --" + req.level
	}
	return errf(constants.ExitNotFound, "no current place for kae %s %s here", verb, what)
}

// pickPlace chooses the place --current or --at names. A failure has already
// been reported and its exit code is returned; otherwise the code is ExitOK and
// the choice says what was found.
func (app *App) pickPlace(ctx context.Context, opts commonOpts, req lsRequest) (placeChoice, int) {
	switch {
	case req.target == constants.PlaceGroupPin && req.current:
		return app.currentPinPlace(opts)
	case constants.IsTool(req.target):
		pc, err := app.newPlaceContext(ctx, req.explicit)
		if err != nil {
			return placeChoice{}, finish(opts, err)
		}
		return app.selectToolPlace(ctx, opts, pc, req)
	}
	rows, err := app.groupPlaces(ctx, req.target, req.explicit)
	if err != nil {
		return placeChoice{}, finish(opts, err)
	}
	return selectPlace(app, opts, req, rows)
}

// selectToolPlace is selectPlace over a tool's places. The user level and the
// ancestor project levels come before the below levels in list order, so a
// request for one of them (no selector, or --project) is chosen without finding
// the below levels, which can mean a `git ls-files` over the repository. Any
// other request, and one that finds no single place, uses the full list, whose
// numbers are `kae ls <tool>`'s.
func (app *App) selectToolPlace(ctx context.Context, opts commonOpts, pc *placeContext, req lsRequest) (placeChoice, int) {
	if req.at == 0 && (req.level == "" || req.level == constants.PlaceKindProject) {
		rows, err := app.leadingToolPlaces(pc, req.target)
		if err != nil {
			return placeChoice{}, finish(opts, err)
		}
		choice, code := selectPlace(app, opts, req, rows)
		if code != constants.ExitOK || choice.kind == choiceOne {
			return choice, code
		}
	}
	rows, err := app.toolPlaces(ctx, pc, req.target)
	if err != nil {
		return placeChoice{}, finish(opts, err)
	}
	return selectPlace(app, opts, req, rows)
}

// currentPinPlace is `pin --current`: the nearest ancestor bound directory. With
// none it carries no candidates: the pin listing reads every bound directory's
// fragment (and warns about the unreadable ones), which `kae ls pin --current`
// never did, so the caller that offers candidates lists the pin group itself.
func (app *App) currentPinPlace(opts commonOpts) (placeChoice, int) {
	cwd, err := cwdAbs()
	if err != nil {
		return placeChoice{}, finish(opts, err)
	}
	binding := app.governingBindingAt(cwd)
	if binding == nil {
		return placeChoice{kind: choiceNone, governs: cwd}, constants.ExitOK
	}
	rows, err := app.pinGroupPlaces(binding)
	if err != nil {
		return placeChoice{}, finish(opts, err)
	}
	for _, row := range rows {
		if samePath(row.Path, binding.dir) {
			return placeChoice{kind: choiceOne, row: row}, constants.ExitOK
		}
	}
	// Bound by a kae older than the breadcrumb, so not in the listing: no number.
	return placeChoice{kind: choiceOne, row: placeRow{
		Group: constants.PlaceGroupPin, Kind: constants.PlaceKindBoundDirectory, Path: binding.dir,
		Exists: true, InEffect: true, Source: constants.PlaceSourcePin, Mode: binding.info.Mode,
	}}, constants.ExitOK
}

// selectPlace applies --at or --current and a level selector to the rows `kae ls
// <target>` lists, with pickPlace's result contract.
func selectPlace(app *App, opts commonOpts, req lsRequest, rows []placeRow) (placeChoice, int) {
	if req.at != 0 {
		if req.at > len(rows) {
			return placeChoice{}, usageError("kae ls %s lists %d place(s); there is no place %d", req.target, len(rows), req.at)
		}
		return placeChoice{kind: choiceOne, row: rows[req.at-1]}, constants.ExitOK
	}
	var matches []placeRow
	switch {
	case constants.IsTool(req.target) && projectLevelName(req.target) == "":
		return placeChoice{}, finish(opts, errNoPlaces(req.target))
	case constants.IsTool(req.target) && req.level == "":
		matches = rows[:1] // the effective user level is always first
	case req.level == constants.PlaceKindProject:
		// The nearest effective ancestor level: the first in list order.
		matches = app.placesOfLevel(req.target, rows, req.level)
		if len(matches) > 1 {
			matches = matches[:1]
		}
	case req.level != "":
		matches = app.placesOfLevel(req.target, rows, req.level)
	default: // repo, kae
		matches = rows
	}
	switch len(matches) {
	case 0:
		return placeChoice{kind: choiceNone, candidates: rows}, constants.ExitOK
	case 1:
		return placeChoice{kind: choiceOne, row: matches[0]}, constants.ExitOK
	}
	return placeChoice{kind: choiceSeveral, candidates: matches}, constants.ExitOK
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
