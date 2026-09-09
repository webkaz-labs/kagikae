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

## Reuse in another project

Use a trusted, revision-pinned checkout of kagikae as the tool source; keep the
inspected project separate. No files need to be copied into that project. Go and
Bash are prerequisites. The portable checks use the same implementations as this
repository's checks; they do not install kagikae or execute the inspected project's
programs. `scripts/portable` tests the commands against a foreign fixture project.

From the kagikae checkout, inspect another project's relative Markdown links and
named section citations:

```bash
bash scripts/check-docs.sh --portable /absolute/path/to/project
```

Portable mode accepts directory links and omits kagikae's required documents,
Documentation Map, domain layout and count floors. Broken recognized references
and extractor failures still fail. A report with no checkable references explicitly
states its coverage limit; success does not establish complete Markdown coverage.
The syntax exclusions in `scripts/docrefs/main.go` still apply, including external
URLs and unsupported citation/link forms. The target need not be a Go module;
the extractor runs from the tool source with workspace overrides disabled.
Calling `check-docs.sh` without arguments retains kagikae's full document policy.

Compare prose using Go identifiers from the target, optionally supplemented by
terms in the first column of Markdown tables under selected H2 headings:

```bash
GOWORK=off go run ./scripts/docscan -portable -root /absolute/path/to/project
GOWORK=off go run ./scripts/docscan -portable -root /absolute/path/to/project -glossary docs/TERMS.md -glossary-sections 'Vocabulary,Domain terms'
```

The glossary path is relative to the target. An explicitly requested missing file
fails. Without Go identifiers or glossary terms there are no anchors to compare;
the report distinguishes that case and prints the number of candidate pairs. A
zero finding count is not a statement about correctness or exhaustive duplication.
Without `-portable`, the existing kagikae glossary defaults remain in effect.

For quantity wording in a Git diff, run from the inspected Git working tree and
point at the trusted tool checkout:

```bash
bash /absolute/path/to/kagikae/scripts/sweep-quantities.sh --portable trunk
```

Supply the base branch/ref (`main` is the default). Portable mode uses embedded
positive controls through the same matcher instead of kagikae's historical commit.
The merge-base-to-working-tree diff includes tracked committed and uncommitted
changes, but not untracked files. Stage new documents before inspecting them.
This is an English wording report for human triage, not a correctness gate or a
Japanese prose checker. Existing repository mode keeps its historical control.

These commands read the target; Go may populate its build/module caches. Existing
walk exclusions and parser limits remain part of each command's contract. Do not
apply portable mode to weaken this repository's normal commit gate.

## Reuse Go formatting checks

Run the pinned gofumpt and goimports checks against a trusted Go module:

```bash
bash /absolute/path/to/kagikae/scripts/check-go-format.sh --portable /absolute/path/to/project
```

The target must contain its own `go.mod`. Portable mode disables inherited Go
workspace settings and `GOFLAGS`; goimports derives the local import prefix from
the target module. The formatters report files without rewriting them. Missing
modules, formatter errors and formatting findings fail the command. Without
arguments, the existing current-directory behavior is retained for mise and CI.
Analyzer versions are pinned in the script. Go may download those analyzers and
populate caches; `GOCLI_LINT_CACHE_DIR` selects their cache root.

## Scope of the remaining scripts

| Scripts | Reuse boundary |
|---|---|
| `docrefs` | Reference extractor used by portable `check-docs.sh`; use that wrapper for a pass/fail check |
| `smoke-run.sh`, `smoke-env.sh` and smoke selftests | Coupled to kagikae's validation blocks, credential isolation and leak guards; not a general shell sandbox |
| `install.sh`, `install-local.sh`, `installverify` | Coupled to kae archives, installation receipts and lifecycle commands |
| `releaseverify`, `packslipverify`, `release-completions.sh`, `release-smoke/` | Coupled to kagikae release identity, completion registry and acceptance behavior |
| `namingagreement`, `harvest-smoke-selftest.sh` | Check kagikae credential behavior and upstream authentication assumptions |
| `internal/commandrun` | Shared subprocess helper for these Go tools; an internal package, not a separately installable library |

These product-specific commands need a separate design and verification scope
before reuse. Changing their names or repository constants alone does not adapt
their contracts. The selftests remain beside the checks they exercise.
