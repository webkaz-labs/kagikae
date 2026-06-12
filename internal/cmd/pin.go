package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
)

// CmdPin binds the current directory to a profile:
//
//	kae pin [<profile>] [--mode overlay|home|auth] [--auto]
//
// Sugar over `kae mise init --write`: renders and writes the kagikae block
// of .mise.toml immediately. Default mode is overlay — auth and session
// state private to the directory, settings/skills shared with the real
// home. Profile defaults to config default_profile. Re-running pin
// refreshes the overlay's shared-item links. kae unpin removes the block.
func CmdPin(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args, "--mode")
	var mode string
	auto := false
	opts, ok := parseCommon("pin", flags, false, func(fs *flag.FlagSet) {
		fs.StringVar(&mode, "mode", modeOverlay, "isolation: overlay (shared config), home (fully separate), or auth (global tasks)")
		fs.BoolVar(&auto, "auto", false, "add a [hooks.enter] auto-switch (auth mode only)")
	})
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
		return usageError("usage: %s pin [<profile>] [--mode overlay|home|auth] [--auto]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runMiseInit(ctx, app, opts, profileName, mode, auto, true)
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
