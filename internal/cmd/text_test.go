package cmd

import (
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/usagelimit"
)

func withTerminalColumns(t *testing.T, columns int) {
	t.Helper()
	prev := terminalColumns
	terminalColumns = func() int { return columns }
	t.Cleanup(func() { terminalColumns = prev })
}

func TestPrintTableAlignsCellsByDisplayWidth(t *testing.T) {
	withTerminalColumns(t, 0)
	_, out := captureStdout(t, func() int {
		printTable([]string{"Tool", "Limit", "Notes"}, [][]string{
			{"claude", "5h 16% · 7d 95%", "x"},
			{"codex", "-", "y"},
		}, false)
		return 0
	})
	want := "Tool    Limit            Notes\n" +
		"claude  5h 16% · 7d 95%  x\n" +
		"codex   -                y\n"
	if out != want {
		t.Fatalf("table =\n%s\nwant\n%s", out, want)
	}
}

func TestPrintTableStacksRowsWhenTheTerminalIsNarrow(t *testing.T) {
	header := []string{"Tool", "Account", "Active", "Limit"}
	rows := [][]string{
		{"claude", "main", "*", "5h 16% (2h13m) · 7d 95% (3d4h)"},
		{"codex", "side", "", "-"},
	}
	withTerminalColumns(t, 40)
	_, out := captureStdout(t, func() int { printTable(header, rows, false); return 0 })
	want := "claude  main\n" +
		"  Active  *\n" +
		"  Limit   5h 16% (2h13m) · 7d 95% (3d4h)\n" +
		"codex  side\n" +
		"  Limit   -\n"
	if out != want {
		t.Fatalf("stacked =\n%s\nwant\n%s", out, want)
	}
	withTerminalColumns(t, 80)
	_, out = captureStdout(t, func() int { printTable(header, rows, false); return 0 })
	if out[:4] != "Tool" {
		t.Fatalf("a table that fits stays a table:\n%s", out)
	}
}

func TestDisplayWidthCountsWideRunesTwice(t *testing.T) {
	if got := displayWidth("\x1b[33m日本·a\x1b[0m"); got != 6 {
		t.Fatalf("displayWidth = %d, want 6", got)
	}
}

func TestLimitCellColorsEachWindowAndDimsTheCountdown(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	usage := &usageJSON{Windows: []usagelimit.Window{
		{ID: constants.UsageWindowFiveHour, UsedPercent: 16, ResetsAt: now.Add(2*time.Hour + 13*time.Minute)},
		{ID: constants.UsageWindowSevenDay, UsedPercent: 95, ResetsAt: now.Add(76 * time.Hour)},
	}}
	if got, want := limitCell(usage, now, false), "5h 16% (2h13m) · 7d 95% (3d4h)"; got != want {
		t.Fatalf("plain = %q, want %q", got, want)
	}
	want := "5h \x1b[32m16%\x1b[0m \x1b[2m(2h13m)\x1b[0m · 7d \x1b[33m95%\x1b[0m \x1b[2m(3d4h)\x1b[0m"
	if got := limitCell(usage, now, true); got != want {
		t.Fatalf("colored = %q, want %q", got, want)
	}
	if got := limitCell(nil, now, true); got != "-" {
		t.Fatalf("missing = %q", got)
	}
}
