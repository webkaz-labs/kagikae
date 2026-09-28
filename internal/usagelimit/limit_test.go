package usagelimit

import (
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

func TestFormatOrdersTheShortWindowFirst(t *testing.T) {
	windows := []Window{
		{ID: constants.UsageWindowSevenDay, UsedPercent: 95, Minutes: 10080},
		{ID: constants.UsageWindowFiveHour, UsedPercent: 16, Minutes: 300},
	}
	if got, want := Format(windows), "5h 16% · 7d 95%"; got != want {
		t.Fatalf("Format = %q, want %q", got, want)
	}
	if windows[0].Level() != LevelHigh || windows[1].Level() != LevelOK {
		t.Fatalf("levels = %d %d, want high and ok", windows[0].Level(), windows[1].Level())
	}
	windows[0].UsedPercent = 100
	if windows[0].Level() != LevelFull {
		t.Fatalf("100%% must read as full, got %d", windows[0].Level())
	}
}

func TestFreshDropsAWindowThatHasReset(t *testing.T) {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)
	windows := Fresh([]Window{
		{ID: constants.UsageWindowFiveHour, UsedPercent: 10, ResetsAt: now.Add(-time.Minute)},
		{ID: constants.UsageWindowSevenDay, UsedPercent: 4, ResetsAt: now.Add(time.Hour)},
		{ID: "kept", UsedPercent: 1},
	}, now)
	if len(windows) != 2 || windows[0].ID != constants.UsageWindowSevenDay || windows[1].ID != "kept" {
		t.Fatalf("fresh windows = %+v", windows)
	}
}

func TestFormatAtShowsTheTimeLeftUntilEachReset(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	windows := []Window{
		{ID: constants.UsageWindowSevenDay, UsedPercent: 95, ResetsAt: now.Add(3*24*time.Hour + 4*time.Hour + 59*time.Minute)},
		{ID: constants.UsageWindowFiveHour, UsedPercent: 16, ResetsAt: now.Add(2*time.Hour + 13*time.Minute + 30*time.Second)},
		{ID: "window_60", UsedPercent: 1},
	}
	if got, want := FormatAt(windows, now), "5h 16% (2h13m) · 7d 95% (3d4h) · window_60 1%"; got != want {
		t.Fatalf("FormatAt = %q, want %q", got, want)
	}
	if got, want := FormatAt(windows, time.Time{}), "5h 16% · 7d 95% · window_60 1%"; got != want {
		t.Fatalf("FormatAt without now = %q, want %q", got, want)
	}
}

func TestRemainingKeepsTheTwoLargestUnits(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{20 * time.Second, "1m"},
		{45 * time.Minute, "45m"},
		{time.Hour, "1h0m"},
		{23*time.Hour + 59*time.Minute, "23h59m"},
		{24 * time.Hour, "1d0h"},
		{6*24*time.Hour + 23*time.Hour + 59*time.Minute, "6d23h"},
	} {
		if got := Remaining(tc.d); got != tc.want {
			t.Errorf("Remaining(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
