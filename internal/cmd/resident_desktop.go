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
// the transaction: the run's stance, and, for a run that acts or would act, how
// kae gets consent. Only a run that acts with consentAsk or consentYes acts
// afterwards.
type desktopPlan struct {
	stance  residentStance
	consent desktopConsent // for stanceAct and stanceDryRun; zero otherwise
}

// desktopConsent is how kae gets the user's consent to quit the app.
type desktopConsent int

const (
	consentAsk       desktopConsent = iota + 1 // ask on the terminal after the transaction
	consentYes                                 // --yes: quit and relaunch without asking
	consentCannotAsk                           // no terminal or --json, without --yes
)

// planDesktop decides p for a running app. The stance takes the one precedence
// (residentMode.stance), and --yes counts only after it. canAsk is called only
// when the answer matters, since it opens the terminal; it has no other side
// effect, so a --dry-run asks it too and says what a real run would.
func planDesktop(mode residentMode, canAsk func() bool) desktopPlan {
	p := desktopPlan{stance: mode.stance()}
	if p.stance == stanceOptedOut || p.stance == stanceHook {
		return p
	}
	switch {
	case mode.yes:
		p.consent = consentYes
	case mode.json || !canAsk():
		p.consent = consentCannotAsk
	default:
		p.consent = consentAsk
	}
	return p
}

// outcome is the entry's outcome before the transaction. An app the command
// will act on, and any app under --dry-run, reads `planned` until the quit settles
// it.
func (p desktopPlan) outcome() string {
	switch {
	case p.stance == stanceOptedOut:
		return constants.ResidentOutcomeOptedOut
	case p.stance == stanceHook, p.stance == stanceAct && p.consent == consentCannotAsk:
		return constants.ResidentOutcomeWarned
	}
	return constants.ResidentOutcomePlanned
}

// owed reports whether the command acts on the app after the transaction: only
// a run that acts and has, or will ask for, consent. The zero plan of an app kae
// cannot tell about owes nothing.
func (p desktopPlan) owed() bool {
	return p.stance == stanceAct && (p.consent == consentAsk || p.consent == consentYes)
}

// announced is what the notice before the write says kae does to the app.
func (p desktopPlan) announced() residentApp {
	switch {
	case p.stance == stanceOptedOut, p.stance == stanceHook:
		return residentAppLeft
	case p.consent == consentYes:
		return residentAppRelaunch
	case p.consent == consentAsk:
		return residentAppConfirm
	}
	return residentAppNone
}

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
// apps is running and decides what the command does about each running one;
// noticeResidents gives the notice. An app that is not running (or not
// installed, or any app off macOS) gets no entry; one kae cannot tell about gets
// an `unknown` entry, and nothing is done to it.
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
			entries = append(entries, desktopEntry{
				entry: residentEntry{
					Kind: constants.ResidentKindDesktopApp, Observed: constants.ResidentObservedRunning,
					Outcome: plan.outcome(),
				},
				bundleID: id,
				plan:     plan,
			})
		default:
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

// residentLine is one stderr line of an app's outcome, returned rather than
// printed so that reconcileSlot can hold a relaunch back for the success line.
type residentLine struct {
	m    message
	warn bool
}

func noteLine(m message) residentLine { return residentLine{m: m} }
func warnLine(m message) residentLine { return residentLine{m: m, warn: true} }

// print writes the line as a `kae: warning:` or a `kae: note:` line.
func (l residentLine) print() {
	if l.warn {
		warnMessage(l.m)
		return
	}
	noteMessage(l.m)
}

// quitDesktopApp is step 5 for one app: it asks when q's plan says so, checks
// again that the app is still running, then quits and relaunches it, settles q's
// entry, and returns the line that reports it. An app the user closed in the
// meantime is left closed (`none`), and one kae can no longer tell about follows
// the unknown rule (`unknown`, `warned`). Every result but a relaunch and a
// closed app is a warning; none changes the exit code.
func (app *App) quitDesktopApp(ctx context.Context, q pendingQuit) residentLine {
	if q.plan.consent == consentAsk {
		yes, asked := app.confirmDesktopQuit()
		if !asked {
			q.entry.Outcome = constants.ResidentOutcomeWarned
			return warnLine(desktopCannotAskMessage())
		}
		if !yes {
			q.entry.Outcome = constants.ResidentOutcomeDeclined
			return warnLine(desktopDeclinedMessage())
		}
	}
	switch app.desktopAppState(ctx, q.bundleID) {
	case desktopapp.StateRunning:
	case desktopapp.StateNotRunning:
		q.entry.Outcome = constants.ResidentOutcomeNone
		return noteLine(desktopClosedMessage())
	default:
		q.entry.Observed = constants.ResidentObservedUnknown
		q.entry.Outcome = constants.ResidentOutcomeWarned
		return warnLine(desktopUnknownMessage())
	}
	outcome, err := app.desktopController().QuitAndRelaunch(ctx, q.bundleID)
	if err != nil {
		// Interrupted, or a guard of desktopapp's: the quit may not have been asked.
		q.entry.Outcome = constants.ResidentOutcomeQuitFailed
		return warnLine(desktopQuitFailedMessage())
	}
	var line residentLine
	q.entry.Outcome, line = reportDesktopOutcome(outcome, q.bundleID)
	return line
}

// reportDesktopOutcome maps how QuitAndRelaunch ended to the residents token and
// the line that says it.
func reportDesktopOutcome(outcome desktopapp.Outcome, bundleID string) (string, residentLine) {
	switch outcome {
	case desktopapp.OutcomeRelaunched:
		// reconcileSlot holds the first relaunch for the line after the result.
		done, _ := residentDone(false, true)
		return constants.ResidentOutcomeRelaunched, noteLine(alone(done))
	case desktopapp.OutcomeRelaunchFailed:
		return constants.ResidentOutcomeRelaunchFailed,
			warnLine(desktopRelaunchFailedMessage(bundleID))
	case desktopapp.OutcomeQuitTimeout:
		return constants.ResidentOutcomeQuitTimeout,
			warnLine(desktopQuitTimeoutMessage())
	case desktopapp.OutcomeQuitDenied:
		return constants.ResidentOutcomeQuitDenied,
			warnLine(desktopQuitDeniedMessage())
	}
	return constants.ResidentOutcomeQuitFailed, warnLine(desktopQuitFailedMessage())
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
	fmt.Fprint(rw, l10n.Sprintf("Quit and relaunch ChatGPT now? Running tasks will be interrupted. [y/N]: "))
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
