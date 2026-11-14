package cmd

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/state"
	"github.com/webkaz-labs/kagikae/internal/usagelimit"
)

// refetchBody is what the fake usage endpoint answers: windows that differ
// from every cached reading below, so a test can tell which one it was shown.
const refetchBody = `{"five_hour":{"utilization":10,"resets_at":"2026-07-01T00:00:00Z"},"seven_day":{"utilization":20,"resets_at":"2026-07-08T00:00:00Z"}}`

// A remembered reading that is ten minutes old, or that has any window reset,
// is asked again when the stored token is still valid; otherwise it is shown
// as it was, with its original observation time.
func TestUsageRefetchesAStaleRememberedReading(t *testing.T) {
	const validToken = `{"accessToken":"sk-ant-oat01-REFETCH-CANARY","expiresAt":1800000000000}`
	const expiredToken = `{"accessToken":"sk-ant-oat01-REFETCH-CANARY","expiresAt":1000000000000}`
	now := time.Date(2026, 6, 11, 1, 23, 45, 0, time.UTC)
	later := func(d time.Duration) time.Time { return now.Add(d) }
	win := func(five, seven time.Time) []usagelimit.Window {
		return []usagelimit.Window{
			{ID: constants.UsageWindowFiveHour, UsedPercent: 90, ResetsAt: five, Minutes: 300},
			{ID: constants.UsageWindowSevenDay, UsedPercent: 23, ResetsAt: seven, Minutes: 10080},
		}
	}
	open := win(later(2*time.Hour), later(48*time.Hour))
	fiveReset := win(later(-time.Minute), later(48*time.Hour))
	const cachedText, probedText = "5h 90% · 7d 23%", "5h 10% · 7d 20%"
	tests := []struct {
		name     string
		origin   string
		observed time.Time
		windows  []usagelimit.Window
		token    string
		status   int
		noClient bool
		wantHits int
		want     string
		wantAt   time.Time
	}{
		{"stale local reading is asked again", constants.UsageOriginLocal, later(-30 * time.Minute), open, validToken, 200, false, 1, probedText, now},
		{"stale remote reading is asked again", constants.UsageOriginRemote, later(-30 * time.Minute), open, validToken, 200, false, 1, probedText, now},
		{"failed request keeps the cached windows", constants.UsageOriginLocal, later(-30 * time.Minute), open, validToken, 500, false, 1, cachedText, later(-30 * time.Minute)},
		{"expired token sends nothing", constants.UsageOriginLocal, later(-30 * time.Minute), open, expiredToken, 200, false, 0, cachedText, later(-30 * time.Minute)},
		{"no client sends nothing", constants.UsageOriginLocal, later(-30 * time.Minute), open, validToken, 200, true, 0, cachedText, later(-30 * time.Minute)},
		{"one reset window asks again while the other is valid", constants.UsageOriginLocal, later(-time.Minute), fiveReset, validToken, 200, false, 1, probedText, now},
		{"one reset window, request fails: the valid window stays", constants.UsageOriginLocal, later(-time.Minute), fiveReset, validToken, 500, false, 1, "7d 23%", later(-time.Minute)},
		{"a young reading is not asked again", constants.UsageOriginLocal, later(-9 * time.Minute), open, validToken, 200, false, 0, cachedText, later(-9 * time.Minute)},
		{"a young remote reading is not asked again", constants.UsageOriginRemote, later(-9 * time.Minute), open, validToken, 200, false, 0, cachedText, later(-9 * time.Minute)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chdirTemp(t)
			app := testApp(t, nil)
			ctx := context.Background()
			seedClaudeOAuth(t, app, tc.token)
			if code, out := captureStdout(t, func() int {
				return runCapture(ctx, app, commonOpts{Format: formatText, NoColor: true}, "claude", "main")
			}); code != constants.ExitOK {
				t.Fatalf("capture: %s", out)
			}
			saveUsageCache(app.Paths.UsageCacheFile(), usageCache{Entries: []usageEntry{{
				Tool: constants.ToolClaude, Account: "main", Origin: tc.origin,
				ObservedAt: tc.observed, Windows: tc.windows,
			}}})
			hits := 0
			if !tc.noClient {
				app.usageClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					hits++
					return &http.Response{
						StatusCode: tc.status, Header: make(http.Header),
						Body: io.NopCloser(strings.NewReader(refetchBody)),
					}, nil
				})}
			}
			captured, err := account.List(app.Paths.AccountsDir())
			if err != nil {
				t.Fatal(err)
			}
			views := app.accountUsages(ctx, captured, state.New())
			view := views[toolAccount{constants.ToolClaude, "main"}]
			if hits != tc.wantHits {
				t.Fatalf("hits = %d, want %d", hits, tc.wantHits)
			}
			if got := usagelimit.Format(view.Windows); got != tc.want {
				t.Fatalf("windows = %q, want %q", got, tc.want)
			}
			if !view.ObservedAt.Equal(tc.wantAt) {
				t.Fatalf("observed at %s, want %s", view.ObservedAt, tc.wantAt)
			}
		})
	}
}

// `kae use` with no listing in between: the file the previous account's
// session wrote stays with that account and the new one does not get it.
func TestSwitchKeepsTheSharedHomeReadingWithTheOutgoingAccount(t *testing.T) {
	chdirTemp(t)
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText, NoColor: true}
	seedClaude(t, app, mainToken, "main-uuid")
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
		t.Fatalf("capture main: %s", out)
	}
	seedClaude(t, app, sideToken, "side-uuid")
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "side") }); code != constants.ExitOK {
		t.Fatalf("capture side: %s", out)
	}
	st := state.New()
	st.Active[constants.ToolClaude] = "main"
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(app.Env.Home, ".claude", "usage-exact.json"),
		`{"fiveHour":{"usedPercent":16,"resetsAt":1800000000},"sevenDay":{"usedPercent":95,"resetsAt":1800003600}}`)

	if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "side") }); code != constants.ExitOK {
		t.Fatalf("switch: %s", out)
	}
	st, err := state.Load(app.Paths.StateFile())
	if err != nil || st.Active[constants.ToolClaude] != "side" {
		t.Fatalf("state = %+v, err = %v", st, err)
	}
	captured, err := account.List(app.Paths.AccountsDir())
	if err != nil {
		t.Fatal(err)
	}
	views := app.accountUsages(ctx, captured, st)
	if _, ok := views[toolAccount{constants.ToolClaude, "side"}]; ok {
		t.Fatalf("side must not take the pre-switch file: %+v", views)
	}
	if got := usagelimit.Format(views[toolAccount{constants.ToolClaude, "main"}].Windows); got != "5h 16% · 7d 95%" {
		t.Fatalf("main = %q", got)
	}
}
