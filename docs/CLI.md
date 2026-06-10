# CLI Contract

Command surface, flags, exit codes, and output contracts for `kae`.
All commands are non-interactive in v0.1.0; `--yes` is accepted everywhere for
forward compatibility and currently changes nothing.

## Commands

```bash
kae                                  # status summary (same as kae status)
kae init                             # create config and data directories
kae doctor [tool] [--json]           # environment / auth health checks
kae capture <tool> <account>         # snapshot the live auth state into an account
kae switch <tool> <account>          # apply a captured account to the live state
kae switch all <profile>             # switch every enabled tool in a profile
kae s <...>                          # alias of switch
kae current [--json]                 # active account per tool (short)
kae accounts [--json]                # captured accounts, active markers
kae status [--json]                  # full status report
kae backup list [--json]             # list switch backups
kae rollback [--to <backup-id>]      # restore the most recent (or given) backup
kae version | --version | -v
kae help | --help | -h
```

Tool names: `claude`, `codex`, `gemini`, `agy`. Account and profile names must
match `[a-zA-Z0-9._-]+` (max 64 chars); anything else is a usage error.

## Global Flags

| Flag | Commands | Meaning |
|------|----------|---------|
| `--json` | structured commands | shorthand for `--format json` |
| `--format text\|json` | structured commands | output format |
| `--dry-run` | `capture`, `switch`, `rollback` | print planned actions, write nothing |
| `--yes` | all | non-interactive confirmation (reserved; no prompts exist yet) |
| `--no-color` | all | disable color in human text output |
| `--config <path>` | all | explicit config file path (overrides XDG lookup) |

## Exit Codes

| Code | Token | Meaning |
|------|-------|---------|
| `0` | `ok` | success |
| `1` | `error` | general/runtime error |
| `2` | `invalid_config` | config file unreadable or invalid |
| `3` | `auth_missing` | live auth state not found for the requested tool |
| `4` | `lock_busy` | another kae process holds the per-tool lock |
| `5` | `unsupported` | platform or tool operation not supported |
| `6` | `cli_missing` | upstream CLI binary not found when required |
| `7` | `not_found` | account / profile / backup not found |
| `8` | `permission` | file permission or access error |
| `9` | `secret_store` | secret backend unavailable |
| `10` | `unsafe_refused` | live state failed a structure guard; write refused |
| `64` | `usage` | usage / flag error |

These codes diverge intentionally from the minimal shared standard (`0/1/2/64`)
because agents need to branch on switch failures; the token column appears as
`error_code` in JSON error reports.

`doctor` exits `0` when no error-severity findings exist (warnings allowed)
and `1` when at least one check has `status: "error"`. The specific codes above
are reserved for operations where a single cause fails the command.

`switch all` applies per-tool results independently; if any tool fails, the
command exits with the first failing tool's code after attempting rollback of
the tools already switched in the same transaction.

## Output Rules

- Human reports go to stdout; usage and runtime errors go to stderr.
- JSON mode never emits color, progress, prompts, or localized text.
- Secret values never appear in any output, log, or error message; artifacts
  are referenced by name and location only.
- Agent-facing arrays encode as `[]`, never `null`.
- All stable reports carry integer `schema_version` (currently `1`).
- JSON errors: `{"ok": false, "error_code": "<token>", "message": "..."}` on
  stdout with the matching exit code.

## JSON Reports

### `kae status --json`

```json
{
  "schema_version": 1,
  "ok": true,
  "active_profile": "work",
  "mode": "auth",
  "tools": [
    {
      "tool": "claude",
      "enabled": true,
      "account": "work",
      "driver": "claude-keychain-patch",
      "auth_present": true,
      "accounts": ["personal", "work"],
      "warnings": []
    }
  ]
}
```

`account` is `null` when kae has not switched/captured this tool yet.
`active_profile` is `null` when the per-tool accounts do not match any profile.

### `kae accounts --json`

```json
{
  "schema_version": 1,
  "accounts": [
    {
      "tool": "claude",
      "account": "work",
      "driver": "claude-keychain-patch",
      "active": true,
      "captured_at": "2026-06-11T01:23:45Z"
    }
  ]
}
```

Ordering: tool (claude, codex, gemini, agy), then account name ascending.

### `kae doctor --json`

```json
{
  "schema_version": 1,
  "ok": true,
  "platform": "darwin",
  "secret_backend": "keychain",
  "checks": [
    {
      "tool": "claude",
      "code": "binary_present",
      "status": "ok",
      "message": "claude found in PATH"
    },
    {
      "tool": "claude",
      "code": "env_conflict",
      "status": "warn",
      "message": "ANTHROPIC_API_KEY is set and overrides subscription login"
    }
  ]
}
```

Check `status` vocabulary: `ok`, `warn`, `error`, `skipped`.
Stable check codes include: `binary_present`, `auth_present`, `driver`,
`env_conflict`, `credential_store`, `secret_backend`, `config_valid`,
`transition_notice`, `unsupported`.

### `kae switch ... --json`

```json
{
  "schema_version": 1,
  "ok": true,
  "dry_run": false,
  "profile": "work",
  "backup_id": "20260611T012345Z",
  "results": [
    {
      "tool": "claude",
      "account": "work",
      "driver": "claude-keychain-patch",
      "applied": true,
      "actions": [
        {"kind": "keychain", "target": "Claude Code-credentials", "pointer": "/claudeAiOauth"},
        {"kind": "json-pointer", "target": "~/.claude.json", "pointer": "/oauthAccount"}
      ],
      "warnings": []
    }
  ]
}
```

`capture --json` uses the same shape with `"captured": true` instead of
`"applied"` and no `backup_id`. With `--dry-run`, `ok` reflects whether the
plan is valid and `actions` lists what would change.

### `kae backup list --json`

```json
{
  "schema_version": 1,
  "backups": [
    {
      "id": "20260611T012345Z",
      "created_at": "2026-06-11T01:23:45Z",
      "reason": "switch",
      "tools": ["claude", "codex"]
    }
  ]
}
```

Ordering: newest first.

### `kae rollback --json`

```json
{
  "schema_version": 1,
  "ok": true,
  "backup_id": "20260611T012345Z",
  "restored": [
    {"tool": "claude", "artifacts": 2}
  ]
}
```

### `kae version --format json`

Template-standard shape: `schema_version`, `tool`, `version`, `major`,
`minor`, `patch`, `contract` (`pre_stable` for v0.x).

## Human Text

- Summary first: active profile, then a per-tool table
  (`Tool / Account / Driver / Auth / Notes`).
- `switch --dry-run` prints a `Would switch` plan grouped per tool with the
  patched targets and an explicit `preserved` reminder line.
- Color is semantic only (ok green, warn yellow, error red) and disabled for
  non-TTY or `--no-color` / `NO_COLOR`.
- East Asian width is not specially handled in v0.1.0 (ASCII table output).

## Localization

Human messages are English in v0.1.0. JSON tokens are stable English
regardless of locale.
