package cmd

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// runRebind re-binds one tool's credential inside a pinned directory to a
// different account without changing the sharing set:
//
//	kae pin <tool> <account>
//
// Valid only inside a directory bound with `kae pin` (it reads the kae-owned
// fragment). For the shared mechanism the dir is account-agnostic so the
// credential is overwritten in place; for the isolated mechanism the config dir
// is re-keyed to the new account and the fragment's env entry is repointed. The
// fragment's account record and KAE_PROFILE are recomputed (the latter goes
// empty when the new account set matches no named profile). Sessions and
// settings are never disturbed.
func runRebind(ctx context.Context, app *App, opts commonOpts, tool, accountName string) int {
	tool, err := canonicalToolAccount(tool, accountName, "account")
	if err != nil {
		return finish(opts, err)
	}
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	if isolationEnvVar(tool) == "" {
		return finish(opts, errf(constants.ExitUnsupported,
			"%s has no per-directory isolation mechanism; nothing to re-bind", tool))
	}
	info, exists, err := readDirFragment()
	if err != nil {
		return finish(opts, err)
	}
	if !exists {
		return finish(opts, errf(constants.ExitUnsupported,
			"this directory is not pinned; run `kae pin` first"))
	}
	if _, bound := info.Accounts[tool]; !bound {
		return finish(opts, errf(constants.ExitNotFound,
			"%s is not bound in this directory; re-pin the profile to include it", tool))
	}

	absDir, err := cwdAbs()
	if err != nil {
		return finish(opts, err)
	}
	pinID := paths.PinID(absDir)
	be, err := app.secretBackend()
	if err != nil {
		return finish(opts, err)
	}

	// KAE_PROFILE follows the directory's effective per-tool accounts: the
	// global state overlaid with the fragment's isolated bindings and this
	// re-bind. It is the matching named profile, or empty (ad-hoc) when none
	// matches — status reads the real per-tool account regardless.
	st, err := app.loadState()
	if err != nil {
		return finish(opts, err)
	}
	effective := make(map[string]string, len(st.Active)+len(info.Accounts)+1)
	for k, v := range st.Active {
		effective[k] = v
	}
	for k, v := range info.Accounts {
		effective[k] = v
	}
	effective[tool] = accountName
	profile := app.Config.MatchProfile(effective)

	var envDir string // fragment env entry to repoint (isolated only)
	switch info.Mode {
	case paths.SharedSegment:
		sharedDir := app.Paths.SharedDir(pinID, tool)
		if err := app.swapDirCredential(ctx, be, tool, accountName, sharedDir); err != nil {
			return finish(opts, fmt.Errorf("swap shared credential for %s: %w", tool, err))
		}
	case paths.IsolatedSegment:
		newDir, err := app.preparePinConfig(ctx, tool, accountName, pinID)
		if err != nil {
			return finish(opts, fmt.Errorf("prepare isolated config for %s/%s: %w", tool, accountName, err))
		}
		envDir = newDir
	default:
		return finish(opts, errf(constants.ExitError,
			"fragment %s has an unrecognized mode %q", fragmentRelPath, info.Mode))
	}
	// Companions are profile-scoped, so re-bind them to the recomputed profile:
	// a profile match re-applies its bindings (regenerating the git-config file),
	// while an ad-hoc set (profile == "") yields no entries and clears them.
	companionEntries, redactions, prepareCompanions, err := app.companionPlan(profile)
	if err != nil {
		return finish(opts, err)
	}
	if err := prepareCompanions(); err != nil {
		return finish(opts, err)
	}
	companionLines := companionFragmentLines(companionEntries)
	if err := rebindFragment(tool, accountName, envDir, profile, companionLines, redactions); err != nil {
		return finish(opts, fmt.Errorf("update %s: %w", fragmentRelPath, err))
	}
	fmt.Printf("Re-bound %s to account %s (%s; sessions/settings unchanged)\n", tool, accountName, info.Mode)
	return constants.ExitOK
}

// swapDirCredential reads the captured credential for tool/accountName from
// the secret backend and writes it as the private credential file in credDir.
//
// KindJSONPointer snapshots (Linux claude) store only the pointer value, not
// the wrapper file. The wrapper is reconstructed here via patch.SetPointer so
// the resulting file has the correct structure (e.g. {"claudeAiOauth":{...}}).
// KindKeychain (macOS claude) and KindFile snapshots store verbatim file
// content and are written directly.
func (app *App) swapDirCredential(ctx context.Context, be secret.Backend, tool, accountName, credDir string) error {
	acc, found, err := account.Load(app.Paths.AccountDir(tool, accountName))
	if err != nil {
		return err
	}
	if !found {
		return errf(constants.ExitNotFound,
			"account %s/%s is not captured yet (run: kae add --no-login %s %s)",
			tool, accountName, tool, accountName)
	}
	artName := credentialArtifactName(tool)
	if artName == "" {
		return nil
	}
	metaArt, ok := acc.Artifacts[artName]
	if !ok || !metaArt.Present {
		return errf(constants.ExitAuthMissing,
			"account %s/%s has no credential snapshot; re-run kae add --no-login %s %s",
			tool, accountName, tool, accountName)
	}
	data, found, err := be.Get(ctx, metaArt.SecretRef)
	if err != nil {
		return fmt.Errorf("read snapshot credential: %w", err)
	}
	if !found {
		return errf(constants.ExitError,
			"snapshot payload missing; re-run kae add --no-login %s %s", tool, accountName)
	}

	// Resolve the artifact spec to determine write semantics.
	var credSpec *artifact.Spec
	if adp, aerr := adapter.ForTool(tool); aerr == nil {
		if specs, serr := adp.Artifacts(ctx, app.Env); serr == nil {
			for i := range specs {
				if specs[i].Name == artName {
					credSpec = &specs[i]
					break
				}
			}
		}
	}

	for _, credFile := range app.pinCredItems(tool) {
		dst := filepath.Join(credDir, credFile)
		if credSpec != nil && credSpec.Kind != constants.KindKeychain {
			// For KindJSONPointer and KindFile, redirect ApplyLive to credDir.
			// ApplyLive handles wrapper reconstruction and JSONC, keeping IO
			// logic in the artifact package.
			sp := *credSpec
			sp.Target = dst
			if err := artifact.ApplyLive(ctx, sp, artifact.Value{Data: data, Present: true}); err != nil {
				return fmt.Errorf("write credential %s: %w", dst, err)
			}
		} else {
			// KindKeychain: snapshot payload is the verbatim JSON object;
			// write directly to credDir as a file.
			if err := patch.WriteFileAtomic(dst, data, 0o600); err != nil {
				return fmt.Errorf("write credential %s: %w", dst, err)
			}
		}
	}
	return nil
}

// credentialArtifactName returns the snapshot artifact name that holds the
// tool's primary credential (matched by pinCredItems). Empty = no credential.
func credentialArtifactName(tool string) string {
	switch tool {
	case constants.ToolClaude:
		return "claude_ai_oauth"
	case constants.ToolCodex:
		return "auth"
	default:
		return ""
	}
}
