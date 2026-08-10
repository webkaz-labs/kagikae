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
  if printf '%s' "$got" | grep -Fq "$want"; then
    printf 'ok    %s\n' "$name"
  else
    printf 'FAIL  %s\n      wanted the message: %s\n      got: %s\n' "$name" "$want" "$got" >&2
    failures=$((failures + 1))
  fi
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

if [ "$failures" -gt 0 ]; then
  printf 'check-docs-selftest: %s case(s) failed\n' "$failures" >&2
  exit 1
fi

if [ "$cases" -lt 4 ]; then
  fail_count=$((cases))
  printf 'check-docs-selftest: only %s case(s) ran, fewer than this file defines\n' "$fail_count" >&2
  exit 1
fi

printf 'check-docs-selftest: all %s cases hold\n' "$cases"
