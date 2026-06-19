# kagikae

`kae` switches **subscription accounts** for AI coding CLIs — Claude Code,
Codex CLI, Antigravity CLI, OpenCode, the Cursor CLI, and the GitHub Copilot
CLI — without touching your working environment.

Switching `~/.claude` or `~/.codex` wholesale also switches your skills, hooks,
memory, MCP servers, project trust, and session history. `kae` doesn't. By
default it patches **only the authentication artifacts** (an explicit allowlist)
and preserves everything else:

```text
main Claude account    <->  side Claude account     (e.g. a second org you own)
main ChatGPT Codex     <->  side ChatGPT Codex
main Google account    <->  side Google account      (Antigravity)
main ChatGPT           <->  side ChatGPT             (OpenCode)
main Cursor            <->  side Cursor              (Cursor CLI)
```

`kae` never reimplements a login flow — it snapshots and restores what the
official CLIs create — and it never sends your credentials anywhere. Secrets
stay in the OS credential store; switching is a local, reversible, audited
operation.

## Why kae?

If you keep more than one login for the same AI coding CLI — say a main account
and a separate one for a side project or an org you own — the tool gives you one
home directory. The obvious fix — copy
`~/.claude` aside and swap it back — also swaps everything that *isn't* the
login: your skills, MCP servers, hooks, project trust, and chat history. Re-doing
a `claude /login` each time is slow and loses the other session's refresh token.

`kae` separates **who you are logged in as** from **how you have the tool set
up**:

- it switches only the credential (an allowlisted token / keychain item / JSON
  pointer), leaving mixed-state files like `~/.claude.json` untouched;
- it backs up live state before every write and restores it on `kae rollback`;
- it keeps one consistent surface across six different tools that each store
  auth differently (file, macOS Keychain, libsecret, JSON pointer);
- it offers per-directory and per-process scopes, so a single machine can run
  different accounts of the same tool at once.

## What stands out

- **Auth-only by default.** A shared switch changes the credential and nothing
  else; claude self-heals its `/oauthAccount` identity cache from the token.
- **Six tools, one grammar.** `use` / `pin` × `-s` (shared) / `-i` (isolated),
  plus `run`, `add`, `ls`, `doctor` — the same verbs regardless of how the tool
  stores its credential.
- **Three isolation scopes.** Global in-place (`kae use`), per-account private
  home (`kae use -i`), and per-directory binding (`kae pin`) via kae-owned mise
  fragments — your real `~/.claude` and your `mise.toml` are never touched.
- **Safe by construction.** Atomic writes, per-tool locks, pre-write backups,
  structure guards that refuse unknown credential layouts, and full secret
  redaction in every output path.
- **Built for humans and agents.** Readable text by default; deterministic exit
  codes and stable `--json` reports for scripting; dynamic shell completion and
  a "did you mean?" hint for typos.

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

Prebuilt archives and `checksums.txt` for macOS and Linux (amd64/arm64) are on
[GitHub Releases](https://github.com/webkaz-labs/kagikae/releases); release
assets carry build-provenance attestations. Windows is not built yet
([docs/ROADMAP.md](docs/ROADMAP.md)).

`kae` needs the official tool CLIs themselves for logging in — it snapshots and
restores what they create.

## Quick Start

Two verbs by scope: **`use`** switches globally, **`pin`** binds the current
directory. Add **`-i`** for an isolated (private) home, or keep the default
**`-s`** (shared with your real home). **`run`** wraps one process.

```bash
kae init                       # create config
kae edit                       # open it in $EDITOR (profiles live here)
kae profile save main          # or manage profiles without hand-editing TOML:
                               # save / set / unset / rm / default
kae doctor                     # check environment and live auth

# register accounts (official login flow + snapshot; or --no-login to snapshot
# the login you are already on). The account name is optional — kae auto-detects
# it from the live login identity:
kae add claude                 # name auto-detected (e.g. your login email)
kae add claude side            # or name it explicitly
kae add --no-login codex main

# switch now (global):
kae use main                   # every tool in the "main" profile (alias: kae u)
kae use claude side            # one tool

kae ls                         # accounts and profiles in one view
kae                            # what is active
kae rollback                   # undo the last switch
```

`kae use` backs up the live state before every write and `kae rollback`
restores it. `--dry-run` previews exactly what would be patched.

## Pin a Directory

```bash
cd ~/code/side-project
kae pin side                   # this directory now uses the side profile
                               # (shared: settings/sessions shared, credential private)
mise trust                     # mise refuses untrusted configs; its error
                               # between pin and trust is expected
```

Inside the pinned directory (with [mise](https://mise.jdx.dev) activated) claude
and codex run as the `side` accounts. `kae pin` writes a kae-owned mise
fragment (`.config/mise/conf.d/kagikae.toml`, git-ignored); your `mise.toml` is
never touched. Variants:

```bash
kae pin -i side                # isolated: nothing shared with the real home
                               # (opt in via isolated_shared_items)
kae pin claude main            # re-bind one tool in this dir (sessions/settings kept)
kae unpin                      # remove the binding (deletes the kae-owned fragment)
kae use -i main                # global isolated: point every mise-activated
                               # terminal at a per-account private home;
                               # `kae use -s main` tears it down
```

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

# idempotent apply for your own hooks/scripts (no-op when already active):
kae use --quiet
```

## Shell Completion

`kae completion <bash|zsh|fish>` prints a **dynamic** completion script: it calls
a hidden `kae __complete` backend at completion time, so it always offers live
profiles, accounts, tools, and a command's flags. Completion is flag-aware
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
`[hooks.enter]` (opt-in), or prints the script. For **zsh** it prefers an
existing directory already on your `fpath` (`~/.config/zsh/completions`,
`~/.zsh/completions`, `~/.zfunc`) so the file auto-loads in a new shell.

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

## Tool Support

`kae` switches the credential each tool actually uses, and preserves the rest.
The per-tool switched/preserved allowlist is the normative contract in
[docs/ADAPTERS.md](docs/ADAPTERS.md).

| Tool | Switches | Login identity for `kae add` |
|------|----------|------------------------------|
| Claude Code (`claude`) | `/claudeAiOauth` (macOS Keychain item / Linux `.credentials.json`) | `~/.claude.json` `oauthAccount.emailAddress` |
| Codex CLI (`codex`) | `~/.codex/auth.json`, or the `Codex Auth` keychain item (`cli_auth_credentials_store = "keyring"`) | `id_token` email / `account_id` |
| Antigravity CLI (`agy`) | macOS `gemini`/`antigravity` Keychain item (verbatim token); Linux file driver | active Google account in `~/.gemini/google_accounts.json` |
| OpenCode (`opencode`) | the `/openai` entry of `auth.json` (other providers preserved) | access-token email, else `accountId` |
| Cursor CLI (`cursor-agent`) | the access-token Keychain item (macOS) | `cursor-agent status` email |
| GitHub Copilot (`copilot`) | `/lastLoggedInUser` in `~/.copilot/config.json` (all platforms) | `lastLoggedInUser.login` |

One account per tool at a time globally: a shared switch (`kae use`) changes the
live credential store, so running different accounts of the same tool at once
needs an isolated environment — `kae pin` per directory, or `kae use -i`
globally.

## Common Commands

| Command | Purpose |
|---------|---------|
| `kae` / `kae status` (`kae s`) | Show what is active per tool. |
| `kae use <profile\|tool account>` (`kae u`) | Switch globally (`-i` isolated, `--quiet` for hooks). |
| `kae pin [<profile>]` (`kae p`) | Bind the current directory (`-i` isolated). |
| `kae unpin` | Remove the directory binding. |
| `kae run <tool> <account> [-- <cmd>]` (`kae r`) | Run one process under an account (`-s`/`-i`/`--env`). |
| `kae add [<tool>] [<account>]` | Register an account (login flow, or `--no-login`). |
| `kae ls` | List accounts and profiles in one view. |
| `kae account rm\|rename` | Delete or rename a captured account. |
| `kae profile save\|set\|unset\|rm\|default` | Manage profiles without editing TOML. |
| `kae env set\|...` | Manage API-key env profiles for `run --env`. |
| `kae rollback` | Undo the last switch from its backup. |
| `kae doctor` (`kae d`) | Check environment, live auth, and credential health. |
| `kae completion <shell>` | Print or `--install` shell completion. |
| `kae mise init` | Generate mise tasks / completion for a project. |
| `kae version` (`kae -v`) | Print the CLI version. |

Every command takes `--json` for a stable, versioned report and a deterministic
exit code — see [docs/CLI.md](docs/CLI.md).

## Safety Model

- **Auth-only by default.** Only the credential is switched (claude's token,
  codex's `auth.json` or `Codex Auth` keyring item, agy's opaque token, …);
  mixed-state files like `~/.claude.json` are never touched in a shared switch.
- **Secrets in the OS store.** macOS Keychain / Linux libsecret; a plaintext
  file backend is explicit opt-in. Secret values never reach
  stdout/JSON/logs/metadata.
- **Reversible and guarded.** Atomic writes, per-tool locks, pre-write backups,
  and structure guards that refuse unknown credential layouts (exit `10`).
- **Credential freshness.** `kae use` recaptures the account it switches away
  from when its live token changed (so a switch back applies a live token),
  warns on an expired snapshot with no refresh token, and `kae doctor` flags
  stale snapshots and orphaned secret items.

See [docs/SECURITY.md](docs/SECURITY.md).

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
| macOS | Supported (credentials via Keychain). |
| Linux | Supported (libsecret, or the file backend). |
| Windows | Planned ([docs/ROADMAP.md](docs/ROADMAP.md)); not built yet. |

## Development

```bash
mise run check        # go vet, gofmt, go test ./..., go mod verify
git diff --check
```

`mise run check` is the authoritative pre-commit gate; CI
([.github/workflows/ci.yml](.github/workflows/ci.yml)) mirrors it, and tagging
`vX.Y.Z` runs [GoReleaser](https://goreleaser.com) to publish the binaries.

## Documentation

| Document | Purpose |
|----------|---------|
| [docs/DESIGN.md](docs/DESIGN.md) | Mission, modes, terminology, boundaries. |
| [docs/ADAPTERS.md](docs/ADAPTERS.md) | Per-tool switched/preserved contract. |
| [docs/CLI.md](docs/CLI.md) | Commands, flags, exit codes, JSON contracts, completion. |
| [docs/DATA-MODEL.md](docs/DATA-MODEL.md) | Config, snapshots, state, backups, secrets. |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Package layout and boundaries. |
| [docs/SECURITY.md](docs/SECURITY.md) | Safety rules and secret handling. |
| [docs/ROADMAP.md](docs/ROADMAP.md) | Later phases. |
| [docs/RELEASE.md](docs/RELEASE.md) | Active release target and release process. |
| [docs/VALIDATION.md](docs/VALIDATION.md) | Pre-commit and release checks. |

## License

[MIT](LICENSE)
