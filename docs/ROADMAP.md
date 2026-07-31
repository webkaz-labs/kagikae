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
[VALIDATION.md](VALIDATION.md). Two of the five automation ideas shipped —
version/date agreement with a doc-parsing test plus a six-month age check, and
the offline contradiction check for codex's store. The remaining three, in the
order each pays for itself:

3. **Literal-count fingerprints**, per tool, wired into `mise run audit`: assert
   every name kae models still exists in the bundle and is referenced as often.
   Measured stable across claude 2.1.218/219/220 while the minified identifiers
   around them churned, which is what makes the count a usable signal.
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
  what that failure mode means). Worth a row in
  [VALIDATION.md](VALIDATION.md)'s claude assumptions once someone measures it, and
  worth re-checking whether kae's `keychain.WithReadCache` / per-tool locks are enough
  when a switch overlaps a live session's own refresh.

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
  was the part that used to be silent.

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
- **Identity cache in isolation modes**: `kae use` / `kae add` switch claude's
  `/oauthAccount` identity cache, but the per-directory materializers
  (`writeDirCredential`, and its callers `prepareBond`, `preparePinConfig`,
  `prepareGlobalIsolatedHome`) write only the credential. Since
  `CLAUDE_CONFIG_DIR` moves the cache to `<dir>/.claude.json` and
  `prepareBond` only links the entries *of* `~/.claude`, a bonded or isolated
  directory keeps whatever account first ran there, and `kae pin <tool>
  <account>` does not correct it. Auth is unaffected (the token wins) — it is an
  attribution gap: the UI can name the wrong account inside a pinned directory.
  The fix is now one identity step alongside `writeDirCredential`, which all four
  already route through; the design
  question is bond mode, where writing the cache is visible to the real home
  (docs/SCOPE-MODEL.md §6). Until then `doctor`'s `identity_drift` check skips a
  kae-owned isolated home for the same reason: kae applied no identity there, so
  there is nothing of its own to compare the live value against.
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
     keeps everything, so a re-pin restores the directory). The sweep mirrors the
     write gate — keychain items only, only where the adapter declares them bindable
     — so it starts covering codex the moment the capability is declared, with no
     further work. It also closed the same gap for claude, which had been creating
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
- **`account.toml`'s `keychain_account` is write-only.** It is recorded at capture
  and read nowhere, with a doc that tells apply to ignore it (rightly: it is the
  answer for the environment the snapshot was captured in). Either drop the field or
  give it the reader it is evidence for — a doctor check comparing it against
  today's derived account, which is exactly "this snapshot was captured under a
  different `CODEX_HOME`", a natural neighbour of `identity_drift`. A persisted
  field whose only rule is "never read me" is a tripwire.
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
  re-pin restores); only the invisible half, the keychain item, is removed
  ([CLI.md](CLI.md) § pin). What an index would still add is the *other* direction —
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
  `kae doctor` now **reports** a bound directory whose credential is stale or within
  the lead time (`credential_stale` / `credential_expiring`, message `bound to
  <dir>`; see [CLI.md](CLI.md) "Bound-directory credentials"), which is the half
  with one right answer: the remedy is a login inside that directory.
  **Recapturing a pin store's token back into the account snapshot is still not
  done, and not merely unimplemented — it has no non-arbitrary definition yet.**
  Several directories can bind the same account, each with its own
  independently-refreshed token, so nothing says which of them the single global
  snapshot should take; and a directory not opened in weeks holds an *older* token
  than the global one, so writing it back is a downgrade that
  `recaptureWouldDowngrade`'s "is it usable" test cannot catch — both tokens are
  usable, they are just different. Two directions if it is ever wanted: pick the
  store with the latest deadline (needs every store read on every switch, and is
  still wrong when two are equally fresh), or make it explicit —
  `kae add --no-login <tool> <account> --from-dir <dir>` — so the user names the
  authoritative one. The explicit form is the only one that is honest about the
  ambiguity.
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
- **cursor off macOS is now unblocked but unimplemented**: the adapter refuses
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
- **agy home isolation**: revisit once upstream exposes a stable
  home/config env var; until then `home` / `overlay` modes refuse it (the
  same applies to the v0.6.0 adapters until their env vars are verified).
- **copilot isolation** (newly possible, not built): `COPILOT_HOME` is now a
  verified home variable (2026-07-31), so copilot could join claude and codex in
  `isolationEnvVar` and become `use -i` / `pin -i`-capable. Not done with the
  read-the-variable fix: the per-account keychain items coexist and are never
  switched, so what a second home changes (and whether copilot's own
  `--config-dir` precedence can defeat it) has to be established first.
- **agy's file store on macOS** (recorded gap, 2026-07-31): agy skips the
  keychain under ssh/wsl/container detection, on a 1s keyring timeout, and on any
  keyring failure, so the file store is reachable on macOS too — but the fallback
  file's path is not derivable from the 1.0.10 binary, so kae warns instead of
  switching it ([ADAPTERS.md](ADAPTERS.md), [VALIDATION.md](VALIDATION.md)).
  Blocked on a way to make agy write a token without a real login: it has no
  kae-drivable login, so the `security` PATH shim (which does apply to agy) has
  nothing to intercept yet.
- **opencode's DB credential store** (recorded gap, 2026-07-31): `auth.json` is
  still the live store through 1.18.5, and the `credential` table in
  `opencode.db` is a dormant one-shot import. When a release makes that table
  authoritative, kae's pointer patch becomes a silent no-op — and on 1.17.4 the
  imported row is frozen at whichever account auth.json held on first run. The
  `upstream_version` doctor warning is the trigger to re-run the VALIDATION row.

## Exploratory

- richer TTY (routed review surface) if daily use shows the need
- shell completion
- localized human output (Japanese)
- `kae shell init` convenience wrappers

## Review Triggers

- First credential-layout change in any upstream tool: add a regression
  fixture and bump the adapter guard before widening support.
