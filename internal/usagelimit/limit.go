// Package usagelimit is the shared shape of a subscription-window reading.
// Tool adapters parse local files and remote bodies into it; cmd decides which
// reading to show and renders it. The package does no IO and holds no token.
package usagelimit

import (
	"fmt"
	"sort"
	"strings"
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

// Level is this window's attention state.
func (w Window) Level() int {
	switch {
	case w.UsedPercent >= fullPercent:
		return LevelFull
	case w.UsedPercent >= highPercent:
		return LevelHigh
	default:
		return LevelOK
	}
}

// Part is one window of a compact cell, split so a renderer can style the
// percent and the time left separately. Reset is empty when there is no
// deadline to count down to.
type Part struct {
	Label   string
	Percent string
	Reset   string
	Level   int
}

// Text is the part as plain text: "5h 16%" or "5h 16% (2h13m)".
func (p Part) Text() string {
	return p.Render(plain, plain)
}

// Render is Text with the percent and the parenthesized countdown passed
// through their own styling.
func (p Part) Render(percent, reset func(string) string) string {
	if p.Reset == "" {
		return p.Label + " " + percent(p.Percent)
	}
	return p.Label + " " + percent(p.Percent) + " " + reset("("+p.Reset+")")
}

func plain(s string) string { return s }

// Parts lists windows in display order. A zero now, a window without a reset,
// or a reset that is not after now leaves Reset empty.
func Parts(windows []Window, now time.Time) []Part {
	parts := make([]Part, 0, len(windows))
	for _, w := range Normalize(windows) {
		part := Part{Label: label(w), Percent: percent(w.UsedPercent), Level: w.Level()}
		if !now.IsZero() && w.ResetsAt.After(now) {
			part.Reset = CompactDuration(w.ResetsAt.Sub(now))
		}
		parts = append(parts, part)
	}
	return parts
}

// Format renders a compact cell such as "5h 16% · 7d 95%". An empty result
// means there is nothing to show; callers print their own placeholder.
func Format(windows []Window) string {
	return FormatAt(windows, time.Time{})
}

// FormatAt is Format with the time left until each window resets, as in
// "5h 16% (2h13m) · 7d 95% (3d4h)".
func FormatAt(windows []Window, now time.Time) string {
	parts := Parts(windows, now)
	texts := make([]string, len(parts))
	for i, p := range parts {
		texts[i] = p.Text()
	}
	return strings.Join(texts, Separator)
}

// Separator joins the parts of a compact cell.
const Separator = " · "

// CompactDuration renders a duration in the two largest units: "3d4h",
// "2h13m", or "45m", truncated. It is both a window's countdown and a
// reading's age. Anything under a minute, a negative duration included,
// reads "1m": a zero countdown would look already reset, and a zero or
// negative age would claim a reading newer than kae can know it is.
func CompactDuration(d time.Duration) string {
	minutes := int(d / time.Minute)
	switch {
	case minutes < 1:
		return "1m"
	case minutes < 60:
		return fmt.Sprintf("%dm", minutes)
	case minutes < 1440:
		return fmt.Sprintf("%dh%dm", minutes/60, minutes%60)
	default:
		return fmt.Sprintf("%dd%dh", minutes/1440, minutes%1440/60)
	}
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
