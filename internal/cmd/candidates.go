package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
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
			set.groups = append(set.groups, candidateGroup{Heading: g.Heading, Rows: rows})
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
// lists is a candidate, group by group.
func (app *App) allPlaceCandidates(ctx context.Context, req lsRequest) placeCandidates {
	g := app.collectPlaceGroups(ctx, app.readState(), warnGroupOnce())
	var groups []candidateGroup
	if g.pins != nil {
		groups = append(groups, candidateGroup{Heading: constants.PlaceGroupPin, Rows: pinPlaces(g.pins.BoundDirectories)})
	}
	for _, tool := range g.tools {
		groups = append(groups, candidateGroup{Heading: tool, Rows: g.toolRows[tool]})
	}
	groups = append(groups,
		candidateGroup{Heading: constants.PlaceGroupRepo, Rows: g.repo},
		candidateGroup{Heading: constants.PlaceGroupKae, Rows: g.kae})
	return newPlaceCandidates(req, func(int) string { return fmt.Sprintf("kae %s needs a target", req.verb) }, groups...)
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

// offerCandidates puts the set to the user. Until the picker takes it on a
// terminal, that is the list on stderr under a usage error (listCandidates).
func (app *App) offerCandidates(opts commonOpts, req lsRequest, set placeCandidates) int {
	return listCandidates(opts, req, set)
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
