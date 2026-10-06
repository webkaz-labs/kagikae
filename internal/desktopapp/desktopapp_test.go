package desktopapp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/runner"
)

// これらのテストは runner.With でプロセス全体の runner を差し替えるため
// t.Parallel にしない。

const testBundleID = "com.example.app"

type call struct {
	launch bool
	name   string
	args   []string
}

// fakeApp は状態を持つ偽 runner。quit を受理する前の `is running` は running を、
// 受理した後は stillRunning 回だけ true を返し、その後 false を返す。
type fakeApp struct {
	calls []call

	// runningReply が非 nil なら、quit 前の `is running` にこの stdout / code を返す。
	runningReply *reply
	quit         reply
	// stillRunning は quit 受理後に `is running` が true を返す回数。負なら止まらない。
	stillRunning int
	// pollReply が非 nil なら、quit 後の `is running` は常にこれを返す。
	pollReply *reply
	launch    reply
	launchErr error

	quitSeen bool
}

type reply struct {
	stdout, stderr string
	code           int
}

func (f *fakeApp) Run(_ context.Context, name string, args ...string) (string, string, int) {
	f.calls = append(f.calls, call{name: name, args: append([]string(nil), args...)})
	if name != "osascript" {
		return "", "unexpected program", 99
	}
	if len(args) == 2 && strings.HasSuffix(args[1], " is running") {
		if !f.quitSeen {
			if f.runningReply != nil {
				return f.runningReply.stdout, f.runningReply.stderr, f.runningReply.code
			}
			return "true\n", "", 0
		}
		if f.pollReply != nil {
			return f.pollReply.stdout, f.pollReply.stderr, f.pollReply.code
		}
		if f.stillRunning != 0 {
			if f.stillRunning > 0 {
				f.stillRunning--
			}
			return "true\n", "", 0
		}
		return "false\n", "", 0
	}
	if strings.Contains(strings.Join(args, "\n"), " to quit") {
		f.quitSeen = f.quit.code == 0
		return f.quit.stdout, f.quit.stderr, f.quit.code
	}
	return "", "unexpected script", 99
}

func (f *fakeApp) RunInput(ctx context.Context, _ string, name string, args ...string) (string, string, int) {
	return f.Run(ctx, name, args...)
}

func (f *fakeApp) Launch(_ context.Context, name string, args ...string) (int, error) {
	f.calls = append(f.calls, call{launch: true, name: name, args: append([]string(nil), args...)})
	return f.launch.code, f.launchErr
}

// fakeClock は Sleep で時刻を進める。
type fakeClock struct {
	t      time.Time
	sleeps []time.Duration
}

func (c *fakeClock) controller() Controller {
	return Controller{
		GOOS:  "darwin",
		Now:   func() time.Time { return c.t },
		Sleep: func(_ context.Context, d time.Duration) { c.sleeps = append(c.sleeps, d); c.t = c.t.Add(d) },
	}
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)} }

var runningArgv = []string{"-e", `application id "com.example.app" is running`}

var quitArgv = []string{
	"-e", `if application id "com.example.app" is running then`,
	"-e", "with timeout of 10 seconds",
	"-e", `tell application id "com.example.app" to quit`,
	"-e", "end timeout",
	"-e", "end if",
}

func TestRunningReadsOnlyTrueAndFalseWithExitZero(t *testing.T) {
	cases := []struct {
		name string
		r    reply
		want State
	}{
		{"running", reply{stdout: "true\n"}, StateRunning},
		{"not running", reply{stdout: "false\n"}, StateNotRunning},
		{"true with a failing exit", reply{stdout: "true\n", code: 1}, StateUnknown},
		{"false with a failing exit", reply{stdout: "false\n", code: 1}, StateUnknown},
		{"error text", reply{stderr: "execution error: (-1728)", code: 1}, StateUnknown},
		{"other stdout", reply{stdout: "missing value\n"}, StateUnknown},
		{"empty stdout", reply{}, StateUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.r
			fake := &fakeApp{runningReply: &r}
			var got State
			var err error
			runner.With(fake, func() { got, err = newClock().controller().Running(context.Background(), testBundleID) })
			if err != nil || got != tc.want {
				t.Fatalf("Running = %v, %v; want %v", got, err, tc.want)
			}
			want := []call{{name: "osascript", args: runningArgv}}
			if !reflect.DeepEqual(fake.calls, want) {
				t.Fatalf("calls = %#v, want %#v", fake.calls, want)
			}
		})
	}
}

func TestQuitObservedThenRelaunched(t *testing.T) {
	fake := &fakeApp{stillRunning: 3}
	clock := newClock()
	var got Outcome
	var err error
	runner.With(fake, func() { got, err = clock.controller().QuitAndRelaunch(context.Background(), testBundleID) })
	if err != nil || got != OutcomeRelaunched {
		t.Fatalf("QuitAndRelaunch = %v, %v; want relaunched", got, err)
	}
	want := []call{
		{name: "osascript", args: quitArgv},
		{name: "osascript", args: runningArgv},
		{name: "osascript", args: runningArgv},
		{name: "osascript", args: runningArgv},
		{name: "osascript", args: runningArgv},
		{launch: true, name: "open", args: []string{"-b", testBundleID}},
	}
	if !reflect.DeepEqual(fake.calls, want) {
		t.Fatalf("calls = %#v\nwant %#v", fake.calls, want)
	}
	// 間隔は契約値（docs/CLI.md § kae use Semantics の手順 5）なので定数でなく値で書く。
	if wantSleeps := []time.Duration{250 * time.Millisecond, 250 * time.Millisecond, 250 * time.Millisecond}; !reflect.DeepEqual(clock.sleeps, wantSleeps) {
		t.Fatalf("sleeps = %v, want %v", clock.sleeps, wantSleeps)
	}
}

// quit スクリプトは `is running` で包まれていなければならない。包まないと quit の
// ためにアプリを起動してしまう。
func TestQuitScriptIsWrappedInIsRunning(t *testing.T) {
	args := quitArgs(testBundleID)
	script := []string{}
	for i := 0; i < len(args); i += 2 {
		if args[i] != "-e" {
			t.Fatalf("argv %v: every line must follow -e", args)
		}
		script = append(script, args[i+1])
	}
	if first, last := script[0], script[len(script)-1]; first != `if application id "com.example.app" is running then` || last != "end if" {
		t.Fatalf("quit script is not wrapped in an is running test:\n%s", strings.Join(script, "\n"))
	}
}

func TestQuitTimeoutDoesNotRelaunch(t *testing.T) {
	fake := &fakeApp{stillRunning: -1}
	clock := newClock()
	var got Outcome
	runner.With(fake, func() { got, _ = clock.controller().QuitAndRelaunch(context.Background(), testBundleID) })
	if got != OutcomeQuitTimeout {
		t.Fatalf("outcome = %v, want quit_timeout", got)
	}
	for _, c := range fake.calls {
		if c.launch || c.name != "osascript" {
			t.Fatalf("ran %#v after a timeout", c)
		}
	}
	// 20 秒 / 250ms = 80 回待ち、締切の時点で最後にもう一度問う。
	if n := len(clock.sleeps); n != 80 {
		t.Fatalf("slept %d times, want 80", n)
	}
	if elapsed := clock.t.Sub(newClock().t); elapsed != 20*time.Second {
		t.Fatalf("waited %v, want 20s", elapsed)
	}
	if polls := len(fake.calls) - 1; polls != 81 {
		t.Fatalf("polled %d times", polls)
	}
}

// 待機中に unknown が返っても終了とは見なさない。
func TestUnknownPollIsNotAStop(t *testing.T) {
	fake := &fakeApp{pollReply: &reply{stderr: "boom", code: 1}}
	var got Outcome
	runner.With(fake, func() { got, _ = newClock().controller().QuitAndRelaunch(context.Background(), testBundleID) })
	if got != OutcomeQuitTimeout {
		t.Fatalf("outcome = %v, want quit_timeout", got)
	}
	for _, c := range fake.calls {
		if c.launch {
			t.Fatalf("relaunched on an unknown poll")
		}
	}
}

func TestCancelledContextStopsWaiting(t *testing.T) {
	fake := &fakeApp{stillRunning: -1}
	ctx, cancel := context.WithCancel(context.Background())
	clock := newClock()
	c := clock.controller()
	c.Sleep = func(_ context.Context, d time.Duration) { cancel(); clock.t = clock.t.Add(d) }
	var got Outcome
	runner.With(fake, func() { got, _ = c.QuitAndRelaunch(ctx, testBundleID) })
	if got != OutcomeQuitTimeout || len(fake.calls) != 3 {
		t.Fatalf("outcome = %v after %d calls", got, len(fake.calls))
	}
}

func TestQuitFailures(t *testing.T) {
	cases := []struct {
		name string
		r    reply
		want Outcome
	}{
		{"automation not permitted", reply{stderr: "35:70: execution error: Nicht berechtigt. (-1743)\n", code: 1}, OutcomeQuitDenied},
		{"other error number", reply{stderr: "execution error: timed out. (-1712)\n", code: 1}, OutcomeQuitFailed},
		{"-1743 without the parentheses is not read", reply{stderr: "error -17430\n", code: 1}, OutcomeQuitFailed},
		{"no output", reply{code: 1}, OutcomeQuitFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeApp{quit: tc.r}
			var got Outcome
			runner.With(fake, func() { got, _ = newClock().controller().QuitAndRelaunch(context.Background(), testBundleID) })
			if got != tc.want {
				t.Fatalf("outcome = %v, want %v", got, tc.want)
			}
			if want := []call{{name: "osascript", args: quitArgv}}; !reflect.DeepEqual(fake.calls, want) {
				t.Fatalf("calls after a failed quit = %#v", fake.calls)
			}
		})
	}
}

func TestRelaunchFailure(t *testing.T) {
	cases := []struct {
		name string
		code int
		err  error
	}{
		{"open exits non-zero", 1, nil},
		// 起動できない場合はエラーだけで失敗と判定する（exit code に頼らない）。
		{"open cannot start", 0, errors.New("exec: not found")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeApp{launch: reply{code: tc.code}, launchErr: tc.err}
			var got Outcome
			runner.With(fake, func() { got, _ = newClock().controller().QuitAndRelaunch(context.Background(), testBundleID) })
			if got != OutcomeRelaunchFailed {
				t.Fatalf("outcome = %v, want relaunch_failed", got)
			}
		})
	}
}

func TestOffDarwinRunsNothing(t *testing.T) {
	fake := &fakeApp{}
	c := Controller{GOOS: "linux"}
	var state State
	var stateErr, quitErr error
	runner.With(fake, func() {
		state, stateErr = c.Running(context.Background(), testBundleID)
		_, quitErr = c.QuitAndRelaunch(context.Background(), testBundleID)
	})
	if state != StateNotRunning || stateErr != nil || !errors.Is(quitErr, ErrUnsupported) {
		t.Fatalf("Running = %v, %v; QuitAndRelaunch err = %v", state, stateErr, quitErr)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("ran %#v off darwin", fake.calls)
	}
}

func TestInvalidBundleIDRunsNothing(t *testing.T) {
	for _, id := range []string{
		"",
		"codex",
		`com.example" to quit`,
		"com.example.app\nend if",
		`com.example\app`,
		"com.example app",
		"com..example",
		".com.example",
		strings.Repeat("a.", 128) + "b",
	} {
		fake := &fakeApp{}
		c := newClock().controller()
		var runErr, quitErr error
		runner.With(fake, func() {
			_, runErr = c.Running(context.Background(), id)
			_, quitErr = c.QuitAndRelaunch(context.Background(), id)
		})
		if !errors.Is(runErr, ErrInvalidBundleID) || !errors.Is(quitErr, ErrInvalidBundleID) {
			t.Fatalf("%q: errors %v, %v", id, runErr, quitErr)
		}
		if len(fake.calls) != 0 {
			t.Fatalf("%q: ran %#v", id, fake.calls)
		}
	}
	// 実際に使う bundle id は通る。
	if !validBundleID("com.openai.codex") {
		t.Fatal("com.openai.codex rejected")
	}
}
