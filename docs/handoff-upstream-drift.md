# Handoff — upstream drift: what is still wrong, and how to stop finding out the hard way

**Status**: not started. Written 2026-07-30, right after v0.12.0 shipped.
**Branch**: start a new one off `main` (`535d31f` or later).
**Why this file exists**: v0.12.0 fixed one instance of a defect class — *kae
modelled an upstream storage location as a constant when it is actually a rule* —
and the audit that followed found the same class in four more places, plus a set
of gaps that have nothing to do with upstream at all. This file carries the
findings with their evidence, and a design for the recurring process that should
have caught them.

Read [VALIDATION.md](VALIDATION.md) § "Upstream Behaviour Assumptions" first: it
is the current mechanism, and Part 3 here is about replacing its manual half.

## How to read the evidence labels

Findings came from four parallel read-only audits. **Everything below is labelled
by who verified it, because the labels change what you should do first:**

- **VERIFIED HERE** — reproduced in this session, with the command that did it.
  Act on these directly.
- **AGENT-CLAIMED** — an audit agent reported it with a plausible citation, and it
  was *not* independently reproduced. Re-verify before building on it. One
  agent claim was already found wrong (see the box in Part 3) — treat the rest as
  leads, not facts.

---

# Part 1 — Same defect class as the v0.12.0 gate

Each of these is "kae treats an upstream location/name as fixed; it isn't."

### 1.1 cursor: kae switches one of at least two keychain items — **VERIFIED HERE**

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

### 1.2 codex: the keyring account attribute may be derived from `CODEX_HOME` — **AGENT-CLAIMED**

The claim: the `Codex Auth` item's account is not a per-login opaque id but
`cli|` + `sha256(canonical CODEX_HOME)[:16]`, i.e. the same shape of rule claude
uses for its *service* name.

Corroborated here: `cli|` does appear in the binary next to
`login/src/auth/storage.rs`, "failed to load CLI auth from keyring", and
**"Failed to remove auth.json"** — that last one is claude's delete-the-file
behaviour in codex too. Not corroborated: the hash derivation. A
`compute_store_key` symbol exists but in `codex_rmcp_client::oauth`, which is MCP
server OAuth, **not** CLI auth storage — so the obvious symbol is a red herring
and the audit may have conflated them.

`Codex Auth` does not exist on this machine (this codex uses the file store), so a
live attribute read cannot settle it. If the claim holds, three things follow and
all three matter:

1. `internal/cmd/dircred.go`'s refusal for codex is **over-strict** — codex would
   be per-directory scopeable after all, by account rather than by service. That is
   a capability we are currently declining, not a bug.
2. `artifact.Spec.KeychainReplace` deletes by **service only**
   (`internal/artifact/artifact.go`, `keychain.DeleteItem`). With two codex homes
   there are two legitimate items under one service, so a global codex switch could
   **delete another directory's login**. This is the destructive one.
3. `docs/ADAPTERS.md` and `docs/VALIDATION.md` both describe the account as an
   opaque per-login id and assert "exactly one item after a switch". Both would be
   wrong.

### 1.3 codex: `auto` and `ephemeral` stores are folded into "file" — **AGENT-CLAIMED**

`configuredStore` (`internal/adapter/codex/codex.go:51-68`) maps everything that
is not `"keyring"` to the file driver, and returns `"auto"` on a parse failure
(fail-open). The claim is that upstream's enum is `file` | `keyring` | `auto` |
`ephemeral`, that `auto` means *keyring first, file only on failure* and **deletes
`auth.json` when it stores to the keyring**, and that a `secrets` keyring backend
adds an encrypted file store kae does not model at all.

The `auth.json` deletion half is corroborated by the binary string above. If the
rest holds, a user who sets `auto` — a value kae's own docs list — gets the
v0.12.0 failure shape on codex: kae writes `auth.json`, codex reads the keyring,
every guard green.

### 1.4 claude: `CLAUDE_CODE_CUSTOM_OAUTH_URL` moves *both* stores — **AGENT-CLAIMED, cheap to settle**

Already recorded in [ROADMAP.md](ROADMAP.md) for the keychain service name. The new
part of the claim is that the same suffix appears in the **identity file name** —
`.claude<suffix>.json` — and that a `-staging-oauth` channel exists too. If so,
`claudeJSONPath` is wrong under that variable as well as `keychainService`.

This is class (a): a single env var, detectable offline with no measurement. The
cheapest correct move is the refusal `CLAUDE_SECURESTORAGE_CONFIG_DIR` already
gets in `driver()`. Settle the file-name half with the shim procedure in
VALIDATION.md — it takes one run.

Same box, unverified: `CLAUDE_CODE_HOST_CREDS_FILE` /
`CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST` / `CLAUDE_CODE_HOST_AUTH_ENV_VAR` were
reported present in the bundle as a host-managed third credential store. If real,
same one-line refusal covers them.

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

### 2.1 `doctor`'s orphan check false-positives on two whole namespaces — **VERIFIED HERE**

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

# Part 3 — The recurring process (proposed skill)

The manual half of VALIDATION.md is the weak point: re-verifying an assumption
currently means a human deciding to. Two failures in a row were found by a user
noticing, not by a check.

> **Correct an audit claim before you build on it.** The audit proposed
> generalizing the `security` PATH shim to "every keychain-backed tool: claude,
> codex, agy, cursor". **That is wrong for codex** — verified here: with a shim on
> PATH and a temp `CODEX_HOME`, the shim log stays empty and codex fails with
> "Platform secure storage failure: A default keychain could not be found", i.e.
> the Rust `keyring` crate calls Security.framework directly. The shim works for
> claude because claude shells out to `/usr/bin/security`. **Establish per tool
> whether it shells out before assuming the shim applies** — agy is a Go binary
> using a keyring library and is probably in codex's camp; cursor is unverified.

## What the skill should do

Name it for the job — something like `upstream-drift-audit`. It is a *tool-facing*
skill: given a tool (or all), it runs the cheap checks, reports a re-verify
worklist, and updates kae's recorded assumptions in lockstep.

**Techniques, ranked by cost/benefit. The first two need no upstream tool at all:**

1. **Version-copy agreement + assumption age.** Each tool's verified version is
   written in three places (`VerifiedVersion()`, the `docs/ADAPTERS.md` table, the
   per-row column in `docs/VALIDATION.md`) with nothing asserting they agree. Add a
   Go test that parses the docs and asserts all three match, plus a `verified_on`
   date per tool and a doctor check that warns past N days. **Sub-second, and it
   closes `upstream_version`'s biggest blind spot: a user who never upgrades never
   gets any signal at all.**
2. **Offline doctor checks for assumptions that local state can contradict**
   (~10 lines each): a `Codex Auth` item existing while the resolved store is
   file/auto (or the reverse); `CLAUDE_CODE_CUSTOM_OAUTH_URL` set; more than one
   keychain item under a resolved service; an active snapshot whose credential
   lacks `refreshTokenExpiresAt`.
3. **Literal-count fingerprint over the installed bundle.** Per tool, a table of
   the load-bearing literals kae models with their expected occurrence counts.
   Measured across claude 2.1.218/219/220: `-credentials` 17, `profileFetchedAt` 3,
   `refreshTokenExpiresAt` 5, `CLAUDE_CONFIG_DIR` 27,
   `CLAUDE_SECURESTORAGE_CONFIG_DIR` 11, `claude-code-user` 2, `86400000` 37 —
   **identical across all three**, while minified identifiers churned underneath.
   Proves every name kae models still exists and is referenced as often. Does not
   prove semantics; a cosmetic count change is a false alarm that costs one manual
   re-verify, which is the safe direction.
4. **Identifier-normalized behaviour-site hash.** Take ±110 chars around a semantic
   anchor, replace every identifier with `X`, hash. For
   `oauthAccount?.profileFetchedAt` the hash was **identical across 2.1.218–220**
   even though the TTL identifier changed (`TSg` → `sxg`), and resolving it still
   yields `86400000`. This is how you confirm a *behaviour* statically, with no
   login. ~30s per bundle; release-time, not per-commit. Note `strings -n 6` is
   useless on a minified bundle — it is one multi-MB "string"; use bounded
   `grep -o` or split on delimiters.
5. **The subprocess-shim harness, for tools that shell out.** Table-driven:
   expected (subcommand, `-s`, `-a`) tuples per tool, temp HOME, shim on PATH, and
   **diff the tool's argv log against kae's own argv log** — the naming-agreement
   check already written up in VALIDATION.md, turned into a script. Runs on macOS
   CI, no login, no real keychain. Gate it on the per-tool "does it shell out"
   answer from the box above.
6. **Bundle-pair diff on upgrade.** claude keeps old versions on disk; on a version
   change, run 3 and 4 against old and new and report only the sites that moved —
   a targeted re-verify list instead of "re-verify everything".

**Deliberately not automated:** anything needing a valid signed token or a real
account (does the credential alone authenticate; does the keychain payload
round-trip and pass the ACL; the 24h refetch actually rewriting the email; the
delete-on-write half of the storage rule). Those stay in the release acceptance
run, and the skill's job is to shrink that list, not pretend to replace it.

**The skill must also write back.** Finding drift is half the job; the other half
is updating `VerifiedVersion()`, the ADAPTERS table, and the VALIDATION rows **in
the same commit** — the discipline AGENTS.md already requires and that
[nothing currently enforces](VALIDATION.md). Note for whoever builds this: as of
2026-07-30 no `VerifiedVersion()` value has ever been bumped
(`git log -S 'VerifiedVersion() string { return'` returns only the commit that
introduced it), so the lockstep rule has never actually been exercised.

## Suggested order

Part 2.1 first — it is verified, self-contained, and makes `doctor` trustworthy
again. Then Part 1.1 (measure cursor's refresh behaviour; it may be a P0). Then
skill techniques 1 and 2, which need no upstream tool and pay for themselves
immediately. Then settle the codex claims in 1.2/1.3, since one of them is
potentially destructive. Techniques 3–6 after that.
