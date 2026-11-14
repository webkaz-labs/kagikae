package cmd

import (
	"context"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
)

// The active account's live login, as `kae status` and doctor report it
// (docs/CLI.md § Credential freshness in listings, "The active account's live
// login"). The snapshot is what kae would apply; the live copy is what the tool
// runs on, and it can die — claude tombstones it after a failed refresh — while the
// snapshot still reports ok.

// liveStoreCredentialState judges the live copy of tool's credential as env
// resolves it: the artifact Detect reads for auth_present (the first spec), so on
// darwin a caller holding a keychain.WithReadCache context issues no `security`
// call beyond Detect's. The zero values mean kae could not judge it — a tool with
// no datable credential, an unresolvable layout, an absent or unreadable live
// copy — and every consumer treats that as unknown, never as fine.
//
// It reads one artifact rather than readLiveValues' whole set on purpose: cursor's
// refresh token and API key are two more keychain items that cannot change the
// answer (its freshness is the access token's), and claude's identity cache is no
// credential at all.
func liveStoreCredentialState(ctx context.Context, tool string, env adapter.Env, now time.Time) (credentialState, freshness.Info) {
	if !toolIsDatable(tool) {
		return credentialState{}, freshness.Info{}
	}
	ad, err := adapter.ForTool(tool)
	if err != nil {
		return credentialState{}, freshness.Info{}
	}
	specs, err := ad.Artifacts(ctx, env)
	if err != nil || len(specs) == 0 || specs[0].IdentityOnly {
		return credentialState{}, freshness.Info{}
	}
	value, err := artifact.ReadLive(ctx, specs[0])
	if err != nil || !value.Present {
		return credentialState{}, freshness.Info{}
	}
	info := liveValuesFreshness(tool, []artifact.Value{value})
	return credentialStateAt(info, now), info
}

// credentialRank orders the freshness states from unknown to stale, so two
// readings of one account can be compared.
func credentialRank(state string) int {
	switch state {
	case constants.CredentialOK:
		return 1
	case constants.CredentialExpiring:
		return 2
	case constants.CredentialStale:
		return 3
	default:
		return 0
	}
}

// liveCredentialWorse reports whether the active account's live copy deserves
// reporting next to its snapshot: it needs attention (expiring or stale) and is
// worse than the snapshot's state. One rule for the `kae status` cell and doctor's
// live half, so the two agree. A live copy only as bad as the snapshot adds
// nothing: the active account's remedy is `kae relogin <tool>` for either.
func liveCredentialWorse(live, snapshot string) bool {
	needsAttention := live == constants.CredentialExpiring || live == constants.CredentialStale
	return needsAttention && credentialRank(live) > credentialRank(snapshot)
}

// activeLiveCredential is one tool's active account and the state of the live copy
// of its credential in the real home.
type activeLiveCredential struct {
	tool, account string
	info          freshness.Info
	cred          credentialState
}

// activeLiveCredentials reads, for each enabled tool with an active account, the
// live credential in the real home a global switch acts on — the copy the tool
// runs on outside a bound directory. A tool in global isolated mode (`kae use -i`)
// is skipped: it runs on a private home, so the real home's copy is not the one in
// use. Only readings kae could judge are returned. ctx should carry the keychain
// read cache its caller shares with the other live reads.
func (app *App) activeLiveCredentials(ctx context.Context, toolFilter string) []activeLiveCredential {
	st, err := app.loadState()
	if err != nil {
		return nil // activeOrphanChecks reports an unreadable state.json
	}
	env := app.realHomeEnv()
	now := app.Now()
	out := []activeLiveCredential{}
	for _, tool := range app.enabledTools() {
		if toolFilter != "" && tool != toolFilter {
			continue
		}
		name := st.Active[tool]
		if name == "" {
			continue
		}
		if _, isolated := st.Synced[tool]; isolated {
			continue
		}
		cred, info := liveStoreCredentialState(ctx, tool, env, now)
		if cred.State == "" {
			continue
		}
		out = append(out, activeLiveCredential{tool: tool, account: name, info: info, cred: cred})
	}
	return out
}

// liveCredentialChecks is doctor's live half of credential_stale /
// credential_expiring: the active account's live copy, reported only where
// liveCredentialWorse says it adds to the snapshot finding. snapshot maps each tool
// to its active account's snapshot state; a tool absent from it (an unavailable
// secret backend, a snapshot kae could not judge) counts as unknown.
func liveCredentialChecks(lives []activeLiveCredential, snapshot map[string]string, now time.Time) []adapter.Check {
	checks := []adapter.Check{}
	for _, live := range lives {
		if !liveCredentialWorse(live.cred.State, snapshot[live.tool]) {
			continue
		}
		switch live.cred.State {
		case constants.CredentialStale:
			checks = append(checks, adapter.Check{
				Tool: live.tool, Code: constants.CheckCredentialStale, Status: constants.StatusWarn,
				Message: msgf("the live %s credential of active account %s is stale: %s",
					live.tool, live.account, staleCredentialDetail(live.info, live.tool, live.account, true)),
			})
		case constants.CredentialExpiring:
			checks = append(checks, adapter.Check{
				Tool: live.tool, Code: constants.CheckCredentialExpiring, Status: constants.StatusWarn,
				Message: msgf("the live %s credential of active account %s %s",
					live.tool, live.account, expiringCredentialDetail(live.cred.ReloginBy, now, live.tool, live.account, true)),
			})
		}
	}
	return checks
}
