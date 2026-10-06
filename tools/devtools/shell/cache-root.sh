# shellcheck shell=sh disable=SC2154
# Sourced by check-go-format.sh and the staticcheck, golangci-lint and vuln mise tasks.
# Input: cache_name (directory name) and cache_override (the caller's override value,
# possibly empty). Sets cache_root and prepares it as the Go cache root: creates it
# and exports GOPATH, GOCACHE and GOMODCACHE beneath it.
# Precedence: cache_override, then an absolute $XDG_CACHE_HOME/$cache_name (a relative
# value is ignored, as the XDG specification requires), then $HOME/.cache/$cache_name,
# then ${TMPDIR:-/tmp}/$cache_name when neither is usable.
# The default avoids TMPDIR because macOS prunes old TMPDIR files, which strips
# extracted modules and fails the gate with "module X found, but does not contain
# package Y".
case ${XDG_CACHE_HOME:-} in
/*) xdg_cache_home=$XDG_CACHE_HOME ;;
*) xdg_cache_home= ;;
esac
if [ -n "${cache_override:-}" ]; then
  cache_root=$cache_override
elif [ -n "$xdg_cache_home" ]; then
  cache_root=$xdg_cache_home/$cache_name
elif [ -n "${HOME:-}" ]; then
  cache_root=$HOME/.cache/$cache_name
else
  cache_root=${TMPDIR:-/tmp}/$cache_name
fi
mkdir -p "$cache_root"
export GOPATH="$cache_root/gopath"
export GOCACHE="$cache_root/gocache"
export GOMODCACHE="$cache_root/gomodcache"
