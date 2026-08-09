#!/usr/bin/env bash
# Extract the fenced bash blocks under a heading in docs/VALIDATION.md and run
# them in isolation. From the repository root:
#
#   bash scripts/smoke-run.sh '## Smoke Checks'
#   SMOKE_WHOLE_FILE=1 bash scripts/smoke-run.sh '## v0.17.0 surface — the credential harvest'
#
# Use SMOKE_WHOLE_FILE=1 for a section that defines functions or other multi-line
# constructs (the harvest section does). That mode has no per-line verdict and
# says so; the default mode is the one whose exit status means something.
#
# `bash scripts/smoke-run-selftest.sh` checks this script's own guards, and
# `mise run check` runs it.
#
# This exists instead of a paragraph telling the reader to be careful, because
# every hand-written harness for this file has leaked, and the leaks write to
# the machine rather than failing. Measured ways, all closed here by
# construction rather than by warning:
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
# WHAT THE ISOLATION DOES AND DOES NOT COVER. It redirects HOME, every XDG root
# and TMPDIR into a temp HOME and *unsets* the tool-home variables, before the
# block runs, so a block whose own preamble silently fails to take effect still
# cannot reach the real ones. It also forces `KAE_CLAUDE_DRIVER=file` for every
# section, so a section that wants claude's keychain driver cannot be run through
# this script. The tool-home variables are not decoration: `CODEX_HOME`,
# `CLAUDE_CONFIG_DIR`, `COPILOT_HOME` and `CLAUDE_SECURESTORAGE_CONFIG_DIR`
# **outrank** the temp HOME, and a preamble-less block inheriting one of them was
# measured writing outside the sandbox while this script reported a clean run.
#
# `MISE_CEILING_PATHS` is the one entry here that has nothing to do with HOME, and
# guessing at it from the others gets it backwards. mise does not reach the
# operator's config through HOME or any XDG root: it walks **up from the current
# directory**, and a block starts in the checkout, which is inside the operator's
# home. So redirecting HOME does nothing for it — measured, from the repo root mise
# loaded two configs outside the sandbox; from a cwd inside the sandbox, none. A
# ceiling stops that walk, and **both** entries earn their place: the real home
# alone still lets the checkout's own `mise.toml` in, since the walk collects
# configs on the way up to the ceiling rather than only at it. `MISE_GLOBAL_CONFIG_FILE`
# and `MISE_IGNORED_CONFIG_PATHS` were both measured *not* closing this, for the
# same reason — the file is not being loaded as the global config in the first place.
#
# The reachable failure this prevents is not a stray read. Without the ceiling, a
# block running mise from the checkout fails with `error parsing config file`,
# whose real cause one line down is `not trusted` (this script redirects
# XDG_STATE_HOME, and mise keeps `trusted-configs` there, so the sandbox's trust
# store is empty). The documented cure for that, `MISE_TRUSTED_CONFIG_PATHS=/`,
# makes mise *load* the operator's global config and evaluate its `exec()`
# templates — one of which resolved a live GitHub token into a run transcript on
# 2026-08-09. So: never answer a mise trust error in a smoke block by disabling
# trust. With the ceiling there is nothing untrusted to read and the error does not
# arise (measured: `rc=0`, no trust override).
#
# It cannot isolate **the macOS login keychain**, which ignores `$HOME`
# entirely. `KAE_CLAUDE_DRIVER=file` below keeps claude's own credential in a
# file, but kae's *snapshot* store still follows `secret_backend`, and only the
# block's own config.toml can set that — a block that fails to write one will
# put captured payloads in the `kagikae` keychain item on darwin. That is the
# defect that put 956 items in the operator's login keychain, and no environment
# prefix can prevent it. The leak detector below sees the checkout only: writes
# elsewhere on the machine are not detected.
#
# There is also **no wall-clock cap**: a block containing a command that blocks
# stalls this script indefinitely with no diagnostic. Deliberately not added — a
# cap generous enough for the real sections would not catch much, and one tight
# enough to catch a hang would kill a slow legitimate run. Interrupt it yourself;
# the EXIT trap still cleans up. (Observed only in a mutant, never in a real
# block.)

set -u

heading=${1-'## Smoke Checks'}
# SMOKE_DOC exists so this script's own guards can be tested against fixture
# documents that deliberately leak; without it the leak detector and the
# isolation fallback are two more assertions nothing ever evaluates.
doc=${SMOKE_DOC:-docs/VALIDATION.md}

if [ ! -f "$doc" ] || [ ! -f go.mod ]; then
  printf 'smoke-run: run me from the repository root (no %s)\n' "$doc" >&2
  exit 2
fi

# Go's module cache is written read-only, so a plain `rm -rf` on the sandbox
# fails noisily and leaves it behind.
cleanup() {
  [ -n "${safe:-}" ] && chmod -R u+w "$safe" 2>/dev/null
  rm -rf "${block:-}" "${log:-}" "${safe:-}" "${consumed:-}"
}
trap cleanup EXIT

# The build caches stay OUTSIDE the sandbox, deliberately. A block starts with
# `go build`, and with HOME redirected the module cache lands in the sandbox and
# is re-downloaded on every run. These hold compiler output, not credential
# state, so they are not what the isolation is protecting.
#
# Only exported when `go env` actually answered: setting them EMPTY is not the
# same as not setting them — Go falls back to $HOME/go/pkg/mod, which is inside
# the sandbox, i.e. exactly the re-download this is avoiding. `env` below is not
# given -i, so it inherits these.
go_cache=$(go env GOMODCACHE GOCACHE 2>/dev/null || true)   # one call, in argument order
gomodcache=$(printf '%s\n' "$go_cache" | sed -n 1p)
gocache=$(printf '%s\n' "$go_cache" | sed -n 2p)
if [ -n "$gomodcache" ]; then export GOMODCACHE="$gomodcache"; fi
if [ -n "$gocache" ]; then export GOCACHE="$gocache"; fi

# --- extract ---------------------------------------------------------------
# The heading is matched as a prefix: the real ones carry a parenthetical
# ("## Smoke Checks (built binary, isolated env)") that a caller should not have
# to reproduce, and requiring equality silently found nothing. A prefix can
# match several headings, though, and concatenating four sections into one run
# and calling it one block is the silent-subset failure this tool exists to
# stop — so that is an error, not a merge.
matches=$(awk -v h="$heading" 'index($0, h) == 1 && /^## / { print }' "$doc")
count=$(printf '%s' "$matches" | grep -c . || true)
if [ "$count" -gt 1 ]; then
  printf 'smoke-run: %s headings start with %s; name one of them:\n' "$count" "$heading" >&2
  printf '%s\n' "$matches" >&2
  exit 2
fi

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
# Content, not size: `kae pin` appends so a size check would catch the realistic
# case, but the printed claim would be stronger than the check.
gitdir=$(git rev-parse --git-common-dir 2>/dev/null || true)
if [ -z "$gitdir" ]; then
  printf 'smoke-run: not inside a git repository; the leak detector needs one\n' >&2
  exit 2
fi
excl="$gitdir/info/exclude"
status_before=$(git status --porcelain 2>/dev/null)
excl_before=$([ -f "$excl" ] && cat "$excl" || echo missing)

# --- run, pre-isolated ------------------------------------------------------
safe=$(mktemp -d)
log=$(mktemp)
transcript=$(mktemp)
consumed=$(mktemp)

# Line by line, joining backslash continuations, because the exit status has to
# mean something. Sourcing the whole file reports only its *last* command: a
# first attempt did that and returned 0 for a block with a failing assertion in
# the middle — the same "green means nothing" defect as the block it runs. An
# ERR trap does not fix it either, since it also fires for the command half of a
# deliberate `<cmd>; test $? -eq <n>` pair and cannot tell that from a real
# failure.
# MISE_CONFIG_DIR is not decoration either: `kae completion --install` and
# `kae mise init --auto --write` locate mise's *global* config.toml through it,
# so an operator who exports it has those written outside the sandbox.
env -u CODEX_HOME -u CLAUDE_CONFIG_DIR -u COPILOT_HOME \
    -u CLAUDE_SECURESTORAGE_CONFIG_DIR -u OPENCODE_AUTH_CONTENT \
    -u MISE_CONFIG_DIR -u KAE_PROFILE -u KAE_FINGERPRINT \
  HOME="$safe" \
  XDG_CONFIG_HOME="$safe/.config" \
  XDG_DATA_HOME="$safe/.local/share" \
  XDG_STATE_HOME="$safe/.local/state" \
  XDG_RUNTIME_DIR="$safe/.local/run" \
  TMPDIR="$safe/tmp" \
  NO_COLOR=1 \
  KAE_CLAUDE_DRIVER=file \
  MISE_CEILING_PATHS="$(pwd -P):$(cd "$HOME" && pwd -P)" \
  SMOKE_WHOLE_FILE="${SMOKE_WHOLE_FILE:-0}" \
  bash -c '
  # Before anything else: GNU `mktemp` honours TMPDIR and fails on a missing
  # directory, so `out=$(mktemp)` above this line yields an empty path on linux
  # and every subsequent redirect fails. (darwin mktemp ignores TMPDIR, which is
  # why the wrong order was invisible here.)
  mkdir -p "$TMPDIR"
  block=$1; log=$2; tr=$3; lines=$4; out=$(mktemp); acc=""; start=0; n=0; failed=0
  printf "smoke-run transcript: HOME=%s\n\n" "$HOME" >"$tr"
  if [ "$SMOKE_WHOLE_FILE" = 1 ]; then
    # shellcheck disable=SC1090
    . "$block" >>"$tr" 2>&1; exit $?
  fi
  # How far the loop got, rewritten every iteration rather than set on the way out.
  # The report needs this rather than the exit status: a block that ends itself with
  # `exit 0` — idiomatic at the end of a fixture script — leaves a zero status and an
  # empty log, which is the "green" shape exactly, so keying the check on a non-zero
  # status closed only half the hole (found in review, 2026-08-09). An EXIT trap would
  # be tidier and is deliberately not used: this whole script is a single-quoted
  # argument to `bash -c`, so a trap body needs `'"'"'` quoting to survive, and getting
  # that wrong expands the counter in the OUTER shell instead.
  while IFS= read -r line <&3; do
    n=$((n + 1)); printf "%s\n" "$n" > "$lines"
    if [ -n "$acc" ]; then acc="$acc $line"
    else
      # Strip leading whitespace and test the FIRST character. Matching "#"
      # anywhere in an indented line drops indented *commands* that merely carry
      # a trailing comment — a silent subset, inside the tool built to stop
      # silent subsets.
      stripped=${line#"${line%%[! 	]*}"}
      case $stripped in ""|"#"*) continue ;; esac
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
  # Clamped: an exit status is mod 256, so 256 failing lines exited 0 and the
  # report called it 0 failures. Reachable — the harvest block is ~400 lines and
  # an unbuilt /tmp/kae fails nearly all of them.
  [ "$failed" -gt 0 ] && exit 1
  exit 0
' _ "$block" "$log" "$transcript" "$consumed"
rc=$?

# --- report -----------------------------------------------------------------
if [ "${SMOKE_WHOLE_FILE:-0}" = 1 ]; then
  # No per-line verdict exists in this mode, so do not print one. Saying
  # "every line exited 0" here (which an earlier version did, unconditionally)
  # reproduces the defect the per-line loop was written to fix.
  printf 'smoke-run: whole-file mode — block exit status %s, NO per-line verdict\n' "$rc"
else
  # Completeness and per-line failures are INDEPENDENT questions, and an earlier
  # version made the second outrank the first with an `elif`: a block that failed a
  # line *and* then ended early printed the failing line and nothing else, so the
  # verdict implied every other line had run. Both are reported (found in review,
  # 2026-08-09).
  total=$(wc -l < "$block" | tr -d ' ')
  got=$(cat "$consumed" 2>/dev/null || echo 0)
  [ -n "$got" ] || got=0
  if [ -s "$log" ]; then
    printf 'smoke-run: %s line(s) exited non-zero:\n' "$(grep -c '^  line ' "$log")"
    cat "$log"
    printf 'smoke-run: a section whose prose allows non-zero lines has to say which\n'
  fi
  if [ "$got" -ne "$total" ]; then
  # The loop itself only ever returns 0 or 1, and the 1 is always accompanied by a
  # log entry — so an empty log beside a non-zero status means the block ended
  # *itself* and the lines after that point never ran. Reporting "every line exited
  # 0" here is the exact defect this tool exists to stop, and it did exactly that
  # until 2026-08-09: an unterminated here-document (`cat > f <<'EOF'` is one line
  # to this loop, so the body that follows gets evaluated as commands) reached an
  # `exit 9` in that body, which killed the inner shell after 35 of 146 lines. The
  # run printed "every line exited 0" and exited 9, and nothing reads the exit code
  # of a line that already told you it was green.
    printf 'smoke-run: the block ENDED EARLY — %s of %s lines were read (block exit\n' "$got" "$total"
    printf '           status %s). The lines after that point never ran, whatever the\n' "$rc"
    printf '           status says. A multi-line construct (here-document, function body)\n'
    printf '           is the usual cause: this loop evaluates one line at a time. Use\n'
    printf '           SMOKE_WHOLE_FILE=1 for such a section, or rewrite it as single lines.\n'
    rc=1
  elif [ ! -s "$log" ]; then
    printf 'smoke-run: every line exited 0\n'
  fi
fi
printf 'smoke-run: full transcript in %s\n' "$transcript"

# --- did anything escape? ---------------------------------------------------
leak=0
status_after=$(git status --porcelain 2>/dev/null)
excl_after=$([ -f "$excl" ] && cat "$excl" || echo missing)

if [ "$status_before" != "$status_after" ]; then
  printf 'smoke-run: LEAK — the checkout changed while the block ran:\n' >&2
  diff <(printf '%s\n' "$status_before") <(printf '%s\n' "$status_after") >&2
  leak=1
fi
if [ "$excl_before" != "$excl_after" ]; then
  printf 'smoke-run: LEAK — %s changed while the block ran\n' "$excl" >&2
  leak=1
fi
if [ "$leak" -eq 1 ]; then
  printf 'smoke-run: the block wrote to this repository; treat the run as void\n' >&2
  exit 1
fi

printf 'smoke-run: checkout unchanged (git status and info/exclude only — a write\n'
printf '           elsewhere on this machine, including the login keychain, is not\n'
printf '           something this check can see)\n'
exit "$rc"
