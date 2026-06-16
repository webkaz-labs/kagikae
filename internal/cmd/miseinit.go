package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
	"github.com/webkaz-labs/kagikae/internal/paths"
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
// modified. --auto adds a [hooks.enter] entry running `kae use --quiet`. The
// former isolation modes (home/overlay/bond/pin) are gone: bind a directory
// with `kae pin -s|-i`, which owns its own mise fragment (docs/RELEASE.md).
func CmdMise(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] != "init" {
		return usageError("usage: %s mise init [--profile NAME] [--auto] [--write]", toolName)
	}
	flags, positionals := splitArgs(args[1:], "--profile", "P", "--mode")
	var profileName, mode string
	write, auto := false, false
	opts, ok := parseCommon("mise init", flags, false, func(fs *flag.FlagSet) {
		registerProfileFlag(fs, &profileName)
		// --mode is still parsed so an old `--mode bond|pin|home|overlay`
		// invocation gets a clear rejection rather than "flag not defined".
		fs.StringVar(&mode, "mode", constants.ModeAuth, "rendered integration (auth only; bind directories with kae pin)")
		fs.BoolVar(&auto, "auto", false, "add a [hooks.enter] running `kae use --quiet`")
		fs.BoolVar(&write, "write", false, "write/update .mise.toml in the current directory")
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
			"kae mise init renders auth mode only (mode %q is no longer supported); bind a directory with `kae pin -s|-i`", mode)
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
		fmt.Fprintln(os.Stderr, "\nkae: preview only; apply with: "+hint+" --write")
		return constants.ExitOK
	}
	if err := writeMiseBlock(".mise.toml", block); err != nil {
		return finish(opts, err)
	}
	fmt.Printf("Updated .mise.toml: profile %s (auth mode)\n", profileName)
	fmt.Println("Next: mise trust   (mise refuses untrusted configs; its error until then is expected)")
	return constants.ExitOK
}

// isolationPlan resolves the per-tool env entries and the matching directory
// preparer for a per-directory bind (shared/isolated). Used by `kae pin`, which
// renders the kae-owned mise fragment; both mechanisms key their stores by the
// bound directory, so it resolves pin-id here.
func (app *App) isolationPlan(ctx context.Context, mode string, targets []runTarget) ([]isolationEntry, func(tool, account string) (string, error), error) {
	absDir, err := cwdAbs()
	if err != nil {
		return nil, nil, err
	}
	pinID := paths.PinID(absDir)
	switch mode {
	case modeShared:
		return app.bondIsolationEntries(targets, pinID),
			func(tool, account string) (string, error) { return app.prepareBond(ctx, tool, account, pinID) }, nil
	case modeIsolated:
		return app.pinIsolationEntries(targets, pinID),
			func(tool, account string) (string, error) { return app.preparePinConfig(ctx, tool, account, pinID) }, nil
	default:
		return nil, nil, errf(constants.ExitError, "unknown per-directory bind kind %q", mode)
	}
}

// prepareIsolationDirs runs the preparer for every non-warning entry, so a
// failure surfaces before kae writes a fragment or block pointing at a
// directory that does not exist.
func (app *App) prepareIsolationDirs(mode string, entries []isolationEntry, prepare func(tool, account string) (string, error)) error {
	for _, entry := range entries {
		if entry.Warning != "" {
			continue
		}
		if _, err := prepare(entry.Tool, entry.Account); err != nil {
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
		fmt.Fprintln(&b, `script = "kae use --quiet"`)
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

// isolationEntry is one tool's resolved row of a per-directory bind: either an
// env entry pointing at Dir, or a warning comment explaining why the tool keeps
// its real home (no stable home-isolation env var).
type isolationEntry struct {
	Tool    string
	Account string
	EnvVar  string
	Dir     string
	Warning string // non-empty: rendered as a comment, no env entry
}

// isolationEntryFor builds one bind entry for a tool: the env entry pointing at
// dir when the tool has a home-isolation env var, or a warning entry (dir
// ignored) when it does not. Shared by the shared (SharedDir) and isolated
// (IsolatedConfigDir) bind planners, which differ only in how dir is computed.
func isolationEntryFor(tgt runTarget, dir string) isolationEntry {
	entry := isolationEntry{Tool: tgt.Tool, Account: tgt.Account, EnvVar: isolationEnvVar(tgt.Tool)}
	if entry.EnvVar == "" {
		entry.Warning = fmt.Sprintf(
			"%s has no stable home-isolation env var; it keeps the real home (docs/ROADMAP.md)", tgt.Tool)
		return entry
	}
	entry.Dir = dir
	return entry
}

// writeEnvEntries renders the shared [env] block — KAE_PROFILE plus each tool's
// isolation env entry, or a warning comment for a tool that keeps the real home
// — for the kae pin fragment (renderDirFragment). One place to change env-line
// formatting.
func writeEnvEntries(b *strings.Builder, profileName string, entries []isolationEntry) {
	fmt.Fprintln(b, "[env]")
	fmt.Fprintf(b, "%s = %q\n", constants.EnvKaeProfile, profileName)
	for _, entry := range entries {
		if entry.Warning != "" {
			fmt.Fprintf(b, "# warning: %s\n", entry.Warning)
			continue
		}
		fmt.Fprintf(b, "%s = %q\n", entry.EnvVar, entry.Dir)
	}
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
	return append(base, app.Config.SharedDenylistExtra(tool)...)
}

// bondIsolationEntries resolves the per-tool env entries for bond mode.
// SharedDir is account-agnostic (one per pinID×tool), so the account field
// carries the profile's account name for credential-copy bookkeeping only.
func (app *App) bondIsolationEntries(targets []runTarget, pinID string) []isolationEntry {
	entries := make([]isolationEntry, 0, len(targets))
	for _, tgt := range targets {
		entries = append(entries, isolationEntryFor(tgt, app.Paths.SharedDir(pinID, tgt.Tool)))
	}
	return entries
}

// prepareBond creates the bond directory for one tool/pinID: symlinks every
// real-home entry except the hard-coded denylist, then copies the current
// credential privately. Idempotent: stale symlinks are refreshed; real files
// in the bond dir (private overrides) are left untouched.
func (app *App) prepareBond(ctx context.Context, tool, _ string, pinID string) (string, error) {
	bondDir := app.Paths.SharedDir(pinID, tool)
	if err := os.MkdirAll(bondDir, 0o700); err != nil {
		return "", fmt.Errorf("create shared dir: %w", err)
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
			// Source file absent: on macOS, tools that store their credential
			// in the OS keychain (e.g. claude) have no file to copy.
			// Read the keychain payload verbatim so that CLAUDE_CONFIG_DIR
			// isolation (which forces file-based auth even on macOS) can find
			// a .credentials.json in the bond dir.
			kdata, kerr := app.keychainCredForBond(ctx, tool)
			if kerr != nil {
				return "", kerr
			}
			if kdata == nil {
				continue // not logged in; skip silently
			}
			data = kdata
		} else if err != nil {
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

// pinIsolationEntries resolves the per-tool env entries for pin mode.
// IsolatedConfigDir is per-account
// (isolation/<pinID>/<tool>/isolated/<account>/config/), so each target
// carries the account name for directory construction.
func (app *App) pinIsolationEntries(targets []runTarget, pinID string) []isolationEntry {
	entries := make([]isolationEntry, 0, len(targets))
	for _, tgt := range targets {
		entries = append(entries, isolationEntryFor(tgt, app.Paths.IsolatedConfigDir(pinID, tgt.Tool, tgt.Account)))
	}
	return entries
}

// preparePinConfig creates the pin config directory for one tool/account/pinID:
// symlinks opt-in shared items from the real home, then copies the credential
// privately. Idempotent: stale symlinks are refreshed; real files are left.
func (app *App) preparePinConfig(ctx context.Context, tool, account, pinID string) (string, error) {
	configDir := app.Paths.IsolatedConfigDir(pinID, tool, account)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return "", fmt.Errorf("create isolated config dir: %w", err)
	}
	realHome := app.realToolHome(tool)
	if filepath.Clean(realHome) == filepath.Clean(configDir) {
		return "", errf(constants.ExitUnsafeRefused,
			"the real %s home resolves to the pin config dir itself; unset %s and retry",
			tool, isolationEnvVar(tool))
	}

	// Symlink opt-in shared items from the real home.
	for _, item := range app.Config.IsolatedSharedItems(tool) {
		src := filepath.Join(realHome, item)
		if _, err := os.Stat(src); err != nil {
			continue // only link what exists
		}
		dst := filepath.Join(configDir, item)
		info, statErr := os.Lstat(dst)
		if statErr != nil && !os.IsNotExist(statErr) {
			return "", fmt.Errorf("stat pin item %s: %w", dst, statErr)
		}
		if statErr == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				continue // real file/dir in pin dir is a private override; leave it
			}
			if current, readErr := os.Readlink(dst); readErr == nil && current == src {
				continue // already linked correctly
			}
			if err := os.Remove(dst); err != nil {
				return "", fmt.Errorf("refresh pin link %s: %w", dst, err)
			}
		}
		if err := os.Symlink(src, dst); err != nil {
			return "", fmt.Errorf("link pin item %s: %w", dst, err)
		}
	}

	// Private-copy the credential — same logic as prepareBond.
	for _, item := range app.pinCredItems(tool) {
		src := filepath.Join(realHome, item)
		dst := filepath.Join(configDir, item)
		data, err := os.ReadFile(src)
		if os.IsNotExist(err) {
			kdata, kerr := app.keychainCredForBond(ctx, tool)
			if kerr != nil {
				return "", kerr
			}
			if kdata == nil {
				continue
			}
			data = kdata
		} else if err != nil {
			return "", fmt.Errorf("read credential %s: %w", src, err)
		}
		existing, readErr := os.ReadFile(dst)
		if readErr == nil && string(existing) == string(data) {
			continue
		} else if readErr != nil && !os.IsNotExist(readErr) {
			return "", fmt.Errorf("check existing credential %s: %w", dst, readErr)
		}
		if err := patch.WriteFileAtomic(dst, data, 0o600); err != nil {
			return "", fmt.Errorf("copy credential to pin dir: %w", err)
		}
	}

	return configDir, nil
}

// pinCredItems returns the credential file names to private-copy into a pin
// config dir. Mirrors bondDenylistItems but for pin mode.
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

// keychainCredForBond returns the raw keychain payload for a tool's primary
// keychain artifact (verbatim bytes), suitable for writing as the tool's
// credential file in the bond dir. Returns (nil, nil) when the tool has no
// keychain artifact, is not on macOS, or the keychain item is absent (not
// logged in). Returns a non-nil error when the keychain read fails (ACL,
// security CLI error, or keychainGuard shape mismatch).
//
// Claude Code stores its credential in the macOS Keychain as compact JSON
// (`{"claudeAiOauth":{...}}`), which is byte-for-byte the content it expects
// in .credentials.json when CLAUDE_CONFIG_DIR is set. The verbatim round-trip
// is intentional: re-serializing the payload would make Claude Code reject it.
func (app *App) keychainCredForBond(ctx context.Context, tool string) ([]byte, error) {
	if app.Env.GOOS != "darwin" {
		return nil, nil
	}
	adp, err := adapter.ForTool(tool)
	if err != nil {
		return nil, nil // tool has no adapter or no keychain on this platform
	}
	specs, err := adp.Artifacts(ctx, app.Env)
	if err != nil {
		return nil, nil // unsupported platform/tool combination
	}
	for _, sp := range specs {
		if sp.Kind != constants.KindKeychain {
			continue
		}
		v, err := artifact.ReadLive(ctx, sp)
		if err != nil {
			return nil, fmt.Errorf("read keychain credential for %s: %w", tool, err)
		}
		if !v.Present {
			continue
		}
		return v.Data, nil
	}
	return nil, nil
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
