# Roadmap

Long-term ordering beyond the active release ([RELEASE.md](RELEASE.md)).
Implementation history lives in git log.

## Phase 2 — login / run / env mode / mise (target v0.2.x)

- `kae login <tool> <account>`: backup current auth, launch/guide the official
  login flow, capture the result, offer to restore the previous account.
- `kae run <tool|all> <account|profile> -- <cmd>`: auth-mode run as a
  transaction — lock, backup, apply, execute child, **recapture refreshed
  credentials into the account**, restore previous state, unlock.
- `env` mode: inject `ANTHROPIC_API_KEY` / `CLAUDE_CODE_OAUTH_TOKEN` /
  `ANTHROPIC_AUTH_TOKEN` / `GEMINI_API_KEY` / Vertex variables into child
  processes from secret-backend-stored env profiles.
- `kae mise init [--print|--write]`: generate `KAE_PROFILE` env and
  `ai-use` / `claude` / `codex` / `gemini` tasks into `.mise.toml` as a
  non-destructive patch preview.
- `kae env --json` / `--dotenv` and `kae shell init`.
- Codex `codex-keyring` driver (lift the v0.1.0 detect-only restriction once
  the keyring item contract is pinned down and guarded).
- `--quiet` / `--verbose` where useful.

## Phase 3 — home mode (target v0.3.x)

Full isolation via per-account tool homes under
`$XDG_DATA_HOME/kagikae/homes/<tool>/<account>`:

- Claude: `CLAUDE_CONFIG_DIR`
- Codex: `CODEX_HOME`
- Gemini / Antigravity: official isolation env if stable, otherwise HOME
  overlay wrapper (decide at implementation time)

Enables concurrent different accounts, CI, and per-client separation.

## Phase 4 — overlay mode (experimental)

Separate auth/session/cache while sharing settings, skills, hooks, memory,
and MCP via symlinked partial homes. Windows symlink/ACL constraints keep
this experimental until proven.

## Phase 5 — Antigravity full adapter

- `agy` auth snapshot/switch drivers (paths to be detected against released
  versions)
- `gemini -> agy` migration helper

## Unscheduled

- Windows support (paths defined in design; needs Credential Manager backend
  and `%APPDATA%` layout)
- performance polish: combine/cache the multiple `security` subprocess calls
  per macOS switch, run per-tool `Detect` concurrently in `status`
- localized human output (Japanese)
- richer TTY (routed review surface) if daily use shows the need
- shell completion
