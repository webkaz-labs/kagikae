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
  `CLAUDE_CONFIG_DIR` (`Claude Code-credentials-<sha8>` over the *raw* env
  string — no path cleaning, so a trailing slash is a different item), and kae's
  own isolation modes are what set that variable. Modelling the name as a
  constant made every pinned directory on macOS run the previous account with
  every offline guard green, because the tool reads the keychain first and its
  first token refresh creates the per-directory item and deletes the file kae
  wrote. So: resolve a credential's location by asking the adapter with an env
  built for the target directory (`dirCredentialSpec`), never by recomputing a
  path or a service name at the call site; and when a write to the authoritative
  store fails, return the error — a fallback to the secondary store reports
  success while the tool reads something else.
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
