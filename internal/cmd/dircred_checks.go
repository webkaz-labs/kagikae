package cmd

// The pin doctor checks over bound directory stores, and the boundDirStore
// enumeration they run on.

import (
	"context"
	"fmt"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// pinCredentialChecks reports the credential of every bound directory that can no
// longer open a session there, or is within the lead time of that point.
//
// It closes a blind spot that had no signal at all: `credential_stale` reads
// account snapshots, and a bound directory does not use one. It holds its own copy
// of the credential, and the tool refreshes *that* copy in place — so a bound
// directory's login can die while every account snapshot kae has looks fine, and
// nothing said so until the tool refused to start in that directory.
//
// What counts as bound comes from boundDirStores, which owns that gate for every
// report of this shape.
//
// It reads live, unlike the snapshot half: up to one store read per bound
// directory per tool that has a credential kae materializes (claude and codex
// only, so the fan-out is small). On darwin a claude store read is one
// attributes-plus-payload `security` call, the same call Detect already makes for
// the global item.
//
// Deliberately not paired with a recapture into the account snapshot, though no
// longer for the reason this comment used to give. It argued that several
// directories binding one account leave no non-arbitrary answer to which copy the
// single snapshot should take; `expiresAt` is that answer (harvestDirCredential),
// so the objection was wrong. What remains is that doctor is a read-only report
// and harvesting belongs where a copy is about to be destroyed, which is the write
// path. Telling the user is this function's job.
func (app *App) pinCredentialChecks(ctx context.Context, stores []boundDirStore) []adapter.Check {
	checks := []adapter.Check{}
	now := app.Now()
	// One finding per credential, not per binding. Since an account's credential is
	// one store that every directory bound to it reads, N worktrees on one account
	// used to produce N identical findings — and each said "the credential bound to
	// <dir>", which reads as N copies with N separate problems. The first bound
	// directory carries the remedy, and it is the right one for all of them: a
	// `kae relogin` there refreshes the copy the others read.
	//
	// Keyed on the location rather than on the account, so a directory still holding
	// its own pre-split copy is reported on its own — it is a different credential.
	reported := map[string]bool{}
	for _, bound := range stores {
		where := bound.Tool + "\x00" + bound.dirs().credDirOrConfig()
		if reported[where] {
			continue
		}
		info, ok := app.dirCredentialFreshness(ctx, bound.store())
		if !ok {
			continue
		}
		// Marked **after** the probe, never before: a store whose freshness kae cannot
		// resolve has said nothing about that credential, and marking it there would
		// suppress the finding for every sibling that reads the same one.
		reported[where] = true
		switch cred := credentialStateAt(info, now); cred.State {
		case constants.CredentialStale:
			checks = append(checks, adapter.Check{
				Tool: bound.Tool, Code: constants.CheckCredentialStale,
				Status: constants.StatusWarn,
				Message: fmt.Sprintf("the %s credential bound to %s is stale: %s; %s",
					bound.Tool, bound.Dir, staleCredentialReason(info, bound.Tool),
					pinLoginRemedy(bound.Tool, bound.Dir)),
			})
		case constants.CredentialExpiring:
			checks = append(checks, adapter.Check{
				Tool: bound.Tool, Code: constants.CheckCredentialExpiring,
				Status: constants.StatusWarn,
				Message: fmt.Sprintf("the %s credential bound to %s needs an interactive re-login in %s (%s); %s",
					bound.Tool, bound.Dir, roundDays(cred.ReloginBy.Sub(now)), utcStamp(cred.ReloginBy),
					pinLoginRemedy(bound.Tool, bound.Dir)),
			})
		}
	}
	return checks
}

// pinSupersededChecks reports a bound directory whose credential another copy of
// the *same* account has provably overtaken.
//
// It is the one failure in this design with no other signal at all. kae keeps
// copies with lazy sync while claude's refresh token rotates single-use, so of all
// the copies of one account's credential only the one that refreshed last can
// still refresh — and the freshness surfaces cannot see that, because they judge by
// `refreshTokenExpiresAt`, the one field an invalidation does not move. A bound
// directory whose copy was overtaken hours ago reports `ok` everywhere and then
// fails inside the tool, which is why "I used claude in the other worktree and this
// one logged out later" had no visible cause (docs/ROADMAP.md § Every credential
// copy).
//
// **Only what it can prove.** It compares copies kae can order and attribute, and
// says nothing about the rest — this is the area v0.15.0/v0.15.1 got wrong in both
// directions, and a warning that fires on a healthy binding is worth less than no
// warning at all. What that means concretely:
//
//   - The loser must be `orderable`, which is **stricter than what supersedes asks
//     of its b side**, and deliberately so. supersedes lets an un-orderable b lose to
//     anything because its caller is asking "may I overwrite this?", where a copy with
//     no comparable deadline is nothing to lose. The question here is the opposite —
//     "may I tell the user this copy is dead?" — and a copy kae cannot order is one it
//     cannot judge. Do not fold the two: taking supersedes' subset here would report
//     every undated or unparseable store as superseded by anything.
//   - Both sides must be attributed to the account (dirIdentityConfirms). Ordering
//     never establishes *whose* login two copies are, and a `-s` store legitimately
//     holds a previous account's credential — so without this the check reports one
//     account's copy as having overtaken another's.
//   - A tombstoned or unreadable store is left to `credential_stale`, which already
//     names it; reporting one problem as two is the thing pin_stale's silence rules
//     exist to avoid.
//
// Cost is paid in that order: the live reads first (one per bound store, the same
// call pinCredentialChecks makes), the snapshot once per account, and the adapter
// resolution plus identity reads **only** for a finding that is otherwise ready. A
// healthy machine pays no attribution at all — and since attribution for the account's
// own credential store walks every bound directory on the machine, "otherwise ready"
// is load-bearing rather than a nicety: it is what the winner-side guard is asked
// inside the loop for.
//
// ponytail: reads each bound store's credential a second time — pinCredentialChecks
// read the same bytes for a different question moments earlier. Hoisting one read
// per store into buildDoctor and passing it to both is the fix, and it is the same
// move that already put boundDirStores there; it is recorded rather than taken
// because it edits a check that is answering correctly, for a few `security` calls
// on a machine with a handful of bound directories.
func (app *App) pinSupersededChecks(ctx context.Context, be secret.Backend, stores []boundDirStore) []adapter.Check {
	// Every store in one group compares against the **same** account's recorded
	// identity, so attribution reads one key once per losing store and again for the
	// winner on each of those iterations. Measured on three directories bound to one
	// account: four reads of one ref, which on darwin is four `security` calls. The
	// wrap is the one pinIdentityChecks already applies, and safe for the same reason
	// it states there — this path only ever calls Get, and the capability secret.Cached
	// does not forward is Enumerator, which nothing here uses.
	//
	// A healthy machine still pays nothing: the reads happen only behind an ordering
	// finding. This is the cost on exactly the path the check exists to diagnose.
	ctx = secret.WithReadCache(ctx)
	be = secret.Cached(be)
	checks := []adapter.Check{}
	for _, group := range groupBoundStoresByAccount(stores) {
		checks = append(checks, app.supersededChecksFor(ctx, be, group)...)
	}
	return checks
}

// accountStores is every bound store of one (tool, account) pair — the set whose
// copies are copies *of each other*, which is what makes ordering them meaningful.
type accountStores struct {
	Tool    string
	Account string
	Stores  []boundDirStore
}

// groupBoundStoresByAccount groups a boundDirStores walk by the account each store
// holds, preserving that walk's order so a JSON report cannot reorder with a map
// iteration.
func groupBoundStoresByAccount(stores []boundDirStore) []accountStores {
	groups := []accountStores{}
	index := map[string]int{}
	for _, store := range stores {
		key := store.Tool + "/" + store.Account
		if at, ok := index[key]; ok {
			groups[at].Stores = append(groups[at].Stores, store)
			continue
		}
		index[key] = len(groups)
		groups = append(groups, accountStores{Tool: store.Tool, Account: store.Account, Stores: []boundDirStore{store}})
	}
	return groups
}

// supersededChecksFor answers the question for one account: which of its copies
// refreshed last, and which bound directories that leaves behind.
func (app *App) supersededChecksFor(ctx context.Context, be secret.Backend, group accountStores) []adapter.Check {
	// The pure gates first, the same order harvestSupersededDirCredentials uses:
	// resolving specs is not free, so a group this check can never speak about must
	// cost nothing. rotatesSingleUse is the whole premise — where older copies stay
	// usable, being overtaken is not a problem to report.
	//
	// **That gate is load-bearing only by coincidence today, which is why it must not be
	// removed as dead.** Measured 2026-08-06: with two codex-bound directories holding
	// orderable copies seven hours apart, dropping rotatesSingleUse still reports
	// nothing — but not because of this line. It is silenced by the loser-side
	// attribution, which fails because **claude is the only adapter that declares an
	// IdentityOnly artifact**, so storeHoldsAccount can never confirm a codex store. The
	// day a second tool declares one, this gate is the only thing between codex and a
	// finding whose message would read "codex's refresh token rotates single-use".
	artName := credentialArtifactName(group.Tool)
	if !rotatesSingleUse(group.Tool) || artName == "" {
		return nil
	}
	// The account record and its payload are separate needs, and separate failures.
	// The payload is one candidate in the ordering; the record is what *attribution*
	// compares a store's identity cache against, and without it dirIdentityConfirms
	// refuses everything — so taking snapshotCredential's zeroed account on error made
	// the whole store-vs-store comparison unreachable, which its comment then claimed
	// was still happening.
	acc, snapshot, _, err := app.snapshotCredential(ctx, be, group.Tool, group.Account, artName)
	if err != nil {
		snapshot = nil
		if loaded, found, lerr := account.Load(app.Paths.AccountDir(group.Tool, group.Account)); lerr == nil && found {
			acc = loaded
		} else {
			acc = account.Account{}
		}
	}
	// The newest copy kae can order, starting from the account's own snapshot. Ties
	// keep the earlier candidate because supersedes is a strict comparison, so a bind
	// that just copied the snapshot into a store reports nothing.
	newest := freshnessOf(group.Tool, snapshot)
	newestIdx := -1 // the snapshot; an index into group.Stores once a store wins
	if !orderable(newest) {
		newest = freshness.Info{}
	}
	// One read per **credential**, not per binding: since the split every member of a
	// group shares one store, so N worktrees on one account asked the keychain for the
	// same item N times — the case this whole feature exists for. pinCredentialChecks
	// already dedupes the same way (`reported`); this loop did not.
	//
	// Deliberately a memo over the *read* only, leaving the ordering below untouched:
	// a dedup that also skipped the comparison would be a control-flow change in a
	// cleanup pass, which is how this repo has twice put a correctness defect into one.
	live := make([]freshness.Info, len(group.Stores))
	// One entry per credential. No second map for "the read failed": that answer is
	// already in the value, because dirCredentialFreshness returns the zero Info on
	// every failure and !orderable(zero) is the same `continue`.
	//
	// The **key** is deliberately unkillable, and that is the property rather than a
	// gap: changing its granularity (to store.Dir, say) changes only how many reads
	// happen, never what any of them returns — measured 2026-08-07, and it is what
	// makes this a memo rather than a dedup with an opinion.
	seen := map[string]freshness.Info{}
	freshnessOf := func(store boundDirStore) freshness.Info {
		where := store.dirs().credDirOrConfig()
		if info, cached := seen[where]; cached {
			return info
		}
		info, _ := app.dirCredentialFreshness(ctx, store.store())
		seen[where] = info
		return info
	}
	// Attribution is a property of the **credential**, not of the handle asked, so it is
	// memoized on the same key and it is unkillable for the same reason: a shared store's
	// answer comes from its readers and cannot vary between two handles on it, and a
	// per-directory store is the only handle on its own key. What differs from the read
	// above is the cost — the reader walk visits every bound directory on the machine —
	// which is why the loser loop asks through this rather than directly.
	attributed := map[string]bool{}
	holdsAccount := func(store boundDirStore) bool {
		where := store.dirs().credDirOrConfig()
		if ok, cached := attributed[where]; cached {
			return ok
		}
		ok := app.storeHoldsAccount(ctx, be, acc, store)
		attributed[where] = ok
		return ok
	}
	for i, store := range group.Stores {
		info := freshnessOf(store)
		if !orderable(info) {
			continue // nothing kae can place in the ordering; see the doc comment
		}
		live[i] = info
		if supersedes(info, newest) {
			newest, newestIdx = info, i
		}
	}
	if !orderable(newest) {
		return nil
	}
	// Derived once from the winner rather than carried alongside it: where the newest
	// copy is says nothing the index does not, and a third value updated at each
	// assignment site is a third chance to update two of them.
	newestAt := fmt.Sprintf("snapshot %s/%s", group.Tool, group.Account)
	if newestIdx >= 0 {
		newestAt = "the store bound to " + group.Stores[newestIdx].Dir
	}
	checks := []adapter.Check{}
	for i, store := range group.Stores {
		// The index, not a comparison of Dir strings: the winner *is* this element when
		// it is one, and matching on a field relies on an invariant two files away
		// (one breadcrumb per pin-id) to keep two entries from sharing a directory.
		if i == newestIdx {
			continue
		}
		if !orderable(live[i]) || !supersedes(newest, live[i]) {
			continue
		}
		// Attribution last, and on both sides: a copy kae cannot tie to this account
		// says nothing about this account's other copies, in either direction.
		if !holdsAccount(store) {
			continue
		}
		// The winner, asked here rather than hoisted above the loop. Attribution is the
		// most expensive thing this check does — for the account's own credential store
		// it walks every bound directory on the machine — and hoisted it was paid on
		// every machine where any bound copy is newer than the snapshot, which is what a
		// refresh in a bound directory produces. Down here it is paid only once a finding
		// is otherwise ready, and `holdsAccount` memoizes, so a group with several losers
		// still asks once. Pure predicate, evaluated later: same answer, same findings.
		//
		// Phrased as the refusal, not as its complement: `newestIdx < 0` means the winner
		// is the **snapshot**, which this check does not attribute at all, so the guard
		// must not fire there. Written the other way round it read as "no store confirmed
		// the win" and silenced every snapshot-side finding — caught by four tests.
		//
		// **Not dead, and not to be closed by removing it**: it is what keeps kae from
		// telling a user their login is dead on the strength of a copy it cannot
		// attribute. The asymmetry is in blast radius rather than in the predicate — an
		// unattributable winner silences the whole group, an unattributable loser only
		// itself. What made it look like the problem was the predicate it asked, which
		// storeHoldsAccount now states; with that fixed it still fires, and
		// TestSupersededStaysSilentWhenNoHandleCanAttributeTheCopy is the arm that says so.
		if newestIdx >= 0 && !holdsAccount(group.Stores[newestIdx]) {
			continue
		}
		checks = append(checks, adapter.Check{
			Tool: store.Tool, Code: constants.CheckCredentialSuperseded,
			Status: constants.StatusWarn,
			Message: fmt.Sprintf(
				"the %s credential bound to %s is older than another copy of %s/%s (%s); %s's refresh token rotates "+
					"single-use, so if the two are copies of one login only the newer one can still refresh and the "+
					"session in that directory cannot be renewed past %s; %s",
				store.Tool, store.Dir, store.Tool, store.Account, newestAt, store.Tool,
				utcStamp(live[i].ExpiresAt), supersededRemedy(store.Tool, store.Account, store.Dir, newestIdx < 0),
			),
		})
	}
	return checks
}

// supersededRemedy names the fix for a bound copy another copy has overtaken, and it
// depends on **where** the newer copy is — the same reason `kae rollback`'s warning
// branches on that (docs/CLI.md § `kae rollback --json`).
//
// When the newer copy is the account's own snapshot, a re-bind materializes it into
// the store and no browser is involved. That is the one case pinLoginRemedy's
// "deliberately not `kae pin`" reasoning does not cover: its objection is that the
// snapshot may be just as expired as the copy in the store, and here kae has just
// proved the opposite.
//
// When the newer copy is another directory's store, the snapshot is *not* known to
// be newer, so a re-bind could write something older still; a login is the only
// answer that certainly produces a usable credential.
func supersededRemedy(tool, accountName, dir string, newerIsSnapshot bool) string {
	if newerIsSnapshot {
		return fmt.Sprintf("re-bind that directory from the newer snapshot, no login needed: cd %s && %s pin %s %s",
			dir, toolName, tool, accountName)
	}
	return pinLoginRemedy(tool, dir)
}

// storeHoldsAccount reports whether the credential a bound store reads is confirmed
// to be acc's. Any refusal, conflicting or not, means this check must stay silent
// about that store — a conflict says the copy is somebody else's, and missing
// evidence says kae does not know. (pinIdentityChecks is the consumer that reports
// the conflict; here it is only a reason to say nothing.)
//
// The dispatch below is harvestDirCredential's, and it says why there; it is repeated
// here rather than extracted because that one already has `specs` resolved and this one
// must not resolve them on the shared branch, where a resolution failure would refuse a
// copy the readers can attribute. **They move together**: a third per-directory
// mechanism makes this branch three-way (docs/CREDENTIAL-RULES.md § A new per-directory
// mechanism and the link reconcile), and fixing one copy is this repository's
// took-a-subset-of-the-predicate shape.
//
// Asking the one directory about a shared store is what made this check silent
// exactly where it had most to say. Measured 2026-08-16 with the control in
// TestSupersededSurvivesOneSharedHandleLosingItsIdentityCache: with three directories
// bound to one account, whether a finding appeared at all turned on whether the
// handle that happened to win the walk order had an identity cache beside it — while
// a sibling handle on the same file confirmed and was never asked. Two directories
// bound by a current kae are two handles on one file, which is what makes the
// one-handle question the wrong one; a store bound before the split still keeps its
// own copy (`credential_unsplit`), which is the shape the fixtures have to build
// deliberately.
func (app *App) storeHoldsAccount(ctx context.Context, be secret.Backend, acc account.Account, store boundDirStore) bool {
	dirs := store.dirs()
	if dirs.Cred != "" {
		return app.sharedStoreAttribution(ctx, be, store.Tool, dirs.Cred, acc, attributionSource{}).Why == ""
	}
	specs, err := app.dirSpecs(ctx, store.Tool, dirs)
	if err != nil {
		return false
	}
	return dirIdentityConfirms(ctx, be, specs, acc, store.StoreDir).Why == ""
}

// pinUnsplitChecks reports a bound directory that still keeps its own copy of an
// account's credential, which is what a directory bound before v0.17.0 has.
//
// It is the migration prompt, and the state it names is not cosmetic: such a copy
// is invalidated the moment any other binding of that account refreshes, because
// claude's refresh token rotates single-use. Nothing else says so — the copy is
// perfectly healthy until the moment it is not, which is why `credential_stale`
// cannot see this and `credential_superseded` only sees it once the damage has a
// second copy to compare against.
//
// Offline, backend-free, and derived entirely from the walk: a binding is unsplit
// when it has a store for a tool that *can* split (credentialEnvVar) and no
// credential entry recorded for it. A tool with no such variable is not reported,
// because there is nothing for it to migrate to.
func pinUnsplitChecks(stores []boundDirStore) []adapter.Check {
	checks := []adapter.Check{}
	for _, bound := range stores {
		if credentialEnvVar(bound.Tool) == "" || bound.CredDir != "" {
			continue
		}
		checks = append(checks, adapter.Check{
			Tool: bound.Tool, Code: constants.CheckCredentialUnsplit, Status: constants.StatusWarn,
			Message: fmt.Sprintf(
				"the directory bound to %s/%s (%s) keeps its own copy of that account's credential; "+
					"another directory or `%s use -i` on the same account will invalidate it — "+
					"re-bind it: cd %s && %s pin",
				bound.Tool, bound.Account, bound.Dir, toolName, bound.Dir, toolName,
			),
		})
	}
	return checks
}

// pinIdentityChecks reports a bound directory whose own store names an account
// other than the one the directory binds — the bound-directory frame of
// `identity_drift`, which the global check cannot see. That one compares the live
// state of *this shell* against `state.Active`, and inside a kae-owned isolated
// home those are different frames: the live identity is the bound directory's while
// `state.Active` names the global selection, so it skips such a shell entirely
// (identityDriftChecks). This one reads each bound directory's store by its own
// path, so it needs no particular cwd and answers about every binding at once.
//
// It reports **only what it can prove**: both sides readable and their identifying
// keys disagreeing, which is exactly dirIdentityConfirms' Conflicting. Every other
// outcome of that predicate is missing evidence — no identity recorded for the
// account, no cache in the store, a cache shared with the real tool home, an
// unreadable snapshot — and staying silent for those is deliberate. A bound
// directory legitimately has no identity cache until its tool runs there, and one
// bound before v0.16.0 never had one written; warning on that would fire on healthy
// directories, which is how the v0.15.0/v0.15.1 freshness warnings became
// wallpaper. The untracked-snapshot case is not reported here either: it is a
// property of the account, which the global check already states once at `ok`
// level, and repeating it per bound directory says nothing new.
//
// Needs the secret backend to read the account's recorded identity, so unlike
// pinCredentialChecks it does not run when the backend is unavailable.
func (app *App) pinIdentityChecks(ctx context.Context, be secret.Backend, stores []boundDirStore) []adapter.Check {
	// Several directories can bind one account, and the payload compared against is that
	// **account's** recorded identity — the same ref for every one of them, so without a
	// coalescing view of the backend this reads it once per bound directory instead of
	// once per account (measured: two reads for one account at two directories). This is
	// the shape credentialHealthChecks already wraps for the same reason. Safe to wrap
	// here, unlike orphanChecks: this path only ever calls Get, and the capability
	// secret.Cached does not forward is Enumerator.
	//
	// ponytail: per check, not per run. Measured 2026-08-05: a whole `kae doctor` reads
	// one account's identity ref three times — once here and once in
	// credentialHealthChecks, each inside its own cache scope, plus one uncached read in
	// identityDriftChecks. It was four before this scope coalesced its own
	// N-directories-one-account case. Hoisting `secret.WithReadCache` into buildDoctor and
	// keeping only the per-check `Cached(be)` would make it one — safe, since no check
	// buildDoctor reaches calls Set or Delete — but it edits a second check's wrapping for
	// a read-only warn path, so it is recorded rather than taken here.
	ctx = secret.WithReadCache(ctx)
	be = secret.Cached(be)
	checks := []adapter.Check{}
	for _, bound := range stores {
		// The snapshot first, because it is the cheap half: a binding to an account that
		// is gone is pinChecks' finding, and comparing against a snapshot kae cannot read
		// proves nothing either way — so neither is worth an adapter resolution.
		acc, found, err := account.Load(app.Paths.AccountDir(bound.Tool, bound.Account))
		if err != nil || !found {
			continue
		}
		// ponytail: this is the *second* resolution of the same (tool, store) pair in one
		// `kae doctor` — pinCredentialChecks already made one through dirCredentialFreshness.
		// Measured 2026-08-05: on darwin a bound directory that binds codex under
		// `cli_auth_credentials_store = "auto"` therefore pays two `security` probes per run
		// instead of one, to rediscover that codex declares no identity artifact. Left as is
		// because a `doctor` run already makes many, and hoisting the specs into
		// boundDirStores would make two precise tests of dirCredentialFreshness's own
		// resolution ("the refusal happens before the keychain is touched", "it reads the
		// dir-scoped item") assert something weaker. Upgrade path if a run ever feels slow:
		// resolve once in the walk and hand the specs to both halves.
		specs, err := app.dirSpecs(ctx, bound.Tool, bound.dirs())
		if err != nil {
			continue // an unresolvable store is the bind's finding, and pinChecks reports the binding
		}
		if refused := dirIdentityConfirms(ctx, be, specs, acc, bound.StoreDir); !refused.Conflicting {
			continue
		}
		checks = append(checks, adapter.Check{
			Tool: bound.Tool, Code: constants.CheckIdentityDrift, Status: constants.StatusWarn,
			Message: pinIdentityDriftMessage(bound),
		})
	}
	return checks
}

// pinIdentityDriftMessage frames a bound directory whose store disagrees with its
// binding. Two causes produce it and kae cannot tell them apart offline, because
// the token is opaque: something logged in there as another account (so the
// credential in that store is that account's too, and the directory is running an
// account its binding does not name), or kae could not apply the identity when it
// bound the directory (so the credential is the bound account's and only the label
// is wrong). Both are stated, because the remedies point in opposite directions and
// only the user knows which account they meant that directory to run.
//
// Neither the live nor the stored identity value appears: an identity is PII, and
// the tool, account and directory are enough to act on.
//
// The remedy is `kae relogin`, not `kae pin`. `kae pin` was the remedy until 2026-08-08
// and it is a **no-op in the state this check reports most often**: the account's
// credential store is shared, so when a sibling directory still confirms the account, the
// readers disagree, the harvest keeps the copy, and a bind that keeps writes no identity
// label either — so the directory goes on running the other account, and the next `kae
// doctor` prints this same finding with the same remedy. `kae relogin` repairs both causes
// the sentence states: it mints a login in that directory (so the store and the label
// agree with the binding) and captures it back. Found by review; a remedy that lands
// where nothing changes is the same defect as one that names a path nothing reads.
func pinIdentityDriftMessage(bound boundDirStore) string {
	return fmt.Sprintf(
		"the %s identity cache in %s names an account other than %s/%s, which that directory binds: "+
			"either something logged in there as another account — in which case that directory is running "+
			"an account its binding does not name — or kae could not apply the identity when it bound the "+
			"directory, and %s displays the wrong account while running the bound one (kae cannot tell "+
			"those apart offline). To make the binding true again: %s; to keep what is there instead, "+
			"bind the directory to that account",
		bound.Tool, bound.Dir, bound.Tool, bound.Account,
		bound.Tool, pinLoginRemedy(bound.Tool, bound.Dir),
	)
}

// boundDirStore is one per-directory credential store a bound directory points at
// **now**: the account its mise fragment binds for one tool, resolved to the store
// directory that tool reads there.
type boundDirStore struct {
	Dir      string // the bound directory itself, which is what a finding names
	Tool     string
	Account  string
	StoreDir string
	// CredDir is where that binding puts the tool's credential, empty when it
	// stays inside StoreDir — the layout of every directory bound before the
	// credential split, and the state `credential_unsplit` reports.
	CredDir string
}

// dirs is the pair dirSpecs resolves this binding's artifacts against.
func (s boundDirStore) dirs() bindDirs { return bindDirs{Config: s.StoreDir, Cred: s.CredDir} }

// store is this binding as the store the credential readers take. The account is
// deliberately left off: a dirStore's Account is the one a *path* names, which is
// what storeAccount fills in for a shared store, and nothing this conversion feeds
// reads it.
func (s boundDirStore) store() dirStore {
	return dirStore{Tool: s.Tool, Dir: s.StoreDir, CredDir: s.CredDir}
}

// boundDirStores lists every live binding a report may speak about, one entry per
// bound directory × tool. It is the gate every command that says "bound to <dir>"
// needs, in one place because each consumer that re-derived it has got a piece of it
// wrong: `pinChecks` has skipped an unpinned directory since it shipped, and the
// doctor credential sweep shipped its first draft without that gate (AGENTS.md,
// which also names `kae ls --pins` as a third consumer — deliberately left on its
// own display-shaped walk rather than folded in here for a listing it already gets
// right).
//
// Silent skips, each for a reason a caller must not second-guess:
//   - a directory whose recorded path is **gone**: `pinChecks` reports that it may
//     have been deleted or moved, and naming it here too would report one problem as
//     two.
//   - a fragment that cannot be read: `pinChecks` reports that as well.
//   - a directory that was `kae unpin`-ed. Its store is kept on purpose so a re-pin
//     restores the sessions, but nothing there points at it any more, so a finding
//     would say "bound to" about a directory that is not bound and name a remedy that
//     lands where nothing reads.
//   - a tool the fragment does not bind, a mode kae does not recognize
//     (boundStoreDir), or a store that has never been materialized.
//
// The first three **converge** here and are written separately as intent, not because
// a test can tell them apart: a gone or unpinned directory both end at an empty
// account map that boundStoreDir answers "not bound" from, and all three arms
// `continue`. `pinChecks` is where the distinction has consequences — it reports a
// different finding for each — and AGENTS.md records which guards of this walk are
// killable, so nobody writes a test that cannot fail. The last one is killable, and
// only on darwin: a per-directory keychain item outlives its deleted store directory.
//
// Tools are walked in canonical order, so a JSON report cannot reorder with a map
// iteration.
func (app *App) boundDirStores() []boundDirStore {
	index := app.boundDirectoryIndex()
	if index.err != nil {
		return nil // pinChecks already reports an unreadable store root; not twice
	}
	stores := []boundDirStore{}
	for _, pin := range index.directories {
		if !pin.directoryExists() {
			continue
		}
		fragment, exists, ferr := pin.readFragment()
		if ferr != nil || !exists {
			continue
		}
		for _, tool := range constants.Tools {
			credDir, bound := app.boundStoreDir(pin.PinID, tool, fragment)
			if !bound || !dirExists(credDir) {
				continue
			}
			stores = append(stores, boundDirStore{
				Dir: pin.Dir, Tool: tool, Account: fragment.Accounts[tool], StoreDir: credDir,
				CredDir: fragment.CredDirs[tool],
			})
		}
	}
	return stores
}

// boundStoreDir returns the store directory tool's credential lives in under the
// binding fragment describes, and whether that binding covers tool at all.
//
// It reads the fragment rather than the store tree because the two answer
// different questions. The tree is history: `kae unpin` keeps a store on purpose,
// and re-binding one tool of a profile leaves the previous tools' stores in place,
// so a walk returns stores nothing points at any more. Only the fragment says what
// this directory binds *now* — and a report that says "bound to" has to mean it,
// or its remedy (log in here) lands somewhere the tool will not read.
//
// A mode kae does not recognize yields bound=false rather than a guessed path: a
// third per-directory mechanism must be added here deliberately, the same lockstep
// dirCredentialStores needs, and inventing a path for one is how kae ends up
// judging a store that does not exist.
func (app *App) boundStoreDir(pinID, tool string, fragment fragmentInfo) (dir string, bound bool) {
	account, ok := fragment.Accounts[tool]
	if !ok {
		return "", false
	}
	return app.modeStoreDir(fragment.Mode, pinID, tool, account)
}

// pinLoginRemedy names the fix for a bound directory's credential: log in *inside*
// that directory, through `kae relogin`.
//
// It named the tool's own login command directly until v0.17.0, and that remedy was
// correct only in a shell where the pin was active: the isolation variable is what
// sends the login to the store kae bound, so with mise activation absent or the
// config untrusted the same command refreshes the **real home** instead — the wrong
// account moves and this one is still stale. `kae relogin` exports the variable
// itself, so the hazard cannot happen rather than needing a caveat in every message
// that carries this string; it also captures the new login back into the account
// snapshot, which nothing did proactively.
//
// Deliberately not `kae pin` (which would re-copy the account snapshot): that
// snapshot may be just as expired as this copy, in which case re-binding would
// report success and change nothing.
//
// The fallback is for a tool kae has no login command for. That is the same gate
// `kae relogin` selects candidates on (reloginTool), so this never names a command
// that would refuse.
func pinLoginRemedy(tool, dir string) string {
	if loginCommand(tool) != nil {
		return fmt.Sprintf("verify the bound account with kae status in that directory and stop other sessions using its credential; log in inside that directory as the bound account: cd %s && %s relogin %s", dir, toolName, tool)
	}
	return fmt.Sprintf("kae cannot launch a login for %s; before manual login in %s, verify the bound account and that mise activation, trust and the tool environment select its bound store; see docs/CLI.md Recovery guidance", tool, dir)
}

// dirCredentialFreshness reads one per-directory store's credential and parses it,
// reporting ok=false for anything it cannot judge.
//
// The location comes from dirCredentialSpec — the adapter's answer for an
// environment pointed at this store — never from a path or a service name rebuilt
// here. That is the same rule writeDirCredential and removeDirCredential follow,
// and breaking it is the defect that made every bound directory on macOS run the
// previous account with all offline guards green.
//
// The KeychainDirBindable gate mirrors the write gate exactly. Without it, a tool
// whose item does not move with its isolation variable would have its *global*
// login read here and reported as this directory's, so a healthy global login
// would be blamed on a directory it has nothing to do with — and a stale one would
// be reported once per bound directory.
func (app *App) dirCredentialFreshness(ctx context.Context, store dirStore) (freshness.Info, bool) {
	artName := credentialArtifactName(store.Tool)
	if artName == "" {
		return freshness.Info{}, false // no credential kae materializes per directory
	}
	sp, ok, err := app.dirCredentialSpec(ctx, store.Tool, artName, store.dirs())
	if err != nil || !ok {
		return freshness.Info{}, false
	}
	if unbindableDirKeychain(sp) {
		return freshness.Info{}, false
	}
	value, err := artifact.ReadLive(ctx, sp)
	if err != nil || !value.Present {
		// Absent is not a finding here: `kae unpin` keeps the store on purpose, and a
		// bound directory whose tool was never started in it has no credential yet.
		return freshness.Info{}, false
	}
	info := freshnessOf(store.Tool, value.Data)
	return info, info.Known
}
