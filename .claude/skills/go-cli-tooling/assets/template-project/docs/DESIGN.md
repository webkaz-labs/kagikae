---
version: alpha
name: Tool Terminal Design
description: Visual system for visual-critical TTY and TUI output.
colors:
  canvas: "#0C0D0E"
  primary: "#E6E6E6"
  muted: "#8A8F98"
  focus: "#7AA2F7"
  action: "#2AC3DE"
  success: "#9ECE6A"
  attention: "#E0AF68"
  danger: "#F7768E"
typography:
  terminal:
    fontFamily: monospace
    fontSize: "1rem"
    fontWeight: 400
    lineHeight: 1
spacing:
  inset: "2px"
  section-gap: "1px"
  column-gap: "2px"
components:
  app-shell:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.primary}"
  help-text:
    textColor: "{colors.muted}"
  focused-row:
    textColor: "{colors.focus}"
  available-action:
    textColor: "{colors.action}"
  success-state:
    textColor: "{colors.success}"
  attention-state:
    textColor: "{colors.attention}"
  danger-state:
    textColor: "{colors.danger}"
---

# Tool Terminal Design

Delete this file for a plain CLI. A TTY-aware CLI keeps it only when a stable
prompt or composition is visual-critical. A full TUI replaces the example
values and treats this file as the visual source of truth. Behavior belongs in
`UX.md`; test commands and pinned render inputs belong in `VALIDATION.md`.

## Overview

Name the intended visual character with a concrete reference and a small set
of useful traits, for example: a quiet operations console that is dense,
precise, and calm under failure. Explain what the interface must never become.

The YAML palette is the fixed reference theme used for visual baselines. At
runtime, terminal-native or adaptive colors may vary, but their semantic roles,
contrast hierarchy, and required non-color cues remain stable.

## Colors

| Role | Token | Required non-color cue | Use |
|------|-------|------------------------|-----|
| canvas/text | `{colors.canvas}` / `{colors.primary}` | spatial grouping and labels | base surface and primary content |
| muted | `{colors.muted}` | lower hierarchy, never hidden | metadata/help |
| focus | `{colors.focus}` | leading `>` | current keyboard target only |
| action | `{colors.action}` | explicit key or verb | available action |
| success | `{colors.success}` | `ok` or `updated` | completed/healthy |
| attention | `{colors.attention}` | `review` or `hold` | human decision or policy wait |
| danger | `{colors.danger}` | `error`, `blocked`, or destructive verb | failure/risk |

Do not reuse one accent for focus, action, information, and every status. Color
supplements stable words and markers; it never replaces them.

## Typography

The `monospace` token means the user's configured terminal font and cell
metrics, not a bundled font. Establish hierarchy with weight,
dim, spacing, labels, and concise casing rather than multiple font families or
viewport-scaled type. CJK and icons are measured by terminal cell width.

## Layout

- Spacing token magnitudes map to terminal cells, not literal rendered pixels.
- Horizontal inset: `{spacing.inset}` cells, reducing to one only at minimum
  width.
- Section gap: `{spacing.section-gap}` row between unrelated groups.
- Column gap: `{spacing.column-gap}` cells; collapse a secondary column before
  sacrificing identity, status, or action.
- Keep measured header/help rows stable while body state changes.
- Use one simple border or no border. Avoid nested decorative boxes.
- Truncate by cell width and preserve identity/status/action before description.

## Components

| Component | Visual anatomy | Visual invariants |
|-----------|----------------|-------------------|
| app shell | title/status, body, route-aware help | body dimensions do not jump as hints change |
| section header | label, count, aggregate status | stronger than rows without oversized text |
| grouped list | group label, aligned header, rows | headers align by cell width; groups scan quickly |
| row | focus marker, identity, status, compact action, summary | one focus marker; identity is not clipped first |
| expanded detail | concise new evidence, then actions | do not repeat metadata already visible in the row |
| action list | cursor, verb, short consequence | number keys are shortcuts, not the only interaction |
| confirmation | target, exact effect, confirm/cancel | risky operations visibly default to cancel |
| progress/error | block name, state, concise message | one failed block does not blank usable content |

## Visual Baselines

Keep the manifest small. Every row maps to a semantic terminal snapshot and a
rendered visual comparison.

| Baseline ID | Route/state | Viewport | Fixture | Visual evidence |
|-------------|-------------|----------|---------|-----------------|
| `summary-ready` | summary/ready | `120x36` | `<fixture>` | hierarchy, aggregate state, visible focus |
| `list-ready` | list/ready | `120x36` | `<fixture>` | grouping, aligned columns, compact actions |
| `list-expanded-min` | last row expanded | `80x24` | `<fixture>` | no clipping; details/actions reachable |
| `confirm-risk` | risky confirmation | `120x36` | `<fixture>` | target/effect visible; cancel focused |

Layout-size mismatch fails. Pixel comparison defaults to exact. A nonzero
tolerance or ignored region must be baseline-specific, measured, recorded in
`VALIDATION.md`, and reviewed with the actual and diff images.

## Do's and Don'ts

- **Do** preserve hierarchy and complete operation with `NO_COLOR`.
- **Do** use one semantic style helper per role across every screen.
- **Do** review narrow, canonical, and wide layouts for material changes.
- **Don't** communicate state only through hue or Nerd Font glyphs.
- **Don't** add screen-local colors or spacing outside the token system.
- **Don't** accept an automatic golden rewrite after a failed test.
