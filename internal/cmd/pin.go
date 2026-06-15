package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
)

// CmdPin binds the current directory to a profile, by scope and environment:
//
//	kae pin [-s|-i] [<profile>]         bind every enabled tool in the profile
//	kae pin <tool> <account>            re-bind one tool, keeping the sharing set
//
// --shared/-s (the default) shares settings, sessions, and memory with the
// real home while keeping the credential private; --isolated/-i is fully
// isolated, with opt-in shares via pin_shared_items in config.toml. Sugar over
// `kae mise init --write`: renders and writes the kagikae block of .mise.toml.
// Profile defaults to config default_profile. Re-running pin refreshes links
// and the credential copy. kae unpin removes the block.
func CmdPin(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	var shared, isolated bool
	opts, ok := parseCommon("pin", flags, false, func(fs *flag.FlagSet) {
		registerScopeFlags(fs, &shared, &isolated)
	})
	if !ok {
		return constants.ExitUsage
	}
	isolatedMode, ok := resolveScope(shared, isolated)
	if !ok {
		return constants.ExitUsage
	}
	app := newApp(opts.ConfigPath)
	switch len(positionals) {
	case 2:
		// kae pin <tool> <account>: re-bind one tool in place, keeping the
		// other tools and the directory's existing mechanism. Scope flags
		// cannot be honored here (the mechanism is the directory's, not the
		// caller's), so reject them rather than silently dropping them.
		if shared || isolated {
			return usageError("--shared/--isolated do not apply to `kae pin <tool> <account>`; the directory's existing mode is kept")
		}
		return runRebind(ctx, app, opts, positionals[0], positionals[1])
	case 0, 1:
		var profileName string
		if len(positionals) == 1 {
			profileName = positionals[0]
		}
		// --shared maps to the per-directory shared mechanism (bond),
		// --isolated to the fully isolated mechanism (pin); shared is default.
		mode := modeBond
		if isolatedMode {
			mode = modePin
		}
		warnIfLegacyPinBlock()
		return runPin(ctx, app, opts, profileName, mode)
	default:
		return usageError("usage: %s pin [-s|-i] [<profile>] | %s pin <tool> <account>", toolName, toolName)
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
		fmt.Fprintln(os.Stderr, "kae: warning: this directory has a legacy overlay-mode block.")
		fmt.Fprintln(os.Stderr, "kae: run `kae unpin && kae pin --isolated <profile>` to migrate to isolated mode,")
		fmt.Fprintln(os.Stderr, "kae: or `kae unpin && kae pin --shared <profile>` for shared-settings mode.")
	}
}

// runPin binds the current directory by writing the kae-owned mise fragment
// (./.config/mise/conf.d/kagikae.toml): it prepares the isolation dirs first
// (so the fragment never points at a missing dir), renders the fragment with
// the kae: records `kae status` reads back, writes it, adds it to .gitignore,
// and prints the export fallback when mise activation is not detected.
func runPin(ctx context.Context, app *App, opts commonOpts, profileName, mode string) int {
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	if profileName == "" {
		profileName = app.Config.DefaultProfile
	}
	if profileName == "" {
		return finish(opts, errf(constants.ExitUsage,
			"no profile given and no default_profile in config; use: kae pin <profile>"))
	}
	targets, _, err := app.resolveTargets("all", profileName)
	if err != nil {
		return finish(opts, err)
	}
	entries, prepare, err := app.isolationPlan(ctx, mode, targets)
	if err != nil {
		return finish(opts, err)
	}
	if err := app.prepareIsolationDirs(mode, entries, prepare); err != nil {
		return finish(opts, err)
	}
	scope := userScopeMode(mode)
	if err := writeDirFragment(renderDirFragment(profileName, scope, entries)); err != nil {
		return finish(opts, err)
	}
	fmt.Printf("Pinned this directory: profile %s (%s)\n", profileName, scope)
	fmt.Printf("Wrote %s (added to .gitignore); your mise.toml is untouched.\n", fragmentRelPath)
	if app.miseActivated() {
		fmt.Println("mise applies it on the next prompt (or run `mise env`).")
	} else {
		fmt.Fprintln(os.Stderr, "kae: warning: mise activation not detected; the binding takes effect once mise is active.")
		fmt.Fprintln(os.Stderr, "kae: to apply it in the current shell now, run:")
		fmt.Fprint(os.Stderr, exportFallback(profileName, entries))
	}
	return constants.ExitOK
}

// CmdUnpin removes the binding from the current directory: it deletes the
// kae-owned mise fragment and also strips a pre-v0.7.2 kagikae marker block
// from .mise.toml (so `kae unpin && kae pin` migrates cleanly). The isolation
// directories and their login state, and everything else in the user's files,
// are left intact.
func CmdUnpin(_ context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("unpin", flags, false, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s unpin", toolName)
	}
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
		fmt.Printf("Removed %s and the legacy kagikae block from .mise.toml\n", fragmentRelPath)
	case removedFragment:
		fmt.Printf("Removed %s\n", fragmentRelPath)
	case removedBlock:
		fmt.Println("Removed the legacy kagikae block from .mise.toml")
	default:
		return finish(opts, errf(constants.ExitNotFound,
			"this directory is not pinned (no %s and no kagikae block in .mise.toml)", fragmentRelPath))
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
