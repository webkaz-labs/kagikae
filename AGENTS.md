# kagikae working guide

Standalone public repository. Follow the bundled Go CLI standard in
[.claude/skills/go-cli-tooling/](.claude/skills/go-cli-tooling/SKILL.md)
(references under `references/`), plus these local rules.

## Documentation Map

| Document | When To Read |
|----------|--------------|
| [README.md](README.md) | user-facing command or setup changes |
| [docs/DESIGN.md](docs/DESIGN.md) | mission, modes, terminology, boundary changes |
| [docs/ADAPTERS.md](docs/ADAPTERS.md) | anything that touches what a tool adapter switches or preserves |
| [docs/ADAPTERS-COMPANION.md](docs/ADAPTERS-COMPANION.md) | anything that touches what companion-auth lockstep (git/gh/cloud CLIs) switches or preserves |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | package layout, adapter interface, transaction, lock changes |
| [docs/CLI.md](docs/CLI.md) | command flags, output, exit codes, JSON contract changes |
| [docs/DATA-MODEL.md](docs/DATA-MODEL.md) | config, snapshot, state, backup, secret-ref changes |
| [docs/SECURITY.md](docs/SECURITY.md) | secrets, subprocess, permission, redaction changes |
| [docs/ROADMAP.md](docs/ROADMAP.md) | long-term ordering changes |
| [docs/RELEASE.md](docs/RELEASE.md) | active release target changes |
| [docs/VALIDATION.md](docs/VALIDATION.md) | before commit and release checks |
| [docs/handoff-upstream-drift.md](docs/handoff-upstream-drift.md) | picking up the post-v0.12.0 audit findings, or building the upstream-drift audit skill — delete this file when both land |

## Validation

```bash
mise run check
git diff --check
```

`mise run check` is the authoritative gate; it must pass before every commit.
It runs `lint` (gofumpt + goimports format check, `staticcheck -checks=SA*`,
curated `golangci-lint`, `shellcheck`), `go test ./...`, `go vet`,
`go mod verify`, and `go build ./...`. `mise run audit` (govulncheck) and
`mise run goreleaser-check` are slower release-time checks. Lint tools run via
`go run <tool>@<pinned version>`; the first run downloads them.

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
- **A per-directory keychain item has to be removed when nothing points at it any
  more**, and the sweep (`pruneDirCredentials`) mirrors the write gate exactly:
  keychain items only, only where the adapter declares them `KeychainDirBindable`.
  The asymmetry with a file store is deliberate — a file credential lives *inside*
  the store directory, which `kae unpin` and a mode toggle keep on purpose, while an
  item lives under a per-directory service name that appears nowhere in kae's data
  dir and cannot be enumerated on darwin, so nothing could ever find it again.
  Two things must move in lockstep with it: a **third** per-directory mechanism
  (today `shared` and `isolated`) has to be added to `dirCredentialStores`, or its
  stores are silently never swept; and the sweep must run **after** the new binding
  is written, or a mid-sequence failure leaves the live binding pointing at a store
  whose credential is already gone.
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
  guard `subcommandVerbs` + `TestSubcommandCompletionParity`. `kae <cmd> <TAB>`
  must never be a dead end (a new subcommand group shipped without completion in
  v0.10.0). Completion is dynamic, so candidate changes resolve live; only a
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
