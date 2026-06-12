# Release Target: kae v0.5.0

A memorable scope-verb command system and shared-config directory isolation.
One verb per scope: `use` switches now (global), `pin` binds a directory,
`run` wraps one process. Directory isolation defaults to **overlay** mode —
settings, skills, and memory stay shared with the real home while auth and
session state are private — because separate logins per directory are wanted
far more often than separate configs. Pre-stable: this release removes
commands (see Breaking Changes).

Previous baseline: v0.4.0 (project-scoped switching: `use`/`u`, `sync`,
`mise init --auto` / `--mode home`; see git tag v0.4.0).

## Scope

- **`kae pin [<profile>]` / `kae unpin`** — bind/unbind the current
  directory. `pin` writes the kagikae block into `.mise.toml` (creating the
  file if missing) in **overlay** mode by default, prepares the overlay
  homes (private dirs + shared-item symlinks, docs/ADAPTERS.md Isolation),
  and re-running `pin` refreshes stale symlinks — no enter-hook dependency.
  Profile defaults to `default_profile`. `--mode home|auth` and `--auto`
  (auth only) select the v0.4.0 renderings. `unpin` removes only the
  marker-delimited block and leaves the rest of `.mise.toml` intact.
  `kae mise init` remains as the low-level form (`--write`-less preview,
  explicit flags); `pin` is sugar over it and writes immediately.
- **overlay rendering for mise** — `kae mise init --mode overlay` renders
  `[env]` entries pointing `CLAUDE_CONFIG_DIR` / `CODEX_HOME` at the
  per-account overlay homes. Symlink maintenance moves into a shared helper
  used by `kae run --mode overlay`, `mise init --mode overlay --write`, and
  `pin`; it runs at write/pin time, not on directory entry. Tools without a
  stable home env var (gemini, agy) keep the real home with an inline
  warning, as in home mode.
- **overlay promotion** — `overlay_mode_enabled` flips to default **on**
  (the per-tool opt-out remains). Gate: the flip ships only after the
  real-machine acceptance below passes; if overlay fails acceptance,
  `pin` falls back to `--mode home` as default and the flip is reverted.
- **`kae use <tool> <account>`** — `use` absorbs single-tool switch.
- **`kae add <tool> <account> [--no-login] [--restore]`** — one verb to
  register an account: default runs the official login flow then captures
  (old `login`); `--no-login` snapshots the current live state (old
  `capture`). Reports and exit codes carry over unchanged (including
  exit `11` auth_unchanged and `--restore`).
- **Removals** — `switch` / `s` (use `use`), `login` and `capture` (use
  `add`), `current` (bare `kae` already shows the same summary).
- **Docs** — README, CLI.md, and DESIGN.md rewritten around the
  use / pin / run triad; ADAPTERS.md overlay section gains the mise/pin
  surface; ROADMAP pointer updated.

## Non-Goals (this release)

Gemini/agy home or overlay isolation, codex/agy keyring drivers, login UX
polish, `env export --dotenv --reveal`, performance polish, claude
file-driver override, Windows — see [ROADMAP.md](ROADMAP.md). No automatic
overlay refresh on directory entry (mise hooks stay experimental; `pin`
re-run is the refresh path). No removal of `kae sync` or `kae mise init`.

## Breaking Changes

| Removed | Replacement |
|---------|-------------|
| `kae switch <tool> <account>` / `kae s` | `kae use <tool> <account>` |
| `kae switch all <profile>` | `kae use <profile>` (since v0.4.0) |
| `kae login <tool> <account>` | `kae add <tool> <account>` |
| `kae capture <tool> <account>` | `kae add --no-login <tool> <account>` |
| `kae current` | `kae` (status summary) |

Removed commands return the unknown-command usage error (exit `64`) with the
replacement named in the message for one release.

## Acceptance Criteria

- `kae pin clientA` in a fresh directory writes the overlay `[env]` block,
  creates the overlay homes with the shared-item symlinks, and is
  idempotent; after adding a new shared item to the real home, re-running
  `pin` links it. `kae unpin` removes the block and nothing else.
- Real-machine overlay acceptance (macOS, real accounts): inside a pinned
  directory claude sees the real home's settings/skills/CLAUDE.md, starts
  unauthenticated until logged in once, the login persists in the overlay
  across fresh processes, and the real home's login and `~/.claude.json`
  identity are untouched throughout (fresh-process AUTH-OK check on both
  sides — VALIDATION.md).
- `kae use <tool> <account>` matches the old single-tool switch report and
  exit codes; removed commands fail with exit `64` naming the replacement.
- `kae add` matches the old login behavior (including `--restore` and exit
  `11`); `kae add --no-login` matches the old capture report.
- `mise run check` passes; JSON keeps `schema_version: 1`, stable tokens,
  `[]` arrays.

## Release Steps

1. Acceptance criteria green; `docs/VALIDATION.md` checklist done (smoke
   uses codex-only profiles on macOS — keychain warning).
2. README examples verified against the built binary.
3. Bump `toolVersion` (and its test), tag `v0.5.0`, GitHub release with
   breaking-changes table in the notes.
