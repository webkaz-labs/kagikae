# Sourced/executed only by the release smoke under scripts/smoke-run.sh.
set -e
autoload -Uz compinit
compinit -d "$HOME/.zcompdump"
source "$XDG_CONFIG_HOME/zsh/completions/_kae"
compadd() { shift; print -rl -- "$@"; }
words=(kae env --config=/p set '')
CURRENT=5
out=$(_kae)
[[ " $out " == *claude* ]]
words=(kae env set --format json claude '')
CURRENT=7
out=$(_kae)
[[ " $out " == *main* ]]
words=(kae env list '')
CURRENT=4
out=$(_kae)
test -z "$out"
words=(kae env '')
CURRENT=3
out=$(_kae)
for expected in set unset list; do [[ " ${out//$'\n'/ } " == *" $expected "* ]]; done
words=(kae backup '')
CURRENT=3
out=$(_kae)
[[ " $out " == *list* ]]
