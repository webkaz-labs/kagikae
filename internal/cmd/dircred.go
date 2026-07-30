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
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// errGlobalCredentialStore reports that a tool keeps one global credential store
// that no isolation env var namespaces, so a bound directory cannot have its own
// copy of it and kae must not write it.
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
		fmt.Fprintf(os.Stderr,
			"kae: warning: %s keeps one global credential store, so this directory shares %s's login "+
				"with every other directory (its settings and sessions are still isolated)\n", tool, tool)
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
	// belongs to that directory. codex's keyring item is one global `Codex Auth`
	// whatever CODEX_HOME says, so writing it here would overwrite the *global*
	// login — and KeychainReplace deletes the prior item first, leaving nothing to
	// restore. Refuse before touching anything; the caller decides whether one
	// unisolatable tool is fatal.
	if sp.Kind == constants.KindKeychain && !sp.KeychainDirScoped {
		return fmt.Errorf("%w: %s keeps one global credential store that %s does not namespace",
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
	env := app.Env
	inner := app.Env.Getenv
	env.Getenv = func(key string) string {
		if key == envVar {
			return credDir
		}
		return inner(key)
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

// wholeDocumentKind reports whether a spec kind stores the artifact's *whole*
// document rather than the value under a JSON pointer. It is the shape a payload
// has, and the two families are not interchangeable: KindFile and KindKeychain
// round-trip the entire document, while KindJSONPointer carries only the inner
// pointer value.
func wholeDocumentKind(kind string) bool {
	return kind == constants.KindFile || kind == constants.KindKeychain
}

// checkPayloadShape refuses to apply a snapshot whose payload has the other
// shape from what the destination spec expects, because one of those transitions
// corrupts silently rather than failing: applying a whole-document payload
// through a pointer spec nests it under its own key
// (`{"claudeAiOauth":{"claudeAiOauth":…}}`), which claude reads as a malformed
// credential. The reverse writes an inner object as a whole document.
//
// Reachable by capturing under one driver and applying under another — the
// KAE_CLAUDE_DRIVER / [tools.claude] driver override does exactly that. A
// KindFile/KindKeychain transition is allowed: both are whole documents, which is
// what makes codex's auth.json and its keyring item the same bytes.
//
// An empty storedKind is a snapshot from before the kind was recorded, or an
// identity artifact absent from an older snapshot: nothing to compare, so nothing
// to refuse. The rollback path needs no such check — specFromRecord rebuilds the
// spec *from the backup record*, so its kind is the one the payload was captured
// under by construction.
func checkPayloadShape(tool, accountName, artName, storedKind, destKind string) error {
	if storedKind == "" || wholeDocumentKind(storedKind) == wholeDocumentKind(destKind) {
		return nil
	}
	return errf(constants.ExitUnsafeRefused,
		"account %s/%s captured %s as %q but this environment resolves it as %q, "+
			"and the two payload shapes are not interchangeable; recapture with "+
			"`kae add --no-login %s %s` under the current driver",
		tool, accountName, artName, storedKind, destKind, tool, accountName)
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
