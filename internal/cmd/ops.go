package cmd

import (
	"context"
	"fmt"
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

// captureKeychainAccount reads the verbatim per-login account to persist for a
// present KeychainReplace keychain item (codex keyring's `cli|<opaque>`),
// refusing an item with no account attribute (apply could not recreate it).
// For a non-replace or absent spec it returns ok=false so the caller keeps its
// own default. It is the shared capture-or-refuse policy for the snapshot
// (persistSnapshot) and backup (createBackup) paths.
func captureKeychainAccount(ctx context.Context, tool string, sp artifact.Spec, present bool) (account string, ok bool, err error) {
	if !sp.KeychainReplace || !present {
		return "", false, nil
	}
	acctName, err := artifact.ReadKeychainAccount(ctx, sp)
	if err != nil {
		return "", false, fmt.Errorf("read %s keychain account: %w", tool, err)
	}
	if acctName == "" {
		return "", false, fmt.Errorf("%s keychain item %q has no account attribute; cannot capture it safely", tool, sp.Target)
	}
	return acctName, true, nil
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
			// A per-login dynamic keychain account (codex keyring) is captured
			// verbatim so a rollback recreates the right item; stable-account
			// items keep the spec's constant account.
			keychainAccount := sp.KeychainAccount
			if acctName, ok, err := captureKeychainAccount(ctx, plan.Tool, sp, value.Present); err != nil {
				return meta, err
			} else if ok {
				keychainAccount = acctName
			}
			meta.Artifacts = append(meta.Artifacts, backup.ArtifactRecord{
				Tool: plan.Tool, Name: sp.Name, Kind: sp.Kind,
				Target: sp.Target, Pointer: sp.Pointer,
				KeychainAccount: keychainAccount, KeychainReplace: sp.KeychainReplace,
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

// applyBackup restores live state from a backup, optionally limited to the
// given tools (nil = all).
func applyBackup(ctx context.Context, be secret.Backend, meta backup.Meta, only map[string]bool) error {
	for _, rec := range meta.Artifacts {
		if only != nil && !only[rec.Tool] {
			continue
		}
		value := artifact.Value{}
		if rec.Present {
			data, found, err := be.Get(ctx, rec.SecretRef)
			if err != nil {
				return fmt.Errorf("read backup payload %s: %w", rec.SecretRef, err)
			}
			if !found {
				return errf(constants.ExitNotFound, "backup payload %s is missing from the secret store", rec.SecretRef)
			}
			value = artifact.Value{Data: data, Present: true}
		}
		if err := artifact.ApplyLive(ctx, specFromRecord(rec), value); err != nil {
			return fmt.Errorf("restore %s/%s: %w", rec.Tool, rec.Name, err)
		}
	}
	return nil
}

// specFromRecord rebuilds the artifact spec a backup record was captured from
// (including the keychain account, so a rollback recreates an item under the
// tool's own account, not the generic fallback).
func specFromRecord(rec backup.ArtifactRecord) artifact.Spec {
	return artifact.Spec{
		Name: rec.Name, Kind: rec.Kind, Target: rec.Target,
		Pointer: rec.Pointer, KeychainAccount: rec.KeychainAccount,
		KeychainReplace: rec.KeychainReplace, KeychainMatchAccount: rec.KeychainMatchAccount,
		JSONC: rec.JSONC,
	}
}

// plansFromBackupMeta rebuilds per-tool artifact plans from backup records
// so a rollback can itself be backed up before it overwrites live state.
func plansFromBackupMeta(meta backup.Meta) []toolPlan {
	specsByTool := map[string][]artifact.Spec{}
	order := []string{}
	for _, rec := range meta.Artifacts {
		if _, seen := specsByTool[rec.Tool]; !seen {
			order = append(order, rec.Tool)
		}
		specsByTool[rec.Tool] = append(specsByTool[rec.Tool], specFromRecord(rec))
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

// applySnapshot applies one captured account to the live state.
func applySnapshot(ctx context.Context, be secret.Backend, plan toolPlan) error {
	for _, sp := range plan.Specs {
		metaArt, ok := plan.Meta.Artifacts[sp.Name]
		if !ok {
			return errf(constants.ExitError,
				"snapshot %s/%s lacks artifact %s; re-run kae add --no-login %s %s",
				plan.Tool, plan.Account, sp.Name, plan.Tool, plan.Account)
		}
		// A KeychainReplace item (codex keyring) carries its captured per-login
		// account in the snapshot; the fresh adapter spec cannot know it (the
		// target is not live), so restore it here before ApplyLive writes. Gated
		// on the structural KeychainReplace flag (not just a non-empty field) so
		// a stable-account item's adapter-supplied constant is never overridden.
		if sp.KeychainReplace && metaArt.KeychainAccount != "" {
			sp.KeychainAccount = metaArt.KeychainAccount
		}
		value := artifact.Value{}
		if metaArt.Present {
			data, found, err := be.Get(ctx, metaArt.SecretRef)
			if err != nil {
				return fmt.Errorf("read snapshot payload %s: %w", metaArt.SecretRef, err)
			}
			if !found {
				return errf(constants.ExitError,
					"snapshot payload %s is missing; re-run kae add --no-login %s %s",
					metaArt.SecretRef, plan.Tool, plan.Account)
			}
			value = artifact.Value{Data: data, Present: true}
		}
		if err := artifact.ApplyLive(ctx, sp, value); err != nil {
			return err
		}
	}
	return nil
}
