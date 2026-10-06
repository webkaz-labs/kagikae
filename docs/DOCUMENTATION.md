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
`tools/devtools/docrefs/parser.go` and `scripts/check-docs.sh` headers for what the automated
checks can and cannot see; a passing link check is not evidence of correct prose.

When moving, splitting or consolidating prose, run `mise run docs-scan` and assess
its results after reading `tools/devtools/cmd/docscan/main.go`'s header. It reports duplication,
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
`scripts/sweep-quantities.sh`, `tools/devtools/cmd/docscan/main.go`, `tools/devtools/docrefs/parser.go`
and `scripts/check-docs.sh`. Consult those headers before interpreting results.

## Reuse in another project

Start at [tools/devtools](../tools/devtools/README.md) for the shared entrypoints.
Use a trusted, revision-pinned checkout of kagikae as the tool source; keep the
inspected project separate. No files need to be copied into that project. Go and
Bash are prerequisites. The portable checks use the same implementations as this
repository's checks; they do not install kagikae or execute the inspected project's
programs. `tools/devtools/portable` tests the commands against a foreign fixture project.

From the kagikae checkout, inspect another project's relative Markdown links and
named section citations:

```bash
bash tools/devtools/shell/check-docs.sh /absolute/path/to/project
```

Portable mode accepts directory links and omits kagikae's required documents,
Documentation Map, domain layout and count floors. Broken recognized references
and extractor failures still fail. A report with no checkable references explicitly
states its coverage limit; success does not establish complete Markdown coverage.
The syntax exclusions in `tools/devtools/docrefs/parser.go` still apply, including external
URLs and unsupported citation/link forms. The target need not be a Go module;
the extractor runs from the tool source with workspace overrides disabled.
Calling `scripts/check-docs.sh` without arguments retains kagikae's full document policy.

Compare prose using Go identifiers from the target, optionally supplemented by
terms in the first column of Markdown tables under selected H2 headings:

```bash
GOWORK=off go run ./tools/devtools/cmd/docscan -portable -root /absolute/path/to/project
GOWORK=off go run ./tools/devtools/cmd/docscan -portable -root /absolute/path/to/project -glossary docs/TERMS.md -glossary-sections 'Vocabulary,Domain terms'
```

The glossary path is relative to the target. An explicitly requested missing file
fails. Without Go identifiers or glossary terms there are no anchors to compare;
the report distinguishes that case and prints the number of candidate pairs. A
zero finding count is not a statement about correctness or exhaustive duplication.
Glossary input is always explicit. `mise run docs-scan` supplies kagikae's glossary
and heading selection; the common command has no product glossary default.

For quantity wording in a Git diff, run from the inspected Git working tree and
point at the trusted tool checkout:

```bash
bash /absolute/path/to/kagikae/tools/devtools/shell/sweep-quantities.sh trunk
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
bash /absolute/path/to/kagikae/tools/devtools/shell/check-go-format.sh --portable /absolute/path/to/project
```

The target must contain its own `go.mod`. Portable mode disables inherited Go
workspace settings and `GOFLAGS`; goimports derives the local import prefix from
the target module. The formatters report files without rewriting them. Missing
modules, formatter errors and formatting findings fail the command. Without
arguments, the existing current-directory behavior is retained for mise and CI.
Analyzer versions are pinned in the script. Go may download those analyzers and
populate caches. `GOCLI_LINT_CACHE_DIR` selects their cache root; the default is
`${XDG_CACHE_HOME:-$HOME/.cache}/kae-lint`. The `vuln` task uses `GOCLI_AUDIT_CACHE_DIR`
and `kae-audit`. The full resolution order is in `tools/devtools/shell/cache-root.sh`.

## Reuse distribution and completion verification

[Distribution verification](DISTRIBUTION-VERIFY.md) describes the portable
`distributionverify` and `completionverify` commands, trusted product specs,
synthetic versus published trust, dependencies and coverage limits. The existing
kagikae adapters use the shared verification implementations.

## Scope of the remaining scripts

| Scripts | Reuse boundary |
|---|---|
| `docrefs` | Reference extractor used by portable `check-docs.sh`; use that wrapper for a pass/fail check |
| `smoke-run.sh`, `smoke-env.sh` and smoke selftests | Coupled to kagikae validation blocks, credential isolation and leak guards; not a general shell sandbox |
| `distributionverify` | Go, gh and Packslip; trusted product spec; native/installer modes execute in temporary HOME/XDG roots; synthetic mode explicitly omits provenance |
| `completionverify` | Go plus real Bash and Zsh; trusted scripts, binary directory and candidate cases; no fish/interactive-TTY claim |
| `install.sh`, `install-local.sh`, `installverify` | Product installation/receipt/lock adapters; shared fixture transport in `internal/distribution` |
| `releaseverify`, `packslipverify`, `release-completions.sh`, `release-smoke/` | kagikae identity, historical release policy and lifecycle adapters; shared distribution/completion engines |
| `namingagreement`, `harvest-smoke-selftest.sh` | kagikae credential behavior and upstream authentication assumptions |
| `internal/commandrun`, `internal/distribution`, `internal/completioncheck` | Internal maintainer libraries consumed through portable commands, not separately installable modules |

Product receipt, credential and lifecycle rules require their own adapter and
acceptance. The selftests remain beside the checks they exercise.

## Maintainer task selection

Keep routine validation, installation and release entrypoints visible in mise.
Implementation checks stay individually callable but use `hide = true` when they
are components of those entrypoints. Preserve their dependency ordering. Harness
refusal controls belong in Go tests; an installed upstream program is inspected
only through an explicit, reviewed-artifact verification command.

## Shared tooling boundary

Place reusable development commands, shell entrypoints and libraries under
`tools/devtools` within the repository's existing Go module. Keep product-specific
validation policy and release adapters under `scripts`; pass glossary and other
product expectations explicitly. Shared libraries must not import application
packages. Their directory must remain importable by those adapters, rather than
using a Go `internal` boundary that excludes `scripts`.
