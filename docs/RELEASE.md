# Release Process

Releases are cut by pushing a `vX.Y.Z` tag; GitHub Actions
([.github/workflows/release.yml](../.github/workflows/release.yml)) runs
[GoReleaser](https://goreleaser.com) ([.goreleaser.yaml](../.goreleaser.yaml))
to build, archive, checksum, and publish. Do **not** create the GitHub release
by hand — the tag does it.

1. Bump `toolVersion` in `internal/cmd/cmd.go` to the new `vX.Y.Z` (the binary's
   reported version is hardcoded, not injected; it must match the tag) and the
   `TestBuildVersionReport` expectation.
2. `mise run check` and `git diff --check`; update the docs (RELEASE/ROADMAP/
   VALIDATION and any behavior docs).
3. Merge to `main` and push; CI (`ci.yml`) must be green.
4. Tag and push: `git tag -a vX.Y.Z -m "kae vX.Y.Z — <summary>"` then
   `git push origin vX.Y.Z`. The release workflow gates on `go vet`/`gofmt`/
   `go test`, then GoReleaser builds darwin/linux × amd64/arm64
   (`kae_<version>_<os>_<arch>.tar.gz` + `checksums.txt`), creates the release
   with a grouped changelog, and attests the release checksum manifest.
5. Verify the published assets and `scripts/install.sh` against the new tag.

GoReleaser auto-generates the changelog from commits; edit the release body
afterward for curated highlights when useful. Windows is not built
([ROADMAP.md](ROADMAP.md): `internal/lock` is Unix-only).

---

# kae v0.17.0 (2026-08-08)

**One account per worktree — and kae no longer kills the login in it.** A binding
has always belonged to a *directory*, which makes a `git worktree` a first-class
unit: the way kae marked its fragment as ignored fought that, nothing could show
more than one binding at a time, and — measured while making worktrees prominent —
re-binding a directory destroyed the credential the tool had refreshed inside it,
because claude's refresh token turns out to rotate single-use.

Baseline: v0.16.0. `schema_version` stays `1`. **Behaviour changes**: an account's
credential now lives in one store shared by every directory bound to it, so a bind
writes a second env entry and a bind's sweep no longer deletes that credential;
where `kae pin` writes its ignore rule; a bind or a superseded-credential sweep now
harvests a newer credential from the store it is about to overwrite or delete, and
declines to delete one it could not preserve; `kae run -s` skips a restore that would
put back a credential its child has superseded, `kae rollback` says when the copy it
restores can no longer refresh, and `kae use`'s switch-away recapture declines a live
copy its own snapshot supersedes; `kae run -s`'s own **recapture** now refuses the two
cases that switch-away recapture already refused and keeps the account's recorded login
identity, and both recaptures now refuse when the identity records they would compare
are not readable as records at all; `kae unpin --purge` names the account credential it
removes as the account's rather than the directory's; and the remedy every
bound-credential finding names changed from the tool's own login command to
**`kae relogin`**. Plus three
contract-additive surfaces — the `kae ls --pins` view, `doctor`'s existing
`identity_drift` code reported for a bound directory's own store, and a new
`credential_superseded` code — plus completion cases for `kae env` and
`kae backup`, which changes the generated script; the rest is documentation.

- **One credential per account, sessions still per directory.** Two worktrees bound
  to one account each held their own copy of that account's credential, and claude's
  refresh token rotates single-use — so the first refresh in either one logged the
  other out, up to eight hours later, inside the tool, with every offline check here
  green. The harvest below keeps a *sequence* of directories working; it cannot make
  two of them work at once, because the refresh happens with kae absent.

  claude has a second variable that moves the credential **alone**
  (`CLAUDE_SECURESTORAGE_CONFIG_DIR`): the keychain service name's hash input and the
  `.credentials.json` directory follow it, while sessions, settings and the identity
  file keep following `CLAUDE_CONFIG_DIR`. So a bind now exports a per-directory
  config dir *and* a per-account credential store (`credstore/<tool>/<account>/`), and
  nothing that was private becomes shared. `kae use -i` and `kae run -i` read the same
  store — their home is already per-account, and leaving them out would have made that
  home the one copy the design forgot.

  The premise was measured before any of it was built, against a negative control in
  the same session: two processes with *different* config dirs sharing one credential
  store both authenticate and the shared item rotates once with no tombstone, while
  the same pair given separate stores holding copies fails 1/2 and tombstones
  (docs/VALIDATION.md). The specific fear worth measuring was a loser tombstoning the
  shared item, which would log out every directory at once.

  Two rules invert with it, and both are in docs/ADAPTERS.md § Per-account credential
  store. Deleting: a per-account store is not one directory's to remove, so a bind's
  sweep leaves it and only `kae unpin --purge` may take it — once no fragment and no
  globally isolated home still points at it, keeping it whenever kae could not read
  one of those to tell. And attribution: where a store's credential lives is read
  from the recorded binding, never derived from the account, because the store walk
  returns stores of older bindings forever and a leftover one would otherwise be
  handed another account's credential to harvest from.

  **Migration is to re-run `kae pin`** in each bound directory; until then it keeps
  the layout it was bound with, and `kae doctor` names it (`credential_unsplit`). If
  upstream ever stops honoring the variable the failure is offline-observable and
  loud rather than silent: the credential lands at the config dir's name instead —
  a different item kae also computes — and the config dir still points at kae's own
  store, so it is a logout and never another account's session. `SSCD=""` is
  deliberately not built and is refused: it collapses every config dir onto claude's
  one global item, which would let `kae use <other>` silently change what a bound
  directory runs.

- **`kae relogin` — the login that lands where the binding points** (new verb).
  The remedy for a bound directory's expired credential was `cd <dir> && claude
  /login`, and it was correct only in a shell where the pin was active: the isolation
  variable is what makes a login land in the store kae bound, so with mise activation
  absent or the config untrusted the same command refreshed the **real home** — the
  wrong account moved and the bound one was still stale. `kae relogin [<tool>]`
  exports the variable itself, so the login cannot land anywhere else, and then
  captures the result back into the account snapshot (`harvestDirCredential`, with
  every guard it already had: it declines a copy that does not supersede the
  snapshot, and one it cannot attribute to this account). That capture-back closes
  the last piece of a gap the harvest left: a login made inside a bound directory
  only reached the snapshot when a bind or a sweep next ran, so until then
  `kae use <tool> <account>` applied the older copy globally. A flow that changed
  nothing exits `11` rather than reporting a login that did not happen; every
  bound-credential message in `doctor` now names this command
  (docs/CLI.md § kae relogin Semantics). It refuses rather than guessing when the
  store the binding points at is not there — the path is recomputed from a hash of
  the directory's current path while the tool reads the literal value mise exports,
  so a directory that moved would otherwise have kae create a store nothing reads and
  call it a login.

- **`doctor` reports the copy that lost the race** (`credential_superseded`,
  contract-additive). When two copies of one account's credential exist and one
  refreshes, single-use rotation means the other cannot any more — and no freshness
  surface could see it, because they all judge by `refreshTokenExpiresAt`, which an
  invalidation does not move. "I used claude in the other worktree and this one
  logged out hours later" had no visible cause. The check compares each bound
  directory's copy against the account's snapshot and the other bound copies, and
  reports **only what it can prove**: both sides orderable, both attributed to the
  account by the store's own identity cache, and a strict ordering, so a directory
  pinned moments ago (whose store holds exactly what the snapshot does) says nothing.
  It requires the *losing* side to be orderable too, which is stricter than the
  shared comparator asks — there the question is "may I overwrite this", where a copy
  with no comparable deadline is nothing to lose; here it is "may I tell the user
  this is dead", where it is a copy kae cannot judge. The consequence is stated
  **conditionally** ("if the two are copies of one login"), because `expiresAt` orders
  two payloads without saying whether they are one chain or two independent logins of
  the same account — and this release is what makes the second shape reachable. The
  remedy branches on where the newer copy is: a re-bind when it is in the snapshot
  (no browser needed), a `kae relogin` when it is another directory's store.

- **`kae pin` records its ignore rule in the repository's exclude file, not in a
  tracked `./.gitignore`** (behaviour change). Up to v0.16.0 every `kae pin` appended
  a line to `./.gitignore`, so binding a repository plus three worktrees left four
  dirty working trees, each waiting for the user to commit a line about their own
  machine. The rule now goes to `$GIT_COMMON_DIR/info/exclude`, where **one entry
  covers the main checkout and every linked worktree** — a worktree's own
  `$GIT_DIR/info/exclude` is not consulted at all, which is measured rather than
  assumed (docs/VALIDATION.md § git behaviour kae depends on, and a test that builds
  a real worktree so `mise run check` re-measures it).

  Both halves of the destination come from `git rev-parse` through
  `internal/runner`, and the second is the one that looks like success when it is
  wrong: an `info/exclude` entry is anchored at the **repository root**, while a
  `.gitignore` entry is anchored at its own directory. kae is run from the directory
  being bound, which may be any depth below the root, so the entry carries
  `--show-prefix`. Reusing the old entry string would have written a rule matching
  nothing while `kae pin` still reported success.

  **Recording the rule can never fail the pin.** It is the last step, so by then the
  stores are materialized, the credential is written and the fragment is in place —
  the directory *is* bound. An error there would skip `pruneDirCredentials` (leaving
  the superseded per-directory keychain item holding a credential nothing points at,
  the exact state that sweep exists to prevent) and swallow the export fallback a
  non-mise shell needs, on every re-run, since the cause does not go away. So kae
  warns on stderr, keeps the exit code at `0`, and omits the `ignored via` clause.
  This matters more than it did for `./.gitignore`, which lived *inside* the pinned
  directory: the exclude file is outside it, so binding a linked worktree writes into
  the **main checkout's** `.git`, which can be unwritable while the worktree is fine.

  A directory name reaches the file as part of a **pattern**, so its wildmatch
  metacharacters are escaped. Measured: a subdirectory named `[wip]-feature`
  produced `/[wip]-feature/…`, which git read as a character class and did not
  ignore — the same silent failure as a missing `--show-prefix`, triggered by the
  directory's name instead.

  Migration: **none required, and none performed.** A `./.gitignore` line written by
  an older kae is left alone — a duplicate ignore rule is harmless, and removing a
  line from a tracked file is a change kae was not asked to make. The same goes for
  an exclude line: if one is already there in an unescaped form (only reachable from
  an unreleased build of this branch, or by hand), the first pin appends the escaped
  form beside it and every later pin matches that and returns early — one leftover
  line, not one per pin, verified by pinning four times. Outside a
  repository (or with no `git` on `PATH`) kae writes no rule and the success report
  omits the `ignored via …` clause rather than claiming one; the tracked
  `.gitignore` an older kae would have created in a non-repository is no longer
  created at all. `kae unpin` leaves the exclude entry in place, symmetrically with
  the store it keeps for a re-pin.

- **`kae ls --pins` lists every bound directory, from anywhere** (contract-additive:
  a new view of an existing command, `bound_directories` in `--json`; nothing
  existing changed shape). `kae status` answers for the directory it is run in,
  which is the wrong question once a repository has one worktree per agent and each
  binds a different account. Rows carry the directory, a `*` for the current one,
  the profile (`(ad-hoc)` when the account set matches no named profile), the mode,
  and the bound account per tool, ordered by directory so sibling worktrees sort
  together.

  It lists what is bound **now**, which is deliberately not what the store tree
  says: `kae unpin` keeps a store so a re-pin restores the directory's sessions, and
  a single-tool re-bind leaves the previously bound tools' stores behind, so a
  directory appears only while it still has a fragment to read. Listing stores would
  name directories that are not bound and remedies that land where nothing reads —
  the same distinction the v0.16.0 doctor credential sweep had to make. An
  *unreadable* fragment is not folded into that skip: the directory is left out with
  a stderr warning naming it, the way `pinChecks` already separates the two. And it
  reads no config, so a malformed `config.toml` (which makes plain `kae ls` exit `2`)
  does not stop it answering.

- **Tool tiers are written down** (documentation). claude and codex are **tier 1**
  (every mode); agy, opencode, cursor and copilot are **tier 2** (global credential
  switching, `kae run --env`, backup/rollback, doctor, identity detection where the
  tool exposes one). The normative definition is [DESIGN.md](DESIGN.md) § Tool
  Tiers, with the mechanical form in [ADAPTERS.md](ADAPTERS.md) § Isolation env vars
  and the tier-2 items collected under a ROADMAP section that says they are
  descriptions of those tools rather than queued work.

  **No guard was relaxed to do this**, and that is the point of stating the tier
  rather than quietly deprioritizing: every refusal — never declare an artifact for
  an unmeasured location, never fall back to a secondary store after an
  authoritative write fails, never derive a keychain account from a live item,
  refuse rather than approximate a store-selecting config value — applies
  identically at both tiers. What the tier changes is which *modes* a tool gets. The
  reason for the split is where the damage has actually come from: both failures
  that cost a user a wrong-account session were kae's own modelling errors in the
  tier-1 tools, not upstream changes in the tier-2 ones.

  One accuracy fix came out of writing it down: the isolation env var table said
  "none stable" for copilot, which was false — `COPILOT_HOME` was verified on
  2026-07-31. The table now distinguishes "none known" from "one exists and is
  deliberately not wired up", because those look identical from the outside and are
  not the same fact.

- **A per-directory bind retracts a link it no longer intends to share** (bug fix).
  v0.16.0 removed a symlink for a *denied* entry, but from inside the loop over the
  real home's entries — so it could only ever see a link whose name the real home
  still had, and the isolated bind had no retraction at all. Two residues followed.
  Bond a directory while `CLAUDE_CONFIG_DIR` is set and then unset it, and
  `<bondDir>/.claude.json` kept pointing into the old config dir **forever**: the
  per-directory identity write was declined by `identityTargetEscapes` on every
  later pin, warning each time, with no remedy but deleting the link by hand.
  Separately, dropping an entry from `isolated_shared_items` left its link in place,
  so a directory went on sharing what the config no longer said to share.

  Both are now one reconcile against the intended set, so a re-bind converges instead
  of only growing. Real files stay untouched in both, the rule that keeps a private
  override and kae's own per-directory credential and identity copies alive.
  **Which source states the intent differs per mode, deliberately, and the shared
  bind cannot always establish it — a real home it cannot enumerate, or one listing
  nothing shareable, warns on stderr and retracts nothing.** That asymmetry is the
  design decision in this fix; [ADAPTERS.md](ADAPTERS.md) carries it (§ per-directory
  shared bind, § per-directory isolated bind) rather than being re-derived here.

- **A bind no longer destroys the login in the directory it is binding** (bug fix,
  and the reason this release exists). claude's refresh token was measured on
  2026-08-04 to **rotate single-use** (docs/VALIDATION.md owns the measurement): of
  all the copies of one account's credential, only the one that refreshed last can
  refresh again. kae's per-directory stores are copies, and the tool refreshes the
  copy *inside* the bound directory, in place, at a moment no kae command is running
  — so `kae pin` re-run, `kae use -i` / `kae run -i` re-materialized, and
  `kae unpin --purge` all wrote or deleted over the only copy that still worked. It
  reported success, `kae doctor` stayed green, and the directory failed up to an
  access token's ~8h later, mid-session. `kae pin` is what kae's own remedy for a
  stale bound credential tells the user to run.

  Every path now **harvests before it overwrites or deletes**: it reads the copy that
  is there, and when that copy is the newer one it goes into the account snapshot
  first. Newer means the larger `expiresAt`, the guards are refusals rather than
  guesses, and the whole mechanism is claude-only —
  [ADAPTERS.md](ADAPTERS.md) § Per-directory credential store is normative for all of
  it. What a user sees: `kae: harvested …` when a copy is moved, a reason plus a login
  remedy when kae declines one, and — from `kae unpin --purge` only — a deleted copy
  whose account no longer exists, since that command was asked to remove these
  credentials and the sweep a *bind* runs was not.

  **Two things the plan did not name, both found by review on a version that looked
  complete and passed its own tests.** *Attribution*: a shared store is
  account-agnostic, so re-binding it to another account finds the previous account's
  copy there — usually the newer one, because it is the one in daily use — and
  harvesting that would file one account's token under another's name, which nothing
  offline can detect afterwards because the token is opaque. And *a chokepoint is not
  coverage*: the write path cannot see the store a mode toggle or an isolated re-key is
  moving off, so the directory the user had just bound held the credential rotation had
  already killed, with every offline check green. Hence a pin-level pass over every
  store of the directory before any of them is written, with the delete sweep still
  after the new binding.

  What this does **not** fix is stated because a message implying otherwise would be
  worse than silence: two directories bound to one account still cannot run at the
  same time. The refresh happens inside the tool with kae absent, so only one *copy*
  of the credential can fix that ([ROADMAP.md](ROADMAP.md)). Nor do the freshness
  surfaces improve: `refreshTokenExpiresAt` does not move when a copy is
  invalidated, and no offline check can see that it was.

- **`kae doctor` reports a bound directory that is running an account its binding
  does not name** (contract-additive: an existing check code, `identity_drift`, in a
  frame it never covered). The check has always compared a tool's live identity
  against what kae applied — and always skipped a kae-owned isolated home, because
  `state.Active` names the *global* account while the live cache there is the *bound
  directory's*, so the two sides are different frames. Since v0.16.0 a bind writes
  the identity into the store, so there is something of kae's own to compare against
  in that frame; nothing was.

  The new pass reads each bound directory's store **by its own path**, so one
  `kae doctor` answers for every binding rather than only the shell you are standing
  in. It reuses the harvest's attribution predicate rather than a second copy of it,
  and reports only that predicate's positive-evidence answer — everything else is
  missing evidence and stays silent, which is the point rather than a detail: the
  ordinary states (a store whose tool has not run there yet, a directory bound before
  v0.16.0) are in that list, so an unconditional warning would fire on healthy
  directories and become wallpaper, the mistake v0.15.0/v0.15.1 made in both
  directions. [ADAPTERS.md](ADAPTERS.md) § Per-directory credential store is normative
  for the predicate and its refusals; [CLI.md](CLI.md) § doctor for the check's
  contract, message and remedy.

  **Review changed the predicate, and with it the harvest.** A payload that is valid
  JSON but not an object was reaching the proof branch: the keyed comparison falls back
  to bytes when it cannot read either side, so a store naming *no* account was reported
  as naming another one. The rule now applies to the **recorded** side as much as the
  live one, and in both directions — which matters most where nobody was looking. Two
  such payloads used to *confirm* a store on the strength of agreeing about nothing, so
  a shared store re-bound between two accounts that both recorded one would have had
  the previous account's token filed under the new name: the undetectable
  misattribution this guard exists to prevent. Refusing costs the opposite way and
  loudly — the bind overwrites a newer copy it declined to preserve, naming the reason
  and the login that fixes it, while the superseded-credential sweep now **keeps** an
  item it would previously have harvested and swept. A destroyed login the user is told
  about is the lesser of the two, which is the trade every refusal in this mechanism
  makes. What nothing yet reports is the state underneath it — a recorded identity kae
  cannot read at all ([ROADMAP.md](ROADMAP.md)).

  One gate differs from the rest of the bound-directory family, and deliberately.
  `pin_stale` and the bound credential checks need no secret backend and run even
  when it is unavailable — which is exactly when someone is diagnosing. This one has
  to read the account's *recorded* identity, so it is skipped there instead of
  reporting a comparison it could not make. The walk that decides what is bound is
  now shared with the credential checks — one walk per run, the fragment and never the
  store tree — so a future consumer inherits the gate rather than re-deriving it.

- **Restoring a backup no longer hands a tool a token that cannot refresh** (behaviour
  change, to `kae run -s`, `kae rollback` and `kae use`'s switch-away recapture). The
  same measurement that drove the harvest applies to every copy kae keeps, and a
  *backup* is a copy: `kae run -s <tool> <the account that was already active>` backed
  up that account's credential, let the child refresh it, and then wrote the pre-child
  copy back — logging the real home out while printing "previous auth state restored".
  `kae rollback` printed "Rolled back to" for a credential anything might have
  superseded since. Both were reported as success with every offline check green.

  The two answer differently, because what the user asked for differs. `run -s`
  **skips** the restore for such a tool: it applied that account itself, so restoring is
  a no-op apart from destroying the newest copy, and `previous auth state restored` is
  now printed only when something was. `kae rollback` **warns and restores anyway** —
  going back is the request — and the warning names *where* the newer copy is, because
  that decides the remedy: in the account snapshot it survives, so `kae use <tool>
  <account>` applies it; in the live store the rollback overwrites it and only the
  pre-rollback backup still holds it. Exit codes and both JSON reports are unchanged.

  Three things this cost that the plan did not name. The ordering test had to become a
  **single shared comparator** (`supersedes`), because a third hand-written copy of the
  `expiresAt` cutoff is the drift this repo keeps bleeding on. The **switch-away
  recapture needed the same comparator**, or the very next `kae use` files a
  rolled-back copy over the snapshot that still worked — an invalidation the existing
  "is it usable" refusal cannot see, since both copies are usable and differ only in
  order. And the two candidates for "newest" have to be compared **against each other**:
  choosing by branch order names a copy that is not the newest, which is the remedy
  misdirection this project has shipped before. `run -s`'s skip additionally requires
  the identity beside the credential to still name the account the backup recorded, so a
  child that logged in as somebody else is restored over rather than kept — and it reads
  that from the **backup**, not the account snapshot, because `run -s`'s own recapture
  has already rewritten the snapshot with whatever the child left behind. claude only,
  like the harvest, because ordering two copies needs a measured rotation.

- **`kae run -s`'s own recapture is guarded, and so is the guard** (bug fix, behaviour
  change). The sentence above — "`run -s`'s own recapture has already rewritten the
  snapshot with whatever the child left behind" — described a real defect, and this
  closes it. That recapture called `captureSnapshot` directly, applying neither guard the
  switch-away recapture applies, so a child that ran the tool's own login flow and landed
  on somebody else's account had that credential *and* that identity filed under the
  target account's name — undetectable afterwards, since the token is opaque and every
  surface then agrees on a wrong label — and a child whose refresh failed had its
  tombstone written over a snapshot that still worked. It now applies
  `keepSnapshotIdentity` and `recaptureWouldDowngrade`, **those two and no third**, and
  the restore still reads the backup rather than the snapshot, which stays correct rather
  than becoming redundant.

  A **third** defect sat on the same line and no plan named it: `persistSnapshot` builds
  the snapshot from `plan.Identity`, which the run paths never set, so every `run -s`
  blanked the account's recorded login identity — a different field from the identity
  payload, and the reason the fix carries `plan.Meta.Identity`. It was found by measuring
  the line rather than by reading it.

  And `keepSnapshotIdentity`'s comparison needed the decodability gate its two siblings
  (`dirIdentityConfirms`, `liveLoginMatchesBackup`) already had — but as a **qualifier on
  the wording, not on the decision**, which is not what the plan said and is the more
  interesting half. `identityDiffers` falls back to a byte comparison for a payload it
  cannot key on, right for the drift check and wrong for attribution, so a refusal there
  used to claim the live login was somebody else's when kae had only observed a change it
  could not read. It now says which of those two it means. The refusal *set* is unchanged.
  Built the way the roadmap prescribed — comparability as a precondition for recapturing
  at all — it destroyed credentials, and that is measured rather than argued: the only
  newly-refusing shape is two identity payloads that are both non-records **and
  byte-identical**, which is the shape kae itself creates (applying a snapshot writes its
  recorded identity into the live cache, so a recorded `/oauthAccount: null` puts that
  same non-record on both sides), and which no login can leave behind because `/login`
  rewrites the identifying keys. So it fired where nothing had happened, and on `run -s`
  the refusal threw away the child's refreshed token. docs/ROADMAP.md records the
  withdrawal and the general shape of the mistake: refusing is conservative for a guard
  that declines to overwrite, and destructive for one that declines to preserve.

  Which exposed a second thing, fixed here too: on `run -s` a refusal was destructive even
  when refusing is right. Its backup predates the child, so a declined copy lived only in
  the store the restore overwrites. It now takes a second backup of the post-child state
  (reason `run-unattributable`) and names it, with the two-step that turns it into an
  account of its own. `kae use` needed nothing — its backup already precedes its
  recapture, the asymmetry "the same two guards and no third" had assumed away.

- **`kae unpin --purge` says which credential it removed** (message only). One line
  reported both removals in this sweep, and `removeDirCredential` deletes at the location
  the store *reads* — which for a split store is the account's own — so removing an
  account-wide credential was announced as a "superseded per-directory" one at the
  config-dir store: neither the thing removed nor where it lived, and it understated a
  removal that affects every other binding of that account. Found by running the smoke
  procedure in docs/VALIDATION.md § v0.17.0 per-account credential. Both existing
  assertions on that literal are negative, so nothing pinned the account-wide wording;
  the per-directory arm was pinned all along, on the line's store dir rather than on its
  sentence.

- **`kae env <TAB>` and `kae backup <TAB>` offer their sub-verbs** (completion only;
  neither command changed). Both are subcommand groups that shipped with no case in
  any of the three generated scripts, so the first positional was a dead end. `env`
  completes the tool and account of `env set|unset` as well, gated on the sub-verb,
  since `env list` takes no arguments. The recurrence guard that should have caught
  this iterates its own opt-in table, so a group missing from *both* the table and
  the scripts was invisible to it; the guard added alongside is keyed by the command
  list instead, and every command is now classified as taking a positional — and then
  required to have a branch in all three shells, and that branch must offer
  candidates and read its arguments from the slots that shell numbers them by — or
  as taking none ([ROADMAP.md](ROADMAP.md) § Command-system expansion). The
  generated scripts are also parsed now by whichever of the three shells the
  machine has (bash required), which nothing did before: they are Go string
  constants, so the shellcheck task never saw them.
  A **structural** script
  change like this one is the kind that needs an installed completion file
  rewritten ([CLI.md](CLI.md) § Keeping completion current).

---

# kae v0.16.0 (shipped 2026-07-31)

**The bookkeeping, and who a bound directory says it is.** Four write sequences
that could leave kae's own records naming something that is not there, plus the
attribution gap that made a pinned directory display the wrong account.

Baseline: v0.15.3. Contract-additive — one new `doctor` check code,
`secret_missing`; `schema_version` stays `1`. Three changes are not additive and are
each described below: a **behaviour change** to `kae rollback`, a **removed field**
in `account.toml`, and claude's `.claude.json` joining the shared-bind denylist,
which changes what a bond dir shares for users who set `CLAUDE_CONFIG_DIR`
themselves.

- **`kae account rename` completes the new snapshot before anything points at it.**
  It used to set `state.Active[tool] = newName` inside its state mutation and only
  *afterwards* copy the secret payloads and write the renamed snapshot dir, so a
  failure in between left state naming a snapshot that did not exist (the state
  v0.15.3's `active_orphan` reports). It is now three stages — build the new
  snapshot, move the logical pointers (`[profiles]` references, then `state.json`),
  destroy the old snapshot — and the *shape* is what matters, not the reorder:
  moving the flip below the old single `Get(old)` → `Set(new)` → `Delete(old)` pass
  would have traded one detectable state for an undetectable one, the old dir
  loading fine while the `SecretRef` its metadata declares was already deleted.
  Stage 3 deletes the refs before removing the dir, which maps its own crash window
  onto `secret_missing` rather than `secret_orphan` (see below for why that
  matters). One state still needs a manual step and is documented rather than
  guessed at: a crash between stages 1 and 2 leaves both snapshots present, and
  re-running hits the existing `account already exists` refusal, which is
  deliberately not relaxed — a half-written rename target is indistinguishable from
  a name that is genuinely taken ([CLI.md](CLI.md) § `kae account`).

- **`kae doctor` reports a snapshot whose stored payload is gone
  (`secret_missing`).** `secret_orphan` asks whether a stored key still has a
  snapshot dir behind it; nothing asked the reverse, so an account that could not be
  applied at all looked healthy. This direction needs no enumeration primitive — it
  looks up the refs the snapshots themselves name — which is why it matters more
  than a mirror check usually would: `secret_orphan` is skipped on the darwin
  keychain for want of a listing primitive, so on the primary platform this is the
  only one of the two that reports anything. An artifact captured as *absent* is
  never reported, and a backend that errors on the read is left to `secret_backend`.

- **`kae rollback` restores an active pointer only when its snapshot is still there
  (behaviour change).** The state mutation wrote `st.Active[tool] =
  meta.ActiveBefore[tool]` unconditionally, and a backup's `active_before` keeps the
  name it had at capture time — so rolling back across a `kae account rm`/`rename`
  recorded an account that was gone, and the next `kae use <tool>` failed with
  `account <tool>/<name> is not captured yet`. `reapplyHint` had applied exactly the
  right predicate to exactly that value since it shipped, but only to shape a hint
  string; it is now the shared `restorableActiveAccount` and the live pointer goes
  through it. A pointer that cannot be restored is dropped — the same "no active
  account for this tool" state `kae account rm` leaves — and warned about on stderr.
  Never fatal: the credentials are rolled back either way, and what is lost is a
  label. The other end of the same gap is decided the other way: `account
  rm`/`rename` still do **not** rewrite existing backups, because a backup is the
  record of what was true when it was taken and the restore can re-check the one
  value that goes stale ([DATA-MODEL.md](DATA-MODEL.md) § Backups).

- **A bound directory now carries the identity of the account it is bound to.**
  `kae use` / `kae add` switched claude's `/oauthAccount` cache; the four
  per-directory materializers wrote only the credential. Since `CLAUDE_CONFIG_DIR`
  moves the cache to `<dir>/.claude.json`, a bonded or isolated directory kept
  whichever account first ran there, and `kae pin <tool> <account>` could not
  correct it. Auth was never wrong — the token decides — which is exactly why it
  survived: the only symptom was a UI, and a `kae add` identity detection, naming
  the previous account. The step (`writeDirIdentity`) sits inside the one helper all
  four route through and runs **last**, because a directory labelled with an account
  whose credential kae could not put there is worse than an unlabelled one. A
  snapshot with no recorded identity applies as absent, so the tool refetches rather
  than keeping a label for an account that is no longer there. An identity write that
  fails warns instead of failing the bind: the credential is already correct, an
  identity is a label the tool rebuilds, and returning would leave `kae pin` without
  the mise fragment it had not written yet. Fixed with it: the credential path
  returned early for a non-keychain spec, which would have skipped the identity on
  every Linux bind.

- **claude's mixed-state file is private in a bound directory (behaviour change for
  one configuration).** This was the open design question of the item above, and the
  answer had to be one or the other: a directory cannot both name its own account and
  live-share the file that records which account it is. `.claude.json` joins claude's
  hard-coded shared-bind denylist, so a bond dir gets a private copy and the identity
  lands there. What that changes depends on a setting, which is the part worth
  reading: claude keeps the file **inside** `CLAUDE_CONFIG_DIR` when the user sets one
  and at `$HOME/.claude.json` when they do not, so it was an entry of the real tool
  home — and therefore symlinked into every bond dir — only for the first group.
  Everyone else never shared it. So the sharing this removes was an accident of file
  placement rather than a design choice, and only `CLAUDE_CONFIG_DIR` users see a
  difference: their bond dirs start from claude's defaults for `projects`,
  `mcpServers` and project trust, which means one trust prompt per bound directory.
  Sessions are unaffected — they live in `projects/` under the tool home and are still
  shared. There is deliberately no knob to restore the sharing: the two behaviours are
  exclusive. Nothing changes until the directory is re-pinned; upgrading the binary
  alone leaves an existing bond dir as it was, and the first `kae pin` there retracts
  the stale link. That retraction closes an older gap of its own — a denied entry's
  existing symlink was never removed, so `tools.<tool>.shared_denylist_extra` gaining
  a name had no effect on directories already bound.

  **To carry those keys over instead of starting fresh**, replace the bond dir's link
  with a real copy of the file *before* the first re-pin. A real file there is a
  private override, so retraction leaves it and the identity step then rewrites only
  `/oauthAccount` in it — the directory starts from the real home's projects and MCP
  servers under its own account name. Inside the still-pinned directory mise exports
  `CLAUDE_CONFIG_DIR` pointing at the bond dir, so:

  ```bash
  link="$CLAUDE_CONFIG_DIR/.claude.json"
  real="$(readlink "$link")" && rm "$link" && cp "$real" "$link"
  ```

  If mise is not active in that shell, the same path is written verbatim in the
  directory's fragment: `grep CLAUDE_CONFIG_DIR .config/mise/conf.d/kagikae.toml`.
  Do not assemble it from a pin id — `kae pin` does not report one, and the store
  root moves with `XDG_DATA_HOME`.

  Removing the link first is the part that matters: `cp` onto a symlink writes
  *through* it, into the real home. For the same reason retraction deletes rather
  than copies — it fires for every denied entry, credentials included, and copying
  the real home's `.credentials.json` into a bound directory is the opposite of what
  the denylist is for.

  Refused alongside it: `tools.claude.isolated_shared_items = [".claude.json"]`,
  which used to load. `isolated_shared_items` was governed by a rule about
  *credentials*, and this file is not one, so opting it in was permitted — and did
  the same damage in the stricter mode, every `kae pin -i` directory displaying the
  real home's account whichever one it was logged in as. If your config has it,
  remove that entry. Both fields and the shared bind's denylist now read one table
  (`constants.PrivateBindItems`) instead of three hand-kept copies — which is what
  let this diverge in the first place — with a guard test pinning the wiring.

- **`account.toml` no longer records a keychain account (removed field).** It was
  written at capture and read nowhere, with a doc comment telling apply to ignore it
  — rightly, since where a keychain item lives is the adapter's answer for the
  environment being *written*, while a snapshot can only hold the answer for the
  environment it was captured in. The alternative was to give it the reader it would
  be evidence for (a doctor check for "this snapshot was captured under a different
  `CODEX_HOME`"), rejected because applying such a snapshot is *correct* behaviour.
  Reading an older `account.toml` is unaffected — the decoder ignores a key it no
  longer models, pinned by `TestLoadIgnoresRetiredKeys` — and nothing consulted the
  value, so there is no migration. `backup.ArtifactRecord` keeps its account, and the
  asymmetry is the point: a restore addresses the item it captured, by identity.

- **`mise run audit` checks the upstream literal fingerprints** (in `main` since
  before this release, `c4e6ad4`, and shipped by it). Counts of chosen literals in
  each installed tool's own artifacts, recorded in
  [VALIDATION.md](VALIDATION.md) and enforced in lockstep with the behaviour
  assumptions, so an upstream release that moves a store or renames a variable shows
  up offline. Untagged at the time because it changes no shipped byte — tests, docs
  and a mise task only. Notes worth keeping: the table must **not** be generated from
  kae's own constants (`Claude Code-credentials` and cursor's service names are
  assembled upstream, so they legitimately count **0**, and a generated table would
  keep the most important rows permanently red); `0` is a valid expectation and "it
  changed from 0" is the news; versions are pinned in the table, because picking the
  newest by mtime read the wrong build twice.

---

# kae v0.15.3 (shipped 2026-07-31)

**A smoke procedure that was not isolated, and a state kae could not see.** The two
ship together because the first produced the second on the operator's own machine.

Baseline: v0.15.2. Contract-additive — one new `doctor` check code,
`active_orphan`; no field, flag or exit-code changes, and `schema_version` stays `1`.

- **`kae doctor` reports an active account it cannot confirm (`active_orphan`).**
  `state.json` can name an account for a tool with no snapshot behind it, and nothing
  said so: `kae status` displayed the phantom name, and no check looked at
  `state.json` at all (`config_valid` reflects `config.toml` only). Warn-level and
  offline, and wired in **outside** the secret-backend gate on purpose — an
  unavailable backend is exactly when someone is diagnosing, and the check needs no
  backend. The same code covers a `state.json` that cannot be read and an active
  snapshot whose metadata will not parse; both returned silently in the first draft.
  The causes are open-ended, so the check compares the two records rather than
  watching for one: an interrupted `kae account rename` (ordering fix recorded in
  [ROADMAP.md](ROADMAP.md), deliberately not in this release), `kae rollback`
  restoring an `active_before` that a later `account rm`/`rename` invalidated, and a
  writer outside kae.

- **The documented smoke procedure no longer writes to the real machine.** A temp
  `HOME` does not isolate kae: `paths.Resolve` reads `XDG_CONFIG_HOME`,
  `XDG_DATA_HOME`, `XDG_STATE_HOME` and `XDG_RUNTIME_DIR` independently, and an
  absolute value inherited from the environment beats the temp `HOME`. A smoke run
  shaped exactly that way wrote a fixture account into the operator's live
  `state.json` on 2026-07-31, leaving `active.claude` naming an account that did not
  exist — precisely the state `active_orphan` now reports. The exports now live in
  one shellcheck-covered file, `scripts/smoke-env.sh`, sourced by every block:
  three hand-written copies is *how* the omission happened, added next to two correct
  ones. Two blocks in [VALIDATION.md](VALIDATION.md) were not isolated at all — the
  first runnable block in § Smoke Checks would have overwritten the operator's real
  `~/.config/kagikae/config.toml` and `~/.claude/.credentials.json`, and the
  companion-auth block its real `~/.gitconfig`.

No code path other than `doctor` changed; `kae`'s switching, capture and rollback
behaviour is byte-identical to v0.15.2.

---

# kae v0.15.2 (shipped 2026-07-31)

**v0.15.1 was an over-correction and this reverts it.** One fix, and an apology in
changelog form.

Baseline: v0.15.1. Contract-stable — no codes, fields or flags change.
`credential_expiring` starts firing again for the credentials v0.15.1 silenced.

- **The lead-time notice is restored for refresh-backed credentials.** v0.15.0 warned
  on `refreshTokenExpiresAt`; v0.15.1 stopped, on the theory that it was a rolling
  shelf life renewed by every refresh. It is not. It is the **login's absolute
  expiry**: `expiresAt` (the access token, ~8h) rolls forward on every refresh, that
  one is set when `/login` runs and stays put. Anthropic's own documentation is the
  giveaway — Claude Code warns `Your login expires in N days · run /login to renew`
  three days ahead of it, and the operator confirms that warning appears only near the
  end rather than at every startup, with a real re-login cadence of about a month.

  The measurement that misled me was my own: `relogin_by − captured_at` is the time
  *left* at capture, not the lifetime. Two snapshots taken a day or two before their
  deadline, of logins performed a month earlier, read as "1.6 and 2.0 days" and I
  called that the window. So v0.15.0's original report — two accounts needing
  attention within hours — was correct, and v0.15.1 removed a true warning.

  The seven-day threshold stands as first shipped: against a month-long login it is
  silent for most of a credential's life, and Claude Code's own three-day warning
  covers only the account you are actively using, which is precisely the one kae does
  not need to tell you about.

Both failure directions are now pinned by tests — firing for every account, and firing
for none — and `docs/VALIDATION.md` records what the deadline actually is, retracts the
two-day figure, and says how to re-measure it correctly (only from a credential
captured immediately after a completed `/login`).

---

# kae v0.15.1 (shipped 2026-07-31) — superseded by v0.15.2

**The reasoning below is wrong and v0.15.2 reverts it**; kept as the record of what
was shipped. One fix, found by running v0.15.0 against a real machine an hour after
shipping it.

Baseline: v0.15.0. Contract-stable — no new codes, no field changes, `schema_version`
stays `1`. `credential_expiring` simply stops firing where it carried no information.

- **`credential_expiring` no longer anticipates a refresh-backed deadline.** The
  v0.15.0 lead time treated `refreshTokenExpiresAt` as an end of life. It is a
  **shelf life**: every refresh mints a new refresh token, so the stored number says
  "this frozen copy stops being able to refresh at T", not "this login dies at T".
  Measured on the operator's machine: two claude snapshots carried refresh expiries
  **1.6 and 2.0 days past their own capture**, for logins performed a **month**
  earlier. Against that, a seven-day window fired from the moment of capture and
  never stopped — no claude account could ever read `ok`, and the notice said the
  same thing forever, which is precisely the desensitisation the threshold was
  chosen to avoid.

  The notice is now scoped to deadlines that really are an end of life: a credential
  with **no refresh token** behind it, where the access token's own expiry is the
  whole story (cursor's JWT — cursor-agent never redeems a refresh token). One
  predicate, no per-tool constants. `credential_stale` is untouched and still reports
  a spent shelf life at switch time and in doctor, and the `Credential` column now
  reads `ok` for a refresh-backed credential rather than a permanent `expiring`.

Newly written down rather than fixed: **kae has never actually observed that a spent
refresh token forces an interactive login.** That is the premise of `credential_stale`
itself, and it long predates this release. The same measurement makes it worth
settling, because kae calls a claude snapshot stale considerably more often than the
operator re-logs in. [VALIDATION.md](VALIDATION.md) carries a real-machine gate for
it where either outcome is a result, and [ROADMAP.md](ROADMAP.md) the consequence if
it goes the other way.

---

# kae v0.15.0 (shipped 2026-07-31)

The expiry story had a working *back* half and no *front* half. kae could tell you
a credential was already dead — and only ever after it was, only for accounts it
kept snapshots of, and only if you thought to run `kae doctor`. This release makes
the same knowledge arrive early, arrive in the commands you already run, and cover
the one place kae could not see at all.

Baseline: v0.14.0, shipped 2026-07-31. Contract-stable: `schema_version` stays
`1`, no flag changes. One **new doctor check code** (`credential_expiring`) and two
**additive `omitempty` fields** on the `kae ls` / `kae accounts` / `kae status`
rows (`credential`, `relogin_by`). Everything new is warn-level, so no exit code
changes. Nothing needs re-capturing.

- **A seven-day warning before a credential needs a re-login.** kae's freshness
  was binary: it spoke up only once the access token had expired *and* no usable
  refresh token was left. Claude Code warns at three days, but only about the
  account it is currently using, so the first thing kae ever said about any other
  account was that it was already dead. Both questions now read one deadline, so
  "has it passed?" and "how long until it does?" cannot disagree about where the
  line is. Seven days is a judgement, not a measurement — three is enough for the
  tool you look at daily, while a kae account that is not active is only shown to you
  when you run kae. **The scoping of this notice was wrong in v0.15.0 and is fixed in
  v0.15.1** — see below. It names `kae add --restore <tool> <account>`, the one command
  that logs that account in and puts your currently-live login back afterwards,
  which is the whole point of warning before the deadline rather than after it.
  Silent where the deadline is genuinely unknowable: codex and opencode store a
  refresh token without publishing its expiry, and that zero means *unknown*, never
  "never expires".
- **The inventory shows what needs attention.** `kae ls`, `kae accounts` and
  `kae status` referenced freshness nowhere, so the command that lists your
  accounts could not tell you which one was about to strand you. Each row now
  carries `credential` (`ok` / `expiring` / `stale`) and `relogin_by`, and the human
  tables a `Credential` column that spells out the time left. Absent means kae could
  not judge the snapshot and **never** that it is fine — including when the secret
  store is unreadable, because these are the commands you reach for when something
  is already wrong, so they drop the column rather than fail. The expiry is read
  from the payload every time rather than cached into `account.toml`: a copy of a
  fact is a second source of truth, and a recapture path that forgot to refresh it
  would have `kae ls` calling a dead account healthy.
- **A bound directory's own credential is finally visible.** `kae pin` gives a
  directory its own copy of the credential and the tool refreshes *that copy* in
  place, so a pinned project's login could expire while every snapshot kae has
  still looked fine — and nothing said so, because the stale check reads snapshots.
  `kae doctor` now reports it under the same two codes, naming the directory, and
  the fix is a login *inside* that directory (not `kae pin`, which would re-copy a
  snapshot that may be just as expired). It reads a keychain item only where the
  adapter declares the item moves with its isolation variable — the same gate the
  write side uses, and load-bearing: without it codex's **global** login would be
  read and blamed on an unrelated directory.

Deliberately not shipped: recapturing a bound directory's refreshed token back into
the account snapshot. Several directories can bind one account, each with its own
independently-refreshed token, so nothing says which the single global snapshot
should take — and a directory not opened in weeks holds an *older* token, a
downgrade that "is it usable" cannot detect because both are usable.
[ROADMAP.md](ROADMAP.md) records the two ways it could be defined.

Still open and unchanged: the two live-machine gates in
[VALIDATION.md](VALIDATION.md) — "codex per-directory keyring bind" and "Cursor
full credential set" — both of which need a real keychain and two real accounts.

---

# kae v0.14.0 (shipped 2026-07-31)

v0.13.0 fixed a class — *kae modelled an upstream store location as a constant
when it is a rule*. This release finishes the audit that found it and closes the
gaps that were **not** about upstream at all: two silent lost updates in kae's
own state, a credential store no command could name, and a token that could
reach a doctor message. It also ships the workflow for the next time, as a
skill.

Baseline: v0.13.0, shipped 2026-07-31. Contract-stable: `schema_version` stays
`1`, no flag changes. One **new doctor check code**, `pin_stale`, and
`upstream_version` now covers a second condition (assumption age) — both
warn-level, so no exit code changes. Nothing needs re-capturing.

- **`state.json` lost updates between concurrent switches.** The per-tool locks
  deliberately let `kae use claude <a>` and `kae use codex <b>` run at once, and
  each wrote back a copy of the whole document loaded before the other finished —
  so the loser's field reverted with no error anywhere, `kae status` then named an
  account that was not live, and `kae rollback` restores credentials, not this
  file. Every state write now re-reads under a `state` lock. The same applies to
  *decisions*: `kae account rm` re-checks under that lock whether the account it
  removes is still the active one, because the pre-lock answer can predate a
  switch that finished in between.
- **A bound directory is findable again.** The fragment that selects a
  per-directory store lives *in* the bound directory and the store is named by a
  hash of the path, so nothing outside could answer "which directories bind this
  account": `kae account rm` / `rename` and `kae profile rm` invalidated bindings
  in silence, and a deleted directory stranded its store forever. Each store now
  records the directory it belongs to. Those three commands name the directories
  they invalidate, and `kae doctor` gains **`pin_stale`** for a bound directory
  that is gone or that binds an account you no longer have. `kae pin`,
  `kae pin <tool> <account>` and `kae unpin` also take a per-directory lock, and a
  shared-mode re-bind now repairs a bond dir whose links were wiped — the isolated
  one always could.
- **Every keychain item is scoped to its account, and the adapter's answer wins.**
  claude and cursor were read, written and deleted by service name alone. Worse
  than the read was the write: creating an item preferred *the live item's*
  account over the adapter's, so one item under a stale account (a former `$USER`)
  captured every later write while the tool went on reading the account its own
  rule names — the write succeeds, the item exists, and the tool reports no login.
  A conformance guard now refuses a keychain spec that is not account-scoped.
- **A relative `CLAUDE_CONFIG_DIR` or `CODEX_HOME` warns, per tool.** Both were
  measured rather than assumed, and they diverge differently: claude uses the
  value verbatim, so a relative one moves its files but **not** its keychain item
  (the service name hashes the raw string), while codex canonicalizes against its
  own working directory, which moves the file store *and* the keyring account.
- **A doctor probe's own token can no longer reach its warning.**
  `companion_token_drift` authenticates with the bound token and put the probe's
  stderr into the message verbatim, so a tool that echoed a request header or a
  URL carrying it would have published the secret to stdout and to `--json`. Found
  by writing the redaction assertion AGENTS.md requires of every new output path;
  all three of the newest probes now have one.
- **`upstream_version` also watches the assumptions' age.** It only fired when the
  installed tool moved past the verified release, so a user who never upgrades got
  no signal at all and cursor — which declares no usable version — would get none
  ever. Adapters now declare `VerifiedOn()`, doctor warns at six months, and a
  test parses the version table in `docs/ADAPTERS.md` so the "bump both in one
  commit" lockstep cannot be half-done. codex additionally reports a keychain item
  that exists while kae resolved its file store, which is the disk contradicting
  the model.
- **`kae doctor <tool>` says what it skipped**, and backup pruning takes a lock
  (the backups directory is global while the tool locks are not).
- **New skill: `upstream-auth-drift`.** A *response* workflow, not a monitor —
  detection is its least valuable entry point, because both real failures kae has
  had were noticed by a human first. It carries the expensive middle: measure the
  new behaviour without a login, make kae's model match it, re-record the
  assumption in the same commit. `docs/handoff-upstream-drift.md` is retired; its
  techniques are in the skill, its remaining automation ideas and its
  audited-clean list in [ROADMAP.md](ROADMAP.md).

Still open and unchanged: the two live-machine gates in
[VALIDATION.md](VALIDATION.md) — "codex per-directory keyring bind" and "Cursor
full credential set" — both of which need a real keychain and two real accounts.

---

# kae v0.13.0 (shipped 2026-07-31)

One theme, found by auditing every adapter after v0.12.0: kae modelled several
upstream **store locations as constants when they are rules**, and the rules
resolve per environment, per tool home, and — in two cases — to more than one
store at once. v0.12.0 fixed the first instance (claude's per-config-dir keychain
item); this release fixes the rest of the class, including one that was
**destructive** and shipped.

Baseline: v0.12.0 (identity as a switched artifact + upstream-behaviour checks),
shipped 2026-07-30. Contract-stable: `schema_version` stays `1`, no flag or
exit-code changes. Two credential models changed, so **cursor and codex snapshots
captured before this release must be re-captured** (kae refuses to apply an
incomplete one rather than switching half a credential).

- **codex's keyring item is one item per codex home, and kae was deleting the
  wrong one.** `Codex Auth` is a single service whose *account* attribute is
  derived from `CODEX_HOME` (`cli|` + 16 hex of `sha256` over the **canonicalized**
  path). kae deleted by service name alone, so a codex switch under
  `cli_auth_credentials_store = "keyring"` **destroyed another `CODEX_HOME`'s
  login** — reachable by any user with more than one codex home, and shipped
  through v0.12.0. Reads had the mirror bug (first item of the service wins). Now
  every read, write and delete is scoped by the account kae computes for the
  environment being written, never from the live item or a snapshot. Four review
  rounds on that fix found five more instances of the same shape in the
  restore/rollback paths (a credential written or deleted in a store the tool does
  not read *there*), all closed: a rollback re-resolves the store after a login
  child moved it, a delete is never redirected into the store a tool moved to, and
  a keychain spec with no account is refused rather than applied broadly.
- **codex's store selector is modelled as the enum it is.** `file` (the default
  for an absent key) | `keyring` | `auto` | `ephemeral`, where `auto` means
  *keyring first, file only if the item is absent*. Mapping "anything not keyring"
  to the file store wrote `auth.json` while codex read the keychain. `ephemeral`,
  an unknown value, an unparseable `config.toml`, and the
  `[features] secret_auth_storage` flag are **refused**, not approximated.
- **cursor's credential is a set of three keychain items, not one.** cursor-agent
  writes the access token, the refresh token and the api key as a unit and clears
  them together. kae switched only the access token, so `cursor-agent status`
  reported a consistent-looking pair from two accounts, and an api key left behind
  re-minted the **previous** account's tokens on the next expiry. All three now
  switch as one unit, absent applies as absent, and the `cursor-bedrock-*` triple
  is refused rather than approximated. `applySnapshot` resolves every artifact
  before the first live write, so an incomplete pre-v0.13.0 snapshot fails before
  anything is touched.
- **A per-directory keychain credential is now cleaned up, and the capability is
  declared by item identity.** The flag that decides whether a keychain item can
  be bound to one directory asks "does the item's *identity* move" (service **or**
  account) rather than "does the service name move", and its parity guard derives
  that truth from the spec — which revealed the guard had been skipping codex
  entirely. A superseded per-directory item is removed once nothing points at it
  (`kae unpin`, or a mode toggle), sweeping after the new binding is written so a
  mid-sequence failure cannot leave a live binding pointing at a deleted
  credential.
- **`doctor` stops crying orphan over two whole namespaces.** The secret backend
  holds four key shapes; the orphan check understood two, so every companion
  binding and every env-profile variable warned forever on an enumerable backend —
  with a remediation line that did not even parse. Keys are now composed from one
  shared classifier, so the check and the key builders cannot drift.
- **claude is refused where `CLAUDE_CODE_CUSTOM_OAUTH_URL` renames its stores.**
  A non-empty value moves *both* the keychain service and the identity file
  (`.claude<suffix>.json`). Refused rather than computed, because the other source
  of that suffix — the build channel — is invisible from the environment, so
  computing it would cover one of three sources and stay silently wrong for the
  rest. The host-managed credential trio (`CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST`
  and friends) is a different class — it moves *what authenticates*, not where kae
  writes — so it warns instead.
- **copilot honours `COPILOT_HOME`.** It replaces `~/.copilot` outright, so every
  switch in a directory that sets it was patching a `config.json` copilot does not
  read. A **relative** value is followed but warned about: copilot resolves it
  against its own working directory and kae is invoked from anywhere in a project.
  One branch of copilot's precedence — the deprecated `--config-dir` flag — cannot
  be seen from the environment at all and is documented rather than guessed at.
- **opencode warns where `auth.json` is not what opencode reads**:
  `OPENCODE_AUTH_CONTENT` supplies a whole credential file inline and is consulted
  before the file, and a relative `XDG_DATA_HOME` (which opencode uses verbatim,
  against its own cwd) puts kae and the tool on different files. Re-verified in the
  same pass: `auth.json` **is** still the live store — `account.json` is derived
  from it and the `credential` table in `opencode.db` is a dormant one-shot import
  — so the store did not move, and the audit's claim that kae patches a dead file
  did not hold.
- **agy warns where it will skip the keychain.** agy's keyring is conditional: an
  ssh/wsl/container detector bypasses it, every keyring operation has a 1s timeout,
  and any failure falls back to a file. kae warns on the env-visible triggers and
  deliberately declares **no** file artifact on macOS, because the fallback file's
  path is not derivable from the binary and a guessed path is a write nothing
  reads.
- **Recorded, not guessed** ([ROADMAP.md](ROADMAP.md)): codex's per-directory
  keyring capability (declared unavailable until the item lifecycle is settled),
  agy's unmeasured macOS fallback path, opencode's dormant DB store, copilot
  isolation (now possible, not built), and claude's build-channel OAuth suffix.
  Every assumption above is a row in [VALIDATION.md](VALIDATION.md) with a
  login-free re-verification procedure — several of them replacing recipes that
  used to need a real account.
- **Acceptance**: `mise run check`, `mise run audit` (govulncheck: 0 reachable),
  and `mise run goreleaser-check` green; every fix carries a regression test
  confirmed to fail against the old behaviour. Two real-machine gates stay open
  and are listed in [VALIDATION.md](VALIDATION.md): the codex per-directory
  keyring bind and the cursor three-item credential set both need two live logins.

---

# kae v0.12.0 (shipped 2026-07-30)

Make the *identity* a switched artifact, and start watching the upstream
behaviour kae depends on. Prompted by a live failure: `kae` switched claude's
credential correctly while Claude Code kept displaying the previous account, and
no check fired — the layout had not changed, only the behaviour behind it.

Baseline: v0.11.0 (companion re-bind lockstep + token-identity drift), shipped
2026-06-27.

- **`/oauthAccount` is switched with the credential** (`artifact.Spec.IdentityOnly`).
  claude's self-heal of it is real but gated behind a 24h `profileFetchedAt` TTL
  that every token refresh renews without rewriting `emailAddress`, so for a
  credential in daily use it never fires. The credential remains claude's sole
  *auth* artifact; this is attribution. Pointer-patch only — every other key of
  `~/.claude.json` survives byte-identical.
- **Two offline `doctor` checks for the class of failure that started this**:
  `identity_drift` (the live identity no longer matches what kae applied — compared
  on the account-naming keys, never byte-wise) and `upstream_version` (the
  installed tool is a newer major/minor than the `VerifiedVersion()` its adapter
  declares; patch bumps stay silent). `docs/VALIDATION.md` gains an "Upstream
  Behaviour Assumptions" table — the original assumption was load-bearing and had
  never been written down as a verifiable item, which is what let it expire
  unnoticed.
- **Expiry is read, not guessed.** `freshness.Info` gains `RefreshExpiresAt` and
  `Revoked`: a refresh token now lives days rather than a month, so its mere
  presence no longer counts as recoverable, and the tombstone claude writes after
  a failed refresh (`accessToken:""`, `expiresAt:0`) is read as dead instead of
  as "no expiry recorded". A one-directional recapture guard stops a dead live
  credential from overwriting a still-usable snapshot — the one path that could
  lose a credential unrecoverably.
- **Stale warnings are delivered where they can be acted on**: stderr, before
  anything is applied, not suppressed by `--quiet` (the form the mise hook runs),
  with a roll-up line when several tools need a re-login. Exit codes unchanged.
- **A bound directory's credential goes where the tool reads it.** claude's
  keychain service name is derived from `CLAUDE_CONFIG_DIR`, not modelled as a
  constant, so the four mechanisms that set an isolation env var
  (`kae pin -s|-i`, `kae use -i`, `kae run -i`) write the per-directory item
  rather than a file the tool stops reading after its first token refresh. One
  helper does it for all four, which also fixes the credential being taken from
  the live store instead of the bound account's snapshot. See the release gate
  below.

Contract: additive apart from four deliberate behaviour changes.
`schema_version` stays `1`; two new `doctor` check codes; one new artifact in
claude's snapshot (`oauth_account`), applied as absent when a snapshot predates
it. The changes: claude reports as unsupported (exit `5`) while
`CLAUDE_SECURESTORAGE_CONFIG_DIR` is set, because it moves the credential store
outside what kae can model; binding a profile whose account has no captured
credential now warns instead of skipping in silence; `kae pin <tool> <account>`
on an uncaptured account fails (exit `7`) in isolated mode as it already did in
shared mode; and applying a snapshot whose payload shape does not match the
artifact the current environment resolves is refused (exit `10`) with recapture
guidance, instead of silently nesting a whole document under its own JSON pointer
— reachable by switching claude's driver after a capture.

One dependency added: `golang.org/x/text`, for the NFC normalization claude
applies before hashing a config dir into its keychain service name. Getting that
wrong is the same silent wrong-credential class this release fixes, and it is not
a few lines of our own.

**Release gate (met): the macOS pin gap.** `CLAUDE_CONFIG_DIR` does not force
file-based auth on macOS — claude namespaces its keychain service by the config
dir and deletes kae's credential copy on the first token refresh, after which
`kae pin <tool> <account>` reported success and changed nothing a pinned
directory read. Fixed at the root: the service name is now derived from the
config dir (`claude.keychainService`), and one helper writes a bound directory's
credential into the store the tool actually reads, for all four mechanisms that
set an isolation env var (`kae pin -s|-i`, `kae use -i`, `kae run -i`) — the
defect reached further than `kae pin`. The same change fixes the credential being
copied from the live store instead of the account's snapshot.

Acceptance for this gate is the login-free `security`-shim procedure in
[VALIDATION.md](VALIDATION.md) § "Upstream Behaviour Assumptions" (confirm the
service name claude resolves matches what kae wrote), plus a real two-account pin
on macOS: re-bind, launch claude in the directory, and confirm it reports the
account kae bound.

---

# kae v0.11.0 (shipped 2026-06-27)

Close the companion-auth identity-drift gaps: keep companions in lockstep on a
single-tool re-bind, and add the token-side drift check that mirrors git's.
Additive and contract-stable: `schema_version` stays `1`; the only new config is
a reserved, kae-managed `expected_login` metadata key on token companions.

Baseline: v0.10.1 (companion completion + self-maintaining completion), shipped
2026-06-23.

- **Companion re-bind in lockstep**: `kae pin <tool> <account>` now recomputes the
  directory's effective profile and re-applies that profile's companions
  (regenerating the git-config file), clearing them when the new account set is
  ad-hoc (`KAE_PROFILE` empty). Before, a single-tool re-bind left the fragment's
  companion block (git-config path, token `exec()` lines, redactions) pointing at
  the old profile — a stale git/token identity could outlive the re-bind.
- **Token-identity drift** (`companion_token_drift`, opt-in): the token
  counterpart to `companion_drift`. It resolves the live login a bound token maps
  to (`gh api user`) and compares it to a recorded `expected_login`, warning on a
  wrong-account token or an inactive pin (token absent from the env).
  `expected_login` is captured automatically at `kae companion add` (best-effort
  via the spec's declarative `LoginProbe`; an offline/invalid probe leaves it
  unset). The check makes a network call, so it is opt-in: doctor prompts on a TTY
  or honours `--yes`, and skips under `--json`/non-interactive. gh only;
  cloudflare is deferred (`wrangler whoami` needs the binary and a user-scoped
  token), with the `LoginProbe`/`expected_login` mechanism generalized for it.
- **Acceptance**: `mise run check` green; new unit tests cover re-bind companion
  lockstep + ad-hoc clear, the drift check's match/mismatch/inactive/probe-fail
  paths, the add-time probe (success + gentle skip), and the opt-in resolution;
  JSON contract unaffected (`schema_version` 1).

---

# kae v0.10.1 (released 2026-06-23)

Finish companion's command UX and make shell completion self-maintaining.
Companion-auth shipped in v0.10.0 without completion for its subcommand, and the
registered completion script could go stale after a structural change with no
signal to the user. Additive and contract-stable: `schema_version` stays `1`, no
config change; the only new surface is a `kae completion --refresh` flag.

Baseline: v0.10.0 (companion-auth lockstep), shipped 2026-06-23.

- **`kae companion` completion**: the `add`/`rm`/`list` sub-verbs, then a
  profile, a companion id, and that companion's knob names complete in bash, zsh,
  and fish. Two new `__complete` kinds (`companions`, `companion-knobs <id>`)
  back the dynamic candidates.
- **Completion parity guard**: a `subcommandVerbs` table + a test assert every
  subcommand group has a completion case with its sub-verbs in all three scripts,
  so a new group cannot ship as a completion dead end again.
- **Self-maintaining completion** (`kae completion --refresh`): rewrites every
  already-registered completion file from the current binary (never creates a new
  registration). `mise run install` and `scripts/install.sh` run it after placing
  the binary, so an upgrade or a local rebuild propagates a structural completion
  change with no manual re-install. The mise-hook registration self-sources and
  needs nothing; for zsh under `compinit -C`, `--refresh` prints the
  compdump-rebuild command rather than mutating the user's cache.
- **Acceptance**: `mise run check` green; completion behavior covered by unit
  tests (parity, refresh, the new kinds); JSON contract unaffected.

---

# kae v0.10.0 (released 2026-06-23)

Companion-auth lockstep: bind the identity of the non-AI tools an agent shells
out to — `git`, `gh`, and cloud CLIs — to the same profile, so an agent and the
tools it runs act under one account and never commit, push, or deploy as the
wrong identity. Companions are not Tools: kae does not capture their
credentials, it only drives the env/config those tools already read. Additive
and contract-stable: `schema_version` stays `1`, config stays version `1` (the
`[profiles.<name>.companions]` table is an additive key old kae tolerates), no
new runtime dependency.

Baseline: v0.9.1 (manual login-identity override), shipped 2026-06-20.

- **Declarative companion registry** (`internal/companion`): one `Spec` struct
  literal per tool, keyed by override kind — `git-config` (render a kae-owned
  `GIT_CONFIG_GLOBAL` file that `[include]`s `~/.gitconfig` and overrides only
  the identity fields, so the user's gitconfig is never modified), `token`
  (`GH_TOKEN`/`CLOUDFLARE_API_TOKEN` resolved at mise eval time via an `exec()`
  lookup against the secret backend, never written to disk), and `config-dir`
  (`KUBECONFIG` set to a user-supplied path kae only references). Adding a tool
  is one literal + registration. git/gh/cloudflare/kubectl ship.
- **Delivery and CLI**: bindings are opt-in per profile and delivered through
  the per-directory `kae pin` mise fragment (the global fragment holds no
  profile, so it carries no companions); reverted by `kae unpin`.
  `kae companion add/rm/list` manages them — token values go to the secret
  store via stdin, config.toml holds only knob names and non-secret values. The
  hidden `kae __companion-token` helper is the one documented stdout-secret
  path (a git-credential-helper-style seam mise's `exec()` template invokes),
  excluded from all human/JSON reporting and added to the fragment's
  `redactions`.
- **doctor binding health**: `companion_missing` (a bound token knob has no
  stored secret) and `companion_binary` (a bound companion's CLI is absent from
  PATH), config-level and deterministic, on the unfiltered report.
- **live-identity drift** (`companion_drift`): inside a pinned directory binding
  git, shell out to `git config --get user.<knob>` and compare the identity git
  would actually commit with against the bound `email`/`name`/`signingkey`,
  flagging a repo-local override or an inactive/untrusted pin — the silent
  wrong-author commit companion-auth exists to prevent. Scoped to git: its
  expected values are non-secret and the probe is offline; token companions keep
  no expected identity and a live check would need a network call, so they are
  out of scope. Warn-level, so doctor's exit stays `0`.
- **Normative contract**: [docs/ADAPTERS-COMPANION.md](ADAPTERS-COMPANION.md)
  defines per-companion switched/preserved; code must match it.
- **mise trust**: an `exec()` token fragment must be trusted before mise loads
  it — the existing requirement for any kae pin fragment, not new; trusting it
  also authorizes the `__companion-token` helper (kae-owned, git-ignored
  fragment, helper only reads the secret backend).
- **MVP constraints**: a single-tool `kae pin <tool> <account>` re-bind keeps
  the companion lines; `kae pin <profile>` re-binds them. token-companion drift
  and more companions (fly/supabase/…) are follow-ups.
- **Acceptance**: `mise run check` green; companion-auth real-machine smoke
  (docs/VALIDATION.md "companion-auth surfaces", incl. the `companion_drift`
  assertion) on a temp HOME with `mise trust`.

---

# kae v0.9.1 (released 2026-06-20)

Make a login identity recordable when kae cannot auto-detect it, and stop a
missing one from reading like a bug. Shipped 2026-06-20.

- **`kae add --identity <value>`** records the identity verbatim (sanitized);
  with no explicit name it also derives the account name from it.
- **`kae account set-identity <tool> <account> <value>`** sets or replaces a
  captured account's identity without re-capturing the credential (additive
  `--json` report: `tool`, `account`, `identity`).
- A failed auto-detection on an explicit-name `kae add` is now a calm **note**
  (identity is optional), not a silent blank and not an alarming warning with a
  raw filesystem error. `kae status` / `kae ls` render an empty identity as `-`
  (the not-set placeholder) instead of a blank cell.
- Shell completion offers the new `set-identity` subcommand (`kae account
  <TAB>`); `--identity` is offered by the existing flag completion.
- Motivation: on current Antigravity (1.0.x) agy's live account is resolved from
  an opaque keychain token server-side and is no longer written to disk, so
  `~/.gemini/google_accounts.json` is stale and auto-detection cannot see it
  (docs/ADAPTERS.md). The override is generic — it helps any tool whose identity
  kae cannot read.
- Acceptance: `mise run check` green; JSON contract additive (`schema_version`
  1); no breaking change.

---

# kae v0.9.0 (released 2026-06-19)

Ship installable binaries and bring the README to OSS parity. Until now `kae`
was `go install`-only with no release assets; v0.9.0 adds a GoReleaser pipeline
so `curl | sh`, mise, and prebuilt archives work, and rewrites the README around
that. Additive and contract-stable: `schema_version` stays `1`, no new runtime
dependency.

Previous baseline: v0.8.9 (zsh completion `--install` fpath detection).

Shipped 2026-06-19.
- **Release pipeline**: `.goreleaser.yaml` + `.github/workflows/release.yml`
  build darwin/linux × amd64/arm64 archives with `checksums.txt` and a
  checksum-manifest attestation on every `v*` tag; `scripts/install.sh` is a
  checksum-verifying `curl | sh` installer; `.github/workflows/ci.yml` runs the
  shared check gate (`check.yml`: vet/gofmt/test/mod-verify) on push/PR. See
  [Release Process](#release-process).
- **README**: rewritten to OSS/updev parity — Why, what stands out, install
  (curl/mise/go/releases), a dedicated Shell Completion section (including the
  zsh compdump-rebuild gotcha), Tool Support and Common Commands tables. The
  stale "release archives ship a `kae` binary" claim is now actually true.
- **zsh completion install note**: the on-fpath `--install` note warns that a
  stale compinit *compdump* hides a newly added function and how to rebuild it
  (the cause of a real "installed but not showing" report).

# kae v0.8.9 (released 2026-06-18)

`kae completion zsh --install` wrote to a fixed `$XDG_DATA_HOME/zsh/site-functions`
dir that is often not on the user's `fpath`, so the installed file never loaded
without a manual `.zshrc` edit — completion appeared to work only after
`eval "$(kae completion zsh)"`. Additive, contract-stable: `schema_version`
stays `1`, no new dependency.

Previous baseline: v0.8.8 (opencode identity + flag-aware/flag-name completion).

Shipped 2026-06-18. `completionTarget` now prefers the first **existing** common
user zsh completions dir (`~/.config/zsh/completions`, `~/.zsh/completions`,
`~/.zfunc`) — one the user created because it is on their `fpath`, so the file
auto-loads in a new shell with no `.zshrc` change. Only when none exists does it
fall back to `$XDG_DATA_HOME/zsh/site-functions` and print the `fpath=(…)` line
to add. kae does not shell out to zsh to read `$fpath` (an interactive zsh's
stdout is easily polluted by rc files); directory existence is a robust proxy
for "on fpath". bash/fish are unchanged (their XDG dirs auto-load).

# kae v0.8.8 (released 2026-06-18)

Daily-use fixes surfaced right after v0.8.7: opencode auto-named accounts by an
opaque UUID, and shell completion broke when a flag preceded the positionals
(`kae add --no-login <TAB>` offered nothing). Additive and contract-stable:
`schema_version` stays `1`, no new dependency.

Previous baseline: v0.8.7 (complete account-identity coverage).

Shipped 2026-06-18.
- **opencode identity**: `opencode.Identity` now decodes the `/openai` access
  token (a JWT) and prefers its `https://api.openai.com/profile` email claim,
  falling back to the opaque `accountId` UUID only when no email is present
  (mirrors codex). Re-capture an existing UUID-named opencode account to pick up
  the email.
- **flag-aware completion**: the bash/zsh/fish scripts route by the
  flag-filtered positional index, so a flag before the positionals no longer
  shifts completion (`kae add --no-login <TAB>` completes tools; `kae use -i
  claude <TAB>` completes accounts).
- **flag-name completion**: a new `kae __complete flags <command>` backend lists
  a command's flags (sourced from the same per-command registrars the parser
  uses, `internal/cmd/flagspec.go`, so the list never drifts); the scripts call
  it when the current word starts with `-` (`kae add --<TAB>`, `kae run -<TAB>`).

# kae v0.8.7 (released 2026-06-18)

Complete account-identity coverage. agy was the last tool whose login identity
kae could not read, so its accounts showed a blank `Identity` and `kae add agy`
required an explicit name. v0.8.7 implements `agy.Identity` from the active
Google account in `~/.gemini/google_accounts.json` (`.active`, written by the
Antigravity login; the keychain token itself is opaque), so every tool now
exposes an identity. `kae status` gains an `Identity` column to match `kae ls` /
`kae accounts`. Additive and contract-stable: `schema_version` stays `1`, no new
dependency.

Previous baseline: v0.8.6 (agy keyring driver + terser run).

Shipped 2026-06-18. §A: `agy.Identity` reads `~/.gemini/google_accounts.json`
`.active`, so `kae add agy` (no name) auto-detects the account name and the
snapshot records the identity; `TestIdentifierConformance` pins that all six
tool adapters now implement `adapter.Identifier`. §B: `kae status` shows the
active account's identity (text column + additive `identity` JSON field,
`omitempty`). Existing accounts captured before their tool gained identity stay
blank until re-captured (`kae add --no-login <tool> <name>` while logged into
that account backfills it) — the documented backfill path, no new command.

# kae v0.8.6 (released 2026-06-18)

Lift agy account switching on macOS (the one tool kae still cannot switch here)
and pay down small daily-use friction, closing the open real-machine acceptance
items from v0.8.3/v0.8.4 along the way. Additive and contract-stable: no
JSON-contract break (`schema_version` stays `1`), no new dependency. The agy
driver reuses the verbatim-keychain pattern already proven for codex/claude/
cursor (the `security` calls go through `internal/runner`); the run change
reuses `adapter.Binary()` and the existing `run` transaction.

Previous baseline: v0.8.5 (did-you-mean nearest-match hint).

Shipped 2026-06-18. §A: the agy keychain driver switches the
`gemini`/`antigravity` Keychain item on macOS, matched by service **and**
account so a sibling `gemini` item is never touched; Linux/WSL keeps the file
driver. §B: `kae run <tool> <account>` defaults the child to the tool binary
when no `-- <cmd>` is given. §C: `claude /login` is launched via the upstream
flow (unchanged); agy login stays deferred (GUI OAuth, no kae-drivable login).
§D: the **agy two-account real-keychain gate PASSED**; **fish was dropped from
the verified shells** (`kae completion fish` stays best-effort); the codex
keyring two-account gate stays the one carried, unit-covered open item
([VALIDATION.md](VALIDATION.md)).

## Scope

### A. agy keyring driver (macOS) — switch agy accounts

The 2026-06-18 discovery settled agy's credential contract on macOS: it lives in
the **login Keychain**, not a file. Lift the file-only agy adapter so kae can
switch agy on macOS, mirroring the codex keyring driver (v0.8.3 §C):

- **Item contract**: service `gemini`, account `antigravity` (a fixed literal,
  **not** derived per tool home like codex's `cli|<hash of CODEX_HOME>`). The payload is a
  single **opaque ~686-byte token string** — not JSON. kae captures and applies
  it **verbatim** through `security` (the `internal/runner` seam), with a
  structure guard of "non-empty, single-line" (no JSON parse, unlike codex).
- **Match by service *and* account.** The `gemini` service is shared with the
  Gemini ecosystem; only `acct=antigravity` is agy's, so kae must never touch a
  `gemini` item with a different account. Apply replaces the single
  `gemini`/`antigravity` item (`security add-generic-password -U`).
- **macOS keyring path; file path unchanged elsewhere.** On macOS the adapter
  resolves the keychain driver; the existing file-based driver
  (`credentials.enc`/`credentials.json`/`oauth_creds.json`) stays for Linux/WSL
  headless setups. `Detect`/`doctor` report the keychain item's presence on
  macOS instead of warning "kae cannot switch agy yet". `Binary()` stays `agy`.
- **Capture is `--no-login` only.** agy has no kae-drivable login (§C), so
  `kae add agy <name>` snapshots the live keychain item; there is no login flow.
- Fake-`security` round-trip tests (as for codex); the two-account real-keychain
  gate is a release item (§D).

### B. Terser one-shot `kae run` (default the child to the tool binary)

`kae run <tool> <account> -- <tool>` is the documented way to open a session
under another account (works inside a pinned directory without unpinning), but
the trailing `-- <tool>` is redundant. Default it:

- **`kae run [-s|-i|--env] <tool> <account>`** with no `-- <cmd>` runs the
  adapter's `Binary()` as the child (claude→`claude`, cursor→`cursor-agent`,
  agy→`agy`, …). `kae run claude main` ⇒ runs `claude` with the account applied;
  `kae run -i claude main` runs it in the isolated home.
- An explicit `-- <cmd>` is unchanged and still wins.
- An `all`/profile target (no single child binary) or a tool with no launchable
  upstream binary still requires `-- <cmd>`, erroring clearly when it is missing.

### C. Login UX: `claude /login` verification (agy login stays deferred)

- **agy login — discovery done (2026-06-18); not kae-drivable.** The `agy` CLI
  has **no `login`/`auth`/`whoami` subcommand** — authentication is GUI/browser
  OAuth, so kae's shell-out login flow cannot drive it. agy capture is
  `--no-login` only (§A); switching now works via the keychain driver, but the
  login itself stays the user's GUI action.
- **`claude /login`**: verify behavior across recent claude versions; the
  v0.8.x "login flow exited without changing auth → exit `11`" detection stays.

### D. Close the open real-machine acceptance gates

Run these real-keychain/real-shell gates during the v0.8.6 release gate where
the environment allows, and record PASS/defer in VALIDATION.md:

- **agy two-account real-keychain gate** (new, §A): with two agy logins,
  `kae add agy <a>` / `kae add agy <b>` and `kae use agy <a>` round-trip through
  the `gemini`/`antigravity` keychain item and a fresh agy session reports the
  switched account.
- **fish real-machine completion smoke** (v0.8.4 — the release machine had no
  fish; bash/zsh verified).
- **codex keyring two-account real-keychain gate** (v0.8.3 — the file-driver
  round-trip is unit-covered; the two-account real-keychain path is not).

The agy and codex drivers are covered by fake-`security` round-trip tests, so a
gate that cannot run on the release machine stays deferred with the reason
recorded — not a v0.8.6 code blocker.

## Non-Goals (this release)

- TUI, Windows, remote share-list shipping — see [ROADMAP.md](ROADMAP.md).
- agy login flow / identity auto-detection — no kae-drivable login (GUI OAuth),
  and the token is opaque, so agy stays `--no-login` capture with an explicit
  name.
- agy *home* isolation (`use -i agy`) — unchanged; only credential switching is
  added. agy keeps refusing `-i` (no redirectable home env var).
- `kae env export` / explicit value reveal — CI does not use kae; dropped.
- Global mise tasks (`kae mise init --global`) — separate, design-first candidate.
- Any JSON-contract break: `schema_version` stays `1`.

## Acceptance Criteria

- **agy keyring switch**: on macOS, `kae add agy <name>` snapshots the
  `gemini`/`antigravity` keychain item and `kae use agy <name>` writes it back
  verbatim (matched by service **and** account); a fresh agy session reflects the
  switched account. A non-`antigravity` `gemini` item is never touched. An empty
  payload is refused (structure guard). The detect-only / "cannot switch" warning
  is gone on macOS; the file driver still works on Linux/WSL. Fake-`security`
  round-trip + temp-HOME tests; the redaction tests confirm the token never
  reaches stdout/JSON/logs/metadata.
- **run default child**: `kae run claude main` (no `--`) runs `claude` with the
  account applied; `kae run -i claude main` runs it in the isolated home; an
  explicit `-- <cmd>` still wins; an `all`/profile target or a binary-less tool
  without `-- <cmd>` errors naming the explicit form. Asserted via the runner
  seam; temp-HOME tests.
- **login**: `claude /login` verified across versions; agy login stays deferred
  (no kae-drivable login), with the reason recorded.
- **gates**: agy two-account + codex keyring two-account + fish smoke recorded in
  VALIDATION.md (PASS or deferred-with-reason).
- `mise run check` passes; no new entry in `go.mod`; the JSON contract is
  unchanged.

## Release Steps

1. Bump `toolVersion` to v0.8.6.
2. §A agy keyring driver (macOS `gemini`/`antigravity` verbatim item; service+
   account match; file driver retained for Linux/WSL); fake-`security` +
   temp-HOME tests; redaction test.
3. §B `run` default-child-binary; temp-HOME tests.
4. §C `claude /login` verification (agy login stays deferred).
5. Docs (ADAPTERS / DATA-MODEL / CLI / SECURITY / ARCHITECTURE / README as
   needed).
6. §D real-machine gates (agy two-account, codex keyring two-account, fish
   smoke); tag `v0.8.6`, GitHub release.

---

# kae v0.8.5 (released 2026-06-17)

Catch a typo before it becomes a "no such command". When an unknown command,
tool, or profile is close to a real one, name the nearest match in the error —
"did you mean `use`?" — instead of only listing the full vocabulary. The
candidate lists are exactly the ones v0.8.4's `kae __complete` backend already
surfaces (router commands, `constants.Tools`, config profiles), so this is a
thin, additive layer over a settled source of truth: no JSON-contract break
(`schema_version` stays `1`), no new dependency (the edit-distance check is
hand-rolled), and no change to any existing resolution path.

Previous baseline: v0.8.4 (dynamic shell completion).

Shipped 2026-06-17. §A: the did-you-mean hint fires at all three sites plus
`kae doctor` (unified onto the shared `validateTool`); fully covered by
unit/temp-HOME tests — no real-machine gate (a pure-text behavior). §B (the
chezmoi standardization of the mise-integration + did-you-mean patterns into the
go-cli-tooling shared standard) also landed 2026-06-17: a new
`docs/go-cli/PATTERNS.md` in the chezmoi repo, with this repo's bundled
`.claude/skills/go-cli-tooling/` resynced from it.

## Scope

### A. "Did you mean?" nearest-match hint (kae)

A shared `internal/cmd` helper computes the nearest candidate to an unmatched
token by Levenshtein distance and appends a hint to the existing usage error.
It is suggestion-only — the command still fails with the same exit code; only
the message gains a "did you mean X?" line.

- **Threshold (avoid noise)**: suggest only when the best distance is `<= 2`
  **and** `<= len(input)/3 + 1` (so a 3-char typo of a long word still hints,
  but a wildly different token does not). A tie or no candidate under the
  threshold appends nothing — the error is unchanged.
- **Three call sites**, each table-driven off the same list `kae __complete`
  uses, so candidates never drift:
  - **unknown command** — `Root()`'s `default` arm, against `completionCommands`
    (aliases like `u`/`p`/`s` included in the match set so `kae uze` → `use`).
  - **unknown tool** — `validateTool`, against `constants.Tools` (after the
    prefix-alias and removed-tool paths, which are unchanged: a hint fires only
    when `resolveToolArg` did not resolve and the tool is genuinely unknown).
    `kae doctor <tool>` was unified onto this same `validateTool` call (it had a
    divergent copy of the unknown-tool error), so it gains the hint and the
    removed-tool successor message too.
  - **unknown profile** — the profile-resolution not-found error, against
    `Config.ProfileNames()`.
- **Out of scope**: account names (too many, low-value, and they sanitize
  freely) and flags. Single best match only — no multi-candidate "did you mean
  X, Y, or Z?" list (that was the v0.8.4 non-goal; one suggestion keeps the
  message terse).
- Temp-HOME tests: a near-miss at each call site yields the hint; an unrelated
  token yields the unchanged error; an exact alias/prefix still resolves with no
  hint (no regression to `resolveToolArg`).

### B. Standardize the reusable patterns into the Go CLI standard (chezmoi)

**Separate from the kae release** (kae repo unaffected): promote two reusable
patterns proven in kae into the shared Go CLI standard so sibling tools inherit
them. This folds in v0.8.4 §E (the mise-integration pattern) and adds the
did-you-mean pattern from §A above. All three targets are sourced from chezmoi
(`~/.local/share/chezmoi`); apply with `chezmoi apply`:

1. **mise-integration pattern** (v0.8.4 §E): pin env-redirect fragments +
   dynamic completion via a hidden `__complete` backend (usage/`complete`,
   global-vs-project registration rules) — captured in the agent memory
   `mise-integration-pattern`.
2. **did-you-mean pattern** (§A): a hand-rolled nearest-match hint over the same
   live candidate lists the completion backend exposes (no framework), with the
   noise-avoiding distance threshold.

Reflect both in the three standard locations:
- **CLI standard docs** — `docs/go-cli/` and `docs/go-cli-architecture.md`.
- **go-cli-tooling skill** — `dot_agents/skills/go-cli-tooling/` (the canonical
  source; it symlinks into `~/.claude` and `~/.agents`, and this repo's bundled
  `.claude/skills/go-cli-tooling/` re-syncs from it).
- **Templates** — the relevant `chezmoi_templates/` / `dot_*` `.tmpl` files.

## Non-Goals (this release)

- Multi-candidate suggestion lists, account/flag suggestions — single
  best-match command/tool/profile hints only.
- A fuzzy-matching dependency — the edit-distance check stays hand-rolled.
- fish real-machine completion smoke and the codex keyring two-account gate —
  open acceptance items, tracked separately (not blockers for v0.8.5).
- Any JSON-contract break: `schema_version` stays `1`.

## Acceptance Criteria

- **hint**: `kae uze` names `use`; `kae add clade` names `claude`;
  `kae use mian` (a near profile) names `main`; each still exits with its
  original code. An unrelated token (`kae zzzzz`) appends no hint. An exact
  prefix/alias (`kae u`, `kae cl main`) resolves with no hint and no behavior
  change. Temp-HOME tests at all three sites.
- **no drift**: the suggestion candidate lists are the same ones
  `kae __complete commands|tools|profiles` returns (asserted by sharing the
  source slice/function, not a copy).
- **standard (chezmoi)**: the mise-integration and did-you-mean patterns appear
  in `docs/go-cli/`, the go-cli-tooling skill, and the templates; `chezmoi apply`
  is clean. (Out-of-repo; verified in the chezmoi tree, not by `mise run check`.)
- `mise run check` passes; no new entry in `go.mod`; the JSON contract is
  unchanged.

## Release Steps

1. Bump `toolVersion` to v0.8.5.
2. §A did-you-mean helper + the three call sites; temp-HOME tests.
3. Docs (CLI: note the hint in the relevant command/error sections; README if a
   user-facing example helps; ARCHITECTURE if the helper is worth a line).
4. Tag `v0.8.5`, GitHub release.
5. §B standardize the patterns in chezmoi (separate work item, after v0.8.5
   ships): mise-integration + did-you-mean into `docs/go-cli/`, the
   go-cli-tooling skill, and the templates; `chezmoi apply`.

---

# kae v0.8.4 (released 2026-06-17)

Make shell completion deep and dynamic — sourced from kae's live state — and
lean on mise where the user already has it. One hidden `kae __complete` backend
feeds both kae's own shell completion (`kae use <TAB>` → real
profiles/accounts/flags) and mise task-argument completion
(`mise run <task> <TAB>`). No JSON-contract break (`schema_version` stays `1`);
no new dependency (kae stays hand-rolled). Reusable mise-integration patterns
recorded for sibling tools.

Previous baseline: v0.8.3 (discovery-unblock).

Shipped 2026-06-17. bash and zsh completion verified on macOS — `kae <TAB>`,
`kae use <TAB>`, and `kae use claude <TAB>` resolve live commands / profiles+tools
/ tool-scoped accounts through `kae __complete` (the two-TAB listing is the
shells' standard ambiguous-completion behavior, governed by the user's own
`LIST_AMBIGUOUS` / `show-all-if-ambiguous` settings, not a kae defect). The
**fish real-machine smoke is deferred** (fish was not installed on the release
machine) and is the one open acceptance item — run the VALIDATION.md "v0.8.4
real-machine smoke" for fish before relying on fish completion. Making the mise
`ai-switch` tasks available globally (not just in the project that ran
`kae mise init`) is a post-ship candidate (ROADMAP.md).

## Scope

### A. `kae __complete` — one completion backend

A hidden `kae __complete <kind> [args]` subcommand (omitted from `kae help`)
prints one candidate per line from kae's live surface:

- `commands` — the router's public commands (from the `Root()` table)
- `tools` — `constants.Tools`
- `profiles` — config profile names
- `accounts [<tool>]` — captured accounts, optionally scoped to one tool

It is the single source every completion surface consults, so candidate lists
never drift from the real router/config/state. Read-only, no locks, fast. The
line-oriented output is an internal contract (not the JSON contract).

### B. Native shell completion on the backend (`kae <TAB>`)

Rewrite `kae completion <bash|zsh|fish>` so the emitted script calls
`kae __complete` **dynamically** instead of baking a static word list at
generation time. Result: `kae use <TAB>` offers live profiles+accounts,
`kae use claude <TAB>` offers claude's accounts, `kae account rm <TAB>` /
`kae add <TAB>` resolve from state, and word 1 completes commands; per-command
flag completion where cheap.

`kae completion <shell> --install` registers it **interactively**: detect
whether mise is active, then let the user choose where to register — the shell's
standard completions dir (fpath / `~/.config/fish/completions/` /
bash-completion dir; mise-independent, the default suggestion), a **global**
mise `[hooks.enter]` that sources `kae completion <shell>` (mise-native,
opt-in), or print-only. kae never silently rewrites the user's global mise
config. kae's own completion is binary-scoped, so registration is always global,
never per-project (a per-directory registration would make `kae <TAB>` blink in
and out by directory).

### C. mise task-argument completion (`mise run <task> <TAB>`)

`kae mise init` generates tasks with a `usage` spec and
`complete "<arg>" run="kae __complete …"` directives, so `mise run <task> <TAB>`
completes from kae's live state through the same backend. Add argument-taking
tasks where it helps (a profile-argument switch task; a `tool`/`account` run
task); the fixed-profile convenience tasks stay. Task-argument completion is
project-scoped, so it lives in the project mise block — the opposite of §B's
global registration. Open point settled during implementation: whether mise's
`complete run` exposes the prior argument (to scope accounts by tool); if not,
the task completes all accounts while kae's native path keeps the tool-scoped
behavior.

### D. Docs — both audiences

Document three registration paths so non-mise users are first-class:
(1) `eval "$(kae completion <shell>)"` in the shell rc, (2) a completion file in
the shell's fpath / completions dir, (3) `kae completion --install`. The mise
enter-hook path is an opt-in convenience, not the primary route (mise hooks are
experimental and need `mise activate` + a trusted config). Update CLI (completion
section + `kae mise init` task usage), README quickstart, DATA-MODEL (mise block
/ task shape), ARCHITECTURE (the `__complete` seam), and VALIDATION (a completion
smoke).

### E. Standardize the mise-integration pattern (post-implementation follow-up)

**Only after §A–§D land and the completion shape is settled**, promote the
reusable mise-integration pattern (pin env-redirect fragments + completion via a
`__complete` backend, usage/`complete`, and the global-vs-project registration
rules — captured in the agent memory `mise-integration-pattern`) into the shared
Go CLI standard so sibling tools inherit it. Reflect it in **three places**, all
sourced from chezmoi (`~/.local/share/chezmoi`):

1. **CLI standard docs** — `docs/go-cli/` and `docs/go-cli-architecture.md`.
2. **go-cli-tooling skill** — `dot_agents/skills/go-cli-tooling/` (the canonical
   source; it symlinks into `~/.claude` and `~/.agents`, and this repo's bundled
   `.claude/skills/go-cli-tooling/` re-syncs from it).
3. **Templates** — the relevant `chezmoi_templates/` / `dot_*` `.tmpl` files,
   then `chezmoi apply`.

This is a separate work item after v0.8.4 ships (not part of the kae release
itself); listed here so it is not lost.

## Non-Goals (this release)

- "Did you mean X?" unknown-command suggestion — stays a separate ROADMAP
  candidate.
- A completion-framework dependency (cobra / carapace / `jdx/usage`): kae stays
  hand-rolled and dependency-minimal; the `__complete` backend reproduces the
  dynamic-completion pattern natively.
- TUI, Windows, remote share-list shipping — see [ROADMAP.md](ROADMAP.md).
- Any JSON-contract break: `schema_version` stays `1`.

## Acceptance Criteria

- **backend**: `kae __complete commands|tools|profiles|accounts` prints the live
  candidates one per line; `accounts <tool>` scopes to that tool; an unknown
  kind exits non-zero; the subcommand is absent from `kae help`. Temp-HOME tests.
- **native completion**: a generated zsh/bash/fish script completes commands at
  word 1 and live profiles/accounts at the argument positions via
  `kae __complete`; `kae completion <shell>` still emits a valid script for each
  shell. `kae completion <shell> --install` writes to the chosen location, is
  idempotent, and never mutates the global mise config unless the user picks that
  option. Temp-HOME tests.
- **mise tasks**: `kae mise init` renders tasks whose `usage` / `complete`
  directives reference `kae __complete`; a generated `.mise.toml` parses and
  `mise run <task> <TAB>` resolves candidates on a real machine (smoke).
- **docs**: the non-mise registration paths and the mise opt-in are both
  documented; CLI / README / DATA-MODEL / ARCHITECTURE / VALIDATION current.
- `mise run check` passes; no new entry in `go.mod`; the JSON contract is
  unchanged.

## Release Steps

1. Bump `toolVersion` to v0.8.4.
2. §A `kae __complete` backend (hidden subcommand); temp-HOME tests.
3. §B native completion on the backend + interactive `--install`; temp-HOME
   tests.
4. §C `kae mise init` task `usage` / `complete` generation; temp-HOME tests.
5. §D docs (CLI / README / DATA-MODEL / ARCHITECTURE / VALIDATION; both
   audiences).
6. Real-machine smoke: register completion in each shell, confirm `kae <TAB>`
   and `mise run <task> <TAB>` resolve live candidates; tag `v0.8.4`, GitHub
   release.

---

# kae v0.8.3 (released 2026-06-17)

Lift the two discovery-blocked items, consolidate per-tool credential knowledge
onto the adapter registry, and make the detected login identity visible. The
real-machine discovery for both deferred items is done (2026-06-16; contracts in
[ADAPTERS.md](ADAPTERS.md)), so the scope is de-risked: §A
freshness-as-adapter-capability, §B cursor `kae add` identity, §C codex keyring
driver, §D store + display the detected account identity. No JSON-contract break
(`schema_version` stays `1`; new tokens are additive).

Shipped 2026-06-17. The cursor identity real-machine gate passed; the **codex
keyring two-account real-keychain gate was deferred** (the driver is covered by
fake-`security` round-trip tests) and stays the one open acceptance item — run
it before relying on the keyring driver in production
([VALIDATION.md](VALIDATION.md)).

Previous baseline: v0.8.2 (daily-use polish).

## Scope

### A. Freshness as an adapter capability

Move `freshness.Inspect`'s per-tool `switch tool` onto a per-tool adapter
`Freshness(payload) Info` method (an optional capability, beside `Identifier`),
so per-tool credential knowledge has one home (the registry). The shared
`jwtExpiry`/`epochToTime`/`decodeObject` and `internal/jwt` primitives stay in
`internal/freshness`; `freshness.Inspect` becomes a thin dispatch to the adapter
(or `cmd.accountFreshness` consults the adapter directly). A tool with no
datable credential (copilot pointer, agy blob) returns `Known=false`; a tool
that ships without a `Freshness` method stays fail-safe (not-datable). Pure
refactor — the existing freshness / doctor / stale-warning tests pass unchanged.

### B. `kae add` account identity for cursor

Implement cursor's `adapter.Identifier` via `cursor-agent status` (discovery
2026-06-16: a single line `✓ Logged in as <email>`, UTF-8 check glyph, **no
ANSI**, exit 0). Run it through the runner seam; extract the text after
`Logged in as `, trim, and let `cmd` sanitize the email to a local-part account
name (the v0.8.2 §B path). A non-matching line, a non-zero exit, or an empty
identity is a detection failure naming the explicit form. Fake-runner tests
cover the logged-in and logged-out / garbled cases. (cursor-agent status may
hit the network; acceptable on the interactive `kae add` path.)

### C. Codex keyring driver

Lift the codex `codex-keyring` driver from detect-only (the v0.8.1 §E / v0.8.2
deferral). Discovered contract (2026-06-16): the OS-keychain item is service
`Codex Auth`, account `cli|<opaque>` (an opaque per-login id — **captured
verbatim, never computed** by kae), and the payload is the whole `auth.json`
JSON (`tokens`, `OPENAI_API_KEY`, `auth_mode`, `last_refresh`). kae treats it
with the existing verbatim-keychain pattern (as claude / cursor): capture reads
the single live `Codex Auth` item's account + payload; apply writes them back
verbatim through `security`. Structure guard: the payload must parse as a JSON
object containing `tokens`. The keychain account is carried in the snapshot
(like cursor's `keychain_account`) so apply recreates the right item.

Open design point to settle during implementation, with a two-account real
keychain round-trip: whether codex matches by service only or service+account.
If service+account, apply deletes the existing `Codex Auth` item before adding
the target's (so exactly one active item exists); if service-only, an `add -U`
replace suffices. The detect-only refusal (exit 10) is replaced by the working
driver; `auto` store with neither `auth.json` nor a keyring item stays "not
logged in".

> **Settled 2026-07-30, and the guess above was wrong in the dangerous
> direction.** codex matches by **service + account**, the account is *derived*
> from `CODEX_HOME` (never opaque, never captured), and one service holds one item
> per codex home — so the delete-before-add this section chose deleted another
> home's login. Current contract: [ADAPTERS.md](ADAPTERS.md) § "Keyring item
> contract"; the assumption row and its login-free verification:
> [VALIDATION.md](VALIDATION.md) § "Upstream Behaviour Assumptions".

### D. Store and display the detected account identity

Today auto-detection (§B v0.8.2) reads the live login identity only to derive
the account name, then discards it — so the snapshot keeps the sanitized name
(`alice`) but not the real identity (`alice@example.com`, or a codex
`account_id`). Persist it: at capture (`kae add`, **both** the explicit-name and
auto-detect forms), best-effort call the adapter's `Identity` and record the raw
detected value in the snapshot. This builds on §B (the `Identifier` capability
for every tool, including cursor).

- `account.toml` gains an optional `identity` field (the raw detected identity),
  separate from the account name. Backfilled only on a fresh `kae add`; absent
  for pre-existing snapshots and unaffected accounts.
- `kae ls` and `kae accounts` show an `Identity` column (blank when absent); the
  `--json` account rows gain an additive `identity` field (`schema_version`
  stays `1`, `omitempty`).
- Best-effort: a tool with no `Identifier` (agy), or a detection failure, leaves
  `identity` empty and never errors — the account name is unaffected.
- The identity (an email or account id) is PII but **not** a secret credential;
  it is stored in plaintext metadata exactly like the account name and never a
  token (SECURITY.md note; no redaction-test change beyond confirming no token
  leaks). It disambiguates accounts whose identities sanitize to the same name.

## Non-Goals (this release)

- TUI, Windows, remote share-list shipping, `env export --reveal`, "did you
  mean" suggestions — see [ROADMAP.md](ROADMAP.md).
- Any JSON-contract break: `schema_version` stays `1`.

## Acceptance Criteria

- **freshness capability**: the existing switch / login / doctor / stale-warning
  tests pass unchanged; per-tool expiry/refresh logic lives on the adapters and
  the primitives in `internal/freshness`; a tool with no `Freshness` method is
  treated as not-datable (`Known=false`).
- **cursor identity**: `kae add cursor` (no name) on a live `cursor-agent
  status` login captures under the sanitized detected email; a logged-out or
  unparseable status errors naming the explicit form (fake-runner tests).
- **codex keyring**: with `cli_auth_credentials_store = "keyring"`,
  `kae add codex` / `kae use codex <account>` round-trip through the `Codex
  Auth` keychain item and a fresh-process `codex login status` reports logged
  in; the detect-only refusal is gone. Two-account real-machine gate recorded in
  VALIDATION.md.
- **identity store/display**: `kae add claude` (auto-detect) and `kae add claude
  <name>` (explicit) both record the detected `identity` in `account.toml`;
  `kae ls` / `kae accounts` show it; `--json` carries an additive `identity`
  (`schema_version` still `1`). agy (no `Identifier`) and a detection failure
  leave it empty without erroring. Temp-HOME tests.
- `mise run check` passes; JSON keeps `schema_version: 1`, stable tokens, `[]`
  arrays; redaction tests cover any new output path (the keyring payload is a
  credential and must never reach stdout/JSON/logs/metadata).

## Release Steps

1. Bump `toolVersion` to v0.8.3.
2. §A freshness-as-capability refactor; existing tests green; temp-HOME tests.
3. §B cursor `Identifier` via `cursor-agent status`; fake-runner tests.
4. §D store + display the detected identity (capture records it for every tool;
   `kae ls`/`accounts` + `--json` show it); builds on §B; temp-HOME tests.
5. §C codex keyring driver (verbatim `Codex Auth` item) with a fake `security`
   runner; structure guard; temp-HOME tests.
6. Docs (ADAPTERS / DATA-MODEL / CLI / ARCHITECTURE / SECURITY / README /
   VALIDATION).
7. Real-machine gate: codex keyring two-account round-trip + cursor `kae add`
   identity on a live login; README verified; tag `v0.8.3`, GitHub release.

---

# kae v0.8.2 (released 2026-06-16)

Daily-use polish: make the most-run command fast, the most-typed command
shorter, and pay down the freshness debt v0.8.1 left. No JSON-contract break
(`schema_version` stays `1`; new tokens are additive). The codex keyring driver
(v0.8.1 §E) stays deferred — it is discovery-blocked, not patch-shaped.

Previous baseline: v0.8.1 (credential freshness / auto-recapture).

## Scope

### A. `status` speed + switch-time double read

- **Concurrent `Detect` in `status`**: today `kae status` probes each enabled
  tool's live state sequentially; on macOS each claude/cursor `Detect` is a
  `security` call, so the most-run command pays the sum. Run the per-tool
  `Detect` concurrently and reassemble results in canonical `constants.Tools`
  order (output unchanged). Bound to the enabled-tool count; failures stay
  per-tool, not fatal.
- **Coalesce the switch-time snapshot read**: `buildSwitch` reads each target's
  snapshot payload twice from kae's own secret store — once for the §B stale
  warning (`accountFreshness`) and again in `applySnapshot`. The v0.8.1
  `keychain.WithReadCache` covers the **upstream** tool keychain, not kae's own
  `secret.Backend`. Add the same context-scoped read-cache shape to
  `internal/secret` and wire it into the switch so the snapshot is read once.
  Writes (`Set`/`Delete`) invalidate the key; never cached across a child run.

### B. `kae add` account-name auto-detection (default; explicit still works)

- **`kae add <tool>`** (account omitted): detect the live login identity, derive
  a sanitized account name, and capture under it — the new default. Detection is
  a per-tool adapter capability `Identity(ctx, env) (string, error)`: claude →
  `oauthAccount.emailAddress` (from `~/.claude.json`), codex → the `id_token`
  email claim or `account_id` in `auth.json`, opencode → `accountId`, copilot →
  `lastLoggedInUser.login`. **cursor is deferred** — its `cursor-agent status`
  output is undocumented (discovery-blocked, like the codex keyring item), so
  cursor requires an explicit name until a real-machine discovery; see
  [ROADMAP.md](ROADMAP.md). The raw identity is sanitized to `[a-zA-Z0-9._-]`
  (email → local part before `@`), capped at 64.
- **`kae add <tool> <account>`** (explicit): unchanged — the given name wins.
- Works on both the login flow and `--no-login` (detect from the post-login /
  current live state). Detection failure (no identity exposed, or it sanitizes
  to empty) is an error naming the explicit form, not a silent fallback. agy has
  no `Identity` (add unsupported), so it always requires an explicit name where
  applicable.

### C. `kae ls`

- A single mise-style listing of accounts **and** profiles, today split across
  `kae accounts` and `kae status`. Table-driven from `constants.Tools` +
  captured accounts + config profiles; active markers; stable `--json`
  (`schema_version: 1`). Read-only; no new state.

### D. v0.8.1 freshness hardening

- **Two-account real-machine recapture**: extend `docs/VALIDATION.md` with a
  real-keychain gate that captures two accounts and verifies a refreshed token
  round-trips on switch-away (the v0.8.1 gate covered the file-driver logic and
  the single-account real-keychain round-trip only).
- **Shared live↔snapshot comparator**: `freshness.go`'s `valuesDiverge` and
  `login.go`'s `loginChangedAuth` implement the same "compare live values to a
  stored snapshot" loop with different error policies. Extract one comparator
  parameterized on the policy so the rule lives in one place.
- **(splittable) Freshness as an adapter capability** — **split to v0.8.3**:
  moving `freshness.Inspect`'s `switch tool` into a per-tool adapter
  `Freshness(payload) Info` method touches all six adapters plus the interface,
  which grows this patch past its daily-use-polish scope. Deferred per the
  splittable note; the shared `jwtExpiry`/`epochToTime`/`decodeObject`
  primitives stay in `internal/freshness` (see [ROADMAP.md](ROADMAP.md)).

## Non-Goals (this release)

- Codex keyring driver (v0.8.1 §E) — still discovery-blocked (see ROADMAP.md).
- TUI, Windows, remote share-list shipping, `env export --reveal`,
  "did you mean" suggestions — see [ROADMAP.md](ROADMAP.md).
- Any JSON-contract break: `schema_version` stays `1`.

## Acceptance Criteria

- **status**: `kae status --json` output is byte-identical to the sequential
  version (same tool order, fields, `[]` arrays); the per-tool `Detect` runs
  concurrently (asserted via the runner seam — overlapping calls, or a count
  proving no serialization). A single tool's `Detect` failure does not abort the
  report.
- **switch read**: a single `kae use` reads each target snapshot payload from the
  secret backend once (asserted via the backend seam call count); the switch
  result is unchanged.
- **add auto-detect**: `kae add --no-login claude` (no name) on a live login
  captures an account whose name is the sanitized detected identity; `kae add
  --no-login claude <name>` still uses `<name>`; a tool with no detectable
  identity errors naming the explicit form. Temp-HOME tests with fixture
  identities.
- **ls**: `kae ls` lists every captured account and every profile with active
  markers; `kae ls --json` keeps `schema_version: 1` and `[]` arrays.
- **hardening**: the shared comparator passes the existing switch/login tests
  unchanged; the two-account real-machine gate is recorded in VALIDATION.md.
- `mise run check` passes; redaction tests cover any new output path (no token
  or identity-derived secret in output beyond the sanitized account name).

## Release Steps

1. Bump `toolVersion` to v0.8.2.
2. §A `status` concurrency + the `secret.Backend` read cache; temp-HOME tests.
3. §B adapter `Identity` + `kae add` auto-detect default; temp-HOME tests.
4. §C `kae ls`; temp-HOME tests.
5. §D shared comparator + (splittable) freshness-as-capability; temp-HOME tests.
6. Docs (CLI/ARCHITECTURE/ADAPTERS/DATA-MODEL/README/VALIDATION).
7. Real-machine gate (two-account recapture); README verified; tag `v0.8.2`.

---

# kae v0.8.1 (released 2026-06-16)

Credential freshness. Every supported tool authenticates with a refreshable
OAuth/JWT credential, but `kae use` (and bare `use`) write the **capture-time**
snapshot back to the live store with no recapture — only `run -s` recaptures
(via `runAuthTransaction`'s post-child `captureSnapshot`). So a token rotated
outside kae (a re-login in the tool, a long-unused account) leaves the snapshot
stale, and a switch back to it can break auth — dropping to a login prompt when
the refresh token has also rotated (observed in the v0.8.0 real-machine gate,
[VALIDATION.md](VALIDATION.md)). v0.8.1 closes this gap symmetrically with
`run`, surfaces staleness it cannot self-heal, and pays down the per-switch
keychain cost the recapture adds.

Previous baseline: v0.8.0 (surface vocabulary unification).

## Scope

### A. Switch-source auto-recapture (`use` / bare `use`)

Before `kae use` / bare `use` switches away, recapture the **currently active**
account's live credential into its snapshot — the `run -s` mechanism made
symmetric for `use` — so the next switch back applies a live token. Only
`use`/bare `use` overwrite the **real** store and need this; `use -i` /
`pin -s|-i` / `rebind` / `run -i` write kae-owned isolation dirs (live store
untouched), so they stay as-is. Recapture **only when the live store and the
snapshot diverge**, to avoid a needless keychain read on every switch.

### B. Switch-time stale warning + recovery path

The account being switched **to** may be stale and is not live, so it cannot be
recaptured. At switch time, detect an expired snapshot (`expiresAt` past, or
divergence from the live store) and: proceed when the refresh token is still
usable (the tool self-refreshes), otherwise warn and name `kae add` to
re-capture. Share the staleness predicate with §D.

### C. `security` subprocess coalescing (macOS)

Recapture adds a keychain read per switch, each a `security` invocation (and a
possible auth dialog). Coalesce/cache the multiple `security` calls per switch
so the recapture does not multiply prompts; this is the practical prerequisite
for §A. (Also the v0.7.x "performance polish" backlog item.)

### D. `doctor` credential-health

Surface staleness the switch path only warns about inline: a `doctor`
stale-snapshot check (expired `expiresAt` / divergence from the live store),
reusing §B's predicate. Fold in the v0.7.1-deferred keychain-orphan check where
enumeration is reliable (file backend `readdir`, Linux `libsecret`); the darwin
keychain cannot enumerate by service, so it stays a documented gap there.

### E. Codex keyring driver — **deferred to v0.8.2**

Lifting the codex `codex-keyring` driver from detect-only requires pinning down
the OS credential-store item contract used by
`cli_auth_credentials_store = "keyring"`, which upstream does not document. A
round-trip cannot be implemented safely without first discovering the item's
service/account naming on a real machine with a live codex keyring login —
guessing it would violate the structure-guard rule (refuse unknown layouts,
never best-effort write; [ADAPTERS.md](ADAPTERS.md)). Per the splittable note,
**§E is deferred to v0.8.2** and A–D ship as v0.8.1. The deferral and its reason
are recorded in [ROADMAP.md](ROADMAP.md); the detect-only refusal (exit 10 with
guidance) is unchanged.

## Non-Goals (this release)

- **Tracking rotation that happens entirely outside kae** — a re-login in the
  tool can rotate the refresh token with no kae involvement; v0.8.1 warns
  (§B/§D) rather than silently repairing.
- TUI, Windows, remote share-list shipping, `env export --reveal` — see
  [ROADMAP.md](ROADMAP.md).

## Acceptance Criteria

- **recapture**: after `kae use A` → `kae use B` → `kae use A`, the credential
  re-applied for A is the one live at the first switch-away, not the original
  capture (temp-HOME test simulating a token refresh while A was active);
  recapture is skipped (no keychain read) when live and snapshot already match.
- **stale warning**: a switch to an account whose snapshot `expiresAt` is past
  warns and names `kae add`; with a usable refresh token it still proceeds.
- **coalescing**: a single `use` performs at most one `security` read per
  keychain **item** (service + account) for the recapture decision (asserted via
  the runner seam call count). Per *tool* until cursor gained a three-item
  credential set: the cache is keyed by item, so a tool with three services costs
  three reads — and possibly three keychain prompts — not one.
- **doctor**: a stale snapshot produces a `credential_stale` warn-level check;
  the JSON report keeps `schema_version: 1`; file-backend orphans are detected.
- **codex keyring** (if kept): `kae add`/`use` round-trip through the keyring
  store passes a fresh-process auth check; otherwise the item is deferred with
  the reason recorded.
- `mise run check` passes; JSON keeps stable tokens and `[]` arrays; redaction
  tests cover any new output path (no token value in warnings/doctor output).

## Release Steps

1. Bump `toolVersion` to v0.8.1.
2. §C `security` coalescing first (prerequisite), then §A recapture + §B
   switch-time warning (shared predicate); temp-HOME tests.
3. §D `doctor` credential-health on the shared predicate; temp-HOME tests.
4. §E codex keyring driver — **deferred to v0.8.2** (undocumented keyring item
   contract; needs real-machine discovery, reason recorded in ROADMAP.md).
5. Docs (CLI/ADAPTERS/DATA-MODEL/SECURITY/README); temp-HOME tests.
6. Real-machine gate — **re-capture a live token immediately before the gate and
   use a throwaway account** (the teardown rewrites the live keychain from the
   snapshot; see VALIDATION.md). Confirm a switch-back applies a live token and
   the stale warning fires on an expired snapshot.
7. README verified against the binary; tag `v0.8.1`, GitHub release.

---

# kae v0.8.0 (released 2026-06-16)

Finish the scope×environment vocabulary: one surface, one set of names. v0.7.2
unified `use`/`pin` on `-s`/`-i`; v0.8.0 folds `apply` into `use`, redesigns
`run` onto `-s`/`-i`/`--env`, removes the mechanism-vocabulary leak from
`mise init` and the config keys, and adds input ergonomics (tool-name prefixes,
shell completion). **Pre-1.0 breaking release**: the `run --mode` flag and the
`bond_`/`pin_`/`overlay_`/`home_` config keys are removed outright — no alias,
just a migration note.

Previous baseline: v0.7.2 (use/pin × -s/-i, global isolated home).

## Scope

### A. `apply` folds into `use`

`apply` is not merely `use -s`; it adds idempotency, profile resolution, and a
quiet mode. Fold those into `use` and remove the verb:

- **bare `kae use`** (no positional) resolves the profile (`$KAE_PROFILE`, then
  `default_profile`, then `-P <name>`) and applies it **idempotently** — no-op
  (exit `0`, no lock, no backup) when `state.json` `active` already matches, like
  today's `apply`. `--quiet` suppresses the success report; JSON keeps `changed`.
- **`kae use <profile | tool account>`** (explicit positional) keeps the forced
  switch + backup (unchanged).
- **`apply`** becomes a one-release removed-command pointer (exit `64`) naming
  `kae use [--quiet]`.
- `mise init --auto`'s enter hook script becomes `kae use --quiet`.

### B. `run` redesign (`-s` / `-i` / `--env`; `--mode` removed)

Six modes collapse to three; `--mode` is removed (hard break):

- **`run -s`** (default): the child sees the **real home** (= old `auth`:
  backup → apply → run → recapture refreshed creds → restore). The per-tool lock
  is held for the whole child run.
- **`run -i`**: an **isolated home**, reusing the global-isolated store
  `isolation/global/<tool>/<account>` (shared with `kae use -i`); no lock, no
  live mutation. This is the right tool for **interactive sessions** under
  another account — concurrent `kae use` in other terminals is never blocked and
  never seen by the isolated process.
- **`run --env`**: inject the env-profile vars (old `--mode env`); no home
  redirect, no lock.
- **Removed**: `--mode` and the `auth|env|home|overlay|bond|pin` values. `home`
  folds into `-i`; `overlay` is retired; per-directory `bond`/`pin` via `run` is
  gone — a `kae pin`-ed directory already redirects the tool through its mise
  fragment, so `run` is unnecessary there.
- **Confusion guard** (`run -i` shares a store with `use -i`): `run -i` prints
  the exact home and that it is shared with `kae use -i <account>`, and
  `kae status` surfaces the global-isolated homes (§D), so the shared state is
  never invisible. Docs state the three isolation scopes plainly: global
  (`use -i` / `run -i` share one home per account), per-directory (`pin`).

### C. `mise init` trim

- Drop `--mode bond|pin` (the per-directory binding is `kae pin -s|-i`, which
  owns the fragment). Keep `--mode auth` (tasks + the opt-in enter hook, now
  emitting `kae use --quiet`); `home`/`overlay` rendering is removed with the
  `run` modes.

### D. Mechanism + config-key rename (breaking, no alias)

With `run` no longer exposing the mechanism strings, the vocabulary moves
cleanly to `shared`/`isolated`:

- internal: `modeBond`/`modePin` → `modeShared`/`modeIsolated`; retire
  `modeOverlay`/`modeHome`.
- config keys: `bond_denylist_extra` → `shared_denylist_extra`;
  `pin_shared_items` → `isolated_shared_items`; remove
  `overlay_extra_shared` / `overlay_mode_enabled` / `home_mode_enabled`. Old keys
  are **not** accepted — config load errors naming the new key (migration note in
  the release).
- `kae status` reports the global-isolated (`synced`) homes so `use -i` / `run
  -i` state is visible (also the §B confusion guard).

### E. `-i` with a profile mapping unsupported tools

- `use -i` / `run -i` for a **profile** that includes a tool with no isolation
  env var (agy, opencode, cursor, copilot) **skips it with a warning** and
  isolates claude/codex only, instead of exiting `5`. A single-tool
  `kae use -i agy <account>` still exits `5`. (Fixes the shipped `use -i`
  behavior too.)

### F. Input ergonomics

- **Tool-name prefix aliases** in tool positions (`cl`→claude, `cod`→codex,
  `cu`→cursor, `cop`→copilot, `o`→opencode, `a`→agy); ambiguous prefixes (`c`,
  `co`) error with the candidate list. Input-only (resolved to the canonical
  name, never stored); the ambiguity set is computed from `constants.Tools`.
- **`kae completion <bash|zsh|fish>`** generator, table-driven from the router +
  `constants.Tools` + config (profiles/accounts).
- **`-P`** short form for `--profile` on `run` / bare `use` / `mise init`.

## Non-Goals (this release)

- **Alias / transition window** for `--mode` or the renamed config keys — pre-1.0
  hard break with a migration note.
- TUI, Windows, Codex keyring driver, agy home isolation, remote share-list
  shipping, doctor orphan enumeration — see [ROADMAP.md](ROADMAP.md).
- "Did you mean X?" unknown-command suggestion — may ride along but not required.

## Acceptance Criteria

- **apply fold**: bare `kae use` (resolved profile) is idempotent (no-op when
  active, no lock, no backup); `kae use --quiet` is silent; JSON keeps
  `changed`; `apply` exits `64` naming `kae use`.
- **run**: `kae run -i claude <acct> -- claude` runs in
  `isolation/global/claude/<acct>` with no lock and no live mutation, and a
  concurrent `kae use` in another shell is not blocked; `run -s` holds the lock
  and restores the previous login; `run --env` injects only the profile vars;
  `run --mode …` exits usage (removed). `run -i` output names the shared home.
- **mise init**: `--mode bond|pin` rejected; `--mode auth` renders the
  `kae use --quiet` enter hook.
- **rename**: a config with `bond_denylist_extra` / `pin_shared_items` fails at
  load naming the new keys; `shared_denylist_extra` / `isolated_shared_items`
  work; `kae status` shows global-isolated homes.
- **profile skip**: `kae use -i <profile-including-agy>` isolates claude/codex,
  warns on agy, exits `0`; `kae use -i agy <account>` exits `5`.
- **ergonomics**: unambiguous tool prefixes resolve and ambiguous ones error with
  candidates; `kae completion zsh` emits a script; `-P <profile>` resolves.
- `mise run check` passes; JSON keeps `schema_version: 1`, stable tokens, `[]`
  arrays; redaction tests cover any new output path.

## Release Steps

1. Bump `toolVersion` to v0.8.0.
2. Fold `apply` into bare `kae use` (idempotent + `--quiet`); update the enter
   hook; `apply` tombstone; temp-HOME tests.
3. Redesign `run` (`-s`/`-i`/`--env`, `--mode` removed); `run -i` on
   `isolation/global`; surface `synced` in `kae status`; temp-HOME tests.
4. Trim `mise init` (drop bond/pin; hook → `kae use --quiet`).
5. Mechanism + config-key rename (hard break) with the migration note; retire
   overlay/home and their config keys.
6. Input ergonomics (tool prefixes, `kae completion`, `-P`); `-i` profile
   skip+warning.
7. Docs fold (CLI/DESIGN/ADAPTERS/DATA-MODEL/SECURITY/README); temp-HOME tests;
   real-machine gate (`run -i` interactive AUTH-OK, concurrent `use` not blocked).
8. README verified against the binary; tag `v0.8.0`, GitHub release.

---

# kae v0.7.2 (released 2026-06-16)

Unify the switching surface and ship the last cell of the scope×environment
model (global isolated).

Four switching behaviors collapse into **two verbs by scope** plus **two flags
by environment**, so the model reads as one grid instead of four unrelated
verbs:

|                              | `--shared` / `-s` (default)                                               | `--isolated` / `-i`                                                       |
|------------------------------|---------------------------------------------------------------------------|---------------------------------------------------------------------------|
| **`kae use`** / `u` — global  | switch every terminal's account in place, real home shared (v0.7.1 `auth`)| point every terminal at a per-account private home via a kae-owned global mise fragment (NEW) |
| **`kae pin`** / `p` — per-dir | bind this dir: settings/sessions shared, credential private (v0.7.1 `bond`)| bind this dir: fully isolated, opt-in shares (v0.7.1 `pin`)               |

Both verbs accept `<profile>` or `<tool> <account>`. `-i`/`-s` are short forms
of `--isolated`/`--shared`. Defaults: `use` shared (the everyday global
switch), `pin` shared (the common per-directory case). This is a pre-1.0
surface change with no released users of the affected verbs; the old verbs
become one-release removed-command pointers.

Previous baseline: v0.7.1 (file-driver override, `kae account rm`/`rename`,
`kae profile`, comment-preserving config writer; see git tag v0.7.1).

## Scope

### A. Surface unification (`use`/`pin` × `-s`/`-i`)

- **`use`/`pin` gain `--shared`/`-s` and `--isolated`/`-i`** (`internal/cmd`),
  selecting the environment. `use` defaults to shared, `pin` to shared.
- **Aliases**: `u` = `use` (already), `p` = `pin` (new route in `Root()`).
- **`bond` → `pin --shared`**: `bond` becomes a removed-command pointer (exit
  `64`, one release) naming `kae pin --shared`. The per-directory shared
  mechanism (symlink-everything-but-credential) is unchanged; only the surface
  moves under `pin -s`.
- **`as` removed**: changing one tool's account inside a bound directory is now
  `kae pin <tool> <account>` (re-binds that tool only, leaving the others and
  the sharing set intact). `as` becomes a removed-command pointer (exit `64`,
  one release) naming `kae pin <tool> <account>`.
- **`--global` flag removed**: `use` is inherently global, so it always resolves
  the real home (it auto-applies what `--global` used to do — hide kae-managed
  isolation env vars). Inside a pinned directory `use` no longer refuses (the
  v0.6.0 exit `5` guard is gone); it prints a one-line warning — "this directory
  is pinned; you are changing GLOBAL state, which this directory will not see —
  re-bind with `kae pin`" — and proceeds.

### B. Isolation via kae-owned mise fragments (the real home and `mise.toml` are never touched)

Both isolated environments set `CLAUDE_CONFIG_DIR` / `CODEX_HOME` through a
**generated, kae-owned mise fragment** at `.config/mise/conf.d/kagikae.toml`,
which mise loads and merges (a project fragment overrides the global one, so
`pin` wins over `use -i` inside a bound directory). kae **never reads or writes
the user's `mise.toml`** and never mutates the real `~/.claude` / `~/.codex`;
the fragment is regenerated from kae state, and teardown just deletes it.

- **global** (`use -i`): `~/.config/mise/conf.d/kagikae.toml`, regenerated from
  `state.json` `synced` (tool→account).
- **per-directory** (`pin`): `./.config/mise/conf.d/kagikae.toml` in the
  project, carrying the tool env entries, `KAE_PROFILE`, and (for shared) the
  bound account.
- kae creates `.config/mise/conf.d/` if absent and **adds the project fragment
  to `.gitignore`** (it holds machine-specific absolute paths and account names
  that must not be committed); the file self-documents in a header comment.
- **Requires mise activation** for the scope (global activation for `use -i`;
  the usual project activation for `pin`). When kae cannot confirm activation it
  warns and prints the `export …` line for the current shell.
- **`kae unpin`** deletes the project fragment. **Migration**: directories
  pinned before v0.7.2 carry a `# >>> kagikae` marker block inside `mise.toml`;
  there is no auto-migration — re-run `kae unpin && kae pin` once per directory.

### C. Global isolated home (`use --isolated`) — claude/codex only

- Prepare `isolation/global/<tool>/<account>/` as a full per-account private
  home (materialize the credential); the global fragment points the tool there.
  claude and codex only (others exit `5`). On macOS `CLAUDE_CONFIG_DIR` makes
  claude read the file credential, not the keychain (proven in the v0.7.0 gate),
  so the real login keychain is never touched.
- **Teardown is `use --shared`** (or bare `kae use`): drop the tool from
  `synced`, regenerate (or delete) the global fragment, then switch the real
  home in place. `-i`/`-s` toggle the global environment; no `unsync` verb, no
  backups, no restore.

### D. Per-directory account changes and status

- **`kae pin <tool> <account>`** re-binds one tool (regenerate the project
  fragment's entry for that tool); `KAE_PROFILE` recomputed (ad-hoc when no
  profile matches).
- **`status` reports the real per-tool account**, not the `KAE_PROFILE` label.
  Shared dirs record the account in the fragment so it survives re-entry; the
  isolated path already encodes the account.

### E. Data path renames (clarity)

- global isolated home `synchomes/<tool>/<account>/` →
  **`isolation/global/<tool>/<account>/`** (`synchomes` named the removed `sync`
  verb). Not shipped yet — a free rename.
- per-dir mechanism segments renamed for clarity: `…/<tool>/bond/` →
  **`…/<tool>/shared/`**; `…/<tool>/pin/<account>/…` →
  **`…/<tool>/isolated/<account>/…`**. The v0.7.1 stores under the old names are
  abandoned in place; a one-time `kae unpin && kae pin` re-creates them under the
  new names (no auto-migration).

## Non-Goals (this release)

- **`apply` / `run` redesign** — `apply` stays the idempotent hook form of the
  global shared switch; `run --mode` keeps its current mode values. Folding them
  into the `-s`/`-i` vocabulary is deferred ([ROADMAP.md](ROADMAP.md)).
- **Live bidirectional sync / watcher daemon** — `use -i` is a *switch* of which
  private home is live, not a sync engine. The §6 finding (claude self-heals
  `/oauthAccount` from the token) means no copy+patch is needed; a resident
  watcher conflicts with the CLI-only design ([SCOPE-MODEL.md](SCOPE-MODEL.md)).
- **Renaming `run --mode` values** — `run --mode bond|pin|home|overlay` keeps
  its names even though the per-directory data paths are renamed to
  `shared`/`isolated`; aligning `run`'s vocabulary is deferred with the rest of
  the `apply`/`run` review ([ROADMAP.md](ROADMAP.md)).
- **Tools without a redirectable home** (agy, opencode, cursor, copilot) —
  global shared (`use`) and `run --mode env` only, unchanged.
- TUI, Windows, Codex keyring driver — see [ROADMAP.md](ROADMAP.md).
- No automatic network access.

## Acceptance Criteria

- **surface**: `kae u -i <acct>`, `kae u -s <acct>`, `kae p -i <acct>`,
  `kae p -s <acct>` each select the right scope×environment; bare `use`/`pin`
  default to shared; `u`/`p` aliases resolve. `bond`/`as` print exit-`64`
  pointers to `pin --shared` / `pin <tool> <account>`. `--global` is gone;
  `use` inside a pinned dir warns and switches global state.
- **isolation fragments**: `kae pin` writes `./.config/mise/conf.d/kagikae.toml`
  and `kae use -i` writes `~/.config/mise/conf.d/kagikae.toml`; the user's
  `mise.toml` and the real `~/.claude` / `~/.codex` are never modified. The
  project fragment is added to `.gitignore`. `kae unpin` deletes the project
  fragment; `kae use -s` drops the tool from `synced` and regenerates/deletes the
  global fragment (temp-HOME tests).
- **global-isolated real-machine gate** (required before merge): on a staging
  machine with global mise active, `kae use -i <account>` makes a fresh-process
  `claude -p '' --model haiku` run as that account's private home and return
  AUTH-OK; the real login keychain is not polluted (file-driver path). `kae use
  -s` returns the shell to the real home. Recorded in
  [VALIDATION.md](VALIDATION.md).
- **per-dir re-bind**: `kae pin claude <other>` in a bound dir changes only
  claude; `KAE_PROFILE` drops to ad-hoc when the combination matches no profile;
  `kae status` shows the real per-tool account. A shared dir's active account
  survives re-entry (recorded in the fragment).
- **paths**: stores resolve under `isolation/global/<tool>/<account>/` and
  `isolation/<pin-id>/<tool>/{shared,isolated/<account>}/…`.
- **mise activation**: with mise not active, `use -i` / `pin` warn and print the
  `export` line and exit `0`.
- **unsupported tools**: `kae use -i agy <account>` (and opencode/cursor/
  copilot) exits `5`.
- `mise run check` passes; JSON keeps `schema_version: 1`, stable tokens, `[]`
  arrays; redaction tests cover any new output path.

## Release Steps

1. Land the surface unification: `-s`/`-i` flags, `p` alias, `pin <tool>
   <account>` re-bind, `bond`/`as` pointers, `--global` removal + pinned-dir
   warning; temp-HOME tests green. Bump `toolVersion` to v0.7.2.
2. Move per-dir isolation to a kae-owned project fragment
   (`./.config/mise/conf.d/kagikae.toml`): replace the `mise.toml` marker-block
   renderer, add `.gitignore` handling, rename the data paths to
   `shared`/`isolated`, `unpin` deletes the fragment, `status` shows the real
   per-tool account; temp-HOME tests.
3. Land global isolated (`use -i`): prepare `isolation/global/<tool>/<account>/`,
   regenerate `~/.config/mise/conf.d/kagikae.toml` from `synced`, and the
   `use -s` teardown; mise-activation warning; temp-HOME tests.
4. Run the real-machine gate (global mise active); record in
   `docs/VALIDATION.md`.
5. Phase 7 docs fold-down: reduce `docs/SCOPE-MODEL.md` to rationale/history now
   that the whole model is implemented (or keep with a reason).
6. README examples verified against the built binary.
7. Tag `v0.7.2`, GitHub release.

---

# kae v0.7.1 (released 2026-06-15)

Operational safety and account/profile lifecycle. This release closes daily-use
gaps and de-risks the global-isolated `sync` mode landing in v0.7.2: a
file-driver override so smoke/container checks never touch the real login
keychain; a comment-preserving `config.toml` writer; account removal/rename plus
profile save/rm/set/unset so cleanup and reconfiguration no longer mean manual
keychain surgery or hand-editing TOML; and (discovery-gated) doctor detection of
orphaned keychain items.

Previous baseline: v0.7.0 (bond mode, credential-private per-directory
isolation, `/oauthAccount` removal, `kae pin` semantics flip, `kae as`; see
git tag v0.7.0).

## Scope

- **claude file-driver override** — on macOS the claude adapter resolves a
  keychain driver, which ignores a temp `$HOME`; that makes claude switch
  smoke checks unsafe outside Linux (they would touch the real login keychain).
  Add an explicit override that forces the file-patch driver (`.credentials.json`
  under `CLAUDE_CONFIG_DIR`) even on darwin. **Env var is the primary surface**:
  `KAE_CLAUDE_DRIVER=file` (new `constants.EnvKaeClaudeDriver`, following the
  existing `KAE_PROFILE` convention). The override is an ephemeral
  smoke/container escape hatch; persisting it in config would silently break a
  real macOS login (the live claude reads the keychain, not the file), so a
  per-tool config option (`[tools.claude]`, default off) is only a secondary,
  explicit opt-in. The override is read inside `claude` adapter's `driver(env)`
  and must apply on **both the capture (`kae add`) and apply (`kae use`)
  paths** — overriding only one side breaks the round-trip. With it set, the
  whole round-trip closes on files: no `security` subprocess, no real keychain
  access.
- **`kae account rm <tool> <account>`** — remove a captured account: delete the
  snapshot dir (`accounts/<tool>/<account>`) and every secret-backend item
  (`SecretRef(tool, account, artifact)` under service `kagikae`). Today this is
  manual two-step surgery (`rm -rf` the dir plus `security
  delete-generic-password`), error-prone because it touches the keychain by
  hand. Refuse to remove the **active** account with exit `10`
  (`ExitUnsafeRefused`; **not** `5`/`ExitUnsupported`, which is the OS-support
  code) unless `--force`, which also drops it from `state.json` `active` and
  recomputes the active profile. If any `[profiles]` entry references the
  account (`Profile.Accounts` is a tool→account map), the comment-preserving
  writer (below) **removes the offending `accounts.<tool>` key from each
  profile in the same transaction**, naming the touched profiles in the output —
  `account rm` no longer refuses on a profile reference (the v0.7.0
  dangling-reference trap is gone now that kae can surgically edit
  `config.toml`). Unknown account exits `7`
  (`ExitNotFound`). `--dry-run` prints the plan (including the profile edits)
  and writes nothing. Per-tool lock plus the config lock held throughout.
- **`kae account rename <tool> <old> <new>`** — rename a captured account.
  Secret-backend keys cannot be renamed in place, so copy-then-delete each
  item; move the snapshot dir and metadata; update `state.json` `active[tool]`
  if it pointed at `<old>`. Any `[profiles]` entry referencing `<old>` for
  `<tool>` is **rewritten to `<new>` by the comment-preserving writer (below) in
  the same transaction**, naming the updated profiles in the output — no refuse,
  no manual `kae edit`. Refuse with exit `10` if `<new>` already exists; unknown
  `<old>` exits `7`; sanitize the new name with the existing account-name rule.
  `--dry-run` prints the plan and writes nothing. Per-tool lock plus the config
  lock held throughout.
- **comment-preserving `config.toml` writer** (`internal/config`) — a surgical
  editor that applies key-level mutations (remove a
  `profiles.<name>.accounts.<tool>` entry, rewrite an account value, add or
  remove a whole `[profiles.<name>]` table, set/clear `default_profile`) while
  keeping the file's comments, field order, and unrelated keys intact. Today kae
  writes `config.toml` exactly once — from the `init` string template — and
  every later change is a manual `kae edit`; there is no round-trip writer, so
  this is new infrastructure. **Trap**: `BurntSushi/toml` (the current
  dependency) is Marshal/Unmarshal only and drops every comment on re-encode, so
  a decode-then-encode round-trip would silently strip the template's
  explanatory comments — the writer must do targeted text/AST edits instead.
  Atomic write via `patch.WriteFileAtomic` at `0600`, under the config lock.
  `account rm`/`rename` and every `kae profile` mutation route through it.
- **`kae profile save|set|unset|rm|default`** — manage `[profiles]` entries
  without hand-editing TOML (mirrors the existing `kae env set|unset|list`
  shape, and is the scriptable, validated counterpart to free-form `kae edit`).
  `save <name>` writes or overwrites profile `<name>` from the current
  `state.json` active accounts (snapshot what you are running now);
  `set <name> <tool> <account>` sets one `accounts.<tool>` mapping, creating the
  profile if absent; `unset <name> <tool>` drops one mapping, removing the now-
  empty profile entry if that was its last; `rm <name>` deletes the whole
  profile. The default profile is its own verb so it never collides with the
  per-mapping `set`/`unset`: `default <name>` points `default_profile` at an
  existing profile, bare `default` prints the current one, and
  `default --clear` empties it. Unknown account, tool, or profile exits `7`
  (`ExitNotFound`); the account is validated against the captured snapshots and
  sanitized with the existing account-name rule. `rm` (and an `unset` that
  empties the default) refuses to leave `default_profile` dangling: removing the
  default exits `10` (`ExitUnsafeRefused`) unless `--force`, which clears
  `default_profile`. `--dry-run` prints the plan and writes nothing. Every
  mutation goes through the comment-preserving writer (above) under the config
  lock.
- **doctor keychain-orphan detection (discovery-gated)** — warn when a
  `kagikae` secret item exists with no matching `accounts/<tool>/<account>`
  dir (a leftover from manual cleanup). **Discovery first**: confirm whether
  the secret store can *stably enumerate* all items under service `kagikae`
  (on darwin a single `find-generic-password -s kagikae` returns only the first
  match and `dump-keychain` is heavy/brittle; on Linux `secret-tool search`
  may enumerate cleanly). Record the finding in a discovery note; implement
  only where enumeration is reliable, otherwise defer with the reason written
  down. Scope this release to darwin + keychain backend; note Linux/libsecret
  as a follow-up. With `account rm` shipping in the same release, orphans
  become rare, so this is a nice-to-have, not a gate.

## Non-Goals (this release)

- **Phase 6 (`kae sync`, global isolated mode)** — the highest-risk mode
  (symlink-swaps the real `~/.claude`); deferred to **v0.7.2**. The file-driver
  override here is its safety prerequisite (its real-machine gate can then run
  fully detached from the real login keychain). The `sync` tombstone (Phase 0,
  v0.7.0) spans v0.7.1 before the name is reclaimed in v0.7.2 — comfortably
  past the one-release minimum.
- **Backup back-references are not rewritten** by `account rm`/`rename`. An
  existing backup's `Meta.ActiveBefore` keeps the old account name; rolling
  back to such a backup restores the old name into
  `state.json` while the snapshot no longer exists, so the next `kae use`/
  `apply` errors with "account not captured". Documented limitation; prune the
  affected backups manually if needed.
- TUI, Windows, Codex keyring driver, account auto-detection,
  `env export --dotenv --reveal` — see [ROADMAP.md](ROADMAP.md).
- No automatic network access.

## Acceptance Criteria

- **file-driver override**: with `KAE_CLAUDE_DRIVER=file`, `kae use claude
  <account> --dry-run` on darwin reports a `.credentials.json` file action
  (not a keychain action); unset, darwin keeps the keychain driver (no
  regression). A temp-HOME smoke check switches claude with the override on
  both `kae add` and `kae use`, and asserts the real login keychain is never
  read or written ([docs/VALIDATION.md](docs/VALIDATION.md) updated with the
  procedure).
- **`kae account rm`**: removes the snapshot dir and all secret items; prints a
  confirmation; refuses the active account (exit `10`) without `--force`;
  refuses a profile-referenced account (exit `10`) naming the profiles;
  `--dry-run` writes nothing; unknown account exits `7`.
- **`kae account rename`**: round-trips secret items (copy+delete), moves the
  dir, updates `state.json active[tool]`; refuses (exit `10`), naming the
  profiles, when a profile references `<old>`; refuses an existing `<new>`. A test asserts the renamed
  account resolves via `kae use` after rename.
- **`config.toml` writer**: a programmatic edit (e.g. `kae profile set`)
  preserves the file's leading comments, field order, and unrelated
  `[tools.*]` keys; a round-trip test asserts comments and untouched keys
  survive.
- **`kae profile`**: `save` captures the active accounts into a named profile;
  `set`/`unset` add and remove a single tool mapping (an `unset` of the last
  mapping removes the empty profile); `default <name>` sets `default_profile`
  (unknown profile exits `7`) and `default --clear` empties it; `rm` deletes a
  profile and refuses (exit `10`) to orphan `default_profile` without `--force`;
  unknown account/tool exits `7`; `--dry-run` writes nothing.
- **doctor orphan**: discovery note committed; if implemented, a `kagikae`
  item with no snapshot dir produces a `keychain_orphan` warn-level check, and
  the JSON report keeps `schema_version: 1`.
- `mise run check` passes; JSON keeps `schema_version: 1`, stable tokens, `[]`
  arrays; redaction tests cover any new output path.

## Release Steps

1. Land the file-driver override; smoke check proves real-keychain
   non-interference (this unblocks the v0.7.2 Phase 6 gate).
2. Land the comment-preserving `config.toml` writer (shared dependency), then
   `kae account rm` / `rename`; profile-reference and active-account guards
   (exit `10`) tested; backup back-reference limitation documented.
3. Land `kae profile save|set|unset|rm` on the writer; `default_profile`
   orphan guard (exit `10`) and `--dry-run` tested.
4. doctor orphan: run discovery, then implement or defer with the reason.
5. `docs/VALIDATION.md` v0.7.1 smoke results; README examples verified against
   the built binary.
6. Tag `v0.7.1`, GitHub release.

---

# kae v0.7.0

Bond mode, credential-private per-directory isolation, and the scope×environment
model foundations.

Previous baseline: v0.6.0 (three new adapters — copilot, cursor, opencode —
and pinned-directory guard; see git tag v0.6.0).

## Scope

- **`kae bond [<profile>]`** — new per-directory mode: shares settings,
  sessions, hooks, and memory with the real home, while credentials are
  private to the directory. A denylist approach: everything in the real home
  directory is symlinked except credential files (hard-coded: claude →
  `.credentials.json`; codex → `auth.json`), which are private-copied at
  `0600`. Bond dir is account-agnostic (`isolation/<pin-id>/<tool>/bond/`,
  where pin-id = first 16 hex chars of SHA-256 of the absolute directory
  path), so switching accounts inside a bonded directory does not change the
  dir layout. `kae run --mode bond` also available.
- **`bond_denylist_extra`** config option — per-tool list of extra file names
  to exclude from bond symlinking (on top of the built-in credential list).
  Hard-coded credential artifacts are refused to prevent misconfiguration.
- **`kae sync` → `kae apply` rename (Phase 0)** — completed; old `sync`
  command removed.
- **Paths/constants cleanup (Phase 1)** — `paths.PinID`, `paths.BondDir`,
  and related constants moved to the canonical `internal/paths` package.
- **`/oauthAccount` removal (Phase 3)** — `~/.claude.json`'s `oauthAccount`
  field is no longer switched. Real-machine validation (2026-06-14) confirmed
  it is a token-derived identity cache that claude self-heals; switching it
  risked corrupting live sessions. Claude adapters now declare one artifact
  only (the token). `~/.claude.json` is symlinked wholesale in isolation modes.
- **`kae pin` semantics flip (Phase 4)** — `kae pin` now defaults to fully
  isolated mode (`isolation/<pin-id>/<tool>/pin/<account>/config/`), replacing
  the v0.6.0 overlay default. Opt-in sharing via `tools.<tool>.pin_shared_items`
  (default empty). Legacy overlay-mode blocks are detected and warn on
  `kae pin`; migrate with `kae unpin && kae pin <profile>` (isolated) or
  `kae unpin && kae bond <profile>` (shared). `kae run --mode pin` available.
- **`kae as <tool> <account>` (Phase 5)** — new command: swaps the credential
  inside a bonded or pinned directory to a different account without touching
  settings, sessions, or memory. Bond mode: credential overwritten in the
  account-agnostic bond dir. Pin mode: new per-account config dir prepared,
  `.mise.toml` env entry updated.

## Acceptance Criteria

- `kae bond <profile>` writes `.mise.toml` with `CLAUDE_CONFIG_DIR` /
  `CODEX_HOME` pointing to `isolation/<pin-id>/<tool>/bond/`.
- Bond dir contains symlinks for non-credential real-home items and a
  private copy (`0600`) of the credential file.
- Re-running `kae bond` is idempotent (stale symlinks refreshed, no error).
- Missing credential (not logged in) is silently skipped, not an error.
- `kae run --mode bond ... -- <cmd>` sets the isolation env without mutating
  live state.
- **Real-machine gate**: `kae bond <profile>` in a project directory, then
  `claude -p '' --model haiku`; asserts AUTH-OK inside the directory while
  `~/.claude` remains unchanged. Required before merge to main. On macOS,
  where `CLAUDE_CONFIG_DIR` suppresses keychain access, kae copies the
  keychain credential bytes into the bond dir's `.credentials.json` so
  claude authenticates without touching the real `~/.claude`.
- `mise run check` passes; no regression in existing modes.
- **Phase 3**: `kae use claude <account> --dry-run` reports exactly 1 action
  (the token); `/oauthAccount` never appears in actions.
- **Phase 4**: `kae pin <profile>` writes a pin-mode block
  (`isolation/<pin-id>/claude/pin/<account>/config/`); a legacy overlay-mode
  `.mise.toml` triggers the migration warning. `kae run --mode pin` succeeds.
- **Phase 5**: `kae as claude <account>` inside a bonded directory overwrites
  the credential and prints confirmation. Inside a pinned directory it prepares
  a new config dir and updates the `.mise.toml` env entry.

## Release Steps

1. Pass all acceptance criteria above, including real-machine gate.
2. Update `docs/VALIDATION.md` v0.7.0 smoke-check results.
3. README examples verified against the built binary.
4. Tag `v0.7.0`, GitHub release.

---

# kae v0.6.0

Tool coverage and pin hardening: three new adapters (copilot, cursor,
opencode), the gemini → agy transition, and closing the pinned-directory
semantics gap. Pre-stable: this release removes the gemini adapter (see
Breaking Changes).

Previous baseline: v0.5.0 (the use/pin/run command system and overlay
isolation; see git tag v0.5.0).

## Scope

- **Pinned-directory guard** — inside a pinned directory, `kae use`,
  `kae add`, and `kae apply` refuse with exit `5` and guidance: change the
  directory's accounts with `kae pin <profile>`, or act on the real home
  with the new `--global` flag (which makes the adapters ignore
  kae-managed isolation env vars when resolving base paths). Rationale:
  today such a run splits across three states — the keychain (global),
  the identity file (overlay), and state.json (global belief) — a
  three-way mismatch. Detection reuses the pin context already surfaced
  by `kae status`.
- **gemini removal + agy promotion** (breaking) — upstream retired Gemini
  CLI in favor of Antigravity (2026-05-19); the gemini adapter is removed
  (unknown-tool error; release-notes pointer to agy). agy graduates from
  experimental: pin down the OS-keyring item contract (the default agy
  storage), add structure guards, generate its mise run task, and pass
  real-machine acceptance.
- **copilot adapter** — GitHub Copilot CLI. Auth artifacts: OAuth token in
  the OS keychain (service `copilot-cli`; plaintext `~/.copilot/config.json`
  fallback on keychain-less systems) plus the `~/.copilot/settings.json`
  account state. Discovery first: per-account keychain item layout, the
  interplay with copilot's native `/user switch` (last-used account
  record), and whether the claude verbatim-keychain pattern (capture/
  restore raw bytes via the `security` CLI, ACL-preserving) carries over.
  `kae doctor` gains `env_conflict` checks for `COPILOT_GITHUB_TOKEN` /
  `GH_TOKEN` / `GITHUB_TOKEN`, which outrank the keychain login. The gh
  CLI's own auth is out of scope and untouched (separate storage; lowest-
  priority fallback only).
- **cursor adapter** — Cursor CLI (`cursor-agent`). Browser login with
  locally stored credentials; discovery first (`~/.cursor` artifact
  layout), then the standard switched/preserved allowlist.
- **opencode adapter** — OpenCode. ChatGPT subscription login (native
  since the OpenAI partnership; Claude subscription login was removed
  upstream in 2026-01). Auth state is expected file-based (XDG data
  `auth.json`; discovery first). API-key providers remain env-mode
  territory, as for every tool.
- **`overlay_unshared`** — per-tool exclusions from the built-in overlay
  share list (the mirror of `overlay_extra_shared`); `kae pin` prints
  what it linked and what it skipped so the effective share set is
  visible without reading docs.
- **Remote share-list definitions (design only)** — design loading the
  shared-item defaults from a published definition file so the list can
  follow upstream changes without a kae release. Hard requirements
  already agreed: the auth/identity denylist stays hard-coded, fetching
  is an explicit command (never automatic or at switch time), and the
  diff is shown before adoption. Outcome: a design section in docs, not
  necessarily shipped code.

Implementation order: pinned-directory guard → gemini/agy → copilot →
cursor → opencode → overlay_unshared → remote-definition design. Each
adapter lands behind its own discovery note in ADAPTERS.md before code.

## Non-Goals (this release)

TUI (ROADMAP), Windows, Codex keyring driver, login UX polish,
`env export --dotenv --reveal`, performance polish, claude file-driver
override — see [ROADMAP.md](ROADMAP.md). No automatic network access:
the remote-definition work is design only.

## Breaking Changes

| Removed | Replacement |
|---------|-------------|
| `gemini` tool (adapter, tasks, doctor checks) | `agy` (Antigravity CLI, the upstream successor) |

`kae <cmd> gemini ...` fails as an unknown tool naming agy; captured
gemini accounts remain on disk untouched (manual cleanup, documented in
the release notes).

## Acceptance Criteria

- Inside a pinned directory `kae use <profile>` exits `5` naming
  `kae pin` and `--global`; `kae use --global <profile>` switches the
  real home with state.json consistent (real machine).
- `kae use agy <account>` round-trips with the keyring storage and passes
  the fresh-process auth check; gemini commands fail as unknown tool.
- copilot / cursor / opencode each: `kae add --no-login` → `kae use`
  round-trip with a fresh-process auth check on the real machine, a
  normative switched/preserved table in ADAPTERS.md, and redaction tests
  for any new output path. copilot: doctor flags the token env vars.
- A built-in shared item listed in `overlay_unshared` is not linked by a
  new `kae pin`, and the pin output lists linked/skipped items.
- `mise run check` passes; JSON keeps `schema_version: 1`, stable tokens,
  `[]` arrays.

## Release Steps

1. Bump `toolVersion` (and its test) at cycle start — the gemini removal
   error names v0.6.0, so the binary must agree from the first dev build.
2. Acceptance criteria green; `docs/VALIDATION.md` checklist done (smoke
   uses file-based tools on macOS — keychain warning; copilot smoke needs
   the same care as claude).
3. README examples verified against the built binary.
4. Tag `v0.6.0`, GitHub release with the breaking-changes table.
