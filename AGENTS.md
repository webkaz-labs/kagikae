# kagikae working guide

Read the user-level `~/.agents/skills/go-cli-tooling/SKILL.md` and applicable
references. This repository deliberately does not bundle that standard; do not
copy it here even if its template recommends doing so. A local copy becomes stale;
[docs/ROADMAP.md](docs/ROADMAP.md) tracks the remaining upstream-standard issues.
If the installed skill is unavailable, report it rather than restoring a snapshot.

Read only the documents relevant to the task through the map below. Keep this file
for always-needed rules and routing; put detailed contracts in their owning docs.

## Documentation Map

| Document | When To Read |
|----------|--------------|
| [docs/DOCUMENTATION.md](docs/DOCUMENTATION.md) | document edits, moves, deletions and evidence checks |
| [docs/DISTRIBUTION-VERIFY.md](docs/DISTRIBUTION-VERIFY.md) | reusing distribution, installer and shell completion verification |
| [docs/memories.md](docs/memories.md)  | maintenance/verification tooling; durable working preferences |
| [README.md](README.md)  | user-facing commands and setup |
| [README.ja.md](README.ja.md)  | Japanese setup; update with English guidance |
| [docs/PRODUCT.ja.md](docs/PRODUCT.ja.md)  | Japanese product scope; PRODUCT.md is normative |
| [docs/GUIDE.ja.md](docs/GUIDE.ja.md)  | Japanese daily use, recovery and safety; CLI.md is normative |
| [docs/CONTEXT.md](docs/CONTEXT.md)  | terminology before naming; behavior belongs in the owning contract |
| [docs/L10N-JA.md](docs/L10N-JA.md)  | writing or changing Japanese output strings: terms, style, allowed characters |
| [docs/PRODUCT.md](docs/PRODUCT.md)  | scope and modes; read § Tool Tiers before widening tool support |
| [docs/ADAPTERS.md](docs/ADAPTERS.md)  | adapter mutations; § Verified Upstream Versions before changing verification metadata |
| [docs/ADAPTERS-COMPANION.md](docs/ADAPTERS-COMPANION.md)  | git/gh/cloud companion authentication |
| [docs/CREDENTIAL-RULES.md](docs/CREDENTIAL-RULES.md)  | before writing, harvesting, attributing, ordering or deleting credential copies |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)  | packages, transactions and locks; § Known Traps before file mutation |
| [docs/CLI.md](docs/CLI.md)  | commands, output and JSON; § Keeping completion current before router changes |
| [docs/DATA-MODEL.md](docs/DATA-MODEL.md)  | configuration, state, snapshots, receipts and secret references |
| [docs/SECURITY.md](docs/SECURITY.md)  | secrets, permissions, subprocesses and mixed-state mutation |
| [docs/SCOPE-MODEL.md](docs/SCOPE-MODEL.md)  | isolation rationale; consult owning contracts for behavior |
| [docs/ROADMAP.md](docs/ROADMAP.md)  | § Current work order before selecting work |
| [docs/RELEASE.md](docs/RELEASE.md)  | release preparation and publication |
| [docs/VALIDATION.md](docs/VALIDATION.md)  | commit/release checks; § Smoke Checks for isolation limits |
| [docs/ACCEPTANCE.md](docs/ACCEPTANCE.md)  | release acceptance; unexpected login or expiry behavior before relogin |
| [.claude/skills/upstream-auth-drift/](.claude/skills/upstream-auth-drift/SKILL.md)  | upstream authentication drift, upgrades or wrong-account reports |

## What Belongs In This File

Prefer executable checks for mechanically testable properties. Keep only rules
needed before a task-specific cue here; route other guidance through the map.
Do not add incident histories or duplicate contracts.

## Validation

| Change effect | Required pre-commit checks |
|---|---|
| Implementation, dependencies, build/CI, executable scripts | `mise run check` and `git diff --check` |
| Explanatory prose, status, measurements | `mise run docs-check`, `git diff --check`, and verify changed claims |
| Executable Markdown, parsed tables, rules or procedures | Docs checks plus affected consumers; full gate if impact is unclear |

Mixed changes take the union. Report the chosen gate and its result.
`mise.toml` owns the full gate; [docs/VALIDATION.md](docs/VALIDATION.md) owns release
checks and coverage limits. Read checker headers before treating success as proof.
Use gopls for symbol definitions/references/types and edit diagnostics; it does not
replace the commit gate. Grep is for textual searches.

Never test against the real HOME or XDG roots. Go tests use `t.TempDir()`.
Run documented smoke blocks through `bash scripts/smoke-run.sh '## <heading>'`;
do not execute them by hand or invent isolation exports. The harness uses
`scripts/smoke-env.sh`; its header and VALIDATION's smoke section state the
keychain, leak-detection and subprocess isolation limits.

## Implementation Boundaries

- Before credential IO, read [docs/CREDENTIAL-RULES.md](docs/CREDENTIAL-RULES.md)
  and the affected adapter in [docs/ADAPTERS.md](docs/ADAPTERS.md). Bare AGENTS
  citations in code route to those contracts, not a replacement rule here.
- Before keychain addressing, read ADAPTERS § Keyring item contract and
  § Credential storage resolution; before comparing identity/credentials, read
  [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) § Adapter Interface.
- Use architecture § Package Layout and § Layering for handlers, subprocesses and
  artifact IO. Read `runnertest.Fake` before stubbing a runner: verify argv as well
  as fabricated results.
- `state.json` writes use `App.mutateState`; config edits use `config.Editor`.
  Read architecture § Locking and § Known Traps before mutation.
- Treat worktrees as bound directories. Before changing fragment exclusion, read
  `ensureGitExcluded` and [docs/CLI.md](docs/CLI.md) § kae pin: resolve the common
  Git directory and repository-relative prefix through the runner, verify returned
  paths, and never assume `.git` layout.
- New output paths need redaction tests. Warnings must not change exit codes.
  Read CLI § Output Rules; enforce [docs/SECURITY.md](docs/SECURITY.md)
  § Mutation Safety Rules in review for mixed-state writes.
- JSON contract tokens belong in `internal/constants`. Command/subcommand/shell
  changes update completion in the same commit under CLI § Keeping completion current.
- Adapter behavior changes update ADAPTERS in the same commit. Verification
  metadata changes follow ADAPTERS § Verified Upstream Versions.

## Example Names in Docs and Tests

Never use real account names, profile names, or email addresses in docs,
test fixtures, code comments, or commit messages. Use only generic placeholders
that frame one person's own multiple accounts:

| Context | Allowed names |
|---------|---------------|
| Profile / account names | `main`, `side` |
| Extra accounts (3+ in one test) | neutral names like `alt`, `beta`, `zeta` |
| Example directory | `~/code/side-project` (or `main-app`) |
| Identity email | `you@example.com` |
| Tool examples | the real tool name (`claude`, `codex`, etc.) |

Never use a real login handle.

## Review scope

Perform correctness review, then independent quality review. Confirm each finding's
fix in the changed material and its affected consumers; do not repeat a full review
of unchanged material. After a quality fix, return to correctness review for the
affected scope before accepting the quality result. New behavior or
newly discovered impact widens the scope; a passing test alone does not close a
review finding. Report both verdicts and the disposition of findings.

## Documentation Update Checklist

Derive the owned set with `git ls-files '*.md'` once per scope. Decide changed or
unchanged for every file, including repo-local skills and memory.
Report changed, related-but-unchanged and remaining-unchanged groups with reasons;
name individual files when reasons differ. Reassess affected decisions if scope
changes. Keep this assessment in the work report, not a new tracker.

Before editing, moving or deleting documentation, read
[docs/DOCUMENTATION.md](docs/DOCUMENTATION.md) for reference checks, evidence rules
and content placement. Preserve current contracts, update affected English/Japanese
guidance together, and remove completed plans after recording their outcome.
