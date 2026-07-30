# Roadmap

Long-term ordering beyond the active release ([RELEASE.md](RELEASE.md)).
Implementation history lives in git log.

Active release target: v0.12.0 (switch claude's `/oauthAccount` identity with the
credential, and add the two offline `doctor` checks — `identity_drift` and
`upstream_version` — that watch for an upstream *behaviour* change no layout guard
can see; plus read refresh-token expiry and the post-failed-refresh tombstone
instead of guessing. **Gated on the macOS pin gap below** — see
[RELEASE.md](RELEASE.md)). v0.11.0 (companion re-bind lockstep + the opt-in
`companion_token_drift` doctor check that resolves a bound token's live login
(`gh api user`) against a recorded `expected_login`) shipped 2026-06-27.
v0.10.1 (finish `kae companion` shell completion and
make completion self-maintaining via `kae completion --refresh`) shipped
2026-06-23. v0.10.0 (companion-auth lockstep — bind git/gh/cloud-CLI
identity per profile, delivered by `kae pin`, plus a `companion_drift` doctor
check that flags when the live git commit identity diverges from the binding)
shipped 2026-06-23. v0.9.1 (manual login-identity override — `kae add
--identity` and `kae account set-identity` — for tools kae cannot auto-detect,
e.g. agy on current Antigravity) shipped 2026-06-20. v0.9.0
(installable binaries: a GoReleaser pipeline +
`scripts/install.sh` + CI so `curl | sh`, mise, and prebuilt
darwin/linux archives work, with the README rewritten to OSS parity; additive,
no contract break — see [RELEASE.md](RELEASE.md)) shipped 2026-06-19. v0.8.9
(`kae completion zsh --install` detects an existing user `fpath` dir so the
installed completion auto-loads with no `.zshrc` edit) shipped 2026-06-18. v0.8.8
(daily-use fixes: opencode identity prefers the access-token email over the
opaque accountId UUID; shell completion is flag-aware — flags before positionals
no longer shift it — and completes flag names via a new `kae __complete flags`
kind) shipped the same day. v0.8.7 (complete account-identity
coverage: `agy.Identity` from `~/.gemini/google_accounts.json` so every tool
exposes a login identity, plus an `Identity` column in `kae status`) shipped the
same day. v0.8.6 (agy account switching on
macOS via a Keychain driver + a terser one-shot `kae run <tool> <account>` +
`claude /login` verification) shipped the same day; its agy two-account
real-keychain gate **passed**, fish was **dropped** from the verified shells
(`kae completion fish` stays best-effort), and the codex-keyring two-account gate
stays the one carried, unit-covered open item. What remains is hardening and
platform coverage, ordered below by user impact.

v0.8.5 (a "did you mean?" nearest-match hint
for an unknown command/tool/profile, table-driven off the same live lists
v0.8.4's `kae __complete` backend surfaces; additive, hand-rolled, no contract
break — see [RELEASE.md](RELEASE.md)) shipped 2026-06-17, both §A and §B. §B
(standardizing the reusable mise-integration + did-you-mean patterns into the
go-cli-tooling shared standard via chezmoi) landed the same day as a new
`docs/go-cli/PATTERNS.md`, with this repo's bundled skill resynced from it.

v0.8.4 (deep, dynamic shell completion sourced from kae's live state on a single
hidden `kae __complete` backend, feeding both kae's own completion and mise
task-argument completion) shipped 2026-06-17 — bash/zsh verified; **fish was
dropped from the verified shells** (2026-06-18; `kae completion fish` stays
best-effort, not release-gated). v0.8.3 (discovery-unblock:
freshness-as-adapter-capability, cursor `kae add` identity, codex keyring driver,
stored+displayed identity) shipped 2026-06-17 — its codex keyring two-account
real-keychain gate is deferred (also open; see [VALIDATION.md](VALIDATION.md)).
Earlier: v0.8.2 (daily-use polish), v0.8.1 (credential freshness /
auto-recapture), v0.8.0 (surface vocabulary unification), v0.7.2 (use/pin ×
-s/-i, global isolated home). What remains beyond v0.8.5 is hardening and
platform coverage, ordered below by user impact.

Follow-up from v0.8.4 (not yet scheduled):
- **Global mise tasks**: `kae mise init` writes the `ai-switch` / `ai-switch-tool`
  tasks (and their dynamic completion) into the project's `.mise.toml` only, so
  they exist where the tasks live. A `--global` option emitting them into the
  global mise config (`~/.config/mise/config.toml` or `~/.config/mise/tasks/`)
  would make `mise run ai-switch <TAB>` available in every directory. Scope
  addition; design before implementing.

## Hardening backlog — daily-use robustness

- **Surface vocabulary unification (`run` / `apply` / `mise init`)** *(shipped
  in v0.8.0 — see [RELEASE.md](RELEASE.md))*: folded `apply` into `use`,
  redesigned `run` onto `-s`/`-i`/`--env`, trimmed `mise init`, and hard-renamed
  the mechanism + config-key vocabulary to `shared`/`isolated`.
- **Credential freshness / auto-recapture** *(v0.8.1 — A–D implemented, see
  [RELEASE.md](RELEASE.md))*: `use`/bare `use` wrote the capture-time snapshot
  back to the live store with no recapture (only `run -s` recaptured), so a
  token rotated outside kae broke a switch-back (a login prompt when the refresh
  token had also rotated; seen in the v0.8.0 gate). v0.8.1 added switch-source
  recapture (symmetric with `run -s`, divergence-gated), switch-time stale
  warnings + `doctor` credential-health (`credential_stale` / `secret_orphan`),
  and `security`-read coalescing (a per-command keychain cache). Spans every
  OAuth/JWT tool, not just claude. The codex keyring driver (§E) is **split to
  v0.8.2** — see below.
- **Identity cache in isolation modes**: `kae use` / `kae add` switch claude's
  `/oauthAccount` identity cache, but the per-directory materializers
  (`writeDirCredential`, and its callers `prepareBond`, `preparePinConfig`,
  `prepareGlobalIsolatedHome`) write only the credential. Since
  `CLAUDE_CONFIG_DIR` moves the cache to `<dir>/.claude.json` and
  `prepareBond` only links the entries *of* `~/.claude`, a bonded or isolated
  directory keeps whatever account first ran there, and `kae pin <tool>
  <account>` does not correct it. Auth is unaffected (the token wins) — it is an
  attribution gap: the UI can name the wrong account inside a pinned directory.
  The fix is now one identity step alongside `writeDirCredential`, which all four
  already route through; the design
  question is bond mode, where writing the cache is visible to the real home
  (docs/SCOPE-MODEL.md §6). Until then `doctor`'s `identity_drift` check skips a
  kae-owned isolated home for the same reason: kae applied no identity there, so
  there is nothing of its own to compare the live value against.
- **claude's OAuth build suffix is not modelled**: the keychain service name is
  `Claude Code` + the build's OAuth suffix + `-credentials` + the per-config-dir
  suffix. kae hard-codes the production spelling (empty suffix); a local build, or
  any build run with `CLAUDE_CODE_CUSTOM_OAUTH_URL` pointing at an approved
  endpoint, uses `-local-oauth` / `-custom-oauth` and kae would read and write the
  wrong item. Detectable offline from the env var alone, so the cheap fix is the
  same refusal `CLAUDE_SECURESTORAGE_CONFIG_DIR` already gets. Measured on 2.1.220
  (docs/VALIDATION.md).
- **A re-bind leaves the previous account's per-directory keychain item**: in
  isolated mode the config dir is keyed by account, so re-binding moves the
  binding to a new dir and the old dir's keychain item keeps its credential. It is
  unreachable (nothing points `CLAUDE_CONFIG_DIR` there anymore) but not removed,
  the same way `kae unpin` leaves the isolation directories intact. Cleaning it up
  needs a record of which dirs were previously bound, which is why it is not part
  of the write path.
- **Pinned directories never refresh their snapshot**: a bound directory's tool
  refreshes its own token in place, so kae's snapshot for that account ages. Now
  that kae writes and can read the per-directory store, a recapture path out of a
  pinned directory is reachable — it was not while kae only wrote a file the tool
  had stopped reading.
- **TUI**: an interactive mode (profiles/accounts browser, pin status,
  config maintenance) on top of the stable JSON surface, so daily
  switching does not require remembering flags. Candidate once the
  v0.5.0 command system has settled.
- **Remote share-list definitions (ship)**: implement the v0.6.0 design if
  it holds — published defaults for the overlay share list, explicit
  fetch, diff-before-adopt, hard-coded auth denylist.
- **Codex keyring driver** *(v0.8.3 §C — discovery done)*: lift the detect-only
  restriction on `cli_auth_credentials_store = "keyring"`. The item contract was
  discovered on a real machine 2026-06-16 (service `Codex Auth`, account
  `cli|<opaque>` captured verbatim, payload = whole `auth.json` JSON; see
  [ADAPTERS.md](ADAPTERS.md)), so the verbatim-keychain driver is now
  implementable with structure guards. The detect-only refusal stays until the
  v0.8.3 driver lands (and its two-account real-keychain gate).
- **Login UX polish** *(v0.8.6 §C — claude verified; agy deferred)*: `claude
  /login` is launched via the upstream flow (`internal/cmd/login.go`); the
  "login flow exited without changing auth" case is detected and refused with
  exit `11`. agy login stays **deferred** — a 2026-06-18 discovery (with the
  `agy` CLI installed) found **no `login`/`auth`/`whoami` subcommand**; agy
  authenticates via GUI/browser OAuth, which kae's shell-out login flow cannot
  drive, so `kae add agy` stays `--no-login` capture only.
- **agy keyring driver (macOS)** *(v0.8.6 §A — implemented; real-keychain gate
  open)*: on macOS agy stores its credential in the **login Keychain**, not a
  file — item `svce="gemini"`, `acct="antigravity"`; the payload is a single
  **opaque ~686-byte token string** (not JSON/JWT — verbatim capture/apply with
  a non-empty single-line guard, unlike codex's `auth.json` JSON). v0.8.6 lifted
  the file-only adapter with the verbatim-keychain pattern used for
  codex/claude/cursor, matching by **service and account** (the `gemini` service
  is shared, only `acct=antigravity` is agy's; apply upserts with
  `add-generic-password -U`, never touching a sibling item). The file driver
  stays for Linux/WSL. Identity auto-detection stays deferred (no whoami; the
  token is opaque). See [ADAPTERS.md](ADAPTERS.md); the two-account real-keychain
  gate is the open acceptance item ([VALIDATION.md](VALIDATION.md)).
- **`kae env export --dotenv --reveal`** *(deferred — no current use)*:
  explicit-flag value export for CI bootstrapping (today values are
  injection-only by design). Considered for v0.8.6 but dropped: CI does not use
  kae, so there is no consumer for a value-reveal path. Revisit only if a
  kae-driven CI flow emerges.
- **Performance polish** *(v0.8.2 §A — shipped)*: the per-switch
  `security`-read coalescing shipped in v0.8.1 §C (a context-scoped keychain
  read cache in `internal/keychain`). v0.8.2 §A added concurrent per-tool
  `Detect` in `status` and a matching read cache for kae's own `secret.Backend`
  (`secret.WithReadCache` + `Cached`, collapsing the switch-time double read of
  each target snapshot) — see [RELEASE.md](RELEASE.md).
- **doctor keychain-orphan detection** *(shipped in v0.8.1 §D as the
  `secret_orphan` check)*: warns when a `kagikae` secret item has no matching
  snapshot dir, via a new `secret.Enumerator` (file `readdir`, Linux
  `secret-tool search --all`). The darwin keychain still cannot list items by
  service through the `security` CLI, so the check is silently skipped on the
  keychain backend (documented gap; [SECURITY.md](SECURITY.md)).
- **claude driver override for isolated smoke checks** *(v0.7.1 — see
  [RELEASE.md](RELEASE.md))*: on macOS the keychain driver ignores temp
  `$HOME`s, so claude switch smoke checks can only run safely on Linux today;
  an explicit file-driver override (env var primary, config opt-in secondary)
  lets containers and smoke environments never touch the real login keychain.
  Also the safety prerequisite for the v0.7.2 global-isolated (`kae use -i`)
  real-machine gate.

## Command-system expansion

Daily-use ergonomics, designed together as mise-style verbs so the surface
stays coherent rather than accreting ad hoc. Account delete/rename graduates
to v0.7.1 (see [RELEASE.md](RELEASE.md)); the rest remain candidates:

- **`kae profile save <name>`**: snapshot the current active set into a
  named profile, instead of hand-editing config via `kae edit`.
- **Account rm/rename** *(v0.7.1 — see [RELEASE.md](RELEASE.md))*: `kae
  account rm` / `kae account rename`, replacing manual snapshot-dir + keychain
  surgery. **`kae profile save|set|unset|rm|default`** also shipped in v0.7.1
  (the comment-preserving config writer; see [RELEASE.md](RELEASE.md)).
- **`kae ls`** *(v0.8.2 §C — shipped)*: a mise-style listing of accounts and
  profiles in one view (was split across `kae accounts` and `kae status`).
- **Account-name auto-detection** *(v0.8.2 §B — shipped; cursor v0.8.3, agy
  v0.8.7)*: an adapter exposes the live login identity via the optional
  `Identifier` capability so `kae add <tool>` auto-detects and sanitizes a name
  by default, while an explicit `kae add <tool> <account>` still wins. **All six
  tools now implement it** (claude/codex/opencode/copilot since v0.8.2, cursor
  via `cursor-agent status` in v0.8.3, agy via `~/.gemini/google_accounts.json`
  in v0.8.7); `TestIdentifierConformance` pins the full coverage.
- **Shorter ad-hoc switch inside a pinned directory** *(v0.8.6 §B)*: `kae run
  <tool> <account> -- <tool>` already works (it is not blocked by the pinned-
  directory guard), but it is verbose; v0.8.6 defaults the child to the
  adapter's `Binary()` when `-- <cmd>` is omitted, so `kae run <tool> <account>`
  opens a session under that account directly.
- **Tool-name prefix aliases** *(v0.8.0 — see [RELEASE.md](RELEASE.md); input-only sugar)*: accept any unambiguous
  prefix in tool positions (`cl`→claude, `cod`→codex, `cu`→cursor,
  `cop`→copilot, `o`→opencode, `a`→agy); ambiguous prefixes (`c`, `co`) error
  with the candidate list. Resolved to the canonical name immediately and never
  stored (config/state/JSON keep canonical names), and computed dynamically from
  `constants.Tools` so a new tool self-adjusts the ambiguity set. Only in tool
  positions of the two-arg forms (`use`/`pin`/`run`/`add`/`account`/`env`); a
  one-arg `kae use cl` stays a profile lookup. (Verb aliases `u`/`p`/`r`/`d`/`s`
  shipped in v0.7.2.)
- **Flag short forms** *(v0.8.0 — see [RELEASE.md](RELEASE.md))*: `-P` for
  `--profile` on `run` / bare `use` / `mise init`.
- **Generic completion + "did you mean"** *(static completion is v0.8.0;
  dynamic completion is v0.8.4; "did you mean" shipped in v0.8.5 — see
  [RELEASE.md](RELEASE.md))*: (1) `kae completion <bash|zsh|fish>` shipped in
  v0.8.0 as a static-list generator; v0.8.4 makes it **dynamic** via a hidden
  `kae __complete` backend (live profiles/accounts at the argument positions,
  shared with mise task completion) and adds an interactive `--install`.
  (2) an unknown command/tool/profile printing a Levenshtein "did you mean X?"
  hint shipped in v0.8.5, table-driven off the same
  router/`constants.Tools`/config lists (the `kae __complete` source).
  (3) v0.8.8 made completion flag-aware (flags before positionals no longer
  shift it) and added flag-name completion via a `kae __complete flags <command>`
  kind sourced from the parser's own per-command flag registrars.

These overlap with the TUI item above at the surface level but are the
plain-CLI layer; the TUI sits on top of them.

## Platform coverage

- **Windows**: `%APPDATA%` layout, Credential Manager secret backend, lock
  implementation, `%USERPROFILE%\.claude` file-patch driver.
- **agy home isolation**: revisit once upstream exposes a stable
  home/config env var; until then `home` / `overlay` modes refuse it (the
  same applies to the v0.6.0 adapters until their env vars are verified).

## Exploratory

- richer TTY (routed review surface) if daily use shows the need
- shell completion
- localized human output (Japanese)
- `kae shell init` convenience wrappers

## Review Triggers

- First credential-layout change in any upstream tool: add a regression
  fixture and bump the adapter guard before widening support.
