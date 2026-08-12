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
# Adapted from the shared Go CLI standard's template check, which checks required files,
# one-level domain indexes and link targets. kae has no docs/<domain>/ subdirectories, so
# the domain-index half is replaced by the Documentation Map check, and the required-file
# half is gone with the bundle that used to hold the standard here (AGENTS.md's opening
# says why the bundle went away). The link walk is the standard's.
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
# Why it cannot. Deleting a required document under docs/ is caught — the Map links it and
# the walk resolves that link — but only while some document still links it, and nothing
# enforces that. Measured inbound links: each docs/ one has several, README.md is
# reachable only from the Map's own row, and AGENTS.md and CLAUDE.md from nothing at all.
# So deleting README.md together with its row passes, which was measured, and the other
# two rest on no link whatsoever. AGENTS.md does trip the missing-heading branch below,
# but that is a side effect of a different check rather than coverage, and it does not
# survive AGENTS.md being replaced by a directory.
#
# The predicate is three-sided on purpose. Missing, not a regular file, and empty all
# reach the same outcome, and all three were measured reporting `ok` when this tested only
# `-f` on one file: CLAUDE.md is what loads AGENTS.md for Claude Code, so an empty
# CLAUDE.md removes every project rule exactly as thoroughly as a deleted one.
for required in README.md AGENTS.md CLAUDE.md; do
  if [ ! -f "$required" ] || [ ! -s "$required" ]; then
    fail "$required is missing, empty, or not a regular file — no link here reaches it, so nothing else would notice"
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
# The extractor's exit status is read, which `done < <(python3 …)` threw away. Measured:
# an extractor that emits part of the walk and then dies produced
# `ok — 13 docs in the Map, 75 links resolved` and exit 0, because the count stayed far
# above its floor — the exact "walk that collapsed" this script's floors are described as
# catching. The `|| fail` keeps that loud rather than letting `set -e` kill the script with
# no message, which is indistinguishable from a clean run to anything reading only the
# status.
links=$(python3 "$root/scripts/docs_links.py") ||
  fail "the link extractor exited non-zero, so the walk below is truncated and its count means nothing"

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
  # `-f`, not `-e`: a directory satisfies `-e`, and replacing `docs/PRODUCT.md` with a
  # directory of that name was measured reporting `ok — 12 docs in the Map` with a whole
  # required document gone. Every target in this repository resolves to a regular file
  # today (measured over all of them, none resolving to a directory or a symlink to one),
  # so the narrowing costs nothing; a link deliberately pointing at a directory would fail
  # loudly here and is the case to revisit this line for.
  if [ ! -f "$md_dir/$target" ]; then
    fail "$md_rel link target does not exist: $link"
  fi
done <<LINKS
$links
LINKS
if [ "$links_checked" -lt 50 ]; then
  fail "resolved only $links_checked relative links, which is fewer than this repository has"
fi

if [ "$failures" -gt 0 ]; then
  printf 'check-docs: %s problem(s)\n' "$failures" >&2
  exit 1
fi

printf 'check-docs: ok — %s docs in the Map, %s links resolved\n' "$docs_checked" "$links_checked"
