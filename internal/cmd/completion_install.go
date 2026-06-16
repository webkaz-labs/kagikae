package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	path, _ := completionInstallPath(env, shell)
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
		path, err := completionInstallPath(app.Env, shell)
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
		fmt.Fprint(os.Stderr, completionActivationNote(shell, path))
		return constants.ExitOK
	default:
		return finish(opts, errf(constants.ExitError, "unhandled completion install choice %d", choice))
	}
}

// completionInstallPath returns the shell's standard user completions file for
// kae (XDG-aware). bash-completion v2 and fish auto-load their dirs; zsh needs
// the dir on fpath (completionActivationNote says so).
func completionInstallPath(env adapter.Env, shell string) (string, error) {
	switch shell {
	case "bash":
		return filepath.Join(paths.XDGDataHome(env.Getenv, env.Home, ""), "bash-completion", "completions", "kae"), nil
	case "zsh":
		return filepath.Join(paths.XDGDataHome(env.Getenv, env.Home, ""), "zsh", "site-functions", "_kae"), nil
	case "fish":
		return filepath.Join(paths.XDGConfigHome(env.Getenv, env.Home, ""), "fish", "completions", "kae.fish"), nil
	default:
		return "", errf(constants.ExitUsage, "unsupported shell %q (supported: bash, zsh, fish)", shell)
	}
}

// completionActivationNote returns the shell-specific note printed after a
// successful fpath install (zsh needs the dir on fpath; others auto-load).
func completionActivationNote(shell, path string) string {
	switch shell {
	case "zsh":
		dir := filepath.Dir(path)
		return fmt.Sprintf("Ensure this is on your fpath, e.g. add to ~/.zshrc:\n"+
			"  fpath=(%s $fpath)\n  autoload -Uz compinit && compinit\nThen open a new shell.\n", dir)
	default:
		return "Open a new shell to load it.\n"
	}
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
	block := miseHookBlock(shell)
	data, readErr := os.ReadFile(path)
	if os.IsNotExist(readErr) {
		if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
			return path, false, mkErr
		}
		return path, true, patch.WriteFileAtomic(path, []byte(block), 0o644)
	}
	if readErr != nil {
		return path, false, readErr
	}
	content := string(data)
	if before, after, ok := cutMiseBlock(content); ok {
		updated := before + block + after
		if updated == content {
			return path, false, nil
		}
		return path, true, patch.WriteFileAtomic(path, []byte(updated), 0o644)
	}
	if strings.Contains(content, "[hooks.enter]") {
		return path, false, errf(constants.ExitUnsafeRefused,
			"%s already defines [hooks.enter]; add the kae completion line to it manually:\n  script = %q",
			path, miseHookScriptLine(shell))
	}
	sep := ""
	if content != "" && !strings.HasSuffix(content, "\n") {
		sep = "\n"
	}
	return path, true, patch.WriteFileAtomic(path, []byte(content+sep+block), 0o644)
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
	var b strings.Builder
	fmt.Fprintln(&b, miseBlockStart)
	fmt.Fprintln(&b, "# kae shell completion via mise (opt-in, experimental). Needs `mise activate`,")
	fmt.Fprintln(&b, "# a trusted config, and `mise settings experimental=true`. Fires on directory")
	fmt.Fprintln(&b, "# entry. Non-mise users register via the fpath file or")
	fmt.Fprintln(&b, `# eval "$(kae completion <shell>)" (docs/CLI.md).`)
	fmt.Fprintln(&b, "[hooks.enter]")
	fmt.Fprintf(&b, "script = %q\n", miseHookScriptLine(shell))
	fmt.Fprintln(&b, miseBlockEnd)
	return b.String()
}

// miseHookScriptLine is the shell-appropriate command that loads kae's
// completion into the current shell (bash/zsh eval a process substitution;
// fish pipes to source).
func miseHookScriptLine(shell string) string {
	if shell == "fish" {
		return "kae completion fish | source"
	}
	return "source <(kae completion " + shell + ")"
}
