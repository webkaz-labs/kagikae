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
	if active {
		delete(st.Active, tool)
		if err := app.saveActive(st, nil, ""); err != nil {
			return nil, err
		}
	}
	if err := os.RemoveAll(app.Paths.AccountDir(tool, accountName)); err != nil {
		return nil, fmt.Errorf("remove snapshot dir: %w", err)
	}
	for _, name := range acc.ArtifactNames() {
		if err := be.Delete(ctx, acc.Artifacts[name].SecretRef); err != nil {
			return nil, fmt.Errorf("delete secret %s: %w", acc.Artifacts[name].SecretRef, err)
		}
	}
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

	// Logical update first (config references + state), then the physical
	// secret move and snapshot rename. If the process dies mid-rename the old
	// snapshot is still intact and `kae account rename` is simply re-runnable;
	// the reverse order could strand config/state on a name whose dir is gone.
	if len(profiles) > 0 {
		if err := app.editConfig(func(e *config.Editor) {
			for _, name := range profiles {
				e.SetProfileAccount(name, tool, newName)
			}
		}); err != nil {
			return nil, err
		}
	}
	if activeUpdate {
		if err := app.saveActive(st, map[string]string{tool: newName}, ""); err != nil {
			return nil, err
		}
	}

	// Secret-backend keys cannot be renamed in place, so copy each payload to
	// its new ref before deleting the old one, rewriting the metadata refs.
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
				if err := be.Delete(ctx, art.SecretRef); err != nil {
					return nil, fmt.Errorf("delete old secret %s: %w", art.SecretRef, err)
				}
			}
		}
		art.SecretRef = newRef
		acc.Artifacts[name] = art
	}
	acc.Name = newName
	if err := account.Save(app.Paths.AccountDir(tool, newName), acc); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(app.Paths.AccountDir(tool, oldName)); err != nil {
		return nil, fmt.Errorf("remove old snapshot dir: %w", err)
	}
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
