package cmd

// Per-directory credential stores: enumerating, pruning and removing them, and
// the reader accounting that decides whether a shared store may go.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/keychain"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// dirStore is one per-directory credential store a bound directory has
// materialized. Dir is the config dir the tool reads, i.e. what its isolation env
// var points at, which is what resolves the store's item identity.
//
// Account is the account whose credential the store holds, and it is empty for
// an account-agnostic mechanism (shared): that store is one directory per
// pin×tool, so its path records no account and only the fragment kae is
// replacing can name one (storeAccount).
type dirStore struct {
	// Mode is the bind mode whose store this is (a bindModes name), as the walk found
	// it on disk; empty for a store built from a binding rather than the walk, which
	// storeAccount then never attributes from the replaced fragment.
	Mode    string
	Tool    string
	Dir     string
	Account string
	// CredDir is where this store's credential resolves, empty when it resolves
	// inside Dir. It comes from the recorded binding and never from the account,
	// and that difference is the whole migration: a directory bound before the
	// credential split keeps its credential in the store itself, so deriving the
	// per-account path here would send every sweep at a place nothing ever wrote —
	// the harvest would find nothing to preserve and the delete would remove an
	// item that is not the one in use. Reading the string kae actually exported is
	// the same rule attribution follows (docs/ROADMAP.md).
	CredDir string
}

// dirs is the pair dirSpecs resolves this store's artifacts against.
func (s dirStore) dirs() bindDirs { return bindDirs{Config: s.Dir, Cred: s.CredDir} }

// dirCredentialStores lists the per-directory stores that exist on disk for one
// bound directory, across every bindModes mechanism and every account of a
// per-account one.
//
// It walks isolation/<pinID> rather than consulting a record of past bindings,
// because no such record exists: the mise fragment describes the binding kae is
// about to replace, not the ones before it. The directory tree is the only
// history, and it is enough for the operations that need one — every caller is
// standing *in* the bound directory, so pinID comes from its own cwd and the walk
// can never reach another directory's stores.
//
// The history is what makes it the wrong tool for asking "what is bound *now*":
// the walk returns stores of tools this directory no longer binds, and stores of a
// directory that has been unpinned. A reader of the live binding must go through
// the fragment instead (boundStoreDir).
// ponytail: a store directory is kept forever (a re-pin restores its sessions), so
// a stale isolated account's dir is re-probed on every later pin — one extra
// attributes-only `security` call per such account per pin. Fine at single-digit
// account counts; record swept stores (or cache the probe) if `kae pin` latency
// ever shows up.
// prev is the binding whose recorded credential entries say where each tool's
// credential lives; pass the zero value where no binding was read, which reads
// every store as pre-split (its credential inside itself).
func (app *App) dirCredentialStores(pinID string, prev fragmentInfo) ([]dirStore, error) {
	pinDir := app.Paths.PinDir(pinID)
	tools, err := os.ReadDir(pinDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list per-directory stores in %s: %w", pinDir, err)
	}
	stores := []dirStore{}
	add := func(store dirStore) {
		store.CredDir = app.attributedCredDir(store, prev)
		stores = append(stores, store)
	}
	for _, toolEntry := range tools {
		if !toolEntry.IsDir() {
			continue
		}
		tool := toolEntry.Name()
		for _, m := range bindModes() {
			if !m.perAccount {
				if dir := m.storeDir(app.Paths, pinID, tool, ""); dirExists(dir) {
					add(dirStore{Mode: m.name, Tool: tool, Dir: dir})
				}
				continue
			}
			accounts, err := os.ReadDir(filepath.Join(pinDir, tool, m.segment))
			if err != nil {
				continue // no stores of this mode for this tool
			}
			for _, acct := range accounts {
				if !acct.IsDir() {
					continue
				}
				if dir := m.storeDir(app.Paths, pinID, tool, acct.Name()); dirExists(dir) {
					// The account is the directory's own name (kae composes the path from it),
					// which is what lets the sweep harvest a per-account store it is about to
					// delete without consulting any binding.
					add(dirStore{Mode: m.name, Tool: tool, Dir: dir, Account: acct.Name()})
				}
			}
		}
	}
	return stores, nil
}

// attributedCredDir says where one store's credential lives, and refuses to guess.
// Empty means "inside the store", which is both the pre-split layout and the safe
// answer for a store kae cannot attribute.
//
// The recorded entry alone is not enough, and that is the whole point of this
// function. The walk returns stores of *older* bindings too — kae keeps a store so a
// re-pin restores its sessions — so taking the replaced fragment's credential entry
// verbatim would hand a leftover store bound to one account the credential store of
// another. The harvest would then read that copy, compare it against the leftover's
// own identity cache, and on a match file one account's token under the other's name:
// the silent mislabelling every attribution guard here exists to prevent.
//
// So the entry counts only when it names *this store's own* account. Anything else
// falls back to the store directory, where a pre-split credential is — and where a
// post-split store simply has none, so the pass and the sweep find nothing and do
// nothing. Failing towards "does nothing" is the direction that cannot destroy or
// mislabel a login.
func (app *App) attributedCredDir(store dirStore, prev fragmentInfo) string {
	recorded := prev.CredDirs[store.Tool]
	if recorded == "" {
		return ""
	}
	if own := app.credStoreDir(store.Tool, storeAccount(store, prev)); own != "" && own == recorded {
		return recorded
	}
	return ""
}

// pruneDirCredentials removes the per-directory keychain credential of every store
// of this pin that keep does not name, and returns one line per removal for the
// caller to print as part of its result. A failure is warned about here, where it
// is detected, and never escalated: the new binding is already correct, so a store
// kae could not clean is a leftover secret rather than a broken bind — and a
// warning must not change an exit code.
//
// Call it **after** the new binding is in place. Before, a failure part-way
// through the re-bind would leave the live binding pointing at a store whose
// credential kae had already deleted.
//
// onlyTool limits the sweep to one tool ("" sweeps every tool of this pin), for
// the single-tool re-bind that must not touch a sibling tool's store.
//
// prev is the binding this operation replaces (the fragment as it was before the
// caller rewrote or removed it), and it is the only thing that can name the
// account a *shared* store's credential belongs to — see storeAccount. Pass the
// zero value only where no such binding was read; the sweep then keeps a store
// whose newer credential it cannot attribute, rather than deleting it.
//
// A keychain item is removed where the adapter declares it bindable — exactly the
// class writeDirCredential creates, and the case that most needs sweeping, since an
// item is invisible from the directory tree and would otherwise hold a credential
// nothing can find. A **file** credential is removed only where it is no longer the
// copy its own store reads; removeDirCredential states that rule once, and this
// comment deliberately does not restate it. What is never removed is the store
// directory itself, with its sessions and settings.
//
// Deleting one is unrecoverable, so it harvests first: the item can hold the only
// copy of the account's credential that still refreshes (harvestDirCredential),
// and an item kae could not preserve is kept instead of deleted. A leftover secret
// is a smaller fault than a login destroyed by a cleanup.
func (app *App) pruneDirCredentials(ctx context.Context, be secret.Backend, pinID, onlyTool string,
	keep map[string]bool, prev fragmentInfo, purging bool,
) []string {
	stores, err := app.dirCredentialStores(pinID, prev)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kae: warning: %v\n", err)
		return nil
	}
	removals := []string{}
	for _, store := range stores {
		if onlyTool != "" && store.Tool != onlyTool {
			continue
		}
		// A kept store is normally left alone — it is the one this binding points at.
		// The exception is the migration: the store is kept for its sessions while its
		// *credential* has just moved out of it, into the account's own store. Its
		// pre-split keychain item would otherwise survive under a service name nothing
		// resolves any more, holding a full copy of the credential — the state
		// docs/ROADMAP.md § "Per-directory keychain items outlive everything that could
		// name them" records having found five of on a real machine, and the one that
		// poisons the offline regression detector, which reads an item at the config
		// dir's name as "something wrote there since the last bind".
		//
		// **And only when the pin-level pass did not refuse it.** This sweep runs
		// *after* the materializer, which ends by stamping that same store with this
		// account's identity (writeDirIdentity) — so the evidence the harvest inside
		// it would attribute the copy by is evidence kae wrote itself, three steps
		// earlier in this command. Without the check, a copy the pass declined as
		// unattributable is harvested and deleted anyway, and a store holding *another
		// account's* login (the state identity_drift exists to report) has that login
		// filed under this account's name — undetectable afterwards, since the token is
		// opaque and every surface then agrees on a label that is simply wrong. Both
		// measured 2026-08-07, and both are regressions of this exception rather than
		// of the harvest.
		//
		// `refusalReported` covers every store the pass **judged**: it marks one only
		// when it refused *and* the binding is moving off it, which is the set this
		// exception accepts. Where the pass succeeded there is nothing left to
		// mis-attribute — the snapshot already holds that copy, so the harvest below
		// finds nothing newer and the delete carries no judgement at all.
		//
		// The stores the pass skipped *before* judging arrive here unmarked, and they
		// are caught by `harvestBeforeDelete` instead — which is why that second gate is
		// not redundant with this one. One such path is live today: the account the
		// previous binding named is gone, so the pass returns at `snapshotCredential`
		// and the sweep keeps the copy on the account-gone arm (measured 2026-08-07).
		// That catch reaches only a store holding its **own** credential: where the
		// credential is the account's, removeDirCredential's
		// `store.CredDir != "" && !purging` return fires first and neither gate runs, so
		// an account-gone copy is kept in silence rather than reported (measured
		// 2026-08-16). What reached that state was a `kae account rename`, which no longer
		// does — it harvests before its first write (harvestRenamedAccountCredentials) —
		// so nothing routine is known to arrive here in silence today.
		// The one that would open silently is a tool with a credential variable whose
		// rotation has never been measured — the pass returns at `rotatesSingleUse` and
		// `harvestBeforeDelete` lets such a tool through unconditionally, so its kept
		// store would be deleted with neither harvest nor attribution.
		// TestNoSplittingToolSkipsBothNets refuses that combination.
		//
		// The contrast worth keeping: migratePreSplitHome does this job correctly by
		// running *before* the write. This one cannot — a delete has to follow the new
		// binding — so it defers to the pass's verdict instead of re-deciding.
		// Two uses, and the name fits one of them. For a bind it is what lets a store
		// the binding still points at be swept at all. For `kae unpin --purge` nothing
		// migrated — the fragment was removed — and `keep` is nil, so the branch below
		// is irrelevant; what the flag still carries there is "this store's file is not
		// the copy it reads any more", which is the answer removeDirCredential needs to
		// take a pre-split file credential the purge was asked to remove.
		migrating := app.credentialMovedOutOf(store, pinID, prev)
		// Keyed on the credential's own location, the same expression the pass marks and the
		// write reads, so the three cannot drift apart. The *granularity* here cannot be
		// killed by a test, and this says so rather than inventing a reason: `migrating` is
		// only ever true when `store.CredDir` is empty (credentialMovedOutOf returns false
		// otherwise), and there `credDirOrConfig()` is `store.Dir` by definition — measured
		// 2026-08-08, the config-dir variant survives every test. The term itself is live:
		// forcing it either way kills tests.
		if keep[store.Dir] && (!migrating || app.refusalReported[store.dirs().credDirOrConfig()]) {
			continue
		}
		removed, err := app.removeDirCredential(ctx, be, store, storeAccount(store, prev), purging, migrating)
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr,
				"kae: warning: could not remove the superseded %s credential for %s: %v\n",
				store.Tool, store.Dir, err)
		// Two removals share this loop and they are not the same event, so they do not
		// share a sentence. removeDirCredential deletes at the location the store
		// *reads*, which for a split store is the account's own — so reporting that as
		// a "per-directory" credential at store.Dir named neither the thing removed nor
		// where it lived, and understated the scope of the one removal that affects
		// every other binding of the account. Found by running the smoke procedure in
		// docs/VALIDATION.md § Switching a per-account credential store. What no test pinned was the
		// **account-wide** sentence — the two assertions on this literal are both negative;
		// the per-directory arm below is pinned positively, but on `lines[0]` containing the
		// store dir rather than on the wording (TestPruneDirCredentialsRemovesSupersededItem),
		// so do not read that assertion as dead weight.
		case removed && store.CredDir != "":
			removals = append(removals, fmt.Sprintf(
				"Removed the %s credential this account's bindings shared; nothing points at it any more (%s)",
				store.Tool, store.CredDir,
			))
		case removed:
			removals = append(removals, fmt.Sprintf(
				"Removed the superseded per-directory %s credential (%s)", store.Tool, store.Dir,
			))
		}
	}
	return removals
}

// credentialMovedOutOf reports whether this store's credential has just been
// relocated to the account's own store — i.e. the binding being replaced kept it
// inside the store (no recorded entry) and the tool now has somewhere else to put
// it. It is the one reason a *kept* store still has something to sweep.
//
// It answers false for a tool that cannot split the two, so a store that simply
// holds its credential is never swept while it is still bound.
func (app *App) credentialMovedOutOf(store dirStore, pinID string, prev fragmentInfo) bool {
	if store.CredDir != "" || prev.CredDirs[store.Tool] != "" {
		return false // already split, or never was
	}
	// The store the **previous** binding pointed at, and only that one — the single
	// load-bearing condition here (measured 2026-08-07; the three above it are each
	// masked downstream by removeDirCredential's own gates and cannot be killed).
	// Two weaker rules were wrong for the same reason — they answer "yes" from the absence of a
	// credential entry, which every store of a pre-split binding shares:
	//
	//   - "prev records no entry" alone fires on a zero `prev` too (no binding could
	//     be read, or a caller that has none), sweeping the item of a store the
	//     current binding still points at on no evidence at all;
	//   - "…and prev bound this tool" still fires on the store the re-bind is moving
	//     *to*, whose credential never lived in it.
	//
	// What has actually migrated is the one store that held the credential before.
	previous, bound := app.boundStoreDir(pinID, store.Tool, prev)
	if !bound || previous != store.Dir {
		return false
	}
	return app.credStoreDir(store.Tool, storeAccount(store, prev)) != ""
}

// storeAccount names the account whose credential store holds, for the sweep that
// is about to delete it. Empty means nothing kae can read says so.
//
// A per-account store answers for itself: its path is composed from the account.
// An account-agnostic one cannot — one directory serves every account this pin ever
// bound there — so the answer comes from the binding being replaced, and only when
// that binding used the store's own mechanism. Reading it from a fragment in another
// mode would attribute a store left over from an *earlier* binding to the wrong
// account, which is the mislabelling the harvest's identity check exists to catch;
// there is no reason to hand it that case on purpose.
func storeAccount(store dirStore, prev fragmentInfo) string {
	if store.Account != "" {
		return store.Account
	}
	if m, ok := bindModeFor(store.Mode); ok && !m.perAccount && store.Mode == prev.Mode {
		return prev.Accounts[store.Tool]
	}
	return ""
}

// removeDirCredential deletes the keychain item one store directory's tool reads,
// reporting whether there was one to delete. It resolves the item the same way
// writeDirCredential does — by asking the adapter with an env pointed at the store
// — so the item removed is the one that directory owns and never a global login.
//
// accountName is the account that store's credential belongs to, or "" when kae
// cannot say (storeAccount). The delete is unrecoverable and the item can hold the
// newest copy of a rotating credential, so it is harvested into that account's
// snapshot first; a copy kae could not preserve — including one it could not
// attribute — leaves the item in place.
func (app *App) removeDirCredential(ctx context.Context, be secret.Backend, store dirStore,
	accountName string, purging, migrating bool,
) (bool, error) {
	tool := store.Tool
	artName := credentialArtifactName(tool)
	if artName == "" {
		return false, nil
	}
	// Resolved once for the delete and the harvest's identity comparison; a second
	// resolution costs a `security` subprocess for codex (writeDirCredential says why).
	specs, err := app.dirSpecs(ctx, tool, store.dirs())
	if err != nil {
		return false, err
	}
	sp, ok := specByName(specs, artName)
	if !ok {
		return false, nil
	}
	// A keychain item always; a credential **file** only where it is not the copy that
	// store still reads. The asymmetry this used to state — a file credential lives
	// *inside* the store directory, which `kae unpin` deliberately keeps along with its
	// sessions and settings — holds for exactly one case now, and both others are the
	// ones that would otherwise leave a full plaintext copy of a live account behind:
	//
	//   - a per-account store (CredDir set) holds the credential and nothing else, so
	//     `--purge` leaving its file behind keeps a secret in a directory kept for no
	//     other reason;
	//   - a store whose credential has just moved out of it (migrating) keeps a file
	//     nothing resolves any more — until a shell that exports only the config
	//     variable finds it, refreshes it, and invalidates the copy every directory
	//     bound to that account now shares.
	//
	// Getting this wrong is invisible: every reader resolves through the new location,
	// so `doctor` sees nothing. The global-isolated migration has always deleted the
	// file (migratePreSplitHome), and the two paths must not disagree about one state.
	if sp.Kind != constants.KindKeychain && store.CredDir == "" && !migrating {
		return false, nil
	}
	if sp.Kind == constants.KindKeychain && !sp.KeychainDirBindable {
		return false, nil
	}
	// A per-account credential store is not this directory's to delete. It belongs to
	// the account the way its snapshot does, and other directories — and a globally
	// isolated home — read the same copy, so the asymmetry that licenses this sweep
	// (an item under a per-directory service name is addressable from nowhere) simply
	// does not hold for it: credstore/<tool>/<account> is a path kae can name.
	//
	// So ordinary housekeeping leaves it, and only an explicit `--purge` may take it,
	// and only once nothing points at it. Without the count, one directory's
	// `kae unpin --purge` logs out every sibling worktree bound to the same account.
	if store.CredDir != "" {
		if !purging {
			return false, nil
		}
		switch refs, known := app.credStoreRefs(store.CredDir); {
		case !known:
			fmt.Fprintf(os.Stderr,
				"kae: warning: kae could not tell whether another binding still uses the %s credential for "+
					"%s, so it is left in place rather than deleted\n", tool, accountName)
			return false, nil
		case refs > 0:
			fmt.Fprintf(os.Stderr,
				"kae: note: %d other binding(s) still use the %s credential for %s, so it is kept\n",
				refs, tool, accountName)
			return false, nil
		}
	}
	// Probe before deleting, attributes only, so the caller can report what it
	// actually removed: the delete primitive treats "no such item" as success, so
	// without this a store that never had an item is announced as cleaned up. The
	// probe is scoped the way the delete is — account-scoped only for a service that
	// holds more than one legitimate item, since asking with an account a service
	// does not scope by would answer "absent" for an item the delete still removes.
	existed, err := dirCredentialExists(ctx, sp)
	if err != nil || !existed {
		return false, err
	}
	// Last chance to keep what this item holds. Unlike the overwrite in
	// writeDirCredential, nothing can be reconstructed afterwards: a re-pin
	// re-materializes a store's credential from the account snapshot, so a copy that
	// was never harvested into one is simply gone.
	if !app.harvestBeforeDelete(ctx, be, specs, tool, accountName, store.dirs(), purging) {
		return false, nil
	}
	if err := artifact.ApplyLive(ctx, sp, artifact.Value{Present: false}); err != nil {
		return false, err
	}
	return true, nil
}

// dirCredentialExists answers "is there anything to delete" for either store kind,
// so the caller can report what it actually removed rather than announcing a cleanup
// of a store that never held one — the delete primitive treats absence as success.
func dirCredentialExists(ctx context.Context, sp artifact.Spec) (bool, error) {
	if sp.Kind != constants.KindKeychain {
		// Present, not "the read succeeded": a store that never held a credential must
		// answer no, or `--purge` announces having removed one from it.
		value, err := artifact.ReadLive(ctx, sp)
		if err != nil {
			return false, err
		}
		return value.Present, nil
	}
	return dirItemExists(ctx, sp)
}

// dirItemExists answers "is there an item to delete" for a keychain spec, scoped
// the way that spec's delete is: account-scoped only where the service can hold
// more than one legitimate item.
func dirItemExists(ctx context.Context, sp artifact.Spec) (bool, error) {
	if sp.KeychainMatchAccount {
		return keychain.ItemExistsForAccount(ctx, sp.Target, sp.KeychainAccount)
	}
	return keychain.ItemExists(ctx, sp.Target)
}

// credStoreRefs counts what still reads a per-account credential store, and says
// whether that count can be trusted. known is false when any source could not be
// read — the caller must then keep the credential, because "kae found no reference"
// and "kae could not look" are the same answer only if you are willing to log
// somebody out on a read error.
//
// Two sources, and both are needed: every bound directory's fragment (the string it
// exports, not a path re-derived from the account, so a hand-edited or pre-split
// fragment counts as what it actually says), and `state.synced`, which is how a
// globally isolated home reads the same store without any fragment naming it.
//
// A pin whose recorded directory is gone is not counted as a reference. That is exact
// for a deleted directory; a moved directory may still read the store through the
// fragment that moved with it, but this index cannot locate it. `pinChecks` reports
// that ambiguity without recommending deletion. A fragment that exists and cannot be
// parsed is the unknown case, not the zero case.
func (app *App) credStoreRefs(credDir string) (refs int, known bool) {
	index := app.boundDirectoryIndex()
	if index.err != nil || !index.complete {
		// A store whose breadcrumb could not be read names a directory kae cannot
		// reach — and that directory may be one that reads this credential.
		return 0, false
	}
	for _, pin := range index.directories {
		// Equivalent to the !exists branch below rather than merely stricter, and worth
		// saying so: readFragmentAt on a missing directory returns exists=false with no
		// error, so both arms continue identically (measured 2026-08-07). The one case
		// they differ on is a path that exists and is not a directory, where dropping
		// this gate yields ENOTDIR — which os.IsNotExist does not match, so it degrades
		// to known=false. Deleting it on the grounds that it "cannot fail" would change
		// that case silently.
		if !pin.directoryExists() {
			continue
		}
		fragment, exists, ferr := pin.readFragment()
		if ferr != nil {
			return 0, false
		}
		if !exists {
			continue // unpinned: the store is kept, but nothing reads it
		}
		for _, dir := range fragment.CredDirs {
			if dir == credDir {
				refs++
			}
		}
	}
	st, err := app.loadState()
	if err != nil {
		return 0, false
	}
	for tool, account := range st.Synced {
		if app.credStoreDir(tool, account) == credDir {
			refs++
		}
	}
	return refs, true
}

// credStoreReader is one directory reading a credential store, in its two aspects. Config
// is where its identity cache is, which is the evidence attribution reads. Dir is the
// directory a **message** may name, which is not the same string: for a binding it is the
// bound directory rather than kae's store under it — a store path names a pin-id hash and
// no user can tell which worktree that is — and for a globally isolated home the two are
// the same path, because that home is where the tool actually runs.
//
// Not `boundDirStore`, which answers a reporting question and skips an absent
// store directory. Both consume boundDirectoryIndex observations, but readers
// require completeness; a report can retain the readable subset.
type credStoreReader struct {
	Config string
	Dir    string
}

// credStoreReaders names the config dirs of everything currently reading the credential
// in credDir for tool — the directories whose identity cache is evidence about *that copy*.
//
// It exists because the per-account store broke the assumption attribution used to rest on.
// A per-directory store's credential and its identity cache sat in one directory, so the
// cache beside the credential was evidence about it. Since the split the credential is the
// account's and the cache is the directory's, and the two answer different questions: in
// shared mode a directory's config dir belongs to its pin-id, so it still carries the
// **previous** binding's label. Reading that as evidence about the new account's store is
// how a re-bind between two accounts came to destroy a live credential (measured
// 2026-08-08; docs/ADAPTERS.md § Per-directory credential store is normative).
//
// Read from the fragments on disk, which during a bind still describe the **previous**
// state — and that is the property that makes this correct rather than a coincidence. A
// directory being re-bound to a different account is not yet a reader of the new account's
// store, so its stale label is excluded without anyone having to pass the previous binding
// down here. A re-pin to the same account is still a reader, so its cache still counts.
//
// complete is false when kae cannot enumerate them, which every caller must read as missing
// evidence rather than as "nobody reads it".
//
// Four of the guards below are **structurally unobservable** and are written as intent
// rather than covered by a test that cannot fail (measured 2026-08-08): the `credDir == ""`
// arm, because the only caller is gated on `dirs.Cred != ""`; the `!exists` half of the
// fragment test, because a nil `CredDirs` map already fails the comparison beside it; the
// `dirExists` arm, which converges with `!exists` for the same reason `boundDirStores`
// records; and the `syncedTool == tool` shape the global walk replaced, since the store
// path embeds the tool. TestBoundDirectoryConsumerPolicies covers unreadable
// fragments making the set incomplete. Dropping the `bound` check
// would append `""` — which `dirSpecs` resolves to the **real home**, so a future third
// per-directory mechanism would silently attribute the account's store from the real home's
// identity cache — the reason a mode resolves through its bindModes row and never
// through a default.
// Each call obtains a fresh boundDirectoryIndex and then reads global homes. The
// supersedes gate decides whether attribution calls it at all; a bind must not
// retain those observations through its later fragment write and teardown.
func (app *App) credStoreReaders(credDir, tool string) (readers []credStoreReader, complete bool) {
	if credDir == "" {
		return nil, false
	}
	index := app.boundDirectoryIndex()
	if index.err != nil || !index.complete {
		return nil, false
	}
	for _, pin := range index.directories {
		// A recorded directory that is gone is skipped, and deliberately **without**
		// making the set incomplete — which is the opposite of what "kae could not look"
		// usually earns here. `kae unpin` never removes the breadcrumb (only the
		// fragment), so a deleted worktree leaves one forever: treating it as
		// incompleteness would refuse every harvest for every account from the first
		// deleted temp worktree onward, permanently, and the mechanism would silently stop
		// working. What that costs is recorded in docs/ROADMAP.md § A moved bound
		// directory — a directory that was *moved* rather than deleted still exports the
		// old store from the fragment that travelled with it, and kae cannot read it at the
		// recorded path.
		if !pin.directoryExists() {
			continue
		}
		fragment, exists, ferr := pin.readFragment()
		if ferr != nil {
			return nil, false
		}
		if !exists || fragment.CredDirs[tool] != credDir {
			continue // unpinned, or bound to some other account's credential
		}
		if store, bound := app.boundStoreDir(pin.PinID, tool, fragment); bound {
			readers = append(readers, credStoreReader{Config: store, Dir: pin.Dir})
		}
	}
	// A globally isolated home reads the account's credential too, and it has no fragment;
	// its home *is* the config dir.
	//
	// Read from **disk**, not from `state.synced`, and that is the opposite source from
	// credStoreRefs on purpose — the two ask different questions. Refs asks "is anything
	// still using this", where a home nobody has selected must not keep a credential alive
	// forever; this asks "whose login is this copy", where the identity cache a tool left
	// in a home is honest evidence whether or not that home is selected right now. Sourced
	// from `state.synced`, `kae run -i` had **no reader at all** — it exports both
	// variables and never writes that map — so every run after the first kept the copy and
	// the account snapshot was never updated again (found by review, 2026-08-08).
	//
	// The asymmetry has a benign second half worth stating: such a home is trusted to
	// *attribute* a copy while not counting as a *reference*, so the last bound directory's
	// `kae unpin --purge` deletes a credential that home reads. Nothing is lost — the purge
	// harvests into the snapshot first, and the next `run -i` re-materializes the home from
	// it — which is why refs may stay on the narrower source.
	entries, err := os.ReadDir(filepath.Join(app.Paths.GlobalIsolationDir(), tool))
	if err != nil && !os.IsNotExist(err) {
		return nil, false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Matched by path rather than by name, the same rule the fragments are matched by:
		// an account directory whose name does not compose to this store is a different
		// account's home.
		if home := app.Paths.GlobalIsolatedHomeDir(tool, entry.Name()); app.credStoreDir(tool, entry.Name()) == credDir {
			readers = append(readers, credStoreReader{Config: home, Dir: home})
		}
	}
	return readers, true
}

// sharedStoreAttribution answers "whose login is the copy in the account's own credential
// store" by asking every directory that reads it, instead of asking the one directory kae
// happens to be binding.
//
// Four outcomes, and the two mixed ones are the point:
//
//   - every reader that can speak says this account: confirmed.
//   - every reader that can speak says somebody else **and the directory this operation
//     acts for is one of them**: `Conflicting`. The store really does hold another
//     account's credential, this account's is elsewhere, and the bind may replace it —
//     which is the housekeeping re-pin that switches a directory back.
//     That second half is load-bearing and was missing for one round. Without it, a
//     *sibling* directory that had been logged in as somebody else made a brand-new
//     directory's first bind read `Conflicting` — the one refusal that still overwrites —
//     and destroy the only copy of that sibling's login, which the bind before this change
//     had kept. A majority of readers is not different from a majority of one: unless the
//     acting directory is itself a reader that disagrees, this operation is not the event
//     that gets to decide whose the copy is, and it takes the keep branch with everything
//     else that cannot establish an owner.
//   - readers **disagree**: refused, and deliberately *not* `Conflicting`. One reader
//     logged in as somebody else, so the copy is live and somebody's, and this bind is not
//     the event that should decide whose. Overwriting on a majority would destroy a login
//     that has no backup; `kae doctor` reports the disagreeing directory as
//     `identity_drift` and the user resolves it. Measured 2026-08-08: this is the case that
//     filed a foreign token under this account's name. **Nothing outvotes it, including a
//     `kae relogin` that ran the login itself** — see attributionSource for what was tried
//     and what measuring it cost. So the refusal carries the one thing it can: `Disagreeing`,
//     which lets a caller name the remedy instead of only the reason.
//   - nobody can speak (a first bind, an unenumerable index, no cache anywhere yet):
//     refused, missing evidence, so the caller keeps the copy.
//
// What a reader is **not** is an independent observer. A successful bind writes the
// account's recorded identity into that directory's store, so a reader whose tool has
// never run there confirms against a label kae planted — narrower than asking the one
// directory being bound, and not gone. docs/ROADMAP.md § Attribution reads a label kae
// may have written itself owns the residue and the two candidate fixes; do not read the
// outcomes above as "several independent readers agreed".
//
// The last outcome is where the *reason* has to be carried rather than summarised. A
// reader that cannot speak has its own reason for it — no cache, a cache kae cannot read
// as an account record, a cache symlinked out to the real tool home — and an earlier
// version of this answered all of them with one sentence saying no reader had a cache at
// all. That claims something kae did not observe (AGENTS.md), and it sends the user to
// look for a missing file when the file is there and unreadable, which on this path is
// also the only early signal of an upstream format change.
func (app *App) sharedStoreAttribution(ctx context.Context, be secret.Backend,
	tool, credDir string, acc account.Account, src attributionSource,
) harvestRefusal {
	readers, complete := app.credStoreReaders(credDir, tool)
	if !complete {
		return harvestRefusal{
			Why: "kae could not tell which directories read this credential",
		}
	}
	// A reader the caller has already unbound is still a reader for this question — see
	// attributionSource. Appended rather than substituted: an unpin of one of several
	// bindings leaves the others, and they answer first.
	if src.Unbound && src.Dir != "" && !readsFrom(readers, src.Dir) {
		// No Dir: the caller has torn this binding down, so there is no bound directory left
		// to name — and a message that named its store instead would send the reader to a
		// path nothing runs in. The naming below skips it; the count and the vote do not.
		readers = append(readers, credStoreReader{Config: src.Dir})
	}
	confirmed := 0
	var conflict harvestRefusal
	conflicting := []credStoreReader{} // the readers that named another account
	silent := []harvestRefusal{}       // readers that could not speak, and why each could not
	for _, reader := range readers {
		dir := reader.Config
		specs, err := app.dirSpecs(ctx, tool, bindDirs{Config: dir, Cred: credDir})
		if err != nil {
			// One unreadable reader is missing evidence, not a verdict — and it is a
			// reader that could not speak like any other. Dropped silently it made the
			// count below a lie, so a lone reader kae could not even resolve would have
			// reported that *nothing* reads this credential.
			//
			// Untested: dirSpecs fails on a tool with no isolation variable or an adapter
			// error, neither of which a fixture can produce for one reader out of several
			// while the harvest is claude-only. The count is what it protects, so a change
			// to either arm has to keep them in step by reading rather than by a red test.
			silent = append(silent, harvestRefusal{
				Why: "kae could not resolve where one directory that reads it keeps its identity",
			})
			continue
		}
		switch refused := dirIdentityConfirms(ctx, be, specs, acc, dir); {
		case refused.Conflicting:
			conflicting = append(conflicting, reader)
			conflict = refused
		case refused.Why != "":
			silent = append(silent, refused)
		default:
			confirmed++
		}
	}
	switch {
	case confirmed > 0 && len(conflicting) > 0:
		return harvestRefusal{
			Why:         "the directories that read this credential disagree about whose login it is",
			Disagreeing: namedReaders(conflicting, src.Dir),
		}
	case len(conflicting) > 0 && readsFrom(conflicting, src.Dir):
		return conflict
	// A **mode toggle** of one directory does not satisfy that test even though the same
	// directory is the conflicting reader: the reader set is derived from the fragment, which
	// still names the previous mode's config dir, while src.Dir is the new mode's. Left
	// alone deliberately rather than aliased to the previous-mode dir. Aliasing would make
	// the toggle *replace*, and what it would replace is a login with no snapshot anywhere
	// — the same trade AGENTS.md settles by keeping, and the same answer the code before
	// the reader model gave (a fresh isolated config dir has no cache, so attribution
	// refused for missing evidence there too). So this is not a regression; it is an
	// asymmetry with a same-mode re-pin, and docs/ROADMAP.md § A mode toggle records it.
	case len(conflicting) > 0:
		// Every reader that can speak says the copy is somebody else's, and the directory
		// this operation acts for is not one of them — so it has no reading of its own and
		// is not entitled to spend a login that is demonstrably in use somewhere. Worded
		// from the readers rather than from the copy, because what kae observed is a
		// disagreement between this operation and the store's readers.
		return harvestRefusal{
			Why: "the directories that read this credential say it belongs to another account, " +
				"and this directory does not read it yet",
			ForeignToReaders: true,
		}
	case confirmed > 0:
		return harvestRefusal{}
	case len(silent) == 1:
		// One reader, so its own reason is the whole story, and it is the one a user can
		// act on. The count is what makes this safe to say: with a second reader in play
		// the sentence would describe one of them as if it described the store.
		return silent[0]
	case len(silent) > 1:
		return harvestRefusal{
			Why: "no directory that reads this credential could attribute it",
		}
	default:
		return harvestRefusal{
			Why: "no directory reads this credential yet, so nothing can say whose login it is",
		}
	}
}

// readsFrom reports whether configDir is among these readers. Both its callers ask about
// the directory an operation is acting for: whether the walk already saw it, and whether it
// is one of the readers that disagree. Keyed on Config, never on Dir — what ties a reader to
// this operation is the cache attribution read, and the two strings differ for a binding.
func readsFrom(readers []credStoreReader, configDir string) bool {
	return slices.ContainsFunc(readers, func(r credStoreReader) bool { return r.Config == configDir })
}

// namedReaders is the directories among these that a message may name, which is why it
// takes the one it must not: `acting` is the directory the caller is already talking about,
// and naming it as somewhere to go turns "kae cannot confirm the login **in this
// directory**" into advice to go and fix this directory. Reachable — a login as another
// account inside a directory whose sibling still confirms puts the acting directory in the
// conflicting set — and measured naming the cwd before this argument existed.
//
// An unbound caller's own entry carries no Dir and drops out for the same reason: its
// binding is gone, so its store is a path nothing runs in. A caller that finds the list
// empty therefore has nothing to point at, rather than something wrong to point at. That
// half is a statement of intent and **cannot be killed by a test today** (measured): the
// only entry without a Dir comes from the delete path, and no message that path prints
// reads this list. It stays because the arm that would expose it is a new consumer away.
func namedReaders(readers []credStoreReader, acting string) []string {
	dirs := []string{}
	for _, r := range readers {
		if r.Dir != "" && r.Config != acting {
			dirs = append(dirs, r.Dir)
		}
	}
	return dirs
}
