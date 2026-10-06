package cmd

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
	"github.com/webkaz-labs/kagikae/internal/textui"
)

// The ChatGPT app's confirmation, quit and relaunch (docs/CLI.md § kae use
// Semantics, **Resident processes (codex)**, step 5). These swap runner.Default,
// runner.LaunchWithEnv and os.Stdout/os.Stderr, so none of them runs in parallel.

// chatGPTBundleID is the bundle id the fake app answers to; the tests hand it to
// the App's seam so they run the same on every platform.
const chatGPTBundleID = "com.openai.codex"

// fakeChatGPT is a stateful osascript and open: `is running` answers running
// until a quit is accepted, then stopped (or still running when it never stops).
// It records each call in order, and, at the quit, whether the codex lock was
// free and how many daemon restarts had run.
type fakeChatGPT struct {
	t    *testing.T
	f    *residentFixture
	next runner.Runner

	mu         sync.Mutex
	running    bool   // before the quit
	queryFails bool   // `is running` exits non-zero
	neverStops bool   // the app stays running after the quit
	quitStderr string // with quitCode, the quit's failure
	quitCode   int
	openCode   int

	quitSeen       bool
	calls          []string // "query", "quit", "open"
	openArgv       []string
	quitLockFree   bool
	restartsAtQuit int
}

func (a *fakeChatGPT) Run(ctx context.Context, name string, args ...string) (string, string, int) {
	if name != "osascript" {
		return a.next.Run(ctx, name, args...)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(args) < 2 || args[1] != `set i to "`+chatGPTBundleID+`"` {
		a.t.Errorf("osascript argv does not name the allowlisted bundle id: %q", args)
	}
	script := strings.Join(args, "\n")
	switch {
	case strings.Contains(script, "tell application id i to quit"):
		a.calls = append(a.calls, "quit")
		if held, err := lock.Acquire(a.f.app.Paths.LocksDir(), constants.ToolCodex); err == nil {
			a.quitLockFree = true
			held.Release()
		}
		a.restartsAtQuit = a.f.restartCount()
		a.quitSeen = a.quitCode == 0
		return "", a.quitStderr, a.quitCode
	case strings.Contains(script, "set r to application id i is running"):
		a.calls = append(a.calls, "query")
		if a.queryFails {
			return "", "osascript: some failure", 1
		}
		if a.quitSeen && !a.neverStops {
			return "false\n", "", 0
		}
		if a.running {
			return "true\n", "", 0
		}
		return "false\n", "", 0
	}
	a.t.Errorf("unexpected osascript: %q", args)
	return "", "", 1
}

func (a *fakeChatGPT) RunInput(ctx context.Context, stdin, name string, args ...string) (string, string, int) {
	if name == "osascript" {
		return a.Run(ctx, name, args...)
	}
	return a.next.RunInput(ctx, stdin, name, args...)
}

func (a *fakeChatGPT) Launch(_ context.Context, name string, args ...string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, name)
	a.openArgv = append([]string(nil), args...)
	return a.openCode, nil
}

// quits counts the quit requests the app received.
func (a *fakeChatGPT) quits() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.Count(strings.Join(a.calls, " "), "quit")
}

// withChatGPT gives f's App the ChatGPT app on the darwin path, running unless
// the caller changes the returned fake, and no terminal.
func (f *residentFixture) withChatGPT(t *testing.T) *fakeChatGPT {
	t.Helper()
	a := &fakeChatGPT{t: t, f: f, next: runner.Default, running: true}
	saved := runner.Default
	runner.Default = a
	t.Cleanup(func() { runner.Default = saved })
	f.app.desktopApps = func(adapter.ResidentHolder) []string { return []string{chatGPTBundleID} }
	f.app.desktopGOOS = "darwin"
	return a
}

// fakeTerminal is the terminal a test answers on: what kae writes, and the
// answer it reads.
type fakeTerminal struct {
	in  io.Reader
	out bytes.Buffer
}

func (r *fakeTerminal) Read(p []byte) (int, error)  { return r.in.Read(p) }
func (r *fakeTerminal) Write(p []byte) (int, error) { return r.out.Write(p) }

// answering gives f's App a terminal on which the user types answer.
func (f *residentFixture) answering(answer string) *fakeTerminal {
	term := &fakeTerminal{in: strings.NewReader(answer)}
	f.app.openTerminal = func() (*textui.Terminal, bool) { return &textui.Terminal{}, true }
	f.app.terminalIOForTest = term
	return term
}

func appEntry(observed, outcome string) residentEntry {
	return residentEntry{Kind: constants.ResidentKindDesktopApp, Observed: observed, Outcome: outcome}
}

const (
	appPrompt         = "ChatGPT keeps the codex account it started with. Quit and relaunch it now? Tasks running in ChatGPT will be interrupted. [y/N]: "
	appNoticeAhead    = "kae: note: codex: the ChatGPT app is running and keeps the codex account it started with; after the switch, kae quits and relaunches it with your consent"
	appNoticePlanned  = "kae: note: codex: the ChatGPT app is running and keeps the codex account it started with; the switch would quit and relaunch it with your consent"
	appWarnOptedOut   = "kae: warning: codex: the ChatGPT app keeps the codex account it started with, and --no-restart leaves it running; to use the codex account now live, quit and reopen it"
	appWarnHook       = "kae: warning: codex: the ChatGPT app keeps the codex account it started with, and the enter hook (--auto) does not quit it; to use the codex account now live, quit and reopen it"
	appWarnCannotAsk  = "kae: warning: codex: the ChatGPT app keeps the codex account it started with, and kae cannot ask here whether to quit it (no terminal, or --json); to use the codex account now live, quit and reopen it, or pass --yes to let kae do it"
	appWarnUnknown    = "kae: warning: codex: could not tell whether the ChatGPT app is running; if it is, quit and reopen it to use the codex account now live"
	appWarnDeclined   = "kae: warning: codex: left the ChatGPT app running; it keeps the codex account it started with until you quit and reopen it"
	appNoteRelaunched = "kae: note: codex: quit and relaunched the ChatGPT app, so it uses the codex account now live"
	appWarnNoRelaunch = "kae: warning: codex: the ChatGPT app quit, but kae could not open it again (open -b com.openai.codex); open it yourself"
	appWarnTimeout    = "kae: warning: codex: the ChatGPT app did not quit in time and kae left it running; it keeps the codex account it started with until you quit and reopen it"
	appWarnDenied     = "kae: warning: codex: macOS did not let kae control the ChatGPT app; to use the codex account now live, quit it yourself (Command-Q in the app) and open it again"
	appWarnQuitFailed = "kae: warning: codex: could not ask the ChatGPT app to quit; to use the codex account now live, quit it yourself (Command-Q in the app) and open it again"
	addAppNoticeAhead = "kae: note: codex: the ChatGPT app is running and keeps the codex account it started with; kae quits and relaunches it now with your consent"
	rbAppNoticeAhead  = "kae: note: codex: the ChatGPT app is running and keeps the codex account it started with; after the rollback, kae quits and relaunches it with your consent"
	rbAppNoticePlan   = "kae: note: codex: the ChatGPT app is running and keeps the codex account it started with; the rollback would quit and relaunch it with your consent"
	// Any line about the app contains this.
	appMention = "ChatGPT"
)

func requireLines(t *testing.T, stderr string, lines ...string) {
	t.Helper()
	for _, line := range lines {
		if !strings.Contains(stderr, line+"\n") {
			t.Errorf("stderr lacks %q:\n%s", line, stderr)
		}
	}
}

// Answered yes on the terminal: kae asks with the contract's sentence, after the
// switch has released its lock and after the daemon's restart, asks the app to
// quit, sees it stop, and relaunches it with `open -b <id>`.
func TestUseAsksThenQuitsAndRelaunchesTheChatGPTApp(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	f.stubRestart(t, f.restartTo(t))
	chatgpt := f.withChatGPT(t)
	term := f.answering("y\n")

	stdout, stderr := f.use(t, context.Background(), commonOpts{Format: formatText}, constants.ToolCodex, "main")
	if term.out.String() != appPrompt {
		t.Errorf("prompt = %q, want %q", term.out.String(), appPrompt)
	}
	if want := []string{"query", "quit", "query", "open"}; !reflect.DeepEqual(chatgpt.calls, want) {
		t.Errorf("calls = %q, want %q", chatgpt.calls, want)
	}
	if want := []string{"-b", chatGPTBundleID}; !reflect.DeepEqual(chatgpt.openArgv, want) {
		t.Errorf("open argv = %q, want %q", chatgpt.openArgv, want)
	}
	if !chatgpt.quitLockFree {
		t.Error("the app was quit while the codex switch lock was held")
	}
	if chatgpt.restartsAtQuit != 1 {
		t.Errorf("the quit ran after %d daemon restarts, want after the one restart", chatgpt.restartsAtQuit)
	}
	requireLines(t, stderr, appNoticeAhead, appNoteRelaunched)
	if strings.Index(stderr, appNoticeAhead) > strings.Index(stderr, appNoteRelaunched) {
		t.Errorf("the outcome precedes the notice:\n%s", stderr)
	}
	assertNoResidentPII(t, stdout, stderr, term.out.String())
}

// Answered no, or just Enter (the default): nothing is quit and the outcome is
// declined.
func TestUseDeclinedLeavesTheChatGPTAppRunning(t *testing.T) {
	for _, answer := range []string{"n\n", "\n", "no\n", ""} {
		f := newResidentFixture(t)
		chatgpt := f.withChatGPT(t)
		term := f.answering(answer)
		_, stderr := f.use(t, context.Background(), commonOpts{Format: formatText}, constants.ToolCodex, "main")
		if term.out.String() != appPrompt {
			t.Errorf("%q: prompt = %q", answer, term.out.String())
		}
		if chatgpt.quits() != 0 || slices.Contains(chatgpt.calls, "open") {
			t.Errorf("%q: calls = %q, want the query only", answer, chatgpt.calls)
		}
		requireLines(t, stderr, appNoticeAhead, appWarnDeclined)
	}
}

// The JSON report carries the app's entry between the daemon's and the
// sessions'. --yes quits and relaunches without asking, with or without a
// terminal; without --yes, a run with no terminal or with --json warns and asks
// nothing.
func TestUseChatGPTAppConsent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		opts     commonOpts
		terminal bool
		outcome  string
		lines    []string
		quit     bool
	}{
		{"yes without a terminal", commonOpts{Yes: true, Format: formatJSON}, false, constants.ResidentOutcomeRelaunched, []string{appNoticeAhead, appNoteRelaunched}, true},
		{"yes on a terminal", commonOpts{Yes: true, Format: formatJSON}, true, constants.ResidentOutcomeRelaunched, []string{appNoticeAhead, appNoteRelaunched}, true},
		{"no terminal", commonOpts{Format: formatJSON}, false, constants.ResidentOutcomeWarned, []string{appWarnCannotAsk}, false},
		{"json on a terminal", commonOpts{Format: formatJSON}, true, constants.ResidentOutcomeWarned, []string{appWarnCannotAsk}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newResidentFixture(t)
			chatgpt := f.withChatGPT(t)
			var term *fakeTerminal
			if tc.terminal {
				term = f.answering("n\n")
			}
			stdout, stderr := f.use(t, context.Background(), tc.opts, constants.ToolCodex, "main")
			wantCodexResidents(t, stdout,
				daemonEntry(constants.ResidentObservedAbsent, constants.ResidentOutcomeNone),
				appEntry(constants.ResidentObservedRunning, tc.outcome), sessionEntry)
			if term != nil && term.out.Len() != 0 {
				t.Errorf("prompted: %q", term.out.String())
			}
			if got := chatgpt.quits() == 1; got != tc.quit {
				t.Errorf("quit = %v, want %v (calls %q)", got, tc.quit, chatgpt.calls)
			}
			requireLines(t, stderr, tc.lines...)
			if tc.quit && strings.Contains(stderr, appWarnCannotAsk) {
				t.Errorf("--yes warned that kae cannot ask:\n%s", stderr)
			}
			assertNoResidentPII(t, stdout, stderr)
		})
	}
}

// A text run without a terminal and without --yes warns before the switch and
// does nothing to the app.
func TestUseWithoutATerminalWarnsAboutTheChatGPTApp(t *testing.T) {
	f := newResidentFixture(t)
	chatgpt := f.withChatGPT(t)
	_, stderr := f.use(t, context.Background(), commonOpts{Format: formatText}, constants.ToolCodex, "main")
	requireLines(t, stderr, appWarnCannotAsk)
	if chatgpt.quits() != 0 {
		t.Errorf("quit without consent: %q", chatgpt.calls)
	}
	if strings.Contains(stderr, appNoticeAhead) {
		t.Errorf("announced a quit it cannot ask for:\n%s", stderr)
	}
}

// --no-restart, the hook shape and --dry-run each win over --yes: the app is
// asked about but never quit, and the outcome says why.
func TestUseChatGPTAppSuppressed(t *testing.T) {
	explicit := func(ctx context.Context, app *App, o commonOpts) int {
		return runSwitch(ctx, app, o, constants.ToolCodex, "main")
	}
	hook := func(ctx context.Context, app *App, o commonOpts) int {
		return runUseAuto(ctx, app, o, "main", true)
	}
	for _, tc := range []struct {
		name    string
		opts    commonOpts
		run     func(context.Context, *App, commonOpts) int
		outcome string
		line    string
	}{
		{"no-restart", commonOpts{NoRestart: true, Yes: true}, explicit, constants.ResidentOutcomeOptedOut, appWarnOptedOut},
		{"auto with yes", commonOpts{ResidentHook: true, Yes: true}, hook, constants.ResidentOutcomeWarned, appWarnHook},
		{"dry-run with yes", commonOpts{DryRun: true, Yes: true}, explicit, constants.ResidentOutcomePlanned, appNoticePlanned},
		{"dry-run no-restart", commonOpts{DryRun: true, NoRestart: true, Yes: true}, explicit, constants.ResidentOutcomeOptedOut, appWarnOptedOut},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newResidentFixture(t)
			chatgpt := f.withChatGPT(t)
			term := f.answering("y\n")
			opts := tc.opts
			opts.Format = formatJSON
			code, stdout, stderr := captureBoth(t, func() int { return tc.run(context.Background(), f.app, opts) })
			mustExit(t, constants.ExitOK, code, stdout+stderr)
			session := sessionEntry
			wantCodexResidents(t, stdout,
				daemonEntry(constants.ResidentObservedAbsent, constants.ResidentOutcomeNone),
				appEntry(constants.ResidentObservedRunning, tc.outcome), session)
			if want := []string{"query"}; !reflect.DeepEqual(chatgpt.calls, want) {
				t.Errorf("calls = %q, want %q", chatgpt.calls, want)
			}
			if term.out.Len() != 0 {
				t.Errorf("prompted: %q", term.out.String())
			}
			requireLines(t, stderr, tc.line)
			if strings.Contains(stderr, appNoticeAhead) {
				t.Errorf("announced a quit:\n%s", stderr)
			}
		})
	}
}

// How the quit ended maps to its own outcome and line; only an observed stop is
// followed by `open -b`.
func TestUseChatGPTAppQuitOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		set     func(*fakeChatGPT)
		outcome string
		line    string
		open    bool
	}{
		{"relaunch failed", func(a *fakeChatGPT) { a.openCode = 1 }, constants.ResidentOutcomeRelaunchFailed, appWarnNoRelaunch, true},
		{"timeout", func(a *fakeChatGPT) { a.neverStops = true }, constants.ResidentOutcomeQuitTimeout, appWarnTimeout, false},
		{"denied", func(a *fakeChatGPT) {
			a.quitCode, a.quitStderr = 1, "execution error: Not authorized to send Apple events to ChatGPT. (-1743)"
		}, constants.ResidentOutcomeQuitDenied, appWarnDenied, false},
		{"failed", func(a *fakeChatGPT) {
			a.quitCode, a.quitStderr = 1, "execution error: ChatGPT got an error: Connection is invalid. (-609)"
		}, constants.ResidentOutcomeQuitFailed, appWarnQuitFailed, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newResidentFixture(t)
			chatgpt := f.withChatGPT(t)
			tc.set(chatgpt)
			code, stdout, stderr := captureBoth(t, func() int {
				return runSwitch(context.Background(), f.app, commonOpts{Format: formatJSON, Yes: true}, constants.ToolCodex, "main")
			})
			mustExit(t, constants.ExitOK, code, stdout+stderr)
			wantCodexResidents(t, stdout,
				daemonEntry(constants.ResidentObservedAbsent, constants.ResidentOutcomeNone),
				appEntry(constants.ResidentObservedRunning, tc.outcome), sessionEntry)
			if got := slices.Contains(chatgpt.calls, "open"); got != tc.open {
				t.Errorf("open ran = %v, want %v (calls %q)", got, tc.open, chatgpt.calls)
			}
			requireLines(t, stderr, tc.line)
			if strings.Contains(stderr, appNoteRelaunched) {
				t.Errorf("reported a relaunch:\n%s", stderr)
			}
			if f.liveAccountID(t) != residentMain {
				t.Error("the switch did not stay applied")
			}
			assertNoResidentPII(t, stdout, stderr)
		})
	}
}

// When kae cannot tell whether the app runs, it warns and does nothing, even
// with --yes.
func TestUseChatGPTAppUnknown(t *testing.T) {
	f := newResidentFixture(t)
	chatgpt := f.withChatGPT(t)
	chatgpt.queryFails = true
	stdout, stderr := f.use(t, context.Background(), commonOpts{Format: formatJSON, Yes: true}, constants.ToolCodex, "main")
	wantCodexResidents(t, stdout,
		daemonEntry(constants.ResidentObservedAbsent, constants.ResidentOutcomeNone),
		appEntry(constants.ResidentObservedUnknown, constants.ResidentOutcomeWarned), sessionEntry)
	if want := []string{"query"}; !reflect.DeepEqual(chatgpt.calls, want) {
		t.Errorf("calls = %q, want %q", chatgpt.calls, want)
	}
	requireLines(t, stderr, appWarnUnknown)
}

// Nothing is asked about or done to the app, and no entry is listed, when it is
// not running, off macOS, or when the switch leaves codex's account as it was.
func TestUseLeavesTheChatGPTAppAlone(t *testing.T) {
	for _, tc := range []struct {
		name    string
		set     func(*residentFixture, *fakeChatGPT)
		account string
		queries int
	}{
		{"not running", func(_ *residentFixture, a *fakeChatGPT) { a.running = false }, "main", 1},
		{"not darwin", func(f *residentFixture, _ *fakeChatGPT) { f.app.desktopGOOS = "linux" }, "main", 0},
		{"no desktop apps", func(f *residentFixture, _ *fakeChatGPT) { f.app.desktopApps = nil }, "main", 0},
		{"account unchanged", func(*residentFixture, *fakeChatGPT) {}, "side", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newResidentFixture(t)
			chatgpt := f.withChatGPT(t)
			tc.set(f, chatgpt)
			term := f.answering("y\n")
			stdout, stderr := f.use(t, context.Background(), commonOpts{Format: formatJSON, Yes: true}, constants.ToolCodex, tc.account)
			for _, e := range codexResidents(t, stdout)[constants.ToolCodex] {
				if e.Kind == constants.ResidentKindDesktopApp {
					t.Errorf("listed %+v", e)
				}
			}
			if len(chatgpt.calls) != tc.queries || chatgpt.quits() != 0 {
				t.Errorf("calls = %q, want %d queries and no quit", chatgpt.calls, tc.queries)
			}
			if term.out.Len() != 0 || strings.Contains(stderr, appMention) {
				t.Errorf("said something about the app: prompt %q, stderr:\n%s", term.out.String(), stderr)
			}
		})
	}
}

// Production asks the holder, whose DesktopApps lists nothing off macOS; an App
// without the seam (every test App) lists nothing.
func TestDesktopAppsSeamIsTheHolders(t *testing.T) {
	f := newResidentFixture(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	app := newApp("")
	if got, want := app.desktopAppIDs(f.holder), f.holder.DesktopApps(); !slices.Equal(got, want) {
		t.Errorf("production desktop apps = %q, want the holder's %q", got, want)
	}
	if (&App{}).desktopAppIDs(f.holder) != nil {
		t.Error("an App without the seam lists desktop apps")
	}
}

// kae add's login flow asks after the flow, once its lock is released and the
// daemon restarted; --yes quits without asking.
func TestAddQuitsAndRelaunchesTheChatGPTApp(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	f.stubRestart(t, f.restartTo(t))
	f.loginAs(t, codexChatGPTAuth(residentMain, "codex-login-token"))
	f.app.pinnedGlobalScope()
	chatgpt := f.withChatGPT(t)

	code, stdout, stderr := f.add(t, commonOpts{Format: formatText, Yes: true}, false)
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	if want := []string{"query", "quit", "query", "open"}; !reflect.DeepEqual(chatgpt.calls, want) {
		t.Errorf("calls = %q, want %q", chatgpt.calls, want)
	}
	if !chatgpt.quitLockFree {
		t.Error("the app was quit while the add's codex lock was held")
	}
	if chatgpt.restartsAtQuit != 1 {
		t.Errorf("the quit ran after %d daemon restarts, want after the one restart", chatgpt.restartsAtQuit)
	}
	requireLines(t, stderr, addAppNoticeAhead, appNoteRelaunched)
	assertNoResidentPII(t, stdout, stderr)
}

// kae add --no-login changes no account, so it leaves the app alone even with
// --yes; a login flow that restores the previous account does too.
func TestAddWithoutAnAccountChangeLeavesTheChatGPTAppAlone(t *testing.T) {
	t.Run("no-login", func(t *testing.T) {
		f := newResidentFixture(t)
		chatgpt := f.withChatGPT(t)
		code, stdout, stderr := captureBoth(t, func() int {
			return runCapture(context.Background(), f.app, commonOpts{Format: formatText, Yes: true}, constants.ToolCodex, "side")
		})
		mustExit(t, constants.ExitOK, code, stdout+stderr)
		if len(chatgpt.calls) != 0 || strings.Contains(stderr, appMention) {
			t.Errorf("calls = %q, stderr:\n%s", chatgpt.calls, stderr)
		}
	})
	t.Run("restore", func(t *testing.T) {
		f := newResidentFixture(t)
		f.loginAs(t, codexChatGPTAuth(residentMain, "codex-login-token"))
		f.app.pinnedGlobalScope()
		chatgpt := f.withChatGPT(t)
		code, stdout, stderr := f.add(t, commonOpts{Format: formatText, Yes: true}, true)
		mustExit(t, constants.ExitOK, code, stdout+stderr)
		if len(chatgpt.calls) != 0 || strings.Contains(stderr, appMention) {
			t.Errorf("calls = %q, stderr:\n%s", chatgpt.calls, stderr)
		}
	})
}

// kae rollback carries the app's entry in its restored codex item, quits after
// its locks are released, and --dry-run plans only.
func TestRollbackQuitsAndRelaunchesTheChatGPTApp(t *testing.T) {
	f := newRollbackFixture(t)
	f.withDaemon(t, residentMain)
	f.stubRestart(t, f.restartTo(t))
	chatgpt := f.withChatGPT(t)

	code, stdout, stderr := f.rollback(t, commonOpts{Format: formatJSON, DryRun: true, Yes: true})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	want := []residentEntry{
		daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomePlanned),
		appEntry(constants.ResidentObservedRunning, constants.ResidentOutcomePlanned), sessionEntry,
	}
	if got := rollbackResidents(t, stdout)[constants.ToolCodex]; !reflect.DeepEqual(got, want) {
		t.Errorf("dry-run codex residents = %+v, want %+v", got, want)
	}
	requireLines(t, stderr, rbAppNoticePlan)
	if chatgpt.quits() != 0 {
		t.Fatalf("the dry-run quit the app: %q", chatgpt.calls)
	}

	code, stdout, stderr = f.rollback(t, commonOpts{Format: formatJSON, Yes: true})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	want = []residentEntry{
		daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestarted),
		appEntry(constants.ResidentObservedRunning, constants.ResidentOutcomeRelaunched), sessionEntry,
	}
	if got := rollbackResidents(t, stdout)[constants.ToolCodex]; !reflect.DeepEqual(got, want) {
		t.Errorf("codex residents = %+v, want %+v", got, want)
	}
	if !chatgpt.quitLockFree {
		t.Error("the app was quit while the rollback's codex lock was held")
	}
	if chatgpt.restartsAtQuit != 1 {
		t.Errorf("the quit ran after %d daemon restarts, want after the one restart", chatgpt.restartsAtQuit)
	}
	requireLines(t, stderr, rbAppNoticeAhead, appNoteRelaunched)
	assertNoResidentPII(t, stdout, stderr)
}

// In Japanese the confirmation takes the form docs/L10N-JA.md fixes, and the
// notices and outcomes are Japanese; the accepted answer is still y.
func TestChatGPTAppInJapanese(t *testing.T) {
	l10ntest.UseJapanese(t)
	f := newResidentFixture(t)
	chatgpt := f.withChatGPT(t)
	term := f.answering("y\n")
	_, stderr := f.use(t, context.Background(), commonOpts{Format: formatText}, constants.ToolCodex, "main")
	const prompt = "ChatGPT アプリは起動時の codex アカウントを使い続けます。アプリで実行中のタスクは中断されます。いま終了して再起動する場合は y を入力してください [y/N]: "
	if term.out.String() != prompt {
		t.Errorf("prompt = %q, want %q", term.out.String(), prompt)
	}
	if chatgpt.quits() != 1 {
		t.Errorf("calls = %q, want one quit", chatgpt.calls)
	}
	requireLines(t, stderr,
		"kae: note: codex: ChatGPT アプリが起動中で、起動時の codex アカウントを使い続けています。切替の後に、同意を得たうえで kae が終了して再起動します。",
		"kae: note: codex: ChatGPT アプリを終了して再起動しました。現在有効な codex アカウントを使います。")
}
