# kae v0.8.8 (released 2026-06-18)

Daily-use fixes surfaced right after v0.8.7: opencode auto-named accounts by an
opaque UUID, and shell completion broke when a flag preceded the positionals
(`kae add --no-login <TAB>` offered nothing). Additive and contract-stable:
`schema_version` stays `1`, no new dependency.

Previous baseline: v0.8.7 (complete account-identity coverage).

Shipped 2026-06-18.
- **opencode identity**: `opencode.Identity` now decodes the `/openai` access
  token (a JWT) and prefers its `https://api.openai.com/profile` email claim,
  falling back to the opaque `accountId` UUID only when no email is present
  (mirrors codex). Re-capture an existing UUID-named opencode account to pick up
  the email.
- **flag-aware completion**: the bash/zsh/fish scripts route by the
  flag-filtered positional index, so a flag before the positionals no longer
  shifts completion (`kae add --no-login <TAB>` completes tools; `kae use -i
  claude <TAB>` completes accounts).
- **flag-name completion**: a new `kae __complete flags <command>` backend lists
  a command's flags (sourced from the same per-command registrars the parser
  uses, `internal/cmd/flagspec.go`, so the list never drifts); the scripts call
  it when the current word starts with `-` (`kae add --<TAB>`, `kae run -<TAB>`).

# kae v0.8.7 (released 2026-06-18)

Complete account-identity coverage. agy was the last tool whose login identity
kae could not read, so its accounts showed a blank `Identity` and `kae add agy`
required an explicit name. v0.8.7 implements `agy.Identity` from the active
Google account in `~/.gemini/google_accounts.json` (`.active`, written by the
Antigravity login; the keychain token itself is opaque), so every tool now
exposes an identity. `kae status` gains an `Identity` column to match `kae ls` /
`kae accounts`. Additive and contract-stable: `schema_version` stays `1`, no new
dependency.

Previous baseline: v0.8.6 (agy keyring driver + terser run).

Shipped 2026-06-18. §A: `agy.Identity` reads `~/.gemini/google_accounts.json`
`.active`, so `kae add agy` (no name) auto-detects the account name and the
snapshot records the identity; `TestIdentifierConformance` pins that all six
tool adapters now implement `adapter.Identifier`. §B: `kae status` shows the
active account's identity (text column + additive `identity` JSON field,
`omitempty`). Existing accounts captured before their tool gained identity stay
blank until re-captured (`kae add --no-login <tool> <name>` while logged into
that account backfills it) — the documented backfill path, no new command.

# kae v0.8.6 (released 2026-06-18)

Lift agy account switching on macOS (the one tool kae still cannot switch here)
and pay down small daily-use friction, closing the open real-machine acceptance
items from v0.8.3/v0.8.4 along the way. Additive and contract-stable: no
JSON-contract break (`schema_version` stays `1`), no new dependency. The agy
driver reuses the verbatim-keychain pattern already proven for codex/claude/
cursor (the `security` calls go through `internal/runner`); the run change
reuses `adapter.Binary()` and the existing `run` transaction.

Previous baseline: v0.8.5 (did-you-mean nearest-match hint).

Shipped 2026-06-18. §A: the agy keychain driver switches the
`gemini`/`antigravity` Keychain item on macOS, matched by service **and**
account so a sibling `gemini` item is never touched; Linux/WSL keeps the file
driver. §B: `kae run <tool> <account>` defaults the child to the tool binary
when no `-- <cmd>` is given. §C: `claude /login` is launched via the upstream
flow (unchanged); agy login stays deferred (GUI OAuth, no kae-drivable login).
§D: the **agy two-account real-keychain gate PASSED**; **fish was dropped from
the verified shells** (`kae completion fish` stays best-effort); the codex
keyring two-account gate stays the one carried, unit-covered open item
([VALIDATION.md](VALIDATION.md)).

## Scope

### A. agy keyring driver (macOS) — switch agy accounts

The 2026-06-18 discovery settled agy's credential contract on macOS: it lives in
the **login Keychain**, not a file. Lift the file-only agy adapter so kae can
switch agy on macOS, mirroring the codex keyring driver (v0.8.3 §C):

- **Item contract**: service `gemini`, account `antigravity` (a fixed literal,
  **not** an opaque per-login id like codex's `cli|<opaque>`). The payload is a
  single **opaque ~686-byte token string** — not JSON. kae captures and applies
  it **verbatim** through `security` (the `internal/runner` seam), with a
  structure guard of "non-empty, single-line" (no JSON parse, unlike codex).
- **Match by service *and* account.** The `gemini` service is shared with the
  Gemini ecosystem; only `acct=antigravity` is agy's, so kae must never touch a
  `gemini` item with a different account. Apply replaces the single
  `gemini`/`antigravity` item (`security add-generic-password -U`).
- **macOS keyring path; file path unchanged elsewhere.** On macOS the adapter
  resolves the keychain driver; the existing file-based driver
  (`credentials.enc`/`credentials.json`/`oauth_creds.json`) stays for Linux/WSL
  headless setups. `Detect`/`doctor` report the keychain item's presence on
  macOS instead of warning "kae cannot switch agy yet". `Binary()` stays `agy`.
- **Capture is `--no-login` only.** agy has no kae-drivable login (§C), so
  `kae add agy <name>` snapshots the live keychain item; there is no login flow.
- Fake-`security` round-trip tests (as for codex); the two-account real-keychain
  gate is a release item (§D).

### B. Terser one-shot `kae run` (default the child to the tool binary)

`kae run <tool> <account> -- <tool>` is the documented way to open a session
under another account (works inside a pinned directory without unpinning), but
the trailing `-- <tool>` is redundant. Default it:

- **`kae run [-s|-i|--env] <tool> <account>`** with no `-- <cmd>` runs the
  adapter's `Binary()` as the child (claude→`claude`, cursor→`cursor-agent`,
  agy→`agy`, …). `kae run claude work` ⇒ runs `claude` with the account applied;
  `kae run -i claude work` runs it in the isolated home.
- An explicit `-- <cmd>` is unchanged and still wins.
- An `all`/profile target (no single child binary) or a tool with no launchable
  upstream binary still requires `-- <cmd>`, erroring clearly when it is missing.

### C. Login UX: `claude /login` verification (agy login stays deferred)

- **agy login — discovery done (2026-06-18); not kae-drivable.** The `agy` CLI
  has **no `login`/`auth`/`whoami` subcommand** — authentication is GUI/browser
  OAuth, so kae's shell-out login flow cannot drive it. agy capture is
  `--no-login` only (§A); switching now works via the keychain driver, but the
  login itself stays the user's GUI action.
- **`claude /login`**: verify behavior across recent claude versions; the
  v0.8.x "login flow exited without changing auth → exit `11`" detection stays.

### D. Close the open real-machine acceptance gates

Run these real-keychain/real-shell gates during the v0.8.6 release gate where
the environment allows, and record PASS/defer in VALIDATION.md:

- **agy two-account real-keychain gate** (new, §A): with two agy logins,
  `kae add agy <a>` / `kae add agy <b>` and `kae use agy <a>` round-trip through
  the `gemini`/`antigravity` keychain item and a fresh agy session reports the
  switched account.
- **fish real-machine completion smoke** (v0.8.4 — the release machine had no
  fish; bash/zsh verified).
- **codex keyring two-account real-keychain gate** (v0.8.3 — the file-driver
  round-trip is unit-covered; the two-account real-keychain path is not).

The agy and codex drivers are covered by fake-`security` round-trip tests, so a
gate that cannot run on the release machine stays deferred with the reason
recorded — not a v0.8.6 code blocker.

## Non-Goals (this release)

- TUI, Windows, remote share-list shipping — see [ROADMAP.md](ROADMAP.md).
- agy login flow / identity auto-detection — no kae-drivable login (GUI OAuth),
  and the token is opaque, so agy stays `--no-login` capture with an explicit
  name.
- agy *home* isolation (`use -i agy`) — unchanged; only credential switching is
  added. agy keeps refusing `-i` (no redirectable home env var).
- `kae env export` / explicit value reveal — CI does not use kae; dropped.
- Global mise tasks (`kae mise init --global`) — separate, design-first candidate.
- Any JSON-contract break: `schema_version` stays `1`.

## Acceptance Criteria

- **agy keyring switch**: on macOS, `kae add agy <name>` snapshots the
  `gemini`/`antigravity` keychain item and `kae use agy <name>` writes it back
  verbatim (matched by service **and** account); a fresh agy session reflects the
  switched account. A non-`antigravity` `gemini` item is never touched. An empty
  payload is refused (structure guard). The detect-only / "cannot switch" warning
  is gone on macOS; the file driver still works on Linux/WSL. Fake-`security`
  round-trip + temp-HOME tests; the redaction tests confirm the token never
  reaches stdout/JSON/logs/metadata.
- **run default child**: `kae run claude work` (no `--`) runs `claude` with the
  account applied; `kae run -i claude work` runs it in the isolated home; an
  explicit `-- <cmd>` still wins; an `all`/profile target or a binary-less tool
  without `-- <cmd>` errors naming the explicit form. Asserted via the runner
  seam; temp-HOME tests.
- **login**: `claude /login` verified across versions; agy login stays deferred
  (no kae-drivable login), with the reason recorded.
- **gates**: agy two-account + codex keyring two-account + fish smoke recorded in
  VALIDATION.md (PASS or deferred-with-reason).
- `mise run check` passes; no new entry in `go.mod`; the JSON contract is
  unchanged.

## Release Steps

1. Bump `toolVersion` to v0.8.6.
2. §A agy keyring driver (macOS `gemini`/`antigravity` verbatim item; service+
   account match; file driver retained for Linux/WSL); fake-`security` +
   temp-HOME tests; redaction test.
3. §B `run` default-child-binary; temp-HOME tests.
4. §C `claude /login` verification (agy login stays deferred).
5. Docs (ADAPTERS / DATA-MODEL / CLI / SECURITY / ARCHITECTURE / README as
   needed).
6. §D real-machine gates (agy two-account, codex keyring two-account, fish
   smoke); tag `v0.8.6`, GitHub release.

---

# kae v0.8.5 (released 2026-06-17)

Catch a typo before it becomes a "no such command". When an unknown command,
tool, or profile is close to a real one, name the nearest match in the error —
"did you mean `use`?" — instead of only listing the full vocabulary. The
candidate lists are exactly the ones v0.8.4's `kae __complete` backend already
surfaces (router commands, `constants.Tools`, config profiles), so this is a
thin, additive layer over a settled source of truth: no JSON-contract break
(`schema_version` stays `1`), no new dependency (the edit-distance check is
hand-rolled), and no change to any existing resolution path.

Previous baseline: v0.8.4 (dynamic shell completion).

Shipped 2026-06-17. §A: the did-you-mean hint fires at all three sites plus
`kae doctor` (unified onto the shared `validateTool`); fully covered by
unit/temp-HOME tests — no real-machine gate (a pure-text behavior). §B (the
chezmoi standardization of the mise-integration + did-you-mean patterns into the
go-cli-tooling shared standard) also landed 2026-06-17: a new
`docs/go-cli/PATTERNS.md` in the chezmoi repo, with this repo's bundled
`.claude/skills/go-cli-tooling/` resynced from it.

## Scope

### A. "Did you mean?" nearest-match hint (kae)

A shared `internal/cmd` helper computes the nearest candidate to an unmatched
token by Levenshtein distance and appends a hint to the existing usage error.
It is suggestion-only — the command still fails with the same exit code; only
the message gains a "did you mean X?" line.

- **Threshold (avoid noise)**: suggest only when the best distance is `<= 2`
  **and** `<= len(input)/3 + 1` (so a 3-char typo of a long word still hints,
  but a wildly different token does not). A tie or no candidate under the
  threshold appends nothing — the error is unchanged.
- **Three call sites**, each table-driven off the same list `kae __complete`
  uses, so candidates never drift:
  - **unknown command** — `Root()`'s `default` arm, against `completionCommands`
    (aliases like `u`/`p`/`s` included in the match set so `kae uze` → `use`).
  - **unknown tool** — `validateTool`, against `constants.Tools` (after the
    prefix-alias and removed-tool paths, which are unchanged: a hint fires only
    when `resolveToolArg` did not resolve and the tool is genuinely unknown).
    `kae doctor <tool>` was unified onto this same `validateTool` call (it had a
    divergent copy of the unknown-tool error), so it gains the hint and the
    removed-tool successor message too.
  - **unknown profile** — the profile-resolution not-found error, against
    `Config.ProfileNames()`.
- **Out of scope**: account names (too many, low-value, and they sanitize
  freely) and flags. Single best match only — no multi-candidate "did you mean
  X, Y, or Z?" list (that was the v0.8.4 non-goal; one suggestion keeps the
  message terse).
- Temp-HOME tests: a near-miss at each call site yields the hint; an unrelated
  token yields the unchanged error; an exact alias/prefix still resolves with no
  hint (no regression to `resolveToolArg`).

### B. Standardize the reusable patterns into the Go CLI standard (chezmoi)

**Separate from the kae release** (kae repo unaffected): promote two reusable
patterns proven in kae into the shared Go CLI standard so sibling tools inherit
them. This folds in v0.8.4 §E (the mise-integration pattern) and adds the
did-you-mean pattern from §A above. All three targets are sourced from chezmoi
(`~/.local/share/chezmoi`); apply with `chezmoi apply`:

1. **mise-integration pattern** (v0.8.4 §E): pin env-redirect fragments +
   dynamic completion via a hidden `__complete` backend (usage/`complete`,
   global-vs-project registration rules) — captured in the agent memory
   `mise-integration-pattern`.
2. **did-you-mean pattern** (§A): a hand-rolled nearest-match hint over the same
   live candidate lists the completion backend exposes (no framework), with the
   noise-avoiding distance threshold.

Reflect both in the three standard locations:
- **CLI standard docs** — `docs/go-cli/` and `docs/go-cli-architecture.md`.
- **go-cli-tooling skill** — `dot_agents/skills/go-cli-tooling/` (the canonical
  source; it symlinks into `~/.claude` and `~/.agents`, and this repo's bundled
  `.claude/skills/go-cli-tooling/` re-syncs from it).
- **Templates** — the relevant `chezmoi_templates/` / `dot_*` `.tmpl` files.

## Non-Goals (this release)

- Multi-candidate suggestion lists, account/flag suggestions — single
  best-match command/tool/profile hints only.
- A fuzzy-matching dependency — the edit-distance check stays hand-rolled.
- fish real-machine completion smoke and the codex keyring two-account gate —
  open acceptance items, tracked separately (not blockers for v0.8.5).
- Any JSON-contract break: `schema_version` stays `1`.

## Acceptance Criteria

- **hint**: `kae uze` names `use`; `kae add clade` names `claude`;
  `kae use wrok` (a near profile) names `work`; each still exits with its
  original code. An unrelated token (`kae zzzzz`) appends no hint. An exact
  prefix/alias (`kae u`, `kae cl work`) resolves with no hint and no behavior
  change. Temp-HOME tests at all three sites.
- **no drift**: the suggestion candidate lists are the same ones
  `kae __complete commands|tools|profiles` returns (asserted by sharing the
  source slice/function, not a copy).
- **standard (chezmoi)**: the mise-integration and did-you-mean patterns appear
  in `docs/go-cli/`, the go-cli-tooling skill, and the templates; `chezmoi apply`
  is clean. (Out-of-repo; verified in the chezmoi tree, not by `mise run check`.)
- `mise run check` passes; no new entry in `go.mod`; the JSON contract is
  unchanged.

## Release Steps

1. Bump `toolVersion` to v0.8.5.
2. §A did-you-mean helper + the three call sites; temp-HOME tests.
3. Docs (CLI: note the hint in the relevant command/error sections; README if a
   user-facing example helps; ARCHITECTURE if the helper is worth a line).
4. Tag `v0.8.5`, GitHub release.
5. §B standardize the patterns in chezmoi (separate work item, after v0.8.5
   ships): mise-integration + did-you-mean into `docs/go-cli/`, the
   go-cli-tooling skill, and the templates; `chezmoi apply`.

---

# kae v0.8.4 (released 2026-06-17)

Make shell completion deep and dynamic — sourced from kae's live state — and
lean on mise where the user already has it. One hidden `kae __complete` backend
feeds both kae's own shell completion (`kae use <TAB>` → real
profiles/accounts/flags) and mise task-argument completion
(`mise run <task> <TAB>`). No JSON-contract break (`schema_version` stays `1`);
no new dependency (kae stays hand-rolled). Reusable mise-integration patterns
recorded for sibling tools.

Previous baseline: v0.8.3 (discovery-unblock).

Shipped 2026-06-17. bash and zsh completion verified on macOS — `kae <TAB>`,
`kae use <TAB>`, and `kae use claude <TAB>` resolve live commands / profiles+tools
/ tool-scoped accounts through `kae __complete` (the two-TAB listing is the
shells' standard ambiguous-completion behavior, governed by the user's own
`LIST_AMBIGUOUS` / `show-all-if-ambiguous` settings, not a kae defect). The
**fish real-machine smoke is deferred** (fish was not installed on the release
machine) and is the one open acceptance item — run the VALIDATION.md "v0.8.4
real-machine smoke" for fish before relying on fish completion. Making the mise
`ai-switch` tasks available globally (not just in the project that ran
`kae mise init`) is a post-ship candidate (ROADMAP.md).

## Scope

### A. `kae __complete` — one completion backend

A hidden `kae __complete <kind> [args]` subcommand (omitted from `kae help`)
prints one candidate per line from kae's live surface:

- `commands` — the router's public commands (from the `Root()` table)
- `tools` — `constants.Tools`
- `profiles` — config profile names
- `accounts [<tool>]` — captured accounts, optionally scoped to one tool

It is the single source every completion surface consults, so candidate lists
never drift from the real router/config/state. Read-only, no locks, fast. The
line-oriented output is an internal contract (not the JSON contract).

### B. Native shell completion on the backend (`kae <TAB>`)

Rewrite `kae completion <bash|zsh|fish>` so the emitted script calls
`kae __complete` **dynamically** instead of baking a static word list at
generation time. Result: `kae use <TAB>` offers live profiles+accounts,
`kae use claude <TAB>` offers claude's accounts, `kae account rm <TAB>` /
`kae add <TAB>` resolve from state, and word 1 completes commands; per-command
flag completion where cheap.

`kae completion <shell> --install` registers it **interactively**: detect
whether mise is active, then let the user choose where to register — the shell's
standard completions dir (fpath / `~/.config/fish/completions/` /
bash-completion dir; mise-independent, the default suggestion), a **global**
mise `[hooks.enter]` that sources `kae completion <shell>` (mise-native,
opt-in), or print-only. kae never silently rewrites the user's global mise
config. kae's own completion is binary-scoped, so registration is always global,
never per-project (a per-directory registration would make `kae <TAB>` blink in
and out by directory).

### C. mise task-argument completion (`mise run <task> <TAB>`)

`kae mise init` generates tasks with a `usage` spec and
`complete "<arg>" run="kae __complete …"` directives, so `mise run <task> <TAB>`
completes from kae's live state through the same backend. Add argument-taking
tasks where it helps (a profile-argument switch task; a `tool`/`account` run
task); the fixed-profile convenience tasks stay. Task-argument completion is
project-scoped, so it lives in the project mise block — the opposite of §B's
global registration. Open point settled during implementation: whether mise's
`complete run` exposes the prior argument (to scope accounts by tool); if not,
the task completes all accounts while kae's native path keeps the tool-scoped
behavior.

### D. Docs — both audiences

Document three registration paths so non-mise users are first-class:
(1) `eval "$(kae completion <shell>)"` in the shell rc, (2) a completion file in
the shell's fpath / completions dir, (3) `kae completion --install`. The mise
enter-hook path is an opt-in convenience, not the primary route (mise hooks are
experimental and need `mise activate` + a trusted config). Update CLI (completion
section + `kae mise init` task usage), README quickstart, DATA-MODEL (mise block
/ task shape), ARCHITECTURE (the `__complete` seam), and VALIDATION (a completion
smoke).

### E. Standardize the mise-integration pattern (post-implementation follow-up)

**Only after §A–§D land and the completion shape is settled**, promote the
reusable mise-integration pattern (pin env-redirect fragments + completion via a
`__complete` backend, usage/`complete`, and the global-vs-project registration
rules — captured in the agent memory `mise-integration-pattern`) into the shared
Go CLI standard so sibling tools inherit it. Reflect it in **three places**, all
sourced from chezmoi (`~/.local/share/chezmoi`):

1. **CLI standard docs** — `docs/go-cli/` and `docs/go-cli-architecture.md`.
2. **go-cli-tooling skill** — `dot_agents/skills/go-cli-tooling/` (the canonical
   source; it symlinks into `~/.claude` and `~/.agents`, and this repo's bundled
   `.claude/skills/go-cli-tooling/` re-syncs from it).
3. **Templates** — the relevant `chezmoi_templates/` / `dot_*` `.tmpl` files,
   then `chezmoi apply`.

This is a separate work item after v0.8.4 ships (not part of the kae release
itself); listed here so it is not lost.

## Non-Goals (this release)

- "Did you mean X?" unknown-command suggestion — stays a separate ROADMAP
  candidate.
- A completion-framework dependency (cobra / carapace / `jdx/usage`): kae stays
  hand-rolled and dependency-minimal; the `__complete` backend reproduces the
  dynamic-completion pattern natively.
- TUI, Windows, remote share-list shipping — see [ROADMAP.md](ROADMAP.md).
- Any JSON-contract break: `schema_version` stays `1`.

## Acceptance Criteria

- **backend**: `kae __complete commands|tools|profiles|accounts` prints the live
  candidates one per line; `accounts <tool>` scopes to that tool; an unknown
  kind exits non-zero; the subcommand is absent from `kae help`. Temp-HOME tests.
- **native completion**: a generated zsh/bash/fish script completes commands at
  word 1 and live profiles/accounts at the argument positions via
  `kae __complete`; `kae completion <shell>` still emits a valid script for each
  shell. `kae completion <shell> --install` writes to the chosen location, is
  idempotent, and never mutates the global mise config unless the user picks that
  option. Temp-HOME tests.
- **mise tasks**: `kae mise init` renders tasks whose `usage` / `complete`
  directives reference `kae __complete`; a generated `.mise.toml` parses and
  `mise run <task> <TAB>` resolves candidates on a real machine (smoke).
- **docs**: the non-mise registration paths and the mise opt-in are both
  documented; CLI / README / DATA-MODEL / ARCHITECTURE / VALIDATION current.
- `mise run check` passes; no new entry in `go.mod`; the JSON contract is
  unchanged.

## Release Steps

1. Bump `toolVersion` to v0.8.4.
2. §A `kae __complete` backend (hidden subcommand); temp-HOME tests.
3. §B native completion on the backend + interactive `--install`; temp-HOME
   tests.
4. §C `kae mise init` task `usage` / `complete` generation; temp-HOME tests.
5. §D docs (CLI / README / DATA-MODEL / ARCHITECTURE / VALIDATION; both
   audiences).
6. Real-machine smoke: register completion in each shell, confirm `kae <TAB>`
   and `mise run <task> <TAB>` resolve live candidates; tag `v0.8.4`, GitHub
   release.

---

# kae v0.8.3 (released 2026-06-17)

Lift the two discovery-blocked items, consolidate per-tool credential knowledge
onto the adapter registry, and make the detected login identity visible. The
real-machine discovery for both deferred items is done (2026-06-16; contracts in
[ADAPTERS.md](ADAPTERS.md)), so the scope is de-risked: §A
freshness-as-adapter-capability, §B cursor `kae add` identity, §C codex keyring
driver, §D store + display the detected account identity. No JSON-contract break
(`schema_version` stays `1`; new tokens are additive).

Shipped 2026-06-17. The cursor identity real-machine gate passed; the **codex
keyring two-account real-keychain gate was deferred** (the driver is covered by
fake-`security` round-trip tests) and stays the one open acceptance item — run
it before relying on the keyring driver in production
([VALIDATION.md](VALIDATION.md)).

Previous baseline: v0.8.2 (daily-use polish).

## Scope

### A. Freshness as an adapter capability

Move `freshness.Inspect`'s per-tool `switch tool` onto a per-tool adapter
`Freshness(payload) Info` method (an optional capability, beside `Identifier`),
so per-tool credential knowledge has one home (the registry). The shared
`jwtExpiry`/`epochToTime`/`decodeObject` and `internal/jwt` primitives stay in
`internal/freshness`; `freshness.Inspect` becomes a thin dispatch to the adapter
(or `cmd.accountFreshness` consults the adapter directly). A tool with no
datable credential (copilot pointer, agy blob) returns `Known=false`; a tool
that ships without a `Freshness` method stays fail-safe (not-datable). Pure
refactor — the existing freshness / doctor / stale-warning tests pass unchanged.

### B. `kae add` account identity for cursor

Implement cursor's `adapter.Identifier` via `cursor-agent status` (discovery
2026-06-16: a single line `✓ Logged in as <email>`, UTF-8 check glyph, **no
ANSI**, exit 0). Run it through the runner seam; extract the text after
`Logged in as `, trim, and let `cmd` sanitize the email to a local-part account
name (the v0.8.2 §B path). A non-matching line, a non-zero exit, or an empty
identity is a detection failure naming the explicit form. Fake-runner tests
cover the logged-in and logged-out / garbled cases. (cursor-agent status may
hit the network; acceptable on the interactive `kae add` path.)

### C. Codex keyring driver

Lift the codex `codex-keyring` driver from detect-only (the v0.8.1 §E / v0.8.2
deferral). Discovered contract (2026-06-16): the OS-keychain item is service
`Codex Auth`, account `cli|<opaque>` (an opaque per-login id — **captured
verbatim, never computed** by kae), and the payload is the whole `auth.json`
JSON (`tokens`, `OPENAI_API_KEY`, `auth_mode`, `last_refresh`). kae treats it
with the existing verbatim-keychain pattern (as claude / cursor): capture reads
the single live `Codex Auth` item's account + payload; apply writes them back
verbatim through `security`. Structure guard: the payload must parse as a JSON
object containing `tokens`. The keychain account is carried in the snapshot
(like cursor's `keychain_account`) so apply recreates the right item.

Open design point to settle during implementation, with a two-account real
keychain round-trip: whether codex matches by service only or service+account.
If service+account, apply deletes the existing `Codex Auth` item before adding
the target's (so exactly one active item exists); if service-only, an `add -U`
replace suffices. The detect-only refusal (exit 10) is replaced by the working
driver; `auto` store with neither `auth.json` nor a keyring item stays "not
logged in".

### D. Store and display the detected account identity

Today auto-detection (§B v0.8.2) reads the live login identity only to derive
the account name, then discards it — so the snapshot keeps the sanitized name
(`alice`) but not the real identity (`alice@example.com`, or a codex
`account_id`). Persist it: at capture (`kae add`, **both** the explicit-name and
auto-detect forms), best-effort call the adapter's `Identity` and record the raw
detected value in the snapshot. This builds on §B (the `Identifier` capability
for every tool, including cursor).

- `account.toml` gains an optional `identity` field (the raw detected identity),
  separate from the account name. Backfilled only on a fresh `kae add`; absent
  for pre-existing snapshots and unaffected accounts.
- `kae ls` and `kae accounts` show an `Identity` column (blank when absent); the
  `--json` account rows gain an additive `identity` field (`schema_version`
  stays `1`, `omitempty`).
- Best-effort: a tool with no `Identifier` (agy), or a detection failure, leaves
  `identity` empty and never errors — the account name is unaffected.
- The identity (an email or account id) is PII but **not** a secret credential;
  it is stored in plaintext metadata exactly like the account name and never a
  token (SECURITY.md note; no redaction-test change beyond confirming no token
  leaks). It disambiguates accounts whose identities sanitize to the same name.

## Non-Goals (this release)

- TUI, Windows, remote share-list shipping, `env export --reveal`, "did you
  mean" suggestions — see [ROADMAP.md](ROADMAP.md).
- Any JSON-contract break: `schema_version` stays `1`.

## Acceptance Criteria

- **freshness capability**: the existing switch / login / doctor / stale-warning
  tests pass unchanged; per-tool expiry/refresh logic lives on the adapters and
  the primitives in `internal/freshness`; a tool with no `Freshness` method is
  treated as not-datable (`Known=false`).
- **cursor identity**: `kae add cursor` (no name) on a live `cursor-agent
  status` login captures under the sanitized detected email; a logged-out or
  unparseable status errors naming the explicit form (fake-runner tests).
- **codex keyring**: with `cli_auth_credentials_store = "keyring"`,
  `kae add codex` / `kae use codex <account>` round-trip through the `Codex
  Auth` keychain item and a fresh-process `codex login status` reports logged
  in; the detect-only refusal is gone. Two-account real-machine gate recorded in
  VALIDATION.md.
- **identity store/display**: `kae add claude` (auto-detect) and `kae add claude
  <name>` (explicit) both record the detected `identity` in `account.toml`;
  `kae ls` / `kae accounts` show it; `--json` carries an additive `identity`
  (`schema_version` still `1`). agy (no `Identifier`) and a detection failure
  leave it empty without erroring. Temp-HOME tests.
- `mise run check` passes; JSON keeps `schema_version: 1`, stable tokens, `[]`
  arrays; redaction tests cover any new output path (the keyring payload is a
  credential and must never reach stdout/JSON/logs/metadata).

## Release Steps

1. Bump `toolVersion` to v0.8.3.
2. §A freshness-as-capability refactor; existing tests green; temp-HOME tests.
3. §B cursor `Identifier` via `cursor-agent status`; fake-runner tests.
4. §D store + display the detected identity (capture records it for every tool;
   `kae ls`/`accounts` + `--json` show it); builds on §B; temp-HOME tests.
5. §C codex keyring driver (verbatim `Codex Auth` item) with a fake `security`
   runner; structure guard; temp-HOME tests.
6. Docs (ADAPTERS / DATA-MODEL / CLI / ARCHITECTURE / SECURITY / README /
   VALIDATION).
7. Real-machine gate: codex keyring two-account round-trip + cursor `kae add`
   identity on a live login; README verified; tag `v0.8.3`, GitHub release.

---

# kae v0.8.2 (released 2026-06-16)

Daily-use polish: make the most-run command fast, the most-typed command
shorter, and pay down the freshness debt v0.8.1 left. No JSON-contract break
(`schema_version` stays `1`; new tokens are additive). The codex keyring driver
(v0.8.1 §E) stays deferred — it is discovery-blocked, not patch-shaped.

Previous baseline: v0.8.1 (credential freshness / auto-recapture).

## Scope

### A. `status` speed + switch-time double read

- **Concurrent `Detect` in `status`**: today `kae status` probes each enabled
  tool's live state sequentially; on macOS each claude/cursor `Detect` is a
  `security` call, so the most-run command pays the sum. Run the per-tool
  `Detect` concurrently and reassemble results in canonical `constants.Tools`
  order (output unchanged). Bound to the enabled-tool count; failures stay
  per-tool, not fatal.
- **Coalesce the switch-time snapshot read**: `buildSwitch` reads each target's
  snapshot payload twice from kae's own secret store — once for the §B stale
  warning (`accountFreshness`) and again in `applySnapshot`. The v0.8.1
  `keychain.WithReadCache` covers the **upstream** tool keychain, not kae's own
  `secret.Backend`. Add the same context-scoped read-cache shape to
  `internal/secret` and wire it into the switch so the snapshot is read once.
  Writes (`Set`/`Delete`) invalidate the key; never cached across a child run.

### B. `kae add` account-name auto-detection (default; explicit still works)

- **`kae add <tool>`** (account omitted): detect the live login identity, derive
  a sanitized account name, and capture under it — the new default. Detection is
  a per-tool adapter capability `Identity(ctx, env) (string, error)`: claude →
  `oauthAccount.emailAddress` (from `~/.claude.json`), codex → the `id_token`
  email claim or `account_id` in `auth.json`, opencode → `accountId`, copilot →
  `lastLoggedInUser.login`. **cursor is deferred** — its `cursor-agent status`
  output is undocumented (discovery-blocked, like the codex keyring item), so
  cursor requires an explicit name until a real-machine discovery; see
  [ROADMAP.md](ROADMAP.md). The raw identity is sanitized to `[a-zA-Z0-9._-]`
  (email → local part before `@`), capped at 64.
- **`kae add <tool> <account>`** (explicit): unchanged — the given name wins.
- Works on both the login flow and `--no-login` (detect from the post-login /
  current live state). Detection failure (no identity exposed, or it sanitizes
  to empty) is an error naming the explicit form, not a silent fallback. agy has
  no `Identity` (add unsupported), so it always requires an explicit name where
  applicable.

### C. `kae ls`

- A single mise-style listing of accounts **and** profiles, today split across
  `kae accounts` and `kae status`. Table-driven from `constants.Tools` +
  captured accounts + config profiles; active markers; stable `--json`
  (`schema_version: 1`). Read-only; no new state.

### D. v0.8.1 freshness hardening

- **Two-account real-machine recapture**: extend `docs/VALIDATION.md` with a
  real-keychain gate that captures two accounts and verifies a refreshed token
  round-trips on switch-away (the v0.8.1 gate covered the file-driver logic and
  the single-account real-keychain round-trip only).
- **Shared live↔snapshot comparator**: `freshness.go`'s `valuesDiverge` and
  `login.go`'s `loginChangedAuth` implement the same "compare live values to a
  stored snapshot" loop with different error policies. Extract one comparator
  parameterized on the policy so the rule lives in one place.
- **(splittable) Freshness as an adapter capability** — **split to v0.8.3**:
  moving `freshness.Inspect`'s `switch tool` into a per-tool adapter
  `Freshness(payload) Info` method touches all six adapters plus the interface,
  which grows this patch past its daily-use-polish scope. Deferred per the
  splittable note; the shared `jwtExpiry`/`epochToTime`/`decodeObject`
  primitives stay in `internal/freshness` (see [ROADMAP.md](ROADMAP.md)).

## Non-Goals (this release)

- Codex keyring driver (v0.8.1 §E) — still discovery-blocked (see ROADMAP.md).
- TUI, Windows, remote share-list shipping, `env export --reveal`,
  "did you mean" suggestions — see [ROADMAP.md](ROADMAP.md).
- Any JSON-contract break: `schema_version` stays `1`.

## Acceptance Criteria

- **status**: `kae status --json` output is byte-identical to the sequential
  version (same tool order, fields, `[]` arrays); the per-tool `Detect` runs
  concurrently (asserted via the runner seam — overlapping calls, or a count
  proving no serialization). A single tool's `Detect` failure does not abort the
  report.
- **switch read**: a single `kae use` reads each target snapshot payload from the
  secret backend once (asserted via the backend seam call count); the switch
  result is unchanged.
- **add auto-detect**: `kae add --no-login claude` (no name) on a live login
  captures an account whose name is the sanitized detected identity; `kae add
  --no-login claude <name>` still uses `<name>`; a tool with no detectable
  identity errors naming the explicit form. Temp-HOME tests with fixture
  identities.
- **ls**: `kae ls` lists every captured account and every profile with active
  markers; `kae ls --json` keeps `schema_version: 1` and `[]` arrays.
- **hardening**: the shared comparator passes the existing switch/login tests
  unchanged; the two-account real-machine gate is recorded in VALIDATION.md.
- `mise run check` passes; redaction tests cover any new output path (no token
  or identity-derived secret in output beyond the sanitized account name).

## Release Steps

1. Bump `toolVersion` to v0.8.2.
2. §A `status` concurrency + the `secret.Backend` read cache; temp-HOME tests.
3. §B adapter `Identity` + `kae add` auto-detect default; temp-HOME tests.
4. §C `kae ls`; temp-HOME tests.
5. §D shared comparator + (splittable) freshness-as-capability; temp-HOME tests.
6. Docs (CLI/ARCHITECTURE/ADAPTERS/DATA-MODEL/README/VALIDATION).
7. Real-machine gate (two-account recapture); README verified; tag `v0.8.2`.

---

# kae v0.8.1 (released 2026-06-16)

Credential freshness. Every supported tool authenticates with a refreshable
OAuth/JWT credential, but `kae use` (and bare `use`) write the **capture-time**
snapshot back to the live store with no recapture — only `run -s` recaptures
(via `runAuthTransaction`'s post-child `captureSnapshot`). So a token rotated
outside kae (a re-login in the tool, a long-unused account) leaves the snapshot
stale, and a switch back to it can break auth — dropping to a login prompt when
the refresh token has also rotated (observed in the v0.8.0 real-machine gate,
[VALIDATION.md](VALIDATION.md)). v0.8.1 closes this gap symmetrically with
`run`, surfaces staleness it cannot self-heal, and pays down the per-switch
keychain cost the recapture adds.

Previous baseline: v0.8.0 (surface vocabulary unification).

## Scope

### A. Switch-source auto-recapture (`use` / bare `use`)

Before `kae use` / bare `use` switches away, recapture the **currently active**
account's live credential into its snapshot — the `run -s` mechanism made
symmetric for `use` — so the next switch back applies a live token. Only
`use`/bare `use` overwrite the **real** store and need this; `use -i` /
`pin -s|-i` / `rebind` / `run -i` write kae-owned isolation dirs (live store
untouched), so they stay as-is. Recapture **only when the live store and the
snapshot diverge**, to avoid a needless keychain read on every switch.

### B. Switch-time stale warning + recovery path

The account being switched **to** may be stale and is not live, so it cannot be
recaptured. At switch time, detect an expired snapshot (`expiresAt` past, or
divergence from the live store) and: proceed when the refresh token is still
usable (the tool self-refreshes), otherwise warn and name `kae add` to
re-capture. Share the staleness predicate with §D.

### C. `security` subprocess coalescing (macOS)

Recapture adds a keychain read per switch, each a `security` invocation (and a
possible auth dialog). Coalesce/cache the multiple `security` calls per switch
so the recapture does not multiply prompts; this is the practical prerequisite
for §A. (Also the v0.7.x "performance polish" backlog item.)

### D. `doctor` credential-health

Surface staleness the switch path only warns about inline: a `doctor`
stale-snapshot check (expired `expiresAt` / divergence from the live store),
reusing §B's predicate. Fold in the v0.7.1-deferred keychain-orphan check where
enumeration is reliable (file backend `readdir`, Linux `libsecret`); the darwin
keychain cannot enumerate by service, so it stays a documented gap there.

### E. Codex keyring driver — **deferred to v0.8.2**

Lifting the codex `codex-keyring` driver from detect-only requires pinning down
the OS credential-store item contract used by
`cli_auth_credentials_store = "keyring"`, which upstream does not document. A
round-trip cannot be implemented safely without first discovering the item's
service/account naming on a real machine with a live codex keyring login —
guessing it would violate the structure-guard rule (refuse unknown layouts,
never best-effort write; [ADAPTERS.md](ADAPTERS.md)). Per the splittable note,
**§E is deferred to v0.8.2** and A–D ship as v0.8.1. The deferral and its reason
are recorded in [ROADMAP.md](ROADMAP.md); the detect-only refusal (exit 10 with
guidance) is unchanged.

## Non-Goals (this release)

- **Tracking rotation that happens entirely outside kae** — a re-login in the
  tool can rotate the refresh token with no kae involvement; v0.8.1 warns
  (§B/§D) rather than silently repairing.
- TUI, Windows, remote share-list shipping, `env export --reveal` — see
  [ROADMAP.md](ROADMAP.md).

## Acceptance Criteria

- **recapture**: after `kae use A` → `kae use B` → `kae use A`, the credential
  re-applied for A is the one live at the first switch-away, not the original
  capture (temp-HOME test simulating a token refresh while A was active);
  recapture is skipped (no keychain read) when live and snapshot already match.
- **stale warning**: a switch to an account whose snapshot `expiresAt` is past
  warns and names `kae add`; with a usable refresh token it still proceeds.
- **coalescing**: a single `use` performs at most one `security` read per tool
  for the recapture decision (asserted via the runner seam call count).
- **doctor**: a stale snapshot produces a `credential_stale` warn-level check;
  the JSON report keeps `schema_version: 1`; file-backend orphans are detected.
- **codex keyring** (if kept): `kae add`/`use` round-trip through the keyring
  store passes a fresh-process auth check; otherwise the item is deferred with
  the reason recorded.
- `mise run check` passes; JSON keeps stable tokens and `[]` arrays; redaction
  tests cover any new output path (no token value in warnings/doctor output).

## Release Steps

1. Bump `toolVersion` to v0.8.1.
2. §C `security` coalescing first (prerequisite), then §A recapture + §B
   switch-time warning (shared predicate); temp-HOME tests.
3. §D `doctor` credential-health on the shared predicate; temp-HOME tests.
4. §E codex keyring driver — **deferred to v0.8.2** (undocumented keyring item
   contract; needs real-machine discovery, reason recorded in ROADMAP.md).
5. Docs (CLI/ADAPTERS/DATA-MODEL/SECURITY/README); temp-HOME tests.
6. Real-machine gate — **re-capture a live token immediately before the gate and
   use a throwaway account** (the teardown rewrites the live keychain from the
   snapshot; see VALIDATION.md). Confirm a switch-back applies a live token and
   the stale warning fires on an expired snapshot.
7. README verified against the binary; tag `v0.8.1`, GitHub release.

---

# kae v0.8.0 (released 2026-06-16)

Finish the scope×environment vocabulary: one surface, one set of names. v0.7.2
unified `use`/`pin` on `-s`/`-i`; v0.8.0 folds `apply` into `use`, redesigns
`run` onto `-s`/`-i`/`--env`, removes the mechanism-vocabulary leak from
`mise init` and the config keys, and adds input ergonomics (tool-name prefixes,
shell completion). **Pre-1.0 breaking release**: the `run --mode` flag and the
`bond_`/`pin_`/`overlay_`/`home_` config keys are removed outright — no alias,
just a migration note.

Previous baseline: v0.7.2 (use/pin × -s/-i, global isolated home).

## Scope

### A. `apply` folds into `use`

`apply` is not merely `use -s`; it adds idempotency, profile resolution, and a
quiet mode. Fold those into `use` and remove the verb:

- **bare `kae use`** (no positional) resolves the profile (`$KAE_PROFILE`, then
  `default_profile`, then `-P <name>`) and applies it **idempotently** — no-op
  (exit `0`, no lock, no backup) when `state.json` `active` already matches, like
  today's `apply`. `--quiet` suppresses the success report; JSON keeps `changed`.
- **`kae use <profile | tool account>`** (explicit positional) keeps the forced
  switch + backup (unchanged).
- **`apply`** becomes a one-release removed-command pointer (exit `64`) naming
  `kae use [--quiet]`.
- `mise init --auto`'s enter hook script becomes `kae use --quiet`.

### B. `run` redesign (`-s` / `-i` / `--env`; `--mode` removed)

Six modes collapse to three; `--mode` is removed (hard break):

- **`run -s`** (default): the child sees the **real home** (= old `auth`:
  backup → apply → run → recapture refreshed creds → restore). The per-tool lock
  is held for the whole child run.
- **`run -i`**: an **isolated home**, reusing the global-isolated store
  `isolation/global/<tool>/<account>` (shared with `kae use -i`); no lock, no
  live mutation. This is the right tool for **interactive sessions** under
  another account — concurrent `kae use` in other terminals is never blocked and
  never seen by the isolated process.
- **`run --env`**: inject the env-profile vars (old `--mode env`); no home
  redirect, no lock.
- **Removed**: `--mode` and the `auth|env|home|overlay|bond|pin` values. `home`
  folds into `-i`; `overlay` is retired; per-directory `bond`/`pin` via `run` is
  gone — a `kae pin`-ed directory already redirects the tool through its mise
  fragment, so `run` is unnecessary there.
- **Confusion guard** (`run -i` shares a store with `use -i`): `run -i` prints
  the exact home and that it is shared with `kae use -i <account>`, and
  `kae status` surfaces the global-isolated homes (§D), so the shared state is
  never invisible. Docs state the three isolation scopes plainly: global
  (`use -i` / `run -i` share one home per account), per-directory (`pin`).

### C. `mise init` trim

- Drop `--mode bond|pin` (the per-directory binding is `kae pin -s|-i`, which
  owns the fragment). Keep `--mode auth` (tasks + the opt-in enter hook, now
  emitting `kae use --quiet`); `home`/`overlay` rendering is removed with the
  `run` modes.

### D. Mechanism + config-key rename (breaking, no alias)

With `run` no longer exposing the mechanism strings, the vocabulary moves
cleanly to `shared`/`isolated`:

- internal: `modeBond`/`modePin` → `modeShared`/`modeIsolated`; retire
  `modeOverlay`/`modeHome`.
- config keys: `bond_denylist_extra` → `shared_denylist_extra`;
  `pin_shared_items` → `isolated_shared_items`; remove
  `overlay_extra_shared` / `overlay_mode_enabled` / `home_mode_enabled`. Old keys
  are **not** accepted — config load errors naming the new key (migration note in
  the release).
- `kae status` reports the global-isolated (`synced`) homes so `use -i` / `run
  -i` state is visible (also the §B confusion guard).

### E. `-i` with a profile mapping unsupported tools

- `use -i` / `run -i` for a **profile** that includes a tool with no isolation
  env var (agy, opencode, cursor, copilot) **skips it with a warning** and
  isolates claude/codex only, instead of exiting `5`. A single-tool
  `kae use -i agy <account>` still exits `5`. (Fixes the shipped `use -i`
  behavior too.)

### F. Input ergonomics

- **Tool-name prefix aliases** in tool positions (`cl`→claude, `cod`→codex,
  `cu`→cursor, `cop`→copilot, `o`→opencode, `a`→agy); ambiguous prefixes (`c`,
  `co`) error with the candidate list. Input-only (resolved to the canonical
  name, never stored); the ambiguity set is computed from `constants.Tools`.
- **`kae completion <bash|zsh|fish>`** generator, table-driven from the router +
  `constants.Tools` + config (profiles/accounts).
- **`-P`** short form for `--profile` on `run` / bare `use` / `mise init`.

## Non-Goals (this release)

- **Alias / transition window** for `--mode` or the renamed config keys — pre-1.0
  hard break with a migration note.
- TUI, Windows, Codex keyring driver, agy home isolation, remote share-list
  shipping, doctor orphan enumeration — see [ROADMAP.md](ROADMAP.md).
- "Did you mean X?" unknown-command suggestion — may ride along but not required.

## Acceptance Criteria

- **apply fold**: bare `kae use` (resolved profile) is idempotent (no-op when
  active, no lock, no backup); `kae use --quiet` is silent; JSON keeps
  `changed`; `apply` exits `64` naming `kae use`.
- **run**: `kae run -i claude <acct> -- claude` runs in
  `isolation/global/claude/<acct>` with no lock and no live mutation, and a
  concurrent `kae use` in another shell is not blocked; `run -s` holds the lock
  and restores the previous login; `run --env` injects only the profile vars;
  `run --mode …` exits usage (removed). `run -i` output names the shared home.
- **mise init**: `--mode bond|pin` rejected; `--mode auth` renders the
  `kae use --quiet` enter hook.
- **rename**: a config with `bond_denylist_extra` / `pin_shared_items` fails at
  load naming the new keys; `shared_denylist_extra` / `isolated_shared_items`
  work; `kae status` shows global-isolated homes.
- **profile skip**: `kae use -i <profile-including-agy>` isolates claude/codex,
  warns on agy, exits `0`; `kae use -i agy <account>` exits `5`.
- **ergonomics**: unambiguous tool prefixes resolve and ambiguous ones error with
  candidates; `kae completion zsh` emits a script; `-P <profile>` resolves.
- `mise run check` passes; JSON keeps `schema_version: 1`, stable tokens, `[]`
  arrays; redaction tests cover any new output path.

## Release Steps

1. Bump `toolVersion` to v0.8.0.
2. Fold `apply` into bare `kae use` (idempotent + `--quiet`); update the enter
   hook; `apply` tombstone; temp-HOME tests.
3. Redesign `run` (`-s`/`-i`/`--env`, `--mode` removed); `run -i` on
   `isolation/global`; surface `synced` in `kae status`; temp-HOME tests.
4. Trim `mise init` (drop bond/pin; hook → `kae use --quiet`).
5. Mechanism + config-key rename (hard break) with the migration note; retire
   overlay/home and their config keys.
6. Input ergonomics (tool prefixes, `kae completion`, `-P`); `-i` profile
   skip+warning.
7. Docs fold (CLI/DESIGN/ADAPTERS/DATA-MODEL/SECURITY/README); temp-HOME tests;
   real-machine gate (`run -i` interactive AUTH-OK, concurrent `use` not blocked).
8. README verified against the binary; tag `v0.8.0`, GitHub release.

---

# kae v0.7.2 (released 2026-06-16)

Unify the switching surface and ship the last cell of the scope×environment
model (global isolated).

Four switching behaviors collapse into **two verbs by scope** plus **two flags
by environment**, so the model reads as one grid instead of four unrelated
verbs:

|                              | `--shared` / `-s` (default)                                               | `--isolated` / `-i`                                                       |
|------------------------------|---------------------------------------------------------------------------|---------------------------------------------------------------------------|
| **`kae use`** / `u` — global  | switch every terminal's account in place, real home shared (v0.7.1 `auth`)| point every terminal at a per-account private home via a kae-owned global mise fragment (NEW) |
| **`kae pin`** / `p` — per-dir | bind this dir: settings/sessions shared, credential private (v0.7.1 `bond`)| bind this dir: fully isolated, opt-in shares (v0.7.1 `pin`)               |

Both verbs accept `<profile>` or `<tool> <account>`. `-i`/`-s` are short forms
of `--isolated`/`--shared`. Defaults: `use` shared (the everyday global
switch), `pin` shared (the common per-directory case). This is a pre-1.0
surface change with no released users of the affected verbs; the old verbs
become one-release removed-command pointers.

Previous baseline: v0.7.1 (file-driver override, `kae account rm`/`rename`,
`kae profile`, comment-preserving config writer; see git tag v0.7.1).

## Scope

### A. Surface unification (`use`/`pin` × `-s`/`-i`)

- **`use`/`pin` gain `--shared`/`-s` and `--isolated`/`-i`** (`internal/cmd`),
  selecting the environment. `use` defaults to shared, `pin` to shared.
- **Aliases**: `u` = `use` (already), `p` = `pin` (new route in `Root()`).
- **`bond` → `pin --shared`**: `bond` becomes a removed-command pointer (exit
  `64`, one release) naming `kae pin --shared`. The per-directory shared
  mechanism (symlink-everything-but-credential) is unchanged; only the surface
  moves under `pin -s`.
- **`as` removed**: changing one tool's account inside a bound directory is now
  `kae pin <tool> <account>` (re-binds that tool only, leaving the others and
  the sharing set intact). `as` becomes a removed-command pointer (exit `64`,
  one release) naming `kae pin <tool> <account>`.
- **`--global` flag removed**: `use` is inherently global, so it always resolves
  the real home (it auto-applies what `--global` used to do — hide kae-managed
  isolation env vars). Inside a pinned directory `use` no longer refuses (the
  v0.6.0 exit `5` guard is gone); it prints a one-line warning — "this directory
  is pinned; you are changing GLOBAL state, which this directory will not see —
  re-bind with `kae pin`" — and proceeds.

### B. Isolation via kae-owned mise fragments (the real home and `mise.toml` are never touched)

Both isolated environments set `CLAUDE_CONFIG_DIR` / `CODEX_HOME` through a
**generated, kae-owned mise fragment** at `.config/mise/conf.d/kagikae.toml`,
which mise loads and merges (a project fragment overrides the global one, so
`pin` wins over `use -i` inside a bound directory). kae **never reads or writes
the user's `mise.toml`** and never mutates the real `~/.claude` / `~/.codex`;
the fragment is regenerated from kae state, and teardown just deletes it.

- **global** (`use -i`): `~/.config/mise/conf.d/kagikae.toml`, regenerated from
  `state.json` `synced` (tool→account).
- **per-directory** (`pin`): `./.config/mise/conf.d/kagikae.toml` in the
  project, carrying the tool env entries, `KAE_PROFILE`, and (for shared) the
  bound account.
- kae creates `.config/mise/conf.d/` if absent and **adds the project fragment
  to `.gitignore`** (it holds machine-specific absolute paths and account names
  that must not be committed); the file self-documents in a header comment.
- **Requires mise activation** for the scope (global activation for `use -i`;
  the usual project activation for `pin`). When kae cannot confirm activation it
  warns and prints the `export …` line for the current shell.
- **`kae unpin`** deletes the project fragment. **Migration**: directories
  pinned before v0.7.2 carry a `# >>> kagikae` marker block inside `mise.toml`;
  there is no auto-migration — re-run `kae unpin && kae pin` once per directory.

### C. Global isolated home (`use --isolated`) — claude/codex only

- Prepare `isolation/global/<tool>/<account>/` as a full per-account private
  home (materialize the credential); the global fragment points the tool there.
  claude and codex only (others exit `5`). On macOS `CLAUDE_CONFIG_DIR` makes
  claude read the file credential, not the keychain (proven in the v0.7.0 gate),
  so the real login keychain is never touched.
- **Teardown is `use --shared`** (or bare `kae use`): drop the tool from
  `synced`, regenerate (or delete) the global fragment, then switch the real
  home in place. `-i`/`-s` toggle the global environment; no `unsync` verb, no
  backups, no restore.

### D. Per-directory account changes and status

- **`kae pin <tool> <account>`** re-binds one tool (regenerate the project
  fragment's entry for that tool); `KAE_PROFILE` recomputed (ad-hoc when no
  profile matches).
- **`status` reports the real per-tool account**, not the `KAE_PROFILE` label.
  Shared dirs record the account in the fragment so it survives re-entry; the
  isolated path already encodes the account.

### E. Data path renames (clarity)

- global isolated home `synchomes/<tool>/<account>/` →
  **`isolation/global/<tool>/<account>/`** (`synchomes` named the removed `sync`
  verb). Not shipped yet — a free rename.
- per-dir mechanism segments renamed for clarity: `…/<tool>/bond/` →
  **`…/<tool>/shared/`**; `…/<tool>/pin/<account>/…` →
  **`…/<tool>/isolated/<account>/…`**. The v0.7.1 stores under the old names are
  abandoned in place; a one-time `kae unpin && kae pin` re-creates them under the
  new names (no auto-migration).

## Non-Goals (this release)

- **`apply` / `run` redesign** — `apply` stays the idempotent hook form of the
  global shared switch; `run --mode` keeps its current mode values. Folding them
  into the `-s`/`-i` vocabulary is deferred ([ROADMAP.md](ROADMAP.md)).
- **Live bidirectional sync / watcher daemon** — `use -i` is a *switch* of which
  private home is live, not a sync engine. The §6 finding (claude self-heals
  `/oauthAccount` from the token) means no copy+patch is needed; a resident
  watcher conflicts with the CLI-only design ([SCOPE-MODEL.md](SCOPE-MODEL.md)).
- **Renaming `run --mode` values** — `run --mode bond|pin|home|overlay` keeps
  its names even though the per-directory data paths are renamed to
  `shared`/`isolated`; aligning `run`'s vocabulary is deferred with the rest of
  the `apply`/`run` review ([ROADMAP.md](ROADMAP.md)).
- **Tools without a redirectable home** (agy, opencode, cursor, copilot) —
  global shared (`use`) and `run --mode env` only, unchanged.
- TUI, Windows, Codex keyring driver — see [ROADMAP.md](ROADMAP.md).
- No automatic network access.

## Acceptance Criteria

- **surface**: `kae u -i <acct>`, `kae u -s <acct>`, `kae p -i <acct>`,
  `kae p -s <acct>` each select the right scope×environment; bare `use`/`pin`
  default to shared; `u`/`p` aliases resolve. `bond`/`as` print exit-`64`
  pointers to `pin --shared` / `pin <tool> <account>`. `--global` is gone;
  `use` inside a pinned dir warns and switches global state.
- **isolation fragments**: `kae pin` writes `./.config/mise/conf.d/kagikae.toml`
  and `kae use -i` writes `~/.config/mise/conf.d/kagikae.toml`; the user's
  `mise.toml` and the real `~/.claude` / `~/.codex` are never modified. The
  project fragment is added to `.gitignore`. `kae unpin` deletes the project
  fragment; `kae use -s` drops the tool from `synced` and regenerates/deletes the
  global fragment (temp-HOME tests).
- **global-isolated real-machine gate** (required before merge): on a staging
  machine with global mise active, `kae use -i <account>` makes a fresh-process
  `claude -p '' --model haiku` run as that account's private home and return
  AUTH-OK; the real login keychain is not polluted (file-driver path). `kae use
  -s` returns the shell to the real home. Recorded in
  [VALIDATION.md](VALIDATION.md).
- **per-dir re-bind**: `kae pin claude <other>` in a bound dir changes only
  claude; `KAE_PROFILE` drops to ad-hoc when the combination matches no profile;
  `kae status` shows the real per-tool account. A shared dir's active account
  survives re-entry (recorded in the fragment).
- **paths**: stores resolve under `isolation/global/<tool>/<account>/` and
  `isolation/<pin-id>/<tool>/{shared,isolated/<account>}/…`.
- **mise activation**: with mise not active, `use -i` / `pin` warn and print the
  `export` line and exit `0`.
- **unsupported tools**: `kae use -i agy <account>` (and opencode/cursor/
  copilot) exits `5`.
- `mise run check` passes; JSON keeps `schema_version: 1`, stable tokens, `[]`
  arrays; redaction tests cover any new output path.

## Release Steps

1. Land the surface unification: `-s`/`-i` flags, `p` alias, `pin <tool>
   <account>` re-bind, `bond`/`as` pointers, `--global` removal + pinned-dir
   warning; temp-HOME tests green. Bump `toolVersion` to v0.7.2.
2. Move per-dir isolation to a kae-owned project fragment
   (`./.config/mise/conf.d/kagikae.toml`): replace the `mise.toml` marker-block
   renderer, add `.gitignore` handling, rename the data paths to
   `shared`/`isolated`, `unpin` deletes the fragment, `status` shows the real
   per-tool account; temp-HOME tests.
3. Land global isolated (`use -i`): prepare `isolation/global/<tool>/<account>/`,
   regenerate `~/.config/mise/conf.d/kagikae.toml` from `synced`, and the
   `use -s` teardown; mise-activation warning; temp-HOME tests.
4. Run the real-machine gate (global mise active); record in
   `docs/VALIDATION.md`.
5. Phase 7 docs fold-down: reduce `docs/SCOPE-MODEL.md` to rationale/history now
   that the whole model is implemented (or keep with a reason).
6. README examples verified against the built binary.
7. Tag `v0.7.2`, GitHub release.

---

# kae v0.7.1 (released 2026-06-15)

Operational safety and account/profile lifecycle. This release closes daily-use
gaps and de-risks the global-isolated `sync` mode landing in v0.7.2: a
file-driver override so smoke/container checks never touch the real login
keychain; a comment-preserving `config.toml` writer; account removal/rename plus
profile save/rm/set/unset so cleanup and reconfiguration no longer mean manual
keychain surgery or hand-editing TOML; and (discovery-gated) doctor detection of
orphaned keychain items.

Previous baseline: v0.7.0 (bond mode, credential-private per-directory
isolation, `/oauthAccount` removal, `kae pin` semantics flip, `kae as`; see
git tag v0.7.0).

## Scope

- **claude file-driver override** — on macOS the claude adapter resolves a
  keychain driver, which ignores a temp `$HOME`; that makes claude switch
  smoke checks unsafe outside Linux (they would touch the real login keychain).
  Add an explicit override that forces the file-patch driver (`.credentials.json`
  under `CLAUDE_CONFIG_DIR`) even on darwin. **Env var is the primary surface**:
  `KAE_CLAUDE_DRIVER=file` (new `constants.EnvKaeClaudeDriver`, following the
  existing `KAE_PROFILE` convention). The override is an ephemeral
  smoke/container escape hatch; persisting it in config would silently break a
  real macOS login (the live claude reads the keychain, not the file), so a
  per-tool config option (`[tools.claude]`, default off) is only a secondary,
  explicit opt-in. The override is read inside `claude` adapter's `driver(env)`
  and must apply on **both the capture (`kae add`) and apply (`kae use`)
  paths** — overriding only one side breaks the round-trip. With it set, the
  whole round-trip closes on files: no `security` subprocess, no real keychain
  access.
- **`kae account rm <tool> <account>`** — remove a captured account: delete the
  snapshot dir (`accounts/<tool>/<account>`) and every secret-backend item
  (`SecretRef(tool, account, artifact)` under service `kagikae`). Today this is
  manual two-step surgery (`rm -rf` the dir plus `security
  delete-generic-password`), error-prone because it touches the keychain by
  hand. Refuse to remove the **active** account with exit `10`
  (`ExitUnsafeRefused`; **not** `5`/`ExitUnsupported`, which is the OS-support
  code) unless `--force`, which also drops it from `state.json` `active` and
  recomputes the active profile. If any `[profiles]` entry references the
  account (`Profile.Accounts` is a tool→account map), the comment-preserving
  writer (below) **removes the offending `accounts.<tool>` key from each
  profile in the same transaction**, naming the touched profiles in the output —
  `account rm` no longer refuses on a profile reference (the v0.7.0
  dangling-reference trap is gone now that kae can surgically edit
  `config.toml`). Unknown account exits `7`
  (`ExitNotFound`). `--dry-run` prints the plan (including the profile edits)
  and writes nothing. Per-tool lock plus the config lock held throughout.
- **`kae account rename <tool> <old> <new>`** — rename a captured account.
  Secret-backend keys cannot be renamed in place, so copy-then-delete each
  item; move the snapshot dir and metadata; update `state.json` `active[tool]`
  if it pointed at `<old>`. Any `[profiles]` entry referencing `<old>` for
  `<tool>` is **rewritten to `<new>` by the comment-preserving writer (below) in
  the same transaction**, naming the updated profiles in the output — no refuse,
  no manual `kae edit`. Refuse with exit `10` if `<new>` already exists; unknown
  `<old>` exits `7`; sanitize the new name with the existing account-name rule.
  `--dry-run` prints the plan and writes nothing. Per-tool lock plus the config
  lock held throughout.
- **comment-preserving `config.toml` writer** (`internal/config`) — a surgical
  editor that applies key-level mutations (remove a
  `profiles.<name>.accounts.<tool>` entry, rewrite an account value, add or
  remove a whole `[profiles.<name>]` table, set/clear `default_profile`) while
  keeping the file's comments, field order, and unrelated keys intact. Today kae
  writes `config.toml` exactly once — from the `init` string template — and
  every later change is a manual `kae edit`; there is no round-trip writer, so
  this is new infrastructure. **Trap**: `BurntSushi/toml` (the current
  dependency) is Marshal/Unmarshal only and drops every comment on re-encode, so
  a decode-then-encode round-trip would silently strip the template's
  explanatory comments — the writer must do targeted text/AST edits instead.
  Atomic write via `patch.WriteFileAtomic` at `0600`, under the config lock.
  `account rm`/`rename` and every `kae profile` mutation route through it.
- **`kae profile save|set|unset|rm|default`** — manage `[profiles]` entries
  without hand-editing TOML (mirrors the existing `kae env set|unset|list`
  shape, and is the scriptable, validated counterpart to free-form `kae edit`).
  `save <name>` writes or overwrites profile `<name>` from the current
  `state.json` active accounts (snapshot what you are running now);
  `set <name> <tool> <account>` sets one `accounts.<tool>` mapping, creating the
  profile if absent; `unset <name> <tool>` drops one mapping, removing the now-
  empty profile entry if that was its last; `rm <name>` deletes the whole
  profile. The default profile is its own verb so it never collides with the
  per-mapping `set`/`unset`: `default <name>` points `default_profile` at an
  existing profile, bare `default` prints the current one, and
  `default --clear` empties it. Unknown account, tool, or profile exits `7`
  (`ExitNotFound`); the account is validated against the captured snapshots and
  sanitized with the existing account-name rule. `rm` (and an `unset` that
  empties the default) refuses to leave `default_profile` dangling: removing the
  default exits `10` (`ExitUnsafeRefused`) unless `--force`, which clears
  `default_profile`. `--dry-run` prints the plan and writes nothing. Every
  mutation goes through the comment-preserving writer (above) under the config
  lock.
- **doctor keychain-orphan detection (discovery-gated)** — warn when a
  `kagikae` secret item exists with no matching `accounts/<tool>/<account>`
  dir (a leftover from manual cleanup). **Discovery first**: confirm whether
  the secret store can *stably enumerate* all items under service `kagikae`
  (on darwin a single `find-generic-password -s kagikae` returns only the first
  match and `dump-keychain` is heavy/brittle; on Linux `secret-tool search`
  may enumerate cleanly). Record the finding in a discovery note; implement
  only where enumeration is reliable, otherwise defer with the reason written
  down. Scope this release to darwin + keychain backend; note Linux/libsecret
  as a follow-up. With `account rm` shipping in the same release, orphans
  become rare, so this is a nice-to-have, not a gate.

## Non-Goals (this release)

- **Phase 6 (`kae sync`, global isolated mode)** — the highest-risk mode
  (symlink-swaps the real `~/.claude`); deferred to **v0.7.2**. The file-driver
  override here is its safety prerequisite (its real-machine gate can then run
  fully detached from the real login keychain). The `sync` tombstone (Phase 0,
  v0.7.0) spans v0.7.1 before the name is reclaimed in v0.7.2 — comfortably
  past the one-release minimum.
- **Backup back-references are not rewritten** by `account rm`/`rename`. An
  existing backup's `Meta.ActiveBefore` keeps the old account name; rolling
  back to such a backup restores the old name into
  `state.json` while the snapshot no longer exists, so the next `kae use`/
  `apply` errors with "account not captured". Documented limitation; prune the
  affected backups manually if needed.
- TUI, Windows, Codex keyring driver, account auto-detection,
  `env export --dotenv --reveal` — see [ROADMAP.md](ROADMAP.md).
- No automatic network access.

## Acceptance Criteria

- **file-driver override**: with `KAE_CLAUDE_DRIVER=file`, `kae use claude
  <account> --dry-run` on darwin reports a `.credentials.json` file action
  (not a keychain action); unset, darwin keeps the keychain driver (no
  regression). A temp-HOME smoke check switches claude with the override on
  both `kae add` and `kae use`, and asserts the real login keychain is never
  read or written ([docs/VALIDATION.md](docs/VALIDATION.md) updated with the
  procedure).
- **`kae account rm`**: removes the snapshot dir and all secret items; prints a
  confirmation; refuses the active account (exit `10`) without `--force`;
  refuses a profile-referenced account (exit `10`) naming the profiles;
  `--dry-run` writes nothing; unknown account exits `7`.
- **`kae account rename`**: round-trips secret items (copy+delete), moves the
  dir, updates `state.json active[tool]`; refuses (exit `10`), naming the
  profiles, when a profile references `<old>`; refuses an existing `<new>`. A test asserts the renamed
  account resolves via `kae use` after rename.
- **`config.toml` writer**: a programmatic edit (e.g. `kae profile set`)
  preserves the file's leading comments, field order, and unrelated
  `[tools.*]` keys; a round-trip test asserts comments and untouched keys
  survive.
- **`kae profile`**: `save` captures the active accounts into a named profile;
  `set`/`unset` add and remove a single tool mapping (an `unset` of the last
  mapping removes the empty profile); `default <name>` sets `default_profile`
  (unknown profile exits `7`) and `default --clear` empties it; `rm` deletes a
  profile and refuses (exit `10`) to orphan `default_profile` without `--force`;
  unknown account/tool exits `7`; `--dry-run` writes nothing.
- **doctor orphan**: discovery note committed; if implemented, a `kagikae`
  item with no snapshot dir produces a `keychain_orphan` warn-level check, and
  the JSON report keeps `schema_version: 1`.
- `mise run check` passes; JSON keeps `schema_version: 1`, stable tokens, `[]`
  arrays; redaction tests cover any new output path.

## Release Steps

1. Land the file-driver override; smoke check proves real-keychain
   non-interference (this unblocks the v0.7.2 Phase 6 gate).
2. Land the comment-preserving `config.toml` writer (shared dependency), then
   `kae account rm` / `rename`; profile-reference and active-account guards
   (exit `10`) tested; backup back-reference limitation documented.
3. Land `kae profile save|set|unset|rm` on the writer; `default_profile`
   orphan guard (exit `10`) and `--dry-run` tested.
4. doctor orphan: run discovery, then implement or defer with the reason.
5. `docs/VALIDATION.md` v0.7.1 smoke results; README examples verified against
   the built binary.
6. Tag `v0.7.1`, GitHub release.

---

# kae v0.7.0

Bond mode, credential-private per-directory isolation, and the scope×environment
model foundations.

Previous baseline: v0.6.0 (three new adapters — copilot, cursor, opencode —
and pinned-directory guard; see git tag v0.6.0).

## Scope

- **`kae bond [<profile>]`** — new per-directory mode: shares settings,
  sessions, hooks, and memory with the real home, while credentials are
  private to the directory. A denylist approach: everything in the real home
  directory is symlinked except credential files (hard-coded: claude →
  `.credentials.json`; codex → `auth.json`), which are private-copied at
  `0600`. Bond dir is account-agnostic (`isolation/<pin-id>/<tool>/bond/`,
  where pin-id = first 16 hex chars of SHA-256 of the absolute directory
  path), so switching accounts inside a bonded directory does not change the
  dir layout. `kae run --mode bond` also available.
- **`bond_denylist_extra`** config option — per-tool list of extra file names
  to exclude from bond symlinking (on top of the built-in credential list).
  Hard-coded credential artifacts are refused to prevent misconfiguration.
- **`kae sync` → `kae apply` rename (Phase 0)** — completed; old `sync`
  command removed.
- **Paths/constants cleanup (Phase 1)** — `paths.PinID`, `paths.BondDir`,
  and related constants moved to the canonical `internal/paths` package.
- **`/oauthAccount` removal (Phase 3)** — `~/.claude.json`'s `oauthAccount`
  field is no longer switched. Real-machine validation (2026-06-14) confirmed
  it is a token-derived identity cache that claude self-heals; switching it
  risked corrupting live sessions. Claude adapters now declare one artifact
  only (the token). `~/.claude.json` is symlinked wholesale in isolation modes.
- **`kae pin` semantics flip (Phase 4)** — `kae pin` now defaults to fully
  isolated mode (`isolation/<pin-id>/<tool>/pin/<account>/config/`), replacing
  the v0.6.0 overlay default. Opt-in sharing via `tools.<tool>.pin_shared_items`
  (default empty). Legacy overlay-mode blocks are detected and warn on
  `kae pin`; migrate with `kae unpin && kae pin <profile>` (isolated) or
  `kae unpin && kae bond <profile>` (shared). `kae run --mode pin` available.
- **`kae as <tool> <account>` (Phase 5)** — new command: swaps the credential
  inside a bonded or pinned directory to a different account without touching
  settings, sessions, or memory. Bond mode: credential overwritten in the
  account-agnostic bond dir. Pin mode: new per-account config dir prepared,
  `.mise.toml` env entry updated.

## Acceptance Criteria

- `kae bond <profile>` writes `.mise.toml` with `CLAUDE_CONFIG_DIR` /
  `CODEX_HOME` pointing to `isolation/<pin-id>/<tool>/bond/`.
- Bond dir contains symlinks for non-credential real-home items and a
  private copy (`0600`) of the credential file.
- Re-running `kae bond` is idempotent (stale symlinks refreshed, no error).
- Missing credential (not logged in) is silently skipped, not an error.
- `kae run --mode bond ... -- <cmd>` sets the isolation env without mutating
  live state.
- **Real-machine gate**: `kae bond <profile>` in a client directory, then
  `claude -p '' --model haiku`; asserts AUTH-OK inside the directory while
  `~/.claude` remains unchanged. Required before merge to main. On macOS,
  where `CLAUDE_CONFIG_DIR` suppresses keychain access, kae copies the
  keychain credential bytes into the bond dir's `.credentials.json` so
  claude authenticates without touching the real `~/.claude`.
- `mise run check` passes; no regression in existing modes.
- **Phase 3**: `kae use claude <account> --dry-run` reports exactly 1 action
  (the token); `/oauthAccount` never appears in actions.
- **Phase 4**: `kae pin <profile>` writes a pin-mode block
  (`isolation/<pin-id>/claude/pin/<account>/config/`); a legacy overlay-mode
  `.mise.toml` triggers the migration warning. `kae run --mode pin` succeeds.
- **Phase 5**: `kae as claude <account>` inside a bonded directory overwrites
  the credential and prints confirmation. Inside a pinned directory it prepares
  a new config dir and updates the `.mise.toml` env entry.

## Release Steps

1. Pass all acceptance criteria above, including real-machine gate.
2. Update `docs/VALIDATION.md` v0.7.0 smoke-check results.
3. README examples verified against the built binary.
4. Tag `v0.7.0`, GitHub release.

---

# kae v0.6.0

Tool coverage and pin hardening: three new adapters (copilot, cursor,
opencode), the gemini → agy transition, and closing the pinned-directory
semantics gap. Pre-stable: this release removes the gemini adapter (see
Breaking Changes).

Previous baseline: v0.5.0 (the use/pin/run command system and overlay
isolation; see git tag v0.5.0).

## Scope

- **Pinned-directory guard** — inside a pinned directory, `kae use`,
  `kae add`, and `kae apply` refuse with exit `5` and guidance: change the
  directory's accounts with `kae pin <profile>`, or act on the real home
  with the new `--global` flag (which makes the adapters ignore
  kae-managed isolation env vars when resolving base paths). Rationale:
  today such a run splits across three states — the keychain (global),
  the identity file (overlay), and state.json (global belief) — a
  three-way mismatch. Detection reuses the pin context already surfaced
  by `kae status`.
- **gemini removal + agy promotion** (breaking) — upstream retired Gemini
  CLI in favor of Antigravity (2026-05-19); the gemini adapter is removed
  (unknown-tool error; release-notes pointer to agy). agy graduates from
  experimental: pin down the OS-keyring item contract (the default agy
  storage), add structure guards, generate its mise run task, and pass
  real-machine acceptance.
- **copilot adapter** — GitHub Copilot CLI. Auth artifacts: OAuth token in
  the OS keychain (service `copilot-cli`; plaintext `~/.copilot/config.json`
  fallback on keychain-less systems) plus the `~/.copilot/settings.json`
  account state. Discovery first: per-account keychain item layout, the
  interplay with copilot's native `/user switch` (last-used account
  record), and whether the claude verbatim-keychain pattern (capture/
  restore raw bytes via the `security` CLI, ACL-preserving) carries over.
  `kae doctor` gains `env_conflict` checks for `COPILOT_GITHUB_TOKEN` /
  `GH_TOKEN` / `GITHUB_TOKEN`, which outrank the keychain login. The gh
  CLI's own auth is out of scope and untouched (separate storage; lowest-
  priority fallback only).
- **cursor adapter** — Cursor CLI (`cursor-agent`). Browser login with
  locally stored credentials; discovery first (`~/.cursor` artifact
  layout), then the standard switched/preserved allowlist.
- **opencode adapter** — OpenCode. ChatGPT subscription login (native
  since the OpenAI partnership; Claude subscription login was removed
  upstream in 2026-01). Auth state is expected file-based (XDG data
  `auth.json`; discovery first). API-key providers remain env-mode
  territory, as for every tool.
- **`overlay_unshared`** — per-tool exclusions from the built-in overlay
  share list (the mirror of `overlay_extra_shared`); `kae pin` prints
  what it linked and what it skipped so the effective share set is
  visible without reading docs.
- **Remote share-list definitions (design only)** — design loading the
  shared-item defaults from a published definition file so the list can
  follow upstream changes without a kae release. Hard requirements
  already agreed: the auth/identity denylist stays hard-coded, fetching
  is an explicit command (never automatic or at switch time), and the
  diff is shown before adoption. Outcome: a design section in docs, not
  necessarily shipped code.

Implementation order: pinned-directory guard → gemini/agy → copilot →
cursor → opencode → overlay_unshared → remote-definition design. Each
adapter lands behind its own discovery note in ADAPTERS.md before code.

## Non-Goals (this release)

TUI (ROADMAP), Windows, Codex keyring driver, login UX polish,
`env export --dotenv --reveal`, performance polish, claude file-driver
override — see [ROADMAP.md](ROADMAP.md). No automatic network access:
the remote-definition work is design only.

## Breaking Changes

| Removed | Replacement |
|---------|-------------|
| `gemini` tool (adapter, tasks, doctor checks) | `agy` (Antigravity CLI, the upstream successor) |

`kae <cmd> gemini ...` fails as an unknown tool naming agy; captured
gemini accounts remain on disk untouched (manual cleanup, documented in
the release notes).

## Acceptance Criteria

- Inside a pinned directory `kae use <profile>` exits `5` naming
  `kae pin` and `--global`; `kae use --global <profile>` switches the
  real home with state.json consistent (real machine).
- `kae use agy <account>` round-trips with the keyring storage and passes
  the fresh-process auth check; gemini commands fail as unknown tool.
- copilot / cursor / opencode each: `kae add --no-login` → `kae use`
  round-trip with a fresh-process auth check on the real machine, a
  normative switched/preserved table in ADAPTERS.md, and redaction tests
  for any new output path. copilot: doctor flags the token env vars.
- A built-in shared item listed in `overlay_unshared` is not linked by a
  new `kae pin`, and the pin output lists linked/skipped items.
- `mise run check` passes; JSON keeps `schema_version: 1`, stable tokens,
  `[]` arrays.

## Release Steps

1. Bump `toolVersion` (and its test) at cycle start — the gemini removal
   error names v0.6.0, so the binary must agree from the first dev build.
2. Acceptance criteria green; `docs/VALIDATION.md` checklist done (smoke
   uses file-based tools on macOS — keychain warning; copilot smoke needs
   the same care as claude).
3. README examples verified against the built binary.
4. Tag `v0.6.0`, GitHub release with the breaking-changes table.
