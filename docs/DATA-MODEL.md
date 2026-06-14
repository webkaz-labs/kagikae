# Data Model

Config schema, on-disk layout, state, backups, secret references, and status
vocabulary for `kae`.

## Directory Layout (XDG)

`kagikae` itself is XDG-compliant on every platform, including macOS:

| Purpose | Path |
|---------|------|
| config | `${XDG_CONFIG_HOME:-~/.config}/kagikae/config.toml` |
| account snapshots (metadata) | `${XDG_DATA_HOME:-~/.local/share}/kagikae/accounts/<tool>/<account>/account.toml` |
| env profiles (metadata) | `${XDG_DATA_HOME:-~/.local/share}/kagikae/env/<tool>/<account>/env.toml` |
| home-mode tool homes | `${XDG_DATA_HOME:-~/.local/share}/kagikae/homes/<tool>/<account>/` |
| overlay-mode tool homes | `${XDG_DATA_HOME:-~/.local/share}/kagikae/overlays/<tool>/<account>/` |
| bond-mode tool homes | `${XDG_DATA_HOME:-~/.local/share}/kagikae/isolation/<pin-id>/<tool>/bond/` |
| pin-mode config dirs | `${XDG_DATA_HOME:-~/.local/share}/kagikae/isolation/<pin-id>/<tool>/pin/<account>/config/` |
| file-backend secrets (opt-in) | `${XDG_DATA_HOME:-~/.local/share}/kagikae/secrets/...` |
| state | `${XDG_STATE_HOME:-~/.local/state}/kagikae/state.json` |
| backups (metadata) | `${XDG_STATE_HOME:-~/.local/state}/kagikae/backups/<id>.json` |
| locks | `${XDG_RUNTIME_DIR}/kagikae/locks/<tool>.lock`, falling back to `${XDG_STATE_HOME:-~/.local/state}/kagikae/locks/` when `XDG_RUNTIME_DIR` is unset |

Directories holding metadata or secrets are created `0700`; secret and
metadata files are written `0600`. Windows paths are defined in the design
but not implemented in v0.1.0.

## Config Schema

`config.toml`, created by `kae init`:

```toml
version = 1
default_profile = "personal"   # optional

[security]
secret_backend = "auto"        # auto | keychain | libsecret | file
backup_keep = 30               # backups retained per pruning pass

[tools.claude]
enabled = true
# Force the file-patch credential driver (.credentials.json under
# CLAUDE_CONFIG_DIR) even on macOS — the persisted, explicit opt-in
# counterpart to the KAE_CLAUDE_DRIVER=file env var (claude only; the env var
# takes precedence). Only "file" is accepted. Persisting it breaks a real macOS
# login (live claude reads the keychain), so it is for smoke/container use:
# driver = "file"

[tools.codex]
enabled = true

[tools.agy]
enabled = true

[tools.opencode]
enabled = true

[tools.cursor]
enabled = true

[tools.copilot]
enabled = true

# Per tool (any [tools.<tool>] section); both default to true and gate the
# kae run isolation modes and the pin / mise init renderings:
# home_mode_enabled = true
# overlay_mode_enabled = true
# Extra real-home items to share into overlays, on top of the built-in
# allowlist (docs/ADAPTERS.md). Bare file names only; the auth/identity
# artifacts (.credentials.json, .claude.json, auth.json) are refused:
# overlay_extra_shared = ["output-styles"]
# Extra items to exclude from bond-mode symlinking, on top of the built-in
# denylist (claude: .credentials.json; codex: auth.json). Bare file names
# only; the built-in auth artifacts are refused to prevent misconfiguration:
# bond_denylist_extra = ["custom-session.json"]
# Items to share (symlink) from the real home into the pin-mode config dir.
# Default is empty (full isolation). Bare file names only; credential files
# (.credentials.json, auth.json) are refused at config load:
# pin_shared_items = ["settings.json", "CLAUDE.md"]

[profiles.work]
label = "Work"

[profiles.work.accounts]
claude = "work"
codex = "work"

[profiles.personal]
label = "Personal"

[profiles.personal.accounts]
claude = "personal"
codex = "personal"
```

References to removed tools (e.g. `gemini`) load with a warning and are ignored.

Precedence: defaults, then config file, then environment overrides
(secrets/CI only), then CLI flags. Unknown keys produce a warning (not an
error) while the schema is pre-1.0. `version` greater than the supported
schema is an error (`invalid_config`).

A profile may omit tools; `switch all <profile>` switches only the tools the
profile maps and reports the others as `skipped`.

## Account Snapshot Metadata

`accounts/<tool>/<account>/account.toml` holds metadata only — never secret
values:

```toml
version = 1
tool = "claude"
account = "work"
driver = "claude-keychain-patch"
captured_at = 2026-06-11T01:23:45Z

[artifacts.claude_ai_oauth]
kind = "keychain"              # json-pointer | file | keychain
target = "Claude Code-credentials"
pointer = "/claudeAiOauth"
secret_ref = "claude/work/claude_ai_oauth"

[artifacts.oauth_account]
kind = "json-pointer"
target = "~/.claude.json"
pointer = "/oauthAccount"
secret_ref = "claude/work/oauth_account"
```

`kind` semantics:

| Kind | Capture | Apply |
|------|---------|-------|
| `json-pointer` | read pointer value from JSON file | patch pointer in JSON file atomically, preserving all other keys |
| `file` | read whole file | atomic replace, mode `0600` |
| `keychain` | read whole item payload verbatim (pointer guards the shape; an empty pointer marks an opaque non-JSON payload, e.g. a raw token, guarded only as non-empty) | write captured bytes back verbatim via `security -U`; absent value deletes the item |

## Secret References

Secret payloads live in the secret backend, keyed by:

```text
service: kagikae
key:     <tool>/<account>/<artifact>          # account snapshots
key:     backup/<backup-id>/<tool>/<artifact> # backups
```

Backends:

| Backend | Platform | Mechanism |
|---------|----------|-----------|
| `keychain` | macOS | `security` CLI generic passwords (via runner) |
| `libsecret` | Linux | `secret-tool` (via runner) |
| `file` | any (opt-in) | plaintext JSON under `data/secrets/`, file mode `0600` |

`auto` resolves to `keychain` on macOS, `libsecret` on Linux when
`secret-tool` is available, otherwise the command fails with exit code 9 and
guidance to either install libsecret tools or opt in to the file backend with
`secret_backend = "file"`.

## State

`state.json`:

```json
{
  "schema_version": 1,
  "active_profile": "work",
  "active": {"claude": "work", "codex": "work"},
  "updated_at": "2026-06-11T01:23:45Z"
}
```

`active` records what kae last applied (or captured from a matching live
state); it is kae's belief, not upstream truth. `status` re-verifies
`auth_present` against the live state. `active_profile` is set by
`switch all <profile>` and cleared when a single-tool switch makes the active
set diverge from that profile's mapping. `kae apply` decides its no-op by
comparing the target profile against `active` (belief only — external drift
is neither verified nor repaired).

## Backups

Before any live mutation, `switch`, `rollback`, `run` (auth mode), and
`login` capture the current live artifacts into a backup (`reason` is
`"switch"`, `"rollback"`, `"run"`, or `"login"`), so every mutation is
reversible:

- metadata: `backups/<id>.json` (id format `YYYYMMDDTHHMMSSZ`, suffixed
  `-2`, `-3`, ... on collision)
- payloads: secret backend under `backup/<id>/...`

```json
{
  "schema_version": 1,
  "id": "20260611T012345Z",
  "created_at": "2026-06-11T01:23:45Z",
  "reason": "switch",
  "tools": ["claude"],
  "active_before": {"claude": "personal"},
  "artifacts": [
    {"tool": "claude", "name": "claude_ai_oauth", "kind": "keychain",
     "target": "Claude Code-credentials", "pointer": "/claudeAiOauth",
     "keychain_account": "alice",
     "secret_ref": "backup/20260611T012345Z/claude/claude_ai_oauth",
     "present": true}
  ]
}
```

`keychain_account` and `jsonc` are optional restore-fidelity fields: the
first recreates a deleted keychain item under the tool's own account (e.g.
`cursor-user`) instead of the generic fallback; the second routes a JSONC
target (e.g. Copilot's commented `config.json`) through the
comment-preserving patch on restore instead of the plain-JSON path, which
would reject the leading `//` comments. Both are omitted for artifacts that
do not need them and are absent in backups written before the field existed.

`present: false` records that the artifact did not exist live (so rollback
removes/skips it instead of writing an empty value). After a successful
switch, backups beyond `backup_keep` are pruned oldest-first (metadata and
secret payloads together).

**`account rm`/`rename` do not rewrite existing backups.** A backup's
`active_before` keeps the old account name, so rolling back to a backup taken
before a remove/rename restores that name into `state.json` while the snapshot
no longer exists under it; the next `kae use`/`apply` then errors with "account
not captured". Prune the affected backups manually if this matters.

## Status Vocabulary

Defined in `internal/constants`; JSON uses exactly these tokens:

- check status: `ok`, `warn`, `error`, `skipped`
- error codes: `ok`, `error`, `invalid_config`, `auth_missing`, `lock_busy`,
  `unsupported`, `cli_missing`, `not_found`, `permission`, `secret_store`,
  `unsafe_refused`, `auth_unchanged`, `usage`
- artifact kinds: `json-pointer`, `file`, `keychain`
- drivers: `claude-file-patch`, `claude-keychain-patch`, `codex-auth-json`,
  `agy-file-snapshot`, `opencode-file-patch`, `cursor-keychain`,
  `copilot-config-pointer`
- modes: `auth`, `env`, `home`, `overlay`, `bond`
- backup reasons: `switch`, `rollback`, `run`, `login`

## Env Profiles

`env/<tool>/<account>/env.toml` holds variable **names** only:

```toml
version = 1
tool = "claude"
account = "ci"
updated_at = 2026-06-11T01:23:45Z
vars = ["ANTHROPIC_API_KEY"]
```

Values live in the secret backend under `env/<tool>/<account>/<VAR>`.
Variable names must match `[A-Z_][A-Z0-9_]{0,127}`.
