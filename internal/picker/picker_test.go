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
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fixture is two groups and a third: a project root with its .claude/ row
// beneath (items 2 and 3), then codex and kae.
func fixture() []Item {
	return []Item{
		{Kind: Heading, Label: "claude", Parent: -1}, // 0
		{Label: "~/.claude", Detail: "user", Note: "#1", Extra: "main", Value: "/h/.claude", Parent: -1, Filter: "~/.claude /h/.claude user claude"},                                         // 1
		{Label: "~/work/repo", Detail: "project root", Note: "#2", Value: "/h/work/repo", Parent: -1, Filter: "~/work/repo /h/work/repo project root claude"},                                // 2
		{Label: "~/work/repo/.claude", Detail: "project", Note: "#2", Value: "/h/work/repo/.claude", Depth: 1, Parent: 2, Filter: "~/work/repo/.claude /h/work/repo/.claude project claude"}, // 3
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
	eq(t, "filter 'project  claude'", visible(m), []string{"claude", "~/work/repo", "~/work/repo/.claude"})
	// Only the child matches ("/h/work/repo/.claude" is in its filter text alone):
	// its root row stays above it.
	m = New(fixture(), Options{NoColor: true})
	m = press(m, typed("repo/.claude")...)
	eq(t, "child only", visible(m), []string{"claude", "~/work/repo", "~/work/repo/.claude"})
	if m.cursor != 2 {
		t.Fatalf("cursor = %d, want the kept parent row 2", m.cursor)
	}
	// A matching parent does not pull its children in.
	m = New(fixture(), Options{NoColor: true})
	m = press(m, typed("root")...)
	eq(t, "parent only", visible(m), []string{"claude", "~/work/repo"})
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
		{"~/work/very/long/path/.claude", 12, "…th/.claude"},
		{"~/日本語のディレクトリ/.claude", 12, "…リ/.claude"},
	} {
		got := fitLeft(tc.in, tc.w)
		if runewidth.StringWidth(got) > tc.w || got != tc.want {
			t.Fatalf("fitLeft(%q, %d) = %q (width %d), want %q", tc.in, tc.w, got, runewidth.StringWidth(got), tc.want)
		}
	}
	m := press(New([]Item{
		{Kind: Heading, Label: "g", Parent: -1},
		{Label: "~/very/long/directory/name/that/does/not/fit/.claude", Detail: "project", Note: "#3", Value: "x", Parent: -1, Filter: "x"},
	}, Options{NoColor: true}), tea.WindowSizeMsg{Width: 40, Height: 24})
	for _, line := range strings.Split(m.View().Content, "\n") {
		if runewidth.StringWidth(line) > 40 {
			t.Fatalf("line wider than the terminal: %q", line)
		}
	}
	if !strings.Contains(m.View().Content, "> …at/does/not/fit/.claude  project  #3") {
		t.Fatalf("the path's tail and the columns must survive:\n%s", m.View().Content)
	}
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
