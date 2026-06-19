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
		registerCompletionFlags(fs, &install)
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
  local cur cmd i
  cur="${COMP_WORDS[COMP_CWORD]}"
  if [ "$COMP_CWORD" -eq 1 ]; then
    COMPREPLY=( $(compgen -W "$(kae __complete commands)" -- "$cur") )
    return
  fi
  cmd="${COMP_WORDS[1]}"
  # Typing a flag: complete this command's flag names.
  if [[ "$cur" == -* ]]; then
    COMPREPLY=( $(compgen -W "$(kae __complete flags "$cmd")" -- "$cur") )
    return
  fi
  # Positional args after the command, excluding flags, up to the cursor — so a
  # flag like --no-login / -i / -P before the positionals does not shift the
  # completion (np is the positional slot the cursor is at).
  local -a pos=()
  for (( i=2; i<COMP_CWORD; i++ )); do
    case "${COMP_WORDS[i]}" in
      -*) ;;
      *) pos+=("${COMP_WORDS[i]}") ;;
    esac
  done
  local np=${#pos[@]}
  case "$cmd" in
    use|u|pin|p|run|r)
      if [ "$np" -eq 0 ]; then
        COMPREPLY=( $(compgen -W "$(kae __complete profiles) $(kae __complete tools)" -- "$cur") )
      elif [ "$np" -eq 1 ]; then
        COMPREPLY=( $(compgen -W "$(kae __complete accounts "${pos[0]}")" -- "$cur") )
      fi
      ;;
    add|doctor|d)
      if [ "$np" -eq 0 ]; then
        COMPREPLY=( $(compgen -W "$(kae __complete tools)" -- "$cur") )
      elif [ "$np" -eq 1 ]; then
        COMPREPLY=( $(compgen -W "$(kae __complete accounts "${pos[0]}")" -- "$cur") )
      fi
      ;;
    account)
      if [ "$np" -eq 0 ]; then
        COMPREPLY=( $(compgen -W "rm rename set-identity" -- "$cur") )
      elif [ "$np" -eq 1 ]; then
        COMPREPLY=( $(compgen -W "$(kae __complete tools)" -- "$cur") )
      elif [ "$np" -eq 2 ]; then
        COMPREPLY=( $(compgen -W "$(kae __complete accounts "${pos[1]}")" -- "$cur") )
      fi
      ;;
    profile)
      if [ "$np" -eq 0 ]; then
        COMPREPLY=( $(compgen -W "save set unset rm default" -- "$cur") )
      fi
      ;;
    completion)
      if [ "$np" -eq 0 ]; then
        COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") )
      fi
      ;;
    mise)
      if [ "$np" -eq 0 ]; then
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
  local cmd i
  local -a pos
  if (( CURRENT == 2 )); then
    compadd -- ${(f)"$(kae __complete commands)"}
    return
  fi
  cmd="${words[2]}"
  # Typing a flag: complete this command's flag names.
  if [[ "${words[CURRENT]}" == -* ]]; then
    compadd -- ${(f)"$(kae __complete flags $cmd)"}
    return
  fi
  # Positional args after the command, excluding flags, up to the cursor, so a
  # flag (--no-login / -i / -P) before the positionals does not shift completion.
  for (( i=3; i<CURRENT; i++ )); do
    [[ "${words[i]}" == -* ]] || pos+=("${words[i]}")
  done
  local np=${#pos[@]}
  case "$cmd" in
    use|u|pin|p|run|r)
      if (( np == 0 )); then
        compadd -- ${(f)"$(kae __complete profiles)"} ${(f)"$(kae __complete tools)"}
      elif (( np == 1 )); then
        compadd -- ${(f)"$(kae __complete accounts ${pos[1]})"}
      fi
      ;;
    add|doctor|d)
      if (( np == 0 )); then
        compadd -- ${(f)"$(kae __complete tools)"}
      elif (( np == 1 )); then
        compadd -- ${(f)"$(kae __complete accounts ${pos[1]})"}
      fi
      ;;
    account)
      if (( np == 0 )); then
        compadd -- rm rename set-identity
      elif (( np == 1 )); then
        compadd -- ${(f)"$(kae __complete tools)"}
      elif (( np == 2 )); then
        compadd -- ${(f)"$(kae __complete accounts ${pos[2]})"}
      fi
      ;;
    profile)
      if (( np == 0 )); then
        compadd -- save set unset rm default
      fi
      ;;
    completion)
      if (( np == 0 )); then
        compadd -- bash zsh fish
      fi
      ;;
    mise)
      if (( np == 0 )); then
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
    # Typing a flag: complete this command's flag names.
    if string match -q -- '-*' (commandline -ct)
        kae __complete flags $cmd
        return
    end
    # Positional args after the command, excluding flags, so a flag
    # (--no-login / -i / -P) before the positionals does not shift completion.
    set -l pos
    for i in (seq 3 $n)
        if not string match -q -- '-*' $tokens[$i]
            set -a pos $tokens[$i]
        end
    end
    set -l np (count $pos)
    switch $cmd
        case use u pin p run r
            if test $np -eq 0
                kae __complete profiles
                kae __complete tools
            else if test $np -eq 1
                kae __complete accounts $pos[1]
            end
        case add doctor d
            if test $np -eq 0
                kae __complete tools
            else if test $np -eq 1
                kae __complete accounts $pos[1]
            end
        case account
            if test $np -eq 0
                printf '%s\n' rm rename set-identity
            else if test $np -eq 1
                kae __complete tools
            else if test $np -eq 2
                kae __complete accounts $pos[2]
            end
        case profile
            if test $np -eq 0
                printf '%s\n' save set unset rm default
            end
        case completion
            if test $np -eq 0
                printf '%s\n' bash zsh fish
            end
        case mise
            if test $np -eq 0
                printf '%s\n' init
            end
    end
end
complete -c kae -f -a '(__kae_complete)'
`
