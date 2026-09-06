# Sourced/executed only by the release smoke under scripts/smoke-run.sh.
set -e
autoload -Uz compinit
compinit -d "$HOME/.zcompdump-hook"
eval "$(mise activate zsh)"
eval "$(mise hook-env -s zsh)"
(( $+functions[_kae] ))
test "${_comps[kae]}" = _kae
