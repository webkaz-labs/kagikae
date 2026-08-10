# kagikae working guide

Standalone public repository. Follow the bundled Go CLI standard in
[.claude/skills/go-cli-tooling/](.claude/skills/go-cli-tooling/SKILL.md)
(references under `references/`), plus these local rules.

## Documentation Map

| Document | When To Read |
|----------|--------------|
| [README.md](README.md) | user-facing command or setup changes |
| [docs/CONTEXT.md](docs/CONTEXT.md) | before naming anything, and whenever a word for an existing thing has to be chosen — it is the authority on the vocabulary it holds, the user-facing terms and the mechanism vocabulary alike. Not for a JSON contract token, which is an enum owned by `internal/constants`; its own routing table says so. It is a glossary and states no rule: an entry that names something a predicate decides says which predicate and stops, so a question about *behaviour* is never answered here |
| [docs/DESIGN.md](docs/DESIGN.md) | mission, modes, boundary changes — and **§ Tool Tiers before adding or widening surface for any tool**. A tier decides which *modes* a tool gets and never which guards apply; that section is the only place that says which tool is in which tier, so do not copy the mapping here or anywhere else |
| [docs/ADAPTERS.md](docs/ADAPTERS.md) | anything that touches what a tool adapter switches or preserves |
| [docs/ADAPTERS-COMPANION.md](docs/ADAPTERS-COMPANION.md) | anything that touches what companion-auth lockstep (git/gh/cloud CLIs) switches or preserves |
| [docs/CREDENTIAL-RULES.md](docs/CREDENTIAL-RULES.md) | before any code writes, harvests, attributes, orders or deletes a credential **copy**. It is the normative text for its own thirteen sections and not for the whole subject: several of them defer the per-tool contract to `docs/ADAPTERS.md` or `docs/CLI.md` where they say so, and § Implementation Boundaries below keeps the credential rules that did not move, beside one routing line per rule that did |
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
`go mod verify`, `go build ./...`, and `smoke-selftest`
(`scripts/smoke-run-selftest.sh`, which checks the smoke runner's own guards — no
`kae` build and no network, so it costs a few seconds). `mise run audit` (govulncheck, plus the
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

**Sourcing it correctly is also not enough, so do not drive these blocks by hand
either — `bash scripts/smoke-run.sh '## <heading>'`.** Sourcing the preamble is a
step a harness can perform and still not get: `. scripts/smoke-env.sh` inside
`$(...)` exports nothing, because command substitution is a subshell, and the run
then looks isolated while writing to the real `$HOME`. That happened twice on
2026-08-09, in the same session that a progress `echo` from an `ERR`/`DEBUG` trap
was captured by a `$(...)` in the block — once corrupting an assertion into a
non-integer, once landing inside `HOME=$(mktemp -d)` and creating a directory in
the checkout. `smoke-run.sh` closes the class instead of warning about it, by
isolating the environment **before** the block runs rather than relying on the
block to do it. **Its header is normative for what it isolates and what it does
not; do not restate that here.** This paragraph twice described the mechanism from
memory and was wrong both times — naming four of the eight cleared variables, and
claiming a temp `HOME` covers a set that the tool-home variables outrank.

**Two things it cannot do, and a green run does not claim them.** It cannot
isolate the macOS login keychain, which ignores `$HOME`: only `secret_backend =
"file"` in the block's own config keeps kae's snapshot store out of it, and that
is the 956-item defect. And its leak detector sees the checkout only — a write
elsewhere on the machine is invisible to it. Those two bound what any green run
proves, which is why they are here and not only in the script. `mise run check`
runs `scripts/smoke-run-selftest.sh`, which is what keeps the script's own claims
honest; the sentence it replaced vouched for guards that had been checked by hand
once and were then shown to be false.

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
- **A keychain item's identity is service + account, and per-tool.** codex derives
  the account of its single-service `Codex Auth` item from `CODEX_HOME`
  (`cli|` + 16 hex of sha256 over the **canonicalized** path — symlinks resolved),
  so one service holds one legitimate item per tool home; claude hashes the value
  **without resolving or cleaning the path** into the **service** name instead
  (`docs/ADAPTERS.md` § Credential storage resolution derives it, NFC step included,
  and `claude.keychainService` is the only code that may).
  Same idea, two incompatible formulas: derive each in its own adapter and never
  port one.
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

For every change, decide and report "changed / no change needed" for **each tracked
markdown file this repository owns** — derive the set rather than trusting a list:
`git ls-files '*.md' | grep -v '^\.claude/skills/go-cli-tooling/'` (18 today). It
covers `README.md`, `AGENTS.md`, `CLAUDE.md`, everything under `docs/`, and the
repo-local `upstream-auth-drift` skill, which the Documentation Map cites as
normative and which a `docs/`-only list cannot reach — that is how a rule that had
moved kept naming `AGENTS.md` as its authority.

`mise run docs-scan` belongs to this sweep and to nothing else — it reports prose two
documents carry twice, and it can fail nothing, so it is not a check and is not in
`mise run check`. `scripts/docscan/main.go`'s header is normative for what a report
does and does not mean; read it before acting on one.

`.claude/skills/go-cli-tooling/` is excluded because it is a **generated** export of
the shared Go CLI standard: never hand-edit it, or the next re-sync silently drops
the edit. A change there belongs in the chezmoi source and arrives by re-export.

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

**That grep sees whether the target exists and nothing else, so what it cannot see is
a quantity written *beside* a citation.** `docs/ROADMAP.md` said "the two commands in
CONTEXT.md § Not converged" after a later commit merged them into one: § Not converged
still existed, so every heading-fragment grep stayed green and only reading the sentence
found it. Sweep the added lines, then check by hand each hit that describes **another**
file — the `+++ b/...` headers are kept in the output for exactly that — **and each hit
that counts something inside its own file, because one diff can change what it counts.**
That second half was missing, and the sweep's first use after it was written proved why:
a commit un-struck one of ten struck entries in `docs/ROADMAP.md` and wrote "all ten" in
the present tense in the same diff, the pattern caught the line, and this step said to
skip it because it named no other file. Triage by whether the quantity can go stale, not
by which file it points at:

```bash
git diff main...HEAD | grep '^+' | grep -iE \
 '^\+\+\+ |(^|[^A-Za-z*`])\*{0,2}`?(both|one|two|three|four|five|six|seven|eight|nine|ten|thirteen'\
'|first|second|third|fourth|fifth|sixth|seventh|[0-9]+)`?\*{0,2} '\
'\*{0,2}(commands?|terms?|rules?|rows?|entries|entry|sections?|bullets?|places?|copies|copy|files?|lines?|names?|pairs?|sites?|tools?)\b'
```

Before trusting a clean run, fire it at a commit with a known bad count — the positive
control any negative assertion here needs. **`git show 89341f4 | grep '^+' | <the
second grep above>`** produces the `docs/ROADMAP.md` line quoted above; if it stops
producing it, the pattern is broken. `git show`, not `git diff main...89341f4`: that
was the first form written here and it went vacuous the day the branch merged, because
a range against an ancestor is empty and an empty input greps clean. A positive control
that a merge can silence is not one.

The same emptiness bites the sweep itself before the first commit on a branch:
`main...HEAD` is a range, so it sees nothing uncommitted, and a run on a branch with no
commits yet reports clean about a working tree full of changes. Use `git diff main` while
the change is uncommitted. Either way the count of `^+` lines is the control — if that is
zero, the sweep proved nothing.

It is **a net, not a proof**, and its holes are not a closed set: the noun and number
lists are enumerations, and every enumeration written in this file has turned out to be
one short — including this one, twice: `thirteen` had to be added because this
repository writes "the thirteen rules", and the **ordinals** had to be added after a
diff wrote "a third site" and swept clean. Anything larger than the words listed is
written as a digit, which `[0-9]+` covers. Two holes
are known today. The first is that a quantity which **wraps** across lines is invisible
to a line-based grep — demonstrated without depending on how this file happens to be
wrapped, since a reflow would silently retire the demonstration:

```bash
printf 'the **two**\ncommands in X\nthe two commands in X\n' | grep -cE '(one|two) commands?'
# 1, not 2 — the wrapped one is missed
```

The second is that the command matches its own defining sentences, which is exactly why
the citation rule above rejects `git grep '\.md §'`: a run whose only hits are this
file's own prose is not a clean run.

The cheapest fix for the whole class is the one the rest of the tree already uses —
**do not write the number, write the derivation**: see
[docs/CONTEXT.md](docs/CONTEXT.md) § Not converged, the `docs/ROADMAP.md` entry that
cites it, and the `EXPECTED_GUARDS` note in `scripts/smoke-run-selftest.sh`'s header,
which says it deliberately does not repeat the count because the two drifted apart the
moment a guard was added. The mechanism that would retire the sweep is the unbuilt
claim-reconciliation stage `scripts/docscan/main.go`'s header names, filed in
`docs/ROADMAP.md`.
