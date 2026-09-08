#!/usr/bin/env bash
# Generate archive resources from this release's command/completion registry.
set -euo pipefail
mkdir -p .release-completions
for shell in bash zsh fish; do
  go run . completion "$shell" > ".release-completions/kae.$shell"
done
