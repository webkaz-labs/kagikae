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

# The fixture is the working tree's tracked files, so this needs a git work tree. Say so
# rather than letting a case die inside python on a missing file, which is what happened
# when this was first run against an extracted copy of the tree.
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
fixture() {
  local dir="$work/$1"
  rm -rf "$dir"
  mkdir -p "$dir"
  git ls-files -z | xargs -0 tar -cf - | tar -xf - -C "$dir"
  printf '%s' "$dir"
}

check() {
  local name="$1" want="$2" got="$3"
  cases=$((cases + 1))
  if ! printf '%s' "$got" | grep -Fq "$want"; then
    printf 'FAIL  %s\n      wanted the message: %s\n      got: %s\n' "$name" "$want" "$got" >&2
    failures=$((failures + 1))
    return
  fi
  # A case that wants a complaint must not also see the success line. Asserting the
  # diagnostic catches a guard that stopped firing; it does not catch one that fires too
  # late. Measured: moving check-docs.sh's root-document test below the exit gate makes it
  # print the complaint *and* `check-docs: ok` and exit 0, and every case here still held.
  if [ "$want" != "check-docs: ok" ] && printf '%s' "$got" | grep -Fq 'check-docs: ok'; then
    printf 'FAIL  %s\n      the wanted complaint appeared beside the success line: %s\n' "$name" "$got" >&2
    failures=$((failures + 1))
    return
  fi
  printf 'ok    %s\n' "$name"
}

# 1. Baseline. Without this, every case below could be satisfied by a script that always
#    fails, and the suite would look green while the check was useless.
dir=$(fixture baseline)
out=$( (cd "$dir" && bash scripts/check-docs.sh 2>&1) || true)
check 'the working tree passes' 'check-docs: ok' "$out"

# 2. The link predicate. No floor reaches this: the count rises, and stays above 50.
dir=$(fixture brokenlink)
printf '\n[deliberately broken](docs/NO-SUCH-FILE.md)\n' >> "$dir/README.md"
out=$( (cd "$dir" && bash scripts/check-docs.sh 2>&1) || true)
check 'a broken link is named' 'link target does not exist' "$out"

# 3. The Map membership predicate, on its own routing row rather than any mention of the
#    filename — a substring test passed this because another row's prose names the file.
dir=$(fixture orphan)
python3 - "$dir/AGENTS.md" <<'PY'
import pathlib, re, sys
p = pathlib.Path(sys.argv[1])
text = p.read_text()
row = re.compile(r'^\| \[docs/SCOPE-MODEL\.md\]\(docs/SCOPE-MODEL\.md\) \|.*$', re.M)
if not row.search(text):
    raise SystemExit('fixture anchor miss: no SCOPE-MODEL.md routing row to delete')
p.write_text(row.sub('', text))
PY
out=$( (cd "$dir" && bash scripts/check-docs.sh 2>&1) || true)
check 'a doc whose routing row is gone is named' 'SCOPE-MODEL.md is not listed' "$out"

# 4. A link inside a code span is an example, not a link. Both backtick runs, because a
#    single-backtick pattern left the double-backtick idiom exposed and turned correct
#    prose into a gate failure.
dir=$(fixture codespan)
printf '\nSee `[X.md](X.md)` and ``[Y.md](Y.md)`` forms.\n' >> "$dir/README.md"
out=$( (cd "$dir" && bash scripts/check-docs.sh 2>&1) || true)
check 'links inside code spans are ignored' 'check-docs: ok' "$out"

# 5. Degenerate input, docs side. Every floor in check-docs.sh exists because a walk that
#    collapses otherwise reports a clean run, and until these two cases the floors were
#    verified by hand mutation and recorded in a commit message — the state
#    docs/ROADMAP.md's smoke-guards entry describes. The assertion names the floor's own
#    message rather than any failure, because an empty docs/ breaks Map links too.
dir=$(fixture emptydocs)
rm -f "$dir"/docs/*.md
out=$( (cd "$dir" && bash scripts/check-docs.sh 2>&1) || true)
check 'an empty docs/ trips the walk floor' 'walked only 0 files' "$out"

# 6. Degenerate input, link side: an extractor that emits nothing must not read as a
#    clean run. Replacing it is the cheapest way to reach that floor, and it is exactly
#    what a mistyped glob or an over-broad prune would do in practice.
dir=$(fixture nolinks)
printf '#!/usr/bin/env python3\n' > "$dir/scripts/docs_links.py"
out=$( (cd "$dir" && bash scripts/check-docs.sh 2>&1) || true)
check 'an extractor emitting nothing trips the link floor' 'resolved only 0 relative links' "$out"

# 7. Degenerate input, Map side. Renaming the heading the Map walk anchors on reaches two
#    floors with one mutation — the table-row count and the link-extraction count — which
#    is why this is one case rather than two. It also proves the heading-absent branch is
#    reachable at all: an unguarded `grep` there used to kill the script before it ran.
dir=$(fixture nomap)
python3 - "$dir/AGENTS.md" <<'RENAME'
import pathlib, sys
p = pathlib.Path(sys.argv[1])
text = p.read_text()
if '## Documentation Map' not in text:
    raise SystemExit('fixture anchor miss: no "## Documentation Map" heading to rename')
p.write_text(text.replace('## Documentation Map', '## Doc Map', 1))
RENAME
out=$( (cd "$dir" && bash scripts/check-docs.sh 2>&1) || true)
check 'renaming the Map heading trips its extraction floor' 'extracted only 0 docs/ links' "$out"

# 8. `CLAUDE.md`, which no link reaches. No floor is involved in this case or in the
#    root-document cases after it — a floor bounds a walk, and these are single tests.
#    Referring to them that way rather than by number, because inserting a case renumbers
#    every reference to one and nothing here would report that.
dir=$(fixture noclaudemd)
rm -f "$dir/CLAUDE.md"
out=$( (cd "$dir" && bash scripts/check-docs.sh 2>&1) || true)
check 'a deleted CLAUDE.md is named' 'CLAUDE.md is missing, empty, or not a regular file' "$out"

# 9. The case that put README.md in that loop, and the only one of the three the link walk
#    looked like it covered: README.md is reachable from the Map's own row and nothing else,
#    so removing the row in the same change removes the coverage with it. Measured passing
#    with `ok — 13 docs in the Map, 278 links resolved` before the loop existed.
dir=$(fixture noreadme)
rm -f "$dir/README.md"
python3 - "$dir/AGENTS.md" <<'ROW'
import pathlib, sys
p = pathlib.Path(sys.argv[1])
lines = p.read_text().splitlines(True)
kept = [line for line in lines if '](README.md)' not in line]
if len(kept) == len(lines):
    raise SystemExit('fixture anchor miss: no Documentation Map row links README.md')
p.write_text(''.join(kept))
ROW
out=$( (cd "$dir" && bash scripts/check-docs.sh 2>&1) || true)
check 'README.md deleted with its only Map row is named' 'README.md is missing, empty, or not a regular file' "$out"

# 10. Emptied rather than deleted. `-f` alone passed this, and an empty CLAUDE.md removes
#     every project rule exactly as thoroughly as a deleted one.
dir=$(fixture emptyclaudemd)
: > "$dir/CLAUDE.md"
out=$( (cd "$dir" && bash scripts/check-docs.sh 2>&1) || true)
check 'an emptied CLAUDE.md is named' 'CLAUDE.md is missing, empty, or not a regular file' "$out"

# 11. A root document replaced by a directory of the same name. This is the only case that
#     pins `-f` in that loop: deleted is caught by `-e` too, and empty by `-s`, so weakening
#     `-f` to `-e` is invisible without it. CLAUDE.md is used because nothing else reads it,
#     which keeps the assertion on the loop rather than on a second check firing too.
dir=$(fixture claudemddir)
rm -f "$dir/CLAUDE.md"
mkdir "$dir/CLAUDE.md"
out=$( (cd "$dir" && bash scripts/check-docs.sh 2>&1) || true)
check 'a root doc replaced by a directory is named' 'CLAUDE.md is missing, empty, or not a regular file' "$out"

# 12. A required document under docs/ replaced by a directory of the same name. This reaches the link
#     walk's target test rather than the loop above: `-e` is satisfied by a directory, and
#     this was measured reporting `ok — 12 docs in the Map, 121 links resolved` with a
#     required document gone. It also proves docs_links.py skips a non-file `*.md` instead
#     of dying on it.
dir=$(fixture docsdir)
rm -f "$dir/docs/PRODUCT.md"
mkdir "$dir/docs/PRODUCT.md"
out=$( (cd "$dir" && bash scripts/check-docs.sh 2>&1) || true)
check 'a required doc replaced by a directory is named' 'link target does not exist: docs/PRODUCT.md' "$out"

# 13. A partially collapsed walk. The empty-extractor case above covers one that emits
#     nothing, which the floor catches; this covers one that emits some and then fails,
#     which the floor cannot see once the count clears it. The assertion names the
#     extractor's exit status, not the floor — this fixture trips both, and only one of them
#     is what this case is for.
dir=$(fixture truncatedlinks)
cat > "$dir/scripts/docs_links.py" <<'TRUNC'
#!/usr/bin/env python3
print("README.md\tdocs/CLI.md")
raise SystemExit(3)
TRUNC
out=$( (cd "$dir" && bash scripts/check-docs.sh 2>&1) || true)
check 'an extractor that dies mid-walk is named' 'the link extractor exited non-zero' "$out"

if [ "$failures" -gt 0 ]; then
  printf 'check-docs-selftest: %s case(s) failed\n' "$failures" >&2
  exit 1
fi

# Two-directional, the way scripts/smoke-run-selftest.sh's EXPECTED_GUARDS is: a floor
# would let a fifth case be added and then silently deleted back down to four. Adding a
# case has to bump this, and that is the point.
EXPECTED_CASES=13
if [ "$cases" -ne "$EXPECTED_CASES" ]; then
  printf 'check-docs-selftest: %s case(s) ran, expected %s\n' "$cases" "$EXPECTED_CASES" >&2
  exit 1
fi

printf 'check-docs-selftest: all %s cases hold\n' "$cases"
