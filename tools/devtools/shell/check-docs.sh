#!/usr/bin/env bash
# Check references in an explicit foreign tree; no product layout or count floors.
set -euo pipefail
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)"
if [ "${1:-}" = --portable ] && [ "$#" -eq 2 ]; then shift; fi
if [ "$#" -ne 1 ]; then printf 'usage: check-docs.sh ROOT\n' >&2; exit 2; fi
target=$(cd -- "$1" && pwd -P)
portable=1
failures=0
fail() { printf 'check-docs: %s\n' "$*" >&2; failures=$((failures + 1)); }
cd -- "$target"
# shellcheck source=tools/devtools/shell/references.sh
source "$root/tools/devtools/shell/references.sh"
if [ "$failures" -gt 0 ]; then printf 'check-docs: %s problem(s)\n' "$failures" >&2; exit 1; fi
printf 'check-docs: portable — %s links, %s section citations checked; repository policy checks not applied\n' "$links_checked" "$sections_checked"
if [ "$links_checked" -eq 0 ] && [ "$sections_checked" -eq 0 ]; then printf 'check-docs: no checkable references found; this is not evidence of reference coverage\n'; fi
