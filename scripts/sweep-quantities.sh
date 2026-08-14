#!/usr/bin/env bash
# Report every quantity a change writes next to a claim, so each can be triaged by hand.
#
# Usage: bash scripts/sweep-quantities.sh [<base>]
#   <base> defaults to `main`. Before the first commit on a branch pass nothing and the
#   working tree is swept as well; see "the empty range" below for why that matters.
#
# Report-only. It always exits 0 and is deliberately not in `mise run check`: every hit
# needs a human decision, and a quantity that can go stale is not distinguishable from one
# that cannot by anything this script can compute. Triage each hit by whether the quantity
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
# # The three ways a clean run has already lied
#
# **A negative with no positive control.** `POSITIVE_CONTROL_COMMIT` writes a known bad
# count, and a run that stops reporting it means the pattern is broken rather than the tree
# clean. It is fired through `git show`, not `git diff main...<commit>`: that was the first
# form and it went vacuous the day the branch merged, because a range against an ancestor
# is empty and an empty input greps clean. A positive control a merge can silence is not
# one.
#
# **The empty range.** `main...HEAD` sees nothing uncommitted, so a run on a branch with no
# commits yet reports clean about a working tree full of changes. This script sweeps the
# committed range and the working tree both, and refuses to call a run clean when the two
# together added no lines at all.
#
# **A fixture that no longer fires.** The two wrapped/non-adjacent shapes are run through
# the same function the sweep uses, before the sweep, and a failure here is fatal — the
# only case where this script does not exit 0.
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
# or deleted — looks like it wants a net of this shape too. One was built and measured on
# 2026-08-14, at a broad word width and at a narrowed negative-existential one, over the
# branch that added the rule. Both fired on their positive control and stayed silent on a
# derivation, and both also fired on ordinary prose throughout; the narrow width returned no
# live instance at all. Two readers counted its records differently and neither count is
# recorded, because the count was never the finding: neither width separated the defect from
# the background, and a third was not tried.
#
# It cannot, and that is the difference worth keeping. Here, the written number is itself
# the artifact that goes stale, so the defect and the vocabulary coincide. There, a closure
# word is ordinary vocabulary in this tree, and what goes stale is a measurement that was
# never run — the discriminating features are the subject's ownership and a derivation's
# *absence*, and no pattern over words can see an absence.

set -euo pipefail

BASE=${1:-main}

# The commit whose docs/ROADMAP.md line says "the two commands in CONTEXT.md § Not
# converged" after a later commit had merged them into one.
POSITIVE_CONTROL_COMMIT=89341f4

sweep() {
  grep '^+' |
    sed 's/^+//' |
    awk '{print prev" "$0; prev=$0}' |
    grep -iE \
      '^\+\+ b/|(^|[^A-Za-z*`])\*{0,2}`?(both|one|two|three|four|five|six|seven|eight|nine|ten|thirteen'\
'|first|second|third|fourth|fifth|sixth|seventh|[0-9]+)`?\*{0,2}( +[^ ]+){0,3} +'\
'\*{0,2}(commands?|terms?|rules?|rows?|entries|entry|sections?|bullets?|places?|copies|copy|files?|lines?|names?|pairs?|sites?|tools?|assertions?)\b' ||
    true
}

fail() {
  printf 'sweep-quantities: %s\n' "$1" >&2
  exit 1
}

controls() {
  local got
  got=$(printf '+the **two**\n+commands in X\n' | sweep | wc -l | tr -d ' ')
  if [ "$got" != "1" ]; then
    fail "the wrapped-quantity fixture yielded $got records, not 1: the pattern no longer joins lines"
  fi
  got=$(printf '+one of ten **struck** entries here\n' | sweep | wc -l | tr -d ' ')
  if [ "$got" != "1" ]; then
    fail "the non-adjacent fixture yielded $got records, not 1: the pattern no longer allows words between a number and its noun"
  fi
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

# `$BASE...HEAD` and `git diff HEAD` partition the change; `git diff $BASE` would be the
# same lines a second time, which double-counted every record and doubled the added-line
# figure this script prints — a false quantity, printed by the quantity net, on its first
# real run.
committed=$(git diff "$BASE...HEAD")
uncommitted=$(git diff HEAD)
added=$(printf '%s\n%s\n' "$committed" "$uncommitted" | grep -c '^+' || true)

if [ "$added" = "0" ]; then
  printf 'sweep-quantities: %s...HEAD and the working tree added no lines, so this run proved nothing\n' \
    "$BASE" >&2
  exit 0
fi

hits=$(printf '%s\n%s\n' "$committed" "$uncommitted" | sweep)

if [ -z "$hits" ]; then
  printf 'sweep-quantities: ok — %s added lines, no quantity beside a claim\n' "$added"
  exit 0
fi

printf 'sweep-quantities: %s added lines; triage each record by whether its quantity can go stale\n' "$added"
printf '%s\n' "$hits"
exit 0
