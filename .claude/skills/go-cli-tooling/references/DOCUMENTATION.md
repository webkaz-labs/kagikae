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
    PRODUCT.md
    ARCHITECTURE.md
    CLI.md
    DATA-MODEL.md
    SECURITY.md
    ROADMAP.md
    RELEASE.md
    VALIDATION.md
```

Add `docs/UX.md` when behavior changes on a TTY: prompts, progress, focus,
routes, raw-mode input, or terminal lifecycle. Plain CLIs omit it.

Add `docs/DESIGN.md` when the tool has a visual-critical TTY/TUI surface. It is
the design-system source of truth, not the product or software design document.
Plain CLIs normally omit it. TTY-aware CLIs add it only when a stable prompt or
other visual composition is release-critical; full TUIs require it.

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

When a canonical domain grows, keep its uppercase file as the stable entrypoint
and move independently owned details into a lowercase sibling directory:

```text
docs/
  PRODUCT.md
  product/
    SCOPE.md
    JOURNEYS.md
  UX.md
  ux/
    NAVIGATION.md
    PERFORMANCE.md
  ARCHITECTURE.md
  architecture/
    PROVIDERS.md
    CACHE.md
```

The uppercase entrypoint remains an index and owns only the domain summary,
cross-cutting invariants, child-document map, and ownership table. Child files
own complete decision areas; the parent links to them instead of repeating
their long-form content. Split by change reason, reader, and owning code rather
than arbitrary chapter size. Keep the hierarchy to one directory below
`docs/` unless a tool documents a concrete need for deeper nesting.

Treat 300-500 lines as a review signal, not an automatic shard size. Split
earlier when unrelated common questions require scrolling past each other;
first remove implementation history, completed checklists, and duplicate prose.
Do not split `RELEASE.md` into historical ledgers: current release state stays
flat and old release detail belongs in release notes or git history.

`DESIGN.md` is the exception to domain sharding. It follows the
[Google Labs DESIGN.md specification](https://github.com/google-labs-code/design.md)
as one self-contained design system. Supplemental screenshots,
baseline artifacts, or implementation notes may live elsewhere, but normative
tokens, visual rationale, components, and do/don't rules stay in that file.

Do not split when:

- the new file would only hold a few bullets that cannot stand alone;
- the topic is only a transient implementation note;
- the content duplicates another canonical doc without adding a narrower
  decision boundary.

## File Roles

| File | Role |
|------|------|
| `README.md` | human entrypoint, common commands, config path, development commands, doc map; for a standalone repo also an **Install** section (curl\|sh / mise / go install) and, when the tool ships shell completion, a **Shell Completion** section (register + the zsh compdump gotcha — see PATTERNS.md) |
| `AGENTS.md` | tool-specific working rules, validation commands, doc update checklist |
| `CLAUDE.md` | Claude Code dynamic-load entrypoint that imports `AGENTS.md` |
| `PRODUCT.md` | mission, user problem, product boundaries, completion goal, primary journeys, and product-level source-of-truth map |
| `UX.md` | conditional TTY interaction contract: human journey, route/focus/return behavior, loading/error states, and interaction rules; omit for a plain CLI |
| `DESIGN.md` | optional/conditional visual design system: visual identity, semantic tokens, spatial rules, component appearance, visual baseline IDs, and do/don't rules |
| `ARCHITECTURE.md` | package boundaries, provider/backend/runner/cache details, traps |
| `CLI.md` | command surface, flags, exit codes, output-mode selection, text/JSON shape, TTY entrypoints, and localization; stateful TTY behavior belongs in `UX.md` |
| `DATA-MODEL.md` | config schema, desired/live state, cache/report files, status vocabulary |
| `SECURITY.md` | secret handling, subprocess safety, scanner/API policy, security evidence |
| `ROADMAP.md` | long-term ordering and later targets |
| `RELEASE.md` | active release target, non-goals, release-ready criteria |
| `VALIDATION.md` | smoke tests, regression commands, real-machine checks if relevant |

For an interactive tool, `UX.md` defines the screen/route catalog, return and
focus behavior, loading/empty/error/stale states, input precedence, async
readiness, and behavioral acceptance. `DESIGN.md` defines visual identity,
semantic visual roles, responsive composition, component appearance, canonical
visual baseline IDs, and visual acceptance. Keep framework/package details in
`ARCHITECTURE.md` and test commands, pinned render environment, and measured
diff exceptions in `VALIDATION.md`. `CLI.md` still owns how a user selects TTY,
plain, or JSON mode and which command enters a flow; it does not duplicate the
flow's route/focus/state contract.

Validate a design-system file with an explicitly pinned Google DESIGN.md CLI,
passing the repository path directly, for example
`npx --yes --package @google/design.md@0.2.0 designmd lint docs/DESIGN.md`.
The dot-free `designmd` alias also avoids Windows file-association ambiguity.
The format does not require the file to be at repository root; the local
AGENTS/doc map provides discovery in this monorepo.

## Maintenance Rules

- Docs are current-state references, not implementation logs.
- Move completed TODOs out of design docs.
- Keep release target and long-term roadmap separate; apply the release
  retention rule in [RELEASE.md](RELEASE.md#release-document-retention).
- Keep logs, commit hashes, and "implemented in phase X" history out of stable
  docs unless the history itself is needed to avoid a known trap.
- Keep a single canonical home for each policy. Other docs should link to it,
  not restate it.
- Every child document must be linked from its uppercase domain index. Every
  domain index must be linked from README or AGENTS. A docs check should reject
  broken links and orphaned domain children.
- Do not create a second writable table of contents. The domain index is the
  navigation source of truth; generated site navigation may derive from it.
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
