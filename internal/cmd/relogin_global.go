package cmd

import (
	"context"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
)

// runGlobalRelogin is `kae relogin [<tool>]` outside a bound directory: it logs
// the tool's active global account in again and keeps the new login live
// (docs/CLI.md § kae relogin Semantics). It is `kae add <tool> <active>` without
// --restore — the same lock, backup, comparison, capture and attribution guard —
// with the account taken from the state rather than typed, so the one command a
// user has to remember for "my login expired" is `kae relogin <tool>` wherever they
// are. `kae add --restore` cannot do it: the previous login it puts back is this
// account's own older copy.
//
// The login runs with the shell's environment, so it refuses where that
// environment carries kae's own isolation: a global login there would land in the
// store the variable names, not the real home kae compares and captures.
func runGlobalRelogin(ctx context.Context, app *App, opts commonOpts, explicitTool string) int {
	st, err := app.loadState()
	if err != nil {
		return finish(opts, err)
	}
	tool, err := globalReloginTool(st.Active, explicitTool)
	if err != nil {
		return finish(opts, err)
	}
	if envVar := app.kaeIsolationVar(tool); envVar != "" {
		return finish(opts, errf(constants.ExitUsage,
			"this directory is not bound, but this shell carries kae's isolation (%s points into kae's stores, as a bound directory above this one or an isolated home sets it), so a global %s login here would land in that store; run kae relogin in the bound directory itself, or in a shell where that binding is not active",
			envVar, tool))
	}
	active := st.Active[tool]
	if _, found, err := account.Load(app.Paths.AccountDir(tool, active)); err != nil {
		return finish(opts, err)
	} else if !found {
		return finish(opts, errf(constants.ExitNotFound,
			"the active %s account %s/%s has no snapshot, so there is no account to log in again; to register it with the official login flow, run: kae add %s %s",
			tool, tool, active, tool, active))
	}
	app.applyGlobalScope()
	return runLoginRequest(ctx, app, opts, loginRequest{tool: tool, explicitName: active, activeOnly: true})
}

// globalReloginTool picks the tool whose active account to log in: the one named,
// or the single tool that has an active account and a login command. Several is a
// usage error for the reason reloginTool gives for a bound directory.
func globalReloginTool(active map[string]string, explicitTool string) (string, error) {
	if explicitTool != "" {
		if loginCommand(explicitTool) == nil {
			return "", errf(constants.ExitUnsupported,
				"kae has no login command for %s, so it cannot log it in again (see docs/ADAPTERS.md)", explicitTool)
		}
		if active[explicitTool] == "" {
			return "", errf(constants.ExitNotFound,
				"no %s account is active globally, so there is no account to log in again; to register one, run: kae add %s <account>",
				explicitTool, explicitTool)
		}
		return explicitTool, nil
	}
	candidates := []string{}
	for _, tool := range constants.Tools {
		if active[tool] != "" && loginCommand(tool) != nil {
			candidates = append(candidates, tool)
		}
	}
	switch len(candidates) {
	case 0:
		return "", errf(constants.ExitNotFound,
			"no tool kae can log in has an active global account; to register one, run: kae add <tool> <account>")
	case 1:
		return candidates[0], nil
	default:
		return "", errf(constants.ExitUsage,
			"%s each have an active global account; name the one to log in again; run: kae relogin <tool>",
			l10n.List(candidates))
	}
}

// kaeIsolationVar names a variable in this shell that points into kae's own
// stores in a way that would redirect a global login of tool: any tool's
// per-directory binding (the shell is inside, or below, a bound directory), or
// tool's own isolation or credential variable set by kae (a bound directory or
// an isolated home). "" when the shell's environment is the real one for tool.
func (app *App) kaeIsolationVar(tool string) string {
	for _, t := range constants.Tools {
		envVar := isolationEnvVar(t)
		if envVar == "" {
			continue
		}
		value := app.Env.Getenv(envVar)
		if _, perDirectory := bindModeFor(app.kaeManagedHomeKind(value)); perDirectory {
			return envVar
		}
		if t == tool && app.isKaeManagedHome(value) {
			return envVar
		}
	}
	if credVar := credentialEnvVar(tool); credVar != "" && app.isKaeManagedCredStore(app.Env.Getenv(credVar)) {
		return credVar
	}
	return ""
}
