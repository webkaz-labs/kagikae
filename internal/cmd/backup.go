package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
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
		latest, found, err := latestRestorable(app.Paths.BackupsDir())
		if err != nil {
			return nil, err
		}
		if !found {
			// Named from the same predicate that excluded them, not from a literal: this
			// sentence said `run-unattributable` alone while a second preserved reason
			// existed, so a user looking at a `relogin-unattributable` backup was told the
			// refusal was about something else. It reports what is actually there.
			return nil, errf(constants.ExitNotFound,
				"no backup kae can roll back to%s; see kae backup list, then kae rollback --to <id>",
				preservedOnlyDetail(app.Paths.BackupsDir()))
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
	preMeta, err := app.createBackup(ctx, be, plansFromBackupMeta(meta, current), st, constants.BackupReasonRollback)
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

// latestRestorable is the newest backup a bare `kae rollback` may target: the newest one
// that records a state kae was **about to change**. Four reasons do; one does not.
//
// A `run-unattributable` backup records the post-child state `kae run -s` *declined to
// adopt*, kept only so that a refusal is not a deletion — a preserved artifact rather
// than an undo target. It is also the newest backup in existence at exactly the moment a
// user is most likely to type `kae rollback` meaning "undo that run", and targeting it
// installs a login kae explicitly refused to name into the real home while `state.json`
// names another account: the state `identity_drift` exists to report, reached through the
// command meant to reverse things. `--to <id>` still gets there, which is what the
// refusal's own message tells the user to type.
//
// Filtering here rather than inside backup.Latest keeps that package free of kae's reason
// vocabulary, and `kae backup list` deliberately still shows every backup.
func latestRestorable(dir string) (backup.Meta, bool, error) {
	metas, err := backup.List(dir)
	if err != nil {
		return backup.Meta{}, false, err
	}
	for _, meta := range metas { // List is newest-first
		if isUndoTarget(meta) {
			return meta, true, nil
		}
	}
	return backup.Meta{}, false, nil
}

// isUndoTarget reports whether a backup records a state kae was **about to change**, as
// opposed to one it declined to adopt. Two rules follow from it and they are coupled, so
// they read it from here rather than each spelling the reason out: a bare `kae rollback`
// targets only an undo target (latestRestorable), and `backup_keep` counts only undo
// targets (pruneBackups). Written twice, a sixth reason added to one site reintroduces
// whichever of those two the other site owns — the drift AGENTS.md describes for
// `supersedes`, one predicate over.
func isUndoTarget(meta backup.Meta) bool {
	return meta.Reason != constants.BackupReasonRunUnattributable &&
		meta.Reason != constants.BackupReasonReloginUnattributable
}

// preservedOnlyDetail explains an empty bare-rollback target when the directory is not
// empty: every backup in it is a preserved copy rather than an undo target. It names the
// reasons actually present, so the sentence cannot go stale the way a hardcoded one did
// when a second preserved reason shipped. Empty when there is genuinely nothing there,
// where "no backup kae can roll back to" is the whole story.
func preservedOnlyDetail(dir string) string {
	metas, err := backup.List(dir)
	if err != nil || len(metas) == 0 {
		return ""
	}
	seen := map[string]bool{}
	reasons := []string{}
	for _, m := range metas {
		if !isUndoTarget(m) && !seen[m.Reason] {
			seen[m.Reason] = true
			reasons = append(reasons, m.Reason)
		}
	}
	if len(reasons) == 0 {
		return ""
	}
	return fmt.Sprintf(" (every backup here is a preserved copy rather than an undo target: %s)",
		strings.Join(reasons, ", "))
}

// fromBoundStore reports whether a backup's records were taken from a store kae itself
// pointed one directory at, rather than from the tool's own global store.
//
// It gates the restore's moved-store check (restoreSpec), and the reason it has to is the
// asymmetry that check rests on: it exists because a **child process** can move a tool's
// credential between the tool's *own* stores, so today's global resolution is the better
// answer than a stale record. A record kae took from a bound store was never in the global
// store, so that resolution is not an answer about it at all. Every other reason records
// global specs, which is why nothing needed this until the first bound-store backup.
//
// Two consequences, and the severe one is not the one reachable today — worth writing
// down, because a reader who checks only claude will conclude the guard is unnecessary.
// Where a tool's two stores hold **interchangeable** payloads (codex: `auth.json` and its
// keyring item are both whole documents) the check *redirects*, and the restore then writes
// the tool's **global** store: one directory's loss becomes a global logout. Where they do
// not (claude today: a JSON pointer into `.credentials.json` against a keychain item) it
// *refuses*, so the preserved copy cannot be put back at all — recoverable, but it makes
// the backup worthless exactly when it is needed. One predicate covers both, and
// TestBoundStoreBackupIsNeverRedirectedToTheRealHome pins each with its own control.
//
// Keyed on the reason rather than on the recorded target, because a target is not always a
// path: a keychain record's is a *service name*, so a path test would answer "not kae's"
// for exactly the per-directory item that most needs this.
func fromBoundStore(meta backup.Meta) bool {
	return meta.Reason == constants.BackupReasonReloginUnattributable
}
