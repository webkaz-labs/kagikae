package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/secret"
	"github.com/webkaz-labs/kagikae/internal/state"
)

// CmdAccount manages captured account lifecycle:
//
//	kae account rm <tool> <account> [--force] [--dry-run]
//	kae account rename <tool> <old> <new> [--dry-run]
//	kae account set-identity <tool> <account> <value> [--dry-run]
//
// (kae accounts, plural, lists them.)
func CmdAccount(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return usageError("usage: %s account rm|rename|set-identity ...", toolName)
	}
	switch args[0] {
	case "rm", "remove":
		return cmdAccountRm(ctx, args[1:])
	case "rename", "mv":
		return cmdAccountRename(ctx, args[1:])
	case "set-identity":
		return cmdAccountSetIdentity(ctx, args[1:])
	default:
		return usageError("unknown account subcommand %q (rm, rename, set-identity)", args[0])
	}
}

type accountRmReport struct {
	SchemaVersion   int      `json:"schema_version"`
	OK              bool     `json:"ok"`
	DryRun          bool     `json:"dry_run"`
	Tool            string   `json:"tool"`
	Account         string   `json:"account"`
	SecretsRemoved  int      `json:"secrets_removed"`
	ProfilesUpdated []string `json:"profiles_updated"`
	ActiveCleared   bool     `json:"active_cleared"`
}

func cmdAccountRm(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	force := false
	opts, ok := parseCommon("account rm", flags, true, func(fs *flag.FlagSet) {
		registerAccountRmFlags(fs, &force)
	})
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 2 {
		return usageError("usage: %s account rm <tool> <account> [--force]", toolName)
	}
	tool, accountName := positionals[0], positionals[1]
	report, err := buildAccountRm(ctx, newApp(opts.ConfigPath), opts, tool, accountName, force)
	if err != nil {
		return finish(opts, err)
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	printAccountRm(report)
	return constants.ExitOK
}

func buildAccountRm(ctx context.Context, app *App, opts commonOpts, tool, accountName string, force bool) (*accountRmReport, error) {
	tool, err := canonicalToolAccount(tool, accountName, "account")
	if err != nil {
		return nil, err
	}
	if err := app.requireConfig(); err != nil {
		return nil, err
	}
	acc, found, err := account.Load(app.Paths.AccountDir(tool, accountName))
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errf(constants.ExitNotFound, "account %s/%s is not captured", tool, accountName)
	}

	st, err := app.loadState()
	if err != nil {
		return nil, err
	}
	active := st.Active[tool] == accountName
	if active && !force {
		return nil, errf(constants.ExitUnsafeRefused,
			"%s/%s is the active account; switch away first or rerun with --force", tool, accountName)
	}
	profiles := app.profilesReferencing(tool, accountName)

	report := &accountRmReport{
		SchemaVersion: constants.SchemaVersion, OK: true, DryRun: opts.DryRun,
		Tool: tool, Account: accountName,
		SecretsRemoved: len(acc.ArtifactNames()), ProfilesUpdated: profiles,
		ActiveCleared: active,
	}
	if opts.DryRun {
		return report, nil
	}

	be, err := app.secretBackend()
	if err != nil {
		return nil, err
	}
	locks, err := app.acquireLocks([]string{tool})
	if err != nil {
		return nil, err
	}
	defer releaseLocks(locks)
	cfgLock, err := app.acquireConfigLock()
	if err != nil {
		return nil, err
	}
	defer cfgLock.Release()

	// Logical removal first (config + state), then physical cleanup (snapshot
	// dir, secrets). A failure after this point leaves at most an orphaned
	// keychain item, which kae doctor flags — never a half-edited config.
	if len(profiles) > 0 {
		if err := app.editConfig(func(e *config.Editor) {
			for _, name := range profiles {
				e.RemoveProfileAccount(name, tool)
			}
		}); err != nil {
			return nil, err
		}
	}
	// Whether this account is still the active one is decided *inside* the state
	// lock, not from the copy read above the tool lock: a switch that completed
	// in between makes another account active, and clearing on the stale answer
	// would drop a binding this command was never asked to touch. The report
	// follows what the locked decision did.
	if _, err := app.mutateState(func(st *state.State) {
		report.ActiveCleared = st.Active[tool] == accountName
		if !report.ActiveCleared {
			return
		}
		delete(st.Active, tool)
		st.ActiveProfile = app.Config.MatchProfile(st.Active)
	}); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(app.Paths.AccountDir(tool, accountName)); err != nil {
		return nil, fmt.Errorf("remove snapshot dir: %w", err)
	}
	for _, name := range acc.ArtifactNames() {
		if err := be.Delete(ctx, acc.Artifacts[name].SecretRef); err != nil {
			return nil, fmt.Errorf("delete secret %s: %w", acc.Artifacts[name].SecretRef, err)
		}
	}
	app.warnPinnedAccountGone(tool, accountName, "")
	return report, nil
}

func printAccountRm(r *accountRmReport) {
	verb := "Removed"
	if r.DryRun {
		verb = "Would remove"
	}
	fmt.Printf("%s %s/%s (%d secret item(s))\n", verb, r.Tool, r.Account, r.SecretsRemoved)
	if len(r.ProfilesUpdated) > 0 {
		fmt.Printf("  dropped the %s reference from profile(s): %v\n", r.Tool, r.ProfilesUpdated)
	}
	if r.ActiveCleared {
		fmt.Printf("  cleared the active %s account in state\n", r.Tool)
	}
}

type accountRenameReport struct {
	SchemaVersion   int      `json:"schema_version"`
	OK              bool     `json:"ok"`
	DryRun          bool     `json:"dry_run"`
	Tool            string   `json:"tool"`
	Old             string   `json:"old"`
	New             string   `json:"new"`
	SecretsMoved    int      `json:"secrets_moved"`
	ProfilesUpdated []string `json:"profiles_updated"`
	ActiveUpdated   bool     `json:"active_updated"`
}

func cmdAccountRename(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("account rename", flags, true, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 3 {
		return usageError("usage: %s account rename <tool> <old> <new>", toolName)
	}
	tool, oldName, newName := positionals[0], positionals[1], positionals[2]
	report, err := buildAccountRename(ctx, newApp(opts.ConfigPath), opts, tool, oldName, newName)
	if err != nil {
		return finish(opts, err)
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	printAccountRename(report)
	return constants.ExitOK
}

func buildAccountRename(ctx context.Context, app *App, opts commonOpts, tool, oldName, newName string) (*accountRenameReport, error) {
	tool, err := resolveToolArg(tool)
	if err != nil {
		return nil, err
	}
	if err := validateToolAccount(tool, oldName, "account"); err != nil {
		return nil, err
	}
	if err := validateToolAccount(tool, newName, "account"); err != nil {
		return nil, err
	}
	if oldName == newName {
		return nil, errf(constants.ExitUsage, "old and new account names are the same (%q)", oldName)
	}
	if err := app.requireConfig(); err != nil {
		return nil, err
	}
	acc, found, err := account.Load(app.Paths.AccountDir(tool, oldName))
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errf(constants.ExitNotFound, "account %s/%s is not captured", tool, oldName)
	}
	if _, exists, err := account.Load(app.Paths.AccountDir(tool, newName)); err != nil {
		return nil, err
	} else if exists {
		return nil, errf(constants.ExitUnsafeRefused, "account %s/%s already exists", tool, newName)
	}

	st, err := app.loadState()
	if err != nil {
		return nil, err
	}
	activeUpdate := st.Active[tool] == oldName
	profiles := app.profilesReferencing(tool, oldName)

	report := &accountRenameReport{
		SchemaVersion: constants.SchemaVersion, OK: true, DryRun: opts.DryRun,
		Tool: tool, Old: oldName, New: newName,
		SecretsMoved: len(acc.ArtifactNames()), ProfilesUpdated: profiles,
		ActiveUpdated: activeUpdate,
	}
	if opts.DryRun {
		return report, nil
	}

	be, err := app.secretBackend()
	if err != nil {
		return nil, err
	}
	locks, err := app.acquireLocks([]string{tool})
	if err != nil {
		return nil, err
	}
	defer releaseLocks(locks)
	cfgLock, err := app.acquireConfigLock()
	if err != nil {
		return nil, err
	}
	defer cfgLock.Release()

	// The harvest below and stage 1 read the same ref — the pass compares the old snapshot's
	// payload, then stage 1 copies it — which is one extra `security` subprocess per rename
	// on the darwin keychain. Only the backend cache, not the keychain one `runRebind` also
	// wraps: nothing here reads a live store twice. Safe against the harvest's own write
	// because `secret.Cached` invalidates the key on Set and Delete.
	be = secret.Cached(be)

	// Stage 0: preserve what the bound directories are reading into the account being
	// renamed, while its snapshot and the fragments naming it are both still there. Stage 1
	// then carries the result forward like any other payload, so the three stages below are
	// unchanged. Its own doc says why it cannot move after any of them.
	//
	// `acc` is re-read because a harvest rewrites the account's capture time, and saving the
	// copy loaded before it would file the harvested payload under the older date — the same
	// re-read rule recordHarvestTime states for itself.
	app.harvestRenamedAccountCredentials(ctx, be, tool, oldName)
	reloaded, stillThere, err := account.Load(app.Paths.AccountDir(tool, oldName))
	if err != nil {
		return nil, err
	}
	if !stillThere {
		return nil, errf(constants.ExitNotFound, "account %s/%s is not captured", tool, oldName)
	}
	acc = reloaded

	// A rename is a copy-then-destroy in three stages — build the new snapshot,
	// flip the logical pointers, destroy the old snapshot — so that every crash
	// window leaves the pointers on a snapshot that is complete. Do not collapse
	// it back into one pass. This used to flip `state.Active` *first* and only
	// then move the payloads, which left state naming a snapshot that did not
	// exist yet; and moving the flip below a single Get/Set/Delete pass would
	// trade that for a window nothing reports at all, since the old dir would
	// keep loading fine while the `SecretRef` its metadata declares was already
	// deleted from the backend. Stage 3's own comment,
	// below, is why refs go before the dir.

	// Stage 1: copy every payload to its new ref and complete the new snapshot
	// dir. Secret-backend keys cannot be renamed in place, hence a copy; the old
	// refs stay readable, so nothing is lost if this stage dies part-way.
	//
	// supersededRefs collects exactly the old refs whose payload reached the new
	// name, which is what stage 3 may delete — an artifact captured as absent, or
	// one whose payload the backend no longer holds, was never copied and must
	// not be deleted as though it had been.
	supersededRefs := make([]string, 0, len(acc.Artifacts))
	for _, name := range acc.ArtifactNames() {
		art := acc.Artifacts[name]
		newRef := account.SecretRef(tool, newName, name)
		if art.Present {
			payload, ok, err := be.Get(ctx, art.SecretRef)
			if err != nil {
				return nil, fmt.Errorf("read secret %s: %w", art.SecretRef, err)
			}
			if ok {
				if err := be.Set(ctx, newRef, payload); err != nil {
					return nil, fmt.Errorf("write secret %s: %w", newRef, err)
				}
				supersededRefs = append(supersededRefs, art.SecretRef)
			}
		}
		art.SecretRef = newRef
		acc.Artifacts[name] = art
	}
	acc.Name = newName
	if err := account.Save(app.Paths.AccountDir(tool, newName), acc); err != nil {
		return nil, err
	}

	// Stage 2: move the logical pointers (config references + state), now that
	// both names resolve to a complete snapshot and before either is destroyed.
	if len(profiles) > 0 {
		if err := app.editConfig(func(e *config.Editor) {
			for _, name := range profiles {
				e.SetProfileAccount(name, tool, newName)
			}
		}); err != nil {
			return nil, err
		}
	}
	// Re-decided under the state lock, for the reason `kae account rm` gives:
	// the pre-lock copy can be older than a concurrent switch, and renaming on
	// it would point the active binding at an account nobody selected.
	if _, err := app.mutateState(func(st *state.State) {
		report.ActiveUpdated = st.Active[tool] == oldName
		if !report.ActiveUpdated {
			return
		}
		st.Active[tool] = newName
		st.ActiveProfile = app.Config.MatchProfile(st.Active)
	}); err != nil {
		return nil, err
	}

	// Stage 3: destroy the old copy. Refs first, dir last — deliberately, and not
	// interchangeable. A crash between them leaves a snapshot dir declaring a ref
	// the backend no longer has, which `kae doctor` reports for any backend
	// (secret_missing looks up the refs one snapshot names). The reverse order
	// would leave backend keys with no snapshot dir, which secret_orphan reports
	// only on a backend kae can *enumerate* — never on the darwin keychain, where
	// the leftover would be silent.
	//
	// Stage 1 can leave that same silent shape (new refs, no dir behind them yet),
	// and the difference is that it heals itself: the pre-checks above find no dir
	// under newName, so re-running the identical command overwrites those copies and
	// finishes. Nothing re-runs stage 3 for you, which is why its window is the one
	// that has to land on a check that fires everywhere.
	for _, ref := range supersededRefs {
		if err := be.Delete(ctx, ref); err != nil {
			return nil, fmt.Errorf("delete old secret %s: %w", ref, err)
		}
	}
	if err := os.RemoveAll(app.Paths.AccountDir(tool, oldName)); err != nil {
		return nil, fmt.Errorf("remove old snapshot dir: %w", err)
	}
	app.warnPinnedAccountGone(tool, oldName, newName)
	return report, nil
}

func printAccountRename(r *accountRenameReport) {
	verb := "Renamed"
	if r.DryRun {
		verb = "Would rename"
	}
	fmt.Printf("%s %s/%s to %s/%s (%d secret item(s))\n", verb, r.Tool, r.Old, r.Tool, r.New, r.SecretsMoved)
	if len(r.ProfilesUpdated) > 0 {
		fmt.Printf("  rewrote the %s reference in profile(s): %v\n", r.Tool, r.ProfilesUpdated)
	}
	if r.ActiveUpdated {
		fmt.Printf("  updated the active %s account in state\n", r.Tool)
	}
}

type accountSetIdentityReport struct {
	SchemaVersion int    `json:"schema_version"`
	OK            bool   `json:"ok"`
	DryRun        bool   `json:"dry_run"`
	Tool          string `json:"tool"`
	Account       string `json:"account"`
	Identity      string `json:"identity"`
}

// cmdAccountSetIdentity records or updates an account's login identity without
// re-capturing its credential. It exists because some tools cannot expose a
// per-account identity to kae (agy on current Antigravity resolves the account
// from an opaque keychain token server-side and never writes it to disk), so
// auto-detection records nothing; this lets the user set it explicitly.
func cmdAccountSetIdentity(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("account set-identity", flags, true, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 3 {
		return usageError("usage: %s account set-identity <tool> <account> <value>", toolName)
	}
	report, err := buildAccountSetIdentity(newApp(opts.ConfigPath), opts, positionals[0], positionals[1], positionals[2])
	if err != nil {
		return finish(opts, err)
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	printAccountSetIdentity(report)
	return constants.ExitOK
}

func buildAccountSetIdentity(app *App, opts commonOpts, tool, accountName, value string) (*accountSetIdentityReport, error) {
	tool, err := canonicalToolAccount(tool, accountName, "account")
	if err != nil {
		return nil, err
	}
	identity := sanitizeIdentity(value)
	if identity == "" {
		return nil, errf(constants.ExitUsage, "the identity value has no usable characters")
	}
	if err := app.requireConfig(); err != nil {
		return nil, err
	}
	dir := app.Paths.AccountDir(tool, accountName)
	report := &accountSetIdentityReport{
		SchemaVersion: constants.SchemaVersion, OK: true, DryRun: opts.DryRun,
		Tool: tool, Account: accountName, Identity: identity,
	}
	if opts.DryRun {
		// Verify existence before claiming success, but skip locking and writing.
		if _, found, err := account.Load(dir); err != nil {
			return nil, err
		} else if !found {
			return nil, errf(constants.ExitNotFound, "account %s/%s is not captured", tool, accountName)
		}
		return report, nil
	}
	locks, err := app.acquireLocks([]string{tool})
	if err != nil {
		return nil, err
	}
	defer releaseLocks(locks)
	// Load under the lock so a concurrent capture is not clobbered.
	acc, found, err := account.Load(dir)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errf(constants.ExitNotFound, "account %s/%s is not captured", tool, accountName)
	}
	acc.Identity = identity
	if err := account.Save(dir, acc); err != nil {
		return nil, err
	}
	return report, nil
}

func printAccountSetIdentity(r *accountSetIdentityReport) {
	verb := "Set"
	if r.DryRun {
		verb = "Would set"
	}
	fmt.Printf("%s the %s/%s identity to %s\n", verb, r.Tool, r.Account, r.Identity)
}

// profilesReferencing returns the config profiles whose accounts map points at
// account for tool, in sorted order.
func (app *App) profilesReferencing(tool, accountName string) []string {
	names := []string{}
	for name, profile := range app.Config.Profiles {
		if profile.Accounts[tool] == accountName {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
