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
# READ THIS BEFORE EDITING EITHER SCRIPT. **These files have 22 guards and zero
# tests of those guards.** Four separate times a change made for an unrelated
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
#     Deliberately not done here: disabling trust to make mise talk. That is the
#     documented cure and it makes mise load the operator's global config and
#     evaluate its `exec()` templates; one resolved a live GitHub token into a
#     transcript on 2026-08-09. The runner's header says never to do it, and a
#     self-test that did it anyway would be teaching the opposite.
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
if [ "$rc" -ne 0 ] && grep -q 'exited non-zero' "$tmp/out" && grep -q 'ENDED EARLY' "$tmp/out"; then
  printf 'ok    a failing line and an early end are both reported\n'
  ok=$((ok + 1))
else
  printf 'FAIL  a failing line and an early end are both reported (rc=%s)\n' "$rc"
  fails=$((fails + 1))
fi

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
EXPECTED_GUARDS=22
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
