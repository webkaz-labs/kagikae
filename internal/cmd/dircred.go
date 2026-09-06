package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
	"github.com/webkaz-labs/kagikae/internal/keychain"
	"github.com/webkaz-labs/kagikae/internal/paths"
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
				"capture it with `kae add --no-login %s %s` and re-run\n",
			tool, account, tool, tool, account)
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
// It is the single answer to "where does a pinned directory's credential go",
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

// liveCredentialState is what kae found in a per-directory credential store, and it
// has three values because "nothing to lose" and "kae cannot tell" must not be
// folded together: the first licenses a delete, the second forbids it.
type liveCredentialState int

const (
	// liveUnreadable — the store could not be read, or holds a payload this tool's
	// parser does not recognize. It may be a working login in a format kae has not
	// been taught, which is exactly what an upstream change looks like.
	liveUnreadable liveCredentialState = iota
	// liveNothing — absent, or present with nothing left to authenticate or refresh
	// with. `kae unpin` keeps a store on purpose and a directory whose tool never
	// started in it has no credential yet, so absence is the ordinary case; the
	// tombstone a failed refresh leaves behind is a fully-formed payload, which is why
	// presence cannot stand in for this (docs/VALIDATION.md).
	liveNothing
	// liveUsable — a login worth preserving.
	liveUsable
)

// readLiveCredential reads the credential live at sp and classifies it.
//
// `liveUsable` is exactly `orderable`, and the yes/no half below is *taken from* it
// rather than restated. What this adds is a reason: the delete path needs "nothing left
// to lose" told apart from "kae cannot tell", and collapsing those two into one refusal
// is killed by the harvest and prune tests. So a caller that only needs the yes/no calls
// `orderable`; this is the one place that also needs to know why.
func readLiveCredential(ctx context.Context, tool string, sp artifact.Spec) ([]byte, freshness.Info, liveCredentialState) {
	live, err := artifact.ReadLive(ctx, sp)
	switch {
	case err != nil:
		return nil, freshness.Info{}, liveUnreadable
	case !live.Present:
		return nil, freshness.Info{}, liveNothing
	}
	info := freshnessOf(tool, live.Data)
	if !orderable(info) {
		// **Only the measured tombstone is "nothing to lose"; everything else kae cannot
		// order, it cannot judge.** `liveNothing` licenses a delete (harvestBeforeDelete
		// removes the item without harvesting or warning, and harvestDirCredential lets
		// the overwrite pass unreported); `liveUnreadable` forbids one. `orderable` fails
		// for three different reasons and they do **not** collapse to two answers — two
		// separate folds of them each shipped a silent delete of a live login, one field
		// apart, so this names the tombstone exactly rather than approximating it:
		//
		//   - `!Known`: no `expiresAt` key at all. Unreadable.
		//   - `Known && !Revoked` and undated: claude sets `Known` on the key's mere
		//     *presence* and parses a non-numeric value to the zero time, so an upstream
		//     type change puts a working login here. Unreadable.
		//   - `Known && Revoked` with a **non-zero** deadline (future or past — a past one is
		//     the shape the dependency below is about, not a fifth case): `Revoked` is derived from token
		//     fields that are empty *or absent*, so an upstream rename of the token keys
		//     reads as revoked while being a working login — and docs/VALIDATION.md's own
		//     row justifies that wide reading by saying it makes every path *decline* to
		//     touch the copy, which is false for this consumer. Unreadable.
		//   - `Known && Revoked && ExpiresAt.IsZero()`: the tombstone as **measured**
		//     (blank tokens, `expiresAt: 0`, `refreshTokenExpiresAt` retained — the claude
		//     row in docs/VALIDATION.md). Nothing to lose.
		//
		// So this depends on the tombstone continuing to zero `expiresAt`, which that row
		// now records as a dependency: if upstream tombstones without zeroing it, kae stops
		// sweeping tombstones and leaves a spent secret behind. That is the safe direction,
		// and it is a recorded consequence rather than a surprise. `EpochToTime` maps every
		// `n <= 0` to the zero time, so a negative deadline stays in this bucket — and by the
		// same token a *non-number* is indistinguishable from a zero here, which is why a zero
		// deadline with a token still in it is retained rather than swept (the claude adapter's
		// Freshness comment and docs/ROADMAP.md carry that consequence).
		//
		// The `Known` conjunct is a statement of intent, not a filter: claude's is the only
		// `Revoked` assignment in the tree and it always sits with `Known: true`, so a mutation
		// dropping it cannot be killed today. It is kept so a second adapter cannot reach
		// `liveNothing` without declaring the deadline field this arm reasons about.
		if info.Known && info.Revoked && info.ExpiresAt.IsZero() {
			return nil, freshness.Info{}, liveNothing
		}
		return nil, freshness.Info{}, liveUnreadable
	}
	return live.Data, info, liveUsable
}

// dirIdentityConfirms reports whether the identity cache sitting beside the live
// credential in configDir names the account whose snapshot a harvest would write to,
// and says why not when it does not.
//
// This is the guard that makes the harvest safe, because a store can legitimately
// hold a credential that is not acc's at all: the shared mechanism's store is
// account-agnostic (one directory per pin×tool), so re-binding it to another
// account finds the previous account's credential there — usually the *newer* one,
// since it is the one in daily use. Harvesting that would file account B's token
// under account A's name and identity, after which nothing offline can tell: the
// token is opaque, so live, snapshot and doctor all agree on a label that is
// simply wrong. The two global recaptures refuse on the same evidence
// (keepSnapshotIdentity), through the same pair of predicates — identityComparable
// above identityDiffers, in that order, for the reason identityComparable states.
//
// Positive evidence is required — both sides readable, and agreeing. Absence is
// not evidence of a match, and insisting is cheap: a copy worth harvesting is one
// the tool refreshed in that directory, and a tool that ran there wrote its
// identity there.
//
// `doctor` is the second consumer (pinIdentityChecks), and it reads only the
// Conflicting refusal: the same asymmetry that decides what the harvest may say is
// what decides what doctor may report — a conflict is proof that a store disagrees
// with the account named for it, while every other refusal is missing evidence and
// warning on those would fire on healthy bound directories. So the two stay one
// predicate; a change here changes both, on purpose.
func dirIdentityConfirms(ctx context.Context, be secret.Backend, specs []artifact.Spec,
	acc account.Account, configDir string,
) harvestRefusal {
	confirmed := false
	for _, sp := range specs {
		if !sp.IdentityOnly {
			continue
		}
		art, ok := acc.Artifacts[sp.Name]
		if !ok || !art.Present {
			return harvestRefusal{Why: fmt.Sprintf("no %s identity is recorded for that account", sp.Name)}
		}
		// A target that leaves the store labels the *real* home, not this directory
		// (a pre-v0.16.0 bind linked it there), so it says nothing about whose
		// credential this store holds.
		//
		// Both of these stay **non**-Conflicting, and since doctor started reading that flag
		// (pinIdentityChecks) the distinction decides whether every pre-v0.16.0 shared bind on
		// a machine gets a false `identity_drift`: the payload read through such a link is the
		// real home's, so it disagrees with the bound account whenever the global account
		// differs — which is the ordinary case. Pinned by
		// TestBoundDirectoryIdentitySharedWithTheRealHomeIsSilent; before it, flipping this to
		// Conflicting survived the whole suite (measured 2026-08-05), because the harvest tests
		// assert only *that* it refuses.
		switch outside, err := identityTargetEscapes(sp.Target, configDir); {
		case err != nil:
			return harvestRefusal{Why: "kae could not resolve where its identity cache is"}
		case outside:
			return harvestRefusal{Why: "its identity cache is shared with the real tool home"}
		}
		live, err := artifact.ReadLive(ctx, sp)
		if err != nil || !live.Present {
			return harvestRefusal{Why: "the directory holds no identity cache to compare"}
		}
		stored, found, err := be.Get(ctx, art.SecretRef)
		if err != nil || !found {
			return harvestRefusal{Why: "that account's recorded identity cannot be read"}
		}
		// Evidence either way has to be a comparison of two account **records**. A payload
		// that is well-formed JSON but not an object names no account, so it can neither
		// prove a conflict nor confirm a match, and it belongs with the missing evidence
		// above. identityDiffers falls back to a byte comparison for exactly those — right
		// for the drift check, which must not call two payloads it cannot read equal, and
		// wrong for attribution in **both** directions. Measured 2026-08-05: a store whose
		// `/oauthAccount` was `null`, a string, a number or an array was reported by `doctor`
		// as naming *another account* when it names none; and two identical such payloads
		// took the confirming path below, letting the harvest attribute a copy on the
		// strength of two sides agreeing about nothing. The gate is above the comparison so
		// one branch of this function cannot be stricter than the other.
		if !identityComparable(stored, live.Data) {
			return harvestRefusal{Why: "kae cannot read the identity records it would compare"}
		}
		if identityDiffers(sp, stored, live.Data) {
			// The one reason that is **positive** evidence rather than missing evidence: the
			// copy belongs to somebody else. Callers must not then tell the user to log this
			// account in again — the credential this bind writes is fine, and a login would
			// mint a chain that invalidates the copy kae just harvested.
			return harvestRefusal{Why: "its identity names a different account", Conflicting: true}
		}
		confirmed = true
	}
	if !confirmed {
		return harvestRefusal{Why: "this platform records no identity for it"}
	}
	return harvestRefusal{}
}

// unbindableDirKeychain reports whether sp is a keychain artifact the adapter has not
// declared bindable to a directory — the one predicate the write, the harvest and the
// freshness read all apply before touching such a store, and which the delete states in
// its own inverted form because it needs "keychain **and** bindable" rather than the
// refusal.
func unbindableDirKeychain(sp artifact.Spec) bool {
	return sp.Kind == constants.KindKeychain && !sp.KeychainDirBindable
}

// specByName picks one artifact spec out of a resolved set.
func specByName(specs []artifact.Spec, name string) (artifact.Spec, bool) {
	for _, sp := range specs {
		if sp.Name == name {
			return sp, true
		}
	}
	return artifact.Spec{}, false
}

// writeDirIdentity applies the bound account's identity-only artifacts inside
// configDir, so the tool *names* the account whose credential is now there.
//
// Auth never depended on this — the token decides who you are — which is why the
// gap survived so long: a bonded or isolated directory kept whatever account first
// ran in it, `kae pin <tool> <account>` did not correct it, and the only symptom
// was a UI (and a `kae add` identity detection) naming the previous account.
//
// A snapshot with no identity payload applies as **absent**, which removes the
// live cache rather than leaving it. That is the same choice applySnapshot and the
// rollback cleanup make, for the same reason: the tool rebuilds the cache from the
// credential it can now see, whereas a kept one is a label for an account that is
// no longer there.
//
// Called only after the credential write has succeeded, with the specs and the
// snapshot that write already resolved — asking the adapter or reloading
// `account.toml` a second time would buy nothing and costs a `security` subprocess
// for codex. The order is not interchangeable: a directory labelled with an account
// whose credential kae could not put there is worse than an unlabelled one, since
// the label is the only thing a user checks.
func writeDirIdentity(ctx context.Context, be secret.Backend, specs []artifact.Spec, acc account.Account, configDir string) error {
	for _, sp := range specs {
		if !sp.IdentityOnly {
			continue
		}
		if outside, err := identityTargetEscapes(sp.Target, configDir); err != nil {
			return err
		} else if outside {
			// Bond mode links every entry of the real tool home into the store, so this
			// target can be a link back out of it (docs/SCOPE-MODEL.md §6). Writing
			// through it — which artifact.ApplyLive does deliberately, to keep the
			// sharing a bond dir exists for — would relabel the *real* home with this
			// directory's account, turning one directory's attribution gap into a
			// global one. So kae declines this one write and says so.
			fmt.Fprintf(os.Stderr,
				"kae: warning: %s's identity cache in this directory is shared with the real %s home "+
					"(%s), so kae is not writing it here; %s may display an account other than %s "+
					"until you log in inside the directory\n",
				acc.Tool, acc.Tool, sp.Target, acc.Tool, acc.Name)
			continue
		}
		art := acc.Artifacts[sp.Name]
		// identityOnly is true, so a payload the backend has lost degrades to absent
		// rather than erroring — the same rule applySnapshot and the rollback cleanup
		// apply to this artifact class, from the same helper. The missing callback is
		// unreachable at that flag, and is written honestly rather than nil so it stays
		// correct if the flag ever moves.
		value, err := storedValue(ctx, be, art.SecretRef, art.Present, true, func() error {
			return errf(constants.ExitError, "identity payload %s is missing from the secret store", art.SecretRef)
		})
		if err != nil {
			return err
		}
		if err := artifact.ApplyLive(ctx, sp, value); err != nil {
			return fmt.Errorf("write %s identity for account %s: %w", acc.Tool, acc.Name, err)
		}
	}
	return nil
}

// retractDirIdentity removes the identity-only artifacts inside configDir. It is
// writeDirIdentity's inverse and shares its one hard rule: never act through a target that
// resolves outside the store, because that target is the real tool home and removing the
// account there is a global change made from one directory.
//
// Its caller is the keep path, which has established that the label disagrees with the
// account being bound — see writeDirCredential for why leaving one there turns a second
// identical bind into a destroy.
//
// The escape check is **unreachable today**, and it stays as a statement of intent rather
// than as a test that cannot fail. The reason is not the one it first said, which was a
// condition short: dirIdentityConfirms *returns* at the first spec that conflicts, so with
// two identity-only specs — a differing one and an escaping one — it answers `Conflicting`
// and this function is then called with the escaping spec still in the set. What makes it
// unreachable is that **claude declares exactly one IdentityOnly artifact**, no other
// adapter declares any, and the keep path is claude-only. A second identity-only artifact
// on the same tool reaches this guard, and nothing guards that count the way
// TestKeychainSpecsAreAccountScoped guards keychain scoping (measured by review,
// 2026-08-08).
func retractDirIdentity(ctx context.Context, specs []artifact.Spec, configDir string) error {
	for _, sp := range specs {
		// Redundant with the escape guard below today and kept anyway: a credential spec's
		// target resolves inside the *credential store*, which is outside configDir, so the
		// guard already skips it (measured 2026-08-08). The redundancy stops being one for a
		// tool whose credential lives in its config dir, where deleting it here would be a
		// logout rather than a relabel.
		if !sp.IdentityOnly {
			continue
		}
		outside, err := identityTargetEscapes(sp.Target, configDir)
		if err != nil {
			return err
		}
		if outside {
			continue
		}
		if err := artifact.ApplyLive(ctx, sp, artifact.Value{Present: false}); err != nil {
			return err
		}
	}
	return nil
}

// identityTargetEscapes reports whether target resolves outside configDir, i.e.
// whether writing it would leave the store this bind owns.
//
// Both sides are resolved before comparing, because the store path itself can run
// through a symlink (`/tmp` on macOS is `/private/tmp`), and comparing a resolved
// target against an unresolved root would call every write an escape. A target
// that does not exist yet is resolved through its parent — the file kae is about
// to create is inside whatever directory the parent names.
func identityTargetEscapes(target, configDir string) (bool, error) {
	root, err := filepath.EvalSymlinks(configDir)
	if err != nil {
		return false, fmt.Errorf("resolve store dir %s: %w", configDir, err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("resolve identity target %s: %w", target, err)
		}
		// "Not exist" covers two states that need opposite answers, so ask what the
		// path *is* rather than inferring it from the failure. A path that simply does
		// not exist yet will be created inside its parent — but a symlink whose
		// destination is gone reports the same error, and it leaves the store exactly
		// as much as a live link does. Resolving its parent would call it inside, and
		// the write would then reach artifact.ApplyLive's own symlink guard, which
		// refuses (correctly) and turns a declinable case into a failure.
		if info, lerr := os.Lstat(target); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
			return true, nil
		}
		parent, perr := filepath.EvalSymlinks(filepath.Dir(target))
		if perr != nil {
			return false, fmt.Errorf("resolve identity target dir %s: %w", filepath.Dir(target), perr)
		}
		resolved = filepath.Join(parent, filepath.Base(target))
	}
	// pathWithin owns what counts as inside, so "escapes" cannot drift from the
	// answer the rest of the package gives; the work above is only about *which*
	// paths to hand it.
	return !pathWithin(resolved, root), nil
}

// dirCredentialSpec resolves the tool's credential spec as it applies *inside*
// credDir. ok is false when this platform has no artifact by that name. For a
// caller that needs a second spec of the same tool and directory, resolve once with
// dirSpecs and pick with specByName instead — each resolution can cost a subprocess.
func (app *App) dirCredentialSpec(ctx context.Context, tool, artName string, dirs bindDirs) (artifact.Spec, bool, error) {
	specs, err := app.dirSpecs(ctx, tool, dirs)
	if err != nil {
		return artifact.Spec{}, false, err
	}
	sp, ok := specByName(specs, artName)
	return sp, ok, nil
}

// bindDirs names the two directories a per-directory bind resolves a tool's
// artifacts against. Config is the tool's home — sessions, settings, the identity
// cache — and Cred is where its credential resolves.
//
// They are separate fields rather than two string arguments on purpose: the pair
// is swappable at a call site and getting it backwards is silent. kae would write
// the credential under the config dir's name, the tool would read it under the
// credential dir's, and every offline check would stay green while the directory
// ran the previous account.
//
// An empty Cred means "wherever Config puts it", which is the answer for every
// tool that cannot separate the two (credentialEnvVar) and for a directory bound
// before the split existed. It is not the same as leaving the variable alone —
// see dirSpecs.
type bindDirs struct {
	Config string
	Cred   string
}

// credDirOrConfig is the directory the credential actually resolves against.
func (d bindDirs) credDirOrConfig() string {
	if d.Cred != "" {
		return d.Cred
	}
	return d.Config
}

// dirSpecs resolves every artifact spec of tool as it applies *inside* dirs, by
// asking the adapter with an env whose isolation variable points at the config
// dir and whose credential variable points at the credential store.
//
// The credential variable is always overridden, never left alone — with the
// config dir itself when the pair is not split. An ambient value would otherwise
// win: kae runs inside the bound shell that exported one, so resolving a *legacy*
// store while a split binding is active would read the account-wide item and call
// it that store's, which is what a harvest is then free to overwrite.
func (app *App) dirSpecs(ctx context.Context, tool string, dirs bindDirs) ([]artifact.Spec, error) {
	envVar := isolationEnvVar(tool)
	if envVar == "" {
		return nil, errf(constants.ExitUnsupported,
			"%s has no per-directory isolation mechanism", tool)
	}
	credVar, credDir := credentialEnvVar(tool), dirs.credDirOrConfig()
	adp, err := adapter.ForTool(tool)
	if err != nil {
		return nil, err
	}
	// Outermost wrapper, so the override wins over any inner masking of
	// kae-managed isolation values (applyGlobalScope) and over an outer bind's
	// value leaking in from the caller's own environment.
	//
	// Both env-reading seams are overridden. Only Getenv resolves the isolation
	// variable today, but leaving LookupEnv pointing at the real environment would
	// mean an adapter that later reads it through Env.IsSet silently escapes the
	// per-directory override — the exact class of "kae's view differs from the
	// tool's" this file exists to close.
	override := map[string]string{envVar: dirs.Config}
	if credVar != "" {
		override[credVar] = credDir
	}
	env := app.Env
	innerGetenv, innerLookup := app.Env.Getenv, app.Env.LookupEnv
	env.Getenv = func(key string) string {
		if value, ok := override[key]; ok {
			return value
		}
		return innerGetenv(key)
	}
	env.LookupEnv = func(key string) (string, bool) {
		if value, ok := override[key]; ok {
			return value, true
		}
		if innerLookup == nil {
			value := innerGetenv(key)
			return value, value != ""
		}
		return innerLookup(key)
	}
	specs, err := adp.Artifacts(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("resolve %s artifacts for %s: %w", tool, dirs.Config, err)
	}
	return specs, nil
}

// snapshotCredential returns the captured account, its credential payload, and the
// spec kind the payload was captured as, which fixes its shape (checkPayloadShape).
//
// The snapshot is the only correct source for a per-directory bind: the live
// store holds whichever account is globally active, which is the account being
// bound only by coincidence.
//
// It returns the loaded account because the identity step needs the same one, and
// loading `account.toml` twice for a single bind meant two copies of the
// "is it captured at all" guard, one of which then argued in a comment that it could
// never fire.
func (app *App) snapshotCredential(ctx context.Context, be secret.Backend, tool, accountName, artName string) (account.Account, []byte, string, error) {
	acc, found, err := account.Load(app.Paths.AccountDir(tool, accountName))
	if err != nil {
		return account.Account{}, nil, "", err
	}
	if !found {
		return account.Account{}, nil, "", errf(constants.ExitNotFound,
			"account %s/%s is not captured yet (run: kae add --no-login %s %s)",
			tool, accountName, tool, accountName)
	}
	metaArt, ok := acc.Artifacts[artName]
	if !ok || !metaArt.Present {
		return account.Account{}, nil, "", errf(constants.ExitAuthMissing,
			"account %s/%s has no credential snapshot; re-run kae add --no-login %s %s",
			tool, accountName, tool, accountName)
	}
	data, found, err := be.Get(ctx, metaArt.SecretRef)
	if err != nil {
		return account.Account{}, nil, "", fmt.Errorf("read snapshot credential: %w", err)
	}
	if !found {
		return account.Account{}, nil, "", errf(constants.ExitError,
			"snapshot payload missing; re-run kae add --no-login %s %s", tool, accountName)
	}
	return acc, data, metaArt.Kind, nil
}

// dirStore is one per-directory credential store a bound directory has
// materialized. Dir is the config dir the tool reads, i.e. what its isolation env
// var points at, which is what resolves the store's item identity.
//
// Account is the account whose credential the store holds, and it is empty for
// the shared mechanism: that store is one directory per pin×tool, so its path
// records no account and only the fragment kae is replacing can name one
// (storeAccount).
type dirStore struct {
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
// bound directory, across both mechanisms and every account of the isolated one.
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
	for _, toolEntry := range tools {
		if !toolEntry.IsDir() {
			continue
		}
		tool := toolEntry.Name()
		if shared := app.Paths.SharedDir(pinID, tool); dirExists(shared) {
			store := dirStore{Tool: tool, Dir: shared}
			store.CredDir = app.attributedCredDir(store, prev)
			stores = append(stores, store)
		}
		accounts, err := os.ReadDir(filepath.Join(pinDir, tool, paths.IsolatedSegment))
		if err != nil {
			continue // no isolated stores for this tool
		}
		for _, acct := range accounts {
			if !acct.IsDir() {
				continue
			}
			if dir := app.Paths.IsolatedConfigDir(pinID, tool, acct.Name()); dirExists(dir) {
				// The account is the directory's own name (kae composes the path from it),
				// which is what lets the sweep harvest an isolated store it is about to
				// delete without consulting any binding.
				store := dirStore{Tool: tool, Dir: dir, Account: acct.Name()}
				store.CredDir = app.attributedCredDir(store, prev)
				stores = append(stores, store)
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

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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
// An isolated store answers for itself: its path is composed from the account.
// A shared store cannot — one directory serves every account this pin ever bound
// there — so the answer comes from the binding being replaced, and only when that
// binding used the shared mechanism. Reading it from a fragment that was in
// isolated mode would attribute a shared store left over from an *earlier*
// binding to the wrong account, which is the mislabelling the harvest's identity
// check exists to catch; there is no reason to hand it that case on purpose.
func storeAccount(store dirStore, prev fragmentInfo) string {
	if store.Account != "" {
		return store.Account
	}
	if prev.Mode == modeShared {
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
// identity cache. That last one belongs in the same lockstep list `dirCredentialStores`
// carries.
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
					"a working login in a shape kae does not recognize, it is gone\n", tool, credDir)
			return true
		}
		fmt.Fprintf(os.Stderr,
			"kae: warning: kae cannot read or date the %s credential in %s, so it is left in place "+
				"instead of deleted (a payload kae cannot judge may still be a working login); "+
				"if it is spent, kae unpin --purge in that directory removes it — that tears the "+
				"binding down too, so re-pin afterwards\n", tool, credDir)
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
			"kae: warning: no account named %s/%s exists any more, so the %s credential this directory held "+
				"for it is deleted without being kept anywhere (%s)\n",
			tool, accountName, tool, credDir)
	case exitOf(err) == constants.ExitNotFound:
		// Same condition, and this is *housekeeping* rather than a purge — the case that
		// used to delete the newest copy of a renamed account's credential. Worded from the
		// fact rather than from the error: snapshotCredential says "not captured yet (run:
		// kae add --no-login …)", the wrong instruction for an account the user removed or
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
			"kae: warning: no account named %s/%s exists any more, so the %s credential this directory "+
				"held for it is left in place rather than deleted (%s); `kae unpin --purge` removes it, "+
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
		consequence := "so kae is leaving it where it is"
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
			// those are different directories, and every other speaker in this file was moved
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
// own credential store walks every pinned directory on the machine, "otherwise ready"
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
	// above is the cost — the reader walk visits every pinned directory on the machine —
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
		// it walks every pinned directory on the machine — and hoisted it was paid on
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
		return fmt.Sprintf("log in inside that directory: cd %s && %s relogin %s", dir, toolName, tool)
	}
	return fmt.Sprintf("log in again in %s from inside that directory (cd %s)", tool, dir)
}

// dirCredentialFreshness reads one per-directory store's credential and parses it,
// reporting ok=false for anything it cannot judge.
//
// The location comes from dirCredentialSpec — the adapter's answer for an
// environment pointed at this store — never from a path or a service name rebuilt
// here. That is the same rule writeDirCredential and removeDirCredential follow,
// and breaking it is the defect that made every pinned directory on macOS run the
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
