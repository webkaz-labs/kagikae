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
# shape (leading // comments + lastLoggedInUser/loggedInUsers/trustedFolders):
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
`TestCodexKeyringEmptyAccountRefused` / `TestKeychainReplaceUsesCapturedAccount`).
§D round-trip + recapture-preservation by `internal/cmd`
`TestAddRecordsIdentity*` / `TestRecapturePreservesIdentity`.

### v0.8.3 real-machine gate (required before release)

The driver-agnostic range is unit/temp-HOME covered above. Two surfaces need a
real machine — they exercise live subprocesses the temp-HOME smoke cannot fake:

**Codex keyring two-account round-trip** (macOS, real `Codex Auth` keychain).
Set `cli_auth_credentials_store = "keyring"` in `~/.codex/config.toml`, then:

- [ ] `kae add codex` (no name) on a live keyring login captures under the
      detected account (the `id_token` email / `account_id`), and `account.toml`
      records the opaque `keychain_account` (`cli|<opaque>`), not the payload.
- [ ] Log in as a second account; `kae add codex` it.
- [ ] `kae use codex <first>`: a fresh-process `codex login status` (or a
      `codex` run) reports logged in as the first account — the verbatim keyring
      round-trip restored it; exactly one `Codex Auth` item exists afterwards
      (`security find-generic-password -s "Codex Auth"` shows the first
      account's opaque id). **Settles the open discovery point** (service-only
      vs service+account match): record which it turned out to be.
- [ ] No token value ever appeared in `kae` output, `--json`, or `account.toml`.

**Cursor `kae add` identity** (macOS, live `cursor-agent` login):

- [ ] `kae add cursor` (no name) on a live `cursor-agent status` login captures
      under the sanitized detected email (local part).
- [ ] Logged out (or `cursor-agent status` unparseable): `kae add cursor` exits
      `64` naming the explicit form.

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
```

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
  [`KeychainReplace`/`JSONC` are absent too]; apply re-derives specs from the
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
  verbatim item with delete-then-add; `TestCodexKeyringEmptyAccountRefused`;
  `TestKeychainReplaceUsesCapturedAccount`). The two-account **real**-keychain
  gate (settling service-only vs service+account match) remains the one open
  acceptance item — run it per the "v0.8.3 real-machine gate" procedure before
  relying on the keyring driver in production.

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
