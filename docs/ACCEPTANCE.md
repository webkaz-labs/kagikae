# Release Acceptance

What a release verifies on a real machine, plus account-combination checks an
operator may run when the required accounts are available. Nearly everything here
needs a real keychain, a real login, or both — the exception is
§ Bound-directory credential store's shim procedure, which needs no account —
and `mise run check` reaches none of it. § Credential-expiry observation — does
`refreshTokenExpiresAt` predict the login's death? is not a second exception to that
but an observation rather than a run, so a release owes it nothing. Account
combinations outside the release run are classified in
§ Optional account-combination checks. Read
[VALIDATION.md](VALIDATION.md) for what a commit owes; two release-only smokes
stayed there, beside the surfaces they check.

**Every result is recorded here, under the check it settles, naming the exact
candidate revision it was run against and the release tag when one exists.** This
document owns the results; results recorded elsewhere are invisible to the next run.

## Applicability for a maintainer-only release

A release that changes only maintainer tooling may reuse recorded live-account
acceptance for unchanged application behavior. Compare the candidate with the
accepted revision: relevant application sources, dependencies and build inputs
must be unchanged, apart from the reviewed version literal and its report test.
Confirm the reviewed upstream version and driver mechanism, and run the installed
behavior audit. Compare artifact digests when the earlier record contains them;
without an earlier digest, record version-level agreement rather than claiming
identical bytes. A contrary incident, relevant drift, changed assertion or unclear
impact requires the affected live check again, with the session protections below.

Record which original results are reused, their original revision/date, the
comparison and its limits. Reuse is not a new live run or assurance that a current
login is healthy. Re-run affected maintainer checks and isolated smokes; verify the
newly published artifacts and installer. Application changes still require the
affected live acceptance before release.

## Credential-expiry observation — does `refreshTokenExpiresAt` predict the login's death? (**open**)

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

Reconfirmed 2026-09-05 on the installed v0.18.1 candidate at `b5e5815`, with
Claude Code 2.1.260 and two freshly captured accounts. A stale `side` snapshot first
failed the required fresh-process check, so the account was logged in through the
official flow and immediately recaptured before the measured run. `side` → `main` →
`side`, rollback to `main`, and the final apply of `side` each authenticated in a
fresh prompt and matched the official profile endpoint. Every transition preserved
every byte outside the `oauthAccount` value; the final active account was `side`.

Reconfirmed 2026-09-06 (JST) on the v0.18.2 implementation candidate at
`67b6ff2`, tested from `3269b67` with the same executable code, with Claude Code
2.1.261 and two freshly captured accounts. `main` → `side` → `main` and an
explicit rollback to `side` each authenticated in a fresh process and matched the
identity cache against the official profile endpoint. Every kae mutation preserved
every byte outside `/oauthAccount`; the accounts had distinct identities.

Reconfirmed 2026-09-06 on the v0.18.3 executable candidate at `64c4ec3`,
with Claude Code 2.1.261 after the operator stopped the active `side` session.
`side` → `main` → `side` → `main`, then an explicit rollback to `side`, each
passed a fresh-process prompt and profile/cache identity comparison. The measured
global switches and rollback preserved every byte outside `/oauthAccount`.
Captures were updated after fresh authentication rather than rejecting a stored
access token before the CLI could refresh it.

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

Reconfirmed 2026-09-05 on the installed v0.18.1 candidate at `b5e5815`, with the
one available Copilot account. Capture, apply, fresh non-interactive prompt and
rollback passed; the leading JSONC comments and every value outside
`/lastLoggedInUser` survived, and the per-account keychain item was not rewritten.
The optional two-account switch remains unmeasured.

Reconfirmed 2026-09-06 (JST) on the v0.18.2 implementation candidate at
`67b6ff2`, tested from `3269b67` with the same executable code, with Copilot
1.0.83 and one account captured immediately before the run. Same-account apply and
explicit rollback preserved every byte outside `/lastLoggedInUser`, including the
leading JSONC comments, and the addressed keychain item's attributes were unchanged.
A fresh non-interactive prompt returned the requested reply with no tools enabled.
The optional two-account switch remains unmeasured.

Reconfirmed 2026-09-06 on the v0.18.3 executable candidate at `64c4ec3`,
with Copilot 1.0.83 and one freshly captured account. Same-account apply, a fresh
prompt with no tools enabled, and explicit rollback passed. Bytes outside
`/lastLoggedInUser`, leading JSONC comments and keychain item attributes were
unchanged. The optional two-account Copilot switch remains unmeasured.

### Bound-directory credential store (macOS, no login needed)

kae and claude must agree on the keychain service and account for a bound directory.
Run `mise run naming-agreement` from the repository with Go, Python 3.11 or newer,
and the reviewed macOS Claude binary installed. The task runs its refusal and
mismatch controls before comparing independently observed addresses.

The harness in `scripts/namingagreement/` reuses `scripts/smoke-env.sh` inside an
owned temporary directory. It intercepts the production adapter/artifact writer
through `internal/runner`, and observes Claude's reads through a non-forwarding
`security` shim. It refuses missing or unreviewed Claude bytes before launching
them, checks the shim and isolated roots before each case, and rejects missing
observations, mismatches and plaintext credential files. The digest and its
reachability provenance live beside the executable check; updating them requires
the [upstream measuring procedure](../.claude/skills/upstream-auth-drift/references/measuring.md).
An empty log does not establish containment.

This checks the production credential writer, not the full `kae pin` command or
its generated environment. The real-account bind below supplies that wiring and
payload/ACL evidence. Supply **both** `CLAUDE_CONFIG_DIR` and
`CLAUDE_SECURESTORAGE_CONFIG_DIR` from the generated fragment there: passing only
the former asks Claude about a different credential store. The harness does not
relax `smoke-run.sh`'s file-driver guard and is not an OS network sandbox.

Confirmed 2026-09-06 on the v0.18.3 candidate at `64c4ec3` with
`mise run naming-agreement`: the production writer and Claude 2.1.261 read
addresses matched for the default, explicit config, trailing slash, decomposed
Unicode, separate credential directory, relative directory and invalid-USER cases.
The task's refusal/mismatch controls passed first; no plaintext credential fallback
was observed. This is the harness's adapter/artifact observation, not a fresh
real-account authentication or a generated-fragment wiring result.

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

Reconfirmed 2026-09-05 on the installed v0.18.1 candidate at `b5e5815`. The
login-free security shim produced the same independently derived service name for
kae and Claude. In a separate real-keychain scratch directory, `side` was bound and
then rebound to `main`; each fresh Claude process authenticated and its cache matched
the official profile endpoint, neither store contained plaintext credentials, and
`kae unpin --purge` removed the isolated credential. The global `side` login remained
authenticated and active afterwards.

The login-free naming procedure passed 2026-09-05 on the v0.18.2 candidate at
`67b6ff2`, with Claude Code 2.1.260. In a temporary HOME with a file secret backend
and simulated `security` commands, kae's write and a fresh Claude process's read
matched the independently computed credential-directory service name. Both
generated Claude variables were supplied, and neither directory contained a
plaintext credential file before or after Claude ran. The shim handled the
interactive stdin form of `security` as well as its ordinary argument form;
refusing that write in an earlier fixture caused Claude to fall back to plaintext.
Claude exited 1 with the synthetic credential. This result establishes naming
agreement only; the real-account run below supplies the authentication evidence.

Reconfirmed 2026-09-06 (JST) on the v0.18.2 implementation candidate at
`67b6ff2`, tested from `3269b67` with the same executable code, with Claude Code
2.1.261. A separate scratch inventory captured both live accounts through
`kae add --no-login`. Binding `side` and rebinding `main` each authenticated in a
fresh process using both generated Claude environment variables; the cache matched
the official profile endpoint and neither store contained plaintext credentials.
Purging the `main` binding removed its keychain item. The earlier `side` item
remained, so it was rebound and purged separately; its subsequent lookup returned
exit 44. The resulting account captures were returned through normal CLI operations,
with global `side` active and authenticated.

Reconfirmed 2026-09-06 on the v0.18.3 executable candidate at `64c4ec3`,
with Claude Code 2.1.261 and a temporary file-backend inventory. An isolated
`side` binding and its rebind to `main` each authenticated in a fresh process using
both generated environment variables; profile/cache identities matched and no
plaintext credential file was observed in either bound store. Purge removed the
`main` keychain item. Cleanup recreated a profile binding for the retained `side`
item and purged it too; both removed-item lookups returned exit 44. The latest
accounts were captured back through normal CLI operations, global `side` was
authenticated, and the temporary file-backend inventory was removed. A cleanup
attempt to use the one-tool rebind form after purge was refused because the
fragment was already gone; the profile form recreated the binding as required.

## Optional account-combination checks

These checks are optional for v0.18.0 and for future releases. They are retained as
repeatable coverage for an operator who has the required accounts; an incomplete or
unrun checklist does not block a release. Each check's result paragraph owns what
has and has not been measured.

For the v0.18.1 candidate at `b5e5815`, the Copilot two-account switch, Codex
two-account keyring round-trip, Cursor two-account/API-key directions, and Codex
per-directory keyring check were not run. Their existing partial results below remain
the available evidence. The last check is optional only as release evidence: Codex
per-directory keyring support remains fail-closed and is not enabled.

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
