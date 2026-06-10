# Release Target: kae v0.2.0

Modes and workflow release: `run` / `login` / `env` / `mise init` plus the
`env` / `home` / `overlay` modes and an experimental agy adapter. Pre-stable:
contracts may still change with clear release notes.

Previous baseline: v0.1.0 (auth-mode MVP — `init`, `doctor`, `capture`,
`switch`, `current`, `accounts`, `status`, `backup list`, `rollback`, with
locks, backups, atomic writes, OS-credential-store secrets; see git tag
v0.1.0).

## Scope

- `kae run [--mode auth|env|home|overlay] <tool|all> <name> -- <cmd...>`
  - auth: lock across the child run, backup (`reason: "run"`), apply,
    recapture refreshed credentials, restore; child exit code passthrough
  - env: inject env profile into the child only
  - home: isolated tool home via `CLAUDE_CONFIG_DIR` / `CODEX_HOME`
  - overlay: experimental per-tool opt-in; shared symlinks + private auth
- `kae login <tool> <account> [--restore]` wrapping the official flows
- `kae env set|unset|list` with stdin value input and names-only listing
- `kae mise init [--profile P] [--write]` with marker-block updates
- `agy-file-snapshot` adapter (experimental; keyring storage detect-only)
- config keys `home_mode_enabled` / `overlay_mode_enabled`
- mise `[tools]` pinning and `install` / `build` tasks

## Non-Goals (this release)

Codex/agy keyring drivers, Windows, gemini/agy home isolation, dotenv value
export, TTY, completion. See [ROADMAP.md](ROADMAP.md).

## Acceptance Criteria

- `kae run claude work -- <cmd>` (auth): work account live during the child,
  refreshed token persisted into the snapshot, previous login restored
  afterwards, child exit code returned; lock held throughout (second kae
  gets exit 4).
- `kae run --mode env` injects exactly the stored variables; values never
  appear in stdout/stderr/metadata (regression-tested).
- `kae run --mode home claude a` and `... b` produce disjoint
  `CLAUDE_CONFIG_DIR`s; gemini/agy refused with exit 5.
- overlay refuses without opt-in, links only the documented allowlist,
  refuses (not replaces) real files at link locations, is idempotent.
- `kae login claude work --restore` captures the new login and restores the
  previous one; both states applyable afterwards.
- `kae mise init --write` never modifies a file lacking the marker block.
- agy: capture/switch round-trips a file-based credential; keyring-likely
  setups get a doctor warning and `auth_missing` guidance.
- `mise run check` passes; new JSON reports keep `schema_version: 1`,
  stable tokens, `[]` arrays.

## Release Steps

1. Acceptance criteria green; `docs/VALIDATION.md` checklist done.
2. README examples verified against the built binary.
3. Tag `v0.2.0`, GitHub release with notes.
