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
| `codex-keyring` | detect-only in v0.1.0 | OS credential store entry |

The keyring item naming used by Codex is not a documented contract, so v0.1.0
only detects the keyring configuration:

- explicit `cli_auth_credentials_store = "keyring"`: `doctor` flags it as an
  error and `capture` / `switch` refuse with exit code 10 and guidance to set
  `cli_auth_credentials_store = "file"` upstream or wait for the keyring
  driver (see [ROADMAP.md](ROADMAP.md));
- `auto` (or unset) with no `auth.json`: indistinguishable from "not logged
  in", so `capture` fails with `auth_missing` (exit 3) including a keyring
  hint, while `switch` proceeds normally — switching to a captured account
  legitimately creates `auth.json`. `doctor` and `status` carry the same
  hint as a warning.

### Preserved

```text
~/.codex/config.toml  ~/.codex/*.config.toml  ~/.codex/hooks.json
~/.codex/history.jsonl  ~/.codex/logs/  ~/.codex/cache
project/.codex/  AGENTS.md  rules / hooks / MCP / skills
```

## Antigravity CLI (`agy`) — experimental

> **Note:** The `gemini` adapter was removed in v0.6.0 after upstream retired
> Gemini CLI for Antigravity on 2026-05-19. Captured gemini accounts remain on
> disk untouched; use `agy` going forward.


Antigravity CLI keeps its state under `~/.gemini/antigravity-cli/`
(`settings.json`, `log/`, `skills/`). Credentials go to the OS keyring when
available, falling back to a credential file on platforms without one
(observed on WSL/headless setups). The keyring item contract is undocumented.

### Driver

| Driver | Status | Switched artifacts |
|--------|--------|--------------------|
| `agy-file-snapshot` | experimental | whole files `credentials.enc`, `credentials.json`, `oauth_creds.json` under `~/.gemini/antigravity-cli/` (whichever exist; names cover observed versions) |

When no credential file exists, `capture` fails with `auth_missing`; if the
CLI directory exists, `doctor` warns that agy is likely using the OS keyring,
which kae cannot switch yet (see [ROADMAP.md](ROADMAP.md)). `kae add agy`
is not supported. `ANTIGRAVITY_API_KEY` can be handled through env profiles
(`kae env set agy ...`).

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
`https://github.com:webkaz`) and the items **coexist** — logging into a second
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
  "lastLoggedInUser": {"host": "https://github.com", "login": "webkaz"},
  "loggedInUsers": [{"host": "https://github.com", "login": "webkaz"}]
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

## Isolation (home / overlay / bond / pin Modes)

`kae run --mode home|overlay|bond|pin` points a tool at an alternate home
directory; `kae pin` / `kae bond` / `kae mise init --mode home|overlay|bond|pin`
render the same mapping as mise `[env]` entries scoped to a project
directory (docs/CLI.md):

| Tool | Isolation env var | home mode | overlay mode | bond mode | pin mode |
|------|-------------------|-----------|--------------|-----------|----------|
| claude | `CLAUDE_CONFIG_DIR` | supported | supported | supported | supported (`kae pin` default) |
| codex | `CODEX_HOME` | supported | supported | supported | supported (`kae pin` default) |
| agy | none stable | refused | refused | refused | refused |
| opencode | none stable | refused | refused | refused | refused |
| cursor | none stable | refused | refused | refused | refused |
| copilot | none stable | refused | refused | refused | refused |

"Refused" means exit `5` from `kae run`; `kae pin` / `kae bond` /
`kae mise init` instead omit those tools with an inline warning comment
(they keep the real home).
`tools.<tool>.home_mode_enabled = false` / `overlay_mode_enabled = false`
(both default true) disable all of these surfaces per tool.

When resolving the **real** home for overlay and bond sharing, an isolation
env var that points inside kae's own homes/overlays/isolation data dirs is
ignored (that is kae's own redirection — e.g. exported by a pinned
directory's `.mise.toml`). Honoring it would make an overlay/bond share from
itself and create symlink cycles (ELOOP); re-running `kae pin`/`kae bond`
repairs any such stale links. The auth adapters still honor the env var as
the live base path — the semantics of global commands run inside a pinned
directory refuse with exit `5` since v0.6.0 (`kae use/add/sync`;
`--global` acts on the real home instead).

Overlay shared items (symlinked from the real home; everything else —
credentials, sessions, history, and the mixed-state `.claude.json` — stays
private to the overlay):

```text
claude: settings.json, CLAUDE.md, skills/, agents/, commands/, plugins/
codex:  config.toml, AGENTS.md, hooks.json, prompts/, skills/
```

Only items that exist in the real home are linked; a real file occupying a
link location in the overlay is refused (`unsafe_refused`), never replaced.

The allowlist is the fail-safe default: unknown files a future tool version
adds stay **private**, because a new file is more likely session/identity
state (which must not cross accounts) than shareable config. To follow
upstream additions without a kae release, extend the list per tool with
`tools.<tool>.overlay_extra_shared` (bare file names; the auth/identity
artifacts are refused at config load — docs/DATA-MODEL.md).

**Bond mode** (`kae bond`) uses a *denylist* instead of an allowlist: every
real-home entry is symlinked into the bond directory **except** the hard-coded
auth artifacts below. The credential file is private-copied (not symlinked),
so authentication is private to the directory while all other files — settings,
sessions, memory, MCP configs — stay shared with the real home.

Bond denylist (hard-coded per tool; always excluded from symlink sharing):

```text
claude: .credentials.json  (Linux-only; macOS uses keychain — harmless to list)
codex:  auth.json
```

Unknown files a future tool version adds are **shared by default** (the
inverse of overlay's fail-safe), because bond's purpose is sharing — a new
file is more likely config or memory than an auth secret. To add extra items
to the denylist, use `tools.<tool>.bond_denylist_extra` (bare file names;
the hard-coded auth artifacts above are refused at config load to avoid
confusion). A real file already present in the bond directory is treated as a
private override and is never replaced or linked over.

**Pin mode** (`kae pin`) uses a per-account *private config dir*
(`isolation/<pin-id>/<tool>/pin/<account>/config/`): nothing is shared with
the real home by default. Items explicitly listed in
`tools.<tool>.pin_shared_items` (bare file names; credential files
`.credentials.json` / `auth.json` are refused at config load) are symlinked
from the real home; the credential is private-copied at `0600`. Switching
accounts inside a pinned directory without re-running `kae pin` is done with
`kae as <tool> <account>`.

`tools.<tool>.pin_shared_items` is the opt-in counterpart to
`overlay_extra_shared`: it names items to *share* rather than items to *add
on top of a default allowlist*. The default is empty (full isolation).
Re-running `kae pin` refreshes opt-in shared-item links and the credential
copy.

**`kae as <tool> <account>`** swaps the credential inside a bonded or pinned
directory to a different account without disturbing settings, sessions, or
memory:

- **bond mode**: the credential file is overwritten in the account-agnostic
  bond dir (`isolation/<pin-id>/<tool>/bond/`).
- **pin mode**: a new per-account config dir is prepared
  (`isolation/<pin-id>/<tool>/pin/<account>/config/`) with opt-in shared
  links and the new credential; the `.mise.toml` env entry is updated to
  point at it.

In both cases the tool picks up the new account on next launch with no
change to sessions or settings.

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

## Adding A New Adapter

1. Document the live auth locations, drivers, preserved paths, and environment
   conflicts in this file first.
2. Implement `adapter.Adapter` with capture/apply/verify built from `artifact`
   primitives (`json-pointer`, `file`, `keychain`) so backup/rollback and
   redaction come for free.
3. Add structure guards: refuse unknown layouts instead of writing.
4. Add fake-runner / temp-HOME tests for capture, apply, missing-auth, and
   guard-refusal paths.
