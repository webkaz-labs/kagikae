#!/usr/bin/env bash
# Mutation checks for the guards in smoke-run.sh and smoke-run-selftest.sh.
# This is deliberately a closed list of concrete edits, not a mutation framework.
# `fast` runs the four defect shapes that previously shipped; `full` pairs every
# current guard with a mutation that must make that guard report FAIL. Both modes
# use fixed batches of eight independent fixtures (the fast set naturally fills four);
# results are emitted in table order, so concurrency shortens the run without making
# its evidence nondeterministic.

set -u

mode=${1:-full}
case $mode in fast|full) ;; *) printf 'usage: %s [fast|full]\n' "$0" >&2; exit 2 ;; esac

root=$(cd "$(dirname "$0")/.." && pwd -P)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
printf '[env]\nSMOKE_OUTSIDE = "1"\n' > "$work/mise.toml"
failed=0
ran=0
pids=()
indices=()

fixture() {
  dir=$work/$1
  mkdir -p "$dir/scripts"
  cp "$root/go.mod" "$dir/go.mod"
  cp "$root/scripts/smoke-env.sh" "$root/scripts/smoke-run.sh" \
    "$root/scripts/smoke-run-selftest.sh" "$dir/scripts/"
  git -C "$dir" init -q
  printf '%s' "$dir"
}

subst_once() {
  file=$1 old=$2 new=$3
  awk_old=$old awk_new=$new awk '
    BEGIN { old = ENVIRON["awk_old"]; repl = ENVIRON["awk_new"] }
    !hit && (p = index($0, old)) {
      $0 = substr($0, 1, p - 1) repl substr($0, p + length(old)); hit = 1
    }
    { print }
    END { exit(hit ? 0 : 3) }
  ' "$file" > "$file.new" || return 1
  mv "$file.new" "$file"
}

mutate() {
  dir=$1 id=$2 runner=$dir/scripts/smoke-run.sh self=$dir/scripts/smoke-run-selftest.sh preamble=$dir/scripts/smoke-env.sh
  case $id in
    drop-cleared) subst_once "$runner" '-u CODEX_HOME -u CLAUDE_CONFIG_DIR -u COPILOT_HOME' '-u CLAUDE_CONFIG_DIR -u COPILOT_HOME' ;;
    empty-preamble) subst_once "$self" "grep -oE '^export [A-Z_]+' scripts/smoke-env.sh" "grep -oE '^zzexport [A-Z_]+' scripts/smoke-env.sh" ;;
    unowned-preamble) subst_once "$preamble" '"${TMPDIR:-/tmp}/kae-smoke.XXXXXXXX"' '"${SMOKE_UNOWNED_PARENT:-${TMPDIR:-/tmp}}/kae-smoke.XXXXXXXX"' ;;
    failed-allocation) subst_once "$preamble" ' || { unset kae_smoke_home; return 1; }' '' ;;
    checkout-leak) subst_once "$runner" 'if [ "$status_before" != "$status_after" ]; then' 'if [ "$status_before" = "$status_after" ]; then' ;;
    exclude-leak) subst_once "$runner" 'if [ "$excl_before" != "$excl_after" ]; then' 'if [ "$excl_before" = "$excl_after" ]; then' ;;
    restore-missing) subst_once "$self" 'else rm -f "$excl"; fi' 'else :; fi' ;;
    root-escape) subst_once "$runner" '  XDG_DATA_HOME="$safe/.local/share" \' '  XDG_DATA_HOME="$safe/../escape" \' ;;
    driver) subst_once "$runner" '  KAE_CLAUDE_DRIVER=file \' '  KAE_CLAUDE_DRIVER=bogus \' ;;
    ignore-failures) subst_once "$runner" '    if [ "$rc" -ne 0 ]; then' '    if false; then' ;;
    many-257) subst_once "$self" 'for _ in $(seq 256)' 'for _ in $(seq 257)' ;;
    whole-verdict) subst_once "$runner" "printf 'smoke-run: whole-file mode — block exit status" "printf 'smoke-run: every line exited 0; whole-file mode — block exit status" ;;
    whole-label) subst_once "$runner" 'smoke-run: whole-file mode — block exit status' 'smoke-run: whole-file — block exit status' ;;
    skip-indent) subst_once "$runner" 'case $stripped in ""|"#"*)' 'case $stripped in ""|*"#"*)' ;;
    allow-ambiguous) subst_once "$runner" 'if [ "$count" -gt 1 ]; then' 'if [ "$count" -gt 2 ]; then' ;;
    allow-empty) subst_once "$runner" 'if [ ! -s "$block" ]; then' 'if [ ! -f "$block" ]; then' ;;
    cross-heading) subst_once "$runner" '  insec && /^## /             { insec = 0 }' '  insec && /^### /            { insec = 0 }' ;;
    split-continuation) subst_once "$runner" '    if [ -n "$acc" ]; then acc="$acc $line"' '    if false; then acc="$acc $line"' ;;
    color) subst_once "$runner" '  NO_COLOR=1 \' '  NO_COLOR=0 \' ;;
    ceiling) subst_once "$runner" 'MISE_CEILING_PATHS="$(pwd -P):$(cd "$HOME" && pwd -P)"' 'MISE_CEILING_PATHS="/tmp/not-the-smoke-sandbox"' ;;
    early) subst_once "$runner" '  if [ "$got" != complete ]; then' '  if [ "$got" = complete ]; then' ;;
    dangling) subst_once "$runner" '  if [ -z "$acc" ]; then printf "complete\n" > "$lines"; fi' '  printf "complete\n" > "$lines"' ;;
    no-markers) subst_once "$runner" 'markers=$(grep -Ein '\''^#[[:space:]]*(assert|expect|verify|check|confirm|ensure)[[:space:]]*:'\'' "$block" || true)' 'markers=' ;;
    marker-one-space) subst_once "$runner" "'^#[[:space:]]*(assert|expect|verify|check|confirm|ensure)" "'^#   (assert|expect|verify|check|confirm|ensure)" ;;
    marker-case) subst_once "$runner" 'grep -Ein ' 'grep -En ' ;;
    marker-word) subst_once "$runner" '(assert|expect|verify|check|confirm|ensure)' '(assert)' ;;
    marker-colon) subst_once "$runner" ')[[:space:]]*:' ') *:' ;;
    marker-wide) subst_once "$runner" "'^#[[:space:]]*" "'^.*#[[:space:]]*" ;;
    marker-prose) subst_once "$runner" "'^#[[:space:]]*(assert|expect|verify|check|confirm|ensure)[[:space:]]*:'" "'^#.*(assert|expect|verify|check|confirm|ensure)[[:space:]]*:'" ;;
    *) return 1 ;;
  esac
}

baseline=$(fixture baseline)
baseline_out=$work/baseline.out
if ! (cd "$baseline" && bash scripts/smoke-run-selftest.sh > "$baseline_out"); then
  printf 'smoke-run-mutations: unmutated selftest failed\n' >&2
  exit 1
fi
: > "$work/table-guards"

run_mutation_case() {
  index=$1 id=$2 guard=$3
  result=$work/result-$index marker=$work/ok-$index
  dir=$(fixture "m$index")
  if ! mutate "$dir" "$id"; then
    printf 'FAIL  %s (mutation %s could not be applied)\n' "$guard" "$id" > "$result"
    return
  fi
  (cd "$dir" && bash scripts/smoke-run-selftest.sh > "$dir/out" 2>&1); rc=$?
  if [ "$rc" -eq 0 ] || ! grep -Fq "FAIL  $guard" "$dir/out"; then
    {
      printf 'FAIL  mutation %s did not trip %s (exit %s)\n' "$id" "$guard" "$rc"
      grep '^FAIL' "$dir/out" | sed 's/^/      /'
    } > "$result"
    return
  fi
  printf 'ok    %s\n' "$guard" > "$result"
  : > "$marker"
}

flush_workers() {
  for pid in "${pids[@]}"; do
    wait "$pid" || true
  done
  for index in "${indices[@]}"; do
    cat "$work/result-$index"
    if [ ! -f "$work/ok-$index" ]; then
      failed=$((failed + 1))
    fi
  done
  pids=()
  indices=()
}

while IFS='|' read -r tier id guard baseline_guard; do
  [ -n "$tier" ] || continue
  if [ "$mode" = fast ] && [ "$tier" != fast ]; then continue; fi
  ran=$((ran + 1))
  printf '%s\n' "${baseline_guard:-$guard}" >> "$work/table-guards"
  run_mutation_case "$ran" "$id" "$guard" &
  pids+=("$!")
  indices+=("$ran")
  if [ "${#pids[@]}" -eq 8 ]; then
    flush_workers
  fi
done <<'MUTATIONS'
fast|drop-cleared|scripts/smoke-run.sh and this file disagree about which variables are handled|the runner sets exactly the variables this file guards
fast|empty-preamble|scripts/smoke-env.sh isolates|the runner isolates everything scripts/smoke-env.sh does
fast|root-escape|a root escaped the sandbox|all 6 roots land inside the sandbox
fast|many-257|256 failing lines do not wrap to exit 0
full|checkout-leak|a block touching the checkout is caught
full|exclude-leak|an append to info/exclude is caught
full|restore-missing|restore_excl removes a file that did not exist before
full|unowned-preamble|sourced HOME is reclaimed on success and failure without deleting outside files
full|failed-allocation|failed preamble allocation preserves the caller environment
full|drop-cleared|only 7 of 8 tool variables cleared|all 8 inherited tool variables are cleared
full|driver|claude is forced onto the file driver
full|ignore-failures|a failing line in the middle fails the run
full|ignore-failures|a failing LAST line fails the run
full|whole-verdict|whole-file mode must not print a per-line verdict|whole-file mode does not print a per-line verdict
full|whole-label|whole-file mode says which mode it is
full|skip-indent|an indented command with a comment is not skipped
full|allow-ambiguous|an ambiguous heading prefix is refused
full|allow-empty|a heading with no block is refused
full|cross-heading|extraction stops at the next heading
full|split-continuation|a backslash continuation is joined, not split
full|color|colour is disabled for the block
full|ceiling|mise cannot reach a config outside the sandbox
full|early|a block that ends early is not reported as green
full|early|a block that ends early with status 0 is caught too
full|early|a failing line and an early end are both reported
full|early|a block that ends on its last line is caught
full|dangling|a dangling continuation on the last line is caught
full|no-markers|a column-0 assert comment is refused
full|no-markers|the refusal comes before the block runs
full|marker-one-space|a one-space column-0 assert comment is refused too
full|marker-case|a capitalised column-0 assert comment is refused
full|marker-word|a column-0 marker spelled expect: is refused too
full|marker-colon|both space and tab before the marker colon are refused
full|marker-word|the runner refuses exactly the marker words this file declares
full|marker-wide|a trailing assert comment on a real command is accepted
full|marker-wide|an accepted block does reach its first line
full|marker-prose|a column-0 comment whose assert: follows prose is accepted
MUTATIONS
if [ "${#pids[@]}" -gt 0 ]; then
  flush_workers
fi

# The full table must cover the selftest that exists now, not a remembered count.
# A guard added without a mutation, or a stale mutation left after a guard is removed,
# changes one side of this comparison and fails release evidence.
if [ "$mode" = full ]; then
  sed -n 's/^ok    //p' "$baseline_out" | LC_ALL=C sort > "$work/baseline-guards"
  LC_ALL=C sort "$work/table-guards" > "$work/sorted-table-guards"
  if ! diff -u "$work/baseline-guards" "$work/sorted-table-guards" > "$work/coverage-diff"; then
    printf 'FAIL  full mutation table does not pair every current guard:\n' >&2
    cat "$work/coverage-diff" >&2
    failed=$((failed + 1))
  fi
fi

if [ "$failed" -ne 0 ]; then
  printf 'smoke-run-mutations: %s of %s mutation checks failed\n' "$failed" "$ran" >&2
  exit 1
fi
printf 'smoke-run-mutations: all %s %s mutation checks bite\n' "$ran" "$mode"
