package cmd

import (
	"context"
	"fmt"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/keychain"
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
	// The harvest reads a per-directory store twice in one command — the pin-level pass
	// classifies it, then the materializer's chokepoint reads it again before writing —
	// and reads that account's snapshot payload twice with it. Both are the exact shape
	// the switch path already coalesces, so opt in rather than restructure signatures to
	// save a `security` call (docs/RELEASE.md §A/§C). Safe here for the same reason it is
	// safe there: nothing between the two reads writes that store (the harvest writes
	// only the account snapshot, and a write invalidates its own cache entry), and no
	// child process runs during a bind — which is the one case both caches warn against.
	ctx = keychain.WithReadCache(secret.WithReadCache(ctx))
	be = secret.Cached(be)

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

	// The mode is validated before anything is written, including the harvest below: a
	// command that is going to refuse an unrecognized fragment must not leave an
	// account snapshot rewritten behind it.
	if info.Mode != paths.SharedSegment && info.Mode != paths.IsolatedSegment {
		return finish(opts, errf(constants.ExitError,
			"fragment %s has an unrecognized mode %q", fragmentRelPath, info.Mode))
	}
	// Before either branch writes a store: in isolated mode the binding moves to a
	// store keyed by the new account, and in shared mode it stays in one store whose
	// credential still belongs to the *previous* account — and `info` is the only
	// thing that names it. Harvesting here is what keeps a re-bind from destroying the
	// login it is re-binding away from (docs/ROADMAP.md).
	app.harvestSupersededDirCredentials(ctx, be, pinID, absDir, tool, info)

	var envDir string   // fragment env entry to repoint (isolated only)
	var boundDir string // the store this tool reads after the re-bind
	switch info.Mode {
	case paths.SharedSegment:
		// prepareBond, not writeDirCredential alone: the bond dir also holds the
		// symlinks to the real home that carry settings and sessions, and only
		// prepareBond re-creates them. Writing just the credential left a re-bind
		// unable to repair a bond dir that had been wiped, while the isolated
		// branch below (preparePinConfig) could — an asymmetry with no reason
		// behind it. prepareBond writes the credential too, and is idempotent.
		sharedDir, err := app.prepareBond(ctx, be, tool, accountName, pinID)
		if err != nil {
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
		// Unreachable: the same check runs above, before the harvest, so a refused mode
		// costs nothing. Kept so the switch stays total if that check ever moves.
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
	// `info` is the binding this re-bind replaced, which is what names the account a
	// shared store's credential belongs to (storeAccount) — read before
	// rebindFragment rewrote it.
	reportPruned(app.pruneDirCredentials(ctx, be, pinID, tool, map[string]bool{boundDir: true}, info, false))
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
