# Scope × Environment Model (design guidance)

> Status: **Phases 0–5 implemented** (v0.7.0). Phase 0 (kae sync → kae apply
> rename), Phase 1 (paths/constants), Phase 2 (kae bond), Phase 3 (/oauthAccount
> removal), Phase 4 (kae pin semantics flip), Phase 5 (kae as) are complete.
> Phase 6 (kae sync global isolated mode) deferred to v0.8.0+ (tombstone
> constraint: must span at least one release after Phase 0). Phase 7 (docs
> fold-down) is this update. Normative parts have been folded into DESIGN.md /
> CLI.md / ADAPTERS.md; this file now serves as rationale/history.

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

## 9. Store layout

`pin-id` = SHA-256 of the bound directory's absolute path, hex, truncated to 16
chars (stable, deterministic, rename-proof). All new stores live under
`isolation/<pin-id>/` (per-directory) and `synchomes/` (global isolated). No
copy+patch anywhere — the mixed-state finding (§6) removed that need.

- **bond**: config dir `isolation/<pin-id>/<tool>/bond/` is **account-agnostic**;
  it holds symlinks to the real home (everything except the hard-coded denylist)
  plus the **current account's credential materialised privately** (Linux claude
  `.credentials.json` / codex `auth.json` copied from the account snapshot; macOS
  claude needs no credential file — keychain). `.claude.json` is a symlink to the
  real home. `kae as` swaps **only** the credential file in place — the env var
  (`CLAUDE_CONFIG_DIR`) does not change. This is exactly the backbone principle:
  only the credential follows the account.
- **pin**: opt-in shared items live in `isolation/<pin-id>/<tool>/pin/shared/`
  (dir-keyed, shared across accounts used in the same directory); the private
  credential lives in `isolation/<pin-id>/<tool>/pin/<account>/cred/`; the config
  dir composes the two (symlink the shared store + the account's credential).
  Isolated from the real home; nothing shared unless opted in.
- **sync** (global isolated): `synchomes/<tool>/<account>/` is a full private
  tool home per account; `~/.claude` (resp. `~/.codex`) becomes a symlink to it
  (§10).

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

## 12. Implementation plan (phased)

Dependency-ordered; each phase is independently shippable and reviewable. File
paths are relative to the repo root. Tests use `t.TempDir()` HOME/XDG roots
(AGENTS.md); JSON-contract tokens live in `internal/constants`.

### Phase 0 — rename `sync` → `apply`; tombstone old `sync`

- `internal/cmd/sync.go`: rename exported `CmdSync` → `CmdApply` (body
  unchanged: `syncReport`, `buildSync`, `printSyncReport` stay). Add `CmdSync`
  stub → `removedCommand("sync", "kae apply")` (exit 64).
- `internal/cmd/cmd.go`: `Root()` route `case "apply"` → `CmdApply`;
  `case "sync"` → tombstone. Update `printHelp()`, bump `toolVersion`.
- `internal/cmd/miseinit.go`: enter-hook script `kae sync --quiet` →
  `kae apply --quiet`.
- `internal/cmd/modes.go`: `pinnedIsolationGuard` error message `kae sync` →
  `kae apply`.
- Docs: CLI.md, DESIGN.md (Switch Surface Map), RELEASE.md/ROADMAP.md.
- Validation: `mise run check`; update `sync_test.go` to `CmdApply`; add a
  tombstone test (exit 64 names `apply`). Temp-HOME only.

### Phase 1 — paths re-keying: `isolation/<pin-id>/` + `synchomes/`

- `internal/paths/paths.go`: add `PinID(absDir) string` (SHA-256→hex[:16]),
  `IsolationDir()`, `BondDir(pinID, tool)`, `PinSharedDir(pinID, tool)`,
  `PinCredDir(pinID, tool, account)`, `PinConfigDir(pinID, tool, account)`,
  `SyncHomesDir()`, `SyncHomeDir(tool, account)`.
- `internal/cmd/modes.go`: extend `kaeManagedHomeKind` (a.k.a. the kae-managed
  classifier used by `realToolHome` + `pinnedIsolationGuard`) to also match
  `IsolationDir()` and `SyncHomesDir()`. **Critical**: missing this lets
  `kae use` bypass the guard inside bond/pin dirs.
- Validation: pure path unit tests (PinID stability/no-collision; dir builders).

### Phase 2 — `kae bond` (per-dir shared)

- `internal/constants/constants.go`: add `ModeBond`, `ModePin`, `ModeSync`.
- `internal/cmd/miseinit.go`: `bondDenylistItems(tool) []string` (claude
  `.credentials.json` Linux-only; codex `auth.json`); `prepareBond(tool,
  account, pinID)` (ReadDir real home → symlink all but denylist into
  account-agnostic `BondDir`; materialise current account's credential
  privately; `.claude.json` symlink → real home with self-ref guard);
  `miseBondBlock(...)` renderer; route `modeBond` in `runMiseInit`.
- `internal/cmd/bond.go` (new): `CmdBond`.
- `internal/cmd/cmd.go`: `case "bond"`.
- `internal/cmd/modes.go`: `validMode` += bond; `pinnedIsolationGuard`
  classifies BondDir as kae-managed.
- `internal/config/config.go`: `Tool.BondDenylistExtra` (validated bare names,
  refuse the hard-coded auth artifacts).
- Docs: ADAPTERS.md isolation table (bond row + denylist + unknown-file policy).
- Validation: temp-HOME (symlinks for non-denylist; credential not symlinked;
  re-bond idempotent). Real-machine gate: bond a claude account, enter dir,
  `claude -p '' --model haiku` → AUTH-OK; real `~/.claude` unchanged.

### Phase 3 — claude adapter: drop `/oauthAccount`

- `internal/adapter/claude/claude.go`: `Artifacts()` returns the token spec
  only (remove `oauthSpec`).
- `internal/config/config.go`: remove `.claude.json` from `refusedOverlayShare`
  (no longer an auth artifact → safe to share wholesale).
- Docs: ADAPTERS.md (Switched: drop `/oauthAccount`; Preserved: `.claude.json`
  now shared/preserved, not patched). SCOPE-MODEL.md §11 → "implemented".
- **Keychain hazard gate**: do not merge until real-machine fresh-process auth
  (token-only switch, no `/oauthAccount` write) passes on macOS + Linux;
  keychain must not be polluted. Unit: `Artifacts()` returns exactly one spec
  per driver; integration: `buildSwitch` no longer patches `.claude.json`;
  drop the `oauth_account` action-row assertions in `switch_test.go`.

### Phase 4 — `kae pin` (per-dir isolated, opt-in shares; meaning flip)

- `internal/cmd/pin.go`: default mode `modeOverlay` → `modePin`; update usage.
- `internal/cmd/miseinit.go`: `preparePinShared(tool, pinID)` (symlink opt-in
  items from real home into `PinSharedDir`); `preparePinCred(tool, account,
  pinID)`; `misePinBlock(...)`; route `modePin`.
- `internal/config/config.go`: `Tool.PinSharedItems` (bare names; hard-coded
  auth denylist `.credentials.json`/`auth.json` refused at load; `.claude.json`
  NOT in this denylist after Phase 3).
- `internal/cmd/modes.go`: `validMode` += pin; classifier += PinConfigDir.
- Docs: ADAPTERS.md (pin row), DATA-MODEL.md (`isolation/<pin-id>/` layout).
- Migration: existing `overlays/<tool>/<account>/` keep working; `kae unpin` +
  `kae bond`/`kae pin` re-run migrates. Warn for one release on `kae pin` when
  it detects an old-format block. Release note required.
- Validation: temp-HOME (only opt-in items linked; credential private).

### Phase 5 — `kae as` (in-dir credential-only swap)

- `internal/cmd/as.go` (new): `CmdAs(<tool> <account>)` — detect bond/pin from
  the cwd's `.mise.toml` block; refuse if not bonded/pinned; `PinID(cwd)`; swap
  only the private credential; re-render the block.
- `internal/cmd/cmd.go`: `case "as"`.
- `internal/cmd/modes.go`: guard message names `kae as` as the sanctioned
  in-directory account-change path.
- Validation: temp-HOME (credential swapped in the private slot; real home
  untouched).

### Phase 6 — `kae sync <account>` (global isolated) — highest risk

- `internal/cmd/sync_global.go` (new): `CmdSyncGlobal` — prepare
  `SyncHomeDir(tool, account)` (full private home), then **symlink-swap**
  `~/.claude`/`~/.codex` to it (first run: backup real dir →
  `~/.claude.kae-backup-<ts>`, create symlink, verify, rollback on failure;
  subsequent: atomic `rename` of a temp symlink). Update `state.json`.
- `internal/cmd/cmd.go`: `case "sync"` now → `CmdSyncGlobal` (reclaims the name;
  the Phase 0 tombstone is removed here — **must be a later release than
  Phase 0**, never the same version).
- `internal/cmd/modes.go`: classifier += SyncHomeDir; guard resolves
  `os.Readlink(~/.claude)` to detect a sync home.
- **Real-machine gate** (§10): on a staging machine with a disposable
  `~/.claude`, fresh-process AUTH-OK after the swap; keychain not polluted.

### Phase 7 — docs fold-down

- Fold SCOPE-MODEL.md normative content into DESIGN.md (model + surface map +
  `as` semantics), CLI.md (full command table), ADAPTERS.md (final isolation
  table). Reduce SCOPE-MODEL.md to rationale/history. Update DATA-MODEL.md,
  RELEASE.md, README.md.

### Cross-cutting risks (carry into each phase)

- **`kaeManagedHomeKind` coverage** (Phase 1) is load-bearing for the guard;
  verify for bond/pin/sync dirs.
- **Per-pin-id lock**: `bond`/`pin`/`as` mutating one dir's store need a
  `PinLockDir(pinID)` lock (the existing per-tool lock only covers global auth).
  `sync` uses the existing per-tool lock for the home swap.
- **`sync` name timeline**: Phase 0 frees it (tombstone), Phase 6 reclaims it —
  span at least one release; RELEASE.md tracks when the tombstone is removed.
- **`--auto` flag**: auth-mode only; refuse for bond/pin (they take effect via
  `[env]` on entry), same as today's overlay/home refusal.
