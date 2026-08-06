package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/keychain"
	"github.com/webkaz-labs/kagikae/internal/state"
)

type backupItem struct {
	ID        string   `json:"id"`
	CreatedAt string   `json:"created_at"`
	Reason    string   `json:"reason"`
	Tools     []string `json:"tools"`
}

type backupListReport struct {
	SchemaVersion int          `json:"schema_version"`
	Backups       []backupItem `json:"backups"`
}

func CmdBackup(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] != "list" {
		return usageError("usage: %s backup list [--json]", toolName)
	}
	flags, positionals := splitArgs(args[1:])
	opts, ok := parseCommon("backup list", flags, false, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s backup list [--json]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runBackupList(ctx, app, opts)
}

func runBackupList(_ context.Context, app *App, opts commonOpts) int {
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	metas, err := backup.List(app.Paths.BackupsDir())
	if err != nil {
		return finish(opts, err)
	}
	report := backupListReport{SchemaVersion: constants.SchemaVersion, Backups: []backupItem{}}
	for _, meta := range metas {
		tools := meta.Tools
		if tools == nil {
			tools = []string{}
		}
		report.Backups = append(report.Backups, backupItem{
			ID:        meta.ID,
			CreatedAt: meta.CreatedAt.UTC().Format(time.RFC3339),
			Reason:    meta.Reason,
			Tools:     tools,
		})
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	if len(report.Backups) == 0 {
		fmt.Println("no backups yet (backups are created automatically before each switch)")
		return constants.ExitOK
	}
	rows := [][]string{}
	for _, item := range report.Backups {
		rows = append(rows, []string{item.ID, item.CreatedAt, item.Reason, fmt.Sprint(item.Tools)})
	}
	printTable([]string{"ID", "Created", "Reason", "Tools"}, rows)
	return constants.ExitOK
}

type restoredItem struct {
	Tool      string `json:"tool"`
	Artifacts int    `json:"artifacts"`
}

type rollbackReport struct {
	SchemaVersion int            `json:"schema_version"`
	OK            bool           `json:"ok"`
	DryRun        bool           `json:"dry_run"`
	BackupID      string         `json:"backup_id"`
	Restored      []restoredItem `json:"restored"`
}

func CmdRollback(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args, "--to")
	var toID string
	opts, ok := parseCommon("rollback", flags, true, func(fs *flag.FlagSet) {
		registerRollbackFlags(fs, &toID)
	})
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s rollback [--to <backup-id>] [--dry-run] [--json]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runRollback(ctx, app, opts, toID)
}

func runRollback(ctx context.Context, app *App, opts commonOpts, toID string) int {
	report, err := buildRollback(ctx, app, opts, toID)
	if err != nil {
		return finish(opts, err)
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	verb := "Rolled back to"
	if report.DryRun {
		verb = "Would roll back to"
	}
	fmt.Printf("%s backup %s\n", verb, report.BackupID)
	for _, item := range report.Restored {
		fmt.Printf("  %s: %d artifact(s)\n", item.Tool, item.Artifacts)
	}
	return constants.ExitOK
}

func buildRollback(ctx context.Context, app *App, opts commonOpts, toID string) (*rollbackReport, error) {
	if err := app.requireConfig(); err != nil {
		return nil, err
	}
	// Rollback restores global live state. Recorded artifacts carry absolute
	// targets, but the pre-rollback backup and the unrecorded-identity cleanup
	// resolve today's adapter specs through app.Env — inside a pinned shell those
	// would follow CLAUDE_CONFIG_DIR into the isolation tree and touch the wrong
	// copy. Hide the kae-managed isolation env first, as use/add already do.
	// applyGlobalScope, not pinnedGlobalScope: rollback keeps its output unchanged.
	app.applyGlobalScope()
	var meta backup.Meta
	if toID == "" {
		latest, found, err := backup.Latest(app.Paths.BackupsDir())
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, errf(constants.ExitNotFound, "no backups exist yet")
		}
		meta = latest
	} else {
		loaded, err := backup.Get(app.Paths.BackupsDir(), toID)
		if os.IsNotExist(err) {
			return nil, errf(constants.ExitNotFound, "backup %q not found (see: kae backup list)", toID)
		}
		if err != nil {
			return nil, err
		}
		meta = loaded
	}

	counts := map[string]int{}
	for _, rec := range meta.Artifacts {
		counts[rec.Tool]++
	}
	report := &rollbackReport{
		SchemaVersion: constants.SchemaVersion,
		OK:            true,
		DryRun:        opts.DryRun,
		BackupID:      meta.ID,
		Restored:      []restoredItem{},
	}
	for _, tool := range constants.Tools {
		if n, ok := counts[tool]; ok {
			report.Restored = append(report.Restored, restoredItem{Tool: tool, Artifacts: n})
		}
	}
	if opts.DryRun {
		return report, nil
	}

	// The pre-rollback backup and the superseded-credential warning read the same live
	// credential and identity, which on darwin is a `security` subprocess each. No child
	// process runs during a rollback, so the cache cannot observe a store something else
	// moved, and the restore's own writes invalidate the entries they touch — the same
	// idiom as the switch (docs/ARCHITECTURE.md § Caching).
	ctx = keychain.WithReadCache(ctx)

	be, err := app.secretBackend()
	if err != nil {
		return nil, err
	}
	locks, err := app.acquireLocks(meta.Tools)
	if err != nil {
		return nil, err
	}
	defer releaseLocks(locks)

	st, err := app.loadState()
	if err != nil {
		return nil, err
	}
	// Resolve today's specs once: the pre-rollback backup needs them to cover an
	// artifact this backup predates, and the identity cleanup below needs them to
	// know which artifact that is. One resolution means one warning per tool.
	current, unresolved := app.currentSpecs(ctx, meta)
	for _, u := range unresolved {
		fmt.Fprintf(os.Stderr,
			"kae: warning: could not resolve current %s artifacts (%v); the pre-rollback backup "+
				"covers only what this backup recorded, and a stale %s identity cache is left as "+
				"it is — fix that, then %s\n", u.Tool, u.Err, u.Tool, app.reapplyHint(meta, u.Tool))
	}
	// rollback is itself a live mutation: back up the current state first so
	// it stays reversible.
	preMeta, err := app.createBackup(ctx, be, plansFromBackupMeta(meta, current), st, "rollback")
	if err != nil {
		return nil, err
	}

	// Said before the write, and never fatal: going back is what the user asked for,
	// and what kae can add is that the credential it is about to put back may already
	// be dead — claude's refresh token rotates single-use, so anything that refreshed
	// that account after this backup was taken left the recorded copy unable to
	// refresh. Placed after preMeta because one of the two remedies is preMeta itself.
	app.warnRestoringSupersededCredential(ctx, be, meta, preMeta.ID, current)

	// rollbackTo is one transaction (see its doc comment), so state.json below is
	// updated only after it returns success. The recovery path restores preMeta
	// with current=nil: it is a backup created moments ago from today's specs, so
	// nothing in it may degrade to absent — a lost payload there must fail loudly
	// rather than look restored.
	if err := app.rollbackTo(ctx, be, meta, current); err != nil {
		if restoreErr := app.applyBackup(ctx, be, preMeta, nil, false); restoreErr != nil {
			return nil, errf(exitOf(err),
				"rollback failed (%v) and restore also failed (%v); inspect backups %s and %s",
				err, restoreErr, meta.ID, preMeta.ID)
		}
		return nil, errf(exitOf(err),
			"rollback failed, live state restored from backup %s: %v", preMeta.ID, err)
	}
	// Decided once, for both the warning and the write, so the two cannot answer
	// differently about the same tool — and so `restorableActiveAccount`'s
	// `account.Load` is paid once per tool instead of twice.
	//
	// The recorded name is only written back when its snapshot still resolves.
	// `active_before` keeps the name it had at capture time, so rolling back across
	// an `account rm`/`rename` used to record an account that no longer exists —
	// state naming a snapshot nothing can load (doctor's active_orphan) and the next
	// `kae use <tool>` failing with "is not captured yet". Dropping the entry is the
	// established way to say "no active account for this tool", the same thing an
	// unrecorded tool here and `kae account rm` do. An *empty* recorded value lands in
	// the same branch, where the old code wrote `st.Active[tool] = ""`: a change in
	// representation only, and a better one — every consumer treats a blank entry as
	// no selection, while the deleted key stops `kae status --json` and
	// `kae profile save` from carrying an empty account name.
	restorable := make(map[string]string, len(meta.Tools))
	for _, tool := range meta.Tools {
		if acct, ok := app.restorableActiveAccount(meta, tool); ok {
			restorable[tool] = acct
		}
	}
	// Warned before the write it qualifies, and never fatal: the credentials are
	// already rolled back, and what cannot be restored is a label. Nothing is said
	// about a tool the backup recorded no account for — there is no pointer that
	// failed to come back.
	for _, tool := range meta.Tools {
		recorded := meta.ActiveBefore[tool]
		if recorded == "" || restorable[tool] != "" {
			continue
		}
		fmt.Fprintf(os.Stderr,
			"kae: warning: backup %s recorded %s/%s as the active account and that snapshot is no longer "+
				"captured, so kae is leaving %s with no active account rather than naming one that is gone; %s\n",
			meta.ID, tool, recorded, tool, app.reapplyHint(meta, tool))
	}
	if _, err := app.mutateState(func(st *state.State) {
		for _, tool := range meta.Tools {
			if before := restorable[tool]; before != "" {
				st.Active[tool] = before
			} else {
				delete(st.Active, tool)
			}
		}
		st.ActiveProfile = app.Config.MatchProfile(st.Active)
	}); err != nil {
		return nil, errf(exitOf(err),
			"live state was rolled back but recording it failed (%v); verify with kae status, undo with: kae rollback --to %s",
			err, preMeta.ID)
	}
	app.pruneBackups(ctx, be)
	return report, nil
}
