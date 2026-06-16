# kae v0.8.0 (released 2026-06-16)

> No active release target. The next candidate is **v0.8.1** (snapshot freshness
> / auto-recapture) — see [ROADMAP.md](ROADMAP.md).

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
