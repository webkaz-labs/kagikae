// Package picker is an inline, filterable chooser drawn on a terminal.
//
// It knows nothing about what it lists: a caller hands it rows under group
// headings and gets back the chosen row's value. It draws below the cursor
// without taking over the screen and erases itself on exit, so a shell command
// that runs it leaves nothing behind. Output and input are injected: a command
// whose stdout is captured (`$(…)`) gives it the controlling terminal, never
// stdout.
//
// Keys: typing filters at once; Up, Down, Ctrl-P and Ctrl-N move over the
// selectable rows without wrapping, and PgUp, PgDn, Home and End jump; Enter
// chooses; Backspace and Ctrl-U edit the filter; Esc clears a filter and, with
// none, cancels; Ctrl-C cancels. Letters such as `q` and `/` are filter text: a
// chooser is typed into, a deliberate departure from the go-cli standard's
// routed-review keys.
package picker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/mattn/go-runewidth"
)

// Kind says whether an item is a group heading or a row that can be chosen.
type Kind int

const (
	// Row is an item the cursor can rest on and Enter can choose.
	Row Kind = iota
	// Heading names the group of rows after it, up to the next heading. It is
	// never selectable, and is hidden when none of its rows match the filter.
	Heading
)

// Item is one line of the list.
type Item struct {
	Kind Kind
	// Label is the row's main text, or a heading's text. A row's label is
	// shortened from the left (with "…") when the line does not fit.
	Label string
	// Detail, Note and Extra follow the label, in that order, separated by two
	// spaces. Note is dimmed.
	Detail, Note, Extra string
	// Value is returned when the row is chosen.
	Value string
	// Depth indents a row beneath its parent (0 or 1 in kae's use).
	Depth int
	// Parent is the index in the items of the row this row sits beneath, or -1.
	// A row that matches the filter keeps its parent visible.
	Parent int
	// Filter is the text the filter's terms are matched against, case-insensitively.
	Filter string
}

// Options tunes a picker.
type Options struct {
	// NoColor draws no color or other styling.
	NoColor bool
}

// noMatch is what an empty result says.
const noMatch = "no matching place"

// hint is the footer line.
const hint = "up/down move   enter choose   esc clear or cancel"

// Model is the picker's state. It is a Bubble Tea model, exported so a caller
// can test what it draws; use Run to show one.
type Model struct {
	items  []Item
	lower  []string // items' Filter, lower-cased
	color  bool
	input  textinput.Model
	width  int
	height int

	vis     []int  // indices of the visible items, in display order
	matched []bool // per item: a row that matches the filter itself (not only a kept parent)
	cursor  int    // index of the selected item, or -1 when no row is visible
	offset  int    // position in vis of the first drawn line

	done      bool
	cancelled bool
	value     string
}

// New builds a picker over items, sized 80x24 until a window size arrives. The
// cursor starts on the first row.
func New(items []Item, opts Options) Model {
	in := textinput.New()
	in.Prompt = "> "
	in.Placeholder = "type to filter"
	in.SetVirtualCursor(false)
	in.SetStyles(textinput.Styles{}) // plain: styling is the picker's, and only with color
	// Ctrl-V would run a clipboard program outside internal/runner; a terminal's
	// own paste arrives as a PasteMsg and is handled.
	in.KeyMap.Paste.SetEnabled(false)
	in.Focus()
	m := Model{items: items, color: !opts.NoColor, input: in, width: 80, height: 24}
	m.lower = make([]string, len(items))
	for i, it := range items {
		m.lower[i] = strings.ToLower(it.Filter)
	}
	m.resize(80, 24)
	m.refilter()
	return m
}

// Result is what the picker ended with: the chosen row's value, or cancelled.
// ok is false while it is still running.
func (m Model) Result() (value string, cancelled, ok bool) {
	return m.value, m.cancelled, m.done
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return textinput.Blink }

// resize takes a terminal size; a zero dimension means unknown and keeps the
// 80x24 default, so the picker never draws into nothing.
func (m *Model) resize(w, h int) {
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	m.width, m.height = w, h
	m.input.SetWidth(max(m.width-runewidth.StringWidth(m.input.Prompt)-1, 1))
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.done {
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		m.scroll()
		return m, nil
	case tea.InterruptMsg:
		return m.finish(true), tea.Quit
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m.edit(msg)
}

// edit gives msg (a typed key, a paste, a blink) to the filter field and
// re-filters whenever its value changed, whichever message changed it.
func (m Model) edit(msg tea.Msg) (tea.Model, tea.Cmd) {
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != before {
		m.refilter()
	}
	return m, cmd
}

func (m Model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m.finish(true), tea.Quit
	case "esc":
		if m.input.Value() != "" {
			m.input.Reset()
			m.refilter()
			return m, nil
		}
		return m.finish(true), tea.Quit
	case "enter":
		if m.cursor < 0 {
			return m, nil
		}
		m.value = m.items[m.cursor].Value
		return m.finish(false), tea.Quit
	case "up", "ctrl+p":
		m.move(-1)
	case "down", "ctrl+n":
		m.move(1)
	case "pgup":
		m.move(-m.bodyHeight())
	case "pgdown":
		m.move(m.bodyHeight())
	case "home":
		m.move(-len(m.items))
	case "end":
		m.move(len(m.items))
	default:
		return m.edit(msg)
	}
	return m, nil
}

func (m Model) finish(cancelled bool) Model {
	m.done, m.cancelled = true, cancelled
	if cancelled {
		m.value = ""
	}
	return m
}

// move puts the cursor n selectable rows from where it is (negative is up),
// stopping at the first or last row rather than wrapping.
func (m *Model) move(n int) {
	if m.cursor < 0 {
		return
	}
	at := -1
	var rows []int // positions in vis of the selectable rows
	for pos, i := range m.vis {
		if m.items[i].Kind == Row {
			if i == m.cursor {
				at = len(rows)
			}
			rows = append(rows, pos)
		}
	}
	if at < 0 || len(rows) == 0 {
		return
	}
	to := min(max(at+n, 0), len(rows)-1)
	m.cursor = m.vis[rows[to]]
	m.scroll()
}

// refilter recomputes the visible items from the filter's terms and puts the
// cursor on the first visible row.
func (m *Model) refilter() {
	terms := strings.Fields(strings.ToLower(m.input.Value()))
	matches := func(i int) bool {
		for _, t := range terms {
			if !strings.Contains(m.lower[i], t) {
				return false
			}
		}
		return true
	}
	show := make([]bool, len(m.items))
	m.matched = make([]bool, len(m.items))
	for i, it := range m.items {
		if it.Kind == Row && matches(i) {
			show[i], m.matched[i] = true, true
			if it.Parent >= 0 && it.Parent < len(m.items) {
				show[it.Parent] = true
			}
		}
	}
	m.vis = m.vis[:0]
	heading := -1 // the last heading seen, shown once one of its rows is
	for i, it := range m.items {
		if it.Kind == Heading {
			heading = i
			continue
		}
		if show[i] {
			if heading >= 0 {
				m.vis = append(m.vis, heading)
				heading = -1
			}
			m.vis = append(m.vis, i)
		}
	}
	// The cursor goes to the first row that matches itself: a parent kept for
	// context stays visible and selectable but is not where Enter lands.
	m.cursor, m.offset = -1, 0
	for _, i := range m.vis {
		if m.items[i].Kind == Row && m.matched[i] {
			m.cursor = i
			break
		}
	}
	m.scroll()
}

// budget is the most lines the picker draws: the terminal height minus 2, but
// never fewer than the filter line and one row.
func (m Model) budget() int { return max(m.height-2, 2) }

// showHint says whether the footer fits: it goes first when space is short.
func (m Model) showHint() bool { return m.budget() >= 3 }

// bodyHeight is how many list lines are drawn: the visible items, within the
// budget less the filter line and the hint, and at least one line (the
// empty-result message).
func (m Model) bodyHeight() int {
	overhead := 1
	if m.showHint() {
		overhead = 2
	}
	return max(min(len(m.vis), m.budget()-overhead), 1)
}

// scroll moves the window over the visible items so the cursor, and the heading
// above it when it is the first row of its group, is drawn.
func (m *Model) scroll() {
	bh := m.bodyHeight()
	pos := -1
	for p, i := range m.vis {
		if i == m.cursor {
			pos = p
			break
		}
	}
	if pos < 0 {
		m.offset = 0
		return
	}
	top := pos
	for top > 0 && m.items[m.vis[top-1]].Kind == Heading {
		top--
	}
	if top < m.offset {
		m.offset = top
	}
	if pos >= m.offset+bh {
		m.offset = pos - bh + 1
	}
	m.offset = min(max(m.offset, 0), max(len(m.vis)-bh, 0))
}

// View implements tea.Model. A finished picker draws nothing, which erases it.
func (m Model) View() tea.View {
	if m.done {
		return tea.NewView("")
	}
	lines := []string{strings.TrimRight(m.input.View(), " ")}
	if len(m.vis) == 0 {
		lines = append(lines, "  "+m.dim(noMatch))
	} else {
		end := min(m.offset+m.bodyHeight(), len(m.vis))
		for _, i := range m.vis[m.offset:end] {
			lines = append(lines, m.line(i))
		}
	}
	if m.showHint() {
		lines = append(lines, m.dim(runewidth.Truncate(hint, m.width, "")))
	}
	v := tea.NewView(strings.Join(lines, "\n"))
	v.Cursor = m.input.Cursor()
	return v
}

func (m Model) line(i int) string {
	it := m.items[i]
	if it.Kind == Heading {
		return m.style(lipgloss.NewStyle().Bold(true), runewidth.Truncate(it.Label, m.width, "…"))
	}
	marker := "  "
	if i == m.cursor {
		marker = "> "
	}
	prefix := marker + strings.Repeat("  ", it.Depth)
	type cell struct {
		text string
		dim  bool
	}
	var tail []cell
	tailWidth := 0
	for _, c := range []cell{{it.Detail, false}, {it.Note, true}, {it.Extra, false}} {
		if c.text != "" {
			tail = append(tail, c)
			tailWidth += 2 + runewidth.StringWidth(c.text)
		}
	}
	avail := m.width - runewidth.StringWidth(prefix)
	label := it.Label
	switch {
	case runewidth.StringWidth(label)+tailWidth <= avail:
	case avail-tailWidth >= 2:
		label = fitLeft(label, avail-tailWidth)
	default:
		label, tail = fitLeft(label, avail), nil // no room for the columns: the path wins
	}
	var b strings.Builder
	b.WriteString(prefix)
	switch {
	case i == m.cursor:
		b.WriteString(m.style(lipgloss.NewStyle().Bold(true), label))
	case !m.matched[i]:
		b.WriteString(m.dim(label)) // shown only as the parent of a matching row
	default:
		b.WriteString(label)
	}
	for _, c := range tail {
		b.WriteString("  ")
		if c.dim {
			b.WriteString(m.dim(c.text))
		} else {
			b.WriteString(c.text)
		}
	}
	return b.String()
}

// fitLeft shortens s from the left to w display cells, marking the cut with "…".
func fitLeft(s string, w int) string {
	if runewidth.StringWidth(s) <= w {
		return s
	}
	return runewidth.TruncatePrefix(s, w, "…")
}

func (m Model) style(s lipgloss.Style, text string) string {
	if !m.color {
		return text
	}
	return s.Render(text)
}

func (m Model) dim(text string) string { return m.style(lipgloss.NewStyle().Faint(true), text) }

// Run shows the picker over items, reading keys from in and drawing on out, and
// returns the chosen row's value or cancelled. It draws inline and leaves the
// screen as it found it. A non-nil error is a failure of the terminal or of ctx.
func Run(ctx context.Context, in io.Reader, out io.Writer, items []Item, opts Options) (value string, cancelled bool, err error) {
	programOpts := []tea.ProgramOption{tea.WithInput(in), tea.WithOutput(out), tea.WithContext(ctx)}
	if opts.NoColor {
		programOpts = append(programOpts, tea.WithColorProfile(colorprofile.NoTTY))
	}
	final, err := tea.NewProgram(New(items, opts), programOpts...).Run()
	if err != nil {
		return "", false, fmt.Errorf("picker: %w", err)
	}
	model, ok := final.(Model)
	if !ok {
		return "", false, errors.New("picker: unexpected final model")
	}
	value, cancelled, done := model.Result()
	if !done {
		return "", false, errors.New("picker: exited without a choice")
	}
	return value, cancelled, nil
}
