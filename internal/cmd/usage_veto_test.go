package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/secret"
	"github.com/webkaz-labs/kagikae/internal/state"
	"github.com/webkaz-labs/kagikae/internal/usagelimit"
)

// Fictional account ids of the two captured codex logins and of a login kae
// has not captured. They must reach no output, error or cache.
const (
	vetoMainID     = "acct-veto-main-0000"
	vetoSideID     = "acct-veto-side-1111"
	vetoStrangerID = "acct-veto-stranger-2222"
)

// seedVetoCodex records codex main and side as captured, each with a
// credential naming the given account id ("" for none), side active, and
// returns them in the shape a listing passes to accountUsages.
func seedVetoCodex(t *testing.T, app *App, mainID, sideID string) ([]account.Account, *state.State) {
	t.Helper()
	be, err := app.secretBackend()
	if err != nil {
		t.Fatal(err)
	}
	var captured []account.Account
	for _, acct := range []struct{ name, id string }{{"main", mainID}, {"side", sideID}} {
		ref := account.SecretRef(constants.ToolCodex, acct.name, "auth")
		auth := `{"auth_mode":"chatgpt","tokens":{"access_token":"codex-` + acct.name + `-token"}}`
		if acct.id != "" {
			auth = codexChatGPTAuth(acct.id, "codex-"+acct.name+"-token")
		}
		if err := be.Set(context.Background(), ref, []byte(auth)); err != nil {
			t.Fatal(err)
		}
		acc := account.Account{
			Version: 1, Tool: constants.ToolCodex, Name: acct.name,
			Artifacts: map[string]account.Artifact{"auth": {Kind: "file", SecretRef: ref, Present: true}},
		}
		if err := account.Save(app.Paths.AccountDir(constants.ToolCodex, acct.name), acc); err != nil {
			t.Fatal(err)
		}
		captured = append(captured, acc)
	}
	st := state.New()
	st.Active[constants.ToolCodex] = "side"
	return captured, st
}

// writeRollout writes a codex session rollout under home: a session_meta line
// naming creator (none when ""), then one rate_limits event.
func writeRollout(t *testing.T, home, creator string) string {
	t.Helper()
	path := filepath.Join(home, "sessions", "2026", "10", "06", "rollout-veto.jsonl")
	meta := `{"type":"session_meta","payload":{"id":"thread-1","cli_version":"0.160.0"}}`
	if creator != "" {
		meta = `{"type":"session_meta","payload":{"id":"thread-1","cli_version":"0.160.0","creator_account_id":"` + creator + `"}}`
	}
	event := `{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"plan_type":"plus","primary":{"used_percent":42,"window_minutes":300,"resets_at":1800000000},"secondary":{"used_percent":7,"window_minutes":10080,"resets_at":1800003600}}}}`
	writeFile(t, path, meta+"\n"+event+"\n")
	return path
}

func sharedCodexHome(app *App) string { return filepath.Join(app.Env.Home, ".codex") }

// A listing before the veto existed remembered a reading of the rollout on
// side, at the rollout's current mtime or at an earlier one. Either way the
// veto drops it: the creator does not change within a file.
func TestCodexUsageVetoesAReadingAnotherCapturedAccountCreated(t *testing.T) {
	for name, age := range map[string]time.Duration{"same mtime": 0, "earlier mtime": time.Hour} {
		t.Run(name, func(t *testing.T) {
			app := testApp(t, nil)
			captured, st := seedVetoCodex(t, app, vetoMainID, vetoSideID)
			path := writeRollout(t, sharedCodexHome(app), vetoMainID)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			remembered := info.ModTime().Add(-age).UnixNano()
			writeFile(t, app.Paths.UsageCacheFile(), `{"schema_version": 1, "entries": [
  {"tool": "codex", "account": "side", "origin": "local", "source_path": "`+path+`",
   "mod_time_unix": `+strconv.FormatInt(remembered, 10)+`, "observed_at": "2026-06-11T00:00:00Z",
   "windows": [{"id": "five_hour", "used_percent": 42, "resets_at": "2027-01-15T08:00:00Z", "minutes": 300}]}]}`)

			views := app.accountUsages(context.Background(), captured, st)
			if view, ok := views[toolAccount{constants.ToolCodex, "side"}]; ok {
				t.Fatalf("side must not take main's reading: %+v", view)
			}
			if view, ok := views[toolAccount{constants.ToolCodex, "main"}]; ok {
				t.Fatalf("a veto must not re-attribute the reading to main: %+v", view)
			}
			cache := readFile(t, app.Paths.UsageCacheFile())
			if strings.Contains(cache, path) || strings.Contains(cache, `"side"`) {
				t.Fatalf("a vetoed reading must leave the cache:\n%s", cache)
			}
		})
	}
}

// countSecretReads installs a backend that counts Get calls.
func countSecretReads(t *testing.T, app *App) *countingBackend {
	t.Helper()
	fileBE, err := secret.Resolve(secret.BackendFile, app.Env.GOOS, app.Paths.SecretsDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	counter := &countingBackend{Backend: fileBE, gets: map[string]int{}}
	app.backendForTest = counter
	return counter
}

func (c *countingBackend) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, v := range c.gets {
		n += v
	}
	return n
}

// The veto opens the secret store only for a shared home's rollout that names
// a creator, and only for a tool that records one.
func TestCodexUsageVetoReadsNoSecretWithoutACreator(t *testing.T) {
	app := testApp(t, nil)
	counter := countSecretReads(t, app)
	captured, st := seedVetoCodex(t, app, vetoMainID, vetoSideID)
	writeRollout(t, sharedCodexHome(app), "")
	captured = append(captured, account.Account{
		Version: 1, Tool: constants.ToolClaude, Name: "main",
		Artifacts: map[string]account.Artifact{"oauth": {Kind: "keychain", SecretRef: "claude/main/oauth", Present: true}},
	})
	st.Active[constants.ToolClaude] = "main"
	writeFile(t, filepath.Join(app.Env.Home, ".claude", "usage-exact.json"),
		`{"fiveHour":{"usedPercent":16,"resetsAt":1800000000}}`)
	counter.reset()
	views := app.accountUsages(context.Background(), captured, st)
	if len(views) != 2 {
		t.Fatalf("both readings are kept: %+v", views)
	}
	if n := counter.total(); n != 0 {
		t.Fatalf("secret reads = %d, want 0: %v", n, counter.gets)
	}
}

// A listing reads each captured credential once, whether or not the veto
// needs the keys: the veto reuses what the freshness column read.
func TestCodexUsageVetoAddsNoSecretReadToAListing(t *testing.T) {
	reads := func(creator string) int {
		chdirTemp(t)
		app := testApp(t, nil)
		counter := countSecretReads(t, app)
		_, st := seedVetoCodex(t, app, vetoMainID, vetoSideID)
		if err := state.Save(app.Paths.StateFile(), st); err != nil {
			t.Fatal(err)
		}
		if creator != "none" {
			writeRollout(t, sharedCodexHome(app), creator)
		}
		counter.reset()
		code, out := captureStdout(t, func() int { return runLs(context.Background(), app, commonOpts{Format: formatText, NoColor: true}) })
		mustExit(t, constants.ExitOK, code, out)
		return counter.total()
	}
	baseline := reads("none")
	if baseline == 0 {
		t.Fatal("the listing's freshness column reads no credential; the comparison proves nothing")
	}
	for _, creator := range []string{vetoSideID, vetoMainID} {
		if got := reads(creator); got != baseline {
			t.Fatalf("secret reads with a creator = %d, without a rollout = %d", got, baseline)
		}
	}
}

// One listing reads a tool's keys once, however many readings ask.
func TestCodexUsageVetoReadsAToolsKeysOnce(t *testing.T) {
	app := testApp(t, nil)
	counter := countSecretReads(t, app)
	captured, _ := seedVetoCodex(t, app, vetoMainID, vetoSideID)
	path := writeRollout(t, sharedCodexHome(app), vetoMainID)
	ad, err := adapter.ForTool(constants.ToolCodex)
	if err != nil {
		t.Fatal(err)
	}
	keys := &credentialKeys{app: app, captured: captured}
	reading := usagelimit.Reading{Path: path}
	counter.reset()
	for range 3 {
		if !keys.vetoes(context.Background(), ad, toolAccount{constants.ToolCodex, "side"}, reading) {
			t.Fatal("the reading is vetoed")
		}
	}
	if n := counter.total(); n != 2 {
		t.Fatalf("secret reads = %d, want one per captured account: %v", n, counter.gets)
	}
}

func TestCodexUsageKeepsAReadingTheAttributedAccountCreated(t *testing.T) {
	app := testApp(t, nil)
	captured, st := seedVetoCodex(t, app, vetoMainID, vetoSideID)
	writeRollout(t, sharedCodexHome(app), vetoSideID)
	views := app.accountUsages(context.Background(), captured, st)
	side := views[toolAccount{constants.ToolCodex, "side"}]
	if side.Source != constants.UsageSourceLocal || usagelimit.Format(side.Windows) != "5h 42% · 7d 7%" {
		t.Fatalf("side = %+v (%s)", side, usagelimit.Format(side.Windows))
	}
}

// Without a positive match on another captured account, the reading keeps the
// attribution it had before the veto existed.
func TestCodexUsageKeepsAReadingWithoutAPositiveMatch(t *testing.T) {
	for _, tc := range []struct {
		name, mainID, sideID, creator string
	}{
		{"no creator (0.154 and earlier)", vetoMainID, vetoSideID, ""},
		{"creator matches no captured account", vetoMainID, vetoSideID, vetoStrangerID},
		{"attributed account names no id", vetoMainID, "", vetoMainID},
		{"creator is the attributed account's, shared by another", vetoSideID, vetoSideID, vetoSideID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testApp(t, nil)
			captured, st := seedVetoCodex(t, app, tc.mainID, tc.sideID)
			writeRollout(t, sharedCodexHome(app), tc.creator)
			views := app.accountUsages(context.Background(), captured, st)
			side := views[toolAccount{constants.ToolCodex, "side"}]
			if side.Source != constants.UsageSourceLocal || usagelimit.Format(side.Windows) != "5h 42% · 7d 7%" {
				t.Fatalf("side = %+v (%s)", side, usagelimit.Format(side.Windows))
			}
			cache := readFile(t, app.Paths.UsageCacheFile())
			if !strings.Contains(cache, `"side"`) {
				t.Fatalf("a kept reading is cached:\n%s", cache)
			}
		})
	}
}

// An isolated home belongs to the account in its path, whoever created the
// session in it.
func TestCodexUsageVetoLeavesAnIsolatedHomeAlone(t *testing.T) {
	app := testApp(t, nil)
	captured, st := seedVetoCodex(t, app, vetoMainID, vetoSideID)
	writeRollout(t, app.Paths.GlobalIsolatedHomeDir(constants.ToolCodex, "side"), vetoMainID)
	views := app.accountUsages(context.Background(), captured, st)
	side := views[toolAccount{constants.ToolCodex, "side"}]
	if side.Source != constants.UsageSourceLocal || usagelimit.Format(side.Windows) != "5h 42% · 7d 7%" {
		t.Fatalf("side = %+v (%s)", side, usagelimit.Format(side.Windows))
	}
}

// No account id reaches a listing's output, its errors or the cache, whether
// the reading is vetoed or kept.
func TestCodexUsageVetoDoesNotLeakAccountIDs(t *testing.T) {
	for name, creator := range map[string]string{"vetoed": vetoMainID, "kept": vetoSideID} {
		t.Run(name, func(t *testing.T) {
			chdirTemp(t)
			app := testApp(t, nil)
			_, st := seedVetoCodex(t, app, vetoMainID, vetoSideID)
			if err := state.Save(app.Paths.StateFile(), st); err != nil {
				t.Fatal(err)
			}
			writeRollout(t, sharedCodexHome(app), creator)
			ctx := context.Background()
			var outputs []string
			for _, opts := range []commonOpts{
				{Format: formatText, NoColor: true},
				{Format: formatText, NoColor: true, Full: true},
				{Format: formatJSON},
			} {
				code, stdout, stderr := captureBoth(t, func() int { return runLs(ctx, app, opts) })
				mustExit(t, constants.ExitOK, code, stdout+stderr)
				outputs = append(outputs, stdout, stderr)
				code, stdout, stderr = captureBoth(t, func() int { return runStatus(ctx, app, opts) })
				mustExit(t, constants.ExitOK, code, stdout+stderr)
				outputs = append(outputs, stdout, stderr)
			}
			if creator == vetoSideID && !strings.Contains(strings.Join(outputs, "\n"), "5h 42%") {
				t.Fatalf("the kept reading is not listed:\n%s", strings.Join(outputs, "\n"))
			}
			if creator == vetoMainID && strings.Contains(strings.Join(outputs, "\n"), "5h 42%") {
				t.Fatalf("the vetoed reading is listed:\n%s", strings.Join(outputs, "\n"))
			}
			if cache, err := os.ReadFile(app.Paths.UsageCacheFile()); err == nil {
				outputs = append(outputs, string(cache))
			}
			for _, out := range outputs {
				for _, id := range []string{vetoMainID, vetoSideID} {
					if strings.Contains(out, id) {
						t.Fatalf("account id leaked:\n%s", out)
					}
				}
			}
		})
	}
}
