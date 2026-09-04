package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
	"github.com/webkaz-labs/kagikae/internal/paths"
)

// completionInstallChoice is where `kae completion <shell> --install` registers
// the completion script.
type completionInstallChoice int

const (
	// installFpath writes the script to the shell's standard completions dir
	// (mise-independent, the default suggestion).
	installFpath completionInstallChoice = iota
	// installMiseHook adds a global mise [hooks.enter] that sources the script
	// (mise-native, opt-in, experimental).
	installMiseHook
	// installPrintOnly prints the script and writes nothing.
	installPrintOnly
)

// runCompletionInstall prompts for a registration target and applies it. The
// choice is read interactively; applyCompletionInstall holds the testable
// file-writing core.
func runCompletionInstall(app *App, opts commonOpts, shell, script string) int {
	choice := promptCompletionChoice(app.Env, shell)
	return applyCompletionInstall(app, opts, shell, script, choice)
}

// promptCompletionChoice asks the user where to register completion. mise is
// detected to order the menu, but the fpath file stays the default either way
// (kae never silently rewrites the user's global mise config). A blank line or
// any unrecognized answer selects the default.
func promptCompletionChoice(env adapter.Env, shell string) completionInstallChoice {
	miseActive := completionMiseDetected(env)
	// An unsupported shell never reaches here (validated in CmdCompletion), so
	// the error is safe to drop for the display path.
	path, _, _ := completionTarget(env, shell)
	fmt.Fprintf(os.Stderr, "Register kae %s completion:\n", shell)
	fmt.Fprintf(os.Stderr, "  1) completion file in the shell's standard dir (%s) [default]\n", path)
	miseNote := ""
	if !miseActive {
		miseNote = " — mise not detected on PATH"
	}
	fmt.Fprintf(os.Stderr, "  2) global mise [hooks.enter] (opt-in, experimental)%s\n", miseNote)
	fmt.Fprintln(os.Stderr, "  3) print the script only")
	fmt.Fprint(os.Stderr, "Choice [1]: ")

	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.TrimSpace(line) {
	case "2":
		return installMiseHook
	case "3":
		return installPrintOnly
	default:
		return installFpath
	}
}

// completionMiseDetected reports whether mise looks active: the activation env
// var is set, or the binary is on PATH.
func completionMiseDetected(env adapter.Env) bool {
	if env.Getenv("MISE_SHELL") != "" || env.Getenv("__MISE_DIFF") != "" {
		return true
	}
	if env.LookPath != nil {
		if _, err := env.LookPath("mise"); err == nil {
			return true
		}
	}
	return false
}

// applyCompletionInstall registers the script per the chosen target. It is the
// testable core (no stdin); the interactive wrapper only selects choice.
func applyCompletionInstall(app *App, opts commonOpts, shell, script string, choice completionInstallChoice) int {
	switch choice {
	case installPrintOnly:
		fmt.Print(script)
		return constants.ExitOK
	case installMiseHook:
		path, changed, err := installMiseGlobalHook(app.Env, shell)
		if err != nil {
			return finish(opts, err)
		}
		if changed {
			fmt.Printf("Registered kae %s completion via global mise hook: %s\n", shell, path)
			fmt.Println("Note: mise hooks are experimental — needs `mise activate`, a trusted")
			fmt.Println("config, and `mise settings experimental=true`. Open a new shell to load it.")
		} else {
			fmt.Printf("kae %s completion already registered in %s\n", shell, path)
		}
		return constants.ExitOK
	case installFpath:
		path, autoLoaded, err := completionTarget(app.Env, shell)
		if err != nil {
			return finish(opts, err)
		}
		changed, err := writeCompletionFile(path, script)
		if err != nil {
			return finish(opts, err)
		}
		if changed {
			fmt.Printf("Installed kae %s completion: %s\n", shell, path)
		} else {
			fmt.Printf("kae %s completion already up to date: %s\n", shell, path)
		}
		fmt.Fprint(os.Stderr, completionActivationNote(shell, path, autoLoaded))
		return constants.ExitOK
	default:
		return finish(opts, errf(constants.ExitError, "unhandled completion install choice %d", choice))
	}
}

// zshCompdumpRebuild is the command that rebuilds zsh's cached completion index
// after a completion file changes (compinit -C reuses a stale dump otherwise).
// Shared by the post-install note and the post-refresh hint so the two never
// drift; the ${ZSH_COMPDUMP:-…} fallback covers a relocated dump.
const zshCompdumpRebuild = `  rm -f "${ZSH_COMPDUMP:-$HOME/.zcompdump}" && autoload -Uz compinit && compinit`

// runCompletionRefresh rewrites every already-registered kae completion file
// from the current binary, without prompting and without creating a new
// registration. It is what the build/install path calls so a structural
// completion change (a new subcommand case or __complete kind) reaches the
// registered script automatically — rebuilding the binary alone leaves the file
// a stale snapshot. The mise-hook registration self-sources from the binary, so
// its script body needs no refresh; the one exception is migrating the exact
// legacy spawned-hook block that kae generated before current-shell hooks were
// used.
func runCompletionRefresh(app *App, opts commonOpts) int {
	var anyRegistered, zshChanged bool
	misePath, miseShell, miseRegistered, miseChanged, err := refreshLegacyMiseGlobalHook(app.Env)
	if err != nil {
		return finish(opts, err)
	}
	anyRegistered = miseRegistered
	if miseChanged {
		fmt.Printf("Refreshed kae %s completion mise hook: %s\n", miseShell, misePath)
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		path, _, err := completionTarget(app.Env, shell)
		if err != nil {
			return finish(opts, err)
		}
		if _, statErr := os.Stat(path); statErr != nil {
			continue // not registered for this shell; never create one here
		}
		anyRegistered = true
		script, _ := completionScript(shell) // shell is one of the literals above; ok is always true
		changed, werr := writeCompletionFile(path, script)
		if werr != nil {
			return finish(opts, werr)
		}
		if changed {
			fmt.Printf("Refreshed kae %s completion: %s\n", shell, path)
			if shell == "zsh" {
				zshChanged = true
			}
		}
	}
	if !anyRegistered {
		fmt.Println("No registered kae completion to refresh (run: kae completion <bash|zsh|fish> --install).")
		return constants.ExitOK
	}
	// A normal compinit picks up the rewritten file by mtime; compinit -C
	// (speed-tuned setups) reuses a cached compdump, so hand over the rebuild for
	// the running/next shell rather than mutating the user's cache from here.
	if zshChanged {
		fmt.Fprintln(os.Stderr, "zsh: if completion does not update, rebuild the cache:")
		fmt.Fprintln(os.Stderr, zshCompdumpRebuild)
	}
	return constants.ExitOK
}

// completionTarget resolves the user completions file for kae and whether the
// shell auto-loads that location (so no fpath/.zshrc step is needed). bash and
// fish auto-load their standard XDG dirs. zsh loads only dirs on `fpath`, which
// varies per user, so completionTarget prefers an existing user completions dir
// (one the user created because it is on their fpath — see zshCompletionDir);
// only when none exists does it fall back to the XDG dir, which the user must
// add to fpath (completionActivationNote says so).
func completionTarget(env adapter.Env, shell string) (path string, autoLoaded bool, err error) {
	switch shell {
	case "bash":
		return filepath.Join(paths.XDGDataHome(env.Getenv, env.Home, ""), "bash-completion", "completions", "kae"), true, nil
	case "zsh":
		dir, onFpath := zshCompletionDir(env)
		return filepath.Join(dir, "_kae"), onFpath, nil
	case "fish":
		return filepath.Join(paths.XDGConfigHome(env.Getenv, env.Home, ""), "fish", "completions", "kae.fish"), true, nil
	default:
		return "", false, errf(constants.ExitUsage, "unsupported shell %q (supported: bash, zsh, fish)", shell)
	}
}

// zshCompletionDir picks the zsh completions dir to install into. It prefers the
// first **existing** common user fpath dir — a dir the user created precisely
// because it is on their `fpath`, so the file loads in a new shell with no
// .zshrc change (onFpath=true). When none exists it returns the XDG data dir
// (onFpath=false), which the install step creates and asks the user to add to
// fpath. (kae does not shell out to zsh to read `$fpath`: an interactive zsh
// subprocess is slow and its stdout is easily polluted by the user's rc files.)
func zshCompletionDir(env adapter.Env) (dir string, onFpath bool) {
	for _, candidate := range []string{
		filepath.Join(paths.XDGConfigHome(env.Getenv, env.Home, ""), "zsh", "completions"),
		filepath.Join(env.Home, ".zsh", "completions"),
		filepath.Join(env.Home, ".zfunc"),
	} {
		if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
			return candidate, true
		}
	}
	return filepath.Join(paths.XDGDataHome(env.Getenv, env.Home, ""), "zsh", "site-functions"), false
}

// completionActivationNote returns the note printed after an fpath install.
// bash/fish auto-load their dirs, so a new shell suffices. zsh needs the dir on
// fpath AND a fresh compinit: when the dir is already on fpath we still warn that
// a cached compdump can hide a newly added function (a common cause of "I
// installed it but completion does not appear"); when it is not, we name the
// fpath line to add (which re-runs compinit too).
func completionActivationNote(shell, path string, autoLoaded bool) string {
	if shell == "zsh" {
		if autoLoaded {
			return "Open a new shell. If completion does not appear, your zsh completion\n" +
				"cache is stale — remove your compdump and rebuild it:\n" +
				zshCompdumpRebuild + "\n"
		}
		return fmt.Sprintf("Ensure this is on your fpath, e.g. add to ~/.zshrc:\n"+
			"  fpath=(%s $fpath)\n  autoload -Uz compinit && compinit\nThen open a new shell.\n",
			filepath.Dir(path))
	}
	return "Open a new shell to load it.\n"
}

// writeCompletionFile writes script to path idempotently, creating parent
// directories. changed is false when the file already holds the same script.
func writeCompletionFile(path, script string) (bool, error) {
	existing, err := os.ReadFile(path)
	switch {
	case err == nil && string(existing) == script:
		return false, nil
	case err != nil && !os.IsNotExist(err):
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := patch.WriteFileAtomic(path, []byte(script), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// installMiseGlobalHook adds (or refreshes) the kagikae marker block carrying a
// [hooks.enter] that sources kae's completion into the global mise config. It
// reuses the miseinit.go marker constants so the block is replaced in place on
// re-run (idempotent). A config that already defines [hooks.enter] outside our
// block is refused (TOML forbids a duplicate table) with manual guidance — kae
// never clobbers a hook it does not own.
func installMiseGlobalHook(env adapter.Env, shell string) (path string, changed bool, err error) {
	path = globalMiseConfigPath(env)
	writePath, info, exists, resolveErr := resolveMiseConfigTarget(path)
	if resolveErr != nil {
		return path, false, resolveErr
	}
	block := miseHookBlock(shell)
	if !exists {
		if mkErr := os.MkdirAll(filepath.Dir(writePath), 0o755); mkErr != nil {
			return path, false, mkErr
		}
		return path, true, patch.WriteFileAtomic(writePath, []byte(block), 0o644)
	}
	data, readErr := os.ReadFile(writePath)
	if readErr != nil {
		return path, false, readErr
	}
	mode := info.Mode().Perm()
	content := string(data)
	if before, after, ok := cutMiseBlock(content); ok {
		if !miseTableEndsBeforeContent(after) {
			return path, false, errf(constants.ExitUnsafeRefused,
				"%s has settings after the kagikae marker that still belong to [hooks.enter]; move them inside the marker or start a new TOML table before reinstalling completion",
				path)
		}
		updated := before + block + after
		if updated == content {
			return path, false, nil
		}
		return path, true, patch.WriteFileAtomic(writePath, []byte(updated), mode)
	}
	if strings.Contains(content, "[hooks.enter]") {
		return path, false, errf(constants.ExitUnsafeRefused,
			"%s already defines [hooks.enter]; merge the kae completion hook manually:\n  shell = %q\n  script = %q",
			path, shell, miseHookScriptLine(shell))
	}
	sep := ""
	if content != "" && !strings.HasSuffix(content, "\n") {
		sep = "\n"
	}
	return path, true, patch.WriteFileAtomic(writePath, []byte(content+sep+block), mode)
}

// resolveMiseConfigTarget fixes the path that may be atomically replaced. An
// atomic rename against a symlink path replaces the link itself, so an existing
// link is fully resolved first and only its regular-file target may be written.
// Missing ordinary paths are returned for creation; dangling links and
// non-regular files are refused rather than mistaken for a missing config.
func resolveMiseConfigTarget(path string) (writePath string, info os.FileInfo, exists bool, err error) {
	linkInfo, lstatErr := os.Lstat(path)
	if os.IsNotExist(lstatErr) {
		return path, nil, false, nil
	}
	if lstatErr != nil {
		return "", nil, false, lstatErr
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		if !linkInfo.Mode().IsRegular() {
			return "", nil, false, errf(constants.ExitUnsafeRefused,
				"%s is not a regular file; refusing to replace it", path)
		}
		return path, linkInfo, true, nil
	}
	resolved, evalErr := filepath.EvalSymlinks(path)
	if evalErr != nil {
		return "", nil, false, errf(constants.ExitUnsafeRefused,
			"%s is a dangling or unresolvable symlink; refusing to replace it", path)
	}
	resolved, absErr := filepath.Abs(resolved)
	if absErr != nil {
		return "", nil, false, absErr
	}
	targetInfo, statErr := os.Stat(resolved)
	if statErr != nil {
		return "", nil, false, errf(constants.ExitUnsafeRefused,
			"%s does not resolve to a readable regular file; refusing to replace the symlink", path)
	}
	if !targetInfo.Mode().IsRegular() {
		return "", nil, false, errf(constants.ExitUnsafeRefused,
			"%s resolves to a non-regular file; refusing to replace the symlink", path)
	}
	return resolved, targetInfo, true, nil
}

// globalMiseConfigPath resolves the user's global mise config file
// ($MISE_CONFIG_DIR/config.toml, else $XDG_CONFIG_HOME/mise/config.toml, else
// ~/.config/mise/config.toml).
func globalMiseConfigPath(env adapter.Env) string {
	if dir := env.Getenv("MISE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "config.toml")
	}
	return filepath.Join(paths.XDGConfigHome(env.Getenv, env.Home, "mise"), "config.toml")
}

// miseHookBlock renders the kagikae marker block with a [hooks.enter] sourcing
// kae's completion for the shell. Ends with a newline so it composes cleanly.
func miseHookBlock(shell string) string {
	return renderMiseHookBlock(shell, miseHookScriptLine(shell), true)
}

// legacyMiseHookBlock is the exact block emitted from v0.8.4 until kae moved
// completion registration to mise's current-shell hook form. Keep it only for
// the bounded completion --refresh migration; never use it for new installs.
func legacyMiseHookBlock(shell string) string {
	return renderMiseHookBlock(shell, legacyMiseHookScriptLine(shell), false)
}

func renderMiseHookBlock(shell, script string, currentShell bool) string {
	var b strings.Builder
	fmt.Fprintln(&b, miseBlockStart)
	fmt.Fprintln(&b, "# kae shell completion via mise (opt-in, experimental). Needs `mise activate`,")
	fmt.Fprintln(&b, "# a trusted config, and `mise settings experimental=true`. Fires on directory")
	fmt.Fprintln(&b, "# entry. Non-mise users register via the fpath file or")
	fmt.Fprintln(&b, `# eval "$(kae completion <shell>)" (docs/CLI.md).`)
	fmt.Fprintln(&b, "[hooks.enter]")
	if currentShell {
		fmt.Fprintf(&b, "shell = %q\n", shell)
	}
	fmt.Fprintf(&b, "script = %q\n", script)
	fmt.Fprintln(&b, miseBlockEnd)
	return b.String()
}

// miseHookScriptLine is the shell-appropriate command that loads kae's
// completion into the current shell (bash/zsh eval the generated script; fish
// pipes it to source).
func miseHookScriptLine(shell string) string {
	if shell == "fish" {
		return "kae completion fish | source"
	}
	return `eval "$(kae completion ` + shell + `)"`
}

func legacyMiseHookScriptLine(shell string) string {
	if shell == "fish" {
		return "kae completion fish | source"
	}
	return "source <(kae completion " + shell + ")"
}

// refreshLegacyMiseGlobalHook migrates only a byte-for-byte legacy marker
// block generated by kae. A foreign or manually edited hook is left untouched;
// a current block is recognized as an existing registration and is a no-op.
func refreshLegacyMiseGlobalHook(env adapter.Env) (path, shell string, registered, changed bool, err error) {
	path = globalMiseConfigPath(env)
	writePath, info, exists, resolveErr := resolveMiseConfigTarget(path)
	if resolveErr != nil {
		return path, "", false, false, resolveErr
	}
	if !exists {
		return path, "", false, false, nil
	}
	data, readErr := os.ReadFile(writePath)
	if readErr != nil {
		return path, "", false, false, readErr
	}
	content := string(data)
	for _, candidate := range []string{"bash", "zsh", "fish"} {
		current := miseHookBlock(candidate)
		if strings.Contains(content, current) && miseEnterHookMatches(content, candidate, true) {
			return path, candidate, true, false, nil
		}
		legacy := legacyMiseHookBlock(candidate)
		if legacyStart := strings.Index(content, legacy); legacyStart >= 0 {
			afterLegacy := content[legacyStart+len(legacy):]
			if !miseTableEndsBeforeContent(afterLegacy) || !miseEnterHookMatches(content, candidate, false) {
				return path, "", false, false, nil
			}
			updated := content[:legacyStart] + current + afterLegacy
			var parsed map[string]any
			if _, parseErr := toml.Decode(updated, &parsed); parseErr != nil {
				return path, candidate, true, false, fmt.Errorf("validate migrated mise config: %w", parseErr)
			}
			if writeErr := patch.WriteFileAtomic(writePath, []byte(updated), info.Mode().Perm()); writeErr != nil {
				return path, candidate, true, false, writeErr
			}
			return path, candidate, true, true, nil
		}
	}
	return path, "", false, false, nil
}

// miseEnterHookMatches verifies the decoded [hooks.enter] table as well as the
// marker bytes. This catches keys that continue after the marker and any other
// table extension that a substring match cannot see.
func miseEnterHookMatches(content, shell string, current bool) bool {
	var parsed map[string]any
	if _, err := toml.Decode(content, &parsed); err != nil {
		return false
	}
	hooks, ok := parsed["hooks"].(map[string]any)
	if !ok {
		return false
	}
	enter, ok := hooks["enter"].(map[string]any)
	if !ok {
		return false
	}
	expectedScript := legacyMiseHookScriptLine(shell)
	if current {
		expectedScript = miseHookScriptLine(shell)
	}
	if script, ok := enter["script"].(string); !ok || script != expectedScript {
		return false
	}
	if !current {
		return len(enter) == 1
	}
	selectedShell, ok := enter["shell"].(string)
	return ok && selectedShell == shell && len(enter) == 2
}

// miseTableEndsBeforeContent reports whether the table opened inside a marker
// block is followed only by blank/comment lines before EOF or the next TOML
// table header. Any key after the marker still belongs to [hooks.enter], so it
// makes replacing the block unsafe (the replacement could introduce a duplicate
// shell or script key).
func miseTableEndsBeforeContent(after string) bool {
	for _, line := range strings.Split(after, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "", strings.HasPrefix(trimmed, "#"):
			continue
		case strings.HasPrefix(trimmed, "["):
			return true
		default:
			return false
		}
	}
	return true
}
