# Security

`kae` reads, stores, and writes live credentials for other tools. Safety rules
here are part of the command contract.

## Mutation Safety Rules (mandatory)

- Never replace a tool home or a mixed-state file wholesale. Mixed-state files
  (`~/.claude.json`) are patched only through the JSON Pointer allowlist
  defined in [ADAPTERS.md](ADAPTERS.md).
- Never delete unknown keys; preserve everything outside the allowlist.
- Back up the live artifacts before every write; rollback must always be
  possible (`kae rollback`).
- Hold the per-tool lock for the entire read-modify-write window.
- All file writes are atomic (temp file + rename, same directory) and set
  mode `0600` for credential files.
- Validate structure before writing; refuse with `unsafe_refused` (exit 10)
  when the live layout is unrecognized.
- Support `--dry-run` on every mutating command.

## Secret Handling

- Secret values never enter stdout, stderr, logs, JSON reports, error
  messages, or metadata files. Reports reference artifacts by name, kind,
  target path, and pointer only.
- **One documented exception** to the stdout rule: the hidden
  `kae __companion-token <profile> <id> <knob>` credential helper prints a
  single companion token to stdout. It is a git-credential-helper-style seam
  invoked **only** by the mise `exec()` template a companion binding writes
  (see [ADAPTERS-COMPANION.md](ADAPTERS-COMPANION.md)); it resolves the value
  from the secret backend at environment-evaluation time so the token is never
  written to disk in the fragment. It is never reached on a human or JSON
  reporting path, and the token's env var is added to the fragment's mise
  `redactions` so task logs mask it. `kae companion list` shows knob names and
  non-secret values only; token values are never printed.
- Account snapshot payloads and backup payloads are stored in the secret
  backend (OS credential store by default; see
  [DATA-MODEL.md](DATA-MODEL.md#secret-references)).
- The plaintext `file` backend requires explicit
  `security.secret_backend = "file"` in config. It writes `0600` files under
  a `0700` directory and `doctor` permanently warns while it is active.
- `kae` never stores secrets in TOML and never echoes captured values back
  for confirmation.
- The `kae status` `global_isolated` field and `run -i`'s home-path message
  contain only directory paths and account names — never secret values.
- The detected login `identity` (v0.8.3 §D — an email or account id stored in
  `account.toml` and shown by `kae ls`/`kae accounts`) is **PII but not a
  secret**: it is plaintext metadata exactly like the account name, never a
  token. It is read from already-trusted live state and never derived from a
  credential value.
- The codex keyring payload (the `Codex Auth` keychain item, v0.8.3 §C) **is** a
  credential and is treated like every other secret: captured verbatim into the
  secret backend, never written to stdout/JSON/logs/metadata; only the item's
  opaque account id (`cli|<opaque>`, not a secret) is recorded in `account.toml`.
- The agy keychain payload (the `gemini`/`antigravity` item, v0.8.6 §A) **is** a
  credential — an opaque ~686-byte token — and is treated identically: captured
  verbatim into the secret backend, never written to stdout/JSON/logs/metadata.
  Its account attribute is the fixed literal `antigravity` (not a secret, not
  recorded as captured state); kae matches by service **and** account so it never
  reads or writes a `gemini` item belonging to another tool.

### Secret enumeration (v0.8.1 `secret_orphan`)

The base `Backend` interface is get/set/delete by key only. `secret.Enumerator`
(optional, `Keys(ctx)`) adds listing, used by the `doctor` `secret_orphan`
check (a secret item with no matching `accounts/<tool>/<account>` snapshot dir):

- **darwin keychain: cannot enumerate via the `security` CLI**, so
  `keychainBackend` does **not** implement `Enumerator` and the orphan check is
  silently skipped there. `security find-generic-password -s kagikae` returns
  only the **first** matching item; `security dump-keychain` dumps the entire
  login keychain, prompts per item, and is brittle — not a stable path.
- **`file`** (`readdir` over `*.secret`) and **Linux `libsecret`**
  (`secret-tool search --all`, parsing `attribute.key` only) implement
  `Enumerator`, so the check runs there. The libsecret search output also
  carries `secret = ...` lines; only `attribute.key` is read and the raw output
  is never logged, so no secret value leaks.

`kae account rm` deletes the snapshot dir and every secret item together, so
orphans are rare; the check catches leftovers from manual cleanup.

### Per-switch `security` read coalescing (v0.8.1)

A single switch reads one tool's keychain service several times (`Detect`, the
backup, and the switch-away recapture). `keychain` provides a context-scoped
read cache (`WithReadCache`) wired into the switch path so those collapse to one
`security` invocation (and at most one auth prompt); writes invalidate the
cached service. Most drivers match by service alone, so the cache is keyed by
service; agy's account-scoped match (`gemini`/`antigravity`) keys the cache by
service **and** account so a shared service stays correctly partitioned. The
cache is per-command and never spans a child process run (`run -s`), where the
child could rotate the live credential unseen — a cached value would be stale.

## Subprocesses

- `security`, `secret-tool`, and binary detection run through
  `internal/runner` with `exec.CommandContext` and argv arrays (no shell
  strings).
- Keychain payloads are passed to `security` via argv: the security CLI has
  no non-interactive stdin password input. This is an accepted, documented
  trade-off — on macOS, another user cannot read a process's argv
  (`ps -E`/`ps -ww` show arguments only for the same user or root), and any
  same-user process could read the keychain through `security` anyway, so
  argv exposure grants no privilege beyond what the keychain itself grants.
  stdout of `security find-generic-password -w` is treated as secret and
  redacted from any diagnostics.
- User-controlled account/profile names are validated against
  `[a-zA-Z0-9._-]{1,64}` before use in paths, lock names, or secret keys.

## File Permissions

- `~/.claude/.credentials.json`, `~/.codex/auth.json`,
  `~/.local/share/opencode/auth.json`, and agy credential files under
  `~/.gemini/antigravity-cli/` are written `0600`; kagikae metadata/state
  dirs `0700`. `~/.copilot/config.json` is owned by copilot (kae only patches
  its `/lastLoggedInUser` pointer); `doctor` warns if it is not `0600`.
- `doctor` warns when live credential files are group/world readable.
- Isolated homes (`isolation/global/<tool>/<account>/`) are created `0700`
  and treated as credential-bearing. Credential files within them (e.g.
  `.credentials.json`, `auth.json`) are written `0600`. The real
  `~/.claude`/`~/.codex` and the live keychain are never touched by isolation.

## Environment Conflicts

`doctor` warns when environment variables override the subscription login the
user thinks they are switching: `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`,
`CLAUDE_CODE_OAUTH_TOKEN`, `GEMINI_API_KEY`, `GOOGLE_APPLICATION_CREDENTIALS`,
`COPILOT_GITHUB_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`.

## Concurrency

`kae use` (shared mode, `-s`) and `kae run -s` mutate shared live state.
Per-tool locks serialize kae against itself, but cannot stop the upstream CLI
from refreshing tokens concurrently. Therefore:

- locks are held across the whole switch transaction (`use -s`), and across
  the entire child run for `kae run -s`;
- simultaneous different accounts for one tool are unsupported in shared
  mode (documented; isolated mode via `use -i` / `run -i` is the supported
  path for parallel sessions);
- `kae run -s` recaptures refreshed credentials into the account snapshot
  before restoring the previous state, so token refreshes during the child
  run are never lost.
- `kae use -s` / bare `use` recapture the currently-active account before
  switching away when its live credential diverges from the snapshot, so a
  token rotated in-tool is not silently lost on the next switch back. This is
  best-effort and divergence-gated — a logged-out account is left untouched
  with a warning. It cannot track a refresh-token rotation that happens entirely
  outside kae; that case surfaces as the `credential_stale` warning, not a
  silent repair.

`kae run -i` operates in the global isolated home (`isolation/global/<tool>/<account>/`)
with no lock and no live mutation. It is safe to run concurrently with
`kae use` in other terminals — the real home is never touched.

## Isolation Safety

Three isolation scopes exist; their credential boundaries are:

| Scope | Command | Credential store | Live home touched? |
|-------|---------|------------------|--------------------|
| Global isolated | `use -i` / `run -i` | `isolation/global/<tool>/<account>/` | No |
| Per-directory shared | `pin -s` | `isolation/<pin-id>/<tool>/shared/` (symlinks to the real home, credential private-copied) | No (symlink source only) |
| Per-directory isolated | `pin -i` | `isolation/<pin-id>/<tool>/isolated/<account>/config/` | No |

The hard-coded credential denylist for shared binds (enforced at config load)
refuses adding credential files (`.credentials.json`, `auth.json`, etc.) to
the `shared_denylist_extra` or `isolated_shared_items` config keys. This
prevents accidentally leaking credentials across directories.

## Env Profiles And kae run

- `kae env set ... KEY=VALUE` receives the value via argv, which also lands
  in shell history. For secrets prefer the stdin form
  (`kae env set <tool> <account> KEY < file`, or piped).
- Profile metadata stores variable names only; values live in the secret
  backend and are injected solely into the child process environment of
  `kae run --env`. `kae env list` never prints values.
- `kae add` (login flow) and `kae run` launch upstream CLIs, and `kae edit`
  launches `$VISUAL`/`$EDITOR`, all with inherited stdio; kae passes no
  secrets on their command lines.

## External Tools

| Tool | Use | Trust boundary |
|------|-----|----------------|
| `security` (macOS) | keychain read/write | output of `-w` is secret |
| `secret-tool` (Linux) | libsecret read/write | stdin used for store; output of lookup is secret |
| upstream CLIs | binary presence detection only in v0.1.0 | never invoked with credentials |
