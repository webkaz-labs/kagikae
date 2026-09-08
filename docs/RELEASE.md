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
proposal, not a commitment to a new version or release date, and does not declare
the pre-stable CLI contract frozen.

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

### Implementation assessment

The implementation candidates below are planning decisions, not shipped behavior.
The selected scope includes installation receipts and automatic binary removal
for supported direct installations, in addition to integration cleanup. Unsupported
or unrecorded installations retain source-specific manual guidance.

#### Existing seams and gaps

| Surface | Evidence to inspect | Consequence for implementation |
|---|---|---|
| Directory removal | [pin.go](../internal/cmd/pin.go), `runUnpin` and `removeLegacyMiseBlock`; [fragment.go](../internal/cmd/fragment.go), `removeDirFragment` | The handler resolves cwd, prints directly and uses cwd-relative removal. Extract an explicit-directory removal operation; do not loop over the CLI handler or change process cwd. Strengthen ownership checks before sharing it with uninstall: a filename or marker alone is not sufficient to delete customized content. Preserve existing unpin behavior unless separately approved. |
| Discovery | [pinindex.go](../internal/cmd/pinindex.go), `boundDirectoryIndex` and `pinnedDirsComplete`; [miseinit.go](../internal/cmd/miseinit.go), `runMiseInit` | Breadcrumbs can be incomplete and are not a machine-wide registry. Project task generation writes `.mise.toml` without recording the project. Include known bindings and explicitly supplied project paths; show the unregistered-project/manual-shell coverage limit rather than promising whole-machine cleanup. |
| Completion files | [completion_install.go](../internal/cmd/completion_install.go), `completionTarget`, `zshCompletionDir`, `writeCompletionFile` | Refresh selects one current path per shell and file writes do not record install provenance. Enumerate the finite supported locations, including all zsh candidates; delete only recognized generated content. Old/custom content and arbitrary user shell setup require explicit unresolved-item guidance. |
| Global integration | [mise_global.go](../internal/cmd/mise_global.go), `knownCompletion`, `splitGlobalMise`, `updateGlobalMiseCompletion`; [global_fragment.go](../internal/cmd/global_fragment.go), `teardownSynced` | Reuse strict recognition and file-target handling. Completion migration adds a hook, so uninstall must not invoke refresh as cleanup. Resolve or refuse an outstanding migration journal, then remove selection and update synced state through the existing state mutation seam. Do not implement this by invoking `use -s`, which also switches live credentials. |
| Binary ownership | [install.sh](../scripts/install.sh), final copy and refresh steps | The inspected installer copies the executable without recording an installation receipt. A resolved executable path or Go module identity establishes what is running, not which package manager owns its removal. Keep unknown ownership explicit. |

The symbol investigation used gopls with a temporary Go cache. The targeted
`TestGlobalMise*`, `TestCompletionRefresh*`, `TestCompletionInstall*`,
`TestZshCompletionDirPriority` and `TestRemoveLegacyMiseBlock*` tests passed on
2026-09-09. They exercise the existing seams, not the proposed uninstall.

#### Alternatives and recommended contract

| Alternative | Assessment |
|---|---|
| Documentation-only removal recipe | Smallest change, but still makes users find scattered integrations; insufficient for the accepted usability problem. |
| Sequence existing CLI commands | Reuses command names but inherits cwd dependence, direct output and credential-switching side effects; reject as the orchestration design. |
| Inspect, confirm, then apply a structured integration-removal plan | Recommended. Keep discovery and ownership checks beside mutation, return per-target results to the command, and reuse locks and state mutation rather than copying handlers. |
| Universal self-delete and recursive data purge | Requires broader ownership and credential policy changes; outside the first increment. |

Proposed CLI: `kae uninstall`, with `--dry-run`, `--yes`, `--json` and repeatable
`--dir <path>` for additional project locations. Reuse common configuration/path
selection flags. Normal interactive use shows the plan and asks before applying;
non-interactive mutation requires `--yes`, and JSON mode does not imply consent.
All completion and installation writers that share a removable target must join
the appropriate mutation lock; existing completion-file refresh has no such lock.
Cover that writer in this implementation instead of adding a reader-only guard.
Dry-run does not create locks, journals, registrations or state. Never print shell
configuration bodies or credential contents in the plan.

Use separate results for discovery completeness, integration removal and binary
removal. Each target needs a kind, path, planned action, outcome and classified
reason; token spellings belong in `internal/constants` when implemented. Retained
credentials are an intentional outcome, not a failed removal. Missing provenance,
inaccessible directories and custom hooks remain unresolved. Completeness is bounded to the explicitly reported discovery scope; unregistered
custom shell/project code is outside automatic discovery. A successful preview
means an inspection completed, not that removal succeeded. Use existing usage,
lock-busy and unsafe-refusal exit categories; a partially applied plan exits
nonzero and includes completed and remaining targets. A guided binary step must
be reported as pending, never as a completed whole uninstall.

Acquire the relevant existing locks and re-read identity/content before applying
an item. Consent covers only the previewed action; changed content or a new target
requires another preview. Follow the established lock order rather than adding an
uninstall-only lock that other commands ignore. Directory removal takes its pin
lock; global synced changes follow lifecycle/tool/state ordering where applicable
([ARCHITECTURE.md](ARCHITECTURE.md) § Locking). Avoid holding a pin lock while
acquiring a tool lock. Re-inventory after apply to expose
new registrations where observable; do not claim an atomic machine-wide snapshot.

Preserve symlink targets and user edits according to the owning operation's
contract, refusing unsupported ownership rather than following a link and deleting
its target. Keep current-shell exports, shell functions, custom rc code and
unregistered project hooks in the manual-action report. Do not edit tracked Git
ignore files or remove shared exclude entries during this increment.

Prefer idempotent per-target operations over a new whole-machine rollback system.
Keep discovery breadcrumbs and recovery data. Global fragment/state transitions
must retain the existing crash-recovery invariant; a stale synced selection must
not recreate a removed fragment on retry. Choose any additional journal only after
fault-injection tests demonstrate which transition cannot be retried from retained
state. Invalid config must not hide file-based discovery; unreadable state must
block dependent state mutation and remain visible in the report.

#### Installation receipts and automatic binary removal

The operator selected receipt-backed automatic removal on 2026-09-09. Use this
support matrix for the first implementation:

| Installation source | Planned behavior |
|---|---|
| Official shell installer, direct regular-file destination | Record installation after verified download; automatically remove the recorded executable as the final uninstall step when ownership and content still match. |
| Repository `mise run install`, direct local build | Use the same install/receipt operation after building; label the source as a local build rather than claiming release provenance. |
| `mise use` managed tool | Remove kae-owned integrations, then identify the owning config and provide a mise removal step. A receipt must not turn a managed executable or shim into a direct installation. |
| Plain `go install`, manual copy, or legacy install without a receipt | Remove recognized integrations and give the exact supported next step where identifiable. Reinstall through a supported direct installer to obtain a receipt; do not infer one from PATH or a module name. |

Proposed receipt storage is a versioned, non-secret record under the resolved kae
state root's `installations/`, keyed by a digest of the absolute installation path.
Record source kind, destination, version, executable SHA-256 and platform, with
owner-only record/directory permissions. The paths belong in `internal/paths` and
the schema in DATA-MODEL when implemented. Multiple destinations retain separate
records; uninstall targets its own selected installation, not every matching name
on PATH. A changed XDG state root can make a receipt unavailable; report that
instead of searching other users' roots or silently recreating ownership.

Use one `internal/installation/` operation for binary replacement plus receipt
registration, invoked by both the release installer and local install task. Prefer
having the staged new binary perform this operation rather than implementing JSON,
locking and recovery separately in shell. Keep checksum verification before that
invocation, and preserve release verification's isolated transport. Do not add an
installer downloader, privilege elevation or package-manager invocation to uninstall.

The receipt is local bookkeeping, not an authenticated provenance statement or a
permission grant for arbitrary deletion. Validate its schema, filename/path binding,
source, ownership and permissions; check the executable's module identity, digest,
regular-file type and supported parent path. Refuse unexpected symlinks, hard-link
aliases, managed locations, replaced parents and a changed binary. The official
[os.Executable documentation](https://pkg.go.dev/os#Executable) explicitly warns that
the returned path may no longer refer to the executable that started the process;
re-reading that path is not proof of the running image's identity. Establish an
implementation-specific safe self-removal check on macOS/Linux, with replacement
fault injection, before enabling deletion. Do not promise protection against a
malicious process with the same user's write access merely from a receipt hash.

Both supported installers and uninstall must use the same installation lock. Hold
it across binary/receipt transitions, and revalidate before the final unlink.
External installers do not honor that lock: a stale or changed receipt must refuse
automatic removal. Installation succeeds only with a matching receipt; if a receipt
write fails after replacement, report partial installation and leave automatic
removal disabled until an explicit supported reinstall repairs it. Never delete a
newly replaced binary as an unconditional rollback of a failed receipt write.

Remove the executable only after all required in-scope integration cleanup succeeds
and the final removal appears in the confirmed plan. Delete the exact owned file,
not its directory, through the running process; no delayed shell helper or broad
recursive removal. Use the receipt for any necessary pending/removed transition,
retaining enough non-secret evidence to explain interruption after unlink. Define
how a supported reinstall reconciles that state before shipping. Report metadata
finalization failure separately from binary deletion, rather than encouraging a
retry through a binary that no longer exists. Retained data and receipt history are
listed explicitly and remain outside credential purge.

#### Implementation order and acceptance

1. Define report/confirmation/exit contracts and ownership recognition fixtures.
   Add command routing/help/completion together when the command becomes runnable.
2. Implement discovery and dry-run with explicit paths and bounded candidate lists.
   Keep the command/report in `internal/cmd/uninstall.go`; place reusable ownership
   and file-removal operations in `internal/integration/` only where callers share
   them. Keep state mutation in the existing App seam, avoiding a second state writer.
3. Implement the shared installation/receipt operation and route the shell installer
   and local install task through it. Then apply integration cleanup with content
   rechecks and existing locks, finishing with receipt-backed self-removal or manual
   guidance according to the support matrix.
4. Test through the command/report interface: clean/legacy/custom files, markers in
   strings, all supported completion locations, symlinks, duplicate paths, unreadable
   or missing breadcrumbs, invalid config/state, pending migration, lock conflicts,
   edits between preview/apply, failure at each write, and a second run after partial
   success. Fake runners must reject credential reads/writes and package removal.
5. Exercise actual temporary-binary install/uninstall with offline release fixtures:
   first install, upgrade, receipt failure, interrupted cleanup, stale/tampered receipt,
   self replacement, symlinks/hard links, paths with spaces, changed XDG roots, multiple
   installations and package-manager-owned paths. Stub package managers; never remove
   the operator's binary. Cover macOS/Linux file semantics through the supported gates.
6. Verify that retained credential payloads and stores remain byte-identical, no
   unrelated file changes, and nothing reports whole removal when work remains.
   Run the full gate and an isolated uninstall smoke, update English/Japanese user
   docs and detailed contracts, and use the existing release procedure. No additional
   live login or real uninstall is needed to accept this scope.

For mise guidance, distinguish removing a configured request from deleting an
installed version. The official [unuse documentation](https://mise.jdx.dev/cli/unuse.html)
describes configuration removal with pruning, and points to `mise uninstall` for
installation-only removal. Confirm the installed mise version's help and the exact
owning config before displaying a command; do not assume the global config or
unlink a mise shim. This investigation executed help only, not either removal.

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

The next increment should first prototype a maintainer-only structured report over
existing fingerprint/naming checks, outside the shipped CLI and outside the default
commit gate. Compare that prototype with direct command use before accepting it.
Do not parse free-form test logs as the long-term interface: expose structured
results at the existing check seam if the report is retained. Name checked properties
and unavailable/not-checked cases; leave verified versions and dates unchanged.

On 2026-09-09, the installed-artifact `mise run fingerprint` check passed; its
Codex exclusion remained explicit. The inspected Claude Homebrew cask version
directory contained `2.1.261` only, so this investigation established no old/new
artifact pair. `scripts/namingagreement/verify.py` already gates execution on a
reviewed digest. Do not execute an unreviewed build through it to fill the gap.
For a future pair comparison, accept explicit existing artifact paths with recorded
source/version/digest first; defer an automatic cache/downloader and behaviour-site
hashes until reviewed inputs and controls exist. A green fingerprint is not proof
of unchanged authentication behavior.

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
