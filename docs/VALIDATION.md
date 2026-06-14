# Validation

## Standard Suite (before every commit)

```bash
mise run check     # go test ./..., go vet ./..., go mod verify
git diff --check
```

Run `go mod tidy` before committing dependency changes.

## Smoke Checks (built binary, isolated env)

All smoke checks run against a temp HOME. On Linux this isolates every
credential path. **On macOS it does not isolate the keychain-backed tools
(claude, cursor)**: those adapters always select a keychain driver and the
`security` CLI ignores `$HOME`, so their capture/switch/login against a temp
HOME still read — and switch **writes** — the real login keychain item. Run
the claude fixture block below on Linux only (e.g. in a container); on macOS
stick to the read-only commands and file-based tools, **or set
`KAE_CLAUDE_DRIVER=file`** to force claude onto the file-patch driver so the
whole capture/switch round-trip closes on `.credentials.json` and never reads
or writes the real login keychain (see [ADAPTERS.md](ADAPTERS.md) "File-driver
override"). cursor is darwin-only, so it cannot be live-switched safely in a
smoke run at all (Linux reports it unsupported, macOS would touch the real
keychain) — verify cursor on the real machine only.

To exercise claude switching on macOS without touching any keychain, set **two**
things: `KAE_CLAUDE_DRIVER=file` (claude's live credential → file driver) **and**
`[security] secret_backend = "file"` (kae's own snapshot store → file backend,
not the `kagikae` keychain). The driver override alone still leaves `kae add`
writing the captured payload to the `kagikae` keychain item, which prompts a
macOS authorization dialog.

```bash
export KAE_CLAUDE_DRIVER=file
mkdir -p "$XDG_CONFIG_HOME/kagikae"
printf 'version = 1\n[security]\nsecret_backend = "file"\nbackup_keep = 30\n' \
  > "$XDG_CONFIG_HOME/kagikae/config.toml"
# seed $CLAUDE_CONFIG_DIR/.credentials.json (or ~/.claude/.credentials.json):
printf '{"claudeAiOauth":{"accessToken":"tok-A"}}' > "$HOME/.claude/.credentials.json"
/tmp/kae add --no-login claude work          # "driver: claude-file-patch"
/tmp/kae use claude work --dry-run --json    # json-pointer action, no keychain
```

```bash
go build -o /tmp/kae .
# Two separate export lines: in `export A=new B=$A`, $A expands to A's OLD
# value, so a single line would point every XDG_* path at the real HOME.
export HOME=$(mktemp -d)
export XDG_CONFIG_HOME=$HOME/.config XDG_DATA_HOME=$HOME/.local/share \
       XDG_STATE_HOME=$HOME/.local/state NO_COLOR=1

/tmp/kae init
/tmp/kae doctor --json
/tmp/kae status --json
/tmp/kae version --format json
```

With fixture credentials (see `internal/cmd` tests for the fixture shapes;
Linux only — see the macOS keychain warning above):

```bash
# seed ~/.claude/.credentials.json + ~/.claude.json fixtures, then:
/tmp/kae add --no-login claude work
/tmp/kae use claude work --dry-run
/tmp/kae use claude work --json
/tmp/kae backup list --json
/tmp/kae rollback

# v0.2.0 surfaces:
/tmp/kae run claude work -- /usr/bin/true        # auth transaction + restore
echo sk-test | /tmp/kae env set claude ci ANTHROPIC_API_KEY
/tmp/kae env list --json
/tmp/kae run --mode env claude ci -- /usr/bin/env  # var visible to child only
/tmp/kae run --mode home claude a -- /usr/bin/true
/tmp/kae mise init --profile work                  # preview, no write

# v0.4.0 surfaces (on macOS use codex-only profiles for live switching —
# see the keychain warning above; codex auth.json is file-based):
/tmp/kae use work --json
/tmp/kae apply --profile work --json               # re-run: "changed": false
KAE_PROFILE=personal /tmp/kae apply --json         # env resolution
/tmp/kae apply --quiet                             # prints nothing on success
/tmp/kae mise init --profile work --auto           # preview: [hooks.enter]
/tmp/kae mise init --profile work --mode home      # preview: [env] tool homes

# v0.5.0 surfaces (pin/overlay never mutate live state, so claude is safe
# to include in the pinned profile even on macOS):
/tmp/kae add --no-login codex work --json          # old capture shape
/tmp/kae use codex work --json                     # tool+account form
/tmp/kae pin clientA                               # writes .mise.toml (overlay)
#   assert: overlay env entries; shared-item symlinks under
#   $XDG_DATA_HOME/kagikae/overlays/<tool>/<account>; re-running pin links
#   items added to the real home afterwards
/tmp/kae unpin                                     # removes only the block
/tmp/kae switch x y; echo $?                       # 64 + replacement pointer
EDITOR=true /tmp/kae edit                          # validate round-trip
/tmp/kae status --json                             # has "pinned" + "profiles"

# v0.7.0 surfaces (bond mode):
# codex: auth.json is file-based — safe on macOS.
# claude: on macOS CLAUDE_CONFIG_DIR suppresses keychain, so kae reads the
#   keychain credential bytes and writes them as .credentials.json into the
#   bond dir. Real-machine gate required (temp-HOME smoke cannot cover this).
/tmp/kae bond clientA                              # writes .mise.toml (bond mode)
#   assert: CODEX_HOME entry in .mise.toml pointing to isolation/<pin-id>/codex/bond/
#   assert: config.toml symlinked from real ~/.codex; auth.json private-copied
#   assert: re-running kae bond is idempotent (no error, symlinks refreshed)

# v0.7.0 surfaces (pin mode and kae as):
/tmp/kae pin clientA                               # writes .mise.toml (pin mode)
#   assert: CODEX_HOME entry pointing to isolation/<pin-id>/codex/pin/work/config/
#   assert: no symlinks by default (full isolation); credential private-copied
#   assert: re-running kae pin is idempotent (links refreshed, no error)
#   assert: legacy overlay-mode block triggers migration warning on stderr
# kae as (requires bonded or pinned directory with a second captured account):
# (cd into the directory that has .mise.toml written by kae bond/pin)
/tmp/kae as codex personal
#   bond mode assert: auth.json in bond dir updated to personal credential
#   pin mode assert: CODEX_HOME entry in .mise.toml points to new account dir
/tmp/kae switch x y; echo $?                       # 64 (renamed in v0.7.0, re-test)

# v0.6.0 surfaces (opencode auth.json is file-based — safe on macOS; seed
# $XDG_DATA_HOME/opencode/auth.json with {"openai":{...},"other":{...}}):
/tmp/kae add --no-login opencode work --json
/tmp/kae use opencode work --json
#   assert: the "other" sibling key in auth.json is untouched
/tmp/kae doctor --json                             # opencode checks present

# copilot is config.json-pointer based (kae never touches the keychain
# tokens), so it is safe on macOS; seed ~/.copilot/config.json with the JSONC
# shape (leading // comments + lastLoggedInUser/loggedInUsers/trustedFolders):
/tmp/kae add --no-login copilot webkaz --json
/tmp/kae use copilot webkaz --json
#   assert: leading // comments and trustedFolders survive the patch
/tmp/kae doctor --json                             # copilot checks present
```

Enter-hook firing (`mise init --auto --write`) needs a live mise:
`mise settings experimental=true` (hooks are experimental; the global config
this writes must itself be `mise trust`-ed), `mise trust` on the project
`.mise.toml`, and a shell with `mise activate`. In a temp-HOME smoke, point
`ZDOTDIR` at a temp dir whose `.zshrc` exports PATH and evals
`mise activate zsh`, then run `zsh -i -c 'cd <project> && true'` from a
neutral directory (the repo's own untrusted mise.toml otherwise aborts
hook-env) and assert the switch happened and that re-entry adds no backup.

Use `secret_backend = "file"` in the temp config for smoke checks so no real
keychain entries are created.

## Real-Machine Acceptance (release only)

Manual, on macOS, with real logged-in accounts and a fresh backup of
`~/.claude.json`:

1. `kae add --no-login claude <current-account>`
2. log in to the other account with the official CLI, `kae add --no-login` it
3. `kae use claude <first>` / back, verifying upstream CLI identity each
   time and `git`-diffing `~/.claude.json` for non-allowlist drift
4. `kae rollback` and verify identity returns

**Verifying identity means launching a fresh tool process and confirming it is
actually authenticated** — e.g. `claude -p "say hi" </dev/null` returns a
reply, not "Not logged in". Hash-comparing the stored credential or relying on
a still-running session is **not** sufficient: the payload can be byte-correct
yet unreadable by the tool (a re-serialized keychain payload, or one written by
a process outside the item's ACL, makes Claude Code report "not logged in"
despite an intact token). A past acceptance pass that skipped the fresh-process
check missed exactly this class of bug.

For copilot (active-account pointer, all platforms — kae never touches the
per-account keychain tokens, only `~/.copilot/config.json` `/lastLoggedInUser`,
so it is safe on macOS):

1. `kae add --no-login copilot <current-account>`
2. `kae use copilot <account>`, then `git`-diff `~/.copilot/config.json`: only
   the `/lastLoggedInUser` value changes (re-compacted to one line is expected
   and harmless); the leading `//` comments, `trustedFolders`, and
   `loggedInUsers` must survive.
3. `kae rollback` and confirm the leading `//` comments still survive — this
   exercises the JSONC restore path (a backup whose JSONC flag was dropped
   patches through the plain-JSON path and fails on the comments).

copilot has no `whoami`/`status` subcommand, so the fresh-process auth check is
a non-interactive prompt: `copilot -p "say AUTH-OK" --no-color --allow-all-tools`
returns a reply when authenticated, an error/login prompt when not. The CLI
emits ANSI/spinner control codes, so strip them
(`sed 's/\033\[[0-9;]*[a-zA-Z]//g'`) before asserting on the output. Switching
between two accounts is a v0.7.0 acceptance item; with a single account this
verifies the verbatim round-trip and comment preservation only.

Never run real-machine acceptance with uncommitted work in progress in the
live tool sessions.

## Secret Leak Regression

`go test ./... -run Redact` includes tests asserting that captured fixture
secret values never appear in text output, JSON output, error messages, or
metadata files written by capture/switch/rollback.

## Release Acceptance Log

### v0.7.0 (2026-06-14, macOS darwin 24.6.0)

All acceptance criteria passed:

- **bond gate**: `kae bond clientA` wrote `.mise.toml` with CLAUDE_CONFIG_DIR →
  `isolation/<pin-id>/claude/bond`; dir contained `.credentials.json` at `0600`
  and symlinks for all other real-home items; `claude -p "say AUTH-OK"` returned
  AUTH-OK; `~/.claude.json` MD5 unchanged before and after.
- **Phase 3**: `kae use claude work --dry-run` showed exactly 1 action (keychain
  `/claudeAiOauth`); no `/oauthAccount` in output.
- **Phase 4**: `kae pin clientA` wrote pin-mode block
  (`isolation/<pin-id>/claude/pin/work/config`); legacy overlay-mode block
  triggered migration warning on stderr; `kae run --mode pin … -- /usr/bin/true`
  succeeded.
- **Phase 5 (bond)**: `kae as claude work` inside bonded dir printed "Switched …
  bond dir; sessions/settings unchanged".
- **Phase 5 (pin)**: `kae as claude clientB` inside pinned dir prepared
  `…/pin/clientB/config` and updated `.mise.toml` CLAUDE_CONFIG_DIR to the new
  account path.
