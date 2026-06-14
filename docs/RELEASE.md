# Release Target: kae v0.7.1

Operational safety and account lifecycle. Three additions that close daily-use
gaps and de-risk the global-isolated `sync` mode landing in v0.7.2: a
file-driver override so smoke/container checks never touch the real login
keychain, account removal/rename so cleanup is no longer manual keychain
surgery, and (discovery-gated) doctor detection of orphaned keychain items.

Previous baseline: v0.7.0 (bond mode, credential-private per-directory
isolation, `/oauthAccount` removal, `kae pin` semantics flip, `kae as`; see
git tag v0.7.0).

## Scope

- **claude file-driver override** — on macOS the claude adapter resolves a
  keychain driver, which ignores a temp `$HOME`; that makes claude switch
  smoke checks unsafe outside Linux (they would touch the real login keychain).
  Add an explicit override that forces the file-patch driver (`.credentials.json`
  under `CLAUDE_CONFIG_DIR`) even on darwin. **Env var is the primary surface**
  (`KAE_*`, name TBD against the existing `KAE_PROFILE` convention): the
  override is an ephemeral smoke/container escape hatch, and persisting it in
  config would silently break a real macOS login (the live claude reads the
  keychain, not the file). A per-tool config option (`[tools.claude]`,
  default off) is a secondary, explicit opt-in. The override changes the
  capture/restore path to the file route, so the round-trip closes entirely on
  files — no `security` subprocess, no real keychain access.
- **`kae account rm <tool> <account>`** — remove a captured account: delete the
  snapshot dir (`accounts/<tool>/<account>`) and every secret-backend item
  (`SecretRef(tool, account, artifact)` under service `kagikae`). Today this is
  manual two-step surgery (`rm -rf` the dir plus `security
  delete-generic-password`), error-prone because it touches the keychain by
  hand. Refuse to remove the **active** account (exit `5`) unless `--force`,
  which also drops it from `state.json` `active` and recomputes the active
  profile. Refuse (warn) if any `[profiles]` entry still references the account
  (`Profile.Accounts` is a tool→account map). `--dry-run` prints the plan and
  writes nothing. Per-tool lock held throughout.
- **`kae account rename <tool> <old> <new>`** — rename a captured account.
  Secret-backend keys cannot be renamed in place, so copy-then-delete each
  item; move the snapshot dir and metadata; update `state.json` `active[tool]`
  if it pointed at `<old>`; **rewrite every `[profiles]` `accounts` reference
  from `<old>` to `<new>`** (the load-bearing detail — a missed reference
  leaves a dangling profile). Refuse if `<new>` already exists; sanitize the
  new name with the existing account-name rule.
- **doctor keychain-orphan detection (discovery-gated)** — warn when a
  `kagikae` secret item exists with no matching `accounts/<tool>/<account>`
  dir (a leftover from manual cleanup). **Discovery first**: confirm whether
  the `security` CLI can *stably enumerate* all items under service `kagikae`
  (a single `find-generic-password -s kagikae` returns only the first match;
  `dump-keychain` is heavy and brittle). Record the finding in a discovery
  note; implement only if enumeration is reliable, otherwise defer with the
  reason written down. darwin + keychain backend only. With `account rm`
  shipping in the same release, orphans become rare, so this is a nice-to-have,
  not a gate.

## Non-Goals (this release)

- **Phase 6 (`kae sync`, global isolated mode)** — the highest-risk mode
  (symlink-swaps the real `~/.claude`); deferred to **v0.7.2**. The file-driver
  override here is its safety prerequisite (its real-machine gate can then run
  fully detached from the real login keychain). The `sync` tombstone (Phase 0,
  v0.7.0) spans v0.7.1 before the name is reclaimed in v0.7.2 — comfortably
  past the one-release minimum.
- TUI, Windows, Codex keyring driver, account auto-detection, `kae profile
  save`, `env export --dotenv --reveal` — see [ROADMAP.md](ROADMAP.md).
- No automatic network access.

## Acceptance Criteria

- **file-driver override**: with the override env var set, `kae use claude
  <account> --dry-run` on darwin reports a `.credentials.json` file action
  (not a keychain action); unset, darwin keeps the keychain driver (no
  regression). A temp-HOME smoke check switches claude with the override and
  asserts the real login keychain is never read or written
  ([docs/VALIDATION.md](docs/VALIDATION.md) updated with the procedure).
- **`kae account rm`**: removes the snapshot dir and all secret items; prints a
  confirmation; refuses the active account without `--force`; refuses a
  profile-referenced account; `--dry-run` writes nothing; unknown account exits
  with guidance.
- **`kae account rename`**: round-trips secret items (copy+delete), moves the
  dir, updates `state.json` and every `[profiles]` reference; refuses an
  existing target name. A test asserts a renamed account still resolves through
  its referencing profile.
- **doctor orphan**: discovery note committed; if implemented, a `kagikae`
  item with no snapshot dir produces a `keychain_orphan` warn-level check, and
  the JSON report keeps `schema_version: 1`.
- `mise run check` passes; JSON keeps `schema_version: 1`, stable tokens, `[]`
  arrays; redaction tests cover any new output path.

## Release Steps

1. Land the file-driver override; smoke check proves real-keychain
   non-interference (this unblocks the v0.7.2 Phase 6 gate).
2. Land `kae account rm` / `rename`; profile-reference propagation tested.
3. doctor orphan: run discovery, then implement or defer with the reason.
4. `docs/VALIDATION.md` v0.7.1 smoke results; README examples verified against
   the built binary.
5. Tag `v0.7.1`, GitHub release.

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
