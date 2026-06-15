# Roadmap

Long-term ordering beyond the active release ([RELEASE.md](RELEASE.md)).
Implementation history lives in git log.

The active target (v0.7.2 — the `use` / `pin` × `-s` / `-i` surface
unification and the global-isolated `kae use -i` home swap) lives in
[RELEASE.md](RELEASE.md). What remains beyond it is hardening and platform
coverage, ordered below by user impact.

## Hardening backlog — daily-use robustness

- **Surface vocabulary unification (`run` / `apply` / `mise init`)**: fold the
  scope×environment vocabulary into the rest of the surface — see the dedicated
  subsection below.
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

## Surface vocabulary unification (run / apply / mise init)

v0.7.2 put `use` / `pin` on the scope×environment grid (`-s` / `-i`), but `run`,
`apply`, and `mise init` still speak the older mechanism vocabulary. Deferred
from v0.7.2 (where `apply` stays global-shared and `run --mode` keeps its
mechanism names). Remaining gaps:

- **`run --mode auth|env|home|overlay|bond|pin`** conflates environment
  (`auth`=shared, `home`/`overlay`/`pin`=isolated, `bond`=per-dir shared) with
  env-var injection (`env`); `home` predates and overlaps `use -i`, and
  `overlay` is legacy.
- **`apply`** covers only global-shared — no `-i`, so a `use -i` binding has no
  idempotent hook form.
- **`mise init --mode bond|pin`** is redundant now that `kae pin -s|-i` owns the
  fragment; `mise init`'s unique role is `--mode auth` (tasks / enter hook).
- The same "environment" concept is spelled three ways: `-s` / `-i` (use/pin),
  `--mode <value>` (run / mise init), and nothing (apply).
- `pin` is overloaded at the user surface: the verb `kae pin` vs `run --mode pin`.
- Config keys stay mechanism-named: `bond_denylist_extra`, `pin_shared_items`,
  `overlay_extra_shared`, `overlay_mode_enabled`, `home_mode_enabled`.

Proposed direction, in safe (least-breaking-first) order:

1. **`apply [-s|-i]`** — add the environment axis; default `-s` (unchanged), `-i`
   idempotently maintains the `synced` global-isolated state. Additive, low-risk.
2. **`run [-s|-i]` + `--env`** — `-s` = current `auth` (apply + restore the real
   home for the process), `-i` = a private isolated home; split env-var injection
   out to `--env`. Introduce `--mode` as a transitional deprecated alias
   (`auth`→`-s`, `home`→`-i`, `env`→`--env`; `bond`/`pin`/`overlay` → guidance to
   `kae pin` then `kae run`). Removes the user-surface `pin` overload.
3. **`mise init`** — drop `bond` / `pin` (point at `kae pin`); keep `auth` (and
   legacy `home` / `overlay`).
4. **Mechanism rename (broadest, last)** — `modeBond` / `modePin` →
   `modeShared` / `modeIsolated`, the `run --mode` values, and the config keys
   (`bond_denylist_extra` → `shared_denylist_extra`, etc.) behind a transitional
   old-key alias so the migration itself is non-breaking.

   **Final step of P4 — remove the legacy compat.** Once the deprecation window
   passes, delete every transitional shim introduced above in one clean break:
   the `run --mode` alias and its mechanism values, the old config-key aliases,
   and any removed-command pointers from this cleanup. After P4 the surface
   speaks only the scope×environment vocabulary — no `--mode`, no mechanism-named
   keys. (`apply`/`use`/`pin`/`run` keep their verbs; `apply` and `run` stay as
   distinct scopes, not folded into `use`.)

The internal-name drift fixed in v0.7.2 (`sync.go`→`apply.go`, per-dir fragment
`*PinFragment`→`*DirFragment`, `as.go`→`rebind.go`; see git log) was the
non-breaking half; the mechanism vocabulary above is intentionally still pending
because it touches the `run --mode` and config-key contracts.

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
