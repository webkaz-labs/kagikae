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
package desktopapp

import (
	"context"
	"errors"
	"regexp"
	"runtime"
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
	StateNotRunning
)

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

// Wait limits for the quit (docs/CLI.md § kae use Semantics, step 5).
const (
	PollInterval = 250 * time.Millisecond
	QuitDeadline = 20 * time.Second
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

// deniedMarker is the Apple Events error number osascript prints when the user
// has not allowed kae to control the app. Its message text is localized, so the
// number is the only thing read from stderr.
const deniedMarker = "(-1743)"

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

func running(ctx context.Context, bundleID string) State {
	stdout, _, code := runner.Run(ctx, "osascript", "-e", `application id "`+bundleID+`" is running`)
	if code != 0 {
		return StateUnknown
	}
	switch strings.TrimSpace(stdout) {
	case "true":
		return StateRunning
	case "false":
		return StateNotRunning
	}
	return StateUnknown
}

// quitArgs is the osascript argv of the quit. The `is running` test keeps the
// request from launching an app that is not running, which `tell` alone does.
func quitArgs(bundleID string) []string {
	return []string{
		"-e", `if application id "` + bundleID + `" is running then`,
		"-e", "with timeout of 10 seconds",
		"-e", `tell application id "` + bundleID + `" to quit`,
		"-e", "end timeout",
		"-e", "end if",
	}
}

// QuitAndRelaunch asks the app to quit, polls `is running` every PollInterval
// for at most QuitDeadline, and runs `open -b <id>` only once the app has been
// observed stopped. A poll that reads StateUnknown is not a stop. It never
// forces the quit. The caller has already established that the app is running
// and that the user consented.
func (c Controller) QuitAndRelaunch(ctx context.Context, bundleID string) (Outcome, error) {
	if !validBundleID(bundleID) {
		return 0, ErrInvalidBundleID
	}
	if !c.darwin() {
		return 0, ErrUnsupported
	}
	if _, stderr, code := runner.Run(ctx, "osascript", quitArgs(bundleID)...); code != 0 {
		if strings.Contains(stderr, deniedMarker) {
			return OutcomeQuitDenied, nil
		}
		return OutcomeQuitFailed, nil
	}
	deadline := c.now().Add(QuitDeadline)
	for {
		if running(ctx, bundleID) == StateNotRunning {
			if code, err := runner.Launch(ctx, "open", "-b", bundleID); err != nil || code != 0 {
				return OutcomeRelaunchFailed, nil
			}
			return OutcomeRelaunched, nil
		}
		if ctx.Err() != nil || !c.now().Before(deadline) {
			return OutcomeQuitTimeout, nil
		}
		c.sleep(ctx, PollInterval)
	}
}
