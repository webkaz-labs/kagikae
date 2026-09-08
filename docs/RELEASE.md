# Release Process

## Current release — kae v0.20.2

Recovery guidance clarifies the account and store prerequisites for existing
login/capture operations and the next checks for incomplete metadata lists.
This is a patch release: command syntax, JSON structure, diagnostic codes, exit
codes and credential mutation policy remain unchanged. The affected validation
is recorded in [ACCEPTANCE.md](ACCEPTANCE.md) § Recovery guidance validation boundary.
The release record is the tag and
[GitHub release](https://github.com/webkaz-labs/kagikae/releases/tag/v0.20.2).
On 2026-09-09 (JST), `mise run release-verify -- v0.20.2` verified the archives,
checksums, provenance and isolated installer; the native binary reported
`kae v0.20.2`. The installer used verified assets rather than live HTTP transport.

## Next release — credential recovery and offline validation

Scope agreed; implementation has not started. Consider all candidates below within
the constraint of no additional real-machine acceptance. Use isolated synthetic
credentials, existing reviewed upstream evidence and CI. This admits investigation
and evidence-backed changes, not unconditional capability enablement or a waiver
of [ACCEPTANCE.md](ACCEPTANCE.md). Choose the version from the accepted changes.

| Priority / status | Candidate | Deliverable and admission gate |
|---|---|---|
| Primary / planned | Preserve unknown-format credentials and provide explicit recovery | Reproduce unreadable, unparseable and undated cases separately. Compare existing preservation and relogin mechanisms. Admit a behavior change only after preservation, attribution, destination and recovery prerequisites are settled and the applicable acceptance can be satisfied without another live run. Otherwise retain the reproduction and a concrete deferred decision. |
| Primary / planned | Failure and retry safety | Exercise capacity exhaustion, preservation failure, interrupted login, competing changes and retry through the affected command interface. Require preservation before destructive writes and verify original bytes survive failure; tests use synthetic data and existing isolation harnesses. |
| Primary / planned | State-specific diagnostics | Distinguish observations users can act on without declaring unreadable or unfamiliar credentials invalid. Preserve redaction and avoid automatic deletion or unverified recovery promises. Agree any new JSON contract tokens before implementation. |
| Conditional / planned | Numeric zero versus unknown deadline | Characterize missing, nonnumeric, zero and negative values and their consumers. Preserve the distinction only where it informs an accepted decision. Treat revocation and deletion policy as a separate decision requiring tool-specific evidence. |
| Conditional / planned | Upstream behaviour-site detection | Establish reproducible old/new artifacts and compare detection controls and cost against existing fingerprints. Build only the bounded check justified by that comparison; record missing inputs rather than invent a generic hash framework. No upstream login or live credential access. |
| Conditional / planned | Moved directories and path aliases | Use temporary directory/symlink fixtures to characterize lost readers, duplicate pin IDs and migration failure modes. Compare a recoverable migration with diagnostic-only handling. No PinID rekeying, credential-store migration or changed attribution until its existing ROADMAP prerequisites and acceptance are met. |
| Conditional / planned | Codex per-directory keyring preparation | Review and strengthen synthetic item-addressing, coexistence and teardown controls where a concrete gap exists. Keep capability disabled: synthetic controls cannot replace the mandatory live capability check. Do not create an unverified enabled path. |
| Conditional / planned | Cursor Linux preparation | Compare the documented file-store contract with Linux fixtures and identify adapter gaps. Do not enable platform support or claim upstream compatibility on the strength of simulated credentials alone. No new live-account validation. |
| After accepted changes / planned | CI placement and delivery | Reuse existing CI for new regressions; add checks only with distinct failure controls and Linux cost evidence. Run the applicable commit and release gates, record exact coverage and deferred candidates, and finish correctness and quality reviews. |

The primary lane is credential preservation/recovery. Independent characterization
and offline drift comparisons may proceed alongside it; shared files and
credential-policy decisions remain sequential. Follow the user's prohibition on
subagents. Reuse existing modules before introducing abstractions, and do not
repeat successful unchanged checks solely to accumulate evidence.

All candidates receive an explicit accepted/deferred verdict with its reason.
A candidate requiring new live acceptance stays disabled or research-only and does
not block independently releasable work. Do not label an application behavior
change maintainer-only to reuse acceptance that does not cover it. If no candidate
meets its gate, record that outcome rather than cut a release without a justified
change. No release date is promised.

Codex refresh/rotation research, broad attribution redesign, new shells, TUI,
Windows, global mise tasks and unrelated refactoring are outside this scope.
This plan authorizes no real login, live credential mutation, package installation
or public posting as a research shortcut. Publication is a separate execution step
under § Release procedure.

On completion, move current contracts to their owned documents, results to
[ACCEPTANCE.md](ACCEPTANCE.md), remaining work to [ROADMAP.md](ROADMAP.md), and
remove this next-release section. Historical planning stays in git.

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
