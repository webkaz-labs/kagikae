package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
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

// reloginLeadTime is how far ahead of the deadline kae reports that a credential
// will need an interactive re-login (doctor's credential_expiring, and the
// switch-time notice).
//
// Seven days, and the number is a judgement rather than a constant kae measured.
// Claude Code warns at three, which is enough for the account you are *using* —
// you look at that tool every day, so three days is three chances to act. A kae
// account that is not the active one is different: nothing shows it to you until
// you run kae or switch to it, which may be twice a week, so three days can pass
// unseen. One week is the shortest horizon that still contains a working day for
// anyone who does not run kae daily.
//
// It is deliberately not longer. Against the ~30-day refresh-token lifetime these
// credentials have, seven days keeps the notice silent for roughly three quarters
// of the credential's life, so when it does appear it reads as "act now" — the
// same reasoning that keeps cursor out of upstream_version and puts the
// assumption-age threshold at six months. A two-week window would warn for half
// of every credential's life, which is how a warning becomes wallpaper.
const reloginLeadTime = 7 * 24 * time.Hour

// snapshotFreshnessWarning returns the switch-time warning acc's snapshot
// credential deserves (docs/RELEASE.md §B): the stale message once it can no
// longer open a session without an interactive re-login, the expiring notice
// while that deadline is still ahead but within reloginLeadTime, and "" when the
// credential is fine, undated, or not-datable.
//
// stale reports which of the two it is. Only a credential that cannot log in at
// all makes the switch unusable, and that is the one the caller's roll-up line
// counts; a switch to an account with five days left works today.
//
// The snapshot is read once for both questions, because doctor's backend is not
// the switch path's cached one and a second read there would be a second
// `security` invocation per account.
func (app *App) snapshotFreshnessWarning(ctx context.Context, be secret.Backend, acc account.Account) (msg string, stale bool, err error) {
	info, err := app.accountFreshness(ctx, be, acc)
	if err != nil {
		return "", false, err
	}
	now := app.Now()
	if needsRelogin(info, now) {
		return "snapshot credential is stale: " + staleCredentialDetail(info, acc.Tool, acc.Name), true, nil
	}
	if deadline, ok := reloginDueWithin(info, now, reloginLeadTime); ok {
		return "snapshot credential " + expiringCredentialDetail(deadline, now, acc.Tool, acc.Name), false, nil
	}
	return "", false, nil
}

// credentialState is one account's snapshot freshness as the inventory commands
// report it: a state token from the JSON vocabulary plus, when kae knows it, the
// deadline that state was decided against. The zero value means kae could not
// judge (an opaque payload, an unreadable backend), and every consumer renders
// that as "unknown" rather than as "fine".
type credentialState struct {
	State     string
	ReloginBy time.Time
}

// credentialStates reads the snapshot freshness of every captured account, for the
// inventory commands (`kae ls`, `kae accounts`, `kae status`). Accounts kae cannot
// judge are simply absent from the map.
//
// It exists because the freshness a user needs — "which of my accounts needs
// attention" — was only reachable through `kae doctor`, while the commands that
// list the accounts showed none of it.
//
// The payload is the single source of truth, deliberately: an expiry copied into
// account.toml at capture time would be a second record of the same fact, and this
// repo has paid for that twice (a snapshot's recorded keychain account overriding
// the adapter's, a pin registry drifting from the store tree). A recapture path
// that forgot to refresh the copy would make `kae ls` report a healthy account
// that is dead. The snapshot bytes only change when kae rewrites them, so reading
// them is exactly as accurate and cannot go out of step.
//
// The reads run concurrently. On darwin each one is a `security` invocation, so a
// sequential loop would put the sum of them in front of the most-run commands —
// the same reason detectTools fans out. A backend error for one account leaves
// that account unjudged instead of failing the listing: `kae ls` answers from
// metadata and must keep working when the secret store does not.
func (app *App) credentialStates(ctx context.Context, be secret.Backend, captured []account.Account) map[toolAccount]credentialState {
	states := make([]credentialState, len(captured))
	var wg sync.WaitGroup
	for i, acc := range captured {
		wg.Add(1)
		go func(i int, acc account.Account) {
			defer wg.Done()
			info, err := app.accountFreshness(ctx, be, acc)
			if err != nil {
				return
			}
			states[i] = app.credentialStateOf(info)
		}(i, acc)
	}
	wg.Wait()
	out := map[toolAccount]credentialState{}
	for i, acc := range captured {
		if states[i].State != "" {
			out[toolAccount{acc.Tool, acc.Name}] = states[i]
		}
	}
	return out
}

// capturedCredentialStates is what the read-only inventory commands call: it
// resolves the secret backend itself and reports nothing at all when that fails.
//
// Best-effort is the contract here, not a shortcut. `kae ls`, `kae accounts` and
// `kae status` answer from metadata and must keep listing the inventory when the
// secret store is unavailable — turning a freshness column into a reason those
// commands exit non-zero would be a regression in the commands a user reaches for
// when something is already wrong.
func (app *App) capturedCredentialStates(ctx context.Context, captured []account.Account) map[toolAccount]credentialState {
	be, err := app.secretBackend()
	if err != nil {
		return nil
	}
	return app.credentialStates(ctx, secret.Cached(be), captured)
}

// credentialStateOf classifies one freshness reading into the three states the
// inventory commands report, built from the same two predicates doctor uses so a
// row can never disagree with the check about the same account.
func (app *App) credentialStateOf(info freshness.Info) credentialState {
	now := app.Now()
	switch deadline, soon := reloginDueWithin(info, now, reloginLeadTime); {
	case needsRelogin(info, now):
		// A tombstone is past every deadline with no timestamp to name, so this state
		// deliberately carries no ReloginBy.
		if d, ok := reloginDeadline(info); ok {
			return credentialState{State: constants.CredentialStale, ReloginBy: d}
		}
		return credentialState{State: constants.CredentialStale}
	case soon:
		return credentialState{State: constants.CredentialExpiring, ReloginBy: deadline}
	case info.Known:
		// Known and not due: "ok" only where kae actually has a deadline it is ahead
		// of. A payload kae parses but that records no usable deadline (codex's
		// refresh token with no published expiry, an api-key-only auth.json) is left
		// unjudged — reporting it "ok" would claim knowledge kae does not have.
		if d, ok := reloginDeadline(info); ok {
			return credentialState{State: constants.CredentialOK, ReloginBy: d}
		}
	}
	return credentialState{}
}

// reloginDeadline returns the instant at which info stops being able to open a
// session without the tool's interactive login, and whether kae can know it.
//
// It is the one place that decides where that line falls, so the "is it past?"
// question (needsRelogin) and the "how long until it is?" question
// (reloginDueWithin) cannot disagree about it. They once were separate predicates
// and a credential expiring exactly on the tick read as usable to one and
// unusable to the other.
//
// ok is false in three cases, and all three mean *unknown*, never "never":
//   - a payload kae cannot parse (Known=false),
//   - a tombstone (Revoked) — already past every deadline, with no timestamp to
//     name, which is why needsRelogin answers that case before asking here,
//   - a refresh token whose own expiry the payload does not publish
//     (RefreshExpiresAt zero with HasRefresh). Only claude publishes one; codex
//     and opencode do not, so for them the deadline is genuinely unknowable and
//     kae says nothing rather than guessing that the access-token expiry is it.
//     Reading that zero as "never expires" is the mistake this comment exists for.
//   - a known format that records no access-token expiry at all (an api-key-only
//     codex auth.json). That is "no expiry", so nothing here can move it: the
//     refresh expiry must not be promoted into a deadline the access token does
//     not have.
//
// The refresh expiry extends the deadline only when a refresh token is actually
// there. HasRefresh and RefreshExpiresAt are read from the payload independently
// — claude takes one from `refreshToken` and the other from
// `refreshTokenExpiresAt` — so a blanked refresh token can sit next to a leftover
// future expiry, and folding that in ungated made an expired credential with no
// way to recover read as good for another month. That is the exact false negative
// this whole file exists to prevent, so the gate is not defensive.
//
// Where a refresh token *is* present the deadline is the *later* of the two: one
// that died before the access token it backs still leaves the access token good
// until its own expiry.
func reloginDeadline(info freshness.Info) (time.Time, bool) {
	if !info.Known || info.Revoked {
		return time.Time{}, false
	}
	if info.HasRefresh && info.RefreshExpiresAt.IsZero() {
		return time.Time{}, false
	}
	if info.ExpiresAt.IsZero() {
		return time.Time{}, false
	}
	deadline := info.ExpiresAt
	if info.HasRefresh && info.RefreshExpiresAt.After(deadline) {
		deadline = info.RefreshExpiresAt
	}
	return deadline, true
}

// needsRelogin is the shared predicate of the switch-time stale warning
// (docs/RELEASE.md §B) and doctor's credential_stale (§D): the credential cannot
// produce a session again without the tool's interactive login. Either the tool
// revoked it itself (a tombstone written after a failed refresh), or its
// re-login deadline has passed. A not-datable or still-valid credential is false.
//
// What it deliberately does not assume: that a refresh *string* means
// recoverable. Refresh tokens now expire in days, so a stored one is routinely
// dead too, and treating its mere presence as recovery silenced the warning
// exactly when the user needed it. That, and the treatment of an unpublished
// refresh expiry, are reloginDeadline's business.
//
// A deadline landing exactly on now counts as passed.
func needsRelogin(info freshness.Info, now time.Time) bool {
	if !info.Known {
		return false
	}
	if info.Revoked {
		return true
	}
	deadline, ok := reloginDeadline(info)
	return ok && !deadline.After(now)
}

// reloginDueWithin reports the re-login deadline when it is still ahead of now
// but no further away than lead — the lead-time band that gives a human time to
// re-login before the credential stops working. ok is false for a credential
// that is already past its deadline (needsRelogin's case, a different message),
// one with more than lead to go, and one whose deadline kae cannot know.
func reloginDueWithin(info freshness.Info, now time.Time, lead time.Duration) (time.Time, bool) {
	deadline, ok := reloginDeadline(info)
	if !ok || !deadline.After(now) || deadline.After(now.Add(lead)) {
		return time.Time{}, false
	}
	return deadline, true
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
	case info.Revoked:
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

// expiringCredentialDetail explains that a credential is still good but will need
// an interactive re-login soon, and names the one command that refreshes it.
//
// `kae add --restore` rather than the two steps the stale message names, because
// this credential still works and that is what makes the difference: --restore
// backs up the login that is live right now, runs the tool's own login flow for
// this account, captures it, and puts the previous login back — so the account
// that needs attention is refreshed without disturbing whichever one you are
// currently using. That is the whole point of warning ahead of the deadline
// instead of after it. A tool kae cannot drive a login for (agy) falls back to
// naming the manual pair.
func expiringCredentialDetail(deadline, now time.Time, tool, accountName string) string {
	when := fmt.Sprintf("needs an interactive re-login in %s (%s)",
		roundDays(deadline.Sub(now)), utcStamp(deadline))
	if loginCommand(tool) != nil {
		return fmt.Sprintf("%s; refresh it now without disturbing the active login: kae add --restore %s %s",
			when, tool, accountName)
	}
	return fmt.Sprintf("%s; log in again in %s, then re-capture with: kae add --no-login %s %s",
		when, tool, tool, accountName)
}

// roundDays renders a lead time the way a human reads a deadline. Under a day it
// says hours, because "in 0 days" is worse than useless on the last day; a
// fraction of a day is rounded down so the number never overstates the time left.
func roundDays(d time.Duration) string {
	if days := int(d / (24 * time.Hour)); days >= 1 {
		return fmt.Sprintf("%d day(s)", days)
	}
	hours := int(d / time.Hour)
	if hours < 1 {
		return "under an hour"
	}
	return fmt.Sprintf("%d hour(s)", hours)
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
