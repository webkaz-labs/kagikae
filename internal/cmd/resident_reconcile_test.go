package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
	"github.com/webkaz-labs/kagikae/internal/testutil/wsrpctest"
)

// The tests in this file swap runner.RunWithEnv and os.Stdout/os.Stderr, so none
// of them runs in parallel.

// Accounts of the two captured codex logins: main's and side's account ids.
const (
	residentMain = probeAccount
	residentSide = probeOtherAcct
)

func codexChatGPTAuth(accountID, token string) string {
	return `{"auth_mode":"chatgpt","tokens":{"access_token":"` + token + `","account_id":"` + accountID + `"}}`
}

// residentDaemon is a fake codex managed daemon whose answer the test changes,
// as a restart would.
type residentDaemon struct {
	socket   string
	accepted *atomic.Int32
	answer   atomic.Pointer[string]
}

func (d *residentDaemon) holds(accountID string) {
	reply := accountReadReply(accountID)
	d.answer.Store(&reply)
}

func (d *residentDaemon) answers(reply string) { d.answer.Store(&reply) }

func startResidentDaemon(t *testing.T, accountID string) *residentDaemon {
	t.Helper()
	d := &residentDaemon{}
	d.holds(accountID)
	d.socket, d.accepted = wsrpctest.ServeEach(t, func(p *wsrpctest.Peer) {
		if p.Upgrade() != nil {
			return
		}
		for i := 0; ; i++ {
			f, err := p.ReadFrame()
			if err != nil || f.Op == wsrpctest.OpClose {
				return
			}
			if i == 2 {
				_ = p.Send(wsrpctest.Text(*d.answer.Load()))
			}
		}
	})
	return d
}

// restartCall is one call of runner.RunWithEnv the reconcile made.
type restartCall struct {
	argv []string
	env  []string
	// codexHome is the CODEX_HOME the child would see: extraEnv appended to the
	// process environment, the last entry winning as os/exec does.
	codexHome string
	// lockFree records whether the codex switch lock could be taken during the
	// restart, which it can only once the switch has released it.
	lockFree bool
}

// residentFixture is an App with codex accounts main and side captured, side
// live and recorded, claude main and side too, and profiles main and side over
// both tools. Its clock advances only in the restart's waits.
type residentFixture struct {
	app    *App
	holder adapter.ResidentHolder
	daemon *residentDaemon

	mu       sync.Mutex
	restarts []restartCall
	sleeps   int
}

func newResidentFixture(t *testing.T) *residentFixture {
	t.Helper()
	app := testApp(t, nil)
	app.Config.Profiles = map[string]config.Profile{
		"main": {Accounts: map[string]string{constants.ToolClaude: "main", constants.ToolCodex: "main"}},
		"side": {Accounts: map[string]string{constants.ToolClaude: "side", constants.ToolCodex: "side"}},
	}
	app.residentProbeTimeout = 2 * time.Second
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	codexHome := filepath.Join(app.Env.Home, ".codex")
	writeFile(t, filepath.Join(codexHome, "config.toml"), "model = \"gpt-5.4\"\n")
	for _, acct := range []struct{ name, id, token, claude string }{
		{"main", residentMain, "codex-main-token", mainToken},
		{"side", residentSide, "codex-side-token", sideToken},
	} {
		writeFile(t, filepath.Join(codexHome, "auth.json"), codexChatGPTAuth(acct.id, acct.token))
		code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, constants.ToolCodex, acct.name) })
		mustExit(t, constants.ExitOK, code, out)
		seedClaude(t, app, acct.claude, acct.name+"-uuid")
		code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, constants.ToolClaude, acct.name) })
		mustExit(t, constants.ExitOK, code, out)
	}
	ad, err := adapter.ForTool(constants.ToolCodex)
	if err != nil {
		t.Fatal(err)
	}
	f := &residentFixture{app: app, holder: ad.(adapter.ResidentHolder)}
	now := app.Now()
	app.Now = func() time.Time {
		f.mu.Lock()
		defer f.mu.Unlock()
		return now.Add(time.Duration(f.sleeps) * 250 * time.Millisecond)
	}
	app.sleepForTest = func(d time.Duration) {
		if d != 250*time.Millisecond {
			t.Errorf("waited %v between re-probes, want 250ms", d)
		}
		f.mu.Lock()
		f.sleeps++
		f.mu.Unlock()
	}
	// Any restart the test did not expect fails it.
	f.stubRestart(t, func(*restartCall) int {
		t.Error("the daemon was restarted")
		return 0
	})
	return f
}

// withDaemon serves a fake daemon holding accountID behind the declared socket.
func (f *residentFixture) withDaemon(t *testing.T, accountID string) *residentDaemon {
	t.Helper()
	f.daemon = startResidentDaemon(t, accountID)
	linkSocket(t, f.app, f.holder, f.daemon.socket)
	return f.daemon
}

// liveAccountID is the account id in the live auth.json.
func (f *residentFixture) liveAccountID(t *testing.T) string {
	t.Helper()
	var doc struct {
		Tokens struct {
			AccountID string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(f.app.Env.Home, ".codex", "auth.json"))), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Tokens.AccountID
}

// stubRestart replaces runner.RunWithEnv for the test: it records the call and
// returns what run returns. restartTo is the usual run: the daemon comes back on
// the live account and the command succeeds.
func (f *residentFixture) stubRestart(t *testing.T, run func(*restartCall) int) {
	t.Helper()
	saved := runner.RunWithEnv
	t.Cleanup(func() { runner.RunWithEnv = saved })
	runner.RunWithEnv = func(_ context.Context, extraEnv []string, name string, args ...string) (string, string, int) {
		call := restartCall{argv: append([]string{name}, args...), env: append([]string(nil), extraEnv...)}
		for _, kv := range append(os.Environ(), extraEnv...) {
			if v, ok := strings.CutPrefix(kv, "CODEX_HOME="); ok {
				call.codexHome = v
			}
		}
		if held, err := lock.Acquire(f.app.Paths.LocksDir(), constants.ToolCodex); err == nil {
			call.lockFree = true
			held.Release()
		}
		code := run(&call)
		f.mu.Lock()
		f.restarts = append(f.restarts, call)
		f.mu.Unlock()
		return "", "", code
	}
}

func (f *residentFixture) restartTo(t *testing.T) func(*restartCall) int {
	return func(*restartCall) int {
		f.daemon.holds(f.liveAccountID(t))
		return 0
	}
}

func (f *residentFixture) restartCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.restarts)
}

// codexResidents decodes a switch report (explicit or bare) and returns the
// residents of each tool's result.
func codexResidents(t *testing.T, out string) map[string][]residentEntry {
	t.Helper()
	var report struct {
		Results []struct {
			Tool      string          `json:"tool"`
			Residents []residentEntry `json:"residents"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid report: %v: %s", err, out)
	}
	byTool := map[string][]residentEntry{}
	for _, r := range report.Results {
		if r.Residents == nil {
			t.Errorf("%s: residents is null, want an array: %s", r.Tool, out)
		}
		byTool[r.Tool] = r.Residents
	}
	return byTool
}

func daemonEntry(observed, outcome string) residentEntry {
	return residentEntry{Kind: constants.ResidentKindDaemon, Observed: observed, Outcome: outcome}
}

var sessionEntry = residentEntry{
	Kind: constants.ResidentKindSession, Observed: constants.ResidentObservedUnknown, Outcome: constants.ResidentOutcomeWarned,
}

// Text every resident line is checked against: none of the daemon's personal
// data, and none of the credential's account ids.
func assertNoResidentPII(t *testing.T, outputs ...string) {
	t.Helper()
	for _, out := range outputs {
		for _, banned := range []string{probeEmail, residentMain, residentSide, "plus"} {
			if strings.Contains(out, banned) {
				t.Errorf("output carries %q:\n%s", banned, out)
			}
		}
	}
}

const (
	noticeRestart = "kae: note: codex: the managed daemon (codex app-server daemon) holds another account than this switch leaves live; kae restarts it after the switch"
	noticePlanned = "kae: note: codex: the managed daemon (codex app-server daemon) holds another account than this switch would leave live; the switch would restart it"
	warnOptedOut  = "kae: warning: codex: the managed daemon (codex app-server daemon) holds another account than this switch leaves live, and --no-restart leaves it running; to move it to the new account, run: codex app-server daemon restart"
	warnHook      = "kae: warning: codex: the managed daemon (codex app-server daemon) holds another account than this switch leaves live, and the enter hook (--auto) does not restart it; to move it to the new account, run: codex app-server daemon restart"
	warnUnknown   = "kae: warning: codex: could not read which account the managed daemon (codex app-server daemon) holds; if it still uses the previous account, run: codex app-server daemon restart"
	warnSession   = "kae: warning: codex sessions started before this switch that are not connected to the managed daemon keep the previous account until they are restarted"
	noteRestarted = "kae: note: codex: restarted the managed daemon (codex app-server daemon); it now holds the account this switch left live"
	warnUnverfied = "kae: warning: codex: restarted the managed daemon (codex app-server daemon) but could not confirm that it holds the account now live; if it still uses the previous account, run: codex app-server daemon restart"
	warnFailed    = "kae: warning: codex: codex app-server daemon restart failed (exit 3); the switch is kept, and the managed daemon may still use the previous account; to retry, run: codex app-server daemon restart"
)

// The everyday case: the daemon holds the live account (side), the switch leaves
// main live, so the daemon differs from what the switch leaves and is restarted
// once, after the switch has released its lock, under the real home's
// CODEX_HOME even when the environment carries a bound directory's.
func TestUseRestartsADaemonHoldingAnotherAccount(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	f.stubRestart(t, f.restartTo(t))
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "bound", ".codex"))

	code, stdout, stderr := captureBoth(t, func() int {
		return runSwitch(context.Background(), f.app, commonOpts{Format: formatJSON}, constants.ToolCodex, "main")
	})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	if f.liveAccountID(t) != residentMain {
		t.Fatal("the switch did not apply main")
	}
	if got := codexResidents(t, stdout)[constants.ToolCodex]; !reflect.DeepEqual(got, []residentEntry{
		daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestarted), sessionEntry,
	}) {
		t.Errorf("residents = %+v", got)
	}
	if f.restartCount() != 1 {
		t.Fatalf("restarted %d times, want once", f.restartCount())
	}
	call := f.restarts[0]
	if want := []string{"codex", "app-server", "daemon", "restart"}; !reflect.DeepEqual(call.argv, want) {
		t.Errorf("argv = %q, want %q", call.argv, want)
	}
	spec := f.holder.ResidentDaemon(f.app.Env)
	if !reflect.DeepEqual(call.env, spec.Env) {
		t.Errorf("extra env = %q, want the spec's %q", call.env, spec.Env)
	}
	if want := strings.TrimPrefix(spec.Env[0], "CODEX_HOME="); call.codexHome != want {
		t.Errorf("the restart sees CODEX_HOME=%s, want the real home %s", call.codexHome, want)
	}
	if !call.lockFree {
		t.Error("the restart ran while the codex switch lock was held")
	}
	for _, line := range []string{noticeRestart, warnSession, noteRestarted} {
		if !strings.Contains(stderr, line+"\n") {
			t.Errorf("stderr lacks %q:\n%s", line, stderr)
		}
	}
	if strings.Index(stderr, noticeRestart) > strings.Index(stderr, noteRestarted) {
		t.Errorf("the outcome precedes the notice:\n%s", stderr)
	}
	assertNoResidentPII(t, stdout, stderr)
}

// The probe compares the daemon with the account the switch leaves live, not
// the one live before it: a daemon already on the target matches and is left
// alone, though it differs from the live login at probe time.
func TestUseComparesTheDaemonWithTheTargetSnapshot(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentMain)
	code, stdout, stderr := captureBoth(t, func() int {
		return runSwitch(context.Background(), f.app, commonOpts{Format: formatJSON}, constants.ToolCodex, "main")
	})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	if got := codexResidents(t, stdout)[constants.ToolCodex]; !reflect.DeepEqual(got, []residentEntry{
		daemonEntry(constants.ResidentObservedMatches, constants.ResidentOutcomeNone), sessionEntry,
	}) {
		t.Errorf("residents = %+v", got)
	}
	if strings.Contains(stderr, "managed daemon (codex app-server daemon)") {
		t.Errorf("a matching daemon printed a notice:\n%s", stderr)
	}
}

// absent prints nothing about the daemon; a switch that leaves the account as it
// was prints no session warning either.
func TestUseWithoutADaemonOrAnAccountChange(t *testing.T) {
	f := newResidentFixture(t)
	for _, tc := range []struct {
		target  string
		session bool
	}{{"side", false}, {"main", true}} {
		code, stdout, stderr := captureBoth(t, func() int {
			return runSwitch(context.Background(), f.app, commonOpts{Format: formatJSON}, constants.ToolCodex, tc.target)
		})
		mustExit(t, constants.ExitOK, code, stdout+stderr)
		want := []residentEntry{daemonEntry(constants.ResidentObservedAbsent, constants.ResidentOutcomeNone)}
		if tc.session {
			want = append(want, sessionEntry)
		}
		if got := codexResidents(t, stdout)[constants.ToolCodex]; !reflect.DeepEqual(got, want) {
			t.Errorf("use codex %s: residents = %+v, want %+v", tc.target, got, want)
		}
		if strings.Contains(stderr, warnSession) != tc.session {
			t.Errorf("use codex %s: session warning present = %v, want %v:\n%s",
				tc.target, !tc.session, tc.session, stderr)
		}
		if strings.Contains(stderr, "managed daemon (codex app-server daemon)") {
			t.Errorf("use codex %s: an absent daemon printed a notice:\n%s", tc.target, stderr)
		}
	}
}

// What suppresses the restart, each on a daemon that differs: nothing is
// restarted, the warning names the manual step, and the outcome says why.
// --yes changes none of it.
func TestUseSuppressesTheRestart(t *testing.T) {
	// The three forms of a shared use: explicit `kae use codex main`, bare
	// `kae use --quiet -P main` and the hook's `kae use --auto -P main`.
	explicit := func(ctx context.Context, app *App, o commonOpts) int {
		return runSwitch(ctx, app, o, constants.ToolCodex, "main")
	}
	bare := func(ctx context.Context, app *App, o commonOpts) int {
		return runUseBare(ctx, app, o, false, "main", true)
	}
	hook := func(ctx context.Context, app *App, o commonOpts) int {
		return runUseAuto(ctx, app, o, "main", true)
	}
	type suppression struct {
		name    string
		opts    commonOpts
		run     func(ctx context.Context, app *App, opts commonOpts) int
		outcome string
		line    string
	}
	for _, tc := range []suppression{
		{"no-restart", commonOpts{NoRestart: true}, explicit, constants.ResidentOutcomeOptedOut, warnOptedOut},
		{"no-restart bare", commonOpts{NoRestart: true, Yes: true}, bare, constants.ResidentOutcomeOptedOut, warnOptedOut},
		{"auto", commonOpts{ResidentHook: true}, hook, constants.ResidentOutcomeWarned, warnHook},
		{"auto with yes", commonOpts{ResidentHook: true, Yes: true}, hook, constants.ResidentOutcomeWarned, warnHook},
		{"dry-run", commonOpts{DryRun: true, Yes: true}, explicit, constants.ResidentOutcomePlanned, noticePlanned},
		{"dry-run auto", commonOpts{DryRun: true, ResidentHook: true}, hook, constants.ResidentOutcomeWarned, warnHook},
		{"dry-run no-restart", commonOpts{DryRun: true, NoRestart: true}, explicit, constants.ResidentOutcomeOptedOut, warnOptedOut},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, format := range []string{formatJSON, formatText} {
				f := newResidentFixture(t)
				f.withDaemon(t, residentSide)
				opts := tc.opts
				opts.Format = format
				code, stdout, stderr := captureBoth(t, func() int { return tc.run(context.Background(), f.app, opts) })
				mustExit(t, constants.ExitOK, code, stdout+stderr)
				if f.restartCount() != 0 {
					t.Errorf("%s: restarted", format)
				}
				for _, line := range []string{tc.line, warnSession} {
					if !strings.Contains(stderr, line+"\n") {
						t.Errorf("%s: stderr lacks %q:\n%s", format, line, stderr)
					}
				}
				if strings.Contains(stderr, noticeRestart) {
					t.Errorf("%s: announced a restart:\n%s", format, stderr)
				}
				if format == formatJSON {
					if got := codexResidents(t, stdout)[constants.ToolCodex]; !reflect.DeepEqual(got, []residentEntry{
						daemonEntry(constants.ResidentObservedDiffers, tc.outcome), sessionEntry,
					}) {
						t.Errorf("residents = %+v", got)
					}
				}
				wantLive := residentMain
				if opts.DryRun {
					wantLive = residentSide
				}
				if f.liveAccountID(t) != wantLive {
					t.Errorf("%s: live account after the run is not the expected one", format)
				}
				assertNoResidentPII(t, stdout, stderr)
			}
		})
	}
}

// --quiet changes output only: a bare `kae use --quiet` typed by hand restarts,
// and its notice and outcome still reach stderr.
func TestBareUseQuietStillRestarts(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	f.stubRestart(t, f.restartTo(t))
	code, stdout, stderr := captureBoth(t, func() int {
		return runUseBare(context.Background(), f.app, commonOpts{Format: formatText}, false, "main", true)
	})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	if stdout != "" {
		t.Errorf("--quiet printed a report: %q", stdout)
	}
	if f.restartCount() != 1 || !f.restarts[0].lockFree {
		t.Fatalf("restarts = %+v, want one outside the lock", f.restarts)
	}
	for _, line := range []string{noticeRestart, warnSession, noteRestarted} {
		if !strings.Contains(stderr, line+"\n") {
			t.Errorf("stderr lacks %q:\n%s", line, stderr)
		}
	}
}

// A profile switch reconciles once, after the whole transaction, and only the
// codex result carries resident entries.
func TestProfileSwitchReconcilesOnce(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	f.stubRestart(t, f.restartTo(t))
	claudeLockFree := false
	inner := runner.RunWithEnv
	runner.RunWithEnv = func(ctx context.Context, env []string, name string, args ...string) (string, string, int) {
		if held, err := lock.Acquire(f.app.Paths.LocksDir(), constants.ToolClaude); err == nil {
			claudeLockFree = true
			held.Release()
		}
		return inner(ctx, env, name, args...)
	}
	code, stdout, stderr := captureBoth(t, func() int {
		return runSwitch(context.Background(), f.app, commonOpts{Format: formatJSON}, "all", "main")
	})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	byTool := codexResidents(t, stdout)
	if got := byTool[constants.ToolClaude]; len(got) != 0 {
		t.Errorf("claude residents = %+v, want []", got)
	}
	if got := byTool[constants.ToolCodex]; !reflect.DeepEqual(got, []residentEntry{
		daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestarted), sessionEntry,
	}) {
		t.Errorf("codex residents = %+v", got)
	}
	if f.restartCount() != 1 {
		t.Errorf("restarted %d times, want once", f.restartCount())
	}
	if !claudeLockFree {
		t.Error("the restart ran while the profile's claude lock was held")
	}
}

// A switch whose transaction fails restarts nothing, and its notice was on
// stderr before the failing write.
func TestFailedSwitchDoesNotRestart(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through a read-only directory")
	}
	for _, target := range []string{"codex", "all"} {
		f := newResidentFixture(t)
		f.withDaemon(t, residentSide)
		codexHome := filepath.Join(f.app.Env.Home, ".codex")
		if err := os.Chmod(codexHome, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(codexHome, 0o700) })
		name := "main"
		code, stdout, stderr := captureBoth(t, func() int {
			return runSwitch(context.Background(), f.app, commonOpts{Format: formatText}, target, name)
		})
		if code == constants.ExitOK {
			t.Fatalf("use %s %s: the switch succeeded through a read-only home:\n%s%s", target, name, stdout, stderr)
		}
		if f.restartCount() != 0 {
			t.Errorf("use %s %s: restarted after a failed transaction", target, name)
		}
		if !strings.Contains(stderr, noticeRestart+"\n") {
			t.Errorf("use %s %s: the notice did not precede the write:\n%s", target, name, stderr)
		}
		if strings.Contains(stderr, noteRestarted) {
			t.Errorf("use %s %s: reported a restart:\n%s", target, name, stderr)
		}
	}
}

// A failed restart is a warning: exit 0, the switch kept, restart_failed.
func TestRestartFailureKeepsTheSwitch(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	f.stubRestart(t, func(*restartCall) int { return 3 })
	code, stdout, stderr := captureBoth(t, func() int {
		return runSwitch(context.Background(), f.app, commonOpts{Format: formatJSON}, constants.ToolCodex, "main")
	})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	if got := codexResidents(t, stdout)[constants.ToolCodex]; !reflect.DeepEqual(got, []residentEntry{
		daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestartFailed), sessionEntry,
	}) {
		t.Errorf("residents = %+v", got)
	}
	if !strings.Contains(stderr, warnFailed+"\n") {
		t.Errorf("stderr lacks the failure warning:\n%s", stderr)
	}
	if f.liveAccountID(t) != residentMain {
		t.Error("a failed restart rolled the switch back")
	}
	if st, _ := f.app.loadState(); st.Active[constants.ToolCodex] != "main" {
		t.Errorf("recorded active codex = %q, want main", st.Active[constants.ToolCodex])
	}
}

// A daemon that still differs, or cannot be read, when the re-probes run out is
// restart_unverified; the waits are 250 ms apart and stop at 5 s.
func TestRestartUnverifiedAfterTheWait(t *testing.T) {
	for name, after := range map[string]func(d *residentDaemon){
		"still differs": func(*residentDaemon) {},
		"unreadable": func(d *residentDaemon) {
			d.answers(`{"jsonrpc":"2.0","id":2,"result":{"account":null,"workspaceRouting":null}}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newResidentFixture(t)
			d := f.withDaemon(t, residentSide)
			f.stubRestart(t, func(*restartCall) int { after(d); return 0 })
			code, stdout, stderr := captureBoth(t, func() int {
				return runSwitch(context.Background(), f.app, commonOpts{Format: formatJSON}, constants.ToolCodex, "main")
			})
			mustExit(t, constants.ExitOK, code, stdout+stderr)
			if got := codexResidents(t, stdout)[constants.ToolCodex]; !reflect.DeepEqual(got, []residentEntry{
				daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestartUnverified), sessionEntry,
			}) {
				t.Errorf("residents = %+v", got)
			}
			if !strings.Contains(stderr, warnUnverfied+"\n") {
				t.Errorf("stderr lacks the unverified warning:\n%s", stderr)
			}
			// 5 s of 250 ms waits (docs/CLI.md), spelled out rather than derived from
			// the constants under test.
			if want := 20; f.sleeps != want {
				t.Errorf("waited %d times, want %d", f.sleeps, want)
			}
		})
	}
}

// The re-probe compares the daemon with the credential live at that moment, so
// a switch another kae process makes during the wait (here: back to side, with
// the daemon following it) still reads as restarted.
func TestRestartVerifiesAgainstTheLiveCredential(t *testing.T) {
	f := newResidentFixture(t)
	d := f.withDaemon(t, residentSide)
	f.stubRestart(t, func(*restartCall) int {
		writeFile(t, filepath.Join(f.app.Env.Home, ".codex", "auth.json"), codexChatGPTAuth(residentSide, "codex-side-token"))
		d.holds(residentSide)
		return 0
	})
	code, stdout, stderr := captureBoth(t, func() int {
		return runSwitch(context.Background(), f.app, commonOpts{Format: formatJSON}, constants.ToolCodex, "main")
	})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	if got := codexResidents(t, stdout)[constants.ToolCodex]; !reflect.DeepEqual(got, []residentEntry{
		daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestarted), sessionEntry,
	}) {
		t.Errorf("residents = %+v", got)
	}
	if f.sleeps != 0 {
		t.Errorf("waited %d times for a daemon already on the live account", f.sleeps)
	}
}

// unknown is never restarted, whatever the flags, and names the manual step.
func TestUnknownDaemonOnlyWarns(t *testing.T) {
	for _, opts := range []commonOpts{{}, {NoRestart: true}, {DryRun: true}} {
		f := newResidentFixture(t)
		d := f.withDaemon(t, residentSide)
		d.answers(`{"jsonrpc":"2.0","id":2,"result":{"account":{"type":"apiKey","email":"` + probeEmail + `"},"workspaceRouting":null}}`)
		opts.Format = formatJSON
		code, stdout, stderr := captureBoth(t, func() int {
			return runSwitch(context.Background(), f.app, opts, constants.ToolCodex, "main")
		})
		mustExit(t, constants.ExitOK, code, stdout+stderr)
		if got := codexResidents(t, stdout)[constants.ToolCodex]; !reflect.DeepEqual(got, []residentEntry{
			daemonEntry(constants.ResidentObservedUnknown, constants.ResidentOutcomeWarned), sessionEntry,
		}) {
			t.Errorf("%+v: residents = %+v", opts, got)
		}
		if !strings.Contains(stderr, warnUnknown+"\n") {
			t.Errorf("%+v: stderr lacks the unknown warning:\n%s", opts, stderr)
		}
		if f.restartCount() != 0 {
			t.Errorf("%+v: restarted an unknown daemon", opts)
		}
		assertNoResidentPII(t, stdout, stderr)
	}
}

// The notices and the outcome render in Japanese.
func TestUseResidentsInJapanese(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	f.stubRestart(t, f.restartTo(t))
	l10ntest.UseJapanese(t)
	code, stdout, stderr := captureBoth(t, func() int {
		return runSwitch(context.Background(), f.app, commonOpts{Format: formatText}, constants.ToolCodex, "main")
	})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	for _, line := range []string{
		"kae: note: codex: 管理デーモン（codex app-server daemon）は、この切替で有効になるアカウントとは別のアカウントを使っています。切替の後に kae が再起動します。",
		"kae: warning: この切替より前に起動し、管理デーモンに接続していない codex セッションは、再起動するまで前のアカウントを使います。",
		"kae: note: codex: 管理デーモン（codex app-server daemon）を再起動しました。この切替で有効になったアカウントを使っています。",
	} {
		if !strings.Contains(stderr, line+"\n") {
			t.Errorf("stderr lacks %q:\n%s", line, stderr)
		}
	}
	assertNoResidentPII(t, stdout, stderr)
}

// --no-restart is a flag of use, offered by completion.
func TestUseOffersNoRestart(t *testing.T) {
	if !strings.Contains(strings.Join(flagCompletions("use"), " "), "--no-restart") {
		t.Errorf("use flags = %q", flagCompletions("use"))
	}
}
