# shellcheck shell=sh disable=SC2034,SC2154
# Sourced by check-go-format.sh and the staticcheck, golangci-lint and vuln mise tasks.
# Input: cache_name (directory name) and cache_override (the caller's override value,
# possibly empty). Sets cache_root, the cache root for a pinned tool set.
# Precedence: cache_override, then ${XDG_CACHE_HOME:-$HOME/.cache}/$cache_name, then
# ${TMPDIR:-/tmp}/$cache_name when neither XDG_CACHE_HOME nor HOME is set.
# The default is outside TMPDIR because macOS prunes old TMPDIR files, which strips
# extracted modules and fails the gate with "module X found, but does not contain
# package Y". It is a developer-tool cache, not kae configuration or state.
if [ -n "${cache_override:-}" ]; then
  cache_root=$cache_override
elif [ -n "${XDG_CACHE_HOME:-}" ]; then
  cache_root=$XDG_CACHE_HOME/$cache_name
elif [ -n "${HOME:-}" ]; then
  cache_root=$HOME/.cache/$cache_name
else
  cache_root=${TMPDIR:-/tmp}/$cache_name
fi
