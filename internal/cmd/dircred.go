package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
// per-directory bind, at the location the tool bound to credDir will actually
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
// specs against an env whose isolation variable already points at credDir yields
// the per-directory keychain service name and the per-directory file path alike.
// Recomputing either here is what let kae's model of the credential's location
// drift away from the tool's in the first place.
//
// A keychain write that fails is returned, never downgraded to a plaintext
// write. The fallback would look like success and reproduce the original defect:
// a credential file in a directory whose tool reads the keychain first.
func (app *App) writeDirCredential(ctx context.Context, be secret.Backend, tool, accountName, credDir string) error {
	artName := credentialArtifactName(tool)
	if artName == "" {
		return nil // the tool has no credential kae materializes per directory
	}
	// Resolved once for both halves. Asking the adapter twice is not free: codex
	// under `cli_auth_credentials_store = "auto"` probes the keychain to decide which
	// store it is on, so a second resolution is a second `security` subprocess per
	// bind (and a second read of its config.toml).
	specs, err := app.dirSpecs(ctx, tool, credDir)
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
	// bound directory, which is what a mode toggle or an isolated re-bind moves the
	// binding away from. That is the pin-level pass
	// (harvestSupersededDirCredentials), and both are needed: this one is the only
	// harvest on the paths that have no pin at all (`kae use -i`, `kae run -i`).
	data, _, refused := app.harvestDirCredential(ctx, be, specs, tool, accountName, acc, credDir, data)
	if refused.Why != "" && !app.refusalReported[credDir] {
		// The backstop, not the primary voice. A bound directory's pin-level pass says this
		// better — it knows the account and the bound directory, so it can name a login
		// remedy — and it records what it said, so this site fires only for a store nobody
		// spoke about: a global isolated home (no pin, no pass), or a store the pass could
		// not attribute, had no snapshot for, or never reached. Keying the suppression on
		// the store's *kind* instead looked equivalent and was not: it silenced exactly
		// those cases, which are the destructive ones (both shapes measured, 2026-08-04).
		//
		// No remedy here: this function has a *store* path, not the bound directory a login
		// would have to happen in — pinLoginRemedy on a kae-owned store dir names a place
		// logging in would not even work.
		fmt.Fprintf(os.Stderr,
			"kae: warning: the %s credential already in %s is newer than snapshot %s/%s and kae is not "+
				"harvesting it because %s, so this write replaces it\n",
			tool, credDir, tool, accountName, refused.Why)
	}
	if err := artifact.ApplyLive(ctx, sp, artifact.Value{Data: data, Present: true}); err != nil {
		return fmt.Errorf("write %s credential for account %s: %w", tool, accountName, err)
	}
	if sp.Kind == constants.KindKeychain {
		// The keychain item is what the tool reads (reads try it first and only fall
		// back to the file), so once kae has written it a plaintext copy in the bound
		// directory is a credential nothing reads, and kae removes it rather than
		// leaving a stale secret on disk forever.
		//
		// Stated as the condition it rests on, not as an absolute: **while the tool
		// keeps preferring the item**, the file cannot come back and cannot hold
		// anything newer than what was just written — claude's first refresh promotes a
		// file store to an item and deletes the file (docs/VALIDATION.md), so a file
		// beside a live item is not a state upstream produces. If that ever changes,
		// this removal is a harvest kae skips: the harvest above reads the credential
		// artifact the adapter resolves, which is the item, and never this file.
		for _, name := range app.pinCredItems(tool) {
			stale := filepath.Join(credDir, name)
			if err := os.Remove(stale); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove superseded credential copy %s: %w", stale, err)
			}
		}
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
	if err := writeDirIdentity(ctx, be, specs, acc, credDir); err != nil {
		fmt.Fprintf(os.Stderr,
			"kae: warning: could not apply %s's identity cache for account %s in this directory (%v); "+
				"%s may display another account until you log in inside it\n",
			tool, accountName, err, tool)
	}
	return nil
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
}

func (app *App) harvestDirCredential(ctx context.Context, be secret.Backend, specs []artifact.Spec,
	tool, accountName string, acc account.Account, credDir string, snapshot []byte,
) (newest []byte, preserved bool, refused harvestRefusal) {
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
			Why: "kae cannot read the copy already there, and a payload kae does not recognize may still be a login",
		}
	}
	// A snapshot kae cannot read, or one that is itself a tombstone, loses to any
	// usable live copy: it has no deadline worth comparing, and writing it over a
	// working credential is the destruction this function exists to stop.
	cutoff := time.Time{}
	if stored := freshnessOf(tool, snapshot); stored.Known && !stored.Revoked {
		cutoff = stored.ExpiresAt
	}
	if !liveInfo.ExpiresAt.After(cutoff) {
		return snapshot, true, harvestRefusal{}
	}
	if refused := dirIdentityConfirms(ctx, be, specs, acc, credDir); refused.Why != "" {
		// Reported by the caller, not here. Two harvests can look at one store in a
		// single command (the pin-level pass and this chokepoint), so printing at the
		// point of detection said the same thing twice — measured, 2026-08-04 — and only
		// the caller knows what its own next write or delete costs anyway.
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
func readLiveCredential(ctx context.Context, tool string, sp artifact.Spec) ([]byte, freshness.Info, liveCredentialState) {
	live, err := artifact.ReadLive(ctx, sp)
	switch {
	case err != nil:
		return nil, freshness.Info{}, liveUnreadable
	case !live.Present:
		return nil, freshness.Info{}, liveNothing
	}
	info := freshnessOf(tool, live.Data)
	switch {
	case !info.Known:
		return nil, freshness.Info{}, liveUnreadable
	case info.Revoked || info.ExpiresAt.IsZero():
		return nil, freshness.Info{}, liveNothing
	}
	// Note on the `IsZero` half: it is the guard docs/ROADMAP.md prescribes, and it is
	// also **unobservable**, which is worth writing down so nobody removes it as dead
	// code or adds a test that cannot fail. A zero `expiresAt` parses to the zero time,
	// which is never `After` any cutoff, so such a copy could not be harvested even
	// without this line — and claude's only measured zero is the tombstone, which
	// `Revoked` already catches. Mutating it away survives the suite by construction.
	return live.Data, info, liveUsable
}

// dirIdentityConfirms reports whether the identity cache sitting beside the live
// credential in credDir names the account whose snapshot a harvest would write to,
// and says why not when it does not.
//
// This is the guard that makes the harvest safe, because a store can legitimately
// hold a credential that is not acc's at all: the shared mechanism's store is
// account-agnostic (one directory per pin×tool), so re-binding it to another
// account finds the previous account's credential there — usually the *newer* one,
// since it is the one in daily use. Harvesting that would file account B's token
// under account A's name and identity, after which nothing offline can tell: the
// token is opaque, so live, snapshot and doctor all agree on a label that is
// simply wrong. The global recapture refuses on the same evidence
// (keepSnapshotIdentity), through the same predicate (identityDiffers).
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
	acc account.Account, credDir string,
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
		switch outside, err := identityTargetEscapes(sp.Target, credDir); {
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
		_, storedIsRecord := freshness.DecodeObject(stored)
		_, liveIsRecord := freshness.DecodeObject(live.Data)
		if !storedIsRecord || !liveIsRecord {
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
// credDir, so the tool *names* the account whose credential is now there.
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
func writeDirIdentity(ctx context.Context, be secret.Backend, specs []artifact.Spec, acc account.Account, credDir string) error {
	for _, sp := range specs {
		if !sp.IdentityOnly {
			continue
		}
		if outside, err := identityTargetEscapes(sp.Target, credDir); err != nil {
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

// identityTargetEscapes reports whether target resolves outside credDir, i.e.
// whether writing it would leave the store this bind owns.
//
// Both sides are resolved before comparing, because the store path itself can run
// through a symlink (`/tmp` on macOS is `/private/tmp`), and comparing a resolved
// target against an unresolved root would call every write an escape. A target
// that does not exist yet is resolved through its parent — the file kae is about
// to create is inside whatever directory the parent names.
func identityTargetEscapes(target, credDir string) (bool, error) {
	root, err := filepath.EvalSymlinks(credDir)
	if err != nil {
		return false, fmt.Errorf("resolve store dir %s: %w", credDir, err)
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
func (app *App) dirCredentialSpec(ctx context.Context, tool, artName, credDir string) (artifact.Spec, bool, error) {
	specs, err := app.dirSpecs(ctx, tool, credDir)
	if err != nil {
		return artifact.Spec{}, false, err
	}
	sp, ok := specByName(specs, artName)
	return sp, ok, nil
}

// dirSpecs resolves every artifact spec of tool as it applies *inside* credDir, by
// asking the adapter with an env whose isolation variable points there.
func (app *App) dirSpecs(ctx context.Context, tool, credDir string) ([]artifact.Spec, error) {
	envVar := isolationEnvVar(tool)
	if envVar == "" {
		return nil, errf(constants.ExitUnsupported,
			"%s has no per-directory isolation mechanism", tool)
	}
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
	env := app.Env
	innerGetenv, innerLookup := app.Env.Getenv, app.Env.LookupEnv
	env.Getenv = func(key string) string {
		if key == envVar {
			return credDir
		}
		return innerGetenv(key)
	}
	env.LookupEnv = func(key string) (string, bool) {
		if key == envVar {
			return credDir, true
		}
		if innerLookup == nil {
			value := innerGetenv(key)
			return value, value != ""
		}
		return innerLookup(key)
	}
	specs, err := adp.Artifacts(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("resolve %s artifacts for %s: %w", tool, credDir, err)
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
}

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
func (app *App) dirCredentialStores(pinID string) ([]dirStore, error) {
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
			stores = append(stores, dirStore{Tool: tool, Dir: shared})
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
				stores = append(stores, dirStore{Tool: tool, Dir: dir, Account: acct.Name()})
			}
		}
	}
	return stores, nil
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
// Only a keychain item is removed, and only where the adapter declares the item
// bindable — exactly the class writeDirCredential creates. A file store's
// credential lives *inside* the store directory, which a mode toggle and
// `kae unpin` deliberately leave intact along with its sessions and settings; an
// item, by contrast, is invisible from the directory tree and would otherwise
// hold a credential nothing can find.
//
// Deleting one is unrecoverable, so it harvests first: the item can hold the only
// copy of the account's credential that still refreshes (harvestDirCredential),
// and an item kae could not preserve is kept instead of deleted. A leftover secret
// is a smaller fault than a login destroyed by a cleanup.
func (app *App) pruneDirCredentials(ctx context.Context, be secret.Backend, pinID, onlyTool string,
	keep map[string]bool, prev fragmentInfo, purging bool,
) []string {
	stores, err := app.dirCredentialStores(pinID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kae: warning: %v\n", err)
		return nil
	}
	removals := []string{}
	for _, store := range stores {
		if keep[store.Dir] || (onlyTool != "" && store.Tool != onlyTool) {
			continue
		}
		removed, err := app.removeDirCredential(ctx, be, store, storeAccount(store, prev), purging)
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr,
				"kae: warning: could not remove the superseded %s credential for %s: %v\n",
				store.Tool, store.Dir, err)
		case removed:
			removals = append(removals, fmt.Sprintf(
				"Removed the superseded per-directory %s credential (%s)", store.Tool, store.Dir,
			))
		}
	}
	return removals
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
	accountName string, purging bool,
) (bool, error) {
	tool, credDir := store.Tool, store.Dir
	artName := credentialArtifactName(tool)
	if artName == "" {
		return false, nil
	}
	// Resolved once for the delete and the harvest's identity comparison; a second
	// resolution costs a `security` subprocess for codex (writeDirCredential says why).
	specs, err := app.dirSpecs(ctx, tool, credDir)
	if err != nil {
		return false, err
	}
	sp, ok := specByName(specs, artName)
	if !ok {
		return false, nil
	}
	if sp.Kind != constants.KindKeychain || !sp.KeychainDirBindable {
		return false, nil
	}
	// Probe before deleting, attributes only, so the caller can report what it
	// actually removed: the delete primitive treats "no such item" as success, so
	// without this a store that never had an item is announced as cleaned up. The
	// probe is scoped the way the delete is — account-scoped only for a service that
	// holds more than one legitimate item, since asking with an account a service
	// does not scope by would answer "absent" for an item the delete still removes.
	existed, err := dirItemExists(ctx, sp)
	if err != nil || !existed {
		return false, err
	}
	// Last chance to keep what this item holds. Unlike the overwrite in
	// writeDirCredential, nothing can be reconstructed afterwards: a re-pin
	// re-materializes a store's credential from the account snapshot, so a copy that
	// was never harvested into one is simply gone.
	if !app.harvestBeforeDelete(ctx, be, specs, tool, accountName, credDir, purging) {
		return false, nil
	}
	if err := artifact.ApplyLive(ctx, sp, artifact.Value{Present: false}); err != nil {
		return false, err
	}
	return true, nil
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
	tool, accountName, credDir string, purging bool,
) (mayDelete bool) {
	artName := credentialArtifactName(tool)
	sp, ok := specByName(specs, artName)
	if !rotatesSingleUse(tool) || !ok {
		return true
	}
	switch _, _, state := readLiveCredential(ctx, tool, sp); state {
	case liveNothing:
		return true
	case liveUnreadable:
		fmt.Fprintf(os.Stderr,
			"kae: warning: kae cannot read the %s credential in %s, so it is left in place instead of deleted "+
				"(a payload kae does not recognize may still be a working login)\n", tool, credDir)
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
		// than as "nowhere to harvest it, now or ever", which is false after a
		// `kae account rename` — the renamed snapshot exists and could have received the
		// copy; kae does not track renames, so it cannot tell that from a removal. Only a
		// caller that was *asked* to delete these credentials acts on it: keeping it
		// otherwise strands a live token no kae command can address, while deleting it
		// during housekeeping destroys a login nobody asked kae to touch.
		fmt.Fprintf(os.Stderr,
			"kae: warning: no account named %s/%s exists any more, so the %s credential this directory held "+
				"for it is deleted without being kept anywhere (%s); if that account was renamed rather "+
				"than removed, re-bind first (kae pin %s <new name>) and it is harvested instead\n",
			tool, accountName, tool, credDir, tool)
	case exitOf(err) == constants.ExitNotFound:
		// Same condition, and this is *housekeeping* rather than a purge — which
		// `kae account rename` reaches through kae's own re-bind remedy (`kae pin <tool>
		// <new name>`), and which used to delete the newest copy of the renamed account's
		// credential. Worded from the fact rather than from the error: snapshotCredential
		// says "not captured yet (run: kae add --no-login …)", the wrong instruction for an
		// account the user removed or renamed on purpose.
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
		_, preserved, refused := app.harvestDirCredential(ctx, be, specs, tool, accountName, acc, credDir, snapshot)
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
// is writing, and the two operations that hurt most move the binding to a
// *different* store: a `-s` ↔ `-i` toggle, and an isolated re-bind that re-keys the
// store by account. There the new store is built from the account snapshot while the
// copy the tool actually refreshed sits in the old store — so without this pass the
// directory the user just bound holds the copy rotation has already invalidated,
// with every offline check green (measured by review, 2026-08-04).
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
// once in writeDirCredential. That is one extra `security` call per bound tool, in
// exchange for the case where the two are not the same store.
func (app *App) harvestSupersededDirCredentials(ctx context.Context, be secret.Backend,
	pinID, dir, onlyTool string, prev fragmentInfo,
) {
	// Cleared here, not left to live as long as the App: the coordination with
	// writeDirCredential's backstop is scoped to **one bind**, and a stale entry
	// suppresses a report the next bind owes. Production builds an App per command so
	// this never showed, and it made a test pass for the wrong reason — its second bind
	// was silenced by the first one's mark (measured, 2026-08-04).
	app.refusalReported = nil
	stores, err := app.dirCredentialStores(pinID)
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
		specs, err := app.dirSpecs(ctx, store.Tool, store.Dir)
		if err != nil {
			continue // an unresolvable store is the bind's problem, and it reports it
		}
		acc, snapshot, _, err := app.snapshotCredential(ctx, be, store.Tool, accountName, artName)
		if err != nil {
			continue // no snapshot to harvest into; the bind or the sweep reports it
		}
		_, _, refused := app.harvestDirCredential(ctx, be, specs, store.Tool, accountName, acc, store.Dir, snapshot)
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
			app.markRefusalReported(store.Dir)
			fmt.Fprintf(os.Stderr,
				"kae: warning: the %s credential in %s belongs to an account other than %s/%s (%s), so "+
					"kae is not harvesting it and this bind replaces it\n",
				store.Tool, store.Dir, store.Tool, accountName, refused.Why)
		default:
			// Missing evidence rather than a conflict: the copy may well be this account's,
			// so the directory may be left with an older credential — and here the remedy is
			// right, because dir is the bound directory rather than the store.
			app.markRefusalReported(store.Dir)
			fmt.Fprintf(os.Stderr,
				"kae: warning: kae could not preserve the %s credential this directory held for %s/%s "+
					"(%s), and this bind replaces it; %s\n",
				store.Tool, store.Tool, accountName, refused.Why, pinLoginRemedy(store.Tool, dir))
		}
	}
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
	for _, bound := range stores {
		info, ok := app.dirCredentialFreshness(ctx, dirStore{Tool: bound.Tool, Dir: bound.StoreDir})
		if !ok {
			continue
		}
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
	// one account's identity ref three times, because this check, credentialHealthChecks
	// and identityDriftChecks each open their own cache (it was four before this became
	// one of them). Hoisting `secret.WithReadCache` into buildDoctor and keeping only the
	// per-check `Cached(be)` would make it one — safe, since doctor never writes — but it
	// edits a second check's wrapping for a read-only warn path, so it is recorded rather
	// than taken here.
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
		specs, err := app.dirSpecs(ctx, bound.Tool, bound.StoreDir)
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
func pinIdentityDriftMessage(bound boundDirStore) string {
	return fmt.Sprintf(
		"the %s identity cache in %s names an account other than %s/%s, which that directory binds: "+
			"either something logged in there as another account — in which case that directory is running "+
			"an account its binding does not name — or kae could not apply the identity when it bound the "+
			"directory, and %s displays the wrong account while running the bound one (kae cannot tell "+
			"those apart offline). To make the binding true again: cd %s && kae pin %s %s, which replaces "+
			"what is in that store; to keep what is there instead, bind the directory to that account",
		bound.Tool, bound.Dir, bound.Tool, bound.Account,
		bound.Tool, bound.Dir, bound.Tool, bound.Account,
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
//   - a directory that is **gone**: `pinChecks` reports its orphaned store, and
//     naming it here too would report one problem as two.
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
	pins, err := app.pinnedDirs()
	if err != nil {
		return nil // pinChecks already reports an unreadable store root; not twice
	}
	stores := []boundDirStore{}
	for _, pin := range pins {
		if !dirExists(pin.Dir) {
			continue
		}
		fragment, exists, ferr := readFragmentAt(pin.Dir)
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
// that directory. The isolation variable the directory exports is what makes the
// tool write to the store kae bound, so the login lands in the right place with no
// kae step afterwards.
//
// Deliberately not `kae pin` (which would re-copy the account snapshot): that
// snapshot may be just as expired as this copy, in which case re-binding would
// report success and change nothing.
func pinLoginRemedy(tool, dir string) string {
	if login := loginCommand(tool); login != nil {
		return fmt.Sprintf("log in inside that directory: cd %s && %s", dir, strings.Join(login, " "))
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
	sp, ok, err := app.dirCredentialSpec(ctx, store.Tool, artName, store.Dir)
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
