# Interactive UX

This document owns TTY-dependent behavior. A plain CLI keeps only its
non-interactive behavior in `CLI.md` and may delete this file. A TTY-aware CLI
keeps only the relevant prompt/progress rules. A full TUI completes every
section below.

## Applicability

| Product shape | Required UX content | Testing boundary |
|---------------|---------------------|------------------|
| plain CLI | none; text/JSON/error behavior stays in `CLI.md` | unit, command parsing, built-binary non-TTY smoke |
| TTY-aware CLI | relevant prompt, progress, width, cancel, and fallback rules | PTY only for behavior that changes on a TTY |
| full TUI | complete route, focus, state, responsive, and lifecycle contract | model tests plus critical built-binary PTY journeys |

## Information Architecture

List each distinct route once. Multiple entry points are useful only when they
arrive with a meaningful scope or selected item.

| Route | Job | Default focus / primary action | Return contract |
|-------|-----|--------------------------------|-----------------|
| summary | orient and choose the next review area | first actionable status group | exit |
| list | scan, filter, and select items | prior item, otherwise first actionable row | previous summary and selection |
| detail | understand one item | primary item-scoped action | same list/filter/item/scroll |
| confirm | preview a mutation | cancel by default when risk is meaningful | same detail; refresh after success |

Document exceptional transitions explicitly. Avoid a route that only opens an
identical unscoped list.

## Screen States

Every data-bearing route defines these states rather than rendering blank:

| State | Must remain visible | Available action |
|-------|---------------------|------------------|
| loading | stable shell, route title, progress by block | Back/quit |
| empty | active filters/scope and why no rows matched | clear filter/Back |
| ready | content, focus, compact action hint | primary action/navigation |
| attention | affected item and reason | review or remediation |
| error | failed block, retained usable data, concise cause | retry/Back |
| stale | cached age/source | refresh or continue read-only |

## Layout And Responsive Behavior

| Viewport | Contract |
|----------|----------|
| `80x24` | minimum supported; one primary column, secondary evidence collapses |
| `120x36` | canonical review size |
| `160x48` | optional wide layout for useful context, not decoration |

- Reserve stable rows for title/status and focused action/help.
- Define the collapse order for secondary columns and panels. Never truncate
  the primary identity or hide the only action.
- Opening the last visible item scrolls its detail/actions into view.
- Width calculations use terminal cell width, including CJK and icons.

## Input, Focus, And Accessibility

| Intent | Example keys | Rule |
|--------|--------------|------|
| move | arrows, `j`/`k`, PageUp/PageDown | never trigger an action |
| primary | Enter/Space | operate the focused row/action |
| back/home | Esc/Backspace, documented Home key | restore route state |
| filter/help/quit | `/`, `?`, `q` | do not shadow active input or modal keys |

- Input precedence is modal, active input, focused action, focused row, then
  global keys.
- Mouse support is optional and must not make text selection or keyboard use
  worse.
- Color is never the only status signal. Define an ASCII fallback for icons.

## Async And Lifecycle

- Render the useful shell first and update stable blocks in place.
- Define `ready`, `busy`, and `idle`; tests wait on state, not arbitrary time.
- Correlate async results with the route/item so stale responses cannot steal
  focus.
- Do not hide useful provider progress behind a blank alternate screen.
- Exit and signals restore the terminal and preserve documented return values.

## Behavioral Acceptance

- Primary journeys complete without an unnecessary intermediate selector.
- Back/cancel/error restores route, focus, filter, identity, and scroll.
- Loading, empty, ready, attention, error, and stale states are understandable.
- `80x24` has no overlap or inaccessible actions.
- Keyboard-only and `NO_COLOR` use remain complete.

## Domain Index

If this file grows beyond one coherent interaction contract, retain it as the
index and split complete topics under `docs/ux/`, for example
`NAVIGATION.md`, `PERFORMANCE.md`, and `REVIEW-FLOWS.md`. Link every child here
and keep each behavioral fact in one owner.
