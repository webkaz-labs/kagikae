# Tool Adapters

This document defines, per upstream tool, what `auth` mode switches and what it
must preserve. The allowlists here are the normative contract; the adapters
implement exactly this and refuse anything outside it.

Upstream credential layouts are not stable public APIs. Every adapter must
guard on the expected structure (`kae doctor <tool>` reports what was
detected) and refuse to write when the live layout is unrecognized
(exit code 10, `unsafe operation refused`).

## Claude Code

### Live auth locations

| Platform | Credential storage |
|----------|--------------------|
| macOS | Keychain generic password, service `Claude Code-credentials`; payload is JSON containing `claudeAiOauth` |
| Linux | `~/.claude/.credentials.json` (mode `0600`), key `claudeAiOauth` |
| Windows | `%USERPROFILE%\.claude\.credentials.json` (not supported in v0.1.0) |

`~/.claude.json` is **mixed state**: it contains `projects`, `mcpServers`,
onboarding, cache keys, and `oauthAccount`. kae does **not** switch
`oauthAccount`: it is a token-derived identity cache that claude self-heals
on the next authenticated run (verified 2026-06-14; docs/SCOPE-MODEL.md §6).
The file is symlinked wholesale in isolation modes; auth mode never touches it.

If `CLAUDE_CONFIG_DIR` is already set in the environment, the adapter uses it
as the live base path for `.credentials.json`. `auth` mode never sets or
changes `CLAUDE_CONFIG_DIR` itself.

### Drivers

| Driver | Platform | Switched artifacts |
|--------|----------|--------------------|
| `claude-file-patch` | Linux | `~/.claude/.credentials.json` pointer `/claudeAiOauth` |
| `claude-keychain-patch` | macOS | Keychain item `Claude Code-credentials` payload pointer `/claudeAiOauth` |

The macOS driver reads and writes the keychain through the `security` CLI via
the runner seam. The captured keychain item is stored and restored
**verbatim** — the pointer `/claudeAiOauth` is only a structure guard (the
payload must parse as a JSON object containing it; otherwise the driver
refuses), never an extraction-and-re-encode path. Claude Code stores compact,
unsorted JSON and rejects a re-serialized payload, so the bytes must round-trip
unchanged. Because the claude keychain item has `claudeAiOauth` as its single
top-level key, capturing the whole item is equivalent to capturing that key.

#### File-driver override

`KAE_CLAUDE_DRIVER=file` forces `claude-file-patch` even on macOS, so the whole
round-trip (capture on `kae add`, apply on `kae use`) closes on
`.credentials.json` under `CLAUDE_CONFIG_DIR` with no `security` subprocess and
no real keychain access. It is read inside the adapter's `driver(env)`, so it
applies to both the capture and apply paths; overriding only one side would
break the round-trip. The only accepted value is `file` — any other value is
refused as unsupported rather than silently ignored. This is an **ephemeral
smoke/container escape hatch**: a live macOS claude reads the keychain, not the
file, so persisting it would silently break a real login. The persisted,
explicit opt-in counterpart is `[tools.claude]` `driver = "file"` (claude only;
the env var takes precedence; see [DATA-MODEL.md](DATA-MODEL.md)).

### Preserved (never touched in auth mode)

```text
~/.claude/settings.json        ~/.claude/CLAUDE.md
~/.claude/skills/              ~/.claude/agents/
~/.claude/.credentials.json    -> all keys except /claudeAiOauth
~/.claude.json                 (symlinked wholesale in isolation modes;
                               never touched in auth mode — /oauthAccount
                               is token-derived and self-healed by claude)
project/.claude/  project/CLAUDE.md  project/.mcp.json
MCP / hooks / permissions / trust state / session history / plugins
```

### Environment conflicts

`ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, and `CLAUDE_CODE_OAUTH_TOKEN`
override subscription login inside Claude Code. `kae doctor` warns when any of
them is set, because a switch would silently have no effect.

## Codex CLI

### Live auth locations

Codex keeps everything under `CODEX_HOME` (default `~/.codex`). Credentials
live in `~/.codex/auth.json` or in the OS credential store, selected by
`cli_auth_credentials_store = "file" | "keyring" | "auto"` in
`~/.codex/config.toml`. `auth.json` contains only authentication state
(tokens, account id, last refresh), so unlike `~/.claude.json` it may be
swapped as a whole file.

`auth` mode never sets or changes `CODEX_HOME`. If it is already set in the
environment, the adapter uses it as the live base path.

### Drivers

| Driver | Status | Switched artifacts |
|--------|--------|--------------------|
| `codex-auth-json` | implemented | whole `~/.codex/auth.json` (file mode `0600`) |
| `codex-keyring` | implemented (v0.8.3) | keychain item service `Codex Auth`, captured and restored verbatim |

Store selection by `cli_auth_credentials_store`:

- explicit `cli_auth_credentials_store = "keyring"`: kae switches the `Codex
  Auth` keychain item (driver below). `auto` (or unset) with neither `auth.json`
  nor a keyring item is indistinguishable from "not logged in", so `capture`
  fails with `auth_missing` (exit 3) including a keyring hint, while `switch`
  proceeds normally — switching to a captured account legitimately creates the
  live state. `doctor` and `status` carry the same hint as a warning.

#### Keyring item contract (discovery 2026-06-16; driver shipped v0.8.3)

Real-machine discovery (`cli_auth_credentials_store = "keyring"` + `codex
login` on macOS) found the keychain item:

- **service** `Codex Auth`
- **account** `cli|<opaque>` — a per-login opaque id (not a hash of
  `account_id` / `sub` / `email` / `jti` / `sid`), so kae **captures it
  verbatim and never computes it**.
- **payload** the whole `auth.json` JSON (`tokens`, `OPENAI_API_KEY`,
  `auth_mode`, `last_refresh`) — file-mode-equivalent content.

The `codex-keyring` driver reuses the verbatim-keychain pattern (as claude /
cursor): capture the single live `Codex Auth` item's account (`KeychainReplace`,
stored as the snapshot's `keychain_account`) and payload; structure guard =
payload parses as a JSON object containing `/tokens`. On apply kae **deletes the
existing `Codex Auth` item before writing the target's** under its captured
account, so exactly one item remains — robust whether codex matches by service
only or service+account (the open discovery point, conservatively covered). The
detect-only exit-10 refusal is gone; identity auto-detection reads the keychain
payload's `id_token` email / `account_id` just like the file store.

### Preserved

```text
~/.codex/config.toml  ~/.codex/*.config.toml  ~/.codex/hooks.json
~/.codex/history.jsonl  ~/.codex/logs/  ~/.codex/cache
project/.codex/  AGENTS.md  rules / hooks / MCP / skills
```

## Antigravity CLI (`agy`)

> **Note:** The `gemini` adapter was removed in v0.6.0 after upstream retired
> Gemini CLI for Antigravity on 2026-05-19. Captured gemini accounts remain on
> disk untouched; use `agy` going forward.

Antigravity CLI keeps its state under `~/.gemini/antigravity-cli/`
(`settings.json`, `log/`, `skills/`). On macOS the credential lives in the login
Keychain; on Linux/WSL headless setups it falls back to a credential file.

### Drivers

| Driver | Platform | Switched artifacts |
|--------|----------|--------------------|
| `agy-keychain` | macOS | Keychain item service `gemini`, account `antigravity`, captured and restored verbatim (matched by service **and** account) |
| `agy-file-snapshot` | Linux/WSL | whole files `credentials.enc`, `credentials.json`, `oauth_creds.json` under `~/.gemini/antigravity-cli/` (whichever exist; names cover observed versions) |

#### Keychain item contract (discovery 2026-06-18; driver shipped v0.8.6)

Real-machine discovery (an Antigravity login on macOS) found the credential in
the **login Keychain**, not a file:

- **service** `gemini` — **shared with the Gemini ecosystem**, so it alone does
  not identify agy's item.
- **account** `antigravity` — a **fixed literal** (not a per-login opaque id
  like codex's), so kae matches by **service *and* account** and never reads,
  writes, or deletes a `gemini` item under a different account.
- **payload** a single opaque ~686-byte token (single line, not JSON, not a
  JWT) — captured and applied **verbatim**. Structure guard = non-empty,
  single line (no JSON parse, unlike codex).

The `agy-keychain` driver reuses the verbatim-keychain pattern (as claude /
cursor / codex): capture reads the live `gemini`/`antigravity` item's payload;
apply upserts it back (`security add-generic-password -U -s gemini -a
antigravity`, matched on service+account, so a sibling item is never touched).
No delete-replace and no account reuse, unlike codex's per-login opaque
account. `Detect`/`doctor` report the keychain item's presence on macOS; the
"kae cannot switch agy yet" warning is gone there. On Linux/WSL the file driver
is unchanged: when no credential file exists, `capture` fails with
`auth_missing`, and `doctor` warns the keyring may be in use.

`kae add agy` is **`--no-login` only**: agy has no kae-drivable login
(authentication is GUI/browser OAuth via the Antigravity app — no
`login`/`auth`/`whoami` subcommand). agy's `Identity` reads the active Google
account email from `~/.gemini/google_accounts.json` (`.active`). **Caveat
(current Antigravity, 1.0.x): this file is legacy and may be stale.** Antigravity
resolves the live account from the opaque keychain token server-side and renders
it only in the interactive banner; it no longer writes the account to disk
(`google_accounts.json` is left at its old Gemini-CLI value, and the keychain
token cannot be decoded). So auto-detection may record an out-of-date identity or
none at all — this is expected, not a failure, and `kae add agy` succeeds either
way. To record the real identity, pass it explicitly:

```bash
kae add --no-login --identity <email> agy <name>   # at capture time
kae account set-identity agy <name> <email>         # backfill an existing account
```

identity is optional metadata (switching works without it); a missing one is
reported as a calm note, never an error. An explicit `kae add agy <name>` still
wins for the account name. agy home isolation (`use -i agy`) stays unsupported
(no redirectable home env var); only credential switching is added.
`ANTIGRAVITY_API_KEY` can be handled through env profiles (`kae env set agy ...`).

### Preserved

```text
~/.gemini/antigravity-cli/settings.json
~/.gemini/antigravity-cli/skills/  ~/.gemini/antigravity-cli/log/
plugins / MCP / hooks / subagents
```

## OpenCode

### Live auth locations

OpenCode keeps credentials in `$XDG_DATA_HOME/opencode/auth.json` (default
`~/.local/share/opencode/auth.json`, mode `0600`), one top-level key per
provider. The ChatGPT-subscription login (native since the OpenAI
partnership; the Claude subscription login was removed upstream in 2026-01)
is the `openai` key: `{type, refresh, access, expires, accountId}`.

This file is **mixed state**: sibling keys are independent provider
credentials (API keys added via `opencode auth login`), which belong to env
mode and must survive an account switch. It is patched via JSON Pointer
only, never replaced.

If `XDG_DATA_HOME` is already set in the environment, the adapter uses it as
the live base path (absolute values only — a relative value is ignored per
the XDG spec, as everywhere in kae). `auth` mode never sets or changes it.

### Driver

| Driver | Platform | Switched artifacts |
|--------|----------|--------------------|
| `opencode-file-patch` | all | `auth.json` pointer `/openai` |

An `auth.json` that does not parse as JSON fails the structure guard on
read, and one whose root is not a JSON object is refused on write (both
exit 10) — the file is never replaced wholesale. An `auth.json` without an
`openai` entry is "not logged in" for kae: `capture` fails with
`auth_missing` (exit 3), and `doctor` / `status` explain that only the
ChatGPT subscription login is switched.

### Preserved

```text
~/.local/share/opencode/auth.json -> all keys except /openai
~/.config/opencode/               -> settings, skills, plugins
~/.local/share/opencode/storage/  -> projects, sessions
```

## Cursor CLI (`cursor-agent`)

### Live auth locations

| Platform | Credential storage |
|----------|--------------------|
| macOS | Keychain generic password, service `cursor-access-token`, account `cursor-user`; the payload is an opaque raw JWT (not JSON) |
| Linux | undocumented; unsupported in v0.6.0 |
| Windows | unsupported |

`cursor-agent login` (browser flow) creates the item. The access token is the
whole credential — there is no mixed-state file to patch.

`~/.cursor/agent-cli-state.json` holds only UI tip flags, not auth, and the
rest of `~/.cursor` belongs to the Cursor IDE (extensions, hooks); all of it
is preserved. The separate `Cursor Safe Storage` keychain item is the IDE's
Electron safeStorage key and is never touched.

### Driver

| Driver | Platform | Switched artifacts |
|--------|----------|--------------------|
| `cursor-keychain` | macOS | Keychain item `cursor-access-token`, captured and restored verbatim |

The payload round-trips verbatim through the `security` CLI, ACL-preserving,
exactly as for claude — but it is opaque (a raw JWT, not JSON), so there is no
JSON-pointer structure guard (an empty pointer marks the opaque payload; see
docs/DATA-MODEL.md). On a non-darwin platform capture / switch refuse with
exit `5` (unsupported).

### Preserved

```text
~/.cursor/                     -> IDE extensions, hooks, agent-cli-state.json
Cursor Safe Storage (keychain) -> the IDE's Electron key, never touched
```

## GitHub Copilot CLI (`copilot`)

Copilot is the odd one out: each account's OAuth token lives in its **own**
OS-keychain item (service `copilot-cli`, account `<host>:<user>`, e.g.
`https://github.com:main`) and the items **coexist** — logging into a second
account does not evict the first. "Switching accounts" is therefore not a
credential swap; it repoints the active account recorded in
`~/.copilot/config.json`.

### Live auth locations

`~/.copilot/config.json` (mode `0600`) is **JSONC** (a leading `//` comment
block) and mixed-state:

```jsonc
// User settings belong in settings.json.
// This file is managed automatically.
{
  "trustedFolders": ["/workspaces"],
  "lastLoggedInUser": {"host": "https://github.com", "login": "main"},
  "loggedInUsers": [{"host": "https://github.com", "login": "main"}]
}
```

The CLI has no native account-switch or logout command (only `copilot login`,
an OAuth device flow). Tokens are env-overridable, precedence
`COPILOT_GITHUB_TOKEN` → `GH_TOKEN` → `GITHUB_TOKEN`.

### Driver

| Driver | Platform | Switched artifacts |
|--------|----------|--------------------|
| `copilot-config-pointer` | all | `~/.copilot/config.json` pointer `/lastLoggedInUser` (JSONC; comments preserved) |

Only `/lastLoggedInUser` is switched. The per-account keychain tokens are
**never touched** (they coexist), so a switch only works between accounts
already present in `loggedInUsers` (logged in once via `copilot login`); kae
does not move tokens. The file is patched as JSONC so the leading comments,
trailing commas, and formatting survive (docs/DATA-MODEL.md). An unparseable
config refuses with exit `10`; a config without `/lastLoggedInUser` is
"not logged in" (`auth_missing`, exit 3).

Multi-account switching is verified only on a single account so far; the
cross-account behaviour (does repointing `/lastLoggedInUser` make copilot use
the other keychain token) is a v0.7.0 acceptance item (docs/ROADMAP.md).

### Preserved

```text
~/.copilot/config.json -> /loggedInUsers, /trustedFolders, /firstLaunchAt
~/.copilot/settings.json (hooks), AGENTS.md, hooks/, skills/, ide/, mcp config
the per-account keychain items (service copilot-cli) — never touched
```

### Environment conflicts

`COPILOT_GITHUB_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN` outrank the keychain login;
`kae doctor` warns when any is set. The gh CLI's own auth is separate and out
of scope.

## Isolation

`kae` provides two isolation scopes: **per-directory** (`kae pin -s|-i`) and
**global** (`kae use -i` / `kae run -i`). Overlay and home modes are retired
as of v0.8.0. `kae mise init` renders auth mode only; bind a directory with
`kae pin -s|-i`.

### Isolation env vars

The env var that redirects a tool to an alternate home directory:

| Tool | Isolation env var |
|------|-------------------|
| claude | `CLAUDE_CONFIG_DIR` |
| codex | `CODEX_HOME` |
| agy | none stable |
| opencode | none stable |
| cursor | none stable |
| copilot | none stable |

Tools with no stable isolation env var are skipped with an inline warning
comment in `kae pin` (they keep the real home). For `kae use -i` / `kae run
-i` with a **profile**, tools with no isolation env var are also skipped with
a warning and claude/codex are still isolated (exit `0`). A single-tool
explicit invocation on an unsupported tool exits `5`.

### Real-home resolution

When resolving the **real** home for shared-bind linking, an isolation env var
that points inside kae's own isolation data dirs is ignored (that is kae's own
redirection — e.g. exported by a pinned directory's mise fragment). Honoring
it would create self-referential symlinks (ELOOP); re-running `kae pin` repairs
any such stale links. A global command run inside a bound directory (`kae use`
/ `kae add`) resolves the real home automatically — it ignores the directory's
isolation env vars — and `kae use` warns that the change is global.

### Per-directory shared bind (`kae pin -s`)

Uses a *denylist*: every real-home entry is symlinked into the shared directory
(`isolation/<pin-id>/<tool>/shared/`) **except** the hard-coded credential
artifacts below. The credential is private-copied (not symlinked), so
authentication is private to the directory while all other files — settings,
sessions, memory, MCP configs — stay shared with the real home.

Hard-coded denylist (always excluded from symlink sharing):

```text
claude: .credentials.json  (Linux-only; macOS uses keychain — harmless to list)
codex:  auth.json
```

Unknown files a future tool version adds are **shared by default** (the inverse
of isolated-bind's fail-safe), because shared-bind's purpose is sharing — a new
file is more likely config or memory than an auth secret.

To add extra items to the denylist:
`tools.<tool>.shared_denylist_extra` (bare file names; the hard-coded auth
artifacts above are refused at config load to avoid confusion).

A real file already present in the shared directory is treated as a private
override and is never replaced or linked over.

### Per-directory isolated bind (`kae pin -i`)

Uses a per-account *private config dir*
(`isolation/<pin-id>/<tool>/isolated/<account>/config/`): nothing is shared
with the real home by default. Items explicitly listed in
`tools.<tool>.isolated_shared_items` (bare file names; credential files
`.credentials.json` / `auth.json` are refused at config load) are symlinked
from the real home; the credential is private-copied at `0600`.

`isolated_shared_items` is the opt-in share list: default is empty (full
isolation). Re-running `kae pin` refreshes opt-in shared-item links and the
credential copy.

Re-bind one tool to another account with `kae pin <tool> <account>`:

- **shared (`pin -s`)**: the credential file is overwritten in the
  account-agnostic shared dir (`isolation/<pin-id>/<tool>/shared/`); the new
  account is recorded in the kae-owned mise fragment.
- **isolated (`pin -i`)**: a new per-account config dir is prepared
  (`isolation/<pin-id>/<tool>/isolated/<account>/config/`) with opt-in shared
  links and the new credential; the kae-owned mise fragment's env entry is
  updated to point at it.

In both cases the tool picks up the new account on next launch with no change
to sessions or settings, and `KAE_PROFILE` is recomputed (ad-hoc when no
profile matches).

### Global isolated home (`kae use -i` / `kae run -i`)

Both `kae use -i` and `kae run -i` use the same per-account store:
`isolation/global/<tool>/<account>/`. State written by one is visible to the
other, so the shared location is never invisible: `kae run -i` prints the exact
home and that it is shared with `kae use -i <account>`, and `kae status`
surfaces the global-isolated homes.

`kae run -i` runs the child in this home with no lock and no live mutation —
concurrent `kae use` in other terminals is never blocked and never seen by the
isolated process. `kae run -s` (default) uses the real home, holds the per-tool
lock for the full child run, and restores the previous login.

## Login Commands

`kae add` launches the official flow and captures the result:

| Tool | Command |
|------|---------|
| claude | `claude /login` |
| codex | `codex login` |
| agy | unsupported |
| opencode | `opencode auth login` |
| cursor | `cursor-agent login` |
| copilot | `copilot login` |

The opencode flow is a provider picker; picking a provider other than the
OpenAI subscription leaves `/openai` unchanged, so `kae add` correctly
refuses with `auth_unchanged` (exit 11) — kae switches only the
subscription login.

## Account Identity (auto-detection)

`kae add <tool>` with no account name defaults it to the live login identity,
read through the optional `adapter.Identifier` capability
(`Identity(ctx, env) (string, error)`). The raw identity is sanitized into an
account name by `cmd` (`[a-zA-Z0-9._-]`, email → local part before `@`, capped
at 64); an explicit name always wins. The per-tool source:

| Tool | Identity source |
|------|-----------------|
| claude | `~/.claude.json` `oauthAccount.emailAddress` |
| codex | `auth.json` `id_token` email claim (JWT), else `tokens.account_id` |
| opencode | the `/openai` access token's `https://api.openai.com/profile` email claim (JWT), else `/openai` `accountId` (an opaque UUID; v0.8.8 prefers the email) |
| copilot | `config.json` (JSONC) `/lastLoggedInUser.login` |
| agy | `~/.gemini/google_accounts.json` `.active` — the active Google account email the Antigravity login writes (v0.8.7; the keychain token itself is opaque) |
| cursor | `cursor-agent status` prints `✓ Logged in as <email>` (discovery 2026-06-16: single line, no ANSI, exit 0); the `Identifier` (v0.8.3) parses the text after `Logged in as ` through the runner seam. A non-zero exit, a missing marker, or an empty identity is a detection failure. `cursor-agent status` may hit the network — acceptable on the interactive `kae add` path. |

A detection failure (logged out, unreadable, or sanitizes to empty) is a usage
error naming the explicit form, never a silent fallback. Identity reads only
already-trusted live state; it never verifies a JWT signature (the shared JWT
claims decoder is `internal/jwt`).

## Adding A New Adapter

1. Document the live auth locations, drivers, preserved paths, and environment
   conflicts in this file first.
2. Implement `adapter.Adapter` with capture/apply/verify built from `artifact`
   primitives (`json-pointer`, `file`, `keychain`) so backup/rollback and
   redaction come for free.
3. Add structure guards: refuse unknown layouts instead of writing.
4. If the credential is a refreshable OAuth/JWT token, implement
   `adapter.Fresher` (`Freshness(payload) freshness.Info`) using the primitives
   in `internal/freshness` (JWTExpiry / EpochToTime / DecodeObject / …) so the
   switch-time stale warning and `doctor credential_stale` can read its expiry
   and refresh-token presence; a static API key or a pointer-only artifact stays
   not-datable — just omit the method (Known=false). See the per-tool field map
   in [DATA-MODEL.md](DATA-MODEL.md).
5. Optionally implement `adapter.Identifier` so `kae add <tool>` can default the
   account name (above). Skip it when the tool exposes no readable identity.
6. Add fake-runner / temp-HOME tests for capture, apply, missing-auth, and
   guard-refusal paths.
