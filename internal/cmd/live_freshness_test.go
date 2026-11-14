package cmd

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/adapter/claude"
	"github.com/webkaz-labs/kagikae/internal/adapter/codex"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/keychain"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/state"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The incident: claude/main is active, its snapshot is good, and the live login
// claude runs on is the tombstone a failed refresh leaves. kae status said
// Credential=ok and doctor said nothing (docs/CLI.md § Credential freshness in
// listings, "The active account's live login").

const (
	liveSecretToken = "sk-ant-oat01-LIVE-SECRET-ffff"
	claudeTombstone = `{"accessToken":"","refreshToken":"","expiresAt":0}`
)

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
	fresh := func(now time.Time) string { return refreshBackedClaudeCred(now, 30*24*time.Hour) }
	tombstone := func(time.Time) string { return claudeTombstone }
	for _, tc := range []struct {
		name       string
		snapshot   func(now time.Time) string // claudeAiOauth object captured as main
		live       func(now time.Time) string // claudeAiOauth object left live afterwards
		mutate     func(t *testing.T, app *App)
		liveState  string         // status live_credential
		snapState  string         // status credential
		code       string         // the live doctor finding, "" for none
		want       []string       // in that finding
		cell       string         // the status Credential cell
		findingsOf map[string]int // credential checks by code
	}{
		{
			name: "live tombstone, fresh snapshot", snapshot: fresh, live: tombstone,
			liveState: constants.CredentialStale, snapState: constants.CredentialOK,
			code: constants.CheckCredentialStale,
			want: []string{liveMarker + " is stale", "failed token refresh", "run: kae relogin claude"},
			cell: "re-login now (live login)", findingsOf: map[string]int{constants.CheckCredentialStale: 1},
		},
		{
			name: "live expiring, fresh snapshot", snapshot: fresh,
			live:      func(now time.Time) string { return endOfLifeClaudeCred(now, 3*24*time.Hour, liveSecretToken) },
			liveState: constants.CredentialExpiring, snapState: constants.CredentialOK,
			code: constants.CheckCredentialExpiring,
			want: []string{liveMarker + " needs an interactive re-login in 3 day(s)", "run: kae relogin claude"},
			cell: "3 day(s) left (live login)", findingsOf: map[string]int{constants.CheckCredentialExpiring: 1},
		},
		{
			name: "live fresh", snapshot: fresh,
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
		{
			// The live half needs no secret backend; with none every snapshot counts as
			// unknown, so a dead live copy is still reported.
			name: "no secret backend", snapshot: fresh, live: tombstone,
			mutate:    func(_ *testing.T, app *App) { app.Config.Security.SecretBackend = "unavailable-for-test" },
			liveState: constants.CredentialStale, snapState: "",
			code: constants.CheckCredentialStale, want: []string{liveMarker + " is stale"},
			cell: "re-login now (live login)", findingsOf: map[string]int{constants.CheckCredentialStale: 1},
		},
		{
			// In global isolated mode the tool runs on a private home, so the real
			// home's copy is not the one in use and neither surface judges it.
			name: "global isolated mode", snapshot: fresh, live: tombstone,
			mutate: func(t *testing.T, app *App) {
				if _, err := app.mutateState(func(st *state.State) {
					st.Synced = map[string]string{constants.ToolClaude: "main"}
				}); err != nil {
					t.Fatal(err)
				}
			},
			liveState: "", snapState: constants.CredentialOK,
			cell: constants.CredentialOK, findingsOf: map[string]int{},
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
			if tc.mutate != nil {
				tc.mutate(t, app)
			}

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
			if tc.code != "" && !slices.Contains(checks[tc.code], live) {
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

// An account that is not active is judged by its snapshot alone, and the live copy is
// compared with the *active* account's snapshot: a stale inactive one listed after it
// must not stand in for it.
func TestLiveCredentialLeavesInactiveAccountsAlone(t *testing.T) {
	app := testApp(t, nil)
	chdirTemp(t)
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	captureClaudeAt(t, app, "side", sideToken, app.Now().Add(time.Hour))
	seedClaude(t, app, "", "zeta-uuid")
	seedClaudeOAuth(t, app, `{"accessToken":"old","refreshToken":"","expiresAt":1577836800000}`)
	mustExit(t, constants.ExitOK, runCaptureQuiet(t, app, "zeta"), "capture zeta")
	mustExit(t, constants.ExitOK, func() int {
		code, _ := captureStdout(t, func() int {
			return runSwitch(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "side")
		})
		return code
	}(), "switch to side")
	seedClaudeOAuth(t, app, claudeTombstone)

	var live []string
	for _, m := range credentialChecks(t, app)[constants.CheckCredentialStale] {
		if strings.Contains(m, "the live claude credential") {
			live = append(live, m)
		}
	}
	if len(live) != 1 || !strings.Contains(live[0], "of active account side") {
		t.Fatalf("want one live finding for side: %v", live)
	}
	items, _, err := buildAccountItemsWith(context.Background(), app, constants.ToolClaude, app.readState())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		want := constants.CredentialOK
		if item.Account == "zeta" {
			want = constants.CredentialStale
		}
		if item.Credential != want {
			t.Errorf("kae ls stays snapshot-only: %s is %q", item.Account, item.Credential)
		}
	}
}

// doctor's live half reads the real home a global switch acts on, not a kae-managed
// home this shell points the tool at.
func TestDoctorLiveHalfReadsTheRealHome(t *testing.T) {
	app := testApp(t, nil)
	chdirTemp(t)
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	kaeHome := filepath.Join(app.Paths.IsolationDir(), "0123456789abcdef", constants.ToolClaude, "shared")
	writeFile(t, filepath.Join(kaeHome, ".credentials.json"), `{"claudeAiOauth":`+claudeTombstone+`}`)
	app.Env.Getenv = func(key string) string {
		if key == isolationEnvVar(constants.ToolClaude) {
			return kaeHome
		}
		return ""
	}
	for _, msgs := range credentialChecks(t, app) {
		for _, m := range msgs {
			if strings.Contains(m, "the live claude credential") {
				t.Fatalf("the healthy real home must not be reported from a kae home: %q", m)
			}
		}
	}
	seedClaudeOAuth(t, app, claudeTombstone)
	if got := credentialChecks(t, app)[constants.CheckCredentialStale]; len(got) != 1 ||
		!strings.Contains(got[0], "the live claude credential of active account main") {
		t.Fatalf("the dead real home must be reported: %v", got)
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

// serviceKeychain is a `security` double holding one item per service, safe for
// kae status's concurrent Detects, counting every find-generic-password call —
// payload reads and attribute-only probes alike.
type serviceKeychain struct {
	mu    sync.Mutex
	items map[string]string // service -> payload
	calls int
}

func (k *serviceKeychain) Run(_ context.Context, name string, args ...string) (string, string, int) {
	if name != "security" || len(args) == 0 || args[0] != "find-generic-password" {
		return "", "", 0
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.calls++
	payload, ok := k.items[valueAfter(args, "-s")]
	if !ok {
		return "", "The specified item " + keychain.NotFoundMarker + " in the keychain.", 44
	}
	if slices.Contains(args, "-w") {
		return payload + "\n", "", 0
	}
	return "keychain: \"login\"\nattributes:\n    \"acct\"<blob>=\"me\"\n", "", 0
}

func (k *serviceKeychain) RunInput(ctx context.Context, _, name string, args ...string) (string, string, int) {
	return k.Run(ctx, name, args...)
}

func (k *serviceKeychain) take() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := k.calls
	k.calls = 0
	return n
}

// Judging the live copy costs no `security` call beyond what the command already
// made, in kae status and in kae doctor: claude's keychain item, and codex's `auto`
// store, whose resolution probes for its keychain item (here absent, so auth.json).
func TestLiveCredentialAddsNoKeychainCall(t *testing.T) {
	sim := &serviceKeychain{items: map[string]string{}}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		chdirTemp(t)
		ctx := context.Background()
		sim.items[claude.KeychainService] = claudeOAuthPayload(mainToken, app.Now().Add(time.Hour))
		mustExit(t, constants.ExitOK, runCaptureQuiet(t, app, "main"), "capture claude main")
		seedCodex(t, app, "codex-token")
		writeFile(t, filepath.Join(app.Env.Home, ".codex", "config.toml"), "cli_auth_credentials_store = \"auto\"\n")
		code, out := captureStdout(t, func() int {
			return runCapture(ctx, app, commonOpts{Format: formatText}, constants.ToolCodex, "main")
		})
		mustExit(t, constants.ExitOK, code, out)
		if _, ok := sim.items[codex.KeychainService]; ok {
			t.Fatal("the codex item must stay absent so `auto` resolves auth.json by probing")
		}

		sim.take()
		if _, err := detectTools(ctx, app.Env, app.enabledTools()); err != nil {
			t.Fatal(err)
		}
		detectOnly := sim.take()
		row := claudeStatusRow(t, app)
		if got := sim.take(); got != detectOnly {
			t.Errorf("kae status made %d keychain calls, Detect alone %d", got, detectOnly)
		}
		if row.LiveCredential != constants.CredentialOK {
			t.Fatalf("the live copy must be judged from the cached read: %q", row.LiveCredential)
		}

		// claude end to end: the adapter's auth_present read is the only other one.
		ad, err := adapter.ForTool(constants.ToolClaude)
		if err != nil {
			t.Fatal(err)
		}
		ad.Doctor(ctx, app.Env)
		adapterOnly := sim.take()
		buildDoctor(ctx, app, constants.ToolClaude, doctorOptIns{})
		if got := sim.take(); got != adapterOnly {
			t.Errorf("kae doctor claude made %d keychain calls, the adapter's checks alone %d", got, adapterOnly)
		}
		// codex: doctor's other checks probe its store outside this cache, so pin the
		// live half against the reads it shares one cache with.
		cached := keychain.WithReadCache(ctx)
		ad, err = adapter.ForTool(constants.ToolCodex)
		if err != nil {
			t.Fatal(err)
		}
		ad.Doctor(cached, app.Env)
		app.credentialConflictLiveChecks(cached, constants.ToolCodex)
		sim.take()
		app.activeLiveCredentials(cached, constants.ToolCodex)
		if got := sim.take(); got != 0 {
			t.Errorf("the codex live half made %d keychain calls after Detect and the conflict read", got)
		}
	})
}
