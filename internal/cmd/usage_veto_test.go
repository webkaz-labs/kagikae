package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/constants"
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

func TestCodexUsageVetoesAReadingAnotherCapturedAccountCreated(t *testing.T) {
	app := testApp(t, nil)
	captured, st := seedVetoCodex(t, app, vetoMainID, vetoSideID)
	path := writeRollout(t, sharedCodexHome(app), vetoMainID)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// A listing before the veto existed remembered this file on side.
	writeFile(t, app.Paths.UsageCacheFile(), `{"schema_version": 1, "entries": [
  {"tool": "codex", "account": "side", "origin": "local", "source_path": "`+path+`",
   "mod_time_unix": `+strconv.FormatInt(info.ModTime().UnixNano(), 10)+`, "observed_at": "2026-06-11T00:00:00Z",
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
