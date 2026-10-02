package cmd

// Live-credential reads, the directory identity label, and the bindDirs and
// artifact-spec resolution the directory credential code shares.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

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
	// tool's" the dircred files exist to close.
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
		return account.Account{}, nil, "", errUncapturedWithRemedy(tool, accountName)
	}
	metaArt, ok := acc.Artifacts[artName]
	if !ok || !metaArt.Present {
		return account.Account{}, nil, "", errf(constants.ExitAuthMissing,
			"account %s/%s has no credential snapshot; %s",
			tool, accountName, verifiedCaptureRemedy(tool, accountName))
	}
	data, found, err := be.Get(ctx, metaArt.SecretRef)
	if err != nil {
		return account.Account{}, nil, "", fmt.Errorf("read snapshot credential: %w", err)
	}
	if !found {
		return account.Account{}, nil, "", errf(constants.ExitError,
			"snapshot payload %s is missing; %s", metaArt.SecretRef, verifiedCaptureRemedy(tool, accountName))
	}
	return acc, data, metaArt.Kind, nil
}
