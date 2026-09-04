#!/usr/bin/env bash
# Prove that the snap-consumer positive controls in the Harvesting smoke block
# reject the historical false-green shape where snap() reads no snapshot. This
# is release evidence, not a general mutation harness: the one bounded edit and
# the four tagged consumers below are the complete contract of this script.

set -eu

root=$(cd "$(dirname "$0")/.." && pwd -P)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
doc=$work/VALIDATION.md
block=$work/harvest.sh
out=$work/out
cp "$root/docs/VALIDATION.md" "$doc"

old='snap() { base64 -d < "$XDG_DATA_HOME/kagikae/secrets/claude/$1/claude_ai_oauth.secret"; }'
new='snap() { :; }'
awk_old=$old awk_new=$new awk '
  BEGIN { old = ENVIRON["awk_old"]; repl = ENVIRON["awk_new"] }
  /^## Harvesting a credential/ { insec = 1 }
  insec && /^## / && !/^## Harvesting a credential/ { insec = 0 }
  insec && $0 == old { $0 = repl; changed++ }
  { print }
  END { exit(changed == 1 ? 0 : 3) }
' "$doc" > "$doc.new" || {
  printf 'harvest-smoke-selftest: snap mutation did not match exactly once\n' >&2
  exit 1
}
mv "$doc.new" "$doc"

awk '
  /^## Harvesting a credential/ { insec = 1; next }
  insec && /^## /              { insec = 0 }
  insec && /^```bash/          { inblk = 1; next }
  insec && /^```$/             { inblk = 0; next }
  insec && inblk               { print }
' "$doc" > "$block"
controls=$(grep -nE '^snap (solo|main|side) \| grep [A-Z-]+[[:space:]]+# assert: positive control' "$block" || true)
if [ "$(printf '%s\n' "$controls" | grep -c . || true)" -ne 4 ]; then
  printf 'harvest-smoke-selftest: expected exactly four tagged snap controls\n' >&2
  printf '%s\n' "$controls" >&2
  exit 1
fi

set +e
(cd "$root" && SMOKE_DOC="$doc" bash scripts/smoke-run.sh '## Harvesting a credential') > "$out" 2>&1
rc=$?
set -e
if [ "$rc" -eq 0 ]; then
  printf 'harvest-smoke-selftest: empty snap mutation survived\n' >&2
  exit 1
fi

missing=0
while IFS=: read -r line _; do
  if ! grep -Eq "^  line[[:space:]]+${line}[[:space:]]+rc=[1-9]" "$out"; then
    printf 'harvest-smoke-selftest: tagged snap control on block line %s did not bite\n' "$line" >&2
    missing=1
  fi
done <<EOF
$controls
EOF
if [ "$missing" -ne 0 ]; then
  exit 1
fi

printf 'harvest-smoke-selftest: all four tagged snap controls bite\n'
