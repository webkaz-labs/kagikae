package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/freshness"
	"github.com/webkaz-labs/kagikae/internal/secret"
	"github.com/webkaz-labs/kagikae/internal/state"
)

// freshnessOf reads the freshness of a captured payload for tool by dispatching
// to the tool's adapter.Fresher (§A: per-tool credential knowledge lives on the
// adapter, not a central switch). A tool with no Fresher — or an unknown id —
// is not-datable (Known=false), matching the previous default branch.
func freshnessOf(tool string, payload []byte) freshness.Info {
	ad, err := adapter.ForTool(tool)
	if err != nil {
		return freshness.Info{}
	}
	if f, ok := ad.(adapter.Fresher); ok {
		return f.Freshness(payload)
	}
	return freshness.Info{}
}

// accountFreshness reads acc's stored credential and reports its expiry and
// refresh-token presence. It returns the first artifact whose payload parses as
// the tool's known credential format; a not-datable account (copilot pointer,
// agy blob) yields Known=false. Shared by the switch-time stale warning
// (docs/RELEASE.md §B) and doctor credential-health (§D).
func (app *App) accountFreshness(ctx context.Context, be secret.Backend, acc account.Account) (freshness.Info, error) {
	for _, name := range acc.ArtifactNames() {
		art := acc.Artifacts[name]
		if !art.Present {
			continue
		}
		data, found, err := be.Get(ctx, art.SecretRef)
		if err != nil {
			return freshness.Info{}, err
		}
		if !found {
			continue
		}
		if info := freshnessOf(acc.Tool, data); info.Known {
			return info, nil
		}
	}
	return freshness.Info{}, nil
}

// staleSnapshotWarning returns a switch-time warning when acc's snapshot
// credential can no longer open a session without an interactive re-login, so a
// switch to it cannot self-heal (docs/RELEASE.md §B). An expired credential with
// a still-usable refresh token returns "" — the tool refreshes it on next use.
// A fresh, undated, or not-datable account returns "".
func (app *App) staleSnapshotWarning(ctx context.Context, be secret.Backend, acc account.Account) (string, error) {
	info, err := app.accountFreshness(ctx, be, acc)
	if err != nil {
		return "", err
	}
	if !needsRelogin(info, app.Now()) {
		return "", nil
	}
	return "snapshot credential is stale: " + staleCredentialDetail(info, acc.Tool, acc.Name), nil
}

// snapshotExpired reports whether a known, dated credential is past expiry.
func snapshotExpired(info freshness.Info, now time.Time) bool {
	return info.Known && !info.ExpiresAt.IsZero() && info.ExpiresAt.Before(now)
}

// refreshUsable reports whether info's refresh token can still buy a new access
// token: present, and not itself past a published expiry. A payload that
// publishes no refresh expiry leaves RefreshExpiresAt zero and presence is all
// kae has to go on; a zero value is "unknown", never "never expires".
func refreshUsable(info freshness.Info, now time.Time) bool {
	return info.HasRefresh && (info.RefreshExpiresAt.IsZero() || info.RefreshExpiresAt.After(now))
}

// needsRelogin is the shared predicate of the switch-time stale warning
// (docs/RELEASE.md §B) and doctor's credential_stale (§D): the credential cannot
// produce a session again without the tool's interactive login. Either the tool
// invalidated it itself (a tombstone written after a failed refresh), or the
// access token expired with no usable refresh token behind it. A not-datable or
// still-valid credential is false.
//
// What it deliberately no longer assumes: that a refresh *string* means
// recoverable. Refresh tokens now expire in days, so a stored one is routinely
// dead too, and treating its mere presence as recovery silenced the warning
// exactly when the user needed it.
func needsRelogin(info freshness.Info, now time.Time) bool {
	if !info.Known {
		return false
	}
	return info.Invalid || (snapshotExpired(info, now) && !refreshUsable(info, now))
}

// staleCredentialDetail explains why a credential needs a re-login and how to
// recover. Callers only reach it for a needsRelogin credential, so the dated
// branches always have the timestamp they print. The recovery is two steps: the
// tool's own login flow, *then* a re-capture — naming only `kae add --no-login`
// (as both messages used to) sends the user to freeze the same dead credential
// back into the snapshot.
func staleCredentialDetail(info freshness.Info, tool, accountName string) string {
	var reason string
	switch {
	case info.Invalid:
		reason = fmt.Sprintf("%s emptied it after a failed token refresh", tool)
	case info.HasRefresh:
		reason = fmt.Sprintf("it expired %s and its refresh token expired %s",
			utcStamp(info.ExpiresAt), utcStamp(info.RefreshExpiresAt))
	default:
		reason = fmt.Sprintf("it expired %s and has no refresh token", utcStamp(info.ExpiresAt))
	}
	capture := fmt.Sprintf("re-capture with: kae add --no-login %s %s", tool, accountName)
	if login := loginCommand(tool); login != nil {
		return fmt.Sprintf("%s; log in again with: %s, then %s", reason, strings.Join(login, " "), capture)
	}
	return fmt.Sprintf("%s; log in again in %s, then %s", reason, tool, capture)
}

// utcStamp formats a credential timestamp for a human-readable warning.
func utcStamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// recaptureActiveBeforeSwitch refreshes each switched tool's currently-active
// account snapshot from the live store before kae use switches away, so a later
// switch back applies a live token (symmetric with run -s). It rewrites the
// snapshot only when the live store and the snapshot diverge, so a no-op switch
// costs no write; the divergence read is coalesced with the switch's other
// keychain reads (docs/RELEASE.md §A/§C). Best-effort: a logged-out or
// unreadable active account is left untouched with a warning, never aborting
// the switch. Only kae use / bare use reach here — use -i / pin / run -i write
// kae-owned isolation dirs and never the real store.
func (app *App) recaptureActiveBeforeSwitch(ctx context.Context, be secret.Backend, st *state.State, plans []toolPlan) {
	for _, plan := range plans {
		active := st.Active[plan.Tool]
		if active == "" {
			continue // nothing previously active for this tool
		}
		acc, found, err := account.Load(app.Paths.AccountDir(plan.Tool, active))
		if err != nil || !found {
			continue // never captured: nothing to refresh
		}
		// plan.Specs are account-agnostic for a given tool, so they read the live
		// store regardless of which account is the switch target.
		values, anyPresent, err := readLiveValues(ctx, plan.Specs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kae: warning: could not read live %s state to refresh %s/%s: %v\n",
				plan.Tool, plan.Tool, active, err)
			continue
		}
		if !anyPresent {
			fmt.Fprintf(os.Stderr, "kae: warning: %s is logged out; snapshot %s/%s left unchanged\n",
				plan.Tool, plan.Tool, active)
			continue
		}
		if keepSnapshotIdentity(ctx, be, plan.Specs, acc, values) {
			// The live login is someone else's: recapturing here would file that
			// credential under this account's name and identity, and after that no
			// offline check can tell the two apart (see keepSnapshotIdentity).
			fmt.Fprintf(os.Stderr,
				"kae: warning: the live %s identity is not the one kae applied for %s/%s; "+
					"%s was probably logged in again outside kae, so that snapshot is left unchanged\n",
				plan.Tool, plan.Tool, active, plan.Tool)
			fmt.Fprintf(os.Stderr,
				"kae: if that live login is one you want to keep, import it first with: kae add --no-login %s <account>\n",
				plan.Tool)
			continue
		}
		if app.recaptureWouldDowngrade(ctx, be, plan.Tool, acc, values) {
			fmt.Fprintf(os.Stderr,
				"kae: warning: the live %s credential needs a re-login while snapshot %s/%s still holds a usable one; "+
					"snapshot left unchanged\n", plan.Tool, plan.Tool, active)
			continue
		}
		if !valuesDiverge(ctx, be, plan.Specs, acc, values) {
			continue // live already matches the snapshot: skip the write
		}
		// Same tool/driver/specs as the target plan; only the account differs
		// (copy so a future toolPlan field is not silently dropped). Carry the
		// existing snapshot's identity so a recapture refreshes the credential
		// without blanking the §D identity field.
		activePlan := plan
		activePlan.Account = active
		activePlan.Identity = acc.Identity
		if err := app.persistSnapshot(ctx, be, activePlan, values); err != nil {
			fmt.Fprintf(os.Stderr, "kae: warning: recapture of %s/%s failed: %v\n", plan.Tool, active, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "kae: refreshed %s/%s snapshot from the live store before switching away\n",
			plan.Tool, active)
	}
}

// snapshotArtifactDiffers reports whether one live artifact value differs from
// its stored snapshot: a presence change, or — when present — a missing,
// unreadable, or byte-different stored payload. It is the shared core of the
// switch-away recapture decision (valuesDiverge) and the post-login change
// check (loginChangedAuth), which differ only in where the stored side comes
// from (account snapshot vs backup record), how the live value is read, and the
// backend-read error policy. err is the raw backend read error; the caller
// chooses whether to propagate it or treat it as a difference.
func snapshotArtifactDiffers(ctx context.Context, be secret.Backend, storedRef string, storedPresent bool, live artifact.Value) (differs bool, err error) {
	if storedPresent != live.Present {
		return true, nil
	}
	if !live.Present {
		return false, nil
	}
	stored, found, err := be.Get(ctx, storedRef)
	if err != nil {
		return false, err
	}
	return !found || !bytes.Equal(stored, live.Data), nil
}

// keepSnapshotIdentity replaces the freshly-read value of every identity-only
// artifact with what acc's snapshot already holds, so a recapture refreshes the
// credential without touching the identity it recorded.
//
// This matters because claude's /oauthAccount self-heal is TTL-gated (§6): the
// live cache can name a different account than the live credential — exactly the
// state kae fixes — and importing that mismatch here would pin the wrong identity
// onto this account permanently, long after the credential is right. An
// unreadable stored payload degrades to absent, which a later switch treats as
// "remove it and let claude refetch": stale-but-wrong is never kept.
//
// It returns outsideLogin when the live identity *names a different account* than
// this snapshot. Keeping the snapshot's identity is right when the live cache is
// merely stale, but a live identity that changed is the one case where it is
// authoritative: `claude /login` rewrites accountUuid/emailAddress
// unconditionally, so a changed one means the live credential belongs to someone
// else. Overwriting this snapshot's credential with it would file account B's
// token under account A's name *and* A's identity, and nothing offline can
// detect that afterwards — the access token is opaque, so live, snapshot and
// doctor all agree on a label that is simply wrong. The caller refuses to
// recapture at all.
func keepSnapshotIdentity(ctx context.Context, be secret.Backend, specs []artifact.Spec, acc account.Account, values []artifact.Value) (outsideLogin bool) {
	for i, sp := range specs {
		if !sp.IdentityOnly {
			continue
		}
		live := values[i]
		values[i] = artifact.Value{}
		art, ok := acc.Artifacts[sp.Name]
		if !ok || !art.Present {
			continue
		}
		data, found, err := be.Get(ctx, art.SecretRef)
		if err != nil || !found {
			continue
		}
		values[i] = artifact.Value{Data: data, Present: true}
		// An absent live identity says nothing (the tool may not have rebuilt it
		// yet); only a present one that names another account is evidence.
		if live.Present && identityDiffers(sp, data, live.Data) {
			outsideLogin = true
		}
	}
	return outsideLogin
}

// recaptureWouldDowngrade reports whether writing the live values into acc's
// snapshot would replace a credential that still works with one that does not.
//
// readLiveValues proves only that the artifact *exists*: claude's tombstone (the
// blanked payload a failed refresh leaves behind) is a fully-formed one, so it
// passes the logged-out guard and would otherwise overwrite a snapshot that was
// still good — irrecoverably, since the backup taken for this switch reads the
// same dead live store.
//
// One-directional on purpose: it never prefers the newer value, it only refuses
// to destroy a working one. "The live store is authoritative" (docs/RELEASE.md
// §A) still holds for every credential that is actually usable; what is dropped
// is the unstated assumption that a credential which exists is usable.
func (app *App) recaptureWouldDowngrade(ctx context.Context, be secret.Backend, tool string, acc account.Account, values []artifact.Value) bool {
	now := app.Now()
	live := freshness.Info{}
	for _, v := range values {
		if !v.Present {
			continue
		}
		if info := freshnessOf(tool, v.Data); info.Known {
			live = info
			break
		}
	}
	if !needsRelogin(live, now) {
		return false
	}
	stored, err := app.accountFreshness(ctx, be, acc)
	return err == nil && stored.Known && !needsRelogin(stored, now)
}

// valuesDiverge reports whether freshly-read live values differ from acc's
// stored snapshot: a missing artifact record, a presence mismatch, or any
// payload difference. An unreadable stored payload is treated as divergence so
// recapture errs toward refreshing rather than silently keeping a stale token.
func valuesDiverge(ctx context.Context, be secret.Backend, specs []artifact.Spec, acc account.Account, values []artifact.Value) bool {
	for i, sp := range specs {
		art, ok := acc.Artifacts[sp.Name]
		if !ok {
			// An identity-only artifact absent from an older snapshot is not a
			// divergence — it is simply not tracked yet. Counting it as one
			// would recapture (and rewrite) on every single switch.
			if sp.IdentityOnly {
				continue
			}
			return true
		}
		// The recapture path treats any backend-read error as divergence, so it
		// refreshes rather than silently keeping a possibly-stale token.
		if differs, err := snapshotArtifactDiffers(ctx, be, art.SecretRef, art.Present, values[i]); err != nil || differs {
			return true
		}
	}
	return false
}
