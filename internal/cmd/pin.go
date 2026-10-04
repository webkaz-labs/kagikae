package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/keychain"
	"github.com/webkaz-labs/kagikae/internal/patch"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// CmdPin binds the current directory to a profile, by scope and environment:
//
//	kae pin [-s|-i|-t] [<profile>]      bind every enabled tool in the profile
//	kae pin <tool> <account>            re-bind one tool, keeping the sharing set
//
// --shared/-s (the default) shares settings, sessions, and memory with the real
// home while keeping the credential private; --isolated/-i is fully isolated, with
// opt-in shares via isolated_shared_items in config.toml; --tree/-t is one
// account-agnostic store for the directory's tree with the same opt-in shares
// (claude only). Sugar over `kae mise init --write`: renders and writes the
// kagikae block of .mise.toml. Profile defaults to config default_profile.
// Re-running pin refreshes links and the credential copy. kae unpin removes the
// block.
func CmdPin(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	var shared, isolated, tree, noLink bool
	opts, ok := parseCommon("pin", flags, false, func(fs *flag.FlagSet) {
		registerPinFlags(fs, &shared, &isolated, &tree, &noLink)
	})
	if !ok {
		return constants.ExitUsage
	}
	mode, ok := resolvePinMode(shared, isolated, tree)
	if !ok {
		return constants.ExitUsage
	}
	app := newApp(opts.ConfigPath)
	switch len(positionals) {
	case 2:
		// kae pin <tool> <account>: re-bind one tool in place, keeping the
		// other tools and the directory's existing mechanism. Mode flags
		// cannot be honored here (the mechanism is the directory's, not the
		// caller's), so reject them rather than silently dropping them.
		if shared || isolated || tree {
			return usageError("--shared/--isolated/--tree do not apply to `kae pin <tool> <account>`; the directory's existing mode is left unchanged")
		}
		return runRebind(ctx, app, opts, positionals[0], positionals[1], noLink)
	case 0, 1:
		var profileName string
		if len(positionals) == 1 {
			profileName = positionals[0]
		}
		warnIfLegacyPinBlock()
		return runPin(ctx, app, opts, profileName, mode, noLink)
	default:
		return usageError("usage: %s pin [-s|-i|-t] [<profile>] | %s pin <tool> <account>", toolName, toolName)
	}
}

// resolvePinMode validates `kae pin`'s mutually exclusive mode flags and returns the
// selected bind mode; shared is the default. ok is false (and a usage error already
// emitted) when more than one is set. resolveScope is the two-flag sibling for use and
// run, which take no -t.
func resolvePinMode(shared, isolated, tree bool) (mode string, ok bool) {
	set := 0
	for _, on := range []bool{shared, isolated, tree} {
		if on {
			set++
		}
	}
	switch {
	case set > 1:
		usageError("--shared, --isolated and --tree are mutually exclusive")
		return "", false
	case isolated:
		return modeIsolated, true
	case tree:
		return modeTree, true
	default:
		return modeShared, true
	}
}

// warnIfLegacyPinBlock prints a migration hint when the current directory's
// .mise.toml contains an old overlay-mode kagikae block (written by
// `kae pin --mode overlay` before v0.7.0). The semantics of `kae pin` changed
// in v0.7.0: it now binds an isolated (not shared) environment. Run
// `kae unpin && kae pin <profile>` to migrate.
func warnIfLegacyPinBlock() {
	data, err := os.ReadFile(".mise.toml")
	if err != nil {
		return
	}
	// The overlay-mode comment written by the old `kae pin` (miseinit.go).
	if strings.Contains(string(data), "Directory-scoped account isolation (kae pin, mode: overlay)") ||
		strings.Contains(string(data), "Directory-scoped overlay mode (legacy)") {
		warnf("this directory has a legacy overlay-mode block.")
		infof("to migrate to isolated mode, run: kae unpin && kae pin --isolated <profile>")
		infof("or, for shared-settings mode, run: kae unpin && kae pin --shared <profile>")
	}
}

// runPin binds the current directory by writing the kae-owned mise fragment
// (./.config/mise/conf.d/kagikae.toml): it prepares the isolation dirs first
// (so the fragment never points at a missing dir), renders the fragment with
// the kae: records `kae status` reads back, writes it, converges this
// directory's store links, records one ignore rule per kae-owned path in the
// repository's shared exclude file (never a tracked ./.gitignore — see
// ensureGitExcluded), and prints the export fallback when mise activation is not
// detected.
func runPin(ctx context.Context, app *App, opts commonOpts, profileName, mode string, noLink bool) int {
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	if profileName == "" {
		profileName = app.Config.DefaultProfile
	}
	if profileName == "" {
		return finish(opts, errf(constants.ExitUsage,
			"no profile given and no default_profile in config; run: kae pin <profile>"))
	}
	absDir, err := cwdAbs()
	if err != nil {
		return finish(opts, err)
	}
	pinID := paths.PinID(absDir)
	// Takes the pin lock and records which directory this store belongs to; a
	// store nothing can name is what pinindex.go exists to prevent.
	pinLock, err := app.beginBind(absDir)
	if err != nil {
		return finish(opts, err)
	}
	defer pinLock.Release()
	targets, _, err := app.resolveTargets("all", profileName)
	if err != nil {
		return finish(opts, err)
	}
	be, err := app.secretBackend()
	if err != nil {
		return finish(opts, err)
	}
	// The harvest reads a per-directory store twice in one command — the pin-level pass
	// classifies it, then the materializer's chokepoint reads it again before writing —
	// and reads that account's snapshot payload twice with it. With the cache below the
	// second read is a hit whenever both resolve the same store, which since the
	// credential split is every re-pin that is not migrating one
	// (TestRunPinCoalescesTheHarvestKeychainReads measures exactly that: one read). Both are the exact shape
	// the switch path already coalesces, so opt in rather than restructure signatures to
	// save a `security` call. Safe here for the same reason it is
	// safe there: nothing between the two reads writes that store (the harvest writes
	// only the account snapshot, and a write invalidates its own cache entry), and no
	// child process runs during a bind — which is the one case both caches warn against.
	ctx = keychain.WithReadCache(secret.WithReadCache(ctx))
	be = secret.Cached(be)
	// Read before anything replaces it: the binding being replaced is the only thing
	// that can name the account a *shared* store's credential belongs to (storeAccount),
	// which both harvests below need — and it is what tells a keep whether the label in the
	// directory it is materializing is this store's or a previous binding's. An unreadable
	// one degrades to "unattributable", which the sweep reports and acts on by keeping the
	// credential.
	// The error is kept, not dropped: a fragment that exists and cannot be read means kae has
	// established **nothing** about what this directory was bound to, and saying otherwise
	// makes the keep retract a live label on the strength of an emptiness that is only the
	// read failing (measured 2026-08-08). `readFragmentAt` returns an error for anything that
	// is not "absent", and absent is a genuine answer.
	prevBinding, prevExists, prevErr := readDirFragment()
	prevKnown := prevErr == nil
	entries, prepare, err := app.isolationPlan(ctx, be, mode, targets, pinID, prevBinding, prevKnown)
	if err != nil {
		return finish(opts, err)
	}
	companionEntries, redactions, prepareCompanions, err := app.companionPlan(profileName)
	if err != nil {
		return finish(opts, err)
	}
	// Warnings before the writes they are about (docs/CLI.md § Output Rules), and after
	// the last planning step that can fail, so a refused pin prints no note about a mode
	// change that never happened.
	warnModeUnboundTools(mode, entries)
	if prevKnown && prevExists {
		app.noteModeChange(pinID, prevBinding, mode, entries)
	}

	// Before the stores are written, not after: a re-bind to another account builds a
	// *different* credential store from the account snapshot — as does a mode toggle for a
	// binding that predates the per-account store — so the copy the tool
	// refreshed in the old one has to be harvested first or the directory ends up
	// bound to a credential rotation has already invalidated. Deleting still happens
	// last (pruneDirCredentials).
	// Where each tool's credential is about to be written, taken from the plan rather than
	// recomposed: it is what tells a refusal whether the store it reports is replaced or
	// merely left behind.
	next := map[string]bindDirs{}
	for _, e := range entries {
		if e.Warning == "" {
			next[e.Tool] = bindDirs{Config: e.Dir, Cred: e.CredDir}
		}
	}
	app.harvestSupersededDirCredentials(ctx, be, pinID, absDir, "", prevBinding, next)
	if err := app.prepareIsolationDirs(mode, entries, prepare); err != nil {
		return finish(opts, err)
	}
	if err := prepareCompanions(); err != nil {
		return finish(opts, err)
	}
	// mode is already the user-facing scope label (shared/isolated/tree).
	companionLines := companionFragmentLines(companionEntries)
	if err := writeDirFragment(renderDirFragment(profileName, mode, entries, companionLines, redactions)); err != nil {
		return finish(opts, err)
	}
	// The binding is in place, so any store this directory used before and does not
	// use now is unreachable: a mode toggle moves every tool to the other
	// mechanism's config dir, and a re-pin to another account re-keys the credential
	// store. Their keychain items would otherwise hold a credential nothing points at —
	// which after the per-account split means a **pre-split** binding's item for the
	// toggle, and the previous account's for the re-key.
	// purging=false: this sweep is housekeeping after a re-bind, so a store whose
	// account no longer exists keeps its credential rather than having it deleted by a
	// command the user ran to *bind* something (harvestBeforeDelete).
	reportPruned(app.pruneDirCredentials(ctx, be, pinID, "", boundDirs(entries), prevBinding, false))
	reportf("Bound this directory: profile %s (%s)", profileName, mode)
	// The stores are named by a hash of this directory's path, so nothing in the
	// directory points at them without these links. Converged over every linkable
	// tool, not only the bound ones: a tool dropped from the profile, and
	// --no-link (which wants none here at all), both have to retract a link that
	// would otherwise keep naming a store this directory no longer binds.
	//
	// Before the report rather than after it: the fragment and the links kae just
	// made are one group of kae-owned paths in this directory, and recording them
	// together is what keeps a pin to one `git rev-parse` and one comment line in
	// the exclude file (ensureGitExcluded).
	var want map[string]string
	if !noLink {
		want = wantedStoreLinks(entries)
	}
	linked, removed := app.syncStoreLinks(linkableTools(), want)
	excludeFile := ensureGitExcluded(ctx, append([]string{fragmentRelPath}, linked...)...)
	// The exclude file is named because it is not a place a user would look, and
	// it is outside the working tree; when there was no repository to tell, say
	// nothing about ignoring rather than claiming it.
	if excludeFile != "" {
		reportf("Wrote %s (ignored via %s); your mise.toml is left unchanged.",
			fragmentRelPath, app.displayPath(excludeFile))
	} else {
		reportf("Wrote %s; your mise.toml is left unchanged.", fragmentRelPath)
	}
	app.reportStoreLinks(linked, removed, excludeFile)
	app.reportMiseHandoff(func() string {
		return exportFallback(profileName, entries, companionExportLines(companionEntries))
	})
	return constants.ExitOK
}

// reportMiseHandoff says how a freshly written fragment reaches the shell: mise
// applies it at the next prompt, or fallback supplies the lines to run now.
func (app *App) reportMiseHandoff(fallback func() string) {
	if app.miseActivated() {
		reportf("mise applies it on the next prompt; to apply it now in bash or zsh, run: eval \"$(mise env)\"")
		return
	}
	warnf("mise activation not detected; the binding takes effect once mise is active.")
	infof("to apply it in the current shell now, run:")
	fmt.Fprint(os.Stderr, fallback())
}

// warnModeUnboundTools says on stderr which of the profile's tools the mode leaves on
// the real home (codex under -t), which the fragment otherwise records only as a
// comment. A tool with no isolation variable at all keeps its comment-only warning.
func warnModeUnboundTools(mode string, entries []isolationEntry) {
	m, ok := bindModeFor(mode)
	if !ok {
		return
	}
	for _, e := range entries {
		// Only the mode's own reason (bindMode.unboundReason): a tool with no isolation
		// variable (agy) keeps its comment-only warning, as under -s and -i.
		if reason := modeUnboundMessage(m, e.Tool); e.Warning != "" && e.Warning == reason.Error() {
			warnMessage(reason)
		}
	}
}

// noteModeChange prints, on stderr, that a pin over a binding of another mode moves no
// sessions: one line per tool naming the store it leaves (which a later pin in that
// mode finds again) and, when the new mode is tree and its store does not exist yet,
// that it starts empty (docs/CLI.md § kae pin and mise init Semantics). It is a note,
// so it never changes the exit code. Paths go through displayPath like every other
// path kae prints; nothing here reads a credential.
//
// Called before the stores are prepared, which is what lets it tell a new tree store
// from one an earlier -t binding left.
func (app *App) noteModeChange(pinID string, prev fragmentInfo, mode string, entries []isolationEntry) {
	old, known := bindModeFor(prev.Mode)
	if !known || prev.Mode == mode {
		return
	}
	for _, tool := range constants.Tools {
		dir, bound := app.boundStoreDir(pinID, tool, prev)
		if !bound {
			continue
		}
		notef("changing this directory from %s to %s moves no sessions; %s's stay in %s, "+
			"which `kae pin %s` finds again",
			prev.Mode, mode, tool, app.displayPath(dir), old.flag)
	}
	if mode != modeTree {
		return
	}
	for _, e := range entries {
		if e.Warning == "" && e.Dir != "" && !dirExists(e.Dir) {
			notef("the new tree store %s starts empty", app.displayPath(e.Dir))
		}
	}
}

// CmdUnpin removes the binding from the current directory: it deletes the
// kae-owned mise fragment and also strips a pre-v0.7.2 kagikae marker block
// from .mise.toml (so `kae unpin && kae pin` migrates cleanly). The isolation
// directories and their login state, and everything else in the user's files,
// are left intact — re-pinning the directory restores the sessions and settings
// that were there.
//
// --purge additionally deletes the per-directory **keychain** credentials this
// directory's tools used. That is opt-in because it is the one part of the store a
// re-pin cannot restore from the directory tree (it is restored from the account
// snapshot instead), and because leaving it is otherwise easy to miss: an item under
// a per-directory service name appears nowhere in kae's data dir, and no kae check
// reports one yet (docs/ROADMAP.md). Sessions, settings and the isolation
// directories survive --purge; only the credential goes — and each one is harvested
// into its account snapshot first, so a purge cannot be what destroys a login
// (harvestBeforeDelete).
func CmdUnpin(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	var purge bool
	opts, ok := parseCommon("unpin", flags, false, func(fs *flag.FlagSet) {
		registerUnpinFlags(fs, &purge)
	})
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s unpin [--purge]", toolName)
	}
	return runUnpin(ctx, newApp(opts.ConfigPath), opts, purge)
}

// runUnpin is CmdUnpin with the App injected, the same split CmdPin/runPin use so
// tests drive it against a temp HOME instead of the real environment.
func runUnpin(ctx context.Context, app *App, opts commonOpts, purge bool) int {
	// unpin is the escape hatch, and removing the fragment needs only a relative
	// path — so a cwd that cannot be resolved (os.Getwd walks every ancestor, and
	// can fail where opening a file in the directory still works) must not block
	// it. Only the lock and --purge need the absolute path: without it, take
	// neither, and refuse --purge rather than sweep the wrong pin.
	absDir, absErr := cwdAbs()
	if absErr == nil {
		pinLock, err := app.acquirePinLock(absDir)
		if err != nil {
			return finish(opts, err)
		}
		defer pinLock.Release()
	} else if purge {
		return finish(opts, fmt.Errorf("resolve the current directory for --purge: %w", absErr))
	} else {
		warnf("could not resolve this directory (%v); removing the fragment without the pin lock", absErr)
	}
	// The binding names the account behind a shared store's credential, so --purge
	// has to read it before removing it (storeAccount). Not fatal: without it the
	// sweep keeps a credential it cannot attribute.
	prevBinding, _, _ := readDirFragment()
	removedFragment, err := removeDirFragment()
	if err != nil {
		return finish(opts, err)
	}
	removedBlock, err := removeLegacyMiseBlock(".mise.toml")
	if err != nil {
		return finish(opts, err)
	}
	switch {
	case removedFragment && removedBlock:
		reportf("Removed %s and the legacy kagikae block from .mise.toml", fragmentRelPath)
	case removedFragment:
		reportf("Removed %s", fragmentRelPath)
	case removedBlock:
		reportf("Removed the legacy kagikae block from .mise.toml")
	default:
		return finish(opts, errf(constants.ExitNotFound,
			"this directory is not bound (no %s and no kagikae block in .mise.toml)", fragmentRelPath))
	}
	// The links are pointers, so they go with the binding rather than with the
	// store: `unpin` keeps the store on purpose, but a link left behind names one
	// nothing in this directory binds any more. Only kae's own are removed, and
	// only the links — everything they pointed at survives, which is what makes a
	// re-pin restore the directory.
	// Nothing is wanted here, so nothing is linked and the exclude call records
	// nothing — it asks git nothing either (ensureGitExcluded), which is what keeps
	// `kae unpin` from running a subprocess to record an empty set. The entries an
	// earlier pin left are kept, like the fragment's own.
	linked, removed := app.syncStoreLinks(linkableTools(), nil)
	app.reportStoreLinks(linked, removed, ensureGitExcluded(ctx, linked...))
	// After the fragment is gone: nothing points at any of this directory's stores
	// now, so the sweep keeps none of them. Before it, a failure would leave a live
	// binding whose credential kae had already deleted.
	//
	// The sweep harvests each credential into its account snapshot before deleting
	// it, so it needs the secret store; a purge that cannot open it would be
	// destroying logins it has no way to preserve, which is worse than leaving the
	// items for a later run.
	if purge {
		be, err := app.secretBackend()
		if err != nil {
			warnf("could not open the secret store (%v); this directory's per-directory "+
				"credentials are left in place rather than deleted without being harvested", err)
			return constants.ExitOK
		}
		// Same coalescing as a bind: the sweep reads each store to classify it and again
		// inside the harvest, and no child process runs here either.
		ctx = keychain.WithReadCache(secret.WithReadCache(ctx))
		be = secret.Cached(be)
		reportPruned(app.pruneDirCredentials(ctx, be, paths.PinID(absDir), "", nil, prevBinding, true))
	}
	return constants.ExitOK
}

// removeLegacyMiseBlock deletes a pre-v0.7.2 marker-delimited kagikae block
// from path, keeping the rest of the file byte-identical. It reports whether a
// block was present; a missing file or absent block is not an error (the
// fragment is now the primary binding).
func removeLegacyMiseBlock(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	before, after, ok := cutMiseBlock(string(data))
	if !ok {
		return false, nil
	}
	if err := patch.WriteFileAtomic(path, []byte(before+after), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// boundDirs is the set of store directories a binding points at, which is what
// pruneDirCredentials must keep. A warning entry has no env entry and therefore
// no store to keep.
func boundDirs(entries []isolationEntry) map[string]bool {
	dirs := map[string]bool{}
	for _, entry := range entries {
		if entry.Warning == "" && entry.Dir != "" {
			dirs[entry.Dir] = true
		}
	}
	return dirs
}

// reportPruned prints what a credential sweep removed. It is part of the command's
// result, so it goes to stdout; the sweep's own warnings go to stderr where they
// are detected.
func reportPruned(removals []message) {
	for _, line := range removals {
		reportMessage(line)
	}
}
