# CLI Contract

Command surface, flags, exit codes, and output contracts for `kae`.
All commands are non-interactive in v0.1.0; `--yes` is accepted everywhere for
forward compatibility and currently changes nothing.

## Commands

One verb per scope: `use` switches now (global), `pin` binds a directory,
`run` wraps one process.

```bash
kae                                  # status summary (same as kae status)
kae init                             # create config and data directories
kae edit                             # open the config in $VISUAL / $EDITOR, then re-validate
kae doctor [tool] [--json]           # environment / auth health checks
kae add <tool> <account> [--restore] # register an account: official login flow + snapshot
kae add --no-login <tool> <account>  # snapshot the current live auth state instead
kae use <profile>                    # switch every enabled tool now (alias: kae u)
kae use <tool> <account>             # switch one tool now
kae pin [<profile>] [--mode overlay|home|auth] [--auto]
                                     # bind this directory (writes .mise.toml)
kae unpin                            # remove the kagikae block from .mise.toml
kae apply [--profile P] [--quiet]    # idempotent profile apply for hooks/scripts
kae run [--mode M] <tool|all> <name> -- <cmd...>   # run cmd with an account applied
kae env set <tool> <account> KEY=VALUE...          # store env-mode variables
kae env set <tool> <account> KEY                   # value read from stdin
kae env unset <tool> <account> [KEY...]            # remove variables / the profile
kae env list [--json]                              # profiles (names only, no values)
kae mise init [--profile P] [--mode auth|home|overlay] [--auto] [--write]
                                     # low-level form of pin (preview first)
kae accounts [--json]                # registered accounts, active markers
kae status [--json]                  # full status report
kae backup list [--json]             # list switch backups
kae rollback [--to <backup-id>]      # restore the most recent (or given) backup
kae version | --version | -v
kae help | --help | -h
```

Tool names: `claude`, `codex`, `agy`, `opencode`, `cursor`, `copilot`.
Account and
profile names must match `[a-zA-Z0-9._-]+` (max 64 chars); anything else is
a usage error.
`gemini` was removed in v0.6.0 (successor: `agy`); it fails as an unknown
tool naming the successor.

Renamed in v0.7.0 (prints its replacement with exit `64` for one release):
`sync` → `apply`.

Removed in v0.5.0 (each still prints its replacement with exit `64` for one
release): `switch`/`s` → `use`, `login` → `add`, `capture` →
`add --no-login`, `current` → bare `kae`.

## Global Flags

| Flag | Commands | Meaning |
|------|----------|---------|
| `--json` | structured commands | shorthand for `--format json` |
| `--format text\|json` | structured commands | output format |
| `--dry-run` | `add --no-login`, `use`, `rollback` | print planned actions, write nothing |
| `--yes` | all | non-interactive confirmation (reserved; no prompts exist yet) |
| `--no-color` | all | disable color in human text output |
| `--config <path>` | all | explicit config file path (overrides XDG lookup) |
| `--mode auth\|env\|home\|overlay` | `run` | switch mode (default `auth`) |
| `--restore` / `--no-login` | `add` | restore the previous login after capturing (login flow only); snapshot without a login flow |
| `--profile <name>` / `--write` | `mise init` | profile for `KAE_PROFILE`; write/update `.mise.toml` |
| `--mode auth\|home\|overlay` / `--auto` | `mise init`, `pin` | rendered integration (`mise init` defaults to `auth`, `pin` to `overlay`); `--auto` adds the enter hook (auth only) |
| `--profile <name>` / `--quiet` | `apply` | profile override; suppress the success report (for hooks) |
| `--to <backup-id>` | `rollback` | backup to restore (default: most recent) |

## kae run Semantics

`kae run` executes the child with inherited stdio and returns the **child's
exit code verbatim** on success; the exit-code table below applies only to
failures before the child starts and to a failed restore afterwards (which
returns the kae error code of the failure cause, with `kae rollback`
guidance). Per mode:

- `auth` (default): per-tool locks are held for the entire child run; the
  live state is backed up (`reason: "run"`), the target accounts applied,
  and after the child exits kae **recaptures refreshed credentials into the
  account snapshots** and restores the previous live state.
- `env`: injects the tool/account env profile (`kae env set`) into the child
  only; no live mutation, no locks.
- `home`: points the tool at an isolated home
  (`CLAUDE_CONFIG_DIR` / `CODEX_HOME` under kae's data dir); agy,
  opencode, cursor, and copilot have no stable isolation mechanism yet and
  are refused.
- `overlay` (default-enabled since v0.5.0; per-tool opt-out via
  `tools.<tool>.overlay_mode_enabled = false`): like `home`, but shared
  items (settings, skills, ...; see docs/ADAPTERS.md) are symlinked from the
  real home while auth/session/history stay private.

## kae edit Semantics

`kae edit` opens the config file in `$VISUAL`, then `$EDITOR`, then `vi`
(the value may carry arguments, e.g. `code --wait`), and re-validates the
result: parse or validation problems exit `2` (`invalid_config`) with the
error, soft issues print as warnings. A missing config exits `7` pointing
at `kae init`; an editor that exits non-zero is reported with exit `1`
(the file is left as last saved, nothing is rolled back).

## kae add Semantics

`kae add <tool> <account>` backs up the live state (`reason: "login"`),
launches the official login flow (`claude /login`, `codex login`,
`opencode auth login`, `cursor-agent login`, `copilot login`), captures
the result into the account, and makes it active — or restores the previous login with
`--restore`. If the flow exits
without changing the live auth state (login refused, window closed, already
cancelled), kae refuses to capture and exits `11` (`auth_unchanged`) instead
of recording a duplicate of the previous account. The agy login flow is not
supported yet.

`kae add --no-login <tool> <account>` snapshots the current live auth state
under the name without launching anything (it supports `--dry-run`; the
login flow does not, and `--restore` requires the login flow).

## kae use and kae apply Semantics

`kae use` switches now, in global scope (alias `kae u`): one positional is a
profile (every enabled tool it maps), two are a tool and an account. Same
JSON report shape, exit codes, and backups as the removed `switch`. It
always applies, even when the recorded state already matches.

`kae apply [--profile P] [--quiet]` is the idempotent variant for hooks and
scripts. Profile resolution order: `--profile`, then `$KAE_PROFILE`, then
config `default_profile` (none of them set is a usage error). When kae's
recorded active state (state.json — kae's belief, not upstream truth, see
docs/DATA-MODEL.md) already matches the profile, it exits `0` with
`"changed": false`, taking no locks and writing no backups; external drift is
neither verified nor repaired. Otherwise it performs a normal full apply
and reports the per-tool results with `"changed": true`. `--quiet` suppresses
the success report entirely (both formats); errors are still reported.

## kae pin and mise init Semantics

`kae pin [<profile>]` binds the current directory to a profile by writing
the marker-delimited kagikae block of `.mise.toml` immediately (profile
defaults to `default_profile`). `kae mise init` is the low-level form: same
flags plus `--write`; without `--write` it previews the block on stdout.
`kae unpin` removes only the block, leaving the rest of `.mise.toml` — and
any overlay/home directories with their login state — intact.

- `--mode overlay` (`pin` default): renders `[env]` entries pointing
  `CLAUDE_CONFIG_DIR` / `CODEX_HOME` at the per-account **overlay** homes
  (docs/DATA-MODEL.md): auth and session state are private to the
  directory while the shared items (settings, skills, ...;
  docs/ADAPTERS.md Isolation) are symlinked from the real home. Writing
  prepares the overlay dirs and links; re-running `pin` refreshes links
  after new shared items appear in the real home (there is no on-entry
  refresh — mise hooks stay experimental). Log in once inside the
  directory per account; the login persists in the overlay.
- `--mode home`: same `[env]` shape but pointing at the fully separate
  home-mode directories — nothing is shared with the real home. `--write`
  pre-creates the directories.
- `--mode auth` (`mise init` default): `[env]` sets `KAE_PROFILE`, plus
  tasks (`ai-use`, `ai-current`, per-tool `kae run` wrappers).
- `--auto` (auth mode only): adds a `[hooks.enter]` entry running
  `kae apply --quiet`, auto-switching on directory entry. Opt-in with an
  inline caveat comment because auth mode mutates the global live state
  shared by every terminal. Firing requires `mise activate`, a trusted
  config, and `mise settings experimental=true` (mise hooks are experimental
  as of mise 2026.6). Combining `--auto` with an isolation mode is a usage
  error: overlay/home already take effect on directory entry via `[env]`.

Isolation modes require the profile to be defined (its accounts pick the
per-account paths). Tools without a stable home env var (agy, opencode,
cursor, copilot) keep their real home and are noted with an inline warning comment,
as are tools with the per-tool mode disabled in config. The mode is per-invocation
(per directory), deliberately not a profile property: the same profile stays
usable for global switching and isolated project homes.

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
| `11` | `auth_unchanged` | login flow exited without changing auth; nothing captured |
| `64` | `usage` | usage / flag error |

These codes diverge intentionally from the minimal shared standard (`0/1/2/64`)
because agents need to branch on switch failures; the token column appears as
`error_code` in JSON error reports.

`doctor` exits `0` when no error-severity findings exist (warnings allowed)
and `1` when at least one check has `status: "error"`. The specific codes above
are reserved for operations where a single cause fails the command.

A profile-wide `use` (and the applying path of `sync`) applies per-tool
results independently; if any tool fails, the command exits with the first
failing tool's code after attempting rollback of the tools already switched
in the same transaction.

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
  "pinned": {"profile": "personal", "mode": "overlay"},
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
  ],
  "profiles": [
    {"name": "personal", "label": "Personal",
     "accounts": {"claude": "work"}, "active": false},
    {"name": "work", "accounts": {"claude": "work"}, "active": true}
  ]
}
```

`account` is `null` when kae has not registered this tool yet.
`active_profile` prefers the recorded profile (state.json) and falls back to
matching the per-tool accounts; it is `null` when neither resolves. `pinned`
is `null` outside pinned directories; inside one it reflects the exported
`KAE_PROFILE` and the isolation mode inferred from where the tools' env vars
point (`auth` when only the profile is exported). `profiles` lists every
defined profile (name ascending) with its mapping and an `active` marker.
The human text leads with the same data: the pin banner, the global active
profile, the per-tool table, then the profiles list.

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

Ordering: tool (claude, codex, agy, opencode, cursor, copilot), then
account name ascending.

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
`unsupported`.

### `kae use ... --json` (the switch report)

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

`profile` is `null` for the tool+account form. `kae add --no-login --json`
uses the same shape with `"captured": true` instead of `"applied"` and no
`backup_id`. With `--dry-run`, `ok` reflects whether the plan is valid and
`actions` lists what would change.

### `kae apply --json`

The switch report plus a `changed` marker (no `dry_run`):

```json
{
  "schema_version": 1,
  "ok": true,
  "changed": false,
  "profile": "work",
  "results": []
}
```

When the profile is applied, `changed` is `true` and `backup_id` / `results`
carry the same per-tool shape as `kae use`. With `--quiet`, the success
report is suppressed entirely.

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

### `kae env list --json`

```json
{
  "schema_version": 1,
  "profiles": [
    {"tool": "claude", "account": "ci", "vars": ["ANTHROPIC_API_KEY"],
     "updated_at": "2026-06-11T01:23:45Z"}
  ]
}
```

Variable values never appear in any output.

### `kae version --format json`

Template-standard shape: `schema_version`, `tool`, `version`, `major`,
`minor`, `patch`, `contract` (`pre_stable` for v0.x).

## Human Text

- Summary first: active profile, then a per-tool table
  (`Tool / Account / Driver / Auth / Notes`).
- `use --dry-run` prints a `Would switch` plan grouped per tool with the
  patched targets and an explicit `preserved` reminder line.
- Color is semantic only (ok green, warn yellow, error red) and disabled for
  non-TTY or `--no-color` / `NO_COLOR`.
- East Asian width is not specially handled in v0.1.0 (ASCII table output).

## Localization

Human messages are English in v0.1.0. JSON tokens are stable English
regardless of locale.
