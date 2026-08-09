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

# 1. A block that writes into the checkout must void the run.
f=$(doc leak '## Leak' '. scripts/smoke-env.sh' 'touch ./SMOKE-SELFTEST-LEAK')
run "$f" '## Leak'; rc=$?
rm -f ./SMOKE-SELFTEST-LEAK
check 'a block touching the checkout is caught' 1 "$rc" 'LEAK' "$tmp/out"

# 2. A block with NO preamble is still isolated — HOME and every XDG root.
f=$(doc noamble '## NoAmble' 'printf "H=%s\n" "$HOME"' 'printf "S=%s\n" "$XDG_STATE_HOME"')
run "$f" '## NoAmble'; rc=$?
check 'a preamble-less block still runs' 0 "$rc"
tr=$(sed -n 's/^smoke-run: full transcript in //p' "$tmp/out")
if grep -q "^H=$HOME\$" "$tr" 2>/dev/null; then
  printf 'FAIL  a preamble-less block must not see the real HOME\n'; fails=$((fails + 1))
else
  printf 'ok    a preamble-less block does not see the real HOME\n'
fi

# 3. The tool-home variables outrank HOME, so the runner must clear them too.
#    This is the case that was measured writing outside the sandbox.
f=$(doc toolhome '## ToolHome' 'printf "C=%s\n" "${CODEX_HOME-unset}"' \
                               'printf "D=%s\n" "${CLAUDE_CONFIG_DIR-unset}"')
CODEX_HOME=/real/codex CLAUDE_CONFIG_DIR=/real/claude run "$f" '## ToolHome'
tr=$(sed -n 's/^smoke-run: full transcript in //p' "$tmp/out")
if grep -q '^C=unset$' "$tr" 2>/dev/null && grep -q '^D=unset$' "$tr" 2>/dev/null; then
  printf 'ok    inherited tool-home variables are cleared\n'
else
  printf 'FAIL  inherited tool-home variables reached the block\n'; fails=$((fails + 1))
fi

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
