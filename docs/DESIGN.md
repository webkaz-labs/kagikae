# kagikae Design

## Mission

`kagikae` (command: `kae`) safely switches accounts, authentication state, and
execution environments for AI coding CLIs:

- Claude Code (`claude`)
- Codex CLI (`codex`)
- Antigravity CLI (`agy`)
- OpenCode (`opencode`)

The primary daily use case is switching subscription accounts:

```text
switch to the work Claude Max account
switch back to the personal Claude Pro account
switch to the company ChatGPT Team Codex account
switch back to the personal ChatGPT Plus Codex account
switch Google AI Pro / Ultra accounts for Antigravity
switch ChatGPT subscription accounts for OpenCode
```

## Core Principle: Auth-Only Switching By Default

The default mode must **not** switch the upstream tool home/config directory.
Replacing `~/.claude` or `~/.codex` wholesale would also separate
skills, hooks, memory, MCP configuration, project trust, session history, and
working context. Users almost always want to keep that working environment and
replace only the subscription credential.

`kae` therefore patches or swaps only an explicit allowlist of authentication
artifacts and preserves everything else. Full isolation remains available as a
separate, explicit mode.

## Terminology

| Term | Meaning |
|------|---------|
| `account` | a tool-specific login snapshot, e.g. `claude/work`, `codex/personal` |
| `profile` | a named bundle mapping each tool to one account, e.g. `work` = claude:work + codex:work + agy:work |
| `driver` | the platform/tool-specific mechanism that captures and applies auth artifacts |
| `artifact` | one captured unit of authentication state (a JSON pointer value, a file, or a keychain item) |

Single-tool and bundle switching both work:

```bash
kae use claude work
kae use codex personal
kae use work                 # resolves the "work" profile
```

## Switch Modes

| Mode | Status | Tool home | Use case |
|------|--------|-----------|----------|
| `auth` | default, implemented | unchanged | switch only the subscription account; share skills / hooks / memory / MCP / trust |
| `env` | implemented (`kae run --mode env`) | unchanged | inject API key / long-lived token into a child process only (CI, non-interactive) |
| `home` | implemented for claude / codex | separate | full isolation: concurrent accounts, CI, per-client separation |
| `overlay` | implemented for claude / codex (default on, per-tool opt-out) | partially separate | separate auth/session/cache, share settings/skills/hooks/MCP |

See [ROADMAP.md](ROADMAP.md) for ordering and [ADAPTERS.md](ADAPTERS.md) for
the per-tool definition of what `auth` mode touches and preserves.

## Switch Surface Map

Every switching feature combines a mode (**what** is switched, above) with
a scope (**where** it applies) — one verb per scope:

| Scope | Surface | Effect |
|-------|---------|--------|
| global (live state) | **`kae use`** / `kae u` (and `kae sync`, its idempotent form for hooks; `kae add`, which registers and activates) | every terminal sees the change until the next switch |
| per-directory | **`kae pin`** / `kae unpin` (sugar over `kae mise init --write`) | the directory is bound to a profile via mise `[env]`; default overlay = auth private to the directory, settings/skills shared; `--mode home` = fully separate; `--mode auth` [+ `--auto`] = mise tasks / enter hook calling the global surface |
| per-process | **`kae run [--mode M] ... -- <cmd>`** | only the spawned child; live state restored afterwards (auth) or never touched (env / home / overlay) |

Global scope supports `auth` mode only (the concurrency boundary below).
Per-process scope supports all modes. Per-directory scope composes:
overlay/home map the directory onto isolation, while auth tasks and the
`--auto` hook call the global surface.

## Subscription-First Authentication Model

`kae` assumes login/subscription accounts as the primary target, not API keys:

| Tool | Primary assumption |
|------|--------------------|
| Claude Code | Claude Pro / Max / Team / Enterprise OAuth login |
| Codex CLI | ChatGPT Plus / Pro / Team / Business / Enterprise login |
| Antigravity CLI | Google login (Google AI Pro / Ultra) |

API-key and Vertex-style profiles are handled later by `env` mode, not by
mutating live credential stores.

## Concurrency Boundary

`auth` mode mutates the live credential store shared by every terminal, so two
different accounts of the same tool cannot run concurrently in `auth` mode.
`kae` enforces a per-tool lock during switching and documents that concurrent
multi-account work requires `home` or `overlay` mode (most ergonomically via
`kae pin`).

```text
OK:  kae use work && claude
NG:  terminal A uses claude/work while terminal B uses claude/personal (auth mode)
```

## Product Boundaries

- `kae` never reimplements upstream login flows. It snapshots and restores the
  artifacts the official CLIs create.
- `kae` never edits upstream settings, skills, hooks, memory, MCP config, or
  project trust in `auth` mode.
- Mixed-state files (for example `~/.claude.json`) are patched only through a
  JSON Pointer allowlist, never replaced wholesale.
- Secrets are stored in the OS credential store by default; a plaintext file
  backend exists only as an explicit opt-in.
- Every mutation is preceded by a backup and is reversible via `kae rollback`.

## Non-Goals

- Managing API usage, billing, or model selection.
- Proxying or wrapping the upstream CLIs' normal execution (except the planned
  `kae run` transaction).
- Supporting simultaneous different accounts of one tool inside `auth` mode.
- Syncing accounts across machines.

## Completion Goal

A developer with work and personal subscriptions for several AI CLIs can:

1. `kae add <tool> <account>` once per account (or `--no-login` while logged in);
2. `kae use work` / `kae use personal` daily, in under a second,
   without losing any working context;
3. trust that a failed or interrupted switch is recoverable via `kae rollback`;
4. script everything via stable `--json` output and deterministic exit codes.

## Current State

`kae v0.5.0` implements the full mode set for macOS and Linux behind the
use / pin / run verb-per-scope surface: `add` (official login flow or
`--no-login` snapshot), `use` and `sync` (global), `pin`/`unpin` and
`mise init` (per-directory; overlay default), `run` (per-process auth
transaction with recapture-and-restore, plus `env` / `home` / `overlay`),
`env` profiles, an experimental file-snapshot adapter for Antigravity
CLI, and a JSON-pointer adapter for OpenCode's ChatGPT-subscription login
(v0.6.0). Keychain items are captured and restored verbatim, and the login
flow refuses exits that change nothing. Windows support, the Codex keyring
driver, and agy/opencode home isolation are roadmap items (v0.6.0 removed
the gemini adapter after upstream retired Gemini CLI for Antigravity on
2026-05-19).
