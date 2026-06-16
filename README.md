# kagikae

`kae` switches **subscription accounts** for AI coding CLIs — Claude Code,
Codex CLI, Antigravity CLI, OpenCode, the Cursor CLI, and the GitHub
Copilot CLI — without touching your working environment.

Switching `~/.claude` or `~/.codex` wholesale also switches your skills,
hooks, memory, MCP servers, project trust, and session history. `kae` doesn't.
By default it patches **only the authentication artifacts** (an explicit
allowlist) and preserves everything else:

```text
work Claude Max  <->  personal Claude Pro
company ChatGPT Team Codex  <->  personal ChatGPT Plus Codex
work Google account  <->  personal Google account (Antigravity)
work ChatGPT  <->  personal ChatGPT (OpenCode)
work Cursor  <->  personal Cursor (Cursor CLI)
```

## Quick Start

Two verbs by scope: **`use`** switches globally, **`pin`** binds the current
directory. Add **`-i`** for an isolated (private) home, or keep the default
**`-s`** (shared with your real home). **`run`** wraps one process.

```bash
kae init                       # create config
kae edit                       # open it in $EDITOR (profiles live here)
kae profile save work          # or manage profiles without hand-editing TOML:
                               # save / set / unset / rm / default
kae doctor                     # check environment and live auth

# register accounts (official login flow + snapshot; or --no-login to
# snapshot the login you are already on). The account name is optional —
# kae auto-detects it from the live login identity:
kae add claude                 # name auto-detected (e.g. your login email)
kae add claude personal        # or name it explicitly
kae add --no-login codex work

# switch now (global):
kae use work                   # every tool in the "work" profile (alias: kae u)
kae use claude personal        # one tool

kae ls                         # accounts and profiles in one view
kae                            # what is active
kae rollback                   # undo the last switch

# shell completion:
kae completion bash             # or zsh / fish — pipe to your shell's completion dir

# clean up captured accounts (snapshot dir + secret items, profile
# references updated in the same step):
kae account rename claude work work-old
kae account rm claude work-old   # --force to remove the active one
```

`kae use` backs up the live state before every write and `kae rollback`
restores it. `--dry-run` previews exactly what would be patched.

## Pin a Directory

```bash
cd ~/code/client-a
kae pin clientA                # this directory now uses the clientA profile
                               # (shared: settings/sessions shared, credential private)
mise trust                     # mise refuses untrusted configs; its error
                               # between pin and trust is expected
```

Inside the pinned directory (with [mise](https://mise.jdx.dev) activated)
claude and codex run as the `clientA` accounts. `kae pin` writes a kae-owned
mise fragment (`.config/mise/conf.d/kagikae.toml`, git-ignored); your
`mise.toml` is never touched. Variants:

```bash
kae pin -i clientA             # isolated: nothing shared with the real home
                               # (opt in via isolated_shared_items)
kae pin claude clientB         # re-bind one tool in this dir to another account
                               # (sessions/settings unchanged)
kae unpin                      # remove the binding (deletes the kae-owned fragment)
kae mise init --profile clientA  # low-level: render auth tasks + opt-in hook
                               # (bind dirs with kae pin -s|-i; --write to apply)
```

And the global isolated switch, visible to every mise-activated terminal:

```bash
kae use -i work                # point every terminal at a per-account private home
                               # via a kae-owned global mise fragment (~/.claude
                               # untouched); `kae use -s work` tears it down
```

## Beyond Switching

```bash
# run one command as another account, then restore the previous login
# (refreshed OAuth tokens are captured back into the account snapshot):
kae run codex work -- codex exec "go test ./..."

# API-key profiles, injected into the child process only:
kae env set claude ci ANTHROPIC_API_KEY    # value read from stdin
kae run --env claude ci -- claude -p "review this"

# one-off isolated home (per-account private home, shared with kae use -i):
kae run -i claude clientA -- claude

# idempotent apply for your own hooks/scripts (no-op when already active):
kae use --quiet
```

## Safety Model

- Auth-only by default: only the credential is switched (claude's token,
  codex's `auth.json`); mixed-state files like `~/.claude.json` are never
  touched in a shared switch (claude self-heals `/oauthAccount` from the token).
- Secrets live in the OS credential store (macOS Keychain / Linux libsecret);
  a plaintext file backend is explicit opt-in.
- Atomic writes, per-tool locks, pre-write backups, structure guards that
  refuse unknown layouts, and full secret redaction in all output.
- Credential freshness: `kae use` recaptures the account it switches away from
  when its live token has changed (so a switch back applies a live token), warns
  when a target snapshot is expired with no refresh token, and `kae doctor`
  flags stale snapshots and orphaned secret items.
- Deterministic exit codes and stable `--json` reports for agents and
  scripts — see [docs/CLI.md](docs/CLI.md).

One account per tool at a time globally: a shared switch (`kae use`) changes
the live credential store, so concurrent different accounts of the same tool
need an isolated environment — `kae pin` per directory, or `kae use -i`
globally.

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
