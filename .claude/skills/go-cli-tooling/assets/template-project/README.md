# dotfiles-tool

Short description of the tool and the boundary it owns.

## Common Commands

```bash
dotfiles-tool
dotfiles-tool -h
dotfiles-tool check
dotfiles-tool ck
dotfiles-tool check --format json
dotfiles-tool version
dotfiles-tool -v
dotfiles-tool version --format json
```

## Configuration

If the tool has normal user policy, keep it in:

```text
${XDG_CONFIG_HOME:-~/.config}/dotfiles-tool/config.toml
```

Secrets and one-off CI/debug overrides stay in environment variables.

## Development

```bash
mise -C tools/dotfiles-tool run check
git diff --check
```

Use the slower audit path before releases or scheduled security reviews:

```bash
mise -C tools/dotfiles-tool run audit
```

## Documentation

| Document | Purpose |
|----------|---------|
| [docs/DESIGN.md](docs/DESIGN.md) | Mission, boundaries, completion goal. |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Package layout and implementation boundaries. |
| [docs/CLI.md](docs/CLI.md) | Command and output contract. |
| [docs/DATA-MODEL.md](docs/DATA-MODEL.md) | Config, state, reports, status vocabulary. |
| [docs/SECURITY.md](docs/SECURITY.md) | Secret handling, subprocess safety, scanner/API evidence. |
| [docs/ROADMAP.md](docs/ROADMAP.md) | Long-term ordering. |
| [docs/RELEASE.md](docs/RELEASE.md) | Active release target. |
| [docs/VALIDATION.md](docs/VALIDATION.md) | Smoke and regression checklist. |
