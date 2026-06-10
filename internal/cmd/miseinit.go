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
//	kae mise init [--profile NAME] [--write]
//
// Default prints the snippet; --write creates .mise.toml or replaces the
// marker-delimited kagikae block. An existing file without markers is never
// modified.
func CmdMise(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] != "init" {
		return usageError("usage: %s mise init [--profile NAME] [--write]", toolName)
	}
	flags, positionals := splitArgs(args[1:], "--profile")
	var profileName string
	write := false
	opts, ok := parseCommon("mise init", flags, false, func(fs *flag.FlagSet) {
		fs.StringVar(&profileName, "profile", "", "profile for KAE_PROFILE (default: config default_profile)")
		fs.BoolVar(&write, "write", false, "write/update .mise.toml in the current directory")
	})
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s mise init [--profile NAME] [--write]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runMiseInit(ctx, app, opts, profileName, write)
}

func runMiseInit(_ context.Context, app *App, opts commonOpts, profileName string, write bool) int {
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
	block := app.miseBlock(profileName)
	if !write {
		fmt.Print(block)
		fmt.Fprintln(os.Stderr, "\nkae: preview only; apply with: kae mise init --profile "+profileName+" --write")
		return constants.ExitOK
	}
	if err := writeMiseBlock(".mise.toml", block); err != nil {
		return finish(opts, err)
	}
	fmt.Println("Updated .mise.toml (kagikae block)")
	return constants.ExitOK
}

// miseBlock renders the marker-delimited snippet with tasks for the enabled
// tools that have a login-capable adapter.
func (app *App) miseBlock(profileName string) string {
	var b strings.Builder
	fmt.Fprintln(&b, miseBlockStart)
	fmt.Fprintln(&b, "[env]")
	fmt.Fprintf(&b, "KAE_PROFILE = %q\n\n", profileName)
	fmt.Fprintln(&b, "[tasks.ai-use]")
	fmt.Fprintln(&b, `description = "Switch AI CLI accounts to this project's profile"`)
	fmt.Fprintln(&b, `run = "kae switch all $KAE_PROFILE"`)
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
		fmt.Fprintf(&b, "run = \"kae run %s $KAE_PROFILE -- %s\"\n", tool, tool)
	}
	fmt.Fprintln(&b, miseBlockEnd)
	return b.String()
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
