#!/usr/bin/env bash
# Check the current Go module with the pinned formatters shared by mise and CI.
# Usage: check-go-format.sh [--portable ROOT]. Explicit targets are single modules;
# ignore inherited workspace/Go flags and require their own go.mod.
set -eu
if [ "${1:-}" = --portable ] && [ "$#" -eq 2 ]; then
  cd -- "$2"
  if [ ! -f go.mod ]; then
    printf 'go format check: target must contain go.mod\n' >&2
    exit 1
  fi
  export GOWORK=off
  export GOFLAGS=''
elif [ "$#" -ne 0 ]; then
  printf 'usage: check-go-format.sh [--portable ROOT]\n' >&2
  exit 2
fi
cache_root="${GOCLI_LINT_CACHE_DIR:-${TMPDIR:-/tmp}/kae-lint}"
mkdir -p "$cache_root"
export GOPATH="$cache_root/gopath"
export GOCACHE="$cache_root/gocache"
export GOMODCACHE="$cache_root/gomodcache"
module_path="$(go list -m)"
gofumpt_files="$(go run mvdan.cc/gofumpt@v0.10.0 -l .)"
goimports_files="$(go run golang.org/x/tools/cmd/goimports@v0.46.0 -local "$module_path" -l .)"
if [ -n "$gofumpt_files" ]; then
  printf 'go format check failed; run gofumpt on:\n%s\n' "$gofumpt_files" >&2
  exit 1
fi
if [ -n "$goimports_files" ]; then
  printf 'go import check failed; run goimports -local %s on:\n%s\n' "$module_path" "$goimports_files" >&2
  exit 1
fi
printf 'go format check: ok\n'
