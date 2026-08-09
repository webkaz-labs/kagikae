#!/usr/bin/env bash
# Extract the fenced bash blocks under a heading in docs/VALIDATION.md and run
# them in isolation. From the repository root:
#
#   bash scripts/smoke-run.sh '## Smoke Checks'
#
# This exists instead of a paragraph telling the reader to be careful, because
# every hand-written harness for this file has leaked, and the leaks write to
# the machine rather than failing. Four measured ways, all of which this script
# closes by construction rather than by warning:
#
#   * `. scripts/smoke-env.sh` inside `$(...)` exports nothing — command
#     substitution is a subshell — so the block runs against the real HOME while
#     looking isolated. The preamble's own header warns about this and it still
#     happened twice on 2026-08-09.
#   * A harness that prints progress from an ERR/DEBUG trap has that output
#     captured by any `$(...)` in the block. One such trap corrupted a
#     `test "$(grep -c ...)"` into a non-integer and invented three failures; a
#     second was captured by `HOME=$(mktemp -d)` *inside* the preamble, making
#     HOME a two-line string and creating a directory in the checkout.
#   * A line-by-line runner splits `printf ... \` from its `> file` and
#     silently truncates the fixture, so later assertions fail for the wrong
#     reason.
#   * `kae pin` binds the *current* directory and appends to
#     $GIT_COMMON_DIR/info/exclude, so a block run from the checkout dirties it.
#
# The isolation is belt and braces on purpose: this script puts every root in a
# temp HOME *before* the block runs, so a block whose own preamble silently
# fails to take effect still cannot reach the real one.

set -u

heading=${1-'## Smoke Checks'}
# SMOKE_DOC exists so this script's own guards can be tested against a fixture
# document that deliberately leaks; without it the leak detector and the
# isolation fallback below are two more assertions nothing ever evaluates.
doc=${SMOKE_DOC:-docs/VALIDATION.md}

if [ ! -f "$doc" ] || [ ! -f go.mod ]; then
  printf 'smoke-run: run me from the repository root (no %s)\n' "$doc" >&2
  exit 2
fi

# --- extract ---------------------------------------------------------------
# The heading is matched as a prefix: the real ones carry a parenthetical
# ("## Smoke Checks (built binary, isolated env)") that a caller should not have
# to reproduce, and requiring equality silently found nothing.
block=$(mktemp)
awk -v h="$heading" '
  index($0, h) == 1 && /^## / { insec = 1; next }
  insec && /^## /             { insec = 0 }
  insec && /^```bash/         { inblk = 1; next }
  insec && /^```$/            { inblk = 0; next }
  insec && inblk              { print }
' "$doc" > "$block"

if [ ! -s "$block" ]; then
  printf 'smoke-run: no bash block found under %s\n' "$heading" >&2
  exit 2
fi
printf 'smoke-run: %s lines extracted from %s under %s\n' \
  "$(wc -l < "$block" | tr -d ' ')" "$doc" "$heading"

# --- record what the checkout looks like now --------------------------------
excl=$(git rev-parse --git-common-dir 2>/dev/null)/info/exclude
status_before=$(git status --porcelain 2>/dev/null)
excl_before=$([ -f "$excl" ] && wc -c < "$excl" || echo missing)

# --- run, pre-isolated ------------------------------------------------------
# The block sources scripts/smoke-env.sh itself and gets its own temp HOME from
# it. These exports are the fallback for when that does not take effect: they
# are what the block would inherit, and none of them is the real HOME.
safe=$(mktemp -d)
log=$(mktemp)
transcript=$(mktemp)

# Line by line, joining backslash continuations, because the exit status has to
# mean something. Sourcing the whole file reports only its *last* command: a
# first attempt did that and returned 0 for a block with a failing assertion in
# the middle — the same "green means nothing" defect as the block it runs. An
# ERR trap does not fix it either, since it also fires for the command half of a
# deliberate `<cmd>; test $? -eq <n>` pair and cannot tell that from a real
# failure. Set SMOKE_WHOLE_FILE=1 for a section that contains functions or other
# multi-line constructs, and read the per-line output rather than the status.
HOME="$safe" \
XDG_CONFIG_HOME="$safe/.config" \
XDG_DATA_HOME="$safe/.local/share" \
XDG_STATE_HOME="$safe/.local/state" \
XDG_RUNTIME_DIR="$safe/.local/run" \
NO_COLOR=1 \
SMOKE_WHOLE_FILE="${SMOKE_WHOLE_FILE:-0}" \
bash -c '
  block=$1; log=$2; tr=$3; out=$(mktemp); acc=""; start=0; n=0; failed=0
  printf "smoke-run transcript: HOME=%s\n\n" "$HOME" >"$tr"
  if [ "$SMOKE_WHOLE_FILE" = 1 ]; then
    # shellcheck disable=SC1090
    . "$block" >>"$tr" 2>&1; exit $?
  fi
  while IFS= read -r line <&3; do
    n=$((n + 1))
    if [ -n "$acc" ]; then acc="$acc $line"
    else
      case $line in ""|"#"*|" "*"#"*) continue ;; esac
      acc=$line; start=$n
    fi
    case $acc in *\\) acc=${acc%\\}; continue ;; esac
    eval "$acc" >"$out" 2>&1; rc=$?
    printf "\$ %s\n" "$acc" >>"$tr"; cat "$out" >>"$tr"
    if [ "$rc" -ne 0 ]; then
      failed=$((failed + 1))
      printf "  line %-4s rc=%-3s %s\n" "$start" "$rc" "$acc" >>"$log"
      sed "s/^/          | /" "$out" | head -3 >>"$log"
    fi
    acc=""
  done 3< "$block"
  exit "$failed"
' _ "$block" "$log" "$transcript"
rc=$?

# --- report -----------------------------------------------------------------
if [ -s "$log" ]; then
  printf 'smoke-run: %s line(s) exited non-zero:\n' "$rc"
  cat "$log"
  printf 'smoke-run: a section whose prose allows non-zero lines has to say which\n'
else
  printf 'smoke-run: every line exited 0\n'
fi
printf 'smoke-run: full transcript in %s\n' "$transcript"


# --- did anything escape? ---------------------------------------------------
leak=0
status_after=$(git status --porcelain 2>/dev/null)
excl_after=$([ -f "$excl" ] && wc -c < "$excl" || echo missing)

if [ "$status_before" != "$status_after" ]; then
  printf 'smoke-run: LEAK — the checkout changed while the block ran:\n' >&2
  diff <(printf '%s\n' "$status_before") <(printf '%s\n' "$status_after") >&2
  leak=1
fi
if [ "$excl_before" != "$excl_after" ]; then
  printf 'smoke-run: LEAK — %s changed (%s -> %s bytes)\n' \
    "$excl" "$excl_before" "$excl_after" >&2
  leak=1
fi
if [ "$leak" -eq 1 ]; then
  printf 'smoke-run: the block wrote to this repository; treat the run as void\n' >&2
  exit 1
fi

printf 'smoke-run: checkout unchanged (status and %s)\n' "$excl"
exit "$rc"
