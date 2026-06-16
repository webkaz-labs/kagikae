# Roadmap

Long-term ordering beyond the active release ([RELEASE.md](RELEASE.md)).
Implementation history lives in git log.

The active target is **v0.8.2** (daily-use polish: concurrent `status`,
switch-read coalescing, `kae add` name auto-detection, `kae ls`, and v0.8.1
freshness hardening — see [RELEASE.md](RELEASE.md)). v0.8.1 (credential freshness
/ auto-recapture) shipped, following v0.8.0 (fold `apply` into `use`, redesign
`run` onto `-s`/`-i`/`--env`, trim `mise init`, hard-rename the mechanism +
config-key vocabulary, input ergonomics) and v0.7.2 (use/pin × -s/-i, global
isolated home). What remains beyond v0.8.2 is hardening and platform coverage,
ordered below by user impact.

Split out of v0.8.2 to **v0.8.3**:
- **Freshness as an adapter capability**: move `freshness.Inspect`'s per-tool
  `switch` onto a per-tool `Freshness(payload) Info` adapter method (beside the
  new `Identity`), so per-tool knowledge has one home. Deferred because it
  touches all six adapters plus the interface, growing the v0.8.2 patch past its
  daily-use-polish scope; the shared `jwtExpiry`/`epochToTime`/`decodeObject`
  primitives stay in `internal/freshness`. New tools stay fail-safe (Known=false).
- **`kae add` identity for cursor**: cursor's `cursor-agent status` output is
  undocumented, so its `Identifier` is discovery-blocked (guessing the format
  would violate the refuse-unknown-layouts discipline). cursor requires an
  explicit account name until a real-machine discovery pins the output down.

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
- **TUI**: an interactive mode (profiles/accounts browser, pin status,
  config maintenance) on top of the stable JSON surface, so daily
  switching does not require remembering flags. Candidate once the
  v0.5.0 command system has settled.
- **Remote share-list definitions (ship)**: implement the v0.6.0 design if
  it holds — published defaults for the overlay share list, explicit
  fetch, diff-before-adopt, hard-coded auth denylist.
- **Codex keyring driver** *(v0.8.2 target — split out of v0.8.1 §E)*: pin down
  the OS-credential-store item contract used by
  `cli_auth_credentials_store = "keyring"`, add structure guards, lift the
  detect-only restriction. Deferred from v0.8.1 because the keyring item naming
  is undocumented upstream and a safe round-trip needs real-machine discovery
  against a live codex keyring login first (guessing the contract would break
  the refuse-unknown-layouts guard). The detect-only refusal stays in place
  until then.
- **Login UX polish**: verify `claude /login` behavior across versions,
  support agy. (The "login flow exited without changing auth" case is now
  detected and refused with exit `11`.)
- **`kae env export --dotenv --reveal`**: explicit-flag value export for CI
  bootstrapping (today values are injection-only by design).
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
  surgery. **`kae profile rm` / set** remain candidates here.
- **`kae ls`** *(v0.8.2 §C — shipped)*: a mise-style listing of accounts and
  profiles in one view (was split across `kae accounts` and `kae status`).
- **Account-name auto-detection** *(v0.8.2 §B — shipped, cursor deferred)*: an
  adapter exposes the live login identity via the optional `Identifier`
  capability so `kae add <tool>` auto-detects and sanitizes a name by default,
  while an explicit `kae add <tool> <account>` still wins. claude/codex/opencode/
  copilot ship; agy has no identity and cursor's `cursor-agent status` output is
  discovery-blocked (both require an explicit name — see the v0.8.3 split above).
- **Shorter ad-hoc switch inside a pinned directory**: `kae run <tool>
  <account> -- <tool>` already works (it is not blocked by the pinned-
  directory guard), but it is verbose; provide a terser way to open an
  interactive session under a different account without unpinning.
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
- **Generic completion + "did you mean"** *(completion is v0.8.0 — see
  [RELEASE.md](RELEASE.md); "did you mean" stays a candidate)*: both are feasible off the existing
  static lists (commands, tools, flags, profiles/accounts from state). (1) a
  `kae completion <bash|zsh|fish>` generator emitting a shell completion script
  — since the surface is hand-rolled (not cobra), the candidate lists are
  enumerated from the router + `constants.Tools` + the config; (2) an unknown
  command/tool prints a Levenshtein "did you mean X?" hint instead of a bare
  error. Both stay table-driven so they track the surface automatically.

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
