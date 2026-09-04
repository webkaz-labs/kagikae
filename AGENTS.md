# kagikae working guide

Standalone public repository. Follow the shared Go CLI standard — read it from the
user-level `go-cli-tooling` skill (`~/.agents/skills/go-cli-tooling/`, which
`~/.claude/skills/` links to; its `SKILL.md` routes to the detail under
`references/go-cli/`) — plus these local rules.

**This repository deliberately carries no copy of that standard, and re-bundling it is a
change to argue for, not a correction to make.** The standard's own
`references/go-cli/TEMPLATE.md` § Copy (standalone public repository) tells a repository
like this one to copy the whole skill into `.claude/skills/go-cli-tooling` and point this
file at the copy, so a reader following the standard correctly restores the bundle unless
something says otherwise — which is what this paragraph is for. What was measured while a
copy was here: the source moves, its canonical path included, so a copy is a snapshot that
goes stale with nothing reporting it; an export taken from a stale branch put an old
standard plus a local fix on `main`; and most of that directory's history here was
re-exports chasing the source. It also froze two upstream defects into this repository's
own gate. Both are **still open**, and both belong to the chezmoi source rather than to
this repository — [docs/ROADMAP.md](docs/ROADMAP.md) carries them, including which half
stopped being duplicated here when the copy went, and it is the status to read rather
than this sentence. Reading the standard from outside is dependable for a specific reason:
`chezmoi apply` validates a staged export and compares a deterministic source hash before
moving the symlink, so the installed directory's own name is the hash of the source it
came from. The cost is equally specific and is not grounds for quietly reversing the
decision: a clone on a machine without that skill cannot read the standard at all, and no
check here can notice that the standard changed.

## Documentation Map

| Document | When To Read |
|----------|--------------|
| [README.md](README.md) | user-facing command or setup changes |
| [docs/CONTEXT.md](docs/CONTEXT.md) | before naming anything, and whenever a word for an existing thing has to be chosen — it is the authority on the vocabulary it holds, the user-facing terms and the mechanism vocabulary alike. Not for a JSON contract token, which is an enum owned by `internal/constants`; its own routing table says so. It is a glossary and states no rule: an entry that names something a predicate decides says which predicate and stops, so a question about *behaviour* is never answered here |
| [docs/PRODUCT.md](docs/PRODUCT.md) | mission, modes, boundary changes — and **§ Tool Tiers before adding or widening surface for any tool**. A tier decides which *modes* a tool gets and never which guards apply; that section is the only place that says which tool is in which tier, so do not copy the mapping here or anywhere else |
| [docs/ADAPTERS.md](docs/ADAPTERS.md) | anything that touches what a tool adapter switches or preserves — and **docs/ADAPTERS.md § Verified Upstream Versions before bumping a `VerifiedVersion()` or `VerifiedOn()`**, which owns what re-verification means and where every copy of the pair lives |
| [docs/ADAPTERS-COMPANION.md](docs/ADAPTERS-COMPANION.md) | anything that touches what companion-auth lockstep (git/gh/cloud CLIs) switches or preserves |
| [docs/CREDENTIAL-RULES.md](docs/CREDENTIAL-RULES.md) | before any code writes, harvests, attributes, orders or deletes a credential **copy**. It is the normative text for its own thirteen sections and not for the whole subject: several of them defer the per-tool contract to `docs/ADAPTERS.md` or `docs/CLI.md` where they say so, and § Implementation Boundaries below keeps the credential rules that did not move, beside one routing line per rule that did |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | package layout, adapter interface, transaction, lock changes — and § Switch Mechanisms, what a cell of the scope × environment matrix does internally. The matrix itself is in `docs/PRODUCT.md`. Also **docs/ARCHITECTURE.md § Known Traps before any programmatic edit of a config, state or credential file** |
| [docs/CLI.md](docs/CLI.md) | command flags, output, exit codes, JSON contract changes — and **docs/CLI.md § Keeping completion current before adding, renaming or removing a command, a subcommand or a shell**, which is normative for what has to move in the same commit and for what no guard checks |
| [docs/DATA-MODEL.md](docs/DATA-MODEL.md) | config, snapshot, state, backup, secret-ref changes |
| [docs/SECURITY.md](docs/SECURITY.md) | secrets, subprocess, permission, redaction changes |
| [docs/SCOPE-MODEL.md](docs/SCOPE-MODEL.md) | the scope/isolation model's rationale and the upstream findings behind it — read it to learn *why* a decision was made, never for the rules themselves, which live in the documents above. Its section numbers have gaps that the file's own opening explains |
| [docs/ROADMAP.md](docs/ROADMAP.md) | long-term ordering changes — and **§ Current work order before picking work up**, which owns the near-term order and what each item waits on |
| [docs/RELEASE.md](docs/RELEASE.md) | release-procedure changes, and for how a past release's own record is read out of git |
| [docs/VALIDATION.md](docs/VALIDATION.md) | before commit checks, for the smoke blocks `scripts/smoke-run.sh` executes, and for the two release-only smokes that stayed beside the surfaces they check |
| [docs/ACCEPTANCE.md](docs/ACCEPTANCE.md) | before cutting a release — what its real-machine run covers, and which account combinations are explicitly optional in **§ Optional account-combination checks**. Also **when claude asks for a login it did not ask for before, or a deadline kae reported passes and it keeps serving** — read what that section says to read before logging back in. It is where every real-machine result is recorded. Nothing here runs from `mise run check` |
| [.claude/skills/upstream-auth-drift/](.claude/skills/upstream-auth-drift/SKILL.md) | an upstream tool may have changed how its authentication works: a doctor `upstream_version` / `identity_drift` warning, a tool upgrade, a "kae says it switched but the tool shows the old account" report, or a routine re-verification |

## What Belongs In This File

Two tests, in this order, before adding anything here.

**Can it be executed?** Then it is not prose: a command with an expected output is a
script with a control, a property of the tree is a Go test, a property of the CI
environment is a constraint line in the workflow. The commit that added this section
(`46b3848`) carries the derivation and its caveats.

**Does a reader need it before anything cues them to look?** Kinds that do: a convention
nothing in a task names (the example-name rule, the `set -e` traps), a tripwire against a
default that looks correct (this file's opening, which has stopped a re-bundle), and a
step every commit performs. Where a cue exists, the cue goes in a Documentation Map row
and the content lives where the row points — that is what the credential rules did, and
their routing entries below still state no rule, which is the property that keeps them
short. Do not read "line" as one physical line; each wraps.

The order is the load-bearing part. A step run on every change answers yes to the second
question and never meets the first, so asking the second first is how a shell pipeline
comes to be maintained in a router file.

## Validation

```bash
mise run check
git diff --check
```

`mise run check` is the authoritative gate; it must pass before every commit.
**Its steps are not listed here** — `mise.toml`'s `[tasks.check]` is the one copy, and
three hand copies of it had already drifted apart. Some are worth a word about what they
do rather than that they exist: `smoke-selftest`
(`scripts/smoke-run-selftest.sh`, which checks the smoke runner's own guards — no
`kae` build and no network, so it costs a few seconds), and `docs-check`
(`scripts/check-docs.sh`, which rejects the docs defects nothing else here catches).
Its header enumerates them, and together with `scripts/check-docs-selftest.sh`'s —
which checks that one — and the extractor
package comment it routes to, is normative for which link and citation forms the walk
reads **and which it cannot**, why each count is a floor, and why a floor cannot reach a
predicate. Read those before trusting a clean run; do not restate them here.
`docs-check` is **not** `mise run docs-scan`, which reports duplicated prose and
deliberately fails nothing. `mise run audit` (govulncheck, plus the
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

**A temp `HOME` is not enough, and this has drawn blood** — a run shaped that way
wrote a fixture account into the operator's live `state.json`.
**Never hand-write the exports: `. scripts/smoke-env.sh`**, which is the one copy of
them, says which roots and why, and carries the reason a temp `HOME` does not cover
them.

**Sourcing it correctly is also not enough, so do not drive these blocks by hand
either — `bash scripts/smoke-run.sh '## <heading>'`.** Sourcing the preamble is a
step a harness can perform and still not get; the measured ways it has failed while
looking isolated are in that script's header. `smoke-run.sh` closes that class
instead of warning about it, by isolating the environment **before** the block runs
rather than relying on the block to do it. **Its header is normative for what it
isolates and what it does not; do not restate that here** — this paragraph twice
described the mechanism from memory and was wrong both times.

**A green run is bounded, and what bounds it is worth knowing before you read one as
proof** — the macOS login keychain and the leak detector.
[docs/VALIDATION.md](docs/VALIDATION.md) § Smoke Checks states what each leaves
uncovered, and that script's header states what it does and does not isolate.
`mise run check` runs `scripts/smoke-run-selftest.sh`, which is what keeps the
script's own claims honest.

## Implementation Boundaries

- **Where a handler, a subprocess and artifact IO each belong** —
  [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) § Package Layout, before adding a
  package, and [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) § Layering, before a
  print site or an `exec.Command`.
- **What a tool switches and what it preserves** —
  [docs/ADAPTERS.md](docs/ADAPTERS.md), before changing either. It is the normative
  contract, so a change to one updates that document in the same commit.
- **Thirteen rules about a credential copy live in
  [docs/CREDENTIAL-RULES.md](docs/CREDENTIAL-RULES.md), which is normative for the
  sections it states.** The lines below say only which section answers which
  question. **None of them states its rule**, so acting on one of these lines is
  acting on a rule you have not read — and a bare citation of `AGENTS.md` for one of
  those rules means the section there. They are also **not a map of everything about
  a credential**: other credential rules stay in this section, so a question the list
  below does not answer is not a question with no answer.
  - § Resolving a credential's location — before computing a credential path or a
    keychain service name at a call site, or falling back to a second store when a
    write fails.
  - § When a store's rule is not knowable from the environment — before declaring an
    artifact for a location you inferred rather than measured, and whenever kae and
    the tool read the same variable differently.
  - § A credential that belongs to an account, not a directory — before touching a
    per-account store, or resolving the config-dir/credential-store pair.
  - § Removing a per-directory credential — before deleting, or deciding not to
    delete, a credential a binding leaves behind.
  - § Harvesting before a write or a delete — before overwriting or removing any
    existing copy of a credential.
  - § A chokepoint is not complete coverage — before placing a preservation pass,
    merging one with a delete sweep, or keying a warning suppression.
  - § Never harvest a copy you cannot attribute — before copying any credential into
    a snapshot or another store.
  - § Ordering two copies of one credential (`supersedes`) — before adding any consumer
    that decides which of two copies is newer, or calls one of them dead.
  - § When a refusal destroys instead of preserving — before adding **or widening** a
    refusal on either recapture path.
  - § What kae observed is not what the tool can do — before narrowing what counts as
    revoked, and before wording any message about a credential kae could not read,
    parse or date.
  - § A restore is not unconditional — before changing what `run -s` or `kae rollback`
    restores, or which record it compares against.
  - § A new per-directory mechanism and the link reconcile — when adding an isolation
    mode.
  - § A store tree is history; a fragment is the binding — before reporting on a
    binding, or walking a bound directory's stores.
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
- **These cross-tool rules already have a normative home; the lines name the question
  and state no rule**, as the credential list above does.
  - How a keychain item is addressed, and why claude's and codex's derivations do not
    port to each other — [docs/ADAPTERS.md](docs/ADAPTERS.md) § Keyring item contract
    and [docs/ADAPTERS.md](docs/ADAPTERS.md) § Credential storage resolution, before
    writing or deleting one.
  - A credential that is a **set** of stores written as one unit —
    [docs/ADAPTERS.md](docs/ADAPTERS.md) § Cursor CLI, before declaring a tool's
    artifacts.
  - An upstream config value that selects a store —
    [docs/ADAPTERS.md](docs/ADAPTERS.md) § Codex CLI, before mapping one to a driver.
  - The two comparison predicates, credential versus identity —
    [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) § Adapter Interface, before comparing
    any captured payload against a live one.
  - `VerifiedVersion()` and `VerifiedOn()` —
    [docs/ADAPTERS.md](docs/ADAPTERS.md) § Verified Upstream Versions, before bumping
    either.
- **What may not appear in output, and how a warning behaves** —
  [docs/CLI.md](docs/CLI.md) § Output Rules, before adding any output path. Two
  halves it does not state: a new output path needs a redaction test, and a warning
  never changes the exit code, because a non-zero exit here breaks the mise enter
  hook.
- **How a mixed-state file may be written** —
  [docs/SECURITY.md](docs/SECURITY.md) § Mutation Safety Rules, before writing one.
  One half it does not state: that refusal is enforced in code review, not only
  written down.
- **`state.json` writes go through `App.mutateState`** — before writing state, or
  deciding anything from a copy of it read earlier
  ([docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) § Locking).
- **`config.toml` edits go through `config.Editor`** — before any programmatic
  config mutation ([docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) § Known Traps).
- **Never inline a JSON contract literal** — `internal/constants` owns the tokens, and
  the Documentation Map row above routes to what each is called.
- **Adding or changing a command, a subcommand or a shell requires updating
  completion in lockstep** — before touching the router, read
  [docs/CLI.md](docs/CLI.md) § Keeping completion current, which is normative for
  what has to move together and for what no guard checks.

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

For every change, decide and report "changed / no change needed" for **each tracked
markdown file this repository owns** — derive the set rather than trusting a list:
`git ls-files '*.md'`. It covers `README.md`, `AGENTS.md`, `CLAUDE.md`,
everything under `docs/`, and the repo-local `upstream-auth-drift` skill, which the
Documentation Map cites as normative and which a `docs/`-only list cannot reach — that
is how a rule that had moved kept naming `AGENTS.md` as its authority. Every tracked
markdown file is now one this repository owns; the command used to filter out
`.claude/skills/go-cli-tooling/`, a generated export of the shared standard where an
edit was lost on the next re-sync, and this file's opening says why that export is gone.

`mise run docs-scan` belongs to this sweep and to nothing else — it reports prose two
documents carry twice, and it can fail nothing, so it is not a check and is not in
`mise run check`. `scripts/docscan/main.go`'s header is normative for what a report
does and does not mean; read it before acting on one.

**A citation that names a `§` names a target that has to exist**, and it quotes the
section name verbatim — so grep the name, not the sigil. Before renaming or moving a
section, `git grep -n` a **short distinctive fragment** of the old name, and repoint
every hit. Short because citations wrap across lines, so a grep for the whole heading
misses them — reproducible today: `git grep -n 'Ordering two copies'` finds the
citation in `internal/cmd/run_test.go`, and `git grep -n 'Ordering two copies of one
credential'` finds nothing there. Do not reach for `git grep '\.md §'`: it misses the
`[X.md](X.md) § Name` and `` `X.md` § Name `` forms, misses every bare `§ Name`
including the routing lines above, and matches this paragraph, so it is neither
complete nor ever empty. A **bare** `AGENTS.md` citation needs none of this — it
survives a rule moving, because the reader lands in § Implementation Boundaries and
the routing list sends them on.

`mise run check` decides one slice of that by itself: `scripts/docrefs` refuses a
`X.md § Name` naming a section that file declares nowhere. The hand grep is for what it
does not reach, which its package comment lists, and everything about *renaming* stays
manual. **Removing content from under a heading that survives is the same class as
renaming and the fragment grep stays green on it**, so the inbound references have to be
read — and an inbound reference need not name a `§` at all.

**A file's own opening is an inbound reference too, and nothing treats it as one.** A `§`
grep looks for citations, `docs-check` resolves targets, and neither reads what a document
says about itself, so a plan a file states in its own voice goes stale in silence. Closing
an entry means reading the opening of every file the entry names.

**That fragment grep sees whether the target exists and nothing else, so what it cannot
see is a quantity written *beside* a citation** — a sentence counting what a section holds
stays green through every heading-fragment grep once the count is wrong, because the
section still exists. Only reading the sentence finds it.

**The fix that holds is not to write the number — write the derivation**, the way
[docs/CONTEXT.md](docs/CONTEXT.md) § Not converged does, and the `EXPECTED_GUARDS` note
in `scripts/smoke-run-selftest.sh`'s header, which deliberately does not repeat a count
because the two drifted apart the moment a guard was added. A quantity never written
cannot go stale. What follows is only the net for the ones already there.

For the ones already there, run `bash scripts/sweep-quantities.sh` and read every record it
returns. It is report-only and deliberately outside `mise run check`, because deciding a
hit needs a reader: **triage by whether the quantity can go stale, not by which file it
points at.** Its header is normative for everything else — the pattern, the fixtures and
the positive control it runs before each sweep, and the classes it cannot reach, one of
which no added-lines sweep ever will.

**An absolute about what *is* — a universal or negative existential whose subject is
another program's behaviour, another file's contents, a past state, or the exhaustiveness
of a set — is written as its derivation or not at all.** Reduce it to a verbatim quote of
the thing itself, a command the reader can re-run beside what it printed and when, or an
observation bounded by who measured it and when — an observation bounds what was *seen*,
never what *exists*. If none of those carries the argument, delete the sentence —
`.github/workflows/check.yml`'s header is where replacing one falsified absolute with the
next one out was paid for. A rule this repository imposes on itself is a commitment, not a
measurement, and is out of scope. Before commit, read your own added lines for a bare
absolute.

**A present-tense claim about a mutable subject belongs with that subject's re-executor,
not in prose that points at one.** Look for the re-executor before building one; the
subjects met so far each had one already. A property of **the tree** is a Go test, and the
prose cites the test by name. A property of the **CI environment** is a constraint line in
`.github/workflows/check.yml`: `GOPROXY: off` with an empty `GOMODCACHE` on a step
*enforces* what a sentence claiming "runs green with no network" only *records*. A property
of an **upstream tool** is a row in [docs/VALIDATION.md](docs/VALIDATION.md) § Upstream
Behaviour Assumptions, which is load-bearing because code around it re-reads the installed
tool — read that file for which check reaches which rows, since they are not uniform.
A subject with no re-reader does not belong there, where it would look re-verified and
not be — a CI runner image was refused on exactly that ground. Measure a property of the
tree by hand twice and it is a test, the way a procedure run by hand twice is a script.

**A constraint is the stronger form only if it can fail, and what it enforces is narrower
than the sentence it replaces.** Both were true of that `GOPROXY` line, and the step's own
comment in `.github/workflows/check.yml` is where the measurements are. A constraint that
cannot fire is worse than the sentence it replaced, because it reads as enforced.

**A derivation is run on the tree that contains the sentence citing it — after the edit,
never only before.** The diff that writes a claim can be what falsifies it:
`docs/RELEASE.md`'s "and nowhere else" was broken by its own diff's other hunks. So before
commit, re-run every command your added lines quote. What this cannot reach is a
derivation recorded in a file the diff never touches — the class no added-lines sweep
reaches, named in `scripts/sweep-quantities.sh`'s header — and the reconciliation stage
filed in `docs/ROADMAP.md` still owns it.

A sibling net over closure words was built and refused; `scripts/sweep-quantities.sh`'s
header carries the reason, beside the net that was kept.
