package cmd

import (
	"context"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// CmdBond binds the current directory in bond mode (per-directory shared):
//
//	kae bond [<profile>]
//
// Sugar over `kae mise init --mode bond --write`: renders and writes the
// kagikae block of .mise.toml. Settings, sessions, and memory stay shared
// with the real home; only credentials are private to the directory.
// Re-running bond refreshes symlinks from the real home.
func CmdBond(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("bond", flags, false, nil)
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
		return usageError("usage: %s bond [<profile>]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runMiseInit(ctx, app, opts, profileName, constants.ModeBond, false, true)
}
