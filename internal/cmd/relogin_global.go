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
// The login flow refuses a shell carrying kae's isolation with this form's own
// remedy (errKaeIsolatedShell).
func runGlobalRelogin(ctx context.Context, app *App, opts commonOpts, explicitTool string) int {
	st, err := app.loadState()
	if err != nil {
		return finish(opts, err)
	}
	tool, err := globalReloginTool(st.Active, explicitTool)
	if err != nil {
		return finish(opts, err)
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
