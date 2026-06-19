# Go CLI Template Project

The template in [template-project/](template-project/) is a minimal starting
point for a new Go CLI for a Go CLI.

## Copy (repository-local tool)

```bash
cp -R assets/template-project tools/<tool-name>
```

Then replace:

- module path in `go.mod`;
- command name in `main.go`, `README.md`, `AGENTS.md`, and `CLAUDE.md`;
- tool release version, report names, and schema version;
- Go version pin in `mise.toml` `[tools]` (match the repo's current Go);
- docs placeholders under `docs/`.

Then **delete** the release-pipeline scaffolding — `.goreleaser.yaml`,
`.github/`, and `scripts/install.sh` — which is for standalone repos only; a
monorepo does not release `tools/<name>` per-tool.

## Copy (standalone public repository)

When the tool lives in its own repository (for example under an org like
`webkaz-labs`), the template's dotfiles-repo assumptions must be replaced,
not just renamed:

1. Copy the template to the repository root (not `tools/<name>`); set the
   module path to `github.com/<org>/<repo>`.
2. Bundle the standard so the repo is self-contained: copy this exported bundle
   into `<repo>/.claude/skills/go-cli-tooling` and point the tool `AGENTS.md` at
   the bundled `SKILL.md`/`references/` instead of
   `../../docs/go-cli-architecture.md` (that relative link only works inside
   this dotfiles repo).
3. Rewrite `AGENTS.md`: drop "follow the repository root AGENTS.md", change
   validation to `mise run check` (no `-C tools/...`), and remove the
   `chezmoi apply --dry-run` step — it does not exist outside this repo.
4. Add what the template omits because the dotfiles repo provides it:
   `LICENSE`, `.gitignore`, and a `mise.toml` `install` task
   (`go build -o ${HOME}/.local/bin/<cmd> .`) for real-machine use.
5. Public repos default to English docs and comments even when the owner's
   global agent rules say otherwise; confirm the language with the owner once.
6. If the tool wants mise integration (per-project env redirect, dynamic shell
   completion via a hidden `__complete` backend) or did-you-mean hints, adopt
   the opt-in patterns in [PATTERNS.md](PATTERNS.md); they are not part of the
   minimal template.
7. Set up the release pipeline (the template's `.goreleaser.yaml`,
   `.github/workflows/{check,ci,release}.yml`, and `scripts/install.sh`):
   replace `dotfiles-tool` / `OWNER/REPO` / the `DOTFILES_TOOL_*` env names,
   trim the GOOS/GOARCH set to what the tool actually builds, and document the
   curl/mise/go-install routes in the README. See
   [RELEASE.md](RELEASE.md#release-automation-standalone-repositories); validate
   with `goreleaser check` + a snapshot release before the first tag.

## First Implementation Steps

1. Keep bare invocation and `check` read-only until the data model is stable.
2. Add TOML config only when a real user policy exists.
3. Add mutation only after preview, validation, snapshot, and rollback
   boundaries are documented.
4. Add TTY interaction only after JSON/non-TTY reports exist.
5. If TTY grows beyond one screen, design it as a routed review layer over the
   same reports. Preserve Back/Home, focused row identity, and item-scoped
   actions from the beginning.
6. Add provider/backends only behind runner/provider seams.

## Required Validation Before First Commit

```bash
mise -C tools/<tool-name> run check   # monorepo; standalone repo: mise run check
git diff --check
```

Run slower release or scheduled audit checks separately:

```bash
mise -C tools/<tool-name> run audit   # standalone repo: mise run audit
```

Run `go mod tidy` before committing dependency changes. Add a non-mutating
`tidy-check` task only when the local Go version supports it.

Run `chezmoi apply --dry-run` only when the new tool adds wrappers,
templates, config, or deploy integration.

If the tool has a TTY, keep a fast local smoke separate from a fuller release
acceptance pass. The fast path should use a built binary and fixtures; the
release path can cover more routes and real-terminal readability.
