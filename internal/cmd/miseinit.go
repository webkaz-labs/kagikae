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
	"github.com/webkaz-labs/kagikae/internal/paths"
)

const (
	miseBlockStart = "# >>> kagikae >>>"
	miseBlockEnd   = "# <<< kagikae <<<"
)

// CmdMise generates project-local mise integration (the low-level form of
// kae pin):
//
//	kae mise init [--profile NAME] [--mode auth|home|overlay] [--auto] [--write]
//
// Default prints the snippet; --write creates .mise.toml or replaces the
// marker-delimited kagikae block. An existing file without markers is never
// modified. The isolation modes (overlay, home) render [env] entries that
// point each isolatable tool at its per-account kae home instead of the
// auth-mode hooks/tasks; --auto (auth mode only) adds a [hooks.enter] entry
// running kae apply --quiet.
func CmdMise(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] != "init" {
		return usageError("usage: %s mise init [--profile NAME] [--mode auth|home|overlay] [--auto] [--write]", toolName)
	}
	flags, positionals := splitArgs(args[1:], "--profile", "--mode")
	var profileName, mode string
	write, auto := false, false
	opts, ok := parseCommon("mise init", flags, false, func(fs *flag.FlagSet) {
		fs.StringVar(&profileName, "profile", "", "profile for KAE_PROFILE (default: config default_profile)")
		fs.StringVar(&mode, "mode", constants.ModeAuth, "rendered integration: auth (tasks), home, or overlay (isolated tool homes)")
		fs.BoolVar(&auto, "auto", false, "add a [hooks.enter] auto-switch (auth mode only)")
		fs.BoolVar(&write, "write", false, "write/update .mise.toml in the current directory")
	})
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageError("usage: %s mise init [--profile NAME] [--mode auth|home|overlay] [--auto] [--write]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runMiseInit(ctx, app, opts, profileName, mode, auto, write)
}

func runMiseInit(_ context.Context, app *App, opts commonOpts, profileName, mode string, auto, write bool) int {
	if mode != constants.ModeAuth && mode != modeHome && mode != modeOverlay && mode != constants.ModeBond {
		return usageError("unsupported mise init mode %q (modes: auth, home, overlay, bond)", mode)
	}
	if auto && mode != constants.ModeAuth {
		return usageError("--auto applies to auth mode only: isolation modes already take effect on directory entry via [env]")
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
	var entries []isolationEntry
	var pinID string
	if mode == constants.ModeAuth {
		block = app.miseBlock(profileName, auto)
	} else {
		// Isolation modes render per-account paths, so the profile mapping
		// must exist (auth mode renders only the name and tolerates a later
		// define).
		targets, _, err := app.resolveTargets("all", profileName)
		if err != nil {
			return finish(opts, err)
		}
		if mode == constants.ModeBond {
			absDir, err := cwdAbs()
			if err != nil {
				return finish(opts, err)
			}
			pinID = paths.PinID(absDir)
			entries = app.bondIsolationEntries(targets, pinID)
		} else {
			entries = app.isolationEntries(mode, targets)
		}
		block = app.miseIsolationBlock(profileName, mode, entries)
	}
	if !write {
		fmt.Print(block)
		hint := "kae mise init --profile " + profileName
		if mode != constants.ModeAuth {
			hint += " --mode " + mode
		}
		if auto {
			hint += " --auto"
		}
		fmt.Fprintln(os.Stderr, "\nkae: preview only; apply with: "+hint+" --write")
		return constants.ExitOK
	}
	// Prepare the isolated homes before touching .mise.toml so a failure
	// here cannot leave the block exporting directories that do not exist
	// (a stray kae-owned 0700 dir is harmless; the reverse is not).
	var prepare func(tool, account string) (string, error)
	switch mode {
	case constants.ModeBond:
		prepare = func(tool, account string) (string, error) {
			return app.prepareBond(tool, account, pinID)
		}
	case modeOverlay:
		prepare = app.prepareOverlay
	default:
		prepare = app.prepareHome
	}
	for _, entry := range entries {
		if entry.Warning != "" {
			continue
		}
		if _, err := prepare(entry.Tool, entry.Account); err != nil {
			return finish(opts, fmt.Errorf("prepare %s-mode dir for %s: %w", mode, entry.Tool, err))
		}
	}
	if err := writeMiseBlock(".mise.toml", block); err != nil {
		return finish(opts, err)
	}
	fmt.Printf("Updated .mise.toml: profile %s, mode %s\n", profileName, mode)
	fmt.Println("Next: mise trust   (mise refuses untrusted configs; its error until then is expected)")
	return constants.ExitOK
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
		fmt.Fprintln(&b, `script = "kae apply --quiet"`)
		fmt.Fprintln(&b)
	}
	fmt.Fprintln(&b, "[tasks.ai-use]")
	fmt.Fprintln(&b, `description = "Switch AI CLI accounts to this project's profile"`)
	fmt.Fprintf(&b, "run = \"kae use $%s\"\n", constants.EnvKaeProfile)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "[tasks.ai-current]")
	fmt.Fprintln(&b, `description = "Show active AI CLI accounts"`)
	fmt.Fprintln(&b, `run = "kae"`)
	for _, tool := range app.enabledTools() {
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "[tasks.%s]\n", tool)
		fmt.Fprintf(&b, "description = \"Run %s with this project's account\"\n", tool)
		fmt.Fprintf(&b, "run = \"kae run %s $%s -- %s\"\n", tool, constants.EnvKaeProfile, toolBinary(tool))
	}
	fmt.Fprintln(&b, miseBlockEnd)
	return b.String()
}

// isolationEntry is one tool's resolved row of an isolation-mode (home /
// overlay) mise block: either an env entry pointing at Dir, or a warning
// comment explaining why the tool keeps its real home.
type isolationEntry struct {
	Tool    string
	Account string
	EnvVar  string
	Dir     string
	Warning string // non-empty: rendered as a comment, no env entry
}

// isolationEntries resolves the per-tool env entries for an isolation mode.
// Gates mirror kae run: no stable env var or a disabled per-tool mode
// becomes a warning (kae run refuses the same cases with exit 5).
func (app *App) isolationEntries(mode string, targets []runTarget) []isolationEntry {
	entries := make([]isolationEntry, 0, len(targets))
	for _, tgt := range targets {
		entry := isolationEntry{Tool: tgt.Tool, Account: tgt.Account}
		entry.EnvVar = isolationEnvVar(tgt.Tool)
		if entry.EnvVar == "" {
			entry.Warning = fmt.Sprintf(
				"%s has no stable home-isolation env var; it keeps the real home (docs/ROADMAP.md)", tgt.Tool)
			entries = append(entries, entry)
			continue
		}
		if mode == modeOverlay {
			entry.Dir = app.Paths.OverlayDir(tgt.Tool, tgt.Account)
			if !app.Config.OverlayModeEnabled(tgt.Tool) {
				entry.Warning = fmt.Sprintf(
					"overlay mode is disabled for %s (tools.%s.overlay_mode_enabled = false); it keeps the real home",
					tgt.Tool, tgt.Tool)
			}
		} else {
			entry.Dir = app.Paths.HomeModeDir(tgt.Tool, tgt.Account)
			if !app.Config.HomeModeEnabled(tgt.Tool) {
				entry.Warning = fmt.Sprintf(
					"home mode is disabled for %s (tools.%s.home_mode_enabled = false); it keeps the real home",
					tgt.Tool, tgt.Tool)
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

// miseIsolationBlock renders the home/overlay/bond snippet: [env] entries
// pointing each isolatable tool at its per-account kae home
// (docs/DATA-MODEL.md), switching inside the directory only.
func (app *App) miseIsolationBlock(profileName, mode string, entries []isolationEntry) string {
	var b strings.Builder
	fmt.Fprintln(&b, miseBlockStart)
	switch mode {
	case modeOverlay:
		fmt.Fprintln(&b, "# Directory-scoped account isolation (kae pin, mode: overlay): auth and")
		fmt.Fprintln(&b, "# session state are private to this directory while settings, skills,")
		fmt.Fprintln(&b, "# and memory stay shared with the real home; the global live auth state")
		fmt.Fprintln(&b, "# is never touched. After adding shared items to the real home, re-run")
		fmt.Fprintln(&b, "# kae pin to refresh the links.")
	case constants.ModeBond:
		fmt.Fprintln(&b, "# Directory-scoped bond mode (kae bond): settings, sessions, and memory")
		fmt.Fprintln(&b, "# are shared with the real home; credentials are private to this directory.")
		fmt.Fprintln(&b, "# Re-run kae bond after adding new files to the real home to refresh links.")
	default:
		fmt.Fprintln(&b, "# Directory-scoped isolation (kae pin --mode home): account and config")
		fmt.Fprintln(&b, "# directory switch inside this directory only; the global live auth")
		fmt.Fprintln(&b, "# state is never touched, safe across concurrent terminals.")
	}
	fmt.Fprintln(&b, "[env]")
	fmt.Fprintf(&b, "%s = %q\n", constants.EnvKaeProfile, profileName)
	for _, entry := range entries {
		if entry.Warning != "" {
			fmt.Fprintf(&b, "# warning: %s\n", entry.Warning)
			continue
		}
		fmt.Fprintf(&b, "%s = %q\n", entry.EnvVar, entry.Dir)
	}
	fmt.Fprintln(&b, miseBlockEnd)
	return b.String()
}

// bondDenylistItems returns the items excluded from bond-mode symlink sharing
// for a tool: the hard-coded auth artifacts plus user-configured extras.
// The hard-coded list is per-tool and intentionally minimal; docs/ADAPTERS.md
// "Isolation" is the normative reference — keep them in sync.
func (app *App) bondDenylistItems(tool string) []string {
	var base []string
	switch tool {
	case constants.ToolClaude:
		// .credentials.json is Linux-only (macOS uses keychain), but harmless
		// to include on all platforms: if absent the copy step is a no-op.
		base = []string{".credentials.json"}
	case constants.ToolCodex:
		base = []string{"auth.json"}
	}
	return append(base, app.Config.BondDenylistExtra(tool)...)
}

// bondIsolationEntries resolves the per-tool env entries for bond mode.
// BondDir is account-agnostic (one per pinID×tool), so the account field
// carries the profile's account name for credential-copy bookkeeping only.
func (app *App) bondIsolationEntries(targets []runTarget, pinID string) []isolationEntry {
	entries := make([]isolationEntry, 0, len(targets))
	for _, tgt := range targets {
		entry := isolationEntry{Tool: tgt.Tool, Account: tgt.Account}
		entry.EnvVar = isolationEnvVar(tgt.Tool)
		if entry.EnvVar == "" {
			entry.Warning = fmt.Sprintf(
				"%s has no stable home-isolation env var; it keeps the real home (docs/ROADMAP.md)", tgt.Tool)
			entries = append(entries, entry)
			continue
		}
		entry.Dir = app.Paths.BondDir(pinID, tgt.Tool)
		entries = append(entries, entry)
	}
	return entries
}

// prepareBond creates the bond directory for one tool/pinID: symlinks every
// real-home entry except the hard-coded denylist, then copies the current
// credential privately. Idempotent: stale symlinks are refreshed; real files
// in the bond dir (private overrides) are left untouched.
func (app *App) prepareBond(tool, _ string, pinID string) (string, error) {
	bondDir := app.Paths.BondDir(pinID, tool)
	if err := os.MkdirAll(bondDir, 0o700); err != nil {
		return "", fmt.Errorf("create bond dir: %w", err)
	}
	realHome := app.realToolHome(tool)
	if filepath.Clean(realHome) == filepath.Clean(bondDir) {
		return "", errf(constants.ExitUnsafeRefused,
			"the real %s home resolves to the bond dir itself; unset %s and retry",
			tool, isolationEnvVar(tool))
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
	for _, de := range des {
		name := de.Name()
		if denied[name] {
			continue
		}
		src := filepath.Join(realHome, name)
		dst := filepath.Join(bondDir, name)
		info, statErr := os.Lstat(dst)
		if statErr != nil && !os.IsNotExist(statErr) {
			return "", fmt.Errorf("stat bond item %s: %w", dst, statErr)
		}
		if statErr == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				// Real file/dir in bond dir = private override; leave it.
				continue
			}
			if current, readErr := os.Readlink(dst); readErr == nil && current == src {
				continue // already linked correctly
			}
			if err := os.Remove(dst); err != nil {
				return "", fmt.Errorf("refresh bond link %s: %w", dst, err)
			}
		}
		if err := os.Symlink(src, dst); err != nil {
			return "", fmt.Errorf("link bond item %s: %w", dst, err)
		}
	}

	// Private-copy the current credential for each denylist item.
	for _, item := range denylist {
		src := filepath.Join(realHome, item)
		dst := filepath.Join(bondDir, item)
		data, err := os.ReadFile(src)
		if os.IsNotExist(err) {
			continue // tool not yet logged in; skip
		}
		if err != nil {
			return "", fmt.Errorf("read credential %s: %w", src, err)
		}
		existing, readErr := os.ReadFile(dst)
		if readErr == nil {
			if string(existing) == string(data) {
				continue // already up to date
			}
		} else if !os.IsNotExist(readErr) {
			return "", fmt.Errorf("check existing credential %s: %w", dst, readErr)
		}
		if err := patch.WriteFileAtomic(dst, data, 0o600); err != nil {
			return "", fmt.Errorf("copy credential to bond dir: %w", err)
		}
	}

	return bondDir, nil
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
