# Go CLI Documentation Standard

Each Go CLI should have a small local documentation set. Keep implementation
history in git log, not in docs.

## Required Files

```text
tools/<tool>/
  README.md
  AGENTS.md
  CLAUDE.md
  docs/
    DESIGN.md
    ARCHITECTURE.md
    CLI.md
    DATA-MODEL.md
    SECURITY.md
    ROADMAP.md
    RELEASE.md
    VALIDATION.md
```

Add other domain-specific docs only when needed, for example
`EXTERNAL-MANAGEMENT.md`.

## Split Policy

Keep tool docs small enough that an agent can choose the right file from the
README/AGENTS doc map before reading details.

Split a document when:

- it mixes product goals, architecture, CLI behavior, data model, and release
  state in one place;
- it accumulates implementation history instead of current behavior;
- it exceeds roughly 300-500 lines and readers must scroll past unrelated
  sections to answer a common question;
- a topic has different maintenance cadence, such as stable architecture versus
  active release scope;
- a topic has a specialized audience, such as security policy, external app
  management, or provider internals.

Do not split when:

- the new file would only hold a few bullets that cannot stand alone;
- the topic is only a transient implementation note;
- the content duplicates another canonical doc without adding a narrower
  decision boundary.

## File Roles

| File | Role |
|------|------|
| `README.md` | human entrypoint, common commands, config path, development commands, doc map |
| `AGENTS.md` | tool-specific working rules, validation commands, doc update checklist |
| `CLAUDE.md` | Claude Code dynamic-load entrypoint that imports `AGENTS.md` |
| `DESIGN.md` | mission, product boundaries, completion goal, current state |
| `ARCHITECTURE.md` | package boundaries, provider/backend/runner/cache details, traps |
| `CLI.md` | command surface, flags, exit codes, JSON/text/TUI contract, localization |
| `DATA-MODEL.md` | config schema, desired/live state, cache/report files, status vocabulary |
| `SECURITY.md` | secret handling, subprocess safety, scanner/API policy, security evidence |
| `ROADMAP.md` | long-term ordering and later targets |
| `RELEASE.md` | active release target, non-goals, release-ready criteria |
| `VALIDATION.md` | smoke tests, regression commands, real-machine checks if relevant |

## Maintenance Rules

- Docs are current-state references, not implementation logs.
- Move completed TODOs out of design docs.
- Keep release target and long-term roadmap separate; apply the release
  retention rule in [RELEASE.md](RELEASE.md#release-document-retention).
- Keep logs, commit hashes, and "implemented in phase X" history out of stable
  docs unless the history itself is needed to avoid a known trap.
- Keep a single canonical home for each policy. Other docs should link to it,
  not restate it.
- Every user-facing command shown in README should be valid.
- Every JSON report described in docs should have a schema/version statement or
  be explicitly unstable.
- When behavior changes, update the narrowest relevant doc first.
- After major edits, reread the doc map and file roles as a routing test: a
  new agent should know which file to open without reading every doc.
- In final reports, state which docs changed and which did not need changes.

## AGENTS Checklist

Each tool `AGENTS.md` should include:

- local doc map with "when to read" guidance;
- validation commands;
- implementation boundaries;
- data/config/localization rules;
- documentation update checklist.

Avoid copying the full root policy. Tool AGENTS files should only add local
rules and links.

Each tool `CLAUDE.md` should stay thin and import `AGENTS.md` with
`@AGENTS.md`; do not duplicate rules there.
