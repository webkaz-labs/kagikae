package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/picker"
	"github.com/webkaz-labs/kagikae/internal/textui"
)

// A request for `kae open` or `kae cd` that names no single place has a
// candidate set: the places it could mean, under a heading per group. One set
// feeds both views of it, the stderr list (offerCandidates) and, on a terminal,
// the picker, so what each entry point offers is decided once, here.

// candidateGroup is one heading and the places beneath it, in `kae ls` order
// with `kae ls`'s numbers.
type candidateGroup struct {
	Heading string
	Rows    []placeRow
	// Relevant marks a group holding a place that bears on the current
	// directory, which the picker lists before the others.
	Relevant bool
}

// placeCandidates is a request's candidate set. groups hold only places whose
// directory exists (choosing a missing one exits 7) and only non-empty groups;
// found counts the places before missing ones were left out, for the request
// that ends up with none.
type placeCandidates struct {
	reason func(listed int) string
	found  int
	groups []candidateGroup
}

// newPlaceCandidates builds the set for req. With --root a place without a root
// is not a candidate: the request would reach its root.
func newPlaceCandidates(req lsRequest, reason func(listed int) string, groups ...candidateGroup) placeCandidates {
	set := placeCandidates{reason: reason}
	for _, g := range groups {
		var rows []placeRow
		for _, row := range g.Rows {
			if req.root && row.Root == "" {
				continue
			}
			set.found++
			if row.Exists {
				rows = append(rows, row)
			}
		}
		if len(rows) > 0 {
			set.groups = append(set.groups, candidateGroup{Heading: g.Heading, Rows: rows, Relevant: g.Relevant})
		}
	}
	return set
}

// listed is the number of places in the set.
func (s placeCandidates) listed() int {
	n := 0
	for _, g := range s.groups {
		n += len(g.Rows)
	}
	return n
}

// allPlaceCandidates is open or cd with no target: every place bare `kae ls`
// lists is a candidate, group by group. A group is relevant when it holds a
// place that bears on the current directory: a governing binding, a tool
// `kae ls` shows, the repository.
func (app *App) allPlaceCandidates(ctx context.Context, req lsRequest) placeCandidates {
	g := app.collectPlaceGroups(ctx, app.readState(), warnGroupOnce())
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
	groups = append(groups,
		candidateGroup{Heading: constants.PlaceGroupRepo, Rows: g.repo, Relevant: true},
		candidateGroup{Heading: constants.PlaceGroupKae, Rows: g.kae})
	return newPlaceCandidates(req, func(int) string { return fmt.Sprintf("kae %s needs a target", req.verb) }, groups...)
}

// pickCandidates is `--pick`: the places `kae ls <target>` lists (no target: all
// places), and with a level selector every place of that level rather than the
// first one a plain request would choose.
func (app *App) pickCandidates(ctx context.Context, opts commonOpts, req lsRequest) (*placeCandidates, int) {
	words := strings.TrimSpace(requestWords(req, false))
	reason := func(n int) string {
		if n == 0 {
			return fmt.Sprintf("kae %s %s --pick lists no place", req.verb, words)
		}
		return fmt.Sprintf("kae %s %s --pick needs a terminal to open the picker; it lists %d place(s)", req.verb, words, n)
	}
	var groups []candidateGroup
	switch {
	case req.target == "" && req.level == "":
		set := app.allPlaceCandidates(ctx, req)
		set.reason = reason
		return &set, constants.ExitOK
	case req.target == "":
		pc, err := app.newPlaceContext(ctx, nil)
		if err != nil {
			return nil, finish(opts, err)
		}
		tools := boundPlaceTools(pc)
		if len(tools) == 0 {
			tools = placeTools()
		}
		if groups, err = app.levelGroups(ctx, pc, tools, req.level); err != nil {
			return nil, finish(opts, err)
		}
	case constants.IsTool(req.target):
		if projectLevelName(req.target) == "" {
			return nil, finish(opts, errf(constants.ExitNotFound, "kae resolves no places for %s", req.target))
		}
		pc, err := app.newPlaceContext(ctx, req.explicit)
		if err != nil {
			return nil, finish(opts, err)
		}
		var rows []placeRow
		if req.level == "" {
			rows, err = app.toolPlaces(ctx, pc, req.target)
		} else {
			rows, err = app.levelPlaces(ctx, pc, req.target, req.level)
		}
		if err != nil {
			return nil, finish(opts, err)
		}
		groups = []candidateGroup{{Heading: req.target, Rows: rows}}
	default:
		rows, err := app.groupPlaces(ctx, req.target, req.explicit)
		if err != nil {
			return nil, finish(opts, err)
		}
		groups = []candidateGroup{{Heading: req.target, Rows: rows}}
	}
	set := newPlaceCandidates(req, reason, groups...)
	return &set, constants.ExitOK
}

// levelPlaces is every place of one level of tool, with `kae ls <tool>`'s
// numbers. The ancestor project levels come first in list order, so they are
// found without looking for the below levels.
func (app *App) levelPlaces(ctx context.Context, pc *placeContext, tool, level string) ([]placeRow, error) {
	var rows []placeRow
	var err error
	if level == constants.PlaceKindProject {
		rows, err = app.leadingToolPlaces(pc, tool)
	} else {
		rows, err = app.toolPlaces(ctx, pc, tool)
	}
	if err != nil {
		return nil, err
	}
	return app.placesOfLevel(tool, rows, level), nil
}

// placesOfLevel picks the rows of a tool's list that a level selector names:
// every effective ancestor project level, every level below, or the real home.
func (app *App) placesOfLevel(tool string, rows []placeRow, level string) []placeRow {
	var matches []placeRow
	for _, row := range rows {
		switch level {
		case constants.PlaceKindProject, constants.PlaceKindBelow:
			if row.Kind == level {
				matches = append(matches, row)
			}
		case constants.PlaceKindHome:
			if (row.Kind == constants.PlaceKindHome || row.Kind == constants.PlaceKindUser) && samePath(row.Path, app.realToolHome(tool)) {
				return []placeRow{row}
			}
		}
	}
	return matches
}

// offerCandidates puts the set to the user: the picker when a terminal is
// there, else the list on stderr under a usage error (listCandidates). The
// picker opens even over one candidate; a set with nothing in it is not_found
// either way. It returns the chosen path, or ExitCancelled when the user backs out.
func (app *App) offerCandidates(ctx context.Context, opts commonOpts, req lsRequest, set placeCandidates) (string, int) {
	if set.listed() == 0 {
		return "", listCandidates(opts, req, set)
	}
	tty, ok := app.terminal()
	if !ok {
		return "", listCandidates(opts, req, set)
	}
	defer tty.Close()
	value, cancelled, err := app.choose(ctx, tty, placePickerItems(app, req, set), picker.Options{NoColor: noColorRequested(opts.NoColor)})
	switch {
	case err != nil:
		return "", finish(opts, errf(constants.ExitError, "%v", err))
	case cancelled:
		return "", constants.ExitCancelled
	}
	return value, constants.ExitOK
}

// choose runs the picker, through the App's seam when a test set one.
func (app *App) choose(ctx context.Context, tty *textui.Terminal, items []picker.Item, opts picker.Options) (string, bool, error) {
	if app.pick != nil {
		return app.pick(ctx, items, opts)
	}
	return picker.Run(ctx, tty.TTY, tty.TTY, items, opts)
}

// listCandidates lists, one per line, the command that reaches each candidate,
// under a usage error whose reason is given the number listed. With none listed
// it is not_found, and the reason is given the number found instead.
func listCandidates(opts commonOpts, req lsRequest, set placeCandidates) int {
	var lines []string
	for _, g := range set.groups {
		for _, row := range g.Rows {
			words := row.Group
			if req.target == row.Group {
				words = requestWords(req, true)
			}
			cmd := fmt.Sprintf("kae %s %s --at %d", req.verb, words, row.Number)
			path := row.Path
			if req.root {
				cmd += " --root"
				path = row.Root // what the command reaches
			}
			lines = append(lines, cmd+"  "+path)
		}
	}
	if len(lines) == 0 {
		return finish(opts, errf(constants.ExitNotFound, "%s, and no existing place to choose", set.reason(set.found)))
	}
	return reportChoices(set.reason(len(lines))+"; choose one", lines)
}

// reportChoices prints a usage error and its candidates on stderr.
func reportChoices(reason string, lines []string) int {
	var b strings.Builder
	b.WriteString(reason + ":")
	for _, line := range lines {
		b.WriteString("\n  " + line)
	}
	return usageError("%s", b.String())
}
