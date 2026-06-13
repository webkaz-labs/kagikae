# Scope × Environment Model (design guidance)

> Status: **design guidance for a future release** (post-v0.6.0). This document
> records the agreed account-switching model. It is not yet implemented; the
> implemented modes are described in [DESIGN.md](DESIGN.md). When this model
> lands, fold the normative parts into DESIGN.md / CLI.md / ADAPTERS.md and
> reduce this file to a rationale/history note.

## 1. Backbone principle

> **Only the credential follows the account. Sessions and settings follow the
> *sharing set of their scope*, and are never disturbed by a temporary account
> switch.**

"Switching an account" means swapping the credential only (for claude: the
token *and* `~/.claude.json`'s `/oauthAccount`). Where sessions/settings are
shared is an independent axis. This principle is what keeps the command surface
coherent: each mode decides *only* the sharing set; a separate command (`as`)
handles the credential-only swap.

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

| Command | Scope | Environment | Role |
|---------|-------|-------------|------|
| `kae use <profile \| tool account>` | global | shared (auth) | switch every terminal (current behaviour) |
| `kae apply` | global | shared (auth) | idempotent re-apply for the enter hook (the command renamed from today's `kae sync`) |
| `kae sync <account>` | global | isolated | switch a global, per-account isolated home |
| `kae bond <profile>` | per-directory | shared | bind a directory to a shared environment (sessions/settings shared with the real home; credential private) |
| `kae pin <profile>` | per-directory | isolated | bind a directory to an isolated environment (nothing shared by default; opt-in shares) |
| `kae as <tool> <account>` | per-directory | — | inside a bonded/pinned directory, swap only the credential (persists within the dir) |
| `kae run [--mode M] … -- <cmd>` | per-process | any | child process only; restored afterwards (current behaviour) |

### Naming notes

- Today's `kae sync` (idempotent re-apply called by the mise enter hook) is
  **renamed to `kae apply`**; the name `sync` is reused for the new global
  isolated mode.
- `kae pin` **changes meaning**: it was per-directory binding defaulting to a
  partial-share overlay; it now means per-directory **isolation**, while the
  new `kae bond` covers per-directory **sharing**. This is a breaking change
  (see §8).

## 4. `as` semantics

`kae as <tool> <account>` changes the account a bonded/pinned directory is
bound to: it swaps **only the credential** (and the auth pointer of any
mixed-state file), leaving the sharing set untouched.

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
  artifacts. Unknown new files are shared (consistent with "same environment as
  global").
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
are needed). This complexity is the reason live symlink sharing (§6.1/§6.2) is
strongly preferred — it makes the whole sync question moot. copy+patch with
hook sync (a) is the fallback only if the §11 validation rules out symlinking.

**Which shape is correct is a fork that depends on claude's runtime behaviour
and must be settled by real-machine validation (§11) before this model is
finalised.** Given claude's proven sensitivity to auth-payload consistency, do
not assume; verify with a fresh-process auth check.

The actual conversation history lives in separate files (claude:
`~/.claude/projects/`), so session continuity is achieved by symlinking those
directories regardless of how the `.claude.json` auth pointer is handled.

## 7. Applicability

`bond`, `pin`, and `sync` all require an isolation env var, so they apply to
**claude and codex only**. Tools without a stable isolation env var (agy,
opencode, cursor, copilot) support **`auth` (and `run --mode env`) only** —
there is no way to make their credential private without redirecting their home.

## 8. Migration / breaking changes

- `kae sync` (idempotent re-apply) → **`kae apply`**. Keep `kae sync` as a
  removed-command pointer for one release (exit `64` naming `apply`), matching
  the gemini→agy precedent.
- `kae pin`'s default behaviour flips from *partial share* (overlay) to
  *isolation*; `kae bond` is the new sharing command. Existing pinned
  directories' `.mise.toml` blocks must be re-rendered.
- The `OverlayDir(tool, account)` store key (account-keyed) moves to a
  `pin-id`-keyed layout (§9) so a directory's sessions can be shared across the
  accounts used inside it.

## 9. Store layout (proposed)

`pin-id` = a stable hash of the bound directory's absolute path.

- **bond**: config dir `isolation/<pin-id>/<account>/`; sessions/settings are
  symlinks into the **real home**; the credential is private; mixed-state files
  are copy+patched.
- **pin**: shared sessions/settings live in `isolation/<pin-id>/shared/`; the
  credential lives in `isolation/<pin-id>/<account>/`; the config dir composes
  the two (symlink the shared store + the account's credential). Sessions are
  therefore shared across accounts used in the same directory, isolated from
  the real home.

## 10. `sync` global pointer (proposed)

To make a global isolated home visible to every terminal, swap the tool home
itself: make `~/.claude` (etc.) a symlink to a kae-managed
`homes/<account>/` and re-point it on `kae sync <account>`. This is immediate
for all terminals, unlike a shell-rc export that only affects new shells.
Symlinking the tool home is behaviourally risky (claude's auth fragility is
proven), so **real-machine fresh-process auth validation is a release gate**
for this mode (see [VALIDATION.md](VALIDATION.md)).

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

### For the implementation blueprint

- Exact per-tool session/settings path enumeration for the `bond` denylist.
- `pin` opt-in share config schema (per-tool bare names vs absolute paths;
  the auth/identity denylist stays hard-coded and refused at config load).
- Whether `sync` needs its own idempotent `apply`-style hook integration.
- Concurrency: `bond`/`pin`/`sync` allow concurrent accounts (separate homes);
  confirm the per-tool lock scope under the new layout.
