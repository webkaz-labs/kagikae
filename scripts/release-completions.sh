#!/usr/bin/env bash
# Generate archive resources from this release's command/completion registry.
set -euo pipefail
mkdir -p .release-completions
for shell in bash zsh fish; do
  # A packaged completion is a completion file: without the kae shell function.
  go run . completion "$shell" --no-function > ".release-completions/kae.$shell"
done
