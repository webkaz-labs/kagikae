# kagikae Design

## Mission

`kagikae` (command: `kae`) safely switches accounts, authentication state, and
execution environments for AI coding CLIs:

- Claude Code (`claude`)
- Codex CLI (`codex`)
- Antigravity CLI (`agy`)
- OpenCode (`opencode`)
- Cursor CLI (`cursor-agent`)
- GitHub Copilot CLI (`copilot`)

The primary daily use case is switching subscription accounts:

```text
switch to the work Claude Max account
switch back to the personal Claude Pro account
switch to the company ChatGPT Team Codex account
switch back to the personal ChatGPT Plus Codex account
switch Google AI Pro / Ultra accounts for Antigravity
switch ChatGPT subscription accounts for OpenCode
switch Cursor accounts for the Cursor CLI
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

## Switching Surface

Every switch is one cell of **scope** (where it applies) × **environment**
(what is shared with the real home). Two verbs select the scope, two flags the
environment:

|                               | `--shared` / `-s` (default)                                                                                | `--isolated` / `-i`                                                                  |
|-------------------------------|------------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------|
| **`kae use`** / `u` — global  | switch every terminal's account in place; skills, hooks, memory, MCP, trust stay shared with the real home  | point every terminal at a per-account private home via a kae-owned global mise fragment (the real home untouched) |
| **`kae pin`** / `p` — per-dir | bind this directory; settings/sessions/memory shared with the real home, credential private                 | bind this directory; fully isolated, nothing shared unless opted in                  |

Both verbs take `<profile>` (every tool it maps) or `<tool> <account>` (one
tool). `use` and `pin` both default to **shared**. The environment is a
per-invocation flag, deliberately not a profile property, so the same profile
serves a global switch and an isolated project home. Inside a bound directory,
re-running `kae pin <tool> <account>` changes that one tool's account without
disturbing the others or the sharing set.

Two more surfaces sit outside the grid:

| Surface | Scope | Role |
|---------|-------|------|
| `kae apply [--profile P] [--quiet]` | global, shared | idempotent re-apply for hooks (the no-op-aware form of `kae use -s`) |
| `kae run [--mode M] … -- <cmd>` | per-process | apply to the spawned child only; live state restored afterwards (`auth`) or never touched (`env`/`home`/`overlay`/`bond`/`pin`) |

**Mechanisms.** Internally each cell maps to a mechanism: global shared =
in-place credential patch (`auth`); global isolated = `CLAUDE_CONFIG_DIR` /
`CODEX_HOME` set by a kae-owned global mise fragment; per-dir shared =
symlink-everything-but-credential (`bond`); per-dir isolated = private config
dir with opt-in shares (`pin`). The per-dir bindings are also kae-owned mise
fragments — kae never edits the user's `mise.toml`. `env`, `home`, and
`overlay` remain reachable through `kae run --mode` only. See
[ADAPTERS.md](ADAPTERS.md) for the per-tool switched/preserved contract and
[ROADMAP.md](ROADMAP.md) for ordering.

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

A global shared switch (`kae use -s`, the default) mutates the live credential
store shared by every terminal, so two different accounts of the same tool
cannot run concurrently this way. `kae` holds a per-tool lock during the switch
and documents that concurrent multi-account work needs an isolated environment
— `kae pin` per directory, or `kae use -i` for a global per-account home.

```text
OK:  kae use work && claude
OK:  cd ~/work && kae pin work   # this dir is work; another dir can pin personal
NG:  two terminals both relying on a global shared switch for different accounts
     of the same tool at the same time
```

## Product Boundaries

- `kae` never reimplements upstream login flows. It snapshots and restores the
  artifacts the official CLIs create.
- `kae` never edits upstream settings, skills, hooks, memory, MCP config, or
  project trust during a global shared switch.
- A global shared switch never touches mixed-state files (for example
  `~/.claude.json`). In isolated environments they are symlinked wholesale; only
  the credential file is private-copied. No mixed-state file is patched or
  replaced.
- Secrets are stored in the OS credential store by default; a plaintext file
  backend exists only as an explicit opt-in.
- Every mutation is preceded by a backup and is reversible via `kae rollback`.

## Non-Goals

- Managing API usage, billing, or model selection.
- Proxying or wrapping the upstream CLIs' normal execution (except the planned
  `kae run` transaction).
- Supporting simultaneous different accounts of one tool within a single global
  shared switch (use an isolated environment instead).
- Syncing accounts across machines.

## Completion Goal

A developer with work and personal subscriptions for several AI CLIs can:

1. `kae add <tool> <account>` once per account (or `--no-login` while logged in);
2. `kae use work` / `kae use personal` daily, in under a second,
   without losing any working context;
3. trust that a failed or interrupted switch is recoverable via `kae rollback`;
4. script everything via stable `--json` output and deterministic exit codes.

## Current State

`kae` v0.7.1 is released: the global shared switch (`kae use` / `kae apply`),
per-directory binding (`kae pin`, and `kae bond` for the shared environment),
the in-directory credential swap (`kae as`), `kae run` (per-process auth
transaction with recapture-and-restore, plus `env` / `home` / `overlay`),
`kae env` profiles, account lifecycle (`add`, `account rm` / `rename`),
`kae profile`, and adapters for claude, codex, agy, opencode, cursor, and
copilot. Keychain items are captured and restored verbatim; the login flow
refuses exits that change nothing; a file-driver override keeps macOS smoke
checks off the real login keychain.

The **v0.7.2 target** ([RELEASE.md](RELEASE.md)) unifies this into the
two-verb × two-flag surface above (`use` / `pin` with `-s` / `-i`; `bond` →
`pin -s`, `as` → `pin <tool> <account>`) and adds the last cell, global
isolated (`kae use -i`), which points every terminal at a per-account private
home via a kae-owned global mise fragment (the real home untouched). Windows,
the Codex keyring driver, and home isolation
for agy/opencode/cursor/copilot remain roadmap items (v0.6.0 removed the gemini
adapter after upstream retired Gemini CLI for Antigravity on 2026-05-19).
