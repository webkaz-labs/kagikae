package cmd

import (
	"context"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// residentDaemonVersionEnabled turns on resident_drift's `daemon version` half
// (docs/CLI.md § `kae doctor --json`, resident_drift), on by default since the
// local acceptance observed that `codex app-server daemon version` starts no
// daemon when none runs and answers with IP traffic denied (docs/ACCEPTANCE.md).
// A var so a test can turn it off and prove the half then runs nothing.
var residentDaemonVersionEnabled = true

// daemonVersionProbe is one `daemon version` run planned for the probe round.
type daemonVersionProbe struct {
	tool   string
	holder adapter.ResidentHolder
	spec   adapter.DaemonSpec
}

// planDaemonVersion returns the `daemon version` run for ad, or ok=false when
// the half is disabled, ad has no managed daemon, or its binary is not on PATH
// (binary_present reports that). The spec is the real home's, like the socket
// half's: CODEX_HOME is set to it explicitly, never inherited from a bound
// directory.
func (app *App) planDaemonVersion(tool string, ad adapter.Adapter, realHome adapter.Env) (daemonVersionProbe, bool) {
	if !residentDaemonVersionEnabled {
		return daemonVersionProbe{}, false
	}
	h, ok := ad.(adapter.ResidentHolder)
	if !ok {
		return daemonVersionProbe{}, false
	}
	spec := h.ResidentDaemon(realHome)
	if len(spec.Status) == 0 {
		return daemonVersionProbe{}, false
	}
	if _, err := app.Env.LookPath(spec.Status[0]); err != nil {
		return daemonVersionProbe{}, false
	}
	return daemonVersionProbe{tool: tool, holder: h, spec: spec}, true
}

// daemonVersionProbes plans one `daemon version` probe per tool in tools for
// doctorProbeRound; a nil entry spawns nothing (planDaemonVersion).
func (app *App) daemonVersionProbes(tools []string) []roundProbe {
	probes := make([]roundProbe, len(tools))
	realHome := app.realHomeEnv()
	for i, tool := range tools {
		ad, err := adapter.ForTool(tool)
		if err != nil {
			continue
		}
		if p, ok := app.planDaemonVersion(tool, ad, realHome); ok {
			probes[i] = func(ctx context.Context) (adapter.Check, bool) { return p.run(ctx, app) }
		}
	}
	return probes
}

// run executes the status command under ctx (the round's deadline) through
// runner.QueryWithEnv, which waits for the command only and not for a daemon it
// may leave running, and returns the finding, if any.
func (p daemonVersionProbe) run(ctx context.Context, app *App) (adapter.Check, bool) {
	stdout, code := runner.QueryWithEnv(ctx, p.spec.Env, p.spec.Status[0], p.spec.Status[1:]...)
	if code != 0 {
		return adapter.Check{}, false // a failing or killed probe is skipped, like `--version`
	}
	running, socket, ok := p.holder.ParseDaemonStatus([]byte(stdout))
	if !ok || !running || !socketMoved(socket, p.spec.Socket) {
		return adapter.Check{}, false
	}
	return adapter.Check{
		Tool: p.tool, Code: constants.CheckResidentDrift, Status: constants.StatusWarn,
		Message: msgf("%s's managed daemon reports that it is running, but not at the socket kae looks for, "+
			"so %s has changed where it puts the socket and a switch can no longer find the daemon to restart it; "+
			"after each %s switch, run: %s",
			p.tool, p.tool, p.tool, app.residentRestartCommand(p.holder, p.spec)),
	}, true
}

// socketMoved reports whether a running daemon's reported socket is not the
// declared one, compared after resolving symlinks as the socket half resolves
// the declared one (resolveDeclaredSocket): the declared socket does not exist
// while the reported one does, or both exist and resolve to different paths. A
// reported path that does not resolve is no evidence either way, nor is an error
// other than a missing declared socket.
func socketMoved(reported, declared string) bool {
	got, err := filepath.EvalSymlinks(reported)
	if err != nil {
		return false
	}
	want, absent, err := resolveDeclaredSocket(declared)
	if err != nil {
		return false
	}
	return absent || got != want
}
