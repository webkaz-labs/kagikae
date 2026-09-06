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
	"init", "edit", "doctor", "add", "use", "pin", "unpin", "relogin", "run", "env",
	"companion", "mise", "accounts", "ls", "account", "profile", "status",
	"backup", "rollback", "completion", "version", "help",
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
	var install, refresh bool
	opts, ok := parseCommon("completion", flags, false, func(fs *flag.FlagSet) {
		registerCompletionFlags(fs, &install, &refresh)
	})
	if !ok {
		return constants.ExitUsage
	}
	// --refresh rewrites whatever is already registered, across shells, so it
	// takes no shell argument and does not combine with --install.
	if refresh {
		if install || len(positionals) != 0 {
			return usageError("usage: %s completion --refresh", toolName)
		}
		return runCompletionRefresh(newApp(opts.ConfigPath), opts)
	}
	if len(positionals) != 1 {
		return usageError("usage: %s completion <bash|zsh|fish> [--install] | %s completion --refresh", toolName, toolName)
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
// word 1 → commands; the argument positions → tools/profiles/accounts/
// companions/companion-knobs. Account completion passes the preceding tool word
// so `kae use claude <TAB>` scopes to claude's accounts, and companion-knob
// completion passes the companion id (`kae companion add main git <TAB>` →
// git's knobs). The live lists come from the backend; the small,
// rarely-changing sub-verb sets (e.g. account rm/rename, companion add/rm/list,
// the shells for completion) are inlined here since they are not part of the
// `__complete` kind contract.

const bashCompletionScript = `# kae bash completion — eval "$(kae completion bash)"
# Dynamic: candidates come from ` + "`kae __complete`" + `, so they track live state.
# Reconstruct arguments without evaluation: COMP_WORDS loses whether '=' was
# adjacent to a flag, separated by whitespace, or part of a longer value.
_kae_bash_words() {
  # Readline's cursor offset is bytes, including in a multibyte locale.
  local LC_ALL=C
  local line="${COMP_LINE:0:COMP_POINT}" ch quote='' token='' started=0 escaped=0 j
  kae_words=()
  for (( j=0; j<${#line}; j++ )); do
    ch="${line:j:1}"
    if [ "$escaped" -eq 1 ]; then
      escaped=0
      if [ "$ch" != $'\n' ]; then token+="$ch"; started=1; fi
      continue
    fi
    if [ "$quote" = "'" ]; then
      if [ "$ch" = "'" ]; then quote=''; else token+="$ch"; fi
      continue
    fi
    if [ "$ch" = '\' ]; then
      if [ "$quote" = '"' ]; then
        case "${line:j+1:1}" in
          '$'|` + "'`'" + `|'"'|'\'|$'\n') escaped=1 ;;
          *) token+="$ch" ;;
        esac
      else
        escaped=1
      fi
      continue
    fi
    if [ "$quote" = '"' ]; then
      if [ "$ch" = '"' ]; then quote=''; else token+="$ch"; fi
      continue
    fi
    case "$ch" in
      "'"|'"') quote="$ch"; started=1 ;;
      ' '|$'\t'|$'\n')
        if [ "$started" -eq 1 ]; then kae_words+=("$token"); token=''; started=0; fi
        ;;
      ';'|'|'|'&') kae_words=(); token=''; started=0 ;;
      *) token+="$ch"; started=1 ;;
    esac
  done
  # The last element is always the current argument, including an empty slot.
  kae_words+=("$token")
}
_kae() {
  local cur cmd i
  local -a kae_words
  local cursor=$COMP_CWORD
  kae_words=("${COMP_WORDS[@]}")
  if [ -n "${COMP_LINE-}" ]; then
    _kae_bash_words
    cursor=$((${#kae_words[@]} - 1))
  fi
  cur="${kae_words[cursor]}"
  if [ "$cursor" -eq 1 ]; then
    COMPREPLY=( $(compgen -W "$(kae __complete commands)" -- "$cur") )
    return
  fi
  cmd="${kae_words[1]}"
  COMPREPLY=()
  # Consume registered flag values before routing positionals. Like splitArgs,
  # a bare -- is a flag token; it does not change the parsing mode.
  local valued=" $(kae __complete valued-flags "$cmd" | tr '\n' ' ') "
  local pending=0 word
  local -a pos=()
  for (( i=2; i<cursor; i++ )); do
    word="${kae_words[i]}"
    if [ "$pending" -eq 1 ]; then
      pending=0
      continue
    fi
    case "$word" in
      -*=*) ;;
      -*)
        case "$valued" in *" $word "*) pending=1 ;; esac
        ;;
      *) pos+=("$word") ;;
    esac
  done
  if [ "$pending" -eq 1 ]; then return; fi
  # An attached value is not a flag-name completion request.
  case "$cur" in -*=*) return ;; esac
  if [[ "$cur" == -* ]]; then
    COMPREPLY=( $(compgen -W "$(kae __complete flags "$cmd")" -- "$cur") )
    return
  fi
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
    relogin)
      # Only a tool: the account comes from the binding, never from a word here.
      if [ "$np" -eq 0 ]; then
        COMPREPLY=( $(compgen -W "$(kae __complete tools)" -- "$cur") )
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
    companion)
      if [ "$np" -eq 0 ]; then
        COMPREPLY=( $(compgen -W "add rm list" -- "$cur") )
      elif [ "$np" -eq 1 ]; then
        COMPREPLY=( $(compgen -W "$(kae __complete profiles)" -- "$cur") )
      elif [ "$np" -eq 2 ]; then
        COMPREPLY=( $(compgen -W "$(kae __complete companions)" -- "$cur") )
      else
        COMPREPLY=( $(compgen -W "$(kae __complete companion-knobs "${pos[2]}")" -- "$cur") )
      fi
      ;;
    profile)
      if [ "$np" -eq 0 ]; then
        COMPREPLY=( $(compgen -W "save set unset rm default" -- "$cur") )
      fi
      ;;
    env)
      if [ "$np" -eq 0 ]; then
        COMPREPLY=( $(compgen -W "set unset list" -- "$cur") )
      elif [ "${pos[0]}" != "list" ]; then
        # set|unset take <tool> <account> KEY…; list takes no arguments.
        if [ "$np" -eq 1 ]; then
          COMPREPLY=( $(compgen -W "$(kae __complete tools)" -- "$cur") )
        elif [ "$np" -eq 2 ]; then
          COMPREPLY=( $(compgen -W "$(kae __complete accounts "${pos[1]}")" -- "$cur") )
        fi
      fi
      ;;
    backup)
      if [ "$np" -eq 0 ]; then
        COMPREPLY=( $(compgen -W "list" -- "$cur") )
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
  local -a valued
  valued=(${(f)"$(kae __complete valued-flags "$cmd")"})
  local pending=0 word
  for (( i=3; i<CURRENT; i++ )); do
    word="${words[i]}"
    if (( pending )); then
      pending=0
      continue
    fi
    case "$word" in
      -*=*) ;;
      -*)
        if (( ${valued[(Ie)$word]} )); then pending=1; fi
        ;;
      *) pos+=("$word") ;;
    esac
  done
  if (( pending )); then return; fi
  case "${words[CURRENT]}" in -*=*) return ;; esac
  if [[ "${words[CURRENT]}" == -* ]]; then
    compadd -- ${(f)"$(kae __complete flags $cmd)"}
    return
  fi
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
    relogin)
      # Only a tool: the account comes from the binding, never from a word here.
      if (( np == 0 )); then
        compadd -- ${(f)"$(kae __complete tools)"}
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
    companion)
      if (( np == 0 )); then
        compadd -- add rm list
      elif (( np == 1 )); then
        compadd -- ${(f)"$(kae __complete profiles)"}
      elif (( np == 2 )); then
        compadd -- ${(f)"$(kae __complete companions)"}
      else
        compadd -- ${(f)"$(kae __complete companion-knobs ${pos[3]})"}
      fi
      ;;
    profile)
      if (( np == 0 )); then
        compadd -- save set unset rm default
      fi
      ;;
    env)
      if (( np == 0 )); then
        compadd -- set unset list
      elif [[ "${pos[1]}" != list ]]; then
        # set|unset take <tool> <account> KEY…; list takes no arguments.
        if (( np == 1 )); then
          compadd -- ${(f)"$(kae __complete tools)"}
        elif (( np == 2 )); then
          compadd -- ${(f)"$(kae __complete accounts ${pos[2]})"}
        fi
      fi
      ;;
    backup)
      if (( np == 0 )); then
        compadd -- list
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
    set -l valued (kae __complete valued-flags $cmd)
    set -l pending 0
    set -l pos
    for i in (seq 3 $n)
        set -l word $tokens[$i]
        if test $pending -eq 1
            set pending 0
            continue
        end
        if string match -q -- '-*=*' $word
            continue
        else if string match -q -- '-*' $word
            if contains -- $word $valued
                set pending 1
            end
        else
            set -a pos $word
        end
    end
    if test $pending -eq 1
        return
    end
    if string match -q -- '-*=*' (commandline -ct)
        return
    end
    if string match -q -- '-*' (commandline -ct)
        kae __complete flags $cmd
        return
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
        case relogin
            # Only a tool: the account comes from the binding, never from a word here.
            if test $np -eq 0
                kae __complete tools
            end
        case account
            if test $np -eq 0
                printf '%s\n' rm rename set-identity
            else if test $np -eq 1
                kae __complete tools
            else if test $np -eq 2
                kae __complete accounts $pos[2]
            end
        case companion
            if test $np -eq 0
                printf '%s\n' add rm list
            else if test $np -eq 1
                kae __complete profiles
            else if test $np -eq 2
                kae __complete companions
            else
                kae __complete companion-knobs $pos[3]
            end
        case profile
            if test $np -eq 0
                printf '%s\n' save set unset rm default
            end
        case env
            if test $np -eq 0
                printf '%s\n' set unset list
            else if test "$pos[1]" != list
                # set|unset take <tool> <account> KEY…; list takes no arguments.
                if test $np -eq 1
                    kae __complete tools
                else if test $np -eq 2
                    kae __complete accounts $pos[2]
                end
            end
        case backup
            if test $np -eq 0
                printf '%s\n' list
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
