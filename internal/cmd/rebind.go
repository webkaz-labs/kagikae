package cmd

import (
	"context"
	"fmt"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
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
	// A directory bound by a kae older than the breadcrumb has none, and a
	// re-bind is the other moment kae knows both halves, so beginBind backfills.
	pinLock, err := app.beginBind(absDir)
	if err != nil {
		return finish(opts, err)
	}
	defer pinLock.Release()
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

	var envDir string   // fragment env entry to repoint (isolated only)
	var boundDir string // the store this tool reads after the re-bind
	switch info.Mode {
	case paths.SharedSegment:
		sharedDir := app.Paths.SharedDir(pinID, tool)
		if err := app.writeDirCredential(ctx, be, tool, accountName, sharedDir); err != nil {
			return finish(opts, fmt.Errorf("swap shared credential for %s: %w", tool, err))
		}
		boundDir = sharedDir
	case paths.IsolatedSegment:
		newDir, err := app.preparePinConfig(ctx, be, tool, accountName, pinID)
		if err != nil {
			return finish(opts, fmt.Errorf("prepare isolated config for %s/%s: %w", tool, accountName, err))
		}
		envDir = newDir
		boundDir = newDir
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
	// In isolated mode the store is keyed by account, so the previous account's dir
	// is now unreachable and its keychain item would keep that credential with
	// nothing pointing at it. Scoped to this tool: a sibling tool's store is still
	// bound by the same fragment. (Shared mode re-uses one dir, so nothing is stale.)
	reportPruned(app.pruneDirCredentials(ctx, pinID, tool, map[string]bool{boundDir: true}))
	fmt.Printf("Re-bound %s to account %s (%s; sessions/settings unchanged)\n", tool, accountName, info.Mode)
	return constants.ExitOK
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
