# Security

`kae` reads, stores, and writes live credentials for other tools. Safety rules
here are part of the command contract.

## Mutation Safety Rules (mandatory)

- Never replace a tool home or a mixed-state file wholesale. Mixed-state files
  (`~/.claude.json`) are patched only through the JSON Pointer allowlist
  defined in [ADAPTERS.md](ADAPTERS.md).
- Never delete unknown keys; preserve everything outside the allowlist.
- Back up the live artifacts before every write; rollback must always be
  possible (`kae rollback`).
- Hold the per-tool lock for the entire read-modify-write window.
- All file writes are atomic (temp file + rename, same directory) and set
  mode `0600` for credential files.
- Validate structure before writing; refuse with `unsafe_refused` (exit 10)
  when the live layout is unrecognized.
- Support `--dry-run` on every mutating command.

## Secret Handling

- Secret values never enter stdout, stderr, logs, JSON reports, error
  messages, or metadata files. Reports reference artifacts by name, kind,
  target path, and pointer only.
- **One documented exception** to the stdout rule: the hidden
  `kae __companion-token <profile> <id> <knob>` credential helper prints a
  single companion token to stdout. It is a git-credential-helper-style seam
  invoked **only** by the mise `exec()` template a companion binding writes
  (see [ADAPTERS-COMPANION.md](ADAPTERS-COMPANION.md)); it resolves the value
  from the secret backend at environment-evaluation time so the token is never
  written to disk in the fragment. It is never reached on a human or JSON
  reporting path, and the token's env var is added to the fragment's mise
  `redactions` so task logs mask it. `kae companion list` shows knob names and
  non-secret values only; token values are never printed.
- Account snapshot payloads and backup payloads are stored in the secret
  backend (OS credential store by default; see
  [DATA-MODEL.md](DATA-MODEL.md#secret-references)).
- The plaintext `file` backend requires explicit
  `security.secret_backend = "file"` in config. It writes `0600` files under
  a `0700` directory and `doctor` permanently warns while it is active.
- `kae` never stores secrets in TOML and never echoes captured values back
  for confirmation.
- The `kae status` `global_isolated` field and `run -i`'s home-path message
  contain only directory paths and account names — never secret values.
- The detected login `identity` (v0.8.3 §D — an email or account id stored in
  `account.toml` and shown by `kae ls`/`kae accounts`) is **PII but not a
  secret**: it is plaintext metadata exactly like the account name, never a
  token. It is read from already-trusted live state and never derived from a
  credential value.
- claude's `oauth_account` artifact (the `/oauthAccount` identity cache) is
  **PII but not a credential**: email, account/org uuid, plan fields, no token.
  It is stored in the secret backend anyway, because every artifact payload goes
  there — no path special-cases it, so it inherits the same
  never-to-stdout/JSON/logs treatment as a credential. It is captured from
  already-trusted live state, never derived from a token value.
- The codex keyring payload (the `Codex Auth` keychain item, v0.8.3 §C) **is** a
  credential and is treated like every other secret: captured verbatim into the
  secret backend, never written to stdout/JSON/logs/metadata; only the item's
  account attribute (`cli|<16 hex of sha256(CODEX_HOME)>` — derived from a path,
  not a secret) is recorded in `account.toml`.
- The agy keychain payload (the `gemini`/`antigravity` item, v0.8.6 §A) **is** a
  credential — an opaque ~686-byte token — and is treated identically: captured
  verbatim into the secret backend, never written to stdout/JSON/logs/metadata.
  Its account attribute is the fixed literal `antigravity` (not a secret, not
  recorded as captured state); kae matches by service **and** account so it never
  reads or writes a `gemini` item belonging to another tool.

### Secret enumeration (v0.8.1 `secret_orphan`)

The base `Backend` interface is get/set/delete by key only. `secret.Enumerator`
(optional, `Keys(ctx)`) adds listing, used by the `doctor` `secret_orphan`
check (a secret item with no matching `accounts/<tool>/<account>` snapshot dir).

The mirror check needs no listing and therefore has no such gap: `secret_missing`
(v0.16.0) asks whether the payload a snapshot *declares* is still in the backend,
which is a lookup of keys the snapshots name. On darwin it is the only one of the
two that reports anything. What enumeration is still required for is the direction
below — a stored key kae has no snapshot for cannot be found by asking snapshots:

- **darwin keychain: cannot enumerate via the `security` CLI**, so
  `keychainBackend` does **not** implement `Enumerator` and the orphan check is
  silently skipped there. `security find-generic-password -s kagikae` returns
  only the **first** matching item; `security dump-keychain` dumps the entire
  login keychain, prompts per item, and is brittle — not a stable path.
- **`file`** (`readdir` over `*.secret`) and **Linux `libsecret`**
  (`secret-tool search --all`, parsing `attribute.key` only) implement
  `Enumerator`, so the check runs there. The libsecret search output also
  carries `secret = ...` lines; only `attribute.key` is read and the raw output
  is never logged, so no secret value leaks.

`kae account rm` deletes the snapshot dir and every secret item together, so
orphans are rare; the check catches leftovers from manual cleanup.

The check reads **only the account namespace** (`secret.AccountKey`): a backup,
companion, or env-profile key has no `accounts/<tool>/<account>` dir behind it by
design, so it is skipped rather than reported (docs/DATA-MODEL.md § Secret
References).

### Per-switch `security` read coalescing (v0.8.1)

A single switch reads one tool's keychain service several times (`Detect`, the
backup, and the switch-away recapture). `keychain` provides a context-scoped
read cache (`WithReadCache`) wired into the switch path so those collapse to one
`security` invocation (and at most one auth prompt); writes invalidate the
cached service. Most drivers match by service alone, so the cache is keyed by
service; agy's account-scoped match (`gemini`/`antigravity`) keys the cache by
service **and** account so a shared service stays correctly partitioned. The
cache is per-command and never spans a child process run (`run -s`), where the
child could rotate the live credential unseen — a cached value would be stale.

## Subprocesses

- `security`, `secret-tool`, and binary detection run through
  `internal/runner` with `exec.CommandContext` and argv arrays (no shell
  strings).
- Keychain payloads are passed to `security` via argv: the security CLI has
  no non-interactive stdin password input. This is an accepted, documented
  trade-off — on macOS, another user cannot read a process's argv
  (`ps -E`/`ps -ww` show arguments only for the same user or root), and any
  same-user process could read the keychain through `security` anyway, so
  argv exposure grants no privilege beyond what the keychain itself grants.
  stdout of `security find-generic-password -w` is treated as secret and
  redacted from any diagnostics.
- User-controlled account/profile names are validated against
  `[a-zA-Z0-9._-]{1,64}` before use in paths, lock names, or secret keys.
- The `companion_drift` doctor check shells out to `git config --get
  user.<knob>` through `internal/runner` (argv array, no shell). It reads only
  non-secret git identity fields, never a credential, and makes no network
  call. The live value it reads back is sanitized (control characters dropped,
  length-capped) before it reaches doctor output, so a hostile repo-local
  `.git/config` cannot inject terminal escapes.
- The `identity_drift` doctor check compares the stored identity payload against
  the live one. It is **offline** — a comparison of state already on the
  machine, no probe and no network call — so nothing about the login is
  transmitted. Neither value reaches the output: an identity is PII (an email
  address), so a finding names only the tool, the account, and the artifact or the
  bound directory it is about. The check has two frames and both obey that rule:
  the active account's live state, and a bound directory's own store.
- The `upstream_version` doctor check runs `<binary> --version` through
  `internal/runner` (argv array, no shell) and reads only the version string.
  **Offline**, no credential in the environment, nothing to redact.
- The `credential_stale` / `credential_expiring` / `credential_superseded` doctor
  checks parse a credential payload to read its expiry. **Offline**, no network.
  Their messages carry only timestamps, a tool name, an account name or a directory
  path, and a suggested command — never a token, and a redaction test pins each of
  the three message builders plus the switch-time warning against exactly that.
  `credential_superseded` reads more payloads than the other two — the account
  snapshot **and** every bound store of that account, since it compares copies rather
  than reading one deadline — so it is the widest of the three, and its canary is a
  sibling test rather than a case in the other's table (it needs a bound directory
  and two copies before it has anything to say).

  Two payload sources, and one of them reads live. The account-snapshot half reads
  kae's own secret store; the **bound-directory** half reads the per-directory
  credential store itself, which on darwin is one `security find-generic-password
  -w` per bound directory per tool — the same call `Detect` already makes for the
  global item, through `internal/runner`. It resolves the item only via the
  adapter (`dirCredentialSpec`, asked with an environment pointed at that store),
  and only where the adapter declares the item moves with its isolation variable,
  so a tool whose item is global (codex under the keyring store) is never read —
  that gate is what keeps a bound-directory finding from being about the global
  login. Read-only throughout: the sweep never writes or deletes an item.
- The `companion_token_drift` doctor check resolves a token companion's live
  login (e.g. `gh api user`) and compares it to the recorded `expected_login`.
  It is the one doctor check that makes a **network call**, so it is **opt-in**
  (the doctor prompt or `--yes`; never under `--json`/non-interactive). The login
  it records and reads back is a public handle, not a credential — sanitized
  before output and safe in logs/JSON. The token reaches the probe only through
  the env var the pin already injects (at `kae companion add` time, through
  `internal/runner`'s env seam); it is never written to argv or stdout. The same
  recorded login (`expected_login`) is non-secret config.toml inline.

## File Permissions

- `~/.claude/.credentials.json`, `~/.codex/auth.json`,
  `~/.local/share/opencode/auth.json`, and agy credential files under
  `~/.gemini/antigravity-cli/` are written `0600`; kagikae metadata/state
  dirs `0700`. `$COPILOT_HOME/config.json` (default `~/.copilot/config.json`) is
  owned by copilot (kae only patches its `/lastLoggedInUser` pointer); `doctor`
  warns if it is not `0600`.
- `doctor` warns when live credential files are group/world readable.
- Isolated homes (`isolation/global/<tool>/<account>/`) are created `0700`
  and treated as credential-bearing. Credential files within them (e.g.
  `.credentials.json`, `auth.json`) are written `0600`. The real
  `~/.claude`/`~/.codex` and the tool's *unsuffixed, globally shared* keychain item
  are never touched by isolation. That global item is a different thing from the
  account's own item below, and the two are easy to read as one.
- Where the tool namespaces its keychain item at all (claude on macOS), a bound
  directory's credential is written to an **item rather than to a file**, and any
  superseded plaintext copy is removed. So on macOS an isolation directory normally
  holds no credential on disk at all — the reason is correctness (a plaintext file
  there is a credential the tool stops reading), and one less plaintext secret is the
  side benefit. **Which** item is not this document's to state: what namespaces it is
  a rule the adapter owns, and since v0.17.0 the answer for claude is the account's
  credential store rather than the directory's. docs/ADAPTERS.md § "Credential
  storage resolution" states it once; § "Per-account credential store" says what
  follows for a bound directory.

## Environment Conflicts

`doctor` warns when environment variables override the subscription login the
user thinks they are switching: `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`,
`CLAUDE_CODE_OAUTH_TOKEN`, `GEMINI_API_KEY`, `GOOGLE_APPLICATION_CREDENTIALS`,
`COPILOT_GITHUB_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`, and — for Claude Code's
host-managed provider, whose token variable the host may rename —
`ANTHROPIC_UNIX_SOCKET`, `CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST`,
`CLAUDE_CODE_HOST_CREDS_FILE`, `CLAUDE_CODE_HOST_AUTH_ENV_VAR`
(docs/ADAPTERS.md "Environment conflicts").

## Concurrency

`kae use` (shared mode, `-s`) and `kae run -s` mutate shared live state.
Per-tool locks serialize kae against itself, but cannot stop the upstream CLI
from refreshing tokens concurrently. Therefore:

- locks are held across the whole switch transaction (`use -s`), and across
  the entire child run for `kae run -s`;
- simultaneous different accounts for one tool are unsupported in shared
  mode (documented; isolated mode via `use -i` / `run -i` is the supported
  path for parallel sessions);
- `kae run -s` recaptures refreshed credentials into the account snapshot
  before restoring the previous state, so a token refresh during the child run is
  not lost — except where kae cannot show the live state is that account's, which
  is a refusal rather than a gap: a child that logged in as somebody else, one that
  changed the identity cache to something kae cannot read as a record, and one whose
  refresh failed all leave the snapshot alone with a warning. Because the restore then
  overwrites the live store, a refusal on the first two takes a **second backup** of the
  post-child state (reason `run-unattributable`) and names it, so nothing kae declines to
  adopt is destroyed ([CLI.md](CLI.md) § kae run Semantics). The restore
  itself is skipped for a tool where putting the
  pre-child copy back would overwrite a credential the child superseded — for claude
  that is a logout rather than a regression, because its refresh token rotates
  single-use ([CLI.md](CLI.md) § kae run Semantics).
- `kae use -s` / bare `use` recapture the currently-active account before
  switching away when its live credential diverges from the snapshot, so a
  token rotated in-tool is not silently lost on the next switch back. This is
  best-effort and divergence-gated — a logged-out account is left untouched
  with a warning. It cannot track a refresh-token rotation that happens entirely
  outside kae; that case surfaces as the `credential_stale` warning, not a
  silent repair.

`kae run -i` operates in the global isolated home (`isolation/global/<tool>/<account>/`)
with no lock and no live mutation. It is safe to run concurrently with
`kae use` in other terminals — the real home is never touched.

## Isolation Safety

Three isolation scopes exist; their credential boundaries are:

| Scope | Command | Config store (sessions, settings, identity) | Credential store | Live home touched? |
|-------|---------|------|------------------|--------------------|
| Global isolated | `use -i` / `run -i` | `isolation/global/<tool>/<account>/` | claude: `credstore/<tool>/<account>/`; other tools: the config store | No |
| Per-directory shared | `pin -s` | `isolation/<pin-id>/<tool>/shared/` (symlinks to the real home) | same | No (symlink source only) |
| Per-directory isolated | `pin -i` | `isolation/<pin-id>/<tool>/isolated/<account>/config/` | same | No |

**The credential column is deliberately not private per directory**, and reading it
as private is the mistake this table exists to prevent. For a tool that can address
its credential separately from its home (claude), every scope bound to one account
reads **one** credential, because copies of it invalidate each other — a directory's
sessions, settings and identity stay its own. A directory bound before v0.17.0 still
has its own copy until it is re-pinned. docs/ADAPTERS.md § "Per-account credential
store" is normative for the boundary and for why it is drawn there.

One hard-coded set governs both config keys (`shared_denylist_extra` and
`isolated_shared_items`, enforced at config load), and it covers two kinds of file
for two different reasons. Credential files (`.credentials.json`, `auth.json`) must
stay private so a directory authenticates as its own account — refusing them keeps a
credential from leaking across directories. claude's `.claude.json` must stay private
so a directory can *name* its own account: shared back to the real home, it makes
every bound directory display whatever the real home displays, whichever account it
is logged in as. Both keys read one table (`constants.PrivateBindItems`), which is
also what the shared bind builds its symlink denylist from, so a name cannot be
denied in one place and permitted in another; a guard test pins that wiring.

**A per-directory credential is removed once nothing points at it.** Two kinds,
two rules, stated once in `removeDirCredential` and summarized here because this
section owns what kae deletes: a **keychain item** where the adapter declares it
bindable, and a **file** credential only where it is no longer the copy its own store
reads — the account's own credential store, which holds nothing else, and a store a
migration has just moved the credential out of. A file that is still the one its
store reads is kept, with the sessions and settings beside it. The rest of this
section describes the item, which is the case that most needs the sweep. A re-bind to
another account supersedes a credential store, and a `-s` ↔ `-i` toggle supersedes a
config store, which since the per-account credential store landed holds a credential only
when the binding predates it. Either way the superseded store's item would otherwise
keep a credential that cannot be found again: it lives under a per-directory
service name, so it appears nowhere in kae's data dir, and no kae check reports one
(`secret_orphan` covers kae's own secret store, not the per-directory items; a doctor
check that attributes them is queued in [ROADMAP.md](ROADMAP.md)). The sweep is
scoped by the item identity the adapter resolves for that directory, so it can only
reach the directory's own item and never a global login; store directories, with
their sessions and settings, are left intact. `kae unpin` keeps the current
credentials (a re-pin restores the directory); `kae unpin --purge` removes them.

**Removing one is preceded by a harvest, and refused when that is not possible.**
The item can hold the only copy of an account's credential that still refreshes
(claude's refresh token rotates single-use), so the sweep copies a newer usable one
into the account snapshot first and **keeps** an item it could not preserve — a
leftover secret being the smaller fault. One exception, and it is opt-in: `kae unpin
--purge` deletes a usable copy whose account no longer exists (`kae account rm`, or a
`rename`), because there is nowhere to preserve it and a purge is the command that
asked for the credential to go. The sweep a *bind* runs keeps that same copy. Two consequences worth stating here. A
`--purge` therefore needs kae's secret store, and warns and keeps everything rather
than deleting logins it cannot preserve when that store cannot be opened. And a
copy kae cannot attribute to an account is never *adopted* into some other account's
snapshot to make it deletable: the hash-derived service name does not say whose
credential it holds, and a mislabelled token is undetectable afterwards
([ADAPTERS.md](ADAPTERS.md) § Per-directory credential store).

## Env Profiles And kae run

- `kae env set ... KEY=VALUE` receives the value via argv, which also lands
  in shell history. For secrets prefer the stdin form
  (`kae env set <tool> <account> KEY < file`, or piped).
- Profile metadata stores variable names only; values live in the secret
  backend and are injected solely into the child process environment of
  `kae run --env`. `kae env list` never prints values.
- `kae add` (login flow) and `kae run` launch upstream CLIs, and `kae edit`
  launches `$VISUAL`/`$EDITOR`, all with inherited stdio; kae passes no
  secrets on their command lines.

## External Tools

| Tool | Use | Trust boundary |
|------|-----|----------------|
| `security` (macOS) | keychain read/write | output of `-w` is secret |
| `secret-tool` (Linux) | libsecret read/write | stdin used for store; output of lookup is secret |
| upstream CLIs | binary presence detection only in v0.1.0 | never invoked with credentials |
