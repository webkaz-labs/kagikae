#!/usr/bin/env bash
# Rejects two docs defects nothing else here catches: a markdown link whose target
# does not exist, and a document under docs/ that AGENTS.md's Documentation Map does
# not list.
#
# The orphan half is not hypothetical. docs/SCOPE-MODEL.md was the one file missing
# from that table, so nothing told a reader when to open it, and a second normative
# copy of an upstream measurement grew inside it unnoticed until a duplication scan
# found it (docs/ROADMAP.md records the pass). The Map is what routes a reader to a
# file; a file it omits is a file that only gets read by accident.
#
# Adapted from the shared Go CLI standard's template check
# (.claude/skills/go-cli-tooling/assets/template-project/scripts/check-docs.sh), which
# checks required files, one-level domain indexes and link targets. kae has no
# docs/<domain>/ subdirectories, so the domain-index half is replaced by the
# Documentation Map check; the required-file list and the link walk are the standard's.
#
# Distinct from `mise run docs-scan`, which reports prose two documents carry twice and
# deliberately fails nothing. This one fails, so it is in `mise run check`.
#
# EVERY count below is a floor asserted against a measured value, because the failure
# mode for a check like this is finding nothing and reporting ok: a mistyped path, a
# `find` that prunes too much, or a `grep` that silently matches zero lines all look
# identical to a clean run. The floors are deliberately far below today's values so
# ordinary editing does not trip them; they exist to catch a walk that collapsed, not
# to pin a number.
#
# A floor is only real if it can be reached. Two here could not: `grep` exits 1 when it
# matches nothing, and under `set -e` with `pipefail` that aborts the assignment it sits
# in, so the script died with no output at all and the floor below it never ran. Both
# were verified by mutating the extraction and confirming the *message* appears, not
# merely that the exit status was non-zero — a silent death also exits non-zero, and it
# was passing for that reason. Every `grep` whose zero-match case is legitimate is
# therefore wrapped in `|| true`.
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd -- "$root"

failures=0
fail() {
  printf 'check-docs: %s\n' "$*" >&2
  failures=$((failures + 1))
}

# The generated export of the shared standard. Excluded for the reason AGENTS.md
# § Documentation Update Checklist excludes it: an edit there is lost on the next
# re-sync, so a finding in it is not actionable here.
generated='.claude/skills/go-cli-tooling'

# --- the standard's required set ------------------------------------------------
# docs/PRODUCT.md is on this list because the standard's DOCUMENTATION.md puts it
# there. It was missing from this repository until the file that holds mission and
# product boundaries was renamed to it from DESIGN.md, which the standard now reserves
# for a visual design system.
required_count=0
for required in PRODUCT ARCHITECTURE CLI DATA-MODEL SECURITY ROADMAP RELEASE VALIDATION; do
  if [ ! -f "docs/$required.md" ]; then
    fail "missing required file: docs/$required.md"
  fi
  required_count=$((required_count + 1))
done
if [ "$required_count" -ne 8 ]; then
  fail "internal: required-file loop ran $required_count times, expected 8"
fi

# --- every docs/*.md is listed in AGENTS.md's Documentation Map -------------------
map_start=$(grep -n '^## Documentation Map' AGENTS.md | cut -d: -f1 || true)
if [ -z "$map_start" ]; then
  fail "AGENTS.md has no '## Documentation Map' heading — the orphan check cannot run"
  map_body=''
else
  # From the heading to the next H2, which is where the table ends.
  map_body=$(awk -v s="$map_start" 'NR>s && /^## /{exit} NR>=s' AGENTS.md)
fi
map_rows=$(printf '%s' "$map_body" | grep -c '^|' || true)
if [ "${map_rows:-0}" -lt 5 ]; then
  fail "Documentation Map has ${map_rows:-0} table rows, which is too few to be the real table"
fi

# Compare against the paths the Map actually links, matched whole. A substring test
# here passes an orphan whose name happens to sit inside another row: docs/RULES.md
# slipped through as "ok" because CREDENTIAL-RULES.md is in the table, and so did
# DAPTERS.md against ADAPTERS.md. Measured before this was written the exact way.
map_targets=$(printf '%s' "$map_body" |
  { grep -oE '\]\(docs/[A-Za-z0-9._-]+\)' || true; } |
  sed -e 's/^](//' -e 's/)$//' |
  sort -u)
map_target_count=$(printf '%s\n' "$map_targets" | grep -c 'docs/' || true)
if [ "${map_target_count:-0}" -lt 10 ]; then
  fail "extracted only ${map_target_count:-0} docs/ links from the Map, fewer than the table holds"
fi

docs_checked=0
while IFS= read -r doc; do
  docs_checked=$((docs_checked + 1))
  if ! printf '%s\n' "$map_targets" | grep -Fxq "$doc"; then
    fail "$doc is not listed in AGENTS.md's Documentation Map"
  fi
done < <(find docs -maxdepth 1 -type f -name '*.md' -print | sed 's|^\./||' | sort)
if [ "$docs_checked" -lt 10 ]; then
  fail "walked only $docs_checked files under docs/, which is fewer than this repository has"
fi

# --- every relative markdown link resolves ---------------------------------------
links_checked=0
while IFS=$'\t' read -r md_rel link; do
  target=${link%%#*}
  target=${target#<}
  target=${target%>}
  case $target in
    '' | '#'*) continue ;;
    *:*) continue ;;   # scheme:… — mailto:, https:, etc.
    /*) continue ;;    # absolute path, not ours to resolve
  esac
  links_checked=$((links_checked + 1))
  md_dir=$(dirname -- "$md_rel")
  if [ ! -e "$md_dir/$target" ]; then
    fail "$md_rel link target does not exist: $link"
  fi
done < <(python3 "$root/scripts/docs_links.py" "$generated")
if [ "$links_checked" -lt 50 ]; then
  fail "resolved only $links_checked relative links, which is fewer than this repository has"
fi

if [ "$failures" -gt 0 ]; then
  printf 'check-docs: %s problem(s)\n' "$failures" >&2
  exit 1
fi

printf 'check-docs: ok — %s docs in the Map, %s links resolved\n' "$docs_checked" "$links_checked"
