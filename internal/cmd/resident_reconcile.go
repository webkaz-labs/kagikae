package cmd

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// How the restart is verified (docs/CLI.md § kae use Semantics, **Resident
// processes (codex)**, step 4): re-probe this often, for at most this long.
const (
	residentRecheckInterval = 250 * time.Millisecond
	residentRecheckLimit    = 5 * time.Second
	// defaultResidentRestartTimeout bounds the restart command itself; one that
	// has not finished by then is restart_failed.
	defaultResidentRestartTimeout = 30 * time.Second
)

// residentEntry is one item of a switch result's `residents` array. It carries
// tokens only: no account, email or plan (docs/SECURITY.md § Resident processes).
type residentEntry struct {
	Kind     string `json:"kind"`
	Observed string `json:"observed"`
	Outcome  string `json:"outcome"`
}

// residentSlot is what a report keeps per tool for the reconcile: the
// `residents` array it prints, and the restart owed once the transaction has
// succeeded. A report's result embeds it, so the owed restart travels with the
// result wherever the result is copied.
type residentSlot struct {
	Residents []residentEntry `json:"residents"`
	restart   *pendingRestart
	quits     []pendingQuit
	// done is the success the reconcile settled (residentDone), held for the line
	// after the command's result line; nil when there is none or it was said.
	done *message
}

// printDone says the held success once: indented under the command's result line
// when underReport, or as a `kae: note:` line for a run that prints no result
// line (--json, --quiet). Both go to stderr.
func (s *residentSlot) printDone(underReportLine bool) {
	if s.done == nil {
		return
	}
	done := *s.done
	s.done = nil
	if underReportLine {
		printUnderReport(underReport(done))
		return
	}
	noteMessage(alone(done))
}

// printResidentsDone is printDone, alone, for each of results.
func printResidentsDone(results []switchResult) {
	for i := range results {
		results[i].printDone(false)
	}
}

// residentMode is what suppresses a restart: --dry-run, --no-restart and the
// hook shape (--auto). --yes and --quiet change none of it. yes and json steer
// only the ChatGPT app's confirmation: --yes answers it, and --json, like a run
// without a terminal, cannot show it. op is the command the notices name.
type residentMode struct {
	dryRun, noRestart, hook bool
	yes, json               bool
	op                      residentOp
}

// residentStance is how a run treats the resident processes it finds: it acts on
// them, or, under --dry-run, --no-restart or the hook shape, it does not.
type residentStance int

const (
	stanceAct residentStance = iota
	stanceDryRun
	stanceOptedOut
	stanceHook
)

// stance is the one precedence the daemon's outcome, the app's plan and the
// notice share: --no-restart wins over the hook shape, and both over --dry-run,
// because `planned` says the run would restart and neither of them would.
func (m residentMode) stance() residentStance {
	switch {
	case m.noRestart:
		return stanceOptedOut
	case m.hook:
		return stanceHook
	case m.dryRun:
		return stanceDryRun
	}
	return stanceAct
}

func residentModeOf(opts commonOpts) residentMode {
	return residentMode{
		dryRun: opts.DryRun, noRestart: opts.NoRestart, hook: opts.ResidentHook,
		yes: opts.Yes, json: opts.Format == formatJSON,
	}
}

// pendingRestart is a daemon restart a switch owes. entry is the `daemon` item
// whose outcome the restart settles; live reads the live credential afresh on
// every call, for the re-probes.
type pendingRestart struct {
	entry  *residentEntry
	op     residentOp
	holder adapter.ResidentHolder
	spec   adapter.DaemonSpec
	live   credentialReader
}

// residentsBeforeSwitch is steps 1 and 2 of the reconcile for one tool, run
// before anything is written and without a lock. When the tool keeps resident
// processes, it probes the daemon of the real home once against target — the
// credential the switch will leave live — decides whether this command changes
// the tool's account, fills slot's residents, gives the notice on stderr, and
// records in slot the restart owed after the transaction (none under --dry-run,
// --no-restart or the hook shape). A tool without resident processes keeps an
// empty array.
func (app *App) residentsBeforeSwitch(ctx context.Context, tool string, target credentialReader,
	slot *residentSlot, mode residentMode,
) {
	app.planResidents(ctx, tool, target, nil, slot, mode)
}

// residentsAfterLogin is the reconcile of `kae add`'s login flow (docs/CLI.md
// § kae add Semantics, codex resident processes): the flow decides the account,
// so it runs after the flow and after the lock is released, probes the daemon
// once against the credential finally left live, and restarts it at once when
// it differs and --no-restart is not given. before reads the credential live
// before the flow, for whether the command changed the tool's account.
// It returns the slot, whose held success follows add's result line.
func (app *App) residentsAfterLogin(ctx context.Context, tool string, before credentialReader, mode residentMode) *residentSlot {
	var slot residentSlot
	mode.op = residentOpLogin
	app.planResidents(ctx, tool, nil, before, &slot, mode)
	app.reconcileSlot(ctx, &slot)
	return &slot
}

// planResidents is residentsBeforeSwitch with the two credentials it compares
// open: target is what the command leaves live and previous what was live
// before it; nil reads the live credential.
func (app *App) planResidents(ctx context.Context, tool string, target, previous credentialReader,
	slot *residentSlot, mode residentMode,
) {
	slot.Residents = []residentEntry{}
	ad, err := adapter.ForTool(tool)
	if err != nil {
		return
	}
	holder, ok := ad.(adapter.ResidentHolder)
	if !ok {
		return
	}
	// One spec for the probe and the restart, so the two cannot resolve the home
	// differently. app.Env is already global-scoped (pinnedGlobalScope): a bound
	// directory's CODEX_HOME does not reach it.
	spec := holder.ResidentDaemon(app.Env)
	// The probe and accountChanges share one read of each credential. A nil
	// target or previous reads the live credential.
	liveReader := liveCredential(ad, app.Env)
	if target == nil {
		target = liveReader
	}
	if previous == nil {
		previous = liveReader
	}
	target = readOnce(target)
	live := readOnce(previous)
	observed := app.probeResidentDaemon(ctx, holder, spec, target)
	outcome, owed := daemonOutcomeBefore(observed, mode)
	residents := []residentEntry{{Kind: constants.ResidentKindDaemon, Observed: observed, Outcome: outcome}}
	// The desktop apps matter only to a command that changes the account: one
	// that does not leaves them on the account they hold, so they are not asked
	// about and get no entry.
	var apps []desktopEntry
	appBase := 0
	changes := accountChanges(ctx, holder, live, target)
	if changes {
		apps = app.planDesktopApps(ctx, holder, mode)
		appBase = len(residents)
		for _, a := range apps {
			residents = append(residents, a.entry)
		}
		residents = append(residents, residentEntry{
			Kind: constants.ResidentKindSession, Observed: constants.ResidentObservedUnknown,
			Outcome: constants.ResidentOutcomeWarned,
		})
	}
	noticeResidents(mode, observed, apps, func() string { return app.residentRestartCommand(holder, spec) })
	// The session warning follows the notice, so the entries are built above it.
	if changes {
		warnMessage(mode.op.session(mode.dryRun))
	}
	// The array is complete before an entry points into it.
	slot.Residents = residents
	if owed {
		slot.restart = &pendingRestart{
			entry: &slot.Residents[0], op: mode.op, holder: holder, spec: spec, live: liveReader,
		}
	}
	for i, a := range apps {
		if a.plan.owed() {
			slot.quits = append(slot.quits, pendingQuit{
				entry: &slot.Residents[appBase+i], bundleID: a.bundleID, plan: a.plan,
			})
		}
	}
}

// readOnce returns a reader that calls r once and replays its answer.
func readOnce(r credentialReader) credentialReader {
	var payload []byte
	var ok, read bool
	return func(ctx context.Context) ([]byte, bool) {
		if !read {
			payload, ok = r(ctx)
			read = true
		}
		return payload, ok
	}
}

// daemonOutcomeBefore is the daemon entry's outcome as far as it is known before
// the transaction. owed is true when a restart is owed; the outcome is then
// `planned` until the restart settles restarted, restart_unverified or
// restart_failed.
func daemonOutcomeBefore(observed string, mode residentMode) (outcome string, owed bool) {
	switch observed {
	case constants.ResidentObservedDiffers:
		switch mode.stance() {
		case stanceOptedOut:
			return constants.ResidentOutcomeOptedOut, false
		case stanceHook:
			return constants.ResidentOutcomeWarned, false
		case stanceDryRun:
			return constants.ResidentOutcomePlanned, false
		}
		return constants.ResidentOutcomePlanned, true
	case constants.ResidentObservedUnknown:
		// Never restarted on a guess, whatever the flags.
		return constants.ResidentOutcomeWarned, false
	}
	return constants.ResidentOutcomeNone, false
}

// noticeResidents is step 2's stderr lines for the daemon and the running apps
// (docs/CLI.md § kae use Semantics, **How the lines read**): a daemon kae cannot
// read gets its own warning; then one line says what the run does to the daemon
// (when it differs) and the apps together — a note, or under --no-restart and the
// hook shape a warning with the manual steps; then each app kae cannot ask about
// or cannot tell about gets its own warning. manual builds the manual restart
// (residentRestartCommand). absent and matches print nothing.
func noticeResidents(mode residentMode, observed string, apps []desktopEntry, manual func() string) {
	if observed == constants.ResidentObservedUnknown {
		warnMessage(daemonUnknownMessage(manual()))
	}
	daemon := observed == constants.ResidentObservedDiffers
	app := residentAppNone
	for _, a := range apps {
		if app == residentAppNone && a.entry.Observed == constants.ResidentObservedRunning {
			app = a.plan.announced()
		}
	}
	switch stance := mode.stance(); stance {
	case stanceOptedOut, stanceHook:
		if m, ok := suppressedMessage(suppressedReason(stance == stanceHook), daemon, app != residentAppNone, manual); ok {
			warnMessage(m)
		}
	case stanceDryRun:
		if action, ok := residentAction(daemon, app); ok {
			noteMessage(mode.op.planned(action))
		}
	default:
		if action, ok := residentAction(daemon, app); ok {
			noteMessage(mode.op.ahead(action))
		}
	}
	for _, a := range apps {
		switch {
		case a.entry.Observed == constants.ResidentObservedUnknown:
			warnMessage(desktopUnknownMessage())
		case a.plan.consent == consentCannotAsk:
			warnMessage(desktopCannotAskMessage())
		}
	}
}

// accountChanges reports whether the switch changes the tool's account: whether
// the account of the live credential differs from the target's. Two credentials
// that both name an account are compared by it. When either names none (an
// API-key login, nothing live, a payload kae cannot read), byte-identical
// payloads are the same login and anything else counts as a change, so a switch
// kae cannot judge still gets the session warning rather than silence.
func accountChanges(ctx context.Context, h adapter.ResidentHolder, live, target credentialReader) bool {
	targetPayload, targetOK := target(ctx)
	livePayload, liveOK := live(ctx)
	if !targetOK || !liveOK {
		return true
	}
	targetAccount, targetNamed := h.CredentialAccount(targetPayload)
	liveAccount, liveNamed := h.CredentialAccount(livePayload)
	if targetNamed && liveNamed {
		return !targetAccount.Same(liveAccount)
	}
	return !bytes.Equal(targetPayload, livePayload)
}

// snapshotCredential reads the credential payload plan will write: the target
// snapshot's, from the one artifact that is not identity-only. ok is false when
// be is nil, the snapshot cannot be read, or it holds no credential.
// backupCredential is its counterpart for what a backup puts back.
func snapshotCredential(be secret.Backend, plan toolPlan) credentialReader {
	return func(ctx context.Context) ([]byte, bool) {
		if be == nil {
			return nil, false
		}
		values, err := snapshotValues(ctx, be, plan)
		if err != nil {
			return nil, false
		}
		var payload []byte
		credentials := 0
		for i, sp := range plan.Specs {
			if sp.IdentityOnly {
				continue
			}
			credentials++
			if values[i].Present {
				payload = values[i].Data
			}
		}
		if credentials != 1 || payload == nil {
			return nil, false
		}
		return payload, true
	}
}

// residentsAtCapture is the reconcile of `kae add --no-login` (docs/CLI.md
// § kae add Semantics, codex resident processes): the live login stays as it
// is, so nothing is restarted, and a daemon of the real home that holds another
// account than the live credential or none, or whose account kae cannot read,
// gets a warning only. It only reads, so --dry-run runs it too. It returns the
// result's `residents`: that daemon's entry, outcome `warned`, or none.
func (app *App) residentsAtCapture(ctx context.Context, tool string) []residentEntry {
	residents := []residentEntry{}
	ad, err := adapter.ForTool(tool)
	if err != nil {
		return residents
	}
	holder, ok := ad.(adapter.ResidentHolder)
	if !ok {
		return residents
	}
	spec := holder.ResidentDaemon(app.Env)
	observed := app.probeResidentDaemon(ctx, holder, spec, liveCredential(ad, app.Env))
	if msg, ok := app.residentDriftMessage(tool, holder, spec, observed); ok {
		warnMessage(msg)
		residents = append(residents, residentEntry{
			Kind: constants.ResidentKindDaemon, Observed: observed, Outcome: constants.ResidentOutcomeWarned,
		})
	}
	return residents
}

// reconcileResidents is step 4 for a switch's results: it runs once per
// command, after the whole transaction has succeeded and every lock is released
// (the caller's position is the contract: after buildSwitchTargets has returned
// and after teardownSynced), and never when the transaction failed or rolled a
// tool back — the caller returns before reaching it then.
func (app *App) reconcileResidents(ctx context.Context, results []switchResult) {
	for i := range results {
		app.reconcileSlot(ctx, &results[i].residentSlot)
	}
}

// reconcileSlot runs the restart slot owes, if any, and settles its daemon
// entry's outcome; then, after the restart, it asks about and quits each desktop
// app slot owes (step 5) and settles that entry. A failed or unverified restart,
// and every app outcome but a relaunch and an app closed meanwhile, is a warning
// said here: the exit code is unchanged and nothing is rolled back. The success —
// a verified restart, the first relaunch, or both — is held in slot.done for the
// line after the command's result (printDone), so an interrupt before then says
// nothing of it. It owes nothing afterwards, so a second call does not act again.
func (app *App) reconcileSlot(ctx context.Context, slot *residentSlot) {
	restarted := false
	if p := slot.restart; p != nil {
		slot.restart = nil
		p.entry.Outcome = app.restartDaemon(ctx, *p)
		restarted = p.entry.Outcome == constants.ResidentOutcomeRestarted
	}
	quits := slot.quits
	slot.quits = nil
	relaunched := false
	for _, q := range quits {
		line := app.quitDesktopApp(ctx, q)
		if !relaunched && q.entry.Outcome == constants.ResidentOutcomeRelaunched {
			relaunched = true
			continue
		}
		line.print()
	}
	if done, ok := residentDone(restarted, relaunched); ok {
		slot.done = &done
	}
}

// restartDaemon runs the daemon's restart command with the spec's environment,
// which sets CODEX_HOME to the real home after everything inherited, so a bound
// directory's value cannot select another daemon, and bounds it by
// residentRestartLimit. It goes through runner.LaunchWithEnv, which reads no
// output, so a daemon the command leaves running cannot hold kae past the limit.
// It then re-probes every residentRecheckInterval for at most
// residentRecheckLimit, each probe bounded by what is left of that, against the
// live credential as it reads at each attempt — not the switch's target, so a
// later switch by another kae process does not make this restart look
// unverified.
func (app *App) restartDaemon(ctx context.Context, p pendingRestart) string {
	manual := func() string { return app.residentRestartCommand(p.holder, p.spec) }
	limit := app.residentRestartLimit()
	runCtx, cancel := context.WithTimeout(ctx, limit)
	code, err := runner.LaunchWithEnv(runCtx, p.spec.Env, p.spec.Restart[0], p.spec.Restart[1:]...)
	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)
	cancel()
	// A command that exited 0 as the deadline passed restarted the daemon, so
	// success is judged first. A cancelled parent (an interrupt) is a plain
	// failure, not a timeout.
	switch {
	case code == 0 && err == nil:
	case timedOut:
		warnMessage(p.op.restartTimedOut(limit, manual()))
		return constants.ResidentOutcomeRestartFailed
	case err != nil:
		warnMessage(p.op.restartNotRun(err, manual()))
		return constants.ResidentOutcomeRestartFailed
	default:
		warnMessage(p.op.restartExited(code, manual()))
		return constants.ResidentOutcomeRestartFailed
	}
	deadline := app.Now().Add(residentRecheckLimit)
	for {
		remaining := deadline.Sub(app.Now())
		if remaining <= 0 || ctx.Err() != nil {
			break
		}
		probeCtx, cancel := context.WithTimeout(ctx, remaining)
		observed := app.probeResidentDaemon(probeCtx, p.holder, p.spec, p.live)
		cancel()
		if observed == constants.ResidentObservedMatches {
			// reconcileSlot holds it for the line after the result.
			return constants.ResidentOutcomeRestarted
		}
		app.sleep(ctx, residentRecheckInterval)
	}
	warnMessage(restartUnverifiedMessage(manual()))
	return constants.ResidentOutcomeRestartUnverified
}

// residentRestartLimit bounds the restart command: App.residentRestartTimeout,
// or the default.
func (app *App) residentRestartLimit() time.Duration {
	if app.residentRestartTimeout > 0 {
		return app.residentRestartTimeout
	}
	return defaultResidentRestartTimeout
}

// sleep waits d or until ctx ends: App.sleepForTest, or a timer.
func (app *App) sleep(ctx context.Context, d time.Duration) {
	if app.sleepForTest != nil {
		app.sleepForTest(d)
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
