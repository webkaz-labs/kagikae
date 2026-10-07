# Release Process

## Current release — kae v0.24.0

Minor release. `kae use`, `kae add` and `kae rollback` restart codex's managed
daemon when it is not using the account they leave live, and on macOS offer to quit
and relaunch the ChatGPT desktop app after changing codex's account, so neither
keeps the previous account; long-running codex sessions get a warning to restart
them by hand, and `--no-restart` warns instead of restarting. `kae doctor --yes`
reports a daemon on another account as `resident_drift`, and usage drops a codex
reading whose rollout another captured account created ([CLI.md](CLI.md) § kae use
Semantics, **Resident processes (codex)**). `--full` shows each Limit reading's
age. The codex adapter's verified version moves to 0.160.1, the claude adapter's
to 2.1.288 and the agy adapter's to 1.2.12. Release binaries are no longer built
for Intel macOS (darwin/amd64), and the published archives are the first signed by
the Packslip 1.6.0 Action. The release is the tag `v0.24.0` and its GitHub release
once published; [ACCEPTANCE.md](ACCEPTANCE.md) § v0.24.0 candidate records the
checks and the publication result.

## Release procedure

Releases are cut by pushing a `vX.Y.Z` tag; GitHub Actions
([.github/workflows/release.yml](../.github/workflows/release.yml)) runs
[GoReleaser](https://goreleaser.com) ([.goreleaser.yaml](../.goreleaser.yaml))
to build, archive, checksum, and publish. Do **not** create the GitHub release
by hand — the tag does it.

1. Bump `toolVersion` in `internal/cmd/cmd.go` to the new `vX.Y.Z` (the binary's
   reported version is hardcoded, not injected; it must match the tag) and the
   `TestBuildVersionReport` expectation. While `.goreleaser.yaml` excludes
   darwin/amd64 unconditionally, that tag is v0.24.0 or later, including a patch
   tag released after a failed signer change (below); otherwise first move
   `intelMacDropped` in `scripts/releaseverify` and the `scripts/install.sh`
   boundary to it.
2. Follow [VALIDATION.md](VALIDATION.md) § Standard Suite: run its commit gate and
   **every slower release-time check it names**, including `release-evidence`, before
   the tag. Update the docs (ROADMAP/VALIDATION and any behavior docs). Work the
   release checks in
   [ACCEPTANCE.md](ACCEPTANCE.md) before the tag — nothing in it runs from
   `mise run check`, and that file is where a result is recorded. Follow its
   § Optional account-combination checks for the release classification of checks
   that require additional account combinations.
3. Merge to `main` and push; CI (`ci.yml`) must be green on that commit, both the
   `check.yml` job and the arm64 `platforms.yml` jobs. The release workflow re-runs
   only `check.yml`, so this step is where the platform jobs gate a release.
4. Tag and push: `git tag -a vX.Y.Z -m "kae vX.Y.Z — <summary>"` then
   `git push origin vX.Y.Z`. The release workflow gates on the same `check.yml`
   CI runs — a **subset** of `mise run check`, and that workflow's own steps are
   the copy of it to read — then GoReleaser builds darwin/arm64 and linux ×
   amd64/arm64 (`kae_<version>_<os>_<arch>.tar.gz` + `checksums.txt`), creates the release
   with a grouped changelog, and attests the archives named by the release
   checksum manifest. A separate `packslip` job then downloads these published
   assets for the exact tag and source commit, verifies their provenance and
   signs their release/v1 metadata with the Packslip 1.6.0 Action. The workflow
   owns the full Action commit pin, and that Action checks the CLI archive it
   downloads against the digest map in the pinned commit, so the pin also fixes
   the signing CLI. The signer stays an exact pin and changes only in its own
   commit and release, together with the fixture's signing CLI
   (`signerPackslip` in `scripts/packslipverify`) and the version, digest and
   source commit in `check.yml`'s `Signing test` step. Linux archives are signed as
   `gnu`: Packslip 1.4.0 and later omit `libc` for a static Linux build, which
   would let musl hosts select it, while the README keeps musl outside the
   Packslip selection. `--packslip-prepare` therefore writes the Action's
   manifest from the verifier's spec, and `--packslip-publish` refuses a
   statement whose `libc` differs. The workflow runs only on a tag and has no
   dry run, so a new signer first runs on the next release. Before that, run the
   fixture smoke and `PACKSLIP_BIN=/absolute/packslip go test -count=1 -run
   TestSignerCLIStatesSpecLibc ./scripts/releaseverify` with the new signer's
   CLI: it signs static archives with the Action inputs read from the workflow
   and checks the statement against the verifier's spec. CI also runs that test
   on linux/amd64 with the current signer, fetched by digest and verified with
   `gh attestation verify` under the pinned Action's provenance policy, so a pull request that bumps the signer runs it with
   the new CLI there. Results are recorded
   in [VALIDATION.md](VALIDATION.md) § Packslip consumer smoke. Completion
   assets are generated by the existing CLI generator and included in each
   archive.
5. Run `mise run release-verify -- vX.Y.Z` from the repository root, alone while
   no other task edits the checkout. It downloads through `gh`, verifies the
   manifest, archive contents and provenance attestations, then checks the native
   version and installer in isolated environments. For v0.21.0 onward, it also
   requires the local tag, a Packslip verifier `>=1.1.1,<2`, mise 2026.9.3 or
   later and Go, checking both versions before downloading; validates the exact
   GitHub OIDC signer, source commit, archive metadata and static resources, and
   requires `packslip verify` to refuse the same bundle under a workflow identity
   that is a strict prefix of the release's; then installs the published version
   through the real native mise consumer with production trust settings, after
   requiring that consumer, in separate HOME/XDG roots, to refuse the same version
   under an identity that is a strict prefix of the release's and under one that
   extends it. Each refusal must be the identity mismatch naming the configured
   identity, not merely a nonzero exit. Verifier versions are lower bounds, not exact
   pins: the JSON `toolchain` field records the Packslip and mise versions the run
   exercised, and those refusal controls are the evidence that a newer release
   still checks the signer. Upstream's 1.2.0 through 1.6.0 release notes state
   that 1.x keeps the release/v1 format, so the verifier need not match the
   signer. Verify a downloaded Packslip with
   `gh attestation verify <archive> --repo jdx/packslip` first; Packslip 1.6.0's
   build provenance names `refs/heads/release-plz` rather than the tag
   (observed 2026-10-05), so do not require `--source-ref refs/tags/vX.Y.Z` for
   it. The installer receives only the verified assets through a non-forwarding
   curl fixture; its HTTP transport is not tested by this command. JSON `status` is `success`, `failed`, or
   `unavailable`. Success exits zero; the other statuses exit nonzero through
   `go run`, so use the JSON status to distinguish them. A missing prerequisite
   is not a passing check.

The consumer normally retains mise's default release-age policy and requests an
exact version. mise 2026.9.10 and later install an exact `packslip:` pin during
the release-age wait (its release notes). Observed 2026-10-05 with mise 2026.10.2:
the exact pin installed v0.23.0 about 16 hours after publication under the default
policy, while `latest` skipped 0.23.0 ("verified release time is after the allowed
cutoff") and `mise ls-remote` hid it. On such a mise, a default-policy pass for a
fresh tag therefore does not show that users of a fuzzy or `latest` request can
install it yet. An older mise may refuse a fresh tag even when its signature
verifies; the identity control then fails closed because the refusal is not an
identity mismatch. Wait for that policy's cutoff, or, only with explicit operator
approval for the isolated check, run
`KAE_RELEASE_VERIFY_FRESH=1 mise run release-verify -- vX.Y.Z`. This sets the
temporary consumer configuration's minimum age to zero; it changes neither the
operator's configuration nor the signature requirements. Record the exception
in the acceptance result; it is not a default-policy pass.

If archives exist but the Packslip job fails, the release is incomplete. Retry
only the failed signing job against the same published bytes, then rerun the
consumer verification. `scripts/releaseverify` accepts an existing bundle only
after verifying it against those bytes and refuses conflicting metadata; its
upload retries are bounded and do not use `--clobber`. A rerun uses the tag's
own workflow, so when a signer change is what fails, revert that commit's
Action pin and `signerPackslip` and release the next patch tag instead; the
failed tag stays incomplete. Do not rerun GoReleaser, replace archives under a
signed manifest, or fall back to an unsigned backend to repair this failure.

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
