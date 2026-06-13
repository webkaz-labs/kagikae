package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
)

// CmdPin binds the current directory to a fully isolated profile:
//
//	kae pin [<profile>]
//
// Sugar over `kae mise init --mode pin --write`: renders and writes the
// kagikae block of .mise.toml immediately. The directory gets its own
// private config dir; nothing is shared with the real home by default
// (opt-in via pin_shared_items in config.toml). Use `kae bond` instead
// for shared-settings isolation. Profile defaults to config default_profile.
// Re-running pin refreshes opt-in shared-item links and the credential copy.
// kae unpin removes the block.
func CmdPin(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("pin", flags, false, nil)
	if !ok {
		return constants.ExitUsage
	}
	var profileName string
	switch len(positionals) {
	case 0:
		// default_profile is resolved by runMiseInit
	case 1:
		profileName = positionals[0]
	default:
		return usageError("usage: %s pin [<profile>]", toolName)
	}
	warnIfLegacyPinBlock()
	app := newApp(opts.ConfigPath)
	return runMiseInit(ctx, app, opts, profileName, modePin, false, true)
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
		fmt.Fprintln(os.Stderr, "kae: run `kae unpin && kae pin <profile>` to migrate to isolated pin mode,")
		fmt.Fprintln(os.Stderr, "kae: or `kae unpin && kae bond <profile>` for shared-settings (bond) mode.")
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
