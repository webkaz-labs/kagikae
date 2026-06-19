# dotfiles-tool

Short description of the tool and the boundary it owns.

<!-- Standalone repo: keep the Install + Shell Completion sections below and the
release pipeline (.goreleaser.yaml, .github/, scripts/install.sh). Monorepo
tools/<name>: delete those files and the two sections. -->

## Install

```bash
# latest release, checksum-verified (replace OWNER/REPO):
curl -fsSL https://raw.githubusercontent.com/OWNER/REPO/main/scripts/install.sh | sh
# pin / choose a dir: | sh -s -- --version vX.Y.Z --install-dir ~/.local/bin

# managed with mise:
mise use -g github:OWNER/REPO@vX.Y.Z

# from source:
go install github.com/OWNER/REPO@latest
```

Prebuilt archives + `checksums.txt` are on GitHub Releases (with build-provenance
attestation). See [docs/RELEASE.md](docs/RELEASE.md).

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

## Shell Completion

<!-- Only if the tool ships `dotfiles-tool completion` (the __complete pattern). -->

```bash
# source it from your shell rc (fish: dotfiles-tool completion fish | source):
eval "$(dotfiles-tool completion zsh)"
# or install a completion file (zsh prefers an existing fpath dir):
dotfiles-tool completion zsh --install
```

> **zsh: installed but completion does not appear?** zsh caches its completion
> index in a *compdump*; rebuild it, then open a new shell:
>
> ```bash
> rm -f "${ZSH_COMPDUMP:-$HOME/.zcompdump}" && autoload -Uz compinit && compinit
> ```

## Development

```bash
mise run check        # standalone repo (from the repo root)
git diff --check
```

<!-- Monorepo tools/<name>: run from the repo root as
`mise -C tools/<name> run check`. -->

Use the slower audit path before releases or scheduled security reviews:

```bash
mise run audit
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
