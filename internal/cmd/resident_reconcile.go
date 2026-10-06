package cmd

import (
	"bytes"
	"context"
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

// residentOutcomeOwed marks a daemon entry whose restart is owed after the
// transaction; reconcileResidents replaces it with the restart's outcome. It is
// not a token: a report that still carried it would show the defect as an
// empty outcome rather than as a restart that never ran.
const residentOutcomeOwed = ""

// residentEntry is one item of a switch result's `residents` array. It carries
// tokens only: no account, email or plan (docs/SECURITY.md § Resident processes).
type residentEntry struct {
	Kind     string `json:"kind"`
	Observed string `json:"observed"`
	Outcome  string `json:"outcome"`
}

// residentMode is what suppresses a restart: --dry-run, --no-restart and the
// hook shape (--auto). --yes and --quiet change none of it.
type residentMode struct {
	dryRun, noRestart, hook bool
}

func residentModeOf(opts commonOpts) residentMode {
	return residentMode{dryRun: opts.DryRun, noRestart: opts.NoRestart, hook: opts.ResidentHook}
}

// pendingRestart is a daemon restart a switch owes once its transaction has
// succeeded and its locks are released. entry indexes the `daemon` item of
// result's residents, whose outcome the restart fills in.
type pendingRestart struct {
	result, entry int
	ad            adapter.Adapter
	holder        adapter.ResidentHolder
	spec          adapter.DaemonSpec
}

// probeResidentsBeforeSwitch is steps 1 and 2 of the reconcile, run before
// anything is written and without a lock: for every result whose tool keeps
// resident processes, it probes the daemon of the real home once against the
// credential the switch will leave live (the target snapshot's), decides whether
// this command changes the tool's account, records both in the result's
// residents, and gives the notice on stderr. It returns the restarts owed after
// the transaction; under --dry-run, --no-restart and the hook shape there are
// none.
//
// be is nil when the secret backend could not be resolved (a dry-run still
// plans then); the target payload then reads as unknown.
func (app *App) probeResidentsBeforeSwitch(ctx context.Context, be secret.Backend, plans []toolPlan,
	results []switchResult, mode residentMode,
) []pendingRestart {
	var pending []pendingRestart
	for i, plan := range plans {
		ad, err := adapter.ForTool(plan.Tool)
		if err != nil {
			continue
		}
		holder, ok := ad.(adapter.ResidentHolder)
		if !ok {
			continue
		}
		// One spec for the probe and the restart, so the two cannot resolve the
		// home differently. app.Env is already global-scoped (pinnedGlobalScope):
		// a bound directory's CODEX_HOME does not reach it.
		spec := holder.ResidentDaemon(app.Env)
		target := snapshotCredential(be, plan)
		observed := app.probeResidentDaemon(ctx, holder, spec, target)
		outcome := daemonOutcomeBefore(observed, mode)
		results[i].Residents = append(results[i].Residents,
			residentEntry{Kind: constants.ResidentKindDaemon, Observed: observed, Outcome: outcome})
		noticeBeforeSwitch(observed, outcome, app.residentRestartCommand(holder, spec))
		if outcome == residentOutcomeOwed {
			pending = append(pending, pendingRestart{
				result: i, entry: len(results[i].Residents) - 1, ad: ad, holder: holder, spec: spec,
			})
		}
		if accountChanges(ctx, holder, liveCredential(ad, app.Env), target) {
			results[i].Residents = append(results[i].Residents, residentEntry{
				Kind: constants.ResidentKindSession, Observed: constants.ResidentObservedUnknown,
				Outcome: constants.ResidentOutcomeWarned,
			})
			if mode.dryRun {
				warnf("codex sessions started before this switch that are not connected to the managed daemon would keep the previous account until they are restarted")
			} else {
				warnf("codex sessions started before this switch that are not connected to the managed daemon keep the previous account until they are restarted")
			}
		}
	}
	return pending
}

// daemonOutcomeBefore is the daemon entry's outcome as far as it is known before
// the transaction; residentOutcomeOwed when a restart is owed, which the restart
// settles as restarted, restart_unverified or restart_failed.
// --no-restart wins over the hook shape, and both over --dry-run, because
// `planned` says the switch would restart and neither of them would.
func daemonOutcomeBefore(observed string, mode residentMode) string {
	switch observed {
	case constants.ResidentObservedDiffers:
		switch {
		case mode.noRestart:
			return constants.ResidentOutcomeOptedOut
		case mode.hook:
			return constants.ResidentOutcomeWarned
		case mode.dryRun:
			return constants.ResidentOutcomePlanned
		}
		return residentOutcomeOwed
	case constants.ResidentObservedUnknown:
		// Never restarted on a guess, whatever the flags.
		return constants.ResidentOutcomeWarned
	}
	return constants.ResidentOutcomeNone
}

// noticeBeforeSwitch is step 2's stderr line for one daemon; restart is the
// manual step it names (residentRestartCommand). absent and matches print
// nothing.
func noticeBeforeSwitch(observed, outcome, restart string) {
	if observed == constants.ResidentObservedUnknown {
		warnf("codex: could not read which account the managed daemon (codex app-server daemon) holds; if it still uses the previous account, run: %s", restart)
		return
	}
	if observed != constants.ResidentObservedDiffers {
		return
	}
	switch outcome {
	case residentOutcomeOwed:
		notef("codex: the managed daemon (codex app-server daemon) holds another account than this switch leaves live; kae restarts it after the switch")
	case constants.ResidentOutcomePlanned:
		notef("codex: the managed daemon (codex app-server daemon) holds another account than this switch would leave live; the switch would restart it")
	case constants.ResidentOutcomeOptedOut:
		warnf("codex: the managed daemon (codex app-server daemon) holds another account than this switch leaves live, and --no-restart leaves it running; to move it to the new account, run: %s", restart)
	case constants.ResidentOutcomeWarned:
		warnf("codex: the managed daemon (codex app-server daemon) holds another account than this switch leaves live, and the enter hook (--auto) does not restart it; to move it to the new account, run: %s", restart)
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

// reconcileResidents is step 4: it runs once per command, after the whole
// transaction has succeeded and every lock is released (the caller's position
// is the contract: after buildSwitchTargets has returned and after
// teardownSynced), and never when the transaction failed or rolled a tool back
// — the caller returns before reaching it then. It fills in each owed restart's
// outcome in results and reports it on stderr. A failed or unverified restart
// is a warning: the exit code is unchanged and nothing is rolled back.
func (app *App) reconcileResidents(ctx context.Context, results []switchResult, pending []pendingRestart) {
	for _, p := range pending {
		outcome := app.restartDaemon(ctx, p)
		results[p.result].Residents[p.entry].Outcome = outcome
	}
}

// restartDaemon runs the daemon's restart command with the spec's environment,
// which sets CODEX_HOME to the real home after everything inherited, so a bound
// directory's value cannot select another daemon, and bounds it by
// residentRestartLimit. It then re-probes against the live credential as it
// reads at each attempt — not the switch's target, so a later switch by another
// kae process does not make this restart look unverified — each probe bounded by
// what is left of the 5 s.
func (app *App) restartDaemon(ctx context.Context, p pendingRestart) string {
	manual := app.residentRestartCommand(p.holder, p.spec)
	limit := app.residentRestartLimit()
	runCtx, cancel := context.WithTimeout(ctx, limit)
	_, _, code := runner.RunWithEnv(runCtx, p.spec.Env, p.spec.Restart[0], p.spec.Restart[1:]...)
	timedOut := runCtx.Err() != nil
	cancel()
	if timedOut {
		warnf("codex: codex app-server daemon restart did not finish within %s; the switch is kept, and the managed daemon may still use the previous account; to retry, run: %s", limit, manual)
		return constants.ResidentOutcomeRestartFailed
	}
	if code != 0 {
		warnf("codex: codex app-server daemon restart failed (exit %d); the switch is kept, and the managed daemon may still use the previous account; to retry, run: %s", code, manual)
		return constants.ResidentOutcomeRestartFailed
	}
	deadline := app.Now().Add(residentRecheckLimit)
	for {
		remaining := deadline.Sub(app.Now())
		if remaining <= 0 || ctx.Err() != nil {
			break
		}
		probeCtx, cancel := context.WithTimeout(ctx, remaining)
		observed := app.probeResidentDaemon(probeCtx, p.holder, p.spec, liveCredential(p.ad, app.Env))
		cancel()
		if observed == constants.ResidentObservedMatches {
			notef("codex: restarted the managed daemon (codex app-server daemon); it now holds the account this switch left live")
			return constants.ResidentOutcomeRestarted
		}
		app.sleep(ctx, residentRecheckInterval)
	}
	warnf("codex: restarted the managed daemon (codex app-server daemon) but could not confirm that it holds the account now live; if it still uses the previous account, run: %s", manual)
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
