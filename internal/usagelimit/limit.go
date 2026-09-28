// Package usagelimit is the shared shape of a subscription-window reading.
// Tool adapters parse local files and remote bodies into it; cmd decides which
// reading to show and renders it. The package does no IO and holds no token.
package usagelimit

import (
	"fmt"
	"sort"
	"time"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// A window is "high" once four fifths of it is gone, and "full" at the cap.
// Both cutoffs are display policy: they decide the color of a compact cell,
// not a claim about when the upstream client itself warns.
const (
	LevelOK = iota
	LevelHigh
	LevelFull

	highPercent = 80
	fullPercent = 100
)

// Window is one subscription window. ID is a JSON contract token
// (constants.UsageWindow*). Minutes is the window length when the tool stated
// one; zero means the id already names the length.
type Window struct {
	ID          string    `json:"id"`
	UsedPercent float64   `json:"used_percent"`
	ResetsAt    time.Time `json:"resets_at,omitempty"`
	Minutes     int       `json:"minutes,omitempty"`
}

// Reading is what a local file or a response body yielded. Path and ModTime
// identify the file a later listing must not reassign to a different account
// while the file is unchanged. Both are empty for a remote reading.
type Reading struct {
	Path    string
	ModTime time.Time
	Windows []Window
}

// Fresh drops windows whose reset is already past. A zero reset is kept: kae
// has a percent and no deadline it can retire.
func Fresh(windows []Window, now time.Time) []Window {
	out := make([]Window, 0, len(windows))
	for _, w := range windows {
		if w.ID == "" {
			continue
		}
		if !w.ResetsAt.IsZero() && !w.ResetsAt.After(now) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// Normalize orders windows the way every listing shows them: the shorter
// known window first, then any other window by its length.
func Normalize(windows []Window) []Window {
	out := append([]Window(nil), windows...)
	sort.SliceStable(out, func(i, j int) bool {
		return rank(out[i]) < rank(out[j])
	})
	return out
}

func rank(w Window) int {
	switch w.ID {
	case constants.UsageWindowFiveHour:
		return 0
	case constants.UsageWindowSevenDay:
		return 1
	default:
		if w.Minutes > 0 {
			return 1000 + w.Minutes
		}
		return 2000
	}
}

// Level is the strongest attention state among windows.
func Level(windows []Window) int {
	level := LevelOK
	for _, w := range windows {
		switch {
		case w.UsedPercent >= fullPercent:
			return LevelFull
		case w.UsedPercent >= highPercent:
			level = LevelHigh
		}
	}
	return level
}

// Format renders a compact cell such as "5h 16% · 7d 95%". An empty result
// means there is nothing to show; callers print their own placeholder.
func Format(windows []Window) string {
	if len(windows) == 0 {
		return ""
	}
	parts := make([]string, 0, len(windows))
	for _, w := range Normalize(windows) {
		parts = append(parts, label(w)+" "+percent(w.UsedPercent))
	}
	return joinDot(parts)
}

func label(w Window) string {
	switch w.ID {
	case constants.UsageWindowFiveHour:
		return "5h"
	case constants.UsageWindowSevenDay:
		return "7d"
	}
	if w.Minutes <= 0 {
		return w.ID
	}
	if w.Minutes%1440 == 0 {
		return fmt.Sprintf("%dd", w.Minutes/1440)
	}
	if w.Minutes%60 == 0 {
		return fmt.Sprintf("%dh", w.Minutes/60)
	}
	return fmt.Sprintf("%dm", w.Minutes)
}

func percent(p float64) string {
	if p < 0 {
		p = 0
	}
	if p > fullPercent {
		p = fullPercent
	}
	return fmt.Sprintf("%.0f%%", p)
}

func joinDot(parts []string) string {
	out := parts[0]
	for _, p := range parts[1:] {
		out += " · " + p
	}
	return out
}

// IDForMinutes maps a window length onto the contract id when the length is
// one kae already names, and a stable fallback otherwise.
func IDForMinutes(minutes int) string {
	switch minutes {
	case 300:
		return constants.UsageWindowFiveHour
	case 10080:
		return constants.UsageWindowSevenDay
	default:
		if minutes <= 0 {
			return ""
		}
		return fmt.Sprintf("window_%d", minutes)
	}
}
