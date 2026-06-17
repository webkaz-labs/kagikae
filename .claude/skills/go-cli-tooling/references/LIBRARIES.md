# Recommended Go Libraries For CLI Tools

Prefer the standard library until a dependency clearly improves UX or reduces
risk. When adding a dependency, document why the standard library is not enough.

## Baseline

| Need | Recommendation |
|------|----------------|
| flags | standard `flag` for simple command-local flags |
| JSON | standard `encoding/json` through a shared `encodeJSON` helper |
| errors | standard `errors` / `fmt.Errorf`, plus small command-local helpers |
| subprocess | standard `os/exec` hidden behind `internal/runner` |
| files/paths | standard `os`, `io/fs`, `path/filepath` |
| TOML | choose one parser per tool; keep parse/validate helpers in `internal/config` |
| XML/plist where feasible | standard `encoding/xml` before adding heavy plist dependencies |
| tests | standard `testing`, table tests, fake runner |
| string similarity / suggestions | hand-rolled Levenshtein; no fuzzy-matching dependency (see [PATTERNS.md](PATTERNS.md)) |

## Approved UI Stack

The two current Go CLIs use the Charm stack for TTY interaction:

| Library | Use |
|---------|-----|
| `charm.land/huh/v2` | forms, selects, confirmation flows |
| `charm.land/bubbletea/v2` | custom TTY browsers or live-updating views |
| `charm.land/bubbles/v2` | reusable Bubble Tea widgets |
| `charm.land/lipgloss/v2` | styling when a richer TTY view needs it |
| `github.com/mattn/go-runewidth` | East Asian display width and truncation |
| `github.com/charmbracelet/x/exp/teatest/v2` | Bubble Tea v2 program tests, only through a thin test helper wrapper |

Rules:

- Use `huh` for ordinary forms before building custom Bubble Tea models.
- Use Bubble Tea only when a plain form cannot express the workflow, such as
  scrollable detail browsers or live progress.
- Keep text/non-TTY output available without Charm dependencies in the report
  builder path.
- Keep mouse actions opt-in if they interfere with text selection.
- Treat `charmbracelet/x` packages as experimental. Pin versions and import
  `teatest/v2` only from an `internal/testutil/tuitest` wrapper so API changes
  stay localized.
- Do not mix Bubble Tea v1 and v2 import paths in the same tool.

## Useful Optional Libraries

| Need | Candidate | Notes |
|------|-----------|-------|
| larger command trees | `github.com/spf13/cobra` / `cobra-cli` | useful when a tool needs many subcommands, shell completion, or generated command docs; overkill for small local tools |
| single-flight cache | `golang.org/x/sync/singleflight` | useful when parallel report builders can issue the same expensive read |
| terminal detection | `golang.org/x/term` or Charm term helpers | use through one textui helper |
| humanized sizes/time | `github.com/dustin/go-humanize` | acceptable for display only; JSON should keep raw values |
| release distribution | GoReleaser | useful for tools distributed outside this dotfiles repository |

## Avoid By Default

- large CLI frameworks for small command trees;
- adopting external template projects wholesale without first matching this
  repository's docs, runner, config, validation, and deploy conventions;
- global mutable config packages;
- untyped map-heavy config handling outside the config package;
- direct OS command wrappers that bypass `internal/runner`;
- dependencies used only to save a few lines of straightforward standard
  library code.
