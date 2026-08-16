# Validation

## Standard Suite (before every commit)

```bash
mise run check     # see mise.toml [tasks.check] for what it depends on
git diff --check
```

`mise run check` is the authoritative gate. **Its steps are not enumerated here**:
`mise.toml`'s `[tasks.check]` `depends` list is the one copy, and three hand copies of
it had already drifted — this line omitted `smoke-selftest` for as long as it existed,
and `AGENTS.md` and `README.md` each carried a third version. Read the task.

CI is a **subset**, not a mirror: `.github/workflows/check.yml`'s own steps are the one
copy of which of those steps run there, and everything else is enforced on a developer's
machine only. This line carried a second copy of that list until 2026-08-14, directly
under the paragraph above making the same point about the local gate's own list
([ROADMAP.md](ROADMAP.md) carries what widening
it further would cost, and `check.yml`'s header why the docs selftest stayed out while
its check went in).

Slower release-time checks live in `mise run audit` (govulncheck) and
`mise run goreleaser-check`. Lint tools run via `go run <tool>@<pinned version>`; the
first run downloads them.

Run `go mod tidy` before committing dependency changes.

## Smoke Checks (built binary, isolated env)

**Every code block in this file assumes `. scripts/smoke-env.sh` is already in
effect in the current shell.** They seed fixtures by writing to whatever `$HOME` and
`$XDG_*` path the surface under test reads — kae's own `config.toml` and `state.json`,
a tool's credential file, an identity file such as `~/.claude.json` or
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
the unit tests over the darwin sim and the real-machine gates in
[ACCEPTANCE.md](ACCEPTANCE.md).

Enter-hook firing (`mise init --auto --write`) needs a live mise:
`mise settings experimental=true` (hooks are experimental; the global config
this writes must itself be `mise trust`-ed), `mise trust` on the project
`.mise.toml`, and a shell with `mise activate`. In a temp-HOME smoke, point
`ZDOTDIR` at a temp dir whose `.zshrc` exports PATH and evals
`mise activate zsh`, then run `zsh -i -c 'cd <project> && true'` from a
neutral directory (the repo's own untrusted mise.toml otherwise aborts
hook-env) and assert `kae use --quiet` fired and that re-entry adds no backup.

## Lead time, inventory freshness and bound directories

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
/tmp/kae doctor claude --json > "$HOME/A.json"; test $? -eq 0
                                                            # assert: exit 0 — a warn
                                                            #  never fails. Taken from
                                                            #  the call above rather than
                                                            #  a second run of it
grep -q '"code": "credential_expiring"' "$HOME/A.json"      # assert: "soon" is reported,
grep -qF 'snapshot \"soon\" needs an interactive re-login in' "$HOME/A.json"
grep -q 'kae add --restore claude soon' "$HOME/A.json"      #  named with its remedy
grep -qE 'needs an interactive re-login in [0-9]+ day' "$HOME/A.json"
                                                            #  and with its day count
grep -qF 'snapshot \"dead\" is stale' "$HOME/A.json"           # assert: credential_stale
test "$(grep -c healthy "$HOME/A.json")" -eq 0              # assert: NO credential_* check
                              #   for "healthy" — the notice must be silent for most of a
                              #   login's life, or it is wallpaper. The four greps above
                              #   are this line's positive control: an empty report would
                              #   satisfy a lone absence check

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
go build -o /tmp/kae .
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
cd "$W/main" && /tmp/kae pin main > "$HOME/A.out" 2>&1
grep -q 'ignored via ~/work/main/.git/info/exclude' "$HOME/A.out"
                                       # assert: the report names the exclude file
test -z "$(git status --porcelain)"    # assert: empty
test -z "$(git -C "$W/wt1" status --porcelain)"
                                       # assert: empty — one entry already covers it
test ! -e "$W/main/.gitignore"         # assert: no tracked .gitignore was created

# --- B. the linked worktree binds independently and stays clean ---
# kae prints this one ABSOLUTE where A printed `~`: it resolves $GIT_COMMON_DIR, which
# git answers canonically (/private/var/... on macOS), and that no longer has $HOME as a
# prefix to abbreviate. Measured; assert what it prints, not what A printed.
MAINP=$(cd "$W/main" && pwd -P)
cd "$W/wt1" && /tmp/kae pin main > "$HOME/B.out" 2>&1
grep -q "ignored via $MAINP/.git/info/exclude" "$HOME/B.out"
                                       # assert: the file named is the MAIN checkout's
test -z "$(git -C "$W/main" status --porcelain)"   # assert: both empty
test -z "$(git status --porcelain)"

# --- C. a nested directory: the entry is anchored at the repository root ---
cd "$W/main/nested" && /tmp/kae pin main
grep -q '^/nested/\.config/mise/conf\.d/kagikae\.toml$' "$W/main/.git/info/exclude"
                                       # assert: anchored at the repository root, not at
                                       #   its own directory — without --show-prefix this
                                       #   would read `/.config/…` and ignore nothing
test -z "$(git -C "$W/main" status --porcelain)"   # assert: empty

# --- D. outside any repository: no rule, and no claim of one ---
mkdir -p "$HOME/norepo"; cd "$HOME/norepo" && /tmp/kae pin main > "$HOME/D.out" 2>&1
grep -q 'your mise.toml is untouched' "$HOME/D.out"   # assert: it still reports the write
test "$(grep -c 'ignored via' "$HOME/D.out")" -eq 0
                                       # assert: and claims no ignore rule. The line
                                       #   above is this one's positive control
test ! -e "$HOME/norepo/.gitignore"

# --- E. kae ls --pins, from outside every bound directory ---
cd "$HOME" && /tmp/kae ls --pins > "$HOME/E1.txt"
test "$(grep -oE '^~[^ ]*' "$HOME/E1.txt" | tr '\n' ' ')" = "~/norepo ~/work/main ~/work/main/nested ~/work/wt1 "
                                       # assert: four rows, sorted by directory — one
                                       #   assertion so a missing row cannot pass as a
                                       #   reordering
test "$(grep -c '\*' "$HOME/E1.txt")" -eq 0   # assert: Current blank for all of them
cd "$W/wt1" && /tmp/kae ls --pins > "$HOME/E2.txt"
grep -qE '^~/work/wt1 +\*' "$HOME/E2.txt"     # assert: `*` on work/wt1 ...
test "$(grep -c '\*' "$HOME/E2.txt")" -eq 1   # assert: ... and only there
/tmp/kae ls --pins --json > "$HOME/E3.json"
grep -q '"schema_version": 1' "$HOME/E3.json"
test "$(grep -c '"directory"' "$HOME/E3.json")" -eq 4   # assert: bound_directories[]
/tmp/kae unpin && /tmp/kae ls --pins > "$HOME/E4.txt"
test "$(grep -c 'work/wt1' "$HOME/E4.txt")" -eq 0
test "$(grep -c '^~/' "$HOME/E4.txt")" -eq 3
                                       # assert: work/wt1 is GONE — unpin keeps the store
                                       #   on purpose, and a store is not a binding. The
                                       #   count of 3 is the absence check's control

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
mkdir -p "$W/main/locked" && cd "$W/main/locked"
/tmp/kae pin main > "$HOME/F.out" 2> "$HOME/F.err"; test $? -eq 0
                                       # assert: exit 0 — a warning never fails the bind
grep -q 'could not tell git to ignore' "$HOME/F.err"
grep -q 'the binding is in place' "$HOME/F.err"
                                       # assert: and the warning says so. Reachable in
                                       #   practice because the exclude file is *outside*
                                       #   the pinned directory: a worktree can be
                                       #   writable while the main checkout's .git is not
test "$(grep -c 'ignored via' "$HOME/F.out")" -eq 0
                                       # assert: NO `ignored via` in the report; the two
                                       #   greps above are its positive control
git -C "$W/main" status --porcelain > "$HOME/F.status"
grep -q '^?? locked/$' "$HOME/F.status"
                                       # assert: the fragment really is unignored, which
                                       #   is what the warning told the user
chmod u+w "$W/main/.git/info/exclude"
```

**PASSED 2026-08-08 on the release tree, 31/31** (`/tmp/kae` = v0.17.0), and before that
2026-08-04 on the pre-release binary: A–F, each assertion checked
individually **at its own point in the block** rather than from the end state. Unlike the
two credential blocks below, this one needed no correction to run on the final tree —
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

## Harvesting a credential before it is overwritten

**Run this section with `bash scripts/smoke-run.sh '## Harvesting a credential'`,
like every other section**, and a correct run exits `0` with every line exiting 0.

Read the transcript anyway when something fails. This section has the worst history of
green runs that proved nothing — fixtures written to a directory kae had stopped
reading, a `grep` defeated by base64, token names that prefixed one another — and a
per-line verdict tells you *which* line, not whether the fixture reached the code it
was aimed at.

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
go build -o /tmp/kae .
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
# One line, because scripts/smoke-run.sh evaluates one line at a time and a function
# body spread over several is the same trap as a here-document.
reset_main() { cred MAIN-OLD $OLD > "$HOME/.claude/.credentials.json"; ident main > "$HOME/.claude.json"; /tmp/kae add --no-login --identity you@example.com claude main; rm -f "$(accstore main)"; }

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
#
# In THIS case the line is a no-op, and that was measured rather than assumed (2026-08-09,
# by deleting it and watching A stay green). The pin above already materializes the
# account's identity artifact into the config store, byte-identical to what `ident main`
# writes, because the snapshot captured the same payload from the real home. No
# contradiction with the paragraph above: kae labels neither the *credential* store nor a
# copy it declined to write — the config store's identity artifact is ordinary
# materialization. It is kept because every other case seeds explicitly and a reader
# should not have to know which ones are redundant. What actually discriminates the
# tool's evidence from kae's is A2, A3, B1, B2 and B3, whose own fixture mutations do
# kill.
ident main > "$(store)/.claude.json"
SP1="$(store)"                          # kept for B, which needs to reach both readers'
                                        # caches from outside their directories
cred MAIN-NEW $NEW > "$(cstore)/.credentials.json"
/tmp/kae pin main 2> "$HOME/A.err"
grep -q 'harvested the newer claude credential' "$HOME/A.err"
grep -q 'into snapshot claude/main' "$HOME/A.err"
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
/tmp/kae pin main 2> "$HOME/A2.err"; test $? -eq 0
grep -q 'harvested the newer claude credential' "$HOME/A2.err"
test "$(grep -c 'this write replaces it' "$HOME/A2.err")" -eq 0
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
/tmp/kae pin solo 2> "$HOME/A3.err"; test $? -eq 0
grep -q 'kept it rather than replacing it' "$HOME/A3.err"
grep -q 'no directory reads this credential yet' "$HOME/A3.err"
test "$(grep -c 'this write replaces it' "$HOME/A3.err")" -eq 0
grep SOLO-NEW "$(accstore solo)"         # assert: the only copy that can refresh survives
test "$(snap solo | grep -c SOLO-NEW)" -eq 0
                                        # assert: 0 — kept is not harvested. kae could not
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
/tmp/kae pin main 2> "$HOME/B1.err"; test $? -eq 0
test "$(grep -c '^kae: ' "$HOME/B1.err")" -eq 1
grep -q 'belongs to an account other than claude/main' "$HOME/B1.err"
test "$(grep -c 'log in inside' "$HOME/B1.err")" -eq 0
#   the three lines above assert: exactly ONE stderr line — `belongs to an account other than claude/main`
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
test "$(snap main | grep -c FOREIGN)" -eq 0
                                        # assert: 0 — a token filed under the wrong
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
/tmp/kae pin main 2> "$HOME/B3.err"; test $? -eq 0
grep -q 'kept it rather than replacing it' "$HOME/B3.err"
grep -q 'this directory does not read it yet' "$HOME/B3.err"
grep -q 'it will run that other account' "$HOME/B3.err"
test "$(grep -c 'this write replaces it' "$HOME/B3.err")" -eq 0
#   `it will run that other account` is only on this arm: it is the one keep where kae has
#   positive evidence about the copy and so can say what the directory does next; the
#   command still prints its success line
grep FOREIGN "$(accstore main)"         # assert: the sibling's live login survives. B1 is
                                        #         the same fixture with the bind run *in* a
                                        #         reader, and there it is replaced — the
                                        #         pair is the whole condition
test "$(snap main | grep -c FOREIGN)" -eq 0   # assert: 0 — kept is not harvested
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
/tmp/kae pin main 2> "$HOME/B2.err"
test "$(grep -c '^kae: ' "$HOME/B2.err")" -eq 1
grep -q 'disagree about whose login it is' "$HOME/B2.err"
grep -q 'leaving it where it is' "$HOME/B2.err"
grep -q 'kae relogin claude' "$HOME/B2.err"
test "$(grep -c 'kept it rather than replacing it' "$HOME/B2.err")" -eq 0
#   A disagreement is missing evidence, so it carries the login remedy, unlike B1. The
#   pin-level pass is the speaker here (it knows the bound directory), so this is *not*
#   the chokepoint's `kept it rather than replacing it`, asserted absent above; that
#   wording appears in A3, where no pass had anything to say
grep FOREIGN "$(accstore main)"         # assert: the live copy survives. This is the half
                                        #         that inverts: a refusal here would be a
                                        #         deletion, not a conservative choice
test "$(snap main | grep -c FOREIGN)" -eq 0   # assert: 0 — kept is not harvested
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
/tmp/kae pin main > "$HOME/C1.out" 2> "$HOME/C1.err"
grep -q 'Pinned this directory' "$HOME/C1.out"
                                        # assert: the command ran — the positive control
                                        #         for the two absences below
test "$(grep -c harvested "$HOME/C1.err")" -eq 0   # assert: NO `harvested` line —
                                        #         presence is not usability
test "$(grep -c '^kae: ' "$HOME/C1.err")" -eq 0
                                        # assert: and NO warning at all. This is the one
                                        #         arm that is silent, so it is what
                                        #         separates the two; without it C passes
                                        #         on either
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
/tmp/kae pin main 2> "$HOME/C2.err"
test "$(grep -c harvested "$HOME/C2.err")" -eq 0   # assert: NO `harvested` line
test "$(grep -c '^kae: ' "$HOME/C2.err")" -eq 1    # assert: exactly one warning
grep -q 'cannot read or date the copy already there' "$HOME/C2.err"
grep -q 'a payload kae cannot judge may still be a login' "$HOME/C2.err"
grep -q "cd $P && kae relogin claude" "$HOME/C2.err"
#   $P, not `pwd -P`: this warning echoes the directory kae was given, so the canonical
#   form does not match. `kae doctor` resolves the binding instead and does print the
#   canonical path — the two are not interchangeable (measured 2026-08-09).
#   The weak claim takes the weak consequence: kae replaces the copy but does not call
#   it dead
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
/tmp/kae pin -i main 2> "$HOME/D.err"   # isolated: a different *config* store, rebuilt
                                        # from the snapshot
test "$(grep -c harvested "$HOME/D.err")" -eq 1   # assert: one `harvested` line
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
/tmp/kae pin claude side 2> "$HOME/E.err"
test "$(grep -c '^kae: ' "$HOME/E.err")" -eq 1
grep -q 'harvested the newer claude credential' "$HOME/E.err"
grep -q 'into snapshot claude/main' "$HOME/E.err"
test "$(grep -c 'into snapshot claude/side' "$HOME/E.err")" -eq 0
#   main, not side, and it is the ONLY stderr line. This is the one case here where the credential's location
#           moves (credstore/claude/main → …/side), which is why the pass has to read the
#           store being left before the bind writes the new one. Nothing reports a foreign
#           copy: the store now being written is side's own. Measured 2026-08-08 — this
#           said "the write path then notes that the copy in the store is not side's",
#           which was true while both accounts' copies shared one per-directory store
snap main | grep MAIN-NEW               # assert: main's login survived the re-bind
test "$(snap side | grep -c MAIN-NEW)" -eq 0   # assert: 0 — not filed under side
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
test "$(/tmp/kae doctor --json | grep -c identity_drift)" -eq 0
                                        # assert: 0 — a store that agrees with
                                        #         its binding says nothing
ident other > "$(store)/.claude.json"   # a login inside the directory as another account
/tmp/kae doctor --json > "$HOME/F.json"
test "$(grep -c identity_drift "$HOME/F.json")" -eq 1   # assert: one warn, naming this
grep -q "$G" "$HOME/F.json"             #         directory and claude/main, with the
grep -q 'claude/main' "$HOME/F.json"    #         relogin remedy
grep -q "cd $G && kae relogin claude" "$HOME/F.json"
test "$(grep -c 'u-other\|other@example.com' "$HOME/F.json")" -eq 0
                                        # assert: 0 — an identity is PII and never
                                        #         reaches the report. The greps above are
                                        #         its positive control
rm "$(store)/.claude.json"
test "$(/tmp/kae doctor --json | grep -c identity_drift)" -eq 0
                                        # assert: 0 — no cache in the store is
                                        #         missing evidence, not drift (the
                                        #         ordinary state until the tool runs
                                        #         there, and permanent for a directory
                                        #         bound before v0.16.0)

# --- the two restore paths. One child script for G and H, differing only in the uuid
#     it leaves behind, because that difference is the whole attribution question ---
# Written a line at a time, not from a here-document: `scripts/smoke-run.sh` evaluates
# one line at a time, so a here-doc has no body there and the lines below it are run as
# commands. That is what stopped the relogin block after 35 of 146 lines while it
# reported every line green (2026-08-09).
: > "$HOME/child.sh"
printf '%s\n' '#!/bin/sh' >> "$HOME/child.sh"
printf '%s\n' '# $1 access token, $2 expiresAt, $3 the account the identity cache names' >> "$HOME/child.sh"
printf '%s\n' "printf '{\"claudeAiOauth\":{\"accessToken\":\"%s\",\"refreshToken\":\"rt-%s\",\"expiresAt\":%s,\"refreshTokenExpiresAt\":1830384000000}}' \"\$1\" \"\$1\" \"\$2\" > \"$HOME/.claude/.credentials.json\"" >> "$HOME/child.sh"
printf '%s\n' "printf '{\"oauthAccount\":{\"accountUuid\":\"u-%s\",\"emailAddress\":\"%s@example.com\"}}' \"\$3\" \"\$3\" > \"$HOME/.claude.json\"" >> "$HOME/child.sh"
chmod +x "$HOME/child.sh"
cd "$HOME"

# --- G. run -s on the account that is ALREADY active keeps the child's refresh ---
cred MAIN-OLD $OLD > "$HOME/.claude/.credentials.json"; ident main > "$HOME/.claude.json"
/tmp/kae add --no-login --identity you@example.com claude main
/tmp/kae run -s claude main -- "$HOME/child.sh" MAIN-NEW $NEW main 2> "$HOME/G.err"
grep -q 'was already the active account' "$HOME/G.err"
test "$(grep -c 'previous auth state restored' "$HOME/G.err")" -eq 0
#   nothing was restored, so it is not claimed; the line above is the positive control
grep MAIN-NEW "$HOME/.claude/.credentials.json"   # assert: the real home still runs the
                                        #         copy that can refresh. Before this it
                                        #         held MAIN-OLD — a logged-out session
                                        #         reported as a successful restore
snap main | grep MAIN-NEW                # assert: the post-child recapture has it too,
                                        #         so `kae use claude main` applies it

# --- H. the same run, except the child logged in as somebody else ---
cred MAIN-OLD $OLD > "$HOME/.claude/.credentials.json"; ident main > "$HOME/.claude.json"
/tmp/kae add --no-login --identity you@example.com claude main
/tmp/kae run -s claude main -- "$HOME/child.sh" FOREIGN $LATER other 2> "$HOME/H.err"
grep -q 'previous auth state restored' "$HOME/H.err"
test "$(grep -c 'already the active account' "$HOME/H.err")" -eq 0
#   a later deadline is not evidence of whose login it is
grep MAIN-OLD "$HOME/.claude/.credentials.json"   # assert: restored. Keeping FOREIGN
                                        #         would leave the real home running one
                                        #         account while kae records another
grep -q 'probably logged in again outside kae' "$HOME/H.err"
grep -q 'kae add --no-login claude' "$HOME/H.err"
#   the recapture refused, and it says how to keep that login instead of discarding it
#   silently
test "$(snap main | grep -c FOREIGN)" -eq 0
                                        # assert: 0. run -s's own recapture now applies
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
/tmp/kae rollback 2> "$HOME/I.err"; test $? -eq 0
#   exit 0 — a warning never moves the exit code. Taken from `kae` itself: `echo $?`
#   after one of the `grep` assertions below reports *grep's* status and can never fail,
#   which is how this line was wrong when first written
grep -q 'older claude credential for claude/main than the one in the live store' "$HOME/I.err"
test "$(grep -c 'kae use claude main' "$HOME/I.err")" -eq 0
#   NOT `kae use claude main`: the snapshot holds the older copy. The live copy is the one
#   being overwritten, so the backup named below is the only place left holding it
IDT=$(sed -n 's/.*kae rollback --to \([0-9A-Za-z-]*\).*/\1/p' "$HOME/I.err")
test -n "$IDT"                          # assert: a `--to <id>` really was printed
/tmp/kae backup list | grep -q "$IDT"   # assert: and it names a backup that exists —
                                        #         the id must be the one this rollback
                                        #         just created, not the one it restored
grep MAIN-OLD "$HOME/.claude/.credentials.json"   # assert: the rollback still happened.
                                        #         Going back is what was asked for; the
                                        #         warning is what kae adds

# --- J. the switch-away recapture declines a live copy the snapshot supersedes ---
# This is what stops I's rolled-back copy from being laundered over a newer snapshot on
# the next switch. Both copies are usable here, so the usability refusal cannot see it.
cred MAIN-NEW $NEW > "$HOME/.claude/.credentials.json"; ident main > "$HOME/.claude.json"
/tmp/kae add --no-login --identity you@example.com claude main
cred MAIN-OLD $OLD > "$HOME/.claude/.credentials.json"   # what a rollback leaves behind
/tmp/kae use claude side 2> "$HOME/J.err"
grep -q 'snapshot claude/main holds a later claude credential than the live store' "$HOME/J.err"
grep -q 'snapshot left unchanged' "$HOME/J.err"
snap main | grep MAIN-NEW                # assert: the only copy that can refresh survived
test "$(snap main | grep -c MAIN-OLD)" -eq 0
                                        # assert: 0 — paired with the positive line above
```

**Every line here exits 0 when the block passes**, so it runs through
`scripts/smoke-run.sh` like every other section and its exit status means something.
That is a change: this block used to leave `grep -c` printing `0` as the assertion and
told the reader to paste it rather than run it, which is the opposite of the rule
§ Smoke Checks states for the same construct — one file, two conventions, and only one
of them checkable. Those lines are wrapped in `test` now, the stderr assertions capture
stderr to a file instead of asking a reader to look, and the two multi-line constructs
(a here-document and `reset_main`) are single lines, because the per-line loop cannot run
either.

**A–J PASSED 2026-08-08 on the release tree** (darwin 24.6.0, file driver, file
backend, temp HOME, `/tmp/kae` = v0.17.0), each assertion checked at its own point rather
than from the end state. A–E and F were first run 2026-08-04 and G–J from 2026-08-05; that
record is superseded because the tree moved under it twice, and the re-run found a defect
in this block rather than in kae. D and E are the two cases a *chokepoint-only* harvest
got wrong, so they are the ones worth re-running after any change to `kae pin`'s ordering:
both depend on the pin-level pass running **before** the stores are materialized, and E
additionally on the replaced fragment being read before it is rewritten.
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

## `kae relogin` and `credential_superseded`

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
**only**, never the harvest cases. Run after them on 2026-08-08, K measured 0 findings
instead of 1 — their bindings of the same account made K's "newest copy" a tie, and the
store that won the walk order was one kae could not attribute, which silenced the whole
group. (A tied store that does *not* win changes nothing; it is already skipped
for not being superseded.) **That particular mechanism is gone**: attribution of the
account's own credential store now asks the directories reading it rather than the one
handle the walk reached first, so a sibling with an identity cache answers for the copy
([ADAPTERS.md](ADAPTERS.md) § Per-directory credential store is normative for what
attributes what, and `TestSupersededSurvivesOneSharedHandleLosingItsIdentityCache` is
the control). The rule
needs none of that to stand — `scripts/smoke-run.sh` gives every section a fresh
sandbox — and seeding here is what makes "preamble only" structural instead of an
instruction a reader has to obey.

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
# One capture, not three runs over state nothing changes in between: an unscoped
# `kae doctor` sweeps every tool plus the companion and pinned-directory checks, ~1.5s
# a call on the machine this was measured on.
/tmp/kae doctor --json > "$HOME/K.json"
test "$(grep -c credential_superseded "$HOME/K.json")" -eq 1
                                        # assert: exactly one directory lost. A count
                                        #   alone would pass as a quiet 0, so the lines
                                        #   below name which one
grep -q "bound to $B is older than another copy of claude/main (the store bound to $A)" "$HOME/K.json"
                                        # assert: B lost, and A is named as where the
                                        #   newer copy is
grep -q "cd $B && kae relogin claude" "$HOME/K.json"  # assert: and the remedy is a login
test "$(grep -c credential_stale "$HOME/K.json")" -eq 0
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

## Shell completion for `kae env` and `kae backup`

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

### Completion real-machine smoke (required before release)

bash and zsh (fish is best-effort, not gated — v0.8.6). In a fresh shell with
completion registered **and refreshed**:

- [x] `kae env <TAB>` offers `set` / `unset` / `list`; `kae backup <TAB>` offers
      `list`.
- [x] `kae env set <TAB>` offers tools; `kae env set claude <TAB>` offers claude's
      accounts.
- [x] `kae env list <TAB>` offers **nothing** — `env list` takes no arguments, and
      the branch is gated on the sub-verb to avoid suggesting a word the command
      rejects.

## Switching a per-account credential store

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

### Real-machine smoke for the per-account store (required before release)

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
adapter's `VerifiedVersion()`, its `VerifiedOn()` and the version recorded here
**in the same commit**. Those are not the whole set —
[.claude/skills/upstream-auth-drift/SKILL.md](../.claude/skills/upstream-auth-drift/SKILL.md)
§ Re-record is normative for it, and is where this chain ends rather than at the
adapter comment that sent you here.

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

### claude (verified on 2.1.233)

**The heading is not evidence that every row under it was re-run on that version, and
here it is not true of most of them.** Each row says what it got and when, which is
the only place that can be right: a row re-measured this pass says so, two say which
*half* of themselves was re-measured, and a row that needs a real login carries the
older `Measured … on 2.1.220` provenance it has always carried. An earlier draft
stated the split as a rule instead — "every login-free row" — and the host-managed
provider row is the counter-example that makes any such rule wrong.

Re-recorded from the same 2026-08-16 pass, because it is a **negative** result and
nothing else would keep it: `SELF_HOSTED_RUNNER_HOST_CONFIG_DIR` exists at 2.1.233
and takes precedence over `CLAUDE_CONFIG_DIR` where it is read, but it does **not**
move the credential. Shim-measured both ways — set alongside `CLAUDE_CONFIG_DIR` the
service still carried the `CLAUDE_CONFIG_DIR` hash, and set alone the service was the
unsuffixed name, so the variable is not consulted for the credential at all. kae
therefore needs no entry for it; what would change that is a later build reading it in
the resolver the row below names, which is the same shim run to re-check.

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
| **Where the credential resolves to** is a rule, not a constant. The keychain service is `Claude Code` + the build's OAuth suffix + `-credentials` + a per-config-dir suffix. That last suffix is empty only while `CLAUDE_CONFIG_DIR` is unset or empty; otherwise it is `-<first 8 hex of sha256(value)>` over the env string **NFC-normalized** — no `resolve`, no cleaning, so a trailing `/` hashes to a different item, and a decomposed non-ASCII component hashes as its composed form. Reads try keychain first and fall back to `<configDir>/.credentials.json`; a write goes to keychain and **deletes that file** when the item was previously absent. `CLAUDE_SECURESTORAGE_CONFIG_DIR`, when set, replaces `CLAUDE_CONFIG_DIR` as both the hash input and the file's directory — and set to the *empty string* it drops the suffix entirely, collapsing every config dir onto one shared item. **Both of those halves are run-confirmed** (2026-08-04, 2.1.220, shim below): with the two variables set to different directories the logged service carries `sha8` of the **securestorage** one, and with it empty the service is the unsuffixed name. Consequently the credential's location is separable from the config dir's — sessions and the identity file follow `CLAUDE_CONFIG_DIR` while the credential follows this variable, which is the mechanism a per-account credential with per-directory sessions would use. Also visible in the same log: claude probes a **second service family**, `Claude Code[-<sha8>]` without `-credentials`, tracking the same suffix rule. No item of that family exists on the measured machine, so nothing is known to write it — but kae models only the `-credentials` one, and "every service one login writes" is the enumeration this table exists to keep honest | Shim `security` rather than logging in. Put an executable ahead of `/usr/bin` on `PATH` that appends `"$*"` to a log and exits 44 (item-not-found), then run `env -i HOME=<temp> PATH=<shim>:/usr/bin:/bin USER="$USER" CLAUDE_CONFIG_DIR=<dir> claude -p hi </dev/null`. The logged `-s <service>` is the name claude actually resolves; check the suffix against `python3 -c 'import hashlib,sys,unicodedata;print(hashlib.sha256(unicodedata.normalize("NFC",sys.argv[1]).encode()).hexdigest()[:8])' <dir>` — the NFC step is not optional, and a `<dir>` with a decomposed non-ASCII component is the case that needs it. Re-run with a trailing slash, with `CLAUDE_SECURESTORAGE_CONFIG_DIR=`, and with it set to another dir to cover all four branches. **No login and no real-keychain access**, so this row is re-verifiable in seconds on any macOS machine — prefer it to the old seed-and-wait-for-refresh procedure. The delete-on-write half is the exception: it is read from the composed store's `update()` in the bundle (`if the keychain read returned absent, delete the plaintext file`) and reproducing it at runtime needs a live refresh token, so treat that half as **source-confirmed, not run-confirmed**. kae reproduces the rule in `claude.keychainService`, so every mechanism that sets `CLAUDE_CONFIG_DIR` writes into the item that config dir resolves to. Recording *storage resolution* as a verifiable rule is the lesson: kae had verified "the credential is at X" and never "how the tool decides where X is", and modelling the name as a constant is exactly what let a pinned directory run the previous account with every offline guard green **Re-measured 2026-08-16 on 2.1.233 and unchanged**, all four branches, each against a hash computed outside kae: a bare config dir, a different `CLAUDE_SECURESTORAGE_CONFIG_DIR`, that variable set empty, and a trailing slash. The second service family is still probed too. |
| The **OAuth suffix** renames *both* stores, not just the keychain one: the service is `Claude Code<suffix>-credentials[-<sha8>]` **and** the identity file is `.claude<suffix>.json` in the same config dir. The suffix is `-custom-oauth` whenever `CLAUDE_CODE_CUSTOM_OAUTH_URL` is **non-empty** (an empty value is falsy and changes nothing; an *unapproved* endpoint makes claude throw rather than fall back), otherwise it comes from the build channel — `""` production, `-local-oauth`, `-staging-oauth` — which a released binary hard-codes to production, so the environment can only ever produce `""` or `-custom-oauth` | Source-read from the installed bundle, 2026-07-30 on 2.1.220, no login and no keychain access. The bundle is one Mach-O with the JS inline, so read it by offset: `B=~/.local/share/claude/versions/<version>`, `grep -oab -- '-custom-oauth' "$B" \| cut -d: -f1`, then `dd if="$B" bs=1 skip=<offset-400> count=1400 2>/dev/null \| LC_ALL=C tr -c '\11\12\15\40-\176' '.'`. A `grep -oa` with a wide context pattern **times out** on 266 MB and `strings` is useless (one giant string) — offsets then `dd` is the technique. Three sites must agree: the suffix function (`if (process.env.CLAUDE_CODE_CUSTOM_OAUTH_URL) return "-custom-oauth"`, else `switch` on a channel function that `return"prod"`), the service assembly (`` return `Claude Code${…OAUTH_FILE_SUFFIX}${"-credentials"}${o}` ``, at the `"-credentials"` literal), and the identity path (`` let e=`.claude${…}.json` ``, plus a loop over all four suffixes). Cross-check with the shim run above and `CLAUDE_CODE_CUSTOM_OAUTH_URL` set. kae **refuses** claude (exit `5`) on a non-empty value rather than computing the suffix, because the build-channel half is not visible from the environment at all — a recorded gap (docs/ROADMAP.md), and modelling one of the three sources would stay silently wrong for the other two **Re-read 2026-08-16 on 2.1.233 and unchanged**: the suffix function still branches on the same variable and otherwise switches on a channel function returning production. |
| A **host-managed provider** is a third credential source: with `CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST` truthy, claude reads the JSON file `CLAUDE_CODE_HOST_CREDS_FILE` names — rejecting it unless it is absolute, ≤64 KiB, owned by the caller, not group/other-readable, and carries a live `pid`/`procStart` and an unexpired `expiresAt` — and injects its `env` entries, with the token landing in the variable `CLAUDE_CODE_HOST_AUTH_ENV_VAR` names (default `ANTHROPIC_AUTH_TOKEN`). `ANTHROPIC_UNIX_SOCKET` marks the same host-managed mode | Same offset-read technique at the `CLAUDE_CODE_HOST_CREDS_FILE` literal (2.1.220): one function validates and parses the file, another gates on the truthy `…MANAGED_BY_HOST` and writes `process.env`. kae cannot name the destination variable when the host renames it, so it warns on the four mechanism variables instead (docs/ADAPTERS.md "Environment conflicts"). A warning, not an unsupported refusal — this moves what authenticates, not where kae writes **Partially re-read 2026-08-16 on 2.1.233**: the mechanism variables are all still present, and `CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST` is still read through a truthiness test rather than compared to a value — but **which read that test gates was not traced, and the file validation above** — the size bound, the ownership and mode tests, the live `pid`/`procStart` — **was not re-read at all**, so both of those rest on the 2.1.220 reading. |
| A **relative** `CLAUDE_CONFIG_DIR` is used verbatim: claude resolves it against its own working directory, so kae (invoked from anywhere in the project) reads and writes different *files* — but **not** a different keychain item, because the service name hashes the variable's raw value rather than a resolved path | Read the resolver out of the bundle: `grep -oab -- 'env.CLAUDE_CONFIG_DIR' "$B"` then a `dd` window at each offset (technique in the row above). At 2.1.220 it is `AIl(){return process.env.CLAUDE_CONFIG_DIR}` and `fn = Vr(() => (AIl() ?? join(homedir(), ".claude")).normalize("NFC"), AIl)` — NFC and no path resolution — with the identity path `join(process.env.CLAUDE_CONFIG_DIR \|\| homedir(), ".claude<suffix>.json")`. Corroborating literal in the same bundle: a subprocess's value must match the parent's "same path, same separators". kae warns (`env_conflict`) and keeps honoring the value **Partially re-measured 2026-08-16 on 2.1.233**: the shim covers the load-bearing half, that the keychain item's hash input is the raw value NFC-normalized with no path resolution — a trailing slash resolves to a different item — while the half about which *files* a relative value resolves against was not re-measured. |
| The keychain item's **account attribute** is `$USER` when it matches `^[a-zA-Z0-9._-]+$`, otherwise the OS username, and the literal `claude-code-user` when neither is usable | The shim log's `-a <account>` names it. It is load-bearing because claude's reads are account-scoped (`find-generic-password -a <account> -s <service>`): an item kae writes under a different account attribute is invisible to claude even when the service name matches exactly **Re-measured 2026-08-16 on 2.1.233 and unchanged**, from the shim log and the character-class test beside it in the bundle. |

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
| opencode | **`auth.json` is still the live store**, with two other stores present but not authoritative: `account.json` (`{version, accounts, active}`) is **derived** from auth.json on every run by 1.17.3 and unreferenced from 1.17.4 on (the filename is in 1.16.2–1.17.0 as well, but a bare `auth list` there does not produce the file), and the `credential` table in `opencode.db` is populated from auth.json **exactly once** behind a `data_migration` marker (`credential.auth-json`, 1.17.4 only) and not maintained afterwards | Login-free, temp XDG root, no real `$HOME`: plant a dummy `{"openai":{"type":"oauth",…}}` at `$XDG_DATA_HOME/opencode/auth.json`, run `opencode auth list`, then **rewrite auth.json with a different provider key** and run it again — the second run must report the new key (the file wins). Cross-check the other two stores: `sqlite3 opencode.db "select name from data_migration; select connector_id,label,active from credential;"` (never the `value` column) and `account.json`'s `active`. Measured 2026-07-31: `auth list` reflects a rewritten auth.json on 1.16.2, 1.17.3, 1.17.4 and 1.18.5 (so the file, not a cache, is what is read); on 1.17.4 — the one version that imports — `auth logout <provider>` then empties auth.json and leaves the imported DB row untouched, which is what makes auth.json the store and the row dormant. 1.18.5 creates neither the marker nor a row. **The failure mode to watch for is a version where the DB row wins**: kae's patch would then be a silent no-op, and on 1.17.4 the row is frozen at whichever account auth.json held on the first run. **Re-run on 1.18.16 (2026-08-16) and the store has not moved**: the second `auth list` reported the rewritten provider key and named `auth.json` as the source, while the `credential` table was **empty** and `data_migration` held **no** marker — so the import is still one-shot and still confined to 1.17.4, and the failure mode above has not arrived | 1.18.16 (behaviour, 2026-08-16; earlier 1.17.4 / 1.16.2 / 1.17.3 / 1.18.5, 2026-07-31) |
| opencode | two environment inputs put kae and opencode on different credentials, and kae **warns** on both rather than following them: `OPENCODE_AUTH_CONTENT` supplies an entire auth.json body inline and is read before the file, and `XDG_DATA_HOME` is used **verbatim with no absolute-path check**, so a relative value resolves against opencode's working directory while kae ignores it per the XDG spec | `Auth.all()` starts `if(process.env.OPENCODE_AUTH_CONTENT) try{return JSON.parse(…)}` and the data home is `process.env.XDG_DATA_HOME \|\| join(homedir(),".local","share")` — both readable in the installed binary (plain minified JS inside the Mach-O: `python3 -c "import re;d=open('<binary>','rb').read();[print(repr(d[m.start()-260:m.end()+260])) for m in re.finditer(rb'XDG_DATA_HOME',d)]"`, and note two bundled plugins repeat the same resolver). Behavioural confirmation, no login: from a temp cwd containing `reldata/opencode/auth.json`, `env XDG_DATA_HOME=reldata opencode auth list` reports the credential and prints the path as `reldata/opencode/auth.json`. opencode sets `OPENCODE_AUTH_CONTENT` itself when spawning a workspace child, so an inherited value is a real case, not a hypothetical | 1.17.4 (source + behaviour, 2026-07-31) |
| cursor | the credential is **three** opaque items under account `cursor-user` — `cursor-access-token` (a raw JWT), `cursor-refresh-token`, `cursor-api-key` — written and cleared as one unit, round-tripped verbatim. The service names come from a build-time domain constant (`cursor`), not from the environment, so kae may model them as constants | switch, then `cursor-agent status` in a fresh process reports the applied account **and** `authenticated` (not `partially-authenticated`, which is what a missing refresh item gives). The unit-ness is a source fact: read `setAuthentication` / `clearAuthentication` in the installed bundle — `~/.local/share/cursor-agent/versions/<version>/index.js` is unminified-enough JS, so `grep -oa` on the credential-store class settles it without a login. Attribute-only `security find-generic-password -s cursor-refresh-token` (never `-w`) shows which items exist | 2026.06.16 (bundle source read, 2026-07-30) |
| cursor | **cursor-agent never redeems the stored refresh token.** Its only path to a new access token exchanges an **api key** (`cursor-api-key`, else `CURSOR_API_KEY`) at `/auth/exchange_user_api_key`, and that write persists all three items. With no api key an expiring token is returned as-is and the request fails — so an expired snapshot needs an interactive login and `Freshness` is right to say so, but for this reason and not "there is no refresh token" | Same file: the refresh helper takes `{currentToken, ephemeralToken, isTokenExpiringSoon, refreshToken}` and its `refreshToken` closure returns null without an api key. Beware the red herring: the bundle's `grant_type=refresh_token` code is the **MCP client's** OAuth (in `cursor-agent-svc.js`), not cursor's own login — the same two-modules trap codex has. If a release starts redeeming it, cursor becomes refreshable and the stale warning has to learn about it | 2026.06.16 (bundle source read, 2026-07-30) |
| cursor | `cursor-agent status` prints `✓ Logged in as <email>` on one line with exit 0 (what `Identity` parses) | run it while logged in; the marker, the single line, and the exit code all hold | 2026.06.16 |
| copilot | per-account tokens coexist in the keychain, so repointing `/lastLoggedInUser` is the entire switch | after a switch, a fresh `copilot -p "say AUTH-OK" --no-color --allow-all-tools` acts as the other account (cross-account item still open — v0.7.0) | 1.0.61 |
| copilot | `config.json` is JSONC; its comments, trailing commas, and formatting must survive the patch | diff after a switch **and** after `kae rollback`: the leading `//` comments and `trustedFolders` survive both | 1.0.61 |
| copilot | **the config directory is a rule**: `--config-dir` (deprecated, hidden) → `COPILOT_HOME` → `~/.copilot`, where `COPILOT_HOME` is the directory *itself* and is used verbatim (no normalization, no absolute-path check). Setting it also skips copilot's one-way `$XDG_CONFIG_HOME/.copilot` → `~/.copilot` migration. The auth write is always `config.json`; the bare `config` file in the same directory is a fallback of the settings-migration loader only. A **relative** value is resolved against the process's own cwd by both sides, so kae warns rather than pretending to know copilot's | The **installed CLI is not the binary on `PATH`**: that one is a launcher which loads `~/.copilot/pkg/universal/<newest>/app.js` (search order includes `$COPILOT_HOME/pkg`), so a version manager can pin an older launcher while `copilot --version` reports the newer package — verify against the version `copilot --version` prints and read `app.js`, which is plain minified JS on disk. `grep -o 'COPILOT_HOME[^,;)]\{0,100\}' app.js` shows the resolver `ss()` (`t?.configDir ? … : process.env.COPILOT_HOME ? … : join(homedir(),".copilot")`), the loader `Umo()` (`join(e,"config.json")`, then `join(e,"config")`), and copilot's own help text ("override the directory where configuration and state files are stored"). Behavioural confirmation without touching the real home: symlink `$T/pkg → ~/.copilot/pkg`, put an `mcp-config` naming a probe server in `$T`, and `COPILOT_HOME=$T copilot mcp list --json` lists it | 1.0.61 (source + behaviour, 2026-07-31) |

The release acceptance run in [ACCEPTANCE.md](ACCEPTANCE.md) § Real-Machine
Acceptance is how these rows get re-verified: it already
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
The minified identifiers around these literals churned between those builds and the
counts did not. **That is a result about adjacent releases and does not generalize**:
re-measured 2026-08-16 on 2.1.233, several of claude's counts below had moved from
their 2.1.220 values while the rest held, and the bundle had grown to 315,654,448
bytes. Which ones moved is `git log -p` on the table below rather than a figure
written here; a first draft wrote the number and got it wrong by counting a row of
the artifact table with the counts. Presence alone would miss a literal that
survives in one place and disappears from another.

**The counts are measured, never derived from kae's own constants.** Two of kae's
most important literals do not exist in the bundle at all, because upstream
*composes* them:

- `claude.KeychainService` is `"Claude Code-credentials"`, and that string occurs
  **0** times — re-confirmed on 2.1.233, 2026-08-16, as it was on 2.1.220. Upstream
  builds it as `"Claude Code"` + the OAuth suffix + `"-credentials"`, so only the
  parts are countable: `Claude Code` on its own is noise (1245 occurrences on 2.1.233,
  1070 on 2.1.220 — the figure moves and nothing reads it), while `-credentials` is
  usable and has its own row in the count table below, which is the one copy of it.
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
byte: it counted 12 in opencode 1.17.4, of which 3 were `auth-json`, against 9
occurrences of the literal in that same build. Both figures are that build's; the
count table below is the one copy of the current one. The check compares literals, so a
regex-inflated count reads as upstream drift on a tool that never moved.

| Tool | Artifact | Measured on |
|---|---|---|
| claude | `~/.local/share/claude/versions/<version>` (one Mach-O, JS inline) | `2.1.233` |
| cursor | `~/.local/share/cursor-agent/versions/<version>/` (webpack chunks) | `2026.06.16-20-30-07-a07d3ac` |
| copilot | `~/.copilot/pkg/universal/<version>/app.js` (plain JS) | `1.0.61` |
| opencode | `~/.local/share/mise/installs/opencode/<version>/opencode` (Bun single-file executable) | `1.18.16` |
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
| claude | `-credentials` | 30 |
| claude | `claude-code-user` | 3 |
| claude | `CLAUDE_CONFIG_DIR` | 51 |
| claude | `CLAUDE_SECURESTORAGE_CONFIG_DIR` | 13 |
| claude | `CLAUDE_CODE_CUSTOM_OAUTH_URL` | 10 |
| claude | `claudeAiOauth` | 20 |
| claude | `oauthAccount` | 75 |
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
| opencode | `auth.json` | 8 |
| agy | `go-keyring-base64:` | 1 |
| agy | `shouldBypassKeyring` | 3 |
| agy | `falling back to file` | 6 |
| agy | `WSL_DISTRO_NAME` | 1 |
| agy | `WSL_INTEROP` | 1 |
| agy | `SSH_CONNECTION` | 1 |
| agy | `google_accounts.json` | 0 |

What each row is evidence *for* lives in the assumptions table above — these are
counts, not explanations. When a count moves, work that tool's rows there.

## Secret Leak Regression

`go test ./internal/cmd/ -run TestSecretsNeverInOutputOrMetadata` asserts that
captured fixture secret values never appear in text output, JSON output, error
messages, or metadata files written by capture/switch/rollback.

Companion token leakage is covered by `TestCompanionFragmentLinesNeverLeakSecret`
(the token reaches the fragment and export fallback only as an `exec()`/`$()`
lookup, never as a literal) and `TestCompanionListHidesSecretValues` (the value
never appears in `kae companion list` text/JSON).
