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

- Provide `mise` tasks for `test`, `vet`, and `mod-verify`.
- Provide a combined `check` task for the normal pre-commit path.
- Use live-provider smoke tests sparingly and document prerequisites in the
  tool's `VALIDATION.md`.
- When provider inventory semantics change, smoke-test the native provider
  command and the CLI report with a forced refresh or cache-version bump.
- Run `chezmoi apply --dry-run` from the repository root when wrappers,
  templates, settings, or deploy integration changed.
