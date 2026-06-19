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
switch to your main Claude account
switch back to your side Claude account
switch to your main ChatGPT Codex account
switch back to your side ChatGPT Codex account
switch Google AI accounts for Antigravity
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
| `account` | a tool-specific login snapshot, e.g. `claude/main`, `codex/side` |
| `profile` | a named bundle mapping each tool to one account, e.g. `main` = claude:main + codex:main + agy:main |
| `driver` | the platform/tool-specific mechanism that captures and applies auth artifacts |
| `artifact` | one captured unit of authentication state (a JSON pointer value, a file, or a keychain item) |

Single-tool and bundle switching both work:

```bash
kae use claude main
kae use codex side
kae use main                 # resolves the "main" profile
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

**Bare `kae use` (no positional argument)** resolves the active profile
(`$KAE_PROFILE`, then `default_profile`, then `-P <name>`) and applies it
idempotently — a no-op (exit `0`, no lock, no backup) when the active account
already matches. `--quiet` suppresses the success report; `--json` keeps the
`changed` field. This is the form used in hook scripts (`kae use --quiet`).

**`kae run`** applies a switch to one spawned child process only:

| Flag | Environment | Behavior |
|------|-------------|----------|
| `-s` (default) | real home | backup → apply → run → recapture refreshed creds → restore; per-tool lock held for the child run |
| `-i` | global isolated home | reuses `isolation/global/<tool>/<account>/` shared with `kae use -i`; no lock, no live mutation — the right choice for concurrent interactive sessions |
| `--env` | env vars only | injects the profile's env vars; no home redirect, no lock |

`run -i` prints the exact isolated home path and that it is shared with
`kae use -i <account>`, so the shared state is never invisible. There are
exactly three isolation scopes: global (`use -i` / `run -i` share one home per
account), per-directory shared (`pin -s`), per-directory isolated (`pin -i`).

**Mechanisms.** Internally: global shared = in-place credential patch;
global isolated = `CLAUDE_CONFIG_DIR` / `CODEX_HOME` via a kae-owned global
mise fragment; per-dir shared = symlink-everything-but-credential; per-dir
isolated = private config dir with opt-in shares. All per-dir bindings use
kae-owned mise fragments — kae never edits the user's `mise.toml`. See
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
OK:  kae use main && claude
OK:  cd ~/code/main-app && kae pin main   # this dir uses main; another dir can pin side
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
- Proxying or wrapping the upstream CLIs' normal execution (except the
  `kae run` transaction).
- Supporting simultaneous different accounts of one tool within a single global
  shared switch (use an isolated environment instead).
- Syncing accounts across machines.

## Completion Goal

A developer with more than one account (a main and a side) for several AI CLIs
can:

1. `kae add <tool> [<account>]` once per account (the name is auto-detected
   from the live login when omitted; or `--no-login` while logged in);
2. `kae use main` / `kae use side` daily, in under a second,
   without losing any working context;
3. trust that a failed or interrupted switch is recoverable via `kae rollback`;
4. script everything via stable `--json` output and deterministic exit codes.

## Current State

`kae` v0.8.0 is released: the unified two-verb × two-flag switching surface
(`use` / `pin` with `-s` / `-i`); bare `kae use` for idempotent hook-driven
profile application (`--quiet`); `kae run` with `-s` / `-i` / `--env` (the
`--mode` flag and `auth|env|home|overlay|bond|pin` values are removed);
per-directory binding via `kae pin -s` (shared) and `kae pin -i` (isolated);
global isolated home (`kae use -i`, `kae run -i`) via a kae-owned global mise
fragment; `kae env` profiles; account lifecycle (`add`, `account rm` /
`rename`); `kae profile`; `kae completion <shell>`; tool-name prefix aliases;
and adapters for claude, codex, agy, opencode, cursor, and copilot. Keychain
items are captured and restored verbatim; a file-driver override keeps macOS
smoke checks off the real login keychain. `kae use -i` / `kae run -i` isolate
claude and codex only; tools with no redirectable home (agy, opencode, cursor,
copilot) are skipped with a warning when addressed through a profile; a
single-tool `kae use -i agy <account>` exits `5`. agy credential switching works
on macOS via the `gemini`/`antigravity` Keychain item (v0.8.6) and on Linux/WSL
via the file driver. Windows and home isolation for agy/opencode/cursor/copilot
remain roadmap items (v0.6.0 removed the gemini adapter after upstream retired
Gemini CLI for Antigravity on 2026-05-19).
