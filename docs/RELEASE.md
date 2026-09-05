# Release Process

## kae v0.18.2 — Diagnostic Trust

Active target: correct diagnostic silence and bound the `kae relogin` success
message to what kae observed. This is a patch release adding missing findings to
an existing doctor report; JSON `schema_version` remains `1`.

- Report an incomplete pin index once as the machine-wide `pin_index_incomplete`
  warning, including when doctor is filtered by tool. Continue diagnosing readable
  pins and preserve the attribution and refcount refusals.
- Report an existing recorded identity payload that cannot serve as an account
  record as `identity_record_invalid`, a warning scoped to its tool and account.
  Output must not include the payload, identity values or secrets. Missing payloads
  remain `secret_missing`; unrecorded identities keep their existing behavior, and
  comparisons remain `identity_drift`. Do not report the same fact twice or relax
  attribution refusals.
- Change the strong relogin success line to
  `Captured the changed <tool> credential for <tool>/<account> from this directory's store`.
  Preserve harvest behavior, child exit codes, refusal conditions and the fallback
  line.

Do not add commands, reports, fields or policy. Keep pin-walker consolidation and
bound-directory index module work after this release. An identity helper may stay
inside the existing identity module if it avoids duplicate classification; do not
replace harvest or restore paths. A relogin observation interface and keychain item
identity refactor are outside this target. The research-only lane, upstream-drift
automation, Codex per-directory keyring enablement, Tier-2 expansion, TUI, Windows
and command-system expansion retain their [ROADMAP.md](ROADMAP.md) gates.

Release readiness requires the procedure below, including release-time validation
and required real-machine acceptance. Additional account combinations remain
optional under [ACCEPTANCE.md](ACCEPTANCE.md) § Optional account-combination checks.

### Candidate readiness

The implementation candidate is `67b6ff2`. Its local commit gate, vulnerability
audit, installed-tool fingerprints, GoReleaser configuration check, release mutation
evidence and snapshot archive build passed on 2026-09-05. The executable smoke blocks
in [VALIDATION.md](VALIDATION.md) passed through the isolated smoke harness; that
document records the additional completion and per-account-store results beside
their procedures.

Required real-account acceptance passed on 2026-09-06 (JST), tested from
`3269b67` with the implementation candidate's executable code: Claude Code 2.1.261
switch/rollback and bound-directory authentication, plus Copilot 1.0.83 same-account
apply/rollback. [ACCEPTANCE.md](ACCEPTANCE.md) records the measured results and their
limits. Global Claude ended on `side`.

Full upstream-assumption re-verification for Claude Code 2.1.261 remains pending:
the earlier release-time checks used 2.1.260, and the new authentication results do
not re-verify every assumption in [VALIDATION.md](VALIDATION.md) § Upstream Behaviour
Assumptions. The declared verified version remains unchanged. The installed-tool
fingerprint gate also failed: Claude 2.1.261 and opencode 1.18.26 differ from the
recorded 2.1.260 and 1.18.25 artifacts, and agy 1.1.23 has a digest mismatch.
Re-measure those artifacts through the upstream verification procedure rather than
updating their recorded identities alone. Complete that pass and the final
commit/release gates before publication. Main integration, push,
tag publication and branch cleanup have not been performed; external publication
requires the operator's explicit approval.

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
5. Verify the published assets and `scripts/install.sh` against the new tag.

GoReleaser auto-generates the changelog from commits; edit the release body
afterward for curated highlights when useful. Windows is not built
([ROADMAP.md](ROADMAP.md): `internal/lock` is Unix-only).

**This file carries the active target and procedure, not the history.** What shipped
is the tag, the GitHub release it created, and `git log`. A per-release entry lived here for
every version through v0.17.0, and the file was cumulative, so
`git show v0.17.0:docs/RELEASE.md` is the whole set. Read an entry as of its own
tag: its forward pointers were not maintained forward, so an item it defers to
[ROADMAP.md](ROADMAP.md) may have shipped since — that file and `git log` are
where to check.
