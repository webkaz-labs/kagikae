#!/usr/bin/env bash
# Checks that scripts/check-docs.sh still detects what it exists to detect.
#
# Why this file exists. That script's floors bound each walk's *size* — how many docs
# were walked, how many links resolved — and nothing bounds its *predicates*. Both were
# measured passing with a real defect present: replacing `-e "$md_dir/$target"` with
# `-e "$md_dir"` reported `ok — 13 docs in the Map, 295 links resolved` while a broken
# link sat in the tree, and replacing the Map membership test with `grep -q .` reported
# ok with docs/SCOPE-MODEL.md's routing row deleted — the exact defect the check was
# written for. No floor can see either, because the counts stay far above them.
#
# docs/ROADMAP.md carries an open entry titled "The smoke guards have no test, and four
# changes switched one off without anything noticing". This is that lesson applied to
# the newer guard before it earns its own entry: the cases below, not a suite.
#
# Each case asserts the DIAGNOSTIC, never the exit status. A script that dies silently
# under `set -e` also exits non-zero, and that is precisely how two unreachable floors
# in check-docs.sh looked like passing controls until the message was asserted instead.
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd -- "$root"

failures=0
cases=0

# The success line, in one place. Every comparison against it goes through this name — the
# cases that want it, and `check`'s test for a complaint printed beside it — and the reason
# is that the two are not independent. Reproduced before this was wired up: reword the
# success line, and only the cases fail, so a maintainer updates the case literals the
# failure points at and leaves the one inside `check` behind. From then on `[ "$want" !=
# "$OK_LINE" ]` is true for every case while `grep -Fq "$OK_LINE"` matches nothing, so the
# beside-the-success-line assertion passes vacuously on all of them; the defect it exists to
# catch was then injected and the suite still reported every case holding. Sharing the
# constant is what makes the reword fail at the cases instead of being absorbed by them.
readonly OK_LINE='check-docs: ok'

# The fixture is the working tree's tracked files, so this needs a git work tree. Say so
# rather than letting a case die inside a fixture edit on a missing file, which is what
# happened when this was first run against an extracted copy of the tree.
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  printf 'check-docs-selftest: not inside a git work tree; the fixture needs `git ls-files`\n' >&2
  exit 1
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# A fresh copy per case, so a mutation cannot leak into the next.
#
# Built from the WORKING TREE's tracked files, not from `git archive HEAD`. This runs in
# `mise run check`, which is the gate before a commit, so checking HEAD would validate
# the previous commit and say nothing about what is being committed — the first version
# did that and reported a failure that had already been fixed in the working tree.
#
# The copy's status is read, because this function is a producer and every producer in
# scripts/check-docs.sh is checked for the reason stated there. It was the one that was not,
# and the consequence is worse here than a short count: an incomplete fixture makes every
# case below run against a tree that is missing files, and a case whose mutation target is
# among them reports ok about a check it never reached. Observed exactly that way — a
# tracked file deleted from the working tree but not staged leaves `git ls-files` naming it,
# tar exits non-zero on it, and this printed `tar: Cannot stat` once per case and then `all
# 18 cases hold`. `set -e` could not see it because the last command in this function is the
# `printf` that returns the path.
fixture() {
  local dir="$work/$1"
  rm -rf "$dir"
  mkdir -p "$dir"
  if ! git ls-files -z | xargs -0 tar -cf - | tar -xf - -C "$dir"; then
    printf 'check-docs-selftest: building the %s fixture failed, so every case would have run against an incomplete copy of the tree\n' "$1" >&2
    exit 1
  fi
  printf '%s' "$dir"
}

# The case runner, in one place: seventeen verbatim copies of this line preceded it. No
# anchor guard is needed here, unlike drop_map_row's — a mistyped path leaves the real
# script in place, the check passes, and the case fails loudly.
run_check() {
  (cd "$1" && bash scripts/check-docs.sh 2>&1) || true
}

# check <name> <wanted message> <output> [message that must be absent]
check() {
  local name="$1" want="$2" got="$3" unwanted="${4:-}"
  cases=$((cases + 1))
  if ! printf '%s' "$got" | grep -Fq "$want"; then
    printf 'FAIL  %s\n      wanted the message: %s\n      got: %s\n' "$name" "$want" "$got" >&2
    failures=$((failures + 1))
    return
  fi
  # A case that wants a complaint must not also see the success line. Asserting the
  # diagnostic catches a guard that stopped firing; it does not catch one that fires too
  # late. Measured: moving check-docs.sh's root-document test below the exit gate makes it
  # print the complaint *and* the success line and exit 0, and every case here still held.
  # Global rather than per-case because a new case inherits it, where a per-case flag is
  # something a new case can simply forget.
  if [ "$want" != "$OK_LINE" ] && printf '%s' "$got" | grep -Fq "$OK_LINE"; then
    printf 'FAIL  %s\n      the wanted complaint appeared beside the success line: %s\n' "$name" "$got" >&2
    failures=$((failures + 1))
    return
  fi
  # Per-case, because "this message must NOT appear" is a claim about one fixture rather
  # than a rule every case shares: a diagnosis can be correct and still name the wrong
  # cause.
  if [ -n "$unwanted" ] && printf '%s' "$got" | grep -Fq "$unwanted"; then
    printf 'FAIL  %s\n      this message should not have appeared: %s\n      got: %s\n' "$name" "$unwanted" "$got" >&2
    failures=$((failures + 1))
    return
  fi
  printf 'ok    %s\n' "$name"
}

# The three fixture edits below were python3, which made an interpreter nothing in
# mise.toml declares a dependency of `mise run check` — and of the half of it that fails
# most opaquely without one: a case dies mid-suite rather than reporting anything, which is
# what an absent python3 was measured doing here.
#
# awk rather than sed because two of the three needles below break sed, and one of the two
# breaks it silently: the `skipDirs` literal's `map[string]bool{…}` is a bracket expression,
# so sed substitutes nothing and exits 0, and a `docs/…` target ends the substitute command.
# (The third, a Markdown heading, holds no sed metacharacter — "every" was this sentence's
# own overreach.) awk's index() and substr() do not interpret a needle at all. Passed through
# the environment rather than through `-v`, which
# expands escape sequences in the value: no literal used here contains a backslash today,
# which is exactly the kind of thing that stops being true without anyone noticing.
#
# Each one refuses loudly when its anchor is absent, which is the half that makes a fixture
# worth trusting: an edit that silently missed proves nothing, and the case built on it goes
# on reporting ok about a mutation that never happened.
fixture_anchor_miss() {
  printf 'check-docs-selftest: fixture anchor miss: %s\n' "$1" >&2
  exit 1
}

# subst_once <file> <literal> <replacement> — replaces the first occurrence.
subst_once() {
  awk_old=$2 awk_new=$3 awk '
    BEGIN { old = ENVIRON["awk_old"]; repl = ENVIRON["awk_new"] }
    !hit && (p = index($0, old)) {
      $0 = substr($0, 1, p - 1) repl substr($0, p + length(old))
      hit = 1
    }
    { print }
    END { exit(hit ? 0 : 3) }
  ' "$1" > "$1.new" ||
    { rm -f "$1.new"; fixture_anchor_miss "$1 does not contain: $2"; }
  mv "$1.new" "$1"
}

# drop_map_row <AGENTS.md> <target> — deletes every Documentation Map row linking a target,
# which is what two cases below need.
#
# The row form is required, not just the filename, to keep the guarantee the orphan case was
# written for: it must delete a routing row, never prose that names the file. That
# requirement is not observable on this tree — every link of this shape is already a table
# row, measured — so dropping it changes nothing today and no case here can tell. It stays
# because the day a document links one of these outside the table, a filename-only filter
# would quietly delete that line too and the case would stop testing what it claims.
drop_map_row() {
  awk_target=$2 awk '
    BEGIN { row = "](" ENVIRON["awk_target"] ")" }
    substr($0, 1, 1) == "|" && index($0, row) { hit = 1; next }
    { print }
    END { exit(hit ? 0 : 3) }
  ' "$1" > "$1.new" ||
    { rm -f "$1.new"; fixture_anchor_miss "no Documentation Map row links $2"; }
  mv "$1.new" "$1"
}

# Cases below replace the extractor, and since the link and citation halves share one program
# they replace the same file.
#
# The guard is not borrowed caution: this said no guard was needed, on run_check's reasoning
# that a mistyped path leaves the real program in place, and a review measured that false.
# `cat` to a mistyped *filename* inside that directory leaves a second `package main` in it,
# so `go run` fails to compile, the producer's `|| fail` fires and both floors read 0 — which
# is every message the stub cases assert, so all of them keep passing while testing a compile
# error. Mistyping the *directory* is the loud half, because `cat` itself fails. Asymmetric,
# and the silent half is the one a rename inside this directory would hit.
stub_extractor() {
  local target="$1/scripts/docrefs/main.go"
  if [ ! -f "$target" ]; then
    fixture_anchor_miss "no $target to replace"
  fi
  cat > "$target"
}

# 1. Baseline. Without this, every case below could be satisfied by a script that always
#    fails, and the suite would look green while the check was useless.
dir=$(fixture baseline)
out=$(run_check "$dir")
check 'the working tree passes' "$OK_LINE" "$out"

# 2. The link predicate. No floor reaches this: the count rises, and stays above 50.
dir=$(fixture brokenlink)
printf '\n[deliberately broken](docs/NO-SUCH-FILE.md)\n' >> "$dir/README.md"
out=$(run_check "$dir")
check 'a broken link is named' 'link target does not exist' "$out"

# 3. The Map membership predicate, on its own routing row rather than any mention of the
#    filename — a substring test passed this because another row's prose names the file.
dir=$(fixture orphan)
drop_map_row "$dir/AGENTS.md" docs/SCOPE-MODEL.md
out=$(run_check "$dir")
check 'a doc whose routing row is gone is named' 'SCOPE-MODEL.md is not listed' "$out"

# 4. A link inside a code span is an example, not a link. Both backtick runs, because a
#    single-backtick pattern left the double-backtick idiom exposed and turned correct
#    prose into a gate failure.
dir=$(fixture codespan)
printf '\nSee `[X.md](X.md)` and ``[Y.md](Y.md)`` forms.\n' >> "$dir/README.md"
out=$(run_check "$dir")
check 'links inside code spans are ignored' "$OK_LINE" "$out"

# 5. Degenerate input, docs side. Every floor in check-docs.sh exists because a walk that
#    collapses otherwise reports a clean run, and until these two cases the floors were
#    verified by hand mutation and recorded in a commit message — the state
#    docs/ROADMAP.md's smoke-guards entry describes. The assertion names the floor's own
#    message rather than any failure, because an empty docs/ breaks Map links too.
dir=$(fixture emptydocs)
rm -f "$dir"/docs/*.md
out=$(run_check "$dir")
check 'an empty docs/ trips the walk floor' 'walked only 0 files' "$out"

# 6. Degenerate input, link side: an extractor that emits nothing must not read as a
#    clean run. Replacing it is the cheapest way to reach that floor, and it is exactly
#    what a mistyped glob or an over-broad prune would do in practice.
#
#    This mutation is shared with the section-floor case further down, because one program
#    emits both kinds. Two cases rather than one because two floors sit behind it and each
#    needs its own assertion — the trip/assert distinction scripts/smoke-run-selftest.sh
#    keeps: one mutation tripping two floors covers one of them per case that names one.
dir=$(fixture nolinks)
stub_extractor "$dir" <<'EMPTY'
package main

func main() {}
EMPTY
out=$(run_check "$dir")
check 'an extractor emitting nothing trips the link floor' 'resolved only 0 relative links' "$out"

# 7. Degenerate input, Map side. Renaming the heading the Map walk anchors on reaches two
#    floors with one mutation — the table-row count and the link-extraction count — which
#    is why this is one case rather than two. It also proves the heading-absent branch is
#    reachable at all: an unguarded `grep` there used to kill the script before it ran.
dir=$(fixture nomap)
subst_once "$dir/AGENTS.md" '## Documentation Map' '## Doc Map'
out=$(run_check "$dir")
check 'renaming the Map heading trips its extraction floor' 'extracted only 0 docs/ links' "$out"

# 8. `CLAUDE.md`, which no link reaches. No floor is involved in this case or in the
#    root-document cases after it — a floor bounds a walk, and these are single tests.
#    Referring to them that way rather than by number, because inserting a case renumbers
#    every reference to one and nothing here would report that.
dir=$(fixture noclaudemd)
rm -f "$dir/CLAUDE.md"
out=$(run_check "$dir")
check 'a deleted CLAUDE.md is named' 'CLAUDE.md is missing, empty, or not a regular file' "$out"

# 9. The case that put README.md in that loop, and the only one of the three the link walk
#    looked like it covered: README.md is reachable from the Map's own row and nothing else,
#    so removing the row in the same change removes the coverage with it. Measured passing
#    with `ok — 13 docs in the Map, 278 links resolved` before the loop existed.
dir=$(fixture noreadme)
rm -f "$dir/README.md"
drop_map_row "$dir/AGENTS.md" README.md
out=$(run_check "$dir")
check 'README.md deleted with its only Map row is named' 'README.md is missing, empty, or not a regular file' "$out"

# 10. Emptied rather than deleted, which `-f` alone passed. Why empty is as bad as absent is
#     stated once, above the predicate in scripts/check-docs.sh.
dir=$(fixture emptyclaudemd)
: > "$dir/CLAUDE.md"
out=$(run_check "$dir")
check 'an emptied CLAUDE.md is named' 'CLAUDE.md is missing, empty, or not a regular file' "$out"

# 11. A root document replaced by a directory of the same name. This is the only case that
#     pins `-f` in that loop: deleted is caught by `-e` too, and empty by `-s`, so weakening
#     `-f` to `-e` is invisible without it. CLAUDE.md is used because nothing else reads it,
#     which keeps the assertion on the loop rather than on a second check firing too.
dir=$(fixture claudemddir)
rm -f "$dir/CLAUDE.md"
mkdir "$dir/CLAUDE.md"
out=$(run_check "$dir")
check 'a root doc replaced by a directory is named' 'CLAUDE.md is missing, empty, or not a regular file' "$out"

# 12. A required document under docs/ replaced by a directory of the same name. This reaches
#     the link walk's target test rather than the loop above, for the reason recorded beside
#     that test in scripts/check-docs.sh. The fourth argument keeps the diagnosis honest:
#     the run must name the broken target, not a dead extractor, which is a correct failure
#     for the wrong reason that no other case here can tell apart.
#
#     What it does NOT pin is any skip inside the extractor, which is what this comment and
#     the extractor's own said until a review measured it: WalkDir reports a `.md` directory
#     through d.IsDir() and never reads it, so making the extractor fatal on a read error
#     leaves this case green. The case below is the one that pins that.
dir=$(fixture docsdir)
rm -f "$dir/docs/PRODUCT.md"
mkdir "$dir/docs/PRODUCT.md"
out=$(run_check "$dir")
check 'a required doc replaced by a directory is named' 'link target does not exist: docs/PRODUCT.md' "$out" \
  'the reference extractor exited non-zero'

# 13. A document the extractor cannot read. The port turned the Python's crash into a silent
#     skip, so every reference in that file went unchecked while the gate printed ok — a
#     fail-open, and the reason the read is fatal again. Mode 000 rather than a directory
#     because a directory never reaches the read at all, which is what made the case above
#     look like it pinned this.
#
#     Running as root would defeat the fixture; the case then fails loudly for want of the
#     message rather than passing, which is the direction to fail in.
dir=$(fixture unreadabledoc)
chmod 000 "$dir/docs/SECURITY.md"
out=$(run_check "$dir")
check 'a document the extractor cannot read is named' 'the reference extractor exited non-zero' "$out"

# 14. The producer of the docs/ walk failing outright, which is the same class as the case
#     below at check-docs.sh's other captured producer. Removing the directory is the cheap
#     way to make `find docs` exit non-zero. It trips a great deal else; the assertion is on
#     the producer's status alone.
#
#     The third producer, inside check_domain_index, stays unpinned: it runs only when a
#     docs/<domain>/ directory exists, and none does, so the function self-no-ops and no
#     fixture can reach it without inventing a domain tree.
dir=$(fixture nodocsdir)
rm -rf "$dir/docs"
out=$(run_check "$dir")
check 'a docs/ walk that fails outright is named' 'walking docs/ failed' "$out"

# 15. A partially collapsed walk. The empty-extractor cases cover one that emits nothing,
#     which the floors catch; this covers one that emits some and then fails, which no
#     floor can see once the count clears it. The assertion names the extractor's exit
#     status, not a floor — this fixture trips both floors, and neither is what it is for.
#
#     One case where there were two. Before the halves shared a producer there was a
#     link-side death and a section-side death to pin; now there is one status to read, so
#     the second was the same assertion written twice rather than a second guard.
dir=$(fixture truncatedwalk)
stub_extractor "$dir" <<'TRUNC'
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("link\tREADME.md\tdocs/CLI.md")
	fmt.Println("cite\tREADME.md\tdocs/CLI.md\tresolves\tkae add")
	os.Exit(3)
}
TRUNC
out=$(run_check "$dir")
check 'an extractor that dies mid-walk is named' 'the reference extractor exited non-zero' "$out"

# 16. The section predicate. `Tier-1 tools` is the citation this repository actually
#     shipped into docs/ROADMAP.md, against a file whose only tier section is
#     `## Tier-2 tools`, so the fixture is the defect rather than an invented one. No
#     floor reaches this: the count rises by one and stays far above 100.
dir=$(fixture phantomsection)
printf '\nSee [ROADMAP.md](docs/ROADMAP.md) § Tier-1 tools for the mapping.\n' >> "$dir/README.md"
out=$(run_check "$dir")
check 'a citation naming a section that does not exist is named' \
  'which that file declares no section for' "$out"

# 17. Degenerate input, section side. The same mutation as the link-floor case above — one
#     program, one file to replace — and it is the one that matters most here: the
#     predicate passes silently on an empty walk, because "no citation was absent" is true
#     of no citations at all.
dir=$(fixture nosections)
stub_extractor "$dir" <<'EMPTY'
package main

func main() {}
EMPTY
out=$(run_check "$dir")
check 'an extractor emitting nothing trips the section floor' \
  'checked only 0 section citations' "$out"

# 18. A `§` citation inside a fenced block is an illustration, not a citation — the
#     section-side counterpart of case 4, and the case that would have caught the strip
#     accepting only a column-0 fence. Indented, because the one file in this tree whose
#     only fenced block is indented is what that defect was measured on: the citation was
#     reported absent (the gate failing on a code block) and a bold label inside the same
#     block became a section name a phantom resolved against.
dir=$(fixture fencedsection)
cat >> "$dir/README.md" <<'FENCED'

  ```bash
  # see docs/CLI.md § Zznosuchsection for the flags
  - **Zzfencedlabel** is not a section
  ```
FENCED
out=$(run_check "$dir")
check 'a citation inside a fenced block is ignored' "$OK_LINE" "$out"

# 19. A kind check-docs.sh does not know. With two kinds sharing a producer, an unrecognised
#     one is a whole walk going unchecked while every count stays plausible, which is why
#     that arm exists — and nothing exercised it, because no real row carries a third kind.
#     Measured before this case: emitting an extra kind with the arm intact fails the gate,
#     and with the arm deleted the gate prints ok and every case here still held.
dir=$(fixture unknownkind)
stub_extractor "$dir" <<'THIRDKIND'
package main

import "fmt"

func main() {
	fmt.Println("note\tREADME.md\tdocs/CLI.md")
}
THIRDKIND
out=$(run_check "$dir")
check 'a reference kind the gate does not know is named' \
  'unrecognised reference kind from the extractor: note' "$out"

# 20. The half of the collapse the floor cannot see. Pruning one directory, or dropping
#     one suffix, stops checking a whole class of citation and still clears any floor this
#     walk could carry — with `internal` pruned the count drops by the Go share and stays far
#     above the floor, which is the whole point; the number is not written down because the
#     pair it was first written as had already gone stale. Nothing pinned
#     that guard until this case: disabling it outright left every other case green. The
#     mutation is a directory prune rather than a suffix drop because the suffix half was
#     the one already stated in a comment, and this is the half that was re-broken by a
#     citation landing in scripts/ for the first time.
dir=$(fixture prunedgo)
subst_once "$dir/scripts/docrefs/main.go" \
  'var skipDirs = map[string]bool{".git": true, "dist": true}' \
  'var skipDirs = map[string]bool{".git": true, "dist": true, "internal": true}'
out=$(run_check "$dir")
check 'a directory prune that loses every Go citation is named' \
  'section citations were found in markdown' "$out"

if [ "$failures" -gt 0 ]; then
  printf 'check-docs-selftest: %s case(s) failed\n' "$failures" >&2
  exit 1
fi

# Two-directional, the way scripts/smoke-run-selftest.sh's EXPECTED_GUARDS is: a floor would
# let a case be added and then silently deleted back to the count before it. Adding a case
# has to bump this, and that is the point. Written without naming either count, because the
# sentence that did name them was left behind by the first bump.
EXPECTED_CASES=20
if [ "$cases" -ne "$EXPECTED_CASES" ]; then
  printf 'check-docs-selftest: %s case(s) ran, expected %s\n' "$cases" "$EXPECTED_CASES" >&2
  exit 1
fi

printf 'check-docs-selftest: all %s cases hold\n' "$cases"
