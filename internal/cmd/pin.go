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
		return runPinRebind(ctx, app, opts, positionals[0], positionals[1])
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
		return runMiseInit(ctx, app, opts, profileName, mode, false, true)
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

// CmdUnpin removes the kagikae block from .mise.toml in the current
// directory, leaving everything else (other tasks, env, the overlay/home
// directories and their login state) intact.
func CmdUnpin(_ context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("unpin", flags, false, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s unpin", toolName)
	}
	if err := removeMiseBlock(".mise.toml"); err != nil {
		return finish(opts, err)
	}
	fmt.Println("Removed the kagikae block from .mise.toml")
	return constants.ExitOK
}

// removeMiseBlock deletes the marker-delimited kagikae block, keeping the
// rest of the file byte-identical.
func removeMiseBlock(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return errf(constants.ExitNotFound, "%s does not exist; nothing to unpin", path)
	}
	if err != nil {
		return err
	}
	before, after, ok := cutMiseBlock(string(data))
	if !ok {
		return errf(constants.ExitNotFound, "%s has no kagikae block; nothing to unpin", path)
	}
	return patch.WriteFileAtomic(path, []byte(before+after), 0o644)
}
