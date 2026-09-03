# Architecture

Package layout, layering, and implementation boundaries for `kae`.
The repository follows the shared Go CLI standard, read from the user-level
`go-cli-tooling` skill rather than a copy carried here
([AGENTS.md](../AGENTS.md) says why).

## Package Layout

```text
kagikae/
  main.go                 # dispatch only
  internal/
    cmd/                  # command handlers, report builders, text/JSON output
    adapter/              # Adapter interface + tool adapters
      claude/
      codex/
      agy/
      opencode/
      cursor/
      copilot/
    companion/            # companion-auth lockstep registry (git/gh/cloud CLIs)
      git/ gh/ cloudflare/ kubectl/   # one declarative Spec per companion
    artifact/             # artifact primitives: json-pointer / file / keychain
    freshness/            # pure per-tool credential expiry / refresh-token parser
    jwt/                  # JWT claims-segment decode (freshness exp, codex identity)
    keychain/             # security-CLI access to upstream tools' keychain items
                          #   (incl. a per-command read cache, WithReadCache)
    config/               # TOML config parse/validate/defaults + comment-preserving editor
    constants/            # JSON contract vocabulary (status, codes, drivers)
    paths/                # XDG resolution for config/data/state/locks
    secret/               # secret backend interface + keychain/libsecret/file
                          #   (incl. a per-command read cache, WithReadCache + Cached)
    patch/                # JSON Pointer get/set + atomic file writes
    lock/                 # per-tool advisory file locks
    backup/               # backup create/list/prune/restore
    envprofile/           # env-mode profiles (var names; values in secret backend)
    state/                # state.json load/save
    runner/               # subprocess seam (template standard)
    testutil/runnertest/  # shared canned-response runner fake for tests
  scripts/
    docrefs/              # `docs-check`'s extractor: every markdown link and every
                          #   `X.md § Name` citation, one stream with a kind column
    docscan/              # `mise run docs-scan`: reports prose two documents carry
                          #   twice
                          # Both are package main outside the released binary
                          #   (.goreleaser.yaml builds `.` only), so the dispatch-only
                          #   rule above is about kae's main.go. This listing named one
                          #   of them and called it "a second package main" while a
                          #   third had already existed for a release — derive the set
                          #   with `grep -rln '^package main$' --include='*.go'` rather
                          #   than counting here
```

## Layering

```text
main -> cmd -> adapter -> artifact -> {patch, secret, runner}
              \-> {config, state, backup, lock, paths}
```

- `cmd` owns flag parsing, report construction, and output. Nothing below
  `cmd` prints.
- Adapters never import `cmd`. They expose typed results; `cmd` renders them.
- `artifact` is the single place that knows how to capture/apply the three
  artifact kinds. Adapters declare *which* artifacts exist for a tool and
  platform; they do not duplicate IO logic.
- All subprocess calls (`security`, `secret-tool`, binary detection) go
  through `internal/runner`. Production code never calls `exec.Command`
  directly.
- **Completion backend seam** (`cmd/complete.go`): the hidden
  `kae __complete <kind>` reads the live router/config/captured state and prints
  one candidate per line. It is the single source for both completion surfaces —
  kae's own generated shell scripts (`cmd/completion.go`) and the `kae mise init`
  task `complete "<arg>" run="kae __complete …"` directives (`cmd/miseinit.go`) —
  so candidate lists never drift from the real surface. The `flags <command>`
  kind lists a command's flags from the same per-command registrars the parser
  uses (`cmd/flagspec.go`, also called by `parseCommon`), so flag completion
  tracks the real flag set. The shell scripts route by the flag-filtered
  positional index (a **boolean** flag before the positionals does not shift
  completion; one that takes a value does — [ROADMAP.md](ROADMAP.md) §
  Command-system expansion owns why). Read-only,
  no locks; its line-oriented output is an internal contract, not the JSON contract.
- **Did-you-mean hints** (`cmd/suggest.go`): the unknown-command, unknown-tool,
  and unknown-profile usage errors append a single hand-rolled Levenshtein
  nearest-match suggestion drawn from the same candidate lists the completion
  backend exposes, so suggestions never drift. Suggestion-only — exit codes and
  the JSON contract are unchanged; the noise threshold (best distance `<= 2` and
  `<= len(input)/3 + 1`, ties suppressed) keeps wildly different tokens silent.

## Adapter Interface

```go
type Adapter interface {
    ID() string
    // Detect inspects the live environment: binary, auth presence, driver.
    Detect(ctx context.Context, env Env) (Info, error)
    // Artifacts returns the auth artifact set for this platform/live env.
    Artifacts(ctx context.Context, env Env) ([]artifact.Spec, error)
    // Doctor returns adapter-specific checks beyond Detect.
    Doctor(ctx context.Context, env Env) []Check
}
```

`Artifacts` returns the credential **first**, and `Detect` reads `specs[0]` as
that credential — the only positional dependency. Nothing else infers the
credential from order: the per-directory materializer (`writeDirCredential`)
resolves it by name through `credentialArtifactName`, and the identity step beside
it (`writeDirIdentity`) filters on `IdentityOnly`, so an adapter that grows a spec
must not assume anything follows position. A spec may set `IdentityOnly` to mark an artifact that records *who*
is logged in without being part of what authenticates (claude's
`/oauthAccount`). Everything that follows from it is a consequence of not being a
credential: its live presence alone is not a login, a change to it is not an auth
change, its live copy is not authoritative for a recapture, and losing it is safe
— so a snapshot or backup that lacks it (or whose payload is gone) applies it as
absent instead of failing, and the tool rebuilds it. Never set it on a
credential. Whether losing it is survivable is policy, so it lives on the spec
and is never persisted into a snapshot or backup record.

Such a spec also declares `IdentityKeys`: the payload keys that actually name the
account. An identity payload carries volatile bookkeeping the tool rewrites on its
own schedule (claude renews `/oauthAccount.profileFetchedAt` on every profile
refetch), so "is the live identity still the one kae applied?" is answered on
those keys alone — `identityDiffers` in `internal/cmd`. Known readers, **not a closed
set**: `doctor identity_drift` (both frames — the active account's live state, and a
bound directory's own store), both recaptures (`kae use`'s switch-away and
`kae run -s`'s post-child), and the per-directory harvest's attribution guard. Comparing whole payloads
made a correct switch look like drift a day later. Credentials keep the strict
byte comparison (`snapshotArtifactDiffers`): one differing bit there is a
different credential.

`Env` carries the resolved home directory, OS, environment lookups, and the
live base paths (honoring `CLAUDE_CONFIG_DIR` / `CODEX_HOME` when already
set, and `CLAUDE_SECURESTORAGE_CONFIG_DIR` for the credential alone).

That last one is why every per-directory resolution takes a **pair** of
directories rather than one (`cmd.bindDirs`: the config dir and the credential
store). `cmd.dirSpecs` overrides both variables when it asks an adapter where a
bound directory's artifacts are — the credential variable **always**, with the
config dir itself when a tool cannot separate the two, because leaving it alone
would let a value inherited from the surrounding bound shell answer for a store it
has nothing to do with. The two halves are a struct and not two string arguments
on purpose: they are swappable at a call site, and getting them backwards is
silent — kae would write the credential under one name while the tool read the
other, with every offline check green.

Capture, apply, verify, backup, and rollback are generic operations in
`internal/account` semantics implemented by `cmd` + `artifact` over the
artifact specs, so every adapter gets locking, backups, dry-run, and
redaction identically.

Adapters may implement optional capability interfaces, type-asserted by `cmd`
(the same pattern as `secret.Enumerator`):

- `Identifier` (`Identity(ctx, env) (string, error)`) reads the live login
  identity so `kae add <tool>` can default the account name and record it in the
  snapshot. A tool without a readable identity (agy) does not implement it, and
  `cmd` falls back to requiring an explicit name.
- `Fresher` (`Freshness(payload) freshness.Info`) reads a credential's expiry and
  refresh-token presence. `cmd.freshnessOf` dispatches to it; a tool with no
  datable credential (copilot pointer, agy blob) omits it and is treated as
  not-datable. `internal/freshness` holds only the shared parsing primitives —
  no per-tool knowledge — so it stays a leaf package (no `adapter` import).

  Five consumers, one deadline, one classifier. `cmd.reloginDeadline` turns an
  `Info` into the instant the credential stops being able to open a session without
  an interactive login; `cmd.credentialStateAt` is the only thing that decides which
  band it is in (`ok` / `expiring` / `stale`). The switch-time warnings,
  `doctor credential_stale`, `doctor credential_expiring`, the
  `kae ls`/`accounts`/`status` freshness column and the bound-directory sweep all
  route through it, so none of them can disagree with another about the same
  credential — that branch was written three times briefly, which is exactly how a
  boundary drifts (two predicates on separate bodies had already drifted once on the
  exact-tick case). `needsRelogin` is the second predicate on the same deadline, for
  the recapture-downgrade guard that needs only the past/not-past half.
  A zero `RefreshExpiresAt` alongside a refresh token means *unknown* and yields no
  deadline at all — so no state, never `ok`; treating it as "never expires" is the
  failure that keeps being rediscovered.

  `doctor`'s `credential_superseded` is deliberately **not** a sixth consumer of this
  classifier, and nobody should fold it in. It asks a different question of a
  different field: `expiresAt` *between copies* (`orderable` / `supersedes`), not the
  relogin deadline. That is the whole reason it can see an invalidation the five
  above cannot — `refreshTokenExpiresAt` is exactly what an invalidation does not
  move.

`VerifiedVersion() string` and `VerifiedOn() string` are **not** among them: they
are methods of `Adapter` itself, because kae relies on undocumented *behaviour* of
every tool and a behaviour-only upstream change passes every structure guard, so
every adapter owes both and the compiler is the right place to say so (a new tool
cannot be added without them). `VerifiedVersion()` declares the upstream release
the adapter's behaviour assumptions were last verified against, so `doctor
upstream_version` can warn when the installed tool is a newer major/minor; `""`
means "no usable signal, skip me", only for a version scheme the comparison cannot
read (cursor's date versions), and `TestVerifiedVersionFormat` guards that value.
`VerifiedOn()` is the date those assumptions were last checked, never empty, and it
is `TestVerifiedVersionsMatchTheDocs` that parses it. The assumptions themselves are
listed in [VALIDATION.md](VALIDATION.md) "Upstream Behaviour Assumptions".

See [ADAPTERS.md](ADAPTERS.md).

Adapters return structured refusals (`unsafe_refused`, `unsupported`,
`auth_missing`) instead of writing when the live layout is unrecognized; the
normative allowlists live in [ADAPTERS.md](ADAPTERS.md).

## Switch Mechanisms

One cell of the scope × environment matrix ([PRODUCT.md](PRODUCT.md) § Switching
Surface is the user-facing statement of it) maps to one mechanism: global shared =
in-place credential patch; global isolated = `CLAUDE_CONFIG_DIR` / `CODEX_HOME` via
a kae-owned global mise fragment; per-dir shared =
symlink-everything-but-credential; per-dir isolated = private config dir with
opt-in shares. All per-dir bindings use kae-owned mise fragments — kae never edits
the user's `mise.toml`. Each clause here is a summary: [ADAPTERS.md](ADAPTERS.md) has
the per-tool switched/preserved contract, [SCOPE-MODEL.md](SCOPE-MODEL.md) §5 the
rationale for the shared mechanism (including what per-dir shared does *not* symlink),
[CONTEXT.md](CONTEXT.md) the vocabulary, and [ROADMAP.md](ROADMAP.md) the ordering.

## Switch Transaction

```text
1. resolve tool/account (or profile -> per-tool accounts)
2. acquire per-tool locks (all requested tools; fail fast with lock_busy)
3. detect live state; build artifact specs per tool
4. create one backup covering all tools about to change
5. recapture: if the currently-active account's live credential diverges from
   its snapshot, rewrite the snapshot first (so a later switch back applies a
   live token); best-effort, never aborts the switch
6. apply artifacts per tool (atomic writes / keychain updates)
7. on any failure: restore the backup for already-applied tools, report
8. update state.json (under the state lock, re-reading the file); prune old
   backups
9. release locks
```

The whole switch wraps `ctx` in `keychain.WithReadCache`, so the `security`
reads steps 3–6 make of one tool's keychain service collapse to a single
invocation (the recapture in step 5 adds no extra read or auth prompt); writes
in step 6 invalidate the cache. The cache key is service+account, matching the
specs: every keychain artifact kae ships is identified by both, so a service
holding more than one legitimate item — agy's `gemini`/`antigravity`, codex's
per-`CODEX_HOME` `Codex Auth` — is never conflated. No child runs
during a switch, so the cache never serves a stale live credential.

`--dry-run` runs steps 1–3 and prints the plan from the artifact specs; it also
annotates a stale switch target (snapshot past `expiresAt` with no refresh
token) with a warning, the same `internal/freshness` predicate `doctor` uses.

`kae use <profile>` / `kae use <tool> <account>` (explicit positional) enters
this transaction directly. Bare `kae use` (no positional, the folded `apply`)
prepends a lock-free belief check — state.json against the resolved profile
mapping — and enters the transaction only on divergence; the matching case
returns before step 2 (no locks, no backup).

## Run Transaction (`run -s`, the real-home mode)

`kae run -s` (the default) extends the switch transaction around a child
process:

```text
lock (held for the entire child run) -> backup (reason "run") -> apply
-> child runs with inherited stdio -> re-resolve each tool's specs
-> recapture refreshed credentials into the account snapshots
-> restore the backup, except for a tool whose live credential the restore
   would supersede -> prune -> unlock
```

The restore is **per tool, not unconditional**. `kae run -s <tool> <the account
that was already active>` backs up that account's own credential and the child then
refreshes it, so for a tool whose refresh token rotates single-use the pre-child copy
in the backup can no longer refresh: writing it back logs the real home out while
reporting success. Such a tool is left as the child left it, with the reason on
stderr, and "previous auth state restored" is printed only when something was
actually restored ([CLI.md](CLI.md) § kae run Semantics is the contract;
[ROADMAP.md](ROADMAP.md) § Every credential copy owns the design).

The re-resolution step is not bookkeeping: a child can move the credential to the
tool's *other* store (codex under `cli_auth_credentials_store = "auto"` creates its
keychain item and deletes `auth.json` on its first save), and every read or write
made through the specs resolved before the child then lands on a store the tool no
longer reads. The restore that follows reconciles the backup's record with today's
declaration rather than trusting either alone: a payload follows the tool into the
store it now reads, a move between non-interchangeable payload shapes is refused,
and a record with no payload is never redirected — that could only delete a
credential kae has no copy of ([DATA-MODEL.md](DATA-MODEL.md) "backup metadata").
`kae add` (the login flow) runs the same child-then-re-resolve sequence.

state.json is untouched: the temporary switch is invisible to the bare `kae`
status summary. `run -i` (the per-account global isolated home, shared with
`kae use -i`) never mutates live state and takes no live-store tool lock; it
holds a shared isolation-lifecycle lock so account rename cannot retire its
path during the child. `run --env` (env-profile vars) takes no lock. Both only
build child environment entries (`internal/cmd/run.go`). Interactive children run through the
`runner.RunInteractive` seam.

## Atomicity And Guards

- JSON pointer patches read the full document, modify only the allowlisted
  pointer, and write via temp-file + `rename` in the same directory. Writes
  always enforce `0600` on credential files, even when the previous file had
  looser permissions.
- Keychain items are captured and restored **verbatim**: the item's bytes
  are stored as-is and written back unchanged through `security -U`. The
  pointer is only a structure guard (the payload must parse as JSON
  containing it). kagikae must not re-serialize the payload — Claude Code
  stores compact, unsorted JSON and rejects a pretty-printed or key-sorted
  payload even when it is semantically identical, reporting "not logged in".
  The write must go through the `security` CLI (not the Security.framework
  API directly): `/usr/bin/security` is in the item's ACL trusted-app list,
  so the owning tool can still read the item without a keychain prompt.
- Structure guards refuse (exit 10) rather than "best effort" write.

## Locking

Advisory `flock`-based locks per tool under the runtime dir. Lock acquisition
is non-blocking; a busy lock fails with `lock_busy` (exit 4) instead of
queueing, because a queued switch could interleave with the other process's
restore step. A separate `config` lock (same mechanism, name `config`) guards
`config.toml` edits; commands that mutate both per-tool state and config
(`account rm`/`rename`) take the tool lock first, then the config lock.

A per-tool reader/writer lock named `isolation-<tool>` guards account-keyed
global-isolation paths. `run -i` takes the shared side before materializing the
home and holds it for the child lifetime, so multiple isolated children may run
together. `use -i` takes the exclusive side for materialize → `state.synced` →
fragment generation, and account rename takes it before the tool and config
locks. Thus rename cannot retire an old path while a child can still refresh it,
and `use -i` cannot recreate the old-name fragment between rename's preflight and
commit. The global acquisition order is isolation lifecycle → tool → config →
state; commands that do not need an outer lock skip it without reversing the
order.

A fourth, `pin-<pin-id>`, serializes the commands that bind one directory
(`kae pin`, `kae pin <tool> <account>`, `kae unpin`): they write the credential,
the companion files and the fragment as separate steps, so two at once in the
same directory could interleave into a fragment pointing at a store the other
re-keyed. It is per directory, so binding two directories at once is unaffected.
`kae relogin` takes the same lock, and holds it across the interactive login it
drives: what must not happen underneath that flow is a re-bind of this directory
overwriting the store the login is writing into. It is the tool's own lock that it
deliberately does **not** take — the store it touches is this directory's, and
blocking every `kae use <tool>` for the length of a human login would be the cost
of covering only the snapshot write its harvest ends with (the same window the
next paragraph describes).
There is deliberately no backup of the previous per-directory credential, and what
makes that safe is the **harvest**: the copy in the store can be newer than the
snapshot (the tool refreshes it in place), so before overwriting or deleting one kae
copies a newer usable copy into the account snapshot — which is what keeps
"re-running `kae pin <tool> <account>` reproduces it" true rather than a claim that
was falsified the moment claude's rotation was measured (docs/CLI.md § kae pin).

**The harvest writes an account snapshot from inside a per-directory lock, which no
tool lock covers.** A concurrent `kae add --no-login <tool> <account>` or a
switch-away recapture of the same account writes the same two files, so the pair can
interleave and the last writer wins. **Do not describe that as losing one of two
equally good payloads** — under single-use rotation at most one copy is refreshable,
so the writer that loses may be the one that had the live credential, leaving the
snapshot holding a copy that cannot refresh while every offline check stays green. The
pin-level pass widens the window rather than narrowing it: one snapshot write per
store per pin, in any bound directory.
It is still recorded rather than locked, for two reasons that are about cost, not
about the loss being small: the state self-heals the next time the directory holding
the live copy is bound (the harvest picks the larger `expiresAt` again), and taking a
tool lock under the pin lock would invert the ordering every other command uses
(tool → config → state), which needs the deadlock analysis that inversion implies
(docs/ROADMAP.md).

What is **not** left to that argument is the shape of the write. The harvest stores
the payload, then re-reads `account.toml` before stamping `captured_at`, rather than
saving the copy it loaded earlier — the same seam rule `App.mutateState` states for
`state.json`, and for the same reason: writing back an older copy would silently
revert artifact records a concurrent `kae add` had just rewritten. The re-read's
*missing* case is the one that mattered in review: `account.Save` starts with
`MkdirAll`, so saving a stale copy after a concurrent `kae account rm` would
**resurrect** an `account.toml` whose payloads are already being deleted — metadata
naming secrets that are not there, which is exactly the class of half-state
`active_orphan` and `secret_missing` exist to report. A snapshot that has gone away
is therefore left gone.

**One file `kae pin` writes falls outside every per-directory lock**, and is meant
to: the ignore rule for the fragment goes in the repository's shared exclude file
(`$GIT_COMMON_DIR/info/exclude` — [CLI.md](CLI.md) § kae pin), which *every
worktree of one repository writes*, while their locks are keyed per directory. It
is therefore an append rather than a locked read-modify-write; the reasoning and
the guarantee are on `ensureGitExcluded`. A lock would be the wrong shape anyway —
it would serialize two independent bindings for a rule that is allowed to fail
outright.

A third lock (name `state`) guards `state.json`, and it is what makes that file
safe to share: the per-tool locks deliberately let `kae use claude <a>` and
`kae use codex <b>` run at the same time, so each held a copy of the whole
document loaded before the other saved, and writing that copy back reverted the
other tool's field with nothing reporting it. Every state write therefore goes
through an `App` state seam: normally `mutateState`, with the synced-and-fragment
variant described below. Each takes this lock, **re-reads the file**, applies
the mutation and saves — the re-read is what makes the update lost-free, the
lock is what makes the re-read atomic. Two consequences for callers: the state
lock is always innermost (isolation lifecycle → tool → config → state) and is held
only for the read plus the write except for that generated-fragment variant, and
**a decision about the state must be made
inside the mutation, not from a copy read earlier** — `kae account rm` re-checks
under the lock whether the account it removes is still the active one, because a
switch can have completed in between. `TestStateWritesGoThroughTheSeam` keeps the
seam from being bypassed from the rest of `internal/cmd`, which is as far as it
globs: `state.Save` stays exported, so nothing else stops a direct write that
compiles, passes review and fails only under concurrency. `kae rollback` does not
cover a mistake here — it restores credentials, not this file.

`state.synced` and the generated global fragment are one logical record, so their
update keeps the state lock through both the atomic state save and fragment
regeneration. They cannot be one filesystem transaction, but this prevents a
concurrent operation from saving newer state and then having an older operation
overwrite the fragment after releasing the lock. `use -i` acquires that state lock
before materializing any home or refreshing any snapshot. If fragment regeneration
returns an error after the state save, kae restores the pre-mutation state while the
lock is still held; each individual write is atomic. A process crash between the two
writes remains a filesystem boundary no rollback handler can run across, so this is
not a claim of cross-file atomicity. Account rename therefore verifies the raw global
fragment against `renderGlobalFragment(state.synced)` under the state lock (an empty
map requires an absent fragment) and refuses an unreadable or mismatched fragment.
The documented `use -s` remedy checks the same invariant and regenerates the fragment
from current state under that lock even when its target has no `synced` entry, closing
the crash state before rename is retried.

## Caching

Commands are short-lived and re-read live state, with two request-scoped
exceptions, both carried in the context and absent unless opted in:

- `keychain.WithReadCache(ctx)` coalesces repeated
  `security find-generic-password` reads of the same **upstream** tool service.
  The switch path uses it so `Detect`, the backup, and the recapture share one
  keychain read instead of three; `WriteItem`/`DeleteItem` invalidate the
  cached service.
- `secret.WithReadCache(ctx)` + `secret.Cached(be)` coalesce reads of **kae's
  own** secret store. The switch path uses it so each target snapshot payload is
  read once — the switch-time stale warning and `applySnapshot` share it instead
  of reading twice; `Set`/`Delete` invalidate the key. (The `Cached` wrapper
  does not forward `Enumerator`, so `doctor` orphan detection uses the raw
  backend.)

Neither cache is ever open **across** a child run (`run -s`, `kae add`'s login
flow), where the child can rotate the live credential behind kae's back and a cached
value would be stale. `run -s` opens the keychain cache once the child has **exited**
and while it still holds the per-tool locks, so its re-resolution, recapture, restore
decision and attribution read one credential and one identity once rather than four
times; `kae rollback` opens one for the whole mutation, where no child runs at all. The
distinction is the child, not the command.

`status` runs each enabled tool's `Detect` concurrently (one goroutine per
tool, reassembled in canonical `constants.Tools` order, output unchanged), so
the most-run command does not pay the sum of the per-tool live probes; a
per-tool `Detect` failure stays a tool warning, not a fatal error.

`internal/freshness` is a pure parser (no IO, no cache): it reads expiry and
refresh-token presence from a captured credential payload and is shared by the
switch-time stale warning and `doctor` credential-health. The "live value vs
stored snapshot" comparison underneath the switch-away recapture
(`valuesDiverge`) and the post-login change check (`loginChangedAuth`) is one
shared helper (`snapshotArtifactDiffers`); each caller keeps its own stored
source and backend-read error policy.

## Known Traps

- JSON pointer patching re-encodes the whole document (sorted keys, 2-space
  indent, `json.Number` for exact numeric round-trip). Sibling values are
  preserved exactly, but byte-level formatting is normalized — never promise
  byte-identical output for patched files.
- `~/.claude.json` can be large and is rewritten by Claude Code itself; always
  re-read immediately before patching inside the lock, never reuse a value
  read earlier in the process.
- macOS `security add-generic-password -U` updates in place but requires the
  same service/account pair. The account is derived from the environment being
  written wherever the spec carries one, rather than read back from the live item —
  the direction kae once had backwards, and which
  `TestKeychainSpecsAreAccountScoped` now refuses for every shipped spec. A rollback
  of a backup whose record predates the account field is the one path that still
  reads the live item, and `internal/artifact/artifact.go` says why.
  [ADAPTERS.md](ADAPTERS.md) § Keyring item contract states the rule in full.
- `secret-tool` returns exit code 1 both for "not found" and some errors;
  treat stderr content as the discriminator.
- Codex `auto` credential store resolves to keyring only when the keyring is
  usable; presence of `auth.json` is the practical signal that file mode is
  in effect.
- `config.toml` is read with BurntSushi/toml (`config.Load`) but **edited**
  with `config.Editor` (github.com/creachadair/tomledit), because BurntSushi
  drops every comment on re-encode. Programmatic config mutations
  (`account rm`/`rename`, `kae profile`) must go through `App.editConfig` /
  the Editor, never a decode-then-encode round-trip, or user comments are
  silently lost. After writing, `editConfig` reloads `app.Config` so the
  in-memory view matches disk.
