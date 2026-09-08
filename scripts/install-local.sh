#!/usr/bin/env bash
# Stage a local build before invoking the shared direct-install operation.
set -euo pipefail
stage=$(mktemp -d "${TMPDIR:-/tmp}/kae-local-install.XXXXXXXX")
trap 'rm -rf "$stage"' EXIT
go build -o "$stage/kae" .
mkdir -p "$HOME/.local/bin"
destination="$(cd "$HOME/.local/bin" && pwd -P)/kae"
"$stage/kae" __install --yes --source-kind local_build --destination "$destination"
"$destination" completion --refresh
