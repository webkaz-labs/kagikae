package cmd

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// residentDaemonVersionEnabled turns on resident_drift's `daemon version` half
// (docs/CLI.md § `kae doctor --json`, resident_drift). It stays false until the
// acceptance records that `codex app-server daemon version` starts no daemon when
// none runs and makes no network call (docs/ROADMAP.md § Current work order); the
// switch is this one line. A var so tests can turn it on.
var residentDaemonVersionEnabled = false

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
// declared one: the declared socket does not exist, or the two resolve to
// different paths. Both are compared after resolving symlinks, as the socket
// half resolves the declared one. A reported path that does not resolve cannot
// be the existing declared socket. An error other than a missing declared
// socket is no evidence either way.
func socketMoved(reported, declared string) bool {
	want, err := filepath.EvalSymlinks(declared)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	got, err := filepath.EvalSymlinks(reported)
	return err != nil || got != want
}
