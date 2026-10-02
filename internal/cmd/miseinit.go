package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

const (
	miseBlockStart = "# >>> kagikae >>>"
	miseBlockEnd   = "# <<< kagikae <<<"
)

// CmdMise generates project-local mise integration — the auth-mode tasks and
// the opt-in enter hook:
//
//	kae mise init [--profile NAME] [-P NAME] [--auto] [--write]
//
// Default prints the snippet; --write creates .mise.toml or replaces the
// marker-delimited kagikae block. An existing file without markers is never
// modified. --auto adds a [hooks.enter] entry running `kae use --auto --quiet`. The
// former isolation modes (home/overlay/bond/pin) are gone: bind a directory
// with `kae pin -s|-i|-t`, which owns its own mise fragment.
func CmdMise(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] != "init" {
		return usageError("usage: %s mise init [--profile NAME] [--auto] [--write]", toolName)
	}
	flags, positionals := splitArgs(args[1:], "--profile", "P", "--mode")
	var profileName, mode string
	write, auto := false, false
	opts, ok := parseCommon("mise init", flags, false, func(fs *flag.FlagSet) {
		registerMiseInitFlags(fs, &profileName, &mode, &auto, &write)
	})
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s mise init [--profile NAME] [--auto] [--write]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runMiseInit(ctx, app, opts, profileName, mode, auto, write)
}

func runMiseInit(_ context.Context, app *App, opts commonOpts, profileName, mode string, auto, write bool) int {
	if mode != constants.ModeAuth {
		return usageError(
			"kae mise init renders auth mode only (mode %q is no longer supported); to bind a directory instead, run: kae pin -s|-i|-t", mode,
		)
	}
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	if profileName == "" {
		profileName = app.Config.DefaultProfile
	}
	if profileName == "" {
		return finish(opts, errf(constants.ExitUsage,
			"no profile given and no default_profile in config; use -P <name>"))
	}
	block := app.miseBlock(profileName, auto)
	if !write {
		fmt.Print(block)
		hint := "kae mise init --profile " + profileName
		if auto {
			hint += " --auto"
		}
		fmt.Fprintln(os.Stderr, "\nkae: preview only; to apply, run: "+hint+" --write")
		return constants.ExitOK
	}
	dir, err := os.Getwd()
	if err != nil {
		return finish(opts, err)
	}
	l, err := app.acquirePinLock(dir)
	if err != nil {
		return finish(opts, err)
	}
	defer l.Release()
	if err := writeMiseBlock(".mise.toml", block); err != nil {
		return finish(opts, err)
	}
	fmt.Printf("Updated .mise.toml: profile %s (auth mode)\n", profileName)
	fmt.Println("Next: mise trust   (mise refuses untrusted configs; its error until then is expected)")
	return constants.ExitOK
}

// isolationPlan resolves the per-tool env entries and the matching directory
// preparer for a per-directory bind (shared/isolated/tree). Used by `kae pin`, which
// renders the kae-owned mise fragment; both mechanisms key their stores by the
// bound directory, whose pin id the caller has already resolved (it needs it for
// the pin lock and the breadcrumb, and two derivations of one id is one too many).
// prev is the binding being replaced, and every materializer needs it for one reason: a
// keep may only retract an identity label it can show is stale, and the acting directory's
// own previous binding is the thing that shows it, through modeLabelStale.
func (app *App) isolationPlan(ctx context.Context, be secret.Backend, mode string, targets []runTarget, pinID string, prev fragmentInfo, prevKnown bool) ([]isolationEntry, func(tool, account string) (string, error), error) {
	// prevKnown gates it because a fragment kae could not read establishes nothing about
	// what this directory was bound to; modeLabelStale owns the rest.
	stale := func(tool, account string) bool {
		return prevKnown && modeLabelStale(mode, prev.Accounts[tool], account)
	}
	m, ok := bindModeFor(mode)
	if !ok {
		return nil, nil, errf(constants.ExitError, "unknown per-directory bind kind %q", mode)
	}
	return app.modeIsolationEntries(m, targets, pinID),
		func(tool, account string) (string, error) {
			return m.prepare(app, ctx, be, tool, account, pinID, stale(tool, account))
		}, nil
}

// prepareIsolationDirs runs the preparer for every non-warning entry, so a
// failure surfaces before kae writes a fragment or block pointing at a
// directory that does not exist.
//
// A profile may name an account that was never captured, or a tool whose
// credential store cannot be scoped to a directory at all. Binding the whole
// profile still makes sense then — the other tools bind, and the directory is
// usable once that account is captured — so those are warnings rather than
// failures, matching how a tool with no isolation env var is handled
// (warnUnisolatableCredential). The warning is what makes it honest: the
// materializers used to skip a missing credential in silence. Every other error
// still fails the bind.
//
// Operations naming one tool and account do not take this path and keep failing
// loudly: for `kae pin <tool> <account>` the unisolatable tool is the whole
// request, not one row of it.
func (app *App) prepareIsolationDirs(mode string, entries []isolationEntry, prepare func(tool, account string) (string, error)) error {
	for _, entry := range entries {
		if entry.Warning != "" {
			continue
		}
		if _, err := prepare(entry.Tool, entry.Account); err != nil &&
			!warnUnisolatableCredential(err, entry.Tool, entry.Account) {
			return fmt.Errorf("prepare %s-mode dir for %s: %w", mode, entry.Tool, err)
		}
	}
	return nil
}

// cwdAbs returns the current working directory as an absolute path.
func cwdAbs() (string, error) {
	return filepath.Abs(".")
}

// miseBlock renders the auth-mode marker-delimited snippet with tasks for
// the enabled tools that have a login-capable adapter; auto adds the
// opt-in enter hook.
func (app *App) miseBlock(profileName string, auto bool) string {
	var b strings.Builder
	fmt.Fprintln(&b, miseBlockStart)
	fmt.Fprintln(&b, "[env]")
	fmt.Fprintf(&b, "%s = %q\n\n", constants.EnvKaeProfile, profileName)
	if auto {
		fmt.Fprintln(&b, "[hooks.enter]")
		fmt.Fprintln(&b, "# Opt-in caveat: this runs on every directory entry, and auth mode")
		fmt.Fprintln(&b, "# mutates the global live auth state shared by every terminal, not just")
		fmt.Fprintln(&b, "# this directory. Firing requires `mise activate`, a trusted config,")
		fmt.Fprintln(&b, "# and `mise settings experimental=true` (mise hooks are experimental).")
		fmt.Fprintln(&b, `run = "kae use --auto --quiet"`)
		fmt.Fprintln(&b)
	}
	fmt.Fprintln(&b, "[tasks.ai-use]")
	fmt.Fprintln(&b, `description = "Switch AI CLI accounts to this project's profile"`)
	fmt.Fprintf(&b, "run = \"kae use $%s\"\n", constants.EnvKaeProfile)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "[tasks.ai-current]")
	fmt.Fprintln(&b, `description = "Show active AI CLI accounts"`)
	fmt.Fprintln(&b, `run = "kae"`)

	// Dynamic-completion tasks: their `usage` arg `complete` directives call the
	// hidden `kae __complete` backend (complete.go), so `mise run <task> <TAB>`
	// offers kae's live profiles/tools/accounts through the same source as the
	// native shell completion. Account completion is intentionally NOT
	// tool-scoped here: mise's `complete run` does not expose the prior `tool`
	// argument, so it lists every account; kae's own shell completion keeps the
	// tool-scoped behavior. mise reads each arg as the
	// `usage_<name>` env var in the run script.
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "[tasks.ai-switch]")
	fmt.Fprintln(&b, `description = "Switch all tools to a profile (TAB-completes live profiles)"`)
	fmt.Fprintln(&b, "usage = '''")
	fmt.Fprintln(&b, `arg "<profile>" help="kae profile to apply"`)
	fmt.Fprintln(&b, `complete "profile" run="kae __complete profiles"`)
	fmt.Fprintln(&b, "'''")
	fmt.Fprintln(&b, `run = 'kae use "$usage_profile"'`)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "[tasks.ai-switch-tool]")
	fmt.Fprintln(&b, `description = "Switch one tool's account (TAB-completes live tools/accounts)"`)
	fmt.Fprintln(&b, "usage = '''")
	fmt.Fprintln(&b, `arg "<tool>" help="AI CLI tool"`)
	fmt.Fprintln(&b, `arg "<account>" help="captured account name"`)
	fmt.Fprintln(&b, `complete "tool" run="kae __complete tools"`)
	fmt.Fprintln(&b, `complete "account" run="kae __complete accounts"`)
	fmt.Fprintln(&b, "'''")
	fmt.Fprintln(&b, `run = 'kae use "$usage_tool" "$usage_account"'`)
	for _, tool := range app.enabledTools() {
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "[tasks.%s]\n", tool)
		fmt.Fprintf(&b, "description = \"Run %s with this project's account\"\n", tool)
		fmt.Fprintf(&b, "run = \"kae run %s $%s -- %s\"\n", tool, constants.EnvKaeProfile, toolBinary(tool))
	}
	fmt.Fprintln(&b, miseBlockEnd)
	return b.String()
}

// isolationEntry is one tool's resolved row of a per-directory bind: either an
// env entry pointing at Dir, or a warning comment explaining why the tool keeps
// its real home (no stable home-isolation env var).
type isolationEntry struct {
	Tool    string
	Account string
	EnvVar  string
	Dir     string
	// The credential half, empty for a tool that cannot separate its credential
	// from its home (credentialEnvVar). Both are written, so the tool reads this
	// account's one shared credential while everything else stays in Dir.
	CredEnvVar string
	CredDir    string
	Warning    string // non-empty: rendered as a comment, no env entry
}

// isolationEntryFor builds the env entries of one tool the bind mode binds: the
// isolation variable pointing at dir and, where the tool has one, the credential
// variable pointing at the account's credential store. A tool the mode leaves on the
// real home never reaches it; modeIsolationEntries gives that one its warning entry
// from bindMode.unboundReason.
//
// The credential entry is written in **every** mode, and it is the account rather
// than the directory that selects it. That is what the shared mode's own
// account-agnostic store cannot express: two directories bound to one account
// each hold a copy there, and claude's refresh token rotates single-use, so the
// first refresh in either one logs the other out (docs/ROADMAP.md § One
// credential per account).
func (app *App) isolationEntryFor(tgt runTarget, dir string) isolationEntry {
	entry := isolationEntry{Tool: tgt.Tool, Account: tgt.Account, EnvVar: isolationEnvVar(tgt.Tool), Dir: dir}
	if credDir := app.credStoreDir(tgt.Tool, tgt.Account); credDir != "" {
		entry.CredEnvVar, entry.CredDir = credentialEnvVar(tgt.Tool), credDir
	}
	return entry
}

// noIsolationVarReason is the warning entry of a tool with no home-isolation variable.
func noIsolationVarReason(tool string) string {
	return fmt.Sprintf("%s has no stable home-isolation env var; it keeps the real home (docs/ROADMAP.md)", tool)
}

// writeEnvEntries renders the shared [env] block — KAE_PROFILE plus each bound
// tool's env entries (its isolation entry, and its credential entry where the tool
// has one), or a warning comment for a tool that keeps the real home — for the
// kae pin fragment (renderDirFragment). One place to change env-line formatting.
func writeEnvEntries(b *strings.Builder, profileName string, entries []isolationEntry, companionLines []string) {
	fmt.Fprintln(b, "[env]")
	fmt.Fprintf(b, "%s = %q\n", constants.EnvKaeProfile, profileName)
	for _, entry := range entries {
		if entry.Warning != "" {
			fmt.Fprintf(b, "# warning: %s\n", entry.Warning)
			continue
		}
		fmt.Fprintf(b, "%s = %q\n", entry.EnvVar, entry.Dir)
		if entry.CredEnvVar != "" {
			fmt.Fprintf(b, "%s = %q\n", entry.CredEnvVar, entry.CredDir)
		}
	}
	for _, line := range companionLines {
		fmt.Fprintln(b, line)
	}
}

// bondDenylistItems returns the items excluded from bond-mode symlink sharing for
// a tool: the files a bind must keep private (constants.PrivateBindItems, the one
// literal — internal/config refuses a user from re-listing any of them, which is
// why it lives a layer down) plus the user's own extras. docs/ADAPTERS.md
// "Per-directory shared bind" is the normative description of both halves.
func (app *App) bondDenylistItems(tool string) []string {
	return append(constants.PrivateBindNames(tool), app.Config.SharedDenylistExtra(tool)...)
}

// modeIsolationEntries resolves the per-tool env entries for a per-directory bind
// in mode m. Each target carries its account: an account-agnostic store (shared,
// one per pinID×tool) uses it for credential-copy bookkeeping only, a per-account
// one (isolated, …/isolated/<account>/config/) also composes its path from it.
//
// A tool the mode does not bind (bindMode.tools: codex under tree) gets a warning
// entry and keeps the real home, exactly as a tool with no isolation variable does.
func (app *App) modeIsolationEntries(m bindMode, targets []runTarget, pinID string) []isolationEntry {
	entries := make([]isolationEntry, 0, len(targets))
	for _, tgt := range targets {
		if reason := m.unboundReason(tgt.Tool); reason != "" {
			// Rendered as a comment only: the fragment records no account for it.
			entries = append(entries, isolationEntry{Tool: tgt.Tool, Account: tgt.Account, Warning: reason})
			continue
		}
		entries = append(entries, app.isolationEntryFor(tgt, m.storeDir(app.Paths, pinID, tgt.Tool, tgt.Account)))
	}
	return entries
}

// unboundReason is why a bind in mode m leaves tool on the real home, or "" for a tool
// it binds: the one answer the planner's warning entries, the bind's stderr warning and
// uninstall's reconstruction of a rendered fragment all read.
func (m bindMode) unboundReason(tool string) string {
	switch {
	case isolationEnvVar(tool) == "":
		return noIsolationVarReason(tool)
	case !m.bindsTool(tool):
		return modeUnboundReason(m, tool)
	default:
		return ""
	}
}

// modeUnboundReason is why mode m, which does not bind tool, leaves it on the real home:
// the fragment's warning comment, the bind's stderr warning and the re-bind's refusal.
func modeUnboundReason(m bindMode, tool string) string {
	return fmt.Sprintf("%s mode binds %s only, so %s keeps the real home (docs/ROADMAP.md)",
		m.name, strings.Join(m.tools, ", "), tool)
}

// errRealHomeIsStore refuses a store that is the tool's real home itself. The format is the
// caller's constant: "the real %s home resolves to the <store> itself; unset %s and retry".
func errRealHomeIsStore(format, tool string) error {
	return errf(constants.ExitUnsafeRefused, format, tool, isolationEnvVar(tool))
}

const (
	realHomeIsBondDir      = "the real %s home resolves to the bond dir itself; unset %s and retry"
	realHomeIsIsolatedDir  = "the real %s home resolves to the isolated config dir itself; unset %s and retry"
	realHomeIsTreeStoreDir = "the real %s home resolves to the tree store itself; unset %s and retry"
)

// linkSharedItem links dst to src, replacing a stale symlink. A real file or
// directory at dst is a private override and is left unchanged.
func linkSharedItem(src, dst string) error {
	info, statErr := os.Lstat(dst)
	if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("stat link item %s: %w", dst, statErr)
	}
	if statErr == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		if current, readErr := os.Readlink(dst); readErr == nil && current == src {
			return nil // already linked correctly
		}
		if err := os.Remove(dst); err != nil {
			return fmt.Errorf("refresh link %s: %w", dst, err)
		}
	}
	if err := os.Symlink(src, dst); err != nil {
		return fmt.Errorf("link item %s: %w", dst, err)
	}
	return nil
}

// prepareBond creates the bond directory for one tool/pinID: symlinks every
// real-home entry except the hard-coded denylist, then materializes the bound
// account's credential privately (writeDirCredential). Idempotent: stale
// symlinks are refreshed; real files in the bond dir (private overrides) are
// left untouched.
func (app *App) prepareBond(ctx context.Context, be secret.Backend, tool, account, pinID string, staleLabel bool) (string, error) {
	bondDir := app.Paths.SharedDir(pinID, tool)
	if err := os.MkdirAll(bondDir, 0o700); err != nil {
		return "", fmt.Errorf("create shared dir: %w", err)
	}
	realHome := app.realToolHome(tool)
	if filepath.Clean(realHome) == filepath.Clean(bondDir) {
		return "", errRealHomeIsStore(realHomeIsBondDir, tool)
	}

	denylist := app.bondDenylistItems(tool)
	denied := make(map[string]bool, len(denylist))
	for _, item := range denylist {
		denied[item] = true
	}

	// Symlink every real-home entry except the denylist.
	des, err := os.ReadDir(realHome)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read real %s home: %w", tool, err)
	}
	// The names this loop treats as shareable, which the reconcile below then keeps.
	// Built here, from the loop's own denied check, so the intent set and the linking
	// cannot disagree about which names are shareable. It is not "everything the loop
	// linked": the loop also skips a name whose destination is already a real file (a
	// private override), and that name stays intended — the reconcile leaves real
	// files alone regardless, so the two rules agree on the outcome.
	intended := make(map[string]bool, len(des))
	for _, de := range des {
		name := de.Name()
		if denied[name] {
			continue
		}
		intended[name] = true
		src := filepath.Join(realHome, name)
		if err := linkSharedItem(src, filepath.Join(bondDir, name)); err != nil {
			return "", err
		}
	}

	// In bond mode the real home's own listing is the statement of intent — share
	// everything it has that is not denied — so an entry that has left it is no
	// longer intended and its link goes with it.
	//
	// Only when that listing was actually read. A real home kae could not enumerate
	// (absent, an unmounted HOME, a CLAUDE_CONFIG_DIR naming a directory that does
	// not exist, or a future tool missing from realToolHome's switch, which returns
	// "" and so reads as absent) leaves the intent *unknown* rather than empty, and so
	// does one that reads fine and lists nothing shareable. Both land here as
	// len(intended) == 0, and both then warn instead of retracting.
	//
	// docs/ADAPTERS.md § "Per-directory shared bind" is normative for why that way
	// round — the two failures are not symmetric — so it is not re-argued here.
	stale, err := unintendedLinks(bondDir, intended)
	if err != nil {
		return "", err
	}
	switch {
	case len(intended) > 0:
		if err := retractLinks(bondDir, stale); err != nil {
			return "", err
		}
	case len(stale) > 0:
		fmt.Fprintf(os.Stderr,
			"kae: warning: the real %s home (%s) lists nothing to share, so kae cannot tell "+
				"whether %d shared link(s) in %s are still wanted; leaving them in place. "+
				"If that home is right, remove the links by hand; if it is not, unset %s (or "+
				"fix it), then run: kae pin\n",
			tool, realHome, len(stale), bondDir, isolationEnvVar(tool))
	}

	// Materialize the bound account's credential where the tool will read it.
	if err := app.writeDirCredential(ctx, be, tool, account, bondDir, staleLabel); err != nil {
		return "", err
	}

	return bondDir, nil
}

// unintendedLinks lists the symlinks in dir whose name is not in the intended set.
// Finding is separate from removing so a caller with no established intent can
// report what it would have retracted instead of retracting it (prepareBond).
//
// Together they are what makes a per-directory bind *converge* on its intended
// shape instead of only growing: both linking loops walk their source — the real
// home's entries, or the configured opt-in list — so neither can see a link whose
// name has since left that source. What each mode counts as intended, and the
// residue that made this necessary, are in docs/ADAPTERS.md (§ per-directory shared
// bind, § per-directory isolated bind).
//
// Symlinks only, the same rule both linking loops follow: a real file is a private
// override, and for an auth artifact it is usually kae's own per-directory copy,
// which must survive. os.ReadDir reports the entry's own type without following it,
// so a link to a directory is still seen as a link.
//
// Every name comes from ReadDir, so it is a single path component and `intended` can
// only ever *prevent* a removal — this cannot reach outside dir even if a caller's
// intended set carries a separator. dir must exist; both callers MkdirAll it first,
// and a third one has to do the same rather than rely on a tolerated ENOENT.
//
// ponytail: byte-compares names. If a store's filesystem normalizes them (HFS+
// turning NFC into NFD) while the real home hands back the other form, a
// just-created link would be retracted on every bind. Needs a non-ASCII top-level
// entry *and* a store on a different filesystem from the home, so it is recorded
// rather than handled; the fix is to compare NFC-normalized names, the same
// normalization claude's keychain service name needs.
func unintendedLinks(dir string, intended map[string]bool) ([]string, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read bind dir %s: %w", dir, err)
	}
	names := []string{}
	for _, de := range des {
		if name := de.Name(); !intended[name] && de.Type()&os.ModeSymlink != 0 {
			names = append(names, name)
		}
	}
	return names, nil
}

// retractLinks unlinks the named entries of dir, each re-checked immediately
// before the removal.
//
// The re-check is the point, and dropping it is a regression this function was
// written to avoid: a `DirEntry.Type()` from a directory listing is a **snapshot**
// of the whole directory, so between the listing and this removal a name can stop
// being a symlink — the bound tool writing that file, an editor's write-to-temp
// then rename, a dotfile manager — and removing it on the stale type deletes a real
// file, the one thing the reconcile promises never to do. The code this replaced
// did its Lstat immediately before its Remove; splitting the walk from the removal
// is what opened the window.
//
// A name that has already gone is not an error for the same reason: something else
// removing a doomed link is the outcome this wanted anyway, and failing the bind
// over it would be a `kae pin` that fails for having less work to do.
func retractLinks(dir string, names []string) error {
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("retract shared link %s: %w", path, err)
		}
	}
	return nil
}

// preparePinConfig creates the pin config directory for one tool/account/pinID:
// symlinks opt-in shared items from the real home, then materializes the
// account's credential privately (writeDirCredential). Idempotent: stale
// symlinks are refreshed; real files are left.
func (app *App) preparePinConfig(ctx context.Context, be secret.Backend, tool, account, pinID string, staleLabel bool) (string, error) {
	return app.prepareOptInStore(ctx, be, tool, account, app.Paths.IsolatedConfigDir(pinID, tool, account), "isolated config dir", realHomeIsIsolatedDir, staleLabel)
}

// prepareTree creates the tree store for one tool/pinID
// (isolation/<pin-id>/<tool>/tree/), whichever account is bound: the isolated bind's
// opt-in links, and the account's credential in its own credential store with the
// identity cache in the tree store (docs/ADAPTERS.md § Per-directory tree bind). An
// account switch runs this same preparer, which is what keeps the store's sessions,
// history and settings and rewrites only what names the account.
func (app *App) prepareTree(ctx context.Context, be secret.Backend, tool, account, pinID string, staleLabel bool) (string, error) {
	return app.prepareOptInStore(ctx, be, tool, account, app.Paths.TreeDir(pinID, tool), "tree store", realHomeIsTreeStoreDir, staleLabel)
}

// prepareOptInStore materializes configDir as a store whose links from the real home
// are the configured isolated_shared_items: the isolated and tree binds, which differ
// only in how configDir is composed. what names the store in errors.
func (app *App) prepareOptInStore(ctx context.Context, be secret.Backend, tool, account, configDir, what, realHomeFormat string, staleLabel bool) (string, error) {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", what, err)
	}
	realHome := app.realToolHome(tool)
	if filepath.Clean(realHome) == filepath.Clean(configDir) {
		return "", errRealHomeIsStore(realHomeFormat, tool)
	}

	// Symlink opt-in shared items from the real home. The *configured list* is this
	// mode's statement of intent, not the real home's contents (docs/ADAPTERS.md
	// § "Per-directory isolated bind" is normative, including what that means for an
	// item whose source is missing and for a symlink placed here by hand).
	items := app.Config.IsolatedSharedItems(tool)
	intended := make(map[string]bool, len(items))
	for _, item := range items {
		intended[item] = true
	}
	for _, item := range items {
		src := filepath.Join(realHome, item)
		if _, err := os.Stat(src); err != nil {
			continue // only link what exists
		}
		if err := linkSharedItem(src, filepath.Join(configDir, item)); err != nil {
			return "", err
		}
	}

	// Unconditional, unlike the shared bind's reconcile: an empty list is this field's
	// default and states full isolation positively, so there is no "kae could not
	// tell" case to warn about.
	stale, err := unintendedLinks(configDir, intended)
	if err != nil {
		return "", err
	}
	if err := retractLinks(configDir, stale); err != nil {
		return "", err
	}

	// Materialize the bound account's credential where the tool will read it.
	if err := app.writeDirCredential(ctx, be, tool, account, configDir, staleLabel); err != nil {
		return "", err
	}

	return configDir, nil
}

// pinCredItems returns the plaintext credential file names a bound directory
// may hold for a tool. On a keychain platform the item, not the file, is what
// the tool reads, so writeDirCredential uses this list to remove the superseded
// file rather than to write it.
func (app *App) pinCredItems(tool string) []string {
	switch tool {
	case constants.ToolClaude:
		return []string{".credentials.json"}
	case constants.ToolCodex:
		return []string{"auth.json"}
	default:
		return nil
	}
}

// cutMiseBlock splits content around the marker-delimited kagikae block:
// the text before the start marker and after the end marker (its trailing
// newline consumed). ok is false when the markers are missing or malformed.
func cutMiseBlock(content string) (before, after string, ok bool) {
	start := strings.Index(content, miseBlockStart)
	end := strings.Index(content, miseBlockEnd)
	if start < 0 || end < 0 || end < start {
		return "", "", false
	}
	return content[:start], strings.TrimPrefix(content[end+len(miseBlockEnd):], "\n"), true
}

// writeMiseBlock creates .mise.toml or replaces an existing kagikae block.
// Files without the markers are left untouched (refused with guidance).
func writeMiseBlock(path, block string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return patch.WriteFileAtomic(path, []byte(block), 0o644)
	}
	if err != nil {
		return err
	}
	before, after, ok := cutMiseBlock(string(data))
	if !ok {
		return errf(constants.ExitUnsafeRefused,
			"%s exists without a kagikae marker block; append the --print output manually or add the markers %q ... %q",
			path, miseBlockStart, miseBlockEnd)
	}
	return patch.WriteFileAtomic(path, []byte(before+block+after), 0o644)
}
