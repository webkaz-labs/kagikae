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
kae add [...] --identity <value>     # record an identity kae cannot auto-detect
                                     #   (e.g. agy: the live account is server-resolved, not on disk)
kae use [-s|-i] [-P <profile>]       # bare: resolve the profile and apply it idempotently
                                     #   (--quiet suppresses success report; folds kae apply)
kae use [-s|-i] <profile>            # switch every enabled tool now, global (alias: kae u)
kae use [-s|-i] <tool> <account>     # switch one tool now, global
kae pin [-s|-i] [<profile>]          # bind this directory (alias: kae p; default shared)
kae pin [-s|-i] <tool> <account>     # re-bind one tool in this directory
kae unpin [--purge]                  # delete the kae-owned mise fragment
                                     # --purge: also delete this directory's
                                     # per-directory keychain credentials
kae run [-s|-i|--env] [-P <profile>] <tool|all> <name> -- <cmd...>
                                     # run cmd with an account applied (alias: kae r)
kae env set <tool> <account> KEY=VALUE...          # store env-mode variables
kae env set <tool> <account> KEY                   # value read from stdin
kae env unset <tool> <account> [KEY...]            # remove variables / the profile
kae env list [--json]                              # profiles (names only, no values)
kae companion add <profile> <id> KEY=VALUE...      # bind non-secret knobs (git identity, config paths)
kae companion add <profile> <id> KEY               # bind a token knob; value read from stdin
kae companion rm <profile> <id> [KEY...]           # drop knobs, or the whole companion
kae companion list [--json]                        # bindings (knob names + non-secret values)
kae mise init [-P <profile>] [--auto] [--write]    # auth-mode tasks + opt-in hook
                                                   # (bind directories with kae pin instead)
kae accounts [--json]                # registered accounts, active markers
kae ls [--json]                      # accounts and profiles in one view
kae account rm <tool> <account> [--force]      # delete a captured account
kae account rename <tool> <old> <new>          # rename a captured account
kae account set-identity <tool> <account> <value>  # set/replace a captured account's identity
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
  write when they match) and best-effort: it never aborts the switch, and it is
  skipped with a warning when the live state cannot be trusted as that account's —
  a logged-out tool, a live identity whose identifying keys name a different
  account (someone ran the tool's own login outside kae), or a live credential that
  needs a re-login while the snapshot still holds a usable one. That last guard is
  one-directional: kae never prefers the older value, it only refuses to overwrite
  a working credential with a dead one.

  If the account being switched **to** needs a re-login (expired with no usable
  refresh token, or emptied by the tool), kae warns and still proceeds; a snapshot
  whose refresh token is still usable proceeds silently — the tool self-refreshes.
  Warnings go to **stderr, before anything is applied**, so they survive a pipe and
  `--quiet` (which suppresses the success report, never a warning), and a switch of
  several tools closes with one roll-up line naming those that need a re-login. The
  exit code stays `0` — the switch itself succeeded. The same warnings also ride in
  each result's `warnings` array for `--json`. Only `kae use` / bare `use`
  recapture; `use -i` / `pin` / `run -i` write kae-owned isolation dirs and never
  the real store.
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
upstream binary (`kae run claude main` ⇒ runs `claude`; cursor ⇒ `cursor-agent`;
agy ⇒ `agy`), so opening a session under another account no longer needs the
redundant trailing `-- <tool>`. An explicit `-- <cmd>` still wins. A profile
(`-P` / `all`) target or a tool with no launchable binary has no single default
and still requires `-- <cmd>`, erroring (exit `64`) when it is missing.

- `-s` (default): per-tool locks are held for the entire child run; the live
  state is backed up (`reason: "run"`), the target accounts applied, and after
  the child exits kae **re-resolves each tool's credential store**, then
  **recaptures refreshed credentials into the account snapshots** and restores the
  previous live state. The re-resolution matters because the child can move the
  credential to the tool's other store (codex under
  `cli_auth_credentials_store = "auto"` creates its keychain item and deletes
  `auth.json` on its first save); reading the pre-run store instead would report
  the tool as logged out and restore into a file nothing reads. (This is the former
  `auth` mode.)
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
of recording a duplicate of the previous account. That comparison, the capture, and
`--restore` all run against **re-resolved** specs, because the login flow itself can
move the credential to the tool's other store (codex under
`cli_auth_credentials_store = "auto"`): compared against the store the tool
abandoned, a successful login looks like no change at all. **agy has no login flow**
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
`kae as`). It recomputes `KAE_PROFILE` from the new account set and re-applies
that profile's companions in lockstep (cleared when the set is ad-hoc), so a
one-tool re-bind never leaves a stale git/token identity bound; see
[ADAPTERS-COMPANION.md](ADAPTERS-COMPANION.md). `kae p` is the alias. `kae unpin` deletes the kae-owned fragment and
also strips a pre-v0.7.2 kagikae marker block from `mise.toml` (so `kae unpin &&
kae pin` migrates cleanly), leaving the user's own `mise.toml` content and any
isolation directories (with their login state) intact.

Both commands sweep a **superseded per-directory keychain credential**: a `-s` ↔
`-i` toggle moves every tool to the other mechanism's store and an isolated
re-bind re-keys the store by account, so the store the directory used before is
unreachable, and its keychain item would otherwise hold a credential nothing
points at — invisible, since it lives under a per-directory service name and the
darwin keychain cannot be enumerated. Only the item goes: the store directory, its
sessions and its settings stay, and a file-backed per-directory credential is left
alone because it lives *inside* that directory. The sweep runs after the new
binding is in place, reports each removal, and never changes the exit code.

`kae unpin --purge` extends that to the directory's *current* stores, which plain
`unpin` deliberately keeps so a re-pin restores the directory. Sessions and
settings survive `--purge` too; only the credentials go, and a re-pin restores them
from the account snapshots.

`kae pin` defaults to **shared** (`-s`); pass `-i` for isolated:

- **`-s` / `--shared`** (default): the fragment points each tool at a
  per-directory shared home (`isolation/<pin-id>/<tool>/shared/`): every
  real-home file except the hard-coded auth artifacts (`.credentials.json`,
  `auth.json`) is symlinked in; the account's credential is written privately
  (same rule as `-i` below).
  Settings, sessions, and memory are shared with the real home while
  authentication is private to the directory. The bound account is recorded in
  the fragment so `kae status` and the profile match survive re-entry. See
  docs/ADAPTERS.md for the per-tool denylist and `shared_denylist_extra`.
- **`-i` / `--isolated`**: the fragment points `CLAUDE_CONFIG_DIR` / `CODEX_HOME`
  at the per-account isolated config dirs
  (`isolation/<pin-id>/<tool>/isolated/<account>/config/`): all state (auth,
  sessions, memory, settings) is private to the account. Items listed in
  `tools.<tool>.isolated_shared_items` are symlinked from the real home; the
  account's credential is written privately. Re-running refreshes the opt-in
  links and the credential.

  The credential always comes from **the account's own snapshot**, so binding an
  account that is not currently active is exact. It is written where the tool
  bound to that directory actually reads it: a private file at `0600`, or — on a
  platform where the tool namespaces a keychain item by the config dir (claude on
  macOS) — that directory's keychain item, with any superseded plaintext copy
  removed. See docs/ADAPTERS.md "Per-directory credential store".

  Binding a profile whose account has no captured credential warns and binds the
  rest; `kae pin <tool> <account>` on an uncaptured account fails (exit `7`). A
  tool whose credential store kae cannot bind per directory (codex with
  `cli_auth_credentials_store = "keyring"`) warns the same way and binds without a
  credential, with its settings and sessions still isolated — kae writes no
  keychain item unless the adapter declares that the item moves with the isolation
  variable. For codex that means the bound directory may have no login until you
  log in inside it (docs/ADAPTERS.md "Per-directory credential store").

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

## kae companion Semantics

`kae companion` binds companion-tool auth (git/gh/cloud CLIs) to a profile so an
agent and the tools it shells out to act under the same account. The profile
must already exist. Bindings live in `[profiles.<name>.companions]` (edited
through the comment-preserving config editor) and are **delivered per-directory
by `kae pin`** — `kae companion` itself only records them; re-run `kae pin` in a
bound directory to refresh its fragment. The global fragment (`kae use -i`)
carries no companions, since it has no single profile.

- **add**: non-secret knobs (git `email`/`name`/`signingkey`, config-dir paths
  like `KUBECONFIG`) take `KEY=VALUE` and are written inline. A token knob
  (`GH_TOKEN`, `CLOUDFLARE_API_TOKEN`) takes a single bare `KEY`; its value is
  read from stdin, stored in the secret backend, and left as an empty marker in
  config. The two forms cannot be mixed, and a token passed as `KEY=VALUE` is
  refused so secrets never reach argv/shell history.
- **rm**: drops the named knobs, or the whole companion when no `KEY` is given;
  a removed token knob's secret is deleted from the backend.
- **list**: shows knob names with non-secret values inline and token knobs as
  `(secret)`; token values are never printed.

`kae doctor` reports companion binding health on the unfiltered report: a bound
token knob with no stored secret (`companion_missing` — the binding would fail
at mise eval) and a bound companion whose CLI is absent from PATH
(`companion_binary` — the binding has no effect). Inside a pinned directory that
binds git it also runs the live commit-misidentity guard: it shells out to
`git config` and compares the identity git would actually commit with against the
profile's bound `user.email`/`name`/`signingkey`, flagging a repo-local override
or an inactive pin (`companion_drift`). The
switched/preserved contract per companion is [ADAPTERS-COMPANION.md](ADAPTERS-COMPANION.md).

## Shell completion

`kae completion <bash|zsh|fish>` prints a **dynamic** completion script: instead
of baking a word list at generation time, the script calls the hidden
`kae __complete <kind>` backend at completion time, so candidates always track
the live router, config, and captured state. Word 1 completes commands; the
argument positions complete tools/profiles, and `kae use claude <TAB>` scopes to
claude's accounts (the tool word is passed to `kae __complete accounts <tool>`).
Subcommand groups complete their sub-verbs and arguments too: `kae account
<TAB>` → `rm`/`rename`/`set-identity`, and `kae companion <TAB>` →
`add`/`rm`/`list`, then a profile, a companion id (`kae __complete companions`),
and that companion's knob names (`kae companion add main git <TAB>` →
`email`/`name`/`signingkey`, via `kae __complete companion-knobs git`).
Positions are computed from the **flag-filtered** argument list, so a flag
before the positionals does not shift completion (`kae add --no-login <TAB>`
still completes tools; `kae use -i claude <TAB>` completes claude's accounts).
When the current word starts with `-`, the command's **flag names** are
completed (`kae add --<TAB>` → `--no-login` / `--restore`; `kae run -<TAB>` →
`-s` / `-i` / `--env` / `-P`).

`kae __complete <commands|tools|companions|companion-knobs <id>|profiles|accounts [<tool>]|flags <command>>`
is read-only, takes no locks, prints one candidate per line, and is
intentionally hidden from `kae help`. The `flags` kind lists a command's flags from the same
per-command registrars the parser uses, so the completion set never drifts. Its
line-oriented output is an internal contract consumed by the generated scripts
and the `kae mise init` task `complete` directives — it is not the JSON contract
(`schema_version` is unaffected).

**bash and zsh are the verified shells.** `kae completion fish` stays available
as a best-effort generator (unit-tested and `fish -n`-valid) but is not a
release-gated, officially-verified surface (dropped 2026-06-18).

**Keeping completion current.** Because the script is dynamic, new candidates (a
profile, account, companion) appear with no action. Only a **structural** change
— a new command/subcommand `case` or a new `__complete` kind — alters the script
body, and that is refreshed automatically:

- The **mise-hook** registration self-sources from the binary on directory
  entry, so it is always current — nothing to do.
- For the **fpath/completions-file** registration, `kae completion --refresh`
  rewrites every already-registered file from the current binary (it never
  creates a new one). The installers run it for you: `mise run install` and
  `scripts/install.sh` refresh after placing the binary, so an upgrade or a local
  rebuild propagates a structural change without a manual re-install.
- `go build` by itself does not (it does not invoke kae); run `kae completion
  --refresh` if you build that way.

For zsh under `compinit -C` (a speed-tuned cache that skips the rescan) the
rewritten file may not load until the compdump is rebuilt; `--refresh` prints the
command (`rm -f "${ZSH_COMPDUMP:-$HOME/.zcompdump}" && autoload -Uz compinit &&
compinit`) when it changed a zsh file. A `subcommandVerbs` parity test fails if a
new subcommand group lacks a completion case, so the script side cannot silently
drift in the first place.

kae's own completion is **binary-scoped**, so it is registered globally, never
per-directory (a per-directory registration would make `kae <TAB>` blink in and
out by directory). Three registration paths, non-mise first:

1. **rc eval** — add `eval "$(kae completion zsh)"` (bash/zsh) or
   `kae completion fish | source` to your shell rc. No files written.
2. **completion file** — write the script to the shell's standard completions
   dir (bash-completion and fish auto-load it). For zsh, `--install` prefers an
   existing user completions dir already on `fpath` (`~/.config/zsh/completions`,
   `~/.zsh/completions`, `~/.zfunc`), so the file auto-loads in a new shell; if
   none exists it falls back to `$XDG_DATA_HOME/zsh/site-functions` and prints
   the `fpath=(…)` line to add. `kae completion <shell> --install` does this for
   you (the default).
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

**zsh: installed but completion does not appear.** zsh caches its completion
index in a *compdump*; a newly added `_kae` is not loaded until that cache is
rebuilt (frameworks that run `compinit -C` skip the rescan). The on-fpath
`--install` note says so; the fix is to remove the compdump and re-run
`compinit`, then open a new shell:

```bash
rm -f "${ZSH_COMPDUMP:-$HOME/.zcompdump}" && autoload -Uz compinit && compinit
```

## Did-you-mean hints

When an unknown command, tool, or profile is close to a real one, the usage
error names the single nearest match — `kae uze` → "did you mean `use`?",
`kae add clade` → "did you mean `claude`?", `kae use mian` → "did you mean
`main`?". The hint is **suggestion-only**: the command still fails with its
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
| `4` | `lock_busy` | another kae process holds the per-tool, config, state, or per-directory pin lock |
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

- Human reports go to stdout; usage and runtime errors go to stderr. So do
  warnings: they must survive a piped stdout and `--quiet`, and they are emitted
  before the write they warn about, not after it.
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
  "pinned": {"profile": "side", "mode": "shared"},
  "active_profile": "main",
  "mode": "auth",
  "global_isolated": [
    {"tool": "claude", "account": "main", "home": "/Users/you/.local/share/kae/isolation/global/claude/main"}
  ],
  "tools": [
    {
      "tool": "claude",
      "enabled": true,
      "account": "main",
      "driver": "claude-keychain-patch",
      "auth_present": true,
      "accounts": ["side", "main"],
      "warnings": []
    }
  ],
  "profiles": [
    {"name": "side", "label": "Side",
     "accounts": {"claude": "side"}, "active": false},
    {"name": "main", "accounts": {"claude": "main"}, "active": true}
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
      "account": "main",
      "identity": "you@example.com",
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
    {"tool": "claude", "account": "main", "identity": "you@example.com",
     "driver": "claude-keychain-patch", "active": true,
     "captured_at": "2026-06-11T01:23:45Z"}
  ],
  "profiles": [
    {"name": "main", "accounts": {"claude": "main"}, "active": true}
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
      "message": "ANTHROPIC_API_KEY is set and overrides the switched login"
    }
  ]
}
```

Check `status` vocabulary: `ok`, `warn`, `error`, `skipped`.
Stable check codes include: `binary_present`, `auth_present`, `driver`,
`env_conflict`, `credential_store`, `secret_backend`, `config_valid`,
`unsupported`, `file_mode`, `credential_stale`, `secret_orphan`,
`companion_missing`, `companion_binary`, `companion_drift`,
`companion_token_drift`, `identity_drift`, `upstream_version`, `pin_stale`.

Credential-health checks (warn-level):
- `credential_stale`: a captured snapshot cannot open a session again without an
  interactive re-login — its access token expired and no **usable** refresh token
  is left (absent, or itself past `refreshTokenExpiresAt`), or the tool emptied
  the credential itself after a failed refresh. Names the tool's own login
  command *and* `kae add --no-login`, in that order: re-capturing first would only
  freeze the dead credential back into the snapshot. Uses the same freshness
  predicate as the switch-time warning, and inspects only the stored snapshot (no
  live read, so no extra keychain prompt). An expired snapshot whose refresh token
  is still usable is not flagged (the tool refreshes it).
- `secret_orphan`: a stored secret item **of the account namespace**
  (`<tool>/<account>/<artifact>`) has no matching snapshot dir — names
  `kae account rm`. Backup, companion, and env-profile keys have no snapshot dir
  by design and are never reported. Detected only where the backend can enumerate (file
  `readdir`, Linux `libsecret`); the darwin keychain cannot list by service, so
  the check is silently skipped there (documented gap; docs/SECURITY.md).

Bound-directory checks (warn-level, unfiltered like the companion ones — a
binding is a property of the directory, not of one tool):
- `pin_stale`: a directory bound with `kae pin` either no longer exists — its
  per-directory store is then orphaned, since the store is named by a hash of the
  path and nothing else records it — or it is still pinned to an account that is
  no longer captured, which is what `kae account rm`/`rename` and
  `kae profile rm` leave behind. Names the directory and the `kae pin` that
  re-binds it. Offline: it reads the breadcrumb each store carries, the fragment
  in the directory it names, and the account snapshots. A directory that was
  simply `kae unpin`-ed is **not** reported: unpin keeps the store on purpose so
  a re-pin restores its sessions and settings.

Upstream-assumption checks (warn-level, per-tool so they honor `kae doctor
<tool>`):
- `identity_drift`: the live value of a tool's identity-only artifact (claude's
  `/oauthAccount`) no longer names the account kae applied for the active account,
  or has disappeared. Offline by construction: stored bytes against live bytes, no
  subprocess and no network. Only the artifact's **identifying** keys are compared
  (`IdentityKeys`: for claude `accountUuid`, `emailAddress`, `organizationUuid`) —
  the rest of that payload is bookkeeping the tool rewrites on its own schedule
  (`profileFetchedAt`, plan fields), and comparing it flagged correct switches as
  drift. Since kae applies the identity together with the credential, a
  divergence in those keys means it was rewritten outside kae — a manual login, or
  upstream changing how it maintains the field. Names `kae use <tool> <account>` to
  re-apply, and points at docs/VALIDATION.md "Upstream Behaviour Assumptions" if
  it drifts again. The identity value itself is never printed (it is PII);
  the message names only the tool, account, and artifact. Skipped when the tool
  has no active account, and inside a kae-owned isolated home (`kae pin`,
  `kae use -i`) — the per-directory materializers never apply an identity there,
  so there is nothing kae wrote to compare against (docs/ROADMAP.md).

  When the active account's snapshot has **no** identity recorded yet (captured
  before kae switched it), the same code reports it at **`ok`** level instead: not
  a problem — a switch clears the stale cache and the tool refetches it, so the
  account still displays correctly — but worth knowing, because kae has no copy to
  apply offline. The message names the one-time fix (start the tool once, then
  `kae add --no-login <tool> <account>`).
- `upstream_version`: the installed tool's `--version` is a newer **major or
  minor** than the version its adapter's behaviour assumptions were verified
  against (`VerifiedVersion()`). A patch bump is silent by design, an older
  installed version is fine, and an unparseable or failing `--version` is skipped
  rather than warned about. It exists because kae's structure guards only catch
  layout changes: a behaviour-only upstream change passes all of them and breaks
  switching silently, so the version is the sole offline signal. **cursor is
  exempt** (it declares no version): its date-based version would read every new
  build month as a minor bump and warn monthly — see docs/ADAPTERS.md "Verified
  Upstream Versions". This is the one doctor check that launches the upstream
  CLIs: one `<binary> --version` per installed tool, run **concurrently** under a
  5s deadline for the whole round. They are assumed offline, but that is a
  property of the third-party binaries rather than of kae (copilot's already
  prints an update hint), so the deadline is what guarantees `kae doctor` cannot
  hang on one; a probe it kills is skipped like any other failing `--version`.

  `upstream_version` also carries the **age** half, which the version comparison
  cannot see: every adapter declares when its assumptions were last verified
  (`VerifiedOn()`), and doctor warns once that is more than six months ago. That
  is the only signal a user who never upgrades the tool ever gets — and the only
  one cursor gets at all, since its date versions make the comparison useless. A
  date kae cannot parse is reported as such rather than skipped, because a typo
  would otherwise read as "nothing to report".

  `kae doctor <tool>` runs only the per-tool checks. The companion and
  pinned-directory checks are not per-tool, so they are skipped — and a note on
  stderr says so, since a filtered run that prints nothing about them otherwise
  reads as "they are fine".

Companion-binding checks (warn-level, unfiltered report only):
- `companion_missing`: a bound token knob has no stored secret, so the mise
  `exec()` lookup would fail at eval — names `kae companion add`.
- `companion_binary`: a bound companion's CLI is absent from PATH, so the
  binding has no effect until it is installed.
- `companion_drift`: the live git commit identity differs from the bound one.
  Only inside a pinned directory binding git, and only when `git` is on PATH; it
  shells out to `git config --get user.<knob>` (offline, non-secret) and compares
  the effective value against the profile's `email`/`name`/`signingkey`. Flags a
  repo-local override (`git config --local`) or an inactive/untrusted pin — both
  of which would commit as the wrong identity. Names the diagnostic
  `git config --show-origin`.
- `companion_token_drift`: the live login a token companion's token resolves to
  differs from the bound `expected_login`, or the token is absent from the env
  (an inactive pin). The token-side analogue of `companion_drift`. **Opt-in**: it
  makes a network call (e.g. `gh api user`), so doctor runs it only when its
  prompt is answered yes or `--yes` is passed; `--json`/non-interactive runs skip
  it. `expected_login` is recorded automatically at `kae companion add` time for
  companions that declare a login probe — currently **gh** only (cloudflare is
  deferred; see ADAPTERS-COMPANION.md).

### `kae use ... --json` (the switch report)

```json
{
  "schema_version": 1,
  "ok": true,
  "dry_run": false,
  "profile": "main",
  "backup_id": "20260611T012345Z",
  "results": [
    {
      "tool": "claude",
      "account": "main",
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

### Bare `kae use --json` (the idempotent apply report)

The switch report plus a `changed` boolean (no `dry_run`):

```json
{
  "schema_version": 1,
  "ok": true,
  "changed": false,
  "profile": "main",
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

`restored` counts the backup's own records. Rollback restores **global** live
state, so it ignores a kae-managed isolation env var the way `use` and `add` do:
recorded artifacts carry absolute targets, but the pre-rollback backup and the
cleanup of an artifact the backup never recorded resolve today's adapter specs,
which inside a pinned shell would otherwise follow `CLAUDE_CONFIG_DIR` into the
isolation tree. Restoring a backup taken before an optional artifact existed also
removes that artifact live (it cannot be restored) — for claude that means the
identity cache is cleared and refetched, never left naming the account the
rollback just left. When the tool has moved the credential between its stores since
the backup (codex between `auth.json` and its keyring item under
`cli_auth_credentials_store = "auto"`), the payload is restored into the store the
tool reads **now** rather than the recorded one — restoring the recorded one would
report success while the live session kept the other account. A move between
payload shapes that are not interchangeable cannot be redirected and is refused
with exit `10`, pointing at `kae use` instead. The same redirect applies to the
restores in `kae run -s` and `kae add --restore`, whose child process is the usual
reason the store moved in the first place. A backup that recorded **no** credential
is never redirected: kae leaves the moved-to store alone rather than delete a
credential it has no copy of, and warns on stderr that the restore was partial.

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
