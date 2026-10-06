package cmd

import "time"

// residentOp is the command a resident reconcile runs for, which its notices name:
// a switch (`kae use`), the login flow of `kae add`, or `kae rollback`. Only the
// sentences that speak of the command differ; the daemon's unreadable account and
// an unverified restart read the same for all three.
//
// No method splices the command's name into a sentence: a catalog key is the
// literal format string at its sink, so each op gets a whole sentence of its own
// for the Japanese lookup to find, or, as in restartFailed, a whole message of its
// own spliced into a shared sentence.
type residentOp int

const (
	residentOpSwitch residentOp = iota
	residentOpLogin
	residentOpRollback
)

// restartAhead is the notice before a restart the command owes.
func (op residentOp) restartAhead() message {
	switch op {
	case residentOpLogin:
		return msgf("codex: the managed daemon (codex app-server daemon) is not using the account this kae add left live; kae restarts it now")
	case residentOpRollback:
		return msgf("codex: the managed daemon (codex app-server daemon) is not using the account this kae rollback puts back; kae restarts it after the rollback")
	}
	return msgf("codex: the managed daemon (codex app-server daemon) is not using the account this switch leaves live; kae restarts it after the switch")
}

// restartPlanned is restartAhead under --dry-run. The login flow has no --dry-run.
func (op residentOp) restartPlanned() message {
	if op == residentOpRollback {
		return msgf("codex: the managed daemon (codex app-server daemon) is not using the account this kae rollback would put back; the rollback would restart it")
	}
	return msgf("codex: the managed daemon (codex app-server daemon) is not using the account this switch would leave live; the switch would restart it")
}

// optedOut is the warning under --no-restart, naming the manual step.
func (op residentOp) optedOut(manual string) message {
	switch op {
	case residentOpLogin:
		return msgf("codex: the managed daemon (codex app-server daemon) is not using the account this kae add left live, and --no-restart leaves it running; to move it to that account, run: %s", manual)
	case residentOpRollback:
		return msgf("codex: the managed daemon (codex app-server daemon) is not using the account this kae rollback puts back, and --no-restart leaves it running; to move it to that account, run: %s", manual)
	}
	return msgf("codex: the managed daemon (codex app-server daemon) is not using the account this switch leaves live, and --no-restart leaves it running; to move it to the new account, run: %s", manual)
}

// session is the fixed warning about codex sessions kae does not look for; dryRun
// says what the command would do (the login flow has no --dry-run).
func (op residentOp) session(dryRun bool) message {
	switch {
	case op == residentOpLogin:
		return msgf("codex sessions started before this kae add that are not connected to the managed daemon keep the previous account until they are restarted")
	case op == residentOpRollback && dryRun:
		return msgf("codex sessions started before this kae rollback that are not connected to the managed daemon would keep the previous account until they are restarted")
	case op == residentOpRollback:
		return msgf("codex sessions started before this kae rollback that are not connected to the managed daemon keep the previous account until they are restarted")
	case dryRun:
		return msgf("codex sessions started before this switch that are not connected to the managed daemon would keep the previous account until they are restarted")
	}
	return msgf("codex sessions started before this switch that are not connected to the managed daemon keep the previous account until they are restarted")
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

// restarted is the note of a verified restart.
func (op residentOp) restarted() message {
	switch op {
	case residentOpLogin:
		return msgf("codex: restarted the managed daemon (codex app-server daemon); it now holds the account this kae add left live")
	case residentOpRollback:
		return msgf("codex: restarted the managed daemon (codex app-server daemon); it now holds the account this kae rollback put back")
	}
	return msgf("codex: restarted the managed daemon (codex app-server daemon); it now holds the account this switch left live")
}

// appAhead is the notice before the ChatGPT app's confirmation and quit, which
// follow the command's transaction (the login flow's have followed it already).
func (op residentOp) appAhead() message {
	switch op {
	case residentOpLogin:
		return msgf("codex: the ChatGPT app is running and keeps the codex account it started with; kae quits and relaunches it now with your consent")
	case residentOpRollback:
		return msgf("codex: the ChatGPT app is running and keeps the codex account it started with; after the rollback, kae quits and relaunches it with your consent")
	}
	return msgf("codex: the ChatGPT app is running and keeps the codex account it started with; after the switch, kae quits and relaunches it with your consent")
}

// appPlanned is appAhead under --dry-run. The login flow has no --dry-run.
func (op residentOp) appPlanned() message {
	if op == residentOpRollback {
		return msgf("codex: the ChatGPT app is running and keeps the codex account it started with; the rollback would quit and relaunch it with your consent")
	}
	return msgf("codex: the ChatGPT app is running and keeps the codex account it started with; the switch would quit and relaunch it with your consent")
}

// desktopCannotAskMessage is the warning of a run that cannot ask, without --yes.
func desktopCannotAskMessage() message {
	return msgf("codex: the ChatGPT app keeps the codex account it started with, and kae cannot ask here whether to quit it (no terminal, or --json); to use the codex account now live, quit and reopen it, or pass --yes to let kae do it")
}

// desktopQuitFailedMessage is the warning of a quit request that failed.
func desktopQuitFailedMessage() message {
	return msgf("codex: could not ask the ChatGPT app to quit; to use the codex account now live, quit it yourself (Command-Q in the app) and open it again")
}

// desktopUnknownMessage is the warning when kae cannot tell whether the app runs.
func desktopUnknownMessage() message {
	return msgf("codex: could not tell whether the ChatGPT app is running; if it is, quit and reopen it to use the codex account now live")
}
