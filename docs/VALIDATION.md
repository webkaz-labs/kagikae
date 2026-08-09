# Validation

## Standard Suite (before every commit)

```bash
mise run check     # lint (gofumpt/goimports/staticcheck/golangci-lint/shellcheck), test, vet, mod-verify, build
git diff --check
```

`mise run check` is the authoritative gate. Slower release-time checks live in
`mise run audit` (govulncheck) and `mise run goreleaser-check`. Lint tools run
via `go run <tool>@<pinned version>`; the first run downloads them.

Run `go mod tidy` before committing dependency changes.

## Smoke Checks (built binary, isolated env)

**Every code block in this file, including the per-release acceptance sections
further down, assumes `. scripts/smoke-env.sh` is already in effect in the current
shell.** They seed fixtures by writing to whatever `$HOME` and `$XDG_*` path the
surface under test reads — kae's own `config.toml` and `state.json`, a tool's
credential file, an identity file such as `~/.claude.json` or
`~/.gemini/google_accounts.json`, `~/.gitconfig` — so the list is as long as the
sections are, and the rule is per block, not per file: **anything a block writes, it
overwrites for real if the preamble is not live.** Re-running an old section means
sourcing the preamble first — and sourcing it again mid-section starts a *new* temp
HOME, discarding the accounts the earlier blocks of that section set up.

All smoke checks run against a temp HOME. On Linux this isolates every
credential path. **On macOS it does not isolate the keychain-backed tools
(claude, cursor)**: those adapters always select a keychain driver and the
`security` CLI ignores `$HOME`, so their capture/switch/login against a temp
HOME still read — and switch **writes** — the real login keychain item. The
block below is therefore safe on both platforms only because it sets **two**
things, and it sets them in its own first lines rather than leaving them to a
reader: `KAE_CLAUDE_DRIVER=file` (claude's live credential → the file-patch
driver, so the whole capture/switch round-trip closes on `.credentials.json`;
see [ADAPTERS.md](ADAPTERS.md) "File-driver override") **and** `[security]
secret_backend = "file"` (kae's own snapshot store → file backend, not the
`kagikae` keychain). Neither alone is enough: the driver override still leaves
`kae add` writing the captured payload to the `kagikae` keychain item, which
prompts a macOS authorization dialog. The `secret_backend` line has to be
written **before the first `kae add`**, which is why the block writes its own
`config.toml` instead of taking what `kae init` leaves.

cursor is darwin-only, so it cannot be live-switched safely in a smoke run at
all (Linux reports it unsupported, macOS would touch the real keychain) —
verify cursor on the real machine only.

```bash
go build -o /tmp/kae .
. scripts/smoke-env.sh          # HOME + every root paths.Resolve reads; the script says why
export KAE_CLAUDE_DRIVER=file   # claude's live credential -> file driver, never the keychain
unset COPILOT_HOME CLAUDE_CONFIG_DIR CODEX_HOME CLAUDE_SECURESTORAGE_CONFIG_DIR
                                # each of these outranks the temp HOME, so a smoke run with
                                # one still set patches the real config

# kae's own snapshot store -> file backend. This must be written BEFORE the first
# `kae add`: the config `kae init` writes puts captured payloads in the `kagikae`
# keychain item, which prompts for authorization on macOS.
mkdir -p "$XDG_CONFIG_HOME/kagikae"
printf '# comment kept to prove the config writer preserves it\nversion = 1\n[security]\nsecret_backend = "file"\nbackup_keep = 30\n' \
  > "$XDG_CONFIG_HOME/kagikae/config.toml"

# Fixtures. Each is a file some adapter reads; the surfaces below capture from them.
mkdir -p "$HOME/.claude" "$HOME/.codex" "$HOME/.copilot" "$XDG_DATA_HOME/opencode"
printf '{"claudeAiOauth":{"accessToken":"tok-A","refreshToken":"r-A","expiresAt":9999999999999}}' \
  > "$HOME/.claude/.credentials.json"
printf '{"oauthAccount":{"emailAddress":"you@example.com","accountUuid":"u-1"}}' \
  > "$HOME/.claude.json"
printf '{"tokens":{"access_token":"a-1","refresh_token":"r-1","account_id":"acct-1"}}' \
  > "$HOME/.codex/auth.json"
printf 'model = "gpt-5"\n' > "$HOME/.codex/config.toml"
printf '{"openai":{"type":"oauth","refresh":"r-main","access":"a-main"},"other":{"type":"api","key":"sk-other"}}' \
  > "$XDG_DATA_HOME/opencode/auth.json"
printf '// managed automatically\n{\n  "trustedFolders": ["/w"],\n  "lastLoggedInUser": {"host":"h","login":"a"},\n  "loggedInUsers": [{"host":"h","login":"a"}]\n}\n' \
  > "$HOME/.copilot/config.json"

/tmp/kae init
/tmp/kae doctor --json
/tmp/kae status --json
/tmp/kae version --format json

# --- capture and switch, one tool at a time -------------------------------
/tmp/kae add --no-login claude main             # reports "driver: claude-file-patch"
/tmp/kae use claude main --dry-run              # both output forms of the preview
/tmp/kae use claude main --dry-run --json       # json-pointer action, no keychain
/tmp/kae use claude main --json
/tmp/kae backup list --json
/tmp/kae rollback

/tmp/kae add --no-login codex main --json
/tmp/kae use codex main --json

/tmp/kae add --no-login opencode main --json
/tmp/kae use opencode main --json
grep -c '"other"' "$XDG_DATA_HOME/opencode/auth.json"   # assert: 1 — the sibling provider
                                                        #   key survives the switch

/tmp/kae add --no-login copilot main --json
/tmp/kae use copilot main --json
head -1 "$HOME/.copilot/config.json" | grep '^//'       # assert: the leading JSONC comment
                                                        #   survives the pointer patch
grep -c '"/w"' "$HOME/.copilot/config.json"             # assert: 1 — trustedFolders survive

/tmp/kae doctor --json                          # opencode and copilot checks present

# --- run: the auth transaction, per-command env, the global isolated home ---
/tmp/kae run claude main -- /usr/bin/true       # auth transaction + restore
echo sk-test | /tmp/kae env set claude ci ANTHROPIC_API_KEY
/tmp/kae env list --json
/tmp/kae run --env claude ci -- /usr/bin/env | grep -c '^ANTHROPIC_API_KEY='   # assert: 1
test "$(env | grep -c '^ANTHROPIC_API_KEY=')" -eq 0   # assert: the child saw it, this shell
                                                #   does not. Wrapped in `test` because a bare
                                                #   `grep -c` here inverts: it exits 1 when the
                                                #   count is the correct 0, and 0 on the leak
                                                #   this line exists to catch
/tmp/kae run -i claude main -- /usr/bin/true    # global isolated home, no lock, no live mutation

# --- profiles, and what a bare `kae use` resolves --------------------------
/tmp/kae profile set main claude main
/tmp/kae profile set side claude main
/tmp/kae profile set side codex main
/tmp/kae use main --json                        # profile form
/tmp/kae use --json; test $? -eq 64             # assert: 64 — a bare `kae use` resolves
                                                #   default_profile, which is not set yet
/tmp/kae profile default main
/tmp/kae use --json                             # now resolves; re-run: "changed": false
KAE_PROFILE=side /tmp/kae use --json            # env resolution
/tmp/kae use --quiet                            # prints nothing on success
/tmp/kae profile default                        # prints the current default
/tmp/kae profile save snapshot                  # from the active accounts
/tmp/kae mise init --profile main               # preview, no write
/tmp/kae mise init --profile main --auto        # preview: [hooks.enter] kae use --quiet

# --- pin: binds the CURRENT directory, so run it inside the temp HOME ------
# `kae pin` also writes a $GIT_COMMON_DIR/info/exclude entry, which is why this is a
# throwaway repository and never this checkout.
P="$HOME/code/side-project"; mkdir -p "$P"; cd "$P"; git init -q .
FRAG=.config/mise/conf.d/kagikae.toml
ISO="$XDG_DATA_HOME/kagikae/isolation"

/tmp/kae pin side                               # DEFAULT IS SHARED (-s), not isolated;
                                                #   docs/CLI.md § kae pin is normative
grep -c 'codex/shared' "$FRAG"                  # assert: 1 — CODEX_HOME points at
                                                #   isolation/<pin-id>/codex/shared/
grep -c 'CLAUDE_SECURESTORAGE_CONFIG_DIR' "$FRAG"   # assert: 1 — a bind exports the pair,
                                                #   config dir and per-account credential store
test -L "$ISO"/*/codex/shared/config.toml       # assert: config.toml symlinked from ~/.codex
test -f "$ISO"/*/codex/shared/auth.json         # assert: auth.json private-copied, not linked
/tmp/kae pin side                               # assert: idempotent (regenerated, no error)

/tmp/kae pin -i side                            # isolated: full isolation, no symlinks
grep -c 'codex/isolated/main/config' "$FRAG"    # assert: 1
test -f "$ISO"/*/codex/isolated/main/config/auth.json   # assert: credential private-copied.
                                                #   POSITIVE CONTROL for the line below: with
                                                #   no isolated store the `find` there expands
                                                #   to nothing and its `test -z` passes for a
                                                #   store that was never created
test -z "$(find "$ISO"/*/codex/isolated -type l)"   # assert: no symlinks in isolated mode.
                                                #   Scoped to the isolated store on purpose:
                                                #   `kae unpin` and a mode switch both leave
                                                #   the previous store in place, so a search
                                                #   of the whole tree finds the shared bind's
                                                #   config.toml link and fails for the wrong
                                                #   reason (measured while writing this block)

# `-s` switches back. Placed after the isolated bind on purpose: run while the
# directory is already shared it proves only that the flag parses, since a build
# that ignored `-s` would look identical.
/tmp/kae pin -s side
grep -c 'codex/shared' "$FRAG"                  # assert: 1 — the bind really moved back
/tmp/kae pin -i side                            # isolated again for the re-bind case below
grep -c 'codex/isolated/main/config' "$FRAG"    # assert: 1

# `kae pin <tool> <account>` re-binds ONE tool and takes an **account**, not a profile.
/tmp/kae add --no-login codex side --json       # so capture the account first
/tmp/kae pin codex side
grep -c 'codex/isolated/side/config' "$FRAG"    # assert: 1 — the codex entry moved
grep -c 'claude/isolated/main/config' "$FRAG"   # assert: 1 — and claude did NOT. Match the
                                                #   whole path, not the bare word `claude`:
                                                #   the fragment names the tool three times
                                                #   whatever it is bound to, so `grep -c claude`
                                                #   reads as a pass even when claude moves too
/tmp/kae unpin                                  # removes only the block

# The legacy overlay-mode block still warns (internal/cmd/pin.go), so it still needs a case.
printf '# Directory-scoped overlay mode (legacy)\n[env]\n' > .mise.toml
/tmp/kae pin side 2>&1 | grep -c 'legacy overlay-mode block'   # assert: 1 — migration hint
rm -f .mise.toml
/tmp/kae unpin
cd - >/dev/null

# --- account and profile lifecycle (comment-preserving config writer) ------
# Ordering matters: nothing here may remove an account a later line needs.
/tmp/kae account rm claude main; test $? -eq 10 # assert: 10 — active, and no --force
/tmp/kae profile set dev codex side             # a profile that references the account
/tmp/kae account rename codex side side2 --json
grep -c side2 "$XDG_CONFIG_HOME/kagikae/config.toml"   # assert: >=1 — the profile ref moved
/tmp/kae profile default dev                    # sets default_profile
/tmp/kae profile rm dev; test $? -eq 10         # assert: 10 — it is the default, no --force
/tmp/kae profile rm dev --force                 # --force takes it, and the default with it
test "$(grep -c '^default_profile' "$XDG_CONFIG_HOME/kagikae/config.toml")" -eq 0
                                                # assert: default_profile is cleared when the
                                                #   profile it named is removed
/tmp/kae profile set dev codex side2            # re-create it for the other removal path
/tmp/kae profile unset dev codex                # unsetting the last mapping removes the profile
test "$(grep -c '^\[profiles\.dev' "$XDG_CONFIG_HOME/kagikae/config.toml")" -eq 0
                                                # assert: and the profile table went with it
/tmp/kae profile default main                   # a default again for the lines below
/tmp/kae account rm codex side2 --force --json  # drops the account and its profile refs
/tmp/kae account rm codex ghost; test $? -eq 7  # assert: 7 (not_found)
grep -c '# comment kept to prove' "$XDG_CONFIG_HOME/kagikae/config.toml"
                                                # assert: 1 — comments and unrelated keys
                                                #   survived every edit above
/tmp/kae switch x y; test $? -eq 64             # assert: 64 + a replacement pointer
EDITOR=true /tmp/kae edit                       # validate round-trip
/tmp/kae status --json                          # has "profiles"; "pinned" is null here, the
                                                #   directory above having been unpinned
```

**Every line must exit `0`**, including the `<cmd>; test $? -eq <n>` ones — those
carry the exit code the surface promises instead of printing it for a reader to
eyeball, so the line as a whole succeeds while the command in it does not. Every
line asserting a `grep -c` of **zero** is wrapped in `test` rather than left bare,
because a bare `grep -c` inverts there: it exits `1` on the correct answer and `0`
on the failure it is looking for. No count of such lines is given deliberately:
this paragraph has twice stated one that the next edit to the block falsified,
the second time inside the very commit that was correcting the first.

**Run it with `bash scripts/smoke-run.sh '## Smoke Checks'`, not by hand.** Every
hand-written harness for this file has leaked, and the leaks write to the machine
rather than failing. That script's header is normative for what it isolates and
what it cannot; do not restate the mechanism here, which is how the copy in
`AGENTS.md` came to name four of the eight cleared variables. Two consequences are
worth knowing before you read a green run, because they bound what it proves: the
macOS login keychain ignores `$HOME` and so is **not** isolated by anything, and
the leak detector sees the checkout only. `mise run check` runs
`scripts/smoke-run-selftest.sh`, so those guards are checked rather than asserted.

**What this block does not cover, said because a green run reads as if it did.**
`KAE_CLAUDE_DRIVER=file` is what makes it safe on macOS, and it keeps claude's
credential in a JSON-pointer file — so claude's **per-directory keychain
service-name derivation is never reached**, and no keychain **write** happens
anywhere in the block (measured with a `security` shim on `PATH`: zero
`add-generic-password` / `delete-generic-password` attempts). It does **not**
follow that the block avoids `internal/keychain`. `kae doctor --json` and
`kae status --json` probe agy, cursor and codex through it, and the `security`
CLI ignores `$HOME` — so the same measured run made ten reads against the
operator's *real* login keychain. cursor especially is only ever **read** here and
never switched, so a green run says nothing about switching it; that stays with
the unit tests over the darwin sim and the real-machine gates further down.

Enter-hook firing (`mise init --auto --write`) needs a live mise:
`mise settings experimental=true` (hooks are experimental; the global config
this writes must itself be `mise trust`-ed), `mise trust` on the project
`.mise.toml`, and a shell with `mise activate`. In a temp-HOME smoke, point
`ZDOTDIR` at a temp dir whose `.zshrc` exports PATH and evals
`mise activate zsh`, then run `zsh -i -c 'cd <project> && true'` from a
neutral directory (the repo's own untrusted mise.toml otherwise aborts
hook-env) and assert `kae use --quiet` fired and that re-entry adds no backup.


## v0.8.0 surfaces

All checks use the same temp-HOME and file-backend setup as the blocks above.
**macOS keychain safety rules are unchanged** — use `KAE_CLAUDE_DRIVER=file`
and `secret_backend = "file"` throughout.

```bash
go build -o /tmp/kae .
. scripts/smoke-env.sh
export KAE_CLAUDE_DRIVER=file
mkdir -p "$XDG_CONFIG_HOME/kagikae" "$HOME/.claude"
printf 'version = 1\n[security]\nsecret_backend = "file"\nbackup_keep = 30\n' \
  > "$XDG_CONFIG_HOME/kagikae/config.toml"
# seed credentials and add accounts:
printf '{"claudeAiOauth":{"accessToken":"tok-main"}}' \
  > "$HOME/.claude/.credentials.json"
/tmp/kae init
/tmp/kae add --no-login claude main
/tmp/kae add --no-login codex side

# --- A. apply fold: bare kae use idempotency ---
/tmp/kae use --json
#   assert: JSON contains "changed": true (first apply; switches to default profile)
/tmp/kae use --json
#   assert: JSON contains "changed": false (already active; no lock, no backup)
/tmp/kae use --quiet
#   assert: no output (silent on success)
KAE_PROFILE=side /tmp/kae use --json
#   assert: JSON shows resolution via KAE_PROFILE env var
/tmp/kae use -P main --json
#   assert: JSON shows -P flag resolution
/tmp/kae apply x; echo $?
#   assert: exit 64; output names "kae use [--quiet]" as the replacement

# --- B. run redesign (-s / -i / --env; --mode removed) ---
/tmp/kae run -i claude main -- /usr/bin/true
#   assert: process ran in isolation/global/claude/main/
#   assert: output names the shared home ("shared with kae use -i main")
#   assert: no per-tool lock held (concurrent kae use in another shell must not be blocked)
#   (concurrency check: open a second shell, run "kae use claude main" while run -i is
#    executing a long-running child; it must complete without waiting)
/tmp/kae run claude main -- /usr/bin/true
#   assert: run -s (default): auth transaction + restore; lock held during child
/tmp/kae run --env claude main -- /usr/bin/env
#   assert: profile env vars visible to child; no home redirect; no lock
/tmp/kae run --mode env claude main -- /usr/bin/true; echo $?
#   assert: usage error / non-zero exit (--mode flag removed in v0.8.0)

# --- C. mise init trim (bond/pin modes rejected; auth renders kae use --quiet hook) ---
/tmp/kae mise init --profile main
#   assert: preview shows [tasks] block; no [env] tool-home entries
/tmp/kae mise init --profile main --auto
#   assert: preview shows [hooks.enter] with "kae use --quiet" (not "kae apply ...")
/tmp/kae mise init --profile main --mode auth
#   assert: identical to --auto preview (explicit --mode auth is accepted)
/tmp/kae mise init --profile main --mode bond; echo $?
#   assert: rejected (non-zero exit); error names kae pin as replacement
/tmp/kae mise init --profile main --mode pin; echo $?
#   assert: rejected (non-zero exit); error names kae pin as replacement

# --- D. config-key rename gate (hard break, no alias) ---
printf 'version = 1\n[security]\nsecret_backend = "file"\n[tools.claude]\nbond_denylist_extra = ["extra"]\n' \
  > "$XDG_CONFIG_HOME/kagikae/config.toml"
/tmp/kae status; echo $?
#   assert: load error naming the new key "shared_denylist_extra" (not silent)
printf 'version = 1\n[security]\nsecret_backend = "file"\n[tools.codex]\npin_shared_items = ["settings"]\n' \
  > "$XDG_CONFIG_HOME/kagikae/config.toml"
/tmp/kae status; echo $?
#   assert: load error naming the new key "isolated_shared_items" (not silent)
printf 'version = 1\n[security]\nsecret_backend = "file"\n[tools.claude]\nshared_denylist_extra = ["extra"]\n[tools.codex]\nisolated_shared_items = ["settings"]\n' \
  > "$XDG_CONFIG_HOME/kagikae/config.toml"
/tmp/kae status --json
#   assert: loads successfully; JSON contains global-isolated homes in "synced"
# restore clean config for remaining checks:
printf 'version = 1\n[security]\nsecret_backend = "file"\nbackup_keep = 30\n' \
  > "$XDG_CONFIG_HOME/kagikae/config.toml"

# --- E. -i profile skip (unsupported tools skipped with warning, not exit 5) ---
# seed a profile that maps both claude and agy:
/tmp/kae profile set multi claude main
/tmp/kae profile set multi agy main  2>/dev/null || true   # agy may not be captured; that's fine
/tmp/kae use -i multi; echo $?
#   assert: exit 0; claude isolated; agy skipped with a warning on stderr
/tmp/kae use -i agy main; echo $?
#   assert: exit 5 (single-tool agy isolation is unsupported)

# --- F. input ergonomics ---
/tmp/kae use cl main --json
#   assert: "cl" prefix resolves to "claude"; JSON shows canonical tool name
/tmp/kae use cod side --json
#   assert: "cod" prefix resolves to "codex"; JSON shows canonical tool name
/tmp/kae use c main; echo $?
#   assert: non-zero exit; error lists "claude" and "codex" as ambiguous candidates
/tmp/kae completion zsh | head -5
#   assert: output is a valid zsh completion script (starts with #compdef or _kae)
/tmp/kae completion bash | head -5
#   assert: output is a valid bash completion script
```

### v0.8.0 real-machine gate (required before release)

On a **staging machine or throwaway account** (never an account you actively
use — the teardown rewrites the live keychain from the snapshot) with global
mise active (`mise activate` in the shell, `mise settings experimental=true`).
**Re-capture the account with `kae add` immediately before the gate** so the
snapshot's `accessToken` is live — a token captured earlier may have expired and
will 401 (see the 2026-06-16 result below). macOS rules apply: use
`KAE_CLAUDE_DRIVER=file` and a file-backend config so the real login keychain is
not touched by the isolated credential write. The gate confirms the isolation
fragment's `CLAUDE_CONFIG_DIR` wins over the real home / keychain.

- [x] `kae use -i claude <acct>` materializes
      `isolation/global/claude/<acct>/`, writes `~/.config/mise/conf.d/kagikae.toml`
      with `CLAUDE_CONFIG_DIR` pointing to that home.
- [x] A fresh-process `claude -p ... --model haiku` with the fragment's
      `CLAUDE_CONFIG_DIR` active returns **AUTH-OK** from the private home.
- [x] `kae run -i claude <acct> -- claude -p ... --model haiku` returns
      **AUTH-OK** in the isolated home and prints the shared-home confusion guard;
      no per-tool lock is held (no lock/backup output; verified lock-free in
      `runIsolatedChild`, which never calls `acquireLocks`).
- [x] The real `~/.claude` is not modified by `kae use -i` (file-driver path
      writes only the isolated home; the post-teardown real `claude` AUTH-OK below
      confirms the live keychain credential survived intact).
- [x] `kae use claude <acct>` (teardown): fragment deleted, `synced` cleared, a
      fresh `claude -p ... --model haiku` (no `CLAUDE_CONFIG_DIR`) returns AUTH-OK
      as the real account.

Result: **passed (2026-06-16, claude 2.1.178)** — run on the real machine after
re-capturing a live token (`kae add --no-login claude <acct>`) immediately
before the gate, so the teardown wrote a live token and the real login was not
disturbed. `use -i` and `run -i` both returned AUTH-OK from the isolated home;
the post-teardown real `claude` returned AUTH-OK.

### First attempt (2026-06-16, before the live re-capture)

An earlier attempt the same day skipped the pre-gate re-capture and broke the
real login (recovered with `claude /login`). It is kept here because the
cause-isolation is the proof that the **mechanism** is correct (no token value
was exposed — only `expiresAt` was inspected):

- `kae use -i claude <acct>` correctly materialized
  `isolation/global/claude/<acct>/.credentials.json` (mode `0600`) and wrote the
  global fragment pointing `CLAUDE_CONFIG_DIR` there; `~/.claude` was not
  modified.
- The fresh-process `claude --model haiku` against that home returned **401**,
  **not** AUTH-OK. Cause: the captured snapshot's `accessToken` was already past
  its `expiresAt` (the account had been captured days earlier and the live token
  had since been refreshed). This is a **stale-snapshot** failure, not an
  isolation-mechanism failure.
- Crucially, the 401 **confirms `CLAUDE_CONFIG_DIR` is honored on macOS**: had
  claude read the keychain instead, it would have found the live (different)
  account and returned AUTH-OK. Reading the expired file credential and failing
  proves the fragment redirect wins over the keychain (the v0.7.0 finding still
  holds on claude 2.1.178).
- **Real-environment damage from the teardown**: `kae use claude <real-acct>`
  rewrote the live keychain with that account's *snapshot* token, which had also
  expired since capture — overwriting the live (refreshed) token and forcing a
  `claude /login`. This is the expected consequence of kae's design (`use`
  applies the capture-time token; only `run` recaptures refreshed tokens), not a
  v0.8.0 regression. **Lesson: never run the gate's teardown against an account
  you actively use; re-capture (`kae add`) immediately before the gate so the
  snapshot token is live, and use throwaway accounts.**

The lesson from the first attempt is now baked into the gate preamble above:
re-capture a live token immediately before the run, and prefer throwaway
accounts — the teardown rewrites the live keychain from the snapshot.

## v0.8.1 surfaces

Credential freshness / auto-recapture. Same temp-HOME + file-backend setup as
the v0.8.0 block (`KAE_CLAUDE_DRIVER=file`, `secret_backend = "file"`). The
claude file driver stores the `/claudeAiOauth` sub-value, so a credential file's
`accessToken` is the snapshot payload.

```bash
# (continues from the v0.8.0 setup: /tmp/kae built, temp HOME + file config)

# --- A. switch-away recapture (use A -> B -> A applies the live-at-switch token) ---
printf '{"claudeAiOauth":{"accessToken":"tok-main-1"}}' > "$HOME/.claude/.credentials.json"
/tmp/kae add --no-login claude main
printf '{"claudeAiOauth":{"accessToken":"tok-side"}}' > "$HOME/.claude/.credentials.json"
/tmp/kae add --no-login claude side
/tmp/kae use claude main
printf '{"claudeAiOauth":{"accessToken":"tok-main-2"}}' > "$HOME/.claude/.credentials.json"  # in-tool refresh
/tmp/kae use claude side     # stderr: "refreshed claude/main snapshot ... before switching away"
/tmp/kae use claude main
grep -q tok-main-2 "$HOME/.claude/.credentials.json"   # assert: refreshed token came back, not tok-main-1

# --- B. switch to an expired snapshot with no refresh token warns (still proceeds) ---
printf '{"claudeAiOauth":{"accessToken":"old","refreshToken":"","expiresAt":1577836800000}}' \
  > "$HOME/.claude/.credentials.json"
/tmp/kae add --no-login claude stale
/tmp/kae use claude side
/tmp/kae use claude stale --json   # assert: results[].warnings names "expired" and "kae add"; exit 0

# --- D. doctor credential-health ---
/tmp/kae doctor claude --json
#   assert: a check {code:"credential_stale", status:"warn"} for claude/stale (names kae add)
# seed an orphan secret with no snapshot dir (file backend):
printf 'eA==\n' > "$XDG_DATA_HOME/kagikae/secrets/claude/ghost/claude_ai_oauth.secret"  # base64("x")
mkdir -p "$XDG_DATA_HOME/kagikae/secrets/claude/ghost" 2>/dev/null
/tmp/kae doctor claude --json
#   assert: a check {code:"secret_orphan", status:"warn"} for claude/ghost (names kae account rm)
# the mirror direction (v0.16.0): a snapshot whose declared payload is gone —
# delete the secret, leave the metadata that names it. Unlike secret_orphan this
# needs no enumeration, so it is the one of the two that also fires on the darwin
# keychain. A throwaway account, so the ones the blocks above rely on stay intact.
printf '{"claudeAiOauth":{"accessToken":"tok-zeta"}}' > "$HOME/.claude/.credentials.json"
/tmp/kae add --no-login claude zeta
rm "$XDG_DATA_HOME/kagikae/secrets/claude/zeta/claude_ai_oauth.secret"
/tmp/kae doctor claude --json
#   assert: a check {code:"secret_missing", status:"warn"} naming snapshot "zeta",
#          the artifact, and `kae add --no-login` — and no secret value anywhere in it
```

§C `security`-read coalescing is asserted by unit tests
(`internal/keychain` cache count; `internal/cmd` `TestSwitchCoalescesKeychainReads`
counts exactly one `find-generic-password -w` per switch). On a real keychain
machine it shows as a single auth prompt per switch rather than several.

### v0.8.1 real-machine gate (required before release) — **PASSED (2026-06-16)**

Two surfaces: the real-keychain-only risks (verbatim round-trip under the new
recapture read, prompt coalescing, doctor on the keychain backend) on the real
machine, and the driver-agnostic freshness logic (recapture-on-divergence, the
stale warning) via the temp-HOME file-driver smoke above (identical code paths).
Re-capture a live token with `kae add` immediately before the real-keychain run
(the teardown rewrites the live keychain from the snapshot).

- [x] Real-keychain round-trip is intact under the recapture read: after
      `kae add --no-login claude <acct>` (live capture) and `kae use claude <acct>`,
      a fresh `claude -p` returned **AUTH-OK** — the new switch-away recapture
      reads the keychain before applying without corrupting the verbatim bytes.
- [x] A single `kae use` issues **no** extra keychain auth prompts (the item ACL
      trusts `/usr/bin/security`, so reads do not prompt; the coalescing keeps it
      to one read, asserted by `TestSwitchCoalescesKeychainReads`). No
      prompt multiplication.
- [x] `kae doctor claude` on the keychain backend: `credential_stale` correctly
      **absent** for the freshly-captured account; `secret_orphan` correctly
      **skipped** (the darwin keychain cannot enumerate — documented gap).
- [x] Recapture-on-divergence and the stale warning (snapshot past `expiresAt`,
      no refresh token, naming `kae add`) confirmed via the temp-HOME file-driver
      smoke above; the logic is driver-agnostic, so it was not re-produced on the
      real keychain (which would need a second account and a natural in-tool
      token refresh).

## v0.8.2 surfaces

Daily-use polish: concurrent `status`, switch-read coalescing, `kae add` name
auto-detection, `kae ls`. Same temp-HOME + file-backend setup as the v0.8.0
block (`KAE_CLAUDE_DRIVER=file`, `secret_backend = "file"`).

```bash
# (continues from the v0.8.0 setup: /tmp/kae built, temp HOME + file config)

# --- B. kae add account-name auto-detection ---
# Seed a claude login whose ~/.claude.json carries an oauthAccount email:
printf '{"claudeAiOauth":{"accessToken":"tok"}}' > "$HOME/.claude/.credentials.json"
printf '{"oauthAccount":{"emailAddress":"alice@example.com"}}' > "$HOME/.claude.json"
/tmp/kae add --no-login claude --json
#   assert: captured account is "alice" (email local part, sanitized)
/tmp/kae add --no-login claude chosen --json
#   assert: explicit name "chosen" is used, not the detected one
/tmp/kae add --no-login agy; echo $?
#   assert: usage error (64) naming "kae add agy <account>" — no ~/.gemini/google_accounts.json
#           in the temp HOME, so agy identity detection fails (v0.8.7; with a real Antigravity
#           login the active Google account auto-names it)
rm "$HOME/.claude.json"
/tmp/kae add --no-login claude; echo $?
#   assert: usage error (64) naming "kae add claude <account>" (logged out: no identity)

# --- C. kae ls ---
/tmp/kae ls --json
#   assert: schema_version 1; "accounts" and "profiles" arrays (>= the captured/defined ones);
#           the active account/profile carry "active": true; both are [] (not null) when empty
/tmp/kae ls
#   assert: text view shows an "Accounts:" table and a "Profiles:" section with active markers
```

§A is asserted by unit tests (driver-agnostic, no real machine needed):
concurrent `Detect` by `internal/cmd` `TestStatusDetectsConcurrently` (every
enabled tool's `Detect` must enter `LookPath` before any is released — a
sequential loop would deadlock), and the switch-time secret-read coalescing by
`TestSwitchReadsTargetSnapshotOnce` (exactly one backend read of the target
snapshot per switch) plus `internal/secret` `cache_test.go`. §D's shared
comparator is covered by the unchanged switch/login tests plus
`TestSnapshotArtifactDiffers`, and `internal/jwt` by `jwt_test.go`.

### v0.8.2 real-machine gate

The §A–§D logic is **driver-agnostic** and fully covered by unit tests and the
temp-HOME file-driver smoke above (single account, no real keychain), so the
single-account-doable range is what gates this release:

- [x] §A concurrency + secret read cache: `TestStatusDetectsConcurrently`,
      `TestSwitchReadsTargetSnapshotOnce`, `internal/secret` `cache_test.go`
      (also `-race` clean).
- [x] §A recapture round-trip (use A → in-tool refresh → use B → use A re-applies
      the refreshed token): `TestSwitchAwayRecapturesRefreshedToken` (temp-HOME
      file driver — same code path as the keychain driver).
- [x] §B auto-detect: temp-HOME smoke captured `claude/alice` from a seeded
      `oauthAccount.emailAddress`; explicit name and the no-identity (agy /
      logged-out) usage error confirmed.
- [x] §C `kae ls`: temp-HOME smoke listed accounts + profiles with active markers
      and kept `[]` arrays in `--json`.
- [x] §D comparator + JWT: `TestSnapshotArtifactDiffers`, `internal/jwt`
      `jwt_test.go`, and the unchanged switch/login tests.

Two-account real-keychain run (claude, macOS) — **passed (2026-06-16)**:

- [x] `kae add --no-login claude` (no name) on a live login captured under the
      detected account name (the sanitized login email).
- [x] `kae use claude A` → `kae use claude B` → `kae use claude A`: a fresh
      `claude -p` as A returned **AUTH-OK** — the verbatim keychain round-trip
      survives the switch-away recapture read across two real accounts.
- [x] A single `kae use` raised no extra keychain auth prompts (the keychain read
      cache and the secret read cache both hold).
- [x] `kae ls` listed both accounts with the active one marked.
- [—] The `refreshed claude/A snapshot …` recapture message did **not** fire in
      this run: A's live token had not diverged from its snapshot at switch-away,
      so the divergence guard correctly skipped the rewrite (no write when they
      match). The recapture-on-divergence round-trip itself is covered
      driver-agnostically by `TestSwitchAwayRecapturesRefreshedToken` (the
      keychain and file drivers share the code path), as in the v0.8.1 gate.

## v0.8.3 surfaces

Lift the discovery-blocked items and surface the identity: §A
freshness-as-adapter-capability, §B cursor `kae add` identity, §C codex keyring
driver, §D store + display the detected identity. Most logic is driver-agnostic
and unit-tested; the temp-HOME smoke below (same `KAE_CLAUDE_DRIVER=file` +
`secret_backend = "file"` setup as the v0.8.0 block) covers the file-driver
range.

```bash
# (continues from the v0.8.0 setup: /tmp/kae built, temp HOME + file config)

# --- D. detected identity is stored + shown ---
printf '{"claudeAiOauth":{"accessToken":"tok"}}' > "$HOME/.claude/.credentials.json"
printf '{"oauthAccount":{"emailAddress":"you@example.com"}}' > "$HOME/.claude.json"
/tmp/kae add --no-login claude --json
#   assert: captured account "you"; account.toml + --json carry identity "you@example.com"
/tmp/kae add --no-login claude chosen --json
#   assert: explicit name "chosen"; identity still recorded "you@example.com" (best-effort)
/tmp/kae ls --json
#   assert: each account row carries "identity" (omitempty); schema_version stays 1
/tmp/kae ls
#   assert: the Accounts table shows an "Identity" column
```

§A is a pure refactor asserted by unit tests: per-tool `Freshness` lives on the
adapters (`internal/adapter/*/...` `TestClaudeFreshness*`, `TestCodexFreshness*`,
`TestOpencodeFreshness*`, `TestCursorFreshness*`), the primitives by
`internal/freshness` `freshness_test.go`, the registry conformance by
`TestFresherConformance`, and the unchanged switch/login/doctor/stale tests.
§B/§C use fake-runner tests (`internal/adapter/cursor` `TestCursorIdentity*`,
`internal/adapter/codex` keyring + `internal/cmd` `TestCodexKeyringRoundTrip` /
`TestCodexKeyringForeignHomeItemNotCaptured` / `TestKeychainCodexHomesCoexist`).
§D round-trip + recapture-preservation by `internal/cmd`
`TestAddRecordsIdentity*` / `TestRecapturePreservesIdentity`.

### v0.8.3 real-machine gate (required before release)

The driver-agnostic range is unit/temp-HOME covered above. Two surfaces need a
real machine — they exercise live subprocesses the temp-HOME smoke cannot fake:

**Codex keyring two-account round-trip** (macOS, real `Codex Auth` keychain).
Set `cli_auth_credentials_store = "keyring"` in `~/.codex/config.toml`, then:

- [ ] `kae add codex` (no name) on a live keyring login captures under the
      detected account (the `id_token` email / `account_id`), and `account.toml`
      holds no payload — nor a keychain account, which it deliberately no longer
      records (v0.16.0; [DATA-MODEL.md](DATA-MODEL.md)). The derived item account
      (`cli|` + 16 hex) is observable on the item itself:
      `security find-generic-password -s "Codex Auth"`, attributes only.
- [ ] Log in as a second account; `kae add codex` it.
- [ ] `kae use codex <first>`: a fresh-process `codex login status` (or a
      `codex` run) reports logged in as the first account — the verbatim keyring
      round-trip restored it. The item's account attribute is unchanged
      (`security find-generic-password -s "Codex Auth"`, attributes only): one
      codex home has one item whichever account is logged into it.
- [ ] A **second `CODEX_HOME`** logged in at the same time still is afterwards:
      `CODEX_HOME=<other> codex login status` reports its own account. This is the
      regression that shipped through v0.12.0 (a switch deleted the service's item
      by service name alone). The login-free half is covered by
      `TestKeychainCodexHomesCoexist`; this checks the real keychain.
- [ ] The payload read works without a keychain prompt. **Open**: codex writes its
      item through the Rust `keyring` crate (Security.framework directly, not
      `/usr/bin/security`), so whether `security -w` is in the item's ACL
      trusted-application list is unverified — if it prompts, the keyring driver
      needs the prompt documented or an ACL fix.
- [ ] No token value ever appeared in `kae` output, `--json`, or `account.toml`.

**Cursor `kae add` identity** (macOS, live `cursor-agent` login):

- [ ] `kae add cursor` (no name) on a live `cursor-agent status` login captures
      under the sanitized detected email (local part).
- [ ] Logged out (or `cursor-agent status` unparseable): `kae add cursor` exits
      `64` naming the explicit form.

**Cursor full credential set** (macOS, two live `cursor-agent` logins — open):

- [ ] `kae add cursor <name>` records `access_token` and `refresh_token` present;
      `api_key` present only for an api-key login.
- [ ] After `kae use cursor <other>`, `cursor-agent status` reports
      `authenticated` (not `partially-authenticated`) **and** the other account,
      and `security find-generic-password -s cursor-refresh-token` (attributes
      only) shows an `mdat` newer than the switch.
- [ ] A snapshot captured before the set was switched (no `refresh_token` entry,
      e.g. by deleting that key from `account.toml`) refuses the
      switch naming `kae add --no-login cursor <account>`, and the live items are
      unchanged afterwards.
- [ ] With an api key configured on one account only: after switching to the
      account **without** one, `cursor-api-key` is absent (kae removed it) rather
      than still holding the other account's key.

**codex per-directory keyring bind** (macOS, two codex homes — open; this is the
gate that must pass **before** codex is dropped from `bindableNotYetDeclared` in
`TestKeychainDirBindableMatchesTheItemIdentity`). Everything else is in place: the
account derivation is measured (§ "Upstream Behaviour Assumptions"), the flag now
measures item identity, and the teardown ships. What has never run is the whole
round-trip, and the failure it would hide is kae writing an item under an account
codex does not look up from that directory:

- [ ] With `cli_auth_credentials_store = "keyring"` and two captured accounts,
      `kae pin -i <profile>` in a scratch directory reports no unisolatable-credential
      warning for codex, and
      `security find-generic-password -s "Codex Auth" -a "cli|<16 hex of sha256 of
      the realpath of the pin config dir>"` (attributes only, hash computed with
      `shasum` outside kae) finds the item.
- [ ] In that directory, with mise active, a fresh `codex login status` names the
      bound account — the check that kae's account and codex's agree.
- [ ] The **global** `Codex Auth` item is untouched: its account attribute still
      resolves from `~/.codex` and its login still works outside the directory.
- [ ] `kae pin -s <profile>` in the same directory: the isolated store's item is
      gone (attributes probe returns not-found) and codex in the directory now reads
      the shared store's item.
- [ ] `kae unpin --purge`: both are gone, the global item survives, and the store
      directories remain.

Run with a committed tree and a throwaway/second account; record the result in
the Release Acceptance Log below.

## v0.8.4 surfaces

Dynamic shell completion sourced from a hidden `kae __complete` backend (§A),
native completion delegating to it plus an interactive `--install` (§B), and
mise task-argument completion through the same backend (§C). The backend, the
install file-writing, and the rendered task block are unit/temp-HOME covered
(`internal/cmd` `TestCompleteBackend*`, `TestCompletionInstall*`,
`TestMiseInitRendersCompletionTasks`, `TestCompletionGenerates`). The temp-HOME
smoke below confirms the binary end-to-end; the shell-level `<TAB>` behavior
needs the real-machine smoke (a non-interactive shell cannot fake completion).

```bash
# (continues from the v0.8.0 setup: /tmp/kae built, temp HOME + file config,
#  with a profile defined and at least one account captured)

# --- A. __complete backend ---
/tmp/kae __complete commands
#   assert: one command per line; "use" present; NO "__complete" line
/tmp/kae __complete tools
#   assert: the six canonical tools, one per line
/tmp/kae __complete profiles
#   assert: the configured profile names, one per line
/tmp/kae __complete accounts claude
#   assert: claude's captured account names, one per line
/tmp/kae __complete bogus; echo $?
#   assert: exit 64 (unknown kind)
/tmp/kae help | grep -c __complete
#   assert: 0 (hidden from help)

# --- B. native completion is dynamic + installs to the fpath file ---
/tmp/kae completion zsh | grep -q 'kae __complete' && echo dynamic-ok
#   assert: the script calls the backend (no baked word list)
printf '3\n' | /tmp/kae completion zsh --install   # 3 = print-only (no stdin TTY)
#   assert: prints the script; writes nothing
# choose the default (completions-dir file) by feeding an empty line:
printf '\n' | /tmp/kae completion fish --install
#   assert: writes $HOME/.config/fish/completions/kae.fish; re-run says "up to date"
test -f "$HOME/.config/fish/completions/kae.fish" && echo installed-ok
test ! -f "$HOME/.config/mise/config.toml" && echo mise-untouched-ok
#   assert: the default install never created the global mise config

# --- C. mise task completion directives ---
/tmp/kae mise init -P <profile> | grep -E 'tasks.ai-switch|complete "profile"|kae __complete'
#   assert: ai-switch / ai-switch-tool tasks with complete run="kae __complete …"
```

### v0.8.4 real-machine smoke (required before release)

The shell `<TAB>` resolution cannot be faked non-interactively. On a real
machine, for **each** of bash and zsh (fish was dropped from the verified shells
2026-06-18 — see the v0.8.6 gate; `kae completion fish` stays best-effort):

- [ ] Register completion (`eval "$(kae completion <shell>)"` or
      `kae completion <shell> --install`); open a fresh shell.
- [ ] `kae use <TAB>` offers live profiles + tools; `kae use claude <TAB>`
      offers claude's accounts; `kae <TAB>` offers commands.
- [ ] With `kae mise init --write` in a trusted, mise-activated project,
      `mise run ai-switch <TAB>` offers live profiles and
      `mise run ai-switch-tool <TAB>` offers live tools/accounts.
- [ ] `kae completion <shell> --install` → option 2 (mise hook) writes the
      kagikae block to the global mise config and is refused when a foreign
      `[hooks.enter]` already exists.

Record the result in the Release Acceptance Log below.

## v0.8.5 surfaces

A single Levenshtein nearest-match "did you mean X?" hint appended to the
unknown-command, unknown-tool, and unknown-profile usage errors (§A). It is a
pure-text behavior with no real-machine gate — fully covered by temp-HOME /
unit tests in `internal/cmd` (`TestNearestMatch` for the threshold/tie/exact
edges, `TestDidYouMeanUnknownCommand` / `TestDidYouMeanUnknownTool` /
`TestDidYouMeanUnknownProfile` for the three sites, and `TestDidYouMeanDoctorTool`
confirming `kae doctor <typo>` shares the validateTool path). Suggestion-only:
the tests assert the original exit code is preserved and an unrelated token
(`zzzzz`) appends nothing.

```bash
# (continues from the v0.8.0 setup: /tmp/kae built, temp HOME + file config)
/tmp/kae uze; echo $?
#   assert: 'unknown command: uze (see kae help) — did you mean "use"?'; exit 64
/tmp/kae add clade 2>&1; echo $?
#   assert: 'unknown tool "clade" ... — did you mean "claude"?'; exit 64
/tmp/kae zzzzz 2>&1
#   assert: no "did you mean" suffix (unrelated token)
```

## v0.8.6 surfaces

The agy keyring driver on macOS (§A) and the terser one-shot `kae run` default
child (§B). Both are unit/temp-HOME covered:

- **§A agy keychain driver** — `internal/adapter` `TestAgyDarwinKeychainDriver`
  (darwin resolves the gemini/antigravity match-account spec; logged-in/out
  Detect + doctor) and `TestAgyFileSnapshotOffDarwin` (Linux keeps the file
  driver). `internal/keychain` `TestReadItemForAccountScopesByAccount` /
  `TestDeleteItemForAccountScopesByAccount` / `TestReadItemServiceOnlyOmitsAccount`
  (the `-a` scoping). `internal/artifact`
  `TestKeychainMatchAccountScopesToAccount` /
  `TestKeychainMatchAccountAbsentDeletesOnlyOwnItem` (read/write/delete touch
  only the antigravity item; a sibling `gemini` item survives) and
  `TestKeychainOpaqueRefusesMultiline` (non-empty single-line guard).
  `internal/cmd` `TestAgyKeychainRoundTrip` (capture→use round-trip through the
  fake `security`, token never in output/metadata, sibling untouched) and
  `TestAgyKeychainEmptyPayloadRefused`.
- **§B run default child** — `internal/cmd` `TestDefaultChildCmd` (single tool →
  its binary; profile/multi-tool → usage error) and `TestRunDefaultsChildBinary`
  (end-to-end: `kae run claude main` with no `--` launches `claude` through the
  runner seam).

```bash
# (continues from the v0.8.0 setup: /tmp/kae built, temp HOME + file config,
#  with a claude account captured)
# §B: no -- defaults the child to the tool binary (here a stub on PATH).
/tmp/kae run claude main        # ⇒ runs `claude`; no trailing -- claude needed
/tmp/kae run -P <profile>; echo $?
#   assert: exit 64 — a profile target still requires -- <cmd>
```

### v0.8.6 real-machine gate

The driver/run logic is unit/temp-HOME covered above; the agy keychain path is
also fake-`security` covered.

**agy two-account real-keychain round-trip** (macOS, real `gemini`/`antigravity`
Keychain item; new in v0.8.6) — **PASSED (2026-06-18, macOS darwin 24.6.0, on the
v0.8.6 build)**: agy account switching round-trips correctly through the
`gemini`/`antigravity` item (verified by the maintainer; a fresh agy session
reflects the switched account). The matching is service+account so a non-agy
`gemini` item is never touched, and the opaque token never reaches output or
metadata (asserted by `TestAgyKeychainRoundTrip`).

**Carried gate** (unchanged by v0.8.6, fake-`security`/unit covered):

- [ ] codex keyring two-account real-keychain round-trip (v0.8.3 — see above);
      still deferred, the file/keyring round-trip is unit-covered.

**fish completion is no longer a gated target.** fish was dropped from the
officially-verified shells (2026-06-18); `kae completion fish` stays available
as a best-effort generator — unit-tested, and parsed by `fish --no-execute` on any
machine that has fish, which is not guaranteed to be the one that cuts the release
(`TestCompletionScriptsAreSyntacticallyValid` skips a shell it cannot find and says
so) — but is **not** a supported/release-gated surface. bash and zsh are the
verified shells.

Record release results in the Release Acceptance Log below.

## v0.8.7 surfaces

Complete account-identity coverage: `agy.Identity` from
`~/.gemini/google_accounts.json` (§A) and an `Identity` column in `kae status`
(§B). Both are pure-additive and unit/temp-HOME covered — **no new real-machine
gate** (agy identity is a plain file read, not a live subprocess):

- **§A agy identity** — `internal/adapter` `TestAgyIdentityFromGoogleAccounts` /
  `TestAgyIdentityMissingOrEmpty`, and `TestIdentifierConformance` pins that all
  six tool adapters implement `adapter.Identifier`. `internal/cmd`
  `TestAddAutoDetectAgyFromGoogleAccounts` (auto-named capture) and
  `TestAddAutoDetectFailureNamesExplicitForm` (no `google_accounts.json` →
  detection failure naming the explicit form).
- **§B status identity** — `internal/cmd` `TestStatusShowsActiveAccountIdentity`
  (text column + additive `identity` JSON field, `schema_version` 1).

```bash
# (continues from the v0.8.0 setup: /tmp/kae built, temp HOME + file config)
printf '{"active":"you@example.com","old":[]}' > "$HOME/.gemini/google_accounts.json"
mkdir -p "$HOME/.gemini/antigravity-cli" && printf 'tok' > "$HOME/.gemini/antigravity-cli/credentials.enc"
/tmp/kae add --no-login agy            # ⇒ auto-detects account "you"; records identity
/tmp/kae status --json | grep -A2 '"tool": "agy"'   # assert: "identity": "you@example.com"
```

Existing accounts captured before their tool gained identity stay blank until
re-captured (`kae add --no-login <tool> <name>` while logged into that account).

## v0.8.8 surfaces

Daily-use fixes: opencode identity prefers the access-token email over the
opaque accountId UUID; flag-aware shell completion + flag-name completion. All
unit/temp-HOME covered; the shell `<TAB>` behavior needs the real-machine smoke
(a non-interactive shell cannot fake completion).

- **opencode identity** — `internal/adapter/opencode`
  `TestOpencodeIdentityPrefersProfileEmail` (email from the access-token JWT) /
  `TestOpencodeIdentityFallsBackToAccountID`.
- **flag-aware + flag-name completion** — `internal/cmd`
  `TestCompleteBackendKinds` (the `flags <command>` kind: add→`--no-login`/
  `--restore`, run→`-s`/`-i`/`--env`/`-P`, unknown→common only),
  `TestCompletionPositionalRouting` (positionals are flag-filtered; the
  flag-skip construct is present per shell), `TestCompletionScriptsCompleteFlags`
  (each script calls `kae __complete flags`), and `TestFlagSpecWiring` (flagSetFor
  reaches each command's real registrar, so the list cannot drift).

```bash
# (continues from the v0.8.0 setup: /tmp/kae built)
/tmp/kae __complete flags add    # assert: --no-login, --restore, + common flags
/tmp/kae __complete flags run    # assert: -s -i --env -P + common
/tmp/kae __complete flags status # assert: common flags only (no extras)
```

### v0.8.8 real-machine smoke (required before release)

bash and zsh (fish is best-effort, not gated — v0.8.6). In a fresh shell with
completion registered:

- [ ] `kae add --no-login <TAB>` completes tools (the flag does not shift it);
      `kae use -i claude <TAB>` completes claude's accounts.
- [ ] `kae add --<TAB>` offers `--no-login` / `--restore`; `kae run -<TAB>`
      offers `-s` / `-i` / `--env` / `-P`.
- [ ] On a live opencode (ChatGPT) login, `kae add opencode` (no name)
      auto-names from the email, not the accountId UUID.

Record the result in the Release Acceptance Log below.

## v0.8.9 surfaces

`kae completion zsh --install` detects an existing user `fpath` dir instead of a
fixed XDG dir. Unit/temp-HOME covered:

- `internal/cmd` `TestCompletionInstallZshPrefersExistingFpathDir` (a seeded
  `~/.config/zsh/completions` is chosen over the XDG fallback, and the activation
  note then omits the `fpath=(…)` instruction) and `TestCompletionInstallFpath`
  (with no user fpath dir present in the temp HOME, zsh still falls back to
  `$XDG_DATA_HOME/zsh/site-functions/_kae` — the prior behavior).

### v0.8.9 real-machine smoke (required before release)

- [ ] On zsh with `~/.config/zsh/completions` on `fpath`,
      `kae completion zsh --install` writes `_kae` there and a fresh shell
      completes `kae <TAB>` with no `.zshrc` change.
- [ ] With no user fpath dir, `--install` falls back to the XDG dir and prints
      the `fpath=(…)` line.

Record the result in the Release Acceptance Log below.

## v0.9.0 surfaces

Installable binaries (GoReleaser pipeline + `scripts/install.sh` + CI) and the
README rewrite. The pipeline is validated locally before tagging; the real
publish happens in CI on the tag.

Local pre-tag validation:

```bash
mise run goreleaser-check                             # config valid
mise run goreleaser-snapshot                          # local archives, no publish
# assert: dist/kae_<version>_<os>_<arch>.tar.gz for darwin/linux x amd64/arm64,
#         checksums.txt as "<sha256>  <archive>", and `kae` at each archive root
sh -n scripts/install.sh && shellcheck scripts/install.sh   # installer parses/lints
actionlint .github/workflows/*.yml                    # workflows lint clean
```

The install layout (archive name `kae_<version>_<os>_<arch>.tar.gz`, flat with
`kae` at the root, `checksums.txt` without `./`) is what `scripts/install.sh`
expects — keep the two in sync.

### v0.9.0 real-machine smoke (required after the release publishes)

- [ ] The `v0.9.0` tag's release has `kae_*_{darwin,linux}_{amd64,arm64}.tar.gz`
      assets + `checksums.txt` + provenance attestations.
- [ ] `curl -fsSL .../scripts/install.sh | sh` installs `kae` to `~/.local/bin`
      and `kae version` prints `v0.9.0` (checksum verified).
- [ ] `mise x github:webkaz-labs/kagikae@v0.9.0 -- kae version` resolves the
      release archive and runs.

## v0.15.x surfaces — credential lead time, inventory freshness, bound directories

All three are read-only reporting, so the whole block runs against a temp HOME with
the file driver and the file backend — no real `$HOME`, no real keychain. Deadlines
are computed from `date +%s` so the fixtures stay valid whenever this is re-run.

```bash
go build -o /tmp/kae .
. scripts/smoke-env.sh
mkdir -p "$HOME/.claude" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_STATE_HOME"
export KAE_CLAUDE_DRIVER=file
/tmp/kae init
printf 'default_profile = "main"\n[security]\nsecret_backend = "file"\n[profiles.main]\naccounts = { claude = "main" }\n' \
  > "$XDG_CONFIG_HOME/kagikae/config.toml"
NOW=$(date +%s); SOON=$(( (NOW + 3*86400) * 1000 )); FAR=$(( (NOW + 60*86400) * 1000 ))
# expiresAt is the access token (rolls forward, routinely in the past in a snapshot);
# refreshTokenExpiresAt is the login's absolute expiry and is the deadline kae judges.
cred() { printf '{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":%s}}' "$1" \
  > "$HOME/.claude/.credentials.json"; }

# --- A. the three bands, on account snapshots ---
cred "$SOON";            /tmp/kae add --no-login claude soon      # login expires in 3 days
cred "$FAR";             /tmp/kae add --no-login claude healthy   # 60 days out
cred 1609459200000;      /tmp/kae add --no-login claude dead      # expired in 2021
/tmp/kae doctor claude --json > "$HOME/A.json"
grep -q '"code": "credential_expiring"' "$HOME/A.json"      # assert: "soon" is reported,
grep -qF 'snapshot \"soon\" needs an interactive re-login in' "$HOME/A.json"
grep -q 'kae add --restore claude soon' "$HOME/A.json"      #  named with its day count
grep -qF 'snapshot \"dead\" is stale' "$HOME/A.json"           # assert: credential_stale
test "$(grep -c healthy "$HOME/A.json")" -eq 0              # assert: NO credential_* check
                              #   for "healthy" — the notice must be silent for most of a
                              #   login's life, or it is wallpaper. The four greps above
                              #   are this line's positive control: an empty report would
                              #   satisfy a lone absence check
/tmp/kae doctor claude --json >/dev/null; test $? -eq 0     # assert: 0 (warn never fails)

# --- B. the inventory column (ls / accounts / status) ---
/tmp/kae ls --no-color > "$HOME/B.txt"
grep -qE '^claude +dead .+re-login now$'          "$HOME/B.txt"   # assert: a Credential
grep -qE '^claude +healthy .+ ok$'                "$HOME/B.txt"   #   column reading
grep -qE '^claude +soon .+[0-9]+ day\(s\) left$'  "$HOME/B.txt"   #   these three
/tmp/kae ls --json > "$HOME/B.json"
grep -q '"schema_version": 1' "$HOME/B.json"                 # assert: schema_version still 1
test "$(grep -c '"credential"' "$HOME/B.json")" -eq 3        # assert: each row has additive
test "$(grep -c '"relogin_by"' "$HOME/B.json")" -eq 3        #   credential + relogin_by
grep -q '"relogin_by": "2021-01-01T00:00:00Z"' "$HOME/B.json"
test "$(grep -c '"relogin_by": "2020-01-01' "$HOME/B.json")" -eq 0
                              # assert: relogin_by is the *login* deadline
                              #   (refreshTokenExpiresAt), not expiresAt. "dead" is the
                              #   row that separates them: its refreshTokenExpiresAt is
                              #   2021-01-01 and its expiresAt 2020-01-01, so the pair
                              #   above pins the field from both sides. The absence half
                              #   cannot carry this alone — anchored on a date no fixture
                              #   uses it passes while testing nothing, measured here on
                              #   2026-08-09 by mutating it to 1999-01-01 and watching it
                              #   stay green

# --- C. a bound directory's own credential (the sweep) ---
cred "$FAR"; /tmp/kae add --no-login claude main
P="$HOME/project"; mkdir -p "$P"; P=$(cd "$P" && pwd -P); cd "$P"
/tmp/kae pin main
# The bound copy is the ACCOUNT's store, not a per-directory one: since the per-account
# credential store, every directory bound to claude/main reads credstore/claude/<account>/.
# This line used to `find` under isolation/<pinID>/, where a claude credential has not
# lived since that split — and `find` exits 0 when it matches nothing, so the variable
# went empty, the write below failed for an unrelated reason, and the case stopped
# testing its own subject without ever going red on the count.
CS="$XDG_DATA_HOME/kagikae/credstore/claude/main/.credentials.json"
test -f "$CS"                                        # assert: and that is where it is
/tmp/kae doctor --json > "$HOME/C1.json"
test "$(grep -c 'bound to' "$HOME/C1.json")" -eq 0   # assert: NO bound-credential check
                                                     #   while the copy is healthy
printf '{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":1609459200000}}' > "$CS"
/tmp/kae doctor --json > "$HOME/C2.json"
grep -q "the claude credential bound to $P is stale" "$HOME/C2.json"
                              # assert: credential_stale, naming the bound directory
grep -q "cd $P && kae relogin claude" "$HOME/C2.json"
                              # assert: the remedy is a login *in that directory*. NOT
                              #   `kae pin` / `kae add`, which land where nothing reads
test "$(grep -c 'kae pin' "$HOME/C2.json")" -eq 0
grep -qF 'snapshot \"dead\" is stale' "$HOME/C2.json"   # assert: the healthy claude/main
test "$(grep -cF 'snapshot \"main\"' "$HOME/C2.json")" -eq 0
                              #   snapshot is not reported alongside it — the "dead"
                              #   line is the positive control for that absence
/tmp/kae unpin
/tmp/kae doctor --json > "$HOME/C3.json"
test "$(grep -c 'bound to' "$HOME/C3.json")" -eq 0   # assert: silent again — unpin keeps
                              #   the store on purpose, so nothing points at it and
                              #   "bound to" would lie

# --- D. kae pin still writes a real store path in both modes (modeStoreDir) ---
cd "$P" && /tmp/kae pin main
SD=$(sed -n 's/^CLAUDE_CONFIG_DIR *= *"\(.*\)"/\1/p' .config/mise/conf.d/kagikae.toml)
printf '%s' "$SD" | grep -q '/claude/shared$'   # assert: shared mode names its own dir
test -d "$SD"                                   # assert: and the directory exists
cd "$P" && /tmp/kae pin -i main
SD=$(sed -n 's/^CLAUDE_CONFIG_DIR *= *"\(.*\)"/\1/p' .config/mise/conf.d/kagikae.toml)
printf '%s' "$SD" | grep -q '/claude/isolated/main/config$'
test -d "$SD"
```

**PASSED 2026-07-31** on the pre-release binaries: A, B, C and D as asserted.

Two things this block deliberately pins, because each is a failure that looks like
success:

- **`credential_expiring` must be silent for a healthy login and must fire for a
  closing one.** Both halves matter and each has been shipped wrong once: v0.15.0
  fired for every claude account (a threshold read against the wrong quantity) and
  v0.15.1 fired for none of them (over-corrected). The `healthy` and `soon` fixtures
  in §A are the pair that pins it. See the claude row in "Upstream Behaviour
  Assumptions" for what the deadline actually is.
- **The unpinned case must be silent.** `kae unpin` keeps the store, so the
  breadcrumb still names the directory — reporting its credential would name a
  directory that is not bound and a remedy that lands where nothing reads. The
  sweep therefore reads the mise **fragment**, not the store tree.

## companion-auth surfaces

Companion-auth lockstep (`kae companion`, delivered per-directory by `kae pin`).
Smoke against a temp HOME with the file backend; the `exec()` token path needs
`mise trust` (the same step any pin fragment needs).

This is the one block that runs `mise`, and `scripts/smoke-run.sh`'s header is
normative for why that is isolated at all — mise finds config by walking up from
the current directory rather than through `$HOME`, so the runner's ceiling is what
keeps the operator's own mise config out of this. Do not answer a trust error here
by widening `trusted_config_paths`; the header says what that costs.

```bash
go build -o /tmp/kae .
. scripts/smoke-env.sh
export KAE_CLAUDE_DRIVER=file
mkdir -p "$XDG_CONFIG_HOME/kagikae" "$HOME/.claude"
printf 'version = 1\n[security]\nsecret_backend = "file"\n' > "$XDG_CONFIG_HOME/kagikae/config.toml"
printf '{"claudeAiOauth":{"accessToken":"tok-main"}}' > "$HOME/.claude/.credentials.json"
/tmp/kae add --no-login claude main
/tmp/kae profile set main claude main
# Canonical, because kae resolves /private/var/... on macOS while `mktemp -d` returns
# /var/... — an uncanonical $proj makes the fragment's paths and these greps disagree.
proj=$(cd "$(mktemp -d)" && pwd -P)
# This overwrites $HOME/.gitconfig, so an un-isolated shell loses the operator's real one.
printf '[alias]\n\tlol = log --oneline\n[user]\n\temail = real@personal.test\n' > "$HOME/.gitconfig"

/tmp/kae companion add main git email=you@example.com name=You
/tmp/kae companion add main kubectl KUBECONFIG="$HOME/.kube/main"
printf 'ghp_smoke\n' | /tmp/kae companion add main gh GH_TOKEN
/tmp/kae companion list > "$HOME/comp.txt"
grep -qE '^main +gh +GH_TOKEN=\(secret\)$' "$HOME/comp.txt"   # assert: never the value
grep -q 'GH_TOKEN = ""' "$XDG_CONFIG_HOME/kagikae/config.toml"
                              # assert: the knob is recorded with an empty value where
                              #   the secret would be — the positive control for the
                              #   absence below, which an unwritten config would satisfy
test "$(grep -c ghp_smoke "$XDG_CONFIG_HOME/kagikae/config.toml")" -eq 0
                              # assert: config.toml holds no token plaintext
test "$(/tmp/kae __companion-token main gh GH_TOKEN)" = ghp_smoke   # assert: helper path

cd "$proj" && /tmp/kae pin main                    # writes the fragment
F=.config/mise/conf.d/kagikae.toml
grep -q 'redactions = \["GH_TOKEN"\]' "$F"         # assert: the fragment declares it
grep -qF 'GH_TOKEN = "{{ exec(' "$F"               # assert: resolved at eval, not stored
grep -q '^GIT_CONFIG_GLOBAL = "' "$F"              # assert: paths, not contents
grep -q '^KUBECONFIG = "' "$F"
test "$(grep -c ghp_smoke "$F")" -eq 0             # assert: no token plaintext either
mise trust "$F"

test "$(mise exec -- git config --get user.email)" = you@example.com
                              # assert: the binding overrides the real ~/.gitconfig
test "$(mise exec -- git config --get alias.lol)" = "log --oneline"
                              # assert: and preserves the rest of it
test "$(mise exec -- sh -c 'echo $GH_TOKEN')" = ghp_smoke
                              # assert: the exec() knob resolves at eval time
test "$(git config --get user.email)" = real@personal.test
                              # assert: unpinned, this shell is unchanged

/tmp/kae doctor --json > "$HOME/D.json"
test "$(grep -c companion_missing "$HOME/D.json")" -eq 0   # assert: secrets are stored
grep -q companion_drift "$HOME/D.json"
                              # assert: companion_drift IS present here — the pin is not
                              #   active in this shell, so git resolves real@personal.test
                              #   rather than the bound you@example.com
mise exec -- /tmp/kae doctor --json > "$HOME/E.json"
grep -q '"schema_version"' "$HOME/E.json"                  # positive control for the next
test "$(grep -c companion_drift "$HOME/E.json")" -eq 0     # assert: gone under the pin
```

`companion_token_drift` is opt-in and needs a network call, so the temp-HOME
smoke does not exercise it (the `ghp_smoke` token is invalid, so `kae companion
add` records no `expected_login` — a gentle skip; unit tests cover the probe with
a faked `gh`). To check it on a real machine: `printf '<real-token>\n' | kae
companion add main gh GH_TOKEN` (records `expected_login` from `gh api user`),
then in the pinned dir `mise exec -- kae doctor --yes` reports a match, and
`kae doctor --yes` outside the pin reports the inactive-pin warn.

## per-worktree surfaces — the exclude file and `kae ls --pins`

Two changes, both read-only outside the fragment write: `kae pin` records its
ignore rule in the repository's shared exclude file instead of a tracked
`./.gitignore`, and `kae ls --pins` lists every bound directory. Temp HOME, file
driver, file backend — no real `$HOME`, no real keychain. The repositories are
built **inside the temp HOME**, so nothing here can dirty a real checkout.

```bash
. scripts/smoke-env.sh
export KAE_CLAUDE_DRIVER=file
mkdir -p "$XDG_CONFIG_HOME/kagikae" "$HOME/.claude"
printf 'version = 1\n[security]\nsecret_backend = "file"\n' > "$XDG_CONFIG_HOME/kagikae/config.toml"
printf '{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":99999999999999}}' \
  > "$HOME/.claude/.credentials.json"
/tmp/kae add --no-login --identity you@example.com claude main
/tmp/kae profile set main claude main

W="$HOME/work"; mkdir -p "$W/main"; cd "$W/main"
git init -q && git -c user.email=you@example.com -c user.name=t commit -q --allow-empty -m init
git worktree add -q "$W/wt1" -b side
mkdir -p "$W/main/nested"

# --- A. the main checkout: no tracked file touched, nothing dirty ---
cd "$W/main" && /tmp/kae pin main
#   assert: the report reads `Wrote … (ignored via …/main/.git/info/exclude)`
git status --porcelain                 # assert: empty
git -C "$W/wt1" status --porcelain     # assert: empty — one entry already covers it
test ! -e "$W/main/.gitignore"         # assert: no tracked .gitignore was created

# --- B. the linked worktree binds independently and stays clean ---
cd "$W/wt1" && /tmp/kae pin main
#   assert: the exclude file named is the *main* checkout's .git/info/exclude
git -C "$W/main" status --porcelain; git status --porcelain   # assert: both empty

# --- C. a nested directory: the entry is anchored at the repository root ---
cd "$W/main/nested" && /tmp/kae pin main
grep nested "$W/main/.git/info/exclude"
#   assert: `/nested/.config/mise/conf.d/kagikae.toml` — without --show-prefix this
#           would read `/.config/…` and ignore nothing
git -C "$W/main" status --porcelain    # assert: empty

# --- D. outside any repository: no rule, and no claim of one ---
mkdir -p "$HOME/norepo"; cd "$HOME/norepo" && /tmp/kae pin main
#   assert: the report says `Wrote …; your mise.toml is untouched.` with NO
#           `ignored via`, and no .gitignore exists here

# --- E. kae ls --pins, from outside every bound directory ---
cd "$HOME" && /tmp/kae ls --pins
#   assert: four rows (norepo, work/main, work/main/nested, work/wt1), sorted by
#           directory, Current blank for all of them
cd "$W/wt1" && /tmp/kae ls --pins
#   assert: `*` in Current on work/wt1 only
/tmp/kae ls --pins --json              # assert: schema_version 1, bound_directories[]
/tmp/kae unpin && /tmp/kae ls --pins
#   assert: work/wt1 is GONE from the list — unpin keeps the store on purpose, and a
#           store is not a binding

# --- F. an unrecordable rule warns; it must not fail the bind ---
# Deliberately last: this case leaves a binding whose fragment is NOT ignored, so
# it dirties $W/main and adds a row — put it earlier and it silently changes E's
# row count, which is how the first draft of this block came to claim assertions
# that no longer held.
# The *file* has to be unwritable, not its directory: `chmod a-w .git` only stops
# entries being created in .git, and info/exclude already exists by now — measured
# 2026-08-04, that pin succeeded and recorded its rule as usual, so that draft
# proved nothing.
chmod a-w "$W/main/.git/info/exclude"
mkdir -p "$W/main/locked" && cd "$W/main/locked" && /tmp/kae pin main; echo "exit=$?"
#   assert: exit 0, a stderr warning `could not tell git to ignore …` saying the
#           binding is in place, and NO `ignored via` in the report. Reachable in
#           practice because the exclude file is *outside* the pinned directory: a
#           worktree can be writable while the main checkout's .git is not
#   assert: `git -C "$W/main" status --porcelain` now shows `?? locked/` — the
#           fragment really is unignored, which is what the warning told the user
chmod u+w "$W/main/.git/info/exclude"
```

**PASSED 2026-08-08 on the release tree, 31/31** (`/tmp/kae` = v0.17.0), and before that
2026-08-04 on the pre-release binary: A–F, each assertion checked
individually **at its own point in the block** rather than from the end state. Unlike the
two v0.17.0 credential blocks, this one needed no correction to run on the final tree —
it touches the fragment and git, neither of which the credential split moved. That
distinction is not pedantry — two earlier runs of this block completed without
erroring while assertions inside it were false (a row count changed by a case
inserted above it, and a `chmod` that did not make anything unwritable). "The block
ran" is not evidence; "this assertion held here" is.

The one that looks like success when it is wrong is **C**. `info/exclude` is
anchored at the repository root while a `.gitignore` entry is anchored at its own
directory, so carrying the old entry string over would write a rule that matches
nothing — and `kae pin` would still report success, with the fragment sitting in
`git status` for the user to find.

## v0.17.0 surface — the credential harvest

**Run this section with
`SMOKE_WHOLE_FILE=1 bash scripts/smoke-run.sh '## v0.17.0 surface — the credential harvest'`.**
The flag is needed because the block defines shell functions, which the per-line
runner cannot execute — and it costs the per-line verdict, so the runner reports
the block's own exit status and says it is not a verdict.

**A correct run of this section exits `1`, and that is not a failure.** Whole-file
mode returns the *last* command's status, and the last line here is
`snap main | grep -c MAIN-OLD` asserting a count of zero — which `grep -c` prints
while exiting 1. So the exit code carries no information about this block at all:
read the transcript the runner names, checking each `# assert:` at its own line.
That is the right way round for this section anyway, since it has the worst history
of green runs that proved nothing — fixtures written to a directory kae had stopped
reading, a `grep` defeated by base64, token names that prefixed one another.

Every path that overwrites a bound directory's credential store reads it first and
copies a newer copy into the account snapshot, because claude's refresh token
rotates single-use (§ Upstream Behaviour Assumptions) — so the older copy is not
merely older, it is rejected. Temp HOME, **file** driver, file backend: no real
`$HOME`, no real keychain, and no login, since every credential here is a fixture
whose `expiresAt` is the only thing that orders two copies.

**The credential is not in the config dir any more, and a fixture written there
tests nothing.** Every credential fixture below goes to
`CLAUDE_SECURESTORAGE_CONFIG_DIR` (`cstore`), every identity fixture to
`CLAUDE_CONFIG_DIR` (`store`) — [ADAPTERS.md](ADAPTERS.md) § Per-account credential store
is normative for which holds what. This block sent **both** to the config dir
until 2026-08-08, and the failure was not a red run: A, D and E stopped exercising the
harvest at all, while C ("no `harvested` line") and every `grep -c … = 0` line went
green *because* nothing had happened — the block reported the release's headline
mechanism as covered while measuring a directory kae no longer reads a credential from.
This file's own § Upstream Behaviour Assumptions had the rule right the whole time, so
one file contradicted itself.

**Whose login a copy is, is answered by the directories that read the store, not by the
one being bound** ([ADAPTERS.md](ADAPTERS.md) § Per-directory credential store), and that
is what A2 and A3 separate: the same first bind of a second directory harvests when a
sibling reader agrees and keeps when nothing reads the store at all. B1 and B2 are the
same split on the other side — all readers naming another account is positive evidence and
overwrites, readers that disagree is missing evidence and keeps.

Three things this block cannot show, so they are not claimed. The sweep's half (a
`--purge` harvesting before it deletes) is unit-covered here rather than smoke-covered
— `TestUnpinPurgeRemovesAFileCredentialFromTheAccountStore`,
`TestUnpinPurgeHarvestsANewerCopyBeforeRemovingIt` and
`TestRePinMigrationRemovesThePreSplitFile` are the file-driver halves, and the
keychain gates above are the other. This block deliberately does not run it: it would
add a purge to a scenario whose subject is the ordering of two copies. Note what
changed and why the older wording here was worse than a gap — it said a file-backed
per-directory credential "is never deleted", which stopped being true when a
credential could move out of the store it sits in, so this section was excusing the
one configuration that could have caught it. The file
backend stores payloads **base64-encoded**, so a snapshot assertion has to decode
before matching: `grep` on the raw file finds nothing and reads as a *passing*
assertion. Each case below resets the account it uses (`reset_main`), because a harvest
makes the snapshot fresh — running D before E without that reset made E's harvest a
no-op and E "passed" while proving nothing. **A re-capture is not that reset**: the
credential store belongs to the account and survives it, so with a sibling reader in play
the next `kae pin` harvests the leftover copy straight back and the case's own fixture is
no longer newer than anything — measured 2026-08-08, with D and E both silently reduced to
proving nothing that way. Token names never prefix one another for the same reason:
`grep MAIN-NEW` matched a `MAIN-NEW2` and three cases passed on a copy they were not about.

```bash
. scripts/smoke-env.sh
export KAE_CLAUDE_DRIVER=file
OLD=1767225600000; NEW=1798761600000; LATER=1814400000000   # expiresAt: 2026-01-01, 2027-01-01, 2027-07-01
cred() { printf '{"claudeAiOauth":{"accessToken":"%s","refreshToken":"rt-%s","expiresAt":%s,"refreshTokenExpiresAt":1830384000000}}' "$1" "$1" "$2"; }
ident() { printf '{"oauthAccount":{"accountUuid":"u-%s","emailAddress":"%s@example.com"}}' "$1" "$1"; }
snap() { base64 -d < "$XDG_DATA_HOME/kagikae/secrets/claude/$1/claude_ai_oauth.secret"; }
# Both anchored at ^, so neither variable's line can be read as the other's.
store()  { sed -n 's/^CLAUDE_CONFIG_DIR *= *"\(.*\)"/\1/p'                .config/mise/conf.d/kagikae.toml; }
cstore() { sed -n 's/^CLAUDE_SECURESTORAGE_CONFIG_DIR *= *"\(.*\)"/\1/p'   .config/mise/conf.d/kagikae.toml; }
# The same store by its absolute path, so a case can reach it without standing in a
# bound directory.
accstore() { printf '%s/kagikae/credstore/claude/%s/.credentials.json' "$XDG_DATA_HOME" "$1"; }
# Re-capturing the account is no longer enough to reset a case. The credential store
# belongs to the **account** and survives the re-capture, and with a sibling directory
# already reading it the next `kae pin` harvests that leftover copy straight back into
# the fresh snapshot — after which the case's own `$NEW` fixture is not newer than
# anything and its guard is never reached. Measured 2026-08-08: D and E had stopped
# testing their own subject exactly that way.
reset_main() {
  cred MAIN-OLD $OLD > "$HOME/.claude/.credentials.json"; ident main > "$HOME/.claude.json"
  /tmp/kae add --no-login --identity you@example.com claude main
  rm -f "$(accstore main)"
}

mkdir -p "$XDG_CONFIG_HOME/kagikae" "$HOME/.claude"
printf 'version = 1\n[security]\nsecret_backend = "file"\n' > "$XDG_CONFIG_HOME/kagikae/config.toml"
cred MAIN-OLD $OLD > "$HOME/.claude/.credentials.json"; ident main > "$HOME/.claude.json"
/tmp/kae add --no-login --identity you@example.com claude main
cred SIDE-OLD $OLD > "$HOME/.claude/.credentials.json"; ident side > "$HOME/.claude.json"
/tmp/kae add --no-login --identity other@example.com claude side
cred SOLO-OLD $OLD > "$HOME/.claude/.credentials.json"; ident solo > "$HOME/.claude.json"
/tmp/kae add --no-login --identity solo@example.com claude solo
/tmp/kae profile set main claude main
/tmp/kae profile set side claude side
/tmp/kae profile set solo claude solo

# --- A. the tool refreshed the copy inside the directory; a re-pin must keep it ---
P="$HOME/proj"; mkdir -p "$P"; cd "$P"
/tmp/kae pin main
# The identity cache the *tool* writes when it runs in a bound directory. Every case that
# expects a harvest seeds it, because attribution must read the tool's evidence and not
# kae's: kae no longer writes a label on the paths where it keeps a copy, precisely so a
# later bind cannot confirm against something kae planted (§ ADAPTERS.md). A block that
# leaned on kae's own label would be testing the shape that was removed.
ident main > "$(store)/.claude.json"
SP1="$(store)"                          # kept for B, which needs to reach both readers'
                                        # caches from outside their directories
cred MAIN-NEW $NEW > "$(cstore)/.credentials.json"
/tmp/kae pin main
#   assert: stderr carries `kae: harvested the newer claude credential from … into
#           snapshot claude/main`
snap main | grep MAIN-NEW              # assert: harvested, so `kae use claude main`
                                       #         applies it too
grep MAIN-NEW "$(cstore)/.credentials.json"  # assert: the re-pin did NOT write the
                                       #         older snapshot back over it

# --- A2. a SECOND directory bound to the same account must not spend its credential ---
# The case A cannot reach, because A re-pins a directory that already has an identity
# cache. A brand-new directory has none — the config dir is created moments earlier and a
# shared bind links no `.claude.json` — and that used to mean the bind wrote the older
# snapshot over the store. That is the account's ONE credential store, so the copy
# destroyed was the only one that could still refresh, for *every* directory bound to it.
# Fixed 2026-08-08; this case is what keeps it fixed.
#
# The evidence is not this directory's, though: it is the sibling P, which reads the same
# store and whose cache names main. So the copy is preserved **and** harvested — a strictly
# better outcome than keeping it, and the one that makes `kae use claude main` apply it
# too. A3 is the same bind with nobody reading the store.
cred MAIN-LATE $LATER > "$(cstore)/.credentials.json"   # the tool refreshed it again here
P2="$HOME/proj2"; mkdir -p "$P2"; cd "$P2"
/tmp/kae pin main
#   assert: exit 0, and stderr carries `harvested the newer claude credential` — NOT
#           `this write replaces it`
grep MAIN-LATE "$(accstore main)"        # assert: the copy that can still refresh is
                                        #         intact. This is the whole case: before
                                        #         the fix it held the older snapshot and
                                        #         every offline check was green.
                                        #         MAIN-LATE, not MAIN-NEW2: `grep MAIN-NEW`
                                        #         elsewhere in this block matches a
                                        #         `MAIN-NEW2` by prefix, so three cases
                                        #         passed on a copy they were not about
snap main | grep MAIN-LATE               # assert: and the sibling's evidence let it be
                                        #         harvested, so the snapshot holds it too
SP2="$(store)"
grep u-main "$SP2/.claude.json"          # assert: the write succeeded, so the label follows
                                        #         it. The label is written **only** on a
                                        #         successful write: kae's own label is what
                                        #         the next bind's attribution reads, and
                                        #         planting one where kae kept a copy let a
                                        #         later `kae pin` confirm against it and
                                        #         harvest the copy the first bind refused
                                        #         (measured 2026-08-08). A3 asserts the
                                        #         absence on the path that keeps
cd "$P"                                  # back to A's directory for the cases below

# --- A3. the same first bind with NOBODY reading the store: kept, and nothing labelled ---
# claude/solo has no bound directory, so no reader can say whose the copy is. That is the
# refusal that must keep rather than overwrite, and it is the one A2 stopped exercising
# once a sibling existed.
mkdir -p "$(dirname "$(accstore solo)")" # the store is created by whatever wrote into it;
                                        # without this the fixture below lands nowhere and
                                        # every assertion in this case is vacuous
cred SOLO-NEW $NEW > "$(accstore solo)"  # left by a `kae use -i`, or by a binding since
                                        # unpinned: the store outlives the directory
S="$HOME/solo-proj"; mkdir -p "$S"; cd "$S"
/tmp/kae pin solo
#   assert: exit 0, and stderr says kae `kept it rather than replacing it`, naming the
#           account's store and the reason `no directory reads this credential yet` —
#           NOT `this write replaces it`
grep SOLO-NEW "$(accstore solo)"         # assert: the only copy that can refresh survives
snap solo | grep -c SOLO-NEW             # assert: 0 — kept is not harvested. kae could not
                                        #         tell whose the copy is, so it neither
                                        #         destroys it nor files it under this account
test -d "$(store)"                       # assert: positive control for the line below. An
                                        #         empty expansion makes it `test ! -e
                                        #         "/.claude.json"`, which passes for a
                                        #         `store()` that returned nothing at all
test ! -e "$(store)/.claude.json"        # assert: and NO label was written; see A2
cd "$P"

# --- B1. every reader says the copy is another account's: replaced, once, no remedy ---
reset_main                               # snapshot back to MAIN-OLD with an empty store,
                                        # so $NEW below really is newer (an equal
                                        # `expiresAt` is never harvested, and reusing a
                                        # deadline here made this case pass without
                                        # reaching the attribution guard at all —
                                        # measured twice, 2026-08-08)
ident other > "$SP1/.claude.json"; ident other > "$SP2/.claude.json"
cred FOREIGN $NEW > "$(accstore main)"
/tmp/kae pin main
#   assert: exactly ONE stderr line — `belongs to an account other than claude/main`
#           — and it does not tell you to log in. One store, one refusal, one message:
#           the store being written is looked at by both the pin-level pass and the
#           write path, and the write path stays quiet where the pass has already
#           spoken. **No remedy on purpose**: the copy is demonstrably somebody else's,
#           this account's credential is being written correctly, and a login here
#           would mint a chain invalidating what kae harvested elsewhere. A
#           missing-evidence refusal (no reader at all, no cache in one, an unreadable
#           one, or one shared with the real home) *does* carry the remedy, with the
#           bound directory rather than the store in it. Exit code still 0
snap main | grep MAIN-OLD               # assert: the snapshot is the one reset_main wrote.
                                        #         This positive line is what makes the
                                        #         negative one below mean anything: a
                                        #         broken snap() (wrong secrets path, or
                                        #         BSD `base64 -D`) makes `grep -c` print
                                        #         0, which reads as a pass
snap main | grep -c FOREIGN             # assert: 0 — a token filed under the wrong
                                        #         account is undetectable afterwards
grep MAIN-OLD "$(accstore main)"        # assert: and positive evidence still overwrites —
                                        #         this account's credential is elsewhere,
                                        #         so the bind has to take effect
grep u-main "$SP1/.claude.json"          # assert: the bind still relabelled the dir

# --- B3. only a SIBLING disagrees: an unrelated bind may not spend the copy ---
# Same store, same unanimous readers as B1 — but the bind runs in a directory that reads
# nothing yet, so it has no reading of its own and is not the event that gets to decide
# whose the copy is. The first version of the reader model took a majority and destroyed
# the only copy of the sibling's login here (found by review, 2026-08-08).
reset_main
ident other > "$SP1/.claude.json"; ident other > "$SP2/.claude.json"
cred FOREIGN $NEW > "$(accstore main)"
Q="$HOME/newcomer"; mkdir -p "$Q"; cd "$Q"
/tmp/kae pin main
#   assert: exit 0, and kae `kept it rather than replacing it`, naming the reason
#           `this directory does not read it yet` — NOT `this write replaces it` — and,
#           only on this arm, the consequence `it will run that other account`. This is
#           the one keep where kae has positive evidence about the copy and so can say
#           what the directory does next; the command still prints its success line
grep FOREIGN "$(accstore main)"         # assert: the sibling's live login survives. B1 is
                                        #         the same fixture with the bind run *in* a
                                        #         reader, and there it is replaced — the
                                        #         pair is the whole condition
snap main | grep -c FOREIGN             # assert: 0 — kept is not harvested
cd "$P"

# --- B2. the readers DISAGREE: kept, and deliberately not reported as a conflict ---
# Somebody logged in as another account inside one bound directory. The copy is live and it
# is somebody's, and this bind is not the event that should decide whose — overwriting on a
# majority destroys a login that has no backup. Measured 2026-08-08: asking only the
# directory being bound is what let an ordinary re-pin file a foreign token under this
# account's name.
reset_main
ident main > "$SP1/.claude.json"        # P says main, P2 still says other
cred FOREIGN $NEW > "$(accstore main)"
/tmp/kae pin main
#   assert: one stderr line saying the directories that read this credential `disagree
#           about whose login it is`, that kae is `leaving it where it is`, and the
#           login remedy — a disagreement is missing evidence, so it carries one, unlike
#           B1. The pin-level pass is the speaker here (it knows the bound directory),
#           so this is *not* the chokepoint's `kept it rather than replacing it`; that
#           wording appears in A3, where no pass had anything to say
grep FOREIGN "$(accstore main)"         # assert: the live copy survives. This is the half
                                        #         that inverts: a refusal here would be a
                                        #         deletion, not a conservative choice
snap main | grep -c FOREIGN             # assert: 0 — kept is not harvested
ident main > "$SP2/.claude.json"        # put P2 back so the cases below start agreed

# --- C. a tombstone is not a login — and only the MEASURED tombstone is silent ---
reset_main
ident main > "$(store)/.claude.json"
# Two shapes, two arms, and this case tested only the second one while claiming the
# first until 2026-08-08. The measured tombstone (§ Upstream Behaviour Assumptions,
# the invalid_grant row) is blank tokens with **expiresAt: 0** and the login deadline
# retained; kae reads that as "nothing to lose" and replaces it without a word. Blank
# tokens with a *future* expiresAt are not that shape — kae cannot order them and will
# not call them dead, so it warns and says how to keep the login it might be. C used
# the future-dated form and asserted only the absence of a `harvested` line, which
# both arms satisfy.
# C1 — the measured tombstone: replaced in silence.
printf '{"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":0,"refreshTokenExpiresAt":1830384000000}}' \
  > "$(cstore)/.credentials.json"
/tmp/kae pin main
#   assert: NO `harvested` line — presence is not usability
#   assert: and NO warning at all. This is the one arm that is silent, so it is the
#           assertion that separates the two; without it C passes on either
snap main | grep MAIN-OLD               # assert: unchanged
grep MAIN-OLD "$(cstore)/.credentials.json"   # assert: and the snapshot really was written
                                        #         over it. C1's other three assertions are
                                        #         all negative, so a fixture that landed
                                        #         where kae does not read satisfies them —
                                        #         which is the defect this block was
                                        #         corrected for. This is the positive half
# C2 — blank tokens, future deadline: kae will not judge it, so it says so.
# $LATER, not $NEW: at an expiresAt equal to the snapshot's the timestamp comparison
# refuses the harvest on its own, so a healthy copy and a blank one behave identically
# and this case would pass whether or not the guard works (measured).
printf '{"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":%s,"refreshTokenExpiresAt":1830384000000}}' $LATER \
  > "$(cstore)/.credentials.json"
/tmp/kae pin main
#   assert: NO `harvested` line
#   assert: one stderr warning — `kae cannot read or date the copy already there`, that
#           `a payload kae cannot judge may still be a login`, and the remedy
#           `cd <this dir> && kae relogin claude`. The weak claim takes the weak
#           consequence: kae replaces the copy but does not call it dead
snap main | grep MAIN-OLD               # assert: unchanged
grep MAIN-OLD "$(cstore)/.credentials.json"   # assert: replaced, which is what separates
                                        #         this arm from the attribution refusal in
                                        #         A3 — that one is *kept*
                                        #         (docs/ROADMAP.md owns why they differ)

# --- D. the mode toggle rebuilds the bound stores; the newer copy must survive it ---
# reset_main, not a bare re-capture: the account's store survives a re-capture, and the
# first pin below would harvest the leftover copy back into the fresh snapshot, leaving
# this case's own $NEW fixture no newer than anything (measured 2026-08-08).
reset_main
T="$HOME/toggle"; mkdir -p "$T"; cd "$T"
/tmp/kae pin main                       # shared
ident main > "$(store)/.claude.json"    # the tool ran here (see A)
cred MAIN-NEW $NEW > "$(cstore)/.credentials.json"
/tmp/kae pin -i main                    # isolated: a different *config* store, rebuilt
                                        # from the snapshot
#   assert: one `harvested` line
grep MAIN-NEW "$(cstore)/.credentials.json"  # assert: the copy that can refresh is still
                                        #         the one this directory is bound to.
                                        #         Without the harvest the materializer
                                        #         writes the snapshot's MAIN-OLD over it,
                                        #         with every offline check green
snap main | grep MAIN-NEW               # assert: and it reached the snapshot
# Measured 2026-08-08: post-split this harvests the SAME store as A
# (credstore/claude/main) — read the path in the stderr line if you doubt it. A mode
# toggle moves the config store, not the credential, so the case where the credential's
# location actually moves is E and only E. Kept as its own case anyway, because what it
# asserts is that rebuilding the bound store set does not lose the copy.

# --- E. shared re-bind to another account: the copy belongs to the OLD one ---
reset_main                              # see D for why a bare re-capture is not a reset
R="$HOME/rebind"; mkdir -p "$R"; cd "$R"
/tmp/kae pin main
ident main > "$(store)/.claude.json"    # the tool ran here (see A)
cred MAIN-NEW $NEW > "$(cstore)/.credentials.json"
/tmp/kae pin claude side
#   assert: `harvested … into snapshot claude/main` — main, not side — and that it is the
#           ONLY stderr line. This is the one case here where the credential's location
#           moves (credstore/claude/main → …/side), which is why the pass has to read the
#           store being left before the bind writes the new one. Nothing reports a foreign
#           copy: the store now being written is side's own. Measured 2026-08-08 — this
#           said "the write path then notes that the copy in the store is not side's",
#           which was true while both accounts' copies shared one per-directory store
snap main | grep MAIN-NEW               # assert: main's login survived the re-bind
snap side | grep -c MAIN-NEW            # assert: 0 — it was not filed under side
grep SIDE-OLD "$(cstore)/.credentials.json"  # assert: the account store now runs side

# --- F. doctor reports a store that names an account other than the binding ---
# Independent of A–E; it needs only a captured account with a recorded identity.
reset_main
G="$HOME/drift"; mkdir -p "$G"; cd "$G"
/tmp/kae pin main
ident main > "$(store)/.claude.json"    # the tool ran here (see A)
grep u-main "$(store)/.claude.json"     # assert: the cache doctor reads is where it expects
                                        #         it and names main.
                                        #         Positive first, and it is what makes the
                                        #         two `grep -c` lines below mean anything:
                                        #         with no cache there the check is silent
                                        #         for *missing evidence*, so a broken
                                        #         comparison would read as a pass
/tmp/kae doctor --json | grep -c identity_drift   # assert: 0 — a store that agrees with
                                        #         its binding says nothing
ident other > "$(store)/.claude.json"   # a login inside the directory as another account
/tmp/kae doctor --json | grep identity_drift      # assert: one warn, naming this
                                        #         directory and claude/main, with both
                                        #         causes and the re-bind remedy
/tmp/kae doctor --json | grep -c 'u-other\|other@example.com'   # assert: 0 — an identity
                                        #         is PII and never reaches the report
rm "$(store)/.claude.json"
/tmp/kae doctor --json | grep -c identity_drift   # assert: 0 — no cache in the store is
                                        #         missing evidence, not drift (the
                                        #         ordinary state until the tool runs
                                        #         there, and permanent for a directory
                                        #         bound before v0.16.0)

# --- the two restore paths. One child script for G and H, differing only in the uuid
#     it leaves behind, because that difference is the whole attribution question ---
cat > "$HOME/child.sh" <<EOF
#!/bin/sh
# \$1 access token, \$2 expiresAt, \$3 the account the identity cache ends up naming
printf '{"claudeAiOauth":{"accessToken":"%s","refreshToken":"rt-%s","expiresAt":%s,"refreshTokenExpiresAt":1830384000000}}' "\$1" "\$1" "\$2" > "$HOME/.claude/.credentials.json"
printf '{"oauthAccount":{"accountUuid":"u-%s","emailAddress":"%s@example.com"}}' "\$3" "\$3" > "$HOME/.claude.json"
EOF
chmod +x "$HOME/child.sh"
cd "$HOME"

# --- G. run -s on the account that is ALREADY active keeps the child's refresh ---
cred MAIN-OLD $OLD > "$HOME/.claude/.credentials.json"; ident main > "$HOME/.claude.json"
/tmp/kae add --no-login --identity you@example.com claude main
/tmp/kae run -s claude main -- "$HOME/child.sh" MAIN-NEW $NEW main
#   assert: stderr carries `main was already the active account` and does NOT carry
#           `previous auth state restored` — nothing was restored, so it is not claimed
grep MAIN-NEW "$HOME/.claude/.credentials.json"   # assert: the real home still runs the
                                        #         copy that can refresh. Before this it
                                        #         held MAIN-OLD — a logged-out session
                                        #         reported as a successful restore
snap main | grep MAIN-NEW                # assert: the post-child recapture has it too,
                                        #         so `kae use claude main` applies it

# --- H. the same run, except the child logged in as somebody else ---
cred MAIN-OLD $OLD > "$HOME/.claude/.credentials.json"; ident main > "$HOME/.claude.json"
/tmp/kae add --no-login --identity you@example.com claude main
/tmp/kae run -s claude main -- "$HOME/child.sh" FOREIGN $LATER other
#   assert: stderr carries `previous auth state restored` and NOT `already the active
#           account` — a later deadline is not evidence of whose login it is
grep MAIN-OLD "$HOME/.claude/.credentials.json"   # assert: restored. Keeping FOREIGN
                                        #         would leave the real home running one
                                        #         account while kae records another
#   assert: stderr also carries `probably logged in again outside kae` and the
#           `kae add --no-login claude <account>` hint — the recapture refused, and it
#           says how to keep that login instead of discarding it silently
snap main | grep -c FOREIGN               # assert: 0. run -s's own recapture now applies
                                        #         the same two guards the switch-away one
                                        #         does, so the stranger's credential is
                                        #         not filed under the target's name. This
                                        #         line asserted `FOREIGN` **present**
                                        #         until 2026-08-07, when the defect it
                                        #         described was fixed; the shape it
                                        #         guarded against is now three unit tests
                                        #         (TestRunSharedRefusesToRecaptureA…) and
                                        #         one measured mutation each.
snap main | grep MAIN-OLD                # assert: the snapshot still holds its own copy —
                                        #         the positive half, without which a
                                        #         `grep -c` of 0 also passes for a snap()
                                        #         that reads the wrong path
/tmp/kae ls --json | grep you@example.com # assert: the recorded login identity survived.
                                        #         persistSnapshot builds it from
                                        #         plan.Identity, which the run paths never
                                        #         set, so every run -s used to blank it —
                                        #         a third defect on the same line that
                                        #         docs/ROADMAP.md did not name

# --- I. rollback says the credential it restores is already dead, and restores it ---
cred MAIN-OLD $OLD > "$HOME/.claude/.credentials.json"; ident main > "$HOME/.claude.json"
/tmp/kae add --no-login --identity you@example.com claude main
/tmp/kae use claude main                 # a backup whose active_before is main
cred MAIN-NEW $NEW > "$HOME/.claude/.credentials.json"   # the tool refreshed in place,
                                        #         with no kae command running
/tmp/kae rollback; echo "exit=$?"
#   assert: exit=0 — a warning never moves the exit code. Take it from `kae` itself:
#           `echo $?` after one of the `grep` assertions below reports *grep's* status
#           and can never fail, which is how this line was wrong when first written
#   assert: stderr carries `older claude credential for claude/main than the one in the
#           live store`, and names `kae rollback --to <the pre-rollback id>` — the live
#           copy is the one being overwritten, so that backup is the only place left
#           holding it. NOT `kae use claude main`: the snapshot holds the older copy.
#           The id must be the one this rollback just created, not the one it restored;
#           compare it against `/tmp/kae backup list`
grep MAIN-OLD "$HOME/.claude/.credentials.json"   # assert: the rollback still happened.
                                        #         Going back is what was asked for; the
                                        #         warning is what kae adds

# --- J. the switch-away recapture declines a live copy the snapshot supersedes ---
# This is what stops I's rolled-back copy from being laundered over a newer snapshot on
# the next switch. Both copies are usable here, so the usability refusal cannot see it.
cred MAIN-NEW $NEW > "$HOME/.claude/.credentials.json"; ident main > "$HOME/.claude.json"
/tmp/kae add --no-login --identity you@example.com claude main
cred MAIN-OLD $OLD > "$HOME/.claude/.credentials.json"   # what a rollback leaves behind
/tmp/kae use claude side
#   assert: stderr carries `snapshot claude/main holds a later claude credential than the
#           live store` and `snapshot left unchanged`
snap main | grep MAIN-NEW                # assert: the only copy that can refresh survived
snap main | grep -c MAIN-OLD             # assert: 0 — paired with the positive line above
```

Several lines here exit non-zero **on purpose** (`grep -c` printing `0` is the
assertion), so paste the block as-is rather than under `set -e`, or add `|| true` to
those.

**A–J PASSED 2026-08-08 on the release tree** (darwin 24.6.0, file driver, file
backend, temp HOME, `/tmp/kae` = v0.17.0), each assertion checked at its own point rather
than from the end state. A–E and F were first run 2026-08-04 and G–J from 2026-08-05; that
record is superseded because the tree moved under it twice — see the entry in § Release
Acceptance Log for what the re-run found, which was a defect in this block rather than in
kae. D and E are the two cases a *chokepoint-only* harvest got wrong, so they are the ones
worth re-running after any change to `kae pin`'s ordering: both depend on the pin-level
pass running **before** the stores are materialized, and E additionally on the replaced
fragment being read before it is rewritten.
G–J were **discriminated against a control** when first written: the same G block run against a
binary with the skip forced off leaves `MAIN-OLD` live and prints "previous auth state
restored", so G is not a tautology. Note the 2026-08-06 measurement of **H does not carry
forward**: H asserted the *defect* that `run -s`'s recapture applied no guards, so fixing
that inverted the assertion, and the earlier 14/14 was measured against a binary the block
as written now refuses. The 2026-08-07 re-run is of the inverted block, and it added the
recorded-identity assertion the older one had no reason to make. Three defects in the block
itself were found by running it — an `echo $?` that reported *grep's* status and so could
never fail; case H going green over a snapshot the run poisons; and, on 2026-08-07, no
positive half beside `grep -c FOREIGN`, which a broken `snap()` would also satisfy. A fourth
came out of the 2026-08-08 re-run and was the worst of them, because it made the block
silently stop testing its own subject: every credential fixture was still going to the
config dir after the credential moved to the account store. Four defects in one block, all
four found by running it and none by reading it, is the argument § Smoke Checks makes for
re-running every block on the final tree.

**What G–J does not cover, stated because a green run reads as if it did.** The block
exports `KAE_CLAUDE_DRIVER=file`, so claude's credential is a JSON-pointer file and its
identity a pointer in `~/.claude.json` — **neither touches `internal/keychain`**. So it
exercises the per-command keychain read cache *not at all*: no read is cached, no
`invalidate` runs, and a stale-read bug would pass all fourteen assertions. The cache the
restore paths now open (`run -s` after its child, `kae rollback` for its mutation) is
covered by unit tests over the darwin sim — including a sibling-account invalidation test
— and, on a real machine, only by running `kae run -s` and `kae rollback` on darwin
against the real keychain, which this branch has not done.

G and H are the pair worth re-running after any change to `run -s`'s
post-child sequence, and J after any change to the switch-away recapture. One remedy is
**not** covered here and is therefore not claimed: the rollback warning's other branch,
where the newer copy is in the account snapshot and the remedy is `kae use <tool>
<account>`, needs the snapshot moved ahead of the backup by something these fixtures do
not do on the real binary; it is pinned by unit tests instead
(`TestRollbackWarnsWhenTheSnapshotHoldsALaterCopy`, and the two "prefers" tests that
pin which of the two candidates wins).

Five of these assertions were **corrected after first passing**, which is the argument
for reading a block adversarially rather than trusting a green run: B reused A's
`expiresAt` and so never reached the attribution guard; C did the same and so proved
nothing about the tombstone guard (a healthy copy behaves identically at an equal
deadline); and B's only snapshot check was a `grep -c` for absence, which prints `0`
— a pass — when `snap()` itself is broken. Each is now dated ahead of the snapshot, or
paired with a positive assertion. The two from 2026-08-05 are a different shape and
worth knowing separately: I's exit-code line read `$?` from the **preceding grep**
rather than from `kae`, so it reported the assertion above it and could not fail; and
H asserted only the live store, which let it pass over a *snapshot* the same command
had filed another account's credential into. A block asserts what it names, and the
thing it does not name is where the defect sits.

## v0.17.0 surface — `kae relogin` and `credential_superseded`

The other half of the same fact: when two copies of one account's credential exist
and one refreshes, the other cannot any more — so `kae doctor` names the directory
that lost, and `kae relogin` is the one command that fixes it. Same isolation as the
harvest block above (temp HOME, **file** driver, file backend, no real keychain and
no real login). It **repeats that block's preamble** rather than continuing from it:
the two sections seed the same fixtures, and this one has to be runnable on its own —
`scripts/smoke-run.sh` gives every section a fresh sandbox, so "continues from the block
above, in the same shell" meant this section could not be run by the tool built to run
it. Read verbatim it failed at the first `kae pin` with `profile "main" is not defined`
and then called `store`, a function nothing had defined.

The duplication is the point, not an oversight: what this block needs is the preamble
**only**, never the harvest cases. Their bindings of the same account make K's "newest
copy" a tie, and the store that happens to win the walk order can be one kae cannot
attribute, which silences the whole group — K measured 0 findings instead of 1 that way
on 2026-08-08. (A tied store that does *not* win changes nothing; it is already skipped
for not being superseded.) `docs/ROADMAP.md` § credential_superseded owns the mechanism
and the control that pinned it. Seeding here is what makes "preamble only" structural
instead of an instruction a reader has to obey.

**Two copies of one account's credential no longer arise from two ordinary bindings**,
which changes how this state has to be built. The per-account store that shipped in this
same release gives every directory bound to one account the *same* credential, so the
two-copies shape now needs two different credential *locations* — and today that means
one directory still in the pre-v0.17.0 shape, which is exactly what `credential_unsplit`
reports and a re-pin repairs. The check's other reachable shape is a bound store against
the **account snapshot** (`newestAt` reads `snapshot <tool>/<account>` and the remedy
becomes `kae pin <tool> <account>`, no login needed), which L exercises on the way out.
Both are live; the one the check was designed for is now a transitional state.

One thing this block does that no other does: it puts a **fake `claude` on `PATH`**.
That is the only way to prove the real binary exports the isolation variable, which
is `kae relogin`'s whole reason to exist — a fake that wrote to a path the script
chose would pass just as happily if kae exported nothing. The fake refuses (exit 8)
when `CLAUDE_CONFIG_DIR` is unset rather than writing somewhere, so a regression
fails loudly instead of quietly writing to the temp `$HOME`.

```bash
# The harvest block's preamble, repeated verbatim so this section stands alone. It must
# be the preamble and nothing else — see above for what running it after A–J does to K.
go build -o /tmp/kae .
. scripts/smoke-env.sh
export KAE_CLAUDE_DRIVER=file
OLD=1767225600000; NEW=1798761600000; LATER=1814400000000   # expiresAt: 2026-01-01, 2027-01-01, 2027-07-01
cred() { printf '{"claudeAiOauth":{"accessToken":"%s","refreshToken":"rt-%s","expiresAt":%s,"refreshTokenExpiresAt":1830384000000}}' "$1" "$1" "$2"; }
ident() { printf '{"oauthAccount":{"accountUuid":"u-%s","emailAddress":"%s@example.com"}}' "$1" "$1"; }
snap() { base64 -d < "$XDG_DATA_HOME/kagikae/secrets/claude/$1/claude_ai_oauth.secret"; }
store()  { sed -n 's/^CLAUDE_CONFIG_DIR *= *"\(.*\)"/\1/p'                .config/mise/conf.d/kagikae.toml; }
cstore() { sed -n 's/^CLAUDE_SECURESTORAGE_CONFIG_DIR *= *"\(.*\)"/\1/p'   .config/mise/conf.d/kagikae.toml; }
mkdir -p "$XDG_CONFIG_HOME/kagikae" "$HOME/.claude"
printf 'version = 1\n[security]\nsecret_backend = "file"\n' > "$XDG_CONFIG_HOME/kagikae/config.toml"
cred MAIN-OLD $OLD > "$HOME/.claude/.credentials.json"; ident main > "$HOME/.claude.json"
/tmp/kae add --no-login --identity you@example.com claude main
cred SIDE-OLD $OLD > "$HOME/.claude/.credentials.json"; ident side > "$HOME/.claude.json"
/tmp/kae add --no-login --identity other@example.com claude side
cred SOLO-OLD $OLD > "$HOME/.claude/.credentials.json"; ident solo > "$HOME/.claude.json"
/tmp/kae add --no-login --identity solo@example.com claude solo
/tmp/kae profile set main claude main
/tmp/kae profile set side claude side
/tmp/kae profile set solo claude solo

# --- K. doctor names the overtaken directory, and only that one ---
A=$(cd "$(mktemp -d)" && pwd -P); B=$(cd "$(mktemp -d)" && pwd -P)
cd "$A" && /tmp/kae pin main && SA=$(cstore)
cd "$B" && /tmp/kae pin main
# B in the pre-v0.17.0 shape, by hand: strip its credential entry, so B's credential
# resolves to its own config dir instead of the account store A shares. Two ordinary
# bindings of one account cannot hold two different copies any more (see above).
# Not `sed -i`: its in-place flag takes an argument on BSD and not on GNU, and this
# block is meant to be pasted on either.
BF="$B/.config/mise/conf.d/kagikae.toml"
grep -v '^CLAUDE_SECURESTORAGE_CONFIG_DIR' "$BF" > "$BF.tmp" && mv "$BF.tmp" "$BF"
SB=$(store)   # B's credential is its config dir again — that is the point of the edit
cred A-NEW $LATER > "$SA/.credentials.json"   # the tool refreshed A's account store
cred B-OLD $NEW   > "$SB/.credentials.json"   # B has been sitting since before that
test "$(/tmp/kae doctor --json | grep -c credential_superseded)" -eq 1
                                        # assert: exactly one directory lost. A count
                                        #   alone would pass as a quiet 0, so the lines
                                        #   below name which one
/tmp/kae doctor | grep "is older than" > "$HOME/K.txt"
grep -q "bound to $B is older than another copy of claude/main (the store bound to $A)" "$HOME/K.txt"
                                        # assert: B lost, and A is named as where the
                                        #   newer copy is
grep -q "cd $B && kae relogin claude" "$HOME/K.txt"   # assert: and the remedy is a login
test "$(/tmp/kae doctor --json | grep -c credential_stale)" -eq 0
                                        # assert: 0 — a superseded copy is NOT stale.
                                        #   Both deadlines here are years out; the whole
                                        #   point is that the field the stale check reads
                                        #   does not move on invalidation. The grep above
                                        #   is the positive control: it fails if doctor
                                        #   reported nothing at all

# --- L. relogin logs in *into the bound store* and captures it back ---
FAKEBIN=$(mktemp -d)
# Written a line at a time rather than from a here-document, and that is not a style
# choice. `scripts/smoke-run.sh` evaluates one line at a time, so `cat > f <<'EOF'` is a
# here-doc with no body: the lines below it are then run as commands, and the `exit 9` in
# this fake's own argv check killed the run after 35 of 146 lines. The runner said "every
# line exited 0" while exiting 9, so the whole of L and M silently never ran (2026-08-09;
# the runner now reports that state, and its guard 14 covers it). The alternative is
# SMOKE_WHOLE_FILE=1, which costs the per-line verdict this section wants.
: > "$FAKEBIN/claude"
printf '%s\n' '#!/bin/sh' >> "$FAKEBIN/claude"
printf '%s\n' '[ "$1" = "/login" ] || { echo "unexpected argv: $*" >&2; exit 9; }' >> "$FAKEBIN/claude"
printf '%s\n' '[ -n "$CLAUDE_CONFIG_DIR" ] || { echo "no CLAUDE_CONFIG_DIR" >&2; exit 8; }' >> "$FAKEBIN/claude"
printf '%s\n' 'printf %s "{\"claudeAiOauth\":{\"accessToken\":\"B-RELOGGED\",\"refreshToken\":\"rt-B-RELOGGED\",\"expiresAt\":1830384000000,\"refreshTokenExpiresAt\":1861920000000}}" > "$CLAUDE_CONFIG_DIR/.credentials.json"' >> "$FAKEBIN/claude"
printf '%s\n' 'printf %s "{\"oauthAccount\":{\"accountUuid\":\"u-main\",\"emailAddress\":\"main@example.com\"}}" > "$CLAUDE_CONFIG_DIR/.claude.json"' >> "$FAKEBIN/claude"
# The fake writes where a **pre-split** directory keeps its credential, which is what B
# is. In a post-split directory the credential is at CLAUDE_SECURESTORAGE_CONFIG_DIR, and
# a fake that wrote to CLAUDE_CONFIG_DIR there would leave the store unchanged — kae would
# correctly report `auth_unchanged` (11) and the whole case would read as a kae regression.
chmod +x "$FAKEBIN/claude"; PATH="$FAKEBIN:$PATH"; export PATH
cd "$B"
/tmp/kae relogin > "$HOME/L.out" 2> "$HOME/L.err"; test $? -eq 0   # assert: exit 0
# kae abbreviates the store with `~` in this message, so the assertion has to as well —
# grepping the absolute $SB matches nothing and takes the count line below down with it.
SBREL="~${SB#$HOME}"
grep -qF "CLAUDE_CONFIG_DIR=$SBREL" "$HOME/L.err"
                                        # assert: kae says it is running the flow against
                                        #   this directory's own store, and names it. The
                                        #   fake exits 8 when the variable is unset, so a
                                        #   green run is itself the proof kae exported it
test "$(grep -c CLAUDE_SECURESTORAGE_CONFIG_DIR "$HOME/L.err")" -eq 0
                                        # assert: and ONLY that variable, since B is
                                        #   pre-split; a post-split directory names the
                                        #   credential store beside it. The grep above is
                                        #   this line's positive control
test "$(grep -c 'harvested the newer claude credential' "$HOME/L.err")" -eq 2
                                        # assert: twice, once on each side of the flow
test "$(grep -n 'harvested the newer claude credential' "$HOME/L.err" | head -1 | cut -d: -f1)" \
  -lt "$(grep -n 'complete the claude login flow' "$HOME/L.err" | head -1 | cut -d: -f1)"
                                        # assert: and the FIRST one precedes the flow.
                                        #   The pre-flow pass is the one with something to
                                        #   lose: the write that replaces the copy is the
                                        #   tool's, so nothing after the flow can see what
                                        #   it destroyed (docs/CLI.md § kae relogin
                                        #   Semantics). One occurrence is a regression even
                                        #   though the capture-back assertions below still
                                        #   pass, which is why this counts and orders
test "$(cat "$HOME/L.out")" = "Logged claude in for claude/main in this directory"
                                        # assert: the strong wording, which kae prints only
                                        #   when it observed all three of: the store
                                        #   changed, what is there now is not a tombstone,
                                        #   and the harvest confirmed the account. Any
                                        #   other case prints `Ran the claude login flow in
                                        #   this directory`, so this is the assertion that
                                        #   all three gates passed
grep -q B-RELOGGED "$SB/.credentials.json"   # assert: the login landed in B's own store
snap main | grep -q B-RELOGGED               # assert: and reached the account snapshot
/tmp/kae doctor | grep "is older than" > "$HOME/L2.txt"
grep -q "bound to $A is older than another copy of claude/main (snapshot claude/main)" "$HOME/L2.txt"
                                        # assert: the finding has *moved* — it names A now,
                                        #   because B is the copy that refreshed last. Where
                                        #   the newer copy is reads `snapshot claude/main`,
                                        #   not B's store: the harvest made the two equal
                                        #   and equal deadlines keep the earlier candidate
grep -q "cd $A && kae pin claude main" "$HOME/L2.txt"
test "$(grep -c relogin "$HOME/L2.txt")" -eq 0
                                        # assert: a re-bind from the newer snapshot needs
                                        #   no browser, so this remedy is NOT a relogin.
                                        #   Scoped to the one line, and the two greps above
                                        #   are its positive control

# --- M. outside a binding nothing is launched ---
C=$(mktemp -d); cd "$C"
/tmp/kae relogin > "$HOME/M.out" 2> "$HOME/M.err"; test $? -eq 7   # assert: 7 (not_found)
grep -q 'this directory is not pinned' "$HOME/M.err"
grep -q 'kae add --restore' "$HOME/M.err"   # assert: and it names the global remedy
grep -q SOLO-OLD "$HOME/.claude/.credentials.json"
                                        # assert: the temp real home still holds what the
                                        #   preamble left there last. The literal is the
                                        #   account the preamble captures **last**, not a
                                        #   fixed name — it writes one payload per account
                                        #   and the final one stays. This has been wrong
                                        #   twice for that reason, each time by one account:
                                        #   `MAIN-OLD` while there were two, then `SIDE-OLD`
                                        #   once `solo` was added (measured 2026-08-08, by
                                        #   running it). A wrong literal matches nothing
                                        #   *and* silently unguards the count below, which
                                        #   is the pair's whole purpose — so re-derive it
                                        #   from the preamble whenever an account is added
test "$(grep -c B-RELOGGED "$HOME/.claude/.credentials.json")" -eq 0
                                        # assert: 0 — paired with the positive line above,
                                        #   because a count for absence prints 0 when the
                                        #   file is missing too. Together they assert the
                                        #   flow was never launched: the fake `claude` is
                                        #   still first on PATH and writes into whatever
                                        #   CLAUDE_CONFIG_DIR names, so with none set it
                                        #   would land here. The exit code alone does not
                                        #   say that
```

`grep -c` printing `0` is an assertion on two lines here, so paste the block as-is
rather than under `set -e`.

**K–M PASSED 2026-08-08 on the release tree, 20/20** (darwin 24.6.0, file driver, file
backend, temp HOME, `/tmp/kae` = v0.17.0), run after the preamble alone, each assertion
checked at its own point. **Re-run the same day by an independent review, which found one
of those 20 asserting a literal that matched nothing**: M's positive control still named
`SIDE-OLD` after the preamble grew a third account, so the resting payload was
`SOLO-OLD` — the identical off-by-one-account this same block had already been corrected
for once, and it takes the `grep -c` beside it down with it. Corrected above, together
with L's harvest assertion, which the pre-flow pass in `kae relogin` turns from one line
into two. Both of those are why a passing record is not a substitute for re-running: the
first was a dead assertion inside a green run, and the second is output that moved
under a block nobody had edited. The 2026-08-06 record it replaces was measured before the
per-account credential store landed, which is what made K's two-copies construction
unbuildable as written; the block above now says how the state is reached and why. It had
also been **re-run verbatim after each review round's changes** — round 1 (the hedged
wording, the split remedy,
the store-must-exist refusal) round 2 (the three-gate success wording), round 3
(the emptied-store arm and the reworded warnings) and round 5 (the real-home
assertion below, which was prose until an execution-type review pointed out that
nothing checked it — and whose first written form asserted the wrong literal and so
matched nothing, caught by running it). A block whose expected output
moved and was not re-run is a block that documents the previous release. The `credential_stale = 0`
line in K is the one worth keeping in view: it is what distinguishes this check from
the one beside it, and if the two ever merge it is the assertion that says so.

**An open measurement this check's wording depends on.** `expiresAt` orders two
payloads; it does not say whether they are copies of **one** login or two independent
logins of the same account. Single-use rotation is measured for the first shape (one
credential, refreshed) and is what makes "only the newest can refresh" a fact there.
The second shape is *not* measured: does a fresh `claude /login` invalidate a chain
another directory is still holding? Until it is, `credential_superseded` states the
consequence conditionally ("if the two are copies of one login"), because this
release is what makes the second shape reachable — `kae relogin` in one worktree
mints a fresh chain there, after which the check reports the other worktree's
independent login as behind it. To measure: log in twice for one account into two
bound directories (two real `/login` runs, no bind in between), then use the tool in
the older one and see whether it refreshes. Requires real logins, so it belongs in
the real-machine gate.

**What this block does not cover.** It never touches `internal/keychain` (the file
driver again), so `kae relogin` against a real per-directory keychain **item** is
unrun — the store read on both sides of the login and the harvest's write would each
be a `security` call there. It also drives a *fake* login: the real `claude /login` is
interactive and its exact post-login file writes are what the assertions above assume.
Both belong in the real-machine gate rather than here.

## v0.17.0 surface — `kae env` and `kae backup` completion

Both subcommand groups gained a case in all three generated scripts, so this is a
**structural** script change: an already-registered completion file has to be
rewritten before the smoke means anything ([CLI.md](CLI.md) § Keeping completion
current owns how).

Unit-covered, in `internal/cmd`:

- `TestCompletionPositionalRouting` — each branch's slots and array indices,
  asserted **inside that branch and in order** rather than anywhere in the script,
  per shell. Both are load-bearing: the constructs recur across branches, and one
  branch's two arms hold the same ones, so a whole-script or unordered check passes
  on a branch that was never written or whose arms were swapped.
- `TestEveryPositionalCommandCompletes` — every command in `completionCommands` is
  classified as taking a positional or not, and each one that does has a branch
  that emits candidates in bash, zsh, and fish.
- `TestSubcommandCompletionParity` — each group's sub-verb run, also branch-scoped.
- `TestCompletionScriptsAreSyntacticallyValid` — the generated scripts are parsed
  by the shell they target (`bash --noprofile --norc -n`, `zsh -f -n`, `fish
  --no-execute`; the rc files are skipped so a failure can only be the script).
  Nothing else reaches them: they are Go string constants, so the shellcheck task,
  which walks `scripts/*.sh`, never sees them. A shell that is not installed is
  skipped and said so in the test log; bash is required, or the check would pass
  having checked nothing. Each shell must first **reject** an unterminated `if`,
  because two ways of checking nothing while passing are reachable here — every
  shell accepts an empty file, which is what an unknown script name yields, and a
  flag that does not parse (`zsh --version`) accepts a broken one.

### v0.17.0 completion real-machine smoke (required before release)

bash and zsh (fish is best-effort, not gated — v0.8.6). In a fresh shell with
completion registered **and refreshed**:

- [x] `kae env <TAB>` offers `set` / `unset` / `list`; `kae backup <TAB>` offers
      `list`.
- [x] `kae env set <TAB>` offers tools; `kae env set claude <TAB>` offers claude's
      accounts.
- [x] `kae env list <TAB>` offers **nothing** — `env list` takes no arguments, and
      the branch is gated on the sub-verb to avoid suggesting a word the command
      rejects.

Record the result in the Release Acceptance Log below.

## v0.17.0 surface — the per-account credential store

The credential moved out of the bound directory's store and into
`credstore/<tool>/<account>/`, which the binding names through a second env entry
([ADAPTERS.md](ADAPTERS.md) § Per-account credential store). The upstream fact it
rests on — that one shared store survives two simultaneous refreshes while two
copies do not — is measured in the table below, with its negative control; it is
the row this whole surface stands on, so re-verify it on a claude upgrade rather
than trusting this section.

Unit-covered, in `internal/cmd` (`credstore_test.go` unless noted):

- `TestTwoDirectoriesOfOneAccountShareOneCredential` — the property itself, read
  from both fragments: one credential entry, two different config dirs.
- `TestUnpinPurgeKeepsACredentialASiblingStillBinds`,
  `…AGloballyIsolatedHomeUses`, and `TestUnpinPurgeRemovesACredentialNothingElseBinds`
  — the refcount in all three directions. The third is the one that keeps the guard
  from being "never delete", which every other assertion here would accept.
- `TestALeftoverStoreIsNotGivenAnotherAccountsCredentialDir` and
  `TestAPreSplitStoreKeepsItsOwnCredentialDir` — attribution of a store's credential
  location, which is where a mislabelled harvest would come from.
- `TestRebindMovesTheCredentialEntryInSharedMode` and
  `…AddsTheCredentialEntryToAPreSplitFragment` — the entry follows the **account**,
  including in shared mode, where the config entry deliberately does not move.
- `TestDoctorNamesADirectoryBoundBeforeTheCredentialSplit` — the migration prompt,
  with its own negative case (a directory bound by this kae reports nothing).
- `TestReloginExportsTheCredentialVariable` — the login flow gets both halves.
- `TestGlobalScopeHidesBothHalvesOfABinding` — including that a user-set value
  survives, since kae masks what it wrote and not the variable.
- `TestClaudeSecureStorageConfigDirSplitsTheStores` (`internal/adapter`) — with both
  variables set, the item is namespaced by the credential dir and **not** by the
  config dir, and the identity file stays with the config dir. The negative half is
  the point: a single-variable model passes every "the item is namespaced" check
  while writing the item claude does not read.

### v0.17.0 per-account credential real-machine smoke (required before release)

Run with `. scripts/smoke-env.sh` sourced, in a temp HOME, and — on macOS — with
`KAE_CLAUDE_DRIVER=file` **and** `[security] secret_backend = "file"`, exactly as
§ Smoke Checks requires of any claude round-trip: without both, a capture or a bind here
reads and writes the real login keychain and leaves per-directory items behind. The
recorded result below is a file-driver observation, so the overrides are not optional
decoration — they are what it was measured under. Two bound directories on one captured
account:

- [x] `kae pin <profile>` in each of two directories; each fragment carries
      `CLAUDE_SECURESTORAGE_CONFIG_DIR` pointing at the **same**
      `credstore/claude/<account>` and `CLAUDE_CONFIG_DIR` pointing at **different**
      stores.
- [x] `kae doctor --json` reports no `credential_unsplit` for either.
- [x] Remove the credential line from one fragment by hand (this is the pre-v0.17.0
      shape); `kae doctor --json` now reports `credential_unsplit` naming that
      directory, and its remedy is `cd <dir> && kae pin`.
- [x] Re-run `kae pin` there; the finding goes away and the entry is back.
- [x] `kae unpin --purge` in one of the two: kae keeps the credential and says how
      many bindings still use it. Repeat in the last one: it is removed, and the
      message names it as **the account's** credential and points at
      `credstore/claude/<account>` — not as a "per-directory" one at the config-dir
      store, which is what it said until 2026-08-07. Read the message, not just the
      state: the state assertion passed the whole time the message was wrong, and
      both unit assertions on that string are negative ones that cannot see it.
- [x] `kae pin claude <other-account>` in a shared-mode directory rewrites the
      credential entry to the other account's store while leaving `CLAUDE_CONFIG_DIR`
      unchanged.

Record the result in the Release Acceptance Log below.

## Upstream Behaviour Assumptions

kae drives undocumented upstream state, so it depends on two different kinds of
fact: the **layout** (where the credential lives, what shape it has) and the
**behaviour** (what the tool does with it at runtime). Every adapter guards the
layout — an unrecognized shape refuses with exit 10, and `kae doctor <tool>`
reports what was detected. **Nothing guards the behaviour.** An upstream release
that keeps the layout byte-identical and changes only what the tool *does* passes
every guard kae has.

That is not hypothetical. `/oauthAccount` was originally left alone because Claude
Code was measured self-healing it. The measurement was right but incomplete: the
self-heal is gated behind a 24h `profileFetchedAt` TTL that every token refresh
renews without rewriting `emailAddress`, so for a credential in daily use it never
fires. The layout never changed, no check fired, and switched sessions kept
displaying the previous account until a user noticed by hand. The assumption was
real and load-bearing, and it **was not written down as a verifiable item** —
which is the gap this table closes.

Note the shape of that mistake, because the rows below are worded to avoid
repeating it: the assumption was recorded as an absolute ("claude self-heals it")
when the fact was conditional ("claude self-heals it when its cache is over 24h
old"). Write the condition down, or the next session will diagnose the condition
firing as a new upstream change.

Two offline `doctor` checks watch the table between releases:

- `identity_drift` — a tool's identity-only artifact no longer matches what kae
  applied: for the active account, and (since v0.17.0) for a bound directory's own
  store against the account its fragment binds. Catches an assumption that has
  *already* broken.
- `upstream_version` — the installed tool is a newer major/minor than the
  `VerifiedVersion()` its adapter declares. Flags the release where one *could*
  have broken, before it costs a wrong-account session. Patch bumps stay silent,
  and **cursor is exempt**: its date version would make every new build month look
  like a minor bump, so its adapter declares `""` and doctor never reports it
  (docs/ADAPTERS.md "Verified Upstream Versions"). Cursor's rows are therefore due
  on the release acceptance run, not on a doctor warning.

A warning from either means: work that tool's rows below, then update the
adapter's `VerifiedVersion()` and the version recorded here **in the same
commit**.

**For a tool whose source is public, the cheapest form of that work is a
tag-to-tag diff of the files the rows name**, which is offline, needs no
real-machine gate, and produces a stronger answer than re-reading one version:
identical files prove the assumption held rather than merely that it still looks
right. codex is the only such tool today. Done for 0.145.0 → 0.146.0 on
2026-08-04 with `gh api -H "Accept: application/vnd.github.raw"
"repos/openai/codex/contents/<path>?ref=rust-v<version>"` for each tag, then
`diff`: `login/src/auth/storage.rs`, `utils/home-dir/src/lib.rs` and
`core/src/config/auth_keyring.rs` were byte-identical, and the three declarations
that live in files with unrelated churn (`AuthCredentialsStoreMode`,
`cli_auth_credentials_store`, the `secret_auth_storage` feature spec) were
compared individually and matched. **Compare the declaration, not the file, when
the file also changed** — and check the hit count is non-zero, since a
grep-versus-grep comparison of two empty results reports "identical". Verifying an assumption always means launching a **fresh** tool process
— a still-running session and a byte-compared payload both prove nothing.

### claude (verified on 2.1.220)

| Assumption | How to verify |
|---|---|
| The credential alone authenticates: applying `/claudeAiOauth` (keychain payload or `.credentials.json`) is the whole login | `kae use claude <acct>`, then `claude -p "say AUTH-OK" </dev/null` in a **new** process returns a reply, not "Not logged in" |
| `/oauthAccount`'s self-heal is **TTL-gated**: claude refetches the profile and rewrites `emailAddress` only when the cached object is incomplete or its `profileFetchedAt` is over **24h** old, and a token refresh renews that timestamp without rewriting `emailAddress` / `accountUuid` | `kae use claude <other>` with a snapshot captured **within** 24h, launch claude, diff `~/.claude.json`: `oauthAccount.emailAddress` is the value kae wrote and does **not** revert. Then age it (`profileFetchedAt` older than 24h) and launch claude again: it now refetches and rewrites `emailAddress` + `profileFetchedAt` on its own. If the TTL ever stops applying, kae's identity switch becomes redundant (not harmful) — record that here rather than dropping the artifact silently |
| `claude /login` rewrites `accountUuid` / `emailAddress` / `organizationUuid` unconditionally (no TTL), and a token **refresh** rewrites none of them | Log in to another account with `claude /login`, diff `~/.claude.json`: those three keys change. Let a session run long enough to refresh the token and diff again: `profileFetchedAt` and the plan fields change, those three do not. kae's `IdentityKeys` (the keyed identity comparison) is exactly this set — if a refresh starts rewriting them, `identity_drift` will warn on correct switches again |
| `refreshTokenExpiresAt` is the **login's absolute expiry**, not a rolling window: `expiresAt` (the access token, ~8h) moves forward on every refresh, this one is set when `/login` runs and stays put. Claude Code warns `Your login expires in N days · run /login to renew` **three days** ahead of it (v2.1.203+; five days before v2.1.217) | Two independent confirmations, no login needed for either. **Upstream documents the warning and its threshold** ([Renew an expiring login](https://code.claude.com/docs/en/authentication)), and it states the warning is informational and that authentication keeps working "until the login actually expires". **The operator confirms (2026-07-31) that the warning appears only near the end, not at every startup**, and that their own re-login cadence is roughly a month — both of which are impossible if the field were a short rolling window. ⚠️ **A 2026-07-31 measurement recorded here as "≈2 days on 2.1.220, from a fresh login's credential" is retracted**: a two-day login would force a re-login every two days, which contradicts both the observed cadence and upstream's own warning behaviour. It was most likely read from a credential that was already old, since `relogin_by − captured_at` measures the time *left* at capture, not the lifetime. Re-measure only from a credential captured immediately after a completed `/login`, and record the login date alongside it. **Re-measured 2026-08-04 on 2.1.220 exactly that way** — a credential read straight after a completed `/login` carried `refreshTokenExpiresAt` **27 days** out (login 2026-08-04, deadline 2026-08-31) — which matches the observed monthly cadence and leaves `credential_expiring`'s seven-day lead time comfortable. The retracted two-day figure is dead; use 27 days as the measured lifetime and re-measure the same way if it ever matters again. kae depends on this in two places: the "recoverable without a re-login" predicate needs the field to exist at all (if it disappears kae falls back to presence alone and under-warns), and `credential_expiring`'s seven-day lead time needs the lifetime to be comfortably longer than seven days — a month satisfies that, two days would not |
| **`Revoked` cannot tell an emptied token from a missing one**, and kae's reading of "this payload carries no usable token" is `accessToken` and `refreshToken` both being absent-or-empty. The measured tombstone (row below) has them **present and empty**, so the two agree today — but a payload that simply does not carry those key *names*, which is what an upstream field rename looks like, reads as revoked while being a working login | Decide it in one read: a renamed-token payload still has a plausible future `expiresAt` and a token value under some other key, a tombstone has neither. kae deliberately keeps the wider reading rather than requiring the keys to exist, because the failure directions are not symmetric: over-reading revoked makes the harvest and both restore paths *decline* to touch the copy (safe), while under-reading it would let a logged-out payload be adopted as a live login. What the wider reading may **not** do is license a claim about the tool — hence the rollback warning says "carries no usable token", never "cannot log in" (docs/CLI.md § `kae rollback --json`). Flagged by review 2026-08-05; the same shape as the `expiresAt`-unit row above |
| A refresh that fails with `invalid_grant` makes claude **tombstone** the credential in place: `accessToken: ""`, `refreshToken: ""`, `expiresAt: 0` — while **`refreshTokenExpiresAt` is retained**. So the tombstone is what kae can see, and it appears only *after* the tool has tried and failed: before that attempt an already-invalidated credential carries an untouched deadline and reads as healthy | Run-confirmed 2026-08-04 on 2.1.220 by the rotation procedure in the row below, whose store C held a genuinely rejected refresh token: afterwards its payload had an empty refresh token (the sha256 of the empty string is the giveaway) and `expiresAt: 0`, with `refreshTokenExpiresAt` unchanged. kae reads the blanked form as invalid, not as "no expiry recorded"; if upstream instead deletes the item, the logged-out guards cover it. The retained deadline is the load-bearing half — it is why `credential_stale` cannot report an invalidated copy until something has already failed |
| A refresh **rotates** the refresh token, and the superseded one is **single-use**: the server rejects it. `expiresAt` moves forward on every refresh, so it orders two copies by which refreshed last; `refreshTokenExpiresAt` does not move (row above) and therefore cannot. **The consequence is the load-bearing part: two stores holding copies of one account's credential invalidate each other.** The first to refresh keeps working; the other runs on its still-valid access token for up to its ~8h remaining life and then fails mid-session with `Failed to authenticate: OAuth session expired and could not be refreshed`. Until that attempt the doomed copy is **indistinguishable from a healthy one offline** — its deadline is untouched and the tombstone (row above) is only written afterwards | Measured 2026-08-04 on 2.1.220 **without touching any real account**: `claude /login` into a throwaway `CLAUDE_CONFIG_DIR` (call it A), then two *file* copies of A's credential with `expiresAt` moved into the past, then `claude -p "…" --model haiku` against each with `CLAUDE_CONFIG_DIR` pointed at it. A store with no keychain item yet reads the file, so seeding needs **no keychain write** — which is what keeps this procedure safe to re-run. B refreshed and its refresh token changed value; C, still holding A's original token, was rejected with that message. Compare **sha256 prefixes only**, never token values. Delete both per-directory items afterwards (`security delete-generic-password -a "$USER" -s "Claude Code-credentials-<sha8>"`) or they linger where nothing can enumerate them. Do this with a throwaway login, never an account in use: a server doing reuse detection could revoke the whole chain. Every kae mechanism that keeps a credential **copy** rests on this row — the per-directory materializers, the global isolated home, backups, and `kae use` applying a capture-time snapshot |
| **One shared credential store survives two simultaneous refreshes, including from two *different* config dirs.** Two processes whose `CLAUDE_CONFIG_DIR` differ but whose `CLAUDE_SECURESTORAGE_CONFIG_DIR` is the same both authenticate, and the shared item rotates once — the losing process does **not** tombstone it. Same for two processes in one identical config dir. This is the premise any "one credential copy per account" design rests on, and it is the *opposite* of the copies case in the row above, so the two must never be conflated | Measured 2026-08-04 on 2.1.220, throwaway login, four consecutive rounds. Seed the shared store, move its `expiresAt` into the past, and start both `claude -p … --model haiku` probes from one thread pool; read the shared payload's refresh-token sha256 prefix before and after. **Run the negative control in the same session or the result is worthless**: give each process its own store holding a *copy* of the same credential and it comes back 1/2, the loser reporting `OAuth session expired and could not be refreshed` and tombstoning its own store. Shared → 2/2 and no tombstone, copies → 1/2 and a tombstone, is the discrimination that makes this row mean something. ⚠️ Two confounds bit this measurement: seeding a store as a **file** makes the first refresh promote it to a keychain item and delete the file, so the *other* process reports `Not logged in · Please run /login` for a reason that has nothing to do with rotation — always re-run once the credential is keychain-resident, and read the message, since "not logged in" (nothing found) and "session expired" (found and refused) are different findings. Nothing here observes that the two refreshes truly overlapped, so this shows a shared store *can* take concurrent use, not that contention was exercised in every round |
| `expiresAt` is an **absolute** epoch (milliseconds since 1970), not a duration. This is the unit every ordering of two copies rests on: `supersedes` compares the two values and nothing else, so a change to relative seconds under the same field name — the `expires_in` / `expires_at` confusion common in OAuth payloads — would leave every copy carrying a small positive number that still parses, still passes `orderable`, and orders **arbitrarily**. kae's only defense against a meaningless deadline is the zero value (`EpochToTime` maps `n <= 0` to the zero time and `orderable` rejects that), which catches a value that is absent or non-numeric and cannot catch one that is merely in the wrong unit | Read one live payload: `expiresAt` is ~13 digits and decodes to a timestamp a few hours ahead (`python3 -c 'import sys,datetime;print(datetime.datetime.fromtimestamp(int(sys.argv[1])/1000))' <value>`), while `refreshTokenExpiresAt` decodes weeks ahead. A 10-digit value would be seconds and a 4-digit one a duration; either means every `supersedes` comparison in kae is meaningless and the harvest and both restore paths must be re-derived, not patched. Flagged by review 2026-08-05 as the next instance of the defect that shipped in this release — the assumption was load-bearing and unwritten |
| The keychain payload must round-trip **verbatim**; a re-serialized payload makes Claude Code reject the credential | Capture → apply → fresh-process auth check on macOS with the real keychain driver. A byte-compare of the stored payload does not cover it: an equivalent-but-re-encoded payload is exactly this failure |
| `~/.claude.json` is mixed state whose other keys must survive a pointer patch | `git`-diff `~/.claude.json` across a switch: only `/oauthAccount` changes; `projects`, `mcpServers`, onboarding and cache keys stay byte-identical |
| **Where the credential resolves to** is a rule, not a constant. The keychain service is `Claude Code` + the build's OAuth suffix + `-credentials` + a per-config-dir suffix. That last suffix is empty only while `CLAUDE_CONFIG_DIR` is unset or empty; otherwise it is `-<first 8 hex of sha256(value)>` over the env string **NFC-normalized** — no `resolve`, no cleaning, so a trailing `/` hashes to a different item, and a decomposed non-ASCII component hashes as its composed form. Reads try keychain first and fall back to `<configDir>/.credentials.json`; a write goes to keychain and **deletes that file** when the item was previously absent. `CLAUDE_SECURESTORAGE_CONFIG_DIR`, when set, replaces `CLAUDE_CONFIG_DIR` as both the hash input and the file's directory — and set to the *empty string* it drops the suffix entirely, collapsing every config dir onto one shared item. **Both of those halves are run-confirmed** (2026-08-04, 2.1.220, shim below): with the two variables set to different directories the logged service carries `sha8` of the **securestorage** one, and with it empty the service is the unsuffixed name. Consequently the credential's location is separable from the config dir's — sessions and the identity file follow `CLAUDE_CONFIG_DIR` while the credential follows this variable, which is the mechanism a per-account credential with per-directory sessions would use. Also visible in the same log: claude probes a **second service family**, `Claude Code[-<sha8>]` without `-credentials`, tracking the same suffix rule. No item of that family exists on the measured machine, so nothing is known to write it — but kae models only the `-credentials` one, and "every service one login writes" is the enumeration this table exists to keep honest | Shim `security` rather than logging in. Put an executable ahead of `/usr/bin` on `PATH` that appends `"$*"` to a log and exits 44 (item-not-found), then run `env -i HOME=<temp> PATH=<shim>:/usr/bin:/bin USER="$USER" CLAUDE_CONFIG_DIR=<dir> claude -p hi </dev/null`. The logged `-s <service>` is the name claude actually resolves; check the suffix against `python3 -c 'import hashlib,sys,unicodedata;print(hashlib.sha256(unicodedata.normalize("NFC",sys.argv[1]).encode()).hexdigest()[:8])' <dir>` — the NFC step is not optional, and a `<dir>` with a decomposed non-ASCII component is the case that needs it. Re-run with a trailing slash, with `CLAUDE_SECURESTORAGE_CONFIG_DIR=`, and with it set to another dir to cover all four branches. **No login and no real-keychain access**, so this row is re-verifiable in seconds on any macOS machine — prefer it to the old seed-and-wait-for-refresh procedure. The delete-on-write half is the exception: it is read from the composed store's `update()` in the bundle (`if the keychain read returned absent, delete the plaintext file`) and reproducing it at runtime needs a live refresh token, so treat that half as **source-confirmed, not run-confirmed**. kae reproduces the rule in `claude.keychainService`, so every mechanism that sets `CLAUDE_CONFIG_DIR` writes into the item that config dir resolves to. Recording *storage resolution* as a verifiable rule is the lesson: kae had verified "the credential is at X" and never "how the tool decides where X is", and modelling the name as a constant is exactly what let a pinned directory run the previous account with every offline guard green |
| The **OAuth suffix** renames *both* stores, not just the keychain one: the service is `Claude Code<suffix>-credentials[-<sha8>]` **and** the identity file is `.claude<suffix>.json` in the same config dir. The suffix is `-custom-oauth` whenever `CLAUDE_CODE_CUSTOM_OAUTH_URL` is **non-empty** (an empty value is falsy and changes nothing; an *unapproved* endpoint makes claude throw rather than fall back), otherwise it comes from the build channel — `""` production, `-local-oauth`, `-staging-oauth` — which a released binary hard-codes to production, so the environment can only ever produce `""` or `-custom-oauth` | Source-read from the installed bundle, 2026-07-30 on 2.1.220, no login and no keychain access. The bundle is one Mach-O with the JS inline, so read it by offset: `B=~/.local/share/claude/versions/<version>`, `grep -oab -- '-custom-oauth' "$B" \| cut -d: -f1`, then `dd if="$B" bs=1 skip=<offset-400> count=1400 2>/dev/null \| LC_ALL=C tr -c '\11\12\15\40-\176' '.'`. A `grep -oa` with a wide context pattern **times out** on 266 MB and `strings` is useless (one giant string) — offsets then `dd` is the technique. Three sites must agree: the suffix function (`if (process.env.CLAUDE_CODE_CUSTOM_OAUTH_URL) return "-custom-oauth"`, else `switch` on a channel function that `return"prod"`), the service assembly (`` return `Claude Code${…OAUTH_FILE_SUFFIX}${"-credentials"}${o}` ``, at the `"-credentials"` literal), and the identity path (`` let e=`.claude${…}.json` ``, plus a loop over all four suffixes). Cross-check with the shim run above and `CLAUDE_CODE_CUSTOM_OAUTH_URL` set. kae **refuses** claude (exit `5`) on a non-empty value rather than computing the suffix, because the build-channel half is not visible from the environment at all — a recorded gap (docs/ROADMAP.md), and modelling one of the three sources would stay silently wrong for the other two |
| A **host-managed provider** is a third credential source: with `CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST` truthy, claude reads the JSON file `CLAUDE_CODE_HOST_CREDS_FILE` names — rejecting it unless it is absolute, ≤64 KiB, owned by the caller, not group/other-readable, and carries a live `pid`/`procStart` and an unexpired `expiresAt` — and injects its `env` entries, with the token landing in the variable `CLAUDE_CODE_HOST_AUTH_ENV_VAR` names (default `ANTHROPIC_AUTH_TOKEN`). `ANTHROPIC_UNIX_SOCKET` marks the same host-managed mode | Same offset-read technique at the `CLAUDE_CODE_HOST_CREDS_FILE` literal (2.1.220): one function validates and parses the file, another gates on the truthy `…MANAGED_BY_HOST` and writes `process.env`. kae cannot name the destination variable when the host renames it, so it warns on the four mechanism variables instead (docs/ADAPTERS.md "Environment conflicts"). A warning, not an unsupported refusal — this moves what authenticates, not where kae writes |
| A **relative** `CLAUDE_CONFIG_DIR` is used verbatim: claude resolves it against its own working directory, so kae (invoked from anywhere in the project) reads and writes different *files* — but **not** a different keychain item, because the service name hashes the variable's raw value rather than a resolved path | Read the resolver out of the bundle: `grep -oab -- 'env.CLAUDE_CONFIG_DIR' "$B"` then a `dd` window at each offset (technique in the row above). At 2.1.220 it is `AIl(){return process.env.CLAUDE_CONFIG_DIR}` and `fn = Vr(() => (AIl() ?? join(homedir(), ".claude")).normalize("NFC"), AIl)` — NFC and no path resolution — with the identity path `join(process.env.CLAUDE_CONFIG_DIR \|\| homedir(), ".claude<suffix>.json")`. Corroborating literal in the same bundle: a subprocess's value must match the parent's "same path, same separators". kae warns (`env_conflict`) and keeps honoring the value |
| The keychain item's **account attribute** is `$USER` when it matches `^[a-zA-Z0-9._-]+$`, otherwise the OS username, and the literal `claude-code-user` when neither is usable | The shim log's `-a <account>` names it. It is load-bearing because claude's reads are account-scoped (`find-generic-password -a <account> -s <service>`): an item kae writes under a different account attribute is invisible to claude even when the service name matches exactly |

### Other tools

| Tool | Assumption | How to verify | Verified on |
|---|---|---|---|
| codex | `auth.json` holds only auth state, so the whole file may be swapped | switch, then `codex login status` in a fresh process names the applied account; `config.toml` and history are untouched | 0.146.0 (re-verified 2026-08-04) |
| codex | the `Codex Auth` keyring item is identified by service **and account**, where the account is a **rule**: `cli\|` + the first 16 hex chars of `sha256(canonical CODEX_HOME)` (symlink-resolved, absolute — codex canonicalizes the path before hashing, and refuses to start when it does not resolve). One service therefore holds **one item per codex home**, all legitimate, and codex's own delete is service+account scoped | Read `codex-rs/login/src/auth/storage.rs` (`compute_store_key`, `DirectKeyringAuthStorage::delete`) at the tag matching `codex --version` — codex is public, so this is a file read, not a measurement. Beware: `compute_store_key` exists in **two** modules and symbol-grepping the stripped binary finds the MCP-OAuth one first. To confirm against a live item without a real login: `printf 'sk-not-a-real-key' \| CODEX_HOME=<temp> codex login --with-api-key` (a purely local write, no network), then `security find-generic-password -s "Codex Auth" -a "cli\|$(printf '%s' "$(python3 -c 'import os,sys;print(os.path.realpath(sys.argv[1]))' <temp>)" \| shasum -a 256 \| cut -c1-16)"` — attributes only, never `-w`. Clean up with `CODEX_HOME=<temp> codex logout` (its own scoped delete), not `security delete-generic-password`. Modelling the account as an opaque per-login id made a kae switch delete another `CODEX_HOME`'s login. **Also confirmed for a path reached through a symlink** (2026-07-30), which is the case that matters for a bond dir: with `CODEX_HOME` set to `<tmp>/link/shared/<pinID>/codex` where `link -> real`, codex created the item under the account of the **resolved** path and the raw-path account was absent — so kae's `EvalSymlinks` step is required, not defensive | 0.146.0 (source 2026-08-04; live item 2026-07-30 on 0.145.0) |
| codex | a **relative** `CODEX_HOME` is canonicalized against **codex's own** working directory before use, and that canonical path is what the keyring account hashes — so it moves the `auth.json` store *and* the `Codex Auth` item, unlike claude's variable which moves only files. codex refuses to start when the relative path does not exist in its working directory | Login-free, temp dirs only: `T=$(mktemp -d); mkdir -p "$T/relcfg"; printf 'this is not = valid toml [[[\\n' > "$T/relcfg/config.toml"`, then `cd "$T" && CODEX_HOME=relcfg codex login status` reports the parse error against `/private/var/.../relcfg/config.toml` (resolved against codex's cwd, symlink-resolved), while the same command from a sibling directory with no `relcfg` fails with `CODEX_HOME points to "relcfg", but that path does not exist`. kae warns (`env_conflict`) | 0.146.0 (source 2026-08-04; behavioural 2026-07-31 on 0.145.0) |
| codex | `cli_auth_credentials_store` is the enum `file` (**the default for an absent key**) \| `keyring` \| `auto` \| `ephemeral`, and `auto` reads the keyring **first**, falling back to `auth.json` only when the item is absent or unreadable. A successful keyring write **deletes** the `auth.json` fallback. `[features] secret_auth_storage` (default: on only on Windows) swaps the keyring backend for an encrypted secrets file, so the credential is not in the item at all | Same file plus `codex-rs/config/src/types.rs` (`AuthCredentialsStoreMode`, `#[default] File`) and `AutoAuthStorage::load`. The delete-the-file half is also live-confirmed: after `codex login --with-api-key` under a keyring store, `CODEX_HOME/auth.json` is gone. Treating everything that is not `keyring` as the file store is what would let kae write `auth.json` while codex reads the item — the failure shape that shipped for claude's per-directory keychain item | 0.146.0 (source 2026-08-04; live 2026-07-30 on 0.145.0) |
| codex | **Open (Linux/WSL).** Under `auto`, kae resolves `auth.json` off macOS on the assumption that a keyring codex could use is not holding the credential. codex's keyring crate does reach the Linux Secret Service, so a desktop with one running is the codex-shaped repeat of the macOS pin defect: kae would switch a file codex no longer reads | Needs a Linux box with a running Secret Service: set `cli_auth_credentials_store = "auto"`, log in, and see which store codex wrote (`secret-tool search`-equivalent attributes, plus whether `auth.json` was deleted). If it uses the keyring, kae must refuse `auto` there too, the way it already refuses keyring-only. Until then treat the Linux `auto` path as unverified rather than confirmed | not verified |
| agy | of the shared `gemini` keychain service, only account `antigravity` is agy's; siblings belong to the Gemini ecosystem and must never be read or written | switch, confirm a fresh agy session reports the applied account and a sibling `gemini` item is unchanged | 1.0.10 |
| agy | the live account is resolved server-side from the opaque token and never persisted, so identity can only come from `~/.gemini/google_accounts.json` `.active` (or `--identity`) | after an Antigravity login, `kae add agy` auto-names from `.active`; no other on-disk source appears | 1.0.10 |
| agy | **the macOS keychain is conditional, not the platform's answer.** agy's auth package pairs a keyring store with a file store: `shouldBypassKeyring` sits next to an ssh / wsl / container detector, every keyring operation has a **1s timeout**, and a failure falls back to the file ("Failed to save token to keyring, falling back to file", plus load and remove variants). So a remote-shell session on a Mac uses the file store, and kae's keychain switch does not reach it. agy **shells out to `/usr/bin/security`** (a Go keyring library, `go-keyring-base64:` / `go-keyring-encoded:` payload prefixes) | Read the binary, no login: `python3 -c "import re;d=open('/usr/local/bin/agy','rb').read();print(len(re.findall(rb'falling back to file',d)))"` for the fallback strings, then extract printable runs (`re.finditer(rb'[\x20-\x7e]{6,}')` — `strings` truncates but works too) and grep them for `jetski/cli/backend/auth/auth\.` to list the store types (`cliTokenStorage` = keyring, `cliFileTokenStorage` = file, `shouldBypassKeyring`, `containerDetector` / `sshDetector` / `wslDetector`). The detector inputs are the literals `SSH_TTY`, `SSH_CONNECTION`, `SSH_CLIENT`, `WSL_DISTRO_NAME`, `WSL_INTEROP`, `/.dockerenv`, `/proc/1/cgroup`. Live corroboration without a login: `~/.gemini/antigravity-cli/log/*.log` records `ChainedAuth: authenticated via keyring (effective: keyring)` per run — an `effective:` other than `keyring` on macOS is this row breaking. **Still unmeasured: the fallback file's path**, which is why kae warns instead of declaring an artifact; agy has no CLI login to drive, so proving it needs the `security` PATH shim (which *does* apply here, unlike codex) plus a way to make agy write a token | 1.0.10 (binary read + live logs, 2026-07-31) |
| opencode | `/openai` is the subscription login, and sibling provider keys are independent credentials that must survive a switch | switch with an extra provider key present in `auth.json`; the sibling key is byte-identical afterwards | 1.17.4 |
| opencode | **`auth.json` is still the live store**, with two other stores present but not authoritative: `account.json` (`{version, accounts, active}`) is **derived** from auth.json on every run by 1.17.3 and unreferenced from 1.17.4 on (the filename is in 1.16.2–1.17.0 as well, but a bare `auth list` there does not produce the file), and the `credential` table in `opencode.db` is populated from auth.json **exactly once** behind a `data_migration` marker (`credential.auth-json`, 1.17.4 only) and not maintained afterwards | Login-free, temp XDG root, no real `$HOME`: plant a dummy `{"openai":{"type":"oauth",…}}` at `$XDG_DATA_HOME/opencode/auth.json`, run `opencode auth list`, then **rewrite auth.json with a different provider key** and run it again — the second run must report the new key (the file wins). Cross-check the other two stores: `sqlite3 opencode.db "select name from data_migration; select connector_id,label,active from credential;"` (never the `value` column) and `account.json`'s `active`. Measured 2026-07-31: `auth list` reflects a rewritten auth.json on 1.16.2, 1.17.3, 1.17.4 and 1.18.5 (so the file, not a cache, is what is read); on 1.17.4 — the one version that imports — `auth logout <provider>` then empties auth.json and leaves the imported DB row untouched, which is what makes auth.json the store and the row dormant. 1.18.5 creates neither the marker nor a row. **The failure mode to watch for is a version where the DB row wins**: kae's patch would then be a silent no-op, and on 1.17.4 the row is frozen at whichever account auth.json held on the first run | 1.17.4 (behaviour, 2026-07-31; also 1.16.2 / 1.17.3 / 1.18.5) |
| opencode | two environment inputs put kae and opencode on different credentials, and kae **warns** on both rather than following them: `OPENCODE_AUTH_CONTENT` supplies an entire auth.json body inline and is read before the file, and `XDG_DATA_HOME` is used **verbatim with no absolute-path check**, so a relative value resolves against opencode's working directory while kae ignores it per the XDG spec | `Auth.all()` starts `if(process.env.OPENCODE_AUTH_CONTENT) try{return JSON.parse(…)}` and the data home is `process.env.XDG_DATA_HOME \|\| join(homedir(),".local","share")` — both readable in the installed binary (plain minified JS inside the Mach-O: `python3 -c "import re;d=open('<binary>','rb').read();[print(repr(d[m.start()-260:m.end()+260])) for m in re.finditer(rb'XDG_DATA_HOME',d)]"`, and note two bundled plugins repeat the same resolver). Behavioural confirmation, no login: from a temp cwd containing `reldata/opencode/auth.json`, `env XDG_DATA_HOME=reldata opencode auth list` reports the credential and prints the path as `reldata/opencode/auth.json`. opencode sets `OPENCODE_AUTH_CONTENT` itself when spawning a workspace child, so an inherited value is a real case, not a hypothetical | 1.17.4 (source + behaviour, 2026-07-31) |
| cursor | the credential is **three** opaque items under account `cursor-user` — `cursor-access-token` (a raw JWT), `cursor-refresh-token`, `cursor-api-key` — written and cleared as one unit, round-tripped verbatim. The service names come from a build-time domain constant (`cursor`), not from the environment, so kae may model them as constants | switch, then `cursor-agent status` in a fresh process reports the applied account **and** `authenticated` (not `partially-authenticated`, which is what a missing refresh item gives). The unit-ness is a source fact: read `setAuthentication` / `clearAuthentication` in the installed bundle — `~/.local/share/cursor-agent/versions/<version>/index.js` is unminified-enough JS, so `grep -oa` on the credential-store class settles it without a login. Attribute-only `security find-generic-password -s cursor-refresh-token` (never `-w`) shows which items exist | 2026.06.16 (bundle source read, 2026-07-30) |
| cursor | **cursor-agent never redeems the stored refresh token.** Its only path to a new access token exchanges an **api key** (`cursor-api-key`, else `CURSOR_API_KEY`) at `/auth/exchange_user_api_key`, and that write persists all three items. With no api key an expiring token is returned as-is and the request fails — so an expired snapshot needs an interactive login and `Freshness` is right to say so, but for this reason and not "there is no refresh token" | Same file: the refresh helper takes `{currentToken, ephemeralToken, isTokenExpiringSoon, refreshToken}` and its `refreshToken` closure returns null without an api key. Beware the red herring: the bundle's `grant_type=refresh_token` code is the **MCP client's** OAuth (in `cursor-agent-svc.js`), not cursor's own login — the same two-modules trap codex has. If a release starts redeeming it, cursor becomes refreshable and the stale warning has to learn about it | 2026.06.16 (bundle source read, 2026-07-30) |
| cursor | `cursor-agent status` prints `✓ Logged in as <email>` on one line with exit 0 (what `Identity` parses) | run it while logged in; the marker, the single line, and the exit code all hold | 2026.06.16 |
| copilot | per-account tokens coexist in the keychain, so repointing `/lastLoggedInUser` is the entire switch | after a switch, a fresh `copilot -p "say AUTH-OK" --no-color --allow-all-tools` acts as the other account (cross-account item still open — v0.7.0) | 1.0.61 |
| copilot | `config.json` is JSONC; its comments, trailing commas, and formatting must survive the patch | diff after a switch **and** after `kae rollback`: the leading `//` comments and `trustedFolders` survive both | 1.0.61 |
| copilot | **the config directory is a rule**: `--config-dir` (deprecated, hidden) → `COPILOT_HOME` → `~/.copilot`, where `COPILOT_HOME` is the directory *itself* and is used verbatim (no normalization, no absolute-path check). Setting it also skips copilot's one-way `$XDG_CONFIG_HOME/.copilot` → `~/.copilot` migration. The auth write is always `config.json`; the bare `config` file in the same directory is a fallback of the settings-migration loader only. A **relative** value is resolved against the process's own cwd by both sides, so kae warns rather than pretending to know copilot's | The **installed CLI is not the binary on `PATH`**: that one is a launcher which loads `~/.copilot/pkg/universal/<newest>/app.js` (search order includes `$COPILOT_HOME/pkg`), so a version manager can pin an older launcher while `copilot --version` reports the newer package — verify against the version `copilot --version` prints and read `app.js`, which is plain minified JS on disk. `grep -o 'COPILOT_HOME[^,;)]\{0,100\}' app.js` shows the resolver `ss()` (`t?.configDir ? … : process.env.COPILOT_HOME ? … : join(homedir(),".copilot")`), the loader `Umo()` (`join(e,"config.json")`, then `join(e,"config")`), and copilot's own help text ("override the directory where configuration and state files are stored"). Behavioural confirmation without touching the real home: symlink `$T/pkg → ~/.copilot/pkg`, put an `mcp-config` naming a probe server in `$T`, and `COPILOT_HOME=$T copilot mcp list --json` lists it | 1.0.61 (source + behaviour, 2026-07-31) |

The release acceptance run below is how these rows get re-verified: it already
launches fresh tool processes against real accounts, which is what every row
needs.

### git behaviour kae depends on

`kae pin` keeps its fragment out of `git status` by writing the repository's
shared exclude file, which rests on two facts about git that no layout check
would notice changing:

| Assumption | Measured |
|---|---|
| A linked worktree's own `$GIT_DIR/info/exclude` (`<main>/.git/worktrees/<name>/info/exclude`) is **not consulted**, while `$GIT_COMMON_DIR/info/exclude` is honoured by the main checkout **and** by every linked worktree — so one entry covers the whole repository | 2026-08-04, git 2.55.0 on darwin 24.6.0 |
| `git rev-parse --git-common-dir` is **relative to the current directory** in an ordinary checkout (`.git`, `../.git`) and absolute in a linked worktree; an entry in `info/exclude` is anchored at the **repository root**, so it needs `--show-prefix` in front of it (unlike a `.gitignore` entry, which is anchored at its own directory) | 2026-08-04, same |
| An entry is a **wildmatch pattern**, not a path: a directory component containing `[`, `]`, `*`, `?` or `\` must be backslash-escaped or the rule matches nothing. Measured: `/[wip]-feature/.config/mise/conf.d/kagikae.toml` left the fragment untracked; `/\[wip\]-feature/…` ignored it, and the same held for a `*` component | 2026-08-04, same |
| `git rev-parse --git-common-dir --show-prefix` emits **exactly two newline-terminated lines** — so three elements after splitting on `\n`, the last empty — in every shape measured: bare repository (`.` + empty prefix, exit 0), checkout root, subdirectory, linked worktree, worktree subdirectory, and even a cwd inside `.git` or `.git/info`. kae requires that exact shape rather than "at least two lines", because rev-parse quotes nothing and a newline is legal in a path component: a longer answer means the two values cannot be told apart, and following it would write an exclude file outside the repository | 2026-08-04, same |

Unlike the AI-tool rows above, these are **guarded by a test rather than by a
release run**: `TestEnsureGitExcludedLeavesEveryWorktreeClean` (`internal/cmd`)
builds a real repository with a linked worktree, records one entry from the main
checkout, and fails if either checkout is dirty afterwards — so `mise run check`
re-measures them on every commit. It then records a second entry from a **nested
subdirectory** and asserts that one reads `/nested/…`, which is what re-measures
the anchoring row specifically: at the repository root the prefix is empty, so a
root-only check cannot tell whether `--show-prefix` is being used at all. It skips
when `git` is not installed, which is the one way it can go quiet. To reproduce by
hand:

```bash
T=$(mktemp -d) && git init -q "$T/main" &&
  git -C "$T/main" -c user.email=you@example.com -c user.name=t commit -q --allow-empty -m init &&
  git -C "$T/main" worktree add -q "$T/wt1" -b side
mkdir -p "$T/wt1/.config/mise/conf.d" && : > "$T/wt1/.config/mise/conf.d/kagikae.toml"
# The worktree's own exclude file does nothing:
mkdir -p "$T/main/.git/worktrees/wt1/info"
echo '/.config/mise/conf.d/kagikae.toml' > "$T/main/.git/worktrees/wt1/info/exclude"
git -C "$T/wt1" status --porcelain          # still shows ?? .config/
# The common one covers it:
rm "$T/main/.git/worktrees/wt1/info/exclude"
echo '/.config/mise/conf.d/kagikae.toml' >> "$T/main/.git/info/exclude"
git -C "$T/wt1" status --porcelain          # empty
```

## Upstream Literal Fingerprints (`mise run audit`)

The table above is verified by a human on a release run. This one is verified by a
machine on every audit: for each literal the *upstream tool* owns and kae depends
on, how many times it occurs in that tool's installed artifact. A count that moves
means the tool's own code around kae's assumption changed — the earliest signal
available offline, and the only one that fires on a release where the layout is
byte-identical.

**Why a count and not just presence.** Measured across the three claude builds on
one machine (2026-07-31): every count below was **identical** on 2.1.218, 2.1.219
and 2.1.220, while the bundle itself grew from 264,548,368 to 266,397,712 bytes.
The minified identifiers around these literals churn between builds; the counts do
not. Presence alone would miss a literal that survives in one place and disappears
from another.

**The counts are measured, never derived from kae's own constants.** Two of kae's
most important literals do not exist in the bundle at all, because upstream
*composes* them:

- `claude.KeychainService` is `"Claude Code-credentials"`, and that string occurs
  **0** times in 2.1.220. Upstream builds it as `"Claude Code"` + the OAuth suffix +
  `"-credentials"`, so only the parts are countable (`Claude Code` occurs 1070
  times — noise; `-credentials` 24 times — usable).
- cursor's four service names (`cursor-access-token`, `cursor-refresh-token`,
  `cursor-api-key`, `cursor-user`) each occur **0** times, because cursor-agent
  composes them from a build-time domain constant. The suffixes are what to count.

Generating this table from kae's Go constants would therefore make its two most
load-bearing rows permanently red. Measure the artifact, then write the number down.

**Zero is a legitimate expected value.** `google_accounts.json` occurs **0** times
in agy 1.0.10 even though kae reads that file for agy's identity — it is a
leftover from the Gemini-CLI era that current Antigravity does not name. Recorded
as 0, the row says "kae depends on this *not* being upstream's business", and a
change to nonzero is news worth acting on.

**Noise beats nothing only in one direction: a noisy row is worse than no row.**
Rejected for that reason, with what they measured: `gemini` (4507 in the agy
binary — the whole Gemini ecosystem's package paths), `Claude Code` (1070),
`antigravity` (175 — mostly Go package paths, so any added file moves it), and the
short generic JSON keys (`email`, `active`, `login`, `tokens`, `accountId`) and
bare enum words (`file`, `keyring`, `auto`, `ephemeral`). Prefer a literal that is
hyphenated, compound, URL-shaped, or an env-var name.

**codex is deliberately absent from the table.** It has no fingerprintable
artifact here: the release binary is stripped Rust, `compute_store_key` exists in
**two** modules so a symbol grep finds the MCP-OAuth one first
([measuring.md](../.claude/skills/upstream-auth-drift/references/measuring.md)),
and on the machine this was measured on codex is not installed as an inspectable
binary at all. Its instrument is the public source at tag
`rust-v<VerifiedVersion()>` — read `codex-rs/login/src/auth/storage.rs`, which is
what every codex row in the table above already does.

### How it runs, and what it refuses to do silently

`TestUpstreamLiteralFingerprints` has two halves:

- The **table parse** runs in `mise run check`, on every commit, with no tool
  installed: it fails on a malformed row, a count that is not a number, a tool
  outside `constants.Tools`, and on a tool that is neither in the table nor in the
  test's named exclusion list. A new adapter therefore cannot be added without
  either fingerprints or a recorded reason.
- The **counting** runs only under `mise run audit` (`KAE_FINGERPRINT=1`), which is
  where reading a 266 MB binary belongs. It reads the artifact of the version this
  table records, once per tool, and logs the path it read.

A tool whose artifact is not where the table says is a **failure naming that exact
path**, never a skip: a fingerprint run that passes because it found nothing to read
would report "the assumptions hold" on no evidence at all — the failure shape this
repo has already shipped twice (a conformance guard that never examined codex, a
doctor check that returned silently). The version comes from the table, so an
upgraded tool lands here, and the remedy is what an upgrade calls for anyway:
re-measure and update both tables. Choosing the newest installed build instead was
tried and is worse — on one machine it read copilot 1.0.36 while 1.0.61 was the
installed CLI, and mise's `opencode/1` alias beat `opencode/1.17.4`, each reporting a
pile of moved counts for a tool that never changed.

### Re-measuring

```bash
# -F: a literal, not a regex. -a: treat the binary as text.
# `wc -l`, not `grep -c`: -c counts matching *lines* even with -o.
grep -Foa -- '<literal>' <artifact> | wc -l
grep -Froa -- '<literal>' <artifact-dir> | wc -l   # cursor: many webpack chunks

# Every literal in one pass — one read of a 266 MB bundle instead of nine:
grep -Foa -f literals.txt <artifact> | sort | uniq -c
```

**`-F` is not optional, and leaving it off is how the first version of this table
shipped a wrong number.** Without it, `auth.json` is a pattern whose `.` matches any
byte: it counted 12 in opencode 1.17.4, of which 3 were `auth-json`. The literal
occurs 9 times. The check compares literals, so a regex-inflated count reads as
upstream drift on a tool that never moved.

| Tool | Artifact | Measured on |
|---|---|---|
| claude | `~/.local/share/claude/versions/<version>` (one Mach-O, JS inline) | `2.1.220` |
| cursor | `~/.local/share/cursor-agent/versions/<version>/` (webpack chunks) | `2026.06.16-20-30-07-a07d3ac` |
| copilot | `~/.copilot/pkg/universal/<version>/app.js` (plain JS) | `1.0.61` |
| opencode | `~/.local/share/mise/installs/opencode/<version>/opencode` (Bun single-file executable) | `1.17.4` |
| agy | `/usr/local/bin/agy` (Go binary; no versions directory) | `1.0.10` |

Only the **version** column is machine-checked. The paths themselves live in
`fingerprintArtifacts` in the test, which is what the check actually opens — this
table documents them, and the audit logs the path it read, so a disagreement shows
up the first time it runs rather than staying hidden.

Three things these paths are not: opencode's is mise's install tree because that is
how it is installed here, not a layout opencode defines; agy installs straight to
`/usr/local/bin` with no per-version directory, so its recorded version cannot be
checked against the path at all (a bump shows up as moved counts instead); and the
`~/.local/share` prefixes are written as measured, **not** resolved through
`XDG_DATA_HOME`. Whether these installers honour that variable for their own install
location is unmeasured, and guessing it is the mistake this file exists to stop — on
a machine where the variable points elsewhere the check fails naming the path it
tried, which is the honest outcome.

A row does not have to be a string kae's code compares. `partially-authenticated`
appears nowhere in kae's source at all, and `exchange_user_api_key` only inside a
comment (`internal/adapter/cursor/cursor.go`, explaining why cursor cannot refresh).
Both anchor cursor assumptions in the table above — a switch that leaves a mixed pair,
and the only path that mints a new access token — and each is what would move if that
assumption changed. Do not delete a row for not matching a Go literal.

| Tool | Literal | Count |
|---|---|---|
| claude | `-credentials` | 24 |
| claude | `claude-code-user` | 3 |
| claude | `CLAUDE_CONFIG_DIR` | 42 |
| claude | `CLAUDE_SECURESTORAGE_CONFIG_DIR` | 13 |
| claude | `CLAUDE_CODE_CUSTOM_OAUTH_URL` | 8 |
| claude | `claudeAiOauth` | 17 |
| claude | `oauthAccount` | 70 |
| claude | `refreshTokenExpiresAt` | 12 |
| claude | `profileFetchedAt` | 7 |
| cursor | `-access-token` | 4 |
| cursor | `-refresh-token` | 1 |
| cursor | `-api-key` | 17 |
| cursor | `exchange_user_api_key` | 1 |
| cursor | `partially-authenticated` | 2 |
| cursor | `Logged in as` | 12 |
| copilot | `COPILOT_HOME` | 23 |
| copilot | `lastLoggedInUser` | 4 |
| copilot | `COPILOT_GITHUB_TOKEN` | 21 |
| opencode | `OPENCODE_AUTH_CONTENT` | 3 |
| opencode | `XDG_DATA_HOME` | 9 |
| opencode | `auth.json` | 9 |
| agy | `go-keyring-base64:` | 1 |
| agy | `shouldBypassKeyring` | 3 |
| agy | `falling back to file` | 6 |
| agy | `WSL_DISTRO_NAME` | 1 |
| agy | `WSL_INTEROP` | 1 |
| agy | `SSH_CONNECTION` | 1 |
| agy | `google_accounts.json` | 0 |

What each row is evidence *for* lives in the assumptions table above — these are
counts, not explanations. When a count moves, work that tool's rows there.

## Real-machine gate — does `refreshTokenExpiresAt` predict the login's death? (**open**)

Opened 2026-07-31, then briefly closed on a documentation citation and reopened the
same day. The reason it was reopened is the useful part.

**What the vendor documents, and it is worth having:**

> "Claude Code tried to renew your saved claude.ai or Claude Console login and the
> OAuth service rejected the stored refresh token, so Claude Code cleared the saved
> credentials. After that, each request stops locally before it reaches the API,
> because **only `/login` can create new credentials**."
> — [Login expired](https://code.claude.com/docs/en/errors#login-expired)

That settles the **consequence** half: a rejected refresh token ends in an interactive
login, with no automatic recovery, and the failed refresh clears the credential —
which is the behaviour kae already models as the tombstone (`Revoked`). So
`credential_stale`'s *remedy* (name the tool's login flow first, re-capture second) is
corroborated.

**What it does not settle, and what this gate is actually for:** that kae's locally
cached `refreshTokenExpiresAt` accurately predicts *when* that rejection happens. That
timing claim is what `credential_stale` and `credential_expiring` both rest on, and no
vendor page states it. A documentation citation is also a weaker instrument than every
sibling gate in this file, each of which is closed only by a dated run — and
[AGENTS.md](../AGENTS.md) is explicit that kae depends on undocumented upstream
*behaviour*, and that where docs and the binary disagree the binary wins. Closing this
one on a citation would have set a precedent the rest of the file does not follow.

**Procedure** (needs a real machine and a real login; no second account):
1. `kae add --no-login claude <acct>` immediately after a completed `/login`, and note
   both `captured_at` and `relogin_by` from `kae ls --json`. Record the login date —
   without it the measurement cannot be interpreted, which is the mistake that
   produced the retracted "≈2 days" figure.
2. Leave that account untouched past `relogin_by`, using a *different* account
   meanwhile so the switch-away recapture does not refresh the snapshot.
3. `kae doctor --json` — assert `credential_stale` for it, and note how close the
   report is to the moment upstream itself starts refusing.
4. `kae use claude <acct>`, then start claude in a **fresh process**: does it serve a
   session, or ask for a login?

**Either outcome is a result.** Serving a session means `refreshTokenExpiresAt` is
pessimistic and kae over-warns; asking for a login at approximately that timestamp is
the confirmation. Record the outcome, the version, and the login-to-deadline interval.

## Real-Machine Acceptance (release only)

This run doubles as the re-verification pass for the **Upstream Behaviour
Assumptions** table above: work each installed tool's rows, then update that
tool's `VerifiedVersion()` and its recorded version in the same commit. `kae
doctor` naming `upstream_version` for a tool is the signal that its rows are due.

Manual, on macOS, with real logged-in accounts and a fresh backup of
`~/.claude.json`:

1. `kae add --no-login claude <current-account>`
2. log in to the other account with the official CLI, `kae add --no-login` it
3. `kae use claude <first>` / back, verifying upstream CLI identity each
   time and `git`-diffing `~/.claude.json` for non-allowlist drift
   (`/oauthAccount` **is** the allowlist there; `projects`, `mcpServers`,
   onboarding and cache keys must be byte-identical)
4. `kae rollback` and verify identity returns

For claude, also confirm the identity cache tracks the credential, because a
correct token with a stale cache is the exact failure this switch exists to
prevent: after step 3, `~/.claude.json` `oauthAccount.emailAddress` must name
the account just applied, and the live token must resolve to the same account —
`curl -s -H "Authorization: Bearer <accessToken>" \
https://api.anthropic.com/api/oauth/profile` reports `account.email`. Do not
substitute "claude will fix it on the next run": it will not while its 24h
profile cache is warm (docs/ADAPTERS.md).

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

### Bound-directory credential store (macOS, no login needed)

kae and claude must agree on the keychain service name for a bound directory; a
disagreement is silent, because kae's write succeeds and claude simply reads a
different item. Shim `security` for **both** processes and compare, which needs
no real account and touches neither the real `$HOME` nor the real keychain:

1. Temp `HOME` plus temp `XDG_*`, and a `security` shim first on `PATH` that logs
   `"$*"`, prints a canned `{"claudeAiOauth":{…}}` for `find-generic-password`,
   and exits 0 otherwise.
2. `kae init`, a config with `secret_backend = "file"` and a one-account profile,
   then `kae add --no-login claude main` (the capture reads the canned payload).
3. `kae pin -i main` in a temp project dir. Read `CLAUDE_CONFIG_DIR` out of
   `.config/mise/conf.d/kagikae.toml`, and the `-s <service>` kae passed to
   `add-generic-password` out of the shim log.
4. Run the real binary against that same directory —
   `env -i HOME=<temp> PATH=<shim>:/usr/bin:/bin USER="$USER"
   CLAUDE_CONFIG_DIR=<dir> claude -p hi </dev/null` — and read the `-s <service>`
   it passed to `find-generic-password`.

The two service names must be identical, and `<dir>/.credentials.json` must not
exist (the superseded plaintext copy is removed). Confirmed on kae's fix commit
with Claude Code 2.1.220; the same run against a pre-fix build writes no keychain
item at all and reads only the unsuffixed shared one, which is the defect.

This is a naming-agreement check. That the payload itself round-trips is the
separate verbatim/ACL assumption above, and it needs the real keychain and a real
account — so a release still runs the two-account pin: re-bind in a pinned
directory, then launch claude there and confirm it reports the account kae bound.

Never run real-machine acceptance with uncommitted work in progress in the
live tool sessions.

## Secret Leak Regression

`go test ./internal/cmd/ -run TestSecretsNeverInOutputOrMetadata` asserts that
captured fixture secret values never appear in text output, JSON output, error
messages, or metadata files written by capture/switch/rollback.

Companion token leakage is covered by `TestCompanionFragmentLinesNeverLeakSecret`
(the token reaches the fragment and export fallback only as an `exec()`/`$()`
lookup, never as a literal) and `TestCompanionListHidesSecretValues` (the value
never appears in `kae companion list` text/JSON).

## Release Acceptance Log

### v0.17.0 (2026-08-08, macOS darwin 24.6.0, git 2.55.0)

Run in two passes, and the distinction matters because the tree moved between them.
The gates and the first smoke sweep were measured on `main` at merge `2882246`, tree
`a49f3430bfbcde9661b314a1d4d27ba4a6cb5304`. That sweep found a defect in this file's own
blocks and then, through the corrected blocks, a **code** defect (below) — so the fix and
every re-measurement live on `fix/validation-blocks-and-release-log`, whose tree is what a
reader should compare against. Binary `kae v0.17.0` in both. Every gate recorded by exit
code, never through a pipe; every smoke assertion checked at its own point in its block
rather than from the end state. Where a line below says "re-run against the fixed binary",
that is the branch tree and not the merge above it.

- `mise run check` **0**, `git diff --check` clean, `mise run goreleaser-check` **0**,
  `mise run audit` **0** — govulncheck reachable **0**, and the upstream literal
  fingerprints unchanged for all five measurable tools (no drift signal; codex has none
  by design).
- **Real-machine `kae doctor --json`, read-only: 22 checks, non-ok 2** — both
  `credential_expiring`, for two cursor snapshots whose logins genuinely expire in four
  days. That is the check doing its job on a real machine, not a release finding; nothing
  else was non-ok, and the codex `upstream_version` drift that the v0.16.0-era run raised
  did not recur.
- **§ per-worktree surfaces A–F: 31 of 31.** Run against the built binary on a temp HOME
  with every XDG root isolated (`. scripts/smoke-env.sh`), the repositories and worktrees
  created *inside* that HOME. This block needed no correction.
- **A code defect the corrected block then exposed, found by review of it and fixed
  before the tag.** A bind that could not *attribute* the copy in the account's credential
  store overwrote it with the older snapshot — reachable on **every first bind**, because
  the config dir attribution reads is created moments earlier and a shared bind links no
  identity cache into it. Reproduced in the shape a user meets it (use claude in one
  worktree, bind a second worktree to the same account) and it destroyed the only copy that
  could still refresh, with `kae doctor` silent afterwards because nothing was left to
  compare. The bind now keeps that copy and says so; `Conflicting` and unreadable copies are
  still replaced, deliberately (docs/ADAPTERS.md is normative, docs/ROADMAP.md owns the
  second one's trade-off). Case **A2** in the block above was added for it — it did not
  exist, and the first run walked the destructive path three times without asserting
  anything about it. **A2 and every other block were then run against the fixed
  binary**, extracted from this file rather than from a script, so nothing below is dated
  ahead of the tree it was measured on. The fix also moved what a bind writes when it keeps
  (no credential, no stale-file sweep, no identity label), which is why the cases that
  expect a harvest now seed the identity cache the *tool* would have written.
  **Superseded later on the same branch** — see the last entry below; A2 now asserts a
  harvest and **A3** is the case that holds the destruction fixed.
- **§ v0.17.0 surface — the credential harvest, A–A2–J: every documented assertion** — but
  **only after fixing the block**, and that is the other finding of this run: every
  credential fixture still targeted
  `CLAUDE_CONFIG_DIR` after this release moved the credential to
  `CLAUDE_SECURESTORAGE_CONFIG_DIR`, so cases stopped exercising the harvest while their
  negative assertions went green *because* nothing happened. kae was correct throughout —
  the block was measuring a directory kae no longer reads a credential from. That section
  above records what was corrected and what each case now proves. A count is deliberately
  not given: 52 assertions were measured, and correcting case C then split it into two
  arms whose separate measurements (the tombstone silent, the future-dated blank warning)
  are what that case now asserts — so a number here would name a set the block no longer
  has.
- **§ v0.17.0 surface — `kae relogin` and `credential_superseded`, K–M: 20 of 20**, also
  after fixing the block: two ordinary bindings of one account now share one credential
  store, so K's two-copies state needs one directory left in the pre-split shape. Running
  it straight after the harvest cases also turned out to be unsafe — that section, and
  docs/ROADMAP.md § `credential_superseded`, own both corrections.
- **§ v0.17.0 per-account credential real-machine smoke: 6 of 6** (26 assertions),
  including that `kae unpin --purge` keeps the credential while another binding uses it
  and, in the last one, names it as **the account's** at `credstore/claude/<account>`.
  The removal applies "absent" through the JSON pointer, so the file survives holding
  `{}` — asserted against a live contrast (a still-bound sibling account keeps its token)
  so it cannot pass by reading a path that is empty either way.
- **§ v0.17.0 completion real-machine smoke: 3 of 3** (23 assertions) in **bash and zsh**,
  driving the real `_kae` from scripts generated by this binary with it behind a PATH shim
  — a stale `kae` on `PATH` would answer `__complete` for the wrong build. `kae env <TAB>`
  offers `set`/`unset`/`list`, `kae backup <TAB>` offers `list`, `kae env set claude <TAB>`
  offers accounts and not tool names, and `kae env list <TAB>` offers nothing. Each shell
  carries a positive control (`kae use <TAB>` non-empty), because the "offers nothing"
  assertion passes for a harness that produced nothing — which it did on the first run,
  where `printf "%s\n" "${COMPREPLY[@]}"` printed one newline for an empty array.
- Secret scan: no real token, key, email or login handle anywhere in the tree; the
  fixtures are `ghp_smoke` / `ghp_secret` / `sk-ant-oat01-<label>` placeholders and
  `@example.com` addresses. Checked with the file backend's **base64** in mind — a raw
  `grep` over a stored payload finds nothing and reads as a pass.
- Deliberately **not** run, and recorded as such: no `kae pin` against the real `$HOME`
  (every bind above ran in a temp HOME with every XDG root isolated), and the operator's
  installed binary was not replaced. The credential blocks run under
  `KAE_CLAUDE_DRIVER=file`, so **nothing here touches `internal/keychain`** — the
  per-command read cache, and `kae relogin` against a real per-directory keychain item,
  stay covered by unit tests over the darwin sim only. Unchanged from the earlier runs and
  stated again because a green run reads as if it did more.

- **A second code defect, found the same way and on the same branch, and the harvest block
  re-run against it.** The fix above stopped a bind from *overwriting* a copy it could not
  attribute; what it did not change was **what attribution reads**. For the account's own
  credential store that was still the bound *directory's* identity cache, which since the
  split is evidence about a different object — so a re-bind between two accounts read
  `Conflicting` about a store the previous binding's label says nothing about and destroyed
  a live credential, and a cache that legitimately named this account confirmed a copy
  another directory had poisoned. Attribution now reads the directories currently reading
  that store. Extracting and running this file's harvest block against the change is what
  showed that **A2, B, D and E had all stopped testing their subjects**: A2's outcome
  legitimately changed (a sibling reader confirms, so the copy is preserved *and*
  harvested), B's two `expiresAt` values had become equal so attribution was never reached,
  and D and E reset their account with a re-capture — which no longer resets anything,
  because the credential store outlives it and the next pin harvests the leftover copy
  back. The block now carries `reset_main`, a no-reader case (**A3**) for the destruction
  A2 used to hold, and a disagreeing-readers case (**B2**); token names no longer prefix one
  another, after `grep MAIN-NEW` was found matching a `MAIN-NEW2`. Re-run end to end on
  this branch: every documented assertion, checked at its own point.

The superseded partial entry (2026-08-04, tree `40fa804`) is dropped rather than kept: its
per-worktree measurement is repeated above on the final tree, and its credential
measurements were made against a block that has since been shown not to test its subject.

**Tag-day pass (2026-08-09), `main` at merge `ed18a5b`, tree `f7572134`.** The tree moved
again between the entry above and the tag, so every gate and block was re-measured here
rather than carried over. What moved: a fourth independent execution review of the
pre-flight **backup** found three more defects in it, and the backup was **withdrawn from
this release** (`e6aaea1`) — the pre-flight's warning ships, the backup does not, and
`docs/ROADMAP.md` § A relogin's pre-flight refusal owes a backup carries the withdrawal
and the eviction question it never settled. The withdrawal changed a user-visible warning
string, which is why the blocks below were re-run rather than assumed.

- `mise run check` **0** on the merge, `git diff --check` clean, `mise run audit` **0**
  (fingerprints read five installed tools and passed — not a silent skip), `mise run
  goreleaser-check` **0**. Every one by exit code, never through a pipe: the first attempt
  read `${PIPESTATUS[0]}` under a shell that does not set it and recorded an empty status.
- **Real-machine `kae doctor --json`, read-only: 22 checks, non-ok 2** — both
  `credential_expiring`, the same two cursor snapshots as the entry above, now three days
  out. Real state, not a release finding.
- **§ per-worktree A–F: 31/31. § v0.17.0 harvest A–J: all assertions. § v0.17.0 relogin
  and `credential_superseded` K–M: 20/20.** K–M is the section the withdrawal could have
  staled and did not: the pre-flight **harvest** is untouched, and its hardest assertion —
  the harvest line appearing once *before* the login-flow line and once after — still holds.
  No file under `docs/`, `README.md` or `AGENTS.md` names the withdrawn backup reason.
- **§ v0.17.0 per-account credential: 16/16**, run under `. scripts/smoke-env.sh` with the
  file driver and file backend, as that section's own text requires. Its heading calls it a
  *real-machine* smoke and no step in it is one — the heading is what kept it out of an
  otherwise complete isolated pass, and it should move in with the temp-HOME blocks.
  One assertion failure in that run was the **runner's**, not kae's: `unpin --purge` on the
  last binding leaves the store file present as `{}` because absent is applied as absent,
  so a `test -f` reads as "not removed". The token is gone (positive control: 1 before, 0
  after) and the message names the account's store, not a per-directory one.
- **§ Smoke Checks (v0.16.0 L76–171) fails, and the document is what is wrong** — checked against
  `docs/CLI.md`, which is normative and disagrees with it. Bare `kae pin` is documented as
  isolated with symlink assertions; it is **shared** (`docs/CLI.md` § kae pin), and the
  fragment carries `codex/shared` with no `isolated/` directory anywhere. `kae pin codex
  side` passes a *profile* name where the command takes an **account**, and the block never
  captures `codex/side`, so it exits `7` under any faithful reading. Two `kae use` lines
  need a `default_profile` the block never sets and exit `64`; setting one makes both behave
  exactly as documented. **All four predate this release** — `git show v0.16.0` carries the
  same text, and the block is unchanged since the initial commit — so they are recorded
  here and left for the deferred documentation pass rather than fixed at a tag.
  The mechanism is worth more than the four: **this block's `# assert:` lines are comments,
  not commands.** It was the only section in this file whose assertions were non-executable,
  and it is where the defects had accumulated — a reader "running the block" gets
  exit 0 and never evaluates them.
  **Fixed in the documentation pass (2026-08-09), and a fifth defect came out of the same
  run**: the block removed `codex/main2` and then asked `kae profile set dev codex main2`
  for it, so four profile-lifecycle commands exited `7` in a cascade nothing named — three
  of them visibly, the fourth (`kae profile rm dev`) masked by the very `; echo $?` idiom
  that was supposed to be reporting it. The block
  is now a single seeded script whose assertions are the commands themselves; the fixtures
  it used to name only in prose are written in it, and its `pin` lines run in a throwaway
  repository inside the temp HOME, because `kae pin` binds the *current* directory and
  writes a `$GIT_COMMON_DIR/info/exclude` entry — run from this checkout, as the rest of
  the section is, it dirties the operator's repository. Verified by extracting the block
  from this file and running it: **every line exits `0`**, with deliberately broken variants
  confirming the assertions can still fail.
  Two claims made in the first draft of this paragraph were themselves wrong and are
  corrected here, which is the defect this whole entry is about. "Every line exits 0 except
  the **two** `grep -c` lines" was one line, not two, and it is now wrapped in `test` so the
  count is zero; and `grep -c 'claude' "$FRAG"` was added as "the other tools are untouched"
  when the fragment names the tool three times whatever it is bound to — an assertion that
  reads as a pass while claude moves with codex, in the same commit that claims to have
  closed that class. Both found by independent execution review.
  **The positive-control finding recorded here belongs to a different section**, and saying
  it here made it unfindable: `A3`, `B1`, `B2`, `B3` and `E` are cases in
  § v0.17.0 surface — the credential harvest, which has no `# assert:`-comment problem at
  all. Re-measured 2026-08-09, and the original wording was also too strong in two places.
  What holds: `B2` and `B3` pair their `snap … | grep -c FOREIGN` with a positive line on
  the **store**, not on `snap()`, so a broken `snap()` still reads as a pass. What does not:
  `A3` carries an explicitly labelled positive control (`test -d "$(store)"`) for its
  `test ! -e` line — what it lacks is one for its `snap` line; and `E` has a working
  `snap main | grep MAIN-NEW` immediately above its negative, which is a genuine control on
  `snap()` itself, weakened only by naming a different account. `B1` has a full one and says
  so.

### v0.9.0 (2026-06-19, macOS darwin 24.6.0)

Installable binaries (GoReleaser + install.sh + CI) and README OSS-parity
rewrite; the zsh `--install` note now warns about a stale compdump.

- `mise run check` green; `schema_version` 1; no new runtime dependency.
- Pipeline validated locally: `goreleaser check` passed and a snapshot release
  produced `kae_<version>_<os>_<arch>.tar.gz` for darwin/linux × amd64/arm64
  with `checksums.txt` and `kae` at each archive root — matching
  `scripts/install.sh` (shellcheck + `sh -n` clean) and `actionlint` clean.
- Windows is excluded from the build (`internal/lock` is Unix-only), matching
  the Platform Support table.
- **Real-machine smoke PASSED** (the v0.9.0 tag's first automated release): the
  Release workflow ran the shared check, the version-matches-tag guard,
  GoReleaser, and provenance attestation green; the release carries
  `kae_0.9.0_{darwin,linux}_{amd64,arm64}.tar.gz` + `checksums.txt`;
  `curl … install.sh | sh` verified the checksum and installed `kae v0.9.0`; and
  `mise x github:webkaz-labs/kagikae@v0.9.0 -- kae version` resolved the archive
  and printed `v0.9.0`.

### v0.8.9 (2026-06-18, macOS darwin 24.6.0)

`kae completion zsh --install` detects an existing user `fpath` dir
(`~/.config/zsh/completions` / `~/.zsh/completions` / `~/.zfunc`) instead of the
fixed XDG dir that was often not on `fpath`.

- `mise run check` green; JSON contract unchanged (`schema_version` 1); no new
  `go.mod` dependency.
- Motivated by a real report: completion only worked after
  `eval "$(kae completion zsh)"` because nothing was installed and the
  prospective `--install` target was not on the user's fpath.
- Unit/temp-HOME covered; the real-shell `--install` + fresh-shell `<TAB>` is the
  open smoke item.

### v0.8.8 (2026-06-18, macOS darwin 24.6.0)

Daily-use fixes: opencode identity (email over UUID); flag-aware + flag-name
shell completion.

- `mise run check` green (all packages); JSON contract unchanged
  (`schema_version` 1); no new `go.mod` dependency.
- Code review APPROVE: the 9-call-site registrar refactor (parseCommon →
  registerCommonFlags + per-command registerXFlags shared with `kae __complete
  flags`) verified behavior-preserving (no flag dropped/renamed/misbound); the
  opencode JWT decode and per-shell positional indexing confirmed correct.
- bash completion simulated locally: `kae add --no-login <TAB>`→tools,
  `kae add --<TAB>`→`--no-login`/`--restore`, `kae run -<TAB>`→`-s`/`-i`/`--env`,
  `kae use -i claude <TAB>`→accounts. Real-shell `<TAB>` smoke is the open item.

### v0.8.7 (2026-06-18, macOS darwin 24.6.0)

Complete account-identity coverage: `agy.Identity` from
`~/.gemini/google_accounts.json` (§A); `Identity` column in `kae status` (§B).

- `mise run check` green (all packages); JSON contract unchanged
  (`schema_version` 1, additive `identity` omitempty); no new `go.mod` dependency.
- Pure-additive, unit/temp-HOME covered — **no new real-machine gate**. The agy
  identity source was confirmed on the maintainer's machine
  (`~/.gemini/google_accounts.json` `.active` = the active Google account).
- Existing blank-identity accounts (agy, pre-identity claude snapshots) backfill
  on re-capture; documented, no new command.

### v0.8.6 (2026-06-18, macOS darwin 24.6.0)

agy keyring driver on macOS (§A), terser one-shot `kae run` default child (§B),
`claude /login` verification (§C — launched via the upstream flow, unchanged).

- `mise run check` green (all packages); JSON contract unchanged
  (`schema_version` 1); no new `go.mod` dependency.
- Code review: APPROVE after one round (the `account.Artifact` finding was
  rebutted — that struct intentionally persists no adapter-structural flags
  [`JSONC` is absent too]; apply re-derives specs from the
  live adapter, and only the adapter-independent backup record carries
  `keychain_match_account`). `/simplify` cleanups (`Spec.matchAccount()`, the
  shared agy no-item message const) were re-reviewed APPROVE.
- **agy two-account real-keychain gate PASSED** (verified by the maintainer):
  agy account switching round-trips through the `gemini`/`antigravity` item; a
  fresh agy session reflects the switched account.
- **fish dropped from the verified shells**: `kae completion fish` stays a
  best-effort generator (unit + `fish -n`), no longer a release-gated surface.
- **codex keyring two-account real-keychain gate: still deferred** (carried from
  v0.8.3; the file/keyring round-trip is unit-covered — not a v0.8.6 blocker).

### v0.8.4 (2026-06-17, macOS darwin 24.6.0)

Dynamic shell completion: §A `kae __complete` backend, §B native completion +
interactive `--install`, §C mise task-argument completion, §D docs.

- `mise run check` green (all packages); JSON contract unchanged
  (`schema_version` 1); no new `go.mod` dependency.
- Code review APPROVE: round-one found a fish `account`-position token-index
  off-by-one (`$tokens[3]` → `$tokens[4]`), fixed with a per-shell token-index
  regression test; the fix and the `/simplify` cleanups (shared
  `paths.XDGConfigHome`, `account.ListForTool` scoped read, constant-kind
  short-circuit, missing `ls`) were re-reviewed APPROVE.
- **bash + zsh real-machine smoke PASSED**: `~/.local/bin/kae` updated to v0.8.4;
  in both shells `kae <TAB>` listed commands (incl. `ls`), `kae use <TAB>` listed
  the live profile + tools, `kae use claude <TAB>` scoped to claude's accounts,
  and `kae account rm claude <TAB>` scoped to claude's accounts. The "two TABs to
  list" is the shells' standard ambiguous-completion behavior (zsh `LIST_AMBIGUOUS`
  + `menu select`; bash `show-all-if-ambiguous` off), not a kae defect — candidate
  generation is correct.
- **fish real-machine smoke: superseded** — fish was dropped from the verified
  shells (2026-06-18; see the v0.8.6 gate), so this is no longer an open
  acceptance item. `kae completion fish` stays a best-effort generator
  (`TestCompletionAccountTokenIndex`, since renamed `TestCompletionPositionalRouting`,
  + `fish -n`), not a release-gated surface.
- mise task-argument completion (`mise run ai-switch <TAB>`) is rendered by
  `kae mise init` and unit-tested (`TestMiseInitRendersCompletionTasks`, TOML
  parse); the live `mise run <task> <TAB>` resolution rides the same backend.

### v0.8.3 (2026-06-17, macOS darwin 24.6.0)

Discovery-unblock: §A freshness-as-adapter-capability, §B cursor `kae add`
identity, §C codex keyring driver, §D store + display the detected identity.

- `mise run check` green (all packages); `-race` clean; redaction tests
  (including the codex keyring payload) passed.
- Code review APPROVE (two rounds; the round-one findings were fixed and the
  fixes re-reviewed APPROVE); `/simplify` applied the shared
  `captureKeychainAccount` helper + a `keychain.WithReadCache` on the capture
  path (the rest clean or declined with reasons).
- **Cursor identity gate PASSED (real machine)**: `kae add --no-login cursor`
  (no name) on a live `cursor-agent status` login captured under the sanitized
  detected email (the local part), and `account.toml` + `kae ls` recorded the
  raw identity (§D); a pre-v0.8.3 cursor snapshot showed no `identity` field
  (omitempty / backfill-only-on-fresh-add confirmed on a real snapshot). The
  logged-out / unparseable path is covered by the fake-runner unit tests
  (`TestCursorIdentityFailures`). The test capture was removed afterward.
- §A/§D logic is driver-agnostic and unit-tested (per-tool `Freshness` on the
  adapters, `TestFresherConformance`, `TestAddRecordsIdentity*`,
  `TestRecapturePreservesIdentity`).
- **Codex keyring two-account real-machine gate: DEFERRED.** Switching the
  working codex install to `cli_auth_credentials_store = "keyring"` and two
  interactive OAuth re-logins with two accounts is disruptive and was deferred
  by decision. The driver is covered by fake-`security` round-trip tests
  (`TestCodexKeyringRoundTrip` — capture A → re-login B → `use A` restores A's
  verbatim item as a single upsert; `TestCodexKeyringForeignHomeItemNotCaptured`;
  `TestKeychainCodexHomesCoexist`). The two-account **real**-keychain gate remains
  the one open acceptance item — run it per the "v0.8.3 real-machine gate"
  procedure before relying on the keyring driver in production. The
  service-only-vs-service+account question this section left open was settled from
  upstream source on 2026-07-30 (service+account), and the service-only answer kae
  had shipped deleted another `CODEX_HOME`'s login.

### v0.8.2 (2026-06-16, macOS darwin 24.6.0)

Daily-use polish: §A concurrent `status` + secret read cache, §B `kae add`
account-name auto-detection, §C `kae ls`, §D shared snapshot comparator + JWT
decode consolidation. (Freshness-as-adapter-capability and cursor identity split
to v0.8.3 — see [RELEASE.md](RELEASE.md) / [ROADMAP.md](ROADMAP.md).)

- `mise run check` green (all packages); `-race` clean on the concurrent paths;
  `TestSecretsNeverInOutputOrMetadata` passed (no secret in the new output).
- Code review APPROVE on each of §A–§D; `/simplify` applied the JWT-decode
  consolidation (the rest clean or declined with reasons).
- Temp-HOME file-driver smoke (single account, no real keychain): §B captured
  `claude/alice` from a seeded login email (explicit name and the no-identity
  usage error confirmed); §C `kae ls` listed accounts + profiles with active
  markers and `[]` arrays; §A `status --json` returned all six tools in canonical
  order via the concurrent `Detect`.
- JSON kept `schema_version: 1`, stable tokens, and `[]` arrays.
- **Two-account real-keychain run passed**: auto-detect captured under the
  detected name; `use A → B → A` returned a fresh `claude -p` **AUTH-OK** (the
  verbatim round-trip survives the recapture read across two real accounts); no
  keychain prompt multiplication; `kae ls` marked the active account. The
  `refreshed …` recapture message did not fire (A's live token had not diverged
  from its snapshot — divergence guard working as designed); the
  recapture-on-divergence round-trip is covered by the driver-agnostic temp-HOME
  test (see the gate above).

### v0.8.1 (2026-06-16, macOS darwin 24.6.0)

Credential freshness / auto-recapture (A–D; §E split to v0.8.2). All gate items
passed:

- **Real-keychain round-trip under recapture**: `kae add --no-login claude main`
  captured via `claude-keychain-patch`; `kae use claude main` switched (backup
  written) and a fresh `claude -p "say AUTH-OK"` returned **AUTH-OK** — the new
  switch-away recapture reads the live keychain before applying without
  corrupting the verbatim payload.
- **§C coalescing**: the switch raised **no** keychain auth prompt (item ACL
  trusts `/usr/bin/security`; reads coalesce to one). No prompt multiplication.
- **§D doctor**: `kae doctor claude` reported `claude-keychain-patch`, live
  credential found, `no blocking problems` — `credential_stale` correctly absent
  for the just-captured account, `secret_orphan` correctly skipped on the
  keychain backend (documented enumeration gap).
- **§A recapture-on-divergence + §B stale warning**: confirmed via the temp-HOME
  file-driver smoke (driver-agnostic code): `use A → in-tool refresh → use B`
  printed `refreshed claude/A snapshot …` and `use A` re-applied the refreshed
  token; a switch to an expired-no-refresh snapshot warned naming `kae add` and
  proceeded; `doctor` flagged the stale snapshot and a seeded `secret_orphan`.
- `mise run check` green; code review APPROVE; JSON kept `schema_version: 1`.

### v0.7.2 (2026-06-16, macOS darwin 24.6.0)

Global-isolated (`kae use -i`) real-machine gate passed against a real,
logged-in claude account (the active account, re-snapshotted with
`kae add --no-login claude <account>` so the snapshot was current — see the
staleness note below). Steps and results:

- **`kae use -i claude <account>`** materialized
  `isolation/global/claude/<account>/.credentials.json` (`0600`, full
  `claudeAiOauth` shape incl. `refreshToken`, byte-matching the live keychain
  item) and wrote `~/.config/mise/conf.d/kagikae.toml` with `CLAUDE_CONFIG_DIR`
  → that home. `mise env` repointed `CLAUDE_CONFIG_DIR` to it (fragment
  mechanism works); `state.json` gained `synced: {claude: <account>}`.
- **Keychain not polluted**: `security find-generic-password -s "Claude
  Code-credentials" -w | md5` was byte-identical before and after `use -i`
  (twice). `use -i` reads the kae snapshot and writes only the private file —
  the real login item is never touched (file-driver path).
- **Fresh-process auth**: `claude -p '...' --model haiku` with the fragment's
  `CLAUDE_CONFIG_DIR` returned **AUTH-OK** from the private home (file
  credential, keychain bypassed on macOS), and the isolated `.credentials.json`
  survived (no clearing).
- **Teardown `kae use -s claude <account>`** deleted the fragment, cleared
  `synced`, switched the real home in place, and `mise env` no longer exported
  `CLAUDE_CONFIG_DIR`; a fresh **real** `claude -p` (no `CLAUDE_CONFIG_DIR`)
  returned AUTH-OK as the real account. `~/.claude.json` changes across the run
  are claude's own state writes (it is never switched by kae — Phase 3), not a
  kae mutation.
- **Staleness note (operational, not a bug)**: a snapshot captured days earlier
  failed the fresh-process check with `401 Invalid authentication credentials`,
  and claude then cleared the isolated `.credentials.json`. `use -i`
  materializes from the **snapshot**, whose OAuth tokens expire/rotate, so
  re-run `kae add --no-login claude <account>` to refresh the snapshot before
  isolating a long-idle account.
- **README examples verified** against the built binary on a temp HOME (file
  backend, `KAE_CLAUDE_DRIVER=file`): Quick Start (`add --no-login`,
  `use <profile>`, `use <tool> <account>`, bare status, `rollback`, `u` alias,
  `profile save`, `account rename`/`rm`), Pin (`pin <profile>` writes
  `./.config/mise/conf.d/kagikae.toml` + `.gitignore`, no `mise.toml`;
  `pin <tool> <account>` re-bind; `unpin` deletes the fragment), global isolated
  (`use -i` writes the global fragment; `use -s` removes it), and Beyond
  Switching (`run --env` injects the var; `run -i` uses the global isolated
  home; bare `kae use --quiet` is a silent no-op with a resolved profile;
  `mise init` preview) all behave as documented — no README changes needed.
- `mise run check` green; JSON kept `schema_version: 1`, stable tokens.

### v0.7.1 (2026-06-15, macOS darwin 24.6.0)

Temp-HOME smoke with the `v0.7.1` binary (file secret backend). All criteria
passed:

- **file-driver override**: with `KAE_CLAUDE_DRIVER=file`, `kae use claude main
  --dry-run` reported a `json-pointer` action on `~/.claude/.credentials.json`
  (driver `claude-file-patch`); unset, `kae status` reported
  `claude-keychain-patch` (no regression). `kae add`/`use` round-tripped on
  files with no `security` subprocess.
- **account rename**: `kae account rename claude main main2` moved the snapshot,
  copy+deleted the secret, set `active_updated`, and rewrote the referencing
  profile's mapping to `main2`; the config's leading comment survived the edit.
- **account rm**: refused the active account with exit `10`, exited `7` for an
  unknown account, named the touched profile, and `--dry-run` wrote nothing.
- **kae profile**: `set`/`default`/`save`/`unset`/`rm` round-tripped;
  `default_profile` was set and shown; removing the default refused without
  `--force`; unsetting a profile's last mapping removed it and cleared the
  default.
- **doctor orphan**: deferred per the committed discovery note (darwin keychain
  cannot enumerate by service via the `security` CLI); see
  [SECURITY.md](SECURITY.md).
- `mise run check` green; JSON kept `schema_version: 1`, stable tokens, `[]`
  arrays.

### v0.7.0 (2026-06-14, macOS darwin 24.6.0)

All acceptance criteria passed:

- **bond gate**: `kae bond side` wrote `.mise.toml` with CLAUDE_CONFIG_DIR →
  `isolation/<pin-id>/claude/bond`; dir contained `.credentials.json` at `0600`
  and symlinks for all other real-home items; `claude -p "say AUTH-OK"` returned
  AUTH-OK; `~/.claude.json` MD5 unchanged before and after.
- **Phase 3**: `kae use claude main --dry-run` showed exactly 1 action (keychain
  `/claudeAiOauth`); no `/oauthAccount` in output.
- **Phase 4**: `kae pin side` wrote pin-mode block
  (`isolation/<pin-id>/claude/pin/side/config`); legacy overlay-mode block
  triggered migration warning on stderr; `kae run --mode pin … -- /usr/bin/true`
  succeeded.
- **Phase 5 (bond)**: `kae as claude main` inside bonded dir printed "Switched …
  bond dir; sessions/settings unchanged".
- **Phase 5 (pin)**: `kae as claude main` inside pinned dir prepared
  `…/pin/main/config` and updated `.mise.toml` CLAUDE_CONFIG_DIR to the new
  account path.
