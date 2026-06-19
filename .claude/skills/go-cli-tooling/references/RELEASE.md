# Go CLI Release Standard

Each tool should keep long-term direction and active release scope separate.
Use the tool-local `ROADMAP.md` for ordering beyond the current release and
`RELEASE.md` for the current target.

## Version Numbering

Use SemVer for command/product releases and write release labels as
`<tool> vMAJOR.MINOR.PATCH`, for example `macset v0.1.0` or `updev v0.4.0`.
The leading `v` is part of release labels, release tags, and release-plan
headings. Do not write bare `0.4.0` for a tool release unless the context is a
machine field that explicitly omits the prefix.

`v0.x.y` means the command contract is still pre-1.0 and can change with clear
release notes. `v1.x.y` means the daily user and agent-facing contract is
stable enough that breaking command, config, cache, or JSON changes require
documented deprecation or an explicit breaking-change target.

The SemVer parts carry these meanings for repository CLIs:

- `MAJOR`: command/config/cache/JSON compatibility generation. Increment for
  breaking user or agent contracts. `0` means the tool is still pre-stable.
- `MINOR`: new commands, reports, providers, policy surfaces, or UX flows that
  preserve existing contracts.
- `PATCH`: bug fixes, doc corrections, performance work, compatibility fixes,
  and small UX polish that do not add a new contract surface.

For `v0.x.y`, minor and patch releases may still reshape contracts, but the
release plan must say so clearly. For `v1.x.y`, breaking changes require a
deprecation path or an explicitly named breaking release target such as
`v2.0.0`.

Keep these version concepts separate:

- tool release version: `updev v0.4.0`, `macset v0.2.0`;
- Go language/toolchain version: `Go 1.25`;
- JSON/report schema version: integer `schema_version`, not SemVer;
- provider package version: a managed dependency version such as `mise 2026.5.18`.

Each CLI should expose its current implemented tool release through
`<tool> version`, `<tool> --version`, and `<tool> -v`. The reported version is
the current implemented contract, not the next target in `RELEASE.md`.

## Release-Ready Checklist

- Command help and README examples match implemented commands.
- Human text, JSON, and TTY behavior match `CLI.md`.
- Stable JSON reports have `schema_version` and deterministic token values.
- Config defaults, precedence, and unknown-key behavior are documented.
- Security-relevant policy and evidence states are visible in JSON.
- Validation commands in `AGENTS.md` and `VALIDATION.md` pass.
- Deprecated commands or flags have documented warnings and replacement paths.
- Docs describe current behavior, not implementation history.

## Release Document Retention

`RELEASE.md` should contain the active release target and, at most, the
immediately previous completed release as a short baseline. When a new target
becomes active:

- move durable behavior into `CLI.md`, `DATA-MODEL.md`, `SECURITY.md`,
  `ARCHITECTURE.md`, `README.md`, or another stable domain doc;
- move future work and ordering into `ROADMAP.md`;
- remove older release-plan details from `RELEASE.md`.

Implementation history belongs in git log, not in release docs.

## Compatibility And Deprecation

- Keep compatibility wrappers thin and documented.
- Warn before removing a command, flag, config key, cache file, or report field
  that users or agents may call directly.
- Prefer one release with a warning before removal unless the old behavior is
  unsafe.
- JSON fields may be added conservatively, but renaming, removing, or changing
  field types requires a documented breaking change.

## Help And Generated Docs

- Small CLIs can keep help text hand-written.
- Consider Cobra or another framework only when many subcommands, completion,
  or generated command docs are worth the dependency.
- If help/docs are generated, document the generation command and validate that
  README examples stay in sync.

## Release Automation (standalone repositories)

A standalone public repo (a tool in its own repository, not a `tools/<name>`
entry in the dotfiles monorepo) should ship installable binaries, not only
`go install`. The recommended pipeline is [GoReleaser](https://goreleaser.com)
driven by a tag — it replaces hand-rolled cross-compile/archive/checksum bash
with a declarative config. The template carries ready-to-adapt files
(`.goreleaser.yaml`, `.github/workflows/{check,ci,release}.yml`,
`scripts/install.sh`); see [TEMPLATE.md](TEMPLATE.md). Monorepo `tools/<name>`
copies delete those — the monorepo does not release per-tool.

Shape of the pipeline:

- **Tag-triggered, not manual.** Pushing `vX.Y.Z` runs the release workflow;
  do not `gh release create` by hand. The workflow gates on the shared check,
  then GoReleaser builds, archives, checksums, creates the GitHub release with a
  grouped changelog, and the workflow attests build provenance.
- **One gate, two callers.** Put the check (vet, gofmt, test, mod verify) in a
  reusable `check.yml` (`on: workflow_call`) that both `ci.yml` (push/PR) and
  `release.yml` call. Tag pushes do not trigger `ci.yml`, so the release must
  run the same gate itself — sharing it by `uses:` stops the release gate from
  drifting from CI.
- **Version is a source constant, so guard it against the tag.** The reported
  version is the hardcoded `toolVersion` (not ldflags-injected; `go install`
  users get no ldflags), so a tag/constant mismatch would publish a binary whose
  `version` lies. Add a release step asserting `toolVersion == $GITHUB_REF_NAME`
  before GoReleaser; bumping `toolVersion` to the tag is release step 1.
- **Platform set matches what builds.** Build only the GOOS/GOARCH the tool
  actually compiles for (e.g. exclude `windows` when locking/syscalls are
  Unix-only) — verify with `goreleaser release --snapshot` before tagging.
- **Archive/checksum naming is a contract.** GoReleaser's `.Version` excludes
  the leading `v`; `.Tag` preserves it. New tools should normally use
  `<tool>_{{ .Version }}_{{ .Os }}_{{ .Arch }}` and have `scripts/install.sh`
  strip the leading `v` from release tags. Existing public tools that already
  publish `v`-prefixed assets should use `.Tag` to preserve the public asset
  contract instead of forcing users to change install URLs. In either case,
  keep `.goreleaser.yaml`, `scripts/install.sh`, README install examples, and
  the mise github backend naming assumption in sync.
- **Checksums are the attestation subject.** Let GoReleaser produce
  `checksums.txt` and use `actions/attest@v4` with
  `subject-checksums: ./dist/checksums.txt`. This attests the complete artifact
  set through the checksum manifest and avoids hand-maintaining per-archive
  `subject-path` globs. Scope `contents`/`id-token`/`attestations: write` to
  the release job only.

`scripts/install.sh` is a `curl … | sh` installer that resolves the latest (or
a pinned) release, **verifies the checksum before trusting the binary**, and
installs to `~/.local/bin`. Document three install routes in the README:
`curl | sh`, `mise use -g github:OWNER/REPO@vX.Y.Z`, and `go install`.

Pre-tag validation (local), then the real publish runs in CI on the tag:

```bash
goreleaser check
goreleaser release --snapshot --clean --skip=publish   # archives match install.sh
sh -n scripts/install.sh && shellcheck scripts/install.sh
actionlint .github/workflows/*.yml
```

Working example: the `kae` repo (`webkaz-labs/kagikae`).
