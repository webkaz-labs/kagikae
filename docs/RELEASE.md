# Release Target: kae v0.1.0

Phase 1 — auth-mode MVP on macOS and Linux. Pre-stable: contracts may still
change with clear release notes.

## Scope

Commands: `init`, `doctor`, `capture`, `switch` (single tool and `all`
profile), `current`, `accounts`, `status`, `backup list`, `rollback`,
`version`, `help`, with `--dry-run`, `--json` / `--format`, `--yes`,
`--no-color`, `--config`.

Adapters and drivers:

| Tool | v0.1.0 coverage |
|------|------------------|
| claude | `claude-file-patch` (Linux), `claude-keychain-patch` (macOS) |
| codex | `codex-auth-json`; keyring store detect-only with refusal guidance |
| gemini | `gemini-oauth-cache`; auth-type detection; Antigravity transition warning |
| agy | detect-only (`doctor`); capture/switch refuse with `unsupported` |

Infrastructure: XDG paths, TOML config with unknown-key warnings, per-tool
locks, atomic writes, pre-write backups with retention pruning, secret
backends (macOS Keychain, Linux libsecret, opt-in file), full redaction,
deterministic exit codes, stable `schema_version: 1` JSON.

## Non-Goals (this release)

`login`, `run`, `env` / `home` / `overlay` modes, mise integration, Codex
keyring writes, Windows, TTY browsers, localization, shell completion. See
[ROADMAP.md](ROADMAP.md).

## Acceptance Criteria

Claude:

- `capture` / `switch` between two accounts changes only
  `/claudeAiOauth` (file or keychain payload) and `/oauthAccount`;
  `settings.json`, `skills/`, `agents/`, and every other `~/.claude.json` key
  are byte-identical afterwards.
- Linux `.credentials.json` stays `0600`. macOS path uses the keychain
  driver; unknown payload structure is refused (exit 10).

Codex:

- only `auth.json` (or its captured snapshot) changes; `config.toml`,
  `hooks.json`, `history.jsonl` untouched.
- effective keyring store is detected and capture/switch refuse with
  actionable guidance.

Gemini:

- only `oauth_creds.json` / `google_accounts.json` change;
  `settings.json` untouched; auth type reported by `doctor`.

Common:

- `switch all work --dry-run` prints the plan and writes nothing.
- `switch all work --yes --json` emits the documented report; no secret value
  appears in any output (tested).
- a second kae process during a switch gets exit 4 (`lock_busy`).
- `kae rollback` restores the pre-switch live state, including artifacts that
  did not exist before (removed again on rollback).
- `mise run check` (test, vet, mod-verify) passes; JSON regression tests
  cover `schema_version`, token stability, ordering, and `[]` encoding.

## Release Steps

1. All acceptance criteria green; `docs/VALIDATION.md` checklist done.
2. README examples verified against the built binary.
3. Tag `v0.1.0`, GitHub release with notes (initial public release).
