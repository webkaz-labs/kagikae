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
}

not_contains() { # not_contains <description> <string> <file>
  if grep -q -- "$2" "$3"; then
    printf 'FAIL  %s (output must NOT contain %s)\n' "$1" "$2"
    fails=$((fails + 1))
  else
    printf 'ok    %s\n' "$1"
  fi
}

run() { SMOKE_DOC=$1 bash "$runner" "$2" > "$tmp/out" 2>&1; }
transcript() { sed -n 's/^smoke-run: full transcript in //p' "$tmp/out"; }

# Every root and every cleared variable is checked, one fixture line each.
# Naming only a subset is how the first version of this file passed while
# `XDG_DATA_HOME` (which holds kagikae/secrets/) and `COPILOT_HOME` were being
# dropped from the runner's prefix — measured writing credentials outside the
# sandbox, certified green.
ROOTS=(HOME XDG_CONFIG_HOME XDG_DATA_HOME XDG_STATE_HOME XDG_RUNTIME_DIR TMPDIR)
CLEARED=(CODEX_HOME CLAUDE_CONFIG_DIR COPILOT_HOME CLAUDE_SECURESTORAGE_CONFIG_DIR
         OPENCODE_AUTH_CONTENT MISE_CONFIG_DIR KAE_PROFILE KAE_FINGERPRINT)

# Point every name at /real/<name> and run: a variable the runner forgot comes
# back verbatim. `grep -c` prints its count and exits 1 when that count is zero,
# so `|| true` is the only safe tail — `|| echo <n>` appends a second line and
# the comparison then errors out. (Written that way here first.)
probe() { # probe <heading> <var...> -> fills $tmp/out, echoes the transcript path
  local h=$1; shift
  local lines=() envs=() v
  for v in "$@"; do
    lines+=("printf '$v=%s\\n' \"\${$v-unset}\"")
    envs+=("$v=/real/$v")
  done
  local f; f=$(doc "${h//[^A-Za-z]/}" "## $h" "${lines[@]}")
  env "${envs[@]}" SMOKE_DOC="$f" bash "$runner" "## $h" > "$tmp/out" 2>&1
  sed -n 's/^smoke-run: full transcript in //p' "$tmp/out"
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
trap 'rm -rf "$tmp" ./SMOKE-SELFTEST-LEAK; [ -f "$tmp/excl.bak" ] && cp "$tmp/excl.bak" "$excl"' EXIT
f=$(doc excl '## Excl' ". scripts/smoke-env.sh" "printf 'smoke-selftest\\n' >> '$excl'")
run "$f" '## Excl'; rc=$?
cp "$tmp/excl.bak" "$excl"
check 'an append to info/exclude is caught' 1 "$rc" 'LEAK' "$tmp/out"

# 2. A preamble-less block must see NONE of the real roots.
tr=$(probe Roots "${ROOTS[@]}")
leaked=$(grep -c '=/real/' "$tr" 2>/dev/null || true)
seen=$(grep -c '^HOME=' "$tr" 2>/dev/null || true)
if [ "$leaked" -eq 0 ] && [ "$seen" -eq 1 ]; then
  printf 'ok    a preamble-less block sees none of the %s real roots\n' "${#ROOTS[@]}"
else
  printf 'FAIL  a real root reached a preamble-less block (seen=%s):\n' "$seen"
  grep '=/real/' "$tr" 2>/dev/null | sed 's/^/        /'
  fails=$((fails + 1))
fi

# 3. The tool-home variables outrank HOME, so the runner must clear them ALL.
tr=$(probe Cleared "${CLEARED[@]}")
got=$(grep -c '=unset$' "$tr" 2>/dev/null || true)
if [ "$got" -eq "${#CLEARED[@]}" ]; then
  printf 'ok    all %s inherited tool variables are cleared\n' "${#CLEARED[@]}"
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
lines=(); for _ in $(seq 300); do lines+=('false'); done
f=$(doc many '## Many' "${lines[@]}")
run "$f" '## Many'; check '300 failing lines do not wrap to exit 0' 1 $?

# 6. Whole-file mode must not claim a per-line verdict it does not have.
f=$(doc whole '## Whole' 'echo first' 'false' 'echo last')
SMOKE_DOC=$f SMOKE_WHOLE_FILE=1 bash "$runner" '## Whole' > "$tmp/out" 2>&1
not_contains 'whole-file mode does not print a per-line verdict' 'every line exited 0' "$tmp/out"
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

printf '\n'
if [ "$fails" -ne 0 ]; then
  printf 'smoke-run-selftest: %s guard(s) FAILED\n' "$fails" >&2
  exit 1
fi
printf 'smoke-run-selftest: all guards hold\n'
