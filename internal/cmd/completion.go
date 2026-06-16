package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// completionCommands is the first-word candidate set for shell completion: the
// public commands routed by Root() (aliases omitted to keep the list tidy).
// Keep in lockstep with Root().
var completionCommands = []string{
	"init", "edit", "doctor", "add", "use", "pin", "unpin", "run", "env",
	"mise", "accounts", "account", "profile", "status", "backup", "rollback",
	"completion", "version", "help",
}

// CmdCompletion emits a shell completion script:
//
//	kae completion <bash|zsh|fish>
//
// The candidate lists are table-driven — commands from Root(), tools from
// constants.Tools, and profile names from the loaded config — so the script
// tracks the surface without hand-maintained duplication.
func CmdCompletion(_ context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("completion", flags, false, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 1 {
		return usageError("usage: %s completion <bash|zsh|fish>", toolName)
	}
	shell := positionals[0]
	app := newApp(opts.ConfigPath)
	// A config error is non-fatal here: completion still works with an empty
	// profile list, so fall back to defaults rather than refusing.
	profiles := app.Config.ProfileNames()

	commands := strings.Join(completionCommands, " ")
	tools := strings.Join(constants.Tools, " ")
	profileWords := strings.Join(profiles, " ")
	// The second-position candidates are tools or profiles (both appear in the
	// <tool|profile> arg of use/pin/run).
	args2 := strings.TrimSpace(tools + " " + profileWords)

	switch shell {
	case "bash":
		fmt.Print(bashCompletion(commands, args2))
	case "zsh":
		fmt.Print(zshCompletion(commands, args2))
	case "fish":
		fmt.Print(fishCompletion(commands, args2))
	default:
		return usageError("unsupported shell %q (supported: bash, zsh, fish)", shell)
	}
	return constants.ExitOK
}

func bashCompletion(commands, args2 string) string {
	return fmt.Sprintf(`# kae bash completion — eval "$(kae completion bash)"
_kae() {
  local cur cmds args2
  cur="${COMP_WORDS[COMP_CWORD]}"
  cmds=%q
  args2=%q
  if [ "$COMP_CWORD" -eq 1 ]; then
    COMPREPLY=( $(compgen -W "$cmds" -- "$cur") )
  else
    COMPREPLY=( $(compgen -W "$args2" -- "$cur") )
  fi
}
complete -F _kae kae
`, commands, args2)
}

func zshCompletion(commands, args2 string) string {
	return fmt.Sprintf(`#compdef kae
# kae zsh completion — eval "$(kae completion zsh)"
_kae() {
  local -a cmds args2
  cmds=(%s)
  args2=(%s)
  if (( CURRENT == 2 )); then
    compadd -- $cmds
  else
    compadd -- $args2
  fi
}
compdef _kae kae
`, commands, args2)
}

func fishCompletion(commands, args2 string) string {
	return fmt.Sprintf(`# kae fish completion — kae completion fish | source
complete -c kae -f
complete -c kae -n __fish_use_subcommand -a %q
complete -c kae -n 'not __fish_use_subcommand' -a %q
`, commands, args2)
}
