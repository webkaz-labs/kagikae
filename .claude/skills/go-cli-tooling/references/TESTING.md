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
- TTY tests should focus on model transitions and rendered key states, not
  provider behavior.
- Add width regression tests when text wraps expanded detail, drill-down rows,
  URLs, paths, or other long unbroken tokens. Cover both the fallback width and
  a narrow terminal width path.
- Keep mouse, scroll, and selection-sensitive behavior behind explicit model
  tests when a custom browser is used.

### Bubble Tea TUI Test Layers

For Bubble Tea v2 TUIs, do not rely on a single terminal smoke test. Cover the
behavior in layers:

| Layer | Purpose |
|-------|---------|
| pure unit | validation, side-effect planning, path/config decisions |
| model/update | key messages and custom messages to state transitions and commands |
| view/golden | fixed model to deterministic screen output |
| program E2E | `tea.NewProgram` or a wrapped `teatest/v2` harness with injected messages |
| built binary PTY E2E | actual compiled binary, raw mode, AltScreen, exit behavior, CLI args, and exit code |

Priority is model/update, then view/golden, then program E2E, then built binary
PTY E2E. VHS or other recording tools are visual/demo aids, not the primary
correctness test.

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

Program E2E verifies that the real Bubble Tea program starts, renders the
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
TERM=xterm-256color
NO_COLOR=1
LC_ALL=C
TZ=UTC
```

Use PTY E2E for full-screen TUI, AltScreen, raw mode, key input, Ctrl-C,
terminal size, and clean exit behavior. PTY helpers should expose
`StartBinaryPTY`, `SetSize`, `SendKeys`, `WaitScreen`, `CaptureScreen`,
`SnapshotOnFailure`, and `Close` or equivalent. Blind sleeps are not allowed;
wait for a screen predicate with a bounded timeout and include the captured
screen/log on failure.

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
