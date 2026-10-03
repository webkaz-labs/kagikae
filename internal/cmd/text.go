package cmd

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/width"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// noColorRequested is --no-color or a non-empty NO_COLOR.
func noColorRequested(noColorFlag bool) bool {
	return noColorFlag || os.Getenv("NO_COLOR") != ""
}

// colorEnabled reports whether semantic color should be used for human text.
func colorEnabled(noColorFlag bool) bool {
	if noColorRequested(noColorFlag) {
		return false
	}
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// paint wraps s in the semantic color for a status token.
func paint(status, s string, color bool) string {
	if !color {
		return s
	}
	var code string
	switch status {
	case constants.StatusOK:
		code = "32" // green
	case constants.StatusWarn:
		code = "33" // yellow
	case constants.StatusError:
		code = "31" // red
	default:
		return s
	}
	return sgr(code, s)
}

// bold and dim are emphasis without a status meaning: table headers and row
// titles stand out, labels and unknown cells recede.
func bold(s string, color bool) string {
	if !color {
		return s
	}
	return sgr("1", s)
}

func dim(s string, color bool) string {
	if !color {
		return s
	}
	return sgr("2", s)
}

func sgr(code, s string) string {
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// toolAccountList renders a tool→account map as "claude:main codex:side" for
// human output, in constants.Tools order so the same mapping always reads the
// same way regardless of map iteration. Shared by the profile lines of `kae ls`
// and `kae status` and by the bound-directory rows of `kae ls --pins`.
//
// constants.Tools is a closed set and the input is not: `kae ls --pins` reads its
// map out of a directory's mise fragment, which an older kae may have written for
// a tool since retired (gemini, dropped in v0.6.0). Anything unrecognized is
// appended, sorted, rather than dropped — a silently shorter cell would make the
// text view disagree with the same map in `--json`, and the stale name is exactly
// what tells the user why to re-pin.
func toolAccountList(accounts map[string]string) string {
	mapping := make([]string, 0, len(accounts))
	for _, tool := range boundTools(accounts) {
		mapping = append(mapping, tool+":"+accounts[tool])
	}
	return strings.Join(mapping, " ")
}

// boundTools is the ordering half of the rule above, shared because a second caller
// re-derived it and got the retired-tool half wrong: `kae relogin`'s refusal names
// what the directory *does* bind, and a list that silently dropped a name would
// answer "it binds claude" about a fragment that also binds something kae has since
// retired — the one name that explains why the directory needs re-pinning.
func boundTools(accounts map[string]string) []string {
	ordered := make([]string, 0, len(accounts))
	for _, tool := range constants.Tools {
		if _, ok := accounts[tool]; ok {
			ordered = append(ordered, tool)
		}
	}
	unknown := []string{}
	for tool := range accounts {
		if !slices.Contains(constants.Tools, tool) {
			unknown = append(unknown, tool)
		}
	}
	sort.Strings(unknown)
	return append(ordered, unknown...)
}

// terminalColumns is the width a human table must fit, or zero for no limit.
// A variable so tests can pose as a narrow terminal.
var terminalColumns = stdoutColumns

// printTable renders rows with left-aligned, space-padded columns. When the
// terminal is narrower than the table, a wrapped row would scatter its cells
// across lines, so each row becomes a block instead: the first two cells as a
// title line, then one indented "Header  value" line per remaining non-empty
// cell. Output that is not a terminal keeps the table. With color, headers
// and titles are bold and "-" cells are dim.
func printTable(header []string, rows [][]string, color bool) {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = displayWidth(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && displayWidth(cell) > widths[i] {
				widths[i] = displayWidth(cell)
			}
		}
	}
	total := 2 * (len(widths) - 1)
	for _, w := range widths {
		total += w
	}
	if limit := terminalColumns(); limit > 0 && total > limit && len(header) > 2 {
		printStacked(header, rows, color)
		return
	}
	printRow := func(cells []string, style func(string) string) {
		parts := make([]string, len(cells))
		for i, cell := range cells {
			pad := widths[i] - displayWidth(cell)
			if pad < 0 {
				pad = 0
			}
			parts[i] = style(cell) + strings.Repeat(" ", pad)
		}
		fmt.Println(strings.TrimRight(strings.Join(parts, "  "), " "))
	}
	printRow(header, func(cell string) string { return bold(cell, color) })
	for _, row := range rows {
		printRow(row, func(cell string) string { return dimUnknown(cell, color) })
	}
}

// columnIdentity and columnDriver head the account-table columns that only
// --full shows (docs/CLI.md § Output Rules).
const (
	columnIdentity = "Identity"
	columnDriver   = "Driver"
)

// printAccountTable prints one of the account tables — status, accounts and the
// Accounts table of ls. Without full it drops the Identity and Driver columns
// before printTable measures the width, so the narrower table is the one that is
// fitted to the terminal or stacked. Every row has one cell per header column,
// as each caller builds it; a shorter row is a caller bug and panics here.
func printAccountTable(header []string, rows [][]string, full, color bool) {
	if !full {
		var keep []int
		for i, h := range header {
			if h != columnIdentity && h != columnDriver {
				keep = append(keep, i)
			}
		}
		narrowed := make([][]string, 0, len(rows)+1)
		for _, cells := range append([][]string{header}, rows...) {
			out := make([]string, len(keep))
			for k, i := range keep {
				out[k] = cells[i]
			}
			narrowed = append(narrowed, out)
		}
		header, rows = narrowed[0], narrowed[1:]
	}
	printTable(header, rows, color)
}

// dimUnknown recedes the "-" placeholder so known values carry the eye.
func dimUnknown(cell string, color bool) string {
	if cell == "-" {
		return dim(cell, color)
	}
	return cell
}

// printStacked is printTable's narrow layout.
func printStacked(header []string, rows [][]string, color bool) {
	label := 0
	for _, h := range header[2:] {
		label = max(label, displayWidth(h))
	}
	for _, row := range rows {
		title := []string{}
		for _, cell := range row[:min(2, len(row))] {
			if cell != "" {
				title = append(title, cell)
			}
		}
		fmt.Println(bold(strings.Join(title, "  "), color))
		for i := 2; i < len(row) && i < len(header); i++ {
			if row[i] == "" {
				continue
			}
			pad := strings.Repeat(" ", label-displayWidth(header[i]))
			fmt.Println("  " + dim(header[i], color) + pad + "  " + dimUnknown(row[i], color))
		}
	}
}

// displayWidth is the number of terminal columns s occupies: SGR sequences,
// combining marks (a macOS NFD path spells "プ" as two runes) and zero-width
// joiners take none, East Asian wide and fullwidth runes take two, and every
// other rune one. Byte length would over-count "·" and misalign the next
// column.
func displayWidth(s string) int {
	n := 0
	for _, r := range stripANSI(s) {
		if unicode.In(r, unicode.Mn, unicode.Me) || r == '\u200d' {
			continue
		}
		switch width.LookupRune(r).Kind() {
		case width.EastAsianWide, width.EastAsianFullwidth:
			n += 2
		default:
			n++
		}
	}
	return n
}

var sgrRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

// stripANSI removes SGR sequences for width calculation.
func stripANSI(s string) string {
	return sgrRE.ReplaceAllString(s, "")
}

// displayPath shortens an absolute path under home to ~/... for output.
func (app *App) displayPath(path string) string {
	home := app.Env.Home
	if home != "" && path == home {
		return "~"
	}
	if home != "" && strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~" + path[len(home):]
	}
	return path
}

// printResultWarnings lists an apply result's warnings as indented report lines.
func printResultWarnings(warnings []string) {
	for _, warning := range warnings {
		fmt.Printf("  warning: %s\n", warning)
	}
}
