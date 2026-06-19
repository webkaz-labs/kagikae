---
name: go-cli-tooling
description: Use this skill when creating, refactoring, reviewing, or planning a repository-local or standalone public Go CLI that should follow this repository's shared Go CLI standard and template. Trigger for requests mentioning Go CLI architecture, CLI standard, command UX for humans and agents, JSON contracts, runner seams, TTY/TUI or Bubble Tea testing, tool-local docs, macos-settings/updev convergence, exporting the standard to a standalone public tool repository, or creating a new tool from the Go CLI template.
argument-hint: <new-tool|refactor|review|plan> [tool-path]
---

# Go CLI Tooling

Use this skill to build or align repository-local Go CLIs with the shared
standard. This exported copy is self-contained: the standard docs live in
`references/` and the template lives in `assets/template-project/`.

## Quick Workflow

1. Read the shared entrypoint:
   `references/go-cli-architecture.md`.
2. Read only the standard docs needed for the task:
   - architecture/package layout/runner: `references/ARCHITECTURE.md`
   - human and agent command UX: `references/CLI-SPEC.md`
   - tool-local docs and split policy: `references/DOCUMENTATION.md`
   - dependency choices: `references/LIBRARIES.md`
   - security boundaries: `references/SECURITY.md`
   - testing strategy, including stable TTY/TUI and Bubble Tea test layers:
     `references/TESTING.md`
   - release readiness: `references/RELEASE.md`
   - new tool bootstrap: `references/TEMPLATE.md`
   - opt-in patterns (mise integration, did-you-mean hints):
     `references/PATTERNS.md`
3. If working on an existing tool, read its local `AGENTS.md` first, then its
   `README.md`, `docs/RELEASE.md`, and `docs/ROADMAP.md`.
4. Decide whether the request is implementation, planning, review, or new-tool
   bootstrapping before editing.

## New Tool Bootstrap

Start from the canonical template unless the user explicitly wants a different
shape:

```bash
cp -R assets/template-project tools/<tool-name>
```

Then replace:

- module path in `go.mod`;
- command name in `main.go`, `README.md`, `AGENTS.md`, and `CLAUDE.md`;
- placeholder command/report names and schema version;
- Go version pin in `mise.toml` `[tools]`;
- placeholder docs under `docs/`.

Keep `main.go` thin, place command parsing/report builders in `internal/cmd`,
and route subprocesses through `internal/runner`.

For a **standalone public repository** (its own repo instead of
`tools/<name>`), follow "Copy (standalone public repository)" in
`references/TEMPLATE.md`: copy the template to the repo root, copy this
exported bundle into `<repo>/.claude/skills/go-cli-tooling`, rewrite `AGENTS.md` to reference the
bundled standard instead of `../../docs/...`, and add `LICENSE`,
`.gitignore`, and an `install` task.

## Existing Tool Convergence

Audit in this order:

1. `mise.toml` has `test`, `vet`, `mod-verify`, and `check`.
2. `AGENTS.md`, `README.md`, `docs/RELEASE.md`, `docs/ROADMAP.md`, and
   `docs/VALIDATION.md` point to the same validation path.
   `docs/RELEASE.md` follows the retention rule in `references/RELEASE.md`.
3. Tool-local docs include the standard set:
   `DESIGN`, `ARCHITECTURE`, `CLI`, `DATA-MODEL`, `SECURITY`, `ROADMAP`,
   `RELEASE`, and `VALIDATION`.
4. JSON reports have `schema_version`, stable English tokens, deterministic
   ordering, and empty arrays as `[]` for agent-facing slices.
5. Human output is summary-first, color is semantic, and complex item sets use
   grouped list views with filters, expandable evidence, compact status/action
   badges, and item-scoped actions where safe.
6. TTY interaction is an optional layer over report data.
7. If the tool has TTY flows, dashboard/list/detail/filter/query/confirmation
   screens stay in a routed review model where Back/Home, focused row identity,
   item-scoped actions, and slow loading rows behave predictably.
8. Subprocesses go through runner seams with context-aware execution, or the
   exception is documented.
9. Bubble Tea or other TUI behavior is covered by model/update, view, program,
   and built-binary PTY tests according to `references/TESTING.md`.
10. Config precedence and unknown-key behavior are documented.
11. Security-sensitive evidence has `available`, `stale`, `unavailable`, or
   `skipped` semantics where relevant.
12. If the tool offers shell completion or mise integration, or hints on an
   unknown name, it follows the opt-in patterns in `references/PATTERNS.md`
   (single `__complete` backend; global-vs-project registration; hand-rolled
   did-you-mean over the same candidate lists). Skip when the tool needs
   neither.

If a gap should not be fixed immediately, update the tool's `RELEASE.md` or
`ROADMAP.md` with the convergence plan instead of leaving an implicit TODO.

## Implementation Rules

- Preserve the tool's current user-facing behavior unless the request is a UX
  change.
- Do not introduce a shared `tools/internal/` package just because names match;
  extract only after two tools need the same tested API.
- Keep TTY, text, and JSON as views over the same typed report model.
- For inventories, findings, and update results, prefer grouped high-function
  lists over flat dumps: meaningful sections, filters/query, compact badges,
  expandable evidence, and item-scoped safe actions.
- For multi-screen TTYs, prefer one router per human journey. Preserve source
  view, focus, filter/query, and item identity when routing to details or
  confirmations, then returning.
- Make Enter/Space operate the focused primary action or route when obvious;
  numbered/action keys should be shortcuts, not the only usable path.
- Render a useful TTY shell before slow optional blocks finish. Refresh stable
  loading rows through messages instead of leaving and restarting the TTY.
- If a tool has `last` or cached-report review, reopen the same review surface
  without rerunning provider mutations and label stale cached evidence.
- For TUI changes, test `Update` and `View` directly first; use program/PTY
  E2E for terminal integration instead of making sleeps or tmux-only smoke
  tests the primary proof.
- Keep TUI E2E split into fast local smoke and fuller release acceptance; use a
  built binary and fixture data for the fast path.
- Keep normal user policy in TOML; use environment variables for secrets,
  debug, CI, fixtures, and temporary overrides.
- Put compatibility wrappers on a deprecation plan rather than deleting them
  during unrelated work.
- Treat `references/` and `assets/template-project/` as canonical.
  Do not edit bundled references by hand; regenerate the export from the canonical source.

## Validation

For template or tool implementation changes, run the tool-local check task:

```bash
mise -C tools/<tool-name> run check
git diff --check
```

Also run `chezmoi apply --dry-run` from the repository root when wrappers,
templates, settings, or deploy integration changed.

For docs-only planning changes, run at least `git diff --check` and reread the
affected doc map to ensure a future agent can find the right file without
reading every document.
