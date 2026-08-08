# kagikae working guide

Standalone public repository. Follow the bundled Go CLI standard in
[.claude/skills/go-cli-tooling/](.claude/skills/go-cli-tooling/SKILL.md)
(references under `references/`), plus these local rules.

## Documentation Map

| Document | When To Read |
|----------|--------------|
| [README.md](README.md) | user-facing command or setup changes |
| [docs/DESIGN.md](docs/DESIGN.md) | mission, modes, terminology, boundary changes — and **§ Tool Tiers before adding or widening surface for any tool**. A tier decides which *modes* a tool gets and never which guards apply; that section is the only place that says which tool is in which tier, so do not copy the mapping here or anywhere else |
| [docs/ADAPTERS.md](docs/ADAPTERS.md) | anything that touches what a tool adapter switches or preserves |
| [docs/ADAPTERS-COMPANION.md](docs/ADAPTERS-COMPANION.md) | anything that touches what companion-auth lockstep (git/gh/cloud CLIs) switches or preserves |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | package layout, adapter interface, transaction, lock changes |
| [docs/CLI.md](docs/CLI.md) | command flags, output, exit codes, JSON contract changes |
| [docs/DATA-MODEL.md](docs/DATA-MODEL.md) | config, snapshot, state, backup, secret-ref changes |
| [docs/SECURITY.md](docs/SECURITY.md) | secrets, subprocess, permission, redaction changes |
| [docs/SCOPE-MODEL.md](docs/SCOPE-MODEL.md) | the scope/isolation model's rationale and the upstream findings behind it — read it to learn *why* a decision was made, never for the rules themselves, which live in the documents above. It was the only file under `docs/` missing from this table, which is how it came to hold a second full copy of one upstream measurement |
| [docs/ROADMAP.md](docs/ROADMAP.md) | long-term ordering changes |
| [docs/RELEASE.md](docs/RELEASE.md) | active release target changes |
| [docs/VALIDATION.md](docs/VALIDATION.md) | before commit and release checks |
| [.claude/skills/upstream-auth-drift/](.claude/skills/upstream-auth-drift/SKILL.md) | an upstream tool may have changed how its authentication works: a doctor `upstream_version` / `identity_drift` warning, a tool upgrade, a "kae says it switched but the tool shows the old account" report, or a routine re-verification |

## Validation

```bash
mise run check
git diff --check
```

`mise run check` is the authoritative gate; it must pass before every commit.
It runs `lint` (gofumpt + goimports format check, `staticcheck -checks=SA*`,
curated `golangci-lint`, `shellcheck`), `go test ./...`, `go vet`,
`go mod verify`, and `go build ./...`. `mise run audit` (govulncheck, plus the
upstream literal fingerprints — it reads the installed tools' own binaries, so it
only means anything on a machine that has them) and `mise run goreleaser-check` are
slower release-time checks. Lint tools run via `go run <tool>@<pinned version>`; the
first run downloads them.

While editing (this is a Go module — the LSP is `gopls`):

- **Symbol work goes through the LSP, not Grep** — resolve definitions,
  references, and types with the LSP (go-to-definition / find-references /
  hover). Grep is for text/string matches, not for "where is this symbol used".
- **Read LSP diagnostics after each edit** — clear errors and warnings as you
  go. The LSP is the fast inner loop; it does not replace `mise run check`,
  which stays the pre-commit gate (a clean LSP does not imply green tests).

Never run tests or smoke checks against the real `$HOME`; every test uses
`t.TempDir()` HOME/XDG roots, and smoke checks export a temp HOME
([docs/VALIDATION.md](docs/VALIDATION.md)).

**A temp `HOME` is not enough, and this has drawn blood.** `paths.Resolve` reads
every XDG root *independently*, and an absolute value already in the environment
**wins over the temp HOME** — so a shell that exports HOME and some of them still
resolves `state.json` under the operator's real `~/.local/state`. A smoke run
shaped exactly that way wrote a fixture account into a live `state.json`
(2026-07-31), leaving `active.claude` pointing at an account that did not exist —
a state `kae doctor` reported nothing about until `active_orphan` was added.
**Never hand-write the exports: `. scripts/smoke-env.sh`**, which is the one copy
of them and says which roots and why. The omission happened while writing a fresh
block in docs/VALIDATION.md, next to two correct ones.

## Implementation Boundaries

- Keep `main.go` as dispatch only; handlers and report builders in
  `internal/cmd`.
- All subprocesses (`security`, `secret-tool`, binary detection) go through
  `internal/runner`.
- Adapters declare artifact specs; capture/apply/backup/rollback IO lives in
  `internal/artifact` and generic layers. Do not duplicate IO in adapters.
- The per-tool switched/preserved allowlists in `docs/ADAPTERS.md` are the
  normative contract: code must match that document, and any change requires
  updating it in the same commit.
- Where a credential lives can be a **rule, not a constant**, and only the
  adapter may evaluate it. claude derives its keychain service name from
  `CLAUDE_CONFIG_DIR` (`Claude Code-credentials-<sha8>` over the env string
  **NFC-normalized**, with no path cleaning at all — so a trailing slash is a
  different item, and a decomposed non-ASCII component must be normalized before
  hashing or kae writes an item claude never reads), and kae's own isolation modes
  are what set that variable. Modelling the name as a
  constant made every pinned directory on macOS run the previous account with
  every offline guard green, because the tool reads the keychain first and its
  first token refresh creates the per-directory item and deletes the file kae
  wrote. So: resolve a credential's location by asking the adapter with an env
  built for the target directory (`dirCredentialSpec`), never by recomputing a
  path or a service name at the call site; and when a write to the authoritative
  store fails, return the error — a fallback to the secondary store reports
  success while the tool reads something else.
- **Some of a store's rule is not knowable from the environment, and there the
  answer is never a guess.** Three shapes, all live. Two leave a proxy in the
  environment, so they **warn** (`env_conflict`, and the adapter keeps its own
  path): kae and the tool read the *same* variable differently (a **relative**
  `XDG_DATA_HOME` / `COPILOT_HOME` resolves against the *tool's* cwd, and kae is
  invoked from anywhere in the project — so following it verbatim is not by itself
  the fix), and a store chosen by something outside the variable that still has an
  env-visible trigger (agy skips the keychain on an ssh/wsl detector, which reads
  variables, though its sibling container detector reads `/.dockerenv` and its 1s
  keyring timeout has no proxy at all). The third leaves **no proxy at all** — a
  flag outranks the variable (copilot's deprecated `--config-dir` beats
  `COPILOT_HOME`) — and an unobservable branch must not be turned into a warning
  on the observable one; record it in `docs/ADAPTERS.md` and move on. In every
  shape: **never declare an artifact for a location you could not measure** — a
  guessed path is a write nothing reads, which is the defect this whole section
  exists for.
- **A credential can belong to an account rather than to a directory, and then the
  rules invert.** claude's `CLAUDE_SECURESTORAGE_CONFIG_DIR` moves the credential
  without moving the sessions, so a bind exports **two** variables and every
  per-directory resolution takes a pair (`bindDirs`) — the config dir and the
  credential store. `docs/ADAPTERS.md` § Per-account credential store is normative;
  three traps that outlive the code. The pair is swappable at a call site and getting
  it backwards is silent: attribution reads the identity cache, which stays in the
  **config** dir, so handing it the credential store makes every identity look like
  one that escapes its store and the harvest refuses every time — overwriting the
  copy it came to preserve. A store's credential location is read from the recorded
  binding and **never derived from the account**: the store walk returns stores of
  older bindings forever, so a leftover store bound to one account would otherwise be
  handed another account's credential to harvest, and a matching identity cache files
  one account's token under the other's name. And deleting inverts: a per-account
  store is not one directory's to remove, so a bind's sweep leaves it and only
  `unpin --purge` may take it, after counting every fragment **and** `state.synced` —
  where a source kae could not read has to mean keep, since "no reference found" and
  "kae could not look" differ by one logged-out sibling.
- **A per-directory credential has to be removed when nothing points at it any
  more.** `removeDirCredential` is normative for *which* ones, and the rule is two
  rules over two kinds, not one: a **keychain item** where the adapter declares it
  `KeychainDirBindable`, and a **file** credential only where it is no longer the
  copy its own store reads (the account's own store, or a store a migration just
  moved the credential out of). Do not restate that anywhere; link to it. It read
  "keychain items only" here for a release, which is a licence to restore a gate that
  leaves a full plaintext copy of a live account behind.
  The item is still the case that most needs the sweep, and that is the part worth
  remembering: it lives under a per-directory service name that appears nowhere in
  kae's data dir, so kae cannot *address* one without already knowing the string it
  hashes from.
  This used to say such an item "cannot be enumerated on darwin"; that is too strong
  and was corrected on 2026-08-04 — `security dump-keychain` lists item attributes
  (service, account, dates) with no prompt, which is how five stale per-directory
  items were found on a real machine. **The sweep's design does not change**: an
  enumeration tells you an item exists, never whether another directory still reads
  it, and only a fragment says that (see the next entry). Two rules come out of the
  correction. Never pass `-d` — it prompts per item and prints the secret. And an
  empty enumeration is not proof of nothing: it reads only file-based keychains, so a
  future move to the Data Protection keychain would make this silently return zero,
  the same trap as comparing two empty greps.
  Things that must move in lockstep with it — not a closed list: a **third**
  per-directory mechanism (today `shared` and `isolated`) has to be added to
  `dirCredentialStores`, or its stores are silently never swept, **and to
  `modeLabelStale`** (beside `modeStoreDir`, which carries the note), where it falls
  through to *not stale* — right for an account-keyed mechanism, and the
  "keep then destroy on the next run" defect for an account-agnostic one; and the sweep must
  run **after** the new binding is written, or a mid-sequence failure leaves the live
  binding pointing at a store whose credential is already gone.
- **The copy in a per-directory store can be newer than the snapshot, so every
  write and delete of one harvests first.** claude's refresh token rotates
  single-use, which turns "kae overwrote the directory's credential with an older
  one" from a regression into a logout — reported as success, green in `kae doctor`,
  failing up to 8h later inside the tool (`docs/VALIDATION.md` owns the measurement,
  `docs/ROADMAP.md` the design). Order two copies by `expiresAt` and nothing else,
  guarded `Known && !Revoked && !ExpiresAt.IsZero()` — a tombstone is a fully-formed
  payload, so presence proves nothing — and keep it **claude-only** until another
  tool's rotation is measured. Adding a tool needs a
  measurement **and** an identity-only artifact for attribution to read; `docs/ROADMAP.md`
  § Rotation is measured for claude only owns that rule.
- **A chokepoint is not the same as complete coverage.** `writeDirCredential` is where
  the harvest belongs for the store it writes — put it in a separate "repair" step and
  the overwrite paths stay unconditional, which ships a release that fixes a login and
  then destroys it. But it cannot see a **sibling** store of the same bound directory,
  which is what a re-bind to a different **account** moves the credential to, so
  `kae pin` and `kae pin <tool> <account>` also run a pin-level pass **before**
  materializing while the delete sweep still runs **after** the new binding
  (`docs/ADAPTERS.md` § Per-directory credential store is normative for all of it).
  **A `-s` ↔ `-i` toggle is no longer one of those cases and naming it as one is how
  this passage read until 2026-08-08** — since the per-account credential store, both
  modes read the *account's* store, so a toggle moves the sessions and leaves the
  credential exactly where it was (measured; `TestRunPinModeToggleRemovesTheOldModesItem`
  says the same in its body). What a toggle can still strand is the per-directory
  credential a binding from **before** the split left in the store it moves off, which
  is what makes a re-pin a migration.
  **One of these sites is not a chokepoint kae controls at all — `kae relogin`'s flow,
  where the write that replaces the copy is the *tool's*.** A pass placed after it sees
  only what the login wrote, never what the login destroyed, so "capture the result
  back" is not the same requirement as "keep what was there" and that command harvests
  on both sides. Two things make it worth remembering rather than deriving: the copy at
  risk is the **account's**, so it can be a login the acting directory's binding says
  nothing about; and this command is what every bound-credential refusal names as its
  remedy, so following kae's own advice is what destroyed it (measured 2026-08-08, end
  to end).
  **And the chokepoint's refusal is not free either: for the account's own credential
  store it now skips the write rather than overwriting.** That store is read by every
  directory bound to the account, so a bind is not entitled to spend it, and the refusal
  is reachable whenever nothing can say whose the copy is — which made "bind a second
  worktree to an account you are using" a logout of both, measured 2026-08-08.
  **Who can say is not the directory being bound**, and reading its cache as evidence
  about the account's store is the same defect one level up: a shared bind's config dir
  belongs to the pin-id, so it still carries the *previous* binding's label, and a re-bind
  between two accounts read that as `Conflicting` — the arm that overwrites — about a store
  the label says nothing about. The evidence is the directories **currently reading that
  store**, read from the fragments before a bind rewrites them; so a first bind with a
  sibling reader that agrees now harvests and writes, and only a store with no reader at
  all is kept. **`Conflicting` needs the acting directory to be one of the readers that
  disagree** — the first version of this took a majority of the readers, and a sibling that
  had been logged in as somebody else then let an unrelated first bind destroy the only copy
  of that login, which is the same defect one level in. Two corollaries that are easy to
  miss: the delete path erases its own
  evidence (`unpin --purge` may only delete once nothing points at the store, which is
  exactly when no reader is left to attribute it), so a caller that has just torn a binding
  down has to say so; and a reader is not an independent observer, because a bind writes
  kae's own label into it. `docs/ADAPTERS.md` § Per-directory credential store is normative
  for the condition; three things about it are easy to get wrong. It is keyed on the
  *attribution* refusal alone (`Unattributed`) — marking every refusal from
  `dirIdentityConfirms` also caught the `Conflicting` one, which must still overwrite, so
  a re-bind silently did not switch. **Nothing of kae's is written when the copy is kept**,
  and the label is the half that matters: an intermediate version wrote it, and kae's own
  label is exactly what a later bind's attribution reads — so `kae pin` again confirmed
  against a cache kae had planted and harvested the copy the first bind refused, filing
  another account's token under this one's name (measured). Skipping it restores the rule
  the rest of that function states, that the identity follows a *successful* credential
  write. **A keep must also retract a label it can show is stale** — not writing one is only
  half of it. Leaving the previous binding's label behind is how a keep destroys what it
  kept: the next run's fragment names the new account, so the directory is one of the store's
  readers and that label is its only reading. What separates a stale label from a live one is
  the **mode** — a shared config dir is one per pin×tool, an isolated one and the globally
  isolated home are keyed by the account, so a disagreement in *those* is a login as somebody
  else and deleting it destroys the only record of whose the credential is. Two other
  derivations were tried and both destroyed a login (the label alone; witness membership,
  which reads every directory as a stranger when the walk is incomplete). What it costs is that the acceptance block must seed the cache the **tool** would
  have written wherever it expects a harvest, which is the honest fixture anyway. And the
  pass words its consequence as *leaving it where it is* rather than predicting the write:
  keyed on its own store's dirs it said "this bind replaces it" about a copy nothing
  replaced, and that wording survived the entire suite until an assertion existed.
  Two more traps that outlive the specific code. Harvesting is not deleting — they belong
  on opposite sides of the write, so do not "simplify" them into one pass. And a
  suppression that keeps two speakers from repeating each other must be keyed on **what
  was actually reported**, not on which store *kind* a pass would have looked at: the
  second version silenced precisely the cases where the pass had nothing to say, which
  are the destructive ones. Both were found by review after a version that looked
  complete and passed its tests.
- **Never harvest a copy you cannot attribute.** A `-s` store is account-agnostic, so
  a re-bind finds the previous account's (usually newer) credential there, and filing
  it under this account's name is undetectable afterwards — the token is opaque, so
  live, snapshot and doctor all agree on a label that is simply wrong. The evidence is
  the identity cache beside the credential, and **absence is not agreement**
  (no recorded identity, no live cache, unreadable, or a target that resolves outside
  the store, which labels the real home instead). Where a store's own path does not
  name its account, only the fragment being *replaced* does — read it before
  overwriting or removing it, and only trust it for the mechanism it describes. For a
  delete, an account kae cannot name at all is a reason to keep the item; so is a
  payload kae cannot read **or date**, which may be a working login in a shape kae has not
  been taught. Two exceptions today, and both turn on **what the user asked for**, not on
  the state — a housekeeping sweep keeps, `kae unpin --purge` takes.
  A usable copy whose account no longer exists is deleted by `kae unpin --purge`
  (refusing would strand a live token nothing can address) and **kept** by the sweep a
  bind runs — `kae account rename` reaches that sweep through kae's own re-bind remedy,
  where deleting destroyed the newest copy of the renamed account's credential. And a
  payload kae could not read or date is deleted by `--purge` too, for the first exception's
  reason rather than as a rule of its own: keeping it strands a secret **nothing kae offers
  can remove**, since a per-directory item is addressable only from the string kae hashes
  its service name from and that sweep is the only path to it — so it reaches only what that
  sweep reaches (`removeDirCredential`'s kinds), and a file still sitting in a store `unpin`
  keeps needs no escape because the user can name its path. Both exceptions say what they
  are destroying — and the second says kae could not tell **whose** it was, since it runs
  before any attribution. Neither lets a housekeeping bind do it.
- **Two copies of one credential are ordered in exactly one place.** `supersedes` is the
  only comparator — `expiresAt`, with the side claiming to be newer gated by `orderable`
  (`Known && !Revoked && !ExpiresAt.IsZero()`) and the other side degrading to a zero
  cutoff. Every consumer shares it (today the harvest, `run -s`, `kae rollback` and the
  switch-away recapture, which without it launders a rolled-back copy over the snapshot
  that still worked); a new one calls it rather than re-deriving the cutoff, and
  `readLiveCredential` is the one deliberate variant, splitting the same predicate three
  ways because a delete must tell "nothing to lose" from "kae cannot tell". **A caller
  comparing against its own copy owes that copy the same `orderable` test.** And the
  asymmetry runs both ways: a caller may owe `orderable` to the **losing** side too,
  which `supersedes` deliberately does not require of it. Neither direction is the safe
  default — read which question the caller is asking ("may I overwrite this copy" and
  "may I tell the user it is dead" take opposite answers), and `pinSupersededChecks` and
  `recaptureWouldDowngrade`'s `preserve` carry the worked examples — the second one added
  after the rule was written and broken anyway, so treat a **new** consumer as owing the
  question rather than as covered by the rule. Taking a
  subset of it is how a copy with no deadline came to read as superseded by anything:
  claude sets `Known` on the mere *presence* of `expiresAt` and parses a non-numeric one
  to the zero time, so an upstream type change yields a payload that is `Known`,
  un-`Revoked` and undated at once.
- **A refusal is conservative only where something else holds the copy; a recapture's is
  not.** The attribution and ordering guards read the same predicates on both sides of a
  seam whose consequences invert. `dirIdentityConfirms` and `liveLoginMatchesBackup`
  decline to *overwrite* or *delete*, so refusing keeps what exists. The two recaptures
  (`keepSnapshotIdentity` and `recaptureWouldDowngrade`, via `kae use`'s switch-away and
  `kae run -s`'s post-child pass) decline to *preserve*, and their caller then overwrites
  the live store — so refusing **destroys** the copy unless a backup holds it. `kae use`
  gets that for free (`createBackup` runs before its recapture); `kae run -s` does not (its
  backup predates the child) and creates a second one, reason `run-unattributable`, whose
  id every such refusal names. Three consequences that outlive the code: a message may not
  imply a copy survives when kae could not back it up; a *new* refusal on either recapture
  owes the same backup — **and so does widening an existing one**, which is what the
  second instance actually was, so a rule keyed on "new" would have read as not applying
  (it did); and that backup is a preserved artifact rather than an undo target, so a bare
  `kae rollback` must not target it (`latestRestorable`). Ported without re-asking the
  question, this shipped the logout twice in one branch — once on attribution, once on
  ordering.
  **A third instance is `kae relogin`'s pre-flight, and it is the one that shows the rule
  is not about who performs the overwrite.** There the write is the *tool's* own login, and
  the copy is gone just the same, so the refusal owes its backup
  (`relogin-unattributable`) exactly as `run -s`'s does. What it also cost is the price of
  being the first backup taken from a **bound** store: every consumer of a backup record
  had assumed the record came from the tool's *global* store, and the restore's moved-store
  check re-resolves specs globally — so following it writes the **real home**, turning one
  directory's loss into a global logout (and for a tool whose two stores are not
  interchangeable it refuses instead, which merely makes the backup worthless when it is
  needed). `fromBoundStore` is the one place that decides it, reading a **recorded**
  `Meta.BoundStore` — not the reason (a derived backup keeps the records and changes the
  reason, so a reason lookup loses it exactly once) and not the target (a keychain record's
  target is a *service name*, so a path test answers "not kae's" for the per-directory item
  that most needs it). A future backup taken from anywhere but the global store owes the
  same question.
  **And it gates a class, not a check — getting that wrong is what shipped.** The first
  version gated the moved-store check alone, while three other consumers went on treating
  such a backup as a statement about global state: the unrecorded-identity sweep (specs
  resolved globally, so it cleared the **real home's** identity), the `ActiveBefore` restore
  (which flipped the globally active account), and the superseded-credential warning (which
  read `ActiveBefore` as the account the recorded copy belongs to and ordered two unrelated
  chains by `expiresAt`). The first two **compose**: the identity wipe removes the evidence
  `keepSnapshotIdentity` refuses on, so the next ordinary `kae use` files one account's
  token under another's name. Measured end to end 2026-08-08 — and nothing failed when the
  global side effects were deleted, which is how they shipped.
  **Which side to keep is forced, not chosen**, and the fact that forces it is worth
  knowing before re-deriving it: **nothing backs up an account snapshot.** `createBackup`
  records live artifacts only (`artifact.ReadLive`), and no command archives a snapshot,
  so "keep the snapshot and back up the live copy" is the only pairing where neither copy
  is destroyed. Overwriting the snapshot with a payload kae cannot judge destroys the one
  durable record of that account with nothing to recover it from — and the two causes of
  that state (an upstream `expiresAt` type change, where the live copy really is newest;
  a truncated payload, where it is worthless) want opposite answers and are
  indistinguishable offline.
- **What kae observed is not what the tool can do, and a message may only claim the
  first.** `Revoked` means "no usable token in this payload", derived from fields that are
  empty *or absent* — so a login whose token keys were renamed upstream reads the same as
  a tombstone. The wider reading is deliberate, because the failure directions are not
  symmetric: over-reading it makes every write path *decline* to touch the copy, while
  under-reading it would adopt a logged-out payload as live. What it may not do is license
  "cannot log in". Same one level up: a payload kae cannot parse at all may be a working
  login in a shape kae has not been taught (`liveUnreadable` says exactly that), so the
  strongest honest claim is that kae cannot *compare* it — and a weak claim takes a weak
  consequence. Flattening these told a user to undo a rollback that had just restored a
  credential which was probably fine. `docs/CLI.md` § `kae rollback --json` is normative
  for the three wordings.
- **A restore is not unconditional, and the two paths answer differently.** `run -s`
  restores a dead or un-orderable recorded copy rather than skipping — otherwise the
  account it applied for one child stays in the real home for good — while `kae rollback`
  reports one and restores anyway. Ordering never establishes *whose* login two copies
  are, so every consumer owes an attribution guard, and **which record it compares against
  is not interchangeable**: `run -s` reads the **backup**, never the account snapshot,
  because its own recapture has already rewritten that snapshot with what the child left
  live. That recapture is itself guarded now — the same two the switch-away recapture
  applies and no third, so a foreign login or a tombstone is refused rather than filed —
  which narrows what the snapshot can be wrong about without making it a record of the
  pre-child state, so the backup stays the right side to read.
  Two deliberate asymmetries to leave alone. `run -s` also requires the target
  to be the account that was already active even though attribution is the stronger
  evidence — on that path the live identity cache is not an independent reading, since
  `run -s` applied the target snapshot's identity into it moments earlier, so a snapshot
  with a wrong recorded identity would otherwise confirm itself. `kae rollback`
  deliberately does *not* require the label to agree, because there both identities are
  genuine reads and demanding it would silence exactly the case where it is wrong. And
  when two places both hold a later copy, compare them **against each other**: the remedy
  differs (a snapshot copy survives the rollback, a live one does not), so picking by
  branch order names a copy that is not the newest.
- **A new per-directory mechanism also owes the link reconcile a statement of
  intent.** `unintendedLinks` retracts every symlink a bind does not intend to share,
  and there is no default to fall back on: the two existing modes take that intent
  from deliberately different places, and one of them cannot always establish it at
  all. `docs/ADAPTERS.md` (§ per-directory shared bind, § per-directory isolated bind)
  is normative for which source each mode uses and what happens when the intent is
  unknown; do not restate the rule here or in a code comment.
- **A bound directory's store tree is history; its mise fragment is the binding.**
  `dirCredentialStores` walks `isolation/<pinID>` and *deliberately* returns stores
  nothing points at any more: `kae unpin` keeps a store so a re-pin restores its
  sessions, and a single-tool re-bind leaves the previously bound tools' stores in
  place. That is exactly right for the teardown sweep, whose job is finding
  leftovers, and wrong for anything that reports on the **live** binding — a leftover
  reported as `bound to <dir>` names a directory that is not bound and a remedy that
  lands where nothing reads. For what is bound *now*, read the fragment
  (`readFragmentAt` → `boundStoreDir`, which derives the store from its mode and
  account). The two functions look interchangeable and are not; `pinChecks` has
  skipped an unpinned directory since it shipped, and the doctor credential sweep
  still shipped its first draft without that gate. Every command that reports on
  bindings needs the same gate, so the doctor consumers now share one walk of it
  (`boundDirStores`) — **use it rather than re-deriving the walk**; `kae ls --pins`
  (`buildLsPins`) keeps its own display-shaped one and is not the last consumer.
  Note also that `fragmentInfo.Accounts` covers **every** bound tool, shared mode
  included.
  Which of that walk's guards a test can actually kill was measured (2026-08-04/05),
  because three of them read like they must be load-bearing and are not — write the
  reason in a comment rather than a test that cannot fail. In `boundDirStores` the
  `!exists` arm, the unreadable-fragment arm and the `dirExists(<bound dir>)` gate all
  **converge**: a missing or gone directory parses to an empty account map, which
  `boundStoreDir` already answers "not bound" from, and all three arms `continue`
  anyway. They are a statement of intent there; the place the same distinction has
  consequences is `pinChecks`, which reports a *different* finding for each. Two
  guards in the same walk **are** killable and must stay tested: the choice of source
  (walking the tree instead of the fragment), and `dirExists(<store dir>)` — that last
  one only bites on darwin, where a per-directory keychain **item** outlives its
  deleted store directory, so a linux-only unit test sees both consumers decline for
  an unrelated reason. Assert it on the walk's own output.
- **A `git worktree` is just another bound directory, and that is the whole
  design** — its own real path, its own pin-id, its own store, its own fragment.
  Worktrees stop being incidental in exactly one place, telling git to ignore the
  fragment, where two measured facts drive the code (`ensureGitExcluded`, which
  carries the detail; contract in `docs/CLI.md` § kae pin): a linked worktree's own
  `$GIT_DIR/info/exclude` is **not consulted**, so the rule goes in
  `$GIT_COMMON_DIR/info/exclude`; and an entry there is anchored at the
  **repository root**, not at its own directory the way a `.gitignore` entry is, so
  it needs `git rev-parse --show-prefix` in front of it or it silently matches
  nothing while `kae pin` reports success. Ask git for both halves through
  `internal/runner`; never assume a `.git` layout, and never act on an answer you
  did not verify (that path must exist).
- **Stubbing a subprocess hides the argv.** A test that fabricates a command's
  output keeps passing when a flag is dropped from the command — it shipped that
  way here. The rule and the reproduction live on `runnertest.Fake`, which is the
  shared fake every package uses; read it before writing a test that stubs
  `internal/runner`.
- **A keychain item's identity is service + account, and per-tool.** codex derives
  the account of its single-service `Codex Auth` item from `CODEX_HOME`
  (`cli|` + 16 hex of sha256 over the **canonicalized** path — symlinks resolved),
  so one service holds one legitimate item per tool home; claude hashes the *raw*
  env string, NFC-normalized, to 8 hex, into the **service** name. Same idea, two
  incompatible formulas: derive each in its own adapter and never port one.
  Consequently **never delete a keychain item by service name alone before writing**
  (`keychain.DeleteItem` on a service another home also uses): that deleted a second
  `CODEX_HOME`'s codex login on every switch, shipped through v0.12.0. **Every**
  keychain spec is `KeychainMatchAccount` now — a guard
  (`TestKeychainSpecsAreAccountScoped`) refuses a new one that is not — and the
  account is derived from the environment being written, never from the live item
  and never from a snapshot captured elsewhere. The direction matters as much as
  the scoping: kae used to prefer the *existing* item's account over the adapter's
  when creating one, so a single item under a wrong account (a former `$USER`)
  pinned every later write to it while the tool went on reading the account its own
  rule names — the write succeeds, the item exists, and the tool reports no login.
- A tool's credential can be a **set** of stores written as one unit, and kae must
  switch the whole set. cursor-agent's `setAuthentication` writes the access token,
  the refresh token and (for an api-key login) the api key together, and its logout
  deletes all three; kae switched only the access token, so `cursor-agent status`
  saw a consistent-looking pair from two accounts, and an api key left behind
  re-minted the *previous* account's tokens on the next expiry. Enumerate what one
  login writes — every item of every service the tool derives — before declaring the
  artifacts, and when a sibling store is deliberately excluded (cursor's
  `cursor-bedrock-*`, written by a separate upstream path) say so in
  `docs/ADAPTERS.md` rather than leaving it unmentioned. A credential artifact is
  never `IdentityOnly`: absent must apply as absent, or the previous account's token
  survives the switch.
- Upstream config values that select a **store** are an enum kae must model whole,
  including its default. codex's `cli_auth_credentials_store` defaults to `file`
  when absent and `auto` means *keyring first, file only if absent* — so mapping
  "anything that is not keyring" to the file store writes `auth.json` while codex
  reads the keychain. A value kae cannot switch (`ephemeral`, or
  `[features] secret_auth_storage`) is refused, not approximated.
- Secret values must never reach stdout/stderr/JSON/metadata/logs. New output
  paths need a redaction test.
- Two comparison predicates, never interchangeable: a **credential** is compared
  byte-exact (`snapshotArtifactDiffers`) — one differing bit is a different
  credential, and that strictness is never loosened. An **identity-only** payload
  is compared on the spec's `IdentityKeys` (`identityDiffers`), because the tool
  rewrites volatile fields in it on its own schedule (claude renews
  `/oauthAccount.profileFetchedAt` past a 24h TTL). Reusing the credential
  comparator for an identity makes every correct switch look like drift a day
  later; it shipped that way once.
- Warnings go to stderr and are emitted **before** the write they warn about.
  `--quiet` suppresses success reports, never warnings, and a warning never
  changes the exit code (a non-zero exit breaks the mise enter hook).
- Mixed-state files are patched by JSON Pointer only; whole-file replacement
  of `~/.claude.json` is forbidden in code review, not just in docs.
- **`state.json` writes go through `App.mutateState`, and a decision about the
  state is made inside the mutation.** The per-tool locks deliberately let
  `kae use claude <a>` and `kae use codex <b>` run at once, so a copy of the
  document loaded earlier in a command is already stale by the time it is
  written back — that reverted the other tool's field with nothing reporting it,
  and `kae rollback` restores credentials, not this file. The seam re-reads
  under a `state` lock; a guard test keeps `state.Save` out of the rest of
  `internal/cmd`. The second half matters just as much: `kae account rm` decides
  *inside* the mutation whether the account it removes is still the active one,
  because the pre-lock answer can predate a switch that finished in between.
- `config.toml` edits go through the comment-preserving `config.Editor` via
  `App.editConfig` (under the config lock). A decode-then-encode round-trip
  (BurntSushi `config.Load` → re-`Marshal`) is forbidden: it silently drops
  every user comment.
- JSON contract tokens live in `internal/constants`; never inline literals.
- Every adapter must implement `adapter.VersionVerifier`, and its
  `VerifiedVersion()` moves in lockstep with that tool's rows in the
  "Upstream Behaviour Assumptions" table of `docs/VALIDATION.md`: re-verify the
  rows, then bump both in the same commit. kae depends on undocumented upstream
  *behaviour*, not just layout, and a behaviour-only change passes every structure
  guard. Record the **condition**, never an absolute: `/oauthAccount` was left
  alone because claude was measured self-healing it, when the fact was that it
  self-heals past a 24h TTL every token refresh renews — so for a credential in
  daily use it never fired, no guard noticed, and switched sessions kept naming
  the previous account. An absolute is what expires silently. The
  `upstream_version` doctor check is the only offline signal, and it *skips
  silently* on a version string it cannot parse — so a stale or typo'd value reads
  as "nothing to report".
- Adding or changing a command or subcommand requires updating shell completion
  in lockstep: the `case` in all three scripts in `internal/cmd/completion.go`,
  any new `kae __complete` kind in `internal/cmd/complete.go`, and the parity
  guard `subcommandVerbs` (its sub-verbs) and the classification
  `positionalCommands` (whether it takes a positional at all), plus
  `completionCommands` and `printHelp`, neither of which any guard checks against
  the router. Adding a **shell** reaches much further than the scripts, and no
  guard checks any of it against `completionScript` — so find the copies rather
  than trust a list of them: `grep -rn fish internal/`. Two kinds fail *silently*
  instead of loudly, which is why the grep is worth running: the installer's
  `--refresh` walk (`completion_install.go`), or the new shell's registered file
  is never rewritten again; and the per-shell loops and table rows in the tests,
  which leave every guard below blind to it.
  `kae <cmd> <TAB>` must never be a dead end — a new subcommand group shipped
  without completion in v0.10.0, and `kae env` / `kae backup` had no case at all
  until v0.17.0. **A guard reaches only what its table names**, which is why
  those two tables are keyed differently and why neither of them closes the
  class on its own; `positionalCommands` **and `subcommandVerbs`** in
  `internal/cmd/completion_install_test.go` are together normative for what each
  guard does and does not see — including why the gap is not closed by
  dispatching each command from a test, and what deleting an entry from
  `subcommandVerbs` silently takes with it. Read them before weakening either. Completion is dynamic, so candidate changes resolve live; only a
  *structural* script change (a new case/kind) alters the registered script.
  That refresh is automatic: `mise run install` and `scripts/install.sh` run
  `kae completion --refresh` (rewrites already-registered files; never creates
  one), and the mise-hook registration self-sources. Plain `go build` does not,
  so run `kae completion --refresh` if you build that way.

## Example Names in Docs and Tests

Never use real account names, profile names, or email addresses in docs,
test fixtures, code comments, or commit messages. Use only generic placeholders
that frame one person's own multiple accounts:

| Context | Allowed names |
|---------|---------------|
| Profile / account names | `main`, `side` |
| Extra accounts (3+ in one test) | neutral names like `alt`, `beta`, `zeta` |
| Example directory | `~/code/side-project` (or `main-app`) |
| Identity email | `you@example.com` |
| Tool examples | the real tool name (`claude`, `codex`, etc.) |

Never use a real login handle.

## Documentation Update Checklist

For every change, decide and report "changed / no change needed" for each:
`README.md`, `AGENTS.md`, and every file under `docs/`.
