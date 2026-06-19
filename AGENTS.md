# kagikae working guide

Standalone public repository. Follow the bundled Go CLI standard in
[.claude/skills/go-cli-tooling/](.claude/skills/go-cli-tooling/SKILL.md)
(references under `references/`), plus these local rules.

## Documentation Map

| Document | When To Read |
|----------|--------------|
| [README.md](README.md) | user-facing command or setup changes |
| [docs/DESIGN.md](docs/DESIGN.md) | mission, modes, terminology, boundary changes |
| [docs/ADAPTERS.md](docs/ADAPTERS.md) | anything that touches what a tool adapter switches or preserves |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | package layout, adapter interface, transaction, lock changes |
| [docs/CLI.md](docs/CLI.md) | command flags, output, exit codes, JSON contract changes |
| [docs/DATA-MODEL.md](docs/DATA-MODEL.md) | config, snapshot, state, backup, secret-ref changes |
| [docs/SECURITY.md](docs/SECURITY.md) | secrets, subprocess, permission, redaction changes |
| [docs/ROADMAP.md](docs/ROADMAP.md) | long-term ordering changes |
| [docs/RELEASE.md](docs/RELEASE.md) | active release target changes |
| [docs/VALIDATION.md](docs/VALIDATION.md) | before commit and release checks |

## Validation

```bash
mise run check
git diff --check
```

`mise run check` is the authoritative gate; it must pass before every commit.
It runs `lint` (gofumpt + goimports format check, `staticcheck -checks=SA*`,
curated `golangci-lint`, `shellcheck`), `go test ./...`, `go vet`,
`go mod verify`, and `go build ./...`. `mise run audit` (govulncheck) and
`mise run goreleaser-check` are slower release-time checks. Lint tools run via
`go run <tool>@<pinned version>`; the first run downloads them.

While editing (this is a Go module — the LSP is `gopls`):

- **Symbol work goes through the LSP, not Grep** — resolve definitions,
  references, and types with the LSP (go-to-definition / find-references /
  hover). Grep is for text/string matches, not for "where is this symbol used".
- **Read LSP diagnostics after each edit** — clear errors and warnings as you
  go. The LSP is the fast inner loop; it does not replace `mise run check`,
  which stays the pre-commit gate (a clean LSP does not imply green tests).

Never run tests or smoke checks against the real `$HOME`; every test uses
`t.TempDir()` HOME/XDG roots, and smoke checks export a temp HOME
([docs/VALIDATION.md](docs/VALIDATION.md)).

## Implementation Boundaries

- Keep `main.go` as dispatch only; handlers and report builders in
  `internal/cmd`.
- All subprocesses (`security`, `secret-tool`, binary detection) go through
  `internal/runner`.
- Adapters declare artifact specs; capture/apply/backup/rollback IO lives in
  `internal/artifact` and generic layers. Do not duplicate IO in adapters.
- The per-tool switched/preserved allowlists in `docs/ADAPTERS.md` are the
  normative contract: code must match that document, and any change requires
  updating it in the same commit.
- Secret values must never reach stdout/stderr/JSON/metadata/logs. New output
  paths need a redaction test.
- Mixed-state files are patched by JSON Pointer only; whole-file replacement
  of `~/.claude.json` is forbidden in code review, not just in docs.
- `config.toml` edits go through the comment-preserving `config.Editor` via
  `App.editConfig` (under the config lock). A decode-then-encode round-trip
  (BurntSushi `config.Load` → re-`Marshal`) is forbidden: it silently drops
  every user comment.
- JSON contract tokens live in `internal/constants`; never inline literals.

## Example Names in Docs and Tests

Never use real account names, profile names, or email addresses in docs,
test fixtures, code comments, or commit messages. Use only generic placeholders
that frame one person's own multiple accounts:

| Context | Allowed names |
|---------|---------------|
| Profile / account names | `main`, `side` |
| Extra accounts (3+ in one test) | neutral names like `alt`, `beta`, `zeta` |
| Example directory | `~/code/side-project` (or `main-app`) |
| Identity email | `you@example.com` |
| Tool examples | the real tool name (`claude`, `codex`, etc.) |

Never use a real login handle.

## Documentation Update Checklist

For every change, decide and report "changed / no change needed" for each:
`README.md`, `AGENTS.md`, and every file under `docs/`.
