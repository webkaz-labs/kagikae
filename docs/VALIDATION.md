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
/tmp/kae run --env claude ci -- /usr/bin/env     # var visible to child only
/tmp/kae run -i claude work -- /usr/bin/true     # global isolated home, no lock, no live mutation
/tmp/kae mise init --profile work                  # preview, no write

# v0.4.0 surfaces (on macOS use codex-only profiles for live switching —
# see the keychain warning above; codex auth.json is file-based):
/tmp/kae use work --json
/tmp/kae use --json                                # idempotent (resolved profile); re-run: "changed": false
KAE_PROFILE=personal /tmp/kae use --json           # env resolution
/tmp/kae use --quiet                               # prints nothing on success
/tmp/kae mise init --profile work --auto           # preview: [hooks.enter] kae use --quiet

# v0.5.0 surfaces (pin binds never mutate live state, so claude is safe
# to include in the pinned profile even on macOS):
/tmp/kae add --no-login codex work --json          # old capture shape
/tmp/kae use codex work --json                     # tool+account form
/tmp/kae pin clientA                               # writes .config/mise/conf.d/kagikae.toml (kae-owned fragment)
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
/tmp/kae pin -s clientA                            # writes .config/mise/conf.d/kagikae.toml (shared mode)
#   assert: CODEX_HOME entry in fragment pointing to isolation/<pin-id>/codex/shared/
#   assert: config.toml symlinked from real ~/.codex; auth.json private-copied
#   assert: re-running kae pin -s is idempotent (no error, symlinks refreshed)

# v0.7.0 surfaces (pin -i mode):
/tmp/kae pin clientA                               # writes fragment (isolated mode, default)
#   assert: CODEX_HOME entry pointing to isolation/<pin-id>/codex/isolated/work/config/
#   assert: no symlinks by default (full isolation); credential private-copied
#   assert: re-running kae pin is idempotent (fragment regenerated, no error)
#   assert: legacy overlay-mode block triggers migration warning on stderr
# Re-bind one tool inside a pinned directory:
/tmp/kae pin codex personal
#   assert: only the codex entry in the fragment is updated; other tools unchanged
/tmp/kae switch x y; echo $?                       # 64 (renamed in v0.7.0, re-test)

# v0.6.0 surfaces (opencode auth.json is file-based — safe on macOS; seed
# $XDG_DATA_HOME/opencode/auth.json with {"openai":{...},"other":{...}}):
/tmp/kae add --no-login opencode work --json
/tmp/kae use opencode work --json
#   assert: the "other" sibling key in auth.json is untouched
/tmp/kae doctor --json                             # opencode checks present

# v0.7.1 surfaces (account lifecycle; config.toml comment-preserving edits):
#   seed a config.toml with a profile that references the account plus a
#   comment, then:
/tmp/kae account rm claude work; echo $?           # 10 if active (no --force)
/tmp/kae account rename codex work work2 --json    # rewrites profile refs
#   assert: config.toml comments and unrelated keys survive the edit
/tmp/kae account rm codex work2 --force --json     # drops active + profile ref
/tmp/kae account rm codex ghost; echo $?           # 7 (not_found)

# v0.7.1 surfaces (profile lifecycle; same comment-preserving writer):
/tmp/kae profile set dev codex work2               # creates/updates a mapping
/tmp/kae profile default dev                       # sets default_profile
/tmp/kae profile default                           # prints the current default
/tmp/kae profile save snapshot                     # from the active accounts
/tmp/kae profile rm dev; echo $?                   # 10 if default (no --force)
/tmp/kae profile unset dev codex                   # last mapping removes profile
#   assert: comments survive; default_profile cleared when its profile is removed

# copilot is config.json-pointer based (kae never touches the keychain
# tokens), so it is safe on macOS; seed ~/.copilot/config.json with the JSONC
# shape (leading // comments + lastLoggedInUser/loggedInUsers/trustedFolders):
/tmp/kae add --no-login copilot work --json
/tmp/kae use copilot work --json
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
printf '{"claudeAiOauth":{"accessToken":"tok-work"}}' \
  > "$HOME/.claude/.credentials.json"
/tmp/kae init
/tmp/kae add --no-login claude work
/tmp/kae add --no-login codex personal

# --- A. apply fold: bare kae use idempotency ---
/tmp/kae use --json
#   assert: JSON contains "changed": true (first apply; switches to default profile)
/tmp/kae use --json
#   assert: JSON contains "changed": false (already active; no lock, no backup)
/tmp/kae use --quiet
#   assert: no output (silent on success)
KAE_PROFILE=personal /tmp/kae use --json
#   assert: JSON shows resolution via KAE_PROFILE env var
/tmp/kae use -P work --json
#   assert: JSON shows -P flag resolution
/tmp/kae apply x; echo $?
#   assert: exit 64; output names "kae use [--quiet]" as the replacement

# --- B. run redesign (-s / -i / --env; --mode removed) ---
/tmp/kae run -i claude work -- /usr/bin/true
#   assert: process ran in isolation/global/claude/work/
#   assert: output names the shared home ("shared with kae use -i work")
#   assert: no per-tool lock held (concurrent kae use in another shell must not be blocked)
#   (concurrency check: open a second shell, run "kae use claude work" while run -i is
#    executing a long-running child; it must complete without waiting)
/tmp/kae run claude work -- /usr/bin/true
#   assert: run -s (default): auth transaction + restore; lock held during child
/tmp/kae run --env claude work -- /usr/bin/env
#   assert: profile env vars visible to child; no home redirect; no lock
/tmp/kae run --mode env claude work -- /usr/bin/true; echo $?
#   assert: usage error / non-zero exit (--mode flag removed in v0.8.0)

# --- C. mise init trim (bond/pin modes rejected; auth renders kae use --quiet hook) ---
/tmp/kae mise init --profile work
#   assert: preview shows [tasks] block; no [env] tool-home entries
/tmp/kae mise init --profile work --auto
#   assert: preview shows [hooks.enter] with "kae use --quiet" (not "kae apply ...")
/tmp/kae mise init --profile work --mode auth
#   assert: identical to --auto preview (explicit --mode auth is accepted)
/tmp/kae mise init --profile work --mode bond; echo $?
#   assert: rejected (non-zero exit); error names kae pin as replacement
/tmp/kae mise init --profile work --mode pin; echo $?
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
/tmp/kae profile set multi claude work
/tmp/kae profile set multi agy work  2>/dev/null || true   # agy may not be captured; that's fine
/tmp/kae use -i multi; echo $?
#   assert: exit 0; claude isolated; agy skipped with a warning on stderr
/tmp/kae use -i agy work; echo $?
#   assert: exit 5 (single-tool agy isolation is unsupported)

# --- F. input ergonomics ---
/tmp/kae use cl work --json
#   assert: "cl" prefix resolves to "claude"; JSON shows canonical tool name
/tmp/kae use cod personal --json
#   assert: "cod" prefix resolves to "codex"; JSON shows canonical tool name
/tmp/kae use c work; echo $?
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
printf '{"claudeAiOauth":{"accessToken":"tok-work-1"}}' > "$HOME/.claude/.credentials.json"
/tmp/kae add --no-login claude work
printf '{"claudeAiOauth":{"accessToken":"tok-personal"}}' > "$HOME/.claude/.credentials.json"
/tmp/kae add --no-login claude personal
/tmp/kae use claude work
printf '{"claudeAiOauth":{"accessToken":"tok-work-2"}}' > "$HOME/.claude/.credentials.json"  # in-tool refresh
/tmp/kae use claude personal     # stderr: "refreshed claude/work snapshot ... before switching away"
/tmp/kae use claude work
grep -q tok-work-2 "$HOME/.claude/.credentials.json"   # assert: refreshed token came back, not tok-work-1

# --- B. switch to an expired snapshot with no refresh token warns (still proceeds) ---
printf '{"claudeAiOauth":{"accessToken":"old","refreshToken":"","expiresAt":1577836800000}}' \
  > "$HOME/.claude/.credentials.json"
/tmp/kae add --no-login claude stale
/tmp/kae use claude personal
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
#   assert: usage error (64); message names "kae add agy <account>" (agy has no identity)
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
printf '{"oauthAccount":{"emailAddress":"work@example.com"}}' > "$HOME/.claude.json"
/tmp/kae add --no-login claude --json
#   assert: captured account "work"; account.toml + --json carry identity "work@example.com"
/tmp/kae add --no-login claude chosen --json
#   assert: explicit name "chosen"; identity still recorded "work@example.com" (best-effort)
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

## Release Acceptance Log

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

- **file-driver override**: with `KAE_CLAUDE_DRIVER=file`, `kae use claude work
  --dry-run` reported a `json-pointer` action on `~/.claude/.credentials.json`
  (driver `claude-file-patch`); unset, `kae status` reported
  `claude-keychain-patch` (no regression). `kae add`/`use` round-tripped on
  files with no `security` subprocess.
- **account rename**: `kae account rename claude work work2` moved the snapshot,
  copy+deleted the secret, set `active_updated`, and rewrote the referencing
  profile's mapping to `work2`; the config's leading comment survived the edit.
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
