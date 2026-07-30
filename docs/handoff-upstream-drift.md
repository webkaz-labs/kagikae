# Handoff — upstream drift: what is still wrong, and how to stop finding out the hard way

**Status**: in progress. Written 2026-07-30, right after v0.12.0 shipped.

**Done since** (branch `fix/codex-keyring-account-scope`): **1.2** — all three
consequences of the codex keyring account rule, except the per-directory capability
(consequence 1), which is now a recorded gap in [ROADMAP.md](ROADMAP.md) with its
prerequisites rather than a wrong model — and **1.3**, settled from the same
upstream source. The account rule was additionally confirmed against a real
keychain item without a login; the procedure is in
[VALIDATION.md](VALIDATION.md) § "Upstream Behaviour Assumptions" and is reusable
for any codex re-verify. Four review rounds on that branch found five further
defects of the same class in the restore/rollback paths (a credential written or
deleted in a store the tool does not read there), all fixed on the branch — so
**treat any restore-path assumption in this document as re-examined**.

**Also done**: **2.1** (branch `fix/doctor-orphan-namespaces`), **1.1**
(branch `fix/cursor-full-credential-set`), **1.4** (branch
`fix/claude-custom-oauth-url`), **1.5** (branch `fix/upstream-drift-1-5`,
where one of the three claims was overturned by measurement — see the entry)
**2.3** (branch `fix/state-lost-update`, where the claim held but review
found the lock alone did not close it — see the entry) and **2.2 / 2.4**
(branch `fix/pin-directory-index`, where two of 2.4's sub-claims were
overturned — see the entry) and **1.6 / 1.7** (branch
`fix/upstream-store-identity`).

**Still open here**: 2.5 and Part 3's skill. Also open,
and unrelated to this document: the two live-machine gates in
[VALIDATION.md](VALIDATION.md) — "codex per-directory keyring bind" (which is all
that stands between the shipped code and dropping codex from
`bindableNotYetDeclared`) and "Cursor full credential set".

**Branch**: start a new one off `main`.
**Why this file exists**: v0.12.0 fixed one instance of a defect class — *kae
modelled an upstream storage location as a constant when it is actually a rule* —
and the audit that followed found the same class in four more places, plus a set
of gaps that have nothing to do with upstream at all. This file carries the
findings with their evidence, and a design for the workflow that should exist for
the next time an upstream tool changes its authentication: detect it, establish the
new rule, **update kae to match**, and re-record the assumption.

Read [VALIDATION.md](VALIDATION.md) § "Upstream Behaviour Assumptions" first: it
is the current mechanism, and Part 3 here is about replacing its manual half.

## How to read the evidence labels

Findings came from four parallel read-only audits. **Everything below is labelled
by who verified it, because the labels change what you should do first:**

- **VERIFIED HERE** — reproduced in this session, with the command that did it.
  Act on these directly.
- **AGENT-CLAIMED** — an audit agent reported it with a plausible citation, and it
  was *not* independently reproduced. Re-verify before building on it.

Both outcomes happened while checking, which is why the labels are worth keeping:
one claim was **wrong** and would have wasted a session (the shim generalization —
see the box in Part 3), and one was **right and the most serious item here** (1.2,
settled from official source in a minute). Treat AGENT-CLAIMED as a lead worth an
hour, not as a fact and not as noise.

---

# Part 1 — Same defect class as the v0.12.0 gate

Each of these is "kae treats an upstream location/name as fixed; it isn't."

### 1.1 cursor: kae switches one of at least two keychain items — **FIXED** (measured from the installed bundle: the refresh token was the wrong suspect)

`internal/adapter/cursor/cursor.go:63-69` switches only `cursor-access-token`.
On this machine:

```
$ security find-generic-password -s cursor-refresh-token   # attributes only, no -w
    "acct"<blob>="cursor-user"
    "svce"<blob>="cursor-refresh-token"
```

The item **exists**, so after `kae use cursor <other>` the refresh token still
belongs to the previous account. `cursor.go:119-127` states "There is no refresh
token (the JWT is the whole credential)" — that comment is wrong, and
`docs/ADAPTERS.md` and `docs/VALIDATION.md` repeat it.

What is *not* yet established: what cursor does with a mismatched
access/refresh pair. Either the switch is merely non-recoverable when the access
token expires, or a refresh silently reverts the session to the previous account.
The second would be a silent wrong-credential exactly like the v0.12.0 gate.
**Measure this before designing the fix** — it decides whether this is a P0 or a
robustness gap. `cursor-api-key` was absent here, so it is presumably created only
for API-key logins; enumerate what a real login writes rather than trusting a
list.

**Measured, and the answer inverted the priority.** cursor-agent ships as
unminified-enough JS at
`~/.local/share/cursor-agent/versions/<version>/index.js`, so this was a source
read, not a behavioural experiment (`grep -oa` on the credential-store class;
`strings` is useless, same as claude's bundle). At 2026.06.16:

- There are **six** services, all under account `cursor-user`, derived from a
  build-time domain constant (`cursor`): access token, refresh token, api key, and
  three `cursor-bedrock-*`. A constant, not a rule — the environment cannot move
  it, unlike claude's and codex's.
- **The stored refresh token is never redeemed.** The refresh path only exchanges
  an **api key** at `/auth/exchange_user_api_key`. So a mismatched access/refresh
  pair does not revert the session: it is a *robustness* gap. The
  `grant_type=refresh_token` code in the bundle belongs to the MCP client's OAuth
  in `cursor-agent-svc.js` — the same two-modules red herring codex has.
- The **P0-shaped path was `cursor-api-key`**, which the audit did not name: with
  one present, an expiring access token is re-minted from it and all three items
  are written back, silently restoring the api key's account.
- `cursor-agent status` reports `authenticated` only with access **and** refresh
  present, which is why the mixed pair looked consistent.
- No file fallback on macOS (the store is chosen by platform alone), so kae's
  darwin=keychain-only model was right; the Linux path is now known and recorded
  in [ROADMAP.md](ROADMAP.md). The IDE does not use these services at all.

Fixed by switching the three-item unit and refusing to approximate the bedrock
triple ([ADAPTERS.md](ADAPTERS.md)). Existing cursor snapshots must be
re-captured, which is why `applySnapshot` now resolves every artifact before the
first live write.

**Left undone, deliberately:** that refusal still lands *after* `kae use` has taken
the tool locks, written a backup, and possibly recaptured the account being left —
`loadPlansWithSnapshots` already holds both `plan.Specs` and
`plan.Meta.Artifacts`, so it could refuse before any of that. Not done here
because the harmful half (a live tool holding two accounts' items) is closed, and
hoisting the check either duplicates its tolerance rule (`!ok && !sp.IdentityOnly`)
in two layers or removes the guard from `applySnapshot`, which `run -s` also calls
directly. Worth doing as its own change, with the check *moved* rather than copied.

### 1.2 codex: the keyring account attribute IS derived from `CODEX_HOME` — **FIXED** (verified from official source *and* against a real item)

codex is open source, and reading it settled in seconds what binary inspection had
left ambiguous. `codex-rs/login/src/auth/storage.rs` at tag `rust-v0.145.0` (the
installed version):

```rust
const KEYRING_SERVICE: &str = "Codex Auth";

// turns codex_home path into a stable, short key string
fn compute_store_key(codex_home: &Path) -> std::io::Result<String> {
    let canonical = codex_home.canonicalize().unwrap_or_else(|_| codex_home.to_path_buf());
    let path_str = canonical.to_string_lossy();
    let mut hasher = Sha256::new();
    hasher.update(path_str.as_bytes());
    let hex = format!("{:x}", hasher.finalize());
    let truncated = hex.get(..16).unwrap_or(&hex);
    Ok(format!("cli|{truncated}"))
}
```

So the account attribute is `cli|` + the first **16** hex chars of
`sha256(canonicalize(CODEX_HOME))`. **Do not reuse claude's derivation**: claude
hashes the *raw* env string NFC-normalized with no path resolution, codex
canonicalizes (resolving symlinks, falling back to the raw path when that fails)
and takes 16 chars, not 8. Two tools, two different rules — which is the whole
point of Part 3.

Also confirmed in the same file: `save_to_keyring` is followed by
`delete_file_if_exists(&self.codex_home)`, i.e. codex deletes its `auth.json`
fallback after a keyring write — claude's behaviour, in codex.

And codex's own delete is **service + key** scoped:

```rust
fn delete(&self) -> std::io::Result<bool> {
    let key = compute_store_key(&self.codex_home)?;
    let keyring_removed = self.keyring_store.delete(KEYRING_SERVICE, &key)?;
    ...
}
```

Three consequences, all now confirmed:

1. **`internal/cmd/dircred.go`'s refusal for codex is over-strict.** codex *is*
   per-directory scopeable — by account attribute rather than by service name. That
   is a capability kae currently declines. Safe, but wrong; and it means
   `KeychainDirScoped` as currently defined ("the *service name* moves") is too
   narrow a question. The real question is "does the *item identity* move", which
   for codex means the account. **Done**: the flag is now `KeychainDirBindable` and
   its parity guard derives the truth from `Target` + `KeychainAccount` — which also
   revealed that the guard had been skipping codex entirely (no `config.toml` in the
   probe dirs meant its spec resolved to `auth.json`, not the keychain). The
   capability itself still waits on the item's lifecycle
   ([ROADMAP.md](ROADMAP.md)).
2. **kae's delete is broader than codex's, and that is destructive.**
   `artifact.Spec.KeychainReplace` deletes via `keychain.DeleteItem(service)` —
   **service only**. With two codex homes there are two legitimate items under
   `Codex Auth`, so a kae codex switch under the keyring store **deletes another
   directory's codex login**. This is on the *global* switch path (`ops.go` →
   `ApplyLive`), which v0.12.0 did not touch, so it is shipped today. Reachable by
   any user with `cli_auth_credentials_store = "keyring"` and more than one
   `CODEX_HOME`. **Treat this as the highest-priority item in this document.**
3. **kae's read can capture the wrong home's credential.**
   `keychain.ReadItem(service)` takes the first item of the service. Same cause.

The fix direction is `KeychainMatchAccount` for codex with a computed account
(kae would have to implement codex's derivation), which also makes item 1's
capability available. The docs to correct in the same commit:
`docs/ADAPTERS.md` and `docs/VALIDATION.md` both describe the account as a
"per-login opaque id" kae must capture verbatim and never compute, and assert
"exactly one item of the service exists after a switch" — all three statements are
wrong.

Note for whoever reads the binary instead: `compute_store_key` exists in **two**
modules — `codex_login::auth::storage` (this one, the CLI credential) and
`codex_rmcp_client::oauth` (MCP server OAuth). Symbol-name grepping in the stripped
binary finds the MCP one first and reads as a red herring; that is exactly the trap
the official source avoids.

### 1.3 codex: `auto` and `ephemeral` stores are folded into "file" — **FIXED** (the claim held, plus two facts it missed)

`configuredStore` (`internal/adapter/codex/codex.go:51-68`) maps everything that
is not `"keyring"` to the file driver, and returns `"auto"` on a parse failure
(fail-open). The claim is that upstream's enum is `file` | `keyring` | `auto` |
`ephemeral`, that `auto` means *keyring first, file only on failure*, and that a
`secrets` keyring backend adds an encrypted file store kae does not model.

The claim held (`codex-rs/config/src/types.rs`, `AutoAuthStorage::load`), and the
read turned up two things it missed: the enum's **default is `file`**, not `auto`, so
kae's "unset means auto" was wrong in the other direction too; and the fourth store
is selected by a **feature flag** (`[features] secret_auth_storage`, default on only
on Windows), not by this key, which moves the credential into an encrypted secrets
file the `Codex Auth` item does not hold. kae now models the enum, resolves `auto`
by probing for the item (attributes only), and refuses `ephemeral`, an unknown
value, an unparseable `config.toml`, and that feature flag.

### 1.4 claude: `CLAUDE_CODE_CUSTOM_OAUTH_URL` moves *both* stores — **FIXED** (the claim held in full; read from the bundle, no shim needed)

The claim was right on both halves, and the bundle settled it without a shim run —
the JS is inline in the Mach-O, so offsets + `dd` read the three sites verbatim
(the technique, and why `grep -oa` with context and `strings` both fail, is in
[VALIDATION.md](VALIDATION.md)'s row). At 2.1.220:

- The suffix function is `if (process.env.CLAUDE_CODE_CUSTOM_OAUTH_URL) return
  "-custom-oauth"`, else a `switch` on a build-channel function that is
  `return"prod"` in a released binary. So **only two suffixes are reachable from
  the environment**: `""` and `-custom-oauth`.
- It is a **truthiness** test, so an empty value changes nothing — the refusal is on
  a non-empty value, unlike `CLAUDE_SECURESTORAGE_CONFIG_DIR` where empty is the
  dangerous case.
- The service assembly is ``  `Claude Code${OAUTH_FILE_SUFFIX}${"-credentials"}${o}` ``
  and the identity path is `` `.claude${suffix}.json` `` — claude even loops over
  all four suffixes when hunting its own identity files. Both halves confirmed.
- An **unapproved** endpoint makes claude *throw* (`is not an approved endpoint`),
  not fall back, so there is no third behaviour to model.

Fixed as the `driver()` refusal, not as a computed suffix: the build-channel half
is invisible from the environment, so computing the name would cover one of three
sources and stay silently wrong for the rest ([ROADMAP.md](ROADMAP.md) carries the
residue).

**The host-managed trio is real, and it is not the same class.**
`CLAUDE_CODE_HOST_CREDS_FILE` is read (absolute path, ≤64 KiB, caller-owned, not
group/other-readable, live `pid`/`procStart`, unexpired `expiresAt`) when
`CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST` is truthy, and its `env` entries are
injected into `process.env` — the token landing in whatever
`CLAUDE_CODE_HOST_AUTH_ENV_VAR` names, default `ANTHROPIC_AUTH_TOKEN`. That moves
*what authenticates*, not *where kae writes*, so it belongs to `envConflicts`
(warn) rather than to the refusal: those four variables are now warned on as the
mechanism, because a fixed list cannot name a destination the host renames.

### 1.5 copilot / opencode / agy: home-var and store assumptions — **FIXED** (two claims held, the store-move claim was overturned)

All three were re-verified against the **installed** versions on 2026-07-31, with
no login and without touching the real `$HOME` or keychain. Procedures and
evidence are now rows in [VALIDATION.md](VALIDATION.md); what changed:

- **copilot — held.** `COPILOT_HOME` *is* the config directory (`ss()`:
  `--config-dir` → `COPILOT_HOME` → `~/.copilot`, used verbatim), so
  `$HOME/.copilot/config.json` was patching a file copilot does not read whenever
  it is set. Fixed in `configHome`. Three facts the claim missed: the precedence
  has a **flag** above the variable that no environment can show kae; setting the
  variable also disables copilot's `$XDG_CONFIG_HOME/.copilot` → `~/.copilot`
  migration; and the loader falls back to a bare `config` file, which no auth path
  writes to — so kae still targets `config.json`, deliberately. Also worth
  knowing: **the binary on `PATH` is a launcher** and the real CLI is
  `~/.copilot/pkg/universal/<newest>/app.js`, so a version manager can pin an old
  launcher while `copilot --version` reports the newer package. That is why
  `VerifiedVersion() == "1.0.61"` was right while the pinned launcher said 1.0.48.
- **opencode — the XDG half held, the store-move half did not.** `XDG_DATA_HOME`
  is used with no absolute-path check (three resolvers, one in the main module and
  two in bundled plugins), confirmed behaviourally: `XDG_DATA_HOME=reldata` makes
  opencode read `reldata/opencode/auth.json` relative to *its* cwd. kae warns
  rather than following it, because following verbatim would resolve the value
  against kae's cwd instead. Found alongside it: **`OPENCODE_AUTH_CONTENT`**
  supplies a whole auth.json inline and is read before the file — also a warning
  (opencode sets it itself for workspace children, so it can be inherited).
  **kae is *not* patching a file nothing reads.** `auth.json` is the live store on
  1.17.3, 1.17.4 and 1.18.5: `auth logout` empties it and leaves the DB row alone.
  `account.json` is *derived* from auth.json on every run by ≤1.17.3 and dropped
  at 1.17.4 (this machine's copy is a leftover), and the `credential` table in
  `opencode.db` is a one-shot import behind a `data_migration` marker, dormant
  afterwards. The forward risk is recorded: on 1.17.4 that row freezes whichever
  account auth.json held at first run, so a release where the DB wins turns kae's
  patch into a silent no-op.
- **agy — held, and broader than claimed.** The fallback is real and its strings
  are in the binary ("Failed to save token to keyring, falling back to file", plus
  load and remove), but it is not only a write-failure path: every keyring
  operation has a **1s timeout**, and `shouldBypassKeyring` skips the keyring
  outright next to an ssh / wsl / container detector — so a remote-shell session
  on a Mac uses the file store from the start. kae warns on the detectors' env
  inputs and does **not** declare a file artifact on darwin: the fallback file's
  path is not derivable from the binary, and none of the three names in kae's
  `credentialFiles` occurs in it. Correction for Part 3's box below: **agy shells
  out to `/usr/bin/security`**, so the shim applies to it.

### 1.6 Cross-cutting: kae reads keychain items service-only, tools read account-scoped — **FIXED** (both halves held; the write direction was the worse one)

Held, and the claimed consequence was the more serious half. `artifact.go`'s
create path preferred **the existing item's account** over the adapter's, with
the comment "so a re-login that changed it is honored" — which has the causality
backwards. Every adapter's account is a *rule* over the environment (claude's
`$USER`, cursor's build-time constant), so a re-login does not change it; what
changes it is the input changing, and then the live item carries the **old**
answer. Preferring it meant one item created under a wrong account pinned every
later kae write to it while the tool went on reading the account its own rule
names — the write succeeds, the item exists, and the tool reports no login.
Self-perpetuating and invisible, exactly as claimed.

Fixed on both halves. claude and cursor now declare `KeychainMatchAccount`, so
their reads, writes and deletes are scoped to the account the adapter derives
rather than to the service's first item — which also closes the read half, where
a capture could have taken a sibling item's payload. With that, **every** keychain
spec kae ships is account-scoped, and `TestKeychainSpecsAreAccountScoped` refuses
a new one that is not. The create-path precedence is inverted: the adapter's
account wins, and the live item is consulted only when the spec has none — which
now happens solely on a rollback of a backup written before the record carried
the account, where the live item genuinely is the only evidence left.

Blast radius checked before generalizing, per the rule v0.12.0 wrote: the only
two specs affected were claude's and cursor's (codex and agy already declared it),
and both services hold exactly one legitimate item, so scoping cannot orphan a
sibling. A user whose live item sits under an account the adapter does not derive
now gets an honest "no login" instead of a silent write nothing reads — and that
user was already broken, because the tool could not see that item either.

### 1.7 A relative path variable diverges for *every* tool, not just the two measured — **FIXED** (measured per tool, and the two divergences are not the same)

1.5 established the shape: a tool that uses a path variable **verbatim** resolves
a relative value against its own working directory, while kae resolves the same
string against kae's — and kae is invoked from anywhere in the project. copilot
(`COPILOT_HOME`) and opencode (`XDG_DATA_HOME`) warned; `CLAUDE_CONFIG_DIR` and
`CODEX_HOME` did not. Both warn now, and the measurement was the work, because
copying one warning to the other tool would have shipped the wrong model.

- **claude 2.1.220 — verbatim, and the keychain item does *not* move.** Read from
  the installed bundle: `AIl()` returns `process.env.CLAUDE_CONFIG_DIR` raw, and
  the resolver is
  `fn = Vr(() => (AIl() ?? join(homedir(), ".claude")).normalize("NFC"), AIl)` —
  Unicode normalization and no path resolution. The identity file is
  `join(process.env.CLAUDE_CONFIG_DIR || homedir(), ".claude<suffix>.json")`, and
  `path.join` does not anchor a relative first segment. A corroborating string in
  the same bundle asks that a subprocess's `CLAUDE_CONFIG_DIR` match the parent's
  "same path, same separators" — claude treats the value as an opaque string.
  **So the keychain service, which hashes that same raw string, resolves to the
  *same item* for both processes**; what diverges is every file artifact. The
  warning says so, because a warning that overstates its blast radius is the kind
  users learn to ignore.
- **codex 0.145.0 — canonicalized, and both stores move.** Behavioural, no login:
  with `CODEX_HOME=relcfg` and a deliberately invalid `relcfg/config.toml`, codex
  reported the error against `/private/var/.../relcfg/config.toml` — resolved
  against **codex's** working directory and symlink-resolved. That canonical path
  is what the keyring account hashes, so a relative value moves `auth.json` *and*
  the `Codex Auth` item. From a directory with no `relcfg` in it, codex refuses to
  start outright (`CODEX_HOME points to "relcfg", but that path does not exist`),
  so the divergence is silent only when a directory of that name exists under both
  working directories — which is precisely what the warning is for.

Both **warn** rather than refuse, matching copilot: kae keeps honoring the
variable, since there is no default to fall back to while it is set, and kae
itself only ever sets these to absolute kae-owned paths — so only a user-set
relative value is at risk. `TestRelativeConfigVariablesWarnPerTool` pins that the
two messages stay different, which is the part a later edit would flatten.

---

# Part 2 — Gaps that are not about upstream

### 2.1 `doctor`'s orphan check false-positives on two whole namespaces — **FIXED** (reproduced first: the old predicate printed `kae account rm companion main`)

`orphanChecks` (`internal/cmd/doctor.go:308-330`) splits a secret key on `/` and
skips only `parts[0] == "backup"`. But the backend holds four namespaces:

| Key shape | Built by | What orphanChecks concludes |
|---|---|---|
| `<tool>/<account>/<artifact>` | `account.SecretRef` | correct |
| `backup/<id>/<tool>/<name>` | `backup.SecretRef` | correctly skipped |
| `companion/<profile>/<id>/<knob>` | `companion.SecretRef:154` | tool=`companion`, acct=`<profile>` → **false orphan** |
| `env/<tool>/<account>/<var>` | `envprofile.SecretRef:39` | tool=`env`, acct=`<tool>` → **false orphan** |

So every companion binding **and** every env-profile variable warns forever on an
enumerable backend (the file backend; the keychain backend is not an
`Enumerator`). The remediation it prints does not even parse —
`kae account rm companion <profile>` names a tool that does not exist. The audit
found the `companion/` case; the `env/` case was found while verifying it, which
is a hint that the fix should be a shared "which namespace is this key" helper
next to the four `SecretRef` builders, not a third special case here.

**Fixed** as `secret.AccountKey` + `secret.NS*`, which the three prefixed
builders now compose their keys from, so the classifier and the key shapes cannot
drift apart. The account namespace is the un-prefixed one, so a tool id must
never equal a reserved prefix — guarded by
`TestToolIDsDoNotCollideWithKeyNamespaces`. Recorded in
[DATA-MODEL.md](DATA-MODEL.md) § Secret References.

### 2.2 Account rename/remove and profile remove ignore per-directory bindings — **FIXED** (the structural half; the claim was right that the index was the whole problem)

Held. Neither `kae account rm`/`rename` nor `kae profile rm` touched a directory
binding, and nothing anywhere recorded which directories were bound — the
fragment that names the store lives *in* the directory, and the store is named by
a hash of that path, so from outside there was nothing to enumerate.

Fixed by recording the bound path as a **breadcrumb inside the store**
(`isolation/<pin-id>/dir`), not as a registry beside it: a registry is a second
source of truth that drifts from the directory tree and needs its own repair
path, whereas here the store's existence *is* the record and the two cannot
disagree. On top of it: `kae account rm`/`rename` and `kae profile rm` now warn
per affected directory, naming the `kae pin` that re-binds it, and `kae doctor`
grows `pin_stale` for the two states nothing could see before — a bound directory
that is gone (its store orphaned forever) and one still pinned to an account that
is not captured.

**Not done, deliberately**: nothing rewrites another directory's fragment. Naming
the directory is the half that was impossible; the fix is one `cd` and one
`kae pin`, and a command that silently edits files outside the directory it was
run in is a worse trade. A directory that was merely `kae unpin`-ed is not
reported either — unpin keeps the store on purpose so a re-pin restores its
sessions.

### 2.3 `state.json` has no lock; concurrent switches on different tools lost-update — **FIXED** (the main claim held; the `agy` half was overturned)

The main claim held exactly as written. `buildSwitch` loads state under the
per-tool locks only (`switch.go`), then spends seconds writing credentials
before `saveActive` writes the whole document back — and `kae use claude <a>`
and `kae use codex <b>` hold different locks, so both run. Five load→mutate→save
sequences had the shape, not one: `saveActive` (switch/capture/login/account),
rollback, `teardownSynced`, `runUseIsolated`.

Fixed as one seam, `App.mutateState`: take a new `state` lock, **re-read the
file**, apply the mutation, save. The re-read is what makes the update
lost-free; the lock is what makes the re-read atomic. Recorded in
[ARCHITECTURE.md](ARCHITECTURE.md) § Locking and as a boundary in
[AGENTS.md](../AGENTS.md), with a guard test keeping `state.Save` out of the
rest of `internal/cmd` — the seam is a convention, since `state.Save` stays
exported for fixtures and read paths.

**What review found that the audit did not, and it is the more interesting
half:** a lock is not enough, because the *decision* can be stale even when the
write is not. `kae account rm` and `kae account rename` read "is this the active
account" **before** taking the tool lock and then applied that answer inside the
fresh, locked read — so a switch completing in between made them clear or rename
a binding nobody asked them to touch. The lock relocated that bug rather than
closing it. Both now re-derive the answer inside the mutation. Generalize:
**whenever a seam re-reads, every predicate over what it re-read has to move
inside it too.**

**Overturned:** the audit's companion claim that `agy` has no `Identifier`, so
the identity guard has nothing to compare for it. agy implements
`Identity()` (`internal/adapter/agy/agy.go`), and
`TestIdentifierConformance` requires it of **every** adapter, so the claim could
not have been true of any tool. Nothing to do.

### 2.4 The pin family is unlocked, unbacked-up, and leaves stores behind — **PARTLY FIXED; two of the three sub-claims were overturned**

- **Unlocked — held, fixed.** `kae pin`, `kae pin <tool> <account>` and
  `kae unpin` took no lock at all. They now take a per-directory `pin-<pin-id>`
  lock, so two of them in one directory cannot interleave into a fragment that
  points at a store the other re-keyed. Per directory, not global: binding two
  different directories at once was never a problem.
- **"No backup of the previous per-directory credential" — true, and it does not
  matter.** That credential is a *copy* of the account snapshot
  (`writeDirCredential` reads the snapshot, never the live store — that was fixed
  in v0.12.0), so `kae pin <tool> <account>` reproduces it byte for byte. A
  half-done bind is re-runnable, not lost data, which is why `kae rollback` has no
  pin state and does not need one. Recorded in
  [ARCHITECTURE.md](ARCHITECTURE.md).
- **"Toggling `pin -s` ↔ `pin -i` never tears down the old store" — overturned.**
  `pruneDirCredentials` (shipped with the codex per-directory work) already sweeps
  every store of the pin the new binding does not keep. It sweeps **keychain items
  only**, and that asymmetry is deliberate and documented: a file store's
  credential lives *inside* the store directory, which a mode toggle and `kae
  unpin` keep along with its sessions and settings, while an item is invisible from
  the directory tree and would otherwise hold a credential nothing can find.
- **`PinID` does not resolve symlinks — held as a fact, rejected as a fix.** The
  reasoning that made it look mandatory does not survive checking: codex hashes a
  *canonicalized* `CODEX_HOME`, but `codex.storeKey` already applies
  `EvalSymlinks` itself (`internal/adapter/codex/codex.go`), so kae's account and
  codex's agree whatever shape `PinID` takes. What is left is narrow: the fragment
  lives in the directory, so both aliases of a symlinked path load the *same*
  fragment and the same store — the split only happens if you re-run `kae pin`
  through the other alias, which orphans the first store rather than splitting a
  live one, and `kae unpin --purge` through the other alias then sweeps nothing.
  Against that, canonicalizing changes the pin id for **every** user whose project
  path contains a symlink (`/tmp` on macOS, a symlinked `~/dev`), silently moving
  their sessions, settings and per-directory credential to a new store — with the
  old keychain items unreachable, because they are keyed by the old path. A
  guaranteed migration hazard for everyone to close a leak for a few. Left as is,
  now *visible* through `pin_stale`, and recorded in [ROADMAP.md](ROADMAP.md).

### 2.5 Smaller items — **AGENT-CLAIMED**

`backup.Prune` races across tools (one global dir, per-tool locks); shared-mode
single-tool rebind cannot self-heal a wiped bond dir while isolated-mode can;
`run -i` inside a `pin -i` directory for the same account makes two never-synced
credential copies; `kae doctor <tool>` silently skips all companion checks;
`kae pin` never checks the tool binary exists; `companion_drift`'s remediation
suggests `mise env`/`mise trust` when the real fix is re-running `kae pin`.

### 2.6 Confirmed clean

Recorded so nobody re-audits: backup-before-write ordering in the switch/run
transaction (including restoring *all* tools when `saveActive` fails); lock
acquisition in canonical order (no deadlock); pin materializes directories before
writing the fragment that points at them; the TOCTOU between a switch's
`account.Load` and a concurrent `account rm` fails loudly and restores; atomic
writes chmod before writing bytes; metadata files never carry secret bytes;
`runner.Snippet` is never applied to a credential read's stdout; rebind round-trips
and full re-pin are idempotent and self-healing; `run --env` fails loud on a
missing env profile. The `security -w` argv exposure is a real but unavoidable
macOS CLI constraint, already accepted in [SECURITY.md](SECURITY.md), and kae uses
stdin wherever an alternative exists (`secret-tool`).

One small real gap in that area: the three newest doctor probes
(`identity_drift`, `companion_drift`, `companion_token_drift`) are safe by
inspection but none has the "the value never appears in the message" assertion
AGENTS.md requires for a new output path.

---

# Part 3 — Proposed skill: respond to an upstream auth change

The gap is not that kae lacks a monitor. It is that when an upstream tool changes
how its authentication works, getting from "something is off" to "kae is correct
again and the assumption is re-recorded" is a long, easy-to-botch sequence that a
session without prior context has to rediscover — which is precisely what happened
twice, and what this whole document is the residue of.

So the skill is a **response workflow**, not a watchdog. Detection is one of its
four entry points and the least valuable one: both real failures were noticed by a
human first.

> **Correct an audit claim before you build on it.** The audit proposed
> generalizing the `security` PATH shim to "every keychain-backed tool: claude,
> codex, agy, cursor". **That is wrong for codex** — verified here: with a shim on
> PATH and a temp `CODEX_HOME`, the shim log stays empty and codex fails with
> "Platform secure storage failure: A default keychain could not be found", i.e.
> the Rust `keyring` crate calls Security.framework directly. The shim works for
> claude because claude shells out to `/usr/bin/security`. **Establish per tool
> whether it shells out before assuming the shim applies** — and note that the
> guess in this box was itself wrong once: **agy does shell out** (its Go keyring
> library invokes `/usr/bin/security` with `find`/`add`/`delete-generic-password`
> and no `SecItemAdd`/`SecItemCopyMatching` appears in the binary), so the shim
> applies to it after all, settled in 1.5. cursor is still unverified.

## What the skill is for

**One sentence: when an upstream tool changes how its authentication works, the
skill detects it, establishes what the new rule actually is, updates kae to match,
and re-records the assumption — as one workflow.**

The mistake to avoid in designing it: treating it as a *monitor*. Detection is the
cheapest part and the least valuable, because both failures kae has actually had
were noticed by a human before any check existed. The expensive, error-prone part
is the middle — measuring the new behaviour without a login, and getting kae's
model to match it exactly — and that is where a skill earns its keep, because it
is the part where a session without prior context flails.

Name it for the job: `upstream-auth-drift`.

### Entry points (all four are normal, not just the first)

1. **A signal fired** — `upstream_version` warns, `identity_drift` warns, or one of
   the fingerprints in step 2 below moved.
2. **A tool upgraded** — the highest-yield moment, because the old bundle is often
   still on disk to diff against.
3. **A human suspects something** — "kae says I switched but the tool shows the old
   account". *This is how both real failures were found.* The skill must accept a
   symptom as input, not only a check code.
4. **Routine** — the age check (nothing has re-verified tool X in N days).

### The phases

**Phase 1 — Scope the suspicion.** Turn the symptom into a falsifiable statement
about *one* assumption. Read [VALIDATION.md](VALIDATION.md) § "Upstream Behaviour
Assumptions" for the tool and pick the row(s) that would produce this symptom; if
no row would, that itself is the finding (a load-bearing assumption nobody wrote
down — Part 1 and Part 2 above are full of those). State what you expect to see if
the assumption still holds, before measuring.

**Phase 2a — Read the official source first.** Reverse-engineering is the fallback,
not the first move. Finding 1.2 in this document is the case study: binary
inspection left the codex store key ambiguous and led to a wrong-module red
herring, and one file read from the public repo settled it exactly — including a
detail measurement would probably have missed (16 hex chars, over a *canonicalized*
path, where claude uses 8 over the raw NFC string).

| Tool | Public source of truth | Notes |
|---|---|---|
| codex | `openai/codex` (Rust, public). Tags are `rust-v<version>`, matching `codex --version` | authoritative; read the file at the **installed** tag, not `main` |
| opencode | public (the `sst/opencode` path now redirects to `anomalyco/opencode` — confirm the canonical owner before citing it) | authoritative |
| claude | not open source. Public docs + changelog/release notes only; auth internals (keychain naming, TTLs) are **undocumented** | docs give intent; the bundle gives what shipped |
| cursor / copilot / agy | closed | bundle inspection only |

Two disciplines that matter:

- **Pin the version.** Read the source at the tag matching the installed binary.
  `main` describes a future the user is not running, and a doc page describes
  whatever shipped last week.
- **When docs and the shipped artifact disagree, the artifact wins.** kae depends on
  behaviour, not intent. Docs are for finding *what to look for* and for
  understanding *why* something changed; the measurement is what you record. When
  both agree, say so in the VALIDATION row — an assumption confirmed from two
  independent sources is the one you can leave alone longest.

Beyond source: release notes and changelogs are the cheapest way to spot an auth
change *before* a user does, and an issue tracker often has the change discussed in
plain language. For a closed tool, the public docs plus the bundle-pair diff
(technique below) is the whole available surface.

**Phase 2b — Measure what no source documents.** For claude's TTLs and keychain
naming, and for every closed tool, there is nothing to read — so measure. The
toolkit, and which tool each technique works on:

| Technique | Proves | Applies to |
|---|---|---|
| Subprocess PATH shim (temp HOME, log argv, exit non-zero) | the exact store name/attribute the tool asks for | **only tools that shell out** — claude yes, codex **no** (verified above) |
| Live keychain *attribute* read (`security find-generic-password -s <svc>`, never `-w`) | which items exist and their account attributes | any macOS keychain tool, read-only, no payload |
| Literal-count fingerprint over the bundle | every name kae models still exists and is referenced as often | any bundled tool |
| Identifier-normalized behaviour-site hash | the *control flow* around a semantic anchor is unchanged, even when minified names churn | any bundled tool |
| Bundle-pair diff (old vs new version on disk) | a targeted re-verify list for this specific upgrade | tools that keep old versions (claude does) |
| Loopback stub endpoint | refresh-failure paths (tombstone) without a real account | tools whose token endpoint is configurable |

Measured facts worth keeping (all reproduced in this session): across claude
2.1.218/219/220 the counts `-credentials` 17, `profileFetchedAt` 3,
`refreshTokenExpiresAt` 5, `CLAUDE_CONFIG_DIR` 27,
`CLAUDE_SECURESTORAGE_CONFIG_DIR` 11, `claude-code-user` 2, `86400000` 37 are
**identical**, while minified identifiers changed underneath; the
`oauthAccount?.profileFetchedAt` site hash is identical across all three even
though the TTL identifier went `TSg` → `sxg`, and resolving it still yields
`86400000`. `strings -n 6` is useless on a minified bundle — it is one multi-MB
"string"; use bounded `grep -o` or split on delimiters.

**Record the condition, never an absolute.** The v0.12.0 post-mortem is exactly
this: "`/oauthAccount` self-heals" was recorded as a fact when the fact was "it
self-heals past a 24h TTL that every refresh renews". An absolute is what expires
silently.

**Phase 3 — Update kae to match.** The part the first draft of this document
under-specified. In order:

1. **Find every place kae encodes the old rule.** The v0.12.0 bug lived in one
   constant, but the *fix* had to touch four call sites because the same credential
   copy was written four times. Grep for the constant, then grep for the callers of
   whatever resolves it — a rule encoded once but consumed four times fails in four
   places.
2. **Put the rule in the adapter, never at the call site.** This is now a boundary
   in [AGENTS.md](../AGENTS.md): only the adapter may evaluate where a credential
   lives. If a caller needs it for a different environment, build an `adapter.Env`
   for that environment and ask again (`dirCredentialSpec` is the worked example) —
   do not recompute a name.
3. **Decide fail-loud vs warn vs refuse, and prefer refusing to guessing.** When the
   new rule puts the credential somewhere kae cannot model, report the tool
   unsupported rather than writing where nothing reads
   (`CLAUDE_SECURESTORAGE_CONFIG_DIR` is the pattern). When one tool of a profile is
   affected, warn and bind the rest; when the user named that tool, fail.
4. **Check the blast radius on the other tools before generalizing.** The single
   most dangerous moment in the v0.12.0 work was generalizing "write the
   per-directory keychain item" to every keychain artifact — which would have
   destroyed codex's *global* login, because its item is one global item and its
   spec deletes before writing. Any change to shared credential IO must be checked
   against every adapter, and a capability like that should be **declared by the
   adapter with the safe default** (`KeychainDirScoped`, default false), with a
   parity test that derives the truth and fails if the declaration disagrees.
5. **Leave a regression test that fails on the old behaviour.** Compute expected
   values *outside* kae (an external `shasum`/`hashlib`), or the test agrees with
   whatever formula the code has, including a wrong one.

**Phase 4 — Re-record, in the same commit.** `VerifiedVersion()`, the
`docs/ADAPTERS.md` table, and the VALIDATION row(s). The condition, not the
absolute. Also update the row's *verification procedure* if you found a cheaper one
— the shim procedure replaced a "seed the file, wait for a token refresh" recipe
that needed a real account, and that is a permanent improvement for everyone after
you. **As of 2026-07-30 no `VerifiedVersion()` has ever been bumped**
(`git log -S 'VerifiedVersion() string { return'` returns only the introducing
commit), so this discipline has never actually been exercised — expect to be the
first, and expect nothing mechanical to remind you until technique 1 below exists.

**Phase 5 — Verify.** `mise run check` is the only gate. Add the login-free smoke
for the rule you just changed, and prove it has teeth by running it against the
pre-fix build or by mutating the new code — a smoke that cannot fail is
documentation, not verification. What genuinely needs a real account (does the
credential alone authenticate; does the payload round-trip and pass the keychain
ACL) stays in release acceptance; the skill's job is to shrink that list, not to
pretend to replace it.

### What to automate first (each pays for itself before the next)

1. **Version-copy agreement + assumption age.** A Go test parsing the two doc
   tables and asserting both match `VerifiedVersion()`, plus a `verified_on` date
   per tool and a doctor check that warns past N days. Sub-second, needs no upstream
   tool, and covers the blind spot `upstream_version` cannot: **a user who never
   upgrades gets no signal at all today.**
2. **Offline doctor checks where local state can contradict a modelled assumption**
   (~10 lines each): a `Codex Auth` item present while the resolved store is
   file/auto, or the reverse; `CLAUDE_CODE_CUSTOM_OAUTH_URL` set; more than one
   keychain item under a resolved service; an active snapshot lacking
   `refreshTokenExpiresAt`.
3. **Literal-count fingerprints**, per tool, wired into `mise run audit`.
4. **The shim harness**, table-driven, gated on the per-tool "does it shell out"
   answer — and it should diff *the tool's* argv log against *kae's* argv log, which
   is the naming-agreement check in VALIDATION.md turned into a script.
5. **Behaviour-site hashes** for the three or four sites that encode real behaviour,
   then bundle-pair diff on upgrade.

## Suggested order

**1.2's second consequence first** — a kae codex switch under the keyring store
deletes another `CODEX_HOME`'s login, it is confirmed from official source, and it
is shipped. Everything else in this document is a wrong-credential or a warning; that
one destroys a login.

Then Part 2.1 (verified, self-contained, makes `doctor` trustworthy again), then
Part 1.1 (measure cursor's refresh behaviour — it may be a P0), then 1.3 and 1.4
(both settle from a source read, not a measurement), then automation items 1 and 2,
which need no upstream tool at all. The rest after that.
