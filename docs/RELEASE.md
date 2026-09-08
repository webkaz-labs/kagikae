# Release Process

## Current release — kae v0.20.3

Offline recovery regression coverage and state-specific recovery guidance, plus a
correction to Cursor's unsupported-platform explanation. This patch adds no
command, JSON token or capability and changes no credential mutation policy.
[ACCEPTANCE.md](ACCEPTANCE.md) § Offline recovery and validation assessment records
the candidate decisions and validation boundary; [ROADMAP.md](ROADMAP.md) retains
raw-document rescue, deadline representation, drift-pair and platform prerequisites.

The release record is tag `v0.20.3` at `d52573c` and the
[GitHub release](https://github.com/webkaz-labs/kagikae/releases/tag/v0.20.3).
On 2026-09-09 (JST), the main CI and release workflow succeeded, and
`mise run release-verify -- v0.20.3` returned `status: success` for the archives,
checksums, provenance and isolated installer. The native binary reported
`kae v0.20.3`; installer transport used verified assets, not live HTTP.
The operator's local installation was not changed.

## Proposed next release — uninstall and upstream drift re-verification

Keep v0.20.3 as the current release while evaluating uninstall usability first
and a bounded upstream re-verification improvement second. This is a planning
proposal, not a commitment to a new version or release date, and does not declare the pre-stable CLI contract frozen.

### Uninstall usability

Candidate entrypoint: `kae uninstall`. Plan an end-to-end removal flow instead of
requiring the user to find every hook before deleting the executable. This is a
proposed command, not part of the current CLI contract.

| Area | Candidate and completion condition |
|---|---|
| Preview and consent | Provide a read-only preview of the exact owned integrations, retained data, unresolved items and binary-removal step. Define human and JSON reports, confirmation and exit behavior before implementation; preview must not mutate files or credentials. |
| Integration teardown | Inventory global isolated settings, completion registrations, generated automatic hooks and known directory bindings. Reuse existing ownership, locks, journal recovery and unpin mechanisms. Preserve unrelated or customized content; report missing, moved, inaccessible or ambiguous targets without claiming complete removal. Never search arbitrary user directories to manufacture completeness. |
| Credentials and retained data | Default to retaining snapshots, backups, preservation records and working stores. Removing environment selection must not silently log another account into the real home or delete upstream credentials. State exactly which data remains and how to inspect it. Full data/credential purge is a separate design decision, outside this candidate. |
| Executable removal | Complete integration teardown before removing the executable. Account for direct installs, mise-managed installs and Go builds, including the `kagikae` binary name. Where ownership is uncertain or a package manager owns the executable, give the source-specific next step instead of unlinking a shim or guessed path. Do not report the whole uninstall complete while that step or an integration remains. |
| Recovery and validation | Make interrupted and repeated teardown understandable and retryable, including invalid config, an outstanding migration journal, multiple registrations and partial failures. Keep enough state to retry unresolved cleanup. Use isolated fixtures and fake package-manager/credential runners; no additional live login or real uninstall is required. Update English/Japanese setup and removal guidance and command completion together if the command ships. |

Settle the report/confirmation contract and the supported automatic removal paths
before implementation. Current-shell environment and already running processes
need an explicit restart/exit instruction where teardown cannot update them.
This plan authorizes neither removing the operator's installation nor deleting
credentials during planning or validation.

### Upstream drift re-verification

| Stage | Candidate and completion condition |
|---|---|
| Baseline | Map existing version/date checks, literal fingerprints and login-free naming checks to their observed properties and blind spots. Identify concrete missed-change or repeated-manual-work cases before adding a check. |
| Reproducible inputs | Establish how to retain or locate reviewed old/new upstream artifacts, with version, source and digest. Keep credentials and user config out of the comparison inputs. Prefer existing artifact locations; decide storage, size limits and retrieval policy before creating a cache or downloader. |
| Bounded report | Compare an existing-task wrapper/report with direct use of current commands. Implement only if it reduces repeated investigation: distinguish changed, unchanged within checked properties, unavailable and not checked, and route findings to the existing upstream-auth-drift procedure. No automatic update of verified versions or dates. |
| Conditional detector | Add a behaviour-site comparison only if an actual artifact pair and positive/negative controls demonstrate value beyond literal fingerprints, including identifier-only changes, missing/ambiguous anchors and unavailable inputs. Otherwise defer it with the missing evidence stated. |
| Delivery decision | Use isolated fixtures and existing reviewed artifacts, with no additional live login or credential mutation. Run applicable gates and measure added cost. Defer this lane if no improvement qualifies; it does not block a separately accepted uninstall release. |

For upstream re-verification, start with the Claude checks for which a reviewed
naming harness already exists; add other tools only for a demonstrated gap with
suitable evidence. This upstream work
changes neither credential policy nor platform capabilities. Scheduling, upgrade
hooks, background monitoring, external notifications and package installation are
not authorized by this proposal. Review the delivery surface and evidence-storage
choice with the operator before fixing the implementation scope.

Reopen product work on a reproduced wrong-account or credential-loss incident,
a documented upstream incompatibility, or concrete unmet daily-use demand.
Unchanged existing regression coverage is not by itself a reason for another
release. [ROADMAP.md](ROADMAP.md) retains deferred research and capability gates.
Remove this proposal after its outcome is recorded in the owned documents.

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
