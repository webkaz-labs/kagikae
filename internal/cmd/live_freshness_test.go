package cmd

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/state"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The incident: claude/main is active, its snapshot is good, and the live login
// claude runs on is the tombstone a failed refresh leaves. kae status said
// Credential=ok and doctor said nothing (docs/CLI.md § Credential freshness in
// listings, "The active account's live login").

const liveSecretToken = "sk-ant-oat01-LIVE-SECRET-ffff"

// credentialChecks returns doctor's credential_stale / credential_expiring
// messages for claude.
func credentialChecks(t *testing.T, app *App) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, c := range buildDoctor(context.Background(), app, constants.ToolClaude, doctorOptIns{}).Checks {
		if c.Code == constants.CheckCredentialStale || c.Code == constants.CheckCredentialExpiring {
			out[c.Code] = append(out[c.Code], c.Message.Error())
		}
	}
	return out
}

func claudeStatusRow(t *testing.T, app *App) toolStatus {
	t.Helper()
	report, err := buildStatus(context.Background(), app)
	if err != nil {
		t.Fatal(err)
	}
	for _, ts := range report.Tools {
		if ts.Tool == constants.ToolClaude {
			return ts
		}
	}
	t.Fatal("no claude row")
	return toolStatus{}
}

func TestActiveAccountLiveCredential(t *testing.T) {
	const liveMarker = "the live claude credential of active account main"
	for _, tc := range []struct {
		name       string
		snapshot   func(now time.Time) string // claudeAiOauth object captured as main
		live       func(now time.Time) string // claudeAiOauth object left live afterwards
		liveState  string                     // status live_credential
		snapState  string                     // status credential
		code       string                     // the live doctor finding, "" for none
		want       []string                   // in that finding
		cell       string                     // the status Credential cell
		findingsOf map[string]int             // credential checks by code
	}{
		{
			name:      "live tombstone, fresh snapshot",
			snapshot:  func(now time.Time) string { return refreshBackedClaudeCred(now, 30*24*time.Hour) },
			live:      func(time.Time) string { return `{"accessToken":"","refreshToken":"","expiresAt":0}` },
			liveState: constants.CredentialStale, snapState: constants.CredentialOK,
			code: constants.CheckCredentialStale,
			want: []string{liveMarker + " is stale", "failed token refresh", "run: kae relogin claude"},
			cell: "re-login now (live login)", findingsOf: map[string]int{constants.CheckCredentialStale: 1},
		},
		{
			name:      "live expiring, fresh snapshot",
			snapshot:  func(now time.Time) string { return refreshBackedClaudeCred(now, 30*24*time.Hour) },
			live:      func(now time.Time) string { return endOfLifeClaudeCred(now, 3*24*time.Hour, liveSecretToken) },
			liveState: constants.CredentialExpiring, snapState: constants.CredentialOK,
			code: constants.CheckCredentialExpiring,
			want: []string{liveMarker + " needs an interactive re-login in 3 day(s)", "run: kae relogin claude"},
			cell: "3 day(s) left (live login)", findingsOf: map[string]int{constants.CheckCredentialExpiring: 1},
		},
		{
			name:      "live fresh",
			snapshot:  func(now time.Time) string { return refreshBackedClaudeCred(now, 30*24*time.Hour) },
			live:      func(now time.Time) string { return endOfLifeClaudeCred(now, 20*24*time.Hour, liveSecretToken) },
			liveState: constants.CredentialOK, snapState: constants.CredentialOK,
			cell: constants.CredentialOK, findingsOf: map[string]int{},
		},
		{
			// One login nearing one deadline is one finding, not two.
			name:      "as bad as the snapshot",
			snapshot:  func(now time.Time) string { return endOfLifeClaudeCred(now, 3*24*time.Hour, "a") },
			live:      func(now time.Time) string { return endOfLifeClaudeCred(now, 3*24*time.Hour, "a") },
			liveState: constants.CredentialExpiring, snapState: constants.CredentialExpiring,
			cell: "3 day(s) left", findingsOf: map[string]int{constants.CheckCredentialExpiring: 1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testApp(t, nil)
			chdirTemp(t)
			withTerminalColumns(t, 0)
			now := app.Now()
			seedClaude(t, app, "", "main-uuid")
			seedClaudeOAuth(t, app, tc.snapshot(now))
			mustExit(t, constants.ExitOK, runCaptureQuiet(t, app, "main"), "capture main")
			seedClaudeOAuth(t, app, tc.live(now))

			checks := credentialChecks(t, app)
			for _, code := range []string{constants.CheckCredentialStale, constants.CheckCredentialExpiring} {
				if len(checks[code]) != tc.findingsOf[code] {
					t.Fatalf("%s findings: %q, want %d", code, checks[code], tc.findingsOf[code])
				}
			}
			var live string
			for _, msgs := range checks {
				for _, m := range msgs {
					if strings.Contains(m, liveMarker) {
						live = m
					}
					if strings.Contains(m, liveSecretToken) || strings.Contains(m, "rt-") {
						t.Fatalf("a credential finding leaked a token: %q", m)
					}
				}
			}
			if (live != "") != (tc.code != "") {
				t.Fatalf("live finding %q, want one=%v", live, tc.code != "")
			}
			if tc.code != "" && !slicesContain(checks[tc.code], live) {
				t.Fatalf("live finding under the wrong code: %v", checks)
			}
			for _, want := range tc.want {
				if !strings.Contains(live, want) {
					t.Errorf("live finding must contain %q: %q", want, live)
				}
			}
			if strings.Contains(live, "--restore") {
				t.Errorf("the active account's remedy is kae relogin: %q", live)
			}

			row := claudeStatusRow(t, app)
			if row.Credential != tc.snapState || row.LiveCredential != tc.liveState {
				t.Fatalf("credential=%q live_credential=%q, want %q / %q", row.Credential, row.LiveCredential, tc.snapState, tc.liveState)
			}
			if cell, _ := statusCredentialCell(row, now); cell != tc.cell {
				t.Errorf("Credential cell %q, want %q", cell, tc.cell)
			}
			_, out := captureStdout(t, func() int { return runStatus(context.Background(), app, commonOpts{Format: formatText}) })
			if !strings.Contains(out, tc.cell) || strings.Contains(out, liveSecretToken) {
				t.Errorf("status text must show %q and no token:\n%s", tc.cell, out)
			}
		})
	}
}

func slicesContain(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// `--json` grows two omitempty fields and nothing else: the snapshot pair keeps its
// meaning and schema_version stays 1.
func TestStatusJSONLiveCredentialIsAdditive(t *testing.T) {
	app := expiredActiveMain(t)
	code, out := captureStdout(t, func() int { return runStatus(context.Background(), app, commonOpts{Format: formatJSON}) })
	mustExit(t, constants.ExitOK, code, out)
	var report struct {
		SchemaVersion int `json:"schema_version"`
		Tools         []map[string]any
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != constants.SchemaVersion {
		t.Fatalf("schema_version %d", report.SchemaVersion)
	}
	for _, row := range report.Tools {
		if row["tool"] != constants.ToolClaude {
			if _, ok := row["live_credential"]; ok {
				t.Errorf("%v has no active account, so no live_credential: %v", row["tool"], row)
			}
			continue
		}
		if row["credential"] != constants.CredentialOK || row["live_credential"] != constants.CredentialStale {
			t.Fatalf("claude row: %v", row)
		}
		// A tombstone has no deadline to name.
		if _, ok := row["live_relogin_by"]; ok {
			t.Errorf("a tombstone carries no live_relogin_by: %v", row)
		}
	}
	if strings.Contains(out, mainToken) {
		t.Fatalf("status leaked a token: %s", out)
	}
}

// An account that is not active is judged by its snapshot alone, and the live
// finding names the account the live store belongs to.
func TestLiveCredentialLeavesInactiveAccountsAlone(t *testing.T) {
	app := testApp(t, nil)
	chdirTemp(t)
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	captureClaudeAt(t, app, "side", sideToken, app.Now().Add(time.Hour))
	mustExit(t, constants.ExitOK, func() int {
		code, _ := captureStdout(t, func() int {
			return runSwitch(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "side")
		})
		return code
	}(), "switch to side")
	seedClaudeOAuth(t, app, `{"accessToken":"","refreshToken":"","expiresAt":0}`)

	checks := credentialChecks(t, app)
	if len(checks[constants.CheckCredentialStale]) != 1 ||
		!strings.Contains(checks[constants.CheckCredentialStale][0], "of active account side") {
		t.Fatalf("want one live finding for side: %v", checks)
	}
	items, _, err := buildAccountItemsWith(context.Background(), app, constants.ToolClaude, app.readState())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Credential != constants.CredentialOK {
			t.Errorf("kae ls stays snapshot-only: %s is %q", item.Account, item.Credential)
		}
	}
}

// The live half needs no secret backend; with none, every snapshot counts as
// unknown, so a dead live copy is still reported.
func TestLiveCredentialWithoutABackend(t *testing.T) {
	app := expiredActiveMain(t)
	app.Config.Security.SecretBackend = "unavailable-for-test"
	checks := credentialChecks(t, app)
	if len(checks[constants.CheckCredentialStale]) != 1 ||
		!strings.Contains(checks[constants.CheckCredentialStale][0], "the live claude credential of active account main is stale") {
		t.Fatalf("want the live finding: %v", checks)
	}
	if row := claudeStatusRow(t, app); row.LiveCredential != constants.CredentialStale || row.Credential != "" {
		t.Fatalf("credential=%q live_credential=%q", row.Credential, row.LiveCredential)
	}
}

// In global isolated mode the tool runs on a private home, so the real home's copy
// is not the one in use and neither surface judges it.
func TestLiveCredentialSkipsGlobalIsolatedMode(t *testing.T) {
	app := expiredActiveMain(t)
	if _, err := app.mutateState(func(st *state.State) {
		st.Synced = map[string]string{constants.ToolClaude: "main"}
	}); err != nil {
		t.Fatal(err)
	}
	for _, msgs := range credentialChecks(t, app) {
		for _, m := range msgs {
			if strings.Contains(m, "the live claude credential") {
				t.Fatalf("global isolated mode must not report the real home's copy: %q", m)
			}
		}
	}
	if row := claudeStatusRow(t, app); row.LiveCredential != "" {
		t.Fatalf("live_credential %q in global isolated mode", row.LiveCredential)
	}
}

// Inside a bound directory the row names the bound account, whose store may not be
// the one this shell reads, so the row's live state is left unjudged.
func TestStatusRowIsRealHome(t *testing.T) {
	app := testApp(t, nil)
	st := &state.State{Synced: map[string]string{constants.ToolCodex: "main"}}
	bound := map[string]string{constants.ToolCursor: "main"}
	isolatedHome := filepath.Join(app.Paths.IsolationDir(), "0123456789abcdef", constants.ToolClaude, "shared")
	app.Env.Getenv = func(key string) string {
		if key == isolationEnvVar(constants.ToolClaude) {
			return isolatedHome
		}
		return ""
	}
	for tool, want := range map[string]bool{
		constants.ToolOpencode: true,
		constants.ToolCodex:    false, // global isolated mode
		constants.ToolCursor:   false, // bound by this directory's fragment
		constants.ToolClaude:   false, // this shell points it at one of kae's homes
	} {
		if got := app.statusRowIsRealHome(tool, bound, st); got != want {
			t.Errorf("%s: real home=%v, want %v", tool, got, want)
		}
	}
}

func TestLiveCredentialInJapanese(t *testing.T) {
	app := expiredActiveMain(t)
	withTerminalColumns(t, 0)
	l10ntest.UseJapanese(t)
	var msg string
	for _, c := range buildDoctor(context.Background(), app, constants.ToolClaude, doctorOptIns{}).Checks {
		if c.Code == constants.CheckCredentialStale {
			msg = l10n.Render(c.Message)
		}
	}
	for _, want := range []string{"有効なアカウント main の現在の claude の認証情報は失効しています", "kae relogin claude を実行"} {
		if !strings.Contains(msg, want) {
			t.Errorf("doctor must say %q: %q", want, msg)
		}
	}
	_, out := captureStdout(t, func() int { return runStatus(context.Background(), app, commonOpts{Format: formatText}) })
	if !strings.Contains(out, "今すぐ再ログイン（現在のログイン）") {
		t.Errorf("status cell in Japanese:\n%s", out)
	}
}

// lockedSim serializes keychainSim for the concurrent Detects of kae status.
type lockedSim struct {
	mu  sync.Mutex
	sim *keychainSim
}

func (l *lockedSim) Run(ctx context.Context, name string, args ...string) (string, string, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sim.Run(ctx, name, args...)
}

func (l *lockedSim) RunInput(ctx context.Context, input string, name string, args ...string) (string, string, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sim.RunInput(ctx, input, name, args...)
}

func (l *lockedSim) reads() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := l.sim.readW
	l.sim.readW = 0
	return n
}

// Judging the live copy costs no `security` call beyond Detect's, in kae status and
// in kae doctor (the claude keychain driver).
func TestLiveCredentialAddsNoKeychainRead(t *testing.T) {
	sim := &lockedSim{sim: &keychainSim{}}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		chdirTemp(t)
		ctx := context.Background()
		sim.sim.present, sim.sim.account = true, "me"
		sim.sim.payload = claudeOAuthPayload(mainToken, app.Now().Add(time.Hour))
		mustExit(t, constants.ExitOK, runCaptureQuiet(t, app, "main"), "capture main")

		sim.reads()
		if _, err := detectTools(ctx, app.Env, app.enabledTools()); err != nil {
			t.Fatal(err)
		}
		detectOnly := sim.reads()
		row := claudeStatusRow(t, app)
		if got := sim.reads(); got != detectOnly {
			t.Errorf("kae status read the keychain %d times, Detect alone %d", got, detectOnly)
		}
		if row.LiveCredential != constants.CredentialOK {
			t.Fatalf("the live copy must be judged from the cached read: %q", row.LiveCredential)
		}

		ad, err := adapter.ForTool(constants.ToolClaude)
		if err != nil {
			t.Fatal(err)
		}
		ad.Doctor(ctx, app.Env)
		doctorOnly := sim.reads()
		buildDoctor(ctx, app, constants.ToolClaude, doctorOptIns{})
		if got := sim.reads(); got != doctorOnly {
			t.Errorf("kae doctor claude read the keychain %d times, the adapter's checks alone %d", got, doctorOnly)
		}
	})
}
