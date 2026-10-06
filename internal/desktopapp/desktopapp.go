// Package desktopapp asks whether a macOS desktop app is running and quits and
// relaunches it, by bundle id, through internal/runner. It prompts for nothing
// and decides nothing: the caller (cmd) owns when to act and what to report
// (docs/CLI.md § kae use Semantics, **Resident processes (codex)**), and
// docs/SECURITY.md § Resident processes owns the limits kept here.
//
// It sends no signal and never identifies a process by name, argv or pid: it
// observes an app only through `osascript` (`application id "<id>" is running`),
// quits it only through an Apple Event wrapped in that test, and relaunches it
// only through `open -b <id>` once the quit has been observed. Off darwin every
// call is a no-op that runs nothing.
//
// The bundle id goes into each script as a variable, never as the literal of an
// `application id "<id>"` specifier: a literal is resolved when osascript
// compiles the script, so on a Mac without the app every script, even the
// `is running` query, failed with -1728 before it ran.
package desktopapp

import (
	"context"
	"errors"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/webkaz-labs/kagikae/internal/runner"
)

// State is what an `is running` query observed. The zero value is StateUnknown,
// on which the caller acts on nothing.
type State int

const (
	// StateUnknown: osascript answered anything but `true` or `false` with exit
	// 0, so kae cannot tell (the `observed: unknown` of a residents entry).
	StateUnknown State = iota
	StateRunning
	// StateNotRunning covers an app that is not installed: osascript cannot
	// resolve its id (-1728), which is known not to be running, not a guess.
	StateNotRunning
)

// String names the constant for diagnostics. It is not a residents token: the
// caller maps to internal/constants.
func (s State) String() string {
	switch s {
	case StateUnknown:
		return "StateUnknown"
	case StateRunning:
		return "StateRunning"
	case StateNotRunning:
		return "StateNotRunning"
	}
	return "State(" + strconv.Itoa(int(s)) + ")"
}

// Outcome is how QuitAndRelaunch ended. Each value maps to one residents
// `outcome` token in internal/constants; the mapping belongs to the caller.
type Outcome int

const (
	// OutcomeRelaunched: the quit was observed, then `open -b` succeeded.
	OutcomeRelaunched Outcome = iota + 1
	// OutcomeRelaunchFailed: the quit was observed and `open -b` failed.
	OutcomeRelaunchFailed
	// OutcomeQuitTimeout: the app was not observed stopped by the deadline. It
	// is left running and not relaunched; the quit is never forced.
	OutcomeQuitTimeout
	// OutcomeQuitDenied: macOS refused permission to send the Apple Event
	// (error -1743, automation not permitted).
	OutcomeQuitDenied
	// OutcomeQuitFailed: the quit request failed for another reason.
	OutcomeQuitFailed
)

// String names the constant for diagnostics. It is not a residents token: the
// caller maps to internal/constants.
func (o Outcome) String() string {
	switch o {
	case OutcomeRelaunched:
		return "OutcomeRelaunched"
	case OutcomeRelaunchFailed:
		return "OutcomeRelaunchFailed"
	case OutcomeQuitTimeout:
		return "OutcomeQuitTimeout"
	case OutcomeQuitDenied:
		return "OutcomeQuitDenied"
	case OutcomeQuitFailed:
		return "OutcomeQuitFailed"
	}
	return "Outcome(" + strconv.Itoa(int(o)) + ")"
}

// Wait limits for the quit (docs/CLI.md § kae use Semantics, step 5). The
// deadline counts from before the quit request, so a slow Apple Event eats into
// the wait instead of extending it.
const (
	PollInterval = 250 * time.Millisecond
	QuitDeadline = 20 * time.Second
	// quitEventTimeout bounds the quit Apple Event itself (`with timeout`);
	// it stays inside QuitDeadline.
	quitEventTimeout = 10 * time.Second
	// maxPolls stops the wait even when only one of Now and Sleep is
	// injected, so a frozen clock or a sleep that does not sleep cannot loop.
	maxPolls = int(QuitDeadline/PollInterval) + 1
)

var (
	// ErrInvalidBundleID: the bundle id is not a reverse-DNS name, so it is
	// not placed in an AppleScript source or argv; nothing was run.
	ErrInvalidBundleID = errors.New("desktopapp: invalid bundle id")
	// ErrUnsupported: QuitAndRelaunch was called off darwin; nothing was run.
	ErrUnsupported = errors.New("desktopapp: desktop apps are handled on darwin only")
)

// bundleIDPattern admits reverse-DNS names only. The id is interpolated into an
// AppleScript string literal, so a quote, backslash, space or newline must never
// pass.
var bundleIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$`)

const maxBundleIDLen = 255

// Apple Events error numbers read from osascript's stderr. The message text is
// localized, so the parenthesized number is the only thing read from it:
// -1743 is automation not permitted, -1712 is an Apple Event that timed out (the
// app did not answer the quit within quitEventTimeout; it may still be quitting).
const (
	deniedMarker   = "(-1743)"
	timedOutMarker = "(-1712)"
)

// absentReply is what runningScript prints when the id resolves to no app.
const absentReply = "absent"

// Controller holds the seams. The zero value runs on the real clock and
// runtime.GOOS; subprocesses always go through runner.Run and runner.Launch.
type Controller struct {
	// GOOS overrides runtime.GOOS (tests).
	GOOS string
	// Now is the wait's clock (cmd passes App.Now).
	Now func() time.Time
	// Sleep waits between polls and returns early when ctx is done.
	Sleep func(ctx context.Context, d time.Duration)
}

func (c Controller) darwin() bool {
	goos := c.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	return goos == "darwin"
}

func (c Controller) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c Controller) sleep(ctx context.Context, d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(ctx, d)
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func validBundleID(id string) bool {
	return len(id) <= maxBundleIDLen && bundleIDPattern.MatchString(id)
}

// Running asks whether the app is running. Off darwin it answers
// StateNotRunning without running anything: there is no app to handle.
func (c Controller) Running(ctx context.Context, bundleID string) (State, error) {
	if !validBundleID(bundleID) {
		return StateUnknown, ErrInvalidBundleID
	}
	if !c.darwin() {
		return StateNotRunning, nil
	}
	return running(ctx, bundleID), nil
}

// runningArgs is the osascript argv of the `is running` query. It prints `true`
// or `false`, or `absent` when the id resolves to no app (-1728 only; any other
// error still fails the script).
func runningArgs(bundleID string) []string {
	return []string{
		"-e", `set i to "` + bundleID + `"`,
		"-e", "try",
		"-e", "set r to application id i is running",
		"-e", "on error number -1728",
		"-e", `return "` + absentReply + `"`,
		"-e", "end try",
		"-e", "return r",
	}
}

func running(ctx context.Context, bundleID string) State {
	stdout, _, code := runner.Run(ctx, "osascript", runningArgs(bundleID)...)
	if code != 0 {
		return StateUnknown
	}
	switch strings.TrimSpace(stdout) {
	case "true":
		return StateRunning
	case "false", absentReply:
		return StateNotRunning
	}
	return StateUnknown
}

// quitArgs is the osascript argv of the quit. The `is running` test keeps the
// request from launching an app that is not running, which `tell` alone does.
func quitArgs(bundleID string) []string {
	return []string{
		"-e", `set i to "` + bundleID + `"`,
		"-e", "if application id i is running then",
		"-e", "with timeout of " + strconv.Itoa(int(quitEventTimeout/time.Second)) + " seconds",
		"-e", "tell application id i to quit",
		"-e", "end timeout",
		"-e", "end if",
	}
}

// QuitAndRelaunch asks the app to quit, polls `is running` every PollInterval
// until QuitDeadline after the request was sent, and runs `open -b <id>` only
// once the app has been observed stopped. A poll that reads StateUnknown is not
// a stop. A quit whose Apple Event timed out (-1712) is not a failure: the app
// may still be quitting, so the wait goes on. It never forces the quit. The
// caller has already established that the app is running and that the user
// consented.
//
// A done ctx returns ctx.Err() and no Outcome; nothing further is run and the
// app is not relaunched. Mapping that to a report is the caller's.
func (c Controller) QuitAndRelaunch(ctx context.Context, bundleID string) (Outcome, error) {
	if !validBundleID(bundleID) {
		return 0, ErrInvalidBundleID
	}
	if !c.darwin() {
		return 0, ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	deadline := c.now().Add(QuitDeadline)
	if _, stderr, code := runner.Run(ctx, "osascript", quitArgs(bundleID)...); code != 0 {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		switch {
		case strings.Contains(stderr, deniedMarker):
			return OutcomeQuitDenied, nil
		case !strings.Contains(stderr, timedOutMarker):
			return OutcomeQuitFailed, nil
		}
	}
	for polls := 1; ; polls++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if running(ctx, bundleID) == StateNotRunning {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			if code, err := runner.Launch(ctx, "open", "-b", bundleID); err != nil || code != 0 {
				return OutcomeRelaunchFailed, nil
			}
			return OutcomeRelaunched, nil
		}
		if polls >= maxPolls || !c.now().Before(deadline) {
			return OutcomeQuitTimeout, nil
		}
		c.sleep(ctx, PollInterval)
	}
}
