package cmd

import (
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// TestNearestMatch covers the noise-avoiding threshold: a near miss hints, a
// wildly different token does not, an exact match never hints, and a tie for the
// best distance is suppressed (docs/RELEASE.md v0.8.5 §A).
func TestNearestMatch(t *testing.T) {
	cands := []string{"use", "pin", "run", "status"}
	cases := []struct {
		name  string
		input string
		cands []string
		want  string // "" means no suggestion
	}{
		{"near command", "uze", cands, "use"},
		{"insert", "satus", cands, "status"},
		{"unrelated token", "zzzzz", cands, ""},
		{"exact match", "use", cands, ""},
		{"empty candidates", "use", nil, ""},
		{"over distance 2", "useless", cands, ""}, // distance 4 to "use"
		{"tie suppressed", "pun", []string{"pin", "run"}, ""},
		{"short typo of long word", "stetus", cands, "status"}, // distance 1, len/3+1=3
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := nearestMatch(tc.input, tc.cands)
			if tc.want == "" {
				if ok {
					t.Fatalf("expected no suggestion, got %q", got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("want %q, got %q (ok=%v)", tc.want, got, ok)
			}
		})
	}
}

// TestDidYouMeanUnknownCommand: a near-miss command names the nearest command;
// an unrelated token leaves the error unchanged; the message still names the
// help pointer and keeps the usage exit code.
func TestDidYouMeanUnknownCommand(t *testing.T) {
	code, out := captureStderr(t, func() int { return Root([]string{"uze"}) })
	if code != constants.ExitUsage {
		t.Fatalf("want ExitUsage, got %d (%s)", code, out)
	}
	if !strings.Contains(out, `did you mean "use"?`) {
		t.Fatalf("expected a did-you-mean hint naming use, got %q", out)
	}
	if !strings.Contains(out, "see kae help") {
		t.Fatalf("hint must not drop the existing help pointer: %q", out)
	}

	_, unrelated := captureStderr(t, func() int { return Root([]string{"zzzzz"}) })
	if strings.Contains(unrelated, "did you mean") {
		t.Fatalf("an unrelated token must not hint: %q", unrelated)
	}
}

// TestDidYouMeanUnknownTool: a near-miss tool hints at the nearest tool; an
// exact prefix alias still resolves with no hint (resolveToolArg unchanged).
func TestDidYouMeanUnknownTool(t *testing.T) {
	err := validateTool("clade")
	if err == nil || !strings.Contains(err.Error(), `did you mean "claude"?`) {
		t.Fatalf("expected a did-you-mean hint naming claude, got %v", err)
	}
	if exitOf(err) != constants.ExitUsage {
		t.Fatalf("hint must keep the usage exit code, got %d", exitOf(err))
	}

	// A genuine prefix alias resolves to the canonical tool with no error/hint.
	if resolved, err := resolveToolArg("cl"); err != nil || resolved != constants.ToolClaude {
		t.Fatalf("prefix alias must still resolve: %q %v", resolved, err)
	}

	// A removed tool keeps its successor message, not a did-you-mean hint.
	for removed := range constants.RemovedTools {
		rmErr := validateTool(removed)
		if rmErr == nil || strings.Contains(rmErr.Error(), "did you mean") {
			t.Fatalf("removed tool %q must keep the successor message, got %v", removed, rmErr)
		}
		break
	}
}

// TestDidYouMeanUnknownProfile: a near-miss profile names the nearest defined
// profile; an unrelated token leaves the not-found error unchanged.
func TestDidYouMeanUnknownProfile(t *testing.T) {
	app := applyTestApp(t, nil) // defines profiles work + personal

	_, _, err := app.resolveTargets("all", "wrok")
	if err == nil || !strings.Contains(err.Error(), `did you mean "work"?`) {
		t.Fatalf("expected a did-you-mean hint naming work, got %v", err)
	}
	if exitOf(err) != constants.ExitNotFound {
		t.Fatalf("hint must keep the not-found exit code, got %d", exitOf(err))
	}

	_, _, unrelated := app.resolveTargets("all", "zzzzz")
	if unrelated == nil || strings.Contains(unrelated.Error(), "did you mean") {
		t.Fatalf("an unrelated profile token must not hint, got %v", unrelated)
	}
}
