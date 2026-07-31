package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
// read it.
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
	sp, ok, err := app.dirCredentialSpec(ctx, tool, artName, credDir)
	if err != nil {
		return err
	}
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
	if sp.Kind == constants.KindKeychain && !sp.KeychainDirBindable {
		return fmt.Errorf("%w: kae cannot give this directory its own %s credential store (%s)",
			errGlobalCredentialStore, tool, isolationEnvVar(tool))
	}
	data, storedKind, err := app.snapshotCredential(ctx, be, tool, accountName, artName)
	if err != nil {
		return err
	}
	if err := checkPayloadShape(tool, accountName, artName, storedKind, sp.Kind); err != nil {
		return err
	}
	if err := artifact.ApplyLive(ctx, sp, artifact.Value{Data: data, Present: true}); err != nil {
		return fmt.Errorf("write %s credential for account %s: %w", tool, accountName, err)
	}
	if sp.Kind != constants.KindKeychain {
		return nil
	}
	// The keychain item is what the tool reads (reads try it first and only fall
	// back to the file), so once kae has written it a plaintext copy in the bound
	// directory is a credential nothing reads. The tool removes that file itself
	// only when a write finds no keychain item, which after this one never happens
	// again — so kae removes it, rather than leaving a stale secret on disk
	// forever.
	for _, name := range app.pinCredItems(tool) {
		stale := filepath.Join(credDir, name)
		if err := os.Remove(stale); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove superseded credential copy %s: %w", stale, err)
		}
	}
	return nil
}

// dirCredentialSpec resolves the tool's credential spec as it applies *inside*
// credDir, by asking the adapter with an env whose isolation variable points
// there. ok is false when this platform has no artifact by that name.
func (app *App) dirCredentialSpec(ctx context.Context, tool, artName, credDir string) (artifact.Spec, bool, error) {
	envVar := isolationEnvVar(tool)
	if envVar == "" {
		return artifact.Spec{}, false, errf(constants.ExitUnsupported,
			"%s has no per-directory isolation mechanism", tool)
	}
	adp, err := adapter.ForTool(tool)
	if err != nil {
		return artifact.Spec{}, false, err
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
		return artifact.Spec{}, false, fmt.Errorf("resolve %s artifacts for %s: %w", tool, credDir, err)
	}
	for _, sp := range specs {
		if sp.Name == artName {
			return sp, true, nil
		}
	}
	return artifact.Spec{}, false, nil
}

// snapshotCredential returns the captured credential payload for tool/account
// together with the spec kind it was captured as, which fixes the payload's
// shape (checkPayloadShape).
//
// The snapshot is the only correct source for a per-directory bind: the live
// store holds whichever account is globally active, which is the account being
// bound only by coincidence.
func (app *App) snapshotCredential(ctx context.Context, be secret.Backend, tool, accountName, artName string) ([]byte, string, error) {
	acc, found, err := account.Load(app.Paths.AccountDir(tool, accountName))
	if err != nil {
		return nil, "", err
	}
	if !found {
		return nil, "", errf(constants.ExitNotFound,
			"account %s/%s is not captured yet (run: kae add --no-login %s %s)",
			tool, accountName, tool, accountName)
	}
	metaArt, ok := acc.Artifacts[artName]
	if !ok || !metaArt.Present {
		return nil, "", errf(constants.ExitAuthMissing,
			"account %s/%s has no credential snapshot; re-run kae add --no-login %s %s",
			tool, accountName, tool, accountName)
	}
	data, found, err := be.Get(ctx, metaArt.SecretRef)
	if err != nil {
		return nil, "", fmt.Errorf("read snapshot credential: %w", err)
	}
	if !found {
		return nil, "", errf(constants.ExitError,
			"snapshot payload missing; re-run kae add --no-login %s %s", tool, accountName)
	}
	return data, metaArt.Kind, nil
}

// dirStore is one per-directory credential store a bound directory has
// materialized. Dir is the config dir the tool reads, i.e. what its isolation env
// var points at, which is what resolves the store's item identity.
type dirStore struct {
	Tool string
	Dir  string
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
				stores = append(stores, dirStore{Tool: tool, Dir: dir})
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
// Only a keychain item is removed, and only where the adapter declares the item
// bindable — exactly the class writeDirCredential creates. A file store's
// credential lives *inside* the store directory, which a mode toggle and
// `kae unpin` deliberately leave intact along with its sessions and settings; an
// item, by contrast, is invisible from the directory tree and would otherwise
// hold a credential nothing can find.
func (app *App) pruneDirCredentials(ctx context.Context, pinID, onlyTool string, keep map[string]bool) []string {
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
		removed, err := app.removeDirCredential(ctx, store.Tool, store.Dir)
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

// removeDirCredential deletes the keychain item one store directory's tool reads,
// reporting whether there was one to delete. It resolves the item the same way
// writeDirCredential does — by asking the adapter with an env pointed at credDir
// — so the item removed is the one that directory owns and never a global login.
func (app *App) removeDirCredential(ctx context.Context, tool, credDir string) (bool, error) {
	artName := credentialArtifactName(tool)
	if artName == "" {
		return false, nil
	}
	sp, ok, err := app.dirCredentialSpec(ctx, tool, artName, credDir)
	if err != nil || !ok {
		return false, err
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
	if err := artifact.ApplyLive(ctx, sp, artifact.Value{Present: false}); err != nil {
		return false, err
	}
	return true, nil
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
// It reads live, unlike the snapshot half: up to one store read per bound
// directory per tool that has a credential kae materializes (claude and codex
// only, so the fan-out is small). On darwin a claude store read is one
// attributes-plus-payload `security` call, the same call Detect already makes for
// the global item.
//
// Deliberately not paired with a recapture into the account snapshot: several
// directories can bind one account, each refreshing its own token, so there is no
// non-arbitrary answer to which of them the single global snapshot should take —
// and a directory not visited in weeks would overwrite a newer global one. Telling
// the user is the part with one right answer; docs/ROADMAP.md carries the rest.
func (app *App) pinCredentialChecks(ctx context.Context) []adapter.Check {
	pins, err := app.pinnedDirs()
	if err != nil {
		return nil // pinChecks already reports an unreadable store root; not twice
	}
	checks := []adapter.Check{}
	now := app.Now()
	for _, pin := range pins {
		// A directory that is gone is pinChecks' finding (its whole store is
		// orphaned); the freshness of a credential nothing can reach is moot, and
		// reporting both would name the same directory twice for one problem.
		// Decided by the directory, never by a failed read of the fragment inside it
		// — the same rule pinChecks states, for the same reason.
		if !dirExists(pin.Dir) {
			continue
		}
		fragment, exists, ferr := readFragmentAt(pin.Dir)
		switch {
		case ferr != nil:
			continue // pinChecks reports an unreadable fragment; not twice
		case !exists:
			// Unpinned. `kae unpin` keeps the store on purpose so a re-pin restores
			// its sessions, but nothing in that directory points at it any more: a
			// finding here would say "bound to" about a directory that is not, and
			// name a login that would land somewhere else. pinChecks skips it too.
			continue
		}
		// Canonical tool order, so the report is deterministic — a JSON contract
		// must not reorder with a map iteration.
		for _, tool := range constants.Tools {
			credDir, bound := app.boundStoreDir(pin.PinID, tool, fragment)
			if !bound || !dirExists(credDir) {
				continue
			}
			store := dirStore{Tool: tool, Dir: credDir}
			info, ok := app.dirCredentialFreshness(ctx, store)
			if !ok {
				continue
			}
			switch cred := credentialStateAt(info, now); cred.State {
			case constants.CredentialStale:
				checks = append(checks, adapter.Check{
					Tool: store.Tool, Code: constants.CheckCredentialStale,
					Status: constants.StatusWarn,
					Message: fmt.Sprintf("the %s credential bound to %s is stale: %s; %s",
						store.Tool, pin.Dir, staleCredentialReason(info, store.Tool),
						pinLoginRemedy(store.Tool, pin.Dir)),
				})
			case constants.CredentialExpiring:
				checks = append(checks, adapter.Check{
					Tool: store.Tool, Code: constants.CheckCredentialExpiring,
					Status: constants.StatusWarn,
					Message: fmt.Sprintf("the %s credential bound to %s needs an interactive re-login in %s (%s); %s",
						store.Tool, pin.Dir, roundDays(cred.ReloginBy.Sub(now)), utcStamp(cred.ReloginBy),
						pinLoginRemedy(store.Tool, pin.Dir)),
				})
			}
		}
	}
	return checks
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
	if sp.Kind == constants.KindKeychain && !sp.KeychainDirBindable {
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
