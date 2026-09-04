# Release Acceptance

What a release verifies on a real machine, plus account-combination checks an
operator may run when the required accounts are available. Nearly everything here
needs a real keychain, a real login, or both — the exception is
§ Bound-directory credential store's shim procedure, which needs no account —
and `mise run check` reaches none of it. § Real-machine gate — does
`refreshTokenExpiresAt` predict the login's death? is not a second exception to that
but an observation rather than a run, so a release owes it nothing. Account
combinations outside the release run are classified in
§ Optional account-combination checks. Read
[VALIDATION.md](VALIDATION.md) for what a commit owes; two release-only smokes
stayed there, beside the surfaces they check.

**Every result is recorded here, under the check it settles, naming the exact
candidate revision it was run against and the release tag when one exists.** This
document owns the results; results recorded elsewhere are invisible to the next run.

## Real-machine gate — does `refreshTokenExpiresAt` predict the login's death? (**open**)

**If claude has just asked for a login it did not ask for before: read that account's
`relogin_by` out of `kae ls --json` before you log back in** ([CLI.md](CLI.md)
§ Credential freshness in listings). Logging in first destroys the measurement. What to
record is below; everything between here and it is why this section exists.

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
sibling measurement in this file, each of which is settled only by a dated run — and
[ADAPTERS.md](ADAPTERS.md) § Verified Upstream Versions is explicit that kae depends
on undocumented upstream *behaviour*, and that where docs and the binary disagree the
binary wins. Closing this
one on a citation would have set a precedent the rest of the file does not follow.

**This is an observation to record when it happens, not a run to schedule** (changed
2026-08-16; the scheduled form and why it was withdrawn are below). It blocks no
release. It keeps the date it was opened and stays open; what it no longer has is
anything to schedule.

**Do not apply § Real-Machine Acceptance's account precondition here.** This observation
applies nothing and provokes nothing, and its "re-capture with `kae add` immediately
before the run" half destroys the record: the deadline under test is the one the
*existing* capture holds, and re-capturing replaces it.

**Either of two moments produces a record — whichever happens, it is the only one.**

1. **claude asks for a login it did not ask for before.** Record which account; the
   deadline kae was reporting for it; the moment of the refusal; and whether that copy
   had been switched to, or run under, since it was captured.
2. **the reported deadline passes and claude keeps serving.** Record the same first
   two, and how far past it the session went on working.

**The deadline is not in `kae doctor --json`**, which prints one only inside the two
freshness codes' messages — so doctor is silent about a snapshot still reading `ok`,
and that is the refusal earlier than the lead-time window, the under-warn outcome below.

The last item of record 1 is what decides whether it measures anything: a copy the tool
refreshed after capture is not the copy whose deadline was recorded, so the interval
compares two different things. Note the claude version, since this is an undocumented
upstream behaviour like every other in
[VALIDATION.md](VALIDATION.md) § Upstream Behaviour Assumptions.

**Every one of these is a result, and the under-warn case is the one with user harm.**
A refusal near the recorded deadline confirms the timing claim `credential_stale` and
`credential_expiring` rest on. A refusal well *past* it, or record 2, means
`refreshTokenExpiresAt` is pessimistic and kae **over**-warns. A refusal well *before*
it means kae **under**-warns — it was reporting `ok` for a login already dead, which is
the direction a user cannot see coming and cannot work around.

**Why the scheduled form was withdrawn.** It asked for an aged specimen: capture
immediately after a `/login`, then leave that account untouched past `relogin_by` while
working in another. Nothing about it was wrong; it is that **the specimen is an account
nobody works in**, and an account in rotation is refreshed before its deadline arrives.
Whether a machine has one to spare is the operator's state and not this repository's:
on the machine this was withdrawn for, none was spare (2026-08-16), so every candidate
got refreshed first. A third account nobody works in would restore the scheduled form,
and that is the whole of what it costs; short of that, the opportunistic record above is
what is available.

This matters only to whoever restores it. **Which branch of it could cost a working
credential was never established** — an earlier note named the branch where the snapshot
still works, which is backwards on the rotation row in
[VALIDATION.md](VALIDATION.md) § Upstream Behaviour Assumptions, and the reasoning
pointing at the other branch is no better measured. Neither record above has such a
branch.

## Real-Machine Acceptance (release only)

This run doubles as the re-verification pass for the **Upstream Behaviour
Assumptions** table: work each installed tool's rows, then re-record in the
same commit — [VALIDATION.md](VALIDATION.md) § Upstream Behaviour Assumptions
carries what that means and routes on to the full copy set. `kae doctor` naming
`upstream_version` for a tool is the
signal that its rows are due.

**Which accounts to run any of this with, and it is the whole section's
precondition rather than one procedure's.** Every check below applies a
*capture-time* snapshot to a live credential store, and so does every check in
§ Optional account-combination checks. The one exception is § Bound-directory credential store's **shim
procedure**, which needs no account at all — but not that subsection as a whole:
its closing two-account pin needs the real keychain and real accounts, and so needs
this precondition like everything else. [VALIDATION.md](VALIDATION.md)
§ Upstream Behaviour Assumptions records the measurement that makes it
consequential: a
refresh rotates the refresh token, the superseded one is single-use, so two stores
holding copies of one account's credential invalidate each other — the copy that
did not refresh last is dead, and offline it is indistinguishable from a healthy
one. So: **use a throwaway or second account, never one you are working in, and
re-capture with `kae add` immediately before the run** so the snapshot being
applied is live. This is not hypothetical caution. It is the lesson of the v0.8.0
gate, which broke the maintainer's real login by applying a snapshot captured days
earlier and needed `claude /login` to recover.

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

Confirmed 2026-09-05 on the v0.18.0 candidate at `31e3594`, with Claude Code
2.1.260 and two freshly captured accounts. Both switch directions preserved every
byte outside the `oauthAccount` value, including the trailing-newline state. After
each switch a fresh prompt authenticated and the identity cache matched the official
profile endpoint; rollback returned to `side`. The run then applied `main` again,
where a fresh prompt and profile comparison passed.

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
between two accounts is the optional account-combination check below; with a single
account this release acceptance verifies the verbatim round-trip and comment
preservation.

Confirmed 2026-09-05 on the v0.18.0 candidate at `4d78206` with the one
available account: a same-account apply changed only `/lastLoggedInUser`, preserved
the leading comments and all values outside that pointer, and did not rewrite the
per-account keychain item. A fresh non-interactive prompt returned a reply. The
optional two-account switch remains unmeasured.

Reconfirmed 2026-09-05 on the installed v0.18.0 candidate at `9360bed`, after the
JSON/JSONC pointer rewrite at `31e3594`. A freshly captured same-account apply
changed only the representation of `/lastLoggedInUser`; every byte outside that
pointer and the leading comments remained unchanged. The keychain item's attributes
were unchanged, a fresh non-interactive prompt returned the requested reply, and
rollback preserved the comments and restored the same pointer value. The optional
two-account switch remains unmeasured.

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
3. `kae pin -i main` in a temp project dir. Read both `CLAUDE_CONFIG_DIR` and
   `CLAUDE_SECURESTORAGE_CONFIG_DIR` out of
   `.config/mise/conf.d/kagikae.toml`, and the `-s <service>` kae passed to
   `add-generic-password` out of the shim log.
4. Run the real binary against that same directory —
   `env -i HOME=<temp> PATH=<shim>:/usr/bin:/bin USER="$USER"
   CLAUDE_CONFIG_DIR=<config-dir>
   CLAUDE_SECURESTORAGE_CONFIG_DIR=<securestorage-dir>
   claude -p hi </dev/null` — and read the `-s <service>` it passed to
   `find-generic-password`.

The two service names must be identical and equal
`Claude Code-credentials-<sha8>`, where `<sha8>` is the first eight hexadecimal
characters of the SHA-256 of `<securestorage-dir>`; that variable, not the session
config directory, selects the credential store. Neither directory may contain
`.credentials.json` (the superseded plaintext copy is removed). Passing only
`CLAUDE_CONFIG_DIR` did not test this candidate's binding: its generated fragment gave
the credential store a different value, so claude derived a service from the config
directory while kae wrote the service derived from the secure-storage directory.

Confirmed 2026-09-04 on the v0.18.0 candidate at `495141f`, with Claude Code
2.1.246: the production-equivalent two-variable path matched the independently
derived service and left no plaintext credential file. The run used a temp file
backend and a `security` shim; it did not test a real account, payload round-trip or
keychain ACL. The same run against a pre-fix build writes no keychain item at all and
reads only the unsuffixed shared one, which is the original defect.

This is a naming-agreement check. That the payload itself round-trips is the
separate verbatim/ACL assumption in [VALIDATION.md](VALIDATION.md)
§ Upstream Behaviour Assumptions, and it needs the real keychain and a real
account — so the release run includes the two-account pin: re-bind in a pinned
directory, then launch claude there and confirm it reports the account kae bound.

Confirmed 2026-09-05 on the v0.18.0 candidate at `4d78206` with two real accounts.
A scratch directory was bound to `side`, then rebound to `main`; a fresh Claude
process authenticated as the bound account after each operation, and neither
isolated store contained a plaintext `.credentials.json`. `kae unpin --purge`
removed the binding and isolated credential, while the global `main` login remained
authenticated.

## Optional account-combination checks

These checks are optional for v0.18.0 and for future releases. They are retained as
repeatable coverage for an operator who has the required accounts; an incomplete or
unrun checklist does not block a release. Each check's result paragraph owns what
has and has not been measured.

One boundary is not relaxed: the codex per-directory check is optional for a
release, but it remains the mandatory evidence for enabling that capability. Codex
stays in `bindableNotYetDeclared`, so kae warns and writes nothing to a bound
directory's keychain store, until the check passes.

**Copilot two-account pointer selection** (all platforms, two live accounts).
Repeat the Copilot procedure above for both accounts and verify a fresh prompt after
each switch. Only `/lastLoggedInUser` may change; the per-account keychain items and
all other config values remain untouched.

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

Partially measured 2026-09-05 on the v0.18.0 candidate at `4d78206`, using one
account and an isolated temporary `CODEX_HOME` because the normal config is
host-managed. The isolated home used the keyring store, captured without payload in
`account.toml`, authenticated in a fresh `codex login status`, and held no plaintext
`auth.json`. The normal home remained authenticated before and after the isolated
home logged out, and that logout removed only the isolated keychain item. This does
not settle the two-account switch, item-attribute stability across accounts, or the
per-directory gate below.

**Cursor full credential set** (macOS, two live `cursor-agent` logins):

- [ ] `kae add cursor <name>` records `access_token` and `refresh_token` present;
      `api_key` present only for an api-key login.
- [ ] After `kae use cursor <other>`, `cursor-agent status` reports
      `authenticated` (not `partially-authenticated`) **and** the other account,
      and `security find-generic-password -s cursor-refresh-token` (attributes
      only) shows an `mdat` newer than the switch.
- [x] A snapshot captured before the set was switched (no `refresh_token` entry,
      e.g. by deleting that key from `account.toml`) refuses the
      switch naming `kae add --no-login cursor <account>`, and the live items are
      unchanged afterwards.
- [ ] With an api key configured on one account only: after switching to the
      account **without** one, `cursor-api-key` is absent (kae removed it) rather
      than still holding the other account's key.

Partially measured 2026-09-05 with the one available no-API-key account. On
`4d78206`, a normal apply restored the access and refresh items, kept the API-key
item absent, advanced the refresh item's modification time, and left
`cursor-agent status` authenticated. At `807ea5b`, an incomplete-snapshot preflight
refused with exit 10 before mutation; the snapshot, state, backup count, credential
digests, and keychain modification times all remained unchanged. The account stayed
authenticated after the fixture was restored. A second account and the API-key
removal direction remain unmeasured.

**codex per-directory keyring bind** (macOS, two codex homes; this is the
capability-enablement check that must pass **before** codex is dropped from
`bindableNotYetDeclared` in
`TestKeychainDirBindableMatchesTheItemIdentity`). Everything else is in place: the
account derivation is measured ([VALIDATION.md](VALIDATION.md)
§ Upstream Behaviour Assumptions), the flag now
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

Account choice for every optional check is § Real-Machine Acceptance's precondition
above, not a separate rule.

Never run real-machine acceptance or an optional account-combination check with
uncommitted work in progress in the live tool sessions.
