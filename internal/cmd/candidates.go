package cmd

import (
	"context"
	"fmt"
	"path/filepath"
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
	// none, when set, words the not_found of a set with nothing listed (given
	// found); otherwise reason(found) and "no existing place to choose" do.
	none   func(found int) string
	found  int
	groups []candidateGroup
}

// constReason is a reason that does not depend on the number listed.
func constReason(reason string) func(int) string { return func(int) string { return reason } }

// placeNoun is "place" for one and "places" otherwise.
func placeNoun(n int) string {
	if n == 1 {
		return "place"
	}
	return "places"
}

// noneMessage is the not_found message for a set with nothing listed.
func (s placeCandidates) noneMessage() string {
	if s.none != nil {
		return s.none(s.found)
	}
	return s.reason(s.found) + ", and no existing place to choose"
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
// lists is a candidate, group by group, in the order placeGroups.groups gives.
func (app *App) allPlaceCandidates(ctx context.Context, req lsRequest) placeCandidates {
	g := app.collectPlaceGroups(ctx, app.readState(), warnGroupOnce())
	return newPlaceCandidates(req, constReason(fmt.Sprintf("kae %s needs a target", req.verb)), g.groups()...)
}

// pickCandidates is `--pick`: the places `kae ls <target>` lists (no target: all
// places), and with a level selector every place of that level rather than the
// first one a plain request would choose.
func (app *App) pickCandidates(ctx context.Context, opts commonOpts, req lsRequest) (*placeCandidates, int) {
	words := slices.DeleteFunc([]string{"kae", req.verb, requestWords(req, false), "--pick"}, func(w string) bool { return w == "" })
	head := strings.Join(words, " ")
	reason := func(n int) string {
		return fmt.Sprintf("%s needs a terminal to open the picker; it lists %d %s", head, n, placeNoun(n))
	}
	none := func(found int) string {
		if found == 0 {
			return head + " lists no place"
		}
		return fmt.Sprintf("%s lists %d %s, and no existing place to choose", head, found, placeNoun(found))
	}
	var groups []candidateGroup
	switch {
	case req.target == "" && req.level == "":
		set := app.allPlaceCandidates(ctx, req)
		set.reason, set.none = reason, none
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
	set.none = none
	return &set, constants.ExitOK
}

// offerCandidates puts the set to the user: the picker when a terminal is
// there and the set has something in it, else the list on stderr under a usage
// error (listCandidates), or not_found for an empty set. The picker opens even
// over one candidate. It returns the chosen path, or ExitCancelled when the user
// backs out.
func (app *App) offerCandidates(ctx context.Context, opts commonOpts, req lsRequest, set placeCandidates) (string, int) {
	tty, ok := app.terminal()
	if !ok || set.listed() == 0 {
		tty.Close() // nil-safe
		return "", listCandidates(opts, req, set)
	}
	defer tty.Close()
	value, cancelled, err := app.choose(ctx, tty, app.placePickerItems(req, set), picker.Options{NoColor: noColorRequested(opts.NoColor)})
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
// it is not_found (noneMessage).
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
		return finish(opts, errf(constants.ExitNotFound, "%s", set.noneMessage()))
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

// placePickerItems turns a candidate set into the picker's list: the groups
// holding a place that bears on the current directory first, each in `kae ls`
// order, then the rest. A project or below place is its root line (kind, number,
// account) with its `.claude/` or `.codex/` directory indented beneath as a
// bare name, both chosen for what they are: the root gives the root, the level
// its own path. With --root only the root lines are shown, since the request
// reaches the root.
func (app *App) placePickerItems(req lsRequest, set placeCandidates) []picker.Item {
	groups := slices.Clone(set.groups)
	slices.SortStableFunc(groups, func(a, b candidateGroup) int {
		switch {
		case a.Relevant == b.Relevant:
			return 0
		case a.Relevant:
			return -1
		}
		return 1
	})
	var items []picker.Item
	for _, g := range groups {
		items = append(items, picker.Item{Kind: picker.Heading, Label: g.Heading, Parent: -1})
		for _, row := range g.Rows {
			switch {
			case row.Root == "":
				items = append(items, app.placePickerRow(g.Heading, row, row.Path, -1))
			case req.root:
				items = append(items, app.placePickerRow(g.Heading, row, row.Root, -1))
			default:
				parent := len(items)
				items = append(items, app.placePickerRow(g.Heading, row, row.Root, -1),
					app.placePickerRow(g.Heading, row, row.Path, parent))
			}
		}
	}
	return items
}

// placePickerRow is one selectable line for path, the place's own or its root.
// A line beneath a root (parent >= 0) is the level directory: its name alone,
// indented, since kind and number are its root line's. Filtering matches the
// displayed and the absolute path, the kind and the group on every line.
func (app *App) placePickerRow(group string, row placeRow, path string, parent int) picker.Item {
	display := app.displayPath(path)
	it := picker.Item{
		Label:  display,
		Detail: row.Kind,
		Note:   fmt.Sprintf("#%d", row.Number),
		Extra:  row.Account,
		Value:  path,
		Parent: parent,
		Filter: display + " " + path + " " + row.Kind + " " + group,
	}
	if parent >= 0 {
		it.Label, it.Detail, it.Note, it.Extra, it.Depth = filepath.Base(path), "", "", "", 1
	}
	return it
}
