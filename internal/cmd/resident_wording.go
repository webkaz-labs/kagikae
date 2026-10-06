package cmd

import "time"

// residentOp is the command a resident reconcile runs for, which its notices name:
// a switch (`kae use`), the login flow of `kae add`, or `kae rollback`. Only the
// sentences that speak of the command's time or result differ; everything else
// reads the same for all three.
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

// residentApp is what the notice before the write says about the ChatGPT app.
type residentApp int

const (
	residentAppNone residentApp = iota // no running app the command acts on
	residentAppAsk                     // asked about after the transaction
	residentAppYes                     // --yes: quit and relaunched without asking
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
	case daemon && app == residentAppAsk:
		return msgf("restart the managed daemon and ask whether to quit and relaunch the ChatGPT app"), true
	case daemon && app == residentAppYes:
		return msgf("restart the managed daemon and quit and relaunch the ChatGPT app"), true
	case daemon:
		return msgf("restart the managed daemon"), true
	case app == residentAppAsk:
		return msgf("ask whether to quit and relaunch the ChatGPT app"), true
	case app == residentAppYes:
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

// optedOutMessage is the one warning under --no-restart for the daemon (daemon)
// and the app (app), naming the manual steps; ok is false when neither is owed.
func optedOutMessage(daemon, app bool, manual func() string) (m message, ok bool) {
	switch {
	case daemon && app:
		return msgf("codex: --no-restart: kae does not restart the managed daemon or the ChatGPT app; to move them to the live account, quit and reopen the app, and run: %s", manual()), true
	case daemon:
		return msgf("codex: --no-restart: kae does not restart the managed daemon; to move it to the live account, run: %s", manual()), true
	case app:
		return msgf("codex: --no-restart: kae does not relaunch the ChatGPT app; to move it to the live account, quit and reopen it"), true
	}
	return message{}, false
}

// hookMessage is optedOutMessage for the hook shape (--auto), which only a switch
// has.
func hookMessage(daemon, app bool, manual func() string) (m message, ok bool) {
	switch {
	case daemon && app:
		return msgf("codex: the enter hook (--auto) does not restart the managed daemon or the ChatGPT app; to move them to the live account, quit and reopen the app, and run: %s", manual()), true
	case daemon:
		return msgf("codex: the enter hook (--auto) does not restart the managed daemon; to move it to the live account, run: %s", manual()), true
	case app:
		return msgf("codex: the enter hook (--auto) does not relaunch the ChatGPT app; to move it to the live account, quit and reopen it"), true
	}
	return message{}, false
}

// daemonUnknownMessage is the warning for a daemon whose account kae cannot read.
func daemonUnknownMessage(manual string) message {
	return msgf("codex: could not read the managed daemon's account; if it is not on the live account, run: %s", manual)
}

// session is the fixed warning about codex sessions kae does not look for; dryRun
// says what the command would do (the login flow has no --dry-run).
func (op residentOp) session(dryRun bool) message {
	switch {
	case op == residentOpLogin:
		return msgf("codex sessions started before kae add keep the previous account until they are restarted")
	case op == residentOpRollback && dryRun:
		return msgf("codex sessions started before kae rollback would keep the previous account until they are restarted")
	case op == residentOpRollback:
		return msgf("codex sessions started before kae rollback keep the previous account until they are restarted")
	case dryRun:
		return msgf("codex sessions started before the switch would keep the previous account until they are restarted")
	}
	return msgf("codex sessions started before the switch keep the previous account until they are restarted")
}

// restartTimedOut, restartNotRun and restartExited are the warnings of a restart
// command that did not succeed (restartFailed).
func (op residentOp) restartTimedOut(limit time.Duration, manual string) message {
	return op.restartFailed(msgf("codex app-server daemon restart did not finish within %s", limit), manual)
}

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

// daemonRestartedMessage is the note of a verified restart, when no relaunched app
// shares its line.
func daemonRestartedMessage() message {
	return msgf("codex: restarted the managed daemon")
}

// appRelaunchedMessage is the note of a relaunched app, when no verified restart
// shares its line.
func appRelaunchedMessage() message {
	return msgf("codex: relaunched the ChatGPT app")
}

// bothRestartedMessage is the one note of a verified restart and a relaunched app.
func bothRestartedMessage() message {
	return msgf("codex: restarted the managed daemon and the ChatGPT app")
}

// desktopCannotAskMessage is the warning of a run that cannot ask, without --yes.
func desktopCannotAskMessage() message {
	return msgf("codex: kae cannot ask here whether to relaunch the ChatGPT app (no terminal, or --json); to move it to the live account, quit and reopen it, or pass --yes")
}

// desktopQuitFailedMessage is the warning of a quit request that failed.
func desktopQuitFailedMessage() message {
	return msgf("codex: could not ask the ChatGPT app to quit; to use the codex account now live, quit it yourself (Command-Q in the app) and open it again")
}

// desktopUnknownMessage is the warning when kae cannot tell whether the app runs.
func desktopUnknownMessage() message {
	return msgf("codex: could not tell whether the ChatGPT app is running; if it is, quit and reopen it to move it to the live account")
}
