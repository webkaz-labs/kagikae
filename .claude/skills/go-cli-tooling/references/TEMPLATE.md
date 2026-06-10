# Go CLI Template Project

The template in [template-project/](template-project/) is a minimal starting
point for a new Go CLI for a Go CLI.

## Copy

```bash
cp -R assets/template-project tools/<tool-name>
```

Then replace:

- module path in `go.mod`;
- command name in `main.go`, `README.md`, `AGENTS.md`, and `CLAUDE.md`;
- tool release version, report names, and schema version;
- docs placeholders under `docs/`.

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
mise -C tools/<tool-name> run check
git diff --check
```

Run `go mod tidy` before committing dependency changes. Add a non-mutating
`tidy-check` task only when the local Go version supports it.

Run `chezmoi apply --dry-run` only when the new tool adds wrappers,
templates, config, or deploy integration.

If the tool has a TTY, keep a fast local smoke separate from a fuller release
acceptance pass. The fast path should use a built binary and fixtures; the
release path can cover more routes and real-terminal readability.
