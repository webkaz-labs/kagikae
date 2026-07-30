package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

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
	data, err := app.snapshotCredential(ctx, be, tool, accountName, artName)
	if err != nil {
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

// snapshotCredential returns the captured credential payload for tool/account.
// The snapshot is the only correct source for a per-directory bind: the live
// store holds whichever account is globally active, which is the account being
// bound only by coincidence.
func (app *App) snapshotCredential(ctx context.Context, be secret.Backend, tool, accountName, artName string) ([]byte, error) {
	acc, found, err := account.Load(app.Paths.AccountDir(tool, accountName))
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errf(constants.ExitNotFound,
			"account %s/%s is not captured yet (run: kae add --no-login %s %s)",
			tool, accountName, tool, accountName)
	}
	metaArt, ok := acc.Artifacts[artName]
	if !ok || !metaArt.Present {
		return nil, errf(constants.ExitAuthMissing,
			"account %s/%s has no credential snapshot; re-run kae add --no-login %s %s",
			tool, accountName, tool, accountName)
	}
	data, found, err := be.Get(ctx, metaArt.SecretRef)
	if err != nil {
		return nil, fmt.Errorf("read snapshot credential: %w", err)
	}
	if !found {
		return nil, errf(constants.ExitError,
			"snapshot payload missing; re-run kae add --no-login %s %s", tool, accountName)
	}
	return data, nil
}
