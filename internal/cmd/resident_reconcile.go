package cmd

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/backup"
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
}

// residentMode is what suppresses a restart: --dry-run, --no-restart and the
// hook shape (--auto). --yes and --quiet change none of it.
type residentMode struct {
	dryRun, noRestart, hook bool
}

func residentModeOf(opts commonOpts) residentMode {
	return residentMode{dryRun: opts.DryRun, noRestart: opts.NoRestart, hook: opts.ResidentHook}
}

// pendingRestart is a daemon restart a switch owes. entry is the `daemon` item
// whose outcome the restart settles; live reads the live credential afresh on
// every call, for the re-probes.
type pendingRestart struct {
	entry  *residentEntry
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
func (app *App) residentsAfterLogin(ctx context.Context, tool string, before credentialReader, mode residentMode) {
	var slot residentSlot
	app.planResidents(ctx, tool, nil, before, &slot, mode)
	app.reconcileSlot(ctx, &slot)
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
	// The probe and accountChanges share one read of each credential.
	if target == nil {
		target = liveCredential(ad, app.Env)
	}
	if previous == nil {
		previous = liveCredential(ad, app.Env)
	}
	target = readOnce(target)
	live := readOnce(previous)
	observed := app.probeResidentDaemon(ctx, holder, spec, target)
	outcome, owed := daemonOutcomeBefore(observed, mode)
	noticeBeforeSwitch(observed, outcome, owed, func() string { return app.residentRestartCommand(holder, spec) })
	residents := []residentEntry{{Kind: constants.ResidentKindDaemon, Observed: observed, Outcome: outcome}}
	if accountChanges(ctx, holder, live, target) {
		residents = append(residents, residentEntry{
			Kind: constants.ResidentKindSession, Observed: constants.ResidentObservedUnknown,
			Outcome: constants.ResidentOutcomeWarned,
		})
		if mode.dryRun {
			warnf("codex sessions started before this switch that are not connected to the managed daemon would keep the previous account until they are restarted")
		} else {
			warnf("codex sessions started before this switch that are not connected to the managed daemon keep the previous account until they are restarted")
		}
	}
	// The array is complete before entry points into it.
	slot.Residents = residents
	if owed {
		slot.restart = &pendingRestart{
			entry: &slot.Residents[0], holder: holder, spec: spec, live: liveCredential(ad, app.Env),
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
// restart_failed. --no-restart wins over the hook shape, and both over --dry-run,
// because `planned` says the switch would restart and neither of them would.
func daemonOutcomeBefore(observed string, mode residentMode) (outcome string, owed bool) {
	switch observed {
	case constants.ResidentObservedDiffers:
		switch {
		case mode.noRestart:
			return constants.ResidentOutcomeOptedOut, false
		case mode.hook:
			return constants.ResidentOutcomeWarned, false
		case mode.dryRun:
			return constants.ResidentOutcomePlanned, false
		}
		return constants.ResidentOutcomePlanned, true
	case constants.ResidentObservedUnknown:
		// Never restarted on a guess, whatever the flags.
		return constants.ResidentOutcomeWarned, false
	}
	return constants.ResidentOutcomeNone, false
}

// noticeBeforeSwitch is step 2's stderr line for one daemon; manual builds the
// manual step the warnings name (residentRestartCommand). absent and matches
// print nothing.
func noticeBeforeSwitch(observed, outcome string, owed bool, manual func() string) {
	if observed == constants.ResidentObservedUnknown {
		warnf("codex: could not read which account the managed daemon (codex app-server daemon) holds; if it still uses the previous account, run: %s", manual())
		return
	}
	if observed != constants.ResidentObservedDiffers {
		return
	}
	switch {
	case owed:
		notef("codex: the managed daemon (codex app-server daemon) holds another account than this switch leaves live; kae restarts it after the switch")
	case outcome == constants.ResidentOutcomePlanned:
		notef("codex: the managed daemon (codex app-server daemon) holds another account than this switch would leave live; the switch would restart it")
	case outcome == constants.ResidentOutcomeOptedOut:
		warnf("codex: the managed daemon (codex app-server daemon) holds another account than this switch leaves live, and --no-restart leaves it running; to move it to the new account, run: %s", manual())
	case outcome == constants.ResidentOutcomeWarned:
		warnf("codex: the managed daemon (codex app-server daemon) holds another account than this switch leaves live, and the enter hook (--auto) does not restart it; to move it to the new account, run: %s", manual())
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

// backupCredential reads tool's credential payload as meta recorded it: what a
// rollback to meta puts back, and for `kae add` what was live before the login
// flow. ok is false when be is nil, the backup has no credential record for tool
// or records it as absent, or its payload cannot be read.
func backupCredential(be secret.Backend, meta backup.Meta, tool string) credentialReader {
	return func(ctx context.Context) ([]byte, bool) {
		if be == nil {
			return nil, false
		}
		rec, ok := backupRecord(meta, tool, credentialArtifactName(tool))
		if !ok || !rec.Present {
			return nil, false
		}
		data, found, err := be.Get(ctx, rec.SecretRef)
		if err != nil || !found {
			return nil, false
		}
		return data, true
	}
}

// residentsAtCapture is the reconcile of `kae add --no-login` (docs/CLI.md
// § kae add Semantics, codex resident processes): the live login stays as it
// is, so nothing is restarted, and a daemon of the real home that holds another
// account than the live credential, or whose account kae cannot read, gets a
// warning only. It only reads, so --dry-run runs it too.
func (app *App) residentsAtCapture(ctx context.Context, tool string) {
	ad, err := adapter.ForTool(tool)
	if err != nil {
		return
	}
	holder, ok := ad.(adapter.ResidentHolder)
	if !ok {
		return
	}
	spec := holder.ResidentDaemon(app.Env)
	observed := app.probeResidentDaemon(ctx, holder, spec, liveCredential(ad, app.Env))
	if msg, ok := app.residentDriftMessage(tool, holder, spec, observed); ok {
		warnMessage(msg)
	}
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
// entry's outcome; the restart reports on stderr. A failed or unverified restart
// is a warning: the exit code is unchanged and nothing is rolled back. It owes
// nothing afterwards, so a second call does not restart again.
func (app *App) reconcileSlot(ctx context.Context, slot *residentSlot) {
	if slot.restart == nil {
		return
	}
	p := slot.restart
	slot.restart = nil
	p.entry.Outcome = app.restartDaemon(ctx, *p)
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
		warnf("codex: codex app-server daemon restart did not finish within %s; the switch is kept, and the managed daemon may still use the previous account; to retry, run: %s", limit, manual())
		return constants.ResidentOutcomeRestartFailed
	case err != nil:
		warnf("codex: could not run codex app-server daemon restart (%v); the switch is kept, and the managed daemon may still use the previous account; to retry, run: %s", err, manual())
		return constants.ResidentOutcomeRestartFailed
	default:
		warnf("codex: codex app-server daemon restart failed (exit %d); the switch is kept, and the managed daemon may still use the previous account; to retry, run: %s", code, manual())
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
			notef("codex: restarted the managed daemon (codex app-server daemon); it now holds the account this switch left live")
			return constants.ResidentOutcomeRestarted
		}
		app.sleep(ctx, residentRecheckInterval)
	}
	warnf("codex: restarted the managed daemon (codex app-server daemon) but could not confirm that it holds the account now live; if it still uses the previous account, run: %s", manual())
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
