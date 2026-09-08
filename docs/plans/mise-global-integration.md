# Global mise integration — v0.20.1

## Scope and state

Agreed release scope: move opt-in global completion into the same
`conf.d/kagikae.toml` used by global isolated selection. Implement and release a
patch because existing commands and selection semantics remain. Implementation,
fixture checks and affected live acceptance are complete; publication and
published-asset verification remain. The ordering is owned by
[medium-term.md](medium-term.md).

## Contract

- Completion and isolated environment settings coexist. Updating either preserves
  the other. Shared teardown removes isolated settings, not completion.
- Auto and account lifecycle checks compare the isolated portion against synced
  state, allowing a recognized completion registration. Invalid or foreign owned
  content is refused rather than overwritten.
- Existing exact current/legacy completion blocks in global config migrate on
  completion refresh/install. Preserve other config bytes, permissions and links;
  customized blocks require manual handling. Unrelated global hooks may coexist.
- Migration is restartable after interruption and restores the source on ordinary
  write failure where possible. Never report successful migration while a legacy
  registration remains duplicated. No credential payload is part of migration.
- Completion and isolated writers share the existing state lock. Resolve their
  global mise directory consistently, including MISE_CONFIG_DIR. Keep project-local
  mise init and bound-directory fragments separate from this global ownership.
- Global tasks, automatic credential repair, credential attribution changes and
  whole-command-system refactoring are outside this release.

## Acceptance and release

Use temporary HOME/XDG fixtures for completion before/after isolated selection,
shared teardown, auto no-op, lifecycle checks, repeated registration, legacy/current
migration, foreign content, symlinks, custom config roots, contention and write
failure/interruption recovery. Verify actual mise loading and shell completion in
an isolated smoke. Preserve existing redaction and credential tests.

Run the full commit gate, correctness review, then quality review; user prohibition
on subagents means both reviews run in the main session. Evaluate every tracked
Markdown target and persistent memory. Follow [RELEASE.md](../RELEASE.md) and
record affected live acceptance and published-artifact evidence in
[ACCEPTANCE.md](../ACCEPTANCE.md). Install the released local binary after successful
publication and verification, as part of this delivery.
