# Architecture

Package layout, layering, and implementation boundaries for `kae`.
The repository follows the bundled Go CLI standard
(`.claude/skills/go-cli-tooling/references/`).

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
  so candidate lists never drift from the real surface. Read-only, no locks; its
  line-oriented output is an internal contract, not the JSON contract.

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

`Env` carries the resolved home directory, OS, environment lookups, and the
live base paths (honoring `CLAUDE_CONFIG_DIR` / `CODEX_HOME` when already
set). Capture, apply, verify, backup, and rollback are generic operations in
`internal/account` semantics implemented by `cmd` + `artifact` over the
artifact specs, so every adapter gets locking, backups, dry-run, and
redaction identically.

Adapters may implement optional capability interfaces, type-asserted by `cmd`
(the same pattern as `secret.Enumerator`):

- `Identifier` (`Identity(ctx, env) (string, error)`) reads the live login
  identity so `kae add <tool>` can default the account name and record it in the
  snapshot. A tool without a readable identity (agy) does not implement it, and
  `cmd` falls back to requiring an explicit name.
- `Fresher` (`Freshness(payload) freshness.Info`) reads a captured credential's
  expiry and refresh-token presence for the switch-time stale warning and
  `doctor credential_stale`. `cmd.freshnessOf` dispatches to it; a tool with no
  datable credential (copilot pointer, agy blob) omits it and is treated as
  not-datable. `internal/freshness` holds only the shared parsing primitives —
  no per-tool knowledge — so it stays a leaf package (no `adapter` import).

See [ADAPTERS.md](ADAPTERS.md).

Adapters return structured refusals (`unsafe_refused`, `unsupported`,
`auth_missing`) instead of writing when the live layout is unrecognized; the
normative allowlists live in [ADAPTERS.md](ADAPTERS.md).

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
8. update state.json; prune old backups
9. release locks
```

The whole switch wraps `ctx` in `keychain.WithReadCache`, so the `security`
reads steps 3–6 make of one tool's account-agnostic keychain service collapse
to a single invocation (the recapture in step 5 adds no extra read or auth
prompt); writes in step 6 invalidate the cache. No child runs during a switch,
so the cache never serves a stale live credential (`run -s` does not use it).

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
-> child runs with inherited stdio -> recapture refreshed credentials into
the account snapshots -> restore the backup -> prune -> unlock
```

state.json is untouched: the temporary switch is invisible to the bare `kae`
status summary. `run -i` (the per-account global isolated home, shared with
`kae use -i`) and `run --env` (env-profile vars) never mutate live state and
take no lock; they only build child environment entries
(`internal/cmd/run.go`). Interactive children run through the
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

Neither cache is used across a child run (`run -s`), where the child can rotate
the live credential behind kae's back and a cached value would be stale.

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
  same service/account pair; the Claude keychain item account name is the
  local username and must be read from the existing item, not assumed.
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
