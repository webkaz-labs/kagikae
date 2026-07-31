# shellcheck shell=sh
# Isolation preamble for the smoke checks in docs/VALIDATION.md. Source it from
# the repository root; running it in a subshell exports nothing:
#
#   . scripts/smoke-env.sh
#
# Every root paths.Resolve reads gets a temp value, each on its own line. Two
# reasons this lives in a file rather than in the doc's prose:
#
#   * In `export A=new B=$A`, $A expands to A's OLD value, so a single line would
#     point every XDG_* path at the real HOME.
#   * paths.Resolve reads each root independently and an absolute value inherited
#     from the real environment wins over the temp HOME, so a copy that misses one
#     is not isolated at all. A hand-written block that set HOME, XDG_CONFIG_HOME
#     and XDG_DATA_HOME wrote a fixture account into the operator's live
#     state.json (2026-07-31). Three copies of these exports is three chances to
#     omit one; this is the only copy.
HOME=$(mktemp -d)
export HOME
export XDG_CONFIG_HOME="$HOME/.config"
export XDG_DATA_HOME="$HOME/.local/share"
export XDG_STATE_HOME="$HOME/.local/state"
export XDG_RUNTIME_DIR="$HOME/.local/run"
export NO_COLOR=1
