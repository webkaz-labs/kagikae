package cmd

import (
	"context"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// conflictVerdictOf asks tool's adapter whether payload carries one account's
// tokens under another's account record (adapter.ResidentHolder's
// CredentialConflict). A tool that does not declare the hook is Unknown, which
// changes no decision.
func conflictVerdictOf(tool string, payload []byte) adapter.ConflictVerdict {
	ad, err := adapter.ForTool(tool)
	if err != nil {
		return adapter.ConflictUnknown
	}
	h, ok := ad.(adapter.ResidentHolder)
	if !ok {
		return adapter.ConflictUnknown
	}
	return h.CredentialConflict(payload)
}

// credentialConflicted reports whether a credential artifact among values, read
// for specs, is a Conflict. Identity-only artifacts are not credentials and are
// not asked.
func credentialConflicted(tool string, specs []artifact.Spec, values []artifact.Value) bool {
	for i, sp := range specs {
		if sp.IdentityOnly || !values[i].Present {
			continue
		}
		if conflictVerdictOf(tool, values[i].Data) == adapter.ConflictDetected {
			return true
		}
	}
	return false
}

// credentialConflictReason is why a recapture declined a Conflict login. It
// names the tool and the account only: the ids that disagree are personal data,
// and the verdict carries none of them anyway.
func credentialConflictReason(tool, accountName string) message {
	return msgf("the live %s login carries one account's tokens under another account's id, so kae cannot file it as %s/%s",
		tool, tool, accountName)
}

// credentialConflictChecks is doctor's credential_account_conflict (docs/CLI.md
// § `kae doctor --json`): the live login of each tool that declares the hook,
// read from the real home a global switch acts on, and every saved snapshot of
// that tool. be is credentialHealthChecks' cached view inside its read-cache
// scope, so a snapshot payload accountFreshness has already read is not read
// again. Neither the ids nor the email reach a message.
func (app *App) credentialConflictChecks(ctx context.Context, be secret.Backend, toolFilter string) []adapter.Check {
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
			continue
		}
		if payload, ok := liveCredential(ad, env)(ctx); ok && h.CredentialConflict(payload) == adapter.ConflictDetected {
			checks = append(checks, adapter.Check{
				Tool: tool, Code: constants.CheckCredentialAccountConflict, Status: constants.StatusWarn,
				Message: msgf("the live %s login carries one account's tokens under another account's id, "+
					"so kae will not file it under any account; log in to %s again as the account you mean to use",
					tool, tool),
			})
		}
	}
	accounts, err := account.List(app.Paths.AccountsDir())
	if err != nil {
		return checks
	}
	for _, acc := range accounts {
		if toolFilter != "" && acc.Tool != toolFilter {
			continue
		}
		if !snapshotConflicted(ctx, be, acc) {
			continue
		}
		checks = append(checks, adapter.Check{
			Tool: acc.Tool, Code: constants.CheckCredentialAccountConflict, Status: constants.StatusWarn,
			Message: msgf("snapshot %q carries one account's tokens under another account's id; %s",
				acc.Name, globalLoginRemedy(acc.Tool, acc.Name)),
		})
	}
	return checks
}

// snapshotConflicted reports whether a saved payload of acc is a Conflict. A
// payload it cannot read is not one: secretMissingChecks reports those. A tool
// without the hook is not read at all, so its payloads cost no keychain read.
func snapshotConflicted(ctx context.Context, be secret.Backend, acc account.Account) bool {
	ad, err := adapter.ForTool(acc.Tool)
	if err != nil {
		return false
	}
	h, ok := ad.(adapter.ResidentHolder)
	if !ok {
		return false
	}
	for _, name := range acc.ArtifactNames() {
		art := acc.Artifacts[name]
		if !art.Present {
			continue
		}
		data, found, err := be.Get(ctx, art.SecretRef)
		if err != nil || !found {
			continue
		}
		if h.CredentialConflict(data) == adapter.ConflictDetected {
			return true
		}
	}
	return false
}
