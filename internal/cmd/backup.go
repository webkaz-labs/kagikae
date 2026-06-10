package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/constants"
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
		fs.StringVar(&toID, "to", "", "backup id to restore (default: most recent)")
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
	// rollback is itself a live mutation: back up the current state first so
	// it stays reversible.
	preMeta, err := app.createBackup(ctx, be, plansFromBackupMeta(meta), st, "rollback")
	if err != nil {
		return nil, err
	}

	if err := applyBackup(ctx, be, meta, nil); err != nil {
		if restoreErr := applyBackup(ctx, be, preMeta, nil); restoreErr != nil {
			return nil, errf(exitOf(err),
				"rollback failed (%v) and restore also failed (%v); inspect backups %s and %s",
				err, restoreErr, meta.ID, preMeta.ID)
		}
		return nil, errf(exitOf(err),
			"rollback failed, live state restored from backup %s: %v", preMeta.ID, err)
	}
	for _, tool := range meta.Tools {
		if before, ok := meta.ActiveBefore[tool]; ok {
			st.Active[tool] = before
		} else {
			delete(st.Active, tool)
		}
	}
	st.ActiveProfile = app.Config.MatchProfile(st.Active)
	st.UpdatedAt = app.Now().UTC()
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		return nil, errf(constants.ExitError,
			"live state was rolled back but recording it failed (%v); verify with kae status, undo with: kae rollback --to %s",
			err, preMeta.ID)
	}
	if _, err := backup.Prune(ctx, be, app.Paths.BackupsDir(), app.Config.Security.BackupKeep); err != nil {
		fmt.Fprintf(os.Stderr, "kae: warning: backup pruning failed: %v\n", err)
	}
	return report, nil
}
