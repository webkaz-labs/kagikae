package cmd

import (
	"fmt"
	"slices"

	"github.com/webkaz-labs/kagikae/internal/picker"
)

// placePickerItems turns a candidate set into the picker's list: the groups
// holding a place that bears on the current directory first, each in `kae ls`
// order, then the rest. A project or below place is its root with the
// `.claude/` or `.codex/` place indented beneath it, both chosen for what they
// are (the root gives the root, the level its own path); with --root only the
// root lines, since the request reaches the root.
func placePickerItems(app *App, req lsRequest, set placeCandidates) []picker.Item {
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
				items = append(items, app.placePickerRow(g.Heading, row, row.Path, 0, -1))
			case req.root:
				items = append(items, app.placePickerRow(g.Heading, row, row.Root, 0, -1))
			default:
				parent := len(items)
				items = append(items, app.placePickerRow(g.Heading, row, row.Root, 0, -1),
					app.placePickerRow(g.Heading, row, row.Path, 1, parent))
			}
		}
	}
	return items
}

// placePickerRow is one selectable line for path, which is the place's own or
// its root. Filtering matches the displayed and the absolute path, the kind and
// the group.
func (app *App) placePickerRow(group string, row placeRow, path string, depth, parent int) picker.Item {
	display := app.displayPath(path)
	return picker.Item{
		Label:  display,
		Detail: row.Kind,
		Note:   fmt.Sprintf("#%d", row.Number),
		Extra:  row.Account,
		Value:  path,
		Depth:  depth,
		Parent: parent,
		Filter: display + " " + path + " " + row.Kind + " " + group,
	}
}
