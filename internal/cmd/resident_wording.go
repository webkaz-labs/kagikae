package cmd

import (
	"fmt"
	"os"

	"github.com/webkaz-labs/kagikae/internal/l10n"
)

// residentOp is the command a resident reconcile runs for, which its notices name:
// a switch (`kae use`), the login flow of `kae add`, or `kae rollback`.
//
// This file is the one place the resident-process lines are worded, and these are
// its rules (docs/CLI.md § kae use Semantics, **How the lines read**, routes here):
//
//   - A line names the command only where it speaks of the command's time or its
//     result: the time words of the notice before the write (after), the session
//     warning (session) and the kept result of a failed restart (resultKept). Every
//     other line reads the same for all three commands.
//   - The time words are "after the switch", "after kae add" and "after kae
//     rollback"; in Japanese they end with a comma (切替後、), so that what follows
//     — a kanji word or the name ChatGPT — needs no space after them.
//   - The terminal lines do not pair the managed daemon with its command name
//     (docs/L10N-JA.md, managed daemon); a warning that names the manual step names
//     the command there.
//   - In Japanese the account a line moves a process to is 「現在有効なアカウント」,
//     or 「現在有効な codex アカウント」 where the line names codex's account.
//
// No method splices the command's name into a sentence: a catalog key is the
// literal format string at its sink, so each op gets a whole sentence of its own
// for the Japanese lookup to find, or, as in restartFailed and ahead, a whole
// message of its own spliced into a shared sentence.
type residentOp int

const (
	residentOpSwitch residentOp = iota
	residentOpLogin
	residentOpRollback
)

// residentApp is what the notice before the write says about the running ChatGPT
// app (desktopPlan.announced).
type residentApp int

const (
	residentAppNone     residentApp = iota // no running app the line speaks of
	residentAppConfirm                     // kae asks before it quits and relaunches the app
	residentAppRelaunch                    // --yes: kae quits and relaunches it without asking
	residentAppLeft                        // --no-restart or the hook shape leaves it running
)

// after is the time words of the notice before the write.
func (op residentOp) after() message {
	switch op {
	case residentOpLogin:
		return msgf("after kae add")
	case residentOpRollback:
		return msgf("after kae rollback")
	}
	return msgf("after the switch")
}

// residentAction is what kae does to the daemon (daemon: a restart) and the app,
// in the base form both ahead and planned splice in. ok is false when it does
// nothing to either.
func residentAction(daemon bool, app residentApp) (m message, ok bool) {
	switch {
	case daemon && app == residentAppConfirm:
		return msgf("restart the managed daemon and ask whether to quit and relaunch the ChatGPT app"), true
	case daemon && app == residentAppRelaunch:
		return msgf("restart the managed daemon and quit and relaunch the ChatGPT app"), true
	case daemon:
		return msgf("restart the managed daemon"), true
	case app == residentAppConfirm:
		return msgf("ask whether to quit and relaunch the ChatGPT app"), true
	case app == residentAppRelaunch:
		return msgf("quit and relaunch the ChatGPT app"), true
	}
	return message{}, false
}

// ahead is the one notice before the write of what the command will do.
func (op residentOp) ahead(action message) message {
	return msgf("codex: %s, kae will %s", op.after(), action)
}

// planned is ahead under --dry-run. The login flow has no --dry-run.
func (op residentOp) planned(action message) message {
	return msgf("codex: %s, kae would %s", op.after(), action)
}

// suppressedReason is why a run restarts and quits nothing: --no-restart, or the
// hook shape (--auto), which only a switch has.
func suppressedReason(hook bool) message {
	if hook {
		return msgf("the enter hook, --auto")
	}
	return msgf("--no-restart")
}

// suppressedMessage is the one warning, before the write, for the daemon
// (daemon) and the app (app) that reason leaves as they are, naming the manual
// steps; ok is false when it leaves neither.
func suppressedMessage(reason message, daemon, app bool, manual func() string) (m message, ok bool) {
	switch {
	case daemon && app:
		return msgf("codex: kae does not restart the managed daemon or the ChatGPT app (%s); to move them to the live account, quit and reopen the app, and run: %s", reason, manual()), true
	case daemon:
		return msgf("codex: kae does not restart the managed daemon (%s); to move it to the live account, run: %s", reason, manual()), true
	case app:
		return msgf("codex: kae does not relaunch the ChatGPT app (%s); to move it to the live account, quit and reopen it", reason), true
	}
	return message{}, false
}

// daemonUnknownMessage is the warning for a daemon whose account kae cannot read.
func daemonUnknownMessage(manual string) message {
	return msgf("codex: could not read the managed daemon's account; if it is not on the live account, run: %s", manual)
}

// probeOriginatorMessage is the warning of a run that connected to a daemon it
// does not restart and found it had become the daemon's first client, so the
// threads the daemon creates from now on carry kae's probe name.
func probeOriginatorMessage(manual string) message {
	return msgf("codex: kae's check was the first client to connect to the managed daemon, so the threads the daemon creates from now on name kae_probe as their client until it restarts; to clear it, run: %s", manual)
}

// session is the fixed warning about codex sessions kae does not look for; dryRun
// says what the command would do (the login flow has no --dry-run).
func (op residentOp) session(dryRun bool) message {
	switch {
	case op == residentOpLogin:
		return msgf("codex sessions started before kae add and not connected to the managed daemon keep the previous account until they are restarted")
	case op == residentOpRollback && dryRun:
		return msgf("codex sessions started before kae rollback and not connected to the managed daemon would keep the previous account until they are restarted")
	case op == residentOpRollback:
		return msgf("codex sessions started before kae rollback and not connected to the managed daemon keep the previous account until they are restarted")
	case dryRun:
		return msgf("codex sessions started before the switch and not connected to the managed daemon would keep the previous account until they are restarted")
	}
	return msgf("codex sessions started before the switch and not connected to the managed daemon keep the previous account until they are restarted")
}

// restartPending is the warning of a restart command kae stopped waiting for and
// left running. Upstream's restart may be waiting for running tasks before it
// starts the new daemon, so it names the manual step for a restart that does not
// go on, not a retry while it may still be running.
func (op residentOp) restartPending(manual string) message {
	return msgf("codex: codex app-server daemon restart did not finish in time, so kae left it running and stopped waiting; it may be waiting for running tasks to finish before it restarts the managed daemon; %s; if the managed daemon does not restart, run: %s",
		op.resultKept(), manual)
}

// restartNotRun and restartExited are the warnings of a restart command that did
// not succeed (restartFailed).
func (op residentOp) restartNotRun(err error, manual string) message {
	return op.restartFailed(msgf("could not run codex app-server daemon restart (%v)", err), manual)
}

func (op residentOp) restartExited(code int, manual string) message {
	return op.restartFailed(msgf("codex app-server daemon restart failed (exit %d)", code), manual)
}

// restartFailed says what went wrong with the restart, that the command's own
// result is kept, and how to retry. It splices messages, not names, so each part
// renders in the selected language from its own literal catalog key.
func (op residentOp) restartFailed(cause message, manual string) message {
	return msgf("codex: %s; %s, and the managed daemon may not be using the live account yet; to retry, run: %s",
		cause, op.resultKept(), manual)
}

// resultKept says that the command's result stands although the restart failed.
func (op residentOp) resultKept() message {
	switch op {
	case residentOpLogin:
		return msgf("kae add's result is kept")
	case residentOpRollback:
		return msgf("kae rollback's result is kept")
	}
	return msgf("the switch is kept")
}

// restartUnverifiedMessage is the warning of a restart command that exited 0
// with a report kae could not read or that named another socket.
func restartUnverifiedMessage(manual string) message {
	return msgf("codex: codex app-server daemon restart succeeded, but kae could not read its report or it named another socket, so kae cannot tell whether the managed daemon restarted; if it is not using the live account, run: %s", manual)
}

// residentDone is the success of a reconcile: a verified restart, a relaunched
// app, or both on one line. It is a phrase, said after the command's result line
// (residentSlot.printDone).
func residentDone(restarted, relaunched bool) (m message, ok bool) {
	switch {
	case restarted && relaunched:
		return msgf("restarted the managed daemon and the ChatGPT app"), true
	case restarted:
		return msgf("restarted the managed daemon"), true
	case relaunched:
		return msgf("relaunched the ChatGPT app"), true
	}
	return message{}, false
}

// underReport is a success phrase as the indented line under the command's
// result line, with no prefix.
func underReport(done message) message {
	return msgf("  codex: %s", done)
}

// alone is a success phrase as a `kae: note:` line, for a run that prints no
// result line to put it under (--json, --quiet).
func alone(done message) message {
	return msgf("codex: %s", done)
}

// printUnderReport writes m on stderr with no prefix.
func printUnderReport(m message) {
	fmt.Fprintln(os.Stderr, l10n.Render(m))
}

// The ChatGPT app's warnings and its note of an app closed meanwhile.

// desktopCannotAskMessage is the warning of a run that cannot ask, without --yes.
func desktopCannotAskMessage() message {
	return msgf("codex: kae cannot ask here whether to relaunch the ChatGPT app (no terminal, or --json); to move it to the live account, quit and reopen it, or pass --yes")
}

// desktopUnknownMessage is the warning when kae cannot tell whether the app runs.
func desktopUnknownMessage() message {
	return msgf("codex: could not tell whether the ChatGPT app is running; if it is, quit and reopen it to move it to the live account")
}

// desktopDeclinedMessage is the warning of an answer no.
func desktopDeclinedMessage() message {
	return msgf("codex: left the ChatGPT app running; it keeps the codex account it started with until you quit and reopen it")
}

// desktopClosedMessage is the note of an app the user closed before kae asked it
// to quit.
func desktopClosedMessage() message {
	return msgf("codex: the ChatGPT app is no longer running, so kae leaves it closed; it uses the codex account now live when you open it")
}

// desktopRelaunchFailedMessage is the warning of an app that quit but did not open.
func desktopRelaunchFailedMessage(bundleID string) message {
	return msgf("codex: the ChatGPT app quit, but kae could not open it again (open -b %s); open it yourself", bundleID)
}

// desktopQuitTimeoutMessage is the warning of an app still running at the deadline,
// which may be showing its quit confirmation dialog; kae never relaunches it later.
func desktopQuitTimeoutMessage() message {
	return msgf("codex: the ChatGPT app did not quit in time and kae left it running; it may be asking you to confirm the quit, and kae will not reopen it, so open it yourself after it quits; until then it keeps the codex account it started with")
}

// desktopQuitDeniedMessage is the warning when macOS refused kae control of the app.
func desktopQuitDeniedMessage() message {
	return msgf("codex: macOS did not let kae control the ChatGPT app; to use the codex account now live, quit it yourself (Command-Q in the app) and open it again")
}

// desktopQuitFailedMessage is the warning of a quit request that failed.
func desktopQuitFailedMessage() message {
	return msgf("codex: could not ask the ChatGPT app to quit; to use the codex account now live, quit it yourself (Command-Q in the app) and open it again")
}
