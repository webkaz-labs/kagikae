# Roadmap

Long-term ordering beyond the active release ([RELEASE.md](RELEASE.md)).
Implementation history lives in git log.

The active target (v0.8.0 — fold `apply` into `use`, redesign `run` onto
`-s`/`-i`/`--env`, trim `mise init`, hard-rename the mechanism + config-key
vocabulary, and add input ergonomics) lives in [RELEASE.md](RELEASE.md); v0.7.2
(use/pin × -s/-i, global isolated home) shipped before it. What remains beyond
v0.8.0 is hardening and platform coverage, ordered below by user impact.

## Hardening backlog — daily-use robustness

- **Surface vocabulary unification (`run` / `apply` / `mise init`)** *(now the
  v0.8.0 target — see [RELEASE.md](RELEASE.md))*: fold `apply` into `use`,
  redesign `run` onto `-s`/`-i`/`--env`, trim `mise init`, and hard-rename the
  mechanism + config-key vocabulary to `shared`/`isolated`.
- **TUI**: an interactive mode (profiles/accounts browser, pin status,
  config maintenance) on top of the stable JSON surface, so daily
  switching does not require remembering flags. Candidate once the
  v0.5.0 command system has settled.
- **Remote share-list definitions (ship)**: implement the v0.6.0 design if
  it holds — published defaults for the overlay share list, explicit
  fetch, diff-before-adopt, hard-coded auth denylist.
- **Codex keyring driver**: pin down the OS-credential-store item contract
  used by `cli_auth_credentials_store = "keyring"`, add structure guards,
  lift the detect-only restriction.
- **Login UX polish**: verify `claude /login` behavior across versions,
  support agy. (The "login flow exited without changing auth" case is now
  detected and refused with exit `11`.)
- **`kae env export --dotenv --reveal`**: explicit-flag value export for CI
  bootstrapping (today values are injection-only by design).
- **Performance polish**: combine/cache the multiple `security` subprocess
  calls per macOS switch; run per-tool `Detect` concurrently in `status`.
- **doctor keychain-orphan detection** *(discovery done in v0.7.1, deferred)*:
  warn when a `kagikae` secret item has no matching snapshot dir. Blocked on
  enumeration — the darwin keychain cannot list items by service via the
  `security` CLI (`find-generic-password -s` returns only the first match;
  `dump-keychain` is heavy/brittle). Feasible for the `file` backend
  (`readdir`) and Linux `libsecret` (`secret-tool search --all`); needs a
  `Backend` enumeration method. Low priority now that `kae account rm` removes
  the snapshot and secrets together, making orphans rare. See
  [SECURITY.md](SECURITY.md) for the discovery note.
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
- **`kae ls`**: a mise-style listing of accounts and profiles in one view
  (today split across `kae accounts` and `kae status`).
- **Account-name auto-detection**: each adapter exposes the live login
  identity (claude email, cursor `cursor-agent status`, codex auth.json) so
  `kae add` can suggest and sanitize a name instead of requiring one.
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
