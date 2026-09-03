# kagikae Product

This was `docs/DESIGN.md` until 2026-08-11. The shared Go CLI standard reserves
`DESIGN.md` for a *visual* design system — semantic tokens, component appearance,
visual baseline IDs, following the Google Labs DESIGN.md specification — and says in
as many words that it is "not the product or software design document". This file has
always been the latter, so it carries the name the standard gives that content, and
kae has no `DESIGN.md`: it is a plain CLI with no TTY surface, which the standard says
should omit one.

## Mission

`kagikae` (command: `kae`) safely switches accounts, authentication state, and
execution environments for AI coding CLIs:

- Claude Code (`claude`)
- Codex CLI (`codex`)
- Antigravity CLI (`agy`)
- OpenCode (`opencode`)
- Cursor CLI (`cursor-agent`)
- GitHub Copilot CLI (`copilot`)

How much surface each one gets is a tier: tier 1 gets every mode, tier 2 gets
credential switching, and both get the same refusals. **§ Tool Tiers below is the
normative statement of which tool is in which tier** — prefer a pointer to it over
a fresh copy, and if a document does state the mapping (today `SCOPE-MODEL.md` §7
and `ROADMAP.md` § Tier-2 tools, where it is what the passage is *about*), that
copy moves in the same commit as this table. Nothing enforces that, which is the
reason to keep the copies few and named here.

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

Every term this repository names — `account`, `profile`, `driver`, `artifact`,
`companion`, and the mechanism vocabulary underneath them — is defined in
[CONTEXT.md](CONTEXT.md), which is the authority on the vocabulary it holds. This
section held a second copy of the first five until they moved there: one name, one
place. It is not where a **JSON contract token** is named — that enum belongs to
`internal/constants`, and CONTEXT.md's routing table says so.

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
| `-s` (default) | real home | backup → apply → run → recapture refreshed creds → restore. Both of the last two are conditional and per tool: the recapture is declined where kae cannot say whose the live copy is or cannot order it against the snapshot (and then the copy it declines is backed up rather than lost), and the restore is skipped where it would put back a credential the child has superseded ([CLI.md](CLI.md) § kae run Semantics is normative for all of it); per-tool lock held for the child run |
| `-i` | global isolated home | reuses `isolation/global/<tool>/<account>/` shared with `kae use -i`; no live-store lock or live mutation. A shared path-lifecycle lock permits concurrent `run -i` children while excluding rename — the right choice for concurrent interactive sessions |
| `--env` | env vars only | injects the profile's env vars; no home redirect, no lock |

`run -i` prints the exact isolated home path and that it is shared with
`kae use -i <account>`, so the shared state is never invisible. There are
exactly three isolation scopes: global (`use -i` / `run -i` share one home per
account), per-directory shared (`pin -s`), per-directory isolated (`pin -i`).

What each cell does internally is
[ARCHITECTURE.md](ARCHITECTURE.md) § Switch Mechanisms.

## Tool Tiers

Six tools, two tiers. A tier says **how much surface kae pursues** for a tool. It
is a scope decision, and it is written down because without it the difference
between the tools reads as a backlog that someone will eventually feel obliged to
close.

| Tier | Tools | Surface kae commits to |
|------|-------|------------------------|
| **1 — full surface** | claude, codex | every mode: global shared (`use`), global isolated (`use -i` / `run -i`), both per-directory binds (`pin -s` / `pin -i`), identity switching and drift detection, per-directory credential stores, and the per-directory login flow (`kae relogin`). Where the tool can address its credential separately from its home, a **per-account** credential store as well, so two directories on one account run at once (claude only — [ADAPTERS.md](ADAPTERS.md) § Per-account credential store). Gaps here are debt with a plan (see [ROADMAP.md](ROADMAP.md)) |
| **2 — credential switching** | agy, opencode, cursor, copilot | global shared (`kae use`), `kae run --env`, capture / apply / backup / `kae rollback`, `kae doctor`, and identity detection as far as the tool exposes one. Nothing else, and that is the specification — not a backlog |

What Tier 2 does **not** get, deliberately: `kae pin` in either mode, and
`kae use -i` / `kae run -i`. All of those redirect the tool's home, which requires
an isolation env var kae has verified end to end for that tool; a profile-wide
`-i` skips such a tool with a warning and a single-tool `kae use -i agy <acct>`
exits `5` (§ Switching Surface, [SCOPE-MODEL.md](SCOPE-MODEL.md) §7).

**Why these two.** Both failures that actually cost a user a wrong-account session
were kae's own modelling errors in the Tier-1 tools — the per-directory claude
keychain item modelled as a constant, and a codex switch deleting another
`CODEX_HOME`'s login — not upstream changes in the Tier-2 ones. Detection is the
least valuable of the four ways kae learns about upstream drift
([.claude/skills/upstream-auth-drift/](../.claude/skills/upstream-auth-drift/SKILL.md));
a human noticed both. Surface spent on a tool nobody switches daily is surface not
spent on the two that carry the daily use case.

**A tier never relaxes a refusal.** Every safety rule applies identically at both
tiers, because the cost of getting one wrong does not scale with how popular the
tool is:

- never declare an artifact for a location kae could not measure — an unmeasured
  store gets a warning, never a guessed path (agy's fallback file, opencode's DB
  table);
- never fall back to a secondary store when the authoritative write fails;
- never derive a keychain item's account from the live item or a foreign snapshot;
- refuse, rather than approximate, an upstream config value that selects a store
  kae cannot switch;
- never adopt a credential copy kae cannot attribute to an account — a copy filed
  under the wrong name is undetectable afterwards, because the token is opaque;
- emit warnings before the write they warn about, and never let one change the
  exit code.

A Tier-2 tool gets the same guarantees about what kae will *not* do. It gets less
of what kae *will* do.

**Two Tier-2 tools worth naming, for opposite reasons.** copilot is the one where
isolation is *possible* today — `COPILOT_HOME` is a verified config-dir variable
that kae already reads — and it is still not built: demand-gated, not blocked.
agy is the floor and is unlikely to move: it has no scriptable login flow to drive,
its identity comes only from a file upstream appears to have left behind
(`google_accounts.json` is a recorded-zero row even though kae reads it — see
§ Upstream Literal Fingerprints in
[VALIDATION.md](VALIDATION.md), which carries the count and the build it was taken
on), its keychain use is conditional on
detectors kae cannot observe, and its file-store path is not derivable from the
environment. agy's open items are recorded facts about the tool, not work queued
against kae.

**Promoting a tool** takes three things, in this order: a home-isolation env var
whose resolution rule is measured (not assumed), the tool's full credential set
enumerated — every store one login writes — and a real-machine round trip proving a
bound directory authenticates as the bound account in a fresh process. Until all
three exist, the tool stays at Tier 2 and kae writes nothing it cannot verify.

## Subscription-First Authentication Model

`kae` assumes login/subscription accounts as the primary target, not API keys:

| Tool | Primary assumption |
|------|--------------------|
| Claude Code | Claude Pro / Max / Team / Enterprise OAuth login |
| Codex CLI | ChatGPT Plus / Pro / Team / Business / Enterprise login |
| Antigravity CLI | Google login (Google AI Pro / Ultra) |

API-key and Vertex-style profiles are handled later by `env` mode, not by
mutating live credential stores.

## Product Boundaries

One boundary is what can run at the same time. A global shared switch
(`kae use -s`, the default) mutates the live credential
store shared by every terminal, so two different accounts of the same tool
cannot run concurrently this way. `kae` holds a per-tool lock during the switch
and documents that concurrent multi-account work needs an isolated environment
— `kae pin` per directory, or `kae use -i` for a global per-account home.

**A second concurrency limit was measured on 2026-08-04 and is now lifted for claude**: two
directories bound to the *same* account could not run at the same time, because each
held its own **copy** of that account's credential and claude's refresh token rotates
single-use — so whichever session refreshed first invalidated the other's copy, which
then failed up to an access token's lifetime later, mid-session, with nothing offline
able to say why ([VALIDATION.md](VALIDATION.md) owns both that measurement and the
one the fix rests on). Two answers, deliberately different, and both are shipped:
kae keeps a *sequence* of such directories working by harvesting the newest copy
before it overwrites one ([CLI.md](CLI.md) § kae pin), and it makes them work
**concurrently** by giving each account one credential store that every directory
bound to it reads, while sessions, settings and memory stay per directory
([ADAPTERS.md](ADAPTERS.md) § Per-account credential store — claude's
`CLAUDE_SECURESTORAGE_CONFIG_DIR` moves the credential without moving anything else,
which is what makes that possible without giving up per-directory isolation).

The limit still stands for any tool with no such variable, and that is the honest
shape of it: it was never a boundary of the design, it is a property of what each
tool lets kae address separately. Directories bound before v0.17.0 also still have
the old layout until they are re-pinned, which `kae doctor` names. Different accounts
in different directories are unaffected either way — that is what per-directory
binding is for.

```text
OK:  kae use main && claude
OK:  cd ~/code/main-app && kae pin main   # this dir uses main; another dir can pin side
NG:  two terminals both relying on a global shared switch for different accounts
     of the same tool at the same time
```

The others are what `kae` will not do:

- `kae` never reimplements upstream login flows. It snapshots and restores the
  artifacts the official CLIs create.
- `kae` never edits upstream settings, skills, hooks, memory, MCP config, or
  project trust during a global shared switch.
- A mixed-state file (for example `~/.claude.json`) is never replaced, and only
  an explicitly allowlisted pointer inside it is written. Today that is claude's
  `/oauthAccount` identity cache, switched with the credential because claude's
  self-heal of it is gated behind a 24h TTL that every token refresh renews
  ([ADAPTERS.md](ADAPTERS.md)); every other key —
  `projects`, `mcpServers`, onboarding, trust state — is left exactly as found.
  The per-directory materializers write the same pointer, in the bound directory's
  **own** copy of that file: a directory cannot both name its own account and
  live-share the file that records which account it is, so there the file is private
  rather than shared ([SCOPE-MODEL.md](SCOPE-MODEL.md) §6).
- Secrets are stored in the OS credential store by default; a plaintext file
  backend exists only as an explicit opt-in.
- Every mutation is preceded by a backup, and `kae rollback` puts the recorded bytes
  back. One backup is not that: a `run-unattributable` backup records a post-child copy
  kae **declined to adopt**, kept so that a refusal is not a deletion, so a bare
  `kae rollback` deliberately skips it and `--to <id>` is how you reach it
  ([CLI.md](CLI.md) § kae rollback, § kae run Semantics). What a backup does **not**
  promise either way is that a restored credential still works:
  claude invalidates the older copies of a login when it refreshes, so a backup can
  hold a payload that is well-formed, unexpired and dead. kae rolls back anyway and
  says so when it can prove it ([CLI.md](CLI.md) § `kae rollback --json`) — reversible
  is a property of kae's records, not of an upstream token.
- Companion-auth lockstep is **opt-in and auth-only**: kae drives the env/config
  the companion CLIs already read (a kae-owned `GIT_CONFIG_GLOBAL` that includes
  the user's own `~/.gitconfig`, an env token resolved at mise eval time, or a
  `KUBECONFIG`-style path) and never reimplements git/gh/cloud behaviour. The
  binding is per-directory (via `kae pin`) and reverts on `kae unpin`.

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

The whole switching surface described above is implemented: the two-verb ×
two-flag matrix (`use` / `pin` with `-s` / `-i`), bare `kae use` for idempotent
hook-driven application (`--quiet`), `kae run` with `-s` / `-i` / `--env`, `kae env`
profiles, companion-auth lockstep, account and profile lifecycle, shell completion,
`kae doctor`, `kae backup` / `kae rollback`, and adapters for all six tools.
Keychain items are captured and restored verbatim; a file-driver override keeps
macOS smoke checks off the real login keychain.

Where the tools differ is § Tool Tiers, which is the normative statement: claude
and codex get every mode, the other four get global shared switching and
`kae run --env`. The one tier-1 capability still open is codex's **per-directory
keyring** bind — the code is in place and the store's account rule is measured, but
the real-machine round trip has not been run, so kae warns and writes nothing there
rather than assuming ([VALIDATION.md](VALIDATION.md), [ROADMAP.md](ROADMAP.md)).

A binding belongs to a directory, so a `git worktree` is a first-class unit: each
worktree of a repository can bind a different account, `kae pin` keeps its fragment
out of every worktree's `git status` through the repository's shared exclude file,
and `kae ls --pins` lists every bound directory from anywhere
([CLI.md](CLI.md)).

Windows remains unimplemented and is tracked in [ROADMAP.md](ROADMAP.md) (v0.6.0
removed the gemini adapter after upstream retired Gemini CLI for Antigravity on
2026-05-19). What shipped when is the release tags and the GitHub releases they
created ([RELEASE.md](RELEASE.md) for how a past release's own entry is read out of
git); `git log` is the per-commit source of truth.
