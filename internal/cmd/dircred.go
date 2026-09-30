package cmd

// Writing a bound directory's credential copy, and the harvest of a newer live
// copy that precedes each write.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// errGlobalCredentialStore reports that kae cannot give a bound directory its own
// copy of a tool's credential store, so it must not write one. The store may be
// genuinely global, or scoped in a way kae has not verified for a bound directory
// (codex's keyring item, scoped by an account derived from CODEX_HOME) — either
// way the safe action is the same, and only the adapter may declare otherwise.
//
// Callers differ, matching how a tool with no isolation env var is already
// handled: binding a *set* of tools warns and carries on (the others still bind,
// and the tool's non-auth state is still isolated), while an operation naming the
// tool refuses.
var errGlobalCredentialStore = errors.New("credential store is not per-directory")

// warnUnisolatableCredential reports whether err is a per-directory credential
// limitation the caller may continue past, printing the warning when it is.
// Emitted here so it precedes the fragment or state write it qualifies, and it
// never changes an exit code.
//
// Only for operations that bind a set of tools resolved from a profile. An
// operation naming one tool and account must let the error through: there the
// unisolatable tool is the whole request, not one row of it.
func warnUnisolatableCredential(err error, tool, account string) bool {
	switch {
	case errors.Is(err, errGlobalCredentialStore):
		// Not "shares the global login": for the one tool that reaches this today
		// (codex under the keyring store) the bound directory resolves a *different*
		// keychain item, so it starts out with no login at all. Say that, and name
		// the fix the user can actually apply.
		fmt.Fprintf(os.Stderr,
			"kae: warning: kae cannot bind %s's credential to this directory, so %s may have no login "+
				"here until you log in inside it (its settings and sessions are still isolated)\n", tool, tool)
		return true
	case exitOf(err) == constants.ExitNotFound || exitOf(err) == constants.ExitAuthMissing:
		fmt.Fprintf(os.Stderr,
			"kae: warning: %s/%s has no captured credential, so this directory binds %s without one; "+
				"%s; then re-run the binding command\n",
			tool, account, tool, verifiedCaptureRemedy(tool, account))
		return true
	}
	return false
}

// writeDirCredential materializes one captured account's credential for a
// per-directory bind, at the location the tool bound to configDir will actually
// read it — and then the identity cache that names it (writeDirIdentity).
//
// The name stays "credential" because every sibling in this file means the same
// thing by it, and only this one grew a second step. The two are not separable
// from a caller's point of view: a bind that switches the credential without the
// identity leaves the directory displaying the previous account, which is the
// defect writeDirIdentity exists to close.
//
// It is the single answer to "where does a bound directory's credential go",
// and it has to be single: that copy used to be written in three places (both
// `kae pin` materializers and the re-bind path), which is how two defects lived
// here at once. Two of the three read the *live* store instead of the account's
// snapshot, so pinning an account that was not currently active seeded the
// directory with whichever credential happened to be live. And all three wrote a
// plaintext file that claude stops reading the moment it namespaces its keychain
// item by the config dir.
//
// The location comes from the adapter, never from this function: resolving the
// specs against an env whose isolation variable already points at configDir yields
// the per-directory keychain service name and the per-directory file path alike.
// Recomputing either here is what let kae's model of the credential's location
// drift away from the tool's in the first place.
//
// A keychain write that fails is returned, never downgraded to a plaintext
// write. The fallback would look like success and reproduce the original defect:
// a credential file in a directory whose tool reads the keychain first.
func (app *App) writeDirCredential(ctx context.Context, be secret.Backend, tool, accountName, configDir string,
	staleLabel bool,
) error {
	artName := credentialArtifactName(tool)
	if artName == "" {
		return nil // the tool has no credential kae materializes per directory
	}
	// Where this account's credential goes, which for a tool that can separate the
	// two is *not* configDir: one store per account, shared by every directory bound
	// to it. Created here because the file driver writes into it and because the
	// sweeps walk the tree to find what exists; the keychain driver needs no
	// directory, and making one anyway keeps the two drivers' layouts comparable.
	dirs := bindDirs{Config: configDir, Cred: app.credStoreDir(tool, accountName)}
	if dirs.Cred != "" {
		if err := os.MkdirAll(dirs.Cred, 0o700); err != nil {
			return fmt.Errorf("create per-account credential store: %w", err)
		}
	}
	// Resolved once for both halves. Asking the adapter twice is not free: codex
	// under `cli_auth_credentials_store = "auto"` probes the keychain to decide which
	// store it is on, so a second resolution is a second `security` subprocess per
	// bind (and a second read of its config.toml).
	specs, err := app.dirSpecs(ctx, tool, dirs)
	if err != nil {
		return err
	}
	sp, ok := specByName(specs, artName)
	if !ok {
		return nil // no such artifact on this platform
	}
	// Writing a keychain item for a bound directory is only isolation if the item
	// belongs to that directory, and the adapter is what declares that its item
	// moves with the isolation variable. Anything else is refused before touching
	// the keychain; the caller decides whether one unisolatable tool is fatal.
	//
	// codex is the case that shows why the declaration is per-adapter and defaults
	// to false. Its item *is* scoped by CODEX_HOME — through the account attribute,
	// not the service name — and codex is now measured resolving a bond-dir-shaped
	// path (symlink included) to the same canonical path kae hashes. What the
	// capability still waits on is the pin round-trip on a real machine
	// (docs/ROADMAP.md), so it stays undeclared rather than assumed.
	if unbindableDirKeychain(sp) {
		return fmt.Errorf("%w: kae cannot give this directory its own %s credential store (%s)",
			errGlobalCredentialStore, tool, isolationEnvVar(tool))
	}
	acc, data, storedKind, err := app.snapshotCredential(ctx, be, tool, accountName, artName)
	if err != nil {
		return err
	}
	if err := checkPayloadShape(tool, accountName, artName, storedKind, sp.Kind); err != nil {
		return err
	}
	// The copy already in this store can be *newer* than the snapshot, and for a
	// tool whose refresh token rotates single-use that makes the overwrite below
	// destructive rather than merely regressive. Harvest before writing, and write
	// whichever copy is newest.
	//
	// This covers the store being written; it cannot see a *sibling* store of the same
	// bound directory, which is what a re-bind to another account moves the credential
	// away from — and, for a binding that predates the per-account credential store, the
	// config store a mode toggle moves off. (A toggle for the same account moves the
	// sessions only: both modes name that account's credential store.) That is the pin-level pass
	// (harvestSupersededDirCredentials), and both are needed: this one is the only
	// harvest on the paths that have no pin at all (`kae use -i`, `kae run -i`).
	data, _, refused := app.harvestDirCredential(ctx, be, specs, tool, accountName, acc, dirs, data, attributionSource{Dir: dirs.Config})
	// **A refusal that cannot preserve is a deletion, and here the store is not this
	// directory's to spend.** When the credential is the *account's* (dirs.Cred is set,
	// so the store is `credstore/<tool>/<account>` and its path names the account), every
	// directory bound to that account reads this one copy — so overwriting it with an
	// older snapshot is not a local action, and under single-use rotation it logs the
	// account out everywhere, up to 8h later, inside the tool.
	//
	// Reachable on any bind whose config dir holds no identity cache to attribute from,
	// which is **every first bind**: the store is created moments earlier, a shared bind
	// deliberately links no `.claude.json`, and writeDirIdentity runs after this. So
	// "use claude in one worktree, then bind a second worktree to the same account"
	// destroyed the only copy that could still refresh. Measured 2026-08-08; before the
	// split this was unreachable, because a fresh directory's own store was empty and
	// readLiveCredential answered liveNothing.
	//
	// Which side to keep is forced by which mistake is *detectable*. Keep, and a bind that
	// should have switched the store may not have — visible, because the tool writes an
	// identity cache there and `kae doctor` reports `identity_drift`, and repairable with
	// one more command. Overwrite, and the copy is gone with nothing to compare against:
	// doctor is silent afterwards, measured. So kae keeps it and says so.
	//
	// Scoped to the **attribution** refusal (`Unattributed`); every other refusal is
	// deliberately left overwriting — not "the other two", because nothing stops a future
	// reason from landing in neither bucket and quietly becoming another one. `Conflicting`
	// is positive evidence that the copy is somebody else's, so this account's credential
	// is elsewhere and the bind has to take effect; the unreadable/undatable case keeps the
	// older behaviour because docs/ROADMAP.md's trade-off is what keeps `kae pin` able to
	// repair a corrupted store at all.
	//
	// The `dirs.Cred != ""` half is a statement of intent, not a live guard, and cannot
	// be killed by a test: the harvest only refuses for a tool whose rotation is measured
	// (claude), and that tool has a credential variable, so dirs.Cred is always set by
	// the time this is reached. It stays because a tool whose per-directory store is
	// account-agnostic must keep overwriting — there the store is one directory's to
	// spend, and the bind is what the user asked for.
	keepLiveCopy := keepsUnattributedCopy(refused, dirs)
	// The backstop, not the primary voice — for either wording. A bound directory's
	// pin-level pass says this better, because it knows the account and the bound
	// directory and so can name a login remedy, and it records what it said; this site
	// fires only for a store nobody spoke about: a global isolated home (no pin, no pass),
	// or a store the pass could not attribute, had no snapshot for, or never reached.
	// Keying the suppression on the store's *kind* instead looked equivalent and was not:
	// it silenced exactly those cases, which are the destructive ones (both shapes
	// measured, 2026-08-04). The suppression is checked once, and the two wordings sit in
	// one switch, because they are mutually exclusive by construction — keepLiveCopy is
	// only ever true for a refusal.
	//
	// No remedy in either: this function has a *store* path, not the bound directory a
	// login would have to happen in — pinLoginRemedy on a kae-owned store dir names a
	// place logging in would not even work.
	// Keyed on the credential's own location, which is what both speakers are talking
	// about. Keyed on the config dir instead, a `-s` ↔ `-i` toggle of one account said
	// the same thing twice: the two config dirs differ while the credential store is
	// identical.
	if !app.refusalReported[dirs.credDirOrConfig()] {
		// Named by where the credential actually is, which is the account's own store
		// once the two are split — naming the config dir would send the reader to a
		// directory that holds no credential at all.
		clause := dirCredentialRefusalClause(tool, dirs, accountName, refused)
		switch {
		case keepLiveCopy:
			// The trailing clause says why the copy is not this bind's to spend, so it must
			// not restate the reason: paired with "no directory reads this credential yet"
			// the older wording ("holds …'s credential for everything that reads it") read
			// as a contradiction inside one sentence. It separates the **store** (this
			// account's, shared) from the **copy** in it (whoever's), because the one arm
			// below says those are two different accounts four words apart.
			consequence := ""
			if refused.ForeignToReaders {
				// The only keep where kae knows what happens next. Without it the command
				// still prints its success line and nothing says the directory will go on
				// running somebody else's login.
				consequence = "; until you log in inside this directory it will run that other account"
			}
			fmt.Fprintf(os.Stderr,
				"kae: warning: %s — so kae kept it rather than replacing it: the store is %s/%s's and "+
					"shared, so a copy in it is not this bind's to spend%s\n",
				clause, tool, accountName, consequence)
		case refused.Why != "":
			fmt.Fprintf(os.Stderr, "kae: warning: %s, so this write replaces it\n", clause)
		}
	}
	// Nothing is written for this artifact when the copy is kept — not the credential, not
	// the stale-file sweep that follows a keychain write, and **not the identity label**.
	// The sweep is obvious: it removes a plaintext copy because an item was just written,
	// and none was. The label is the one that had to be measured, and an earlier version of
	// this fix wrote it: kae's own label is exactly the evidence the next bind's attribution
	// reads, so `kae pin` again in the same directory confirmed against a cache kae had
	// planted and harvested the copy the first bind had refused. Measured 2026-08-08 — a
	// login as another account inside a bound directory, then two ordinary binds elsewhere,
	// and that account's token is filed under this one's name, which is the outcome nothing
	// offline can detect afterwards because the token is opaque.
	// So the rule the rest of this function already states holds here too: the identity
	// follows a **successful credential write** (docs/ADAPTERS.md). Absence is then the
	// honest record of "kae wrote nothing here", and the next cache in this directory is the
	// tool's own — independent evidence, which is what attribution is supposed to read.
	if !keepLiveCopy {
		if err := artifact.ApplyLive(ctx, sp, artifact.Value{Data: data, Present: true}); err != nil {
			return fmt.Errorf("write %s credential for account %s: %w", tool, accountName, err)
		}
		if sp.Kind == constants.KindKeychain {
			// The keychain item is what the tool reads (reads try it first and only fall
			// back to the file), so once kae has written it a plaintext copy in the bound
			// directory is a credential nothing reads, and kae removes it rather than
			// leaving a stale secret on disk forever.
			//
			// Inside the keep guard because it follows the *write*, not because the file could
			// be the copy being preserved — on this platform the harvest read the **item**, so
			// the file was never what it judged. The reason it must not run without the write
			// is the condition spelled out below: the file is only known-superseded once an
			// item has just been written.
			//
			// Stated as the condition it rests on, not as an absolute: **while the tool
			// keeps preferring the item**, the file cannot come back and cannot hold
			// anything newer than what was just written — claude's first refresh promotes a
			// file store to an item and deletes the file (docs/VALIDATION.md), so a file
			// beside a live item is not a state upstream produces. If that ever changes,
			// this removal is a harvest kae skips: the harvest above reads the credential
			// artifact the adapter resolves, which is the item, and never this file.
			// Both directories, because the split moved where the tool would write that
			// file: the account's credential store is where it lands now, and the config
			// dir is where a directory bound before the split left one. Removing only the
			// first would leave that older copy readable forever — and it is a copy of a
			// *different* login by then, since the item this write just made is the one the
			// tool reads.
			for _, dir := range []string{dirs.credDirOrConfig(), configDir} {
				for _, name := range app.pinCredItems(tool) {
					stale := filepath.Join(dir, name)
					if err := os.Remove(stale); err != nil && !os.IsNotExist(err) {
						return fmt.Errorf("remove superseded credential copy %s: %w", stale, err)
					}
				}
			}
		}
	}
	if keepLiveCopy {
		// The directory is bound and the copy it reads is intact; there is nothing of kae's
		// to *record* here. There is something of kae's to **retract**, and leaving it is the
		// one way a keep destroys what it kept.
		//
		// A bind that moves a directory to another account leaves the previous binding's
		// label in its config dir, because a keep writes none. On the next run the fragment
		// names the new account, so this directory *is* one of the store's readers — and its
		// stale label is then read as this directory's own reading of the new account's
		// store, which makes it a conflicting reader and `Conflicting` overwrites the copy
		// the first run preserved. Measured 2026-08-08: two identical `kae pin` calls, the
		// first keeping and the second destroying, with a success line both times.
		//
		// **Only where the caller established the label is stale** (modeLabelStale),
		// and among those only one that **disagrees**. A label that disagrees has two causes
		// wanting opposite actions — left by a previous binding, or written by a login in this
		// very directory — and what separates them is the **mode**, not the reader set: a
		// shared config dir is one per pin×tool, so a change of account makes its label kae's
		// leftover; an account-keyed one (isolated, and the globally isolated home) only ever
		// held labels written while bound to that account, so a disagreement there is a live
		// login.
		//
		// Two derivations were tried and are wrong; both destroyed a login, both measured
		// 2026-08-08. Keyed on the **label alone**, this deleted the live kind on the disagree
		// arm, after which the next identical run saw one silent reader and one confirming
		// sibling, confirmed, and harvested the foreign token — the mis-filing the reader model
		// exists to stop, reopened from the other side. Keyed on **reader membership**, it
		// broke on the enumeration-incomplete arm, where the walk answers "no readers" and so
		// reads every directory as a stranger: an unrelated leftover store root then made kae
		// delete a live label. Do not restore either.
		//
		// Not consulting the walk is the property that makes the mode-derived gate better
		// rather than merely different: on the `!complete` keep the retract is still right,
		// because "a shared config dir whose bound account changed holds a leftover" is true
		// whoever reads the store.
		//
		// A label that agrees is honest evidence and one kae cannot read is left for the same
		// reason an unreadable credential is — kae has not established that it is wrong.
		// Absence is what a first bind already leaves, so this makes the two states the same one.
		if staleLabel && dirIdentityConfirms(ctx, be, specs, acc, configDir).Conflicting {
			if err := retractDirIdentity(ctx, specs, configDir); err != nil {
				fmt.Fprintf(os.Stderr,
					"kae: warning: the %s identity cache in this directory still names the account it was "+
						"bound to before, and kae could not remove it (%v); run `%s relogin %s` here, or the "+
						"next bind may read it as this directory's own and replace the credential kae just kept\n",
					tool, err, toolName, tool)
			}
		}
		return nil
	}
	// Last, and on both store kinds — a file credential needs the matching identity
	// exactly as much as a keychain item does. This used to `return nil` above for a
	// non-keychain spec, which would have skipped the identity on every Linux bind.
	//
	// The only failure in this function that warns instead of returning, and the
	// asymmetry is deliberate. An identity is a label — "losing it is safe" is the
	// property the adapter asserts by marking the artifact `IdentityOnly` — while the
	// credential above is the bind. Returning here would also abandon the caller
	// mid-bind: `kae pin` gives up before writing its mise fragment, so the directory
	// would be left with a fresh private credential and no binding pointing at it.
	// A malformed `.claude.json` the tool left behind, or a momentarily unreadable
	// secret store, is not a reason for that.
	if err := writeDirIdentity(ctx, be, specs, acc, configDir); err != nil {
		fmt.Fprintf(os.Stderr,
			"kae: warning: could not apply %s's identity cache for account %s in this directory (%v); "+
				"%s may display another account until you log in inside it\n",
			tool, accountName, err, tool)
	}
	return nil
}

// migratePreSplitHome harvests and then removes the credential a **global isolated
// home** still holds at its own name, from before kae gave each account one
// credential store. Call it before materializing that home.
//
// It is the global-isolated counterpart of harvestSupersededDirCredentials, and it
// exists for the same reason that pass does: the write path can only see the store
// it is writing, which since the split is the account's, so a copy left at the
// home's own name is invisible to it. A bound directory gets this from the pin-level
// pass and the sweep that follows it; `kae use -i` and `kae run -i` have neither, so
// without this their pre-split homes silently revert to an older snapshot copy —
// which under single-use rotation is a logout, with no finding anywhere
// (`credential_unsplit` walks bound directories only).
//
// Every refusal the harvest and the delete already have applies: an unattributable,
// unreadable or unpreservable copy is left where it is.
func (app *App) migratePreSplitHome(ctx context.Context, be secret.Backend, tool, accountName, home string) {
	// The only question this function answers by itself: is there anywhere to migrate
	// *to*. A tool that keeps its credential in its home has nothing to move, and a
	// home that already is the credential store is not pre-split.
	if credDir := app.credStoreDir(tool, accountName); credDir == "" || credDir == home {
		return
	}
	// Everything after that is the sweep, so it *is* the sweep. `migrating: true` is
	// what says this copy is no longer the one that home reads, which is the same
	// statement the kept-store exception makes for a bound directory; `purging: false`
	// keeps the account-gone copy the same way a bind does. Written as a call rather
	// than a second body because the two used to be one, and removeDirCredential's own
	// comment said the pair "must not disagree about one state" — which is a hazard
	// recorded rather than removed. Two quality lenses found it independently.
	// No Account on the store: removeDirCredential reads Tool, dirs() and CredDir and
	// never that field, and setting it would suggest to the next reader that it is
	// consulted here the way storeAccount consults it for the sweep's own walk.
	store := dirStore{Tool: tool, Dir: home}
	if _, err := app.removeDirCredential(ctx, be, store, accountName, false, true); err != nil {
		fmt.Fprintf(os.Stderr,
			"kae: warning: could not migrate the pre-split %s credential in %s (%v); any copy still "+
				"there is one nothing reads, and a refresh of it elsewhere would invalidate this "+
				"account's\n",
			tool, app.displayPath(home), err)
	}
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
// artifact for a location you could not measure" refusal in this file prevents.
func rotatesSingleUse(tool string) bool { return tool == constants.ToolClaude }

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
			fmt.Fprintf(os.Stderr,
				"kae: warning: harvested the %s credential for %s/%s but could not update its capture time: %v\n",
				tool, tool, accountName, err)
		}
		return
	}
	acc.CapturedAt = app.Now().UTC()
	if err := account.Save(dir, acc); err != nil {
		fmt.Fprintf(os.Stderr,
			"kae: warning: harvested the %s credential for %s/%s but could not update its capture time: %v\n",
			tool, tool, accountName, err)
	}
}
