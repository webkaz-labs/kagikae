# Roadmap

Long-term ordering beyond the active release ([RELEASE.md](RELEASE.md)).
Implementation history lives in git log.

Shipped: v0.12.0 (switch claude's `/oauthAccount` identity with the
credential, and add the two offline `doctor` checks — `identity_drift` and
`upstream_version` — that watch for an upstream *behaviour* change no layout guard
can see; plus read refresh-token expiry and the post-failed-refresh tombstone
instead of guessing; and derive claude's keychain service name from
`CLAUDE_CONFIG_DIR` so a bound directory's credential lands in the store the tool
actually reads — the release gate, now met). v0.11.0 (companion re-bind lockstep + the opt-in
`companion_token_drift` doctor check that resolves a bound token's live login
(`gh api user`) against a recorded `expected_login`) shipped 2026-06-27.
v0.10.1 (finish `kae companion` shell completion and
make completion self-maintaining via `kae completion --refresh`) shipped
2026-06-23. v0.10.0 (companion-auth lockstep — bind git/gh/cloud-CLI
identity per profile, delivered by `kae pin`, plus a `companion_drift` doctor
check that flags when the live git commit identity diverges from the binding)
shipped 2026-06-23. v0.9.1 (manual login-identity override — `kae add
--identity` and `kae account set-identity` — for tools kae cannot auto-detect,
e.g. agy on current Antigravity) shipped 2026-06-20. v0.9.0
(installable binaries: a GoReleaser pipeline +
`scripts/install.sh` + CI so `curl | sh`, mise, and prebuilt
darwin/linux archives work, with the README rewritten to OSS parity; additive,
no contract break — see [RELEASE.md](RELEASE.md)) shipped 2026-06-19. v0.8.9
(`kae completion zsh --install` detects an existing user `fpath` dir so the
installed completion auto-loads with no `.zshrc` edit) shipped 2026-06-18. v0.8.8
(daily-use fixes: opencode identity prefers the access-token email over the
opaque accountId UUID; shell completion is flag-aware — flags before positionals
no longer shift it — and completes flag names via a new `kae __complete flags`
kind) shipped the same day. v0.8.7 (complete account-identity
coverage: `agy.Identity` from `~/.gemini/google_accounts.json` so every tool
exposes a login identity, plus an `Identity` column in `kae status`) shipped the
same day. v0.8.6 (agy account switching on
macOS via a Keychain driver + a terser one-shot `kae run <tool> <account>` +
`claude /login` verification) shipped the same day; its agy two-account
real-keychain gate **passed**, fish was **dropped** from the verified shells
(`kae completion fish` stays best-effort), and the codex-keyring two-account gate
stays the one carried, unit-covered open item. What remains is hardening and
platform coverage, ordered below by user impact.

v0.8.5 (a "did you mean?" nearest-match hint
for an unknown command/tool/profile, table-driven off the same live lists
v0.8.4's `kae __complete` backend surfaces; additive, hand-rolled, no contract
break — see [RELEASE.md](RELEASE.md)) shipped 2026-06-17, both §A and §B. §B
(standardizing the reusable mise-integration + did-you-mean patterns into the
go-cli-tooling shared standard via chezmoi) landed the same day as a new
`docs/go-cli/PATTERNS.md`, with this repo's bundled skill resynced from it.

v0.8.4 (deep, dynamic shell completion sourced from kae's live state on a single
hidden `kae __complete` backend, feeding both kae's own completion and mise
task-argument completion) shipped 2026-06-17 — bash/zsh verified; **fish was
dropped from the verified shells** (2026-06-18; `kae completion fish` stays
best-effort, not release-gated). v0.8.3 (discovery-unblock:
freshness-as-adapter-capability, cursor `kae add` identity, codex keyring driver,
stored+displayed identity) shipped 2026-06-17 — its codex keyring two-account
real-keychain gate is deferred (also open; see [VALIDATION.md](VALIDATION.md)).
Earlier: v0.8.2 (daily-use polish), v0.8.1 (credential freshness /
auto-recapture), v0.8.0 (surface vocabulary unification), v0.7.2 (use/pin ×
-s/-i, global isolated home). What remains beyond v0.8.5 is hardening and
platform coverage, ordered below by user impact.

Follow-up from v0.8.4 (not yet scheduled):
- **Global mise tasks**: `kae mise init` writes the `ai-switch` / `ai-switch-tool`
  tasks (and their dynamic completion) into the project's `.mise.toml` only, so
  they exist where the tasks live. A `--global` option emitting them into the
  global mise config (`~/.config/mise/config.toml` or `~/.config/mise/tasks/`)
  would make `mise run ai-switch <TAB>` available in every directory. Scope
  addition; design before implementing.

## Upstream-drift automation — what is left

The post-v0.12.0 audit and its follow-up are finished; the response workflow
lives in the `upstream-auth-drift` skill
(`.claude/skills/upstream-auth-drift/`), and the assumptions themselves in
[VALIDATION.md](VALIDATION.md). Three of the five automation ideas are done: two
shipped in a release — version/date agreement with a doc-parsing test plus a
six-month age check, and the offline contradiction check for codex's store — and the
literal-count fingerprints are **on `main` without a release of their own**, because
they change no shipped binary (a test, a `mise run audit` task, and the table in
[VALIDATION.md](VALIDATION.md) § "Upstream Literal Fingerprints"). The stability the
idea rested on reproduced — every count identical across claude 2.1.218/219/220 while
the bundle grew 1.8 MB — and measuring turned up three things the design had wrong,
all recorded there. The remaining two, in the order each pays for itself:

4. **The shim harness**, table-driven and gated on the per-tool "does this tool
   shell out to `/usr/bin/security`" answer (claude yes, agy yes, codex **no**,
   cursor unverified). It should diff *the tool's* argv log against *kae's*,
   which turns the naming-agreement check in VALIDATION.md into a script.
5. **Behaviour-site hashes** for the three or four sites that encode real
   behaviour, then a bundle-pair diff on upgrade. The
   `oauthAccount?.profileFetchedAt` site hash was identical across three claude
   releases even though the TTL identifier went `TSg` → `sxg`, so the hash sees
   through minification where a name grep does not.

**Confirmed clean, recorded so nobody re-audits** (four read-only audits,
2026-07-30): backup-before-write ordering in the switch/run transaction
(including restoring *all* tools when the state write fails); lock acquisition in
canonical order (no deadlock); pin materializes directories before writing the
fragment that points at them; the TOCTOU between a switch's `account.Load` and a
concurrent `account rm` fails loudly and restores; atomic writes chmod before
writing bytes; metadata files never carry secret bytes; `runner.Snippet` is never
applied to a credential read's stdout; rebind round-trips and full re-pin are
idempotent and self-healing; `run --env` fails loud on a missing env profile. The
`security -w` argv exposure is a real but unavoidable macOS CLI constraint,
already accepted in [SECURITY.md](SECURITY.md), and kae uses stdin wherever an
alternative exists (`secret-tool`).

## Hardening backlog — daily-use robustness

- **Upstream now documents parallel sessions racing on one credential store**
  (recorded 2026-07-31). Claude Code v2.1.211: *"Fixed parallel Claude Code sessions
  all logging out simultaneously after wake-from-sleep when many sessions share one
  credential store"*
  ([changelog](https://code.claude.com/docs/en/changelog)). That is kae's own
  territory — several accounts through one store, and `kae run` can have more than one
  session live at once — and it is the strongest published evidence that the refresh
  token is **single-use/rotating** (one session's refresh invalidating the others is
  what that failure mode means). **Measured directly on 2026-08-04** and no longer an
  inference: the refresh token rotates and the superseded one is rejected, so the row
  this entry asked for is now in [VALIDATION.md](VALIDATION.md)'s claude assumptions,
  which owns the facts and the procedure. Note the direction the changelog points —
  upstream *fixed* many sessions sharing **one** store, so that configuration is the
  supported one, while kae's per-directory binds keep a **copy per store**, which is
  the configuration nothing upstream is defending.
  Still open from this entry: whether `keychain.WithReadCache` / the per-tool locks
  are enough when a switch overlaps a live session's own refresh.

- ~~**`kae account rename` can strand the active pointer**~~ (recorded 2026-07-31,
  **fixed** — see [RELEASE.md](RELEASE.md) v0.16.0). `buildAccountRename` used to
  update `state.Active[tool] = newName` inside its state mutation and only
  *afterwards* copy the secret payloads and write the renamed snapshot dir, so a
  failure in between left state naming a snapshot that did not exist yet.
  It is now three stages — build the new snapshot, flip the logical pointers, destroy
  the old snapshot — so every crash window leaves the pointers on a snapshot that is
  complete. **`buildAccountRename`'s comment is normative for why the ordering is
  what it is**, including why a naive reorder would have been worse than the original
  and why stage 3 deletes refs before the dir; it sits beside the code and cannot
  drift from it, so this entry does not restate the argument.
  Two decisions worth having outside the code: the "account already exists" guard was
  deliberately left strict, because a half-written rename target is indistinguishable
  from a genuinely taken name, and the manual recovery is documented instead
  ([CLI.md](CLI.md) § `kae account`). The doctor check the reorder needed,
  `secret_missing`, shipped with it.

- ~~**`kae rollback` restores an active pointer without checking its snapshot is
  still there**~~ (recorded 2026-07-31, **fixed** — the sibling of the entry above;
  see [RELEASE.md](RELEASE.md) v0.16.0). The rollback's state mutation wrote
  `st.Active[tool] = meta.ActiveBefore[tool]` unconditionally, so a backup taken
  before an `account rm`/`rename` named a snapshot that was gone and the next
  `kae use` failed with `account <tool>/<name> is not captured yet`
  (`loadPlansWithSnapshots`). The predicate `reapplyHint` had always applied to that
  same value — for a hint string only — is now the shared
  `restorableActiveAccount`, and the live pointer goes through it: recorded when its
  snapshot loads, `delete`d otherwise, with a stderr warning naming what it could
  not restore.
  The other end of the same gap was decided the other way: `account rm`/`rename`
  still do **not** rewrite `active_before` in existing backups. A backup is the
  record of what was true when it was taken, and rewriting every stored `Meta` on an
  account edit would put a whole-directory mutation (and its own half-finished
  state) into two commands, to save a value the restore can re-check for free
  ([DATA-MODEL.md](DATA-MODEL.md) § Backups).

- **Re-login UX: the bound-directory path is the one a human has to think about**
  (measured 2026-08-04; the bound-directory path **fixed in v0.17.0** by
  `kae relogin`, the two global paths still as described). The three expiry paths
  are not equally finished, and the worst one was the per-directory/per-worktree
  case this release makes prominent:
  - *Global account, still-working credential* — already one command and no `cd`:
    `kae add --restore <tool> <account>` drives the tool's own login flow, captures,
    and puts the previous login back (`expiringCredentialDetail`). Nothing to fix.
  - *Global account, dead credential* — **two steps**, the tool's login then
    `kae add --no-login` (`staleCredentialDetail`, which deliberately does not name
    `--restore` because there is no live login worth preserving). Forgetting the
    second step leaves the dead credential in the snapshot, so the next `kae use`
    re-applies it. Worth checking whether plain `kae add <tool> <account>` (login
    flow **and** capture, already one command) can be named here instead.
  - *Bound directory* — was `cd <dir> && <tool login>` (`pinLoginRemedy`), and it
    carried two hazards neither the message nor the docs mentioned. Both are closed;
    the remedy is now `cd <dir> && kae relogin <tool>` ([CLI.md](CLI.md) § kae
    relogin Semantics). ~~**(1)** It does not say the pin has to be active in that
    shell. With mise activation absent or the config untrusted, the isolation
    variable is unset, so the login lands in the **real home**: the wrong account is
    refreshed and the bound one is still stale.~~ — **fixed in v0.17.0**, and note
    *how*, because the plan below said something else. The plan was to refuse with
    the mise remedy, matching `companion_token_drift`'s wording and reusing
    `miseActivated`. What shipped instead **exports the isolation variable itself**,
    appended to the login child's environment, so the login lands in the bound store
    whether or not mise is active — the hazard cannot occur rather than being warned
    about, and the command works in exactly the shell where the old remedy failed.
    `miseActivated` is not consulted by `kae relogin` at all. ~~**(2)** Nothing
    captures the in-directory login back, so a later `kae pin` overwrites the fresh
    login with the older snapshot~~ — **fixed in v0.17.0** (the credential-copy entry
    below): a bind now harvests the copy in the store into the account snapshot
    before writing, so the in-directory login *is* captured back, and the 2026-08-04
    measurement that upgraded this hazard from "regresses" to "destroys" no longer
    applies to it. The remainder of (2) — that nothing captured it back
    **proactively**, so between the login and the next bind or sweep the snapshot
    still held the older copy and `kae use <tool> <account>` would apply it — is
    closed by the same command, which harvests as its last step.

  **On doing it without typing a command.** There is still no `kae doctor --fix` and
  no path that offers to run a remedy; every remedy is a string the human retypes.
  `kae relogin` is a verb they retype it into, not an offer. The surface question
  ("a new verb, versus `--fix` on `doctor`, versus `kae add --restore` learning about
  bindings") was decided for the new verb: `--fix` would have to decide which of
  several findings to act on, and `kae add --restore` is about the *global* store,
  where its backup-and-restore is the whole point and means nothing per directory.

  **And the detection hook is already in the right place but blind.** The mise
  `[hooks.enter]` runs bare `kae use --quiet` on every directory change — the exact
  moment a human arrives in the worktree — and `--quiet` suppresses success reports
  but never warnings. Today that path does not look at the bound credential's
  freshness at all; inside a pinned directory it instead warns about *itself*
  (`pinnedGlobalScope`: "you are changing GLOBAL state, which this directory will
  not see"). Surfacing an expiring bound credential there would tell the user at the
  only moment they do not have to remember anything — but it runs on every `cd`, so
  it needs a rate limit or a state-recorded last-warned stamp, and it must stay
  silent for a healthy login or it becomes wallpaper (the mistake v0.15.0/v0.15.1
  made in both directions).

- **Every credential copy kae keeps can be killed by another copy refreshing, and
  four kae commands do the killing** (measured 2026-08-04; the two write paths are
  **fixed** in v0.17.0, the rest is **not**). claude's refresh token rotates single-use
  ([VALIDATION.md](VALIDATION.md) owns the measurement), so of all the copies of one
  account's credential, only the one that refreshed last can still refresh. kae's
  architecture is copies with lazy sync: the account snapshot, each bound directory's
  store, the global isolated home, and every backup. The global loop closes itself
  (`recaptureActiveBeforeSwitch` harvests the live store before switching away, and
  that mechanism is now load-bearing rather than an optimisation). Nothing harvested
  *across* mechanisms until v0.17.0, and the exceptions that remain are named below
  rather than covered by that sentence.
  What the user sees, worst first. ~~**kae destroys a live login**: `kae pin` re-run,
  `kae use -i` / `kae run -i` re-materialized, and `kae unpin --purge` all write or
  delete without harvesting~~ — **fixed in v0.17.0**, see below. **Two copies are a
  time bomb**: the first refresh silently kills the others. ~~so "I used claude in the
  other worktree and this one logged out hours later" has no visible cause~~ — the
  *cause* became visible in v0.17.0: `kae doctor` reports `credential_superseded` for
  a bound directory whose copy another copy of the same account provably overtook,
  names where the newer one is, and points at the remedy that where decides
  ([CLI.md](CLI.md) § doctor). The bomb itself is unchanged — only one copy can refresh, and the entry
  that makes that stop being true is the SSCD split below. Nor does the check see an
  invalidation kae has no second copy of: a refresh in a directory kae does not know
  about, or in the real home under an account it is not tracking, leaves nothing to
  compare.
  ~~**`kae rollback` reports success restoring a rejected token** whenever anything
  refreshed after the backup, and `kae run -s <tool> <the active account>` writes the
  pre-child backup back over a credential the child refreshed~~ — **fixed in v0.17.0**,
  see below. **And every freshness
  surface misreports**: doctor's `credential_stale` / `credential_expiring`, `kae ls`,
  `kae status` and the switch-time warning all judge by `refreshTokenExpiresAt`, which
  invalidation does not move. That is still true of all of them; what v0.17.0 added is
  a *different* code beside them (`credential_superseded`) that reads `expiresAt`
  across copies instead, which is why it is not a band of the stale one.
  **Built in v0.17.0**, as a two-pass harvest plus a harvest in the delete sweep;
  [ADAPTERS.md](ADAPTERS.md) § Per-directory credential store is normative for the
  mechanism and the refusals, and [AGENTS.md](../AGENTS.md) carries the traps. Two
  things the plan above did not name, recorded because both were found by review
  *after* a version that looked complete and passed its tests: **attribution** (a
  shared store is account-agnostic, so a re-bind finds the previous account's copy
  there and filing it under the new name would be undetectable afterwards), and that
  **a chokepoint is not coverage** (the write path cannot see the store a mode toggle
  or an isolated re-key is moving *off*, which is why there is a pin-level pass at
  all).
  **The two restore paths landed next, also in v0.17.0**, and they answer differently
  because what the user asked for differs. `run -s` **skips** the restore of a tool
  whose live credential the backup's copy would supersede — it put that account there
  itself, so the restore is a no-op apart from destroying the newest copy. `rollback`
  **warns and restores anyway**, naming where the newer copy is, because that decides
  the remedy (in the snapshot it survives; in the live store only the pre-rollback
  backup keeps it). [CLI.md](CLI.md) § kae run Semantics and § `kae rollback --json`
  are the contracts. Three things this cost, all found while building it rather than
  planned: the *ordering* comparison had to be extracted and shared (`supersedes`),
  since a third hand-written copy of the `expiresAt` cutoff is the drift this repo
  bleeds on; the switch-away recapture needed the **same** comparison, or the very next
  `kae use` launders a rolled-back copy over the snapshot that still worked — an
  invalidation `recaptureWouldDowngrade`'s usability test cannot see; and the two
  candidates for "newest" have to be compared **against each other**, or the remedy
  names a copy that is not the newest.
  One thing deliberately left where it is: the "carries no usable token" half of the
  rollback warning (this entry named it after a wording the code has not used since the
  item-5 review; `docs/CLI.md` § `kae rollback --json` is normative and was itself wrong
  about it until 2026-08-06)
  is claude-only like the rest, although the fact it reports — a rollback writing
  a dead recorded copy over a working login destroys that login — holds for **every**
  tool and needs no rotation measurement. Widening it would add a warning to five tier-2
  tools in a release about claude's rotation, so it waits; the gate to move is the
  `rotatesSingleUse` check at the top of `warnRestoringSupersededCredential`, and only
  the not-`Orderable` branch may move, never the ordering one.
  **Still open after it**, smallest first: the freshness surfaces still judge by
  `refreshTokenExpiresAt` (no offline fix exists for *them* — this is a wording and
  expectation-setting problem, not a detection one; the detectable subset, where kae
  holds a second copy to compare against, is `credential_superseded`);
  **`run -s`'s own recapture goes
  through neither guard the switch-away recapture applies** — it calls
  `captureSnapshot` directly, so it neither keeps the snapshot's identity
  (`keepSnapshotIdentity`) nor refuses a downgrade (`recaptureWouldDowngrade`): a child
  that logs in as another account files that credential *and* that identity under the
  target account's name, and a child that logs out files the tombstone. The fix routes
  that recapture through **those two and no third** — they are named here so nobody
  invents one — and `docs/VALIDATION.md` case H asserts the defect's present-day shape,
  so the smoke run turns from green to red the moment it is fixed and has to be updated
  in the same commit. Measured 2026-08-05: `kae doctor` does then report `identity_drift`
  for the account, but its remedy is `kae use <tool> <account>`, which puts the foreign
  credential into the real home — so the reporting surface makes it worse, not better.
  The restore skip above is gated on attribution so it does not compound this, and it
  reads the **backup** rather than the snapshot precisely because the snapshot may
  already be wrong by then; **the switch-away recapture's attribution
  guard has no decodability gate** — `keepSnapshotIdentity` calls `identityDiffers`
  directly, so two identity payloads that are both non-records *and* byte-identical
  (`/oauthAccount: null` on each side, the reachable shape) read as "same account" and
  let the recapture proceed on evidence that names nobody. The two sibling guards
  (`dirIdentityConfirms`, `liveLoginMatchesBackup`) share `identityComparable` for
  exactly this; the third was found by a quality lens after them and is left alone here
  because closing it adds a refusal to `kae use`, which is a behaviour change this
  release did not scope. The fix is one call: route that comparison through
  `identityComparable` too; **a
  superseded *global* isolated home is never harvested** — `kae use -i <a>` then
  `kae use -i <b>` leaves `isolation/global/<tool>/<a>/` holding a's newest copy, and
  because there is no pin, neither the pin-level pass nor any sweep ever looks at it
  (the write-path harvest only covers the home being materialized). **Resolved for
  claude by the credential split**: a's credential is now a's own store, which the
  home switch does not touch and any later bind of a reads directly, so there is no
  second copy left to strand. It stands for a tool that cannot separate its
  credential from its home, which today is every other one; and the harvest
  writes an account snapshot under a per-directory lock that no tool lock covers, so a
  concurrent `kae add` or switch-away recapture of the same account can leave the
  snapshot holding the copy that **cannot** refresh — not merely the older of two good
  ones, since rotation makes at most one refreshable
  ([ARCHITECTURE.md](ARCHITECTURE.md) § Locking states why that is recorded rather than
  locked, and that it self-heals on the next bind of the directory that still holds the
  live copy).
  What it does **not** fix, by construction: two sessions live at once on one account
  in two stores. The refresh happens inside the tool with kae absent, so there is no
  moment to intervene; only one copy of the credential can. That is the entry below,
  and any message about this must not imply otherwise.

- ~~**One credential per account, sessions still per directory — via
  `CLAUDE_SECURESTORAGE_CONFIG_DIR`**~~ (designed and gated 2026-08-04, **built**
  in v0.17.0 — see [RELEASE.md](RELEASE.md) and
  [ADAPTERS.md](ADAPTERS.md) § Per-account credential store, which is normative for
  the mechanism. The design below is kept because it records *why*, and because the
  premise it rests on is an upstream measurement that has to be re-checked, not a
  decision that can be re-read from the code). The entry above keeps a *sequence* of
  directories working; it cannot make two worktrees bound to one account run at the
  same time, because each store holds its own copy and the first refresh invalidates
  the rest. Only one copy fixes that.
  The obvious form — every directory of an account sharing one store — trades away
  what the store holds besides the credential. It does not have to: claude has a
  **second** variable that moves the credential alone. `CLAUDE_SECURESTORAGE_CONFIG_DIR`
  displaces `CLAUDE_CONFIG_DIR` as the keychain service name's hash input *and* as the
  `.credentials.json` directory, while sessions, settings and the `.claude.json`
  identity keep following `CLAUDE_CONFIG_DIR`. So a bind can export a per-directory
  config dir and a per-account credential dir, and nothing that is private today
  becomes shared. Both halves are run-confirmed ([VALIDATION.md](VALIDATION.md)).
  **The premise was the gate, and it is green**: two processes with *different* config
  dirs sharing one credential store both authenticate and the shared item rotates
  once, with no tombstone — measured against a negative control (separate stores, same
  credential) that fails 1/2 in the same session, so the result discriminates. That
  was the specific fear worth measuring: a loser tombstoning the *shared* item would
  log out every directory at once.
  What makes the bet acceptable is that the failure is **observable offline**. The
  variable is undocumented, so upstream could stop honoring it — but then the
  credential lands at `sha8(CLAUDE_CONFIG_DIR)` instead of `sha8(SSCD)`, two
  separately addressable places, and kae already computes both hashes. An item
  appearing at the config-dir name *is* the signal; the write path deletes that name
  after harvesting it, so in steady state its presence means something wrote there
  since the last bind. Note what the detector actually reports — divergence to the
  config-dir side — because the common cause is not an upstream regression but a shell
  where only `CLAUDE_CONFIG_DIR` was exported. Note too that the regression's shape is
  a **loud logout, not a silent wrong account**: the config dir still points at kae's
  own store, so no other account's credential is ever reached.
  Sequencing, all measured or read rather than assumed: the write path must harvest
  before it overwrites (the entry above) *first*, or the migration's re-pin destroys
  the login it is migrating. In the same release as the split: the adapter honoring
  SSCD; the refusal in `driver()` becoming a carve-out that compares the value against
  the binding the fragment describes — **a prefix test is not enough**, since a
  mismatch that passes is the silent-wrong-account class this repo has bled on; a
  second env entry per tool (`isolationEntry` is singular today, and `rebindFragment`
  currently leaves shared-mode env lines alone, which stops being true when the
  account selects the credential dir); `dirSpecs` and `applyGlobalScope` handling two
  variables; and a **refcount** before deleting a shared item, or one directory's
  `unpin --purge` logs out its siblings. Migration is "re-run `kae pin`", with the
  doctor check naming directories whose fragment has no SSCD line yet.
  Deliberately **not** built: `SSCD=""`. It collapses a bound directory onto the
  global item, so `kae use <other>` would silently change what that directory runs
  while its fragment and identity still claim the bound account — kae breaking its own
  binding invariant. Record it as a trap, do not measure it further.
  This is claude-only by construction. codex has no equivalent variable; its item
  account comes from the **canonicalized** `CODEX_HOME`, so symlinking two homes onto
  one directory does share the item — but it shares the whole home with it (history,
  sessions, logs), which is the thing this design exists to avoid. A per-account store
  is the option there, if codex's rotation is ever measured.
  Two things the build settled that the plan above left open, recorded because the
  reasoning is not visible in the result. Globally isolated homes (`kae use -i`,
  `kae run -i`) read the account's store too — their home is already per-account, so
  leaving them out was tempting, and it would have made them the one copy the design
  forgot; that also closes the "superseded global isolated home is never harvested"
  entry below for claude, since there is no second copy left to harvest. And a bind's
  sweep no longer deletes the credential of a store it moves off: the credential is
  the account's, so only `kae unpin --purge` may take it, refcounted. What is still
  swept unchanged is the per-directory item a **pre-split** binding left behind,
  which is what makes re-running `kae pin` a migration rather than a leak.

- **A store bound before the credential split, unbound, then re-bound after it keeps
  its pre-split item** (recorded 2026-08-07, **not fixed** — deliberately). The
  migration sweep is scoped to the store the *previous binding* pointed at
  (`credentialMovedOutOf`), and after an unpin there is no previous binding to name
  it, so a three-step history leaves the item behind. Nothing is lost: the pin-level
  pass still harvests that copy into the account snapshot, so it is a leftover secret
  rather than a lost login.
  It is deliberately not fixed by widening the sweep a third time. Two weaker rules
  were already wrong the same way — every store of a pre-split binding shares the
  "no recorded credential entry" shape, so acting on it means deleting without
  evidence that no binding reads it, and a per-directory item is precisely the thing
  that cannot be counted. The right home is the entry below: **this leftover is
  attributable**, unlike the four found on a real machine, because its hash input is
  `store.Dir` and the pin index still names that directory. So it lands in the
  reportable half of that feature, not the leave-it-alone half.

- **Per-directory keychain items outlive everything that could name them, and now
  they can be found** (measured on a real machine 2026-08-04, **not fixed**). Five
  `Claude Code-credentials-<sha8>` items were on the operator's machine. Hashing every
  path kae knows attributed four: one to a global isolated home (live), one to a
  pre-v0.8.0 `bond` store and one to a `overlays` store — both from mechanisms whose
  vocabulary was renamed away, so current kae cannot reach them at all — and the last
  two to nothing kae owns. Of those two, one had been **modified three weeks earlier**
  (so something still uses it) and one predates kae entirely. The two legacy-kae items
  were deleted by hand; the unattributable ones were deliberately left.
  That is the whole design constraint in one observation. Enumeration is now available
  (AGENTS.md carries the correction and its two caveats), so a doctor check can list
  these items, attribute each by hashing the strings kae wrote into fragments, and
  report what is left over. But **attribution failure must never authorize deletion**:
  the hash is one-way, a candidate could be the operator's own `CLAUDE_CONFIG_DIR` or
  an item synced from another machine, and deleting it destroys a login kae has no
  snapshot of. Nor may an unattributable payload be adopted into a snapshot — same
  silent-adoption defect kae already retired elsewhere. Report and name the manual
  command; that is the whole feature.
  Two prerequisites it exposes: stores in the pre-rename vocabulary are invisible to
  every current sweep, so a migration (or an explicit "these are not mine any more"
  report) is needed for them; and the attribution table should be built from the
  strings recorded in fragments rather than paths re-derived from the tree, since the
  hash is over the string kae actually exported.

- **Rotation is measured for claude only** (recorded 2026-08-04). codex, cursor,
  copilot, opencode and agy have not been measured, so none of the copy-safety work
  above may be ported to them: "the newest copy" is unknowable without it, and
  declaring a rule kae cannot measure is the defect the refusals elsewhere exist to
  prevent. **A measurement is necessary and not sufficient**: the harvest attributes a
  copy through an identity-only artifact, so a tool that declares none (codex today)
  would carry a harvest that can never fire — adding one to `rotatesSingleUse` means
  measuring its rotation *and* giving it an identity artifact, which
  `TestHarvestIsDeclaredForMeasuredToolsOnly` refuses to let drift apart. codex's *file* store is the first one worth measuring, because unlike its
  keyring store it is already copied into bound directories and isolated homes today.
  Also unmeasured, and cheap to fold in: whether a fresh login revokes the previous
  login's chain, which is what decides whether `expiresAt` can order copies **across**
  logins rather than only within one.

- ~~**Link retraction only covers a name the real home still has**~~ (recorded
  2026-07-31, **fixed** — see [RELEASE.md](RELEASE.md) v0.17.0). v0.16.0 made
  `prepareBond` remove a symlink for a denied entry, but the removal lived inside
  the loop over `os.ReadDir(realHome)`, so it never saw a link whose name was no
  longer a real-home entry, and the isolated bind had no retraction at all.
  Both are now one shared reconcile (`unintendedLinks` + `retractLinks`): every
  symlink whose name is not in the intended set is removed, so a re-bind converges on
  that set instead of only growing. **Which source states that intent differs per
  mode, and one mode cannot always establish it** — [ADAPTERS.md](ADAPTERS.md)
  (§ per-directory shared bind, § per-directory isolated bind) is normative for both,
  and a third per-directory mechanism owes the same answer (AGENTS.md).

- **A recorded identity that is not an account record silently disables attribution
  for that account** (measured 2026-08-05 while reviewing the bound-directory
  `identity_drift`, **not fixed**). An identity payload that is well-formed JSON but not
  an object — `/oauthAccount` being `null` is the reachable shape — names no account, so
  it is refused as missing evidence in both directions ([ADAPTERS.md](ADAPTERS.md)
  § Per-directory credential store, which is normative). That refusal is right; what is
  wrong is that nothing tells the user their *recorded* label is unusable, and one bad
  capture propagates: `writeDirIdentity` applies whatever the snapshot holds, so every
  bound store of that account gets the non-record too, and each of them is then
  permanently silent on identity and unharvestable.
  It is not completely unreported today — the **global** `identity_drift` frame fires for
  it, because a non-object recorded side against a readable live one falls to the byte
  comparison and differs — but only while that account is the globally active one. For a
  non-active account nothing in any frame says so. The fix is a check of kae's own
  recorded payload rather than of a comparison: `secret_missing` is the nearest existing
  shape (a snapshot declaring a payload the backend lacks), and this is the same class
  one step further in — a payload that is there and cannot serve its purpose. Note the
  refusals must not be relaxed to close it: silence about the *store* is correct when
  kae's label is broken, so the reporting belongs on the label, not on the comparison.

- **`kae account rename` leaves a bound directory's store under the old name**
  (observed 2026-08-04, **not fixed**). Renaming moves the snapshot and warns about the
  pinned directories, but it does not touch `isolation/<pin-id>/<tool>/isolated/<old>/`
  — so an isolated bound directory's live copy sits under a name no account has any
  more. Everything downstream follows from that: the harvest cannot attribute it (the
  fragment still says the old name, and `storeAccount` answers with a name that resolves
  to nothing), the re-bind kae itself recommends builds the *new* account's store from
  its older snapshot, and only `kae unpin --purge` reaches the old store at all — which
  deletes that copy rather than keeping it, on purpose (docs/CLI.md § kae pin). The
  harvest's messages now name the condition and say a re-bind would harvest it, which is
  false for this shape until the rename moves the store or the fragment. Fixing it means
  deciding whether `rename` rewrites bound fragments, which is a write into directories
  the command was not asked to touch — the reason it only warns today.

- **`kae account rename` / `kae account rm` delete a recorded `SecretRef`
  verbatim** (recorded 2026-07-31, **deliberately not fixed**). Both delete the ref
  the snapshot's metadata names, without checking it is the ref this account would
  produce (`account.SecretRef(tool, name, artifact)`). A snapshot dir whose metadata
  names *another* account's ref — reachable by hand-copying an account directory,
  which is a plausible way to try to duplicate an account — therefore has that other
  account's payload deleted by a command that was not asked to touch it. Not a
  regression from the v0.16.0 restaging (the single pass did the same, and
  `account rm` shares the flaw), and it needs someone to have edited kae's data dir
  by hand, which is why it is recorded rather than fixed inside a release about
  something else. The fix is a comparison at both delete sites, and it belongs with
  whatever else next audits "does this snapshot describe itself".

- **`applySnapshot`'s refusals could be raised one step earlier, in
  `loadPlansWithSnapshots`** (recorded 2026-07-31, deliberately not done). Both
  refusals — a snapshot missing an artifact today's adapter declares
  (`!ok && !sp.IdentityOnly`) and `checkPayloadShape` — need only `plan.Specs` and
  `plan.Meta`, which `loadPlansWithSnapshots` already has, so a profile switch could
  fail before it takes a backup, runs `recaptureActiveBeforeSwitch`, and applies the
  first tool of several.
  Not done because the defect that motivates it is already closed: every refusal is
  raised before the *first live write* of its own tool, and a multi-tool switch that
  refuses on tool two restores tool one from the backup it just took. What is left to
  win is a wasted backup entry, and the cost of winning it is that `applySnapshot`
  stops being safe to call on its own — it would rely on an unenforced ordering
  invariant with every caller, and the refusals would no longer sit next to the
  writes they protect. **Whichever way this is taken, do not copy the tolerance rule
  into both layers**: two copies of `!ok && !sp.IdentityOnly` is how the identity-only
  case drifts apart. Move it, or leave it.

- **`run -i` inside a `pin -i` directory keeps a second credential copy**
  (recorded 2026-07-31, deliberately not fixed). The two mechanisms own separate
  stores by design — `isolation/global/<tool>/<account>` for global isolation and
  `isolation/<pin-id>/<tool>/isolated/<account>/config` for the directory's — so
  asking for `run -i` inside a bound directory materializes both, and they never
  sync. Coupling them would tie two deliberately independent mechanisms together
  for a combination that is a mistake to begin with (`kae run` with no `-i` uses
  the directory's binding, which is what the user wants there). If it turns out to
  bite in practice, the answer is a warning at `run -i` inside a pinned directory,
  not synchronization.

- **`PinID` does not resolve symlinks, and changing that needs a migration**
  (recorded 2026-07-31, deliberately not fixed). `paths.PinID` hashes
  `filepath.Abs` only, so two path aliases for one directory — a symlinked
  project dir, `/tmp` on macOS — are two pins. In practice they still share one
  store, because the fragment that selects it lives *in* the directory; the split
  only appears when `kae pin` is re-run through the other alias, which orphans
  the first store, and `kae unpin --purge` through that alias then sweeps nothing.
  Canonicalizing would fix that but re-key the store of **every** user whose path
  contains a symlink, moving their sessions and per-directory credential and
  stranding the old keychain items (which are named by the old path). So the
  prerequisite is a migration: at pin time, detect a store under the unresolved id
  with no resolved counterpart, sweep its items *before* renaming the directory,
  and say so. Note that codex parity is **not** a reason to hurry — `codex.storeKey`
  already resolves symlinks itself, so kae's keyring account matches whatever shape
  `PinID` takes. `doctor`'s `pin_stale` now makes an orphaned store visible, which
  was the part that used to be silent. **Worktrees add nothing to this** (checked
  2026-08-04): a linked worktree is an ordinary directory with its own real path, so
  it gets its own pin-id by the same rule as any other directory, and the only way a
  worktree meets this entry is the way any directory does — by being reached through
  a path alias. Deleting a worktree is the "bound directory is gone" case, which the
  breadcrumb plus `pin_stale` already report.

- **Surface vocabulary unification (`run` / `apply` / `mise init`)** *(shipped
  in v0.8.0 — see [RELEASE.md](RELEASE.md))*: folded `apply` into `use`,
  redesigned `run` onto `-s`/`-i`/`--env`, trimmed `mise init`, and hard-renamed
  the mechanism + config-key vocabulary to `shared`/`isolated`.
- **Credential freshness / auto-recapture** *(v0.8.1 — A–D implemented, see
  [RELEASE.md](RELEASE.md))*: `use`/bare `use` wrote the capture-time snapshot
  back to the live store with no recapture (only `run -s` recaptured), so a
  token rotated outside kae broke a switch-back (a login prompt when the refresh
  token had also rotated; seen in the v0.8.0 gate). v0.8.1 added switch-source
  recapture (symmetric with `run -s`, divergence-gated), switch-time stale
  warnings + `doctor` credential-health (`credential_stale` / `secret_orphan`),
  and `security`-read coalescing (a per-command keychain cache). Spans every
  OAuth/JWT tool, not just claude. The codex keyring driver (§E) is **split to
  v0.8.2** — see below.
- **Identity cache in isolation modes** *(v0.16.0 — see [RELEASE.md](RELEASE.md))*:
  `kae use` / `kae add` switched claude's `/oauthAccount` identity cache while the
  per-directory materializers wrote only the credential, so a bonded or isolated
  directory kept whatever account first ran there and `kae pin <tool> <account>` did
  not correct it. Auth was unaffected (the token wins) — it was an attribution gap,
  and the UI naming the wrong account inside a pinned directory is what a user sees.
  Now one identity step (`writeDirIdentity`) sits alongside `writeDirCredential`,
  which all four materializers already route through. Shared (bond) mode was the open
  design question, and the answer is that the mixed-state file is **private** in a
  bound directory (denylisted): a directory cannot both name its own account and
  live-share the file that records which account it is. Whether it was shared there
  at all had depended on where claude puts it — inside `CLAUDE_CONFIG_DIR` when the
  user sets one, at `$HOME` otherwise — so the sharing was an accident of file
  placement, and denying it makes both configurations behave alike. A guard
  (`identityTargetEscapes`) still declines any per-directory identity write resolving
  outside the store, kept as defence in depth for the routes the denylist does not
  cover.
  ~~**What is left**: `doctor`'s `identity_drift` still skips a kae-owned isolated
  home~~ — **done in v0.17.0** (`pinIdentityChecks`; see [RELEASE.md](RELEASE.md)).
  The global check still skips such a shell, and keeps doing so for the remaining
  half of the old reason: `state.Active` names the *global* account while the live
  cache is the *bound* directory's, so those two sides are different frames. The
  bound frame is now its own pass, reading each directory's binding and comparing
  against **that** snapshot, beside `pinCredentialChecks` as this entry said it
  should — both now share one walk of the live bindings (`boundDirStores`).
  What the pass reports is narrower than "they differ", and deliberately: only a
  divergence it can **prove** (both sides readable, `IdentityKeys` disagreeing —
  `dirIdentityConfirms`' `Conflicting`). Every missing-evidence outcome stays
  silent, because a bound directory legitimately has no identity cache until its
  tool runs there and one bound before v0.16.0 never had one written, so warning on
  those would fire on healthy directories — the mistake v0.15.0/v0.15.1 made in both
  directions. The two causes it cannot separate offline (a login made inside the
  directory versus an identity kae failed to apply) are both stated in the message,
  since their remedies point opposite ways; [CLI.md](CLI.md) § doctor is normative.
- **A directory-scoped keychain item keeps a stale account attribute**: `ApplyLive`
  reuses an existing item's account attribute so a re-login that changed it is
  honored, which is right for the single global item but not for a per-directory
  one. If `$USER` changed since the item was created, kae updates the item under
  the *old* account while claude looks it up under the new one, and the directory
  reports "not logged in" (an honest failure, not a wrong credential — which is
  why this is not a release gate). A directory-scoped spec should write under the
  adapter-resolved account instead.
- **codex's keyring store is not yet isolated per directory** — a *capability gap*
  now, not an upstream limitation. codex scopes the `Codex Auth` item by an account
  derived from the canonical `CODEX_HOME` (see [ADAPTERS.md](ADAPTERS.md)), so a
  bound directory could have its own item; kae warns and writes nothing because the
  end-to-end path is unverified. Three things it needs, in order:
  1. ~~**Confirm the paths agree.**~~ **Done** (2026-07-30). Measured with the
     login-free procedure in [VALIDATION.md](VALIDATION.md) against a bond-dir-shaped
     `CODEX_HOME` reached through a symlink: codex created the item under the account
     derived from the **resolved** path (the raw-path account was absent), matching
     `storeKey`. Expected values computed outside kae; cleaned up with `codex logout`.
  2. ~~**Fix what the flag measures.**~~ **Done** (2026-07-30). The field is now
     `Spec.KeychainDirBindable` and its parity guard derives the truth from the whole
     item identity (`Target` + `KeychainAccount`), so an adapter that binds by
     account can declare it honestly — under the old Target-only derivation, doing so
     **failed** the guard. Fixing it exposed a second defect: the guard had never
     examined codex at all, because its probe directories held no `config.toml` and
     the store therefore defaulted to `file`. It now selects the keyring store
     explicitly and fails when a tool with an isolation variable yields no keychain
     spec. codex is carried as a named exception (`bindableNotYetDeclared`) pending
     step 3.
  3. ~~**Own the item's lifecycle.**~~ **Done** (2026-07-30). A `pin -s` ↔ `pin -i`
     toggle and an isolated re-bind now sweep the keychain item of the store they
     supersede, and `kae unpin --purge` sweeps the current ones (plain `unpin` still
     keeps everything, so a re-pin restores the directory). The sweep covers a keychain
     item where the adapter declares it bindable — so it starts covering codex the
     moment the capability is declared, with no further work — and, since v0.17.0, a
     file credential that is no longer the copy its own store reads
     (`removeDirCredential` is normative). It also closed the same gap for claude, which had been creating
     per-directory items since v0.12.0 with nothing removing them.
  Then declare the capability (drop codex from `bindableNotYetDeclared`) and add the
  pin round-trip to the real-machine gate. Until step 3 lands, a pinned directory has
  no codex login until you log in inside it.
- **A tool that resolves its store from live state is modelled per artifact, not as
  a set.** codex's `auto` is the only such artifact today (the adapter probes and
  returns one spec), and the restore path reconciles a backup record against it.
  What would justify a deeper model — `Spec` carrying an ordered list of stores, the
  primitives resolving at write time, and `restoreSpec`/`refreshPlan` both going
  away — is a *second* liveness-resolved store: a third codex store, or any tool
  that migrates its own credential. Note the reconciliation would not fully
  disappear even then: "an absent record must never delete the store the tool moved
  to" and the whole-document-vs-pointer refusal are properties of the payload.
- ~~**`account.toml`'s `keychain_account` is write-only.**~~ **Dropped** in v0.16.0
  (see [RELEASE.md](RELEASE.md)). It was recorded at capture and read nowhere, with
  a doc that told apply to ignore it — rightly, since it is the answer for the
  environment the snapshot was captured in, and apply resolves the item for the
  environment it is writing. The alternative considered was to give it the reader it
  would be evidence for (a doctor check comparing it against today's derived
  account: "this snapshot was captured under a different `CODEX_HOME`"). Rejected:
  applying a snapshot captured under a different home is *correct* behaviour, so the
  check would warn about kae working as designed. Removing it also removes a second
  record of a fact only the adapter owns ([DATA-MODEL.md](DATA-MODEL.md)). Note the
  asymmetry that stays: `backup.ArtifactRecord` keeps its account, because a restore
  must address the item it captured.
- **claude's OAuth build suffix: the environment half is refused, the build half is
  undetectable.** The suffix sits in both store names — keychain service
  `Claude Code<suffix>-credentials[-<sha8>]` and identity file
  `.claude<suffix>.json` — and kae hard-codes the production spelling (empty
  suffix). *(2026-07-30)* The one source the environment exposes,
  `CLAUDE_CODE_CUSTOM_OAUTH_URL`, now makes the adapter report claude unsupported
  instead of reading and writing the wrong item. What remains is the build channel
  (`-local-oauth`, `-staging-oauth`): a released binary hard-codes it to production
  and **nothing in the environment reveals it**, so a locally built or staging
  claude is still switched against the wrong names silently. Closing it means
  fingerprinting the binary (the channel function is a `return"prod"` in the
  bundle) — the same offset-read the assumption row records — or asking the user.
  Measured on 2.1.220 (docs/VALIDATION.md).
- ~~**A re-bind leaves the previous account's per-directory keychain item**~~
  *(fixed 2026-07-30)*: it does not any more. The record of "which dirs were
  previously bound" that this was waiting on turned out to be unnecessary for the
  cases that matter: every command that supersedes a store is standing *in* the
  bound directory, so walking `isolation/<pinID>` — computed from its own cwd —
  finds the stores, and anything the new binding does not point at is stale by
  definition. The store directory still stays (it holds sessions and settings a
  re-pin restores); what is removed is the credential, which for a keychain store is
  the invisible half and since v0.17.0 can also be a file the store no longer reads
  ([CLI.md](CLI.md) § pin, `removeDirCredential` for the rule). What an index would still add is the *other* direction —
  reaching a bound directory from outside it — which **landed on 2026-07-31** as a
  breadcrumb inside each store (`isolation/<pin-id>/dir`), so `kae account rm` /
  `rename` / `kae profile rm` now name the directories they invalidate and
  `kae doctor`'s `pin_stale` reports a bound directory that is gone. The keychain
  items of a *deleted* bound directory are still unreachable — they are named by
  the path that no longer exists — so the store can be removed but its items
  cannot; `kae unpin --purge` before deleting a directory is the way to avoid it.
- **Pinned directories never refresh their snapshot** *(detection shipped; the
  recapture is deliberately still open)*: a bound directory's tool refreshes its own
  token in place, so kae's snapshot for that account ages and the directory's own
  copy ages independently of it.
  `kae doctor` **reports** a bound directory whose credential is stale or within the
  lead time (`credential_stale` / `credential_expiring`, message `bound to <dir>`),
  and since v0.17.0 one that a newer copy of the same account overtook
  (`credential_superseded`); see [CLI.md](CLI.md) "Bound-directory credentials".
  The remedy for all three is `cd <dir> && kae relogin <tool>`.
  **What is closed and what is not.** The premise this entry rested on — that
  recapture "has no non-arbitrary definition" because nothing says which of several
  bound stores the single snapshot should take — was answered in v0.17.0 by ordering
  on `expiresAt` (`supersedes`, guarded by `orderable` and by attribution): of all
  copies of one account's credential under single-use rotation, at most one can still
  refresh, so "the newest" is the answer rather than a preference. The harvest applies
  it wherever a copy is about to be destroyed, and `kae relogin` applies it for the
  directory the user is standing in, which is the case with a person present to say
  which store is authoritative.
  What stays open is an **unprompted** recapture: nothing walks every bound store on a
  switch to pull the newest in. It would need every store read on every `kae use`
  (a `security` call per bound directory), and it would still be a write kae performs
  on evidence nobody asked it to gather. `kae add --no-login <tool> <account>
  --from-dir <dir>` — the user naming the authoritative store — remains the explicit
  form if it is ever wanted.
- **TUI**: an interactive mode (profiles/accounts browser, pin status,
  config maintenance) on top of the stable JSON surface, so daily
  switching does not require remembering flags. Candidate once the
  v0.5.0 command system has settled.
- **Remote share-list definitions (ship)**: implement the v0.6.0 design if
  it holds — published defaults for the overlay share list, explicit
  fetch, diff-before-adopt, hard-coded auth denylist.
- **Codex keyring driver** *(shipped v0.8.3; item contract corrected 2026-07-30)*:
  `cli_auth_credentials_store = "keyring"` switches this codex home's `Codex Auth`
  item, identified by service **and** the account codex derives from `CODEX_HOME`
  (see [ADAPTERS.md](ADAPTERS.md)). The two-account real-keychain gate is still
  open, and now also covers "a second `CODEX_HOME`'s login survives a switch".
- **Login UX polish** *(v0.8.6 §C — claude verified; agy deferred)*: `claude
  /login` is launched via the upstream flow (`internal/cmd/login.go`); the
  "login flow exited without changing auth" case is detected and refused with
  exit `11`. agy login stays **deferred** — a 2026-06-18 discovery (with the
  `agy` CLI installed) found **no `login`/`auth`/`whoami` subcommand**; agy
  authenticates via GUI/browser OAuth, which kae's shell-out login flow cannot
  drive, so `kae add agy` stays `--no-login` capture only.
- **agy keyring driver (macOS)** *(v0.8.6 §A — implemented; real-keychain gate
  open)*: on macOS agy stores its credential in the **login Keychain**, not a
  file — item `svce="gemini"`, `acct="antigravity"`; the payload is a single
  **opaque ~686-byte token string** (not JSON/JWT — verbatim capture/apply with
  a non-empty single-line guard, unlike codex's `auth.json` JSON). v0.8.6 lifted
  the file-only adapter with the verbatim-keychain pattern used for
  codex/claude/cursor, matching by **service and account** (the `gemini` service
  is shared, only `acct=antigravity` is agy's; apply upserts with
  `add-generic-password -U`, never touching a sibling item). The file driver
  stays for Linux/WSL. Identity auto-detection stays deferred (no whoami; the
  token is opaque). See [ADAPTERS.md](ADAPTERS.md); the two-account real-keychain
  gate is the open acceptance item ([VALIDATION.md](VALIDATION.md)).
- **cursor off macOS is unblocked but unimplemented** (tier 2 — see § Tier-2
  tools; this is the platform gap, not a missing mode): the adapter refuses
  non-darwin because the storage was undocumented. It no longer is — cursor-agent
  picks its store by platform alone (keychain on darwin, a file everywhere else,
  with no fallback either way) and writes one JSON object
  `{accessToken, refreshToken, apiKey, bedrockCredentials}` to
  `${XDG_CONFIG_HOME:-~/.config}/cursor/auth.json` on Linux (`%APPDATA%/Cursor/`
  on Windows, mode 0600, dir 0700). A Linux driver is therefore three artifacts
  under one `KindJSONPointer` file (`/accessToken`, `/refreshToken`, `/apiKey`) so
  `bedrockCredentials` survives, mirroring the keychain set. What is missing is a
  Linux box to verify it on: the paths come from reading the installed macOS
  bundle's platform switch, and the file store has never been exercised
  ([ADAPTERS.md](ADAPTERS.md) records the layout).
- **`kae env export --dotenv --reveal`** *(deferred — no current use)*:
  explicit-flag value export for CI bootstrapping (today values are
  injection-only by design). Considered for v0.8.6 but dropped: CI does not use
  kae, so there is no consumer for a value-reveal path. Revisit only if a
  kae-driven CI flow emerges.
- **Performance polish** *(v0.8.2 §A — shipped)*: the per-switch
  `security`-read coalescing shipped in v0.8.1 §C (a context-scoped keychain
  read cache in `internal/keychain`). v0.8.2 §A added concurrent per-tool
  `Detect` in `status` and a matching read cache for kae's own `secret.Backend`
  (`secret.WithReadCache` + `Cached`, collapsing the switch-time double read of
  each target snapshot) — see [RELEASE.md](RELEASE.md).
- **doctor keychain-orphan detection** *(shipped in v0.8.1 §D as the
  `secret_orphan` check)*: warns when a `kagikae` secret item has no matching
  snapshot dir, via a new `secret.Enumerator` (file `readdir`, Linux
  `secret-tool search --all`). The darwin keychain still cannot list items by
  service through the `security` CLI, so the check is silently skipped on the
  keychain backend (documented gap; [SECURITY.md](SECURITY.md)).
- **claude driver override for isolated smoke checks** *(v0.7.1 — see
  [RELEASE.md](RELEASE.md))*: on macOS the keychain driver ignores temp
  `$HOME`s, so claude switch smoke checks can only run safely on Linux today;
  an explicit file-driver override (env var primary, config opt-in secondary)
  lets containers and smoke environments never touch the real login keychain.
  Also the safety prerequisite for the v0.7.2 global-isolated (`kae use -i`)
  real-machine gate.

## Command-system expansion

Daily-use ergonomics, designed together as mise-style verbs so the surface
stays coherent rather than accreting ad hoc. Account delete/rename graduates
to v0.7.1 (see [RELEASE.md](RELEASE.md)); the rest remain candidates:

- ~~**`kae env` and `kae backup` have no completion case**~~ (found 2026-08-06 while
  wiring `kae relogin`'s, **fixed in v0.17.0**). Both are subcommand groups —
  `env set|unset|list`, `backup list` — and neither appeared in `subcommandVerbs` nor
  in the `case` blocks of the three generated scripts, so `kae env <TAB>` and
  `kae backup <TAB>` offered nothing at the first positional: the same defect class
  the v0.10.0 companion gap was, past the guard that exists precisely to catch it,
  because that guard iterates its own table rather than the router and a group
  missing from *both* is invisible to it.
  Both now have a case in all three scripts and an entry in `subcommandVerbs`, and
  the second half of the fix is the guard the entry asked for: `positionalCommands`
  classifies **every** command in `completionCommands` as taking a positional or not,
  and `TestEveryPositionalCommandCompletes` requires a branch for each one that does
  — a branch that emits candidates, since a case label in front of an empty body is
  the same dead end — and, from the other side, no branch for one that does not, so
  a stale classification is loud rather than quietly weakening the first half. Being
  keyed by `completionCommands` is what makes it a guard rather than a second opt-in
  table. Review of the fix turned up two more holes of the same family and closed
  them: `kae mise` had a case but no `subcommandVerbs` entry, so its sub-verb was
  asserted nowhere; and the whole of `env`'s new routing could be deleted with every
  test green, because the constructs it uses (`accounts "${pos[1]}"` and friends)
  recur in other branches and were matched against the whole script —
  `TestCompletionPositionalRouting` now asserts each one inside the branch that owns
  it.
  What it still does not see, because nothing machine-checks either list against
  `Root()`: a command dropped from `completionCommands` and the classification
  together. Closing *that* by dispatching each command from a test is not safe —
  several commands reach `newApp`, and with it the real environment, before a bad
  flag stops them — so a verb with no sub-verbs keeps its own test naming it
  literally (`TestCompletionScriptsCompleteRelogin`).

- **The completion scripts are hand-written text, so the tests parse them back**
  (raised 2026-08-07 by review of the entry above, **not started** — advisory, not
  a defect). `completion.go` holds three hand-maintained scripts whose `case`
  blocks encode the same routing three times in three array conventions, and the
  guards therefore reconstruct label→branch from the generated text
  (`completionCaseBlocks`). Holding the per-command spec as data and rendering
  each shell from it would let the guards read the spec instead of re-parsing the
  output, and would make a new command one entry rather than three edits. The
  cost is why it has not been done: rewriting all three generators is an order of
  magnitude more than the change that raised it, and their current output has
  real-machine acceptance behind it. Worth doing when the next command lands or a
  fourth shell is added — not on its own.
- **`printHelp` and docs/CLI.md disagree about `kae add`** (found 2026-08-07 while
  classifying commands for completion, **not fixed**). `printHelp` (`cmd.go`) says
  `kae add <tool> <account>`; `CmdAdd` accepts one or two positionals, so the
  account is optional and `docs/CLI.md`'s `kae add <tool> [<account>]` is the
  correct one. Nothing checks either against the parser, which is the same gap
  the entry above names for `completionCommands`. Left alone deliberately: it is
  unrelated surface, and the completion change is not where an unrelated
  usage-string fix belongs.
- **A flag that takes a *value* shifts every completion slot** (found 2026-08-07 by
  review of the entry above, **not fixed**). The generated scripts build their
  positional list by dropping words that start with `-`, which is enough for a
  boolean flag and wrong for a valued one: in `kae env --config /p set <TAB>` the
  path `/p` survives the filter and becomes the first positional, so the tool slot
  is asked to complete accounts of `set`, and `kae env --config /p list <TAB>`
  walks past the `list` gate the same way. `kae use --config /p claude <TAB>`
  simply goes quiet. Go's `splitArgs` knows which flags consume the next word and
  the shell does not, and the two are hand-kept in step. Not urgent — the cost is
  wrong or missing candidates, never a wrong action — but the fix is one place: a
  `kae __complete valued-flags <command>` kind fed from the same registrars
  `flagSetFor` uses, with each script consuming it in its positional loop. Doing
  it per script without that backend would re-create the drift the dynamic backend
  exists to prevent.
- **`kae profile save <name>`**: snapshot the current active set into a
  named profile, instead of hand-editing config via `kae edit`.
- **Account rm/rename** *(v0.7.1 — see [RELEASE.md](RELEASE.md))*: `kae
  account rm` / `kae account rename`, replacing manual snapshot-dir + keychain
  surgery. **`kae profile save|set|unset|rm|default`** also shipped in v0.7.1
  (the comment-preserving config writer; see [RELEASE.md](RELEASE.md)).
- **`kae ls`** *(v0.8.2 §C — shipped)*: a mise-style listing of accounts and
  profiles in one view (was split across `kae accounts` and `kae status`).
- **Account-name auto-detection** *(v0.8.2 §B — shipped; cursor v0.8.3, agy
  v0.8.7)*: an adapter exposes the live login identity via the optional
  `Identifier` capability so `kae add <tool>` auto-detects and sanitizes a name
  by default, while an explicit `kae add <tool> <account>` still wins. **All six
  tools now implement it** (claude/codex/opencode/copilot since v0.8.2, cursor
  via `cursor-agent status` in v0.8.3, agy via `~/.gemini/google_accounts.json`
  in v0.8.7); `TestIdentifierConformance` pins the full coverage.
- **Shorter ad-hoc switch inside a pinned directory** *(v0.8.6 §B)*: `kae run
  <tool> <account> -- <tool>` already works (it is not blocked by the pinned-
  directory guard), but it is verbose; v0.8.6 defaults the child to the
  adapter's `Binary()` when `-- <cmd>` is omitted, so `kae run <tool> <account>`
  opens a session under that account directly.
- **Tool-name prefix aliases** *(v0.8.0 — see [RELEASE.md](RELEASE.md); input-only sugar)*: accept any unambiguous
  prefix in tool positions (`cl`→claude, `cod`→codex, `cu`→cursor,
  `cop`→copilot, `o`→opencode, `a`→agy); ambiguous prefixes (`c`, `co`) error
  with the candidate list. Resolved to the canonical name immediately and never
  stored (config/state/JSON keep canonical names), and computed dynamically from
  `constants.Tools` so a new tool self-adjusts the ambiguity set. Only in tool
  positions of the two-arg forms (`use`/`pin`/`run`/`add`/`account`/`env`); a
  one-arg `kae use cl` stays a profile lookup. (Verb aliases `u`/`p`/`r`/`d`/`s`
  shipped in v0.7.2.)
- **Flag short forms** *(v0.8.0 — see [RELEASE.md](RELEASE.md))*: `-P` for
  `--profile` on `run` / bare `use` / `mise init`.
- **Generic completion + "did you mean"** *(static completion is v0.8.0;
  dynamic completion is v0.8.4; "did you mean" shipped in v0.8.5 — see
  [RELEASE.md](RELEASE.md))*: (1) `kae completion <bash|zsh|fish>` shipped in
  v0.8.0 as a static-list generator; v0.8.4 makes it **dynamic** via a hidden
  `kae __complete` backend (live profiles/accounts at the argument positions,
  shared with mise task completion) and adds an interactive `--install`.
  (2) an unknown command/tool/profile printing a Levenshtein "did you mean X?"
  hint shipped in v0.8.5, table-driven off the same
  router/`constants.Tools`/config lists (the `kae __complete` source).
  (3) v0.8.8 made completion flag-aware (flags before positionals no longer
  shift it) and added flag-name completion via a `kae __complete flags <command>`
  kind sourced from the parser's own per-command flag registrars.

These overlap with the TUI item above at the surface level but are the
plain-CLI layer; the TUI sits on top of them.

## Platform coverage

- **Windows**: `%APPDATA%` layout, Credential Manager secret backend, lock
  implementation, `%USERPROFILE%\.claude` file-patch driver.

## Tier-2 tools — described, not queued

Everything in this section concerns agy, opencode, cursor or copilot, which are
**tier 2** ([DESIGN.md](DESIGN.md) § Tool Tiers): kae commits to global credential
switching for them and to nothing more. These entries are therefore *descriptions
of those tools*, kept so a future session recognizes a symptom rather than
rediscovering it — not work queued against kae. Each says what would make it
matter, and none of them relaxes a guard in the meantime: an unmeasured store gets
a warning, never a guessed artifact.

Promotion out of tier 2 needs all three of: a measured home-isolation env var, the
tool's full credential set enumerated, and a real-machine round trip. Only copilot
is anywhere near that, and only by demand.

- **copilot isolation** (possible, deliberately not built): `COPILOT_HOME` is a
  verified config-dir variable (2026-07-31), so copilot could join claude and
  codex in `isolationEnvVar` and become `use -i` / `pin -i`-capable. Reading the
  variable was not enough on its own: the per-account keychain items coexist and
  are never switched, so what a second home actually changes — and whether
  copilot's own hidden `--config-dir` can defeat it, which nothing in the
  environment reveals — has to be established first. **Demand-gated**: build it
  when someone needs a per-directory copilot account, not for parity.
- **agy's file store on macOS** (recorded gap, 2026-07-31): agy skips the
  keychain under ssh/wsl/container detection, on a 1s keyring timeout, and on any
  keyring failure, so the file store is reachable on macOS too — but the fallback
  file's path is not derivable from the 1.0.10 binary, so kae warns instead of
  switching it ([ADAPTERS.md](ADAPTERS.md), [VALIDATION.md](VALIDATION.md)).
  Blocked on a way to make agy write a token without a real login: it has no
  kae-drivable login, so the `security` PATH shim (which does apply to agy) has
  nothing to intercept yet. agy is the tier floor and this is the reason.
- **agy home isolation**: no stable home/config env var is known, so the isolation
  modes refuse it. Revisit only if upstream ships one *and* the file-store gap
  above is closed — a redirected home whose fallback store kae cannot find would
  isolate nothing while reporting success.
- **opencode's DB credential store** (recorded gap, 2026-07-31): `auth.json` is
  still the live store through 1.18.5, and the `credential` table in
  `opencode.db` is a dormant one-shot import. When a release makes that table
  authoritative, kae's pointer patch becomes a silent no-op — and on 1.17.4 the
  imported row is frozen at whichever account auth.json held on first run. The
  `upstream_version` doctor warning is the trigger to re-run the VALIDATION row.
  This one *does* need acting on if it fires: it would break the switching kae
  does commit to at tier 2.
- **cursor off macOS** is unblocked but unimplemented; see the entry under
  Hardening backlog for the Linux layout.

## Exploratory

- richer TTY (routed review surface) if daily use shows the need
- shell completion
- localized human output (Japanese)
- `kae shell init` convenience wrappers

## Review Triggers

- First credential-layout change in any upstream tool: add a regression
  fixture and bump the adapter guard before widening support.
