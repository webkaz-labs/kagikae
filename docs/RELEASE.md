# Release Target: kae v0.4.0

Project-scoped switching and ergonomics: one short command to switch a
profile, opt-in per-directory auto-switching through mise, and per-directory
isolation of account **and** config directory. Pre-stable: contracts may
still change with clear release notes.

Previous baseline: v0.3.0 (daily-use hardening — verbatim keychain
capture/restore, login auth-unchanged detection; see git tag v0.3.0).

## Scope

- **`kae use <profile>` (alias `kae u`)** — ergonomic short form of
  `kae switch all <profile>`: same behavior, JSON report, and exit codes.
- **`kae sync [--profile P] [--quiet]`** — idempotent profile apply for
  hooks and scripts. Profile resolution: `--profile`, then `KAE_PROFILE`,
  then `default_profile`. On a match it exits `0` with `"changed": false`,
  taking no locks and writing no backups; otherwise it performs a normal
  `switch all`. The match compares kae's recorded active state (kae's
  belief, not upstream truth — DATA-MODEL.md); external drift is neither
  verified nor repaired. `kae use` forces an apply.
- **mise auto-switch (opt-in)** — `kae mise init --auto` additionally
  renders a mise `[hooks.enter]` entry running `kae sync --quiet`. Opt-in
  with an inline caveat comment because auth mode mutates the global live
  state (DESIGN.md, Concurrency Boundary). Hook firing requires
  `mise activate`, a trusted config, and `mise settings experimental=true`
  (mise hooks are experimental — verified against mise 2026.6.2 during
  implementation; firing and re-entry no-op confirmed in a temp-HOME smoke,
  see VALIDATION.md).
- **`kae mise init --mode auth|home`** — `home` renders `[env]` entries
  pointing `CLAUDE_CONFIG_DIR` / `CODEX_HOME` at the per-account kae home
  directories (DATA-MODEL.md) instead of auth-mode hooks/tasks:
  directory-scoped switching of account and config directory with no live
  mutation, safe across concurrent terminals. Default `auth`. The mode is
  per-invocation (per directory), deliberately not a profile property —
  the same profile stays usable for global auth switching and isolated
  project homes. Tools without a stable home env var (gemini, agy) are
  omitted with an inline warning comment and keep their real home; the
  exit-`5` refusal of `kae run --mode home` for them and the per-tool
  `home_mode_enabled` gate are unchanged.
- **Docs** — DESIGN.md scope × mode map (shipped ahead in this branch);
  README quick start gains the `use` / mise flows; CLI.md and ADAPTERS.md
  gain the new command contracts.

## Non-Goals (this release)

Codex/agy keyring drivers, login UX polish, `env export --dotenv --reveal`,
performance polish, claude file-driver override, Windows, gemini/agy home
isolation — see [ROADMAP.md](ROADMAP.md). `kae sync` does not watch
directories or daemonize; it runs only when invoked. No shell-rc
integration beyond what mise provides.

## Acceptance Criteria

- `kae use work` and `kae u work` behave identically to
  `kae switch all work` (same JSON report shape, exit codes, backups).
- `kae sync` with matching recorded state exits `0`, takes no lock, writes
  no backup, and reports `"changed": false`; with diverging state it
  switches and reports per-tool results. Profile resolution order is
  regression-tested.
- `kae mise init --auto --write` produces a marker block whose enter hook
  auto-switches on directory entry in a mise-activated shell, and re-entry
  is a no-op (manual verification on the real machine).
- `kae mise init --mode home` renders the `[env]` home entries and no auth
  hook/task; running claude inside the directory uses the isolated home
  (manual verification; requires `home_mode_enabled = true`).
- `mise run check` passes; new JSON keeps `schema_version: 1`, stable
  tokens, `[]` arrays.

## Release Steps

1. Acceptance criteria green; `docs/VALIDATION.md` checklist done.
2. README examples verified against the built binary.
3. Tag `v0.4.0`, GitHub release with notes.
