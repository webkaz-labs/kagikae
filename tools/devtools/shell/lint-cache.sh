# shellcheck shell=sh disable=SC2034
# Sourced by check-go-format.sh and the staticcheck and golangci-lint mise tasks.
# Sets cache_root, the cache root for the pinned lint tools.
# Precedence: GOCLI_LINT_CACHE_DIR, then ${XDG_CACHE_HOME:-$HOME/.cache}/kae-lint,
# then ${TMPDIR:-/tmp}/kae-lint when neither XDG_CACHE_HOME nor HOME is set.
# The default is outside TMPDIR because macOS prunes old TMPDIR files, which strips
# extracted modules and fails the gate with "module X found, but does not contain
# package Y". It is a developer-tool cache, not kae configuration or state.
if [ -n "${GOCLI_LINT_CACHE_DIR:-}" ]; then
  cache_root=$GOCLI_LINT_CACHE_DIR
elif [ -n "${XDG_CACHE_HOME:-}" ]; then
  cache_root=$XDG_CACHE_HOME/kae-lint
elif [ -n "${HOME:-}" ]; then
  cache_root=$HOME/.cache/kae-lint
else
  cache_root=${TMPDIR:-/tmp}/kae-lint
fi
