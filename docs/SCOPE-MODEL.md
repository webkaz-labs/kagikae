# Scope × Environment Model (design guidance)

> The model is implemented. Normative parts live in PRODUCT.md / CLI.md /
> ADAPTERS.md / DATA-MODEL.md; this file keeps only the reasoning behind them.
> The surface it describes reached its current shape over v0.7.0–v0.8.0, and
> git log is where that sequence is.
>
> **The section numbers have gaps and must keep them.** Other documents and
> product code cite this file's sections by number — `git grep -n
> 'SCOPE-MODEL\.md[`)]* §'` lists them, `internal/cmd/dircred.go` included. A
> bare `§N` is not a form `docs-check` resolves, so renumbering would repoint
> every one of them at the wrong section in silence. Removing a section leaves
> the numbers of the rest as they are.

## 1. Backbone principle

> **Only the credential follows the account. Sessions and settings follow the
> *sharing set of their scope*, and are never disturbed by a temporary account
> switch.**

"Switching an account" means swapping the credential only (for claude, the
token; `/oauthAccount` is a token-derived cache that was believed to self-heal —
§6 amends that: the self-heal is gated behind a 24h TTL every token refresh
renews, so the cache **is** switched too). Where sessions/settings are shared is an
independent axis. This principle is what keeps the command surface coherent:
each mode decides *only* the sharing set, and `kae pin <tool> <account>` handles
the credential-only swap inside a bound directory.

## 5. Shared mechanism

All per-directory and global-isolated modes are one mechanism with different
parameters:

1. **Point the tool at an alternate config dir via its isolation env var**
   (claude `CLAUDE_CONFIG_DIR`, codex `CODEX_HOME`) — and, for a tool that can
   address its credential separately, at the account's credential store via a
   second variable (claude `CLAUDE_SECURESTORAGE_CONFIG_DIR`), so the sessions are
   the directory's and the credential is the account's. For per-directory modes
   these are mise `[env]` entries, so the scope is the directory automatically
   (set on enter, unset on leave; never touches global live state). For the
   global isolated home it is a global pointer (see §10).
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
is strongly preferred — it makes the whole sync question moot.

**This fork is settled for authentication**: the real-machine validation
([VALIDATION.md](VALIDATION.md) § Upstream Behaviour Assumptions, which carries
both findings with the procedure that re-runs them)
proved the token is claude's sole auth artifact, so no copy+patch and no sync
strategy is needed to keep a directory authenticated. Identity propagation into
isolation modes was the other open question, and it is settled too, in v0.16.0:
the bound directory gets its own private copy of the cache, for the reason given
above (`writeDirIdentity`; [ADAPTERS.md](ADAPTERS.md) § Per-directory shared bind
is normative for the denylist). The caution above
still applies to any future tool whose auth pointer is *not* token-derived — given
claude's proven sensitivity to auth-payload consistency, verify with a
fresh-process auth check, never assume.

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
defined normatively in [PRODUCT.md](PRODUCT.md) § Tool Tiers. Read it before treating
a tier-2 tool's missing isolation as a gap to close — the mechanism in §5 would
work for any tool with a verified isolation env var, and copilot has one, but
building it is demand-gated. What the tier does *not* change is any refusal: an
unmeasured store still gets a warning rather than a guessed artifact, at either
tier.

## 9. Store layout

`pin-id` = SHA-256 of the bound directory's absolute path, hex, truncated to 16
chars (stable, deterministic, rename-proof). All stores live under
`isolation/<pin-id>/` (per-directory) and `isolation/global/` (global isolated).
No copy+patch anywhere — the mixed-state finding (§6) removed that need.

The current per-mode paths, the segment names (`shared` / `isolated` / `global`),
and the kae-owned mise-fragment delivery (a fragment merged by mise, **not** a
`~/.claude` symlink-swap) are normative in [DATA-MODEL.md](DATA-MODEL.md); this
section keeps only the `pin-id` rationale above.

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
