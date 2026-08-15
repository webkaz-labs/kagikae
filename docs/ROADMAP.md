# Roadmap

Long-term ordering beyond the active release ([RELEASE.md](RELEASE.md)), ordered
by user impact. § Current work order, for as long as it is there, carries the
near-term order and the release position that order is set against.

An entry goes once it is *only* a record of what happened: what shipped is
recorded in [RELEASE.md](RELEASE.md) and in git log, not here. **Being labelled
shipped, or struck through, is not that criterion and licenses no deletion** —
several entries below carry one label or the other and stay. An entry stops being
*only* a record as soon as removing it would cost the next reader something: work
still owed, a withdrawn prescription re-issued, a settled result re-measured, a
trap that is in neither the code nor git log re-discovered.

**Anything outside this file that defers a question here, or names an entry, is
repointed in the commit that removes the entry — repoint first, then it may go.**
`git grep -in roadmap -- ':!docs/ROADMAP.md'` is the superset to start from: it
held every reference the trims here had to repoint. For some the citation sits alone
on a wrapped continuation line, so the hit shows no content — read the neighbour.
No grep decides whether what a hit names still exists, so the work is
reading the hits, and filtering them by a list of phrases is how each miss
survived. Release notes hold the largest group; the rest were a design-rationale
document, an acceptance log, a user-facing error message and code comments. A net,
not a proof — a reference that defers a question here without naming the file is
stage 3 of the docs scan, filed below.

## Current work order — batches first, release after

**An ordering decision, not a record** (2026-08-15). A release was planned and
deliberately deferred. What decided it is reproducible rather than quoted:

```bash
git diff v0.17.0..HEAD -- 'internal/**/*.go' 'main.go' go.mod go.sum ':!**/*_test.go' \
  | grep -E '^[+-]' | grep -vE '^(\+\+\+|---)' | grep -vE '^[+-]\s*//' | grep -vE '^[+-]\s*$'
```

On any tree where that prints only a rename and a docs pointer inside an error
string, a tag ships the rename — while [ACCEPTANCE.md](ACCEPTANCE.md) § Open gates
is the cost every release carries whatever it contains, and it is a cost only a
human can pay. Deferring spends that cost on a tree worth spending it on. Cut a
release when the list below has landed, not on a schedule.

`go.mod` and `go.sum` are in that pathspec deliberately. A dependency bump with no
`internal/` change — the shape a `mise run audit` finding produces — would otherwise
print nothing and read as "there is nothing here to ship", which is how this command
could re-affirm the deferral over a fix that ought to go out.

Each names a section or an entry in this file and gives **only its place in the
order and what that place turns on**. What the item *is* stays where it is: a second
copy here is the duplication `mise run docs-scan` reports.

1. § `kae account rename` leaves a bound directory's store under the old name — a
   **measurement before a fix**: that entry and the comments on the code printing the
   warning answer differently, and reading settles neither. It lands as a test.
2. § Delete the prose that is not load-bearing, on the target that section names as
   what is left — and the deferral above removes the conflict a new release entry
   would have had with it.
3. § `kae relogin` declines to capture a login it watched happen when a *sibling*
   directory has drifted — last, because it overrides the shared attribution
   predicate, whose residue § Attribution reads a label kae may have written itself
   keeps open, and because no release date is now compressing the measurement that
   entry asks for.

An item leaves this list when it lands, so what is here is what is left rather than
what was planned; git log holds the order it was worked in.

What stays out does so for unlike reasons. The freshness surfaces' wording waits on a
**result**: [ACCEPTANCE.md](ACCEPTANCE.md) § Real-machine gate — does
`refreshTokenExpiresAt` predict the login's death? is what decides whether kae
over-warns, and wording written first would be the unmeasured claim
[AGENTS.md](../AGENTS.md) refuses. **The part of that gate nothing can compress is
the waiting**, which is why it is started ahead of these batches rather than at tag
time — but do not read that as cheap, because the waiting is not all it asks for:
its Procedure is where what it costs and what the first step must record are
stated, and neither is inferable from the deferral above. Declaring
codex's `KeychainDirBindable` waits on [ACCEPTANCE.md](ACCEPTANCE.md) § Open gates
the same way, and this file's § Hardening backlog entry for codex's keyring store
says what the result unblocks.

This section goes when the list above has landed, and the sentences naming it go too.
Removing it turns `docs-check` red on [RELEASE.md](RELEASE.md)'s citation, so that
one announces itself — measured by renaming this heading and reading the failure.
The other does not: this file's own opening names this section in the bare form no
citation grep reads. What each item cost stays with its entry.

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
   which turns the naming-agreement check in [ACCEPTANCE.md](ACCEPTANCE.md)
   § Bound-directory credential store into a script.
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

- **A test that forgets to install a runner is silent, and it writes to the machine
  it runs on** (recorded 2026-08-09, **not fixed**). `runner.Default` falls back to
  `OSRunner`, so a test that never calls `runner.With` executes the real program and
  passes on a developer's laptop. Two live instances were found the day CI first ran
  on the v0.17.0 branch, and only one of them failed anything: `pinHereAs` ran the
  real `security` and wrote **956** items into the operator's login keychain across
  two days of `mise run check` — the gate AGENTS.md says must never touch the real
  environment — while on linux the same call had no `security` and failed two tests.
  The second, `TestR6OnlyClaudeCanBeDeclined`, only *read*, so nothing failed
  anywhere; what it silently did was let the operator's login state decide which
  tools the guard inspected.
  Both are fixed at their call sites, and that is what makes this worth recording:
  **a per-site fix does not close the class**, because the next omission is just as
  quiet. The seam that would is a `TestMain` in `internal/cmd` installing a runner
  that fails loudly, with the argv, on any command a test did not opt into. It is not
  the ten lines it looks like, and three measurements bound what it would actually
  take.
  First, `go test ./...` issues **171** real `git rev-parse --git-common-dir
  --show-prefix` calls through the seam, all legitimate — `ensureGitExcluded` needs a
  real repository layout — so the guard has to name the credential programs
  (`security`, `secret-tool`) rather than refuse everything, or it fails the wrong
  things. (`TestEnsureGitExcludedLeavesEveryWorktreeClean` makes nine more deliberate
  git calls, but through `exec.Command` directly rather than the runner, so they would
  be invisible to such a guard and cannot constrain its design either way.)
  Second, **that exemption leaves a hole in the same breath**: real git means
  `ensureGitExcluded` appends to the `$GIT_COMMON_DIR/info/exclude` of whatever
  repository contains the temp dir — measured at **292 lines, 146 entries, per suite
  run**, and it accumulates run over run. The defaults on macOS and ubuntu put
  `TMPDIR` outside any repository, which is the only reason this is not already
  happening — but `mise.toml` reads `TMPDIR` in nine places, so a per-project value
  would write those lines into kagikae's own `.git/info/exclude`. A denylist that
  exempts git cannot see it; bounding `TMPDIR` for tests is the separate half.
  Third, `runner.Default` is **one of three seams**. `runner.RunInteractive` and
  `runner.RunWithEnv` are their own package-level vars with their own overrides
  (`withInteractive`, `withRunWithEnv`), and a `TestMain` on the first would not touch
  them. `RunInteractive` inherits the operator's stdio and takes an arbitrary child
  command, so a forgotten override there is worse than a forgotten `runner.With`;
  instrumenting all three seams shows no live instance of either today (the only
  non-git execs through `runner.Default` are `cat` and `false`, from
  `internal/runner`'s own tests).
  Deliberately not done at a release boundary; the two call-site fixes hold until then.
  A **separate** gap surfaced by the same review and older than it: `keychainSim` can
  catch a keychain item deleted under the *wrong* account but not one deleted with **no
  account scoping at all**. Its delete arm is
  `want := valueAfter(args, "-a"); want != "" && …`, so dropping `-a` short-circuits to
  a match, the delete is recorded, and `TestUnpinPurgeRemovesACredentialNothingElseBinds`
  — which asserts only that a delete happened — passes. Measured: replacing
  `DeleteItemForAccount(…, sp.KeychainAccount)` with `DeleteItem(ctx, sp.Target)`
  survives the whole suite, identically before and after the fixture work. That is the
  v0.12.0 defect's own shape (an item addressed by service alone), so the sim should
  refuse an un-scoped delete outright rather than treat it as a wildcard.

- **Two assertions in the harvest block still pass when the thing that reads the
  snapshot is broken** (recorded 2026-08-09, **not fixed**). In
  [VALIDATION.md](VALIDATION.md) § Harvesting a credential, cases `B2` and `B3`
  pair `snap main | grep -c FOREIGN` with a positive line on the **store**
  (`grep FOREIGN "$(accstore main)"`) rather than on `snap()` — so a `snap()` that
  returns nothing at all prints `0` and reads as a pass. That is the defect the block's
  own closing prose records fixing once already, for `B1` on 2026-08-07; it was fixed
  where it was found and not as a class. Re-measured 2026-08-09, two more are partial
  rather than absent, which is why the earlier record of this ("`A3`, `B2`, `B3` and `E`
  do not [have one]") was too strong: `A3` carries an explicitly labelled control
  (`test -d "$(store)"`) for its `test ! -e` line but none for its `snap` line, and `E`'s
  `snap main | grep MAIN-NEW` immediately above its negative *is* a real control on
  `snap()`, weakened only by naming a different account than the negative does.
  Not fixed here because the edit and the evidence are not the same size: one positive
  line per case, but verifying it means extracting the whole 398-line block, running it,
  and then breaking `snap()` on purpose to prove each new control bites. An unverified
  edit to the block a release is accepted against is worse than a recorded gap: this block
  spent 2026-08-07 to 2026-08-08 sending every credential fixture to the config dir after
  the credential had moved to the account store, and went green throughout by asserting
  nothing about its own subject. (The long-accumulation claim belongs to § Smoke Checks,
  whose checks stayed *comments* from the initial commit until 2026-08-09; this block was
  added 2026-08-04 and has only ever shipped in v0.17.0. Conflating the two is the same
  misattribution this entry is named after, made while writing the entry. Two release
  counts were attached to that claim and both were withdrawn — "eight versions", which
  was sourceable nowhere, and "14 releases", which counts releases since the initial
  commit rather than anything about the block: that is byte-identical to v0.9.0's for
  only six tags, and was edited at v0.13.0 and v0.15.3 as well as repeatedly earlier.)

- **The smoke guards have no test, and four changes switched one off without
  anything noticing** (recorded 2026-08-09, **not fixed**).
  `scripts/smoke-run-selftest.sh` checks `scripts/smoke-run.sh`; nothing checks
  the selftest. **There are two selftests now** — `scripts/check-docs-selftest.sh`
  arrived 2026-08-11 for `scripts/check-docs.sh` — and this entry covers both: neither
  is checked by anything. The docs selftest reaches every floor in the script it tests
  **except the Map's table-row count**, via degenerate input — an empty `docs/`, an
  extractor emitting nothing, and a renamed Documentation Map heading — and each was
  verified to fail when its floor is deleted. Renaming that heading trips two floors with
  one mutation but asserts one of them, which is why it is one case. That one exception is
  hand-verified in a commit message rather than by any check here, which is the state
  described below. It used to be two: the other was the derived required set, and the
  floor over it is gone with the derivation itself, which went when this repository
  stopped carrying a copy of the standard to derive it from ([AGENTS.md](../AGENTS.md)
  says why). Its replacement is not a floor and needs none, because a floor bounds a walk and
  these are single tests: a three-sided predicate — missing, empty, or not a regular file —
  over the root documents `README.md`, `AGENTS.md` and `CLAUDE.md`, each arm reached by its
  own case. Do not read this as one file and one dimension; the derivation of which documents
  belong there, and why the link walk cannot vouch for them, is in `scripts/check-docs.sh`
  above that predicate. Across six review rounds, four separate edits made for unrelated
  reasons left a guard passing unconditionally while the suite reported every
  guard holding: a variable list derived from the file it was testing (deleting
  from the subject deleted the test); a containment check weakened to a proxy
  ("not the value I planted" rather than "inside the sandbox"); a fixture count
  shortened from 300 to 257 for speed, where `257 % 256 = 1` is exactly the exit
  status a working runner returns; and a guard whose empty input fell into its
  own success branch. Every one was found by a reviewer mutating the guard by
  hand, and none by any check in the repository.
  The fix is a committed mutation table — roughly 30 lines plus the ~39
  substitutions already used across those rounds, each paired with the guard name
  it must trip, asserting every one is caught. It would have caught all four,
  including the `257`, which no structural rule catches because the table does not
  reason about the value, it only checks that the guard still fires.
  Deliberately not built now: the scripts are closing and stable, so it would be
  machinery with no near-term change to protect, and it costs a full selftest run
  per mutation. Two partial rules are cheaper and are worth applying at review
  time instead — **no guard's pass condition may be satisfiable by an empty or
  degenerate input** (that covers three of the four), and **state a fixture
  value's requirement as a property, not a number** (the only defence against the
  fourth, and it works by making the next editor's mistake visible rather than by
  detecting it). Until the table exists, the header of
  `scripts/smoke-run-selftest.sh` is normative: any change to either script has to
  be mutation-tested by hand.
  Three rounds of review on the assert-marker guard (2026-08-10) put a sharper
  version of those two rules on the record, because each round's survivor sat on the
  guard the round before had added: check that a guard fires **before** the thing it
  protects (move it later and see if anything notices); assert the **diagnostic**, not
  only the verdict; try a **proper subset** of any enumeration, not just its collapse;
  ask **where in the file** a derived expectation reads from; and pin a character class
  with two members and a count, since one example pins one example. Every one of those
  was a live false green here, found by a reviewer and not by the author.

- **`derived_cleared` reads the whole file, so a comment can satisfy it** (recorded
  2026-08-10, **not fixed**). `scripts/smoke-run-selftest.sh`'s
  `grep -oE '\-u [A-Z_]+' "$runner"` is unanchored, so a `-u NAME` written in a comment
  counts as the runner clearing that variable. Its sibling `derived_all` is anchored at
  the prefix's indentation, and `derived_words` was anchored at `^markers=` after
  exactly this defect was measured on it: a comment quoting the retired pattern let the
  code be narrowed while 35 guards reported holding. The remaining instance is the
  weakest of the three because a stray `-u NAME` in a comment is less idiomatic here
  than quoting a retired pattern, which the runner's header does three times — but it
  is the same hole. Anchor it at the `env -u …` continuation lines.

- **CI runs a subset of the gate; `docs-check` was the one step whose price fell**
  (recorded 2026-08-11, `docs-check` **added 2026-08-14**, every other step still
  undecided). `README.md` said CI "mirrors it" and `check.yml`'s own header said it
  mirrored the local gate; both have said subset since 2026-08-11, which was the half
  that could be fixed without deciding anything. Derive the two step lists rather than
  reading one written here — `mise.toml`'s `[tasks.check]`, through `lint`, which fans
  out again, against `check.yml`'s own steps — because hand copies of the local list had
  already drifted apart, which is what `[tasks.check]` is the one copy of and which
  [VALIDATION.md](VALIDATION.md) names.
  The argument for widening is the entry above about the real `security` binary: that
  defect passed on darwin, failed on linux, and a single-environment gate could not see
  the difference. Against it, and per step rather than in general:
  `docs-check-selftest` copies the tracked tree once per case and runs the whole check on
  each, `smoke-selftest` perturbs `.git/info/exclude`, and the lint tools resolve pinned
  versions over the network on first use — so each needs a decision about caching and
  about what a CI runner is allowed to touch, not just a line in a YAML file.
  **`docs-check` stopped being in that list**, which is what changed and why it went in
  alone; its selftest is the only objection above that touches it at all, and it stayed
  out. `check.yml`'s header owns what the step costs and is the copy to read. The one
  thing that belongs here rather than there: the first draft of that header claimed the
  step "writes nothing outside the checkout", and a review measured it false — the
  argument for admitting a step is exactly where an absolute is worth least.
  **No timing is quoted here on purpose**: every absolute measured while this was written
  disagreed with every other, because the machine was running several agents at once —
  one reviewer had the compiled extractor at 727ms and another had `go run` at 150ms in
  the same session.
  Nothing else should be widened silently. Every place that describes **CI** has to say
  the gap — a place that describes only the local gate, as `AGENTS.md` § Validation does,
  owes nothing — and there is no list of them here on purpose: the commit that added this step
  repointed every such place it found, and a review then found one it had missed —
  `RELEASE.md`'s live release procedure, which enumerated part of CI's step set inside an
  instruction rather than in a frozen release entry. `check.yml`'s own header owns why
  the selftest stayed out; it is the copy to repoint the others at.

- ~~**One paragraph in `PRODUCT.md` is architecture**~~ (recorded 2026-08-11, **fixed
  2026-08-14**). The `**Mechanisms.**` paragraph is now
  [ARCHITECTURE.md](ARCHITECTURE.md) § Switch Mechanisms, and § Concurrency Boundary is
  folded into § Product Boundaries as its opening paragraphs.
  What is kept is why the first draft of this entry was wrong, since nothing in the
  result records it: that draft named § Switching Surface and § Concurrency Boundary as
  whole sections and priced a section-level move, because it classified by the **topic
  word** (*locks*, *mechanisms*) rather than
  by the question the passage answers. Reading them said otherwise — a scope ×
  environment table plus verb semantics is a primary journey, which the standard's
  charter for `PRODUCT.md` explicitly includes, and a user-visible limit on what can run
  at once is a product boundary, while its mechanism was already owned separately by
  [ARCHITECTURE.md](ARCHITECTURE.md) § Locking with no overlap. So the work was a
  paragraph move plus a same-file fold, and it cost nothing in citations: everything
  `git grep 'Switching Surface'` finds names the heading, or the table under it that
  `scripts/docscan/main.go`'s calibration note pairs against `RELEASE.md`, and the move
  leaves both in place — while no citation anywhere resolved to § Concurrency Boundary,
  which is why folding it repointed nothing. Derive both rather than reading a count
  here; what counts as a citation is where the figures in this tree disagree. Both greps
  also match this entry's own prose, which names both sections, so neither is ever empty
  and neither answer is the hit count.
  One thing the entry never mentioned and the fix had to carry: `PRODUCT.md`'s own
  opening still said both sections read as architecture and were queued for a move, a
  claim this entry had already withdrawn above. That is the instance;
  [AGENTS.md](../AGENTS.md) § Documentation Update Checklist states the rule it produced.

- **The standard's own docs check cannot run clean, and this repository forked it instead
  of fixing it** (recorded 2026-08-11, **not fixed here** — the fix belongs upstream; the
  duplication half is gone, see the end of this entry).
  `scripts/check-docs.sh` kept the required-file list and the link walk from the standard's
  `assets/template-project/scripts/check-docs.sh` — reachable in the user-level
  `go-cli-tooling` skill, since this repository no longer carries a copy of the standard
  ([AGENTS.md](../AGENTS.md) says why). It re-implemented both, because both are wrong
  there, and every tool on this standard will hit the same two defects. First: that script
  asserts only the files under `docs/`, while its own § Required Files also names
  `README.md`, `AGENTS.md` and `CLAUDE.md` — described and then checked by nothing.
  Second: its link extractor is a bare `grep -Eo` with no fence or code-span stripping, so
  it reports the bracketed example inside `` `[X.md](X.md)` `` in `AGENTS.md`'s citation
  rule as a broken target — **the standard's script cannot pass on a repository that uses
  the citation idiom the standard itself teaches**, and since the template wires it into
  `mise run check` the symptom is a gate blocking a commit on correct prose.
  Both fixes are properties of markdown rather than of kae, so they belong in the chezmoi
  source.
  What is already settled: dropping the bundle removed the required-file half of the
  duplication rather than fixing it. That half existed only to track the standard's
  § Required Files, which cannot be tracked from here now, and re-deriving it from a
  hand-copied literal is the defect the derivation was built to avoid — so the check
  shrank to what is genuinely kae's, the Documentation Map shape plus `CLAUDE.md`, which
  no link reaches, and its header records what deleting the derivation cost. The link walk is
  still a second implementation, which is why this entry stays open. Note that
  `mise run docs-scan` cannot see any of it: that program compares `.md` files, and this
  duplication lives in a shell header and a Go package comment. It said "shell and Python
  headers" until the link extractor stopped being Python — the concept survived the sweep
  that repointed the two hits naming the old directory, which is the class this file's own
  entries keep describing.

- **The claim-reconciliation stage has one slice in the gate and no general
  implementation** (recorded 2026-08-10 as unbuilt, **partly built** 2026-08-13).
  `scripts/docscan/main.go`'s header names a
  four-stage docs scan; stage 2 (duplication) is what that program does, and stage 3 —
  reconciling a claim one document makes *about another* against what the other one says
  — has no general implementation. One slice of it is now in the gate:
  `scripts/docrefs` refuses a citation naming a section its target declares
  nowhere, which is the narrowest claim-about-another-document there is — the target's
  own headings answer it, with no reading needed. **Read its header for what it does not
  reach before trusting a clean run**; the list is there rather than here because it has
  already grown once, and a copy of it would go short the way every enumeration in this
  tree has. What that slice does **not** touch is the shape the rest of this
  entry describes: whether the content under a name that does exist still says what the
  citation claims.
  `AGENTS.md § Documentation Update Checklist` covers one shape
  of it: a quantity written beside a `§` citation, swept for by
  `scripts/sweep-quantities.sh` over the diff's added lines and triaged by hand. That sweep
  is a net rather than a proof by its own admission, and what it still misses is the
  argument for tooling rather than prose — **that script's header** is normative for the
  list, and listing it here as well would go short the moment a review round finds another,
  which has happened. Two of the holes it used to disclose were
  closed instead, by joining each line to its predecessor and allowing words between the
  number and the noun; what no widening of an added-lines sweep can reach is the shape
  this entry exists for, **a quantity on a line the diff never touches**.
  Stage 3 needs a definition covering the claim-forms seen so far, and they do not all
  have the same standing: a quantity beside a `§` citation and a count of something in the
  writer's *own* file — which one diff can change while writing it — have each been
  measured here, and so, since 2026-08-13, has the file-level analogue of it: a
  reference naming *content* of a document that still exists, after a trim removed
  the content. Trimming this file's shipped records left one in every surface that
  cites this file — release notes by a wide margin, and a design-rationale document,
  an acceptance log, a user-facing error message and code comments — each found only
  by reading every reference to this file, after a detector written for the class
  failed its own positive control. Count them from the diff rather than from a number
  written here. The `§`-heading form above stays unobserved and is the harder one: a
  `§` grep sees that the heading exists and never what is under it.
  Not queued, and that header is
  where the yield argument is normative rather than here: every release-breaking docs
  defect so far came from stage 4, running the executable blocks.

- **One directory has two names, and the glossary states a preference the tree does
  not meet** (recorded 2026-08-10, **not fixed**). [CONTEXT.md](CONTEXT.md) names
  **bound directory** as the term and **pinned directory** as the one to avoid, and
  both are still in use — in the user-facing docs and under `internal/` alike, so
  neither is a register the other stays out of. It is filed rather than done because
  it is a rewording of a few dozen sites with no behaviour attached, and nothing about
  it needs to ride with a code change. What it costs meanwhile is a grep: a reader who
  searches for either word finds part of the subject and cannot tell that from all of
  it. Derive the split with the command in CONTEXT.md § Not converged rather than
  from a number quoted anywhere, including this entry — the definition of what counts
  is the whole disagreement in figures like this one. The sibling convergence,
  `witness` → `reader`, is done (`credStoreReaders`), and it is the reason this one is
  visible.

- **`scripts/smoke-env.sh` leaks a temp HOME every time it is sourced** (recorded
  2026-08-09, **not fixed**). It does `HOME=$(mktemp -d)` and nothing ever removes
  the directory, so each direct run of a block in [VALIDATION.md](VALIDATION.md)
  leaves one behind under the system temp dir, holding that run's fixtures.
  Harmless — the contents are placeholders (`tok-A`, `you@example.com`) and the OS
  reclaims the directory eventually — and deliberately left alone, because the
  preamble is sourced *into the caller's shell* and has no place to hang a trap
  that would not also fire on the caller's own exit.
  **Running blocks through `scripts/smoke-run.sh` does not fix it, and the first
  version of this entry said it did.** The runner cleans up its own sandbox and
  sets `TMPDIR` inside it so a nested `mktemp` would land there — but darwin's
  `mktemp` ignores `TMPDIR` entirely, and the block's *own* `. scripts/smoke-env.sh`
  is what allocates the leaked directory. Measured on darwin 24.6.0: one full run
  of § Smoke Checks through the runner took the system temp dir from 1239 to 1240
  entries, exactly as a direct run does. On linux the `TMPDIR` would take effect and
  the claim would hold, which is why it read as true.
  Recorded because it is invisible: the directories are indistinguishable from every
  other `mktemp -d` on the machine, so nothing counts them and no run reports one.

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
  ([CLI.md](CLI.md) § `kae doctor --json`). The bomb itself is unchanged — only one copy can refresh, and the entry
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
  mechanism and the refusals, and [CREDENTIAL-RULES.md](CREDENTIAL-RULES.md) § A credential that belongs to an
  account, not a directory carries the traps. Two
  things the plan above did not name, recorded because both were found by review
  *after* a version that looked complete and passed its tests: **attribution** (a
  shared store is account-agnostic, so a re-bind finds the previous account's copy
  there and filing it under the new name would be undetectable afterwards), and that
  **a chokepoint is not coverage** (the write path cannot see the store a re-bind to
  another account is moving *off*, which is why there is a pin-level pass at all; this
  said "a mode toggle or an isolated re-key" until 2026-08-08, and a toggle for one
  account moves only the sessions).
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
  ~~**`run -s`'s own recapture goes
  through neither guard the switch-away recapture applies**~~ and ~~**the switch-away
  recapture's attribution guard has no decodability gate**~~ — **both fixed 2026-08-07**,
  and together, because the first newly depends on the second. `run -s` called
  `captureSnapshot` directly, so a child that logged in as another account filed that
  credential *and* that identity under the target account's name, and a child whose
  refresh failed filed the tombstone; it now applies `keepSnapshotIdentity` and
  `recaptureWouldDowngrade`, **those two and no third**.
  **And the prescription this entry used to carry for the second half was wrong — it is
  withdrawn, measured.** It said `keepSnapshotIdentity` should "route that comparison
  through `identityComparable` too", adding a refusal to `kae use`. Built that way, the
  only newly-refusing shape is two identity payloads that are both non-records **and
  byte-identical** — and that is the shape *kae itself* produces, because applying a
  snapshot writes its recorded identity into the live cache, so a recorded
  `/oauthAccount: null` makes both sides that same non-record. No login can leave it,
  since `/login` rewrites accountUuid/emailAddress unconditionally. So the refusal fired
  exactly where nothing had happened, and on `run -s` it **destroyed the child's
  refreshed credential** — a logout reported as success, reproduced against `bf77135`
  where the unguarded recapture had kept it. Every other combination (a record against a
  non-record, two different non-records) already refused through `identityDiffers`' byte
  fallback and was unchanged.
  What shipped instead: the gate decides the **wording, not the decision**. The refusal
  set is exactly what it was — `identityDiffers`, byte fallback included — and
  `identityComparable` only chooses between "somebody else is logged in" and "kae cannot
  read the records it would compare", so a claim kae cannot support is no longer made
  about the ordinary case where claude has cleared its cache. The general lesson is the
  one [CREDENTIAL-RULES.md](CREDENTIAL-RULES.md) § When a refusal destroys instead of
  preserving already states and this entry did not apply: refusing is
  the conservative answer for the two sibling guards, which decline to *overwrite* or
  *delete*, and the destructive answer for a recapture, which declines to *preserve*.
  Porting a predicate across that asymmetry needs the caller's question re-asked.
  The reporting this leaves undone is the other entry below (a recorded identity that is
  not an account record), whose prescription — report the broken **label**, not the
  comparison — is the right home and is unaffected.
  Separately, a refusal on `run -s` was destructive even where refusing is *correct* (a
  child that really did log in as somebody else): its backup predates the child, so the
  declined copy lived only in the store the restore overwrites. It now takes a second
  backup (reason `run-unattributable`) of the post-child state and names it in the
  warning. `kae use` needed nothing — `createBackup` already runs before its recapture,
  which is the difference "the same two guards and no third" silently assumed.
  Three things worth keeping. The plan named two defects and there were **three**:
  `persistSnapshot` builds the snapshot from `plan.Identity`, which the run paths never
  set, so every `run -s` blanked the account's *recorded* login identity — a different
  field from the identity payload, found by measuring rather than by reading, and fixed by
  carrying `plan.Meta.Identity`. `docs/VALIDATION.md` case H asserted the defect's shape,
  so it flipped in the same commit as predicted. And the 2026-08-05 measurement still
  stands as the reason this mattered: `kae doctor` did report `identity_drift` afterwards,
  but its remedy was `kae use <tool> <account>`, which put the foreign credential into the
  real home — the reporting surface made it worse, so detection was never the fix.
  The restore skip above is gated on attribution so it did not compound this, and it
  reads the **backup** rather than the snapshot precisely because the snapshot may
  already be wrong by then. **A
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

- **The reader walk runs twice per bind, and a third walk of live bindings now exists**
  (recorded 2026-08-08 by a quality pass, **not fixed**). `credStoreReaders` reads the pin
  index and every bound directory's fragment, and both the pin-level pass and the write call
  it — with nothing between them that changes the answer. On those two it sits behind the
  `supersedes`
  gate, so it costs nothing unless there is a copy worth harvesting; where that stops being
  true is `kae run -i`, which the mise hook makes a per-invocation path, and where the
  per-reader `dirSpecs` resolution stops being free is the day a second tool's rotation is
  measured (codex's `Artifacts` can probe the keychain). **`kae doctor` became a caller that
  is not behind that gate** when `credential_superseded` started attributing a shared store
  by its readers: it walks once per account whose bound stores can be ordered — the ordinary
  state, not a rare one — memoized within a group and not across them. The fix is a per-command memo, and
  the reason it is not an `App` field is that this package has already had one of those make
  a test pass for the wrong reason without a per-operation reset.
  Separately, that walk is the **third** written over the same source: `credStoreRefs` shares
  its mechanics exactly (including the `dirExists` gate whose only consequence is the ENOTDIR
  case, documented on one of them and not the other), and `boundDirStores` shares them with a
  different error policy. Sharing them means giving `boundDirStores` the completeness signal
  it currently swallows, which changes what every doctor consumer sees on an unreadable
  fragment — refuse-versus-skip, the seam where this area's defects live — so it wants its
  own change and its own review rather than a ride-along.

- **The pin index can be incomplete with nothing saying so** (recorded 2026-08-16, **not
  fixed**). `pinnedDirsComplete` answers `complete=false` when a directory under the
  isolation root has a pin record kae cannot read, or one that is empty. The consumers
  that read that flag refuse on it: `credStoreRefs`, where refusing keeps a credential,
  and `credStoreReaders`, where since `credential_superseded` began attributing a shared
  store by its readers refusing means `kae doctor` says nothing about **any** shared
  store on the machine — including for accounts that record has nothing to do with.
  Nothing reports the condition itself. `pinChecks` takes its pins through `pinnedDirs`,
  which drops the flag, so an unreadable pin record produces no `pin_stale` and no other
  signal, and the directory it names is simply absent from every walk. The silence is
  pinned by `TestSupersededGoesSilentWhenThePinIndexCannotBeEnumerated`.
  Refusing is the right direction in both consumers — attribution needs positive
  evidence, and a reader kae could not enumerate is the one that might disagree — so what
  is owed here is the **signal**, not a weakening: a doctor check on that flag, which is a
  new code and its own change. Recorded rather than taken because the trigger is a pin
  record written from outside kae (`recordPinnedDir` writes atomically), which is why the
  empty-record branch is the one measured unkillable on 2026-08-07 and carries its guard
  as a comment rather than a test.

- **A mode toggle and a same-mode re-pin answer a poisoned store differently** (recorded
  2026-08-08 by a reading-type review, **not fixed, and deliberately so**). `Conflicting`
  — the refusal that still overwrites — requires the directory the operation acts for to be
  one of the readers that disagree. A same-mode re-pin satisfies that (its config dir is the
  reader); a `-s` ↔ `-i` toggle does not, because the reader is derived from the fragment
  and still names the *previous* mode's config dir while the operation acts for the new
  one. So a directory someone logged into as another account is switched back by
  `kae pin <same mode>` and kept by `kae pin -i`. Aliasing the previous-mode dir would make
  the toggle replace, and what it would replace is a live login with no snapshot anywhere;
  keeping is the answer [CREDENTIAL-RULES.md](CREDENTIAL-RULES.md) § Never harvest a copy
  you cannot attribute settles on, and the one the code before the reader model
  also gave (a fresh isolated config dir has no cache, so attribution refused for missing
  evidence). What would settle it properly is deciding whether `Conflicting` should
  overwrite at all when the account it names has no snapshot to fall back on — which is the
  same question the `--purge` exceptions turn on, and a wider change than this one.

- **A moved bound directory does not count as a reader, and its absence does not make the
  reader set incomplete** (recorded 2026-08-08 by a reading-type review, **not fixed**).
  `credStoreReaders` skips a pin whose recorded directory is gone and leaves `complete`
  true. For a *deleted* directory that is right and there is no alternative: `kae unpin`
  removes the fragment but never the breadcrumb, so one deleted temp worktree would
  otherwise mark the set incomplete forever and silently stop every harvest for every
  account. A directory that was **moved** is the case that pays for it — the fragment
  travelled with it and still exports the old credential store, so it is a live reader kae
  cannot read at the path it recorded, and a stale confirming reader elsewhere can license
  a harvest it would have disagreed with. What would settle it is a reader set that does
  not depend on the recorded path (`pinChecks` already reports the orphaned store, so the
  user is told something is wrong), or a breadcrumb that `unpin` removes so absence can
  mean incompleteness again.

- **`kae relogin` declines to capture a login it watched happen when a *sibling* directory
  has drifted** (recorded 2026-08-08 by a reading-type review, **not fixed**). Two
  worktrees bind one account and the second one's identity cache names another (an
  unresolved `identity_drift`). `kae relogin` in the first runs the tool's own login flow,
  the tool writes its own cache there, and the store now holds the fresh login — but the
  reader set disagrees, so the harvest refuses and the account snapshot keeps a copy the
  new login has already invalidated, with no way to update it until the drift is resolved.
  The message names the disagreement, so nothing is silent. The fix is to let the directory
  the flow ran in answer for a login kae **itself** just performed there — evidence of a
  different class from a walk of everyone's caches — which is a deliberate override of the
  reader set and wants its own measurement rather than a late edit to the shared predicate.

- **A relogin's pre-flight refusal owes a backup it cannot safely take yet** (recorded
  2026-08-08 by an independent review of the pre-flight itself, **not fixed**).
  [CREDENTIAL-RULES.md](CREDENTIAL-RULES.md) § When a refusal destroys instead of
  preserving states the rule the pre-flight falls under: a refusal that
  cannot preserve is a deletion, so it owes a backup the way `kae run -s`'s recapture
  answers its own with reason `run-unattributable`. `preserveBeforeRelogin` refuses on
  exactly the copy `kae pin` kept and pointed at this command — the two route through one
  `harvestDirCredential`, so a copy the bind could not attribute is a copy the relogin
  cannot attribute either — and then the tool's login replaces it. Today that is loud
  rather than silent, and the action that prevents it (not completing the flow) is still
  available when the warning prints; it is not recoverable.
  What stops the backup being a ride-along is **where a restore of one would land**, which
  is the half a reading of `createBackup` alone does not reach: `createBackup` records the
  spec it is handed, but `applyBackup` re-resolves today's specs **globally**, and
  `restoreSpec` prefers the live spec whenever its `Kind` differs from the record's. A
  bound store's backup taken under one credential driver and restored under another
  therefore writes into the **real home** — a global logout in place of a local one, which
  is worse than the loss it insures against, and the same "a record from one environment
  applied in another" shape the `keychain_account` removal in v0.16.0 turned on. So the
  backup wants the restore path to understand a bound-store record first (or an explicit
  `--to`-only class of backup that is never redirected), which is its own change and its
  own review. Nothing about it is urgent while the refusal is loud and pre-flight.
  **It was built and then withdrawn before v0.17.0 shipped** (2026-08-09), and what the
  attempt measured is worth more than the entry above. The restore-landing hazard is *not*
  what stopped it: a recorded `BoundStore` field on the meta, read at one predicate, gated
  it — and gating one consumer was the first defect, because the hazard is a **class**, not
  a check. kae's backup subsystem rests on an unstated invariant, *a backup records the
  tool's global live state*, which 7 of its 13 consumers had encoded; four independent
  execution reviews each surfaced one more consumer still assuming it (an identity sweep
  that cleared the **real home's** cache, an `ActiveBefore` restore that moved the globally
  active account, a superseded-credential warning that ordered two unrelated chains, and a
  producer handing a global record to a meta marked not-global). Two of those compose into
  filing one account's token under another's name.
  One question the attempt never settled, and a retry must settle **first**: what a bounded
  preserved side should evict. Bounding it made an aborted `kae relogin` — a run that
  changed nothing — evict the previous run's preserved copy, and eviction is purely
  positional, so it took the *irreplaceable* copy (another account's only login) and kept
  one still live in the store. Retention has no notion that a preserved copy whose payload
  is still live is worth less.
  So a retry starts by enumerating that invariant's consumers and deciding the eviction
  rule, not by writing the backup. The warning half shipped and is unaffected: it produced
  zero findings across all four rounds, and it is what turned this from silent to loud.

- **A payload kae can neither read nor date is still overwritten by a bind, and that is
  a decision rather than an oversight** (recorded 2026-08-08, **not fixed**). The bind now
  keeps a newer copy it could not *attribute* in the account's credential store, because
  refusing there would otherwise destroy it. The sibling refusal — a payload kae cannot
  parse or date, which [CREDENTIAL-RULES.md](CREDENTIAL-RULES.md) § What kae observed is not
  what the tool can do is explicit may be a working login in a shape kae has not
  been taught — deliberately kept the old behaviour: extending "keep" to it makes a
  corrupted or upstream-changed account store **unrepairable by `kae pin`**, leaving manual
  deletion of a path the warning names as the only way out. Both readings destroy
  something, which is why this is recorded rather than decided in a release fix. What would
  settle it is a way to repair without a destructive default — an explicit
  `kae pin --replace-credential`, or letting `kae relogin` own that repair — at which point
  "keep" becomes the safe default for this arm too. The warning is loud and precedes the
  write, so nothing here is silent.

- **Attribution reads a label kae may have written itself** (recorded 2026-08-08,
  **narrowed and still not fixed**). A successful bind writes the account's recorded
  identity into that directory's store, and attribution then compares the account's
  recorded identity against exactly that — so a reader whose tool has never actually run
  there confirms by construction. The second of the two candidate fixes below is the one
  that shipped (attribute from the directories currently reading the store,
  `credStoreReaders`), and it narrows this without closing it: the readers are now a set
  rather than the one directory being bound, so a directory that *has* run the tool
  disagrees and the harvest refuses — but a store all of whose readers are kae-labelled
  still confirms. Reachable: bind A to an account and never run the tool there, bind B and
  log in as somebody else, then re-bind B elsewhere; A is now the only reader, it agrees
  with kae's own label, and B's token is harvested under A's account. A **globally isolated
  home** is a strictly easier A than a bound directory: `prepareGlobalIsolatedHome` writes
  the label on every `kae use -i` / `kae run -i`, nothing ever removes such a home, and the
  reader walk reads them from disk without any liveness gate (deliberately — that source is
  what gives `kae run -i` a reader at all). One class of kae-written label **is** retracted now — a shared config dir's,
  once the bound account changes — but that is the leftover kind, and this entry is about the
  kind a *current* binding wrote, which stays.
  **The heading understates it, and the candidate fix below does not close what it
  says it closes** (measured 2026-08-08 by an independent review, both variants of the
  sequence above end to end, plus the globally-isolated one). Run A's sequence with a
  label the **tool** wrote — an honest one, naming A's own account, carrying keys kae
  never writes — and the harvest mis-files exactly the same. Provenance is not the
  property that fails: an identity cache records the last login *observed in that
  directory*, and the store it is being read as evidence about can have been rewritten
  since by somebody else. So a marker saying "the tool wrote this" would be read as
  trustworthy on precisely the run that mis-files. Whatever settles this has to make a
  reader's silence depend on **when** it last observed the store rather than on who
  wrote its cache — which is a different mechanism from the one below, and the reason
  this entry stays open rather than shrinking to an implementation task. Recorded
  because the wrong fix here is cheap to build, looks like it worked, and its failure
  is the undetectable kind. What remains of the
  first candidate fix — record whether a cache was written by kae or observed from the tool
  — is what would let `identity_drift` tell a stale label from a real one, which is worth
  having for its own sake. Do
  not "fix" it by removing the confirmation: that would make every bind keep forever.

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
  ([CREDENTIAL-RULES.md](CREDENTIAL-RULES.md) § Removing a per-directory credential carries
  the correction and its two caveats), so a doctor check can list
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

- **A `run -s` backup that fails half-way now leaves payloads nothing points at**
  (recorded 2026-08-07, **not fixed**). `createBackup` writes the secret payloads first
  and the metadata last, and every other call site aborts its command when it errors. The
  refusal backup added in v0.17.0 deliberately does not — a warning and a continue, because
  failing the whole run would be worse than losing the preserved copy — so a failure
  between the first payload write and `backup.Save` leaves `backup/<id>/…` entries in the
  secret store with no metadata naming them. Nothing sweeps or reports those: `Prune` and
  `Delete` walk metadata, and `doctor`'s `secret_orphan` skips every key that is not an
  account key by construction (a backup key has no snapshot dir behind it, so it cannot be
  judged the way an account key can). Recorded rather than fixed because the leftover is a
  secret nobody reads, which is smaller than the logout the backup prevents, and because
  the fix belongs with whatever next audits "which keys can kae account for" — the same
  question the per-directory keychain-item entry above asks about items.

- **An upstream `expiresAt` format change would make every recapture decline, forever**
  (recorded 2026-08-07, **deliberately not fixed**). `orderable` requires a deadline kae can
  read, and every consumer that cannot order two copies now declines rather than guessing —
  which is right, and self-limiting only as long as the undated shape is rare. If upstream
  changed the field's type or units, *every* live copy would be undated: each `kae use` and
  `kae run -s` would refuse its recapture, the account snapshot would keep the last datable
  copy (dead after one in-tool refresh), and the live copies would accumulate in
  `run-unattributable` backups that `backup_keep` ages out. The offline signals exist — the
  refusals name the condition, and `doctor` has `upstream_version` — but nothing aggregates
  them into "kae can no longer date this tool's credentials at all", which is the finding a
  user would need. Not fixed because the trigger is a specific upstream change that has not
  happened, and a detector for it is a guess about the shape it would take; the assumption
  and how to re-measure it live in docs/VALIDATION.md's claude `expiresAt` row, which is the
  thing to check when a version bump makes this concrete.

- **A zero `expiresAt` is indistinguishable from one kae could not parse, and the adapter's
  comment claimed otherwise** (recorded 2026-08-07, **not fixed**). `EpochToTime` maps both
  `n <= 0` and a non-number to the zero time, so downstream nothing can tell claude's
  measured death certificate (`expiresAt: 0`) from a value whose type changed upstream. That
  cost nothing while both were swept; it started mattering when the per-directory sweep
  learned to keep everything it cannot judge, because a payload with a zero deadline and a
  token still in it is now retained rather than swept. The claude adapter's `Freshness`
  comment asserted that the zero was "translated here into `Revoked`" — it is not; the blank
  tokens are what set `Revoked` — and that comment is corrected, since the line it described
  is now load-bearing. Closing it means teaching `internal/freshness` to distinguish a JSON
  number from a non-number, which is the only real work in it, and then folding a numeric
  `expiresAt <= 0` into `Revoked` so death certificates sweep again. Not done here because
  the retained item is a spent secret rather than a lost login, `kae unpin --purge` now
  removes it, and the change reaches every `Fresher` rather than one call site.

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
     the acceptance gate below, not pending step 3, which has shipped.
  3. ~~**Own the item's lifecycle.**~~ **Done** (2026-07-30). A `pin -s` ↔ `pin -i`
     toggle and an isolated re-bind now sweep the keychain item of the store they
     supersede, and `kae unpin --purge` sweeps the current ones (plain `unpin` still
     keeps everything, so a re-pin restores the directory). The sweep covers a keychain
     item where the adapter declares it bindable — so it starts covering codex the
     moment the capability is declared, with no further work — and, since v0.17.0, a
     file credential that is no longer the copy its own store reads
     (`removeDirCredential` is normative). It also closed the same gap for claude, which had been creating
     per-directory items since v0.12.0 with nothing removing them.
  What is left is neither of those: the pin round-trip **is** on the real-machine gate
  ([ACCEPTANCE.md](ACCEPTANCE.md) § Open gates), and it has never been run. Declaring
  the capability (dropping codex from `bindableNotYetDeclared`) is what that result
  unblocks, and until it passes a pinned directory has no codex login until you log in
  inside it.
- **A tool that resolves its store from live state is modelled per artifact, not as
  a set.** codex's `auto` is the only such artifact today (the adapter probes and
  returns one spec), and the restore path reconciles a backup record against it.
  What would justify a deeper model — `Spec` carrying an ordered list of stores, the
  primitives resolving at write time, and `restoreSpec`/`refreshPlan` both going
  away — is a *second* liveness-resolved store: a third codex store, or any tool
  that migrates its own credential. Note the reconciliation would not fully
  disappear even then: "an absent record must never delete the store the tool moved
  to" and the whole-document-vs-pointer refusal are properties of the payload.
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
  ([CLI.md](CLI.md) § kae pin, `removeDirCredential` for the rule). What an index would
  still add is the *other* direction —
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

## Command-system expansion

Daily-use ergonomics, designed together as mise-style verbs so the surface
stays coherent rather than accreting ad hoc. Most of what is left is the
completion surface those verbs are reached through, and two unbuilt candidates:

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
- **Global mise tasks**: `kae mise init` writes the `ai-switch` / `ai-switch-tool`
  tasks (and their dynamic completion) into the project's `.mise.toml` only, so
  they exist where the tasks live. A `--global` option emitting them into the
  global mise config (`~/.config/mise/config.toml` or `~/.config/mise/tasks/`)
  would make `mise run ai-switch <TAB>` available in every directory. Scope
  addition; design before implementing.

## Platform coverage

- **Windows**: `%APPDATA%` layout, Credential Manager secret backend, lock
  implementation, `%USERPROFILE%\.claude` file-patch driver.

## Tier-2 tools — described, not queued

Everything in this section concerns agy, opencode, cursor or copilot, which are
**tier 2** ([PRODUCT.md](PRODUCT.md) § Tool Tiers): kae commits to global credential
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
  switching it ([ADAPTERS.md](ADAPTERS.md), [VALIDATION.md](VALIDATION.md)
  § Upstream Behaviour Assumptions owns the measurement).
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
- localized human output (Japanese)
- `kae shell init` convenience wrappers

## Delete the prose that is not load-bearing

**Owed work, not a record.** Run these **at `d089914`**, where they printed 13,457 /
1,943 / 22,778 and 164 of 285 — pinned to a commit because on any later tree the first
number counts this entry, and because a reading taken on a different tree is not
distinguishable from a change of method:

```bash
git ls-files '*.md' | xargs wc -l | tail -1
git ls-files 'scripts/*' | xargs grep -hcE '^[[:space:]]*(#|//)' | paste -sd+ - | bc
git ls-files '*.go' | grep -v '_test\.go$' | xargs wc -l | tail -1
git log --no-merges --since=2026-08-01 --format=%H | while read -r c; do \
  git show --name-only --format= "$c" \
  | grep -qE '^(internal/|main\.go$|go\.mod$|go\.sum$)' || echo "$c"; done | wc -l
```

Comment share is 49–63% across the six the gate runs directly (`check-docs.sh`,
`check-docs-selftest.sh`, `smoke-run.sh`, `smoke-run-selftest.sh`,
`sweep-quantities.sh`, `docrefs/main.go`); `scripts/install.sh` is 7%.

**Keep** what a wrong sentence costs a credential or a user: `docs/ADAPTERS.md`'s
allowlists, `docs/CREDENTIAL-RULES.md`, `docs/CLI.md`'s contract, `AGENTS.md`'s safety
rules, a trap in neither the code nor git log, and **anything a program parses or
executes** — `git grep ReadFile -- '*.go' | grep docs`, plus `docs/VALIDATION.md`'s
fenced blocks, which `scripts/smoke-run.sh` runs: emptying one exits 2, *thinning* one
passes green with fewer assertions.

**Delete** the rest rather than declaring it best-effort: rationale narration,
epistemology in script headers, and measurement stories about **this repository's own
history** — not the upstream ones in [VALIDATION.md](VALIDATION.md) § Upstream
Behaviour Assumptions, which are the re-verification instrument and are not in git log.
The workable form of that, applied to the first pass: a sentence goes when it reports
what a past session did or found **and** the reason, rule or trap around it still stands
without it. A rule stays; the round that produced it does not.

**What is left is [RELEASE.md](RELEASE.md), and it is not free prose.** Everything below
its first `---` rule is shipped-release entries, and product code cites them by section
letter: `git grep -n 'RELEASE\.md §' -- internal/` lists the `§A`/`§B`/`§C`/`§D` form,
and `git grep -n 'RELEASE\.md v0\.' -- internal/` the version-named one. `docs-check`
resolves neither — a bare `§A` names no section it can look up — so deleting an entry
dangles a comment in product code with nothing reporting it. Cutting this means repointing
those comments in the same commit, which is a change to `internal/` and a different review
from a prose pass. That is why the first pass stopped at the docs and the scripts.

Rules adopted with it, from the branch that closed `adapter.VersionVerifier`:

- **Two rounds, then land** — one correctness, one quality. A finding in material
  the review loop itself added is fixed inline without re-opening a round, unless it
  changes product behaviour.
- **No new prose-verifier** unless the defect it catches has a product consumer.
  `docs-check`'s link and citation resolution stays; it is decidable and cheap.

## Review Triggers

- First credential-layout change in any upstream tool: add a regression
  fixture and bump the adapter guard before widening support.
