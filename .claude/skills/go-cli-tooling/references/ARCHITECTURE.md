# Go CLI Architecture Standard

This is the normative architecture standard for Go CLIs in this dotfiles
repository. It consolidates patterns proven by `tools/macos-settings` and
`tools/updev`.

## Package Layout

Keep the tool root small:

```text
tools/<tool>/
  main.go
  go.mod
  mise.toml
  README.md
  AGENTS.md
  CLAUDE.md
  docs/
  internal/
    cmd/
    runner/
    textui/
    testutil/
```

Use additional packages only when a stable ownership boundary exists:

| Package | Use When |
|---------|----------|
| `internal/cmd` | command handlers, flag parsing, report builders, text/JSON/TUI output, command-local data |
| `internal/runner` | every subprocess and interactive process test seam |
| `internal/textui` | ANSI/color/table/width/non-TTY-safe rendering helpers |
| `internal/reviewui` | reusable TTY browser/detail behavior, if the tool needs it |
| `internal/testutil/tuitest` | Bubble Tea program/PTY test harness wrappers; test-only imports such as `teatest/v2` |
| `internal/provider` | external provider capability boundaries such as package managers or OS backends |
| `internal/config` | TOML config parse/validate/format helpers |
| `internal/cache` | process-local cache or single-flight cache when repeated reads are expensive |
| `internal/constants` | JSON contract vocabulary: status, mode, source, risk, action tokens |
| `internal/i18n` | pure language helpers and stable translation tables |
| `internal/snapshot` | rollback snapshots for manifest/config mutations |

When an external provider has a machine-readable native command for resolving
active config, use that native interpretation for inventory/report state.
Keep source-file parsers only for cheap hygiene checks, mutation targets,
fallback roots, or file/line evidence that the native command cannot expose.

When persistent cache entries include provider-derived state, bump the cache
schema/version whenever provider resolution semantics or included providers
change. Stale caches must not preserve old drift classifications.

Do not add a broad shared framework before two tools need the same tested API.
Potential future shared packages under `tools/internal/` are `runner`,
`textui`, `reviewui`, `snapshot`, `cache`, `platform`, and `status`.

## Layering

Imports should flow in one direction:

```text
main -> internal/cmd -> provider/backend/config/runner/textui
```

Rules:

- `main.go` only normalizes bare invocation, handles top-level dispatch, and
  returns command exit codes.
- `internal/cmd` owns command parsing, report construction, and output.
- provider/backend packages never import `internal/cmd`.
- TTY views consume reports. They must not be the only place behavior is
  computed.
- Bubble Tea models should be constructed from injected deps so `Update` and
  `View` can be tested without a real terminal, live HOME, keychain, network,
  or provider process.
- OS/package-manager/provider-specific logic belongs in provider/backend
  packages, not in text rendering.

## Command Handlers And Reports

Every command should follow this shape:

```go
func CmdCheck(ctx context.Context, args []string) int {
    opts, ok := parseCheckOptions(args)
    if !ok {
        return exitUsage
    }
    report, err := buildCheckReport(ctx, opts)
    if err != nil {
        return fail(err)
    }
    if opts.Format == formatJSON {
        return encodeJSON(report)
    }
    printCheckReport(report, opts)
    return report.ExitCode()
}
```

Rules:

- Parse flags once into an options struct.
- Build a typed report before printing.
- Pass `context.Context` through command/report/provider paths that can call
  subprocesses, network, filesystem scans, or long-running backends.
- Keep JSON and text output as two views of the same report.
- Keep non-TTY deterministic. Do not emit progress, selectors, colors, or
  loading lines in JSON mode.
- Add `schema_version` to persisted or agent-facing JSON reports.

## Runner Boundary

All subprocess calls go through `internal/runner`.

```go
type Runner interface {
    Run(ctx context.Context, name string, args ...string) (stdout, stderr string, code int)
}

func Run(ctx context.Context, name string, args ...string) (string, string, int) {
    return Default.Run(ctx, name, args...)
}

func With(r Runner, fn func()) {
    saved := Default
    Default = r
    defer func() { Default = saved }()
    fn()
}
```

Rules:

- Production code never calls `exec.Command` directly.
- Production runner implementations use `exec.CommandContext`.
- Tests stub stdout/stderr/exit code through `runner.With`.
- `runner.With` mutates process-global state. Do not use it in `t.Parallel`
  tests; inject a `Runner` dependency directly when parallel tests are needed.
- Interactive editor/process launches still go through runner, using a distinct
  `RunInteractive` helper if needed.
- Runner stubs stay in tests unless they are a public test helper package.

## Config Surface

Normal user policy should live in TOML under `$XDG_CONFIG_HOME/<tool>/`.
Use environment variables only for:

- CI/test one-offs;
- debug toggles;
- endpoints and fixture paths;
- credentials and tokens;
- temporary overrides that must not become user policy.

Config rules:

- Parse and validate config in one package.
- Keep defaults explicit and testable.
- Document precedence in the tool's `DATA-MODEL.md`: defaults, config file,
  environment overrides, then CLI flags.
- Prefer warning on unknown keys while a schema is new; move to errors only
  after the schema and migration path are stable.
- Report config file path and active policy in JSON when policy affects
  mutation or security decisions.
- Never store secrets in TOML.

## JSON Contract

JSON output is a contract for agents and scripts.

Rules:

- Include `schema_version` on stable reports.
- Add fields conservatively; do not rename, remove, or change types without a
  documented breaking change.
- Status/action tokens must be constants, not repeated literals.
- Derived counts should be calculated from source slices, not separately
  mutated fields.
- Arrays that are part of an agent-facing contract should encode as `[]`, not
  `null`; initialize slices at report construction boundaries when empty lists
  are expected.
- Use one `encodeJSON` helper with `SetEscapeHTML(false)` and stable
  indentation.

Example:

```go
func encodeJSON(value any) int {
    encoder := json.NewEncoder(os.Stdout)
    encoder.SetIndent("", "  ")
    encoder.SetEscapeHTML(false)
    if err := encoder.Encode(value); err != nil {
        return fail(err)
    }
    return exitOK
}
```

## TTY And Text UI

Text output must work for both humans and logs.

Rules:

- Use table/width helpers for East Asian width and ANSI-safe truncation.
- Disable color automatically for non-TTY or `NO_COLOR`.
- Keep color semantic, not decorative: ok/warn/error/updated/skipped/held.
- Offer filters before huge lists.
- Provide Back/Home/Exit in multi-step TTY flows.
- Mouse interaction must be opt-in if it interferes with text selection.
- Progress is useful only when a provider can look frozen; never print it in
  JSON output.
- Treat TTY as a routed review surface over typed reports. Keep dashboard,
  list, detail, confirmation, filter, and query screens inside one program for
  a human journey when they share state.
- Model row actions as data: label, kind, target identity, confirmation need,
  and write capability. Detail browsers should not reconstruct action meaning
  by parsing rendered text.
- Keep route state explicit enough to restore the previous screen, selected
  row, filter/query, and item scope after a child action completes or is
  cancelled.
- Separate "routing actions" from "write actions". Routing can open another
  review view; writes need preview/confirmation/validation and should return to
  the originating review context.

## Caching And Performance

Short-lived CLIs often repeat expensive reads inside one process.

Use process cache when:

- the same provider output is needed by multiple report builders;
- a TTY flow rebuilds the same report after user selection;
- expensive reads can fail and negative results should also be cached.

Use bounded timeouts and cached fallback evidence for provider commands that
can hang or be slow. Show clear guidance when evidence is stale or unavailable.

For TTY performance, build the cheap report shell first and prepare slow review
blocks in parallel behind stable loading rows. Prefer message-based refresh of
the existing router over launching a second TTY program after each slow step.

## Error Handling

Separate usage errors, runtime errors, and report findings.

Rules:

- Usage/config/flag errors return `exitUsage` and explain the invalid input on
  stderr.
- Runtime errors return `exitError` and should include enough context to fix
  the failing provider or file.
- Read-only drift, security findings, or review-needed states should be report
  items with a non-zero report exit code, not generic runtime errors.
- JSON reports should include machine-readable error/finding codes where
  agents need to branch on them.
- Human error text belongs on stderr; structured reports belong on stdout.
- An unknown command/tool/profile usage error may append a single nearest-match
  hint through one shared validator (see the did-you-mean pattern in
  [PATTERNS.md](PATTERNS.md)); it is suggestion-only and leaves the exit code
  and JSON unchanged.

## Mutation And Rollback

Mutation commands must be previewable and reversible where feasible.

Rules:

- Snapshot before editing managed manifests.
- Show focused diffs after mutation.
- Validate after writing.
- Provide rollback for file-backed manifests.
- Rollback/restore is itself a mutation: snapshot the current state before
  restoring, so a rollback can be undone too.
- If recording bookkeeping state (a state/ledger file) fails after a
  successful mutation, restore the mutation from its snapshot rather than
  leaving records and reality diverged.
- Keep provider-native install/update mechanics in the provider; the CLI owns
  orchestration and explanation.

## File Split Thresholds

| Signal | Action |
|--------|--------|
| 500+ LOC with mixed presentation and logic | move presentation to `<name>_text.go` |
| 1000+ LOC | split by journey, command, provider, or view |
| multi-screen TTY journey | keep routing in a journey-specific file such as `<name>_router.go` |
| 3rd duplicate parser/formatter/helper | extract a tested helper |
| 5+ localized switch cases | move to map/table based i18n |
| 5+ positional params repeated across functions | introduce a value context struct |

## Validation Baseline

Each Go CLI should provide `mise` tasks:

```toml
[tasks.test]
run = "/usr/bin/time -p go test ./..."

[tasks.test-fresh]
run = "/usr/bin/time -p go test -count=1 ./..."

[tasks.vet]
run = "go vet ./..."

[tasks.mod-verify]
run = "go mod verify"

[tasks.build]
run = "go build ./..."

[tasks.lint]
depends = ["fmt-check", "staticcheck", "golangci-lint", "shellcheck"]

[tasks.check]
depends = ["lint", "test", "vet", "mod-verify", "build"]

[tasks.audit]
depends = ["vuln", "supply-chain", "agent-quality"]
```

`lint` is now part of the shared fast path, but it must stay low-noise:
format/import checks, bug-class static analysis, curated `golangci-lint`, and
ShellCheck for scripts. Keep slower vulnerability, supply-chain, SAST, and
agent-code-quality tools in `audit`. Agent-code-quality checks are evidence by
default, not release blockers, unless the project documents a narrow promoted
finding class.

Run before commit:

```bash
mise -C tools/<tool> run check
git diff --check
```

Run `go mod tidy` before committing changes that add, remove, or update Go
dependencies. Prefer adding a project-local `tidy-check` task only when the
installed Go version supports a non-mutating tidy check.

Also run `chezmoi apply --dry-run` from the repository root when wrappers,
templates, settings, or deploy integration changed.

A standalone tool that integrates with mise (per-project env redirect, an
`install` task, dynamic shell completion via a hidden `__complete` backend)
follows the mise-integration pattern in [PATTERNS.md](PATTERNS.md), which also
defines the global-vs-project registration rule.
