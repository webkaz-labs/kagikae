#!/usr/bin/env bash
# kagikae's historical positive control is product policy. The shared matcher and
# its synthetic controls live under tools/devtools/shell.
set -euo pipefail
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
if [ "${1:-}" = --portable ]; then
  exec bash "$root/tools/devtools/shell/sweep-quantities.sh" "$@"
fi
exec bash "$root/tools/devtools/shell/sweep-quantities.sh" --positive-control 89341f4 'Not converged' "$@"
