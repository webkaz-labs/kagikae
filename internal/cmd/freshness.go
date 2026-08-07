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

// toolIsDatable reports whether tool's adapter can read an expiry out of a
// credential payload at all (adapter.Fresher). Callers use it to skip reads whose
// result is already determined.
func toolIsDatable(tool string) bool {
	ad, err := adapter.ForTool(tool)
	if err != nil {
		return false
	}
	_, ok := ad.(adapter.Fresher)
	return ok
}

// accountFreshness reads acc's stored credential and reports its expiry and
// refresh-token presence. It returns the first artifact whose payload parses as
// the tool's known credential format; a not-datable account (copilot pointer,
// agy blob) yields Known=false. Shared by the switch-time stale warning
// (docs/RELEASE.md §B) and doctor credential-health (§D).
func (app *App) accountFreshness(ctx context.Context, be secret.Backend, acc account.Account) (freshness.Info, error) {
	// A tool with no Fresher (copilot's pointer, agy's opaque blob) can never produce
	// a Known reading, so reading its payloads is guaranteed-wasted work — and on
	// darwin each one is a `security` subprocess, now paid by `kae ls` and
	// `kae status` too. agy declares three file artifacts on Linux; that was three
	// reads per listing for an answer fixed in advance.
	if !toolIsDatable(acc.Tool) {
		return freshness.Info{}, nil
	}
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
// What the deadline is, because getting this wrong cost a release in both
// directions: for claude it is `refreshTokenExpiresAt`, the **login's absolute
// expiry** rather than a rolling window — the access token (`expiresAt`) rolls forward
// on every refresh, this one is set when `/login` runs and stays put, so a credential
// showing two days left really has two days left. The measured lifetime, upstream's
// own warning threshold (which has already changed once, so it is version-qualified),
// and how to re-measure any of it live in one place: the claude row of
// docs/VALIDATION.md § Upstream Behaviour Assumptions.
//
// Seven days, and the number is a judgement rather than a constant kae measured.
// Upstream's own warning window is shorter, and short is right for the account you
// are *using* — you look at that tool every day, so a few days is a few chances to
// act. A kae account that is not the active one is different: nothing shows it to you
// until you run kae or switch to it, which may be twice a week, so a few days can pass
// unseen. One week is the shortest horizon that still contains a working day for
// anyone who does not run kae daily, and against a login lifetime of roughly a month
// it stays silent for most of a credential's life — a window covering most of it would
// make the notice wallpaper.
const reloginLeadTime = 7 * 24 * time.Hour

// snapshotFreshnessWarning returns the switch-time warning acc's snapshot
// credential deserves (docs/RELEASE.md §B): the stale message once it can no
// longer open a session without an interactive re-login, the expiring notice while
// that deadline is still ahead but within reloginLeadTime, and "" when the
// credential is fine, undated, or not-datable.
//
// It returns the state token rather than a "was it stale" bool so the caller reads
// the same vocabulary every other freshness surface uses, and a third band would
// add a case rather than change this signature. Only a credential that cannot log
// in at all makes the switch unusable, and that is the one the caller's roll-up
// line counts; a switch to an account with five days left works today.
func (app *App) snapshotFreshnessWarning(ctx context.Context, be secret.Backend, acc account.Account) (msg, state string, err error) {
	info, err := app.accountFreshness(ctx, be, acc)
	if err != nil {
		return "", "", err
	}
	now := app.Now()
	cred := credentialStateAt(info, now)
	switch cred.State {
	case constants.CredentialStale:
		return "snapshot credential is stale: " + staleCredentialDetail(info, acc.Tool, acc.Name), cred.State, nil
	case constants.CredentialExpiring:
		return "snapshot credential " + expiringCredentialDetail(cred.ReloginBy, now, acc.Tool, acc.Name), cred.State, nil
	}
	return "", cred.State, nil
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
	now := app.Now()
	var wg sync.WaitGroup
	for i, acc := range captured {
		wg.Add(1)
		go func(i int, acc account.Account) {
			defer wg.Done()
			info, err := app.accountFreshness(ctx, be, acc)
			if err != nil {
				return
			}
			states[i] = credentialStateAt(info, now)
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
	// No secret.Cached wrap: it only does anything under a secret.WithReadCache
	// context, which these commands do not install, and credentialStates reads each
	// account's key exactly once regardless — there is nothing to coalesce.
	return app.credentialStates(ctx, be, captured)
}

// credentialStateAt classifies one freshness reading into the three states every
// freshness surface reports. It is the **only** place that decides which of them a
// credential is in: doctor's two checks, the bound-directory sweep, the switch-time
// warning and the inventory column all route through it, so none of them can
// disagree with another about the same credential — the same reason it and
// needsRelogin are both defined on one reloginDeadline. Three copies of this branch
// existed briefly and that is exactly how a boundary drifts.
//
// It reads the deadline once and compares, rather than asking the two predicates
// (which would each re-derive it), and takes now as a parameter so one report
// cannot straddle two clock readings.
//
// The zero credentialState means kae cannot judge: an unparseable payload, or one
// it parses that records no deadline it can trust (codex stores a refresh token
// without publishing its expiry; an api-key-only auth.json has no expiry at all).
// That is never rendered as "ok" — claiming a credential is fine is knowledge kae
// does not have here.
func credentialStateAt(info freshness.Info, now time.Time) credentialState {
	if !info.Known {
		return credentialState{}
	}
	if info.Revoked {
		// A tombstone is past every deadline with no timestamp to name, which is why
		// this state can carry no ReloginBy.
		return credentialState{State: constants.CredentialStale}
	}
	deadline, ok := reloginDeadline(info)
	if !ok {
		return credentialState{}
	}
	switch {
	case !deadline.After(now):
		return credentialState{State: constants.CredentialStale, ReloginBy: deadline}
	case !deadline.After(now.Add(reloginLeadTime)):
		return credentialState{State: constants.CredentialExpiring, ReloginBy: deadline}
	default:
		return credentialState{State: constants.CredentialOK, ReloginBy: deadline}
	}
}

// reloginDeadline returns the instant at which info stops being able to open a
// session without the tool's interactive login, and whether kae can know it.
//
// It is the one place that decides where that line falls, so the "is it past?"
// question (needsRelogin) and the "which band is it in?" question
// (credentialStateAt) cannot disagree about it. Those were separate predicates once
// and a credential expiring exactly on the tick read as usable to one and unusable
// to the other.
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

// staleCredentialDetail explains why a credential needs a re-login and how to
// recover. Callers only reach it for a needsRelogin credential, so the dated
// branches always have the timestamp they print. The recovery is two steps: the
// tool's own login flow, *then* a re-capture — naming only `kae add --no-login`
// (as both messages used to) sends the user to freeze the same dead credential
// back into the snapshot.
func staleCredentialDetail(info freshness.Info, tool, accountName string) string {
	reason := staleCredentialReason(info, tool)
	capture := fmt.Sprintf("re-capture with: kae add --no-login %s %s", tool, accountName)
	if login := loginCommand(tool); login != nil {
		return fmt.Sprintf("%s; log in again with: %s, then %s", reason, strings.Join(login, " "), capture)
	}
	return fmt.Sprintf("%s; log in again in %s, then %s", reason, tool, capture)
}

// staleCredentialReason states why a credential can no longer open a session,
// without naming a remedy. It is split out because the *reason* is a property of
// the credential while the remedy is a property of where it lives: an account
// snapshot is fixed by a login plus a re-capture, and the copy inside a bound
// directory by a login performed in that directory (pinCredentialChecks). Writing
// the three-way explanation twice is how the two would drift apart.
//
// Callers only reach it for a credential that is actually past its deadline, so
// the dated branches always have the timestamp they print.
func staleCredentialReason(info freshness.Info, tool string) string {
	switch {
	case info.Revoked:
		return fmt.Sprintf("%s emptied it after a failed token refresh", tool)
	case info.HasRefresh:
		return fmt.Sprintf("it expired %s and its refresh token expired %s",
			utcStamp(info.ExpiresAt), utcStamp(info.RefreshExpiresAt))
	default:
		return fmt.Sprintf("it expired %s and has no refresh token", utcStamp(info.ExpiresAt))
	}
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
// backupID names the backup this switch already took of the live state; it is what
// makes a refusal non-destructive, so the caller must pass the real one.
func (app *App) recaptureActiveBeforeSwitch(ctx context.Context, be secret.Backend, st *state.State,
	plans []toolPlan, backupID string,
) {
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
		if why := keepSnapshotIdentity(ctx, be, plan.Specs, plan.Tool, active, acc, values); why != "" {
			// Recapturing here would file a credential kae cannot attribute under this
			// account's name and identity, and after that no offline check can tell the
			// two apart (see keepSnapshotIdentity).
			//
			// backupID is this switch's own backup, and naming it is only honest because
			// of where it sits: createBackup runs *before* this recapture and before
			// applySnapshot, so it holds exactly the live copy being declined. `run -s`
			// has to create one of its own for the same sentence to be true there.
			warnRecaptureIdentityUnconfirmed(plan.Tool, why, backupID)
			continue
		}
		if why := app.recaptureWouldDowngrade(ctx, be, plan.Tool, active, acc, values); why != "" {
			fmt.Fprintf(os.Stderr, "kae: warning: %s; snapshot left unchanged\n", why)
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

// warnRecaptureIdentityUnconfirmed reports a recapture declined because kae could
// not establish whose login is live, for both paths that owe that guard: the
// switch-away recapture and `run -s`'s own. One copy, because the two differ only in
// the reason keepSnapshotIdentity built and would otherwise drift apart in the half
// that matters — where the declined copy went.
//
// **Refusing here destroys the copy unless something else holds it**, which is the
// asymmetry that makes this guard different from the two siblings it shares a
// predicate with (dirIdentityConfirms, liveLoginMatchesBackup): for those, refusing
// means declining to overwrite or delete, so refusal is the conservative answer. Here
// the caller goes on to overwrite the live store from a backup, so a refusal that
// names nowhere is a logout reported as success — measured on `run -s`, where the only
// backup predated the child. So backupID is not decoration: it is the whole reason the
// refusal is safe, and a caller that cannot name one must say so rather than imply the
// copy survives. An earlier version told the user to "import it first", naming a
// moment that does not exist inside a single non-interactive command.
func warnRecaptureIdentityUnconfirmed(tool, why, backupID string) {
	fmt.Fprintf(os.Stderr, "kae: warning: %s, so that snapshot is left unchanged\n", why)
	if backupID == "" {
		fmt.Fprintf(os.Stderr,
			"kae: kae could not preserve the live %s login it declined to adopt; it is lost once the "+
				"previous state is restored\n", tool)
		return
	}
	fmt.Fprintf(os.Stderr,
		"kae: the live %s login kae declined to adopt is preserved only in backup %s — to keep it as "+
			"its own account: kae rollback --to %s, then kae add --no-login %s <account>\n",
		tool, backupID, backupID, tool)
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
// It returns a non-empty reason when the caller must not recapture at all, framed
// around accountName rather than acc.Name for the reason recaptureWouldDowngrade
// gives. Two shapes, and they are deliberately not one:
//
// (1) **The live identity names a different account.** Keeping the snapshot's
// identity is right when the live cache is merely stale, but a live identity that
// changed is the one case where it is authoritative: `claude /login` rewrites
// accountUuid/emailAddress unconditionally, so a changed one means the live
// credential belongs to someone else. Overwriting this snapshot's credential with
// it would file account B's token under account A's name *and* A's identity, and
// nothing offline can detect that afterwards — the access token is opaque, so live,
// snapshot and doctor all agree on a label that is simply wrong.
//
// (2) **The two differ and kae cannot read them as records** (identityComparable).
// Same consequence as (1) — the caller declines — and only the claim is weaker, because
// kae has observed a change and not an account. That is the whole of the difference:
// do not read this as a safeguard that (1) has and (2) lacks. Its two sibling guards
// (dirIdentityConfirms, liveLoginMatchesBackup) apply the same gate to the same
// predicate, and the ordering matters here in the opposite direction from theirs — see
// the comment at the switch, which is normative for why the gate may not decide.
//
// Only the **first** reason is kept, so a spec yielding (1) after one yielding (2) is
// reported as (2). Under-claiming, and unobservable today: claude declares the only
// IdentityOnly spec there is, and exactly one (measured — a mutation swapping first-wins
// for last-wins cannot be killed).
//
// accountName is used rather than acc.Name deliberately (recaptureWouldDowngrade says
// why), and that choice is **not** observable either: no fixture builds a snapshot whose
// recorded name disagrees with the name it was resolved under.
//
// The loop finishes either way, so the value substitution above is complete for a
// caller that reads values regardless of the reason. Today none does.
func keepSnapshotIdentity(ctx context.Context, be secret.Backend, specs []artifact.Spec,
	tool, accountName string, acc account.Account, values []artifact.Value,
) string {
	reason := ""
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
		// yet); only a present one is evidence of anything.
		if !live.Present || reason != "" {
			continue
		}
		// The gate decides the **wording**, never the decision. Getting that backwards
		// shipped a logout: refusing on "kae cannot compare" also refused the shape kae
		// itself produces — applySnapshot writes a recorded non-record into the live
		// cache, so both sides are that same non-record, byte for byte — and on `run -s`
		// the refusal then discarded the child's refreshed credential. No login can
		// produce that shape, because `/login` rewrites accountUuid/emailAddress
		// unconditionally (docs/VALIDATION.md, claude assumptions), so the pair kae
		// cannot read *and* that are identical is evidence that nothing happened.
		// docs/ROADMAP.md carries the measurement and why its own earlier prescription
		// was withdrawn.
		if !identityDiffers(sp, data, live.Data) {
			continue
		}
		if !identityComparable(data, live.Data) {
			reason = fmt.Sprintf(
				"kae cannot read the %s identity records it would compare for %s/%s, so it cannot tell "+
					"whose login is live", tool, tool, accountName,
			)
			continue
		}
		reason = fmt.Sprintf(
			"the live %s identity is not the one kae applied for %s/%s; %s was probably logged in "+
				"again outside kae", tool, tool, accountName, tool,
		)
	}
	return reason
}

// supersedes reports whether copy a of one account's credential is provably the
// later of the two — which, for a tool whose refresh token rotates single-use
// (rotatesSingleUse), means b can no longer refresh at all, so writing b over a is
// a logout rather than a regression.
//
// `expiresAt` and nothing else orders them: a successful refresh always moves it
// forward, a failed one tombstones the copy, and a fresh login sorts ahead of an
// older chain because it sets the field to now plus the access token's life. What
// the field cannot do is compare two *different* accounts, so every caller owes an
// attribution guard of its own before acting on the answer (the harvest's is
// dirIdentityConfirms, a backup restore's is liveLoginMatchesBackup, and the
// two recaptures' is keepSnapshotIdentity, applied by each caller).
//
// The a-side guard is `orderable`, the one docs/ADAPTERS.md prescribes. b degrading to
// the zero cutoff is deliberate and *not* the same test: a copy kae cannot order has no
// deadline worth comparing and loses to any copy that has one.
func supersedes(a, b freshness.Info) bool {
	if !orderable(a) {
		return false
	}
	cutoff := time.Time{}
	if b.Known && !b.Revoked {
		cutoff = b.ExpiresAt
	}
	return a.ExpiresAt.After(cutoff)
}

// orderable reports whether a freshness reading can take part in an ordering at all:
// known to the tool's parser, not a tombstone, and carrying an actual deadline. It is
// docs/ADAPTERS.md's `Known && !Revoked && !ExpiresAt.IsZero()`, named because
// `supersedes` is not the only place that needs it — a caller asking "is the copy I am
// about to write older than the live one" must apply the same test to *its* side, and
// getting that subset wrong is how a copy with no deadline came to read as superseded
// by anything, and it shipped that way once.
//
// All three conditions are load-bearing together and none is redundant, which is easy
// to misjudge from one tool: claude sets `Known` on the mere *presence* of `expiresAt`
// and parses a non-numeric one to the zero time, so a payload whose `expiresAt` changed
// type upstream — the shape an upstream format change actually takes — is `Known`,
// un-`Revoked` and undated at once.
//
// What it does **not** and cannot check is that the deadline is *meaningful*. The zero
// value is the only implausible one it can name; a small positive number — what the same
// field would hold if upstream moved from an absolute epoch to a relative duration —
// passes here and orders arbitrarily. That is a unit assumption, not a predicate, so it
// lives where assumptions are re-measured (docs/VALIDATION.md § Upstream Behaviour
// Assumptions, the claude row on `expiresAt`) rather than as a guessed floor here.
func orderable(info freshness.Info) bool {
	return info.Known && !info.Revoked && !info.ExpiresAt.IsZero()
}

// liveValuesFreshness reads the credential freshness out of freshly-read live
// values: the first artifact whose payload parses as tool's credential format,
// which is the rule accountFreshness applies to the snapshot side.
func liveValuesFreshness(tool string, values []artifact.Value) freshness.Info {
	for _, v := range values {
		if !v.Present {
			continue
		}
		if info := freshnessOf(tool, v.Data); info.Known {
			return info
		}
	}
	return freshness.Info{}
}

// recaptureWouldDowngrade reports why writing the live values into acc's snapshot
// would leave that account worse off than leaving it alone, or "" when the write
// may proceed. The caller frames the reason into its warning.
//
// Two refusals, decided together because they compare the same two readings — and
// on darwin the snapshot side is a `security` subprocess, so asking twice is the
// cost the switch's read coalescing exists to avoid (docs/RELEASE.md §A/§C).
//
// (1) **The live copy needs a re-login and the snapshot's does not.**
// readLiveValues proves only that the artifact *exists*: claude's tombstone (the
// blanked payload a failed refresh leaves behind) is a fully-formed one, so it
// passes the logged-out guard and would otherwise overwrite a snapshot that was
// still good — irrecoverably, since the backup taken for this switch reads the
// same dead live store.
//
// (2) **The snapshot already holds a later copy of this account's credential.**
// Both copies are usable there, so (1) cannot see it: they differ only in order,
// and for a tool whose refresh token rotates single-use the later one is the only
// one that can still refresh (supersedes). A live copy *older* than the snapshot is
// not something normal operation produces — kae applies the snapshot and the tool
// refreshes forward from it — it is what `kae rollback` leaves behind by design, so
// without this guard the next `kae use` of that account launders the rolled-back
// copy into the snapshot and destroys the last one that works.
//
// One-directional like (1): it never prefers the live copy, it only refuses to
// overwrite a later one. "The live store is authoritative" (docs/RELEASE.md §A)
// still holds for every credential that is actually the newest; what is dropped is
// the unstated assumption that whatever is live is the newest.
// accountName names the account in the reason rather than acc.Name: account.toml's
// recorded name and the name the caller resolved the snapshot under are not
// guaranteed to agree, and it is the caller's name the user typed.
func (app *App) recaptureWouldDowngrade(ctx context.Context, be secret.Backend,
	tool, accountName string, acc account.Account, values []artifact.Value,
) string {
	now := app.Now()
	live := liveValuesFreshness(tool, values)
	liveNeedsRelogin := needsRelogin(live, now)
	ordered := rotatesSingleUse(tool)
	if !liveNeedsRelogin && !ordered {
		return ""
	}
	stored, err := app.accountFreshness(ctx, be, acc)
	if err != nil || !stored.Known {
		return ""
	}
	if liveNeedsRelogin && !needsRelogin(stored, now) {
		return fmt.Sprintf(
			"the live %s credential needs a re-login while snapshot %s/%s still holds a usable one",
			tool, tool, accountName,
		)
	}
	if ordered && supersedes(stored, live) {
		return fmt.Sprintf(
			"snapshot %s/%s holds a later %s credential than the live store, and %s's refresh token "+
				"rotates single-use, so the live copy can no longer refresh",
			tool, accountName, tool, tool,
		)
	}
	return ""
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
