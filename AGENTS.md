# dotfiles-tool working guide

Follow the repository root `AGENTS.md` plus these tool-local rules.

## Documentation Map

| Document | When To Read |
|----------|--------------|
| [README.md](README.md) | user-facing command or setup changes |
| [docs/DESIGN.md](docs/DESIGN.md) | mission, product boundary, or completion goal changes |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | package layout, runner, provider, cache changes |
| [docs/CLI.md](docs/CLI.md) | command flags, text/JSON/TUI output, exit codes |
| [docs/DATA-MODEL.md](docs/DATA-MODEL.md) | config, report, cache, or status vocabulary changes |
| [docs/SECURITY.md](docs/SECURITY.md) | secrets, subprocesses, external scanner/API, or security evidence changes |
| [docs/ROADMAP.md](docs/ROADMAP.md) | long-term ordering or later target changes |
| [docs/RELEASE.md](docs/RELEASE.md) | current release target, non-goals, release-ready criteria |
| [docs/VALIDATION.md](docs/VALIDATION.md) | before commit and release checks |
| [../../docs/go-cli-architecture.md](../../docs/go-cli-architecture.md) | shared Go CLI standard |

## Validation

```bash
mise -C tools/dotfiles-tool run check
git diff --check
```

Run `chezmoi apply --dry-run` from the repository root when wrappers,
templates, settings, or deploy integration change.

## Implementation Rules

- Keep `main.go` as dispatch only.
- Keep command handlers and report builders in `internal/cmd`.
- Run subprocesses through `internal/runner`.
- Keep JSON reports stable and deterministic.
- Keep TTY views separate from report computation.

## Documentation Update Checklist

For every change, decide whether each local doc needs an update:

- `README.md`
- `AGENTS.md`
- `docs/DESIGN.md`
- `docs/ARCHITECTURE.md`
- `docs/CLI.md`
- `docs/DATA-MODEL.md`
- `docs/SECURITY.md`
- `docs/ROADMAP.md`
- `docs/RELEASE.md`
- `docs/VALIDATION.md`
