# kagikae

`kae` switches **subscription accounts** for AI coding CLIs — Claude Code,
Codex CLI, Gemini CLI (and Antigravity CLI, planned) — without touching your
working environment.

Switching `~/.claude`, `~/.codex`, or `~/.gemini` wholesale also switches your
skills, hooks, memory, MCP servers, project trust, and session history. `kae`
doesn't. By default it patches **only the authentication artifacts** (an
explicit allowlist) and preserves everything else:

```text
work Claude Max  <->  personal Claude Pro
company ChatGPT Team Codex  <->  personal ChatGPT Plus Codex
work Google account  <->  personal Google account (Gemini)
```

## Quick Start

One verb per scope: **`use`** switches now (global), **`pin`** binds a
directory, **`run`** wraps one process.

```bash
kae init                       # create config
kae edit                       # open it in $EDITOR (profiles live here)
kae doctor                     # check environment and live auth

# register accounts (official login flow + snapshot; or --no-login to
# snapshot the login you are already on):
kae add claude work
kae add claude personal
kae add --no-login codex work

# switch now (global):
kae use work                   # every tool in the "work" profile (alias: kae u)
kae use claude personal        # one tool

kae                            # what is active
kae rollback                   # undo the last switch
```

`kae use` backs up the live state before every write and `kae rollback`
restores it. `--dry-run` previews exactly what would be patched.

## Pin a Directory

```bash
cd ~/code/client-a
kae pin clientA                # this directory now uses the clientA profile
mise trust                     # mise refuses untrusted configs; its error
                               # between pin and trust is expected
```

Inside the pinned directory (with [mise](https://mise.jdx.dev) activated)
claude and codex run as the `clientA` accounts; everywhere else keeps the
global login. The default **overlay** mode keeps settings, skills, and
memory shared with your real home while auth and session state stay private
— log in once inside the directory per account, and it persists. Variants:

```bash
kae pin clientA --mode home    # fully separate tool homes (nothing shared)
kae pin work --mode auth --auto  # global auto-switch on entry (opt-in;
                               # needs mise activate + trusted config +
                               # `mise settings experimental=true`)
kae unpin                      # remove the binding (.mise.toml block only)
kae mise init --profile clientA  # preview what pin writes
```

## Beyond Switching

```bash
# run one command as another account, then restore the previous login
# (refreshed OAuth tokens are captured back into the account snapshot):
kae run codex work -- codex exec "go test ./..."

# API-key profiles, injected into the child process only:
kae env set claude ci ANTHROPIC_API_KEY    # value read from stdin
kae run --mode env claude ci -- claude -p "review this"

# one-off isolated homes (the per-process form of pin's modes):
kae run --mode home claude clientA -- claude

# idempotent apply for your own hooks/scripts (no-op when already active):
kae sync --quiet
```

## Safety Model

- Auth-only by default: mixed-state files like `~/.claude.json` are patched
  via a JSON Pointer allowlist (`/oauthAccount`), never replaced.
- Secrets live in the OS credential store (macOS Keychain / Linux libsecret);
  a plaintext file backend is explicit opt-in.
- Atomic writes, per-tool locks, pre-write backups, structure guards that
  refuse unknown layouts, and full secret redaction in all output.
- Deterministic exit codes and stable `--json` reports for agents and
  scripts — see [docs/CLI.md](docs/CLI.md).

One account per tool at a time: `auth` mode switches the live credential
store, so concurrent different accounts of the same tool need the planned
`home` mode (see [docs/ROADMAP.md](docs/ROADMAP.md)).

## Install

```bash
go install github.com/webkaz-labs/kagikae@latest
```

The binary installs as `kagikae`; symlink or alias it to `kae` if you prefer
the short name (release archives ship a `kae` binary).

Requires the official CLIs themselves for logging in — `kae` snapshots and
restores what they create; it never reimplements a login flow.

## Configuration

```text
${XDG_CONFIG_HOME:-~/.config}/kagikae/config.toml
```

Profiles bundle per-tool accounts:

```toml
[profiles.work.accounts]
claude = "work"
codex = "work"
gemini = "work"
```

Full schema: [docs/DATA-MODEL.md](docs/DATA-MODEL.md).

## Platform Support

| Platform | Status |
|----------|--------|
| macOS | supported (Claude credentials via Keychain) |
| Linux | supported |
| Windows | planned ([docs/ROADMAP.md](docs/ROADMAP.md)) |

## Development

```bash
mise run check       # go test ./..., go vet ./..., go mod verify
```

## Documentation

| Document | Purpose |
|----------|---------|
| [docs/DESIGN.md](docs/DESIGN.md) | Mission, modes, terminology, boundaries. |
| [docs/ADAPTERS.md](docs/ADAPTERS.md) | Per-tool switched/preserved contract. |
| [docs/CLI.md](docs/CLI.md) | Commands, flags, exit codes, JSON contracts. |
| [docs/DATA-MODEL.md](docs/DATA-MODEL.md) | Config, snapshots, state, backups, secrets. |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Package layout and boundaries. |
| [docs/SECURITY.md](docs/SECURITY.md) | Safety rules and secret handling. |
| [docs/ROADMAP.md](docs/ROADMAP.md) | Later phases. |
| [docs/RELEASE.md](docs/RELEASE.md) | Active release target. |
| [docs/VALIDATION.md](docs/VALIDATION.md) | Pre-commit and release checks. |

## License

MIT
