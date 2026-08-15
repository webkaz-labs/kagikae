# Credential rules

Thirteen rules about a credential **copy** — where one lives, and who may
overwrite, harvest, attribute, order or delete one, in what order. Read this file
before touching any of that. Do not trust a list of the call sites; find them:
`git grep -ln 'dirCredential\|supersedes\|harvest\|Attribut\|unintendedLinks\|buildLsPins\|EnvConflict' internal/`
— checked against all thirteen rules: each has a call site in the result, and
`EnvConflict` is the only term that reaches `internal/adapter/`, so a rule about
adapter-level code is silently missing without it.

**This is not every credential rule.** They live here rather than in
[AGENTS.md](../AGENTS.md) — the one document loaded into every session — because
none of them is consulted in *every* task. Its § Implementation Boundaries keeps one
routing line per rule and deliberately states none of them, so **this file is the
normative text** and a code comment citing `AGENTS.md` for one of these rules means
the section here. That section also keeps the credential rules an ordinary task does
consult, so a question this file does not answer may still have an answer there.

Each rule carries its own measurement and date. Keep them with the rule.

## Resolving a credential's location

Where a credential lives can be a **rule, not a constant**, and only the
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

## When a store's rule is not knowable from the environment

**Some of a store's rule is not knowable from the environment, and there the
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

## A credential that belongs to an account, not a directory

**A credential can belong to an account rather than to a directory, and then the
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

## Removing a per-directory credential

**A per-directory credential has to be removed when nothing points at it any
more.** `removeDirCredential` is normative for *which* ones — how a keychain item is
addressed at all (service **and** account, never service alone before a write) stays
in [ADAPTERS.md](ADAPTERS.md) § Keyring item contract — and the rule is two
rules over two kinds, not one: a **keychain item** where the adapter declares it
`KeychainDirBindable`, and a **file** credential only where it is no longer the
copy its own store reads (the account's own store, or a store a migration just
moved the credential out of). Do not restate that anywhere; link to it. This rule
read "keychain items only" for a release, which is a licence to restore a gate that
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
it, and only a fragment says that (§ A store tree is history; a fragment is the
binding). Two rules come out of the correction. Never pass `-d` — it prompts per
item and prints the secret. And an
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

## Harvesting before a write or a delete

**The copy in a per-directory store can be newer than the snapshot, so every
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

## A chokepoint is not complete coverage

**A chokepoint is not the same as complete coverage.** `writeDirCredential` is where
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
derivations were tried and both destroyed a login (the label alone; reader membership,
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

## Never harvest a copy you cannot attribute

**Never harvest a copy you cannot attribute.** A `-s` store is account-agnostic, so
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
bind runs — deleting there destroyed the newest copy of a renamed account's credential,
which is why the two answer differently. That pair is reached for a store holding **its
own** credential; where the credential is the account's, a bind's sweep returns earlier
still and keeps the copy without reporting it, so for **that** shape `kae account rename`
no longer reaches this decision through kae's own re-bind remedy — it still does for a
pre-split binding (`TestAccountRenameStrandsTheBoundDirectorysCredential` is the first,
`TestRunRebindSweepKeepsALostAccountsCredential` the second;
[ROADMAP.md](ROADMAP.md) § `kae account rename` leaves a bound directory's store under
the old name carries what is still owed). And a
payload kae could not read or date is deleted by `--purge` too, for the first exception's
reason rather than as a rule of its own: keeping it strands a secret **nothing kae offers
can remove**, since a per-directory item is addressable only from the string kae hashes
its service name from and that sweep is the only path to it — so it reaches only what that
sweep reaches (`removeDirCredential`'s kinds), and a file still sitting in a store `unpin`
keeps needs no escape because the user can name its path. Both exceptions say what they
are destroying — and the second says kae could not tell **whose** it was, since it runs
before any attribution. Neither lets a housekeeping bind do it.

## Ordering two copies of one credential

**Two copies of one credential are ordered in exactly one place.** `supersedes` is the
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

## When a refusal destroys instead of preserving

**A refusal is conservative only where something else holds the copy; a recapture's is
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
**Which side to keep is forced, not chosen**, and the fact that forces it is worth
knowing before re-deriving it: **nothing backs up an account snapshot.** `createBackup`
records live artifacts only (`artifact.ReadLive`), and no command archives a snapshot,
so "keep the snapshot and back up the live copy" is the only pairing where neither copy
is destroyed. Overwriting the snapshot with a payload kae cannot judge destroys the one
durable record of that account with nothing to recover it from — and the two causes of
that state (an upstream `expiresAt` type change, where the live copy really is newest;
a truncated payload, where it is worthless) want opposite answers and are
indistinguishable offline.

## What kae observed is not what the tool can do

**What kae observed is not what the tool can do, and a message may only claim the
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

## A restore is not unconditional

**A restore is not unconditional, and the two paths answer differently.** `run -s`
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

## A new per-directory mechanism and the link reconcile

**A new per-directory mechanism also owes the link reconcile a statement of
intent.** `unintendedLinks` retracts every symlink a bind does not intend to share,
and there is no default to fall back on: the two existing modes take that intent
from deliberately different places, and one of them cannot always establish it at
all. `docs/ADAPTERS.md` (§ per-directory shared bind, § per-directory isolated bind)
is normative for which source each mode uses and what happens when the intent is
unknown; do not restate the rule anywhere else, including in a code comment.

## A store tree is history; a fragment is the binding

**A bound directory's store tree is history; its mise fragment is the binding.**
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
