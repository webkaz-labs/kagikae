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
      gemini/
      agy/
    artifact/             # artifact primitives: json-pointer / file / keychain
    config/               # TOML config parse/validate/defaults
    constants/            # JSON contract vocabulary (status, codes, drivers)
    paths/                # XDG resolution for config/data/state/locks
    secret/               # secret backend interface + keychain/libsecret/file
    patch/                # JSON Pointer get/set + atomic file writes
    lock/                 # per-tool advisory file locks
    backup/               # backup create/list/prune/restore
    state/                # state.json load/save
    runner/               # subprocess seam (template standard)
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

Adapters return structured refusals (`unsafe_refused`, `unsupported`,
`auth_missing`) instead of writing when the live layout is unrecognized; the
normative allowlists live in [ADAPTERS.md](ADAPTERS.md).

## Switch Transaction

```text
1. resolve tool/account (or profile -> per-tool accounts)
2. acquire per-tool locks (all requested tools; fail fast with lock_busy)
3. detect live state; build artifact specs per tool
4. create one backup covering all tools about to change
5. apply artifacts per tool (atomic writes / keychain updates)
6. on any failure: restore the backup for already-applied tools, report
7. update state.json; prune old backups
8. release locks
```

`--dry-run` runs steps 1–3 and prints the plan from the artifact specs.

## Atomicity And Guards

- JSON pointer patches read the full document, modify only the allowlisted
  pointer, and write via temp-file + `rename` in the same directory,
  preserving original file mode (default `0600` for new credential files).
- Keychain patches read the payload, guard that it parses as the expected
  JSON shape, patch the pointer, and write back through `security -U`.
- Structure guards refuse (exit 10) rather than "best effort" write.

## Locking

Advisory `flock`-based locks per tool under the runtime dir. Lock acquisition
is non-blocking; a busy lock fails with `lock_busy` (exit 4) instead of
queueing, because a queued switch could interleave with the other process's
restore step.

## Caching

None in v0.1.0. Commands are short-lived and each re-reads live state.

## Known Traps

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
