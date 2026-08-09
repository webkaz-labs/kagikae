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

check() { # check <description> <expected-rc> <actual-rc> [<must-contain> <output-file>]
  local desc=$1 want=$2 got=$3
  if [ "$want" != "$got" ]; then
    printf 'FAIL  %s (want exit %s, got %s)\n' "$desc" "$want" "$got"
    fails=$((fails + 1))
    return
  fi
  if [ $# -ge 5 ] && ! grep -q -- "$4" "$5"; then
    printf 'FAIL  %s (output missing %s)\n' "$desc" "$4"
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
EXPECTED_OTHER="KAE_CLAUDE_DRIVER NO_COLOR SMOKE_WHOLE_FILE"

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
[ -n "$preamble" ] || missing=" nothing at all (smoke-env.sh unreadable or reformatted?)"
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
cp "$excl" "$tmp/excl.bak" 2>/dev/null || : > "$tmp/excl.bak"
# Restore BEFORE deleting $tmp: the backup lives in it, so the previous order
# made the restore dead code and an interrupted run left the operator's
# info/exclude dirty (measured).
trap 'cp "$tmp/excl.bak" "$excl" 2>/dev/null; rm -rf "$tmp" ./SMOKE-SELFTEST-LEAK' EXIT
f=$(doc excl '## Excl' ". scripts/smoke-env.sh" "printf 'smoke-selftest\\n' >> '$excl'")
run "$f" '## Excl'; rc=$?
cp "$tmp/excl.bak" "$excl"
check 'an append to info/exclude is caught' 1 "$rc" 'LEAK' "$tmp/out"

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
EXPECTED_GUARDS=18
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
