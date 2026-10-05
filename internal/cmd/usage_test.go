package cmd

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter/claude"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/state"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
	"github.com/webkaz-labs/kagikae/internal/usagelimit"
)

func TestUsageStaysWithTheAccountThatWroteTheFile(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captured := []account.Account{
		{Tool: constants.ToolClaude, Name: "main"},
		{Tool: constants.ToolClaude, Name: "side"},
	}
	st := state.New()
	st.Active[constants.ToolClaude] = "main"
	path := filepath.Join(app.Env.Home, ".claude", "usage-exact.json")
	body := func(five, week int) string {
		return `{"schemaVersion":2,"fiveHour":{"usedPercent":` + strconv.Itoa(five) + `,"resetsAt":1800000000},"sevenDay":{"usedPercent":` + strconv.Itoa(week) + `,"resetsAt":1800003600}}`
	}
	writeFile(t, path, body(16, 95))

	views := app.accountUsages(ctx, captured, st)
	main := views[toolAccount{constants.ToolClaude, "main"}]
	if main.Source != constants.UsageSourceLocal || usagelimit.Format(main.Windows) != "5h 16% · 7d 95%" {
		t.Fatalf("main = %+v (%s)", main, usagelimit.Format(main.Windows))
	}
	if _, ok := views[toolAccount{constants.ToolClaude, "side"}]; ok {
		t.Fatal("side must not take the shared home's file")
	}

	// Switching the active account does not rewrite the file, so the reading
	// stays with the account that owned it.
	st.Active[constants.ToolClaude] = "side"
	views = app.accountUsages(ctx, captured, st)
	if views[toolAccount{constants.ToolClaude, "main"}].Source != constants.UsageSourceLocal {
		t.Fatalf("unchanged file stays with main: %+v", views[toolAccount{constants.ToolClaude, "main"}])
	}
	if _, ok := views[toolAccount{constants.ToolClaude, "side"}]; ok {
		t.Fatal("side must not inherit main's unchanged file")
	}

	writeFile(t, path, body(3, 4))
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	views = app.accountUsages(ctx, captured, st)
	side := views[toolAccount{constants.ToolClaude, "side"}]
	if side.Source != constants.UsageSourceLocal || usagelimit.Format(side.Windows) != "5h 3% · 7d 4%" {
		t.Fatalf("rewritten file belongs to the active account: %+v (%s)", side, usagelimit.Format(side.Windows))
	}
	main = views[toolAccount{constants.ToolClaude, "main"}]
	if main.Source != constants.UsageSourceCache || usagelimit.Format(main.Windows) != "5h 16% · 7d 95%" {
		t.Fatalf("main keeps the previous reading: %+v (%s)", main, usagelimit.Format(main.Windows))
	}
}

func TestLsAndStatusShowLocalUsage(t *testing.T) {
	// bare ls resolves places from cwd; keep it off the real checkout.
	chdirTemp(t)
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText, NoColor: true}
	seedClaude(t, app, mainToken, "main-uuid")
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
		t.Fatalf("capture: %s", out)
	}
	st := state.New()
	st.Active[constants.ToolClaude] = "main"
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(app.Env.Home, ".claude", "usage-exact.json"),
		`{"schemaVersion":2,"fiveHour":{"usedPercent":16,"resetsAt":1800000000},"sevenDay":{"usedPercent":95,"resetsAt":1800003600}}`)
	_, text := captureStdout(t, func() int { return runLs(ctx, app, opts) })
	for _, want := range []string{"Limit", "5h 16%", "7d 95%"} {
		if !strings.Contains(text, want) {
			t.Fatalf("ls missing %q:\n%s", want, text)
		}
	}
	code, out := captureStdout(t, func() int { return runLs(ctx, app, commonOpts{Format: formatJSON}) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, `"source": "local"`) || !strings.Contains(out, `"id": "five_hour"`) {
		t.Fatalf("ls JSON missing usage: %s", out)
	}
	_, status := captureStdout(t, func() int { return runStatus(ctx, app, opts) })
	if !strings.Contains(status, "5h 16%") || !strings.Contains(status, "7d 95%") {
		t.Fatalf("status missing windows:\n%s", status)
	}
}

// A remembered reading (the usage cache in the shape the listing writes it,
// observed_at with nanoseconds) shows its age only under --full, and --json
// is byte-identical with and without it.
func TestFullListingsShowTheRememberedReadingAge(t *testing.T) {
	// bare ls resolves places from cwd; keep it off the real checkout.
	chdirTemp(t)
	app := testApp(t, nil)
	ctx := context.Background()
	seedClaude(t, app, mainToken, "main-uuid")
	if code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText, NoColor: true}, "claude", "main")
	}); code != constants.ExitOK {
		t.Fatalf("capture: %s", out)
	}
	st := state.New()
	st.Active[constants.ToolClaude] = "main"
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}
	// app.Now() is 2026-06-11T01:23:45Z: observed 22h13m29s earlier, the
	// five-hour window already reset, the seven-day one resets in 4d8h36m.
	writeFile(t, app.Paths.UsageCacheFile(), `{
  "schema_version": 1,
  "entries": [
    {
      "tool": "claude",
      "account": "main",
      "origin": "local",
      "source_path": "`+filepath.Join(app.Env.Home, ".claude", "usage-exact.json")+`",
      "mod_time_unix": 1781061015123456789,
      "observed_at": "2026-06-10T03:10:15.123456789Z",
      "windows": [
        {"id": "five_hour", "used_percent": 90, "resets_at": "2026-06-10T05:00:00Z", "minutes": 300},
        {"id": "seven_day", "used_percent": 23, "resets_at": "2026-06-15T10:00:00Z", "minutes": 10080}
      ]
    }
  ]
}
`)
	text := func(full bool) commonOpts { return commonOpts{Format: formatText, NoColor: true, Full: full} }
	const plain, aged = "7d 23% (4d8h)", "7d 23% (4d8h) · 22h13m ago"
	for name, run := range map[string]func(commonOpts) int{
		"ls":       func(o commonOpts) int { return runLs(ctx, app, o) },
		"accounts": func(o commonOpts) int { return runAccounts(ctx, app, o) },
		"status":   func(o commonOpts) int { return runStatus(ctx, app, o) },
	} {
		code, out := captureStdout(t, func() int { return run(text(false)) })
		mustExit(t, constants.ExitOK, code, out)
		if !strings.Contains(out, plain) || strings.Contains(out, " ago") || strings.Contains(out, "5h ") {
			t.Fatalf("%s without --full:\n%s", name, out)
		}
		code, out = captureStdout(t, func() int { return run(text(true)) })
		mustExit(t, constants.ExitOK, code, out)
		if !strings.Contains(out, aged) || strings.Count(out, " ago") != 1 || strings.Contains(out, "5h ") {
			t.Fatalf("%s --full:\n%s", name, out)
		}
	}

	for name, run := range map[string]func(commonOpts) int{
		"ls":       func(o commonOpts) int { return runLs(ctx, app, o) },
		"accounts": func(o commonOpts) int { return runAccounts(ctx, app, o) },
		"status":   func(o commonOpts) int { return runStatus(ctx, app, o) },
	} {
		code, plainJSON := captureStdout(t, func() int { return run(commonOpts{Format: formatJSON}) })
		mustExit(t, constants.ExitOK, code, plainJSON)
		code, fullJSON := captureStdout(t, func() int { return run(commonOpts{Format: formatJSON, Full: true}) })
		mustExit(t, constants.ExitOK, code, fullJSON)
		if plainJSON != fullJSON {
			t.Fatalf("%s --json changed under --full:\n%s\n---\n%s", name, plainJSON, fullJSON)
		}
		if !strings.Contains(plainJSON, `"observed_at": "2026-06-10T03:10:15Z"`) || strings.Contains(plainJSON, "ago") {
			t.Fatalf("%s --json:\n%s", name, plainJSON)
		}
	}

	l10ntest.UseJapanese(t)
	code, out := captureStdout(t, func() int { return runLs(ctx, app, text(true)) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "7d 23% (4d8h) · 22h13m 前") {
		t.Fatalf("ja ls --full:\n%s", out)
	}
}

func TestUsageIsolatedHomeBelongsToThatAccount(t *testing.T) {
	app := testApp(t, nil)
	st := state.New()
	st.Active[constants.ToolClaude] = "main"
	path := filepath.Join(app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "side"), "usage-exact.json")
	writeFile(t, path, `{"fiveHour":{"usedPercent":7,"resetsAt":1800000000}}`)
	views := app.accountUsages(context.Background(), []account.Account{
		{Tool: constants.ToolClaude, Name: "main"},
		{Tool: constants.ToolClaude, Name: "side"},
	}, st)
	side := views[toolAccount{constants.ToolClaude, "side"}]
	if side.Source != constants.UsageSourceLocal || usagelimit.Format(side.Windows) != "5h 7%" {
		t.Fatalf("side = %+v (%s)", side, usagelimit.Format(side.Windows))
	}
	if _, ok := views[toolAccount{constants.ToolClaude, "main"}]; ok {
		t.Fatal("main must not receive side's isolated home")
	}
}

func TestUsageProbeDoesNotLeakTheTokenAndUsesTheCache(t *testing.T) {
	// bare ls resolves places from cwd; keep it off the real checkout.
	chdirTemp(t)
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText, NoColor: true}
	const canary = "sk-ant-oat01-USAGE-CANARY-zzzz"
	seedClaudeOAuth(t, app, `{"accessToken":"`+canary+`","refreshToken":"refresh-LEAK-CANARY","expiresAt":1800000000000}`)
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
		t.Fatalf("capture: %s", out)
	}
	var hits int
	app.usageClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hits++
		if r.URL.Host != "api.anthropic.com" {
			t.Errorf("host = %s", r.URL.Host)
		}
		if !strings.Contains(r.Header.Get("Authorization"), canary) {
			t.Error("probe did not present the access token")
		}
		body := `{"five_hour":{"utilization":10,"resets_at":"2026-07-01T00:00:00Z"},"seven_day":{"utilization":20,"resets_at":"2026-07-08T00:00:00Z"}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}
	code, out := captureStdout(t, func() int { return runLs(ctx, app, opts) })
	mustExit(t, constants.ExitOK, code, out)
	if hits != 1 {
		t.Fatalf("hits = %d, want 1", hits)
	}
	if !strings.Contains(out, "5h 10%") || !strings.Contains(out, "7d 20%") {
		t.Fatalf("ls missing the probed windows:\n%s", out)
	}
	if strings.Contains(out, canary) || strings.Contains(out, "refresh-LEAK-CANARY") {
		t.Fatalf("listing leaked a token:\n%s", out)
	}
	cache, err := os.ReadFile(app.Paths.UsageCacheFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cache), canary) || strings.Contains(string(cache), "refresh-LEAK") {
		t.Fatalf("cache leaked a token: %s", cache)
	}
	code, out = captureStdout(t, func() int { return runLs(ctx, app, opts) })
	mustExit(t, constants.ExitOK, code, out)
	if hits != 1 {
		t.Fatalf("a fresh cache must not probe again, hits = %d", hits)
	}
}

func TestUsageProbeRefusesAForeignHost(t *testing.T) {
	app := testApp(t, nil)
	const canary = "sk-ant-oat01-USAGE-CANARY-zzzz"
	app.usageClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("a foreign host must not be contacted")
		return nil, nil
	})}
	req, err := http.NewRequest(http.MethodGet, "https://evil.example/usage", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+canary)
	if _, ok := app.doUsageProbe(context.Background(), claude.Claude{}, req); ok {
		t.Fatal("foreign host was accepted")
	}
}

func TestUsageResetFileDropsTheRememberedPercent(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captured := []account.Account{{Tool: constants.ToolClaude, Name: "main"}}
	st := state.New()
	st.Active[constants.ToolClaude] = "main"
	path := filepath.Join(app.Env.Home, ".claude", "usage-exact.json")
	writeFile(t, path, `{"fiveHour":{"usedPercent":16,"resetsAt":1800000000}}`)
	views := app.accountUsages(ctx, captured, st)
	if usagelimit.Format(views[toolAccount{constants.ToolClaude, "main"}].Windows) != "5h 16%" {
		t.Fatalf("before reset: %+v", views)
	}

	// The tool rewrote the file after the window closed. The old percent
	// must not stay on the account.
	writeFile(t, path, `{"fiveHour":{"usedPercent":16,"resetsAt":1700000000}}`)
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	views = app.accountUsages(ctx, captured, st)
	if _, ok := views[toolAccount{constants.ToolClaude, "main"}]; ok {
		t.Fatalf("a reset window must not keep the old percent: %+v", views)
	}
}

func TestUsageResetFileProbesOnceThenUsesTheCache(t *testing.T) {
	// bare ls resolves places from cwd; keep it off the real checkout.
	chdirTemp(t)
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText, NoColor: true}
	const canary = "sk-ant-oat01-USAGE-CANARY-zzzz"
	seedClaudeOAuth(t, app, `{"accessToken":"`+canary+`","refreshToken":"refresh-LEAK-CANARY","expiresAt":1800000000000}`)
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
		t.Fatalf("capture: %s", out)
	}
	path := filepath.Join(app.Env.Home, ".claude", "usage-exact.json")
	writeFile(t, path, `{"fiveHour":{"usedPercent":90,"resetsAt":1700000000}}`)
	// The file is older than the clock listings use, so a reading fetched
	// after it was written stays newer than the file.
	written := app.Now().Add(-time.Hour)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
	var hits int
	app.usageClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		hits++
		body := `{"five_hour":{"utilization":4,"resets_at":"2026-07-01T00:00:00Z"}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}
	code, out := captureStdout(t, func() int { return runLs(ctx, app, opts) })
	mustExit(t, constants.ExitOK, code, out)
	if hits != 1 || !strings.Contains(out, "5h 4%") {
		t.Fatalf("hits = %d, ls:\n%s", hits, out)
	}
	if strings.Contains(out, canary) {
		t.Fatalf("listing leaked a token:\n%s", out)
	}
	code, out = captureStdout(t, func() int { return runLs(ctx, app, opts) })
	mustExit(t, constants.ExitOK, code, out)
	if hits != 1 {
		t.Fatalf("a reset file must not probe on every listing, hits = %d", hits)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
