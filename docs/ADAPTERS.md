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

In addition, `~/.claude.json` holds the account identity under `oauthAccount`.
This file is **mixed state**: it also contains `projects`, `mcpServers`,
onboarding, and cache keys. It is patched via JSON Pointer only, never
replaced.

If `CLAUDE_CONFIG_DIR` is already set in the environment, the adapter uses it
as the live base path for `.credentials.json`. `auth` mode never sets or
changes `CLAUDE_CONFIG_DIR` itself.

### Drivers

| Driver | Platform | Switched artifacts |
|--------|----------|--------------------|
| `claude-file-patch` | Linux | `~/.claude/.credentials.json` pointer `/claudeAiOauth`; `~/.claude.json` pointer `/oauthAccount` |
| `claude-keychain-patch` | macOS | Keychain item `Claude Code-credentials` payload pointer `/claudeAiOauth`; `~/.claude.json` pointer `/oauthAccount` |

The macOS driver reads and writes the keychain through the `security` CLI via
the runner seam. The captured keychain item is stored and restored
**verbatim** — the pointer `/claudeAiOauth` is only a structure guard (the
payload must parse as a JSON object containing it; otherwise the driver
refuses), never an extraction-and-re-encode path. Claude Code stores compact,
unsorted JSON and rejects a re-serialized payload, so the bytes must round-trip
unchanged. Because the claude keychain item has `claudeAiOauth` as its single
top-level key, capturing the whole item is equivalent to capturing that key.

### Preserved (never touched in auth mode)

```text
~/.claude/settings.json        ~/.claude/CLAUDE.md
~/.claude/skills/              ~/.claude/agents/
~/.claude/.credentials.json    -> all keys except /claudeAiOauth
~/.claude.json                 -> all keys except /oauthAccount
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

## Isolation (home / overlay Modes)

`kae run --mode home|overlay` points a tool at an alternate home directory;
`kae pin` / `kae mise init --mode home|overlay` render the same mapping as
mise `[env]` entries scoped to a project directory (docs/CLI.md):

| Tool | Isolation env var | home mode | overlay mode |
|------|-------------------|-----------|--------------|
| claude | `CLAUDE_CONFIG_DIR` | supported | supported (pin default) |
| codex | `CODEX_HOME` | supported | supported (pin default) |
| agy | none stable | refused | refused |

"Refused" means exit `5` from `kae run`; `kae pin` / `kae mise init` instead
omit those tools with an inline warning comment (they keep the real home).
`tools.<tool>.home_mode_enabled = false` / `overlay_mode_enabled = false`
(both default true) disable all of these surfaces per tool.

When resolving the **real** home for overlay sharing, an isolation env var
that points inside kae's own homes/overlays data dirs is ignored (that is
kae's own redirection — e.g. exported by a pinned directory's `.mise.toml`).
Honoring it would make an overlay share from itself and create symlink
cycles (ELOOP); re-running `kae pin` repairs any such stale links. The auth
adapters still honor the env var as the live base path — the semantics of
global commands run inside a pinned directory refuse with exit `5` since
v0.6.0 (`kae use/add/sync`; `--global` acts on the real home instead).

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

## Login Commands

`kae add` launches the official flow and captures the result:

| Tool | Command |
|------|---------|
| claude | `claude /login` |
| codex | `codex login` |
| agy | unsupported |

## Adding A New Adapter

1. Document the live auth locations, drivers, preserved paths, and environment
   conflicts in this file first.
2. Implement `adapter.Adapter` with capture/apply/verify built from `artifact`
   primitives (`json-pointer`, `file`, `keychain`) so backup/rollback and
   redaction come for free.
3. Add structure guards: refuse unknown layouts instead of writing.
4. Add fake-runner / temp-HOME tests for capture, apply, missing-auth, and
   guard-refusal paths.

## v0.6.0 Adapter Discovery Notes (pre-implementation)

Real-machine findings (macOS, 2026-06-13); these become normative sections
above when each adapter lands.

### opencode

- Credentials: `~/.local/share/opencode/auth.json` (mode `0600`), one key
  per provider; the ChatGPT-subscription login is `/openai` with
  `{type, refresh, access, expires, accountId}`. File-based — the codex
  auth.json pattern applies (pointer `/openai` as the structure guard).
- Preserved: everything under `~/.config/opencode/` (settings, skills,
  plugins) and `~/.local/share/opencode/storage/` (projects, sessions).
- Login: `opencode auth login` (provider picker; no non-interactive form).

### cursor

- Credentials: keychain item, service `cursor-access-token`, account
  `cursor-user` (created by `cursor-agent login`). The claude
  verbatim-keychain pattern (raw-byte capture/restore via the `security`
  CLI) should carry over; Linux storage still unknown → darwin-only first.
- `~/.cursor/agent-cli-state.json` holds only UI tip flags (not auth).
  `~/.cursor` otherwise belongs to the Cursor IDE (extensions, hooks) and
  is preserved; the `Cursor Safe Storage` keychain item is the IDE's
  Electron safeStorage key — never touch it.
- Login: `cursor-agent login` (browser flow).

### copilot

- Credentials: keychain item, service `copilot-cli`, **account
  `<host>:<user>`** (e.g. `https://github.com:webkaz`) — items for
  different GitHub accounts coexist, so switching is not a token swap.
- Active-account record: `~/.copilot/config.json` (mode `0600`) is
  **JSONC** (leading `//` comment) with `lastLoggedInUser`,
  `loggedInUsers`, `login`, `host`, `trustedFolders`, `firstLaunchAt` —
  mixed state (trustedFolders must be preserved), but kae's JSON-pointer
  patching cannot round-trip JSONC comments. Open design question; needs
  a behavioural experiment (what copilot reads on start) before the
  adapter is designed. `settings.json` is hooks-only (preserved).
- Env conflict checks needed: `COPILOT_GITHUB_TOKEN` / `GH_TOKEN` /
  `GITHUB_TOKEN` outrank the keychain login; the gh CLI fallback is
  lowest-priority and untouched.
