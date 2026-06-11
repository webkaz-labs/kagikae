# Release Target: kae v0.3.0

Daily-use hardening release: keychain credentials survive switch round-trips
byte-for-byte, and `kae login` refuses to record a login flow that did not
actually change auth. Pre-stable: contracts may still change with clear
release notes.

Previous baseline: v0.2.0 (modes and workflow release — `run` / `login` /
`env` / `mise init`, `env` / `home` / `overlay` modes, experimental agy
adapter; see git tag v0.2.0).

## Scope

- **Keychain items captured and restored verbatim.** Raw item bytes are
  stored and written back unchanged; kae no longer re-serializes the JSON
  payload (2-space indent, sorted keys), which Claude Code rejected as
  "Not logged in" despite a semantically identical token. Pointer fields
  are now structure guards only.
- **`kae login` auth-unchanged detection.** When the spawned login flow
  exits without changing the tool's credentials (e.g. the tool refuses
  `/login` in the current environment), kae refuses to capture, exits `11`,
  and reports `auth_unchanged` instead of silently duplicating the previous
  account under the new name.
- **Validation hardening (docs).** Real-machine acceptance now requires a
  fresh-process identity check (`claude -p ... </dev/null` must answer, not
  "Not logged in"); macOS smoke checks are documented as unable to isolate
  the claude keychain (Linux-only for the claude fixture block).

## Non-Goals (this release)

Codex/agy keyring drivers, remaining login UX polish (claude `/login`
version differences, agy), `env export --dotenv --reveal`, performance
polish, claude file-driver override for isolated smoke checks, Windows,
gemini/agy home isolation. See [ROADMAP.md](ROADMAP.md).

## Acceptance Criteria

- Real-machine (macOS, real accounts): `kae switch claude <account>` between
  two accounts, `kae login claude <account> --restore`, and `kae rollback`
  all leave a **fresh** claude process authenticated (AUTH-OK), with
  `~/.claude.json` drift limited to the allowlist.
- A login flow that exits without changing auth yields exit `11` /
  `auth_unchanged` and captures nothing (regression-tested).
- Snapshots from the pre-verbatim format are refused by the structure guard
  with exit `10` (re-capture resolves).
- `mise run check` passes; JSON reports keep `schema_version: 1`, stable
  tokens, `[]` arrays.

## Release Steps

1. Acceptance criteria green; `docs/VALIDATION.md` checklist done.
2. README examples verified against the built binary.
3. Tag `v0.3.0`, GitHub release with notes.
