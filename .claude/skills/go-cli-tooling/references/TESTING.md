# Go CLI Testing Standard

Tests should protect the report model, command behavior, and provider seams
without requiring live package managers or network access for ordinary unit
tests.

## Unit Tests

- Use table tests for parsers, classifiers, config defaults, policy decisions,
  and output builders.
- Test report builders before text/TTY rendering.
- Use fake runners for subprocess behavior. Do not call live providers from
  unit tests.
- Do not use process-global runner replacement in `t.Parallel` tests.
- Keep fixtures small and purpose-named.
- When a third package needs the same canned-response fake for a seam (such
  as the runner), extract one shared double into
  `internal/testutil/<seam>test` instead of copying it again.

## Command-Level Parsing Coverage

Tests that call a command's inner `run*` function bypass argument parsing
entirely, so flag-splitting regressions ship invisibly: a value flag whose
value is misparsed as a positional passes every inner-function test and
fails only on a real command line. Exercise each command — including every
value-taking flag — through the outer `Cmd*` entry at least once, either in
a unit test or in the built-binary smoke checks of `VALIDATION.md`.

## JSON Regression

- Add regression tests for stable JSON reports when agents depend on them.
- Assert `schema_version`, stable token values, deterministic ordering, and
  empty arrays as `[]`.
- Avoid golden files for tiny JSON payloads; compare decoded structs or compact
  expected JSON in the test.
- Use golden files only when output is large enough that inline expectations
  make tests harder to read.

## Text And TTY

- Test text output through pure formatting helpers where possible.
- Verify non-TTY output separately from TTY browser behavior.
- Model tests should cover transitions and rendered key states. Built-binary
  PTY tests should cover a few critical user journeys, not live provider logic.
- Add width regression tests when text wraps expanded detail, drill-down rows,
  URLs, paths, or other long unbroken tokens. Cover both the fallback width and
  a narrow terminal width path.
- Keep mouse, scroll, and selection-sensitive behavior behind explicit model
  tests when a custom browser is used.

### Interactive CLI/TUI Test Contracts

Interactive tests protect three different contracts. Keep them separate:

- logic: input/message to state transition and effect plan;
- interaction: CLI arguments, built binary, PTY input, resize, exit, and
  terminal restoration;
- visual: rendered hierarchy, layout, color, and truncation.

For Bubble Tea v2 and other TUIs, use these layers without duplicating the same
scenario at every layer:

| Layer | Requirement | Purpose |
|-------|-------------|---------|
| pure unit | MUST | validation, side-effect planning, path/config decisions |
| model/update | MUST | key/custom messages to state transitions and commands |
| view/golden | SHOULD selectively | complex deterministic rendering only |
| program E2E | MAY | framework integration not proven more cheaply elsewhere |
| built binary PTY E2E | MUST for interactive flows | actual user boundary: raw mode, AltScreen, keys, resize, arguments, exit, and restore |

The normal shape is a broad, fast model suite plus a small set of critical
built-binary PTY journeys. Program E2E is not a mandatory bridge between them.
Non-interactive commands do not need PTY coverage. Recording tools are failure
evidence and demo aids, not the primary correctness test.

Before adding or changing TUI tests, inspect `go.mod`, the Bubble Tea import
path, related Charm major versions, existing test structure, and the CI command.
Do not mix Bubble Tea v1 and v2 imports in the same tool. Bubble Tea v2 projects
use:

```go
tea "charm.land/bubbletea/v2"
```

If a Bubble Tea v2 project uses teatest, use
`github.com/charmbracelet/x/exp/teatest/v2` only through a small
`internal/testutil/tuitest` wrapper. The `charmbracelet/x` packages are
experimental, so pin the dependency in `go.mod`, keep direct imports out of
production and broad tests, and contain API churn inside the wrapper.

Model tests should call `Update` directly and cover initial state, `j/k` and
arrow navigation, Enter/submit, Back/Esc, `q`/Ctrl-C, empty data, loading,
validation errors, success messages, error messages, and async command result
messages. Do not wait on real time for async behavior; inject the resulting
message directly or fake the dependency that creates it.

View and golden tests must fix width, height, data, selected index, time,
paths, color mode, loading/progress state, and spinner frame. Golden updates
must be explicit, for example behind `-update`; never update golden files
automatically on failure. Normalize volatile terminal output before comparing:
CRLF to LF, cursor show/hide, terminal title sequences, irrelevant trailing
whitespace, absolute HOME/temp paths, timestamps, random IDs, and spinner
frames.

Optional program E2E verifies that the real Bubble Tea program starts, renders the
expected screen, responds to key messages, reaches the expected final model, and
quits cleanly. It should not validate CLI flag parsing, OS signal behavior, raw
PTY mode, config path resolution, or process exit codes; those belong to built
binary PTY tests.

When building a custom program harness, wrap `tea.NewProgram` with fixed input,
output, window size, environment, context, and disabled signals. Keep key input
helpers such as `KeyPress`, `EnterKey`, and `CtrlCKey` in test utilities so the
project's exact Bubble Tea v2 message fields are compiled in one place.

Built binary E2E should build the executable once for the test and run that
binary instead of `go run`. Use isolated env and temp dirs:

```text
HOME=<temp>
XDG_CONFIG_HOME=<temp>/.config
XDG_DATA_HOME=<temp>/.local/share
XDG_CACHE_HOME=<temp>/.cache
XDG_STATE_HOME=<temp>/.local/state
XDG_RUNTIME_DIR=<temp>/.local/run
TMPDIR=<temp>/tmp
TERM=xterm-256color
NO_COLOR=1
LC_ALL=C
TZ=UTC
```

That environment is the semantic PTY default. Visual regression is a separate
lane: remove `NO_COLOR`, force the intended color capability, and keep theme and
font inputs fixed so color and hierarchy are actually exercised.

**Setting `HOME` is not enough, and the gap is silent.** A path resolver that
reads each XDG root independently will honour an *absolute value already in the
environment* over the temp `HOME`, so a harness that exports `HOME` and only some
of the roots still resolves the rest under the operator's real home. Redirect
every root the tool reads — including `XDG_STATE_HOME`, which is where a
surprising number of third-party tools keep trust and tracking state — and
`TMPDIR`, or `mktemp` inside the harness lands outside the sandbox.

**Also clear the tool-home variables the CLI itself honours** (`FOO_HOME`,
`FOO_CONFIG_DIR`, and any credential-store override): they *outrank* the temp
`HOME`, so a harness that inherits one from the operator's shell writes to the
real location while reporting a clean run. Unset them explicitly rather than
assuming the temp `HOME` covers them.

Keep the environment prefix in **one** script that the harness applies *before*
the procedure runs, rather than as instructions the procedure is expected to
follow. Sourcing a preamble is a step a harness can perform and still get wrong —
`. ./preamble.sh` inside `$(...)` exports nothing, because command substitution
is a subshell, and the run then looks isolated while writing to the real `$HOME`.
Isolating by construction closes that class; a warning in prose does not.

Use PTY E2E for full-screen TUI, AltScreen, raw mode, key/mouse input, Ctrl-C,
resize, clean exit, and terminal restoration. The standard automation engine is
[Microsoft shell-use](https://github.com/microsoft/shell-use). Pin a tested
release and call it through one thin project wrapper that owns session names,
environment isolation, timeouts, artifact paths, and cleanup. Do not build a
second PTY or terminal-emulation layer in project tests. New coverage should not
adopt the predecessor `@microsoft/tui-test` interface unless a documented
migration blocker requires it.

An existing tmux or custom PTY harness may remain while a bounded migration is
tracked, but do not expand it with new infrastructure. Preserve its proven
critical journeys when moving them to `shell-use`, then remove the duplicate
harness.

Assert semantics before snapshots: wait for meaningful text or state, perform
the interaction, assert the resulting text/state and exit code, then capture a
snapshot only for a stable critical screen. Prefer `shell-use wait text`,
`expect text`, `expect exit-code`, `resize`, and `wait exit`; `wait idle` is a
secondary settling check. Blind sleeps are not allowed except as a bounded last
resort with a documented reason.

Use `120x36` as the canonical snapshot size. Interactive flows must also cover
`80x24`; complex dashboards should cover `160x48`. Resize tests must assert that
focused content, actions, and navigation remain usable rather than only checking
that the process stayed alive.

Split PTY coverage into a fast default path and a fuller release path. The fast
path should prove startup, one representative route, Back/Home, and clean exit
against a built binary with fixtures. The release path can cover more routes,
write confirmations, and real-terminal acceptance. Do not make every local
check wait for the full route suite.

For routed TTYs, test that row actions preserve item identity and return to the
originating view with focus/filter state intact. Include regression coverage for
"open action, cancel or go Back, then press Down/Enter" so stale action state
cannot trigger an unintended route.

For slow or asynchronous TTY preparation, test loading-state rendering and the
result message transition directly. The assertion should be "stable shell first,
same router refreshes later", not elapsed time.

Keep TUI side effects behind dependencies. `Update` should return commands or
call injected services; it should not write user config, call live providers,
or touch keychains directly. TUI tests use fake deps and temp HOME/XDG roots.

### Terminal Snapshots And Visual Regression

Terminal snapshots and visual regression have different jobs:

- terminal snapshots protect stable cells/text and selected color attributes;
- visual regression protects spacing, alignment, clipping, hierarchy, and
  whole-screen composition.

Every critical stable state in the release PTY path must have a terminal
snapshot; do not snapshot transient or low-value screens. Update goldens only
via an explicit reviewed command; never rewrite them automatically after
failure. Keep view, terminal, and visual goldens in separate directories.

For visual-critical screens, capture SVG from `shell-use`, render a canonical
PNG with a pinned [resvg](https://github.com/linebender/resvg), and compare it
with a pinned [ODiff](https://github.com/dmtrKovalenko/odiff). Fix terminal size,
theme, color mode, locale, time zone, and font inputs. A material UI change also
requires inspection of the rendered image by a human or vision-capable reviewer;
visual review supplements semantic assertions and never replaces them.

The tool-local `docs/UX.md` is the source of truth for route, focus, state, and
interaction acceptance. When visual-critical output exists, `docs/DESIGN.md`
owns semantic visual tokens, component appearance, canonical baseline IDs, and
visual acceptance. Every visual-critical baseline listed there must map to a
terminal snapshot and visual test. `VALIDATION.md` owns the exact commands,
pinned environment, and any
explicit per-baseline ODiff exception; tests must not invent undocumented visual
tolerances.

On PTY or visual failure, retain enough evidence to reproduce the state: plain
screen text, state/exit metadata, the actual SVG/PNG, the baseline and diff when
applicable, and the `shell-use` recording or verbose log when lifecycle diagnosis
is needed. Do not retain every artifact on success.

An interactive flow is complete when model transitions are covered, the built
distribution-equivalent binary passes a critical PTY journey at `80x24` and
`120x36`, Back/exit and terminal restoration are verified, snapshots are
reviewed explicitly, and visual-critical changes pass visual comparison and
render inspection.

## Integration And Smoke

- Provide `mise` tasks for `test`, `vet`, `mod-verify`, `lint`, and
  `check`.
- Keep `check` as the normal fast pre-commit path. It should run the
  low-noise local gates: formatting/import checks, bug-class static analysis,
  curated lint, unit tests, `go vet`, module verification, and build.
- Provide a slower `audit` task for release or scheduled checks when the tool
  is public or security-sensitive.
- Use live-provider smoke tests sparingly and document prerequisites in the
  tool's `VALIDATION.md`.
- **A verification procedure written as prose asserts nothing.** If `VALIDATION.md`
  carries copy-paste blocks, make the assertions *be* the commands — `test`,
  `grep -q`, an exit-code check — and run the blocks from a script that extracts
  them by heading and reports a per-line verdict. A block whose checks are
  `#   assert:` comments depends on a human comparing output to a comment, which
  is how one tool accumulated five wrong assertions in a single block, none of
  them found until the block was finally executed verbatim. Of four ways of
  auditing that file — identifier index, duplication scan, claim reconciliation,
  and running the blocks — running the blocks is the only one that has ever found
  a defect able to break a release. That is not a ranking of the four: the
  duplication scan there drove real consolidations, and claim reconciliation was
  never implemented, so it has never had a chance to find anything.
- **Have the extractor refuse a block whose checks are only comments, before it runs
  anything.** A block that cannot check itself is not a run that failed, it is a
  document to fix — so exit with the caller-error status a bad argument gets, not a
  test-failure status, or one legitimately unconverted block turns the gate red and the
  next author deletes the refusal. Asking for real assertions in prose is a convention,
  and a convention does not survive unrelated edits; a refusal in the extractor lasts
  only as well as whatever tests the refusal, so write that test too. Match the marker
  on stated properties rather than a remembered count, because a marker is a *label* and
  the label is the part an author invents: the comment starts at column 0; the label is
  the first thing after the `#` (relaxing that to `^#.*` was measured *refusing a
  legitimate block*, because a comment can narrate about an assertion without being
  one); whitespace is allowed on both sides of the label, and the side before the colon
  matters as much as the side after it; matching is case-insensitive; and the label
  comes from an explicit vocabulary (`assert|expect|verify|check|confirm|ensure`) rather
  than `[A-Za-z]+:`, which was measured flagging a legitimate narration comment.
  Refusing only at column 0 has a ceiling
  worth stating rather than leaving the class to look closed: an indented comment-only
  assertion following no command is a lie this does not catch.
- **A negative assertion needs a positive control beside it.** `grep -c X` prints
  `0` for a missing file, a broken decoder, or a pattern that could never match,
  so an absence check passes for the wrong reason. Pair it with a positive line
  over the same artifact. Wrap zero-asserting counts in `test "$(...)" -eq 0`: a
  bare `grep -c` exits 1 on the correct answer, inverting the verdict.
- **If you build such a runner, verify that the loop finished — not the exit
  status.** A block that ends itself mid-way leaves an empty failure log and a
  status the report will happily call green. This was got wrong in four
  positions in a row: a non-zero `exit`, `exit 0` (idiomatic at the end of a
  fixture), an `exit` on the *last* line, and a trailing backslash continuation
  that leaves a command pending and unrun. One invariant covers all four — the
  loop reached its end with nothing pending — where four special cases did not.
  Note also that a line-by-line runner cannot execute here-documents or
  multi-line function bodies; keep such constructs on one line, or the procedure
  stops partway while reporting success.
- When provider inventory semantics change, smoke-test the native provider
  command and the CLI report with a forced refresh or cache-version bump.
- Run `chezmoi apply --dry-run` from the repository root when wrappers,
  templates, settings, or deploy integration changed.

## Quality Tooling Baseline

`updev` has proven the first quality-tooling wave enough to promote it into
the shared Go CLI standard. Adopt the tooling in tiers so agent iteration stays
fast and release checks stay deeper.

Fast local `check` baseline:

- `gofumpt` plus `goimports` or `gci` for formatting and deterministic import
  grouping.
- Bug-class Staticcheck analyzers, starting with `SA*`.
- `golangci-lint v2` with a curated low-noise config. Good default linters are
  `govet`, `staticcheck`, `ineffassign`, `misspell`, `unconvert`,
  `whitespace`, and `nolintlint`.
- `shellcheck` for repository shell scripts.
- `go test ./...`, `go vet ./...`, `go mod verify`, and `go build ./...`.

Release or scheduled `audit` baseline:

- `govulncheck` for reachable Go vulnerability checks.
- Public-repo supply-chain checks where applicable: CodeQL, Dependabot for Go
  modules and GitHub Actions, dependency review, GitHub Actions SHA/version
  posture, and release artifact checksum or provenance verification.
- Agent-code-quality audit evidence, such as deterministic AI-slop detectors,
  only as non-blocking audit evidence until a finding class repeatedly catches
  real defects without duplicating tests or lint.

Do not make broad `gosec`, broad `errcheck`, broad `unused`, broad complexity
findings, or generic AI-slop findings release-blocking by default. Treat those
as backlog or placement signals unless a project documents a narrow promoted
finding class with low false positives.
