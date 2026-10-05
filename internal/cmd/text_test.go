package cmd

import (
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
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
	if got := displayWidth("\u30d5\u309a e\u0301"); got != 4 {
		t.Fatalf("combining marks take no column: displayWidth = %d, want 4", got)
	}
}

func TestLimitCellColorsEachWindowAndDimsTheCountdown(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	usage := &usageJSON{Windows: []usagelimit.Window{
		{ID: constants.UsageWindowFiveHour, UsedPercent: 16, ResetsAt: now.Add(2*time.Hour + 13*time.Minute)},
		{ID: constants.UsageWindowSevenDay, UsedPercent: 95, ResetsAt: now.Add(76 * time.Hour)},
	}}
	if got, want := limitCell(usage, now, false, false), "5h 16% (2h13m) · 7d 95% (3d4h)"; got != want {
		t.Fatalf("plain = %q, want %q", got, want)
	}
	want := "5h \x1b[32m16%\x1b[0m \x1b[2m(2h13m)\x1b[0m · 7d \x1b[33m95%\x1b[0m \x1b[2m(3d4h)\x1b[0m"
	if got := limitCell(usage, now, true, false); got != want {
		t.Fatalf("colored = %q, want %q", got, want)
	}
	if got := limitCell(nil, now, true, false); got != "-" {
		t.Fatalf("missing = %q", got)
	}
}

func TestLimitCellFullEndsWithTheReadingAgeOnce(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	stamp := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	windows := []usagelimit.Window{
		{ID: constants.UsageWindowFiveHour, UsedPercent: 16, ResetsAt: now.Add(2*time.Hour + 13*time.Minute)},
		{ID: constants.UsageWindowSevenDay, UsedPercent: 95, ResetsAt: now.Add(76 * time.Hour)},
	}
	usage := &usageJSON{ObservedAt: stamp(22*time.Hour + 13*time.Minute + 30*time.Second), Windows: windows}
	if got, want := limitCell(usage, now, false, true), "5h 16% (2h13m) · 7d 95% (3d4h) · 22h13m ago"; got != want {
		t.Fatalf("full = %q, want %q", got, want)
	}
	if got, want := limitCell(usage, now, false, false), "5h 16% (2h13m) · 7d 95% (3d4h)"; got != want {
		t.Fatalf("without --full the cell is unchanged: %q, want %q", got, want)
	}
	want := "5h \x1b[32m16%\x1b[0m \x1b[2m(2h13m)\x1b[0m · 7d \x1b[33m95%\x1b[0m \x1b[2m(3d4h)\x1b[0m · \x1b[2m22h13m ago\x1b[0m"
	if got := limitCell(usage, now, true, true); got != want {
		t.Fatalf("colored = %q, want %q", got, want)
	}

	for _, tc := range []struct {
		name     string
		observed string
		want     string
	}{
		{"under a minute", stamp(30 * time.Second), "7d 95% (3d4h) · 1m ago"},
		{"minutes", stamp(45*time.Minute + 59*time.Second), "7d 95% (3d4h) · 45m ago"},
		{"days", stamp(4*24*time.Hour + 8*time.Hour + 59*time.Minute), "7d 95% (3d4h) · 4d8h ago"},
		{"not in the past", now.Add(time.Hour).Format(time.RFC3339), "7d 95% (3d4h) · 1m ago"},
		{"no observed_at", "", "7d 95% (3d4h)"},
		{"unparseable observed_at", "yesterday", "7d 95% (3d4h)"},
	} {
		one := &usageJSON{ObservedAt: tc.observed, Windows: windows[1:]}
		if got := limitCell(one, now, false, true); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}

	for _, empty := range []*usageJSON{nil, {ObservedAt: stamp(time.Hour)}} {
		if got := limitCell(empty, now, true, true); got != "-" {
			t.Fatalf("no reading gets no age: %q", got)
		}
	}
}

func TestLimitCellFullAgeInJapanese(t *testing.T) {
	l10ntest.UseJapanese(t)
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	usage := &usageJSON{
		ObservedAt: now.Add(-22 * time.Hour).Format(time.RFC3339),
		Windows: []usagelimit.Window{
			{ID: constants.UsageWindowSevenDay, UsedPercent: 23, ResetsAt: now.Add(104 * time.Hour)},
		},
	}
	if got, want := limitCell(usage, now, false, true), "7d 23% (4d8h) · 22h0m 前"; got != want {
		t.Fatalf("ja = %q, want %q", got, want)
	}
	if got, want := limitCell(usage, now, false, false), "7d 23% (4d8h)"; got != want {
		t.Fatalf("ja without --full = %q, want %q", got, want)
	}
}

func TestPrintTableStackedWithColorDimsLabelsAndPlaceholders(t *testing.T) {
	withTerminalColumns(t, 10)
	_, out := captureStdout(t, func() int {
		printTable([]string{"Tool", "Account", "Limit"}, [][]string{{"codex", "side", "-"}}, true)
		return 0
	})
	want := "\x1b[1mcodex  side\x1b[0m\n  \x1b[2mLimit\x1b[0m  \x1b[2m-\x1b[0m\n"
	if out != want {
		t.Fatalf("stacked = %q, want %q", out, want)
	}
}
