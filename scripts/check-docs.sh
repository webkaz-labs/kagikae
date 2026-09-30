#!/usr/bin/env bash
# Rejects the docs defects nothing else here catches: a markdown link whose target does not
# exist, a `X.md § Name` citation naming a section that file declares nowhere, a document
# under docs/ that AGENTS.md's Documentation Map does not list, a root document (README.md,
# AGENTS.md) that is missing, empty, or not a regular file,
# and — dormant, because no docs/<domain>/ directory exists yet — a domain child its
# uppercase index does not link. Enumerated rather than counted, because a count here has
# no reader: it undercounted once by leaving the dormant one out and nothing reported it.
#
# The link and section halves come from one program: tools/devtools/docrefs/parser.go's package
# comment is normative for which link and citation forms it reads, why the two extractors
# are not one, the designs that produced false positives on correct prose before this one,
# and — read it before trusting a clean run — the ceilings it lists, one of which bounds
# how much of a cited name it compares. Both predicates are pinned by main_test.go in that
# directory, which is what `go run` rather than a script buys.
#
# The orphan half is not hypothetical: docs/SCOPE-MODEL.md was once missing from that
# table, and a second normative copy of an upstream measurement grew inside it unnoticed.
# The Map is what routes a reader to a file; a file it omits is a file that only gets
# read by accident.
#
# Adapted from the shared Go CLI standard's template check, which checks required files,
# one-level domain indexes and link targets. kae has no docs/<domain>/ subdirectories, so
# the domain-index half is joined by the Documentation Map check rather than replaced by it —
# it is kept and self-no-ops, as the comment above it says — and the required-file
# half — which read its list out of the copy of the standard this repository used to carry
# (AGENTS.md's opening says why that copy went away) — is replaced by the root-document
# invariant below, which is this repository's own. The link walk is the standard's.
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
# Usage: check-docs.sh [--portable ROOT]. Portable checks references only;
# it omits kagikae document layout and count floors, and accepts directory links.
# Extractor syntax limitations still apply (see tools/devtools/docrefs/parser.go).
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
portable=0
if [ "${1:-}" = --portable ] && [ "$#" -eq 2 ]; then
  portable=1
  target=$(cd -- "$2" && pwd -P)
elif [ "$#" -eq 0 ]; then
  target=$root
else
  printf 'usage: check-docs.sh [--portable ROOT]\n' >&2
  exit 2
fi
cd -- "$target"

failures=0
fail() {
  printf 'check-docs: %s\n' "$*" >&2
  failures=$((failures + 1))
}

# --- the root documents the link walk cannot vouch for -------------------------
# This check used to derive the standard's whole required set by reading § Required Files
# out of the bundled export, precisely so that no literal list here could go stale against
# it. That bundle is gone, and so is the ability to track that document from this
# repository at all: the standard now lives only in the operator's user-level
# `go-cli-tooling` skill, which this gate must not read — a gate that depends on one
# machine's home fails for everyone else. What is left is not a copy of the standard's
# list but this repository's own invariant, which is why a literal is right here and a
# copy of that list was not: these are the required documents living at the root, and the
# root is where the walk below stops being able to vouch for a file.
#
# Why it cannot, per file, counting the documents that link each one and resolving `..`
# rather than comparing the raw target — the first measurement of this did not, so every
# `../AGENTS.md` from docs/ went uncounted and this comment claimed AGENTS.md was linked
# from nothing:
#
#   * each required document under docs/ is linked from several others, so deleting one
#     breaks a link the walk below resolves — but that is coverage by side effect, held up
#     by documents that happen to cite it and nothing that enforces they keep doing so;
#   * README.md is linked from several documents, so deleting it or replacing it with a
#     directory breaks links; what only this loop sees is README.md emptied, which passes
#     every other check;
#   * AGENTS.md is linked from several documents and its deletion, replacement by a
#     directory or emptying also trips the Map extraction and the floors. It is in this
#     loop as cheap redundancy.
#
# The predicate is three-sided on purpose: missing, not a regular file, and empty all reach
# the same outcome, and all three were measured reporting `ok` when this tested only `-f` on
# one file. Empty is as bad as absent: an emptied README.md was measured passing every other
# check. This is the one copy of that reasoning — the selftest cases point here rather than
# restating it.
docs_checked=0
if [ "$portable" -eq 0 ]; then
for required in README.md AGENTS.md; do
  if [ ! -f "$required" ] || [ ! -s "$required" ]; then
    fail "$required is missing, empty, or not a regular file — the root documents are asserted here because no link walk can vouch for all of them"
  fi
done

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
  children=$(find "docs/$domain" -mindepth 1 -maxdepth 1 -type f -name '*.md' -print) ||
    fail "walking docs/$domain failed, so its children were not all checked"
  while IFS= read -r child; do
    if [ -z "$child" ]; then
      continue
    fi
    if ! grep -Fq "]($domain/$(basename -- "$child"))" "docs/$index"; then
      fail "docs/$domain/$(basename -- "$child") is not linked from docs/$index"
    fi
  done <<CHILDREN
$children
CHILDREN
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

# Captured rather than piped in, for the reason the link walk below states at length: a
# producer inside `done < <(…)` can die partway and leave the loop reporting a short count
# as a clean run, because nothing reads its status. Every producer in this script is checked
# the same way now — a `find` that half-fails is less likely than the extractor crashing,
# but "less likely" is not a reason to close the class at one producer and leave it open at
# the rest.
docs_list=$(find docs -maxdepth 1 -type f -name '*.md' -print | sed 's|^\./||' | sort) ||
  fail "walking docs/ failed, so the count below is short and means nothing"

docs_checked=0
while IFS= read -r doc; do
  if [ -z "$doc" ]; then
    continue
  fi
  docs_checked=$((docs_checked + 1))
  if ! printf '%s\n' "$map_targets" | grep -Fxq "$doc"; then
    fail "$doc is not listed in AGENTS.md's Documentation Map"
  fi
done <<DOCS
$docs_list
DOCS
if [ "$docs_checked" -lt 10 ]; then
  fail "walked only $docs_checked files under docs/, which is fewer than this repository has"
fi

fi

# Reference predicates and explicit counters are shared with the portable entrypoint.
# shellcheck source=tools/devtools/shell/references.sh
source "$root/tools/devtools/shell/references.sh"
if [ "$portable" -eq 0 ]; then
if [ "$links_checked" -lt 50 ]; then
  fail "resolved only $links_checked relative links, which is fewer than this repository has"
fi
# A scale floor, like the two above: it bounds how much of the walk ran, never what the
# walk decides. An extractor emitting nothing, or a regex that matches nothing, lands at 0.
if [ "$sections_checked" -lt 100 ]; then
  fail "checked only $sections_checked section citations, which is fewer than this repository has"
fi
# What the floor cannot see, and the reason this is a predicate instead of a bigger number:
# two single-token collapses land *above* any floor this walk could carry. Dropping `.go`
# from tools/devtools/docrefs/parser.go's suffixes, or adding `internal` to its skipDirs, silently stops
# checking every citation in Go and still reports ~135 — measured. Naming both halves is
# two-sided in the way a count is not.
if [ "$sections_md" -eq 0 ] || [ "$sections_go" -eq 0 ]; then
  fail "section citations were found in markdown ($sections_md) and Go ($sections_go), and this repository has both"
fi

fi

if [ "$failures" -gt 0 ]; then
  printf 'check-docs: %s problem(s)\n' "$failures" >&2
  exit 1
fi

if [ "$portable" -eq 1 ]; then
  printf 'check-docs: portable — %s links, %s section citations checked; repository policy checks not applied\n' "$links_checked" "$sections_checked"
  if [ "$links_checked" -eq 0 ] && [ "$sections_checked" -eq 0 ]; then
    printf 'check-docs: no checkable references found; this is not evidence of reference coverage\n'
  fi
  exit 0
fi
printf 'check-docs: ok — %s docs in the Map, %s links resolved, %s section citations resolved\n' \
  "$docs_checked" "$links_checked" "$sections_checked"
