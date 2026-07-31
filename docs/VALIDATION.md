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
/tmp/kae add --no-login claude main          # "driver: claude-file-patch"
/tmp/kae use claude main --dry-run --json    # json-pointer action, no keychain
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
/tmp/kae add --no-login claude main
/tmp/kae use claude main --dry-run
/tmp/kae use claude main --json
/tmp/kae backup list --json
/tmp/kae rollback

# v0.2.0 surfaces:
/tmp/kae run claude main -- /usr/bin/true        # auth transaction + restore
echo sk-test | /tmp/kae env set claude ci ANTHROPIC_API_KEY
/tmp/kae env list --json
/tmp/kae run --env claude ci -- /usr/bin/env     # var visible to child only
/tmp/kae run -i claude main -- /usr/bin/true     # global isolated home, no lock, no live mutation
/tmp/kae mise init --profile main                  # preview, no write

# v0.4.0 surfaces (on macOS use codex-only profiles for live switching —
# see the keychain warning above; codex auth.json is file-based):
/tmp/kae use main --json
/tmp/kae use --json                                # idempotent (resolved profile); re-run: "changed": false
KAE_PROFILE=side /tmp/kae use --json               # env resolution
/tmp/kae use --quiet                               # prints nothing on success
/tmp/kae mise init --profile main --auto           # preview: [hooks.enter] kae use --quiet

# v0.5.0 surfaces (pin binds never mutate live state, so claude is safe
# to include in the pinned profile even on macOS):
/tmp/kae add --no-login codex main --json          # old capture shape
/tmp/kae use codex main --json                     # tool+account form
/tmp/kae pin side                                  # writes .config/mise/conf.d/kagikae.toml (kae-owned fragment)
#   assert: CODEX_HOME / CLAUDE_CONFIG_DIR entry in fragment pointing to
#   isolation/<pin-id>/<tool>/shared/ (shared mode) or
#   isolation/<pin-id>/<tool>/isolated/<account>/config/ (isolated mode)
#   assert: shared bind stores under $XDG_DATA_HOME/kagikae/isolation/<pin-id>/<tool>/shared/
#   assert: isolated bind stores under $XDG_DATA_HOME/kagikae/isolation/<pin-id>/<tool>/isolated/<account>/config/
#   assert: re-running pin is idempotent (fragment regenerated, no error)
/tmp/kae unpin                                     # removes only the block
/tmp/kae switch x y; echo $?                       # 64 + replacement pointer
EDITOR=true /tmp/kae edit                          # validate round-trip
/tmp/kae status --json                             # has "pinned" + "profiles"

# v0.7.0 surfaces (bond → pin --shared, per-directory isolation):
# codex: auth.json is file-based — safe on macOS.
# claude: on macOS CLAUDE_CONFIG_DIR suppresses keychain, so kae reads the
#   keychain credential bytes and writes them as .credentials.json into the
#   shared dir. Real-machine gate required (temp-HOME smoke cannot cover this).
/tmp/kae pin -s side                               # writes .config/mise/conf.d/kagikae.toml (shared mode)
#   assert: CODEX_HOME entry in fragment pointing to isolation/<pin-id>/codex/shared/
#   assert: config.toml symlinked from real ~/.codex; auth.json private-copied
#   assert: re-running kae pin -s is idempotent (no error, symlinks refreshed)

# v0.7.0 surfaces (pin -i mode):
/tmp/kae pin side                                  # writes fragment (isolated mode, default)
#   assert: CODEX_HOME entry pointing to isolation/<pin-id>/codex/isolated/main/config/
#   assert: no symlinks by default (full isolation); credential private-copied
#   assert: re-running kae pin is idempotent (fragment regenerated, no error)
#   assert: legacy overlay-mode block triggers migration warning on stderr
# Re-bind one tool inside a pinned directory:
/tmp/kae pin codex side
#   assert: only the codex entry in the fragment is updated; other tools unchanged
/tmp/kae switch x y; echo $?                       # 64 (renamed in v0.7.0, re-test)

# v0.6.0 surfaces (opencode auth.json is file-based — safe on macOS; seed
# $XDG_DATA_HOME/opencode/auth.json with {"openai":{...},"other":{...}}):
/tmp/kae add --no-login opencode main --json
/tmp/kae use opencode main --json
#   assert: the "other" sibling key in auth.json is untouched
/tmp/kae doctor --json                             # opencode checks present

# v0.7.1 surfaces (account lifecycle; config.toml comment-preserving edits):
#   seed a config.toml with a profile that references the account plus a
#   comment, then:
/tmp/kae account rm claude main; echo $?           # 10 if active (no --force)
/tmp/kae account rename codex main main2 --json    # rewrites profile refs
#   assert: config.toml comments and unrelated keys survive the edit
/tmp/kae account rm codex main2 --force --json     # drops active + profile ref
/tmp/kae account rm codex ghost; echo $?           # 7 (not_found)

# v0.7.1 surfaces (profile lifecycle; same comment-preserving writer):
/tmp/kae profile set dev codex main2               # creates/updates a mapping
/tmp/kae profile default dev                       # sets default_profile
/tmp/kae profile default                           # prints the current default
/tmp/kae profile save snapshot                     # from the active accounts
/tmp/kae profile rm dev; echo $?                   # 10 if default (no --force)
/tmp/kae profile unset dev codex                   # last mapping removes profile
#   assert: comments survive; default_profile cleared when its profile is removed

# copilot is config.json-pointer based (kae never touches the keychain
# tokens), so it is safe on macOS; seed ~/.copilot/config.json with the JSONC
# shape (leading // comments + lastLoggedInUser/loggedInUsers/trustedFolders).
# `unset COPILOT_HOME` first: it outranks the temp HOME, so a smoke run with it
# still set would patch the real config (same for CLAUDE_CONFIG_DIR/CODEX_HOME):
/tmp/kae add --no-login copilot main --json
/tmp/kae use copilot main --json
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
hook-env) and assert `kae use --quiet` fired and that re-entry adds no backup.

Use `secret_backend = "file"` in the temp config for smoke checks so no real
keychain entries are created.

## v0.8.0 surfaces

All checks use the same temp-HOME and file-backend setup as the blocks above.
**macOS keychain safety rules are unchanged** — use `KAE_CLAUDE_DRIVER=file`
and `secret_backend = "file"` throughout.

```bash
go build -o /tmp/kae .
export HOME=$(mktemp -d)
export XDG_CONFIG_HOME=$HOME/.config XDG_DATA_HOME=$HOME/.local/share \
       XDG_STATE_HOME=$HOME/.local/state NO_COLOR=1
export KAE_CLAUDE_DRIVER=file
mkdir -p "$XDG_CONFIG_HOME/kagikae"
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
      records the derived `keychain_account` (`cli|` + 16 hex), not the payload.
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
as a best-effort generator (unit-tested and `fish -n`-valid) but is **not** a
supported/release-gated surface. bash and zsh are the verified shells.

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
  `TestCompletionAccountTokenIndex` (positionals are flag-filtered; the
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

## v0.15.0 surfaces — credential lead time, inventory freshness, bound directories

All three are read-only reporting, so the whole block runs against a temp HOME with
the file driver and the file backend — no real `$HOME`, no real keychain. Deadlines
are computed from `date +%s` so the fixtures stay valid whenever this is re-run.

```bash
S=$(mktemp -d); export HOME="$S/home" \
  XDG_CONFIG_HOME="$S/home/.config" XDG_DATA_HOME="$S/home/.local/share"
mkdir -p "$HOME/.claude" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME"
export KAE_CLAUDE_DRIVER=file
/tmp/kae init
printf 'default_profile = "main"\n[security]\nsecret_backend = "file"\n[profiles.main]\naccounts = { claude = "main" }\n' \
  > "$XDG_CONFIG_HOME/kagikae/config.toml"
NOW=$(date +%s); SOON=$(( (NOW + 3*86400) * 1000 )); FAR=$(( (NOW + 60*86400) * 1000 ))
cred() { printf '{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":%s}}' "$1" \
  > "$HOME/.claude/.credentials.json"; }

# --- A. the three bands, on account snapshots ---
cred "$SOON";                 /tmp/kae add --no-login claude soon
cred "$FAR";                  /tmp/kae add --no-login claude healthy
cred 1609459200000;           /tmp/kae add --no-login claude dead   # refresh expired 2021
/tmp/kae doctor claude --json
#   assert: {code:"credential_expiring", status:"warn"} for "soon", naming the day
#           count and `kae add --restore claude soon`
#   assert: {code:"credential_stale",    status:"warn"} for "dead"
#   assert: NO credential_* check for "healthy" (a month out stays silent)
/tmp/kae doctor claude --json >/dev/null; echo "exit=$?"   # assert: 0 (warn never fails)

# --- B. the inventory column (ls / accounts / status) ---
/tmp/kae ls --no-color        # assert: a Credential column reading
                              #   dead=re-login now, healthy=ok, soon=N day(s) left
/tmp/kae ls --json
#   assert: schema_version still 1; each row has additive credential + relogin_by
#   assert: relogin_by parses as RFC3339 and is the *refresh* deadline, not expiresAt

# --- C. a bound directory's own credential (the sweep) ---
cred "$FAR"; /tmp/kae add --no-login claude main
P="$S/project"; mkdir -p "$P"; cd "$P"; /tmp/kae pin main
STORE=$(find "$XDG_DATA_HOME/kagikae/isolation" -name '.credentials.json' | head -1)
/tmp/kae doctor --json     # assert: NO credential_* check (the copy is healthy)
printf '{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":1609459200000}}' > "$STORE"
/tmp/kae doctor --json
#   assert: {code:"credential_stale"} whose message says `bound to <P>` and
#           `cd <P> && claude /login` — and NOT `kae pin` / `kae add`
#   assert: the healthy claude/main *snapshot* is not reported alongside it
/tmp/kae unpin
/tmp/kae doctor --json     # assert: silent again — unpin keeps the store on
                           # purpose, so nothing points at it and "bound to" would lie

# --- D. kae pin still writes a real store path in both modes (modeStoreDir) ---
cd "$P" && /tmp/kae pin main    && grep CLAUDE_CONFIG_DIR .config/mise/conf.d/kagikae.toml
cd "$P" && /tmp/kae pin -i main && grep CLAUDE_CONFIG_DIR .config/mise/conf.d/kagikae.toml
#   assert: non-empty, ending .../claude/shared and .../claude/isolated/main/config
#           respectively, and both directories exist
```

**PASSED 2026-07-31** on the pre-release binary: A, B, C and D all as asserted.

Two things this block deliberately pins, because each is a failure that looks like
success:

- **`credential_expiring` must be silent for a healthy account.** The seven-day
  lead time is only useful while it is a minority of the credential's life; if it
  ever fires for every account, the check is worse than absent. See the claude row
  in "Upstream Behaviour Assumptions" for the upstream condition it rides on.
- **The unpinned case must be silent.** `kae unpin` keeps the store, so the
  breadcrumb still names the directory — reporting its credential would name a
  directory that is not bound and a remedy that lands where nothing reads. The
  sweep therefore reads the mise **fragment**, not the store tree.

## companion-auth surfaces

Companion-auth lockstep (`kae companion`, delivered per-directory by `kae pin`).
Smoke against a temp HOME with the file backend; the `exec()` token path needs
`mise trust` (the same step any pin fragment needs):

```bash
# config: [security] secret_backend = "file" + a profile, e.g. [profiles.main]
printf '[alias]\n\tlol = log --oneline\n[user]\n\temail = real@personal.test\n' > "$HOME/.gitconfig"
/tmp/kae companion add main git email=you@example.com name=You
/tmp/kae companion add main kubectl KUBECONFIG="$HOME/.kube/main"
printf 'ghp_smoke\n' | /tmp/kae companion add main gh GH_TOKEN
/tmp/kae companion list
#   assert: gh shows GH_TOKEN=(secret); config.toml holds no token plaintext
/tmp/kae __companion-token main gh GH_TOKEN        # prints ghp_smoke (helper path)
cd "$proj" && /tmp/kae pin main                    # writes the fragment
mise trust .config/mise/conf.d/kagikae.toml
#   assert: fragment has redactions = ["GH_TOKEN"], GH_TOKEN as {{ exec(...) }},
#           GIT_CONFIG_GLOBAL + KUBECONFIG as paths, no token plaintext
mise exec -- git config --get user.email           # you@example.com (override)
mise exec -- git config --get alias.lol            # log --oneline (~/.gitconfig preserved)
mise exec -- sh -c 'echo $GH_TOKEN'                # ghp_smoke (resolved at eval)
git config --get user.email                        # real@personal.test (unpinned: unchanged)
/tmp/kae doctor --json                             # no companion_missing when secrets stored
#   companion_drift: present here (pin not active in this shell → git resolves
#   real@personal.test, not the bound you@example.com)
mise exec -- /tmp/kae doctor --json                # no companion_drift (active pin → git matches the binding)
```

`companion_token_drift` is opt-in and needs a network call, so the temp-HOME
smoke does not exercise it (the `ghp_smoke` token is invalid, so `kae companion
add` records no `expected_login` — a gentle skip; unit tests cover the probe with
a faked `gh`). To check it on a real machine: `printf '<real-token>\n' | kae
companion add main gh GH_TOKEN` (records `expected_login` from `gh api user`),
then in the pinned dir `mise exec -- kae doctor --yes` reports a match, and
`kae doctor --yes` outside the pin reports the inactive-pin warn.

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
  applied for the active account. Catches an assumption that has *already* broken.
- `upstream_version` — the installed tool is a newer major/minor than the
  `VerifiedVersion()` its adapter declares. Flags the release where one *could*
  have broken, before it costs a wrong-account session. Patch bumps stay silent,
  and **cursor is exempt**: its date version would make every new build month look
  like a minor bump, so its adapter declares `""` and doctor never reports it
  (docs/ADAPTERS.md "Verified Upstream Versions"). Cursor's rows are therefore due
  on the release acceptance run, not on a doctor warning.

A warning from either means: work that tool's rows below, then update the
adapter's `VerifiedVersion()` and the version recorded here **in the same
commit**. Verifying an assumption always means launching a **fresh** tool process
— a still-running session and a byte-compared payload both prove nothing.

### claude (verified on 2.1.220)

| Assumption | How to verify |
|---|---|
| The credential alone authenticates: applying `/claudeAiOauth` (keychain payload or `.credentials.json`) is the whole login | `kae use claude <acct>`, then `claude -p "say AUTH-OK" </dev/null` in a **new** process returns a reply, not "Not logged in" |
| `/oauthAccount`'s self-heal is **TTL-gated**: claude refetches the profile and rewrites `emailAddress` only when the cached object is incomplete or its `profileFetchedAt` is over **24h** old, and a token refresh renews that timestamp without rewriting `emailAddress` / `accountUuid` | `kae use claude <other>` with a snapshot captured **within** 24h, launch claude, diff `~/.claude.json`: `oauthAccount.emailAddress` is the value kae wrote and does **not** revert. Then age it (`profileFetchedAt` older than 24h) and launch claude again: it now refetches and rewrites `emailAddress` + `profileFetchedAt` on its own. If the TTL ever stops applying, kae's identity switch becomes redundant (not harmful) — record that here rather than dropping the artifact silently |
| `claude /login` rewrites `accountUuid` / `emailAddress` / `organizationUuid` unconditionally (no TTL), and a token **refresh** rewrites none of them | Log in to another account with `claude /login`, diff `~/.claude.json`: those three keys change. Let a session run long enough to refresh the token and diff again: `profileFetchedAt` and the plan fields change, those three do not. kae's `IdentityKeys` (the keyed identity comparison) is exactly this set — if a refresh starts rewriting them, `identity_drift` will warn on correct switches again |
| A **refresh token** carries its own expiry in `refreshTokenExpiresAt`, and each refresh mints a new one — so the number is a **rolling window**, not the credential's life (Claude Code itself warns inside the last 3 days) | Read `refreshTokenExpiresAt` from a fresh login's credential and subtract `expiresAt`'s date: measured ≈2 days on 2.1.220 (it was ≈1 month earlier). **Do not read that 2 days as the time until a re-login**: because a refresh renews the token, a credential in regular use stays alive far longer, and the operator reports the real cadence as roughly a month with `kae doctor` staying quiet (confirmed 2026-07-31). Two kae behaviours ride on this. The "recoverable without a re-login" predicate needs the field to exist at all: if it disappears, kae falls back to presence alone and under-warns. And `credential_expiring`'s **seven-day lead time** assumes the effective lifetime is comfortably longer than seven days — if a release ever made the window genuinely short and non-renewing, that check would be permanently lit for every claude account, which is worse than not having it. Re-measure by taking `refreshTokenExpiresAt` from a credential that has been in daily use for a week, not from a fresh login |
| A refresh that fails with `invalid_grant` makes claude **tombstone** the credential in place: `accessToken: ""`, `refreshToken: ""`, `expiresAt: 0` | Let a credential's refresh token expire, run claude, then read the credential: it is blanked rather than left alone. kae reads that as invalid, not as "no expiry recorded"; if upstream instead deletes the item, the logged-out guards cover it |
| The keychain payload must round-trip **verbatim**; a re-serialized payload makes Claude Code reject the credential | Capture → apply → fresh-process auth check on macOS with the real keychain driver. A byte-compare of the stored payload does not cover it: an equivalent-but-re-encoded payload is exactly this failure |
| `~/.claude.json` is mixed state whose other keys must survive a pointer patch | `git`-diff `~/.claude.json` across a switch: only `/oauthAccount` changes; `projects`, `mcpServers`, onboarding and cache keys stay byte-identical |
| **Where the credential resolves to** is a rule, not a constant. The keychain service is `Claude Code` + the build's OAuth suffix + `-credentials` + a per-config-dir suffix. That last suffix is empty only while `CLAUDE_CONFIG_DIR` is unset or empty; otherwise it is `-<first 8 hex of sha256(value)>` over the env string **NFC-normalized** — no `resolve`, no cleaning, so a trailing `/` hashes to a different item, and a decomposed non-ASCII component hashes as its composed form. Reads try keychain first and fall back to `<configDir>/.credentials.json`; a write goes to keychain and **deletes that file** when the item was previously absent. `CLAUDE_SECURESTORAGE_CONFIG_DIR`, when set, replaces `CLAUDE_CONFIG_DIR` as both the hash input and the file's directory — and set to the *empty string* it drops the suffix entirely, collapsing every config dir onto one shared item | Shim `security` rather than logging in. Put an executable ahead of `/usr/bin` on `PATH` that appends `"$*"` to a log and exits 44 (item-not-found), then run `env -i HOME=<temp> PATH=<shim>:/usr/bin:/bin USER="$USER" CLAUDE_CONFIG_DIR=<dir> claude -p hi </dev/null`. The logged `-s <service>` is the name claude actually resolves; check the suffix against `python3 -c 'import hashlib,sys,unicodedata;print(hashlib.sha256(unicodedata.normalize("NFC",sys.argv[1]).encode()).hexdigest()[:8])' <dir>` — the NFC step is not optional, and a `<dir>` with a decomposed non-ASCII component is the case that needs it. Re-run with a trailing slash, with `CLAUDE_SECURESTORAGE_CONFIG_DIR=`, and with it set to another dir to cover all four branches. **No login and no real-keychain access**, so this row is re-verifiable in seconds on any macOS machine — prefer it to the old seed-and-wait-for-refresh procedure. The delete-on-write half is the exception: it is read from the composed store's `update()` in the bundle (`if the keychain read returned absent, delete the plaintext file`) and reproducing it at runtime needs a live refresh token, so treat that half as **source-confirmed, not run-confirmed**. kae reproduces the rule in `claude.keychainService`, so every mechanism that sets `CLAUDE_CONFIG_DIR` writes into the item that config dir resolves to. Recording *storage resolution* as a verifiable rule is the lesson: kae had verified "the credential is at X" and never "how the tool decides where X is", and modelling the name as a constant is exactly what let a pinned directory run the previous account with every offline guard green |
| The **OAuth suffix** renames *both* stores, not just the keychain one: the service is `Claude Code<suffix>-credentials[-<sha8>]` **and** the identity file is `.claude<suffix>.json` in the same config dir. The suffix is `-custom-oauth` whenever `CLAUDE_CODE_CUSTOM_OAUTH_URL` is **non-empty** (an empty value is falsy and changes nothing; an *unapproved* endpoint makes claude throw rather than fall back), otherwise it comes from the build channel — `""` production, `-local-oauth`, `-staging-oauth` — which a released binary hard-codes to production, so the environment can only ever produce `""` or `-custom-oauth` | Source-read from the installed bundle, 2026-07-30 on 2.1.220, no login and no keychain access. The bundle is one Mach-O with the JS inline, so read it by offset: `B=~/.local/share/claude/versions/<version>`, `grep -oab -- '-custom-oauth' "$B" \| cut -d: -f1`, then `dd if="$B" bs=1 skip=<offset-400> count=1400 2>/dev/null \| LC_ALL=C tr -c '\11\12\15\40-\176' '.'`. A `grep -oa` with a wide context pattern **times out** on 266 MB and `strings` is useless (one giant string) — offsets then `dd` is the technique. Three sites must agree: the suffix function (`if (process.env.CLAUDE_CODE_CUSTOM_OAUTH_URL) return "-custom-oauth"`, else `switch` on a channel function that `return"prod"`), the service assembly (`` return `Claude Code${…OAUTH_FILE_SUFFIX}${"-credentials"}${o}` ``, at the `"-credentials"` literal), and the identity path (`` let e=`.claude${…}.json` ``, plus a loop over all four suffixes). Cross-check with the shim run above and `CLAUDE_CODE_CUSTOM_OAUTH_URL` set. kae **refuses** claude (exit `5`) on a non-empty value rather than computing the suffix, because the build-channel half is not visible from the environment at all — a recorded gap (docs/ROADMAP.md), and modelling one of the three sources would stay silently wrong for the other two |
| A **host-managed provider** is a third credential source: with `CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST` truthy, claude reads the JSON file `CLAUDE_CODE_HOST_CREDS_FILE` names — rejecting it unless it is absolute, ≤64 KiB, owned by the caller, not group/other-readable, and carries a live `pid`/`procStart` and an unexpired `expiresAt` — and injects its `env` entries, with the token landing in the variable `CLAUDE_CODE_HOST_AUTH_ENV_VAR` names (default `ANTHROPIC_AUTH_TOKEN`). `ANTHROPIC_UNIX_SOCKET` marks the same host-managed mode | Same offset-read technique at the `CLAUDE_CODE_HOST_CREDS_FILE` literal (2.1.220): one function validates and parses the file, another gates on the truthy `…MANAGED_BY_HOST` and writes `process.env`. kae cannot name the destination variable when the host renames it, so it warns on the four mechanism variables instead (docs/ADAPTERS.md "Environment conflicts"). A warning, not an unsupported refusal — this moves what authenticates, not where kae writes |
| A **relative** `CLAUDE_CONFIG_DIR` is used verbatim: claude resolves it against its own working directory, so kae (invoked from anywhere in the project) reads and writes different *files* — but **not** a different keychain item, because the service name hashes the variable's raw value rather than a resolved path | Read the resolver out of the bundle: `grep -oab -- 'env.CLAUDE_CONFIG_DIR' "$B"` then a `dd` window at each offset (technique in the row above). At 2.1.220 it is `AIl(){return process.env.CLAUDE_CONFIG_DIR}` and `fn = Vr(() => (AIl() ?? join(homedir(), ".claude")).normalize("NFC"), AIl)` — NFC and no path resolution — with the identity path `join(process.env.CLAUDE_CONFIG_DIR \|\| homedir(), ".claude<suffix>.json")`. Corroborating literal in the same bundle: a subprocess's value must match the parent's "same path, same separators". kae warns (`env_conflict`) and keeps honoring the value |
| The keychain item's **account attribute** is `$USER` when it matches `^[a-zA-Z0-9._-]+$`, otherwise the OS username, and the literal `claude-code-user` when neither is usable | The shim log's `-a <account>` names it. It is load-bearing because claude's reads are account-scoped (`find-generic-password -a <account> -s <service>`): an item kae writes under a different account attribute is invisible to claude even when the service name matches exactly |

### Other tools

| Tool | Assumption | How to verify | Verified on |
|---|---|---|---|
| codex | `auth.json` holds only auth state, so the whole file may be swapped | switch, then `codex login status` in a fresh process names the applied account; `config.toml` and history are untouched | 0.145.0 |
| codex | the `Codex Auth` keyring item is identified by service **and account**, where the account is a **rule**: `cli\|` + the first 16 hex chars of `sha256(canonical CODEX_HOME)` (symlink-resolved, absolute — codex canonicalizes the path before hashing, and refuses to start when it does not resolve). One service therefore holds **one item per codex home**, all legitimate, and codex's own delete is service+account scoped | Read `codex-rs/login/src/auth/storage.rs` (`compute_store_key`, `DirectKeyringAuthStorage::delete`) at the tag matching `codex --version` — codex is public, so this is a file read, not a measurement. Beware: `compute_store_key` exists in **two** modules and symbol-grepping the stripped binary finds the MCP-OAuth one first. To confirm against a live item without a real login: `printf 'sk-not-a-real-key' \| CODEX_HOME=<temp> codex login --with-api-key` (a purely local write, no network), then `security find-generic-password -s "Codex Auth" -a "cli\|$(printf '%s' "$(python3 -c 'import os,sys;print(os.path.realpath(sys.argv[1]))' <temp>)" \| shasum -a 256 \| cut -c1-16)"` — attributes only, never `-w`. Clean up with `CODEX_HOME=<temp> codex logout` (its own scoped delete), not `security delete-generic-password`. Modelling the account as an opaque per-login id made a kae switch delete another `CODEX_HOME`'s login. **Also confirmed for a path reached through a symlink** (2026-07-30), which is the case that matters for a bond dir: with `CODEX_HOME` set to `<tmp>/link/shared/<pinID>/codex` where `link -> real`, codex created the item under the account of the **resolved** path and the raw-path account was absent — so kae's `EvalSymlinks` step is required, not defensive | 0.145.0 (source + live item, 2026-07-30) |
| codex | a **relative** `CODEX_HOME` is canonicalized against **codex's own** working directory before use, and that canonical path is what the keyring account hashes — so it moves the `auth.json` store *and* the `Codex Auth` item, unlike claude's variable which moves only files. codex refuses to start when the relative path does not exist in its working directory | Login-free, temp dirs only: `T=$(mktemp -d); mkdir -p "$T/relcfg"; printf 'this is not = valid toml [[[\\n' > "$T/relcfg/config.toml"`, then `cd "$T" && CODEX_HOME=relcfg codex login status` reports the parse error against `/private/var/.../relcfg/config.toml` (resolved against codex's cwd, symlink-resolved), while the same command from a sibling directory with no `relcfg` fails with `CODEX_HOME points to "relcfg", but that path does not exist`. kae warns (`env_conflict`) | 0.145.0 (behavioural, 2026-07-31) |
| codex | `cli_auth_credentials_store` is the enum `file` (**the default for an absent key**) \| `keyring` \| `auto` \| `ephemeral`, and `auto` reads the keyring **first**, falling back to `auth.json` only when the item is absent or unreadable. A successful keyring write **deletes** the `auth.json` fallback. `[features] secret_auth_storage` (default: on only on Windows) swaps the keyring backend for an encrypted secrets file, so the credential is not in the item at all | Same file plus `codex-rs/config/src/types.rs` (`AuthCredentialsStoreMode`, `#[default] File`) and `AutoAuthStorage::load`. The delete-the-file half is also live-confirmed: after `codex login --with-api-key` under a keyring store, `CODEX_HOME/auth.json` is gone. Treating everything that is not `keyring` as the file store is what would let kae write `auth.json` while codex reads the item — the failure shape that shipped for claude's per-directory keychain item | 0.145.0 (source + live, 2026-07-30) |
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
  (`TestCompletionAccountTokenIndex` + `fish -n`), not a release-gated surface.
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
