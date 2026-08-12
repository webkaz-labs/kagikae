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

# --- the standard's required set, DERIVED from the standard --------------------
# Read out of the generated export rather than copied into this file. A hand-copy is the
# half of this check that exists purely to track an external document, and it is the half
# that cannot notice that document changing: the export re-syncs from chezmoi without this
# repository's involvement, so a re-sync that adds a required file would leave a literal
# here reporting ok forever. An earlier version copied the list and then dropped the floor
# over it, on the correct observation that counting iterations of a literal checks the
# script against itself — the conclusion should have been to stop iterating a literal.
#
# `docs/PRODUCT.md` is in the derived set. It was absent from this repository until the
# file holding mission and product boundaries was renamed to it from `DESIGN.md`, which
# the standard reserves for a visual design system. `UX.md` and `DESIGN.md` are named
# elsewhere in that document as conditional and are correctly not in this block.
#
# Only a directory line at indent 2 becomes the path prefix. A deeper one is consumed
# without setting it, so a grandchild would be reported at the path of the directory above
# it. That fails loudly on both the count and the path, and a two-level docs/ tree breaks
# the one-level rule the standard states — which check_domain_index flags on its own — so
# the case is disclosed here rather than handled.
#
# No apostrophe belongs inside the awk program below: it is single-quoted, and one in a
# comment there terminated the string and broke the script. Third time in this repository,
# after a process substitution and a `bash -c`.
required_files=$(awk '
  /^## Required Files/ { f = 1 }
  f && /^```/ { c++; if (c == 2) exit; next }
  f && c == 1 {
    match($0, /^ */); indent = RLENGTH
    line = $0; sub(/^ +/, "", line)
    if (line ~ /\/$/) { if (indent == 2) dir = line; next }
    if (line ~ /\.md$/) { print (indent > 2 ? dir : "") line }
  }
' "$generated/references/DOCUMENTATION.md" 2>/dev/null || true)
required_count=$(printf '%s\n' "$required_files" | grep -c '\.md' || true)

# The floor is today's derived value, not a lower bound. It was `-lt 8` and 8 is exactly
# what a plausible upstream reformat produces — describing README/AGENTS/CLAUDE in prose
# and keeping only the docs/ tree in the fence — so the collapse landed *on* the floor and
# passed silently, stopping the check from asserting those three files. That is the very
# defect docs/ROADMAP.md files against the template ("described and then checked by
# nothing"), reproduced here. At today's value a legitimate upstream *removal* fails
# loudly, which is the notification a derived set owes its reader.
#
# Do not turn this back into a floor. Measured against three upstream changes: adding a
# file kae already has reports the count alone; adding one kae lacks reports the count and
# names the file; and swapping one file for another — which keeps the count at 11 — is
# caught by the per-file check below rather than by this comparison. The two halves cover
# different things. The cost of equality is that a purely additive upstream change fails
# until this constant is bumped, which is the same bargain `EXPECTED_GUARDS` makes in
# scripts/smoke-run-selftest.sh.
EXPECTED_REQUIRED=11
if [ "${required_count:-0}" -ne "$EXPECTED_REQUIRED" ]; then
  fail "derived ${required_count:-0} required files from the standard, expected $EXPECTED_REQUIRED — check the new or removed file, then bump EXPECTED_REQUIRED"
fi

# Paths come out of the block's own indentation, so no location is hard-coded here. The
# previous version mapped three known names to the root and everything else under docs/,
# which meant a newly required file anywhere else was reported at a path the standard
# never named — failing loudly, but sending the reader to create the wrong file.
while IFS= read -r required; do
  if [ -z "$required" ]; then
    continue
  fi
  if [ ! -f "$required" ]; then
    fail "missing required file: $required (the standard's § Required Files names it)"
  fi
done <<REQUIRED
$required_files
REQUIRED

# --- a docs/<domain>/ child must be linked from its uppercase index ------------
# Kept from the standard's template even though kae has no docs/<domain>/ directories
# today: it self-no-ops when the directory is absent, so it costs nothing, and the same
# standard tells this repository to shard documents past 300-500 lines. On the day
# docs/release/ exists, the Documentation Map walk below (maxdepth 1) will not see its
# children and this will.
#
# The membership test is the link form, not the bare path: prose naming a child used to
# satisfy it. That is a literal, so three legitimate spellings would report "not linked" —
# a link title, angle brackets, and a `./` prefix. All fail in the loud direction, unlike
# the false negative they replaced, and none is worth a regex until a child exists.
check_domain_index() {
  domain=$1
  index=$2
  if [ ! -d "docs/$domain" ]; then
    return
  fi
  while IFS= read -r child; do
    if ! grep -Fq "]($domain/$(basename -- "$child"))" "docs/$index"; then
      fail "docs/$domain/$(basename -- "$child") is not linked from docs/$index"
    fi
  done < <(find "docs/$domain" -mindepth 1 -maxdepth 1 -type f -name '*.md' -print)
  if find "docs/$domain" -mindepth 2 -type f -print -quit | grep -q .; then
    fail "docs/$domain is deeper than the standard's one-level domain hierarchy"
  fi
}
check_domain_index product PRODUCT.md
check_domain_index ux UX.md
check_domain_index architecture ARCHITECTURE.md

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
  # Parameter expansion, not `dirname`: this loop runs once per link, and forking a
  # process there was measured at ~2.1s of the ~2.3s this script took — 294 of its ~320
  # subprocesses. The selftest calls this script four times, so it dominated there too.
  # Written as an `if` rather than `[ ... ] && ...` because AGENTS.md forbids that form
  # under `set -e`.
  md_dir=${md_rel%/*}
  if [ "$md_dir" = "$md_rel" ]; then
    md_dir=.
  fi
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
