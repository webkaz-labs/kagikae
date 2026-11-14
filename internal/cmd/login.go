package cmd

import (
	"context"
	"flag"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/keychain"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// loginCommand returns the interactive official login invocation per tool.
// kae never reimplements a login flow; it launches the upstream one.
// docs/ADAPTERS.md "Login Commands" is the normative source for this table.
func loginCommand(tool string) []string {
	switch tool {
	case constants.ToolClaude:
		return []string{"claude", "/login"}
	case constants.ToolCodex:
		return []string{"codex", "login"}
	case constants.ToolOpencode:
		return []string{"opencode", "auth", "login"}
	case constants.ToolCursor:
		return []string{"cursor-agent", "login"}
	case constants.ToolCopilot:
		return []string{"copilot", "login"}
	default:
		return nil
	}
}

// toolBinary is the executable name for a tool's CLI, used in the generated
// mise run tasks. The adapter is the single source of truth (it usually
// equals the tool id, but cursor's binary is cursor-agent); an unknown tool
// falls back to its id.
func toolBinary(tool string) string {
	if adp, err := adapter.ForTool(tool); err == nil {
		return adp.Binary()
	}
	return tool
}

// CmdAdd registers an account:
//
//	kae add <tool> [<account>] [--restore]    official login flow + snapshot
//	kae add --no-login <tool> [<account>]     snapshot the current live state
//
// With the account name omitted, kae auto-detects it from the live login
// identity: --no-login reads the current live state, the login flow reads the
// post-login state. An explicit
// name always wins. The default backs up the current auth state, launches the
// official login flow, captures the result into the account, and (with
// --restore) puts the previous login back. --no-login skips the flow and
// snapshots whatever is live now (it supports --dry-run; the login flow does
// not).
func CmdAdd(ctx context.Context, args []string) int {
	// --identity takes a value; splitArgs must not treat the value as a positional.
	flags, positionals := splitArgs(args, "--identity")
	opts, restore, noLogin, ok := parseAddFlags(flags)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) < 1 || len(positionals) > 2 {
		return usageLine("add [--no-login] <tool> [<account>] [--restore]")
	}
	if noLogin && restore {
		return usageError("--restore needs the login flow; it cannot be combined with --no-login")
	}
	if !noLogin && opts.DryRun {
		return usageError("--dry-run applies to --no-login snapshots only")
	}
	// Resolve the tool prefix and validate it now; an explicit account name is
	// validated here too (an auto-detected one is sanitized at detection time).
	tool, err := resolveToolArg(positionals[0])
	if err != nil {
		return finish(opts, err)
	}
	if err := validateTool(tool); err != nil {
		return finish(opts, err)
	}
	explicitName := ""
	if len(positionals) == 2 {
		explicitName = positionals[1]
		if err := validateToolAccount(tool, explicitName); err != nil {
			return finish(opts, err)
		}
	}
	app := newApp(opts.ConfigPath)
	app.pinnedGlobalScope()
	if noLogin {
		return runCapture(ctx, app, opts, tool, explicitName)
	}
	return runLogin(ctx, app, opts, tool, explicitName, restore)
}

// parseAddFlags parses `kae add`'s flags, carrying --identity and --no-restart in
// opts.
func parseAddFlags(flags []string) (opts commonOpts, restore, noLogin, ok bool) {
	var noRestart bool
	identityFlag := ""
	opts, ok = parseCommon("add", flags, true, func(fs *flag.FlagSet) {
		registerAddFlags(fs, &restore, &noLogin, &noRestart, &identityFlag)
	})
	opts.NoRestart = noRestart
	opts.IdentityOverride = identityFlag
	return opts, restore, noLogin, ok
}

// runLogin launches the official login flow and snapshots the result. tool is
// already canonical (CmdAdd); explicitName is the given account name, or "" to
// auto-detect it. The name is resolved after the login flow exits, because the
// login identity only becomes live once the flow completes.
//
// Once the flow has succeeded and its lock is released, it reconciles the tool's
// resident processes against the account finally left live (docs/CLI.md § kae add
// Semantics, codex resident processes). A failed flow and an auth_unchanged exit
// reconcile nothing.
func runLogin(ctx context.Context, app *App, opts commonOpts, tool, explicitName string, restore bool) int {
	return runLoginRequest(ctx, app, opts, loginRequest{tool: tool, explicitName: explicitName, restore: restore})
}

// loginRequest is what one run of the login flow is asked to do.
type loginRequest struct {
	tool         string // canonical tool id
	explicitName string // the account named, or "" to detect it from the post-login identity
	restore      bool   // put the pre-login live state back after capturing (kae add --restore)
	// activeOnly is the global form of kae relogin: explicitName is the active
	// account read before the tool lock, and the flow refuses to start once it is
	// not the active account any more.
	activeOnly bool
}

// runLoginRequest is runLogin for a full loginRequest; the global form of
// kae relogin enters here.
func runLoginRequest(ctx context.Context, app *App, opts commonOpts, req loginRequest) int {
	tool := req.tool
	code, before, result := loginFlow(ctx, app, opts, req)
	if code != constants.ExitOK {
		return code
	}
	slot := app.residentsAfterLogin(ctx, tool, before, residentModeOf(opts))
	// The result line follows the reconcile, so that the reconcile's success sits
	// under it as a switch's does.
	reportMessage(result)
	slot.printDone(true)
	return code
}

// loginFlow is runLogin up to the release of the tool lock, which its deferred
// releaseLocks performs before runLogin reconciles. before reads the credential
// live before the flow, as the flow's backup recorded it, and result is the
// report line runLogin prints after the reconcile.
func loginFlow(ctx context.Context, app *App, opts commonOpts, req loginRequest) (code int, before credentialReader, result message) {
	tool, explicitName, restore := req.tool, req.explicitName, req.restore
	if err := app.requireConfig(); err != nil {
		return finish(opts, err), nil, message{}
	}
	command := loginCommand(tool)
	if command == nil {
		return finish(opts, errf(constants.ExitUnsupported,
			"the kae add login flow does not support %s yet (see docs/CLI.md)", tool)), nil, message{}
	}
	// The login runs with the shell's environment, which kae cannot strip a
	// variable from: under kae's isolation the tool would write the login into the
	// store kae's own variable names, while kae compares and captures the global one.
	if refusal := app.errKaeIsolatedShell(tool, req.activeOnly); refusal != nil {
		return finish(opts, refusal), nil, message{}
	}
	// Plan the tool before the name is known; only plan.Account (set after login)
	// depends on it, and that is not read until captureSnapshot below.
	plan, err := app.planTool(ctx, tool, explicitName)
	if err != nil {
		return finish(opts, err), nil, message{}
	}
	be, err := app.secretBackend()
	if err != nil {
		return finish(opts, err), nil, message{}
	}
	locks, err := app.acquireLocks([]string{tool})
	if err != nil {
		return finish(opts, err), nil, message{}
	}
	defer releaseLocks(locks)

	st, err := app.loadState()
	if err != nil {
		return finish(opts, err), nil, message{}
	}
	// Both checks read the active account under the tool lock and refuse before
	// the backup and the login flow, so a refusal changes nothing.
	active := st.Active[tool]
	if restore && explicitName != "" && explicitName == active {
		return finish(opts, errRestoreActiveAccount(tool, explicitName)), nil, message{}
	}
	if req.activeOnly && explicitName != active {
		return finish(opts, errf(constants.ExitUnsafeRefused,
			"the active %s account changed before login (it is no longer %s/%s); the login flow was not started; %s",
			tool, tool, explicitName, reloginRemedy(tool))), nil, message{}
	}
	meta, err := app.createBackup(ctx, be, []toolPlan{plan}, st, constants.BackupReasonLogin)
	if err != nil {
		return finish(opts, err), nil, message{}
	}

	if explicitName != "" {
		infof("complete the %s login flow; the result is captured as %s when it exits (previous state backed up as %s)",
			tool, tool+"/"+explicitName, meta.ID)
	} else {
		infof("complete the %s login flow; the result is captured as the detected account when it exits (previous state backed up as %s)",
			tool, meta.ID)
	}
	if code, err := runner.RunInteractive(ctx, nil, command[0], command[1:]...); err != nil {
		return finish(opts, errLaunchLogin(tool, err)), nil, message{}
	} else if code != 0 {
		infof("%s exited with %d; capturing whatever auth state is live now", command[0], code)
	}
	// The login child has exited, so from here on the live store changes only
	// through kae's own writes, which invalidate the cache: the comparison, the
	// detection, the capture guard and the capture read each keychain item once.
	ctx = keychain.WithReadCache(ctx)

	// A login flow can move the credential to the tool's other store — codex under
	// `cli_auth_credentials_store = "auto"` writes its keychain item and deletes
	// auth.json — so re-resolve before comparing, capturing, or restoring. On the
	// pre-login specs the comparison below reads the store the tool abandoned, finds
	// it unchanged (empty before, empty after), and reports that a successful login
	// changed nothing.
	plan = app.refreshPlan(ctx, plan)

	if changed, err := loginChangedAuth(ctx, be, meta, plan); err != nil {
		return finishLoginFailure(ctx, app, opts, be, meta, restore, loginStepCompare, err), nil, message{}
	} else if !changed {
		// The live state is still the pre-login state, so there is nothing
		// to capture and (with --restore) nothing to put back.
		hint := tool
		if explicitName != "" {
			hint = tool + " " + explicitName
		}
		return finish(opts, errf(constants.ExitAuthUnchanged,
			"%s login flow exited without changing auth; nothing captured; to snapshot the current login, run: kae add --no-login %s",
			tool, hint)), nil, message{}
	}

	// The login flow changed auth, so the new identity is now live: resolve the
	// account name (auto-detected from it unless given explicitly) and the raw
	// identity to record in the snapshot, then snapshot.
	accountName, identity, err := app.resolveAccount(ctx, tool, explicitName, opts.IdentityOverride)
	if err != nil {
		return finishLoginFailure(ctx, app, opts, be, meta, restore, loginStepDetect, err), nil, message{}
	}
	plan.Account = accountName
	plan.Identity = identity

	// A refused capture puts the pre-login backup back whether or not --restore
	// was given, so another account's login is not left live under this account's
	// record (captureIntoExistingRefusal).
	if refusal, err := app.captureIntoExistingRefusal(ctx, be, plan, accountName == active); err != nil {
		return finishLoginFailure(ctx, app, opts, be, meta, restore, loginStepCapture, err), nil, message{}
	} else if refusal != nil {
		if restoreErr := app.applyBackup(ctx, be, meta, nil, false); restoreErr != nil {
			return finish(opts, doubleFailure(loginStepCapture.phrase(),
				refusal.err(msgf("nothing was captured"), true), restoreErr, meta.ID)), nil, message{}
		}
		return finish(opts, refusal.err(
			msgf("nothing was captured and the previous login was restored from backup %s", meta.ID), true,
		)), nil, message{}
	}

	if err := app.captureSnapshot(ctx, be, plan); err != nil {
		return finishLoginFailure(ctx, app, opts, be, meta, restore, loginStepCapture, err), nil, message{}
	}

	if restore && explicitName == "" && accountName == active {
		// errRestoreActiveAccount's case, known only now; the new login passed the
		// attribution guard above, so it stays live.
		return constants.ExitOK, backupCredential(be, meta, tool),
			msgf("Captured %s/%s; it is the active account, so the new login stays live instead of restoring its previous copy (kept in backup %s)",
				tool, accountName, meta.ID)
	}
	if restore {
		if err := app.applyBackup(ctx, be, meta, nil, false); err != nil {
			return finish(opts, errf(exitOf(err),
				"captured %s/%s but restoring the previous login failed: %v; run: kae rollback --to %s",
				tool, accountName, err, meta.ID)), nil, message{}
		}
		return constants.ExitOK, backupCredential(be, meta, tool),
			msgf("Captured %s/%s and restored the previous login", tool, accountName)
	}
	if err := app.saveActive(map[string]string{tool: accountName}, ""); err != nil {
		return finish(opts, err), nil, message{}
	}
	return constants.ExitOK, backupCredential(be, meta, tool), msgf("Captured %s/%s (now active)", tool, accountName)
}

// loginChangedAuth reports whether any live artifact differs from the
// pre-login backup, i.e. whether the login flow actually changed auth.
// A missing backup payload counts as changed so an internal inconsistency
// never blocks a legitimate capture.
func loginChangedAuth(ctx context.Context, be secret.Backend, meta backup.Meta, plan toolPlan) (bool, error) {
	records := map[string]backup.ArtifactRecord{}
	for _, rec := range meta.Artifacts {
		if rec.Tool == plan.Tool {
			records[rec.Name] = rec
		}
	}
	for _, sp := range plan.Specs {
		// An identity-only artifact is identity metadata, not auth. claude rewrites
		// /oauthAccount (at minimum its profileFetchedAt) on any login, so
		// counting it here would report "auth changed" for a re-login to the
		// same account and defeat the auth_unchanged guard.
		if sp.IdentityOnly {
			continue
		}
		live, err := artifact.ReadLive(ctx, sp)
		if err != nil {
			return false, l10n.Errorf("read live %s/%s: %w", plan.Tool, sp.Name, err)
		}
		rec, ok := records[sp.Name]
		if !ok {
			return true, nil
		}
		// Unlike recapture, login propagates a backend-read error rather than
		// treating it as a change, so an internal read failure surfaces.
		differs, err := snapshotArtifactDiffers(ctx, be, rec.SecretRef, rec.Present, live)
		if err != nil {
			return false, l10n.Errorf("read backup payload %s: %w", rec.SecretRef, err)
		}
		if differs {
			return true, nil
		}
	}
	return false, nil
}

// finishLoginFailure reports a failed post-login step. With --restore the
// user asked to end up on the previous login no matter what; put it back
// even when the failed step leaves auth in the post-login state.
func finishLoginFailure(ctx context.Context, app *App, opts commonOpts, be secret.Backend, meta backup.Meta, restore bool, step loginStep, err error) int {
	if restore {
		if restoreErr := app.applyBackup(ctx, be, meta, nil, false); restoreErr != nil {
			return finish(opts, doubleFailure(step.phrase(), err, restoreErr, meta.ID))
		}
		switch step {
		case loginStepCompare:
			return finish(opts, errf(exitOf(err), "compare auth after login"+restoredFromBackup, meta.ID, err))
		case loginStepDetect:
			return finish(opts, errf(exitOf(err), "detect the logged-in account"+restoredFromBackup, meta.ID, err))
		default:
			return finish(opts, errf(exitOf(err), "capture after login"+restoredFromBackup, meta.ID, err))
		}
	}
	switch step {
	case loginStepCompare:
		return finish(opts, l10n.Errorf("compare auth after login failed (previous state is in backup %s): %w", meta.ID, err))
	case loginStepDetect:
		return finish(opts, l10n.Errorf("detect the logged-in account failed (previous state is in backup %s): %w", meta.ID, err))
	default:
		return finish(opts, l10n.Errorf("capture after login failed (previous state is in backup %s): %w", meta.ID, err))
	}
}

// loginStep is the post-login step finishLoginFailure reports as failed.
type loginStep int

const (
	loginStepCompare loginStep = iota // compare auth after login
	loginStepDetect                   // detect the logged-in account
	loginStepCapture                  // capture after login
)

// phrase names the step for doubleFailure, which takes the operation as a message.
func (st loginStep) phrase() message {
	switch st {
	case loginStepCompare:
		return msgf("compare auth after login")
	case loginStepDetect:
		return msgf("detect the logged-in account")
	}
	return msgf("capture after login")
}

// errRestoreActiveAccount refuses `kae add --restore` for the account that is
// already active: the previous live state it would put back is that same account's
// older copy, so an expired login would stay expired with the new one only in the
// snapshot.
func errRestoreActiveAccount(tool, accountName string) *cmdError {
	return errf(constants.ExitUsage,
		"%s/%s is the active account, so --restore would put its previous login back over the new one; nothing was changed; %s",
		tool, accountName, reloginRemedy(tool))
}

// errKaeIsolatedShell is the login flow's refusal of a shell whose environment
// carries kae's isolation (kaeIsolationVar), or nil when it carries none. relogin
// is the global form of kae relogin, which is never inside a bound directory, so
// its remedy is the bound directory above; kae add's is a shell outside it. Under
// tool's global isolated home there is no bound directory to name, and no shell
// kae can name either (docs/ROADMAP.md § Re-login under global isolated mode).
func (app *App) errKaeIsolatedShell(tool string, relogin bool) *cmdError {
	envVar, globalIsolated := app.kaeIsolationVar(tool)
	switch {
	case envVar == "":
		return nil
	case globalIsolated:
		return errf(constants.ExitUsage,
			"kae's global isolated home for %s is active in this shell (%s points into it, as kae use -i sets it), so a global %s login here would write into that home instead of the global store kae captures; kae cannot log %s in globally until that isolation is off in this shell",
			tool, envVar, tool, tool)
	case relogin:
		return errf(constants.ExitUsage,
			"this directory is not bound, but this shell carries a bound directory's isolation (%s points into kae's stores), so a global %s login here would land in that store; run kae relogin in the bound directory itself, or in a shell where that binding is not active",
			envVar, tool)
	default:
		return errf(constants.ExitUsage,
			"this shell carries a bound directory's isolation (%s points into kae's stores), so the %s login flow would write into that store instead of the global one; run kae add outside the bound directory, in a shell where its binding is not active; to log in a bound directory's account, run kae relogin %s in that directory",
			envVar, tool, tool)
	}
}
