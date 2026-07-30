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
| companion generated files | `${XDG_DATA_HOME:-~/.local/share}/kagikae/companion/<profile>/<id>/config` (git-config kind only; token and config-dir kinds generate no file) |
| per-dir shared (`pin -s`) homes | `${XDG_DATA_HOME:-~/.local/share}/kagikae/isolation/<pin-id>/<tool>/shared/` |
| per-dir isolated (`pin -i`) config dirs | `${XDG_DATA_HOME:-~/.local/share}/kagikae/isolation/<pin-id>/<tool>/isolated/<account>/config/` |
| global-isolated (`use -i` / `run -i`) homes | `${XDG_DATA_HOME:-~/.local/share}/kagikae/isolation/global/<tool>/<account>/` (a kae-owned mise fragment points `CLAUDE_CONFIG_DIR` / `CODEX_HOME` here; the real `~/.<tool>` is never touched) |
| file-backend secrets (opt-in) | `${XDG_DATA_HOME:-~/.local/share}/kagikae/secrets/...` |
| state | `${XDG_STATE_HOME:-~/.local/state}/kagikae/state.json` |
| backups (metadata) | `${XDG_STATE_HOME:-~/.local/state}/kagikae/backups/<id>.json` |
| locks | `${XDG_RUNTIME_DIR}/kagikae/locks/<tool>.lock`, falling back to `${XDG_STATE_HOME:-~/.local/state}/kagikae/locks/` when `XDG_RUNTIME_DIR` is unset |
| completion script (`completion --install`, default) | bash: `${XDG_DATA_HOME:-~/.local/share}/bash-completion/completions/kae`; zsh: the first existing user `fpath` dir (`${XDG_CONFIG_HOME:-~/.config}/zsh/completions`, `~/.zsh/completions`, `~/.zfunc`), else `${XDG_DATA_HOME:-~/.local/share}/zsh/site-functions/_kae`; fish: `${XDG_CONFIG_HOME:-~/.config}/fish/completions/kae.fish` (the dynamic script; calls `kae __complete` at completion time) |
| completion mise hook (`completion --install`, opt-in) | kagikae marker block in the global mise config (`$MISE_CONFIG_DIR/config.toml`, else `${XDG_CONFIG_HOME:-~/.config}/mise/config.toml`) carrying a `[hooks.enter]` that sources `kae completion <shell>`; refused if a foreign `[hooks.enter]` already exists |

Directories holding metadata or secrets are created `0700`; secret and
metadata files are written `0600`. Windows paths are defined in the design
but not implemented in v0.1.0.

## Config Schema

`config.toml`, created by `kae init`:

```toml
version = 1
default_profile = "side"   # optional

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

# Per tool (any [tools.<tool>] section):
# Extra items to exclude from per-directory shared-bind symlinking
# (kae pin -s), on top of the built-in denylist
# (claude: .credentials.json; codex: auth.json). Bare file names only;
# the built-in auth artifacts are refused to prevent misconfiguration:
# shared_denylist_extra = ["custom-session.json"]
# Items to share (symlink) from the real home into the per-directory
# isolated-bind config dir (kae pin -i). Default is empty (full isolation).
# Bare file names only; credential files
# (.credentials.json, auth.json) are refused at config load:
# isolated_shared_items = ["settings.json", "CLAUDE.md"]

[profiles.main]
label = "Main"

[profiles.main.accounts]
claude = "main"
codex = "main"

[profiles.side]
label = "Side"

[profiles.side.accounts]
claude = "side"
codex = "side"

# Optional: bind companion-tool auth (git/gh/cloud CLIs) to this profile so an
# agent and the tools it shells out to act under the same account. Managed with
# `kae companion add|rm|list`; delivered per-directory by `kae pin`. See
# docs/ADAPTERS-COMPANION.md.
[profiles.main.companions]
git.email = "you@example.com"     # non-secret knobs are stored inline
git.name = "Your Name"
gh.GH_TOKEN = ""                  # token marker: the value lives in the secret backend
gh.expected_login = "octocat"     # recorded at add time; the login the token must resolve to
kubectl.KUBECONFIG = "~/.kube/main-config"  # config-dir path (non-secret)
```

References to removed tools (e.g. `gemini`) load with a warning and are ignored.

**v0.8.0 key renames (pre-1.0 hard break):** The old per-tool keys
`bond_denylist_extra`, `pin_shared_items`, `overlay_extra_shared`,
`overlay_mode_enabled`, and `home_mode_enabled` are not accepted. Config load
errors naming the replacement:

| Old key | Replacement |
|---------|-------------|
| `bond_denylist_extra` | `shared_denylist_extra` |
| `pin_shared_items` | `isolated_shared_items` |
| `overlay_extra_shared` | *(removed — overlay mode gone; use `kae pin -s|-i`)* |
| `overlay_mode_enabled` | *(removed — overlay mode gone)* |
| `home_mode_enabled` | *(removed — home mode gone; use `kae use -i` / `kae pin -i`)* |

The surviving per-tool keys are: `enabled`, `shared_denylist_extra`,
`isolated_shared_items`, `driver` (claude only).

Precedence: defaults, then config file, then environment overrides
(secrets/CI only), then CLI flags. Unknown keys produce a warning (not an
error) while the schema is pre-1.0. `version` greater than the supported
schema is an error (`invalid_config`).

A profile may omit tools; `switch all <profile>` switches only the tools the
profile maps and reports the others as `skipped`.

`[profiles.<name>.companions]` is an additive table (schema 1; older kae
tolerates it as an unknown key) mapping a companion id (`git`, `gh`,
`cloudflare`, `kubectl`) to its knob values. Non-secret knobs (git identity
fields, config-dir paths) hold their value inline; a token knob holds an empty
string marker and its value lives in the secret backend. A token companion may
also carry a reserved non-secret `expected_login` knob: kae records it at
`kae companion add` time (from the token's live login, via the companion's login
probe) and `doctor`'s `companion_token_drift` check compares the live login
against it. It is not user-settable. Companion ids and knob names are validated;
a value may not contain a newline or NUL. The switched/preserved contract per
companion is [ADAPTERS-COMPANION.md](ADAPTERS-COMPANION.md).

## Account Snapshot Metadata

`accounts/<tool>/<account>/account.toml` holds metadata only — never secret
values:

```toml
version = 1
tool = "claude"
account = "main"
driver = "claude-keychain-patch"
identity = "you@example.com"  # optional: the raw detected login identity (§D)
captured_at = 2026-06-11T01:23:45Z

[artifacts.claude_ai_oauth]
kind = "keychain"              # json-pointer | file | keychain
# The recorded target is the name resolved at capture time, not a constant:
# claude namespaces its keychain service by CLAUDE_CONFIG_DIR, so a capture taken
# inside a bound directory records `Claude Code-credentials-<sha8>`
# (docs/ADAPTERS.md "Credential storage resolution"). Applying resolves the spec
# fresh from the adapter for the environment it is applying to; this field
# records what was read.
target = "Claude Code-credentials"
pointer = "/claudeAiOauth"
secret_ref = "claude/main/claude_ai_oauth"

[artifacts.oauth_account]
kind = "json-pointer"
target = "~/.claude.json"
pointer = "/oauthAccount"
secret_ref = "claude/main/oauth_account"
```

`[artifacts.oauth_account]` (claude only) is claude's identity cache, not a
credential — it is switched so the UI and `kae add` report the applied account
([ADAPTERS.md](ADAPTERS.md)). It is the one artifact the adapter declares
**identity-only**: a snapshot captured before it existed simply has no such
table, and applying that snapshot removes `/oauthAccount` live instead of
refusing the switch. That property is not recorded here — it is policy, read from
today's adapter, so an old snapshot cannot pin a decision kae has since changed.
Its payload is identity metadata (email, account/org uuid, plan fields), stored
in the secret backend like every other artifact payload so no code path
special-cases it; it is PII and is never printed, exactly like `identity` below.

`identity` (optional, v0.8.3 §D) is the raw login identity detected at capture
(an email or account id), separate from the sanitized account `account` name —
it disambiguates accounts whose identities sanitize to the same name. It is PII
but **not** a secret (plaintext metadata, exactly like the account name; never a
token). Every tool now exposes an identity (agy reads the active Google account
from `~/.gemini/google_accounts.json` as of v0.8.7); it is still best-effort:
blank for a detection failure and for any snapshot captured before its tool
gained identity. Auto-detection is not always possible — on current Antigravity
agy's account is resolved server-side from an opaque token and never written to
disk (docs/ADAPTERS.md) — so it is settable explicitly (v0.9.1): `kae add
--identity <value>` at capture, or `kae account set-identity <tool> <account>
<value>` to backfill without re-capturing the credential. `kae ls` /
`kae accounts` / `kae status` show it (an `Identity` column; an additive
`identity` field in `--json`, `omitempty`, `schema_version` still `1`).

A `keychain` artifact may carry `keychain_account`: which item of the service the
payload came from (claude `$USER`, cursor `cursor-user`, codex keyring's
`cli|<16 hex of sha256(CODEX_HOME)>`). It is **diagnostic only** — omitted for
non-keychain artifacts, and deliberately *not* used on apply, because where a
keychain item lives is the adapter's answer for the environment being written,
while this is the answer for the environment the snapshot was captured in.
Applying the recorded one is how a snapshot taken under one `CODEX_HOME` would
write the item of another.

`kind` semantics:

| Kind | Capture | Apply |
|------|---------|-------|
| `json-pointer` | read pointer value from JSON file | patch pointer in JSON file atomically, preserving all other keys |
| `file` | read whole file | atomic replace, mode `0600` |
| `keychain` | read whole item payload verbatim (pointer guards the shape; an empty pointer marks an opaque non-JSON payload, e.g. a raw token, guarded as non-empty **and single-line**) | write captured bytes back verbatim via `security -U`; absent value deletes the item. An item identified by service **and** account (`KeychainMatchAccount`) scopes read/write/delete to that account (`-a`) so a sibling item is never touched — used where the service holds more than one legitimate item: shared with another tool (agy's `gemini`/`antigravity`) or one item per tool home (codex's `Codex Auth`, account derived from `CODEX_HOME`). No path deletes by service name alone before writing; that is what destroyed another codex home's login |

A snapshot is rewritten by `kae add`, `run -s`'s post-child recapture, and (new
in v0.8.1) `kae use`/bare `use`'s switch-away recapture of the currently-active
account when its live credential diverges from the snapshot. The snapshot's
credential expiry, refresh-token state, and explicit invalidation are read (never
stored separately) for the switch-time stale warning and the `doctor`
`credential_stale` check. The per-tool reader is the adapter's
`Freshness(payload)` capability (v0.8.3 §A), built from the shared primitives in
`internal/freshness`: claude `claudeAiOauth.expiresAt` (Unix ms) +
`refreshToken` + `refreshTokenExpiresAt` (Unix ms), codex
`tokens.access_token`/`id_token` JWT `exp` + `refresh_token`, opencode `/openai`
`expires` (Unix ms) + `refresh`, cursor's opaque JWT `exp` (no refresh token).
copilot's `/lastLoggedInUser` and agy's encrypted blob carry no datable token
(no `Freshness` method), so they are never flagged.

A refresh token has its own lifetime — Claude Code's is measured in **days**, and
upstream warns when under three remain — so "a refresh token is present" is not
"recoverable". Where the payload publishes a refresh expiry, kae uses it; where it
does not, presence is all there is. claude also **tombstones** a credential whose
refresh failed (blank `accessToken`/`refreshToken`, `expiresAt: 0`): the adapter
reports that as invalid rather than as "no expiry recorded", which is what the
bytes literally say. Deciding which bytes mean that is per-tool knowledge and
lives in the adapter; `internal/freshness` stays a pure parser.

## Secret References

Secret payloads live in the secret backend, keyed by:

```text
service: kagikae
key:     <tool>/<account>/<artifact>          # account snapshots
key:     backup/<backup-id>/<tool>/<artifact> # backups
key:     env/<tool>/<account>/<VAR>           # env-profile variables
key:     companion/<profile>/<id>/<knob>      # companion token knobs
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
  "active_profile": "main",
  "active": {"claude": "main", "codex": "main"},
  "synced": {"claude": "main"},
  "updated_at": "2026-06-11T01:23:45Z"
}
```

`active` records what kae last applied (or captured from a matching live
state); it is kae's belief, not upstream truth. `status` re-verifies
`auth_present` against the live state. `active_profile` is set by a
profile-wide `use` and cleared when a single-tool switch makes the active set
diverge from that profile's mapping. Bare `kae use` (no positional, idempotent
apply) decides its no-op by comparing the target profile against `active`
(belief only — external drift is neither verified nor repaired).

`synced` records, per tool, the account whose private home the **global** mise
fragment (`~/.config/mise/conf.d/kagikae.toml`) currently points the tool at
(global isolated, `kae use -i` / `kae run -i`). kae regenerates that
kae-owned fragment from `synced`; it is absent/empty when no tool is globally
isolated. `kae use -s` clears the tool's entry and regenerates or deletes the
fragment. The real `~/.<tool>` is never modified. `kae status` surfaces
`synced` as a `global_isolated` array of `{tool, account, home}` so the shared
state between `use -i` and `run -i` is always visible.

## Backups

Before any live mutation, `switch`, `rollback`, `run -s` (real-home mode), and
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
  "active_before": {"claude": "side"},
  "artifacts": [
    {"tool": "claude", "name": "claude_ai_oauth", "kind": "keychain",
     "target": "Claude Code-credentials", "pointer": "/claudeAiOauth",
     "keychain_account": "main",
     "secret_ref": "backup/20260611T012345Z/claude/claude_ai_oauth",
     "present": true},
    {"tool": "claude", "name": "oauth_account", "kind": "json-pointer",
     "target": "~/.claude.json", "pointer": "/oauthAccount",
     "secret_ref": "backup/20260611T012345Z/claude/oauth_account",
     "present": true}
  ]
}
```

`keychain_account`, `keychain_replace`, `keychain_match_account`, and `jsonc` are
optional restore-fidelity fields: `keychain_account` recreates a deleted
keychain item under the tool's own account (e.g. `cursor-user`, or codex
keyring's derived `cli|<16 hex>`) instead of the generic fallback;
`keychain_match_account` marks an item identified by service **and** account,
because the service holds more than one legitimate item — a service shared with
another tool (agy's `gemini`/`antigravity`) or one item per tool home (codex's
`Codex Auth`) — so a rollback scopes its read/write/delete to that account and
never touches a sibling; `keychain_replace` is **legacy and no longer written**
(it meant "delete every item of the service, then write", which destroyed another
`CODEX_HOME`'s codex login), and a record carrying it restores as
`keychain_match_account`; `jsonc`
routes a JSONC target (e.g. Copilot's commented `config.json`) through
the comment-preserving patch on restore instead of the plain-JSON path, which
would reject the leading `//` comments. All are omitted for artifacts that do not
need them and are absent in backups written before the field existed.

Every field here is a **fact about the captured artifact**, needed to write it
back. Restore *policy* is deliberately not recorded: whether losing a payload is
survivable comes from today's adapter spec (`IdentityOnly`), and so does which of
its keys identify the account (`IdentityKeys`), so an old backup cannot pin a
decision kae has since changed. That is also why a rollback can
clear an identity-only artifact the backup has no record of at all — it cannot be
restored, and a stale identity cache would mislabel the restored account.

`present: false` records that the artifact did not exist live (so rollback
removes/skips it instead of writing an empty value). After a successful
switch, backups beyond `backup_keep` are pruned oldest-first (metadata and
secret payloads together).

A record carries the store its payload came from, and a tool can move its
credential between stores while kae is not looking: codex under
`cli_auth_credentials_store = "auto"` creates its keychain item and deletes
`auth.json` on its first save, which a `kae run -s` child or a `kae add` login flow
is enough to trigger. A restore therefore lets the **payload follow the tool** — a
whole-document payload is written through today's spec, the same bytes in either
store — instead of writing the recorded store, which would put the credential where
nothing reads it while kae reported success. A move between shapes that are *not*
interchangeable (a whole document and a JSON pointer value) cannot be redirected
and is refused with exit `10`, exactly as the equivalent snapshot transition is.

**A redirect only ever writes; it never deletes.** An absent record restores as
"logged out", and redirecting that would delete the store the tool moved to — a
credential the backup has no copy of, and on the paths that most need the redirect,
one nothing else has a copy of either (a login flow that succeeded and then failed
before kae captured it). Such a record keeps the recorded store, so the abandoned
one is removed and the live one is left alone, with a warning on stderr that the
restore was partial. Leaving a credential kae cannot account for is recoverable;
deleting it is not.

Two further supports back the write case: the flows that run a child re-resolve
their specs afterwards, so the credential the child left behind is captured before
anything overwrites it, and a rollback's pre-rollback backup resolves **today's**
specs, so what it overwrites is what it backed up.

A legacy `keychain_replace` record with **no** recorded account is refused
outright: without the account it cannot name its own item, and widening the delete
to the whole service is what destroyed another codex home's login.

**`account rm`/`rename` do not rewrite existing backups.** A backup's
`active_before` keeps the old account name, so rolling back to a backup taken
before a remove/rename restores that name into `state.json` while the snapshot
no longer exists under it; the next `kae use` then errors with "account not
captured". Prune the affected backups manually if this matters.

## Status Vocabulary

Defined in `internal/constants`; JSON uses exactly these tokens:

- check status: `ok`, `warn`, `error`, `skipped`
- error codes: `ok`, `error`, `invalid_config`, `auth_missing`, `lock_busy`,
  `unsupported`, `cli_missing`, `not_found`, `permission`, `secret_store`,
  `unsafe_refused`, `auth_unchanged`, `usage`
- artifact kinds: `json-pointer`, `file`, `keychain`
- drivers: `claude-file-patch`, `claude-keychain-patch`, `codex-auth-json`,
  `codex-keyring`, `agy-keychain`, `agy-file-snapshot`, `opencode-file-patch`,
  `cursor-keychain`, `copilot-config-pointer`
- internal mechanisms: `auth`, `env`, `shared`, `isolated`, `sync`
  (`shared`/`isolated` back per-dir `pin -s`/`-i`; `sync` is the
  global-isolated mechanism behind `kae use -i` / `kae run -i`, delivered as a
  kae-owned mise fragment)
- status `pinned.mode` (user-facing environment): `shared`, `isolated`, `auth`
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
