package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/desktopapp"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/textui"
)

// desktopPlan is how a command treats one running desktop app that keeps the
// tool's previous account (docs/CLI.md § kae use Semantics, **Resident processes
// (codex)**, step 5 and the ChatGPT app's `outcome` table). It is decided before
// the transaction, from the flags and whether kae can ask; only desktopAsk and
// desktopYes act afterwards.
type desktopPlan int

const (
	desktopOptedOut  desktopPlan = iota + 1 // --no-restart
	desktopHook                             // the hook shape (--auto), even with --yes
	desktopDryRun                           // --dry-run
	desktopCannotAsk                        // no terminal or --json, without --yes
	desktopAsk                              // ask on the terminal after the transaction
	desktopYes                              // --yes: quit and relaunch without asking
)

// planDesktop decides p for a running app. The order is the outcome table's:
// --no-restart, then the hook shape, then --dry-run, and --yes only after them.
// canAsk is called only when the answer matters, since it opens the terminal.
func planDesktop(mode residentMode, canAsk func() bool) desktopPlan {
	switch {
	case mode.noRestart:
		return desktopOptedOut
	case mode.hook:
		return desktopHook
	case mode.dryRun:
		return desktopDryRun
	case mode.yes:
		return desktopYes
	case mode.json || !canAsk():
		return desktopCannotAsk
	}
	return desktopAsk
}

// outcome is the entry's outcome before the transaction. An app the command
// will act on reads `planned` until the quit settles it.
func (p desktopPlan) outcome() string {
	switch p {
	case desktopOptedOut:
		return constants.ResidentOutcomeOptedOut
	case desktopHook, desktopCannotAsk:
		return constants.ResidentOutcomeWarned
	}
	return constants.ResidentOutcomePlanned
}

// desktopCannotAskMessage is the warning of a run that cannot ask, without --yes.
func desktopCannotAskMessage() message {
	return msgf("codex: the ChatGPT app keeps the codex account it started with, and kae cannot ask here whether to quit it (no terminal, or --json); to use the codex account now live, quit and reopen it, or pass --yes to let kae do it")
}

// desktopQuitFailedMessage is the warning of a quit request that failed.
func desktopQuitFailedMessage() message {
	return msgf("codex: could not ask the ChatGPT app to quit; to use the codex account now live, quit it yourself (Command-Q in the app) and open it again")
}

// owed reports whether the command acts on the app after the transaction.
func (p desktopPlan) owed() bool { return p == desktopAsk || p == desktopYes }

// pendingQuit is a desktop app a command owes a confirmation and a quit, once
// the transaction has succeeded, its locks are released and the daemon's
// restart has run. entry is the `desktop_app` item whose outcome it settles.
type pendingQuit struct {
	entry    *residentEntry
	bundleID string
	ask      bool
}

// desktopEntry is a `desktop_app` residents item with the plan, if any, it owes.
type desktopEntry struct {
	entry    residentEntry
	bundleID string
	plan     desktopPlan
}

// planDesktopApps is the desktop half of steps 1 and 2 for a command that
// changes the tool's account: it asks once whether each of the holder's desktop
// apps is running, decides what the command does about each running one, and
// gives the notice on stderr. An app that is not running (or not installed, or
// any app off macOS) gets no entry; one kae cannot tell about gets a warning
// and an `unknown` entry, and nothing is done to it.
func (app *App) planDesktopApps(ctx context.Context, holder adapter.ResidentHolder, mode residentMode) []desktopEntry {
	var entries []desktopEntry
	ids := app.desktopAppIDs(holder)
	if len(ids) == 0 {
		return nil
	}
	ctrl := app.desktopController()
	for _, id := range ids {
		state, err := ctrl.Running(ctx, id)
		if err == nil && state == desktopapp.StateNotRunning {
			continue
		}
		if err != nil || state != desktopapp.StateRunning {
			warnf("codex: could not tell whether the ChatGPT app is running; if it is, quit and reopen it to use the codex account now live")
			entries = append(entries, desktopEntry{entry: residentEntry{
				Kind: constants.ResidentKindDesktopApp, Observed: constants.ResidentObservedUnknown,
				Outcome: constants.ResidentOutcomeWarned,
			}})
			continue
		}
		plan := planDesktop(mode, app.canPrompt)
		noticeDesktop(mode.op, plan)
		entries = append(entries, desktopEntry{
			entry: residentEntry{
				Kind: constants.ResidentKindDesktopApp, Observed: constants.ResidentObservedRunning,
				Outcome: plan.outcome(),
			},
			bundleID: id,
			plan:     plan,
		})
	}
	return entries
}

// noticeDesktop is step 2's stderr line for a running app, in op's words where
// the command is named.
func noticeDesktop(op residentOp, plan desktopPlan) {
	switch plan {
	case desktopOptedOut:
		warnf("codex: the ChatGPT app keeps the codex account it started with, and --no-restart leaves it running; to use the codex account now live, quit and reopen it")
	case desktopHook:
		// Only a switch has the hook shape.
		warnf("codex: the ChatGPT app keeps the codex account it started with, and the enter hook (--auto) does not quit it; to use the codex account now live, quit and reopen it")
	case desktopDryRun:
		noteMessage(op.appPlanned())
	case desktopCannotAsk:
		warnMessage(desktopCannotAskMessage())
	case desktopAsk, desktopYes:
		noteMessage(op.appAhead())
	}
}

// quitDesktopApp is step 5 for one app: it asks when q says so, then quits and
// relaunches the app, and returns the entry's outcome. Every result but a
// relaunch is a warning; none changes the exit code.
func (app *App) quitDesktopApp(ctx context.Context, q pendingQuit) string {
	if q.ask {
		yes, asked := app.confirmDesktopQuit()
		if !asked {
			warnMessage(desktopCannotAskMessage())
			return constants.ResidentOutcomeWarned
		}
		if !yes {
			warnf("codex: left the ChatGPT app running; it keeps the codex account it started with until you quit and reopen it")
			return constants.ResidentOutcomeDeclined
		}
	}
	outcome, err := app.desktopController().QuitAndRelaunch(ctx, q.bundleID)
	if err != nil {
		// Interrupted, or a guard of desktopapp's: the quit may not have been asked.
		warnMessage(desktopQuitFailedMessage())
		return constants.ResidentOutcomeQuitFailed
	}
	return reportDesktopOutcome(outcome, q.bundleID)
}

// reportDesktopOutcome maps how QuitAndRelaunch ended to the residents token and
// says it on stderr.
func reportDesktopOutcome(outcome desktopapp.Outcome, bundleID string) string {
	switch outcome {
	case desktopapp.OutcomeRelaunched:
		notef("codex: quit and relaunched the ChatGPT app, so it uses the codex account now live")
		return constants.ResidentOutcomeRelaunched
	case desktopapp.OutcomeRelaunchFailed:
		warnf("codex: the ChatGPT app quit, but kae could not open it again (open -b %s); open it yourself", bundleID)
		return constants.ResidentOutcomeRelaunchFailed
	case desktopapp.OutcomeQuitTimeout:
		warnf("codex: the ChatGPT app did not quit in time and kae left it running; it keeps the codex account it started with until you quit and reopen it")
		return constants.ResidentOutcomeQuitTimeout
	case desktopapp.OutcomeQuitDenied:
		warnf("codex: macOS did not let kae control the ChatGPT app; to use the codex account now live, quit it yourself (Command-Q in the app) and open it again")
		return constants.ResidentOutcomeQuitDenied
	}
	warnMessage(desktopQuitFailedMessage())
	return constants.ResidentOutcomeQuitFailed
}

// confirmDesktopQuit asks on the terminal whether to quit and relaunch the app,
// default No. asked is false when there is no terminal to ask on. The answers
// accepted do not depend on the language (docs/CLI.md § Localization).
func (app *App) confirmDesktopQuit() (yes, asked bool) {
	tty, ok := app.terminal()
	if !ok {
		return false, false
	}
	defer tty.Close()
	rw := app.terminalIO(tty)
	fmt.Fprint(rw, l10n.Sprintf("ChatGPT keeps the codex account it started with. Quit and relaunch it now? Tasks running in ChatGPT will be interrupted. [y/N]: "))
	line, _ := bufio.NewReader(rw).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, true
	}
	return false, true
}

// canPrompt reports whether there is a terminal to ask on now. The terminal is
// opened again when kae asks.
func (app *App) canPrompt() bool {
	tty, ok := app.terminal()
	if ok {
		tty.Close()
	}
	return ok
}

// terminalIO is what kae reads and writes on the terminal: its file, or
// App.terminalIOForTest behind the test stand-in, which has none.
func (app *App) terminalIO(tty *textui.Terminal) io.ReadWriter {
	switch {
	case tty.TTY != nil:
		return tty.TTY
	case app.terminalIOForTest != nil:
		return app.terminalIOForTest
	}
	return struct {
		io.Reader
		io.Writer
	}{strings.NewReader(""), io.Discard}
}

// desktopAppIDs is the bundle ids of the holder's desktop apps through the
// App's seam: none when it is nil (tests), holder.DesktopApps in production.
func (app *App) desktopAppIDs(holder adapter.ResidentHolder) []string {
	if app.desktopApps == nil {
		return nil
	}
	return app.desktopApps(holder)
}

// desktopController is the desktop-app controller on the App's clock: its Now,
// and its sleep, which a test advances instead of sleeping. desktopGOOS
// overrides the platform in tests.
func (app *App) desktopController() desktopapp.Controller {
	return desktopapp.Controller{
		GOOS:  app.desktopGOOS,
		Now:   app.Now,
		Sleep: func(ctx context.Context, d time.Duration) { app.sleep(ctx, d) },
	}
}
