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

const (
	miseBlockStart = "# >>> kagikae >>>"
	miseBlockEnd   = "# <<< kagikae <<<"
)

// CmdMise generates project-local mise integration:
//
//	kae mise init [--profile NAME] [--mode auth|home] [--auto] [--write]
//
// Default prints the snippet; --write creates .mise.toml or replaces the
// marker-delimited kagikae block. An existing file without markers is never
// modified. --mode home renders [env] entries that point each isolatable
// tool at its per-account kae home instead of auth-mode hooks/tasks; --auto
// (auth mode only) adds a [hooks.enter] entry running kae sync --quiet.
func CmdMise(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] != "init" {
		return usageError("usage: %s mise init [--profile NAME] [--mode auth|home] [--auto] [--write]", toolName)
	}
	flags, positionals := splitArgs(args[1:], "--profile", "--mode")
	var profileName, mode string
	write, auto := false, false
	opts, ok := parseCommon("mise init", flags, false, func(fs *flag.FlagSet) {
		fs.StringVar(&profileName, "profile", "", "profile for KAE_PROFILE (default: config default_profile)")
		fs.StringVar(&mode, "mode", constants.ModeAuth, "rendered integration: auth (tasks) or home (isolated tool homes)")
		fs.BoolVar(&auto, "auto", false, "add a [hooks.enter] auto-switch (auth mode only)")
		fs.BoolVar(&write, "write", false, "write/update .mise.toml in the current directory")
	})
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s mise init [--profile NAME] [--mode auth|home] [--auto] [--write]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runMiseInit(ctx, app, opts, profileName, mode, auto, write)
}

func runMiseInit(_ context.Context, app *App, opts commonOpts, profileName, mode string, auto, write bool) int {
	if mode != constants.ModeAuth && mode != modeHome {
		return usageError("unsupported mise init mode %q (modes: auth, home)", mode)
	}
	if auto && mode == modeHome {
		return usageError("--auto applies to auth mode only: home mode already takes effect on directory entry via [env]")
	}
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	if profileName == "" {
		profileName = app.Config.DefaultProfile
	}
	if profileName == "" {
		return finish(opts, errf(constants.ExitUsage,
			"no profile given and no default_profile in config; use --profile <name>"))
	}
	var block string
	var homeDirs []string
	if mode == modeHome {
		// Home mode renders per-account paths, so the profile mapping must
		// exist (auth mode renders only the name and tolerates a later define).
		targets, _, err := app.resolveTargets("all", profileName)
		if err != nil {
			return finish(opts, err)
		}
		block, homeDirs = app.miseHomeBlock(profileName, targets)
	} else {
		block = app.miseBlock(profileName, auto)
	}
	if !write {
		fmt.Print(block)
		hint := "kae mise init --profile " + profileName
		if mode == modeHome {
			hint += " --mode home"
		}
		if auto {
			hint += " --auto"
		}
		fmt.Fprintln(os.Stderr, "\nkae: preview only; apply with: "+hint+" --write")
		return constants.ExitOK
	}
	// Pre-create the isolated homes before touching .mise.toml so a failure
	// here cannot leave the block exporting directories that do not exist
	// (a stray kae-owned 0700 dir is harmless; the reverse is not).
	for _, dir := range homeDirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return finish(opts, fmt.Errorf("create home-mode dir: %w", err))
		}
	}
	if err := writeMiseBlock(".mise.toml", block); err != nil {
		return finish(opts, err)
	}
	fmt.Println("Updated .mise.toml (kagikae block)")
	return constants.ExitOK
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
		fmt.Fprintln(&b, `script = "kae sync --quiet"`)
		fmt.Fprintln(&b)
	}
	fmt.Fprintln(&b, "[tasks.ai-use]")
	fmt.Fprintln(&b, `description = "Switch AI CLI accounts to this project's profile"`)
	fmt.Fprintf(&b, "run = \"kae switch all $%s\"\n", constants.EnvKaeProfile)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "[tasks.ai-current]")
	fmt.Fprintln(&b, `description = "Show active AI CLI accounts"`)
	fmt.Fprintln(&b, `run = "kae current"`)
	for _, tool := range app.enabledTools() {
		if tool == constants.ToolAgy {
			continue // experimental adapter; no generated run task yet
		}
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "[tasks.%s]\n", tool)
		fmt.Fprintf(&b, "description = \"Run %s with this project's account\"\n", tool)
		fmt.Fprintf(&b, "run = \"kae run %s $%s -- %s\"\n", tool, constants.EnvKaeProfile, tool)
	}
	fmt.Fprintln(&b, miseBlockEnd)
	return b.String()
}

// miseHomeBlock renders the home-mode snippet: [env] entries pointing each
// tool with a stable isolation env var at its per-account kae home
// (docs/DATA-MODEL.md), switching account and config directory inside the
// directory only. Tools without one, or with home mode disabled, keep the
// real home and are noted with a comment. Returns the block and the home
// directories to create on --write.
func (app *App) miseHomeBlock(profileName string, targets []runTarget) (string, []string) {
	var b strings.Builder
	var dirs []string
	fmt.Fprintln(&b, miseBlockStart)
	fmt.Fprintln(&b, "# Directory-scoped isolation (kae mise init --mode home): account and")
	fmt.Fprintln(&b, "# config directory switch inside this directory only; the global live")
	fmt.Fprintln(&b, "# auth state is never touched, safe across concurrent terminals.")
	fmt.Fprintln(&b, "[env]")
	fmt.Fprintf(&b, "%s = %q\n", constants.EnvKaeProfile, profileName)
	for _, tgt := range targets {
		envVar := isolationEnvVar(tgt.Tool)
		if envVar == "" {
			fmt.Fprintf(&b, "# warning: %s has no stable home-isolation env var; it keeps the real home (docs/ROADMAP.md)\n", tgt.Tool)
			continue
		}
		if !app.Config.HomeModeEnabled(tgt.Tool) {
			fmt.Fprintf(&b, "# warning: home mode is disabled for %s (tools.%s.home_mode_enabled = false); it keeps the real home\n", tgt.Tool, tgt.Tool)
			continue
		}
		dir := app.Paths.HomeModeDir(tgt.Tool, tgt.Account)
		fmt.Fprintf(&b, "%s = %q\n", envVar, dir)
		dirs = append(dirs, dir)
	}
	fmt.Fprintln(&b, miseBlockEnd)
	return b.String(), dirs
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
	content := string(data)
	start := strings.Index(content, miseBlockStart)
	end := strings.Index(content, miseBlockEnd)
	if start < 0 || end < 0 || end < start {
		return errf(constants.ExitUnsafeRefused,
			"%s exists without a kagikae marker block; append the --print output manually or add the markers %q ... %q",
			path, miseBlockStart, miseBlockEnd)
	}
	// Replace everything from the start marker through the end marker
	// (inclusive) with the freshly rendered block.
	rest := strings.TrimPrefix(content[end+len(miseBlockEnd):], "\n")
	updated := content[:start] + block + rest
	return patch.WriteFileAtomic(path, []byte(updated), 0o644)
}
