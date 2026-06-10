# Go CLI Specification Standard

This document defines the minimum command-line behavior expected from Go CLIs
in this repository.

## Command Shape

Every tool should support:

| Command | Requirement |
|---------|-------------|
| bare invocation | human summary hub or primary daily workflow |
| `help` / `--help` / `-h` | deterministic help text |
| `version` / `--version` / `-v` | print the current implemented tool release |
| `check` or `status` | read-only health/drift report |
| `--format json` | stable JSON for agent/script use where the command returns structured data |
| `--no-color` | disable color in human text output |
| `--config <path>` | where a normal user config file exists |
| `--dry-run` | for commands that would mutate or call provider mutation paths |
| `--verbose` | opt-in diagnostic detail where useful |
| `--quiet` | suppress non-essential human text where useful |

Use subcommands that match the user's mental model, not implementation layers.
Advanced subcommands may exist, but daily README examples should stay short.

`version`, `--version`, and `-v` should not call slow providers or require OS
support. Human output is `<tool> vMAJOR.MINOR.PATCH`. If JSON is supported,
include `schema_version`, `tool`, `version`, `major`, `minor`, `patch`, and a
stable contract label such as `pre_stable` for `v0.x.y` or `stable` for
`v1.x.y`. Reserve `-v` for version; use `--verbose` only when a tool explicitly
adopts global verbosity.

Short subcommand aliases are allowed only when they are obvious, documented,
and tested. Prefer read-only aliases such as `st` for `status`, `ck` for
`check`, and `ls` for `list`. Mutation aliases such as `rm` should exist only
when they preserve the same confirmation and dry-run gates as the full command.

## UX Principles

Each CLI serves both humans and agents. Design the command surface so both
audiences can use the same report model without forcing one audience through
the other's output.

### Human UX

- Optimize bare invocation for the user's most common daily workflow.
- Put the decision summary first: ok/warn/error, changed/skipped/deferred
  counts, and the next useful human action.
- Show detailed lists only after the summary, or behind a selector/browser when
  the output is long enough to scroll away important context.
- For complex inventories, findings, or update results, use a grouped,
  high-function list instead of one flat table: stable groups such as provider,
  kind, category, status, or action state; compact badges for changed/held/
  actionable rows; filters and query; expandable evidence; and item-scoped
  actions where safe.
- Wrap expanded detail, drill-down text, and long unbroken values by the active
  terminal width when a TTY width is available; keep a conservative fallback
  width for non-TTY output and tests.
- Prefer semantic color, compact grouping, filters, and drill-down detail over
  printing every raw line by default.
- Keep provider logs visible only when they explain progress or failure; do
  not duplicate raw logs as separate result rows.
- Preserve ordinary terminal behavior: scrolling, copy/select, and keyboard
  navigation must keep working.
- Localize human labels when locale detection makes that useful, but keep
  provider/tool/package identifiers unchanged.

### Agent UX

- Provide deterministic `--format json` for structured commands before relying
  on TTY-only interaction.
- Keep JSON stable, English-tokenized, non-localized, and free of ANSI color,
  progress, prompts, and decorative text.
- Include enough machine-readable fields for agents to act without parsing
  display strings: status, decision, provider, name, version, reason,
  remediation, source/evidence, and config policy where relevant.
- Make long-running or optional checks explicit in the report with stale or
  unavailable evidence states instead of freezing silently.
- Prefer exit codes and structured findings over prose-only failure messages.
- Keep human remediation commands out of default human summaries, but include
  structured remediation in JSON when useful.

## Exit Codes

Use a small, documented set:

| Code | Meaning |
|------|---------|
| `0` | success; no blocking drift/finding |
| `1` | command/runtime error |
| `2` | read-only report found drift, findings, or review-needed state |
| `64` | usage/config/flag error |

If a tool needs more codes, define them in its `docs/CLI.md` and add tests.

## Error Output

- Use stdout for normal human reports and JSON reports.
- Use stderr for usage errors, runtime errors, and diagnostics that are not
  part of the structured report.
- Keep error messages actionable but avoid requiring agents to parse prose.
- JSON report errors/findings should include stable codes when branching is
  expected.

## Output Modes

### Human Text

- Summary first.
- Then details, grouped by provider/section/status.
- Prefer compact tables with semantic color.
- Use grouped list browsers when users need to compare, filter, expand, and act
  on many items. Avoid duplicating the same long flat list behind multiple menu
  entries unless each route applies a meaningful scope or filter.
- Hide machine-oriented next commands unless the user explicitly asks for
  command guidance or the output is non-TTY remediation.
- For long reports, provide a selector/browser in TTY and compact summaries in
  non-TTY.

### JSON

- No color, progress, prompts, or localized display-only decoration.
- Include `schema_version` for stable reports.
- Include machine-readable `status`, `decision`, `provider`, `name`, `reason`,
  and `remediation` fields where relevant.
- Keep key names snake_case.
- Keep ordering deterministic for arrays that represent inventory, checks,
  findings, or diffs.
- Return empty arrays as `[]`, not `null`, for fields agents are expected to
  iterate over.

### TTY

- TTY is an interaction layer over report data.
- TTY-only state must not be required to compute JSON output.
- Provide keyboard controls, `/` filter when lists are large, and Back/Exit in
  multi-step flows.
- Do not enable mouse click actions by default when they break scroll or text
  selection.
- Keep dashboard/list/detail flows in one routed TTY program when users need to
  move between related review screens. Leaving and restarting the TTY between
  views feels like a freeze, loses context, and often breaks Back semantics.
- Actionable rows should expose focused-row action hints before expansion and
  preserve item identity when routing to a detail view. Returning from a
  filtered or item-scoped action should restore the source view, focus, and
  filter instead of sending users to a generic list.
- Enter or Space should run the focused row's primary safe action or route when
  one is obvious. Numbered/action keys are useful shortcuts, but they should
  not be the only way to operate an expanded row.
- Expanded details should add evidence and next-action context, not repeat the
  same provider/name/version/status fields already visible in the collapsed
  table.

## Localization

- Detect Japanese from locale where useful for human text.
- Keep JSON tokens English and stable.
- Keep Japanese labels in i18n helpers/tables, not scattered across command
  logic.
- Avoid translating provider/package/tool identifiers.

## Progress And Slow Providers

Commands that call slow providers should:

- show short progress in TTY human mode;
- use bounded timeouts for known slow probes;
- cache stale-but-useful evidence when safe;
- print "evidence unavailable" as a report item instead of freezing;
- keep `--security off` or equivalent explicit fast paths when checks are
  optional.
- render the first useful TTY shell as soon as possible, then update stable
  loading/progress rows as provider, inventory, security, translation, or
  review-preparation blocks finish;
- reserve layout space for focus/action hint lines and loading rows so the body
  does not jump when asynchronous evidence arrives.

## Observability

- Write a last report when it helps users review a mutation or long report
  after the command exits.
- If a `last` or cached-report command exists, it should reopen the same
  review surface without rerunning provider mutations. Section flags may choose
  an initial view, but cached evidence must show source and age when trust or
  actionability depends on freshness.
- Include cache path, report path, evidence age, and unavailable reasons when
  they affect trust in the result.
- Action, hold, and warning badges should be derived from the current report or
  latest saved report, not from older raw provider caches unless the output
  labels that cache as stale.
- Keep `--verbose` for diagnostics and provider details that would clutter the
  normal human path.
- Keep `--quiet` from suppressing errors or JSON fields.

## Mutation UX

Mutation commands must:

- support dry-run or preview;
- explain planned changes before writing;
- snapshot file-backed state when feasible;
- validate after writing;
- print changed/skipped/deferred summaries;
- write a last report when useful for `last` / review commands.

## Configuration UX

- Use TOML for normal user policy.
- Use env vars for temporary overrides, secrets, fixtures, and CI/debug.
- Expose config path in `--format json` when config changes behavior.
- Prefer explicit opt-in for broad/slow integrations such as optional external
  scanners or non-primary providers.

## Compatibility

- Keep compatibility wrappers thin and clearly documented.
- Deprecated commands, flags, config keys, and JSON fields should warn before
  removal unless the behavior is unsafe.
- Large command trees may use generated help/completion, but small local CLIs
  should keep help text simple and deterministic.
