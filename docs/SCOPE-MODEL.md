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
token; `/oauthAccount` is a token-derived cache that claude self-heals — see
§6, so it is *not* switched). Where sessions/settings are shared is an
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
| `kae run [-s\|-i\|--env] … -- <cmd>` | per-process | shared / isolated / env | child process only; `-s` restores afterwards, `-i` uses the global isolated home, `--env` injects env-profile vars |

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
directory is bound to: it swaps **only the credential** (and the auth pointer of
any mixed-state file), leaving the sharing set untouched.

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
   (claude `CLAUDE_CONFIG_DIR`, codex `CODEX_HOME`). For per-directory modes
   this is a mise `[env]` entry, so the scope is the directory automatically
   (set on enter, unset on leave; never touches global live state). For `sync`
   it is a global pointer (see §10).
2. **Symlink the sharing set** into the alternate dir — from the real home
   (`bond`) or from a per-directory store (`pin`).
3. **Materialise the credential and mixed-state files privately** (never
   symlinked — see §6).

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

The hard case is an auth value **embedded in an otherwise-shareable file**
(claude `~/.claude.json` `/oauthAccount`, which sits alongside `projects`,
`mcpServers`, project trust, etc.).

**Goal: the non-auth parts of such a file must stay *live-shared* with the real
home, not snapshotted.** A snapshot (copy at bond/pin time) drifts — an mcp
server or a trusted project added in the real home would be invisible in the
directory, and vice versa — which is exactly the confusing state we want to
avoid.

**Resolved by real-machine validation (2026-06-14, claude):** `/oauthAccount`
is **not an auth artifact** — it is a token-derived identity cache. Verified:

- claude authenticates from the **token alone**: removing `/oauthAccount`
  entirely, or injecting a wrong-account `/oauthAccount`, still gives a
  fresh-process `AUTH-OK`.
- claude **re-derives** the identity (`emailAddress`, org fields) from the
  token on startup and writes it back into `~/.claude.json`.

Therefore the design is: **treat the token (keychain / `.credentials.json`) as
claude's sole auth artifact, and do not switch `/oauthAccount`.** Then
`~/.claude.json` carries no auth value and is **symlinked wholesale** like any
other shared file. The mixed-state problem disappears.

Consequence to document: in `bond` (shared with the real home), running claude
as the directory's account makes claude rewrite the shared file's
`/oauthAccount` to that account. This is **cosmetic and self-healing** — auth is
unaffected (token wins), and the next claude run in the real home re-derives the
real-home account. In `pin` (`.claude.json` not shared with the real home) there
is no pollution at all.

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

**This fork is settled** (§11): the real-machine validation proved the token is
claude's sole auth artifact, so live symlink sharing is used and copy+patch is
not needed. The caution still applies to any future tool whose auth pointer is
*not* token-derived — given claude's proven sensitivity to auth-payload
consistency, verify with a fresh-process auth check, never assume.

The actual conversation history lives in separate files (claude:
`~/.claude/projects/`), so session continuity is achieved by symlinking those
directories regardless of how the `.claude.json` auth pointer is handled.

**Implementation note — `.claude.json` path under `CLAUDE_CONFIG_DIR`.** The
claude adapter resolves `.claude.json` *inside* the config dir when
`CLAUDE_CONFIG_DIR` is set (`claudeJSONPath()`), not at `~/.claude.json`. The
real-machine validation above was run against the real-home `~/.claude.json`.
In `bond` this is handled by the denylist policy: `.claude.json` is not in the
denylist, so `<config-dir>/.claude.json` is a symlink to the real
`~/.claude.json` like any other shared file. The symlink target must be the
*real* home (use the same self-reference guard that protects `.claude/`
contents).

## 7. Applicability

The per-directory binds (`pin -s`/`-i`) and the global isolated home
(`use -i` / `run -i`) all require a home-isolation env var, so they apply to
**claude and codex only**. Tools without one (agy, opencode, cursor, copilot)
support **global shared (`kae use`) and `kae run --env` only** — there is no way
to make their credential private without redirecting their home. For a `-i`
*profile* that also maps such a tool, it is skipped with a warning (claude/codex
stay isolated); a single explicit unsupported tool exits `5`.

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

### Blocking fork — claude mixed-state behaviour — RESOLVED (2026-06-14)

Settled by real-machine validation (keychain untouched; `~/.claude.json` edited
and restored; each step a fresh-process `claude -p … --model haiku` auth check):

1. **Token only (no `/oauthAccount`)** → `AUTH-OK`. Auth needs the token only.
2. **Token vs wrong `/oauthAccount`** → `AUTH-OK`; claude re-derived
   `emailAddress` from the token (self-healed). Token wins.
3. **claude rewrites `/oauthAccount` on startup** = yes, from the token.

Outcome: `/oauthAccount` is a token-derived cache, not an auth artifact. §6
resolves to "token is claude's sole auth artifact; `.claude.json` is symlinked
wholesale; the cosmetic `/oauthAccount` thrash in `bond` self-heals". copy+patch
is not needed for claude.

## 12. Implementation status

The phased plan that drove this model is fully implemented (v0.7.0–v0.7.2). The
per-commit history is the source of truth (git log) and the release-level record
is in [RELEASE.md](RELEASE.md); current behavior, flags, layout, and contracts
live in DESIGN.md / CLI.md / ADAPTERS.md / DATA-MODEL.md. The remaining
surface-vocabulary alignment (`run`/`apply`/`mise init`) is tracked in
[ROADMAP.md](ROADMAP.md).
