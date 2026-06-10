package cmd

import (
	"fmt"
	"os"
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

// stripANSI removes SGR sequences for width calculation.
func stripANSI(s string) string {
	for {
		start := strings.Index(s, "\x1b[")
		if start < 0 {
			return s
		}
		end := strings.Index(s[start:], "m")
		if end < 0 {
			return s
		}
		s = s[:start] + s[start+end+1:]
	}
}

// displayPath shortens an absolute path under home to ~/... for output.
func (app *App) displayPath(path string) string {
	home := app.Env.Home
	if home != "" && strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~" + path[len(home):]
	}
	return path
}
