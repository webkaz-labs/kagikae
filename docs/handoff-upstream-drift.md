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
(branch `fix/cursor-full-credential-set`) and **1.4** (branch
`fix/claude-custom-oauth-url`).

**Still open here**: 1.5, 1.6, the rest of Part 2, and Part 3's skill. Also open,
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

### 1.5 copilot / opencode / agy: home-var and store assumptions — **AGENT-CLAIMED**

- **copilot** does not honour `COPILOT_HOME`; `copilot.go:44-46` hard-codes
  `$HOME/.copilot/config.json`. One-line fix if true.
- **opencode**: upstream reportedly uses `XDG_DATA_HOME` without an absolute-path
  check, while `paths.go` ignores a relative value per the XDG spec. A relative
  value would put the two on different files. kae should warn rather than silently
  diverge. Also claimed: the credential store moved across versions
  (`account.json` → `auth.json` → a DB), and an `account.json` reportedly exists on
  this machine — check whether kae is patching a file nothing reads.
- **agy** may fall back to a file when the keychain write fails; kae treats darwin
  as keychain-only and skips the file check there.

### 1.6 Cross-cutting: kae reads keychain items service-only, tools read account-scoped — **VERIFIED HERE (mechanism), AGENT-CLAIMED (consequence)**

`keychain.ReadItem` matches by service and takes the first item. claude reads
`find-generic-password -a <account> -s <service>`. Verified here that claude's live
item carries `"acct"<blob>="yamawaki"` — i.e. `$USER`, matching what
`keychainAccount` now computes, so today they agree.

The claimed consequence is worse than the read: `internal/artifact/artifact.go`
prefers **the existing item's account** over the adapter-computed one for
claude/cursor. So one item created under a wrong account (an old `$USER`, or the
`"kagikae"` fallback) makes kae write there forever while the tool looks
elsewhere — self-perpetuating, and invisible. This is the same shape as
[ROADMAP.md](ROADMAP.md)'s recorded stale-account item, but the audit argues it is
broader than recorded. Consider `KeychainMatchAccount` for claude/cursor now that
the account rule is known and tested.

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

### 2.2 Account rename/remove and profile remove ignore per-directory bindings — **AGENT-CLAIMED**

`kae account rm` / `rename` reportedly never touch `IsolatedConfigDir` or any
directory fragment, and **there is no index of pinned directories anywhere**. A
renamed account would leave a pinned directory bound to a name that no longer
exists, with no command able to find or fix it. `kae profile rm` reportedly also
leaves companion secrets in the backend and gives no diagnostic in a directory
pinned to the deleted profile.

The missing pinned-directory index is the structural item here — several findings
in this part reduce to it. Consider whether kae should record bound directories
(it already derives a stable `PinID`), because "no command can find it" is what
makes each of these unrecoverable rather than merely untidy.

### 2.3 `state.json` has no lock; concurrent switches on different tools lost-update — **AGENT-CLAIMED**

Per-tool locks let `kae use claude X` and `kae use codex Y` run concurrently, and
`saveActive` writes the whole document from a copy loaded before the other
finished. The loser's field silently reverts, so `status` and `MatchProfile`
report a wrong active account that `kae rollback` does not fix. Claimed to recur
at every `loadState`/`state.Save` pair. Also claimed: `agy` has no `Identifier`,
so the identity guard that would catch a resulting mismatch has nothing to compare
for that tool.

Verify by reading `switch.go` around `acquireLocks`/`saveActive`; the fix is
presumably a state-file lock or a read-modify-write immediately before save.

### 2.4 The pin family is unlocked, unbacked-up, and leaves stores behind — **AGENT-CLAIMED**

- `kae pin <tool> <account>` mutates the directory's credential, then companion
  files, then the fragment, with no lock and **no backup of the previous
  per-directory credential** — `kae rollback` has no concept of pin state. A
  mid-sequence failure leaves the live credential and the fragment disagreeing.
- Toggling `pin -s` ↔ `pin -i` in one directory never tears down the old store.
- `PinID` hashes `filepath.Abs` without resolving symlinks, so two path aliases for
  one directory fork its isolation store in two.

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
> whether it shells out before assuming the shim applies** — agy is a Go binary
> using a keyring library and is probably in codex's camp; cursor is unverified.

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
