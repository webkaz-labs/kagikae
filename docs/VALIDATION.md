# Validation

## Standard Suite (before every commit)

```bash
mise run check     # go test ./..., go vet ./..., go mod verify
git diff --check
```

Run `go mod tidy` before committing dependency changes.

## Smoke Checks (built binary, isolated env)

All smoke checks run against a temp HOME. On Linux this isolates every
credential path. **On macOS it does not isolate claude**: the claude adapter
always selects the keychain driver and the `security` CLI ignores `$HOME`,
so claude capture/switch/login against a temp HOME still read — and switch
**writes** — the real login keychain item. Run the claude fixture block
below on Linux only (e.g. in a container); on macOS stick to the read-only
commands and non-claude tools.

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
/tmp/kae sync --profile work --json                # re-run: "changed": false
KAE_PROFILE=personal /tmp/kae sync --json          # env resolution
/tmp/kae sync --quiet                              # prints nothing on success
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

# v0.6.0 surfaces (opencode auth.json is file-based — safe on macOS; seed
# $XDG_DATA_HOME/opencode/auth.json with {"openai":{...},"other":{...}}):
/tmp/kae add --no-login opencode work --json
/tmp/kae use opencode work --json
#   assert: the "other" sibling key in auth.json is untouched
/tmp/kae doctor --json                             # opencode checks present
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

Never run real-machine acceptance with uncommitted work in progress in the
live tool sessions.

## Secret Leak Regression

`go test ./... -run Redact` includes tests asserting that captured fixture
secret values never appear in text output, JSON output, error messages, or
metadata files written by capture/switch/rollback.
