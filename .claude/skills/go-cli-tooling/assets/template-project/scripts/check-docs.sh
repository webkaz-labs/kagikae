#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
failures=0

fail() {
  printf 'docs-check: %s\n' "$*" >&2
  failures=$((failures + 1))
}

for required in PRODUCT ARCHITECTURE CLI DATA-MODEL SECURITY ROADMAP RELEASE VALIDATION; do
  if [[ ! -f "$root/docs/$required.md" ]]; then
    fail "missing required file: docs/$required.md"
  fi
done

check_domain_index() {
  local domain="$1"
  local index="$2"
  local child

  if [[ ! -d "$root/docs/$domain" ]]; then
    return
  fi
  while IFS= read -r child; do
    if ! grep -Fq "$domain/$(basename "$child")" "$root/docs/$index"; then
      fail "orphaned docs/$domain child is not linked from docs/$index: $(basename "$child")"
    fi
  done < <(find "$root/docs/$domain" -mindepth 1 -maxdepth 1 -type f -name '*.md' -print)
  if find "$root/docs/$domain" -mindepth 2 -type f -print -quit | grep -q .; then
    fail "docs/$domain is deeper than the standard one-level domain hierarchy"
  fi
}

check_domain_index product PRODUCT.md
check_domain_index ux UX.md
check_domain_index architecture ARCHITECTURE.md

while IFS=$'\t' read -r md_rel link; do
  target="${link%%#*}"
  target="${target#<}"
  target="${target%>}"
  if [[ -z "$target" || "$target" == \#* ]]; then
    continue
  fi
  if [[ "$target" =~ ^[A-Za-z][A-Za-z0-9+.-]*: || "$target" == /* ]]; then
    continue
  fi
  md_dir="$(dirname "$md_rel")"
  if [[ "$md_dir" == "." && "$target" == ../* ]]; then
    continue
  fi
  if ! (cd "$root/$md_dir" && [[ -e "$target" ]]); then
    fail "$md_rel link target does not exist: $link"
  fi
done < <(
  find "$root" -path "$root/.git" -prune -o -type f -name '*.md' -print |
    while IFS= read -r md; do
      md_rel="${md#"$root"/}"
      while IFS= read -r link; do
        printf '%s\t%s\n' "$md_rel" "$link"
      done < <(
        grep -Eo '\[[^]]+\]\([^)]+\)' "$md" |
          sed -E 's/^.*\(([^)]+)\)$/\1/' ||
          true
      )
    done
)

if [[ "$failures" -gt 0 ]]; then
  exit 1
fi

printf 'docs-check: ok\n'
