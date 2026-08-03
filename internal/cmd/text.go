package cmd

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// colorEnabled reports whether semantic color should be used for human text.
func colorEnabled(noColorFlag bool) bool {
	if noColorFlag || os.Getenv("NO_COLOR") != "" {
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
	known := map[string]bool{}
	for _, tool := range constants.Tools {
		known[tool] = true
		if accountName, ok := accounts[tool]; ok {
			mapping = append(mapping, tool+":"+accountName)
		}
	}
	unknown := []string{}
	for tool, accountName := range accounts {
		if !known[tool] {
			unknown = append(unknown, tool+":"+accountName)
		}
	}
	sort.Strings(unknown)
	return strings.Join(append(mapping, unknown...), " ")
}

// printTable renders rows with left-aligned, space-padded columns.
func printTable(header []string, rows [][]string) {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(stripANSI(cell)) > widths[i] {
				widths[i] = len(stripANSI(cell))
			}
		}
	}
	printRow := func(cells []string) {
		parts := make([]string, len(cells))
		for i, cell := range cells {
			pad := widths[i] - len(stripANSI(cell))
			if pad < 0 {
				pad = 0
			}
			parts[i] = cell + strings.Repeat(" ", pad)
		}
		fmt.Println(strings.TrimRight(strings.Join(parts, "  "), " "))
	}
	printRow(header)
	for _, row := range rows {
		printRow(row)
	}
}

var sgrRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

// stripANSI removes SGR sequences for width calculation.
func stripANSI(s string) string {
	return sgrRE.ReplaceAllString(s, "")
}

// displayPath shortens an absolute path under home to ~/... for output.
func (app *App) displayPath(path string) string {
	home := app.Env.Home
	if home != "" && strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~" + path[len(home):]
	}
	return path
}
