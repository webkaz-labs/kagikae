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
export HOME=$(mktemp -d) XDG_CONFIG_HOME=$HOME/.config XDG_DATA_HOME=$HOME/.local/share \
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
/tmp/kae capture claude work
/tmp/kae switch claude work --dry-run
/tmp/kae switch claude work --json
/tmp/kae backup list --json
/tmp/kae rollback

# v0.2.0 surfaces:
/tmp/kae run claude work -- /usr/bin/true        # auth transaction + restore
echo sk-test | /tmp/kae env set claude ci ANTHROPIC_API_KEY
/tmp/kae env list --json
/tmp/kae run --mode env claude ci -- /usr/bin/env  # var visible to child only
/tmp/kae run --mode home claude a -- /usr/bin/true
/tmp/kae mise init --profile work                  # preview, no write
```

Use `secret_backend = "file"` in the temp config for smoke checks so no real
keychain entries are created.

## Real-Machine Acceptance (release only)

Manual, on macOS, with real logged-in accounts and a fresh backup of
`~/.claude.json`:

1. `kae capture claude <current-account>`
2. log in to the other account with the official CLI, `kae capture` it
3. `kae switch claude <first>` / back, verifying upstream CLI identity each
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
