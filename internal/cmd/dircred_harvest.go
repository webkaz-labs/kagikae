package cmd

// Harvesting a directory's credential copy before writing, deleting, renaming
// or superseding it.

import (
	"context"
	"fmt"
	"os"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// harvestBeforeDelete reports whether the credential in credDir may be deleted:
// there is nothing in it to lose, or it is no newer than the account snapshot, or
// it has been harvested into one.
//
// The order is what keeps this from blocking ordinary cleanup. What the store holds
// is asked *first*, because that is answerable without an account: an empty or
// tombstoned item is swept exactly as before, including for an account whose
// snapshot is long gone. Only a store holding something worth keeping needs an
// account to keep it in.
//
// Known reasons to keep an item instead — **not a closed set**, and docs/CLI.md
// § kae pin is the normative list: kae could not read the store, or its parser does
// not recognize the payload (which is what an upstream format change looks like, and
// cannot be told apart from a working login); kae cannot attribute the store to an
// account (storeAccount); the account's snapshot exists but could not be read, so a
// later run may manage it; or the harvest itself refused, which it reports through
// `refused`. The one usable copy that is *deleted* is one no named account holds, and
// only under `purging` — see the branches below for why that turns on what the caller
// was asked to do rather than on the state.
//
// The live read here is repeated inside harvestDirCredential, one extra
// attributes-plus-payload `security` call, and only for the case that has
// something to lose. Reading once would mean threading the payload through the
// harvest's signature for both callers, to save a subprocess in the teardown of a
// bind.
//
// Warnings, not errors: the caller's new binding is already correct, so a store
// kae declines to clean is a leftover secret rather than a broken bind, and a
// warning must never change an exit code.
func (app *App) harvestBeforeDelete(ctx context.Context, be secret.Backend, specs []artifact.Spec,
	tool, accountName string, dirs bindDirs, purging bool,
) (mayDelete bool) {
	// Same pairing rule as harvestDirCredential, which this hands dirs to whole:
	// attribution reads the identity in the config dir, the messages name where the
	// credential is.
	credDir := dirs.credDirOrConfig()
	artName := credentialArtifactName(tool)
	sp, ok := specByName(specs, artName)
	if !rotatesSingleUse(tool) || !ok {
		return true
	}
	switch _, _, state := readLiveCredential(ctx, tool, sp); state {
	case liveNothing:
		return true
	case liveUnreadable:
		// The same asymmetry the account-gone branches below turn on, and for the same
		// reason: it is what the caller was **asked** to do, not what the state is. During
		// housekeeping a payload kae cannot judge is kept — it may be a working login in a
		// shape kae has not been taught, and a bind was not asked to destroy anything. Under
		// `--purge` keeping it strands a secret **nothing kae offers can remove**: the item
		// is named by a per-directory service kae cannot address without the string it
		// hashes from, and this was the only path to it. So the purge takes it and says
		// exactly what it is destroying, which is the loudest kae can be about a copy it
		// could not read.
		if purging {
			// Said before the delete, and it names both limits: this arm runs *before* any
			// attribution, so kae could not tell whose login it was either — the store path
			// carries the account segment, which is all a user has to go on.
			fmt.Fprintf(os.Stderr,
				"kae: warning: kae could not read or date the %s credential in %s — nor tell which "+
					"account it belonged to — so it is deleted without being kept anywhere; if that was "+
					"a working login in a shape kae does not recognize, it is lost\n", tool, credDir)
			return true
		}
		fmt.Fprintf(os.Stderr,
			"kae: warning: kae cannot read or date the %s credential in %s, so it is left in place "+
				"instead of deleted (a payload kae cannot judge may still be a working login); "+
				"if it is spent, run: kae unpin --purge in that directory to remove it — that tears the "+
				"binding down too, so re-bind afterwards\n", tool, credDir)
		return false
	}
	if accountName == "" {
		fmt.Fprintf(os.Stderr,
			"kae: warning: kae cannot tell which account the %s credential in %s belongs to, "+
				"so it is left in place instead of deleted\n", tool, credDir)
		return false
	}
	acc, snapshot, _, err := app.snapshotCredential(ctx, be, tool, accountName, artName)
	switch {
	case exitOf(err) == constants.ExitNotFound && purging:
		// No account kae can **name** holds this copy: the fragment still says
		// `accountName` and there is no such snapshot. Stated as that condition rather
		// than as "nowhere to harvest it, now or ever", which is not the same claim; kae
		// does not track renames, so it cannot tell a rename from a removal here. Only a
		// caller that was *asked* to delete these credentials acts on it: keeping it
		// otherwise strands a live token no kae command can address, while deleting it
		// during housekeeping destroys a login nobody asked kae to touch.
		//
		// It used to add "if that account was renamed rather than removed, re-bind first
		// and it is harvested instead", which was measured false end to end on 2026-08-16:
		// re-binding repoints the fragment, which leaves this store with no reader and the
		// harvest able only to refuse. The rename harvests for itself now
		// (harvestRenamedAccountCredentials), so there is no instruction left to give here
		// — a copy still reaching this arm is one no rename claimed.
		fmt.Fprintf(os.Stderr,
			"kae: warning: account %s/%s no longer exists, so the %s credential this directory held "+
				"for it is deleted without being kept anywhere (%s)\n",
			tool, accountName, tool, credDir)
	case exitOf(err) == constants.ExitNotFound:
		// Same condition, and this is *housekeeping* rather than a purge — the case that
		// used to delete the newest copy of a renamed account's credential. Worded from the
		// fact rather than from the error: snapshotCredential says "is not captured; "
		// followed by a kae add remedy, the wrong instruction for an account the user removed or
		// renamed on purpose.
		//
		// Reached only by a store holding its **own** credential — a pre-split binding, or
		// a tool with no credential variable. Where the credential is the account's
		// (CredDir set), housekeeping returns above at `store.CredDir != "" && !purging`,
		// before the probe and before this function is called at all (measured 2026-08-16).
		// `kae account rename` used to be the routine way into that silence and is not any
		// more: it harvests before its first write, so a copy reaching here is one no rename
		// claimed (harvestRenamedAccountCredentials).
		//
		// The remedy this message names is not the re-bind above: re-binding repoints the
		// fragment at the new account's store, so this copy is left with no reader and the
		// harvest refuses to attribute it. It reads correctly only for a store the *user*
		// re-binds to an account that genuinely holds it.
		fmt.Fprintf(os.Stderr,
			"kae: warning: account %s/%s no longer exists, so the %s credential this directory "+
				"held for it is left in place instead of deleted (%s); to remove it, run: kae unpin --purge, "+
				"or re-bind to the account that holds it now and it is harvested\n",
			tool, accountName, tool, credDir)
		return false
	case err != nil:
		// The account exists and kae could not read its credential snapshot, so a later run
		// may still harvest this copy. Keep it.
		fmt.Fprintf(os.Stderr,
			"kae: warning: could not read snapshot %s/%s to harvest into, so the %s credential in %s "+
				"is left in place instead of deleted (%v)\n", tool, accountName, tool, credDir, err)
		return false
	default:
		_, preserved, refused := app.harvestDirCredential(ctx, be, specs, tool, accountName, acc, dirs, snapshot,
			attributionSource{Dir: dirs.Config, Unbound: purging})
		if !preserved {
			why := refused.Why
			if why == "" {
				why = "kae could not write it into that snapshot"
			}
			fmt.Fprintf(os.Stderr,
				"kae: warning: leaving the %s credential in %s in place instead of deleting it: it is newer "+
					"than snapshot %s/%s and %s\n", tool, credDir, tool, accountName, why)
			return false
		}
	}
	return true
}

// harvestSupersededDirCredentials harvests every per-directory store of one pin
// into the account the binding **being replaced** says it holds, and deletes
// nothing. Call it *before* the new stores are materialized.
//
// It exists because the harvest inside writeDirCredential can only see the store it
// is writing, and the operation that hurts most moves the binding's **credential** to a
// *different* store: a re-bind to another account. There the new store is built from the
// account snapshot while the copy the tool actually refreshed sits in the old store — so
// without this pass the directory the user just bound holds the copy rotation has already
// invalidated, with every offline check green (measured by review, 2026-08-04).
// A `-s` ↔ `-i` toggle is *not* that case since the per-account store landed — both modes
// name the account's own credential store, so a toggle moves the sessions and leaves the
// credential where it is; this comment said otherwise until 2026-08-08 while the same file
// had it right at the chokepoint. The pass still has to walk a toggle, for the credential a
// **pre-split** binding left in the config store being moved off, and for the identity cache
// that makes the chokepoint's attribution possible at all.
//
// It also covers the case the delete sweep gets right and the write path cannot: a
// shared-mode re-bind to *another* account. That store is account-agnostic, so its
// credential belongs to the previous account — and `prev` is the only thing that
// says which. Harvesting it here puts it in **that** account's snapshot before the
// bind overwrites the store with the new account's.
//
// Deliberately not merged into the delete sweep, which must stay *after* the new
// binding is written (AGENTS.md): harvesting is not deleting, and it is only useful
// before. Stores this pin still keeps are included on purpose — one of them is the
// shared store above — so a plain re-pin reads its own store twice, once here and
// once in writeDirCredential. Both callers wrap a keychain read cache, so that second
// read costs a `security` call only when the two resolve **different** stores — the
// migration of a pre-split binding, which happens once per directory
// (TestRunPinCoalescesTheHarvestKeychainReads measures the steady state at one read).
// The duplication buys the case where the two are not the same store.
// harvestRenamedAccountCredentials preserves what is reading tool/accountName's credential
// into that account's **own** snapshot, before `kae account rename` destroys it. Called by
// nothing else: it is the rename's half of the rule that every delete of such a copy
// harvests first (docs/CREDENTIAL-RULES.md § Harvesting before a write or a delete), and
// the rename is a delete of the old account's refs.
//
// It harvests into the **old** name, and that is the whole design. The rename's own copy
// stage then carries the result to the new name, so nothing here reasons about an account
// that does not exist yet, and an abort between the two leaves the copy safe under the name
// it already had. Attribution works for the same reason, and docs/CREDENTIAL-RULES.md
// § Never harvest a copy you cannot attribute states the rule this follows from.
//
// **`kae account rm` deliberately does not share this.** A harvest persists into
// `acc.Artifacts[artName].SecretRef`, which is the ref `rm` is deleting, so preserving
// there would mean re-creating the account the user asked to destroy. The rename is the
// route with a live destination, which is why this is its function and not a seam with one
// real implementation and one that must do nothing.
//
// Before the rename's first write, never after: harvesting is not deleting and the two
// belong on opposite sides of the write (docs/CREDENTIAL-RULES.md § A chokepoint is not
// complete coverage). The dry run returns above the call, so this stays a read.
//
// **Two store shapes, and a walk of the bound directories only finds one of them.** The
// account's own credential store is read by every bound directory *and* by a globally
// isolated home, which has no fragment and no pin record — so `kae use -i claude main`
// followed by a rename lost the copy while a pin walk reported nothing to do (measured
// 2026-08-16). credStoreReaders is the enumeration that spans both, so the first half asks
// it rather than walking pins. The second half is for a copy that lives *inside* a
// per-directory store — a binding from before the credential split, or a tool with no
// credential variable — which is per directory by construction.
func (app *App) harvestRenamedAccountCredentials(ctx context.Context, be secret.Backend, tool, accountName string) {
	artName := credentialArtifactName(tool)
	if !rotatesSingleUse(tool) || artName == "" {
		return
	}
	// The account's own credential store. One copy however many directories read it, so it
	// is harvested once; a reader supplies the config dir because dirSpecs resolves the
	// artifacts against one, and handing it the credential store instead is the silent swap
	// harvestDirCredential's own doc opens with.
	if credDir := app.credStoreDir(tool, accountName); credDir != "" {
		// The three answers are different and only one of them is "do nothing quietly".
		// `complete` is **machine-wide**: credStoreReaders returns `(nil, false)` when any pin
		// record anywhere is unreadable, so a single stale store elsewhere would otherwise
		// make this whole half skip in silence — the shape this branch exists to remove, one
		// level up. An empty *and* complete answer is different: nothing on this machine
		// points at that store, so a copy in it was already unreachable before the rename and
		// doctor's pin_stale owns it.
		readers, complete := app.credStoreReaders(credDir, tool)
		switch {
		case len(readers) > 0:
			// attributionSource stays zero on purpose: its Dir names the directory a bind is
			// *acting for*, and a rename acts for none. Leaving it empty asks the readers alone
			// whose the copy is, which is the question here — storeHoldsAccount asks it the
			// same way.
			app.harvestRenamedStore(ctx, be, tool, accountName, artName,
				bindDirs{Config: readers[0].Config, Cred: credDir})
		case !complete:
			fmt.Fprintf(os.Stderr,
				"kae: warning: kae could not tell what reads the %s credential for %s/%s, so it did not "+
					"harvest it before the rename; if a bound directory or an isolated home held a newer "+
					"copy it stays under the old name (%s)\n", tool, tool, accountName, credDir)
		}
	}
	// A copy inside a per-directory store, which only the bound directories have.
	index := app.boundDirectoryIndex()
	if index.err != nil {
		fmt.Fprintf(os.Stderr, "kae: warning: %v\n", index.err)
		return
	}
	for _, pin := range index.directories {
		info, exists, ferr := pin.readFragment()
		if ferr != nil || !exists || info.Accounts[tool] != accountName {
			continue
		}
		stores, serr := app.dirCredentialStores(pin.PinID, info)
		if serr != nil {
			fmt.Fprintf(os.Stderr, "kae: warning: %v\n", serr)
			continue
		}
		for _, store := range stores {
			// CredDir set means the copy is the account's, which the first half already took;
			// harvesting it again here would re-read it once per bound directory.
			if store.Tool != tool || store.CredDir != "" || storeAccount(store, info) != accountName {
				continue
			}
			app.harvestRenamedStore(ctx, be, tool, accountName, artName, store.dirs())
		}
	}
}

// harvestRenamedStore is one store's worth of the pass above, so its two halves cannot
// answer the same question differently — which is the drift docs/CREDENTIAL-RULES.md
// § A chokepoint is not complete coverage records twice for two hand-kept copies.
//
// The `dirSpecs` → `snapshotCredential` → `harvestDirCredential` order is the same one
// harvestSupersededDirCredentials runs inline; the two are second copies of one shape and
// not third, because harvestBeforeDelete reads live first on purpose. Left as two: they
// differ in attribution source, in control flow and in messaging, so a shared helper would
// hand all three back to its callers for about the lines it saved. **A third hand-written
// copy is where that stops being true.**
func (app *App) harvestRenamedStore(ctx context.Context, be secret.Backend,
	tool, accountName, artName string, dirs bindDirs,
) {
	specs, err := app.dirSpecs(ctx, tool, dirs)
	if err != nil {
		return // an unresolvable store is not this command's to report
	}
	acc, snapshot, _, err := app.snapshotCredential(ctx, be, tool, accountName, artName)
	if err != nil {
		return // the account being renamed has no readable credential to beat
	}
	_, preserved, refused := app.harvestDirCredential(ctx, be, specs, tool, accountName, acc,
		dirs, snapshot, attributionSource{})
	if preserved || refused.Why == "" {
		return
	}
	// The rename is not stopped by this — it renames either way and the copy stays where it
	// is. So the warning has to say that re-binding will not recover it, which is what kae
	// used to imply and never did.
	//
	// The shared clause carries the frame, and hand-writing the prefix here was a fourth
	// copy of it: this pass can refuse *short* of the `supersedes` gate (a payload kae could
	// not read or date), where "is newer than snapshot" is a claim kae has not established
	// and contradicts the reason printed beside it.
	fmt.Fprintf(os.Stderr,
		"kae: warning: %s; the rename leaves that copy under the old name, and re-binding "+
			"will not reach it\n",
		dirCredentialRefusalClause(tool, dirs, accountName, refused))
}

// next maps each tool to the pair the binding being written will point it at — read from
// the plan kae is about to apply, not derived from an account here. Both halves are used
// and for different reasons.
//
// Cred is what makes "this bind replaces it" a fact rather than a guess: a re-bind to
// another account writes a *different* store, so the copy this pass is talking about is
// abandoned rather than replaced, and predicting a replacement tells the user a live login
// is being spent when it is being stranded.
//
// Config is the directory the *write* will act for, and passing it here is what keeps this
// pass and writeDirCredential from answering the same question differently. They did: a
// `-s` ↔ `-i` toggle changes the config dir, so the pass (acting under the old one, which
// is a reader) said `Conflicting` and predicted a replacement while the write (acting under
// the new one, which is not) kept the copy — the message was the exact inverse of what
// happened. Measured 2026-08-08.
//
// A tool absent from the map is one the new binding does not bind, which replaces nothing.
// An empty Cred means the tool has no credential store separate from its config dir, where
// a same-mode re-pin *does* replace the copy and the write never keeps — unreachable while
// the harvest is claude-only (rotatesSingleUse), and it unlocks with the second measured
// tool, which docs/ROADMAP.md § Rotation is measured for claude only already gates.
func (app *App) harvestSupersededDirCredentials(ctx context.Context, be secret.Backend,
	pinID, dir, onlyTool string, prev fragmentInfo, next map[string]bindDirs,
) {
	// Cleared here, not left to live as long as the App: the coordination with
	// writeDirCredential's backstop is scoped to **one bind**, and a stale entry
	// suppresses a report the next bind owes. Production builds an App per command so
	// this never showed, and it made a test pass for the wrong reason — its second bind
	// was silenced by the first one's mark (measured, 2026-08-04).
	app.refusalReported = nil
	stores, err := app.dirCredentialStores(pinID, prev)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kae: warning: %v\n", err)
		return
	}
	// Which stores this operation is actually moving away from: the ones the binding
	// being replaced pointed at. Every other store the walk returns is history, and a
	// warning about one would name a store the command does not touch.
	replaced := map[string]bool{}
	for tool := range prev.Accounts {
		if storeDir, ok := app.boundStoreDir(pinID, tool, prev); ok {
			replaced[storeDir] = true
		}
	}
	for _, store := range stores {
		// Scoped like the sweep: a single-tool re-bind must not speak about a sibling
		// tool's store, which the same fragment still binds. **Unobservable today** and
		// written down so nobody tests it or deletes it: only claude harvests, so a store
		// of any other tool returns at the `rotatesSingleUse` gate below anyway. It stops
		// being unobservable the moment a second tool's rotation is measured.
		if onlyTool != "" && store.Tool != onlyTool {
			continue
		}
		accountName := storeAccount(store, prev)
		if accountName == "" {
			// Nothing kae can read attributes it. The delete sweep says so — but only where
			// there is an item to delete, so on a file store (Linux, or the file driver) a
			// leftover shared store's usable copy is left without anyone mentioning it.
			// Nothing is destroyed either: the store directory stays, so the binding that
			// created it can still reach that copy.
			continue
		}
		// The pure gates first: resolving specs is not free (codex under
		// `cli_auth_credentials_store = "auto"` probes the keychain), so a store this pass
		// can never harvest must cost nothing.
		artName := credentialArtifactName(store.Tool)
		if !rotatesSingleUse(store.Tool) || artName == "" {
			continue
		}
		specs, err := app.dirSpecs(ctx, store.Tool, store.dirs())
		if err != nil {
			continue // an unresolvable store is the bind's problem, and it reports it
		}
		acc, snapshot, _, err := app.snapshotCredential(ctx, be, store.Tool, accountName, artName)
		if err != nil {
			continue // no snapshot to harvest into; the bind or the sweep reports it
		}
		_, _, refused := app.harvestDirCredential(ctx, be, specs, store.Tool, accountName, acc, store.dirs(), snapshot,
			// Attribution only: whether a label is stale feeds the retract, which lives in
			// writeDirCredential and takes it as its own argument. Nothing here reads it.
			attributionSource{Dir: next[store.Tool].Config})
		// The one question both arms below need, asked once: does the write this bind is
		// about to perform land on the very store being reported? Only then is the copy
		// replaced — and only then may a message say so. A refusal that keeps is never a
		// replacement whatever the locations say, which is the other half.
		replacedNow := !refused.Unattributed &&
			next[store.Tool].Cred != "" && next[store.Tool].Cred == store.dirs().credDirOrConfig()
		// Chosen once for the same reason it is asked once: the two arms below said this in
		// two hand-kept copies, and this function's own history is one wording drifting in
		// the unreadable arm and the other in the replaced one. Both measured.
		consequence := "so it is left in place"
		if replacedNow {
			consequence = "and this bind replaces it"
		}
		switch {
		case refused.Why == "" || !replaced[store.Dir]:
			// Nothing to report, or a store from a binding older than the one being replaced
			// — the walk returns those forever (kae keeps a store so a re-pin restores its
			// sessions) and this operation does not touch them. Whatever this pass does not
			// report, writeDirCredential does for the store it writes; the two coordinate
			// through app.refusalReported rather than by guessing about each other.
			continue
		case refused.Conflicting:
			// The copy is demonstrably not this account's, so no login remedy: this
			// account's credential is about to be written correctly, and logging it in
			// again would mint a chain invalidating what kae harvests elsewhere.
			// Reaching here means the binding is moving off this store, so no login remedy
			// is owed on top of the fact: the copy is demonstrably not this account's, and
			// the directory is about to be bound to a credential that is fine.
			app.markRefusalReported(store.dirs().credDirOrConfig())
			// Named by where the credential is, not by the config dir: for a split binding
			// those are different directories, and every other speaker in the dircred files was moved
			// to credDirOrConfig for exactly that reason. And the consequence is measured
			// rather than assumed — on `kae pin <tool> <other account>` this store is the one
			// the binding moves *off*, so nothing replaces the copy in it.
			fmt.Fprintf(os.Stderr,
				"kae: warning: the %s credential in %s belongs to an account other than %s/%s (%s), so "+
					"kae is not harvesting it, %s\n",
				store.Tool, store.dirs().credDirOrConfig(), store.Tool, accountName, refused.Why, consequence)
		default:
			// Missing evidence rather than a conflict: the copy may well be this account's.
			// The remedy is right here, because dir is the bound directory rather than the
			// store — and the *consequence* is whichever one the write will actually apply,
			// read from the same predicate the write uses. A per-account store is kept (every
			// directory bound to that account reads it, so overwriting it is not this
			// directory's call); a per-directory one is still replaced, which is what binds
			// this directory to the account it names.
			app.markRefusalReported(store.dirs().credDirOrConfig())
			// The consequence has to be the one the write applies, and neither wording is right
			// on its own. The copy stays where it is when the write keeps it, and also when this
			// store is not the one the write touches — a pre-split store, whose copy is left
			// alone because the write goes to the account's store instead. Otherwise the write
			// really does replace it, and saying anything softer would imply a copy survived
			// that kae could not back up, which AGENTS.md forbids. One fixed string broke that
			// in the unreadable arm; keying it on this store's own dirs broke the other
			// direction, claiming a replacement of a copy nothing replaced. Both measured.
			fmt.Fprintf(os.Stderr,
				"kae: warning: kae could not preserve the %s credential this directory held for %s/%s "+
					"(%s), %s; %s\n",
				store.Tool, store.Tool, accountName, refused.Why, consequence, pinLoginRemedy(store.Tool, dir))
		}
	}
}

// harvestDirCredential copies the credential live in credDir into acc's snapshot
// when the live copy is the newer of the two, so the caller may then overwrite or
// delete that store without destroying a login.
//
// It exists because kae's architecture is copies with lazy sync while claude's
// refresh token rotates single-use: of all the copies of one account's credential
// only the one that refreshed last can still refresh, and the tool refreshes the
// copy *inside* the bound directory, in place, at a moment no kae command is
// running. So writing the account snapshot over that copy does not regress the
// directory to an older login, it logs it out — reporting success, with every
// offline check green, until the tool fails up to an access token's ~8h later
// (docs/VALIDATION.md owns the measurement; docs/ROADMAP.md § Every credential
// copy owns the design).
//
// Ordering by `expiresAt` is sound because a successful refresh always moves it
// forward and a failed one tombstones the copy to zero — and a fresh login also
// sorts ahead of an older chain, since it sets the field to now plus the access
// token's life. What it cannot do is compare two logins that are both alive,
// which is why attribution (dirIdentityConfirms), not the timestamp, is the guard
// that keeps this from filing one account's token under another's name.
//
// It answers three separate questions, because its three callers need different ones.
// newest is the payload the caller should write. preserved is false when a copy
// worth more than the snapshot is only in this store — **a caller about to delete
// the store must not proceed on it**. refused carries the reason a newer copy was
// left where it is, for the caller to report along with what its own next write or
// delete costs; refused.Why is empty when there was nothing to refuse, including the
// case where kae writes the live copy back after a failed snapshot write.
// harvestRefusal is why a newer copy was not harvested. Conflicting separates the one
// reason that is *positive evidence* — the copy demonstrably belongs to another
// account — from every reason that is merely missing evidence, because they license
// different things to say: a conflicting copy means this account's own credential is
// fine and telling the user to log in again would be wrong (and would mint a chain
// that invalidates what kae just harvested), while missing evidence means the copy may
// well be this account's and the directory may need a login.
type harvestRefusal struct {
	Why         string
	Conflicting bool
	// Unattributed marks the one refusal that says nothing about the payload itself:
	// kae read a usable, newer copy and could not establish *whose* it is. Set
	// positively rather than inferred from `!Conflicting`, because the other reasons
	// are about the payload (unreadable, undatable) and take a different answer — the
	// distinction is what keeps a caller from folding three states into two.
	Unattributed bool
	// ForeignToReaders marks the one *keeping* refusal where kae has positive evidence
	// about the copy: every directory that reads the store says it is another account's,
	// and the directory being bound is not one of them, so it has no reading of its own to
	// weigh against theirs. It keeps like every unattributed refusal, but it is the only
	// one where kae can say what the directory will do next — run that other account — and
	// a success line with no such sentence reads as "kae protected my credential".
	ForeignToReaders bool
	// Disagreeing names the directories that produced a *reader disagreement*: they read this
	// account's credential store and say the copy is somebody else's, while another reader
	// says it is this account's. It is not a fourth kind of refusal — it keeps the copy like
	// every other missing-evidence one — and it carries the directories because this is the
	// one refusal here whose cause is somewhere the user can go and fix, and `Why` alone
	// leaves them to find which of their directories it was.
	//
	// A list rather than a flag, and out of `Why` rather than inside it: the reason is
	// interpolated into frames that several callers build, and a path spliced into it would
	// appear in all of them whether or not that message is one a user can act on. Reading
	// it is opt-in instead — `kae relogin` is the only caller that does today, and the bind
	// path could without anything here changing. Empty for every other refusal, so a
	// consumer that reads it as a flag reads the truth.
	//
	// **Not routed through `kae doctor`**, which is where the first wording sent the user:
	// its identity checks cover bound directories (`pinIdentityChecks`) and the *active*
	// account's real home, so a drifted **globally isolated home** — a reader by the same
	// walk, and reachable with no sibling worktree at all — is reported by neither.
	Disagreeing []string
	// Ordered records that kae **established** the copy in the store is newer than the
	// snapshot, i.e. that the refusal happened past the `supersedes` gate. It is a fact
	// about what kae measured, not about the refusal's kind, and it exists because the
	// messages interpolate `Why` into a frame: one of the reasons is kae saying it
	// *cannot* read or date that copy, so a frame calling it newer contradicts the
	// reason four words later — the fold docs/CLI.md § `kae rollback --json` is
	// normative against, measured on `kae use -i` (2026-08-08) and corrected once
	// before in captureBackAfterRelogin.
	//
	// Set where the fact is known and read by the formatter, rather than re-derived at
	// the call site as `Unattributed || Conflicting`: that expression is true today and
	// is one new refusal away from being wrong, which is the shape this file has been
	// bitten by (keepsUnattributedCopy says the same about its own predicate).
	Ordered bool
}

// keepsUnattributedCopy reports whether a refusal leaves the newer copy where it is
// instead of overwriting it with the snapshot. Its one caller is writeDirCredential, which
// performs the keep.
//
// A named predicate rather than an inline condition because the shape it encodes is the one
// this area keeps getting wrong: `refused.Unattributed` and `!refused.Conflicting` are not
// the same question, and writing the second by hand at a call site is how the conflicting
// refusal — which must still overwrite — ended up on the keep branch.
//
// The pin-level pass reads the **flag**, not this predicate, and the difference is the
// point: it has to say what happens to a store the write may not be touching at all, which
// this predicate says nothing about.
func keepsUnattributedCopy(refused harvestRefusal, dirs bindDirs) bool {
	return refused.Unattributed && dirs.Cred != ""
}

// dirCredentialRefusalClause is the half every refusal message shares: which credential,
// where it is, which snapshot it was not harvested into, and why. Extracted at the second
// occurrence rather than the third — a prefix kept by hand in two places is one edit away
// from describing the store by two different names — and `kae relogin`'s pre-flight is the
// third caller it was extracted for.
//
// **Two frames, and which one is used is a measurement, not a wording choice.** Past the
// `supersedes` gate kae has established the copy is newer than the snapshot, and saying so
// is the most useful thing it knows. Short of it — the copy kae could not read or date —
// it has established no such thing, and the older single frame said "is newer than
// snapshot" beside a reason that reads "kae cannot read or date the copy already there".
// Measured on `kae use -i`, where no pin-level pass speaks first to suppress it.
func dirCredentialRefusalClause(tool string, dirs bindDirs, accountName string, refused harvestRefusal) string {
	if refused.Ordered {
		return fmt.Sprintf(
			"the %s credential already in %s is newer than snapshot %s/%s and kae is not harvesting it because %s",
			tool, dirs.credDirOrConfig(), tool, accountName, refused.Why,
		)
	}
	return fmt.Sprintf(
		"kae is not harvesting the %s credential already in %s into snapshot %s/%s because %s",
		tool, dirs.credDirOrConfig(), tool, accountName, refused.Why,
	)
}

// attributionSource is what the caller knows about the directory it is acting for that a
// walk of the fragments on disk cannot supply. Every field exists because a reader set built
// only from that walk answers the wrong question at one end or the other.
//
// Dir decides whether a store all of whose readers name **another** account may be
// overwritten: only if this directory is one of those readers. A sibling's disagreement is
// evidence that the copy is a live login of somebody's, not a licence for an unrelated
// bind to spend it.
//
// Unbound says the caller has already removed that directory's binding, so the walk cannot
// see it — and only a caller that really did tear it down may say so, which is why the
// delete path passes its own `purging` rather than a literal: `harvestBeforeDelete` is also
// reached at bind time, where the directory is still very much bound, and a hardcoded true
// there would have rested on a `!purging` early return two functions away. The delete path must,
// because its own precondition erases its evidence: a per-account store may be deleted only
// once nothing points at it, and the readers are enumerated from the same source, so by the
// time the delete is allowed there is by construction no reader left to attribute the copy
// it is about to destroy. Refusing there is a deletion rather than a conservative choice,
// which is the inversion AGENTS.md records.
//
// **Do not add a third field saying "kae itself just ran a login in Dir".** One was
// written, and reverted after a review reproduced it filing a sibling's token under this
// account: nothing kae can read offline separates a tool that logged in here from a tool
// that merely wrote its cache here. docs/ADAPTERS.md § Per-directory credential store
// carries the measurement, and docs/ROADMAP.md § `kae relogin` declines to capture a login
// it watched happen is the entry that asked for the field.
type attributionSource struct {
	Dir     string
	Unbound bool
}

func (app *App) harvestDirCredential(ctx context.Context, be secret.Backend, specs []artifact.Spec,
	tool, accountName string, acc account.Account, dirs bindDirs, snapshot []byte, src attributionSource,
) (newest []byte, preserved bool, refused harvestRefusal) {
	// The two halves of dirs are not interchangeable here, and swapping them is
	// silent. Attribution reads the identity cache, which lives in the **config**
	// dir whatever the credential does; handing it the credential store instead
	// makes every identity target look like one that escapes its store, so the
	// harvest refuses every time and each bind overwrites the copy it came to
	// preserve. The messages name the credential's own location, because that is
	// where a reader would go looking for it.
	credDir := dirs.credDirOrConfig()
	artName := credentialArtifactName(tool)
	sp, ok := specByName(specs, artName)
	if !rotatesSingleUse(tool) || !ok {
		return snapshot, true, harvestRefusal{}
	}
	// The same gate the write and the delete apply, here so all three callers inherit
	// it rather than each remembering. Unreachable today (claude's item always moves
	// with its isolation variable), and it stops being unreachable the moment a second
	// tool is measured: a bound directory's codex store resolves the **global**
	// `Codex Auth` item, and reading that would harvest a global login into the store's
	// account snapshot — the exact defect the write gate exists for.
	if unbindableDirKeychain(sp) {
		return snapshot, true, harvestRefusal{}
	}
	// The same pair checkPayloadShape refuses a bind on. Here a mismatch is not a
	// refusal, it just means the live payload cannot be stored as this snapshot's
	// artifact, so there is nothing to harvest.
	if checkPayloadShape(tool, accountName, artName, acc.Artifacts[artName].Kind, sp.Kind) != nil {
		return snapshot, true, harvestRefusal{}
	}
	liveData, liveInfo, state := readLiveCredential(ctx, tool, sp)
	switch state {
	case liveNothing:
		return snapshot, true, harvestRefusal{}
	case liveUnreadable:
		// preserved=false stops a delete; the reason is returned so an *overwrite* is
		// reported too. Only the delete path used to hear about this, which left the
		// asymmetry that `kae pin` / `use -i` / `run -i` would silently overwrite a login
		// in a payload shape kae has not been taught while `unpin --purge` protected it.
		// A store kae cannot read is also the only early signal of an upstream format
		// change on this path: `upstream_version` skips a version string it cannot parse.
		// preserved=false here is only *observable* through a race: the delete path reads
		// the store itself before calling in, so reaching this from there means the tool
		// rewrote the payload in between — where keeping the item is still the right
		// answer. Written as the state it is rather than folded away, so the next reader
		// does not remove it as dead.
		return snapshot, false, harvestRefusal{
			Why: "kae cannot read or date the copy already there, and a payload kae cannot judge may still be a login",
		}
	}
	// A snapshot kae cannot read, or one that is itself a tombstone, loses to any
	// usable live copy: it has no deadline worth comparing, and writing it over a
	// working credential is the destruction this function exists to stop. The
	// live-side guard supersedes applies is redundant *here* — readLiveCredential
	// already refused a payload that is unknown, revoked or undated — and it lives
	// there rather than being split between the two, so a caller whose live read is
	// not that one (the backup-restore paths) cannot get it wrong.
	if !supersedes(liveInfo, freshnessOf(tool, snapshot)) {
		return snapshot, true, harvestRefusal{}
	}
	// Which evidence answers this depends on what the store is. For the account's own
	// credential store, the readers are the evidence (sharedStoreAttribution); for a
	// per-directory store the credential and the cache are in one directory, so that
	// directory is. Asking the bound directory about a shared store destroyed a live
	// credential on a re-bind and mis-filed a foreign token on an ordinary re-pin
	// (docs/ADAPTERS.md § Per-directory credential store is normative for both).
	attribution := func() harvestRefusal {
		if dirs.Cred != "" {
			return app.sharedStoreAttribution(ctx, be, tool, dirs.Cred, acc, src)
		}
		return dirIdentityConfirms(ctx, be, specs, acc, dirs.Config)
	}
	if refused := attribution(); refused.Why != "" {
		// Reported by the caller, not here. Two harvests can look at one store in a
		// single command (the pin-level pass and this chokepoint), so printing at the
		// point of detection said the same thing twice — measured, 2026-08-04 — and only
		// the caller knows what its own next write or delete costs anyway.
		//
		// Marked as the attribution refusal on the way out: everything above this line
		// refused on what the payload *is*, and this one refuses on what kae could not
		// learn about a payload it read perfectly well. writeDirCredential keeps the copy
		// for this reason and no other.
		//
		// `!Conflicting` is not decoration: dirIdentityConfirms answers *both* questions,
		// and marking every one of its refusals unattributed put the conflicting case —
		// the one that must still be overwritten, because the copy is provably somebody
		// else's — on the keep branch, so a re-bind silently did not switch the store. Two
		// tests caught it; it is the same "took a subset of the predicate" shape AGENTS.md
		// records, one condition off, in the fix for that shape.
		if !refused.Conflicting {
			refused.Unattributed = true
		}
		// Reaching here means the `supersedes` gate above passed, so the ordering is
		// something kae measured rather than assumed. Recorded on the way out, at the one
		// point that knows it, because the formatter downstream has no way to tell this
		// refusal from the un-orderable one otherwise.
		refused.Ordered = true
		return snapshot, false, refused
	}
	if err := be.Set(ctx, acc.Artifacts[artName].SecretRef, liveData); err != nil {
		fmt.Fprintf(os.Stderr,
			"kae: warning: could not harvest the newer %s credential from %s into snapshot %s/%s: %v\n",
			tool, credDir, tool, accountName, err)
		// The payload is still the one to write: putting it back where it already is
		// preserves the working login even though the snapshot missed out. So nothing is
		// lost by the write — only by a delete, which preserved=false stops.
		return liveData, false, harvestRefusal{}
	}
	app.recordHarvestTime(tool, accountName)
	fmt.Fprintf(os.Stderr,
		"kae: harvested the newer %s credential from %s into snapshot %s/%s (it is the copy that can still refresh)\n",
		tool, credDir, tool, accountName)
	return liveData, true, harvestRefusal{}
}

// markRefusalReported records that a refusal for this store has already been printed,
// so writeDirCredential's backstop does not repeat it. Per command: an App is built per
// command and this is only ever written from the pin-level pass, which runs before any
// materializer.
func (app *App) markRefusalReported(storeDir string) {
	if app.refusalReported == nil {
		app.refusalReported = map[string]bool{}
	}
	app.refusalReported[storeDir] = true
}

// recordHarvestTime moves the account's captured_at to now, because the payload
// under it just changed and that field is what `kae ls` and `kae status` show for
// this snapshot (the global recapture refreshes it through persistSnapshot for the
// same reason).
//
// It **re-reads the account rather than saving the copy the harvest loaded**, which is
// the seam rule App.mutateState states for state.json; docs/ARCHITECTURE.md § Locking
// owns why it applies here. The half to keep in view while editing this function: the
// *missing* case must not fall through to a save, because account.Save begins with
// MkdirAll and would resurrect an account.toml whose payloads a concurrent
// `kae account rm` is already deleting.
//
// Only the date rides on this write, so every failure is a warning: the credential
// itself is already in the secret store.
func (app *App) recordHarvestTime(tool, accountName string) {
	dir := app.Paths.AccountDir(tool, accountName)
	acc, found, err := account.Load(dir)
	if err != nil || !found {
		if err != nil {
			warnCaptureTimeNotUpdated(tool, accountName, err)
		}
		return
	}
	acc.CapturedAt = app.Now().UTC()
	if err := account.Save(dir, acc); err != nil {
		warnCaptureTimeNotUpdated(tool, accountName, err)
	}
}

// warnCaptureTimeNotUpdated reports a harvest that kept the credential but could not stamp the snapshot.
func warnCaptureTimeNotUpdated(tool, accountName string, err error) {
	fmt.Fprintf(os.Stderr,
		"kae: warning: harvested the %s credential for %s/%s but could not update its capture time: %v\n",
		tool, tool, accountName, err)
}

// rotatesSingleUse reports whether tool's refresh token is measured to rotate
// single-use — whether a newer copy of one account's credential *invalidates*
// the older copies of it, rather than merely being newer than them. That fact is
// what makes "keep the newest copy" a rule instead of a coin flip, and it is why
// the harvest exists at all.
//
// claude only, because claude is the only tool whose rotation has been measured
// ([docs/VALIDATION.md] § Upstream Behaviour Assumptions;
// docs/ROADMAP.md § Rotation is measured for claude only). Adding a tool here
// without that measurement would have kae choose between two copies on a guess
// and destroy the working one — the same class of defect every "never declare an
// artifact for a location you could not measure" refusal in the dircred files prevents.
func rotatesSingleUse(tool string) bool { return tool == constants.ToolClaude }
