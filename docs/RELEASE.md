# Release Target: kae v0.7.2

Phase 6: `kae sync <account>` — the global isolated mode, and the last mode in
the scope×environment model. It makes a per-account private tool home visible
to **every** terminal by symlink-swapping the real `~/.claude` / `~/.codex` to
a kae-managed `synchomes/<tool>/<account>/`. This is the highest-risk mode
(it touches the real tool home, not just a directory's `.mise.toml`), so it
ships behind a real-machine gate that the v0.7.1 file-driver override now lets
us run fully detached from the real login keychain.

Previous baseline: v0.7.1 (file-driver override, `kae account rm`/`rename`,
`kae profile`, comment-preserving config writer; see git tag v0.7.1).

## Scope

- **`kae sync <account>`** (new `internal/cmd/sync_global.go`,
  `CmdSyncGlobal`) — prepare `synchomes/<tool>/<account>/` as a full private
  tool home, then **symlink-swap** the real tool home to it. **First run for a
  tool**: back up the real dir to `~/.claude.kae-backup-<ts>` (resp.
  `~/.codex.kae-backup-<ts>`), create the symlink, verify it resolves, and
  roll back the backup on any failure. **Subsequent re-points**: build a temp
  symlink and `rename` it over the existing one (atomic; no window where the
  home is missing). Record the active sync account in `state.json`. claude and
  codex only (they have a redirectable home); other tools are unsupported
  (exit `5`).
- **`kae unsync [<tool>]`** — the escape hatch (teardown must ship with the
  swap, never after): remove the kae symlink and restore the most recent
  `*.kae-backup-<ts>` in its place; if no backup exists, refuse (exit `10`)
  rather than leave the user with no tool home. Clears the sync entry from
  `state.json`.
- **`sync` name reclaim** — `Root()` routes `case "sync"` to `CmdSyncGlobal`,
  removing the v0.7.0 tombstone (the removed-command pointer to `apply`). This
  is safe only because v0.7.1 shipped in between: the tombstone spanned one
  full release, so `kae sync` has not silently changed meaning within a single
  version.
- **isolation guard + status** (`internal/cmd/modes.go`) — the mode classifier
  learns `SyncHomeDir`; the guard resolves `os.Readlink(~/.claude)` to detect
  that a tool home is a kae sync home, so `kae use`/`add`/`apply` behave
  correctly (and `--global` still reaches real state). `kae status` reports the
  active sync account per tool.
- **doctor** — a check that a tool home symlink points at a live
  `synchomes/<tool>/<account>/` (warn on a dangling sync symlink or a missing
  backup, so a half-torn-down swap is visible).

## Non-Goals (this release)

- **Live bidirectional sync / watcher daemon** — `sync` shares nothing between
  accounts and does not merge changes back to a shared home; it is a global
  *switch* of which private home is live, not a sync engine. The §6 finding
  (claude self-heals `/oauthAccount` from the token) means no copy+patch is
  needed. A resident watcher conflicts with the CLI-only design
  ([SCOPE-MODEL.md](SCOPE-MODEL.md) §6).
- **No mise hook integration** — `sync` is global, not per-directory, so it
  needs no enter/leave hook; it is a one-shot home swap.
- **Tools without a redirectable home** (agy, opencode, cursor, copilot) —
  `auth` / `run --mode env` only, unchanged.
- TUI, Windows, Codex keyring driver — see [ROADMAP.md](ROADMAP.md).
- No automatic network access.

## Acceptance Criteria

- **real-machine gate** (required before merge): on a staging machine with a
  disposable `~/.claude`, `kae sync <account>` swaps the home and a
  fresh-process `claude -p '' --model haiku` returns AUTH-OK; the real login
  keychain is not polluted (the swap targets the file home, exercised with the
  v0.7.1 file-driver override). `kae unsync` restores the original `~/.claude`
  byte-for-byte from the backup. Recorded in
  [VALIDATION.md](VALIDATION.md).
- **first-run safety**: with no prior swap, `kae sync` backs up the real dir,
  symlinks, and a simulated failure mid-swap rolls back to the original dir
  (temp-HOME test).
- **re-point atomicity**: a second `kae sync <other>` re-points without ever
  leaving the home path absent (temp-HOME test asserts the path always
  resolves).
- **`sync` reclaim**: `kae sync <account>` runs the global swap; the v0.7.0 →
  `apply` tombstone is gone; `kae apply` is unaffected.
- **unsync**: removes the symlink and restores the backup; refuses (exit `10`)
  when no backup exists; clears `state.json`.
- **unsupported tools**: `kae sync` for agy/opencode/cursor/copilot exits `5`.
- `mise run check` passes; JSON keeps `schema_version: 1`, stable tokens, `[]`
  arrays; redaction tests cover any new output path.

## Release Steps

1. Land `kae sync` / `kae unsync` with the first-run backup, atomic re-point,
   and rollback; temp-HOME tests green.
2. Reclaim the `sync` name (remove the tombstone) and update help; bump
   `toolVersion` to v0.7.2.
3. Run the real-machine gate on a staging machine (disposable `~/.claude`);
   record results in `docs/VALIDATION.md`.
4. Phase 7 docs fold-down: retire `docs/SCOPE-MODEL.md` into the permanent
   design docs now that the whole model is implemented (or keep with a reason).
5. README examples verified against the built binary.
6. Tag `v0.7.2`, GitHub release.

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
