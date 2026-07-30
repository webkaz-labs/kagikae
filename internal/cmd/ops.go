package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/secret"
	"github.com/webkaz-labs/kagikae/internal/state"
)

// action is the JSON shape of one planned/performed artifact write.
type action struct {
	Kind    string `json:"kind"`
	Target  string `json:"target"`
	Pointer string `json:"pointer,omitempty"`
}

func (app *App) actionsOf(specs []artifact.Spec) []action {
	actions := make([]action, 0, len(specs))
	for _, sp := range specs {
		target := sp.Target
		if sp.Kind != constants.KindKeychain {
			target = app.displayPath(target)
		}
		actions = append(actions, action{Kind: sp.Kind, Target: target, Pointer: sp.Pointer})
	}
	return actions
}

// resolveToolArg resolves a tool-position argument, accepting any unambiguous
// prefix of a known tool id (cl→claude, cod→codex, cu→cursor, cop→copilot,
// o→opencode, a→agy). It is input-only sugar: the canonical name is returned
// and stored, never the prefix. An exact match wins immediately; an ambiguous
// prefix (c, co) is a usage error naming the candidates; an unmatched input is
// returned unchanged so the downstream unknown-tool error fires. The ambiguity
// set is computed from constants.Tools, so a new tool self-adjusts it.
func resolveToolArg(input string) (string, error) {
	if constants.IsTool(input) {
		return input, nil
	}
	var matches []string
	for _, t := range constants.Tools {
		if strings.HasPrefix(t, input) {
			matches = append(matches, t)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return input, nil // unknown; validateToolAccount emits the unknown-tool error
	default:
		return "", errf(constants.ExitUsage,
			"ambiguous tool prefix %q (matches: %s)", input, strings.Join(matches, ", "))
	}
}

// canonicalToolAccount resolves a tool-position prefix alias to its canonical
// id (resolveToolArg) and validates the tool/account pair, returning the
// canonical tool to store. Use it at command entry points that take a <tool>
// <account> pair so a prefix like "cl" never reaches a data path.
func canonicalToolAccount(tool, name, nameKind string) (string, error) {
	canonical, err := resolveToolArg(tool)
	if err != nil {
		return "", err
	}
	if err := validateToolAccount(canonical, name, nameKind); err != nil {
		return "", err
	}
	return canonical, nil
}

// validateTool checks a CLI-provided tool id, naming the successor for a
// removed tool. Split from validateToolAccount so `kae add <tool>` (auto-detect,
// no name yet) can validate the tool without a name.
func validateTool(tool string) error {
	if !constants.IsTool(tool) {
		if successor, removed := constants.RemovedTools[tool]; removed {
			return errf(constants.ExitUsage,
				"%s was removed in v0.6.0; its upstream successor is %s (captured %s accounts on disk are untouched)",
				tool, successor, tool)
		}
		return errf(constants.ExitUsage, "unknown tool %q (tools: %s)%s", tool, strings.Join(constants.Tools, ", "), didYouMean(tool, constants.Tools))
	}
	return nil
}

// validateToolAccount checks CLI-provided tool and account/profile names.
func validateToolAccount(tool, name, nameKind string) error {
	if err := validateTool(tool); err != nil {
		return err
	}
	if !config.ValidName(name) {
		return errf(constants.ExitUsage, "invalid %s name %q (allowed: [a-zA-Z0-9._-], max 64 chars)", nameKind, name)
	}
	return nil
}

// toolPlan is one tool's resolved switch/capture plan.
type toolPlan struct {
	Tool    string
	Account string
	Driver  string
	// Identity is the raw detected login identity to persist in the snapshot
	// (§D). Set by the capture/login paths from resolveAccount; preserved (not
	// re-detected) by switch-away recapture. Empty when undetectable.
	Identity string
	Specs    []artifact.Spec
	Meta     account.Account // populated for switch (captured snapshot)
	Warnings []string
}

// planTool resolves adapter, driver, and artifact specs for one tool.
func (app *App) planTool(ctx context.Context, tool, accountName string) (toolPlan, error) {
	plan := toolPlan{Tool: tool, Account: accountName, Warnings: []string{}}
	ad, err := adapter.ForTool(tool)
	if err != nil {
		return plan, err
	}
	info, err := ad.Detect(ctx, app.Env)
	if err != nil {
		return plan, err
	}
	plan.Driver = info.Driver
	plan.Warnings = append(plan.Warnings, info.Warnings...)
	specs, err := ad.Artifacts(ctx, app.Env)
	if err != nil {
		return plan, err
	}
	plan.Specs = specs
	return plan, nil
}

// refreshPlan re-resolves a plan's driver and artifact specs after a child process
// ran, keeping the account, identity and snapshot the caller already resolved.
//
// A child can move the tool's credential between the stores it supports: codex
// under `cli_auth_credentials_store = "auto"` creates its keychain item and deletes
// `auth.json` on its first save, and a login flow does it on purpose. Every read
// and write kae then makes through the specs it resolved *before* the child lands
// on a store the tool no longer reads — the recapture reports "logged out during
// the run" and keeps a stale snapshot, the login comparison reports "auth
// unchanged" and captures nothing, and the restore writes a file nothing reads
// while reporting success. All three are one cause: specs older than the child.
//
// A resolution failure keeps the pre-child specs and **warns**: they are the best
// answer left, and failing here would abort a restore that is better attempted —
// but they may be stale, so "restored the previous state" would be a claim kae
// cannot support. The child can even be what broke resolution (a codex config the
// adapter now refuses, `ephemeral` say), which is exactly when silence would be
// worst.
func (app *App) refreshPlan(ctx context.Context, plan toolPlan) toolPlan {
	fresh, err := app.planTool(ctx, plan.Tool, plan.Account)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"kae: warning: could not re-resolve %s's credential store after the child (%v); "+
				"continuing with the store resolved before it, which may no longer be the one %s reads\n",
			plan.Tool, err, plan.Tool)
		return plan
	}
	fresh.Identity, fresh.Meta = plan.Identity, plan.Meta
	return fresh
}

// loadState reads state.json.
func (app *App) loadState() (*state.State, error) {
	return state.Load(app.Paths.StateFile())
}

// saveActive updates the active map and recomputes the matching profile.
// explicitProfile overrides recomputation (used by switch all).
func (app *App) saveActive(st *state.State, updates map[string]string, explicitProfile string) error {
	for tool, accountName := range updates {
		st.Active[tool] = accountName
	}
	if explicitProfile != "" {
		st.ActiveProfile = explicitProfile
	} else {
		st.ActiveProfile = app.Config.MatchProfile(st.Active)
	}
	st.UpdatedAt = app.Now().UTC()
	return state.Save(app.Paths.StateFile(), st)
}

// createBackup snapshots the live values of every plan into one backup.
func (app *App) createBackup(ctx context.Context, be secret.Backend, plans []toolPlan, st *state.State, reason string) (backup.Meta, error) {
	id := backup.NewID(app.Paths.BackupsDir(), app.Now())
	meta := backup.Meta{
		SchemaVersion: constants.SchemaVersion,
		ID:            id,
		CreatedAt:     app.Now().UTC(),
		Reason:        reason,
		Tools:         []string{},
		ActiveBefore:  map[string]string{},
		Artifacts:     []backup.ArtifactRecord{},
	}
	for _, plan := range plans {
		meta.Tools = append(meta.Tools, plan.Tool)
		if current, ok := st.Active[plan.Tool]; ok {
			meta.ActiveBefore[plan.Tool] = current
		}
		for _, sp := range plan.Specs {
			value, err := artifact.ReadLive(ctx, sp)
			if err != nil {
				return meta, fmt.Errorf("backup %s: %w", plan.Tool, err)
			}
			ref := backup.SecretRef(id, plan.Tool, sp.Name)
			if value.Present {
				if err := be.Set(ctx, ref, value.Data); err != nil {
					return meta, fmt.Errorf("store backup payload: %w", err)
				}
			}
			meta.Artifacts = append(meta.Artifacts, backup.ArtifactRecord{
				Tool: plan.Tool, Name: sp.Name, Kind: sp.Kind,
				Target: sp.Target, Pointer: sp.Pointer,
				KeychainAccount:      sp.KeychainAccount,
				KeychainMatchAccount: sp.KeychainMatchAccount,
				JSONC:                sp.JSONC,
				SecretRef:            ref, Present: value.Present,
			})
		}
	}
	if err := backup.Save(app.Paths.BackupsDir(), meta); err != nil {
		return meta, fmt.Errorf("save backup metadata: %w", err)
	}
	return meta, nil
}

// applyBackup restores live state from a backup, optionally limited to the given
// tools (nil = all).
//
// It resolves today's specs **itself** rather than taking them from the caller.
// The restore has to know where each credential lives *now* — a child process can
// move it between a tool's stores (restoreSpec) — and threading that in made it a
// thing five call sites could forget: four of them did, which silently restored the
// pre-fix behaviour of writing a store nothing reads and reporting success. Derived
// here it cannot be forgotten. It is also safe to derive: only the tool moves its
// own store, never kae, so a resolution taken now cannot disagree with one taken a
// moment earlier in the same command, and a tool that fails to resolve falls back
// to its records exactly as before.
//
// degradeLostIdentity is the caller's separate decision about a payload that has
// gone missing from the secret store: a rollback lets an identity-only artifact
// degrade to absent (losing it is safe and self-correcting), while a caller
// restoring a backup it just created treats any missing payload as a hard error,
// because it wrote them moments ago.
func (app *App) applyBackup(ctx context.Context, be secret.Backend, meta backup.Meta, only map[string]bool, degradeLostIdentity bool) error {
	current, _ := app.currentSpecs(ctx, meta)
	for _, rec := range meta.Artifacts {
		if only != nil && !only[rec.Tool] {
			continue
		}
		value, err := storedValue(ctx, be, rec.SecretRef, rec.Present,
			degradeLostIdentity && isIdentityOnly(current, rec.Tool, rec.Name), func() error {
				return errf(constants.ExitNotFound, "backup payload %s is missing from the secret store", rec.SecretRef)
			})
		if err != nil {
			return err
		}
		sp, warning, err := restoreSpec(current, rec)
		if err != nil {
			return err
		}
		if warning != "" {
			fmt.Fprintf(os.Stderr, "kae: warning: %s\n", warning)
		}
		if err := artifact.ApplyLive(ctx, sp, value); err != nil {
			return fmt.Errorf("restore %s/%s: %w", rec.Tool, rec.Name, err)
		}
	}
	return nil
}

// rollbackTo restores a backup and then removes every identity-only artifact it
// has no record of. The two are one transaction: they leave the live state in a
// single consistent shape only together, so a caller must not record success (or
// update state.json) unless both landed. Restoring alone can leave a stale
// identity cache naming the account the rollback just left, and a partial
// rollback would put the live credential on one account while kae records
// another — the next switch-away recapture would then file that credential into
// the wrong account's snapshot. Failing between the two therefore surfaces as
// "the rollback failed", so the caller restores its pre-rollback backup.
//
// The cleanup lives here rather than inside applyBackup because only a rollback
// can be handed a backup that predates an artifact; applyBackup's other callers
// restore a backup they just created and would gain a surprising delete pass.
func (app *App) rollbackTo(ctx context.Context, be secret.Backend, meta backup.Meta, current map[string][]artifact.Spec) error {
	if err := app.applyBackup(ctx, be, meta, nil, true); err != nil {
		return err
	}
	if err := clearUnrecordedIdentity(ctx, meta, current); err != nil {
		return fmt.Errorf("clear a stale identity cache: %w", err)
	}
	return nil
}

// storedValue reads one artifact payload from the secret backend. A payload that
// is simply gone degrades to absent for an identity-only artifact — losing it is
// safe and it must never block the credential recorded beside it — and is a hard
// error otherwise, where removing a credential would log the user out. missing
// builds that error, so each caller keeps its own wording and exit code.
func storedValue(ctx context.Context, be secret.Backend, ref string, present, identityOnly bool, missing func() error) (artifact.Value, error) {
	if !present {
		return artifact.Value{}, nil
	}
	data, found, err := be.Get(ctx, ref)
	if err != nil {
		return artifact.Value{}, fmt.Errorf("read payload %s: %w", ref, err)
	}
	switch {
	case found:
		return artifact.Value{Data: data, Present: true}, nil
	case identityOnly:
		return artifact.Value{}, nil
	default:
		return artifact.Value{}, missing()
	}
}

// restoreSpec resolves the spec a backup record must be written through.
//
// By default that is the record's own: `specFromRecord` rebuilds the store the
// payload came from, so the payload's shape is the recorded kind's by
// construction. But a tool can move its credential between the stores it supports
// while kae is not looking — codex under `cli_auth_credentials_store = "auto"`
// creates its keychain item and deletes `auth.json` on its first save — and then
// writing the recorded store puts the credential where nothing reads it while kae
// reports the restore as successful.
//
// So when today's declaration disagrees about *where* the artifact lives, the
// payload follows the tool: a whole-document payload is written through today's
// spec, which for codex is the same bytes in either store. A move between shapes
// that are not interchangeable (a whole document and a JSON pointer value) cannot
// be redirected, and is refused exactly as the equivalent snapshot transition is
// (checkPayloadShape).
//
// **A redirect only ever writes; it never deletes.** An absent record restores as
// "logged out", and redirecting that would delete the store the tool moved to —
// a credential this backup has no copy of, and on the paths that need the redirect
// most, one nothing else has a copy of either: a login flow that failed after
// creating the credential but before kae captured it reaches this exact case, and
// deleting there destroys the login the user just performed. So an absent record
// keeps the recorded spec, which removes the abandoned store (inert) and leaves the
// live one alone, with a warning that the restore was partial. Leaving a credential
// kae cannot account for is recoverable; deleting it is not.
//
// Only a change of **kind** redirects. A record naming the same kind is restored as
// recorded even when today's spec points elsewhere: a different `CODEX_HOME` (or
// `CLAUDE_CONFIG_DIR`) resolves a different item of the same service, and that is
// another home's credential store — not a place this backup belongs.
//
// current is nil only where no declaration was resolved; the record then stands
// alone, which is the pre-fix behaviour.
//
// The returned warning is non-empty when the restore is knowingly partial. It is
// returned rather than printed so the caller that performs the write emits it once,
// immediately before that write — the pre-rollback capture resolves the same spec
// and must not print it a second time.
func restoreSpec(current map[string][]artifact.Spec, rec backup.ArtifactRecord) (sp artifact.Spec, warning string, err error) {
	for _, live := range current[rec.Tool] {
		if live.Name != rec.Name || live.Kind == rec.Kind {
			continue
		}
		if !rec.Present {
			return specFromRecord(rec), fmt.Sprintf(
				"%s moved its credential to %s %q, which this backup has no record of; "+
					"kae left it in place rather than deleting a credential it has no copy of, so %s "+
					"stays logged in as whatever wrote it",
				rec.Tool, live.Kind, live.Target, rec.Tool,
			), nil
		}
		if artifact.WholeDocument(live.Kind) != artifact.WholeDocument(rec.Kind) {
			return artifact.Spec{}, "", errf(constants.ExitUnsafeRefused,
				"%s/%s was backed up from %s %q but %s now keeps it in %s %q, and the two "+
					"payload shapes are not interchangeable; switch with `kae use %s <account>` instead",
				rec.Tool, rec.Name, rec.Kind, rec.Target, rec.Tool, live.Kind, live.Target, rec.Tool)
		}
		return live, "", nil
	}
	return specFromRecord(rec), "", nil
}

// isIdentityOnly answers whether a recorded artifact is identity-only according
// to *today's* adapter, not according to the record. Whether losing an artifact
// is survivable is policy, and policy belongs to the code: an old backup must not
// pin a decision kae has since changed. An unresolvable tool answers false, so
// the fail-loud path stays the default.
func isIdentityOnly(current map[string][]artifact.Spec, tool, name string) bool {
	for _, sp := range current[tool] {
		if sp.Name == name {
			return sp.IdentityOnly
		}
	}
	return false
}

// currentSpecs resolves each of a backup's tools once. A rollback needs today's
// declaration twice over — to complete the pre-rollback backup and to clear an
// identity artifact the backup never recorded — and resolving it per pass would
// mean two chances to disagree plus two warnings about the same adapter. A tool
// that cannot be resolved is absent from the map and returned in unresolved; the
// caller warns once and proceeds with the records alone.
func (app *App) currentSpecs(ctx context.Context, meta backup.Meta) (specs map[string][]artifact.Spec, unresolved []toolResolveError) {
	specs = make(map[string][]artifact.Spec, len(meta.Tools))
	for _, tool := range meta.Tools {
		ad, err := adapter.ForTool(tool)
		if err == nil {
			var got []artifact.Spec
			if got, err = ad.Artifacts(ctx, app.Env); err == nil {
				specs[tool] = got
				continue
			}
		}
		unresolved = append(unresolved, toolResolveError{Tool: tool, Err: err})
	}
	return specs, unresolved
}

// toolResolveError names a tool whose current specs could not be resolved, with
// the reason to report. An error is not proof that nothing is live — an
// unsupported platform looks the same as a bad KAE_CLAUDE_DRIVER on a working
// one — so callers warn rather than assume.
type toolResolveError struct {
	Tool string
	Err  error
}

// reapplyHint names the command that redoes what an unresolved tool skipped.
// It points at re-applying the account rather than re-running the rollback: the
// rollback succeeds and then prunes, so its own id may already be gone, while
// `kae use` reaches the same work from the snapshot side. Only an account whose
// snapshot still exists is named — a backup's active_before keeps the name it had
// at capture time, which a later rename or removal invalidates.
func (app *App) reapplyHint(meta backup.Meta, tool string) string {
	if acct, ok := meta.ActiveBefore[tool]; ok && acct != "" {
		if _, found, err := account.Load(app.Paths.AccountDir(tool, acct)); err == nil && found {
			return fmt.Sprintf("re-apply it: kae use %s %s", tool, acct)
		}
	}
	return fmt.Sprintf("re-apply the %s account you want (see: kae accounts)", tool)
}

// clearUnrecordedIdentity removes every identity-only artifact the backup has no
// record of. It cannot be restored, and leaving today's copy in place would keep
// claude's identity cache naming the account the rollback just left — the
// mislabelling the artifact exists to prevent. Removal is the same fallback
// applySnapshot uses: the tool rebuilds it from the restored credential.
//
// Only a rollback needs this. Every other applyBackup caller restores a meta it
// created moments earlier from today's specs, so nothing is ever unrecorded
// there, and a delete pass would be a surprising duty for a recovery path.
func clearUnrecordedIdentity(ctx context.Context, meta backup.Meta, current map[string][]artifact.Spec) error {
	recorded := make(map[string]bool, len(meta.Artifacts))
	for _, rec := range meta.Artifacts {
		recorded[rec.Tool+"/"+rec.Name] = true
	}
	for _, tool := range meta.Tools {
		for _, sp := range current[tool] {
			if !sp.IdentityOnly || recorded[tool+"/"+sp.Name] {
				continue
			}
			if err := artifact.ApplyLive(ctx, sp, artifact.Value{}); err != nil {
				return fmt.Errorf("clear %s/%s: %w", tool, sp.Name, err)
			}
		}
	}
	return nil
}

// specFromRecord rebuilds the artifact spec a backup record was captured from
// (including the keychain account, so a rollback recreates an item under the
// tool's own account, not the generic fallback). IdentityOnly is deliberately
// not recorded — see isIdentityOnly.
//
// A legacy record's `keychain_replace` (codex keyring, written before the item
// was known to be account-scoped) restores as account-scoped: its recorded
// account is the one the item had, and the flag's own semantics — delete every
// item of the service, then write — is what removed another CODEX_HOME's login.
func specFromRecord(rec backup.ArtifactRecord) artifact.Spec {
	return artifact.Spec{
		Name: rec.Name, Kind: rec.Kind, Target: rec.Target,
		Pointer: rec.Pointer, KeychainAccount: rec.KeychainAccount,
		KeychainMatchAccount: rec.KeychainMatchAccount || rec.KeychainReplace,
		JSONC:                rec.JSONC,
	}
}

// plansFromBackupMeta rebuilds per-tool artifact plans from backup records so a
// rollback can itself be backed up before it overwrites live state. Artifacts
// current declares but the backup has no record of are added, so one introduced
// after the backup was taken is still captured before the rollback clears it —
// otherwise rolling back the rollback could not put it back.
//
// Each record is captured through **the spec the rollback will act on**
// (restoreSpec), never one merely resolved beside it. That pairing is the whole
// safety property: what the pre-rollback backup records is exactly what the
// rollback then overwrites, so rolling back the rollback puts it back. Resolving
// the two independently is what breaks it — preferring today's spec by name alone
// once made this capture a *different* codex home's item from the one the restore
// deleted. A spec restoreSpec refuses is captured from the record; the rollback
// refuses it too, so nothing is overwritten either way.
func plansFromBackupMeta(meta backup.Meta, current map[string][]artifact.Spec) []toolPlan {
	specsByTool := map[string][]artifact.Spec{}
	order := []string{}
	for _, rec := range meta.Artifacts {
		if _, seen := specsByTool[rec.Tool]; !seen {
			order = append(order, rec.Tool)
		}
		sp, _, err := restoreSpec(current, rec)
		if err != nil {
			sp = specFromRecord(rec)
		}
		specsByTool[rec.Tool] = append(specsByTool[rec.Tool], sp)
	}
	for _, tool := range order {
		recorded := make(map[string]bool, len(specsByTool[tool]))
		for _, sp := range specsByTool[tool] {
			recorded[sp.Name] = true
		}
		for _, sp := range current[tool] {
			if !recorded[sp.Name] {
				specsByTool[tool] = append(specsByTool[tool], sp)
			}
		}
	}
	plans := make([]toolPlan, 0, len(order))
	for _, tool := range order {
		plans = append(plans, toolPlan{Tool: tool, Specs: specsByTool[tool]})
	}
	return plans
}

// Error convention in this file: use errf only where a specific stable exit
// code applies (not_found, auth_missing, ...); plain fmt.Errorf flows through
// exitOf's default branch as a general error.

// doubleFailure reports the catastrophic case: the primary operation failed
// AND restoring from the backup failed too. The manual escape hatch is
// always the same.
func doubleFailure(op string, opErr, restoreErr error, backupID string) error {
	return errf(exitOf(opErr),
		"%s failed (%v) and restore also failed (%v); run: kae rollback --to %s",
		op, opErr, restoreErr, backupID)
}

// loadPlansWithSnapshots resolves adapter plans for the targets and loads
// each captured account snapshot, failing before anything is written when a
// target was never captured.
func (app *App) loadPlansWithSnapshots(ctx context.Context, targets []runTarget) ([]toolPlan, error) {
	plans := make([]toolPlan, 0, len(targets))
	for _, tgt := range targets {
		plan, err := app.planTool(ctx, tgt.Tool, tgt.Account)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", tgt.Tool, err)
		}
		acc, found, err := account.Load(app.Paths.AccountDir(tgt.Tool, tgt.Account))
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, errf(constants.ExitNotFound,
				"account %s/%s is not captured yet (run: kae add --no-login %s %s)",
				tgt.Tool, tgt.Account, tgt.Tool, tgt.Account)
		}
		plan.Meta = acc
		plans = append(plans, plan)
	}
	return plans, nil
}

// checkPayloadShape refuses to apply a snapshot whose payload has the other shape
// from what the destination spec expects (artifact.WholeDocument), because one of
// those transitions corrupts silently rather than failing: applying a
// whole-document payload through a pointer spec nests it under its own key
// (`{"claudeAiOauth":{"claudeAiOauth":…}}`), which claude reads as a malformed
// credential. The reverse writes an inner object as a whole document.
//
// The spec comes from the *current* environment while the payload was captured
// under an earlier one, which is how the two can disagree: switching claude's
// driver between capture and apply (KAE_CLAUDE_DRIVER, or [tools.claude] driver)
// is enough. A KindFile/KindKeychain transition is allowed — both are whole
// documents, which is what makes codex's auth.json and its keyring item the same
// bytes.
//
// An empty storedKind is a snapshot from before the kind was recorded, or an
// identity artifact absent from an older snapshot: nothing to compare, so nothing
// to refuse. The rollback path needs no such check at all — specFromRecord
// rebuilds the spec *from the backup record*, so its kind is the one its payload
// was captured under by construction.
//
// It lives here rather than in the artifact package because the refusal is a
// command-level decision: it reads account metadata, carries an exit code, and
// names the recapture that fixes it. Only the shape classification belongs to
// artifact, and that is where it is.
func checkPayloadShape(tool, accountName, artName, storedKind, destKind string) error {
	if storedKind == "" || artifact.WholeDocument(storedKind) == artifact.WholeDocument(destKind) {
		return nil
	}
	return errf(constants.ExitUnsafeRefused,
		"account %s/%s captured %s as %q but this environment resolves it as %q, "+
			"and the two payload shapes are not interchangeable; recapture with "+
			"`kae add --no-login %s %s` under the current driver",
		tool, accountName, artName, storedKind, destKind, tool, accountName)
}

// applySnapshot applies one captured account to the live state.
func applySnapshot(ctx context.Context, be secret.Backend, plan toolPlan) error {
	for _, sp := range plan.Specs {
		metaArt, ok := plan.Meta.Artifacts[sp.Name]
		if !ok && !sp.IdentityOnly {
			return errf(constants.ExitError,
				"snapshot %s/%s lacks artifact %s; re-run kae add --no-login %s %s",
				plan.Tool, plan.Account, sp.Name, plan.Tool, plan.Account)
		}
		// An identity-only artifact missing from an older snapshot applies as absent:
		// metaArt is the zero value, so Present=false removes it live (claude's
		// /oauthAccount identity cache is then refetched from the token).
		//
		// A keychain account recorded in the snapshot is deliberately *not* restored
		// over the spec's. Where a keychain item lives is the adapter's answer for
		// the current environment; the snapshot's is the answer for the environment
		// it was captured in, and applying that one is how kae writes an item the
		// tool never reads (a snapshot taken under one CODEX_HOME applied under
		// another).
		// The snapshot's bytes and the freshly resolved spec must agree on payload
		// shape. They can disagree because the spec comes from the *current*
		// environment while the payload was captured under an earlier one — switching
		// claude's driver between the two is enough — and one of those transitions
		// corrupts silently instead of failing (checkPayloadShape). Refusing before
		// ApplyLive keeps it an apply error, so the caller's restore still runs.
		if err := checkPayloadShape(plan.Tool, plan.Account, sp.Name, metaArt.Kind, sp.Kind); err != nil {
			return err
		}
		value, err := storedValue(ctx, be, metaArt.SecretRef, metaArt.Present, sp.IdentityOnly, func() error {
			return errf(constants.ExitError,
				"snapshot payload %s is missing; re-run kae add --no-login %s %s",
				metaArt.SecretRef, plan.Tool, plan.Account)
		})
		if err != nil {
			return err
		}
		if err := artifact.ApplyLive(ctx, sp, value); err != nil {
			return err
		}
	}
	return nil
}
