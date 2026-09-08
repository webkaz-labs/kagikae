# Release Process

## Current release — kae v0.20.1

The release record is the tag and
[GitHub release](https://github.com/webkaz-labs/kagikae/releases/tag/v0.20.1).
Read its patch-version rationale with `git show v0.20.1:docs/RELEASE.md`.
[CLI.md](CLI.md) owns completion/isolation coexistence and migration from global
config.
[ACCEPTANCE.md](ACCEPTANCE.md) owns the affected live and distribution results.

Published archives, SHA-256 checksums, GitHub provenance attestations and the
installer were verified on 2026-09-08 (JST) with
`mise run release-verify -- v0.20.1`. The macOS arm64 archive and isolated
installer reported `kae v0.20.1`; the installer used verified-asset fixtures
as described below.

## Next release — recovery guidance

Scope agreed; implementation has not started. Help users choose an existing
explicit recovery operation through human-readable output and documentation.
Prioritize metadata-list guidance, then align authentication guidance. Choose the
version after the implementation scope is verified.

| Order / status | Work | Acceptance |
|---|---|---|
| First / planned | Explain the next check for invalid config, enumeration failure, unreadable or invalid metadata, and unexpected entries in backup/preservation lists | Preserve readable rows, anonymity and completeness. Give an actionable check or documentation route without inferring a damaged entry's identity, recommending blind deletion, or presenting metadata readability as proof of restorability. |
| Second / planned | Align global login/capture and bound-directory login advice | Distinguish fresh login from capture of an already verified login. State the target and prerequisites; when the account or destination cannot be established, guide verification before mutation. Cover stale/expiring snapshots, missing snapshots or payloads, and identity-related recapture advice. Preserve tool-specific login support and existing refusals. |
| After both / planned | Update README and CLI guidance and validate delivery | Explain when global rollback and original-store preservation apply, without implying either repairs expired authentication. Command-level controls cover both lists and affected authentication messages, including wrong/unknown account, unavailable login support and secret-bearing input. Complete the applicable validation and review gates below. |

Use the existing `listDiagnostics` module and compare the existing freshness,
identity and bound-directory guidance helpers before introducing another one.
Concentrate repeated guidance decisions only where this removes knowledge from
callers; do not add a generic recovery framework or redesign account lifecycle.
Tests exercise the command interface and retain refusal and redaction controls.

Keep JSON structure, diagnostic codes and exit codes stable. Add no structured
recovery fields. Existing human-readable strings carried inside JSON may receive
the same wording correction as text output; they must not become machine action
instructions. Keep list stdout/stderr responsibilities and quiet behavior intact.
Lists remain metadata-only and independent of credential backend selection.

Do not add commands, automatic repair, credential reads for richer list advice,
or changes to capture, attribution, refresh, restoration, retention, quota or
locking policy. The research prerequisites in [ROADMAP.md](ROADMAP.md) remain.
A discovered defect requiring such a change is a separate scope decision.

Implementation uses [AGENTS.md](../AGENTS.md) § Validation and the applicable
isolated command controls. Assess affected live acceptance under
[ACCEPTANCE.md](ACCEPTANCE.md); wording-only changes do not by themselves establish
a need to repeat credential mutations on a real machine. Publication follows
§ Release procedure when implementation and release are requested.

On completion, remove this next-release section after current contracts and
acceptance evidence have their canonical homes. Keep historical scope in git.

## Release procedure

Releases are cut by pushing a `vX.Y.Z` tag; GitHub Actions
([.github/workflows/release.yml](../.github/workflows/release.yml)) runs
[GoReleaser](https://goreleaser.com) ([.goreleaser.yaml](../.goreleaser.yaml))
to build, archive, checksum, and publish. Do **not** create the GitHub release
by hand — the tag does it.

1. Bump `toolVersion` in `internal/cmd/cmd.go` to the new `vX.Y.Z` (the binary's
   reported version is hardcoded, not injected; it must match the tag) and the
   `TestBuildVersionReport` expectation.
2. Follow [VALIDATION.md](VALIDATION.md) § Standard Suite: run its commit gate and
   **every slower release-time check it names**, including `release-evidence`, before
   the tag. Update the docs (ROADMAP/VALIDATION and any behavior docs). Work the
   release checks in
   [ACCEPTANCE.md](ACCEPTANCE.md) before the tag — nothing in it runs from
   `mise run check`, and that file is where a result is recorded. Follow its
   § Optional account-combination checks for the release classification of checks
   that require additional account combinations.
3. Merge to `main` and push; CI (`ci.yml`) must be green.
4. Tag and push: `git tag -a vX.Y.Z -m "kae vX.Y.Z — <summary>"` then
   `git push origin vX.Y.Z`. The release workflow gates on the same `check.yml`
   CI runs — a **subset** of `mise run check`, and that workflow's own steps are
   the copy of it to read — then GoReleaser builds darwin/linux × amd64/arm64
   (`kae_<version>_<os>_<arch>.tar.gz` + `checksums.txt`), creates the release
   with a grouped changelog, and attests the archives named by the release
   checksum manifest.
5. Run `mise run release-verify -- vX.Y.Z` from the repository root, alone while
   no other task edits the checkout. It downloads through `gh`, verifies the
   manifest, archive contents and provenance attestations, then checks the native
   version and installer in isolated environments. The installer receives only
   the verified assets through a non-forwarding curl fixture; its HTTP transport
   is not tested by this command. JSON `status` is `success`, `failed`, or
   `unavailable`. Success exits zero; the other statuses exit nonzero through
   `go run`, so use the JSON status to distinguish them. A missing prerequisite
   is not a passing check.

The verifier owns its command process groups and installer temporary parent.
Waiting for a shell to exit does not by itself stop its descendants; the lifecycle
controls in `scripts/releaseverify/lifecycle_test.go` cover that boundary. Detached
process groups and cleanup after SIGKILL of the verifier itself are outside the
cleanup guarantee. Do not broaden cleanup to unrelated processes or directories.

GoReleaser auto-generates the changelog from commits; edit the release body
afterward for curated highlights when useful. Windows is not built
([ROADMAP.md](ROADMAP.md): `internal/lock` is Unix-only).

**This file carries the current release pointer, next target and procedure, not the history.** What shipped
is the tag, the GitHub release it created, and `git log`. A per-release entry lived here for
every version through v0.17.0, and the file was cumulative, so
`git show v0.17.0:docs/RELEASE.md` is the whole set. Read an entry as of its own
tag: its forward pointers were not maintained forward, so an item it defers to
[ROADMAP.md](ROADMAP.md) may have shipped since — that file and `git log` are
where to check.
