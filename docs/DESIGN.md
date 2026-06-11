# kagikae Design

## Mission

`kagikae` (command: `kae`) safely switches accounts, authentication state, and
execution environments for AI coding CLIs:

- Claude Code (`claude`)
- Codex CLI (`codex`)
- Gemini CLI (`gemini`)
- Antigravity CLI (`agy`)

The primary daily use case is switching subscription accounts:

```text
switch to the work Claude Max account
switch back to the personal Claude Pro account
switch to the company ChatGPT Team Codex account
switch back to the personal ChatGPT Plus Codex account
switch Google AI Pro / Ultra accounts for Gemini / Antigravity
```

## Core Principle: Auth-Only Switching By Default

The default mode must **not** switch the upstream tool home/config directory.
Replacing `~/.claude`, `~/.codex`, or `~/.gemini` wholesale would also separate
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
| `profile` | a named bundle mapping each tool to one account, e.g. `work` = claude:work + codex:work + gemini:work + agy:work |
| `driver` | the platform/tool-specific mechanism that captures and applies auth artifacts |
| `artifact` | one captured unit of authentication state (a JSON pointer value, a file, or a keychain item) |

Single-tool and bundle switching both work:

```bash
kae switch claude work
kae switch codex personal
kae switch all work          # resolves the "work" profile
```

## Switch Modes

| Mode | Status | Tool home | Use case |
|------|--------|-----------|----------|
| `auth` | default, implemented | unchanged | switch only the subscription account; share skills / hooks / memory / MCP / trust |
| `env` | implemented (`kae run --mode env`) | unchanged | inject API key / long-lived token into a child process only (CI, non-interactive) |
| `home` | implemented for claude / codex | separate | full isolation: concurrent accounts, CI, per-client separation |
| `overlay` | experimental, per-tool opt-in | partially separate | separate auth/session/cache, share settings/skills/hooks/MCP |

See [ROADMAP.md](ROADMAP.md) for ordering and [ADAPTERS.md](ADAPTERS.md) for
the per-tool definition of what `auth` mode touches and preserves.

## Switch Surface Map

Every switching feature combines a mode (**what** is switched, above) with
a scope (**where** it applies):

| Scope | Surface | Effect |
|-------|---------|--------|
| global (live state) | `kae switch` / `kae s`, `kae use` / `kae u`, `kae sync`, `kae login` | every terminal sees the change until the next switch; `sync` is the idempotent form (no-op when kae's recorded state already matches) |
| per-process | `kae run [--mode M] ... -- <cmd>` | only the spawned child; live state restored afterwards (auth) or never touched (env / home / overlay) |
| per-directory | `kae mise init` (mise `[env]` + tasks; `--auto` enter hook; `--mode home` env entries) | a project directory is associated with a profile via `KAE_PROFILE`; switching is invoked through mise — explicitly via tasks, automatically on entry (`--auto` runs `kae sync --quiet`, global auth scope), or as directory-local isolated homes (`--mode home`, no global mutation) |

Global scope supports `auth` mode only (the concurrency boundary below).
Per-process scope supports all modes. Per-directory scope composes with
either: mise tasks and the `--auto` hook call the global surface, while
`--mode home` maps the directory onto home-mode isolation.

## Subscription-First Authentication Model

`kae` assumes login/subscription accounts as the primary target, not API keys:

| Tool | Primary assumption |
|------|--------------------|
| Claude Code | Claude Pro / Max / Team / Enterprise OAuth login |
| Codex CLI | ChatGPT Plus / Pro / Team / Business / Enterprise login |
| Gemini CLI | Google login (Google AI Pro / Ultra, Code Assist) |
| Antigravity CLI | Google login (Gemini CLI migration target) |

API-key and Vertex-style profiles are handled later by `env` mode, not by
mutating live credential stores.

## Concurrency Boundary

`auth` mode mutates the live credential store shared by every terminal, so two
different accounts of the same tool cannot run concurrently in `auth` mode.
`kae` enforces a per-tool lock during switching and documents that concurrent
multi-account work requires `home` (or later `overlay`) mode.

```text
OK:  kae switch all work && claude
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

1. `kae capture <tool> <account>` once per logged-in account;
2. `kae switch all work` / `kae switch all personal` daily, in under a second,
   without losing any working context;
3. trust that a failed or interrupted switch is recoverable via `kae rollback`;
4. script everything via stable `--json` output and deterministic exit codes.

## Current State

`kae v0.3.0` implements the full mode set for macOS and Linux: the v0.1.0
auth-mode commands plus `run` (auth transaction with recapture-and-restore,
`env` / `home` / `overlay` modes), `login`, `env` profiles, `mise init`, and
an experimental file-snapshot adapter for Antigravity CLI. Keychain items
are captured and restored verbatim, and `login` refuses flows that exit
without changing auth. Windows support, the Codex keyring driver, and
gemini/agy home isolation are roadmap items.
