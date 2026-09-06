# Release Process

## Current release — kae v0.18.3

The release record is the tag and
[GitHub release](https://github.com/webkaz-labs/kagikae/releases/tag/v0.18.3).
Read its scope and patch-version rationale with
`git show v0.18.3:docs/RELEASE.md`. The completed
[reliability plan](plans/core-reliability.md) retains the index-reuse decision;
[ACCEPTANCE.md](ACCEPTANCE.md) owns the bounded real-machine measurements.

Published archives, SHA-256 checksums, GitHub provenance attestations and the
installer were verified on 2026-09-06 for v0.18.3. The macOS arm64 archive and
isolated installer both reported `kae v0.18.3`.

## Next release — kae v0.18.4

This patch release follows the
[verification-efficiency plan](plans/verification-efficiency.md): verification by
change impact, faster docs selftests, owned smoke HOME cleanup, saved release
smokes and a Go published-artifact verifier. Account lifecycle refactoring was not
adopted after comparing its differing lock lifetimes and failure recovery; the
plan retains the decision. Application behavior is unchanged apart from the
reported version, so this is a patch release rather than a new command surface.

The plan owns progress and evidence. Resolve each candidate as implemented or
not adopted with a reason, verify required failure detection, and complete the
procedure below. Live acceptance applicability and any reuse of prior evidence
are recorded in [ACCEPTANCE.md](ACCEPTANCE.md). Publication and verification of the
new assets remain part of completing this release; the v0.18.3 record above is
its published baseline.

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
