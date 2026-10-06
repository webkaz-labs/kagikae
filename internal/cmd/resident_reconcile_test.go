package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The tests in this file swap runner.LaunchWithEnv and os.Stdout/os.Stderr, so none
// of them runs in parallel.

// Accounts of the two captured codex logins: main's and side's account ids.
const (
	residentMain = probeAccount
	residentSide = probeOtherAcct
)

func codexChatGPTAuth(accountID, token string) string {
	return `{"auth_mode":"chatgpt","tokens":{"access_token":"` + token + `","account_id":"` + accountID + `"}}`
}

// restartCall is one call of runner.LaunchWithEnv the reconcile made.
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
	daemon *fakeDaemon

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
func (f *residentFixture) withDaemon(t *testing.T, accountID string) *fakeDaemon {
	t.Helper()
	f.daemon = startFakeDaemon(t, accountReadReply(accountID))
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

// stubRestart replaces runner.LaunchWithEnv for the test: it records the call and
// returns what run returns. restartTo is the usual run: the daemon comes back on
// the live account and the command succeeds.
func (f *residentFixture) stubRestart(t *testing.T, run func(*restartCall) int) {
	t.Helper()
	saved := runner.LaunchWithEnv
	t.Cleanup(func() { runner.LaunchWithEnv = saved })
	runner.LaunchWithEnv = func(_ context.Context, extraEnv []string, name string, args ...string) (int, error) {
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
		return code, nil
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

// use runs `kae use <target> <name>` (target "all" for a profile) with opts
// and requires exit 0.
func (f *residentFixture) use(t *testing.T, ctx context.Context, opts commonOpts, target, name string) (stdout, stderr string) {
	t.Helper()
	code, stdout, stderr := captureBoth(t, func() int { return runSwitch(ctx, f.app, opts, target, name) })
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	return stdout, stderr
}

// wantCodexResidents requires the JSON report's codex result to carry exactly
// want.
func wantCodexResidents(t *testing.T, stdout string, want ...residentEntry) {
	t.Helper()
	if got := codexResidents(t, stdout)[constants.ToolCodex]; !reflect.DeepEqual(got, want) {
		t.Errorf("codex residents = %+v, want %+v", got, want)
	}
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
	noticeRestart  = "kae: note: codex: after the switch, kae will restart the managed daemon"
	noticePlanned  = "kae: note: codex: after the switch, kae would restart the managed daemon"
	warnOptedOut   = "kae: warning: codex: kae does not restart the managed daemon (--no-restart); to move it to the live account, run: codex app-server daemon restart"
	warnHook       = "kae: warning: codex: kae does not restart the managed daemon (the enter hook, --auto); to move it to the live account, run: codex app-server daemon restart"
	warnUnknown    = "kae: warning: codex: could not read the managed daemon's account; if it is not on the live account, run: codex app-server daemon restart"
	warnSession    = "kae: warning: codex sessions started before the switch and not connected to the managed daemon keep the previous account until they are restarted"
	warnSessionDry = "kae: warning: codex sessions started before the switch and not connected to the managed daemon would keep the previous account until they are restarted"
	noteRestarted  = "kae: note: codex: restarted the managed daemon"
	warnUnverfied  = "kae: warning: codex: restarted the managed daemon but could not confirm that it holds the live account; if it is still not using it, run: codex app-server daemon restart"
	warnFailed     = "kae: warning: codex: codex app-server daemon restart failed (exit 3); the switch is kept, and the managed daemon may not be using the live account yet; to retry, run: codex app-server daemon restart"
)

// daemonLines is the lines of stderr that speak of the daemon, apart from the
// session warning, which names it too.
func daemonLines(stderr string) string {
	var lines []string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, daemonMention) && !strings.Contains(line, "codex sessions started before") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// The everyday case: the daemon holds the live account (side), the switch leaves
// main live, so the daemon differs from what the switch leaves and is restarted
// once, after the switch has released its lock, under the real home's
// CODEX_HOME even when the environment carries a bound directory's.
func TestUseRestartsADaemonHoldingAnotherAccount(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	f.stubRestart(t, f.restartTo(t))
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "bound", ".codex"))

	stdout, stderr := f.use(t, context.Background(), commonOpts{Format: formatJSON}, constants.ToolCodex, "main")
	if f.liveAccountID(t) != residentMain {
		t.Fatal("the switch did not apply main")
	}
	wantCodexResidents(t, stdout, daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestarted), sessionEntry)
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

// A daemon that answers it holds no account, as one on 0.160.1 did after a
// switch, differs from a target that names one: kae restarts it, and
// --no-restart leaves it running with the opted_out warning.
func TestUseRestartsADaemonHoldingNoAccount(t *testing.T) {
	for _, tc := range []struct {
		name     string
		opts     commonOpts
		outcome  string
		restarts int
		line     string
	}{
		{"restart", commonOpts{}, constants.ResidentOutcomeRestarted, 1, noteRestarted},
		{"no-restart", commonOpts{NoRestart: true}, constants.ResidentOutcomeOptedOut, 0, warnOptedOut},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newResidentFixture(t)
			f.withDaemon(t, residentSide).answers(noAccountReply)
			if tc.restarts != 0 {
				f.stubRestart(t, f.restartTo(t))
			}
			opts := tc.opts
			opts.Format = formatJSON
			stdout, stderr := f.use(t, context.Background(), opts, constants.ToolCodex, "main")
			wantCodexResidents(t, stdout, daemonEntry(constants.ResidentObservedDiffers, tc.outcome), sessionEntry)
			if f.restartCount() != tc.restarts {
				t.Errorf("restarted %d times, want %d", f.restartCount(), tc.restarts)
			}
			if !strings.Contains(stderr, tc.line+"\n") {
				t.Errorf("stderr lacks %q:\n%s", tc.line, stderr)
			}
			assertNoResidentPII(t, stdout, stderr)
		})
	}
}

// The probe compares the daemon with the account the switch leaves live, not
// the one live before it: a daemon already on the target matches and is left
// alone, though it differs from the live login at probe time.
func TestUseComparesTheDaemonWithTheTargetSnapshot(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentMain)
	stdout, stderr := f.use(t, context.Background(), commonOpts{Format: formatJSON}, constants.ToolCodex, "main")
	wantCodexResidents(t, stdout, daemonEntry(constants.ResidentObservedMatches, constants.ResidentOutcomeNone), sessionEntry)
	if daemonLines(stderr) != "" {
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
		stdout, stderr := f.use(t, context.Background(), commonOpts{Format: formatJSON}, constants.ToolCodex, tc.target)
		want := []residentEntry{daemonEntry(constants.ResidentObservedAbsent, constants.ResidentOutcomeNone)}
		if tc.session {
			want = append(want, sessionEntry)
		}
		wantCodexResidents(t, stdout, want...)
		if strings.Contains(stderr, warnSession) != tc.session {
			t.Errorf("use codex %s: session warning present = %v, want %v:\n%s",
				tc.target, !tc.session, tc.session, stderr)
		}
		if daemonLines(stderr) != "" {
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
		// --no-restart wins over the hook shape.
		{"auto no-restart", commonOpts{ResidentHook: true, NoRestart: true}, hook, constants.ResidentOutcomeOptedOut, warnOptedOut},
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
				session := warnSession
				if opts.DryRun {
					session = warnSessionDry
				}
				for _, line := range []string{tc.line, session} {
					if !strings.Contains(stderr, line+"\n") {
						t.Errorf("%s: stderr lacks %q:\n%s", format, line, stderr)
					}
				}
				if strings.Contains(stderr, noticeRestart) {
					t.Errorf("%s: announced a restart:\n%s", format, stderr)
				}
				if format == formatJSON {
					wantCodexResidents(t, stdout, daemonEntry(constants.ResidentObservedDiffers, tc.outcome), sessionEntry)
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
	inner := runner.LaunchWithEnv
	runner.LaunchWithEnv = func(ctx context.Context, env []string, name string, args ...string) (int, error) {
		if held, err := lock.Acquire(f.app.Paths.LocksDir(), constants.ToolClaude); err == nil {
			claudeLockFree = true
			held.Release()
		}
		return inner(ctx, env, name, args...)
	}
	stdout, _ := f.use(t, context.Background(), commonOpts{Format: formatJSON}, "all", "main")
	if got := codexResidents(t, stdout)[constants.ToolClaude]; len(got) != 0 {
		t.Errorf("claude residents = %+v, want []", got)
	}
	wantCodexResidents(t, stdout, daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestarted), sessionEntry)
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
	stdout, stderr := f.use(t, context.Background(), commonOpts{Format: formatJSON}, constants.ToolCodex, "main")
	wantCodexResidents(t, stdout, daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestartFailed), sessionEntry)
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
	for name, after := range map[string]func(d *fakeDaemon){
		"still differs":    func(*fakeDaemon) {},
		"holds no account": func(d *fakeDaemon) { d.answers(noAccountReply) },
		"unreadable": func(d *fakeDaemon) {
			d.answers(apiKeyReply)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newResidentFixture(t)
			d := f.withDaemon(t, residentSide)
			f.stubRestart(t, func(*restartCall) int { after(d); return 0 })
			stdout, stderr := f.use(t, context.Background(), commonOpts{Format: formatJSON}, constants.ToolCodex, "main")
			wantCodexResidents(t, stdout, daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestartUnverified), sessionEntry)
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
	stdout, _ := f.use(t, context.Background(), commonOpts{Format: formatJSON}, constants.ToolCodex, "main")
	wantCodexResidents(t, stdout, daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestarted), sessionEntry)
	if f.sleeps != 0 {
		t.Errorf("waited %d times for a daemon already on the live account", f.sleeps)
	}
}

// unknown is never restarted, whatever the flags, and names the manual step.
func TestUnknownDaemonOnlyWarns(t *testing.T) {
	for _, opts := range []commonOpts{{}, {NoRestart: true}, {DryRun: true}} {
		f := newResidentFixture(t)
		d := f.withDaemon(t, residentSide)
		d.answers(apiKeyReply)
		opts.Format = formatJSON
		stdout, stderr := f.use(t, context.Background(), opts, constants.ToolCodex, "main")
		wantCodexResidents(t, stdout, daemonEntry(constants.ResidentObservedUnknown, constants.ResidentOutcomeWarned), sessionEntry)
		if !strings.Contains(stderr, warnUnknown+"\n") {
			t.Errorf("%+v: stderr lacks the unknown warning:\n%s", opts, stderr)
		}
		if f.restartCount() != 0 {
			t.Errorf("%+v: restarted an unknown daemon", opts)
		}
		assertNoResidentPII(t, stdout, stderr)
	}
}

// noAccountFixture is a resident fixture whose daemon answers that it holds no
// account, live on side, for the tests of how kae words that case.
func noAccountFixture(t *testing.T) *residentFixture {
	t.Helper()
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide).answers(noAccountReply)
	return f
}

// wantStderrLine runs a command, in Japanese when japanese is set, and requires
// exit 0, line on its stderr and no personal data on either stream.
func wantStderrLine(t *testing.T, japanese bool, line string, run func() (code int, stdout, stderr string)) {
	t.Helper()
	if japanese {
		l10ntest.UseJapanese(t)
	}
	code, stdout, stderr := run()
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	if !strings.Contains(stderr, line+"\n") {
		t.Errorf("stderr lacks %q:\n%s", line, stderr)
	}
	assertNoResidentPII(t, stdout, stderr)
}

// The switch's lines that suppress the restart render in Japanese, on a daemon
// that holds no account: the wording says only that it is not using the account
// the switch leaves live, which is true of that daemon too.
func TestUseSuppressedRestartInJapanese(t *testing.T) {
	explicit := func(ctx context.Context, app *App, o commonOpts) int {
		return runSwitch(ctx, app, o, constants.ToolCodex, "main")
	}
	hook := func(ctx context.Context, app *App, o commonOpts) int {
		return runUseAuto(ctx, app, o, "main", true)
	}
	for _, tc := range []struct {
		name string
		opts commonOpts
		run  func(ctx context.Context, app *App, opts commonOpts) int
		line string
	}{
		{
			"dry-run",
			commonOpts{DryRun: true},
			explicit,
			"kae: note: codex: 切替後、管理デーモンを再起動します（--dry-run のため、実際には行いません）。",
		},
		{
			"no-restart",
			commonOpts{NoRestart: true},
			explicit,
			"kae: warning: codex: --no-restart のため、管理デーモンを再起動しません。現在有効なアカウントに移すには、codex app-server daemon restart を実行してください。",
		},
		{
			"auto",
			commonOpts{ResidentHook: true},
			hook,
			"kae: warning: codex: enter フック（--auto）のため、管理デーモンを再起動しません。現在有効なアカウントに移すには、codex app-server daemon restart を実行してください。",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := noAccountFixture(t)
			opts := tc.opts
			opts.Format = formatText
			wantStderrLine(t, true, tc.line, func() (int, string, string) {
				return captureBoth(t, func() int { return tc.run(context.Background(), f.app, opts) })
			})
		})
	}
}

// The switch's warnings when the restart fails, cannot be verified, or the
// daemon cannot be read render in Japanese, worded so that they hold for a daemon
// that held no account.
func TestUseRestartWarningsInJapanese(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, f *residentFixture)
		line  string
	}{
		{
			"failed",
			func(t *testing.T, f *residentFixture) { f.stubRestart(t, func(*restartCall) int { return 3 }) },
			"kae: warning: codex: codex app-server daemon restart に失敗しました（終了コード 3）。切替はそのまま有効ですが、管理デーモンは現在有効なアカウントをまだ使っていない可能性があります。再試行するには、codex app-server daemon restart を実行してください。",
		},
		{
			"unverified",
			func(t *testing.T, f *residentFixture) { f.stubRestart(t, func(*restartCall) int { return 0 }) },
			"kae: warning: codex: 管理デーモンを再起動しましたが、現在有効なアカウントを使っていることを確認できませんでした。まだ使っていない場合は、codex app-server daemon restart を実行してください。",
		},
		{
			"unknown",
			func(t *testing.T, f *residentFixture) {
				f.daemon.answers(apiKeyReply)
			},
			"kae: warning: codex: 管理デーモンのアカウントを読み取れませんでした。現在有効なアカウントでない場合は、codex app-server daemon restart を実行してください。",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := noAccountFixture(t)
			tc.setup(t, f)
			wantStderrLine(t, true, tc.line, func() (int, string, string) {
				return captureBoth(t, func() int {
					return runSwitch(context.Background(), f.app, commonOpts{Format: formatText}, constants.ToolCodex, "main")
				})
			})
		})
	}
}

// The notices and the outcome render in Japanese.
func TestUseResidentsInJapanese(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	f.stubRestart(t, f.restartTo(t))
	l10ntest.UseJapanese(t)
	stdout, stderr := f.use(t, context.Background(), commonOpts{Format: formatText}, constants.ToolCodex, "main")
	for _, line := range []string{
		"kae: note: codex: 切替後、管理デーモンを再起動します。",
		"kae: warning: 管理デーモンに接続していない古い codex セッションは、再起動するまで前のアカウントのままです。",
		"  codex: 管理デーモンを再起動しました",
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

// Inside a shell that exports a kae-managed CODEX_HOME (a bound directory or a
// global isolation), the probe and the restart still reach the real home's
// daemon, and the manual step names the real home: the bare command typed in
// that shell would restart the bound home's daemon instead.
func TestBoundShellReachesAndNamesTheRealHomeDaemon(t *testing.T) {
	for _, noRestart := range []bool{true, false} {
		f := newResidentFixture(t)
		f.withDaemon(t, residentSide)
		f.stubRestart(t, f.restartTo(t))
		realSpec := f.holder.ResidentDaemon(f.app.Env)
		bound := f.app.Paths.GlobalIsolatedHomeDir(constants.ToolCodex, "main")
		if err := os.MkdirAll(bound, 0o700); err != nil {
			t.Fatal(err)
		}
		inner, innerLookup := f.app.Env.Getenv, f.app.Env.LookupEnv
		f.app.Env.Getenv = func(key string) string {
			if key == "CODEX_HOME" {
				return bound
			}
			return inner(key)
		}
		f.app.Env.LookupEnv = func(key string) (string, bool) {
			if key == "CODEX_HOME" {
				return bound, true
			}
			return innerLookup(key)
		}
		// The bound home's own daemon already holds the target: a probe that went
		// there would read matches and restart nothing.
		boundDaemon := startFakeDaemon(t, accountReadReply(residentMain))
		linkSocket(t, f.app, f.holder, boundDaemon.socket)
		canonicalBound, err := filepath.EvalSymlinks(bound)
		if err != nil {
			t.Fatal(err)
		}
		if got := f.holder.ResidentDaemon(f.app.Env).Socket; !strings.HasPrefix(got, canonicalBound) {
			t.Fatalf("fixture: bound socket %s is not under %s", got, canonicalBound)
		}

		_, stderr := f.use(t, context.Background(), commonOpts{Format: formatJSON, NoRestart: noRestart}, constants.ToolCodex, "main")
		if n := boundDaemon.accepted.Load(); n != 0 {
			t.Errorf("noRestart=%v: the bound home's daemon was probed %d times", noRestart, n)
		}
		manual := "CODEX_HOME=" + shellSingleQuote(strings.TrimPrefix(realSpec.Env[0], "CODEX_HOME=")) +
			" codex app-server daemon restart"
		if noRestart {
			if !strings.Contains(stderr, "kae does not restart the managed daemon (--no-restart); to move it to the live account, run: "+manual+"\n") {
				t.Errorf("the manual step does not name the real home %q:\n%s", manual, stderr)
			}
			continue
		}
		if f.restartCount() != 1 {
			t.Fatalf("restarted %d times, want once (the real home's daemon differs)", f.restartCount())
		}
		if want := strings.TrimPrefix(realSpec.Env[0], "CODEX_HOME="); f.restarts[0].codexHome != want {
			t.Errorf("the restart sees CODEX_HOME=%s, want the real home %s", f.restarts[0].codexHome, want)
		}
	}
}

// Outside such a shell the manual step is the bare command.
func TestResidentRestartCommandIsBareForTheRealHome(t *testing.T) {
	f := newResidentFixture(t)
	f.app.pinnedGlobalScope()
	if got := f.app.residentRestartCommand(f.holder, f.holder.ResidentDaemon(f.app.Env)); got != "codex app-server daemon restart" {
		t.Errorf("command = %q", got)
	}
}

// --auto is the hook shape and --no-restart opts out; neither stands for the
// other.
func TestParseUseFlagsWiresTheResidentFlags(t *testing.T) {
	for _, tc := range []struct {
		flags           []string
		hook, noRestart bool
	}{
		{nil, false, false},
		{[]string{"--auto"}, true, false},
		{[]string{"--no-restart"}, false, true},
		{[]string{"--auto", "--no-restart", "--yes"}, true, true},
	} {
		opts, _, ok := parseUseFlags(tc.flags)
		if !ok {
			t.Fatalf("%q: parse failed", tc.flags)
		}
		if opts.ResidentHook != tc.hook || opts.NoRestart != tc.noRestart {
			t.Errorf("%q: ResidentHook=%v NoRestart=%v, want %v %v", tc.flags, opts.ResidentHook, opts.NoRestart, tc.hook, tc.noRestart)
		}
	}
}

// A restart command still running at the limit is restart_pending: kae stops
// waiting, leaves it running (runner.LaunchWithEnv's ErrStillRunning), warns with
// the manual step for a restart that does not go on, and does not probe the
// daemon again; the switch stays applied.
func TestRestartLeftRunningAtTheLimit(t *testing.T) {
	f := newResidentFixture(t)
	d := f.withDaemon(t, residentSide)
	f.app.residentRestartTimeout = 50 * time.Millisecond
	var deadline time.Duration
	var probesBefore int32
	saved := runner.LaunchWithEnv
	t.Cleanup(func() { runner.LaunchWithEnv = saved })
	runner.LaunchWithEnv = func(ctx context.Context, _ []string, _ string, _ ...string) (int, error) {
		if dl, ok := ctx.Deadline(); ok {
			deadline = time.Until(dl)
		}
		probesBefore = d.accepted.Load()
		<-ctx.Done()
		return -1, runner.ErrStillRunning
	}
	stdout, stderr := f.use(t, context.Background(), commonOpts{Format: formatJSON}, constants.ToolCodex, "main")
	wantCodexResidents(t, stdout, daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestartPending), sessionEntry)
	const want = "kae: warning: codex: codex app-server daemon restart did not finish in time, so kae left it running and stopped waiting; it may be waiting for running tasks to finish before it restarts the managed daemon; the switch is kept; if the managed daemon does not restart, run: codex app-server daemon restart\n"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr lacks %q:\n%s", want, stderr)
	}
	if got := d.accepted.Load(); got != probesBefore {
		t.Errorf("the daemon was probed %d more times after a pending restart", got-probesBefore)
	}
	if strings.Contains(stderr, "to retry") {
		t.Errorf("a pending restart was reported as failed:\n%s", stderr)
	}
	if deadline <= 0 || deadline > 50*time.Millisecond {
		t.Errorf("the restart ran with %v left, want at most the 50ms limit", deadline)
	}
	if f.liveAccountID(t) != residentMain {
		t.Error("a pending restart rolled the switch back")
	}
}

// The production limit on the restart command is 30 s.
func TestRestartLimitDefault(t *testing.T) {
	if got := (&App{}).residentRestartLimit(); got != 30*time.Second {
		t.Errorf("default restart limit = %v, want 30s", got)
	}
}

// Each re-probe is bounded by what is left of the 5 s: a daemon that stops
// answering after a restart that took 4.9 s costs 0.1 s more, not a whole probe
// timeout.
func TestReprobesStayWithinTheWait(t *testing.T) {
	f := newResidentFixture(t)
	d := f.withDaemon(t, residentSide)
	f.app.residentProbeTimeout = 10 * time.Second
	// After the restart, the first clock reading (the 5 s deadline) is the
	// fixture's; every later one is 4.9 s on, as if the wait had nearly run out.
	restarted, reads := false, 0
	f.stubRestart(t, func(*restartCall) int {
		d.answers("")
		restarted = true
		return 0
	})
	base := f.app.Now
	f.app.Now = func() time.Time {
		now := base()
		if restarted {
			reads++
			if reads > 1 {
				return now.Add(4900 * time.Millisecond)
			}
		}
		return now
	}
	start := time.Now()
	stdout, _ := f.use(t, context.Background(), commonOpts{Format: formatJSON}, constants.ToolCodex, "main")
	elapsed := time.Since(start)
	wantCodexResidents(t, stdout, daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestartUnverified), sessionEntry)
	if elapsed > 3*time.Second {
		t.Errorf("the wait took %v; a re-probe outlived the 5 s", elapsed)
	}
}

// A launcher that answers exit 0 after the limit has passed restarted the daemon:
// only its ErrStillRunning makes a restart restart_pending, not the limit itself.
func TestRestartExitingZeroAfterTheLimitIsRestarted(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	f.app.residentRestartTimeout = 20 * time.Millisecond
	f.stubRestart(t, f.restartTo(t))
	inner := runner.LaunchWithEnv
	runner.LaunchWithEnv = func(ctx context.Context, env []string, name string, args ...string) (int, error) {
		<-ctx.Done()
		return inner(ctx, env, name, args...)
	}
	stdout, _ := f.use(t, context.Background(), commonOpts{Format: formatJSON}, constants.ToolCodex, "main")
	wantCodexResidents(t, stdout, daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestarted), sessionEntry)
}

// Through the real runner, a restart command that leaves a process running (as
// codex app-server daemon restart leaves the daemon) does not hold kae: the
// process inherits no pipe kae waits on. A stand-in codex on a temporary PATH
// backgrounds a sleep and exits 0. Neither touches HOME, and no real codex runs.
func TestRestartDoesNotWaitForWhatItLeavesRunning(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	f.app.residentRestartTimeout = 300 * time.Millisecond
	// The background sleep outlasts the 3 s bound below, so a kae that waited for
	// it would fail the bound.
	standInCodex(t, "#!/bin/sh\nsleep 5 &\nexit 0\n")
	start := time.Now()
	stdout, _ := f.use(t, context.Background(), commonOpts{Format: formatJSON}, constants.ToolCodex, "main")
	elapsed := time.Since(start)
	wantCodexResidents(t, stdout, daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestartUnverified), sessionEntry)
	if elapsed > 3*time.Second {
		t.Errorf("the switch took %v: it waited on the process the restart left running", elapsed)
	}
}

// Through the real runner, a restart command still running at the limit is not
// killed: kae returns restart_pending, and the command goes on to finish its work
// afterwards, as upstream's restart starts the new daemon only after the old one
// has stopped. dash is covered by the runner's own test.
func TestRestartStillRunningAtTheLimitKeepsRunning(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	f.app.residentRestartTimeout = 100 * time.Millisecond
	marker := filepath.Join(t.TempDir(), "restarted")
	standInCodex(t, fmt.Sprintf("#!/bin/sh\nsleep 1\necho done > '%s'\n", marker))
	// Time the restart alone, not the whole switch, and look for the marker as it
	// returns, so a loaded machine's slower transaction counts against neither.
	var waited time.Duration
	finishedInTime := false
	launch := runner.LaunchWithEnv
	runner.LaunchWithEnv = func(ctx context.Context, env []string, name string, args ...string) (int, error) {
		start := time.Now()
		code, err := launch(ctx, env, name, args...)
		waited = time.Since(start)
		_, statErr := os.Stat(marker)
		finishedInTime = statErr == nil
		return code, err
	}
	stdout, _ := f.use(t, context.Background(), commonOpts{Format: formatJSON}, constants.ToolCodex, "main")
	if finishedInTime {
		t.Fatal("the restart finished within the limit; the test proves nothing")
	}
	if waited <= 0 || waited > 700*time.Millisecond {
		t.Errorf("kae waited %v for the restart, want about the 100ms limit", waited)
	}
	wantCodexResidents(t, stdout, daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestartPending), sessionEntry)
	if !pathAppears(marker, 5*time.Second) {
		t.Fatal("the restart command did not go on after kae stopped waiting for it")
	}
}

// pathAppears polls for path for at most within and reports whether it appeared.
func pathAppears(path string, within time.Duration) bool {
	for deadline := time.Now().Add(within); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}

// standInCodex puts an executable codex with script on a temporary PATH and
// restores the real runner.LaunchWithEnv for the test.
func standInCodex(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "codex"), script)
	if err := os.Chmod(filepath.Join(bin, "codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	saved := runner.LaunchWithEnv
	runner.LaunchWithEnv = osLaunchWithEnv
	t.Cleanup(func() { runner.LaunchWithEnv = saved })
}

// A restart command that cannot be started (no codex on PATH) is restart_failed
// with its own warning, not an exit status it never had.
func TestRestartThatCannotStart(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	t.Setenv("PATH", t.TempDir())
	saved := runner.LaunchWithEnv
	runner.LaunchWithEnv = osLaunchWithEnv
	t.Cleanup(func() { runner.LaunchWithEnv = saved })
	stdout, stderr := f.use(t, context.Background(), commonOpts{Format: formatJSON}, constants.ToolCodex, "main")
	wantCodexResidents(t, stdout, daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestartFailed), sessionEntry)
	if !strings.Contains(stderr, "kae: warning: codex: could not run codex app-server daemon restart (") ||
		!strings.Contains(stderr, "); the switch is kept, and the managed daemon may not be using the live account yet; to retry, run: codex app-server daemon restart\n") {
		t.Errorf("stderr lacks the could-not-run warning:\n%s", stderr)
	}
	if strings.Contains(stderr, "failed (exit") {
		t.Errorf("an unstarted command was reported with an exit status:\n%s", stderr)
	}
}

// The probe and the account-change check share one read of the target
// credential.
func TestResidentsBeforeSwitchReadsTheTargetOnce(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	reads := 0
	target := func(context.Context) ([]byte, bool) {
		reads++
		return []byte(codexChatGPTAuth(residentMain, "codex-main-token")), true
	}
	var slot residentSlot
	_, stderr := captureStderr(t, func() int {
		f.app.residentsBeforeSwitch(context.Background(), constants.ToolCodex, target, &slot, residentMode{dryRun: true})
		return 0
	})
	if reads != 1 {
		t.Errorf("read the target %d times, want once", reads)
	}
	if !reflect.DeepEqual(slot.Residents, []residentEntry{
		daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomePlanned), sessionEntry,
	}) {
		t.Errorf("residents = %+v\n%s", slot.Residents, stderr)
	}
}

// runUseAuto is the hook shape by construction: called without ResidentHook,
// it still only warns, rather than announcing a restart it never runs.
func TestUseAutoIsTheHookShapeWithoutTheFlag(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	code, stdout, stderr := captureBoth(t, func() int {
		return runUseAuto(context.Background(), f.app, commonOpts{Format: formatJSON}, "main", true)
	})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	wantCodexResidents(t, stdout, daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeWarned), sessionEntry)
	if !strings.Contains(stderr, warnHook+"\n") || strings.Contains(stderr, noticeRestart) {
		t.Errorf("stderr is not the hook's warning:\n%s", stderr)
	}
	if f.restartCount() != 0 {
		t.Error("the hook shape restarted the daemon")
	}
}
