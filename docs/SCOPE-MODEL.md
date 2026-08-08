# Scope × Environment Model (design guidance)

> Status: the whole model is **implemented**. v0.7.2 unified the surface into
> two verbs × two flags (`use`/`pin` with `-s`/`-i`) and shipped global isolated
> (`kae use -i`). v0.8.0 then folded `apply` into bare `kae use`, redesigned
> `kae run` onto `-s`/`-i`/`--env` (dropping `--mode`), retired the overlay and
> home mechanisms, and unified the mechanism vocabulary on shared/isolated.
> Normative parts live in DESIGN.md / CLI.md / ADAPTERS.md; this file is
> rationale/history.

## 1. Backbone principle

> **Only the credential follows the account. Sessions and settings follow the
> *sharing set of their scope*, and are never disturbed by a temporary account
> switch.**

"Switching an account" means swapping the credential only (for claude, the
token; `/oauthAccount` is a token-derived cache that was believed to self-heal —
§6 amends that: the self-heal is gated behind a 24h TTL every token refresh
renews, so the cache **is** switched too). Where sessions/settings are shared is an
independent axis. This principle is what keeps the command surface coherent:
each mode decides *only* the sharing set; a separate command (`as`) handles the
credential-only swap.

## 2. The two axes

Every switching mode is one cell of **scope (global / per-directory)** ×
**environment (shared / isolated)**.

| Mode | Scope | Environment | Sessions/settings source | Credential |
|------|-------|-------------|--------------------------|------------|
| **auth** | global | shared | real home (in place) | real-home pointer patch |
| **sync** | global | isolated | per-account (none shared) | per-account, private |
| **bond** | per-directory | shared | real home (symlinked) | per-account, private to the dir |
| **pin** | per-directory | isolated | per-directory store (none shared by default) | per-account, private to the dir |

`auth` is the implemented default mode and is kept as-is. `sync`, `bond`, and
`pin` are new.

## 3. Command surface

Two verbs by scope, two flags by environment (the v0.7.2 unification):

| Command | Scope | Environment | Role |
|---------|-------|-------------|------|
| `kae use [-s] <profile \| tool account>` | global | shared | switch every terminal in place |
| `kae use -i <profile \| tool account>` | global | isolated | point every terminal at a per-account private home via a kae-owned global mise fragment |
| `kae pin [-s] <profile \| tool account>` | per-directory | shared | bind a directory; sessions/settings shared with the real home, credential private |
| `kae pin -i <profile \| tool account>` | per-directory | isolated | bind a directory; nothing shared by default, opt-in shares |
| `kae use` (bare, no positional) | global | shared | idempotent re-apply for the enter hook (resolves the profile; the folded `apply`) |
| `kae run [-s\|-i\|--env] … -- <cmd>` | per-process | shared / isolated / env | child process only; `-s` restores afterwards, except for a tool whose live credential the restore would supersede ([CLI.md](CLI.md) § kae run Semantics), `-i` uses the global isolated home, `--env` injects env-profile vars |
| `kae relogin [<tool>]` | per-directory | (the binding's) | child process only, and it switches nothing: it runs the tool's own login flow with this directory's isolation variable exported, so the login lands in the store the binding points at rather than in the real home, then captures it back into the account snapshot ([CLI.md](CLI.md) § kae relogin Semantics) |

Both `use` and `pin` default to `-s`/`--shared`; `-i`/`-s` are short for
`--isolated`/`--shared`, and `u`/`p` for `use`/`pin`. Re-binding one tool in a
bound directory is `kae pin <tool> <account>`.

### Naming notes (history)

- v0.7.2 collapsed four verbs into two: `bond` → `pin --shared`,
  `as <tool> <account>` → `pin <tool> <account>`, and the global isolated mode
  (once planned as a reclaimed `kae sync`) became `use --isolated`. The
  `--global` flag was dropped because `use` is inherently global.
- Earlier history: v0.7.0 renamed the idempotent re-apply `kae sync` → `kae
  apply` and flipped `kae pin` from a partial-share overlay to per-directory
  isolation (now `pin -i`); v0.7.2's `pin` default of shared (`pin -s`) is the
  v0.7.0 `bond` mechanism.

## 4. In-directory account swap (`as` → `pin <tool> <account>`)

(v0.7.2 folded `kae as` into `kae pin <tool> <account>`; the semantics below are
unchanged.) Re-binding one tool in a bound directory changes the account that
directory is bound to: it swaps **only the credential** and the identity pointer of
a mixed-state file (§6), leaving the sharing set untouched.

- **Persists within the directory** and survives re-entry.
- **Does not leak outside the directory**: the isolation env var is
  directory-scoped (mise sets it on enter, unsets it on leave), so leaving the
  directory naturally reverts to whatever the outer scope had.
- This is the in-directory account-change path. The pinned-directory guard that
  refuses a globally-scoped `kae use` inside a bound directory (exit `5`, since
  v0.6.0) stays; `as` is the sanctioned alternative there.

## 5. Shared mechanism

All per-directory and global-isolated modes are one mechanism with different
parameters:

1. **Point the tool at an alternate config dir via its isolation env var**
   (claude `CLAUDE_CONFIG_DIR`, codex `CODEX_HOME`) — and, for a tool that can
   address its credential separately, at the account's credential store via a
   second variable (claude `CLAUDE_SECURESTORAGE_CONFIG_DIR`), so the sessions are
   the directory's and the credential is the account's. For per-directory modes
   these are mise `[env]` entries, so the scope is the directory automatically
   (set on enter, unset on leave; never touches global live state). For `sync`
   it is a global pointer (see §10).
2. **Symlink the sharing set** into the alternate dir — from the real home
   (`bond`) or from a per-directory store (`pin`).
3. **Materialise the credential and mixed-state files privately** (never
   symlinked — see §6), **reading each store before writing over it**: a private
   copy is not a free copy, because the tool refreshes the one inside the directory
   and for claude that makes every older copy invalid, so a newer copy is harvested
   into the account snapshot first ([CLI.md](CLI.md) § kae pin). A new mechanism
   inherits that only by routing through the same materializer (AGENTS.md).

The only differences between modes are the **sharing source** (real home vs
per-directory store) and the **default sharing set**:

- **bond** = *denylist*: share everything from the real home *except* the auth
  artifacts. The denylist is **hard-coded per tool** (claude `.credentials.json`
  — Linux only; on macOS the credential is keychain-only so there is no file to
  exclude; codex `auth.json`), not a dynamic scan. Unknown new files are shared
  (consistent with "same environment as global"); a newly discovered credential
  file must be added to the denylist *and* to the config-load refusal list in
  the same commit.
- **pin** = *opt-in*: share nothing by default; the user adds specific
  files/directories via config. (This replaces today's fixed
  `settings.json`/`skills` allowlist.)

## 6. Mixed-state files

Adapters declare their auth artifacts as `Target` + optional `Pointer`. The
easy case is a **whole-file/whole-store auth** artifact (codex `auth.json`,
claude keychain / Linux `.credentials.json`): it is **private** (never
symlinked), and its containing store has no shared content, so nothing is lost.

"Private" is a property of the *store*, and whether the store can be private at
all is the tool's decision, not kae's. claude namespaces its keychain item by a rule
the adapter owns (docs/ADAPTERS.md § "Credential storage resolution"), and since
v0.17.0 kae uses the second variable in that rule to make the credential the
**account's** rather than the directory's — so a bound directory's sessions,
settings and identity are private to it while its credential is shared with every
other directory bound to the same account, on purpose: copies of one credential
invalidate each other. codex scopes its `Codex Auth` item by an **account**
derived from `CODEX_HOME` rather than by the service name — so the store *can* be
private, but kae has not verified a bound directory end to end there and does not
declare the capability: it warns and writes nothing rather than assuming
(docs/ADAPTERS.md "Per-directory credential store"). Assuming a store is private
because kae pointed an env var at a directory is what let a pinned claude
directory silently keep running the previous account.

The hard case is an auth value **embedded in an otherwise-shareable file**
(claude `~/.claude.json` `/oauthAccount`, which sits alongside `projects`,
`mcpServers`, project trust, etc.).

**Goal: the non-auth parts of such a file must stay *live-shared* with the real
home, not snapshotted.** A snapshot (copy at bond/pin time) drifts — an mcp
server or a trusted project added in the real home would be invisible in the
directory, and vice versa — which is exactly the confusing state we want to
avoid.

**Resolved by real-machine validation (2026-06-14, claude):** `/oauthAccount`
is **not an auth artifact**. Auth comes from the **token alone**: removing
`/oauthAccount` entirely, or injecting a wrong-account `/oauthAccount`, still
gives a fresh-process `AUTH-OK`. That half still holds.

**Amended by real-machine validation (2026-07-29, Claude Code 2.1.220):** the
*self-heal* half does not hold **unconditionally**. It is real but TTL-gated, and
the gate is long enough relative to how often a credential in daily use refreshes
that the cache is effectively never stale — so a switched token leaves the
previous account's `emailAddress` in place indefinitely, and kae's switch of
`/oauthAccount` is what makes the identity correct *immediately*.

What that looked like on the machine, which is the part this document is for:
`kae` had switched the login while Claude Code kept displaying — and reporting to
`kae add` — the old account. The keychain token resolved to account A via
`api.anthropic.com/api/oauth/profile` while `~/.claude.json` still named account
B, with `profileFetchedAt` refreshed the same day.

**Which write renews which field, and the two consequences that follow** — that a
switched identity's lifetime is its snapshot's, and that the payload is comparable
on the identifying keys only and never byte for byte — are normative in
[ADAPTERS.md](ADAPTERS.md) § claude, beside the switched/preserved allowlist the
code has to match. They were restated here in full until 2026-08-08, which is two
copies of one upstream measurement in a repository that has been bitten by exactly
that; this section keeps the finding and the design decision, and that one keeps
the rules.

Therefore the design is: **the token stays claude's sole auth artifact, and
`/oauthAccount` is switched alongside it as a second, identity-only artifact**
(`oauth_account`, patched by JSON pointer). The mixed-state goal above is
unaffected: a pointer patch rewrites one key, so `projects`, `mcpServers` and
trust state stay live-shared. When the target turns out to be a symlink,
`ApplyLive` resolves it before reading and writing, so the atomic rename lands on
the linked file instead of forking it into a private copy.

Scope of the fix — **every mode**. Isolation modes point `CLAUDE_CONFIG_DIR` at a
kae-owned directory, and the identity cache claude uses there is
`<dir>/.claude.json`, so that is where a per-directory bind writes the bound
account's cache (`writeDirIdentity`, after the credential).

**And that is where the live-sharing goal above stops applying, because in a bound
directory it cannot hold.** The goal is about the *global* switch, where a pointer
patch rewrites one key of the real home's file and everything else stays exactly as
found. A bound directory is the opposite situation: it exists to name a different
account than the real home, and the file that records which account that is also
holds `projects`, `mcpServers`, trust state and whatever claude adds next. One file
cannot be live-shared with the real home *and* say something different in this
directory. So in a per-directory bind the file is **private**, by denylist
([ADAPTERS.md](ADAPTERS.md) "Per-directory shared bind").

What that gives up is less than it sounds, and how much depended on a setting rather
than on any decision — the mechanics are in [ADAPTERS.md](ADAPTERS.md) "Identity
cache", which is the normative site for what claude switches and preserves. In
scope-model terms the consequence is: a bond dir no longer inherits the real home's
`projects` / `mcpServers` / trust state for that one file, so it starts from claude's
defaults there and a trust prompt reappears once per bound directory. Session history
is unaffected — it lives in `projects/` under the tool home and is still symlinked.

The symlink-following rule above still matters as a **guard**. Following a link is
right for a global switch and wrong for a bind, where it would relabel the real home
with one directory's account, so `identityTargetEscapes` declines any per-directory
identity write resolving outside the store and warns; the credential still lands.
With the file denied it should not normally fire, and it is kept because the hazard
is severe and reachable by other routes — a hand-made link, an
`isolated_shared_items` entry naming the file, a future tool kae has not denied.

Consequence to document: where the cache *is* shared with the real home, its
`/oauthAccount` names whichever account `kae use` applied last. Its self-heal is
TTL-gated, so kae is what corrects it promptly — running claude corrects it only
once the applied `profileFetchedAt` is over 24h old.

**Fallback — copy+patch (not needed for claude).** The validation above removes
the need for this for claude. It is retained only for a hypothetical future
tool whose auth pointer is *not* token-derived (i.e. genuinely authoritative and
not self-healed): copy the real file into the alternate dir and overwrite just
the auth pointer. A plain snapshot drifts, so if this path is ever taken it
should be paired with a sync strategy, in increasing cost/complexity:

- **(a) enter/leave hook sync (no daemon — preferred fallback):** on directory
  entry copy real→dir (re-patching the auth pointer), on exit merge dir→real
  excluding the auth pointer. Realised with mise hooks; in-session real-home
  changes land on the next entry ("boundary sync"). Fits kae's no-daemon CLI
  design.
- **(b) tool-launch wrap:** sync before/after a kae-spawned tool process; misses
  changes when the tool is launched directly.
- **(c) watcher daemon:** true live sync, but a resident process conflicts with
  the CLI-only design.

All three carry a second, harder problem: **bidirectional merge conflicts** (if
`~/.claude.json` is edited on both sides, a 3-way JSON merge and race handling
are needed). This complexity is the reason live symlink sharing (this section)
is strongly preferred — it makes the whole sync question moot. copy+patch with
hook sync (a) is the fallback only if the §11 validation rules out symlinking.

**This fork is settled for authentication** (§11): the real-machine validation
proved the token is claude's sole auth artifact, so no copy+patch and no sync
strategy is needed to keep a directory authenticated. What is *not* settled is
identity propagation into isolation modes — the cache there is normally private
to the directory, and how (or whether) kae should switch it is the open question
in §6 and [ROADMAP.md](ROADMAP.md). The caution above still applies to any future
tool whose auth pointer is *not* token-derived — given claude's proven
sensitivity to auth-payload consistency, verify with a fresh-process auth check,
never assume.

The actual conversation history lives in separate files (claude:
`~/.claude/projects/`), so session continuity is achieved by symlinking those
directories regardless of how the `.claude.json` auth pointer is handled.

**Implementation note — `.claude.json` path under `CLAUDE_CONFIG_DIR`.** The claude
adapter resolves `.claude.json` *inside* the config dir when `CLAUDE_CONFIG_DIR` is
set (`claudeJSONPath()`), not at `~/.claude.json`. The real-machine validation above
was run against the real-home `~/.claude.json`.

This is why the file's sharing used to depend on a setting rather than on a
decision, and it is worth keeping straight because the two configurations look
identical from the outside. `prepareBond` links the entries of
`app.realToolHome(tool)`, which is the user's own `CLAUDE_CONFIG_DIR` when they set
one and `~/.claude` otherwise. With it unset, `$HOME/.claude.json` is that
directory's *sibling* and is never enumerated, so the bond dir always held a private
copy. With it set, the file is an entry of the real home and was linked into every
bond dir. It is now on the hard-coded denylist for both, which is what §6 describes;
do not restore the sharing on the strength of this note.

## 7. Applicability

The per-directory binds (`pin -s`/`-i`) and the global isolated home
(`use -i` / `run -i`) all require a home-isolation env var, so they apply to
**claude and codex only**. Tools without one (agy, opencode, cursor, copilot)
support **global shared (`kae use`) and `kae run --env` only** — there is no way
to make their credential private without redirecting their home. For a `-i`
*profile* that also maps such a tool, it is skipped with a warning (claude/codex
stay isolated); a single explicit unsupported tool exits `5`.

That split is now a stated scope decision rather than only a consequence of what
has been measured: claude and codex are **tier 1** and the other four are **tier 2**,
defined normatively in [DESIGN.md](DESIGN.md) § Tool Tiers. Read it before treating
a tier-2 tool's missing isolation as a gap to close — the mechanism in §5 would
work for any tool with a verified isolation env var, and copilot has one, but
building it is demand-gated. What the tier does *not* change is any refusal: an
unmeasured store still gets a warning rather than a guessed artifact, at either
tier.

## 8. Migration / breaking changes

> Historical record; the current migration notes live in
> [RELEASE.md](RELEASE.md). v0.8.0 folded `apply` into bare `kae use` (the
> `apply` pointer replaces the long-gone `sync` one), removed `run --mode`,
> retired overlay/home, and renamed the config keys
> (`bond_denylist_extra`→`shared_denylist_extra`,
> `pin_shared_items`→`isolated_shared_items`; `overlay_`/`home_` keys removed).

- `kae sync` (idempotent re-apply) → **`kae apply`** (v0.7.0), then `apply` →
  bare **`kae use`** (v0.8.0). Each kept a one-release exit-`64` pointer.
- `kae pin`'s default behaviour flips from *partial share* (overlay) to
  *isolation*; `kae bond` is the new sharing command. Existing pinned
  directories' `.mise.toml` blocks must be re-rendered.
- The `OverlayDir(tool, account)` store key (account-keyed) moves to a
  `pin-id`-keyed layout (§9) so a directory's sessions can be shared across the
  accounts used inside it.

## 9. Store layout

`pin-id` = SHA-256 of the bound directory's absolute path, hex, truncated to 16
chars (stable, deterministic, rename-proof). All stores live under
`isolation/<pin-id>/` (per-directory) and `isolation/global/` (global isolated).
No copy+patch anywhere — the mixed-state finding (§6) removed that need.

> v0.7.2 update: the path segments below were renamed for clarity and the
> global home moved under `isolation/` — `bond/` → `shared/`,
> `pin/<account>/` → `isolated/<account>/`, `synchomes/<tool>/<account>/` →
> `isolation/global/<tool>/<account>/`. Isolation is now delivered by a
> kae-owned mise fragment (`.config/mise/conf.d/kagikae.toml`), not by editing
> `mise.toml` or swapping `~/.claude`. See [DATA-MODEL.md](DATA-MODEL.md) and
> [RELEASE.md](RELEASE.md) for the current layout; the rest of this section is
> the original v0.7.0 plan.

The current per-mode paths, the segment names (`shared` / `isolated` / `global`),
and the kae-owned mise-fragment delivery (a fragment merged by mise, **not** a
`~/.claude` symlink-swap) are normative in [DATA-MODEL.md](DATA-MODEL.md); this
section keeps only the `pin-id` rationale above. The original v0.7.0 per-mode
store sketch was removed in the v0.7.2 fold-down (git log).

## 10. Global isolated home pointer (`kae use -i`)

To make a global isolated home visible to every terminal **without touching the
real `~/.claude`**, point the tool at a kae-managed
`isolation/global/<tool>/<account>/` via a kae-owned global mise fragment
(`~/.config/mise/conf.d/kagikae.toml`) exporting `CLAUDE_CONFIG_DIR` /
`CODEX_HOME`. mise re-evaluates env on every prompt and directory change, so the
change reaches all mise-activated terminals on their next prompt — close to the
immediacy of swapping the home, without the risk. The teardown is `kae use -s`
(or bare `kae use`): drop the tool from `state.synced`, regenerate or delete the
fragment, then switch the real home in place. (An earlier design symlink-swapped
`~/.claude` itself; dropped as too risky — claude's auth fragility is proven.)
Real-machine fresh-process auth validation remains a release gate (see
[VALIDATION.md](VALIDATION.md)).

## 11. Open questions

### Blocking fork — claude mixed-state behaviour — RESOLVED (2026-06-14, amended 2026-07-29)

Settled by real-machine validation (keychain untouched; `~/.claude.json` edited
and restored; each step a fresh-process `claude -p … --model haiku` auth check):

1. **Token only (no `/oauthAccount`)** → `AUTH-OK`. Auth needs the token only.
2. **Token vs wrong `/oauthAccount`** → `AUTH-OK`. Token wins.
3. **claude rewrites `/oauthAccount` on startup** = only when its cached copy is
   stale, which in daily use it never is (amended 2026-07-29, §6): the refetch is
   skipped under a 24h `profileFetchedAt` TTL that every token refresh renews
   without rewriting `emailAddress`. Past that TTL the refetch does happen and
   does write the email, so the self-heal is late, not absent.

Outcome: `/oauthAccount` is not an auth artifact (1 and 2 are permanent), and its
self-heal is too late to rely on, so §6 resolves to "the token is claude's sole
auth artifact; `/oauthAccount` is switched alongside it as an identity-only
pointer patch in auth mode, and isolation modes keep a copy of their own — one per
account since v0.17.0, shared by every directory bound to it". copy+patch is
not needed for claude.

## 12. Implementation status

The phased plan that drove this model is fully implemented (v0.7.0–v0.7.2). The
per-commit history is the source of truth (git log) and the release-level record
is in [RELEASE.md](RELEASE.md); current behavior, flags, layout, and contracts
live in DESIGN.md / CLI.md / ADAPTERS.md / DATA-MODEL.md. The remaining
surface-vocabulary alignment (`run`/`apply`/`mise init`) is tracked in
[ROADMAP.md](ROADMAP.md).
