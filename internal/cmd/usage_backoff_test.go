package cmd

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/state"
	"github.com/webkaz-labs/kagikae/internal/usagelimit"
)

// A request that was sent and gave no reading holds the next one back for ten
// minutes, even when no cached window is left to show. A token that cannot be
// sent costs no request, so it leaves no mark. A reading clears the mark.
func TestUsageFailedRequestBacksOffForTheReadingTTL(t *testing.T) {
	const validToken = `{"accessToken":"sk-ant-oat01-BACKOFF-CANARY","expiresAt":1800000000000}`
	const expiredToken = `{"accessToken":"sk-ant-oat01-BACKOFF-CANARY","expiresAt":1000000000000}`
	setup := func(t *testing.T, token string, status *int, hits *int) (*App, []account.Account) {
		chdirTemp(t)
		app := testApp(t, nil)
		seedClaudeOAuth(t, app, token)
		if code, out := captureStdout(t, func() int {
			return runCapture(context.Background(), app, commonOpts{Format: formatText, NoColor: true}, "claude", "main")
		}); code != constants.ExitOK {
			t.Fatalf("capture: %s", out)
		}
		app.usageClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			*hits++
			return &http.Response{
				StatusCode: *status, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(refetchBody)),
			}, nil
		})}
		captured, err := account.List(app.Paths.AccountsDir())
		if err != nil {
			t.Fatal(err)
		}
		return app, captured
	}
	list := func(app *App, captured []account.Account) usageView {
		return app.accountUsages(context.Background(), captured, state.New())[toolAccount{constants.ToolClaude, "main"}]
	}
	advance := func(app *App, d time.Duration) {
		at := app.Now().Add(d)
		app.Now = func() time.Time { return at }
	}

	t.Run("no reading at all", func(t *testing.T) {
		hits, status := 0, 500
		app, captured := setup(t, validToken, &status, &hits)
		list(app, captured)
		list(app, captured)
		if hits != 1 {
			t.Fatalf("hits = %d, want 1 inside the backoff", hits)
		}
		advance(app, usageReadingTTL)
		list(app, captured)
		if hits != 2 {
			t.Fatalf("hits = %d, want 2 once the backoff has passed", hits)
		}
	})

	t.Run("a stale reading stays shown", func(t *testing.T) {
		hits, status := 0, 500
		app, captured := setup(t, validToken, &status, &hits)
		observed := app.Now().Add(-time.Hour)
		saveUsageCache(app.Paths.UsageCacheFile(), usageCache{Entries: []usageEntry{{
			Tool: constants.ToolClaude, Account: "main", Origin: constants.UsageOriginLocal, ObservedAt: observed,
			Windows: []usagelimit.Window{{ID: constants.UsageWindowSevenDay, UsedPercent: 23, ResetsAt: app.Now().Add(48 * time.Hour)}},
		}}})
		for i := 0; i < 2; i++ {
			if view := list(app, captured); usagelimit.Format(view.Windows) != "7d 23%" || !view.ObservedAt.Equal(observed) {
				t.Fatalf("listing %d: %+v", i, view)
			}
		}
		if hits != 1 {
			t.Fatalf("hits = %d, want 1", hits)
		}
		if cache := readFile(t, app.Paths.UsageCacheFile()); !strings.Contains(cache, `"probed_at"`) {
			t.Fatalf("the attempt is not recorded:\n%s", cache)
		}
	})

	t.Run("an expired token leaves no mark", func(t *testing.T) {
		hits, status := 0, 200
		app, captured := setup(t, expiredToken, &status, &hits)
		list(app, captured)
		if hits != 0 {
			t.Fatalf("hits = %d", hits)
		}
		if _, err := os.Stat(app.Paths.UsageCacheFile()); err == nil {
			if cache := readFile(t, app.Paths.UsageCacheFile()); strings.Contains(cache, "probed_at") {
				t.Fatalf("an unsent request left a mark:\n%s", cache)
			}
		}
	})

	t.Run("a local file whose windows have all reset keeps the mark", func(t *testing.T) {
		hits, status := 0, 500
		app, captured := setup(t, validToken, &status, &hits)
		// resetsAt 1000000000 is 2001: every window in the file has reset.
		writeFile(t, filepath.Join(app.Env.Home, ".claude", "usage-exact.json"),
			`{"fiveHour":{"usedPercent":16,"resetsAt":1000000000},"sevenDay":{"usedPercent":95,"resetsAt":1000003600}}`)
		st := state.New()
		st.Active[constants.ToolClaude] = "main"
		for i := 0; i < 3; i++ {
			app.accountUsages(context.Background(), captured, st)
		}
		if hits != 1 {
			t.Fatalf("hits = %d, want 1 inside the backoff", hits)
		}
	})

	t.Run("a reading clears the mark", func(t *testing.T) {
		hits, status := 0, 500
		app, captured := setup(t, validToken, &status, &hits)
		list(app, captured)
		status = 200
		advance(app, usageReadingTTL)
		if got := usagelimit.Format(list(app, captured).Windows); got != "5h 10% · 7d 20%" {
			t.Fatalf("windows = %q", got)
		}
		if cache := readFile(t, app.Paths.UsageCacheFile()); strings.Contains(cache, "probed_at") {
			t.Fatalf("a reading must clear the mark:\n%s", cache)
		}
	})
}

// recordSharedUsage's guards: a file already attributed to another account is
// not relabeled, a newer reading of the outgoing account is not overwritten,
// and a codex rollout another captured account created is not recorded.
func TestRecordSharedUsageGuards(t *testing.T) {
	ctx := context.Background()
	mtime := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	claudeApp := func(t *testing.T) (*App, string, *state.State) {
		app := testApp(t, nil)
		for _, name := range []string{"main", "side", "alt"} {
			acc := account.Account{Version: 1, Tool: constants.ToolClaude, Name: name}
			if err := account.Save(app.Paths.AccountDir(constants.ToolClaude, name), acc); err != nil {
				t.Fatal(err)
			}
		}
		path := filepath.Join(app.Env.Home, ".claude", "usage-exact.json")
		writeFile(t, path, `{"fiveHour":{"usedPercent":16,"resetsAt":1800000000}}`)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		st := state.New()
		st.Active[constants.ToolClaude] = "main"
		return app, path, st
	}
	window := []usagelimit.Window{{ID: constants.UsageWindowFiveHour, UsedPercent: 90, ResetsAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}}
	claudeTarget := map[string]string{constants.ToolClaude: "alt"}

	t.Run("records the outgoing account's file", func(t *testing.T) {
		app, path, st := claudeApp(t)
		app.recordSharedUsage(ctx, st, claudeTarget)
		cache, _ := loadUsageCache(app.Paths.UsageCacheFile())
		if entry, ok := cache.byAccount(constants.ToolClaude, "main"); !ok || entry.SourcePath != path || entry.ModTimeUnix != mtime.UnixNano() {
			t.Fatalf("entry = %+v, %v", entry, ok)
		}
	})
	t.Run("does not relabel a file another account owns", func(t *testing.T) {
		app, path, st := claudeApp(t)
		saveUsageCache(app.Paths.UsageCacheFile(), usageCache{Entries: []usageEntry{{
			Tool: constants.ToolClaude, Account: "side", Origin: constants.UsageOriginLocal,
			SourcePath: path, ModTimeUnix: mtime.UnixNano(), ObservedAt: mtime, Windows: window,
		}}})
		app.recordSharedUsage(ctx, st, claudeTarget)
		cache, _ := loadUsageCache(app.Paths.UsageCacheFile())
		if _, ok := cache.byAccount(constants.ToolClaude, "main"); ok {
			t.Fatalf("main took side's file: %+v", cache.Entries)
		}
		if entry, ok := cache.byAccount(constants.ToolClaude, "side"); !ok || entry.SourcePath != path {
			t.Fatalf("side lost its file: %+v", cache.Entries)
		}
	})
	t.Run("does not overwrite a newer reading", func(t *testing.T) {
		app, _, st := claudeApp(t)
		saveUsageCache(app.Paths.UsageCacheFile(), usageCache{Entries: []usageEntry{{
			Tool: constants.ToolClaude, Account: "main", Origin: constants.UsageOriginRemote,
			ObservedAt: mtime.Add(time.Hour), Windows: window,
		}}})
		app.recordSharedUsage(ctx, st, claudeTarget)
		cache, _ := loadUsageCache(app.Paths.UsageCacheFile())
		if entry, _ := cache.byAccount(constants.ToolClaude, "main"); entry.Origin != constants.UsageOriginRemote || entry.SourcePath != "" {
			t.Fatalf("the newer remote reading was replaced: %+v", entry)
		}
	})
	t.Run("does nothing when the tool is not switching", func(t *testing.T) {
		app, _, st := claudeApp(t)
		app.recordSharedUsage(ctx, st, map[string]string{constants.ToolCodex: "main"})
		if _, err := os.Stat(app.Paths.UsageCacheFile()); err == nil {
			t.Fatal("a cache was written for a tool that is not switching")
		}
	})
	t.Run("codex veto", func(t *testing.T) {
		app := testApp(t, nil)
		_, st := seedVetoCodex(t, app, vetoMainID, vetoSideID)
		writeRollout(t, sharedCodexHome(app), vetoMainID)
		app.recordSharedUsage(ctx, st, map[string]string{constants.ToolCodex: "main"})
		cache, _ := loadUsageCache(app.Paths.UsageCacheFile())
		if entry, ok := cache.byAccount(constants.ToolCodex, "side"); ok {
			t.Fatalf("side recorded a rollout main created: %+v", entry)
		}
	})
}
