# CLI Contract

Command surface, flags, exit codes, and output contracts for `kae`.
All commands are non-interactive in v0.1.0; `--yes` is accepted everywhere for
forward compatibility and currently changes nothing.

## Commands

Two verbs by scope, two flags by environment: `use` switches globally, `pin`
binds the current directory; `-s`/`--shared` (default) shares the real home,
`-i`/`--isolated` keeps a private home. `run` wraps one process.

```bash
kae                                  # status summary (same as kae status)
kae init                             # create config and data directories
kae edit                             # open the config in $VISUAL / $EDITOR, then re-validate
kae doctor [tool] [--json]           # environment / auth health checks (alias: kae d)
kae add <tool> [<account>] [--restore] # register an account: official login flow + snapshot
kae add --no-login <tool> [<account>]  # snapshot the current live auth state instead
                                     #   (account name optional: auto-detected from the live login)
kae use [-s|-i] [-P <profile>]       # bare: resolve the profile and apply it idempotently
                                     #   (--quiet suppresses success report; folds kae apply)
kae use [-s|-i] <profile>            # switch every enabled tool now, global (alias: kae u)
kae use [-s|-i] <tool> <account>     # switch one tool now, global
kae pin [-s|-i] [<profile>]          # bind this directory (alias: kae p; default shared)
kae pin [-s|-i] <tool> <account>     # re-bind one tool in this directory
kae unpin                            # delete the kae-owned mise fragment
kae run [-s|-i|--env] [-P <profile>] <tool|all> <name> -- <cmd...>
                                     # run cmd with an account applied (alias: kae r)
kae env set <tool> <account> KEY=VALUE...          # store env-mode variables
kae env set <tool> <account> KEY                   # value read from stdin
kae env unset <tool> <account> [KEY...]            # remove variables / the profile
kae env list [--json]                              # profiles (names only, no values)
kae mise init [-P <profile>] [--auto] [--write]    # auth-mode tasks + opt-in hook
                                                   # (bind directories with kae pin instead)
kae accounts [--json]                # registered accounts, active markers
kae ls [--json]                      # accounts and profiles in one view
kae account rm <tool> <account> [--force]      # delete a captured account
kae account rename <tool> <old> <new>          # rename a captured account
kae profile save <name>              # snapshot the active accounts into a profile
kae profile set <name> <tool> <account>        # set one profile mapping
kae profile unset <name> <tool>      # drop one profile mapping
kae profile rm <name> [--force]      # delete a profile
kae profile default [<name>|--clear] # show or set default_profile
kae status [--json]                  # full status report (alias: kae s)
kae backup list [--json]             # list switch backups
kae rollback [--to <backup-id>]      # restore the most recent (or given) backup
kae completion <bash|zsh|fish> [--install]     # print (or register) a dynamic completion script
kae version | --version | -v
kae help | --help | -h
```

Tool names: `claude`, `codex`, `agy`, `opencode`, `cursor`, `copilot`.
Any unambiguous prefix is accepted in tool positions of `use`, `pin`, `run`,
`add`, `account`, and `env` (e.g. `cl`→`claude`, `cod`→`codex`, `cu`→`cursor`,
`cop`→`copilot`, `o`→`opencode`, `a`→`agy`). Ambiguous prefixes (`c`, `co`)
are a usage error naming the candidate list. Prefixes are resolved to the
canonical name; they are never stored.
Account and profile names must match `[a-zA-Z0-9._-]+` (max 64 chars);
anything else is a usage error.
`gemini` was removed in v0.6.0 (successor: `agy`); it fails as an unknown
tool naming the successor.

Renamed or folded commands (each prints its replacement with exit `64` for one
release):
- v0.8.0: `apply` → bare `kae use [--quiet]`
- v0.7.2: `bond` → `pin --shared`, `as <tool> <account>` → `pin <tool> <account>`

The `--global` flag is gone — `use` is always global. `sync` → `apply`
(renamed in v0.7.0) is now an unknown command.

Removed in v0.5.0 (each still prints its replacement with exit `64` for one
release): `switch` → `use`, `login` → `add`, `capture` →
`add --no-login`, `current` → bare `kae`. (`s` is no longer the `switch`
pointer — it is the `status` alias since v0.7.2.)

Aliases: `u`=`use`, `p`=`pin`, `r`=`run`, `d`=`doctor`, `s`=`status`.

## Global Flags

| Flag | Commands | Meaning |
|------|----------|---------|
| `--json` | structured commands | shorthand for `--format json` |
| `--format text\|json` | structured commands | output format |
| `--shared` / `-s` | `use`, `pin`, `run` | share the real home (default); credential private |
| `--isolated` / `-i` | `use`, `pin`, `run` | private home via a kae-owned mise fragment (global: `~/.config/mise/conf.d/kagikae.toml`; per-dir: `./.config/mise/conf.d/kagikae.toml`) |
| `--env` | `run` | inject env-profile vars only (no home redirect, no lock) |
| `--dry-run` | `add --no-login`, `use`, `pin`, `rollback` | print planned actions, write nothing |
| `--yes` | all | non-interactive confirmation (reserved; no prompts exist yet) |
| `--no-color` | all | disable color in human text output |
| `--config <path>` | all | explicit config file path (overrides XDG lookup) |
| `--quiet` | bare `use` | suppress the success report (for hooks); errors still reported |
| `--profile <name>` / `-P <name>` | bare `use`, `run`, `mise init` | resolve a named profile instead of the default; `-P` is the short form |
| `--restore` / `--no-login` | `add` | restore the previous login after capturing (login flow only); snapshot without a login flow |
| `--auto` / `--write` | `mise init` | add the enter hook (`kae use --quiet`); write/update `.mise.toml` |
| `--to <backup-id>` | `rollback` | backup to restore (default: most recent) |

## kae use Semantics

`kae use` switches in global scope (alias `kae u`). It always acts on the real
home — inside a pinned directory it ignores the directory's isolation env vars
and prints a one-line warning that the change is global (the directory keeps
its binding; re-bind it with `kae pin`).

**Bare `kae use [-s|-i] [-P <profile>]`** (no positional): resolves the
profile from `--profile`/`-P`, then `$KAE_PROFILE`, then config
`default_profile` (none of them set is a usage error), and applies it
**idempotently**. When kae's recorded active state (`state.json active`) already
matches, it exits `0` with `"changed": false`, taking no locks and writing no
backups; external drift is neither verified nor repaired. Otherwise it performs a
full apply. `--quiet` suppresses the human success report (for enter hooks);
with `--json` the report is still emitted so a script can read `changed`.
Errors are still reported. This is the safe form for hooks and scripts (the
former `kae apply`).

**`kae use [-s|-i] <profile>`** or **`kae use [-s|-i] <tool> <account>`**
(explicit positional): always applies, even when the recorded state already
matches.

- `--shared` / `-s` (default): patch the credential in place; skills, hooks,
  memory, MCP, and trust stay shared with the real home. Same JSON report shape,
  exit codes, and backups as the removed `switch`. This is also the teardown of
  `kae use -i`: it drops the tool from `state.json synced`, regenerates or
  deletes the global mise fragment, and then patches the real home in place.

  Before overwriting the live store, a shared switch **recaptures the
  currently-active account** when its live credential diverges from its snapshot
  (symmetric with `run -s`), so a later switch back applies the token that was
  live at switch-away rather than a stale capture. It is divergence-gated (no
  write when they match) and best-effort: a logged-out active account is left
  untouched with a warning, never aborting the switch. If the account being
  switched **to** has an expired snapshot with no refresh token, kae warns and
  names `kae add` but still proceeds (a snapshot with a refresh token proceeds
  silently — the tool self-refreshes). The warning rides in each result's
  `warnings` array. Only `kae use` / bare `use` recapture; `use -i` / `pin` /
  `run -i` write kae-owned isolation dirs and never the real store.
- `--isolated` / `-i`: point every terminal at a per-account private home
  **without touching `~/.claude`**. kae prepares
  `isolation/global/<tool>/<account>/` (docs/DATA-MODEL.md) and writes a
  kae-owned global mise fragment `~/.config/mise/conf.d/kagikae.toml` exporting
  `CLAUDE_CONFIG_DIR` / `CODEX_HOME`, regenerated from `state.json synced`.
  claude and codex only; a profile that also maps a tool with no home-isolation
  env var (agy, opencode, cursor, copilot) skips it with a warning and isolates
  claude/codex only — a single explicit unsupported tool exits `5`. Requires
  global `mise activate` (otherwise kae warns and prints the `export` line).
  Teardown is `-s` / bare `kae use`.

## kae run Semantics

`kae run [-s|-i|--env] [-P <profile>] <tool|all> <name> [-- <cmd...>]` executes
the child with inherited stdio and returns the **child's exit code verbatim** on
success; the exit-code table below applies only to failures before the child
starts and to a failed restore afterwards (which returns the kae error code of
the failure cause, with `kae rollback` guidance). `-P <profile>` is sugar for
`all <profile>` and takes no positional; otherwise exactly one tool/account pair
is required. At most one of `-s`, `-i`, `--env` may be set.

**Default child**: with no `-- <cmd>`, a single-tool target runs that tool's
upstream binary (`kae run claude work` ⇒ runs `claude`; cursor ⇒ `cursor-agent`;
agy ⇒ `agy`), so opening a session under another account no longer needs the
redundant trailing `-- <tool>`. An explicit `-- <cmd>` still wins. A profile
(`-P` / `all`) target or a tool with no launchable binary has no single default
and still requires `-- <cmd>`, erroring (exit `64`) when it is missing.

- `-s` (default): per-tool locks are held for the entire child run; the live
  state is backed up (`reason: "run"`), the target accounts applied, and after
  the child exits kae **recaptures refreshed credentials into the account
  snapshots** and restores the previous live state. (This is the former `auth`
  mode.)
- `-i`: runs the child with the per-account global isolated home
  (`isolation/global/<tool>/<account>/`) injected via the tool's home-isolation
  env var. This home is **shared with `kae use -i`** for the same account; no
  lock and no live mutation, so a concurrent `kae use` in another terminal is
  never blocked and never sees the isolated process. `run -i` prints the exact
  home and that it is shared with `kae use -i`, so the shared state is never
  invisible. claude and codex only; a profile including an unsupported tool
  skips it with a warning, an explicit unsupported tool exits `5`. (This is the
  former `home` mode, reusing the global-isolated store.)
- `--env`: injects the tool/account env profile (`kae env set`) into the child
  only; no home redirect, no lock. (This is the former `--mode env`.)

The former `--mode` flag and its values (`auth|env|home|overlay|bond|pin`) are
**removed** in v0.8.0. A command using `--mode` exits `64` with a usage error.
`overlay` and per-directory `bond`/`pin` via `run` are retired; bind a
directory with `kae pin -s|-i` instead.

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
of recording a duplicate of the previous account. **agy has no login flow**
(GUI/browser OAuth, no kae-drivable login subcommand), so `kae add agy` is
`--no-login` only; the account name is auto-detected from the active Google
account (`~/.gemini/google_accounts.json`) when omitted, like the other tools
(v0.8.7), and an explicit name still wins.

`kae add --no-login <tool> <account>` snapshots the current live auth state
under the name without launching anything (it supports `--dry-run`; the
login flow does not, and `--restore` requires the login flow).

**Account name auto-detection.** The account name is optional. With it omitted
(`kae add <tool>`), kae derives a default from the live login identity: the
`--no-login` form reads the current live state, the login form reads the
post-login state (the name is resolved only after the flow exits). The raw
identity is sanitized to `[a-zA-Z0-9._-]` (an email keeps only its local part
before `@`), capped at 64 chars. An explicit name always wins. Detection per
tool: claude `~/.claude.json` `oauthAccount.emailAddress`; codex `auth.json`
(or the keyring payload) `id_token` email claim, else `account_id`; opencode
the `/openai` access token's `https://api.openai.com/profile` email claim, else
its `accountId` UUID (v0.8.8 prefers the email); copilot `config.json`
`/lastLoggedInUser.login`; cursor `cursor-agent status` (`✓ Logged in as
<email>`); agy the active Google account in `~/.gemini/google_accounts.json`
(`.active`, v0.8.7). Every tool now exposes an identity. A detection failure
(logged out, unreadable), or an identity that sanitizes to empty, is a usage
error (`64`) naming the explicit form — never a silent fallback.

**Detected identity is recorded.** At capture (both the explicit-name and
auto-detect forms, login and `--no-login`), kae stores the raw detected identity
(the full email or account id) in the snapshot's `identity` field, separate from
the sanitized account name, so accounts that sanitize to the same name stay
distinguishable. It is best-effort: a detection failure leaves it blank and
never errors, and a snapshot captured before the tool gained identity stays
blank until re-captured (`kae add --no-login <tool> <name>` while logged into
that account backfills it). `kae ls` / `kae accounts` / `kae status` show it (an
`Identity` column; an additive `identity` field in `--json`).

## kae ls Semantics

`kae ls` lists every captured account (with its detected `identity`, blank when
absent) and every defined profile in one read-only view (the data otherwise
split across `kae accounts` and `kae status`), each with an active marker. It
takes no locks and writes nothing. `--json` keeps `schema_version: 1` and `[]`
arrays, reusing the `kae accounts` account rows and the `kae status` profile
rows.

## kae account Semantics

`kae account rm <tool> <account>` deletes a captured account: its snapshot
directory and every secret-backend item. It refuses the **active** account
with exit `10` (`unsafe_refused`) unless `--force`, which also drops the tool
from `state.json` `active` and recomputes the active profile. Any `[profiles]`
entry that maps the tool to the account has that `accounts.<tool>` key removed
in the same run (the profiles are named in the output); `kae account rm` never
refuses on a profile reference. Unknown account exits `7` (`not_found`).
`--dry-run` prints the plan (including the profile edits) and writes nothing.

`kae account rename <tool> <old> <new>` renames a captured account: it
copy-then-deletes each secret item (backend keys cannot be renamed in place),
moves the snapshot directory and metadata, updates `state.json active[tool]`
if it pointed at `<old>`, and rewrites every `[profiles]` reference from
`<old>` to `<new>` (named in the output). It refuses an existing `<new>` with
exit `10`, an unknown `<old>` with exit `7`, and sanitizes `<new>` with the
account-name rule. `--dry-run` writes nothing.

Both hold the per-tool lock plus the config lock, and edit `config.toml`
through a comment-preserving writer (comments, field order, and unrelated keys
survive). Limitation: existing backups are **not** rewritten — a backup's
`Meta.ActiveBefore` keeps the old account name (see
[DATA-MODEL.md](DATA-MODEL.md)).

## kae profile Semantics

`kae profile` manages `[profiles]` entries without hand-editing TOML (the
scriptable, validated counterpart to `kae edit`); every mutation goes through
the comment-preserving writer under the config lock and supports `--dry-run`:

- `save <name>` overwrites profile `<name>` from the current `state.json`
  active accounts (a hand-written `label` is preserved; stale tool mappings are
  not). No active accounts exits `7`.
- `set <name> <tool> <account>` sets one `accounts.<tool>` mapping, creating
  the profile if absent. The account must be captured (else exit `7`); the
  profile name, tool, and account are validated.
- `unset <name> <tool>` drops one mapping; if it was the last, the now-empty
  profile is removed (and `default_profile` cleared if it pointed there).
  Unknown profile or tool exits `7`.
- `rm <name>` deletes the whole profile. Removing the `default_profile` exits
  `10` unless `--force`, which also clears `default_profile`. Unknown exits `7`.
- `default <name>` sets `default_profile` (unknown profile exits `7`); bare
  `default` prints the current value; `default --clear` empties it.

## kae pin and mise init Semantics

`kae pin [-s|-i] [<profile>]` binds the current directory to a profile by
writing a kae-owned mise fragment `./.config/mise/conf.d/kagikae.toml` (added to
`.gitignore`); the user's `mise.toml` is **never** touched. The profile defaults
to `default_profile`. `kae pin [-s|-i] <tool> <account>` re-binds **one** tool in
the directory, leaving the others and the sharing set intact (the v0.7.1
`kae as`). `kae p` is the alias. `kae unpin` deletes the kae-owned fragment and
also strips a pre-v0.7.2 kagikae marker block from `mise.toml` (so `kae unpin &&
kae pin` migrates cleanly), leaving the user's own `mise.toml` content and any
isolation directories (with their login state) intact.

`kae pin` defaults to **shared** (`-s`); pass `-i` for isolated:

- **`-s` / `--shared`** (default): the fragment points each tool at a
  per-directory shared home (`isolation/<pin-id>/<tool>/shared/`): every
  real-home file except the hard-coded auth artifacts (`.credentials.json`,
  `auth.json`) is symlinked in; the credential is private-copied at `0600`.
  Settings, sessions, and memory are shared with the real home while
  authentication is private to the directory. The bound account is recorded in
  the fragment so `kae status` and the profile match survive re-entry. See
  docs/ADAPTERS.md for the per-tool denylist and `shared_denylist_extra`.
- **`-i` / `--isolated`**: the fragment points `CLAUDE_CONFIG_DIR` / `CODEX_HOME`
  at the per-account isolated config dirs
  (`isolation/<pin-id>/<tool>/isolated/<account>/config/`): all state (auth,
  sessions, memory, settings) is private to the account. Items listed in
  `tools.<tool>.isolated_shared_items` are symlinked from the real home; the
  credential is always private-copied at `0600`. Re-running refreshes the opt-in
  links and the credential copy.

`kae mise init [-P <profile>] [--auto] [--write]` renders auth-mode tasks and
the opt-in enter hook into a marker-delimited block in `.mise.toml`. Default
prints the snippet to stdout; `--write` creates `.mise.toml` or replaces an
existing kagikae block. `--auto` adds a `[hooks.enter]` entry running
`kae use --quiet`. `-P` selects the profile (falls back to `default_profile`).

The block carries the fixed-profile tasks (`ai-use`, `ai-current`, and a per-
enabled-tool `run` task) plus two argument-taking tasks with dynamic
completion: `ai-switch <profile>` (switch all tools to a profile) and
`ai-switch-tool <tool> <account>` (switch one tool). Their `usage`
`complete "<arg>" run="kae __complete …"` directives resolve candidates from
the same backend as kae's own shell completion, so `mise run ai-switch <TAB>`
offers live profiles and `mise run ai-switch-tool <TAB>` offers live tools and
accounts. Account completion in the task is **not** tool-scoped — mise's
`complete run` does not expose the prior `tool` argument, so it lists every
account; kae's own shell completion keeps the tool-scoped behavior. Task-
argument completion is project-scoped (it lives in the project's `.mise.toml`),
the opposite of kae's own completion, which is global (see "Shell completion").

The former isolation modes (`--mode bond|pin|home|overlay`) are **removed** in
v0.8.0 — passing any of them exits `64`. Bind a directory with `kae pin -s|-i`
instead (which writes a kae-owned fragment, not via `mise init`).

Isolation requires the profile to be defined (its accounts pick the per-account
paths). Tools without a stable home env var (agy, opencode, cursor, copilot)
keep their real home and are noted with an inline warning comment, as are tools
with the per-tool mechanism disabled in config. The environment is
per-invocation (per directory), deliberately not a profile property: the same
profile stays usable for global switching and isolated project homes.

**Migration**: `kae bond` is now `kae pin --shared` and `kae as` is now
`kae pin <tool> <account>` (both print an exit-`64` pointer for one release).
`kae apply` is now bare `kae use [--quiet]` (prints an exit-`64` pointer for
one release). Directories pinned before v0.7.2 carry a kagikae marker block
inside their `mise.toml`; run `kae unpin && kae pin` once to migrate to the
fragment.

## Shell completion

`kae completion <bash|zsh|fish>` prints a **dynamic** completion script: instead
of baking a word list at generation time, the script calls the hidden
`kae __complete <kind>` backend at completion time, so candidates always track
the live router, config, and captured state. Word 1 completes commands; the
argument positions complete tools/profiles, and `kae use claude <TAB>` scopes to
claude's accounts (the tool word is passed to `kae __complete accounts <tool>`).
Positions are computed from the **flag-filtered** argument list, so a flag
before the positionals does not shift completion (`kae add --no-login <TAB>`
still completes tools; `kae use -i claude <TAB>` completes claude's accounts).
When the current word starts with `-`, the command's **flag names** are
completed (`kae add --<TAB>` → `--no-login` / `--restore`; `kae run -<TAB>` →
`-s` / `-i` / `--env` / `-P`).

`kae __complete <commands|tools|profiles|accounts [<tool>]|flags <command>>` is
read-only, takes no locks, prints one candidate per line, and is intentionally
hidden from `kae help`. The `flags` kind lists a command's flags from the same
per-command registrars the parser uses, so the completion set never drifts. Its
line-oriented output is an internal contract consumed by the generated scripts
and the `kae mise init` task `complete` directives — it is not the JSON contract
(`schema_version` is unaffected).

**bash and zsh are the verified shells.** `kae completion fish` stays available
as a best-effort generator (unit-tested and `fish -n`-valid) but is not a
release-gated, officially-verified surface (dropped 2026-06-18).

kae's own completion is **binary-scoped**, so it is registered globally, never
per-directory (a per-directory registration would make `kae <TAB>` blink in and
out by directory). Three registration paths, non-mise first:

1. **rc eval** — add `eval "$(kae completion zsh)"` (bash/zsh) or
   `kae completion fish | source` to your shell rc. No files written.
2. **completion file** — write the script to the shell's standard completions
   dir (bash-completion and fish auto-load it; for zsh ensure the dir is on
   `fpath`). `kae completion <shell> --install` does this for you (the default).
3. **`kae completion <shell> --install`** — interactive: it detects whether mise
   is active, then offers (1) the completions-dir file [default], (2) a global
   mise `[hooks.enter]` that sources the script (opt-in), or (3) print-only. The
   install is idempotent and **never** mutates the global mise config unless you
   pick option 2; a global config that already defines `[hooks.enter]` outside
   kae's marker block is refused (exit `10`) with manual guidance.

The mise enter-hook path is an opt-in convenience, not the primary route: mise
hooks are experimental and need `mise activate`, a trusted config, and
`mise settings experimental=true`. Distinct from §"kae pin and mise init": that
project-scoped task-argument completion lives in the project's `.mise.toml`;
this is kae's own global shell completion.

## Did-you-mean hints

When an unknown command, tool, or profile is close to a real one, the usage
error names the single nearest match — `kae uze` → "did you mean `use`?",
`kae add clade` → "did you mean `claude`?", `kae use wrok` → "did you mean
`work`?". The hint is **suggestion-only**: the command still fails with its
original exit code (`64`/`usage` for a command or tool, `7`/`not_found` for a
profile) and the JSON contract is unchanged; only the human-facing message gains
the hint.

The candidate lists are exactly the ones `kae __complete commands|tools|profiles`
returns (commands include the one-letter aliases `u`/`p`/`s`/`d`/`r`), so a
suggestion never drifts from the real surface. To avoid noise a hint appears only
when the best edit distance is both `<= 2` and `<= len(input)/3 + 1`; a tie for
the best distance, an exact match, or a wildly different token (`kae zzzzz`)
appends nothing. Account names and flags are not suggested, and only one
best-match candidate is named (no multi-candidate list).

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
| `10` | `unsafe_refused` | a write was refused as unsafe: a structure guard failed, or an account remove/rename would hit the active account (no `--force`) or overwrite an existing one |
| `11` | `auth_unchanged` | login flow exited without changing auth; nothing captured |
| `64` | `usage` | usage / flag error |

These codes diverge intentionally from the minimal shared standard (`0/1/2/64`)
because agents need to branch on switch failures; the token column appears as
`error_code` in JSON error reports.

`doctor` exits `0` when no error-severity findings exist (warnings allowed)
and `1` when at least one check has `status: "error"`. The specific codes above
are reserved for operations where a single cause fails the command.

A profile-wide `use` (and bare `use` when it applies) applies per-tool
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
  "pinned": {"profile": "personal", "mode": "shared"},
  "active_profile": "work",
  "mode": "auth",
  "global_isolated": [
    {"tool": "claude", "account": "work", "home": "/Users/you/.local/share/kae/isolation/global/claude/work"}
  ],
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
     "accounts": {"claude": "personal"}, "active": false},
    {"name": "work", "accounts": {"claude": "work"}, "active": true}
  ]
}
```

`account` is `null` when kae has not registered this tool yet.
`active_profile` prefers the recorded profile (state.json) and falls back to
matching the per-tool accounts; it is `null` when neither resolves. `pinned`
is `null` outside bound directories; inside one it reflects the exported
`KAE_PROFILE` and the environment inferred from where the tools' env vars point
(`shared`, `isolated`, or `auth` when only the profile is exported). The bound
account shown for each tool is the real per-tool account (resolved from the
isolated path or the recorded shared-dir account), never a stale profile label.
`profiles` lists every defined profile (name ascending) with its mapping and an
`active` marker. `global_isolated` lists every tool currently pointed at a
global isolated home by `kae use -i` or `kae run -i`, with its private home
path; it is `[]` when no tool is globally isolated. The human text leads with
the same data: the global-isolated homes (if any), the pin banner, the global
active profile, the per-tool table, then the profiles list.

### `kae accounts --json`

```json
{
  "schema_version": 1,
  "accounts": [
    {
      "tool": "claude",
      "account": "work",
      "identity": "work@example.com",
      "driver": "claude-keychain-patch",
      "active": true,
      "captured_at": "2026-06-11T01:23:45Z"
    }
  ]
}
```

Ordering: tool (claude, codex, agy, opencode, cursor, copilot), then
account name ascending. `identity` (the raw detected login identity) is
additive and `omitempty` — absent for pre-v0.8.3 snapshots and tools with no
readable identity; `schema_version` stays `1`.

### `kae ls --json`

```json
{
  "schema_version": 1,
  "accounts": [
    {"tool": "claude", "account": "work", "identity": "work@example.com",
     "driver": "claude-keychain-patch", "active": true,
     "captured_at": "2026-06-11T01:23:45Z"}
  ],
  "profiles": [
    {"name": "work", "accounts": {"claude": "work"}, "active": true}
  ]
}
```

`accounts` reuses the `kae accounts` row shape (same ordering); `profiles`
reuses the `kae status` profile row shape (name ascending). Both are `[]` when
empty.

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
`unsupported`, `file_mode`, `credential_stale`, `secret_orphan`.

Credential-health checks (warn-level):
- `credential_stale`: a captured snapshot is past its `expiresAt` with no
  refresh token, so a switch to it cannot self-heal — names `kae add`. Uses the
  same freshness predicate as the switch-time warning; it inspects only the
  stored snapshot (no live read, so no extra keychain prompt). An expired
  snapshot that still has a refresh token is not flagged (the tool refreshes it).
- `secret_orphan`: a stored secret item has no matching snapshot dir — names
  `kae account rm`. Detected only where the backend can enumerate (file
  `readdir`, Linux `libsecret`); the darwin keychain cannot list by service, so
  the check is silently skipped there (documented gap; docs/SECURITY.md).

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
        {"kind": "keychain", "target": "Claude Code-credentials", "pointer": "/claudeAiOauth"}
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

### Bare `kae use --json` (the idempotent apply report)

The switch report plus a `changed` boolean (no `dry_run`):

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
carry the same per-tool shape as explicit `kae use`. `--quiet` suppresses the
human (text) report only; `--json` still emits the report shown above.

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
