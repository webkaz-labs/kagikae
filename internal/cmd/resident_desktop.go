package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/desktopapp"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/textui"
)

// defaultDesktopQueryTimeout bounds one `is running` query of osascript, which
// can take seconds to start cold.
const defaultDesktopQueryTimeout = 5 * time.Second

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

// owed reports whether the command acts on the app after the transaction.
func (p desktopPlan) owed() bool { return p == desktopAsk || p == desktopYes }

// pendingQuit is a desktop app a command owes a confirmation and a quit, once
// the transaction has succeeded, its locks are released and the daemon's
// restart has run. entry is the `desktop_app` item whose outcome it settles.
type pendingQuit struct {
	entry    *residentEntry
	bundleID string
	plan     desktopPlan
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
	ids := app.desktopAppIDs(holder)
	if len(ids) == 0 {
		return nil
	}
	var entries []desktopEntry
	for _, id := range ids {
		switch app.desktopAppState(ctx, id) {
		case desktopapp.StateNotRunning:
			continue
		case desktopapp.StateRunning:
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
		default:
			warnMessage(desktopUnknownMessage())
			entries = append(entries, desktopEntry{entry: residentEntry{
				Kind: constants.ResidentKindDesktopApp, Observed: constants.ResidentObservedUnknown,
				Outcome: constants.ResidentOutcomeWarned,
			}})
		}
	}
	return entries
}

// desktopAppState asks once whether the app is running, bounded by
// desktopQueryLimit. A query that errs or runs out of time reads StateUnknown,
// on which nothing is done to the app.
func (app *App) desktopAppState(ctx context.Context, bundleID string) desktopapp.State {
	queryCtx, cancel := context.WithTimeout(ctx, app.desktopQueryLimit())
	defer cancel()
	state, err := app.desktopController().Running(queryCtx, bundleID)
	if err != nil || queryCtx.Err() != nil {
		return desktopapp.StateUnknown
	}
	return state
}

// desktopQueryLimit bounds one `is running` query: App.desktopQueryTimeout, or
// the default.
func (app *App) desktopQueryLimit() time.Duration {
	if app.desktopQueryTimeout > 0 {
		return app.desktopQueryTimeout
	}
	return defaultDesktopQueryTimeout
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

// quitDesktopApp is step 5 for one app: it asks when q's plan says so, checks
// again that the app is still running, then quits and relaunches it, and settles
// q's entry. An app the user closed in the meantime is left closed (`none`), and
// one kae can no longer tell about follows the unknown rule (`unknown`,
// `warned`). Every result but a relaunch and a closed app is a warning; none
// changes the exit code.
func (app *App) quitDesktopApp(ctx context.Context, q pendingQuit) {
	if q.plan == desktopAsk {
		yes, asked := app.confirmDesktopQuit()
		if !asked {
			warnMessage(desktopCannotAskMessage())
			q.entry.Outcome = constants.ResidentOutcomeWarned
			return
		}
		if !yes {
			warnf("codex: left the ChatGPT app running; it keeps the codex account it started with until you quit and reopen it")
			q.entry.Outcome = constants.ResidentOutcomeDeclined
			return
		}
	}
	switch app.desktopAppState(ctx, q.bundleID) {
	case desktopapp.StateRunning:
	case desktopapp.StateNotRunning:
		notef("codex: the ChatGPT app is no longer running, so kae leaves it closed; it uses the codex account now live when you open it")
		q.entry.Outcome = constants.ResidentOutcomeNone
		return
	default:
		warnMessage(desktopUnknownMessage())
		q.entry.Observed = constants.ResidentObservedUnknown
		q.entry.Outcome = constants.ResidentOutcomeWarned
		return
	}
	outcome, err := app.desktopController().QuitAndRelaunch(ctx, q.bundleID)
	if err != nil {
		// Interrupted, or a guard of desktopapp's: the quit may not have been asked.
		warnMessage(desktopQuitFailedMessage())
		q.entry.Outcome = constants.ResidentOutcomeQuitFailed
		return
	}
	q.entry.Outcome = reportDesktopOutcome(outcome, q.bundleID)
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
	rw, ok := app.terminalIO(tty)
	if !ok {
		return false, false
	}
	fmt.Fprint(rw, l10n.Sprintf("ChatGPT keeps the codex account it started with. Quit and relaunch it now? Tasks running in ChatGPT will be interrupted. [y/N]: "))
	line, _ := bufio.NewReader(rw).ReadString('\n')
	return isYes(line), true
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
// App.terminalIOForTest behind the test stand-in, which has none. ok is false
// when there is neither.
func (app *App) terminalIO(tty *textui.Terminal) (io.ReadWriter, bool) {
	if tty.TTY != nil {
		return tty.TTY, true
	}
	return app.terminalIOForTest, app.terminalIOForTest != nil
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
		Sleep: app.sleep,
	}
}
