package cmd

import (
	"context"
	"flag"
	"fmt"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// completionCommands is the first-word candidate set for shell completion: the
// public commands routed by Root() (aliases and the hidden __complete backend
// omitted to keep the list tidy). Surfaced through `kae __complete commands`.
// Keep in lockstep with Root().
var completionCommands = []string{
	"init", "edit", "doctor", "add", "use", "pin", "unpin", "run", "env",
	"mise", "accounts", "ls", "account", "profile", "status", "backup",
	"rollback", "completion", "version", "help",
}

// completionCommandAliases are the one-letter command aliases Root() routes
// (u=use, p=pin, s=status, d=doctor, r=run). They are kept out of
// completionCommands (which feeds `kae __complete commands` and stays a tidy
// public list) but ARE in the did-you-mean match set, so a near miss of an
// alias is still caught. Keep in lockstep with Root().
var completionCommandAliases = []string{"u", "p", "s", "d", "r"}

// commandCandidates is the did-you-mean match set for an unknown first word:
// the public commands plus their one-letter aliases. Built from the same
// completionCommands list `kae __complete commands` returns, so suggestions
// never drift from the real router.
func commandCandidates() []string {
	candidates := make([]string, 0, len(completionCommands)+len(completionCommandAliases))
	candidates = append(candidates, completionCommands...)
	candidates = append(candidates, completionCommandAliases...)
	return candidates
}

// CmdCompletion emits a shell completion script and optionally installs it:
//
//	kae completion <bash|zsh|fish> [--install]
//
// The emitted script is dynamic — it calls `kae __complete` (complete.go) at
// completion time rather than baking a static word list, so candidates always
// track the live router/config/state. With --install, the script is registered
// interactively (completion_install.go): the shell's standard completions dir
// (default), a global mise [hooks.enter] (opt-in), or print-only.
func CmdCompletion(_ context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	var install bool
	opts, ok := parseCommon("completion", flags, false, func(fs *flag.FlagSet) {
		fs.BoolVar(&install, "install", false, "register the completion script interactively")
	})
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 1 {
		return usageError("usage: %s completion <bash|zsh|fish> [--install]", toolName)
	}
	shell := positionals[0]
	script, ok := completionScript(shell)
	if !ok {
		return usageError("unsupported shell %q (supported: bash, zsh, fish)", shell)
	}
	if !install {
		fmt.Print(script)
		return constants.ExitOK
	}
	app := newApp(opts.ConfigPath)
	return runCompletionInstall(app, opts, shell, script)
}

// completionScript returns the dynamic completion script for a shell; ok is
// false for an unsupported shell.
func completionScript(shell string) (string, bool) {
	switch shell {
	case "bash":
		return bashCompletionScript, true
	case "zsh":
		return zshCompletionScript, true
	case "fish":
		return fishCompletionScript, true
	default:
		return "", false
	}
}

// The generated scripts route by word position to a `kae __complete` kind:
// word 1 → commands; the argument positions → tools/profiles/accounts. Account
// completion passes the preceding tool word so `kae use claude <TAB>` scopes to
// claude's accounts. The live lists (commands/tools/profiles/accounts) come
// from the backend; the small, rarely-changing sub-verb sets (e.g. account
// rm/rename, the shells for completion) are inlined here since they are not
// part of the `__complete` kind contract.

const bashCompletionScript = `# kae bash completion — eval "$(kae completion bash)"
# Dynamic: candidates come from ` + "`kae __complete`" + `, so they track live state.
_kae() {
  local cur cmd
  cur="${COMP_WORDS[COMP_CWORD]}"
  if [ "$COMP_CWORD" -eq 1 ]; then
    COMPREPLY=( $(compgen -W "$(kae __complete commands)" -- "$cur") )
    return
  fi
  cmd="${COMP_WORDS[1]}"
  case "$cmd" in
    use|u|pin|p|run|r)
      if [ "$COMP_CWORD" -eq 2 ]; then
        COMPREPLY=( $(compgen -W "$(kae __complete profiles) $(kae __complete tools)" -- "$cur") )
      elif [ "$COMP_CWORD" -eq 3 ]; then
        COMPREPLY=( $(compgen -W "$(kae __complete accounts "${COMP_WORDS[2]}")" -- "$cur") )
      fi
      ;;
    add|doctor|d)
      if [ "$COMP_CWORD" -eq 2 ]; then
        COMPREPLY=( $(compgen -W "$(kae __complete tools)" -- "$cur") )
      elif [ "$COMP_CWORD" -eq 3 ]; then
        COMPREPLY=( $(compgen -W "$(kae __complete accounts "${COMP_WORDS[2]}")" -- "$cur") )
      fi
      ;;
    account)
      if [ "$COMP_CWORD" -eq 2 ]; then
        COMPREPLY=( $(compgen -W "rm rename" -- "$cur") )
      elif [ "$COMP_CWORD" -eq 3 ]; then
        COMPREPLY=( $(compgen -W "$(kae __complete tools)" -- "$cur") )
      elif [ "$COMP_CWORD" -eq 4 ]; then
        COMPREPLY=( $(compgen -W "$(kae __complete accounts "${COMP_WORDS[3]}")" -- "$cur") )
      fi
      ;;
    profile)
      if [ "$COMP_CWORD" -eq 2 ]; then
        COMPREPLY=( $(compgen -W "save set unset rm default" -- "$cur") )
      fi
      ;;
    completion)
      if [ "$COMP_CWORD" -eq 2 ]; then
        COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") )
      fi
      ;;
    mise)
      if [ "$COMP_CWORD" -eq 2 ]; then
        COMPREPLY=( $(compgen -W "init" -- "$cur") )
      fi
      ;;
  esac
}
complete -F _kae kae
`

const zshCompletionScript = `#compdef kae
# kae zsh completion — eval "$(kae completion zsh)"
# Dynamic: candidates come from ` + "`kae __complete`" + `, so they track live state.
_kae() {
  local cmd
  if (( CURRENT == 2 )); then
    compadd -- ${(f)"$(kae __complete commands)"}
    return
  fi
  cmd="${words[2]}"
  case "$cmd" in
    use|u|pin|p|run|r)
      if (( CURRENT == 3 )); then
        compadd -- ${(f)"$(kae __complete profiles)"} ${(f)"$(kae __complete tools)"}
      elif (( CURRENT == 4 )); then
        compadd -- ${(f)"$(kae __complete accounts ${words[3]})"}
      fi
      ;;
    add|doctor|d)
      if (( CURRENT == 3 )); then
        compadd -- ${(f)"$(kae __complete tools)"}
      elif (( CURRENT == 4 )); then
        compadd -- ${(f)"$(kae __complete accounts ${words[3]})"}
      fi
      ;;
    account)
      if (( CURRENT == 3 )); then
        compadd -- rm rename
      elif (( CURRENT == 4 )); then
        compadd -- ${(f)"$(kae __complete tools)"}
      elif (( CURRENT == 5 )); then
        compadd -- ${(f)"$(kae __complete accounts ${words[4]})"}
      fi
      ;;
    profile)
      if (( CURRENT == 3 )); then
        compadd -- save set unset rm default
      fi
      ;;
    completion)
      if (( CURRENT == 3 )); then
        compadd -- bash zsh fish
      fi
      ;;
    mise)
      if (( CURRENT == 3 )); then
        compadd -- init
      fi
      ;;
  esac
}
compdef _kae kae
`

const fishCompletionScript = `# kae fish completion — kae completion fish | source
# Dynamic: candidates come from ` + "`kae __complete`" + `, so they track live state.
function __kae_complete
    set -l tokens (commandline -opc)
    set -l n (count $tokens)
    if test $n -le 1
        kae __complete commands
        return
    end
    set -l cmd $tokens[2]
    switch $cmd
        case use u pin p run r
            if test $n -eq 2
                kae __complete profiles
                kae __complete tools
            else if test $n -eq 3
                kae __complete accounts $tokens[3]
            end
        case add doctor d
            if test $n -eq 2
                kae __complete tools
            else if test $n -eq 3
                kae __complete accounts $tokens[3]
            end
        case account
            if test $n -eq 2
                printf '%s\n' rm rename
            else if test $n -eq 3
                kae __complete tools
            else if test $n -eq 4
                kae __complete accounts $tokens[4]
            end
        case profile
            if test $n -eq 2
                printf '%s\n' save set unset rm default
            end
        case completion
            if test $n -eq 2
                printf '%s\n' bash zsh fish
            end
        case mise
            if test $n -eq 2
                printf '%s\n' init
            end
    end
end
complete -c kae -f -a '(__kae_complete)'
`
