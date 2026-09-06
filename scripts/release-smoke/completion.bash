# Sourced/executed only by the release smoke under scripts/smoke-run.sh.
set -e
source "$XDG_DATA_HOME/bash-completion/completions/kae"
COMP_LINE='kae env --config=/p set '
COMP_POINT=${#COMP_LINE}
COMP_WORDS=(kae env --config = /p set '')
COMP_CWORD=6
_kae
[[ " ${COMPREPLY[*]} " == *' claude '* ]]
COMP_LINE='kae env set --format json claude '
COMP_POINT=${#COMP_LINE}
COMP_WORDS=(kae env set --format json claude '')
COMP_CWORD=6
_kae
[[ " ${COMPREPLY[*]} " == *' main '* ]]
COMP_LINE='kae env list '
COMP_POINT=${#COMP_LINE}
COMP_WORDS=(kae env list '')
COMP_CWORD=3
_kae
test "${#COMPREPLY[@]}" -eq 0
COMP_LINE='kae env '
COMP_POINT=${#COMP_LINE}
COMP_WORDS=(kae env '')
COMP_CWORD=2
_kae
for expected in set unset list; do [[ " ${COMPREPLY[*]} " == *" $expected "* ]]; done
COMP_LINE='kae backup '
COMP_POINT=${#COMP_LINE}
COMP_WORDS=(kae backup '')
COMP_CWORD=2
_kae
[[ " ${COMPREPLY[*]} " == *' list '* ]]
