package picker

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-runewidth"

	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// The expected widths and goldens read ambiguous characters as narrow, the
// renderer's default, even when the caller's RUNEWIDTH_EASTASIAN says wide.
//
// It also pins English: the picker's text is human output, and the developer's
// locale must not reach an English assertion (docs/VALIDATION.md § Output
// language in tests).
func TestMain(m *testing.M) {
	cells = newCells("")
	if err := l10ntest.PinEnglish(); err != nil {
		fmt.Fprintf(os.Stderr, "picker tests: cannot pin English: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// fixture is two groups and a third: a project root with its .claude/ row
// beneath (items 2 and 3), then codex and kae.
func fixture() []Item {
	return []Item{
		{Kind: Heading, Label: "claude", Parent: -1}, // 0
		{Label: "~/.claude", Detail: "user", Note: "#1", Extra: "main", Value: "/h/.claude", Parent: -1, Filter: "~/.claude /h/.claude user claude"}, // 1
		{Label: "~/work/repo", Detail: "project", Note: "#2", Value: "/h/work/repo", Parent: -1, Filter: "~/work/repo /h/work/repo project claude"},  // 2
		{Label: ".claude", Value: "/h/work/repo/.claude", Depth: 1, Parent: 2, Filter: "~/work/repo/.claude /h/work/repo/.claude project claude"},    // 3
		{Kind: Heading, Label: "codex", Parent: -1}, // 4
		{Label: "~/.codex", Detail: "user", Note: "#1", Value: "/h/.codex", Parent: -1, Filter: "~/.codex /h/.codex user codex"}, // 5
		{Kind: Heading, Label: "kae", Parent: -1}, // 6
		{Label: "~/.config/kagikae", Detail: "kae config", Note: "#1", Value: "/h/.config/kagikae", Parent: -1, Filter: "~/.config/kagikae /h/.config/kagikae kae config kae"}, // 7
	}
}

func press(m Model, msgs ...tea.Msg) Model {
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func code(c rune) tea.Msg { return tea.KeyPressMsg{Code: c} }

func ctrl(c rune) tea.Msg { return tea.KeyPressMsg{Code: c, Mod: tea.ModCtrl} }

func typed(s string) []tea.Msg {
	var msgs []tea.Msg
	for _, r := range s {
		msgs = append(msgs, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return msgs
}

// visible is the labels drawn, headings included, in order.
func visible(m Model) []string {
	var out []string
	for _, i := range m.vis {
		out = append(out, m.items[i].Label)
	}
	return out
}

func eq(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%s = %q, want %q", what, got, want)
	}
}

func TestCursorStartsOnTheFirstRow(t *testing.T) {
	m := New(fixture(), Options{NoColor: true})
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 (headings are not selectable)", m.cursor)
	}
}

func TestNavigationSkipsHeadingsAndDoesNotWrap(t *testing.T) {
	m := New(fixture(), Options{NoColor: true})
	var path []int
	for range 6 {
		path = append(path, m.cursor)
		m = press(m, code(tea.KeyDown))
	}
	if fmt.Sprint(path) != "[1 2 3 5 7 7]" {
		t.Fatalf("Down from the top visited %v, want [1 2 3 5 7 7]: headings skipped, stopping at the last row", path)
	}
	path = nil
	for range 6 {
		path = append(path, m.cursor)
		m = press(m, code(tea.KeyUp))
	}
	if fmt.Sprint(path) != "[7 5 3 2 1 1]" {
		t.Fatalf("Up from the bottom visited %v, want [7 5 3 2 1 1]", path)
	}
	// Ctrl-N and Ctrl-P are Down and Up; Home and End jump; PgDn and PgUp move a page.
	m = press(m, ctrl('n'), ctrl('n'))
	if m.cursor != 3 {
		t.Fatalf("Ctrl-N twice: cursor %d, want 3", m.cursor)
	}
	m = press(m, ctrl('p'))
	if m.cursor != 2 {
		t.Fatalf("Ctrl-P: cursor %d, want 2", m.cursor)
	}
	m = press(m, code(tea.KeyEnd))
	if m.cursor != 7 {
		t.Fatalf("End: cursor %d, want 7", m.cursor)
	}
	m = press(m, code(tea.KeyHome))
	if m.cursor != 1 {
		t.Fatalf("Home: cursor %d, want 1", m.cursor)
	}
	m = press(m, code(tea.KeyPgDown))
	if m.cursor != 7 {
		t.Fatalf("PgDn over a list shorter than a page: cursor %d, want 7", m.cursor)
	}
	m = press(m, code(tea.KeyPgUp))
	if m.cursor != 1 {
		t.Fatalf("PgUp: cursor %d, want 1", m.cursor)
	}
}

func TestPageKeysMoveByTheDrawnHeight(t *testing.T) {
	var items []Item
	items = append(items, Item{Kind: Heading, Label: "g", Parent: -1})
	for i := range 30 {
		items = append(items, Item{Label: fmt.Sprintf("row%02d", i), Value: fmt.Sprint(i), Parent: -1, Filter: fmt.Sprintf("row%02d", i)})
	}
	m := press(New(items, Options{NoColor: true}), tea.WindowSizeMsg{Width: 60, Height: 10})
	if got := m.bodyHeight(); got != 6 {
		t.Fatalf("body height at 10 rows = %d, want 6 (height-2 for the picker, less the filter and hint lines)", got)
	}
	m = press(m, code(tea.KeyPgDown))
	if m.cursor != 1+6 {
		t.Fatalf("PgDn moved to item %d, want 7 (six rows down)", m.cursor)
	}
	m = press(m, code(tea.KeyEnd))
	view := m.View().Content
	if n := strings.Count(view, "\n") + 1; n > 8 {
		t.Fatalf("the picker drew %d lines at height 10, want at most 8:\n%s", n, view)
	}
	if !strings.Contains(view, "> row29") {
		t.Fatalf("the cursor row is not drawn after End:\n%s", view)
	}
	m = press(m, code(tea.KeyHome))
	if view := m.View().Content; !strings.Contains(view, "g\n> row00") {
		t.Fatalf("at the top the heading above the first row is drawn:\n%s", view)
	}
}

func TestFilterNarrowsAndKeepsTheParent(t *testing.T) {
	m := New(fixture(), Options{NoColor: true})
	m = press(m, typed("CODEX")...) // case-insensitive
	eq(t, "filter CODEX", visible(m), []string{"codex", "~/.codex"})
	if m.cursor != 5 {
		t.Fatalf("cursor = %d after filtering, want the first visible row 5", m.cursor)
	}
	// Every term must match, in any order; a group with no match hides its heading.
	m = New(fixture(), Options{NoColor: true})
	m = press(m, typed("project  claude")...)
	eq(t, "filter 'project  claude'", visible(m), []string{"claude", "~/work/repo", ".claude"})
	// Only the child matches ("repo/.claude" is in its filter text alone): its root
	// row stays above it for context, and the cursor lands on the child, so Enter
	// reaches the level that matched.
	m = New(fixture(), Options{NoColor: true})
	m = press(m, typed("repo/.claude")...)
	eq(t, "child only", visible(m), []string{"claude", "~/work/repo", ".claude"})
	if m.cursor != 3 {
		t.Fatalf("cursor = %d, want the matching child 3, not its kept parent", m.cursor)
	}
	m = press(m, code(tea.KeyEnter))
	if value, _, _ := m.Result(); value != "/h/work/repo/.claude" {
		t.Fatalf("Enter chose %q, want the matching level", value)
	}
	// A matching parent does not pull its children in.
	m = press(New([]Item{
		{Kind: Heading, Label: "g", Parent: -1},
		{Label: "root", Value: "r", Parent: -1, Filter: "alpha"},
		{Label: "child", Value: "c", Depth: 1, Parent: 1, Filter: "beta"},
	}, Options{NoColor: true}), typed("alpha")...)
	eq(t, "parent only", visible(m), []string{"g", "root"})
	// q and / are text, not commands.
	m = press(New(fixture(), Options{NoColor: true}), typed("q/")...)
	if m.input.Value() != "q/" {
		t.Fatalf("filter = %q, want q/", m.input.Value())
	}
	if _, _, done := m.Result(); done {
		t.Fatal("q ended the picker")
	}
	// Backspace and Ctrl-U edit the filter.
	m = press(New(fixture(), Options{NoColor: true}), typed("codexx")...)
	eq(t, "no match", visible(m), nil)
	m = press(m, code(tea.KeyBackspace))
	eq(t, "after Backspace", visible(m), []string{"codex", "~/.codex"})
	m = press(m, ctrl('u'))
	if m.input.Value() != "" || len(m.vis) != len(fixture()) {
		t.Fatalf("Ctrl-U left filter %q with %d items visible", m.input.Value(), len(m.vis))
	}
}

func TestEscClearsTheFilterThenCancels(t *testing.T) {
	m := press(New(fixture(), Options{NoColor: true}), typed("codex")...)
	m = press(m, code(tea.KeyEscape))
	if _, _, done := m.Result(); done || m.input.Value() != "" || len(m.vis) != len(fixture()) {
		t.Fatalf("first Esc: done=%v filter=%q visible=%d; want the filter cleared and the picker open", done, m.input.Value(), len(m.vis))
	}
	m = press(m, code(tea.KeyEscape))
	if value, cancelled, done := m.Result(); !done || !cancelled || value != "" {
		t.Fatalf("second Esc: %q %v %v, want cancelled", value, cancelled, done)
	}
	if got := m.View().Content; got != "" {
		t.Fatalf("a finished picker draws %q, want nothing so that it erases itself", got)
	}
}

func TestCtrlCCancelsEvenWithAFilter(t *testing.T) {
	m := press(New(fixture(), Options{NoColor: true}), append(typed("codex"), ctrl('c'))...)
	if value, cancelled, done := m.Result(); !done || !cancelled || value != "" {
		t.Fatalf("Ctrl-C: %q %v %v, want cancelled", value, cancelled, done)
	}
	m = press(New(fixture(), Options{NoColor: true}), tea.InterruptMsg{})
	if _, cancelled, done := m.Result(); !done || !cancelled {
		t.Fatal("an interrupt signal must cancel")
	}
}

func TestEnterChoosesTheCursorRowsValue(t *testing.T) {
	m := press(New(fixture(), Options{NoColor: true}), code(tea.KeyDown), code(tea.KeyDown), code(tea.KeyEnter))
	if value, cancelled, done := m.Result(); !done || cancelled || value != "/h/work/repo/.claude" {
		t.Fatalf("Enter on the third row: %q %v %v", value, cancelled, done)
	}
	m = press(New(fixture(), Options{NoColor: true}), append(typed("kagikae"), code(tea.KeyEnter))...)
	if value, _, done := m.Result(); !done || value != "/h/.config/kagikae" {
		t.Fatalf("Enter after filtering: %q %v", value, done)
	}
}

func TestEmptyDataAndEmptyResultDoNothing(t *testing.T) {
	for name, m := range map[string]Model{
		"no items":  New(nil, Options{NoColor: true}),
		"no result": press(New(fixture(), Options{NoColor: true}), typed("zzz")...),
	} {
		t.Run(name, func(t *testing.T) {
			if m.cursor != -1 {
				t.Fatalf("cursor = %d, want -1", m.cursor)
			}
			m = press(m, code(tea.KeyDown), code(tea.KeyUp), code(tea.KeyEnd), code(tea.KeyPgDown), code(tea.KeyEnter))
			if _, _, done := m.Result(); done {
				t.Fatal("Enter with nothing to choose ended the picker")
			}
			if view := m.View().Content; !strings.Contains(view, noMatch) {
				t.Fatalf("view lacks %q:\n%s", noMatch, view)
			}
			if got := press(m, code(tea.KeyEscape)); func() bool { _, c, d := got.Result(); return c && d }() == (name == "no result") {
				// no result: the first Esc clears the filter; no items: it cancels.
				t.Fatalf("Esc on %s: unexpected outcome", name)
			}
		})
	}
}

func TestLongPathsAreCutFromTheLeftByDisplayWidth(t *testing.T) {
	for _, tc := range []struct {
		in   string
		w    int
		want string
	}{
		{"~/short", 20, "~/short"},
		{"~/work/very/long/path/.claude", 12, "…ath/.claude"},
		{"~/日本語のディレクトリ/.claude", 12, "…リ/.claude"},
	} {
		got := fitLeft(tc.in, tc.w)
		if cells.StringWidth(got) > tc.w || got != tc.want {
			t.Fatalf("fitLeft(%q, %d) = %q (width %d), want %q", tc.in, tc.w, got, cells.StringWidth(got), tc.want)
		}
	}
	if got := fitView(t, longPath(), cells).Content; !strings.Contains(got, "> …hat/does/not/fit/.claude  project  #3") {
		t.Fatalf("the path's tail and the columns must survive:\n%s", got)
	}
}

// The locale must not change the width, as the renderer ignores it; only
// RUNEWIDTH_EASTASIAN widens ambiguous characters, and then rows still fit.
func TestAmbiguousWidthFollowsTheRenderer(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want int
	}{{"", 1}, {"0", 1}, {"junk", 1}, {"1", 2}, {"true", 2}} {
		if got := newCells(tc.env).StringWidth("…"); got != tc.want {
			t.Errorf("RUNEWIDTH_EASTASIAN=%q: width of … = %d, want %d", tc.env, got, tc.want)
		}
	}

	t.Run("a CJK locale stays narrow", func(t *testing.T) {
		runewidthDefault(t, true)
		useCells(t, newCells(""))
		if got := cells.StringWidth("…"); got != 1 {
			t.Errorf("a CJK locale widened … to %d", got)
		}
		if got := fitView(t, longPath(), cells).Content; !strings.Contains(got, "> …hat/does/not/fit/.claude  project  #3") {
			t.Errorf("a CJK locale changed the narrow layout:\n%s", got)
		}
	})

	t.Run("RUNEWIDTH_EASTASIAN widens and rows still fit", func(t *testing.T) {
		// runewidth's default reads narrow, so a call site measuring with it
		// instead of cells draws a line too wide.
		runewidthDefault(t, false)
		useCells(t, newCells("1"))
		if got := fitView(t, longPath(), cells).Content; !strings.Contains(got, "> …at/does/not/fit/.claude  project  #3") {
			t.Errorf("the cut must leave room for the wide …:\n%s", got)
		}
		fitView(t, []Item{
			{Kind: Heading, Label: strings.Repeat("①…", 25), Parent: -1},
			{Label: "~/①/…/" + strings.Repeat("②", 30), Detail: "…project", Note: "#①", Value: "x", Parent: -1, Filter: "x"},
			{Label: "~/①…", Detail: "①", Note: "#…", Value: "y", Parent: -1, Filter: "y"},
			// 25 columns narrow and 30 wide: fits beside the 13 of its columns only
			// when the ambiguous characters are counted narrow.
			{Label: "~/①①①①①" + strings.Repeat("a", 18), Detail: "project", Note: "#3", Value: "z", Parent: -1, Filter: "z"},
		}, cells)
	})
}

// longPath is a row whose path cannot fit 40 columns beside its columns.
func longPath() []Item {
	return []Item{
		{Kind: Heading, Label: "g", Parent: -1},
		{Label: "~/very/long/directory/name/that/does/not/fit/.claude", Detail: "project", Note: "#3", Value: "x", Parent: -1, Filter: "x"},
	}
}

// runewidthDefault sets how runewidth's package default counts ambiguous
// characters for the rest of t. runewidth reads the locale into it once at init,
// so wide stands in for a CJK locale.
func runewidthDefault(t *testing.T, wide bool) {
	t.Helper()
	ea, def := runewidth.EastAsianWidth, runewidth.DefaultCondition.EastAsianWidth
	t.Cleanup(func() { runewidth.EastAsianWidth, runewidth.DefaultCondition.EastAsianWidth = ea, def })
	runewidth.EastAsianWidth, runewidth.DefaultCondition.EastAsianWidth = wide, wide
}

// useCells makes the picker measure with c for the rest of t.
func useCells(t *testing.T, c *runewidth.Condition) {
	t.Helper()
	saved := cells
	t.Cleanup(func() { cells = saved })
	cells = c
}

// fitView draws items 40 columns wide and fails if a line is wider under c.
func fitView(t *testing.T, items []Item, c *runewidth.Condition) tea.View {
	t.Helper()
	v := press(New(items, Options{NoColor: true}), tea.WindowSizeMsg{Width: 40, Height: 24}).View()
	for _, line := range strings.Split(v.Content, "\n") {
		if w := c.StringWidth(line); w > 40 {
			t.Errorf("line of %d columns: %q", w, line)
		}
	}
	return v
}

func TestNoColorDrawsNoEscapeSequences(t *testing.T) {
	m := press(New(fixture(), Options{NoColor: true}), typed("c")...)
	if strings.Contains(m.View().Content, "\x1b") {
		t.Fatalf("NoColor view has an escape sequence: %q", m.View().Content)
	}
	if colored := New(fixture(), Options{}).View().Content; !strings.Contains(colored, "\x1b") {
		t.Fatalf("a colored view has no styling, so the NoColor check above proves nothing: %q", colored)
	}
}

func TestViewGolden(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 36}} {
		name := fmt.Sprintf("view_%dx%d.golden", size[0], size[1])
		t.Run(name, func(t *testing.T) {
			m := press(New(goldenItems(), Options{NoColor: true}), tea.WindowSizeMsg{Width: size[0], Height: size[1]}, code(tea.KeyDown))
			got := m.View().Content + "\n"
			path := filepath.Join("testdata", name)
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run go test ./internal/picker -update to create it)", err)
			}
			if string(want) != got {
				t.Fatalf("view differs from %s (run with -update to accept):\n--- got\n%s--- want\n%s", path, got, want)
			}
		})
	}
}

// goldenItems is the fixture with one path too long for 80 columns.
func goldenItems() []Item {
	items := fixture()
	items = append(items, Item{Kind: Heading, Label: "repo", Parent: -1},
		Item{Label: "~/work/a/very/deeply/nested/monorepo/packages/service-name/.claude", Detail: "below", Note: "#4", Extra: "side", Value: "/h/deep", Parent: -1, Filter: "deep"})
	return items
}

// A terminal's paste (bracketed paste) filters like typing, and Enter then
// chooses a row the filter allows.
func TestPasteFilters(t *testing.T) {
	m := press(New(fixture(), Options{NoColor: true}), tea.PasteMsg{Content: "CODEX"})
	if m.input.Value() != "CODEX" {
		t.Fatalf("filter = %q after a paste", m.input.Value())
	}
	eq(t, "after paste", visible(m), []string{"codex", "~/.codex"})
	m = press(m, code(tea.KeyEnter))
	if value, _, done := m.Result(); !done || value != "/h/.codex" {
		t.Fatalf("Enter after a paste chose %q (done %v), want the codex row", value, done)
	}
	// Ctrl-V would run a clipboard program; it does nothing here.
	m = New(fixture(), Options{NoColor: true})
	next, cmd := m.Update(ctrl('v'))
	if cmd != nil || next.(Model).input.Value() != "" {
		t.Fatalf("Ctrl-V reached the clipboard path: cmd %v, filter %q", cmd, next.(Model).input.Value())
	}
}

// A row kept only as the parent of a match is drawn dimmed.
func TestKeptParentIsDimmed(t *testing.T) {
	m := press(New(fixture(), Options{}), typed("repo/.claude")...)
	var parent, child string
	for _, line := range strings.Split(m.View().Content, "\n") {
		switch {
		case strings.Contains(line, "~/work/repo"):
			parent = line
		case strings.Contains(line, ".claude"):
			child = line
		}
	}
	if !strings.Contains(parent, "\x1b[2m~/work/repo") || strings.Contains(child, "\x1b[2m.claude") {
		t.Fatalf("parent %q child %q: only the kept parent is dimmed", parent, child)
	}
}

// The picker stays within height-2 lines when the terminal allows, and below
// that keeps the filter line and one row. (A terminal of size 0 never reaches
// the picker: textui.Open refuses it.)
func TestTinyTerminals(t *testing.T) {
	lines := func(m Model) int { return strings.Count(m.View().Content, "\n") + 1 }
	for _, tc := range []struct{ w, h, max, min int }{
		{80, 24, 22, 2},
		{80, 10, 8, 2},
		{80, 6, 4, 2},
		{80, 5, 3, 2},
		{80, 4, 2, 2},
		{80, 3, 2, 2},
		{80, 1, 2, 2},
		{1, 1, 2, 2},
		{0, 0, 22, 2},
	} {
		m := press(New(fixture(), Options{NoColor: true}), tea.WindowSizeMsg{Width: tc.w, Height: tc.h})
		if n := lines(m); n > tc.max || n < tc.min {
			t.Fatalf("%dx%d drew %d lines, want %d..%d:\n%s", tc.w, tc.h, n, tc.min, tc.max, m.View().Content)
		}
		if !strings.Contains(m.View().Content, "\n") || m.View().Content == "" {
			t.Fatalf("%dx%d drew no row", tc.w, tc.h)
		}
	}
}

// With the cursor on a level line, its root comes into view with it when the
// window has room for both.
func TestChildBringsItsRootIntoView(t *testing.T) {
	items := []Item{{Kind: Heading, Label: "g", Parent: -1}}
	for i := range 20 {
		items = append(items, Item{Label: fmt.Sprintf("row%02d", i), Value: "v", Parent: -1, Filter: "x"})
	}
	items = append(items,
		Item{Label: "ROOT", Value: "r", Parent: -1, Filter: "x"},
		Item{Label: "lvl", Value: "l", Depth: 1, Parent: 21, Filter: "x"})
	m := press(New(items, Options{NoColor: true}), tea.WindowSizeMsg{Width: 60, Height: 8}) // 4 list lines
	m = press(m, code(tea.KeyEnd))
	if view := m.View().Content; !strings.Contains(view, "  ROOT\n>   lvl") {
		t.Fatalf("the level line has lost its root:\n%s", view)
	}
	// Scrolling up past it and back down keeps the rule.
	m = press(m, code(tea.KeyUp), code(tea.KeyUp), code(tea.KeyDown), code(tea.KeyDown))
	if view := m.View().Content; !strings.Contains(view, "  ROOT\n>   lvl") {
		t.Fatalf("after moving, the level line has lost its root:\n%s", view)
	}
}
