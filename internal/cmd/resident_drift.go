package cmd

import (
	"context"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// residentDriftChecks is resident_drift's socket half for the real tool home,
// which doctor runs only under --yes; docs/CLI.md § `kae doctor --json` owns what
// it reports. It probes the daemon's socket only, through askResidentDaemon,
// which only reads auth state but whose initialize has side effects on the
// daemon (docs/ADAPTERS.md § Resident processes).
//
// It runs no subprocess of the tool: the `daemon version` half
// (DaemonSpec.Status, resident_daemon_version.go) runs in doctorProbeRound's
// round instead. Reading the live credential may still run the platform's
// keychain reader (`security` on darwin) under the keyring store.
//
// The probe looks at the real home, never the one a bound directory exports:
// doctor run inside a bound directory still answers for the daemon a global
// switch reconciles. It needs no secret backend, so an unavailable backend does
// not hide it. Neither account reaches the message.
func (app *App) residentDriftChecks(ctx context.Context, toolFilter string) []adapter.Check {
	checks := []adapter.Check{}
	env := app.realHomeEnv()
	for _, tool := range app.enabledTools() {
		if toolFilter != "" && tool != toolFilter {
			continue
		}
		ad, err := adapter.ForTool(tool)
		if err != nil {
			continue
		}
		h, ok := ad.(adapter.ResidentHolder)
		if !ok {
			continue // no resident processes: nothing to compare
		}
		spec := h.ResidentDaemon(env)
		observed := app.askResidentDaemon(ctx, h, spec, liveCredential(ad, env)).observed
		msg, ok := app.residentDriftMessage(tool, h, spec, observed)
		if !ok {
			continue
		}
		checks = append(checks, adapter.Check{
			Tool: tool, Code: constants.CheckResidentDrift, Status: constants.StatusWarn, Message: msg,
		})
	}
	return checks
}

// residentDriftMessage is the finding for a daemon observed against the live
// credential, for doctor's resident_drift: ok is false for absent and matches,
// which say nothing. The restart is named for the shell kae runs in, which may
// export another home than the real one the probe looked at (docs/CLI.md § kae
// use Semantics, The manual step).
func (app *App) residentDriftMessage(tool string, h adapter.ResidentHolder, spec adapter.DaemonSpec,
	observed string,
) (message, bool) {
	switch observed {
	case constants.ResidentObservedDiffers:
		return msgf("%s's managed daemon is not using the live credential's account, "+
			"and neither are the sessions connected to it; to make it use the live account, run: %s",
			tool, app.residentRestartCommand(h, spec)), true
	case constants.ResidentObservedUnknown:
		return msgf("kae cannot read which account %s's managed daemon holds, "+
			"so it cannot tell whether the daemon uses the live account; if it does not, run: %s",
			tool, app.residentRestartCommand(h, spec)), true
	}
	return message{}, false
}
