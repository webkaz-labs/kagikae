# Documentation maintenance

Read before editing documentation. [../AGENTS.md](../AGENTS.md) owns the per-change
inventory, validation selection and review gates. This document owns reference
maintenance and evidence requirements.

## Placement and retention

- Keep current behavior in its owning contract, open work in ROADMAP, and the
  current release pointer/procedure in RELEASE. History belongs in git log.
- Route detailed guidance from AGENTS with a specific reading trigger. Do not
  reproduce an existing contract or move a historical narrative merely to retain it.
- Before deleting, establish where a reader can recover necessary information.
  Leave a reference when another document owns it.
- Read the complete affected section and each file's opening after editing, from
  the perspective of a new agent. Repair stale self-descriptions and missing steps.
- Keep English and affected Japanese user guidance aligned. Runtime contract
  tokens remain as specified in CLI.

## References and content moves

Before renaming, moving or removing content, search a short distinctive fragment
of its heading with `git grep -n`, then read every inbound reference and its
surrounding text. Wrapped citations can defeat a full-heading search. A surviving
heading does not make removal of its contents safe. Bare filename references and
the file's own opening also need review. Update affected callers and links.

Section citations must quote the target heading verbatim. Do not rely on a search
for the section sign or Markdown extension to find all references. Read
`scripts/docrefs/main.go` and `scripts/check-docs.sh` headers for what the automated
checks can and cannot see; a passing link check is not evidence of correct prose.

When moving, splitting or consolidating prose, run `mise run docs-scan` and assess
its results after reading `scripts/docscan/main.go`'s header. It reports duplication,
not correctness, and fails nothing. It does not replace inbound-reference review.

## Evidence and mutable claims

- Prefer a reproducible derivation to a maintained count. When adding a quantity or
  changing content an existing quantity counts, run `bash scripts/sweep-quantities.sh`
  and inspect every result for possible staleness, regardless of the referenced
  file. Its header defines the report's coverage and positive control.
- Claims that something always or never happens in an external program, another
  file, a past state or an exhaustive set require a source quote, reproducible
  command with dated output, or a dated observation bounded to what was inspected.
  Delete unsupported absolutes. Repository rules are commitments, not measurements.
- Put mutable tree properties in executable tests and CI properties in enforceable
  workflow constraints. Reuse existing checks before introducing another one.
  Ensure a constraint can actually fail and describe only what it enforces.
- Upstream behavior assumptions belong in [VALIDATION.md](VALIDATION.md)
  § Upstream Behaviour Assumptions only when an existing re-executor reaches them;
  state coverage limits. Do not make an untested subject look re-verified.
- Before commit, rerun derivations quoted by added prose on the edited tree.
  Review nearby quantities and claims as well as changed lines: reference checks
  cannot detect all stale assertions in untouched prose.

Examples and checker limitations are maintained beside their consumers:
`scripts/sweep-quantities.sh`, `scripts/docscan/main.go`, `scripts/docrefs/main.go`
and `scripts/check-docs.sh`. Consult those headers before interpreting results.
