#!/usr/bin/env bash
# Report every quantity a change writes next to a claim, so each can be triaged by hand.
#
# Usage: bash scripts/sweep-quantities.sh [<base>]
#   <base> defaults to `main`. The diff runs from the merge base to the working tree, so
#   committed and uncommitted changes are both swept — untracked files are not, because
#   `git diff` does not see them, and a brand-new document is therefore invisible here.
#
# Report-only, and deliberately not in `mise run check`: every hit needs a human decision,
# and a quantity that can go stale is not distinguishable from one that cannot by anything
# this script can compute. A report exits 0 however many hits it holds; the exits that are
# not 0 are the `fail` calls, and each says which control or precondition it is —
# `grep -n 'fail "'` lists them. Triage each hit by whether the quantity
# **can go stale**, not by which file it points at — a count of something in its own file
# goes stale just as easily, which a commit here proved by un-striking one of the struck
# entries in docs/ROADMAP.md while writing "all ten" of them in the same diff. The
# `++ b/...` lines are kept so you can tell which file a hit came from.
#
# AGENTS.md § Documentation Update Checklist is normative for the rule this serves: the fix
# that holds is not to write the number, it is to write the derivation. This is only the
# net for the ones already there.
#
# # Why the pipeline is shaped the way it is
#
# The `awk` joins each line to its predecessor and the `{0,3}` allows words between the
# number and the noun **within that two-line window** — a gap inside the word budget but
# spread over three lines still escapes — because earlier versions returned zero on a
# quantity that wrapped across lines and on one whose noun was not adjacent to its number.
# Both are the fixtures in `controls` below, run before every sweep rather than described.
#
# The intervening group is `[^ ]+` rather than a letters-only class because the files this
# runs over write emphasis and code spans mid-sentence ("the **two** commands"), and a
# letters-only class let exactly those through — which is why the second fixture carries an
# emphasized word. One example does not pin a character class.
#
# # The ways a clean run has already lied
#
# **A negative with no positive control.** `POSITIVE_CONTROL_COMMIT` writes a known bad
# count, and a run that stops reporting it means the pattern is broken rather than the tree
# clean. It is fired through `git show`, not `git diff main...<commit>`: that was the first
# form and it went vacuous the day the branch merged, because a range against an ancestor
# is empty and an empty input greps clean. A positive control a merge can silence is not
# one.
#
# **The empty range.** `main...HEAD` sees nothing uncommitted, so a run on a branch with no
# commits yet reports clean about a working tree full of changes. This script diffs the
# merge base against the working tree, which covers both tracked halves, and refuses to
# call a run clean when that diff adds no lines at all.
#
# **A fixture that no longer fires.** Every fixture runs through the same function the
# sweep uses, before the sweep, and a failure there is fatal.
#
# **A report whose only hits are this file.** The pattern matches its own defining
# sentences and its own fixtures, so a run over a change to this script reports itself. A
# run whose hits are all this script's header and source is not a clean run and is not a
# dirty one; it is a run that has told you nothing, and the same was true of the prose this
# replaced.
#
# # What it cannot reach
#
# It is a net, not a proof. The noun and number lists are enumerations, and every
# enumeration of them has turned out short so far; each addition was found a different way
# (`thirteen` from this repository's own prose, the ordinals after a diff wrote "a third
# site" and swept clean, `assertions` from reading a hit returned for a different quantity
# on the same line). Anything larger than the listed words is written as a digit, which
# `[0-9]+` covers.
#
# The class it cannot reach at all is **a quantity on a line the diff does not touch**, and
# that is the original defect's own shape: one commit merged two commands and the sentence
# counting them sat unchanged in another file. The positive-control commit was caught only
# because it happened to write that sentence as well. No widening of an added-lines sweep
# reaches this; the mechanism that would is the claim-reconciliation stage
# scripts/docscan/main.go's header names, filed in docs/ROADMAP.md. For the quantity class,
# writing the derivation instead of the number is still the only thing that covers it.
#
# # The sibling net that was refused, so it is not re-proposed
#
# AGENTS.md's closure rule — an absolute about what *is* must be written as its derivation
# or deleted — looks like it wants a net of this shape too. It cannot have one, and that
# is the difference worth keeping. Here, the written number is itself
# the artifact that goes stale, so the defect and the vocabulary coincide. There, a closure
# word is ordinary vocabulary in this tree, and what goes stale is a measurement that was
# never run — the discriminating features are the subject's ownership and a derivation's
# *absence*, and no pattern over words can see an absence.

set -euo pipefail

BASE=${1:-main}

# The commit whose docs/ROADMAP.md line says "the two commands in CONTEXT.md § Not
# converged" after a later commit had merged them into one.
POSITIVE_CONTROL_COMMIT=89341f4

QUANTITY_RE='(^|[^A-Za-z*`])\*{0,2}`?(both|one|two|three|four|five|six|seven|eight|nine|ten|thirteen'\
'|first|second|third|fourth|fifth|sixth|seventh|[0-9]+)`?\*{0,2}( +[^ ]+){0,3} +'\
'\*{0,2}(commands?|terms?|rules?|rows?|entries|entry|sections?|bullets?|places?|copies|copy|files?|lines?|names?|pairs?|sites?|tools?|assertions?|documents?|cells?)\b'

MARKER_RE='^\+\+ b/'

# `sweep markers` adds the file-attribution arm; `sweep` alone matches only quantities.
# The two are separate because a record carries the joined pair `prev current`, so a
# quantity on a file's *first* added line arrives in a record that begins with the
# attribution marker. Filtering the marker out of the combined output therefore discarded
# that quantity — silently, and for the commonest shape there is, a single line added to a
# single file. It reached this script because the arm it replaced was merely noisy.
sweep() {
  local re=$QUANTITY_RE
  if [ "${1:-}" = markers ]; then
    re="$MARKER_RE|$re"
  fi
  grep '^+' |
    sed 's/^+//' |
    awk '{print prev" "$0; prev=$0}' |
    grep -iE "$re" ||
    true
}

fail() {
  printf 'sweep-quantities: %s\n' "$1" >&2
  exit 1
}

# Each fixture is a shape the pattern got wrong once. They pin those shapes and no more:
# a review measured that a proper subset of the noun list, of the number list, or of the
# ordinals survives all of them, so this is a floor on the pattern and not a description of
# it. The diagnostics name what was observed rather than a cause, because dropping a noun
# these fixtures happen to use trips the fixture that uses it and says nothing about why.
fixture() {
  local name=$1 want=$2 input=$3 got
  got=$(printf '%b' "$input" | sweep markers | wc -l | tr -d ' ')
  if [ "$got" != "$want" ]; then
    fail "the $name fixture yielded $got records, not $want, so the pattern no longer reads that shape"
  fi
}

controls() {
  local got
  # A quantity split across two lines: the awk join is what sees it.
  fixture 'wrapped-quantity' 1 '+the **two**\n+commands in X\n'
  # A word between the number and its noun, inside the two-line window.
  fixture 'non-adjacent' 1 '+one of ten **struck** entries here\n'
  # A digit rather than a word, and a noun the list gained late; the header names both as
  # load-bearing and neither was pinned until a review said so.
  fixture 'digit-and-late-noun' 1 '+the 42 assertions below\n'
  # The file attribution the report is read with. Two lines, because the marker is matched
  # as the *predecessor* the awk joins on — a one-line fixture leaves it as the current line
  # with an empty prev, and the record then starts with a space and matches nothing, which
  # is how this fixture failed on the run that added it. What it does not cover is a git
  # config that changes the prefix; the diff above forces those off instead.
  fixture 'file-attribution' 1 '+++ b/x.md\n+ordinary prose\n'
  # Fatal rather than skipped when the commit is absent. Skipping was the first form, and a
  # mutation of this constant to a commit that does not exist then produced a warning and a
  # clean sweep — a typo and a shallow clone are indistinguishable here, so the arm that
  # tolerates one tolerates the other, and the positive control silently stops running.
  if ! git cat-file -e "$POSITIVE_CONTROL_COMMIT^{commit}" 2>/dev/null; then
    fail "the positive control $POSITIVE_CONTROL_COMMIT is not in this clone, so a clean sweep would prove nothing"
  fi
  got=$(git show "$POSITIVE_CONTROL_COMMIT" | sweep | grep -c 'Not converged' || true)
  if [ "$got" = "0" ]; then
    fail "the positive control $POSITIVE_CONTROL_COMMIT no longer reports its known bad count: the pattern is broken"
  fi
}

controls

# Named rather than left to git's own "ambiguous argument" fatal, which reads as a bug in
# this script: a clone that checked out a branch without fetching the base hits it, and did
# so while this script was being mutation-tested.
if ! git rev-parse --verify --quiet "$BASE^{commit}" >/dev/null; then
  fail "no commit named '$BASE' in this clone, so there is nothing to diff against"
fi

# One diff, from the merge base to the working tree, rather than a committed range plus an
# uncommitted one. Two earlier forms were wrong in opposite directions: summing
# `$BASE...HEAD` with `git diff $BASE` counted every committed line twice, and summing it
# with `git diff HEAD` reported a line as added after a later edit had removed it again,
# so a hit already fixed in the working tree kept coming back. The merge base rather than
# `$BASE` itself is what keeps this right when the base has moved ahead.
if ! base=$(git merge-base "$BASE" HEAD 2>/dev/null); then
  fail "no merge base between '$BASE' and HEAD, so there is no range to sweep"
fi
# The prefixes are forced rather than assumed: `diff.noprefix` or `diff.mnemonicPrefix` in
# a user's config turns `+++ b/x` into `+++ x` or `+++ w/x`, which silently retires the
# attribution arm and confuses a header line with content. The fixture below cannot see
# that, because it feeds a fixed string — measured, and the reason this line exists.
diffed=$(git -c diff.noprefix=false -c diff.mnemonicPrefix=false diff "$base")
# `^+++ ` is excluded before counting: a diff header starts with `+` too, so counting it
# inflated this figure by one per file touched, and a deletion-only change reported one
# added line and skipped the guard below. A false quantity printed by the quantity net,
# found by a review after a different false quantity had already been fixed here. `b/` is
# part of the match because the prefixes are forced above, which keeps a document line
# whose own text begins `+++ ` from being read as a header.
added=$(printf '%s\n' "$diffed" | grep -v '^+++ b/' | grep -c '^+' || true)

if [ "$added" = "0" ]; then
  printf 'sweep-quantities: nothing since the merge base with %s adds a line, so this run proved nothing\n' \
    "$BASE" >&2
  exit 0
fi

hits=$(printf '%s\n' "$diffed" | sweep markers)

# Attribution is not a finding, and every change that adds a line produces at least one
# attribution record — so deciding on the printed records made the clean report below
# unreachable and handed back a report on every run, which is how a report-only tool stops
# being read. The decision runs the pattern again *without* the marker arm rather than
# filtering the marker out of these records, because a quantity on a file's first added
# line arrives inside a record that begins with one.
quantities=$(printf '%s\n' "$diffed" | sweep | wc -l | tr -d ' ')

# `hits` empty is a separate case rather than a subset: `grep -c` over one empty line
# returns 1, so `quantities` cannot see it. Reachable under a config the diff above now
# forces off, and cheap to keep.
if [ -z "$hits" ] || [ "$quantities" = "0" ]; then
  printf 'sweep-quantities: ok — %s added lines, no quantity beside a claim\n' "$added"
  exit 0
fi

printf 'sweep-quantities: %s added lines; triage each record by whether its quantity can go stale\n' "$added"
printf '%s\n' "$hits"
exit 0
