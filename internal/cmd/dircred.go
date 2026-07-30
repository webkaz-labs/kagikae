package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
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
	// not the service name — but kae has never verified a bound directory end to
	// end there (does codex resolve the bond dir to the same canonical path kae
	// hashes?), so the capability stays undeclared rather than assumed.
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
// materialized: the tool it belongs to and the config dir the tool reads.
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

// pruneDirCredentials removes the per-directory keychain credential of every
// store of this pin that keep does not name, and returns one line per removal
// plus one per failure. Nothing fails the caller: the new binding is already
// correct, and a store kae could not clean is a leftover secret, not a broken
// bind — so it is reported, never escalated.
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
		return []string{fmt.Sprintf("kae: warning: %v", err)}
	}
	lines := []string{}
	for _, store := range stores {
		if keep[store.Dir] || (onlyTool != "" && store.Tool != onlyTool) {
			continue
		}
		removed, err := app.removeDirCredential(ctx, store.Tool, store.Dir)
		switch {
		case err != nil:
			lines = append(lines, fmt.Sprintf(
				"kae: warning: could not remove the superseded %s credential for %s: %v",
				store.Tool, store.Dir, err,
			))
		case removed:
			lines = append(lines, fmt.Sprintf(
				"Removed the superseded per-directory %s credential (%s)", store.Tool, store.Dir,
			))
		}
	}
	return lines
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
	if err := artifact.ApplyLive(ctx, sp, artifact.Value{Present: false}); err != nil {
		return false, err
	}
	return true, nil
}
