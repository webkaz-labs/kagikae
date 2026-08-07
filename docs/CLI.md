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
kae relogin [<tool>]                 # run the tool's login flow into this directory's
                                     # bound store, then capture it back into the snapshot
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
kae ls --pins [--json]               # every directory bound with kae pin
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
kae rollback [--to <backup-id>]      # restore the most recent restorable (or given) backup
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
| `--to <backup-id>` | `rollback` | backup to restore (default: the most recent **restorable** one — the newest that records a state kae was about to change. A `run-unattributable` backup is skipped by the default because it records a state kae *declined to adopt*, not one it changed; `--to` still reaches it, which is what the refusal that created it tells you to type) |

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
  account (someone ran the tool's own login outside kae), a live identity that
  **differs** from the recorded one where kae cannot read either as an account record
  (so it cannot tell whose login is live — worded weaker than the previous case,
  because kae has observed a change and not an account), a live credential that
  needs a re-login while the snapshot still holds a usable one, a live credential the
  snapshot **provably supersedes** (for a tool whose refresh token rotates single-use the
  older copy cannot refresh at all), or one kae cannot **order** against the snapshot
  because it carries no deadline kae can use.
  `keepSnapshotIdentity` and `recaptureWouldDowngrade` are normative for the set — read
  them rather than this list, which was wrong for a release. The freshness guard is
  one-directional: kae never prefers the older value. What it refuses is wider than "a
  dead credential over a working one" — a usable but *older* copy is refused too, and so
  is one kae cannot judge, which is reported as exactly that rather than as dead.

  **What a refusal costs, and where the copy goes.** Declining to recapture means the
  live copy is not preserved in the snapshot, and the switch then overwrites the live
  store — so the refusal names the backup this switch already took, which holds that
  copy, and the two-step that turns it into an account of its own. Only readable
  identities that *agree* let the recapture proceed; two payloads kae cannot read that
  are byte-identical are treated as agreement, deliberately, because a login always
  rewrites the identity (see § kae run Semantics for what happens when it does not).

  If the account being switched **to** needs a re-login (expired with no usable
  refresh token, or emptied by the tool), kae warns and still proceeds; a snapshot
  whose refresh token is still usable proceeds silently — the tool self-refreshes.
  Warnings go to **stderr, before anything is applied**, so they survive a pipe and
  `--quiet` (which suppresses the success report, never a warning), and a switch of
  several tools closes with one roll-up line naming those that need a re-login. The
  exit code stays `0` — the switch itself succeeded. The same warnings also ride in
  each result's `warnings` array for `--json`. Only `kae use` / bare `use`
  recapture *the real store*; `use -i` / `pin` / `run -i` write kae-owned isolation
  dirs and never touch it — but they do write the account snapshot when they harvest
  a newer credential out of a directory's own store (§ kae pin).
- `--isolated` / `-i`: point every terminal at a per-account private home
  **without touching `~/.claude`**. kae prepares
  `isolation/global/<tool>/<account>/` (docs/DATA-MODEL.md) and writes a
  kae-owned global mise fragment `~/.config/mise/conf.d/kagikae.toml` exporting
  `CLAUDE_CONFIG_DIR` / `CODEX_HOME`, regenerated from `state.json synced`. For a
  tool whose credential can move on its own (claude) the fragment exports that
  variable too, pointed at the **account's** credential store — the same one every
  bound directory of that account reads, so a globally isolated home is not a second
  copy of a credential that only refreshes once ([ADAPTERS.md](ADAPTERS.md)
  § Per-account credential store). `kae run -i` adds the same pair to its child.
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
  That recapture applies **the same two guards a shared switch applies to its own**
  (above), and no third: a child that logged in as another account, or one that changed
  the identity cache to something kae cannot read as a record, leaves the snapshot alone
  with a warning rather than filing a foreign credential and identity under the target's
  name; and a child whose refresh failed leaves the tombstone live rather than over a
  snapshot that still works. It also keeps the account's **recorded login identity**,
  which is a separate field from the identity payload and was blanked on every `run -s`
  before v0.17.0.
  A refusal here would otherwise **destroy** what it declines, which is the one thing
  this path does not inherit from the switch: its backup was taken before the child, so
  the child's copy lives only in the store the restore is about to overwrite. So when a
  recapture is refused for unattributability, kae takes a second backup — reason
  `run-unattributable` — of the post-child state and names it in the warning, with the
  `kae rollback --to <id>` then `kae add --no-login` pair that turns it into an account.
  A tombstone or a **provably** older copy gets no such backup: there is nothing there to
  keep. A copy kae cannot *order* is a third case and takes the backup — `supersedes`
  lets an undated copy lose to anything, which is right for deciding an overwrite and
  wrong for telling the user the copy is finished, so that refusal says only that kae
  cannot tell which of the two can still refresh.
  Three things about that second backup, stated because nothing else would say them. It
  covers **only** the tools whose recapture was declined, unlike the switch's backup, and
  the warning says so — restoring it does not revert the rest of the run. A bare
  `kae rollback` will not target it (§ kae rollback): it records a state kae *declined*,
  not one it changed, so `--to <id>` is the way in. And `backup_keep` counts undo targets
  only, so a preserved copy sits beside the run's own backup rather than evicting it —
  but it is still pruned once `backup_keep` newer undo targets exist, which makes
  "preserved only in backup `<id>`" true and **time-limited**. That matters most in the
  one case that produces it repeatedly: after an upstream `expiresAt` format change every
  live copy is undated, so every run declines, and the only live copies accumulate in
  these backups while the snapshot keeps its last datable one. Adopt one deliberately
  (`kae rollback --to <id>`, then `kae add --no-login`) rather than leaving them to age
  out.
  The restore is **per tool**: `kae run -s <tool> <the account that was already
  active>` backs up that account's own credential, and claude's refresh token rotates
  single-use, so once the child has refreshed it the copy in the backup can no longer
  refresh — writing it back logs the real home out and reports success. kae leaves
  such a tool's credential as the child left it, says so on stderr, and prints
  `previous auth state restored` only when something was restored. It is not enough
  for the live copy to be *newer*: the identity cache beside it must still name the
  account the backup recorded, so a child that logged in as somebody else is restored
  over rather than kept. And the recorded copy must be one kae can **order** — present,
  parseable, not a tombstone, carrying a deadline — because a copy with no deadline
  compares as older than anything, and skipping on that would leave the account this run
  applied temporarily in the real home for good. So a backup that recorded no credential,
  a dead one, or one whose `expiresAt` no longer parses is always restored as recorded,
  and `run -s` never leaves an account applied permanently. claude only
  ([ROADMAP.md](ROADMAP.md) § Every credential copy).
- `-i`: runs the child with the per-account global isolated home
  (`isolation/global/<tool>/<account>/`) injected via the tool's home-isolation
  env var. This home is **shared with `kae use -i`** for the same account; no lock
  and no mutation of the *live* store, so a concurrent `kae use` in another terminal
  is never blocked and never sees the isolated process. (It can write the account
  snapshot, when materializing that home harvests a newer credential out of it —
  § kae pin.) `run -i` prints the exact
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

`kae ls --pins` swaps that view for every directory bound with `kae pin`, from
anywhere: directory, a `*` for the current one, profile (`(ad-hoc)` when the
account set matches no named profile), mode, and the bound account per tool. It
is the answer `kae status` cannot give — `status` reports the directory it is run
in, which is the wrong question once a repository has one worktree per agent and
each binds a different account. Read-only, no locks, and `--json` publishes
`bound_directories` (`[]` when empty).

**A store is not a binding.** A directory is listed only while it still has a
fragment to read: `kae unpin` deliberately keeps the store so a re-pin restores
the directory's sessions, and a single-tool re-bind leaves the previously bound
tools' stores behind, so listing stores would name directories that are not bound
and re-binds that land where nothing reads. A directory that was deleted or moved
is likewise absent here — its orphaned store is `kae doctor`'s `pin_stale`.

An **unreadable** fragment is a different case from an absent one and is not
silently dropped: the directory is left out of the listing with a stderr warning
naming it and the error, and the exit code stays `0`. It reads no config either,
so a malformed `config.toml` — which makes plain `kae ls` exit `2` — does not stop
`kae ls --pins` answering which account each directory is running.

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

The order is three stages, and it is a contract rather than an implementation
detail, because it is what an interrupted rename leaves behind: **(1)** copy every
payload to the new refs and complete the new snapshot dir, **(2)** move the
logical pointers (`[profiles]` references, then `state.json`), **(3)** delete the
old refs and remove the old snapshot dir. So a rename that dies leaves either the
old account intact and still pointed at, or both accounts present with the pointer
on a complete one — never a pointer at a snapshot that does not exist.

The one state that needs a manual step: a crash between stages 1 and 2 leaves
**both** snapshots present, and re-running the same rename then refuses with exit
`10` (`<new>` already exists). That refusal is deliberately not relaxed — a
half-written rename target is indistinguishable from a name that is genuinely
taken, so tolerating it would mean guessing. Recover by choosing which copy to
keep — `kae ls` shows both:

- **discard the new one** and re-run the rename: `kae account rm <tool> <new>`.
  `<new>` is not active in this window (the flip had not happened), so this needs
  no `--force`, and it removes only the copies stage 1 made.
- **keep the new one**: `kae use <tool> <new>` **first**, then
  `kae account rm <tool> <old>`. The order matters — until the pointer moves,
  `<old>` is still the active account and `kae account rm` refuses it with exit
  `10` unless you pass `--force`.

Both hold the per-tool lock plus the config lock, and edit `config.toml`
through a comment-preserving writer (comments, field order, and unrelated keys
survive). Existing backups are **not** rewritten — a backup's `Meta.ActiveBefore`
keeps the old account name, and `kae rollback` re-checks it rather than trusting it
(see [DATA-MODEL.md](DATA-MODEL.md) § Backups).

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
writing a kae-owned mise fragment `./.config/mise/conf.d/kagikae.toml`; the
user's `mise.toml` is **never** touched.

The fragment is kept out of `git status` by an entry in the repository's shared
exclude file — `$GIT_COMMON_DIR/info/exclude`, resolved by asking git rather than
by assuming a layout — **not** by editing a tracked `./.gitignore`, which is what
kae did up to v0.16.0. Two consequences worth knowing:

- **One entry covers the main checkout and every linked worktree.** A worktree's
  own `$GIT_DIR/info/exclude` is not consulted at all, while the common one is
  honoured everywhere, so binding a repository plus three worktrees no longer
  leaves four working trees dirty waiting for a commit about one machine.
- **Outside a repository (or with no `git` on `PATH`) kae writes no ignore rule
  and says so by omission** — the report names the exclude file it used, and
  simply does not mention ignoring when there was none to record. Nothing is
  watching the fragment there, so this is not an error.
- **Failing to record the rule never fails `kae pin`.** It is the last step, so
  by then the stores are materialized, the credential is written and the fragment
  is in place — the directory *is* bound. An error there would skip the
  superseded-credential sweep below and swallow the export fallback a non-mise
  shell needs, on every re-run, since the cause does not go away. So kae warns on
  stderr (naming the reason, and that the fragment still needs ignoring some other
  way), leaves the exit code at `0`, and omits the `ignored via` clause. This is
  reachable: the exclude file lives **outside** the directory being pinned, so
  binding a linked worktree writes into the *main checkout's* `.git`, which can be
  unwritable while the worktree is fine.

A directory name is interpolated into a *pattern*, not a path, so kae escapes the
wildmatch metacharacters (`\ * ? [ ]`) in it. Without that, pinning a
subdirectory called `[wip]-feature` writes a rule git reads as a character class,
which matches nothing — while `kae pin` reports the fragment as ignored.

`kae unpin` leaves the exclude entry in place, symmetrically with the store it
keeps for a re-pin. A `./.gitignore` line written by an older kae is also left
alone: a duplicate ignore rule is harmless, and removing a line from a tracked
file is a change kae was not asked to make. The profile defaults
to `default_profile`. `kae pin [-s|-i] <tool> <account>` re-binds **one** tool in
the directory, leaving the others and the sharing set intact (the v0.7.1
`kae as`). It recomputes `KAE_PROFILE` from the new account set and re-applies
that profile's companions in lockstep (cleared when the set is ad-hoc), so a
one-tool re-bind never leaves a stale git/token identity bound; see
[ADAPTERS-COMPANION.md](ADAPTERS-COMPANION.md). `kae p` is the alias. `kae unpin` deletes the kae-owned fragment and
also strips a pre-v0.7.2 kagikae marker block from `mise.toml` (so `kae unpin &&
kae pin` migrates cleanly), leaving the user's own `mise.toml` content and any
isolation directories (with their login state) intact.

A per-directory credential kae **cannot read or date** is the one case where the two
commands differ on more than which stores they look at. A bind's sweep keeps it — it may be
a working login in a shape kae has not been taught, and a bind was not asked to destroy
anything — and says that `kae unpin --purge` removes it. `--purge` does, and says what it is
destroying, because keeping it there strands a secret nothing else kae offers can address.
Same asymmetry as an account that no longer exists, for the same reason: it turns on what
was asked for, not on the state.

**Scoped to the cases the sweep reaches at all**, which is where that "nothing else can
address it" holds: a **keychain item** (reachable only from the string kae hashes its
service name from), the credential of a per-account store, and a file a migration just
moved out of its store. A file credential still sitting in a per-directory store that
`unpin` keeps is not swept in either mode — see the two-case file rule below — and it needs
no escape, because a path the user can name is one the user can delete.

Both commands sweep a **superseded per-directory credential**: a re-bind to another
account re-keys the credential store, and a `-s` ↔ `-i` toggle moves every tool to the
other mechanism's config store — which since the per-account credential store leaves that
account's credential where it is, but still moves the directory off a store a **pre-split**
binding left a credential in. Either way the store the directory used before is
unreachable, and its credential would otherwise be one nothing points at. A
keychain item is the case that is easiest to miss, and the common one: it lives
under a per-directory service name that appears nowhere in kae's data dir, and no
kae check reports the item itself — `credential_unsplit` names the *directory* whose
re-bind would remove it, which is the closest thing there is. Only the credential goes — the store directory, its
sessions and its settings stay. **Which store kinds a file credential is taken
from is stated once, below**, with the migration case that made it more than the
item; do not read the keychain wording here as the rule. The sweep runs after the
new binding is in place, reports each removal, and never changes the exit code.

**A deletion here is final, so the sweep harvests too**, by the same rule and with
the same refusals as a bind (above): the item can hold the copy that refreshed
last, and nothing rebuilds a credential that was never in an account snapshot. An
item holding a usable copy kae cannot preserve is therefore **kept, not deleted**,
with the reason on stderr — a leftover secret is a smaller fault than a cleanup that
destroys a login. Known ways to reach that, and **not a closed list**: kae could not
**read** the item, or its payload is not a shape kae recognizes (which may still be a
working login, and is what an upstream format change looks like); kae cannot
**attribute** the store to an account — an isolated store names its account in its own
path, while a shared store's comes from the binding being replaced, so one left over
from an older binding may have no name kae can read; no account of that name exists any
more; the account's snapshot exists but could not be read, so a later run may still
manage it; or the harvest itself declined, for one of the reasons above.

One usable copy *is* deleted, and only by `kae unpin --purge`: one belonging to an
account that no longer exists (`kae account rm`, or a `rename` that moved it). There
is nowhere to preserve it now or ever, and a purge is where the user asked for these
credentials to go, so keeping it would strand a live token no kae command can address.
The sweep a **bind** runs makes the opposite call on the same state and keeps it: that
command was asked to bind something, and `kae account rename` reaches it through kae's
own re-bind remedy — deleting there destroyed the newest copy of the renamed account's
credential. kae names the account in both cases.

One store the binding **does** still point at is swept as well, and only in this
shape: a directory bound before v0.17.0, whose credential has just moved into the
account's own store. Its per-directory copy is then addressed by a name nothing
resolves any more, so leaving it is a full copy of a live account that no reader can
see — and one a shell exporting only `CLAUDE_CONFIG_DIR` would find and refresh,
invalidating the copy every other directory now shares. That is the whole migration:
re-running `kae pin` moves the credential and takes the old copy with it.

`kae unpin --purge` extends that to the directory's *current* stores, which plain
`unpin` deliberately keeps so a re-pin restores the directory. Sessions and
settings survive `--purge` too; only the credentials go, and a re-pin restores them
from the account snapshots. Because the sweep harvests first, `--purge` needs kae's
secret store: if it cannot be opened, kae warns and leaves the credentials in place
rather than deleting logins it has no way to preserve.

A **file** credential is deleted in exactly the two cases where it is not the copy
its own store still reads: the account's credential store, which holds the credential
and nothing else, and a store the migration above just moved the credential out of.
What is kept is a per-directory store's file that is still the one that store reads —
it sits beside the sessions and settings that survive an unpin. The unit is the
artifact, which for claude is a pointer inside `.credentials.json`, so the document
is left behind without it.

An account's **shared** credential store is a separate case with its own rule
([ADAPTERS.md](ADAPTERS.md) § Per-account credential store). It is not one
directory's to delete, so a bind's sweep never touches it, and `--purge` takes it
only once nothing points at it any more: every bound directory's fragment and
`state.synced`, counted after this directory's own binding is gone. A count kae
could not complete keeps the credential and says so, because "no reference found"
and "kae could not look" differ by exactly one logged-out sibling. Known sources —
**not a closed set**: an unreadable fragment, an unreadable state file, and a store
whose breadcrumb cannot be read (one bound by a kae older than that record). The last
does not clear itself, so a single legacy store leaves `--purge` permanently unable
to remove a per-account credential; `kae doctor`'s `pin_stale` is what names it. When
kae keeps a credential because bindings still use it, it prints how many.

`kae pin` defaults to **shared** (`-s`); pass `-i` for isolated:

- **`-s` / `--shared`** (default): the fragment points each tool at a
  per-directory shared home (`isolation/<pin-id>/<tool>/shared/`): every
  real-home file except the hard-coded denylist (`.credentials.json` and
  `.claude.json` for claude, `auth.json` for codex) is symlinked in; the account's
  credential and identity are written privately (same rule as `-i` below). Every
  symlink whose name is not in the intended share set is retracted, so the bind
  converges on that set instead of only growing: that covers an entry which is now
  **denied** (the denylist governs existing bound directories, not only new ones)
  and one the real home **no longer has**, whose link used to survive forever. When
  that real home cannot be enumerated, or lists nothing shareable at all, kae has no
  intent to converge on: it warns on stderr and retracts nothing (docs/ADAPTERS.md
  § Per-directory shared bind for why that way round).
  Settings, sessions, and memory are shared with the real home while who the
  directory is logged in as is private to it. The bound account is recorded in
  the fragment so `kae status` and the profile match survive re-entry. See
  docs/ADAPTERS.md for the per-tool denylist and `shared_denylist_extra`.
- **`-i` / `--isolated`**: the fragment points `CLAUDE_CONFIG_DIR` / `CODEX_HOME`
  at the per-account isolated config dirs
  (`isolation/<pin-id>/<tool>/isolated/<account>/config/`): all state (auth,
  sessions, memory, settings) is private to the account. Items listed in
  `tools.<tool>.isolated_shared_items` are symlinked from the real home; the
  account's credential is written privately. Re-running refreshes the opt-in
  links and the credential, and retracts the link of an item removed from the
  list. The configured list is the statement of intent here, so an item still on
  it keeps its link even when its source is missing.

  The credential comes from **the account's own snapshot**, so binding an account
  that is not currently active is exact. Since v0.17.0 it is written to the
  *account's* credential store rather than the directory's, and the fragment carries
  a second env entry pointing at it, so every directory bound to one account reads
  one credential while keeping its own sessions ([ADAPTERS.md](ADAPTERS.md)
  § Per-account credential store). A directory bound by an earlier release keeps its
  own copy until it is re-pinned; `kae doctor` names it (`credential_unsplit`). It is
  written where the tool bound to that directory actually reads it — a file at
  `0600`, or the keychain item that binding resolves, with any superseded plaintext
  copy removed. What decides which item is the adapter's rule, stated once in
  docs/ADAPTERS.md § "Credential storage resolution".

  **Except where a store already holds a newer copy, which kae harvests into the
  snapshot first.** The tool refreshes the credential inside the directory, in
  place, and claude's refresh token is single-use: the copy that refreshed last is
  then the only one that can refresh again. Overwriting it with an older snapshot
  therefore logs the directory out rather than merely dating it back, and nothing
  offline says so until the tool fails mid-session (docs/VALIDATION.md). So kae reads
  the copy that is there, compares `expiresAt`, copies the newer one into the account
  snapshot — reporting `kae: harvested …` on stderr — and then writes that.

  When it declines a copy it could not **attribute** in the account's own credential
  store, the bind leaves that copy in place instead of writing the snapshot over it, and
  says so — every directory bound to the account reads that one copy, so a bind is not
  entitled to replace it on missing evidence. The binding is still written and the exit
  code is still `0`; what the message adds is that the credential there is not the
  snapshot's, and the remedy (`kae relogin <tool>` inside the directory) is the one that
  makes the two agree. A copy that is provably another account's is still replaced, since
  that account's credential is elsewhere and the bind has to take effect.

  It covers **every store the bound directory has, not only the one being written** —
  which is what a re-bind to another account needs, since that binds the directory to a
  *different* credential store, and what reaches the credential a pre-split binding left
  in a config store a mode toggle moves off — and it **refuses rather than guesses**: an
  unusable copy is never harvested, and neither is one kae cannot attribute to the
  account it would be filed under. [ADAPTERS.md](ADAPTERS.md) § Per-directory
  credential store is normative for the mechanism and the full list of refusals; what
  is visible from the command line is that kae reports `kae: harvested …` when it
  moves a copy, says so and names a login remedy when it declines one it may have
  needed, and says only the fact when the copy demonstrably belongs to another account
  (logging that account in again would be the wrong advice). **One store that was not
  harvested produces exactly one message**, whichever of the two harvests reached it
  first — the store a bind overwrites is looked at twice, and one refusal read as two
  problems. The bind proceeds either way. claude only, because no other tool's rotation has been measured
  ([ROADMAP.md](ROADMAP.md) § Rotation is measured for claude only).

  The account's **identity cache** is written with it, after it, so the tool names
  the account the directory is authenticated as rather than whichever one first ran
  there. For claude that cache lives in the mixed-state `.claude.json`, which is
  therefore **private** to a bound directory rather than shared — a directory cannot
  both name its own account and live-share the file recording which account it is.
  If you set `CLAUDE_CONFIG_DIR` yourself, that file used to be shared into a bond
  dir and is not any more, so `projects`, `mcpServers` and project trust start from
  claude's defaults there (sessions are unaffected; they live in `projects/` and are
  still shared). A write that would land outside the directory's own store is
  declined with a stderr warning instead, leaving the credential in place. So is one
  that fails for any other reason — an identity is a label the tool can rebuild, and
  a bind must not be abandoned half-done over it.

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

## kae relogin Semantics

`kae relogin [<tool>]` runs the tool's own login flow **into the store the current
directory is bound to**, then captures the result back into that account's
snapshot. It is the bound-directory counterpart of `kae add`, and it is the remedy
every bound-credential finding names.

It exists because the string it replaced asked the user to remember two things a
message cannot enforce.

- **The login has to land in the bound store.** `cd <dir> && claude /login` does
  that only while the pin is active in that shell; with mise activation absent or
  the config untrusted the isolation variable is unset and the same command
  refreshes the **real home** — the wrong account moves and the bound one is still
  stale. kae exports the variables itself (`CLAUDE_CONFIG_DIR=<the bound store>`
  **and the binding's credential variable**, appended to the child's environment so
  they win over whatever the shell has), so this hazard cannot happen rather than
  needing a caveat. Both, because the login writes the credential where the
  credential variable points: exporting one half sends the new token somewhere kae
  does not read it back from, and the command then reports a login that changed
  nothing while the directory is still stale. A directory bound before v0.17.0 has
  no credential entry, and kae exports what that binding actually says rather than
  what a current bind would say. mise activation is therefore **not** required, and
  not checked.
- **Something has to capture it back.** A login made inside a bound directory only
  reaches the account snapshot when a bind or a sweep next runs, so until then
  `kae use <tool> <account>` applies the older copy globally. This captures it at
  the moment it is the newest copy.

Contract:

- The account is **never an argument**. Which account a directory holds is the
  binding's answer; a name typed here could only be ignored or file one account's
  login under another's.
- Without `<tool>`, the directory's single candidate is used — a tool it binds and
  kae has a login command for. Several candidates is a usage error (exit `64`)
  naming them: two interactive login flows from one word is not a default, and
  taking the first would log in the tool you did not mean.
- Not pinned → exit `7`, naming `kae add --restore <tool> <account>` for the global
  path. A tool this directory does not bind → exit `7`. An unpinned directory whose
  store tree survives (`kae unpin` keeps it on purpose) is **not** pinned for this
  purpose: what is bound comes from the mise fragment.
- The bound store not being there → exit `7`, naming `kae pin <tool> <account>`.
  kae recomputes the store path from a hash of the directory's *current* path, while
  what the tool reads there is the literal value the fragment exports; a directory
  that moved keeps its fragment and gets a different hash, so logging in would create
  a store nothing reads. `kae pin` always materializes the store, so its absence is
  the signal the two have diverged. This refusal comes before the secret backend is
  opened and before the pin lock is taken.
- The flow leaving the store's credential byte-identical → exit `11`
  (`auth_unchanged`), the same code `kae add` uses. An aborted or failed login must
  not be reported as a login, because the reason to run this is that the directory
  was already stale.
- **The success line claims only what kae observed**, and there are two of them:
  - `Logged <tool> in for <tool>/<account> in this directory` — printed only when
    all three of: kae read the store on both sides and the bytes differ; what is
    there now is something kae read as a login; and the capture back *confirmed*
    the login is that account's.
  - `Ran the <tool> login flow in this directory` — every other case, each with its
    own stderr line saying which:
    - kae could not read the store on one side or both, so it cannot tell whether
      anything changed;
    - the store now holds **nothing usable** — either blank tokens, which is what a
      failed refresh leaves behind and is the state a stale directory reaches before
      the user even sees a prompt, or no credential at all where kae resolves one.
      Those are one state to kae, deliberately: they license the same conclusion, and
      splitting them is how a first version of this caught the blanked payload and
      missed the removed one. Note the second is reachable with **no** failure —
      under the file driver a successful login writes the keychain item and deletes
      the plaintext file kae was reading;
    - or the harvest did not confirm the account, which includes every tool whose
      rotation is unmeasured: nothing harvests there, so nothing checked the identity
      either, and absence of a refusal is not confirmation.

    None of these stderr lines says the login failed or tells you to run it again.
    "No usable token" is what kae **read**, and it is derived from token fields being
    empty *or absent* — so a payload whose keys changed upstream reads the same as a
    tombstone, and an instruction to retry would loop against a login that is fine on
    the day kae's parser is the stale thing (§ `kae rollback --json` is normative for
    this family of wordings).

  Exit stays `0` in all of them: the login flow ran, and only `auth_unchanged` above
  is a refusal.
- The capture back is `harvestDirCredential` with every guard it already has: it
  declines a copy that does not supersede the snapshot, and one it cannot attribute
  to this account. It runs whatever the comparison said — a flow kae could not
  compare may still have left a copy worth harvesting. A login as **another** account
  is left in the store (it is that account's, and it is where it belongs) and
  reported, with `kae pin <tool> <account>` as the remedy — never another login,
  which would mint a fresh chain and invalidate the copy just left in place.
- The capture back is claude-only, like every other harvest
  (docs/ROADMAP.md § Rotation is measured for claude only). For any other bound tool
  the login still runs into the right store; nothing is harvested because nothing
  older is invalidated by it.
- Holds the **pin** lock across the flow, so a re-bind of this directory cannot
  overwrite the store mid-login.

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
`kae env <TAB>` → `set`/`unset`/`list`, then — after `set` or `unset` only — a
tool and that tool's accounts, since `env list` takes no arguments and offering
one would suggest a word the command rejects. `kae backup <TAB>` → `list`.
Positions are computed from the **flag-filtered** argument list, so a **boolean**
flag before the positionals does not shift completion (`kae add --no-login
<TAB>` still completes tools; `kae use -i claude <TAB>` completes claude's
accounts). A flag that takes a *value* still shifts them, which costs candidates
and never an action ([ROADMAP.md](ROADMAP.md) § Command-system expansion owns
why, and what the fix would be).
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
as a best-effort generator — unit-tested, and parsed by `fish --no-execute` on any
machine that has fish, which the release machine may not — but is not a
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
compinit`) when it changed a zsh file. Two test-side guards hold the script from
drifting: a `subcommandVerbs` parity test over each group's sub-verbs, and a
classification of **every** command as taking a positional or not, which requires
each one that does to have a branch that offers candidates in all three shells.
Neither sees a command dropped from both the command list and that classification
([ROADMAP.md](ROADMAP.md) § Command-system expansion) — `kae env` and `kae backup`
went without a case at all until v0.17.0 for exactly that reason.

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
      "credential": "expiring",
      "relogin_by": "2026-06-14T01:23:45Z",
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
path; it is `[]` when no tool is globally isolated. `credential` / `relogin_by`
describe the **active** account's snapshot freshness (see "Credential freshness
in listings" below); both are absent when no account is active. The human text
leads with the same data: the global-isolated homes (if any), the pin banner, the
global active profile, the per-tool table, then the profiles list.

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
      "captured_at": "2026-06-11T01:23:45Z",
      "credential": "ok",
      "relogin_by": "2026-07-11T01:23:45Z"
    }
  ]
}
```

Ordering: tool (claude, codex, agy, opencode, cursor, copilot), then
account name ascending. `identity` (the raw detected login identity) is
additive and `omitempty` — absent for pre-v0.8.3 snapshots and tools with no
readable identity; `schema_version` stays `1`. `credential` / `relogin_by` are
additive and `omitempty` too — see "Credential freshness in listings" below.

#### Credential freshness in listings

`kae ls`, `kae accounts` and `kae status` rows carry two additive, `omitempty`
fields describing the **snapshot** kae would apply — a different question from
`auth_present` / the `Auth` column, which report the live store. A tool can be
logged in right now while the snapshot kae would re-apply is already dead.

- `credential`: `ok`, `expiring`, or `stale`. The last two mirror the
  `credential_expiring` / `credential_stale` doctor checks exactly (same
  predicates, same seven-day lead time), so a row and a check can never disagree
  about the same account.
- `relogin_by`: RFC3339 — the instant the credential stops being able to open a
  session without an interactive login (the later of the access-token and
  refresh-token expiries).

**Both fields absent means kae could not judge the snapshot, never that it is
fine.** Three ways to get there: a payload kae cannot parse (copilot's pointer,
agy's blob), one it parses but that records no deadline it can trust (codex stores
a refresh token without publishing an expiry; an `auth.json` holding only an API
key has no expiry at all), and a secret backend it could not read. That last case
is deliberately not an error: these commands answer from metadata and are what a
user reaches for when something is already wrong, so an unreadable secret store
drops the two fields and the listing still succeeds.

Cost: one secret-store read per captured account **of a tool whose credential kae
can date at all**, which is what `kae doctor` already does. copilot and agy expose
no expiry, so their accounts are skipped before any read rather than read and then
discarded, and the remaining reads run concurrently so the wall clock is one read
rather than the sum. The expiry is read from the payload every time rather than cached
into `account.toml`, because a copy of a fact is a second source of truth — a
recapture path that forgot to refresh it would have `kae ls` reporting a healthy
account that is dead. Snapshot bytes only change when kae rewrites them, so
reading them is exactly as accurate and cannot fall out of step.

The human tables render this as a `Credential` column, spelling out the time left
(`3 day(s) left`) rather than repeating the state word, `re-login now` for a stale
one, and `-` for one kae could not judge.

### `kae ls --json`

```json
{
  "schema_version": 1,
  "accounts": [
    {"tool": "claude", "account": "main", "identity": "you@example.com",
     "driver": "claude-keychain-patch", "active": true,
     "captured_at": "2026-06-11T01:23:45Z",
     "credential": "ok", "relogin_by": "2026-07-11T01:23:45Z"}
  ],
  "profiles": [
    {"name": "main", "accounts": {"claude": "main"}, "active": true}
  ]
}
```

`accounts` reuses the `kae accounts` row shape (same ordering); `profiles`
reuses the `kae status` profile row shape (name ascending). Both are `[]` when
empty.

### `kae ls --pins --json`

```json
{
  "schema_version": 1,
  "bound_directories": [
    {"directory": "/Users/you/code/main-app", "profile": "main",
     "mode": "shared", "accounts": {"claude": "main"}, "current": true},
    {"directory": "/Users/you/code/main-app-wt1", "profile": "side",
     "mode": "isolated", "accounts": {"claude": "side"}, "current": false}
  ]
}
```

`bound_directories` is ordered by `directory` ascending (so sibling worktrees sort
together) and is `[]` when nothing is bound. `profile` is empty for an ad-hoc
account set; `accounts` covers every tool the directory binds, in either mode.

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
`unsupported`, `file_mode`, `credential_stale`, `credential_expiring`,
`credential_superseded`, `secret_orphan`, `secret_missing`,
`companion_missing`, `companion_binary`, `companion_drift`,
`companion_token_drift`, `identity_drift`, `upstream_version`, `pin_stale`,
`active_orphan`, `credential_unsplit`.

A `(tool, code)` pair is **not** unique in one report: a code is emitted per subject,
and several subjects can share a tool. `credential_stale` is reported once per account
snapshot and once per bound **credential** — so one finding for *all* the
directories bound to an account, since they read one store, plus a separate one for
any directory still holding its own pre-v0.17.0 copy — `identity_drift` once for the active
account's live state and once per bound directory whose store disagrees with its
binding, and `credential_superseded` once per bound directory that a newer copy
overtook. Consumers must read the list, not index it by code.

Credential-health checks (warn-level):
- `credential_stale`: a captured snapshot cannot open a session again without an
  interactive re-login — its access token expired and no **usable** refresh token
  is left (absent, or itself past `refreshTokenExpiresAt`), or the tool emptied
  the credential itself after a failed refresh. Names the tool's own login
  command *and* `kae add --no-login`, in that order: re-capturing first would only
  freeze the dead credential back into the snapshot. Uses the same freshness
  predicate as the switch-time warning. An expired snapshot whose refresh token
  is still usable is not flagged (the tool refreshes it).

  The **account-snapshot** half inspects only stored bytes — no live read, so no
  extra keychain prompt. The same code also reports the credential of a **bound
  directory** (see "Bound-directory credentials" below), and that half does read
  live, because a bound directory does not use a snapshot. Both halves are told
  apart by the message: the snapshot one names `snapshot "<account>"`, the
  directory one `bound to <dir>`.
- `credential_expiring`: the lead-time half of the same question — the snapshot
  still opens a session, but the point where it stops doing so is **less than
  seven days away**. It names the remaining days, the deadline, and
  `kae add --restore <tool> <account>`: the one command that runs the tool's login
  flow for *that* account and puts the currently-live login back afterwards, so
  the account needing attention is refreshed without disturbing the one in use.
  Mutually exclusive with `credential_stale` by construction — both read one
  deadline through one classifier, which is why this is a separate code and not a second band of that
  one: a consumer filtering on `credential_stale` to find broken accounts must not
  start matching accounts that are fine for another five days.

  The deadline it anticipates is the **login's absolute expiry** — for claude,
  `refreshTokenExpiresAt`. That is a fixed point set when `/login` runs, not a rolling
  window: `expiresAt` (the access token, ~8h) moves forward on every refresh, while
  this one stays put, which is why Claude Code can warn `Your login expires in N days
  · run /login to renew` ahead of it and why that warning shows up only near the end.
  (Upstream's own threshold is version-dependent and has changed once; docs/VALIDATION.md
  carries the current value.) A snapshot reporting two days left has two days left. For a credential
  with no refresh token at all (cursor's access-token JWT) the access expiry *is* that
  deadline, and it is treated the same way.

  Seven days is a judgement, not a measurement: upstream's own warning is enough for
  the account you are *using* (you see that tool daily), while a kae account that is
  not active is only shown to you when you run kae. It is deliberately not
  longer — against a login lifetime of roughly a month, seven days keeps the check
  silent for most of a credential's life, so it still reads as "act now" rather than as
  wallpaper. Both failure directions have been shipped once and are pinned by tests:
  firing for every account, and firing for none.

  It is also silent where the deadline is **unknowable**, a separate case from the
  no-refresh-token one above: a tool that stores a refresh token but publishes no
  expiry for it (codex, opencode) leaves `refreshTokenExpiresAt` at zero, and zero
  means *unknown*, never "never expires". Guessing the access-token expiry is the
  deadline there would warn every few hours about a perfectly healthy credential.

  The same lead-time notice is emitted at switch time next to the stale one
  (stderr, before the write, surviving `--quiet`), but it is **not** counted in
  the "N tools need a re-login before use" roll-up: that switch works today.
- `credential_unsplit`: a bound directory still keeps its own copy of an account's
  credential, because it was bound before kae gave each account one shared
  credential store ([ADAPTERS.md](ADAPTERS.md) § Per-account credential store). Every
  such copy is invalidated the moment any other binding of that account refreshes,
  and nothing else can see the state — the copy is healthy until it is not, so this
  reports the *shape* of the binding rather than the health of the credential.
  Reported once per bound directory and tool; the remedy is to re-run `kae pin`
  there. Offline and backend-free.
- `credential_superseded`: another copy of one account's credential carries a
  **later** `expiresAt` than the copy in a bound directory. For a tool whose refresh
  token rotates single-use (claude — docs/VALIDATION.md owns the measurement), if the
  two are copies of **one** login then only the newer one can still refresh, so the
  session in that directory cannot be renewed past its access-token expiry. The
  message states it that way, conditionally, and the condition is the honest part:
  `expiresAt` orders two payloads, it does not say whether they are one chain or two
  independent logins of the same account. Copies of one login is the ordinary case —
  a bind writes the snapshot into every store — and this release adds a way to make
  the other one (`kae relogin` in one directory mints a fresh chain there), which is
  measured as open in docs/VALIDATION.md rather than asserted either way.

  Names where the newer copy is (`snapshot <tool>/<account>`, or the store bound to
  another directory), because that is the cause the user cannot otherwise see — "I
  used claude in the other worktree and this one logged out hours later" — and
  because it decides the remedy, the same way `kae rollback`'s warning branches on it:
  - newer copy in the **snapshot** → `cd <dir> && kae pin <tool> <account>`. A re-bind
    materializes it into the store with no browser round-trip. This is the one case
    the "deliberately not `kae pin`" reasoning below does not cover — its objection is
    that the snapshot may be just as expired, and here kae has proved otherwise.
  - newer copy in **another directory's store** → `cd <dir> && kae relogin <tool>`.
    The snapshot is not known to be newer, so a re-bind could write something older
    still; a login is the only answer that certainly produces a usable credential.

  Its own code, and not a band of `credential_stale`, for two reasons. The
  consumer-filtering one that separates `credential_expiring` from it, and a
  stronger one: the two read **different fields**. A superseded copy reports `ok` on
  every freshness surface, because the deadline they judge by
  (`refreshTokenExpiresAt`) is precisely what an invalidation does not move.

  **Reported only where kae can prove it**, which is narrower than it may look, and
  deliberately so — a warning that fires on a healthy binding is worth less than
  none (v0.15.0/v0.15.1 shipped that mistake in both directions).
  - Both copies must be *orderable*: parsed, not a tombstone, and carrying a real
    `expiresAt`. In particular a bound copy kae cannot order is not reported —
    that is kae unable to judge it, not evidence it is dead — even though the
    ordering comparator elsewhere lets such a copy lose to anything (there the
    question is "may I overwrite it", where a copy with no comparable deadline is
    nothing to lose).
  - Both must be *attributed* to the account by the store's own identity cache.
    Ordering never establishes whose login two copies are, and a shared store
    legitimately holds a previous account's credential.
  - Equal deadlines are not overtaken, so a directory pinned moments ago — whose
    store holds exactly what the snapshot does — reports nothing.
  - A tombstoned or unreadable bound copy is left to `credential_stale`, which
    already names it.

  Unfiltered like the other bound-directory checks, and it needs the secret backend
  (for the snapshot it compares against and for attribution), so it is skipped when
  that is unavailable — unlike the `credential_stale` half, which is deliberately
  outside that gate.
- `active_orphan`: `state.json` records an account as active for a tool, but no
  snapshot by that name exists — so kae cannot say which account is live, and
  `kae status` would display a name that is not there. Offline and backend-free.
  Known ways to get here, not a closed set — the check compares the two records
  rather than watching for a cause. A writer outside kae reaches it: a test or smoke
  run that isolated `HOME` but inherited a real `XDG_STATE_HOME` will capture
  straight into the live state file. kae's own commands no longer do — `kae account
  rm` clears the pointer before removing anything, `kae account rename` completes
  the new snapshot before moving the pointer to it, and `kae rollback` restores a
  backup's `active_before` only when its snapshot is still captured (all three
  above) — but a `state.json` written before those fixes can still hold the result,
  which is why the check compares records rather than trusting the writers.
  The same code also fires when `state.json` itself cannot be read, or when the
  active account's snapshot metadata will not parse: nothing else in doctor looks at
  either. Names `kae use <tool> <account>` to settle it. Warn, never error: the
  recorded name is bookkeeping and the live credential may well be fine.
- `secret_orphan`: a stored secret item **of the account namespace**
  (`<tool>/<account>/<artifact>`) has no matching snapshot dir — names
  `kae account rm`. Backup, companion, and env-profile keys have no snapshot dir
  by design and are never reported. Detected only where the backend can enumerate (file
  `readdir`, Linux `libsecret`); the darwin keychain cannot list by service, so
  the check is silently skipped there (documented gap; docs/SECURITY.md).
- `secret_missing`: the mirror of `secret_orphan` — a snapshot declares a stored
  payload (an artifact recorded `present`) that the secret backend does not have,
  so applying that account cannot restore the artifact. Names the snapshot, the
  artifact, and `kae add --no-login` to re-capture. Unlike `secret_orphan` it
  needs no enumeration — it looks up the refs the snapshots themselves name — so
  it works on the darwin keychain, where it is the only one of the two that
  reports anything. An artifact captured as **absent** is never reported: there is
  no payload for it to be missing. A backend that errors on the read is not
  reported here either; `secret_backend` already reports an unusable store, and
  blaming every account for one broken backend would bury it.

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

**Bound-directory credentials** (reported under `credential_stale` /
`credential_expiring`, also unfiltered): a bound directory reads a **live copy** of
the credential — the account's own store, shared with every directory bound to it —
and the tool refreshes *that copy* in place, so it can die while every account
snapshot kae has still looks fine. Sharing does not weaken the reason for the check;
it is why one finding now covers all of those directories. Nothing reported this before — the
first signal was the tool refusing to start in that directory.

- The remedy is a login **inside** that directory: `cd <dir> && kae relogin <tool>`
  (§ kae relogin Semantics). It named the tool's own login command until v0.17.0,
  and that was right only in a shell where the pin was active — the isolation
  variable is what makes a login land in the bound store, so anywhere else the same
  command refreshed the real home instead. `kae relogin` exports the variable
  itself, and captures the new login back. Deliberately **not** `kae pin`: that
  re-copies the account snapshot, which may be just as expired, and would report
  success while changing nothing.
- Reads live, unlike the snapshot half: up to one store read per bound directory
  per tool that has a credential kae materializes — claude and codex only, so the
  fan-out is small. On darwin a claude store read is the same single `security`
  call `Detect` already makes for the global item.
- The location comes from the adapter (`dirCredentialSpec`), asked with an
  environment pointed at that store, and a keychain item is read **only** where the
  adapter declares it bindable — the same gate the write side uses. Without it, a
  tool whose item does not move with its isolation variable (codex under the
  keyring store) would have its *global* login read and reported as the
  directory's: a healthy global login blamed on an unrelated directory, or a stale
  one reported once per bound directory.
- What counts as bound comes from the directory's **mise fragment**, not from the
  store tree. The tree is history: `kae unpin` keeps a store on purpose, and
  re-binding one tool of a profile leaves the previous tools' stores in place — so a
  walk of it returns stores nothing points at any more. A check that says "bound to"
  has to mean it, or its remedy sends the user to a login the tool will not read.
- Silent, therefore, for a directory that is **gone** (`pin_stale` already reports
  its orphaned store; naming it twice would be one problem reported as two), for one
  that was `kae unpin`-ed, for a tool the directory **no longer binds**, and for a
  store whose tool has never been started in it (no credential there yet).
- These checks do not recapture — they report. That is a property of *doctor*, not
  an open question about which copy to take: the reason this bullet used to give
  ("no non-arbitrary rule says which of several directories the snapshot should
  take") was answered in v0.17.0 by ordering on `expiresAt`, and the harvest does
  exactly that wherever a copy is about to be destroyed. Recapturing belongs on a
  write path; doctor is read-only. `kae relogin` is the command that both logs in
  and captures back.
- The identity half of the same binding is reported under **`identity_drift`**, not
  here — a store whose identity names an account other than the one the directory
  binds. It shares the gate above (the fragment decides what is bound) but not the
  backend one: see `identity_drift` below.

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
  it drifts again. The identity value itself is never printed (it is PII), in either
  frame of this check: a message names what is enough to act on — the tool, the
  account, and the artifact or the bound directory the finding is about — and never a
  value from either side of the comparison. Skipped when the tool
  has no active account, and inside a kae-owned isolated home (`kae pin`,
  `kae use -i`) — there the live identity is the **bound directory's** while
  `state.Active` names the **global** account, so the two sides are different
  frames and comparing them would warn on every pinned directory whose binding is
  not also the global selection, which is the normal case. (Not because kae writes
  no identity there: it has since v0.16.0. That frame is a separate check, below.)

  **The bound-directory frame** of the same code, reported unfiltered with the rest
  of the bound-directory checks: a bound directory whose *own store* holds an
  identity naming an account other than the one its fragment binds. Read by store
  path rather than from the current shell, so one run answers for every binding. It
  reports **only what it can prove**: it reads the same attribution predicate the
  per-directory harvest uses, and reports only that predicate's positive-evidence
  answer — both sides readable as account records and their `IdentityKeys`
  disagreeing. Every other answer is missing evidence and stays silent; docs/ADAPTERS.md
  § Per-directory credential store is normative for that taxonomy and is not restated
  here. Restraint is the point rather than a detail: the ordinary states are in that
  list (a store whose tool has not run there yet, a directory bound before v0.16.0),
  so warning on them would fire on healthy directories. The message states both causes, because kae cannot tell them
  apart offline — the token is opaque — and they point opposite ways: something
  logged in there as another account (so that directory is *running* an account its
  binding does not name), or kae could not apply the identity when it bound the
  directory (so only the label is wrong). Remedy `cd <dir> && kae pin <tool>
  <account>` makes the binding true again and replaces what is in the store; keeping
  what is there means binding the directory to that account instead. Unlike the
  other bound-directory checks this one **needs the secret backend**, to read the
  account's recorded identity, so an unavailable backend skips it while the bound
  credential checks still run.

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

A rollback **says when the credential it restores is already dead**, and restores it
anyway. Going back is what the user asked for; what kae adds is that claude's refresh
token rotates single-use, so once anything refreshed that account after the backup was
taken, the recorded copy is no longer the one that can refresh and "Rolled back to"
would otherwise be a success report for a rejected token. kae warns only when it can
*prove* the finding — an unconditional version of this would fire on every rollback —
and the warning names where the newer copy is, because that decides the remedy: in the
account **snapshot** it survives the rollback untouched, so `kae use <tool> <account>`
applies it; in the **live store** the rollback overwrites it, so only the pre-rollback
backup still holds it (`kae rollback --to <that id>`). When both hold a later copy the
live store wins the message, since that is the one being overwritten.

A recorded credential kae cannot *order* at all is reported in different words, and which
words depends on **why** — the same distinction kae draws for a bound directory's store. A
**tombstone** is reported as what kae read and no more — "carries no usable token, while
… holds one" — and **never** "cannot log in". `Revoked` is derived from the token fields
being empty *or absent*, so a login whose token keys were renamed upstream reads
identically to a tombstone while working perfectly, and the stronger wording would tell
that user to undo a rollback that was fine. This paragraph claimed the opposite until
2026-08-06, and quoted a string the binary has not contained since the wording was
changed during the item-5 review: `docs/VALIDATION.md` § Upstream Behaviour Assumptions
and `AGENTS.md` both stated the rule correctly and cited **this** section as their
authority, which is how a normative doc comes to license undoing the fix it documents.
A payload kae cannot **parse** — one whose `expiresAt` no longer decodes,
say — may still be a working login in a shape kae has not been taught, so kae claims only
that it "cannot compare" the two and that it cannot tell which can still refresh. The
remedy is the same in every case, because where the other copy is, is what a user acts on.
A backup that recorded **no** credential is silent: removing the credential is what that
backup says, and nothing is being handed back. Warning only —
stderr, no change to the exit code or the JSON report — and claude only, because it is
the only tool whose rotation has been measured ([ROADMAP.md](ROADMAP.md) § Every
credential copy). The counterpart on the other side of the same fact: a `kae use` that
switches away no longer recaptures a live credential its own snapshot supersedes, so
the copy a rollback leaves live cannot be laundered over the newer one.

The **active-account pointer** is restored only when its snapshot is still
captured. A backup's `active_before` keeps the name it had at capture time, so a
rollback across a `kae account rm`/`rename` would otherwise record an account that
no longer exists — `kae status` naming a phantom and the next `kae use <tool>`
failing with `account <tool>/<name> is not captured yet`. kae drops the entry for
that tool instead (the same "no active account" state `kae account rm` leaves) and
warns on stderr, naming the account it could not restore. Never fatal and never a
non-zero exit: the credentials are already rolled back, and what was lost is a
label. Existing backups are **not** rewritten when an account is removed or renamed
(see [DATA-MODEL.md](DATA-MODEL.md) § Backups) — the guard is at the restore, so a
backup stays the record of what was true when it was taken.

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
