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
	if Level(windows) != LevelHigh {
		t.Fatalf("95%% must read as high, got %d", Level(windows))
	}
	windows[1].UsedPercent = 100
	if Level(windows) != LevelFull {
		t.Fatalf("100%% must read as full, got %d", Level(windows))
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
