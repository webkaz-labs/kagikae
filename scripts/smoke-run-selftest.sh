#!/usr/bin/env bash
# Check scripts/smoke-run.sh's own guards. Run from the repository root, or via
# `mise run smoke-selftest` (which `mise run check` includes).
#
# This exists because the guards were once vouched for in AGENTS.md as "tested
# against a deliberately leaking fixture" when the fixture had been written by
# hand, once, and thrown away. An independent review then demonstrated that two
# of the guarded claims were false — which is exactly what a committed fixture
# would have caught. The fixtures are cheap on purpose: no `kae` build, no
# network, so this can run on every commit rather than when somebody remembers.
#
# READ THIS BEFORE EDITING EITHER SCRIPT. **The guards in these two files have no
# tests of their own** (`EXPECTED_GUARDS` at the bottom is the one place that counts
# them; this sentence deliberately does not repeat the number, because the two drifted
# apart the moment a guard was added). Four separate times a change made for an unrelated
# reason switched a guard off while the suite went on reporting every guard
# holding: a list derived from its own subject, a containment check weakened to a
# proxy, a fixture count shortened to the one value that equals the pass status,
# and a guard whose empty input was its success branch. Nothing here would have
# noticed any of them. So: **any change to `smoke-run.sh` or this file must be
# mutation-tested by hand** — break the thing the guard protects and confirm the
# guard fails — because no automated check will. `docs/ROADMAP.md` carries the
# committed-mutation-table proposal that would close this properly.

set -u

cd "$(dirname "$0")/.." || exit 2
runner=scripts/smoke-run.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fails=0
ok=0

doc() { # doc <name> <heading> <body...>
  local f=$tmp/$1.md h=$2; shift 2
  { printf '%s\n\n```bash\n' "$h"; printf '%s\n' "$@"; printf '```\n'; } > "$f"
  printf '%s' "$f"
}

check() { # check <desc> <expected-rc> <actual-rc> [<must-contain> <file>]...
  local desc=$1 want=$2 got=$3
  shift 3
  if [ "$want" != "$got" ]; then
    printf 'FAIL  %s (want exit %s, got %s)\n' "$desc" "$want" "$got"
    fails=$((fails + 1))
    return
  fi
  # Repeated pairs, so a guard needing two patterns does not hand-roll the
  # bookkeeping beside the helper that already does it.
  while [ $# -ge 2 ]; do
    if ! grep -q -- "$1" "$2"; then
      printf 'FAIL  %s (output missing %s)\n' "$desc" "$1"
      fails=$((fails + 1))
      return
    fi
    shift 2
  done
  # A pattern with no file would otherwise be dropped without a word — a silent
  # subset inside the file written to stop silent subsets. Not reachable from any
  # caller today (all pass 0, 2 or 4 trailing arguments); this is for the next one.
  if [ $# -ne 0 ]; then
    printf 'FAIL  %s (odd trailing argument: a pattern with no file)\n' "$desc"
    fails=$((fails + 1))
    return
  fi
  printf 'ok    %s\n' "$desc"
  ok=$((ok + 1))
}

run() { SMOKE_DOC=$1 bash "$runner" "$2" > "$tmp/out" 2>&1; }
transcript() { sed -n 's/^smoke-run: full transcript in //p' "$tmp/out"; }

# The expected sets are WRITTEN OUT here, and separately DERIVED from the runner,
# and the two are required to match. Neither half works alone, and both failures
# were measured on this file:
#
#   * hand-maintained only — a name dropped from the list took its own guard with
#     it. `XDG_DATA_HOME` (it holds kagikae/secrets/) and `COPILOT_HOME` were both
#     measured writing outside the sandbox while every guard reported holding.
#   * derived only — worse, and it is the shape that reads as the clever fix.
#     Deriving from the file under test means removing a name from the *runner*
#     also removes it from the list, so the guard silently stops testing exactly
#     the thing that was just deleted. Dropping `-u COPILOT_HOME` went from
#     KILLED back to SURVIVES the moment the list was derived.
#
# Equality of the two makes both directions loud: a name deleted from the runner
# fails the comparison, and a name added to the runner fails it too until it is
# declared here (and so gets a guard).
EXPECTED_ROOTS="HOME TMPDIR XDG_CONFIG_HOME XDG_DATA_HOME XDG_RUNTIME_DIR XDG_STATE_HOME"
EXPECTED_CLEARED="CLAUDE_CONFIG_DIR CLAUDE_SECURESTORAGE_CONFIG_DIR CODEX_HOME \
COPILOT_HOME KAE_FINGERPRINT KAE_PROFILE MISE_CONFIG_DIR OPENCODE_AUTH_CONTENT"
# The rest of the prefix — set, but not paths into the sandbox. Declared for the
# same reason: matching only `="$safe` values means a NEW assignment pointing
# somewhere else entirely (`XDG_CACHE_HOME="/tmp/notsandbox"`) is invisible,
# which is the "additions are silent" half all over again.
EXPECTED_OTHER="KAE_CLAUDE_DRIVER MISE_CEILING_PATHS NO_COLOR SMOKE_WHOLE_FILE"

# A "root" is definitionally a variable the runner points into the sandbox, so
# that is what is matched: an assignment whose value starts with $safe.
# `mapfile` is bash 4+; macOS ships bash 3.2 and that is what runs this — the
# same portability class as the GNU-vs-BSD `mktemp` difference the runner carries.
norm() { printf '%s\n' $1 | sort -u | tr '\n' ' ' | sed 's/ $//'; }

# Every assignment in the prefix, not only the ones pointing at $safe.
derived_all=$(norm "$(grep -oE '^  [A-Z_]+=' "$runner" | tr -d ' =')")
derived_roots=$(norm "$(grep -oE '^  [A-Z_]+="\$safe' "$runner" | tr -d ' ="$' | sed 's/safe//')")
derived_cleared=$(norm "$(grep -oE '\-u [A-Z_]+' "$runner" | sed 's/-u //')")
want_all=$(norm "$EXPECTED_ROOTS $EXPECTED_OTHER")
want_roots=$(norm "$EXPECTED_ROOTS")
want_cleared=$(norm "$EXPECTED_CLEARED")

if [ "$derived_all" = "$want_all" ] && [ "$derived_roots" = "$want_roots" ] &&
   [ "$derived_cleared" = "$want_cleared" ]; then
  printf 'ok    the runner sets exactly the variables this file guards\n'
  ok=$((ok + 1))
else
  printf 'FAIL  %s and this file disagree about which variables are handled\n' "$runner"
  printf '        set     runner: %s\n        set     guarded: %s\n' "$derived_all" "$want_all"
  printf '        roots   runner: %s\n        roots   guarded: %s\n' "$derived_roots" "$want_roots"
  printf '        cleared runner: %s\n        cleared guarded: %s\n' "$derived_cleared" "$want_cleared"
  fails=$((fails + 1))
fi

# 0b. The runner must isolate at least everything the preamble does. They are two
#     hand-written copies of the same set in two files — which is the "three
#     copies, three chances to omit one" defect smoke-env.sh's own header exists
#     to prevent, reintroduced one layer up. Nothing else compares them, so a
#     sixth root added to paths.Resolve and to smoke-env.sh could be missed here.
preamble=$(norm "$(grep -oE '^export [A-Z_]+' scripts/smoke-env.sh | sed 's/export //')")
missing=""
# An empty set makes the loop below vacuous, and vacuous is the success branch —
# so an unreadable, emptied or reformatted smoke-env.sh would read as a pass.
# Same shape as the `grep -c` inversion this file already carries a note about.
# Non-emptiness alone only closes the total miss: one extra space after `export`
# hides a single name from the pattern and shrinks this guard's coverage silently,
# so the counts have to agree too.
[ -n "$preamble" ] || missing=" nothing at all (smoke-env.sh unreadable or reformatted?)"
if [ "$(grep -c '^export' scripts/smoke-env.sh)" \
     -ne "$(printf '%s\n' $preamble | grep -c .)" ]; then
  missing="$missing (the pattern matched fewer lines than smoke-env.sh has exports)"
fi
for v in $preamble; do
  case " $(norm "$EXPECTED_ROOTS $EXPECTED_OTHER") " in *" $v "*) ;; *) missing="$missing $v" ;; esac
done
if [ -z "$missing" ]; then
  printf 'ok    the runner isolates everything scripts/smoke-env.sh does\n'
  ok=$((ok + 1))
else
  printf 'FAIL  scripts/smoke-env.sh isolates%s, which %s does not\n' "$missing" "$runner"
  fails=$((fails + 1))
fi

ROOTS=(); CLEARED=()
for v in $EXPECTED_ROOTS; do ROOTS+=("$v"); done
for v in $EXPECTED_CLEARED; do CLEARED+=("$v"); done

# Point every name at /real/<name> and run: a variable the runner forgot comes
# back verbatim, and one it merely points somewhere *else* fails the containment
# half below. `grep -c` prints its count and exits 1 when that count is zero, so
# `|| true` is the only safe tail — `|| echo <n>` appends a second line and the
# comparison then errors out. (Written that way here first.)
probe() { # probe <heading> <var...> -> fills $tmp/out, echoes the transcript path
  local h=$1; shift
  local lines=() envs=() v
  for v in "$@"; do
    lines+=("printf '$v=%s\\n' \"\${$v-unset}\"")
    envs+=("$v=/real/$v")
  done
  # The heading is sanitised into the fixture filename; two headings differing
  # only in punctuation or digits would collide. Two callers today.
  local f; f=$(doc "${h//[^A-Za-z]/}" "## $h" "${lines[@]}")
  env "${envs[@]}" SMOKE_DOC="$f" bash "$runner" "## $h" > "$tmp/out" 2>&1
  transcript
}

# 1. A block that writes into the checkout must void the run.
f=$(doc leak '## Leak' '. scripts/smoke-env.sh' 'touch ./SMOKE-SELFTEST-LEAK')
run "$f" '## Leak'; rc=$?
rm -f ./SMOKE-SELFTEST-LEAK
check 'a block touching the checkout is caught' 1 "$rc" 'LEAK' "$tmp/out"

# 1b. The other leak branch: an append to info/exclude, which is the shape the
#     original `kae pin` leak took and the one guard 1 does not reach.
excl="$(git rev-parse --git-common-dir)/info/exclude"
# Whether the file EXISTED is part of the state being restored, not just its
# contents. `cp "$excl" bak 2>/dev/null || : > bak` looks like it handles the
# missing case and does the opposite: on a checkout with no info/exclude the
# fallback writes an EMPTY backup, which the restore then puts back **as a file**.
# Measured: `mise run check` created `.git/info/exclude` in the operator's
# checkout — the gate writing to the real environment, which AGENTS.md forbids —
# and the next `smoke-run.sh` run then reported a false `LEAK`, because its
# detector reads "missing" and "present but empty" as different states. A false
# leak is the expensive half: it says the block wrote to the checkout.
if [ -f "$excl" ]; then cp "$excl" "$tmp/excl.bak"; excl_existed=1; else excl_existed=0; fi
restore_excl() {
  if [ "$excl_existed" = 1 ]; then cp "$tmp/excl.bak" "$excl"; else rm -f "$excl"; fi
}
# Restore BEFORE deleting $tmp: the backup lives in it, so the previous order
# made the restore dead code and an interrupted run left the operator's
# info/exclude dirty (measured).
trap 'restore_excl; rm -rf "$tmp" ./SMOKE-SELFTEST-LEAK' EXIT
f=$(doc excl '## Excl' ". scripts/smoke-env.sh" "printf 'smoke-selftest\\n' >> '$excl'")
run "$f" '## Excl'; rc=$?
restore_excl
check 'an append to info/exclude is caught' 1 "$rc" 'LEAK' "$tmp/out"

# 1c. …and the restore must put back the file's EXISTENCE, not only its bytes, which
#     is the guard for the defect above: the old content-only form left an empty
#     info/exclude in a checkout that had none, so the gate itself wrote to the
#     operator's repository and the next run reported a false LEAK.
#
#     Asserted against `restore_excl` driven over a FIXTURE path, not against the
#     real file's state afterwards. Checking the real one reads as the obvious test
#     and is inert wherever info/exclude exists — both forms restore the content
#     there — and `git init` creates it from the template (measured, git 2.55.0), so
#     every fresh clone takes the vacuous branch while still printing `ok`. That is
#     the shape this file's own header names: a guard whose pass condition is
#     satisfiable by a degenerate input. A subshell rebinds the two variables the
#     function reads, so this perturbs nothing and is deterministic everywhere.
( excl="$tmp/fake-excl"; excl_existed=0; : > "$excl"; restore_excl; [ ! -e "$excl" ] )
check 'restore_excl removes a file that did not exist before' 0 $?

# 2. Every root must land INSIDE the sandbox. Asserting only "not the value I
#    planted" is a weaker thing that reads the same: pointing a root at some
#    third wrong place (or HOME at "$safe/.." — the shared user temp dir, which
#    cleanup then never reclaims) passed that version while credentials were
#    written outside the sandbox. Containment is what the runner promises, so it
#    is what is checked; the /real/ count stays as the second half.
tr=$(probe Roots "${ROOTS[@]}")
sandbox=$(sed -n '1s/^smoke-run transcript: HOME=//p' "$tr" 2>/dev/null)
leaked=$(grep -c '=/real/' "$tr" 2>/dev/null || true)
inside=0
# Only the fixture's own `NAME=value` output lines: the transcript's first line
# is `smoke-run transcript: HOME=<sandbox>`, which a bare count of "=<sandbox>"
# includes, giving 7-of-6 and a failure with nothing wrong.
[ -n "$sandbox" ] &&
  inside=$(grep -E '^[A-Z_]+=' "$tr" 2>/dev/null | grep -cF "=$sandbox" || true)
# Both the derivation above and this containment are PREFIX matches, so
# `XDG_DATA_HOME="$safe/../escape"` satisfies each of them while landing outside
# the sandbox — and, being outside, the credential it holds survives the
# runner's `rm -rf "$safe"` instead of going with it. `HOME` is immune because it
# is what defines $sandbox; every other root was not. Measured 2026-08-09.
escaped=$(grep -E '^[A-Z_]+=' "$tr" 2>/dev/null | grep -c '\.\.' || true)
# `leaked` is kept for the diagnostic only: a root pointed at the planted
# value is not inside the sandbox either, so containment already covers it,
# and gating on both leaves a term that can be weakened to a tautology.
if [ -n "$sandbox" ] && [ "$inside" -eq "${#ROOTS[@]}" ] && [ "$escaped" -eq 0 ]; then
  printf 'ok    all %s roots land inside the sandbox\n' "${#ROOTS[@]}"
  ok=$((ok + 1))
else
  printf 'FAIL  a root escaped the sandbox (%s of %s inside, %s planted values seen)\n' \
    "$inside" "${#ROOTS[@]}" "$leaked"
  grep -E '^[A-Z_]+=' "$tr" 2>/dev/null | grep -vF "=$sandbox" | sed 's/^/        /'
  fails=$((fails + 1))
fi

# 3. The tool-home variables outrank HOME, so the runner must clear them ALL.
tr=$(probe Cleared "${CLEARED[@]}")
got=$(grep -c '=unset$' "$tr" 2>/dev/null || true)
if [ "$got" -eq "${#CLEARED[@]}" ]; then
  printf 'ok    all %s inherited tool variables are cleared\n' "${#CLEARED[@]}"
  ok=$((ok + 1))
else
  printf 'FAIL  only %s of %s tool variables cleared:\n' "$got" "${#CLEARED[@]}"
  grep '=/real/' "$tr" 2>/dev/null | sed 's/^/        /'
  fails=$((fails + 1))
fi

# 3b. KAE_CLAUDE_DRIVER=file is what keeps claude off the real login keychain.
#     Deleting that one line switched the driver to claude-keychain-patch with
#     this file still reporting 11/11.
f=$(doc driver '## Driver' 'printf "KAE_CLAUDE_DRIVER=%s\n" "${KAE_CLAUDE_DRIVER-unset}"')
run "$f" '## Driver'
check 'claude is forced onto the file driver' 0 $? \
  'KAE_CLAUDE_DRIVER=file' "$(transcript)"

# 4. A failing line makes the run fail, including the last line.
f=$(doc mid '## Mid' 'true' 'false' 'true')
run "$f" '## Mid'; check 'a failing line in the middle fails the run' 1 $? 'rc=1' "$tmp/out"
f=$(doc last '## Last' 'true' 'false')
run "$f" '## Last'; check 'a failing LAST line fails the run' 1 $?

# 5. Many failures must not wrap the exit status to 0.
# The count must be **exactly 256**, and the requirement is not "more than the
# wrap point" — it is "a residue that differs from the answer a working runner
# gives". An un-clamped `exit "$failed"` returns `failed % 256`, so at 256 it
# exits 0 and this guard fires, while at 257 it exits 1, which is the pass value:
# a cleanup pass shortened 300 to 257 for speed and silently switched the guard
# off, and the whole suite still reported 18/18. 300 was safe by luck (300 % 256
# = 44); 256 is safe by construction and is also the cheapest.
lines=(); for _ in $(seq 256); do lines+=('false'); done
f=$(doc many '## Many' "${lines[@]}")
run "$f" '## Many'; check '256 failing lines do not wrap to exit 0' 1 $?

# 6. Whole-file mode must not claim a per-line verdict it does not have.
f=$(doc whole '## Whole' 'echo first' 'false' 'echo last')
SMOKE_DOC=$f SMOKE_WHOLE_FILE=1 bash "$runner" '## Whole' > "$tmp/out" 2>&1
if grep -q -- 'every line exited 0' "$tmp/out"; then
  printf 'FAIL  whole-file mode must not print a per-line verdict\n'; fails=$((fails + 1))
else
  printf 'ok    whole-file mode does not print a per-line verdict\n'; ok=$((ok + 1))
fi
# Absence alone passes for a mode that prints nothing at all, so require the
# statement too.
check 'whole-file mode says which mode it is' 0 0 'whole-file mode —' "$tmp/out"

# 7. An indented command carrying a trailing comment must RUN, not be skipped.
f=$(doc indent '## Indent' '  touch "$HOME/ran"   # indented, with a comment' \
                           'test -f "$HOME/ran"')
run "$f" '## Indent'; check 'an indented command with a comment is not skipped' 0 $?

# 8. A heading prefix matching several sections is an error, not a silent merge.
printf '## Dup one\n\n```bash\ntrue\n```\n\n## Dup two\n\n```bash\ntrue\n```\n' > "$tmp/dup.md"
run "$tmp/dup.md" '## Dup'; check 'an ambiguous heading prefix is refused' 2 $? 'name one of them' "$tmp/out"

# 9. A heading with no bash block is an error.
printf '## Empty\n\nprose only\n' > "$tmp/none.md"
run "$tmp/none.md" '## Empty'; check 'a heading with no block is refused' 2 $?

# 10. Extraction must STOP at the next heading. The ambiguity guard covers the
#     heading side of silent merging; this is the section-boundary side, and it
#     is the worse direction — dropping the runner's `insec = 0` reset ran 1065
#     lines of unrelated sections under one heading instead of refusing.
printf '## First\n\n```bash\ntrue\n```\n\n## Second\n\n```bash\nfalse\nfalse\n```\n' > "$tmp/two.md"
run "$tmp/two.md" '## First'
check 'extraction stops at the next heading' 0 $? '1 lines extracted' "$tmp/out"

# 11. The backslash-continuation join — the third of the four leak classes the
#     runner's own header names. Breaking it truncates the fixture silently.
f=$(doc join '## Join' "printf 'joined' \\" '  > "$HOME/j"' 'test "$(cat "$HOME/j")" = joined')
run "$f" '## Join'; check 'a backslash continuation is joined, not split' 0 $?

# 12. NO_COLOR: ANSI escapes in the transcript break a block's own greps.
f=$(doc nocolor '## NoColor' 'printf "NO_COLOR=%s\n" "${NO_COLOR-unset}"')
run "$f" '## NoColor'; check 'colour is disabled for the block' 0 $? 'NO_COLOR=1' "$(transcript)"

# 13. mise must not be able to reach a config outside the sandbox. It is the one
#     isolated thing that has nothing to do with HOME: mise walks up from the
#     **current directory**, and a block starts in the checkout, which sits inside
#     the operator's home — so every root this file guards can be correct and mise
#     still read the operator's real config (measured: two of them, 2026-08-09).
#
#     The first assertion is the positive control and it is not decoration. With
#     the ceiling removed, mise on a machine that has an operator config fails
#     outright (its `trusted-configs` lives under the redirected XDG_STATE_HOME, so
#     the sandbox trust store is empty) and prints nothing at all — which would
#     satisfy a lone "nothing outside the sandbox" check by printing nothing for
#     the wrong reason. That is this file's own recorded defect class: a guard
#     whose empty input is its success branch. So the block seeds a config *inside*
#     the sandbox and requires mise to find that one.
#
#     A second thing it cannot kill, and the reason is the same: writing the ceiling
#     from `$PWD`/`$HOME` instead of `pwd -P`. mise matches the ceiling against the
#     CANONICAL cwd, so a logical path that differs from it silently does not apply —
#     but on a machine where the checkout and the home are already canonical the two
#     spellings are the same string. It bites a checkout reached through a symlink,
#     or a relocated `$HOME` (found in review, 2026-08-09, by measuring a symlinked
#     fixture directory rather than by reading).
#
#     What this guard cannot kill, measured rather than assumed: narrowing the
#     ceiling to `$PWD` alone. The runner never leaves the checkout, and from there
#     the `$PWD` entry stops the walk before anything above it — so the `$HOME`
#     half is invisible here. It is still load-bearing, and the case was measured
#     on 2026-08-09: from the checkout's *parent*, a `$PWD`-only ceiling reaches
#     the operator's config and mise fails on it, while `$PWD:$HOME` is silent.
#     Reproducing that would mean letting a fixture `cd` above the checkout, which
#     buys one mutation and hands every other guard a cwd outside the repository.
#     Note also that the failure there arrives on *stderr* with an empty stdout, so
#     a "count the paths" assertion alone reads it as success — which is the same
#     reason the control below exists.
#
#     Deliberately not done here: disabling trust to make mise talk. The runner
#     header is normative for why (it costs a secret), and a self-test that did it
#     anyway would be teaching the opposite of the file it guards.
f=$(doc ceiling '## Ceiling' \
  'mkdir -p "$HOME/.config/mise"' \
  'printf "[env]\nSMOKE_CEILING_CONTROL = \"1\"\n" > "$HOME/.config/mise/config.toml"' \
  'test "$(mise config ls 2>/dev/null | grep -c "config.toml")" -ge 1' \
  'test "$(mise config ls 2>/dev/null | grep -c "^/")" -eq 0')
run "$f" '## Ceiling'
check 'mise cannot reach a config outside the sandbox' 0 $?

# 14. A block that ends itself must not be reported as green. The per-line loop
#     records failures in a log and the report reads that log, so a block which
#     exits *out* of the loop leaves it empty — which used to select the "every
#     line exited 0" branch while the run exited non-zero. Measured on the real
#     relogin section: 35 of 146 lines ran and it reported every line green.
#     The fixture uses a here-document because that is how it actually happened —
#     one line to this loop, its body then evaluated as commands — so this guard
#     covers the reachable cause and not just the bare `exit`.
#
#     The `ENDED EARLY` half is the load-bearing one and dropping it looks
#     harmless: measured 2026-08-09, a version checking only the exit status
#     SURVIVES, because the broken runner already exited 9. It was the *message*
#     that lied. Anything asserting on this behaviour has to read what the run
#     said, not only what it returned.
f=$(doc early '## Early' \
  'touch "$HOME/before"' \
  "cat > \$HOME/fake <<'XEOF'" \
  'echo unreachable' \
  'exit 9' \
  'XEOF' \
  'touch "$HOME/after"')
run "$f" '## Early'; rc=$?
check 'a block that ends early is not reported as green' 1 "$rc" 'ENDED EARLY' "$tmp/out"

# 15. The same, ending with status **0**. This is the half a status-based check
#     cannot see, and it is the more idiomatic one — a fixture script ends `exit 0`
#     far more often than `exit 9`. Guard 14 used 9 and so passed while this shape
#     still reported "every line exited 0" (found in review, 2026-08-09).
f=$(doc early0 '## Early0' \
  'touch "$HOME/before"' \
  "cat > \$HOME/fake <<'XEOF'" \
  'echo unreachable' \
  'exit 0' \
  'XEOF' \
  'touch "$HOME/after"')
run "$f" '## Early0'; rc=$?
check 'a block that ends early with status 0 is caught too' 1 "$rc" 'ENDED EARLY' "$tmp/out"

# 16. A failing line AND an early end must BOTH be reported. Reporting only the
#     failure implies every other line ran, which is the same lie one level down;
#     an earlier version made the failure branch outrank the completeness one with
#     an `elif` and printed exactly that (found in review, 2026-08-09).
f=$(doc both '## Both' \
  'touch "$HOME/before"' \
  'false' \
  "cat > \$HOME/fake <<'XEOF'" \
  'exit 9' \
  'XEOF' \
  'touch "$HOME/after"')
run "$f" '## Both'; rc=$?
check 'a failing line and an early end are both reported' 1 "$rc" \
  'exited non-zero' "$tmp/out" 'ENDED EARLY' "$tmp/out"

# 17. A block that ends on its LAST line. The counter is written before each line
#     runs, so this shape leaves it equal to the total and passed the completeness
#     check that guards 14-16 exercise — detection depended on there being lines
#     *after* the failure point (found in review, 2026-08-09). The runner writes a
#     sentinel after the loop instead, so the invariant is "the loop finished".
f=$(doc lastexit '## LastExit' 'touch "$HOME/a"' 'true' 'exit 9')
run "$f" '## LastExit'; rc=$?
check 'a block that ends on its last line is caught' 1 "$rc" 'ENDED EARLY' "$tmp/out"

# 18. A last line ending in a backslash. The loop reaches EOF with a command still
#     pending in $acc and discards it; the sentinel called that a finished loop until
#     it was guarded on $acc (found in review, 2026-08-09). Guard 11 covers the other
#     half of the same leak class — a continuation split from its redirection.
f=$(doc dangling '## Dangling' 'touch "$HOME/a"' 'touch "$HOME/b" \\')
run "$f" '## Dangling'; rc=$?
check 'a dangling continuation on the last line is caught' 1 "$rc" 'ENDED EARLY' "$tmp/out"

# 19. A block whose check is a column-0 `#   assert:` comment asserts nothing: the
#     loop skips comments, so the block would run its commands, evaluate none of
#     the claims, and be reported green. Note the fixture's command PASSES — the
#     refusal is about the block being uncheckable, not about it failing, and a
#     version keyed on a failure would let this exact shape through.
#
#     The two trailing patterns are the DIAGNOSTIC, not the verdict, and they are
#     here because both halves of it survived a mutation: dropping `-n` from the
#     grep (losing the line numbers) and hard-coding a false count both passed 27
#     guards. The half that tells an author *which* line to fix is the half that
#     rots unwatched, and a count nothing checks can print a number that is simply
#     untrue. `2:` is where the marker sits — `doc` writes the body verbatim, so the
#     fixture's `touch` is block line 1 and the marker is line 2.
#     TWO markers, not one, and the count asserted. With a single marker the count
#     assertion reads `on 1 line(s)`, which is the value a `grep -Einm1` also prints —
#     measured surviving 35 guards, reporting one line however many the block has. A
#     fixture whose expected value coincides with a degenerate one is the defect class
#     the header above names, and it took two rounds to stop writing it.
f=$(doc assertcomment '## AssertComment' "touch \"$tmp/ac-ran\"" \
  '#   assert: this never runs' '#   assert: and neither does this')
run "$f" '## AssertComment'; rc=$?
check 'a column-0 assert comment is refused' 2 "$rc" 'ASSERTS NOTHING' "$tmp/out" \
  'on 2 line(s)' "$tmp/out" '2:#   assert:' "$tmp/out" '3:#   assert:' "$tmp/out"

# 20. The refusal must happen BEFORE the block runs, which is the whole reason it
#     carries the caller-error status rather than a failure status. Nothing asserted
#     this until now: moving the entire guard to *after* the run loop was measured
#     SURVIVING all 27 guards, because guard 19's fixture command is `true` and no
#     one was watching whether it executed. That is the class the header above names
#     as having bitten four times — a guard that reads as load-bearing and is not.
#     The fixture writes outside the sandbox on purpose ($tmp is this script's own
#     directory, not the block's HOME), because a write inside the sandbox dies with
#     it and cannot be inspected afterwards. Guard 24 is this one's positive control:
#     without it, a `touch` that never worked would read as a refusal that came first.
if [ -e "$tmp/ac-ran" ]; then rc=1; else rc=0; fi
check 'the refusal comes before the block runs' 0 "$rc"

# 21. The same marker with ONE space. Both spellings were in the 56 markers deleted
#     with the v0.8.x sections, so a pattern narrowed to `#   assert:` — which reads
#     as the tidier literal — passes this and loses the guard for half its subjects.
#     That narrowing is this repository's recorded defect class, and it is the
#     mutation to try first on the pattern above.
f=$(doc assertonespace '## AssertOneSpace' 'true' '# assert: also skipped')
run "$f" '## AssertOneSpace'; rc=$?
check 'a one-space column-0 assert comment is refused too' 2 "$rc" 'ASSERTS NOTHING' "$tmp/out"

# 22. And capitalised. `# Assert:` passed the first version of this guard, found by
#     review — the pattern claimed to match spellings and matched exactly two. This
#     is what makes the `-i` load-bearing rather than decorative.
f=$(doc assertcap '## AssertCap' 'true' '#   Assert: capitalised, still a comment')
run "$f" '## AssertCap'; rc=$?
check 'a capitalised column-0 assert comment is refused' 2 "$rc" 'ASSERTS NOTHING' "$tmp/out"

# 22b. And a different WORD. `#   expect:` was measured passing a version of this
#      guard that knew only `assert:` — the same false green, reached by substituting
#      one word, which is the defect class the guard's own comment names. It matters
#      more after the v0.8.x deletion than before it: no column-0 `assert:` marker is
#      left in docs/VALIDATION.md, so the next author invents the label instead of
#      copying one. Narrowing the alternation back to `assert` alone trips only here.
f=$(doc assertexpect '## AssertExpect' 'true' '#   expect: a different label, same lie')
run "$f" '## AssertExpect'; rc=$?
check 'a column-0 marker spelled expect: is refused too' 2 "$rc" 'ASSERTS NOTHING' "$tmp/out"

# 22c. Whitespace before the colon. `#   assert :` is refused at HEAD and nothing named
#      the `[[:space:]]*` that does it, so the token was silently droppable — measured
#      SURVIVING all 33 guards before this existed.
#      **Two fixture lines and a COUNT, because one example pins one example.** A first
#      version used a single space-before-colon line, and narrowing the class to ` *:`
#      then survived 35 guards while a TAB before the colon became a false green: the
#      space line still matched, the refusal still fired, and `ASSERTS NOTHING` was
#      still printed. Asserting `on 2 line(s)` is what makes the character class rather
#      than one member of it the thing under test.
f=$(doc assertspacecolon '## AssertSpaceColon' 'true' \
  '#   assert : space before the colon' \
  "#   assert$(printf '\t'): tab before the colon")
run "$f" '## AssertSpaceColon'; rc=$?
check 'both space and tab before the marker colon are refused' 2 "$rc" \
  'ASSERTS NOTHING' "$tmp/out" 'on 2 line(s)' "$tmp/out"

# 22d. The alternation's WORD LIST, written out here and separately derived from the
#      runner, required to match — the same two-sided form this file already uses for
#      EXPECTED_ROOTS/EXPECTED_CLEARED, and adopted here for the same measured reason.
#      22b pins one word end to end and that is all it pins: narrowing the pattern to
#      `(assert|expect)` was measured SURVIVING all 33 guards, because nothing named
#      the other four even though a `#   verify:` marker is refused at HEAD. Testing
#      each word end to end would cost six more `smoke-run.sh` spawns; this costs
#      none and makes both directions loud, which is what the roots guard is for.
#      A pattern reshaped so the sed no longer matches derives the EMPTY set and fails
#      the comparison rather than passing it — the degenerate-input branch this file's
#      header warns about, checked deliberately.
#      **Anchored at the assignment, and that is the load-bearing part.** With a
#      leading `.*` the sed reads every line of the runner and `norm` unions the
#      matches, so a COMMENT quoting the full pattern satisfies this guard while the
#      code is narrowed — measured SURVIVING 35/35 while a `#   verify:` marker got
#      `every line exited 0`. That is not a hypothetical edit: this runner quotes its
#      retired patterns verbatim in comments three times, because the file asks it to.
#      `derived_all` above is anchored at the prefix's indentation for the same reason;
#      `derived_cleared` is not, which is the same gap and is older than this guard.
#      Both sides go through `norm`, so the declaration does not have to be hand-sorted
#      and a reordering here cannot fail while naming the runner as the culprit.
EXPECTED_MARKER_WORDS="assert check confirm ensure expect verify"
derived_words=$(norm "$(sed -n 's/^markers=.*\^#\[\[:space:\]\]\*(\([a-z|]*\)).*/\1/p' "$runner" | tr '|' ' ')")
check 'the runner refuses exactly the marker words this file declares' \
  "$(norm "$EXPECTED_MARKER_WORDS")" "$derived_words"

# 23. The control the refusals need, and the one that makes the pattern's column-0
#     anchor load-bearing: a real command carrying a TRAILING `# assert:` comment is
#     how every converted section documents what its command proves, and refusing
#     those would refuse all six live sections. Widening the pattern to match
#     `assert:` anywhere on a line trips here and nowhere else.
f=$(doc asserttrailing '## AssertTrailing' "touch \"$tmp/at-ran\"" \
  'test 1 -eq 1   # assert: the command is the assertion')
run "$f" '## AssertTrailing'; rc=$?
check 'a trailing assert comment on a real command is accepted' 0 "$rc" \
  'every line exited 0' "$tmp/out"

# 24. Guard 20's positive control, and it is not optional: guard 20 asserts a file's
#     ABSENCE, which an inert `touch` satisfies for free. This proves the same
#     fixture line does create the file when the block is allowed to run, so the
#     absence above means "refused first" and not "the touch never worked".
if [ -e "$tmp/at-ran" ]; then rc=0; else rc=1; fi
check 'an accepted block does reach its first line' 0 "$rc"

# 25. A column-0 comment whose `assert:` is preceded by prose is not a marker, and
#     the live harvest block has one: `#   the three lines above assert: exactly ONE
#     stderr line`. This is the widening the column-0 anchor does NOT cover, so
#     guard 23 cannot see it — measured, `^#.*assert:` survives every other guard
#     here and refuses § v0.17.0 surface — the credential harvest outright. The
#     marker has to be the first thing after the `#`, and that is what this pins.
f=$(doc assertprose '## AssertProse' 'test 1 -eq 1' \
  '#   the line above will assert: exactly one thing, and this is prose about it')
run "$f" '## AssertProse'; rc=$?
check 'a column-0 comment whose assert: follows prose is accepted' 0 "$rc" \
  'every line exited 0' "$tmp/out"

printf '\n'
# A deleted guard used to leave this file reporting "all guards hold" with one
# fewer ok line — the file could be hollowed out a guard at a time, which is how
# it got here. Both directions are loud now: adding a guard without bumping this
# fails too.
#
# What this does NOT cover, recorded so it is not re-filed: weakening a guard's
# condition *in place* (`-eq` → `-ge`) is undetectable here, as it is in any test
# suite. Four more limits of the same kind:
#   * a root escaping via a symlink rather than via `..` is not refused;
#   * `TMPDIR` in ROOTS proves the runner sets it, not that it has any effect
#     (darwin's `mktemp` ignores TMPDIR entirely, so there it sandboxes nothing);
#   * guard 0b is one-directional by design — it catches a root the preamble has
#     and the runner lacks, not one *missing from the preamble*, which is what
#     the 2026-07-31 incident actually was. The reverse check needs an exception
#     list (TMPDIR, KAE_CLAUDE_DRIVER and SMOKE_WHOLE_FILE are runner-only), which
#     is more machinery than the direction earns;
#   * the GOMODCACHE/GOCACHE handling in the runner has no guard. Its four edge
#     cases (either value empty, both empty, `go env` failing) were verified by
#     hand against a `go` shim on 2026-08-09 and none exports an empty value.
EXPECTED_GUARDS=35
ran=$((ok + fails))
if [ "$ran" -ne "$EXPECTED_GUARDS" ]; then
  printf 'smoke-run-selftest: %s guards ran, expected %s — a guard was added or removed\n' \
    "$ran" "$EXPECTED_GUARDS" >&2
  exit 1
fi
if [ "$fails" -ne 0 ]; then
  printf 'smoke-run-selftest: %s of %s guard(s) FAILED\n' "$fails" "$ran" >&2
  exit 1
fi
printf 'smoke-run-selftest: all %s guards hold\n' "$ran"
