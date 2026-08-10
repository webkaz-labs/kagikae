# Validation

Run the standard local validation suite before committing:

```bash
mise -C tools/dotfiles-tool run check
git diff --check
```

The `check` task runs:

- formatting/import checks with `gofumpt` and `goimports`
- bug-class Staticcheck and curated `golangci-lint`
- `shellcheck` for `scripts/*.sh` when scripts exist
- `go test ./...`
- `go vet ./...`
- `go mod verify`
- `go build ./...`

Run slower release or scheduled audit checks separately:

```bash
mise -C tools/dotfiles-tool run audit
```

The `audit` task runs vulnerability checks and project-specific supply-chain or
agent-code-quality evidence. Keep those checks out of the ordinary edit loop
unless this project promotes a narrow finding class to release-blocking.
Before a public release, replace the template `supply-chain` and
`agent-quality` placeholder tasks with project-specific checks or document why
they are intentionally unavailable.

When the release workflow or `.goreleaser.yaml` changes, validate the release
configuration and local artifact shape before tagging:

```bash
mise -C tools/dotfiles-tool run goreleaser-check
mise -C tools/dotfiles-tool run goreleaser-snapshot
```

Run `go mod tidy` before committing dependency changes.

Run smoke checks for user-facing commands:

```bash
go run . --no-color
go run . --format json
go run . check --format json
```

When text output wraps detail, URLs, paths, or other long unbroken tokens, add
unit tests for both the fallback width and a narrow terminal width.

If the tool has a TTY, keep two validation tiers:

- fast local smoke: built binary, fixture data, one representative route,
  Back/Home, and clean exit;
- release acceptance: fuller routed navigation, write confirmations, and
  real-terminal readability.

Avoid blind sleeps in PTY tests. Wait for a screen predicate and capture the
screen/log on failure.

Use a project-pinned `shell-use` wrapper for built-binary PTY journeys. Keep
semantic checks color-neutral, and run visual-critical screens in a separate,
fixed color/theme/font lane as defined by the shared `TESTING.md`.

For a visual TUI, define tasks equivalent to:

```bash
mise run tui-pty       # semantic journeys against the built binary
mise run tui-visual    # shell-use SVG -> resvg PNG -> ODiff
mise run tui-update    # explicit local-only baseline update for review
```

Record the pinned binary/fixture, `shell-use`, `resvg`, ODiff, terminal size,
theme, color capability, locale, time zone, and font here. Map every canonical
baseline ID in `DESIGN.md` to a test. Layout differences and unlisted baseline
changes fail; nonzero ODiff tolerance or ignored regions require a measured,
per-baseline reason.

When `DESIGN.md` is present, pin and run the Google DESIGN.md linter against
its explicit path:

```bash
npx --yes --package @google/design.md@0.2.0 designmd lint docs/DESIGN.md
```

Plain CLIs without `DESIGN.md` omit this check.

Run `chezmoi apply --dry-run` from the repository root when wrappers,
templates, settings, or deploy integration change.
