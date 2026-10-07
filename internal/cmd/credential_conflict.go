package cmd

import (
	"context"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// residentHolderFor returns tool's adapter and its ResidentHolder, which carries
// the conflict hook; ok is false for a tool without one, whose payloads are
// never asked (or read) for a verdict.
func residentHolderFor(tool string) (adapter.Adapter, adapter.ResidentHolder, bool) {
	ad, err := adapter.ForTool(tool)
	if err != nil {
		return nil, nil, false
	}
	h, ok := ad.(adapter.ResidentHolder)
	return ad, h, ok
}

// credentialConflicted reports whether a credential artifact among values, read
// for specs, is a Conflict (adapter.ResidentHolder's CredentialConflict). A tool
// without the hook is never one. Identity-only artifacts are not credentials and
// are not asked.
func credentialConflicted(tool string, specs []artifact.Spec, values []artifact.Value) bool {
	_, h, ok := residentHolderFor(tool)
	if !ok {
		return false
	}
	for i, sp := range specs {
		if sp.IdentityOnly || !values[i].Present {
			continue
		}
		if h.CredentialConflict(values[i].Data) == adapter.ConflictDetected {
			return true
		}
	}
	return false
}

// liveOwnerDiffers reports whether a live credential among values, read for
// specs, provably belongs to another account than the payload acc's snapshot
// holds for it (adapter.OwnerComparer's OwnerDifferent): the attribution guard of
// a tool whose identity lives inside the credential, which keepSnapshotIdentity
// cannot compare. Only positive evidence counts — a tool without the comparison,
// an absent live value, a snapshot payload kae cannot read and an Unknown verdict
// all decline nothing, as before the guard existed.
func liveOwnerDiffers(ctx context.Context, be secret.Backend, tool string, specs []artifact.Spec,
	acc account.Account, values []artifact.Value,
) bool {
	ad, err := adapter.ForTool(tool)
	if err != nil {
		return false
	}
	oc, ok := ad.(adapter.OwnerComparer)
	if !ok {
		return false
	}
	for i, sp := range specs {
		if sp.IdentityOnly || !values[i].Present {
			continue
		}
		recorded, ok := storedPayload(ctx, be, acc, sp.Name)
		if !ok {
			continue
		}
		if oc.CompareOwner(recorded, values[i].Data) == adapter.OwnerDifferent {
			return true
		}
	}
	return false
}

// mixedLoginFact is the one sentence every message about a Conflict starts from:
// subject names the login (the live one, or a snapshot) and nothing of its
// contents — the ids that disagree are personal data, and the verdict carries
// none of them anyway.
func mixedLoginFact(subject message) message {
	return msgf("%s carries one account's tokens under another account's id", subject)
}

func liveLoginSubject(tool string) message { return msgf("the live %s login", tool) }

// credentialConflictReason is why a recapture declined a Conflict login.
func credentialConflictReason(tool, accountName string) message {
	return msgf("%s, so kae cannot file it as %s/%s", mixedLoginFact(liveLoginSubject(tool)), tool, accountName)
}

// errCredentialConflict is captureSnapshot's refusal of a Conflict login. Its
// remedy is a plain login, not globalLoginRemedy's `kae add --restore`: restoring
// the previous live state would put the mixed login back.
func errCredentialConflict(tool, accountName string) *cmdError {
	return errf(constants.ExitUnsafeRefused, "%s, so kae will not capture it as %s/%s; %s",
		mixedLoginFact(liveLoginSubject(tool)), tool, accountName,
		msgf("to log in to %s again as account %s, run: kae add %s %s", tool, accountName, tool, accountName))
}

// credentialConflictLiveChecks is the live half of doctor's
// credential_account_conflict (docs/CLI.md § `kae doctor --json`): the live
// login of each tool that declares the hook, read from the real home a global
// switch acts on. It needs no secret backend, so doctor runs it when the backend
// is unavailable too.
func (app *App) credentialConflictLiveChecks(ctx context.Context, toolFilter string) []adapter.Check {
	checks := []adapter.Check{}
	env := app.realHomeEnv()
	for _, tool := range app.enabledTools() {
		if toolFilter != "" && tool != toolFilter {
			continue
		}
		ad, h, ok := residentHolderFor(tool)
		if !ok {
			continue
		}
		payload, ok := liveCredential(ad, env)(ctx)
		if !ok || h.CredentialConflict(payload) != adapter.ConflictDetected {
			continue
		}
		checks = append(checks, adapter.Check{
			Tool: tool, Code: constants.CheckCredentialAccountConflict, Status: constants.StatusWarn,
			Message: msgf("%s, so kae will not file it under any account; %s",
				mixedLoginFact(liveLoginSubject(tool)),
				msgf("log in to %s again as the account you mean to use", tool)),
		})
	}
	return checks
}

// credentialConflictSnapshotChecks is the snapshot half: every saved snapshot of
// a tool that declares the hook, inactive ones included. be is the cached view
// credentialHealthChecks passes inside its read-cache scope (which says why).
func credentialConflictSnapshotChecks(ctx context.Context, be secret.Backend, accounts []account.Account, toolFilter string) []adapter.Check {
	checks := []adapter.Check{}
	for _, acc := range accounts {
		if toolFilter != "" && acc.Tool != toolFilter {
			continue
		}
		if !snapshotConflicted(ctx, be, acc) {
			continue
		}
		checks = append(checks, adapter.Check{
			Tool: acc.Tool, Code: constants.CheckCredentialAccountConflict, Status: constants.StatusWarn,
			Message: msgf("%s, so switching to it applies a login that is partly another account's; %s",
				mixedLoginFact(msgf("snapshot %q", acc.Name)), globalLoginRemedy(acc.Tool, acc.Name)),
		})
	}
	return checks
}

// snapshotConflicted reports whether a saved payload of acc is a Conflict. A
// payload it cannot read is not one: secretMissingChecks reports those.
func snapshotConflicted(ctx context.Context, be secret.Backend, acc account.Account) bool {
	_, h, ok := residentHolderFor(acc.Tool)
	if !ok {
		return false
	}
	for _, name := range acc.ArtifactNames() {
		data, ok := storedPayload(ctx, be, acc, name)
		if !ok {
			continue
		}
		if h.CredentialConflict(data) == adapter.ConflictDetected {
			return true
		}
	}
	return false
}

// storedPayload reads the payload acc's snapshot holds for artifact name. ok is
// false when the snapshot has no such artifact, records it absent, or its
// payload is missing from or unreadable in be — callers that read a snapshot
// only as evidence treat all of those alike. accountFreshness (which reports a
// read error) and snapshotArtifactDiffers (which compares presence and returns
// the error) need the distinction and read the backend themselves.
func storedPayload(ctx context.Context, be secret.Backend, acc account.Account, name string) ([]byte, bool) {
	art, ok := acc.Artifacts[name]
	if !ok || !art.Present {
		return nil, false
	}
	data, found, err := be.Get(ctx, art.SecretRef)
	if err != nil || !found {
		return nil, false
	}
	return data, true
}
