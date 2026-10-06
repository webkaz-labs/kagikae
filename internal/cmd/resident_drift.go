package cmd

import (
	"context"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// residentDriftChecks reports a managed daemon of the real tool home that holds
// another account than the live credential, or whose account kae cannot read
// (docs/CLI.md § `kae doctor --json`, resident_drift, which owns the contract).
// It probes the daemon's socket only, through probeResidentDaemon, which only
// reads. `absent` (no daemon) and `matches` are silent.
//
// It does not run `codex app-server daemon version` (DaemonSpec.Status) or any
// other subprocess of the tool. Reading the live credential may still run the
// platform's keychain reader (`security` on darwin) under the keyring store.
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
		// The restart is named for the shell doctor runs in, which may export
		// another home than the real one the probe looked at (docs/CLI.md § kae use
		// Semantics, The manual step).
		var msg message
		switch app.probeResidentDaemon(ctx, h, spec, liveCredential(ad, env)) {
		case constants.ResidentObservedDiffers:
			msg = msgf("%s's managed daemon holds a different account from the live credential, "+
				"so sessions connected to it keep using that account; to make it use the live account, run: %s",
				tool, app.residentRestartCommand(h, spec))
		case constants.ResidentObservedUnknown:
			msg = msgf("kae cannot read which account %s's managed daemon holds, "+
				"so it cannot tell whether the daemon uses the live account; if it does not, run: %s",
				tool, app.residentRestartCommand(h, spec))
		default:
			continue
		}
		checks = append(checks, adapter.Check{
			Tool: tool, Code: constants.CheckResidentDrift, Status: constants.StatusWarn, Message: msg,
		})
	}
	return checks
}
