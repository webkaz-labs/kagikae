package cmd

import (
	"context"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// residentDriftChecks reports a managed daemon of the real tool home that holds
// another account than the live credential, or whose account kae cannot read
// (docs/CLI.md § `kae doctor --json`, resident_drift). It is the socket half of
// that check: a local probe through probeResidentDaemon, which only reads, so it
// runs by default. `absent` (no daemon) and `matches` are silent.
//
// The `daemon version` half (DaemonSpec.Status, which would catch a moved socket)
// is deliberately not implemented: the contract keeps it off until the acceptance
// records that the command starts no daemon and makes no network call
// (docs/ROADMAP.md § Current work order, slice 6), and nothing here runs a
// subprocess of the tool.
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
		restart := strings.Join(spec.Restart, " ")
		switch app.probeResidentDaemon(ctx, h, spec, liveCredential(ad, env)) {
		case constants.ResidentObservedDiffers:
			checks = append(checks, adapter.Check{
				Tool: tool, Code: constants.CheckResidentDrift, Status: constants.StatusWarn,
				Message: msgf("%s's managed daemon holds a different account from the live credential, "+
					"so sessions connected to it keep using that account; to make it use the live account, run: %s",
					tool, restart),
			})
		case constants.ResidentObservedUnknown:
			checks = append(checks, adapter.Check{
				Tool: tool, Code: constants.CheckResidentDrift, Status: constants.StatusWarn,
				Message: msgf("kae cannot read which account %s's managed daemon holds, "+
					"so it cannot tell whether the daemon uses the live account; if it does not, run: %s",
					tool, restart),
			})
		}
	}
	return checks
}

// realHomeEnv is app.Env with the isolation values kae itself set hidden, the
// view a global switch acts on (applyGlobalScope), without changing app.Env for
// the rest of doctor, whose bound-directory checks read the binding.
func (app *App) realHomeEnv() adapter.Env {
	scoped := App{Paths: app.Paths, Env: app.Env}
	scoped.applyGlobalScope()
	return scoped.Env
}
