# kagikae

English | [日本語](README.ja.md)

`kae` switches saved accounts for AI coding CLIs while keeping the working
setup shared by default. Use a profile to switch several tools together, or bind
a project directory to an account where the tool supports isolation.

Supported adapters cover Claude Code, Codex CLI, Antigravity, OpenCode, Cursor CLI
and GitHub Copilot CLI. Available modes and platforms differ; see
[Tool Support](#tool-support) before choosing a workflow.

## Why kae?

Keep skills, hooks, memory, MCP configuration and sessions while changing the
allowlisted authentication artifacts. Explicit isolated modes provide private
homes when you need separate working environments.

A saved credential can be reused only while the upstream service accepts it.
Capture is not a promise of permanent login: expiry, revocation and refresh can
require a new login. Backups recover saved bytes, not upstream token validity.

## What stands out

- Global account switching with `kae use` and multi-tool profiles.
- Directory bindings and isolated processes for supported tools.
- Opt-in git, gh and cloud companion configuration for a bound directory.
- Backups, original-store preservation, diagnostic reports and dynamic completion.
- A Go executable with no background daemon. Official CLIs and the selected
  secret backend are still required for the workflows that use them.

## Install

Quick shell install (latest release, checksum-verified):

```bash
curl -fsSL https://raw.githubusercontent.com/webkaz-labs/kagikae/main/scripts/install.sh | sh
```

This installs the latest GitHub release to `~/.local/bin/kae` and verifies the
release checksum before copying. To pin a release or choose another directory:

```bash
curl -fsSL https://raw.githubusercontent.com/webkaz-labs/kagikae/main/scripts/install.sh |
  sh -s -- --version vX.Y.Z --install-dir ~/.local/bin
```

Managed with [mise](https://mise.jdx.dev):

```bash
mise use -g github:webkaz-labs/kagikae@vX.Y.Z   # the binary is `kae`
kae version
```

This downloads the release archive for your platform (the executable inside is
`kae`). Pin a tag rather than `latest`.

From source with Go (builds the binary as `kagikae`; alias it to `kae`):

```bash
go install github.com/webkaz-labs/kagikae@latest
```

To update an existing install, repeat the corresponding source-specific command
with the desired release version (the shell installer accepts `--version`; mise
accepts a new `@vX.Y.Z` tag). After installing, check which binary the shell will
run:

```bash
command -v kae
kae version
```

If the installer placed `kae` in `~/.local/bin` and that directory is not on
`PATH`, add it to the shell configuration before running the check. The shell
installer refreshes already-registered completion files; after a mise-managed
update or a local build, run `kae completion --refresh` if registered files need
refreshing.

Prebuilt archives and `checksums.txt` for macOS and Linux (amd64/arm64) are on
[GitHub Releases](https://github.com/webkaz-labs/kagikae/releases); release
assets carry build-provenance attestations. Windows is not built yet
([docs/ROADMAP.md](docs/ROADMAP.md)).

`kae` needs the official tool CLIs themselves for logging in — it snapshots and
restores what they create.

There is no `kae uninstall` command. Before removing the binary, remove or
disable shell and mise hooks that invoke `kae`; otherwise a new shell or
directory entry can report `kae` as missing. Removing the binary then removes
the executable only: bindings, shell completion files, config, snapshots,
backups, and preserved credentials remain in place. `kae unpin` removes one
directory binding, not the installation. The completion registration guidance
below covers the shell-owned files and hooks; do not treat binary removal as a
request to delete all user data.

## Quick Start

Two verbs by scope: **`use`** switches globally, **`pin`** binds the current
directory. Add **`-i`** for an isolated (private) home, or keep the default
**`-s`** (shared with your real home). **`run`** wraps one process.

```bash
kae init                       # create config
kae doctor                     # check environment and live auth

# register accounts (official login flow + snapshot; or --no-login to snapshot
# the login you are already on). The account name is optional — kae auto-detects
# it from the live login identity:
kae add claude main            # register the first account explicitly
kae add claude side            # register a second account explicitly

# build profiles after accounts are registered:
kae profile set main claude main
kae profile set side claude side
kae profile default main       # default used by automatic hooks

# switch now (global):
kae use main                   # every tool in the "main" profile (alias: kae u)
kae use claude side            # one tool

kae ls                         # accounts and profiles in one view
kae                            # what is active
kae rollback                   # undo the last switch
```

To add another tool, capture it separately (for example, `kae add --no-login
codex main`) and add that tool to the profile with `kae profile set main codex
main`.

`kae use` backs up the live artifacts it is about to change; `kae rollback`
restores a selected restorable global backup. `kae use --dry-run` previews its
planned artifact changes.
`kae rollback --dry-run` reports the backup ID and artifact counts before checking
the backend, current store or superseded credentials; it does not establish that
restoration can succeed. A rollback goes back
even when the credential it restores has since been superseded — claude invalidates
older copies of a login when it refreshes — but it says so first, and names where the
newer copy still is ([docs/CLI.md](docs/CLI.md) § `kae rollback --json`).

If the default rollback has no target, inspect `kae backup list` and use
`kae rollback --to <backup-id>` for an explicit global backup. A preservation
record is a separate original-store recovery path: use `kae preservation list`
then `kae preservation restore <id>`; it never redirects to the global home
([docs/CLI.md](docs/CLI.md) § kae preservation Semantics).

For expired authentication, first confirm which account and store need attention.
Use `kae add --restore <tool> <account>` for a fresh global login with a supported
tool, or `kae relogin <tool>` inside a bound directory for its bound account.
Use `kae add --no-login` only after verifying the existing live login belongs to
the account being captured. Stop other sessions using the credential before login.
Rollback and preservation restore recover saved bytes; they do not renew a login.
For incomplete lists, unavailable login flows and uncertain destinations, follow
[Recovery guidance](docs/CLI.md#recovery-guidance).

## Pin a Directory

```bash
cd ~/code/side-project
kae pin side                   # this directory now uses the side profile
                               # (shared: settings/sessions shared; the credential
                               # and the account name it displays are private)
mise trust                     # mise refuses untrusted configs; its error
                               # between pin and trust is expected
```

Inside the bound directory (with [mise](https://mise.jdx.dev) activated) claude
and codex run as the `side` accounts. `kae pin` writes a kae-owned mise
fragment (`.config/mise/conf.d/kagikae.toml`); your `mise.toml` is never touched.
The fragment is machine-specific, so kae keeps it out of `git status` through the
repository's own exclude file (`$GIT_COMMON_DIR/info/exclude`) rather than through
a tracked `.gitignore` — nothing to commit, and one entry covers the main checkout
and every linked worktree. Variants:

```bash
kae pin -i side                # isolated: nothing shared with the real home
                               # (opt in via isolated_shared_items)
kae pin claude main            # re-bind one tool in this dir (sessions/settings kept)
kae unpin                      # remove the binding (deletes the kae-owned fragment)
kae relogin                    # log this directory's account in again, into its own
                               # store, and capture the result back
kae use -i main                # global isolated: point every mise-activated
                               # terminal at a per-account private home;
                               # `kae use -s main` tears it down
```

### One account per worktree

A binding belongs to a *directory*, and a `git worktree` is one more directory —
so each worktree of a repository can run a different account, which is what you
want when several agents work the same repository at once:

```bash
git worktree add ../main-app-review -b review
cd ../main-app-review && kae pin side   # this worktree only
kae ls --pins                           # every bound directory, from anywhere
```

`kae status` answers for the directory you are standing in; `kae ls --pins` is the
view across all of them (directory, profile, mode, bound account per tool, and a
`*` on the current one). It lists what is bound **now**: a directory you unpinned
keeps its store so a re-pin restores its sessions, but it is not a binding and is
not listed.

Claude bindings for the same account share its credential store, while their
working homes follow the chosen shared/isolated mode. This avoids independent
copies of the rotating credential in each directory; it does not serialize
upstream processes or guarantee that a session stays logged in.
[ADAPTERS.md](docs/ADAPTERS.md) § Per-account credential store owns the mechanism.
Re-run `kae pin` when `kae doctor` reports a legacy per-directory copy.

## Beyond Switching

```bash
# open a session under another account (no -- needed: the child defaults to the
# tool's binary), then restore the previous login when it exits:
kae run claude main            # ⇒ runs `claude` as main
kae run -i claude side         # ⇒ runs `claude` in an isolated home

# run a specific command as another account (refreshed OAuth tokens are captured
# back into the account snapshot):
kae run codex main -- codex exec "go test ./..."

# API-key profiles, injected into the child process only:
kae env set claude ci ANTHROPIC_API_KEY      # value read from stdin
kae run --env claude ci -- claude -p "review this"

# automatic apply preserves each tool's global isolated account:
kae use --auto --quiet
```

For concurrent sessions, use `kae run -i` or bind each directory with `kae pin`.
The default `kae run -s` holds the tool's shared-store lock for the child lifetime,
so another shared switch of that tool exits lock-busy; retry after the child exits.

Automatic hooks need a profile: set `kae profile default main`, or use
`kae use --auto -P main --quiet`. Creating a profile with `profile set` alone
sets no default. `--auto` preserves a manually selected global isolated account;
manual `kae use -s -P main` returns the profile's tools to shared mode.
Regenerate an existing kae-owned hook block with
`kae mise init --auto --write -P main`. For a handwritten hook, replace its
`kae use --quiet` line with `kae use --auto --quiet`. Mise activation and trust
are required; see [docs/CLI.md](docs/CLI.md) § kae pin and mise init Semantics.

## Companion Auth

An AI agent rarely acts alone — it shells out to `git`, `gh`, `wrangler`,
`kubectl`. kae binds those companion tools to the same profile so they act under
the account you pinned, without capturing their credentials:

- **git** — drives `GIT_CONFIG_GLOBAL` to a kae-owned file that `[include]`s your
  `~/.gitconfig` and overrides only `user.email`/`name`/`signingkey`; your
  gitconfig is never modified and the override is scoped to the bound directory.
- **gh / cloudflare** — set `GH_TOKEN` / `CLOUDFLARE_API_TOKEN` from the secret
  store, resolved at mise eval time so the token never lands on disk.
- **kubectl** — points `KUBECONFIG` at a path you supply.

```bash
kae companion add main git email=you@example.com name="Your Name"
kae companion add main gh GH_TOKEN     # value read from stdin (kept in the secret store)
kae pin main                           # the bound directory now commits and gh's as `main`
```

Companion settings are opt-in per profile, delivered through `kae pin`, and
removed from the directory on `kae unpin`. Run unfiltered `kae doctor` for binding
health and git identity drift. A token companion's live identity check may require
a network call and confirmation (`--yes` accepts that check).
See [ADAPTERS-COMPANION.md](docs/ADAPTERS-COMPANION.md) for each companion's scope.

## Shell Completion

`kae completion <bash|zsh|fish>` prints a **dynamic** completion script: it calls
a hidden `kae __complete` backend at completion time, so it always offers live
profiles, accounts, tools, and a command's flags. It also completes subcommand
groups — `kae companion <TAB>` → `add`/`rm`/`list`, then a profile, a companion
id, and that companion's knobs. Completion is flag-aware
(`kae add --no-login <TAB>` still completes tools) and completes flag names
(`kae add --<TAB>` → `--no-login` / `--restore`).

Register it once. Either source it from your shell rc:

```bash
# ~/.zshrc (or ~/.bashrc); fish: kae completion fish | source
eval "$(kae completion zsh)"
```

…or install a completion file:

```bash
kae completion zsh --install
```

`--install` is interactive: it writes a completion file to your shell's standard
dir (the default), registers a global [mise](https://mise.jdx.dev)
`[hooks.enter]` in `conf.d/kagikae.toml` (opt-in), or prints the script. For **zsh** it prefers an
existing directory already on your `fpath` (`~/.config/zsh/completions`,
`~/.zsh/completions`, `~/.zfunc`) so the file auto-loads in a new shell.

After this one-time registration, structural changes in a later `kae` version (a
new subcommand or `__complete` kind) propagate without re-installing: `mise run
install` and `scripts/install.sh` run `kae completion --refresh`, which rewrites
already-registered files from the new binary (it never creates one, so the
initial `--install` above is still required). A plain `go build` skips this — run
`kae completion --refresh` yourself in that case. The mise-hook registration
loads the script in the active shell, so it is always current; refresh also
moves exact kae-owned current/legacy blocks from global config into that fragment.
The fragment also holds global isolated settings; returning to shared mode keeps
the completion hook. Customized blocks need manual migration; see
[docs/CLI.md](docs/CLI.md) § Global mise integration ownership.

> **zsh: completion installed but not showing?** zsh caches its completion
> index in a *compdump*; a newly added function will not load until that cache
> is rebuilt. Remove it and re-run `compinit`, then open a new shell:
>
> ```bash
> rm -f "${ZSH_COMPDUMP:-$HOME/.zcompdump}" && autoload -Uz compinit && compinit
> ```

`kae mise init` separately generates project-scoped completion for
`mise run <task> <TAB>` in the directory's `.mise.toml` — distinct from this
binary-scoped shell completion.

## Troubleshooting and reporting

Start with the unfiltered health report so bound directories and their bindings
are included:

```bash
kae doctor --json
kae status --json
kae version
```

`kae backup list --json` and `kae preservation list --json` also work with
an invalid config: they report a warning and list metadata from the resolved
state directory. A partial list retains readable rows, reports `complete: false`
and classified `issues`, and exits nonzero. Problem-entry names are hashed;
ordinary metadata still needs redaction before sharing.

Keep the exit code and relevant stderr with the report. Before sharing anything,
redact identity or email values, account and preservation IDs, absolute paths
(including preservation directories), and other private metadata; do not attach
raw JSON or credential output. For a reproducible failure, include the command,
platform, install source, version, and whether the scope was global or pinned.
Report issues at [GitHub Issues](https://github.com/webkaz-labs/kagikae/issues).

## Tool Support

`kae` switches the credential each tool actually uses, and preserves the rest.
The per-tool switched/preserved allowlist is the normative contract in
[docs/ADAPTERS.md](docs/ADAPTERS.md).

Two tiers, a deliberate scope decision rather than a to-do list. **Which tool is
in which tier — with the rationale and the promotion criteria — is normative in
[docs/PRODUCT.md](docs/PRODUCT.md) § Tool Tiers**; this file deliberately does not
repeat the mapping:

- **Tier 1** targets the full surface, subject to capability guards: global switching, global isolated homes, both
  per-directory binds, identity switching and drift detection.
- **Tier 2** gets global switching (`kae use`), `kae run --env`, backup/rollback,
  `kae doctor`, and identity detection where the tool exposes one. No `kae pin` and
  no `-i`: those redirect the tool's home, which needs an isolation variable
  verified end to end for that tool.

A tier never relaxes a safety rule. At both tiers kae refuses to write to a store
it has not measured, never falls back to a secondary store when the authoritative
write fails, and warns before the write rather than after.

| Tool | Switches | Login identity for `kae add` |
|------|----------|------------------------------|
| Claude Code (`claude`) | `/claudeAiOauth` (macOS Keychain item / Linux `.credentials.json`) and `/oauthAccount` in `~/.claude.json` (identity only, pointer patch) | `~/.claude.json` `oauthAccount.emailAddress` |
| Codex CLI (`codex`) | `CODEX_HOME/auth.json`, or this codex home's `Codex Auth` keychain item (`cli_auth_credentials_store = "keyring"`, or `"auto"` once the item exists) | `id_token` email / `account_id` |
| Antigravity CLI (`agy`) | macOS `gemini`/`antigravity` Keychain item (verbatim token); Linux file driver | active Google account in `~/.gemini/google_accounts.json` |
| OpenCode (`opencode`) | the `/openai` entry of `auth.json` (other providers preserved) | access-token email, else `accountId` |
| Cursor CLI (`cursor-agent`) | the access-token, refresh-token and api-key Keychain items (macOS), which cursor-agent writes as one unit | `cursor-agent status` email |
| GitHub Copilot (`copilot`) | `/lastLoggedInUser` in `$COPILOT_HOME/config.json`, default `~/.copilot/config.json` (all platforms) | `lastLoggedInUser.login` |

One account per tool at a time globally: a shared switch (`kae use`) changes the
live credential store, so running different accounts of the same tool at once
needs an isolated environment — `kae pin` per directory, or `kae use -i`
globally. Two directories bound to the *same* account is a different case that
must follow the tool's credential-store model — see [One account per worktree](#one-account-per-worktree).
Codex per-directory keyring binding remains disabled pending its live capability
check. Cursor's adapter remains unsupported on Linux; Linux support for the
binary does not imply support for every adapter.

## Common Commands

| Command | Purpose |
|---------|---------|
| `kae` / `kae status` (`kae s`) | Show what is active per tool. |
| `kae use <profile\|tool account>` (`kae u`) | Switch globally (`-i` isolated; automatic hooks use `--auto --quiet`). |
| `kae pin [<profile>]` (`kae p`) | Bind the current directory (`-i` isolated). |
| `kae unpin [--purge]` | Remove the directory binding. `--purge` also deletes this directory's per-directory keychain credentials, harvesting each into its account snapshot first and keeping any it could not (sessions and settings are kept). One copy it deletes without keeping: one whose account no longer exists, because there is no snapshot to keep it in — it says so, and [docs/CLI.md](docs/CLI.md) § kae pin says why. |
| `kae relogin [<tool>]` | Run the tool's login flow into *this directory's* bound store — kae exports the isolation variable itself, so it lands there whether or not the pin is active in this shell — then capture the new login back into the account snapshot. Before starting, it preserves the current credential for original-store recovery, then attempts the existing account harvest. If preservation fails, login does not start. |
| `kae run <tool> <account> [-- <cmd>]` (`kae r`) | Run one process under an account (`-s`/`-i`/`--env`). |
| `kae add [<tool>] [<account>]` | Register an account (login flow, or `--no-login`). |
| `kae ls` | List accounts and profiles in one view, with each snapshot's credential freshness. |
| `kae ls --pins` | List every directory bound with `kae pin` — one row per bound directory or worktree. |
| `kae account rm\|rename` | Delete or rename a captured account. Both refuse while the account is selected by global isolation and print the safe teardown-and-retry sequence; `--force` on removal permits only the active-account case. |
| `kae profile save\|set\|unset\|rm\|default` | Manage profiles without editing TOML. |
| `kae env set\|...` | Manage API-key env profiles for `run --env`. |
| `kae preservation list / restore / rm` | List preserved credentials, restore an explicit record to its original store, or remove a record with confirmation. Same-origin history retains the latest three distinct copies; see [CLI.md](docs/CLI.md) § kae preservation Semantics. |
| `kae rollback` | Undo the last switch from its backup. |
| `kae doctor` (`kae d`) | Check environment, live auth, and credential health. |
| `kae completion <shell>` | Print or `--install` shell completion. |
| `kae mise init` | Generate mise tasks / completion for a project. |
| `kae version` (`kae -v`) | Print the CLI version. |

Commands that return reports support `--json`; completion scripts, interactive
flows and passthrough child output have their own contracts. See
[docs/CLI.md](docs/CLI.md) for report schemas and exit codes. The version report
labels v0.x as `pre_stable`; deterministic output is not a promise of a frozen v1 API.

## Safety Model

- Shared switching patches only the declared authentication artifacts. Mixed-state
  files use allowlisted JSON pointers rather than whole-file replacement.
- Secrets use macOS Keychain or Linux libsecret, or an explicitly selected file
  backend. Normal reports redact credential bytes; account names, identities and
  paths still need review before sharing.
- Global switches take backups; original-store preservation is a separate recovery
  mechanism. Both have admission and restoration conditions.
- Locks coordinate kae operations, not an upstream process's own refresh.
  Concurrent sessions should use the documented isolation workflow.
- Freshness reports describe what kae can observe. Unknown does not mean invalid,
  and a saved or restored credential is not guaranteed to remain usable.

Read [Recovery guidance](docs/CLI.md#recovery-guidance) before recapturing or
restoring uncertain credentials, and [SECURITY.md](docs/SECURITY.md) for the
mutation, subprocess and concurrency boundaries.

## Configuration

Most workflows need only profiles. The config lives at:

```text
${XDG_CONFIG_HOME:-~/.config}/kagikae/config.toml
```

Profiles bundle per-tool accounts:

```toml
default_profile = "main"

[profiles.main.accounts]
claude = "main"
codex = "main"

[profiles.side.accounts]
claude = "side"
```

Manage them with `kae profile save|set|unset|rm|default` or `kae edit`. Full
schema: [docs/DATA-MODEL.md](docs/DATA-MODEL.md).

## Platform Support

| Platform | Status |
|----------|--------|
| macOS | Release binaries available; adapter-specific capability guards apply. |
| Linux | Release binaries available; libsecret or file backend, with adapter-specific limitations. |
| Windows | Planned ([docs/ROADMAP.md](docs/ROADMAP.md)); not built yet. |

## Development

```bash
mise run check        # see mise.toml [tasks.check] for what it depends on
git diff --check
```

Choose the pre-commit gate using [AGENTS.md](AGENTS.md) § Validation.
`mise run check` remains the full authoritative gate. CI
([.github/workflows/ci.yml](.github/workflows/ci.yml), which calls `check.yml`) runs a
**subset** of it. Static analysers, `shellcheck` and smoke selftests remain in the
local gate. Compare `check.yml`'s steps with `mise.toml`'s `[tasks.check]` for the
current coverage.
Tagging `vX.Y.Z`
runs [GoReleaser](https://goreleaser.com) to publish the binaries, behind that same
subset.

## Documentation

日本語: [README](README.ja.md) · [製品概要](docs/PRODUCT.ja.md) ·
[利用ガイド](docs/GUIDE.ja.md)。These are localized user-facing guides; the English
contract documents below own detailed behavior and verification procedures.


| Document | Purpose |
|----------|---------|
| [docs/PRODUCT.md](docs/PRODUCT.md) | Mission, modes, boundaries. |
| [docs/CONTEXT.md](docs/CONTEXT.md) | The vocabulary — what each term names. Naming only; it states no rule. Not for JSON contract tokens: those are `internal/constants`', documented in docs/DATA-MODEL.md. |
| [docs/ADAPTERS.md](docs/ADAPTERS.md) | Per-tool switched/preserved contract. |
| [docs/ADAPTERS-COMPANION.md](docs/ADAPTERS-COMPANION.md) | Companion-auth (git/gh/cloud CLI) switched/preserved contract. |
| [docs/CREDENTIAL-RULES.md](docs/CREDENTIAL-RULES.md) | Rules for writing, harvesting, attributing, ordering and deleting a credential copy. |
| [docs/CLI.md](docs/CLI.md) | Commands, flags, exit codes, JSON contracts, completion. |
| [docs/DATA-MODEL.md](docs/DATA-MODEL.md) | Config, snapshots, state, backups, secrets, and the JSON status vocabulary. |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Package layout and boundaries. |
| [docs/SECURITY.md](docs/SECURITY.md) | Safety rules and secret handling. |
| [docs/SCOPE-MODEL.md](docs/SCOPE-MODEL.md) | Why the scope/isolation model is shaped the way it is — rationale only, never the rules themselves. |
| [docs/ROADMAP.md](docs/ROADMAP.md) | Later phases, and the near-term work order. |
| [docs/RELEASE.md](docs/RELEASE.md) | The release process, and where a shipped release's record is. |
| [docs/VALIDATION.md](docs/VALIDATION.md) | Pre-commit checks, the smoke blocks that run from them, and two release-only smokes. |
| [docs/ACCEPTANCE.md](docs/ACCEPTANCE.md) | Real-machine release checks, optional account-combination checks, where their results are recorded, and the observation to record when claude asks for a login it did not ask for before. |

## License

[MIT](LICENSE)
