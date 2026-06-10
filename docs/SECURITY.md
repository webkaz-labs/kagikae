# Security

`kae` reads, stores, and writes live credentials for other tools. Safety rules
here are part of the command contract.

## Auth-Mode Safety Rules (mandatory)

- Never replace a tool home or a mixed-state file wholesale. Mixed-state files
  (`~/.claude.json`) are patched only through the JSON Pointer allowlist
  defined in [ADAPTERS.md](ADAPTERS.md).
- Never delete unknown keys; preserve everything outside the allowlist.
- Back up the live artifacts before every write; rollback must always be
  possible (`kae rollback`).
- Hold the per-tool lock for the entire read-modify-write window.
- All file writes are atomic (temp file + rename, same directory) and set
  mode `0600` for credential files.
- Validate structure before writing; refuse with `unsafe_refused` (exit 10)
  when the live layout is unrecognized.
- Support `--dry-run` on every mutating command.

## Secret Handling

- Secret values never enter stdout, stderr, logs, JSON reports, error
  messages, or metadata files. Reports reference artifacts by name, kind,
  target path, and pointer only.
- Account snapshot payloads and backup payloads are stored in the secret
  backend (OS credential store by default; see
  [DATA-MODEL.md](DATA-MODEL.md#secret-references)).
- The plaintext `file` backend requires explicit
  `security.secret_backend = "file"` in config. It writes `0600` files under
  a `0700` directory and `doctor` permanently warns while it is active.
- `kae` never stores secrets in TOML and never echoes captured values back
  for confirmation.

## Subprocesses

- `security`, `secret-tool`, and binary detection run through
  `internal/runner` with `exec.CommandContext` and argv arrays (no shell
  strings).
- Keychain payloads are passed to `security` via argv, not stdin echo through
  a shell; stdout of `security find-generic-password -w` is treated as secret
  and redacted from any diagnostics.
- User-controlled account/profile names are validated against
  `[a-zA-Z0-9._-]{1,64}` before use in paths, lock names, or secret keys.

## File Permissions

- `~/.claude/.credentials.json`, `~/.codex/auth.json`, and Gemini OAuth cache
  files are written `0600`; kagikae metadata/state dirs `0700`.
- `doctor` warns when live credential files are group/world readable.

## Environment Conflicts

`doctor` warns when environment variables override the subscription login the
user thinks they are switching: `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`,
`CLAUDE_CODE_OAUTH_TOKEN`, `GEMINI_API_KEY`, `GOOGLE_APPLICATION_CREDENTIALS`.

## Concurrency

`auth` mode mutates shared live state. Per-tool locks serialize kae against
itself, but cannot stop the upstream CLI from refreshing tokens concurrently.
Therefore:

- locks are held across the whole switch transaction;
- simultaneous different accounts for one tool are unsupported in `auth`
  mode (documented; `home` mode is the supported path);
- the planned `kae run` recaptures refreshed credentials before restoring
  the previous state (Phase 2).

## External Tools

| Tool | Use | Trust boundary |
|------|-----|----------------|
| `security` (macOS) | keychain read/write | output of `-w` is secret |
| `secret-tool` (Linux) | libsecret read/write | stdin used for store; output of lookup is secret |
| upstream CLIs | binary presence detection only in v0.1.0 | never invoked with credentials |
