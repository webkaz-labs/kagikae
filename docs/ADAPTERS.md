# Tool Adapters

This document defines, per upstream tool, what `auth` mode switches and what it
must preserve. The allowlists here are the normative contract; the adapters
implement exactly this and refuse anything outside it.

Upstream credential layouts are not stable public APIs. Every adapter must
guard on the expected structure (`kae doctor <tool>` reports what was
detected) and refuse to write when the live layout is unrecognized
(exit code 10, `unsafe operation refused`).

## Claude Code

### Live auth locations

| Platform | Credential storage |
|----------|--------------------|
| macOS | Keychain generic password, service `Claude Code-credentials`; payload is JSON containing `claudeAiOauth` |
| Linux | `~/.claude/.credentials.json` (mode `0600`), key `claudeAiOauth` |
| Windows | `%USERPROFILE%\.claude\.credentials.json` (not supported in v0.1.0) |

`~/.claude.json` is **mixed state**: it contains `projects`, `mcpServers`,
onboarding, cache keys, and `oauthAccount`. kae switches `/oauthAccount` — the
identity claude displays — and nothing else in that file, by JSON pointer only.
The credential is still claude's sole *auth* artifact; the identity cache is
switched for correct attribution, because claude's **self-heal of it is
TTL-gated, not unconditional**:

- on startup it refetches the profile and rewrites `emailAddress` (and
  `profileFetchedAt`) only when the cached object is incomplete **or** its
  `profileFetchedAt` is more than **24h** old;
- a **token refresh** renews `profileFetchedAt` and the org/plan fields but
  rewrites neither `emailAddress` nor `accountUuid`;
- `claude /login` rewrites `accountUuid` / `emailAddress` / `organizationUuid`
  unconditionally — the TTL does not apply to it.

Since a credential in daily use refreshes well inside 24h, the cache is
effectively never stale and a switched token would leave the previous account's
identity on screen indefinitely (measured on Claude Code 2.1.220; supersedes the
2026-06-14 unconditional-self-heal finding — docs/SCOPE-MODEL.md §6).

Two consequences of the TTL that are easy to misread as an upstream change:

- **A switched identity's lifetime is its snapshot's.** kae applies the
  `profileFetchedAt` that was live at capture time, so applying a snapshot older
  than 24h makes claude refetch on its next start — and write the right email for
  the applied token by itself. kae's switch is what makes the identity correct
  *immediately*; a stale-but-correctly-switched snapshot converges either way.
- **Comparisons of that payload must be keyed, not byte-exact.** The refetch
  rewrites `profileFetchedAt` and the plan fields, so only the identifying keys
  may be compared when asking "is the live identity still the one kae applied?".
  The adapter declares them on the spec (`IdentityKeys`: `accountUuid`,
  `emailAddress`, `organizationUuid` — exactly what `/login` writes and a refresh
  does not), and `doctor identity_drift` compares only those. A credential is
  still compared byte for byte.

**Switched in every mode.** `kae use` / `kae add` switch the cache, and so do the
per-directory materializers (`writeDirIdentity`, alongside the credential and only
after it succeeds). Isolation modes point `CLAUDE_CONFIG_DIR` at a kae-owned
directory, where claude keeps its cache at `<dir>/.claude.json`, so that is where
the bound account's cache is written. A snapshot with no recorded identity applies
as **absent** — the cache is removed and claude refetches it — never left naming
the account the bind replaced. Before this, `kae pin <tool> <account>` copied the
credential and not the cache, so a bonded or isolated directory kept whatever
account first ran in it: auth was correct and the display was not.

**That file is therefore private in a bound directory, by denylist**, and the
reason is not shared by any other entry: a directory cannot both name its own
account and live-share the file that records which account it is. Where the file
sits depends on the environment — inside `CLAUDE_CONFIG_DIR` when the user sets one
(so an entry of the real home, which `prepareBond` would link) and at
`$HOME/.claude.json` when they do not (so not an entry at all, and never linked) —
which means it used to be shared for one configuration and private for the other,
by accident rather than by decision. Denying it makes both behave the same. The
cost falls on whatever else claude keeps in that file — `projects`, `mcpServers`,
onboarding, project trust, and anything it adds later — which stops being
live-shared into a bond dir for the users who had that by chance
([SCOPE-MODEL.md](SCOPE-MODEL.md) §6).

A guard behind it (`identityTargetEscapes`) declines any per-directory identity
write whose target resolves *outside* the store, warning on stderr and leaving the
credential in place. With the denylist it should not normally be reached; it stays
because the hazard it prevents is severe and reachable by other routes — a
hand-made symlink, `tools.claude.isolated_shared_items` naming the file, a future
tool with a mixed-state file kae has not denied. What it prevents: the pointer patch
resolves a symlink before reading and writing, so the shared file is updated rather
than forked by the atomic rename (an unresolvable link is refused, exit 10). That
is right for a global switch and wrong for a bind, where it would relabel the
**real** home with one directory's account — turning one directory's attribution
gap into a global one.

If `CLAUDE_CONFIG_DIR` is already set in the environment, the adapter uses it
as the live base path — for `.credentials.json`, for `.claude.json`, **and for
the keychain service name**, which claude namespaces as
`Claude Code-credentials-<sha8>` over the raw value of that variable (see
"Credential storage resolution" below). `auth` mode never sets or changes
`CLAUDE_CONFIG_DIR` itself.

`CLAUDE_SECURESTORAGE_CONFIG_DIR` displaces `CLAUDE_CONFIG_DIR` for both stores
at once, and set to the empty string it removes the namespacing entirely. kae
cannot keep a per-directory binding honest under either, so the adapter reports
the tool as unsupported (exit `5`) while that variable is present, rather than
writing a credential nothing reads.

`CLAUDE_CODE_CUSTOM_OAUTH_URL` renames both stores a second way: with a non-empty
value the build's OAuth suffix becomes `-custom-oauth`, which goes into the
keychain service name (`Claude Code-custom-oauth-credentials[-<sha8>]`) **and**
into the identity file name (`.claude-custom-oauth.json`). The adapter reports the
tool as unsupported (exit `5`) for the same reason. Unlike
`CLAUDE_SECURESTORAGE_CONFIG_DIR` an *empty* value is harmless — claude tests this
one for truthiness — so the refusal is on a non-empty value only. kae refuses
instead of computing the suffix because its other two values (`-local-oauth`,
`-staging-oauth`) come from the build channel, which a released binary hard-codes
and which no environment variable exposes ([ROADMAP.md](ROADMAP.md)).

### Drivers

| Driver | Platform | Switched artifacts |
|--------|----------|--------------------|
| `claude-file-patch` | Linux | `~/.claude/.credentials.json` pointer `/claudeAiOauth`; `~/.claude.json` pointer `/oauthAccount` |
| `claude-keychain-patch` | macOS | Keychain item `Claude Code-credentials[-<sha8>]` payload pointer `/claudeAiOauth`; `~/.claude.json` pointer `/oauthAccount` |

### Credential storage resolution

Where claude's credential lives is a **rule**, not a constant, and kae's own
isolation modes are what make the difference visible:

| `CLAUDE_CONFIG_DIR` | Keychain service | Plaintext fallback |
|---|---|---|
| unset | `Claude Code-credentials` | `~/.claude/.credentials.json` |
| set to `<dir>` | `Claude Code-credentials-<sha8>` | `<dir>/.credentials.json` |

`<sha8>` is the first 8 hex characters of `sha256` over the value of the
variable, **NFC-normalized** — no path resolution and no cleaning, so `/x/y` and
`/x/y/` are different items. Anything computing the name must hash exactly the
string it puts in the environment; `claude.keychainService` is the only place that
derives it.

The NFC step matters on macOS, where a path component with non-ASCII characters
(a home or `XDG_DATA_HOME`) can come back decomposed: claude normalizes before
hashing, so kae must too, or it writes an item claude never reads. Only the hash
input is normalized — the path itself stays byte-exact so it still resolves on
disk. This is the one thing kae needs `golang.org/x/text/unicode/norm` for.

Reads try the keychain first and fall back to the file. A write goes to the
keychain, and **deletes the plaintext file** when the item was absent
immediately before it. So a freshly materialized directory does authenticate
from a file kae wrote — until the first token refresh, after which only the
per-directory item is read. That is why kae writes the item, not the file, and
removes the superseded copy (see "Per-directory credential store").

The service name also carries the build's OAuth suffix (empty for the production
build, non-empty for a local build or with `CLAUDE_CODE_CUSTOM_OAUTH_URL` set).
kae models the production spelling only; a build with a non-empty suffix is
outside what it can switch ([ROADMAP.md](ROADMAP.md)).

The identity artifact (`oauth_account`) is declared **identity-only**: it records
who is logged in without being part of what authenticates. Losing it is therefore
safe, so a snapshot or backup that has no copy of it — captured before it existed,
or with its payload gone from the secret store — *removes* `/oauthAccount` rather
than failing the switch, and claude refetches the profile from the applied token
on its next run.

**An account captured before this artifact existed keeps working.** Switching to
it removes the stale cache (there is nothing recorded to apply) and claude
refetches the profile on its next start, so the account still displays correctly.
What is missing is only kae's own copy, which matters when claude cannot refetch
(offline) and for the moment right between the switch and claude's next start.

Recording it is a **one-time step per account**: start claude once, *then*
`kae add --no-login claude <account>`. In that order — re-capturing before the
refetch would store whatever account the stale cache still names. `kae doctor`
reports an active account that is still untracked, at `ok` level.

Note what is deliberately *not* done: the switch-away recapture does not adopt a
live identity into a snapshot that has none. It could, and that would migrate
accounts silently — but with nothing recorded to compare against there is also no
way to notice that the live identity belongs to a different account (someone
logged in outside kae), and adopting it would make the wrong identity this
account's recorded truth. A once-per-account manual step is the cheaper trade.

Two more consequences of kae switching a field claude maintains only lazily:

- **`kae add` still requires a credential.** An `/oauthAccount` alone is not a
  login — it outlives a logout — so capture refuses (`auth_missing`, exit 3)
  when the identity cache is the only live artifact.
- **A switch-away recapture keeps the recorded identity.** `kae use` refreshes
  the snapshot of the account it switches *away* from, but deliberately does not
  import the live `/oauthAccount`: it may name a different account than the live
  credential (exactly the drift this artifact fixes), and importing it would pin
  the wrong identity onto that account permanently. The exception is a live
  identity whose *identifying* keys changed: since `/login` writes them
  unconditionally, that means the live credential belongs to another account, so
  the recapture is **skipped entirely** with a warning rather than filing the new
  credential under the old account's name (offline, nothing could detect that
  afterwards — the access token is opaque).

The macOS driver reads and writes the keychain through the `security` CLI via
the runner seam. The captured keychain item is stored and restored
**verbatim** — the pointer `/claudeAiOauth` is only a structure guard (the
payload must parse as a JSON object containing it; otherwise the driver
refuses), never an extraction-and-re-encode path. Claude Code stores compact,
unsorted JSON and rejects a re-serialized payload, so the bytes must round-trip
unchanged. Because the claude keychain item has `claudeAiOauth` as its single
top-level key, capturing the whole item is equivalent to capturing that key.

#### File-driver override

`KAE_CLAUDE_DRIVER=file` forces `claude-file-patch` even on macOS, so the whole
round-trip (capture on `kae add`, apply on `kae use`) closes on
`.credentials.json` under `CLAUDE_CONFIG_DIR` with no `security` subprocess and
no real keychain access. It is read inside the adapter's `driver(env)`, so it
applies to both the capture and apply paths; overriding only one side would
break the round-trip. The only accepted value is `file` — any other value is
refused as unsupported rather than silently ignored. This is an **ephemeral
smoke/container escape hatch**: a live macOS claude reads the keychain, not the
file, so persisting it would silently break a real login. The persisted,
explicit opt-in counterpart is `[tools.claude]` `driver = "file"` (claude only;
the env var takes precedence; see [DATA-MODEL.md](DATA-MODEL.md)).

### Preserved (never touched in auth mode)

```text
~/.claude/settings.json        ~/.claude/CLAUDE.md
~/.claude/skills/              ~/.claude/agents/
~/.claude/.credentials.json    -> all keys except /claudeAiOauth
~/.claude.json                 -> all keys except /oauthAccount (projects,
                               mcpServers, onboarding, caches). kae writes only
                               that pointer in a bound directory too, but there
                               the file is <dir>/.claude.json and is *private*
                               (denylisted), so its other keys start from
                               claude's defaults rather than tracking the real
                               home's — see "Per-directory shared bind".
project/.claude/  project/CLAUDE.md  project/.mcp.json
MCP / hooks / permissions / trust state / session history / plugins
```

### Environment conflicts

`ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, and `CLAUDE_CODE_OAUTH_TOKEN`
override subscription login inside Claude Code. `kae doctor` warns when any of
them is set, because a switch would silently have no effect.

So does a **host-managed provider**, which is a third credential source rather
than an override of the switched one: with `CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST`
truthy, Claude Code reads the JSON file `CLAUDE_CODE_HOST_CREDS_FILE` names and
injects its token into the variable `CLAUDE_CODE_HOST_AUTH_ENV_VAR` names —
defaulting to `ANTHROPIC_AUTH_TOKEN`, but the host may name any variable, so kae
warns on `ANTHROPIC_UNIX_SOCKET`, `CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST`,
`CLAUDE_CODE_HOST_CREDS_FILE` and `CLAUDE_CODE_HOST_AUTH_ENV_VAR` — the mechanism
— instead of a destination a fixed list cannot know. These warn rather than making
the tool unsupported because they do not move kae's stores.

## Codex CLI

### Live auth locations

Codex keeps everything under `CODEX_HOME` (default `~/.codex`). Credentials
live in `~/.codex/auth.json` or in the OS credential store, selected by
`cli_auth_credentials_store = "file" | "keyring" | "auto" | "ephemeral"` in
`~/.codex/config.toml`. `auth.json` contains only authentication state
(tokens, account id, last refresh), so unlike `~/.claude.json` it may be
swapped as a whole file.

`auth` mode never sets or changes `CODEX_HOME`. If it is already set in the
environment, the adapter uses it as the live base path.

### Drivers

| Driver | Status | Switched artifacts |
|--------|--------|--------------------|
| `codex-auth-json` | implemented | whole `~/.codex/auth.json` (file mode `0600`) |
| `codex-keyring` | implemented (v0.8.3) | this codex home's `Codex Auth` keychain item (service + derived account), captured and restored verbatim |

Store selection by `cli_auth_credentials_store`, which is **upstream's four-value
enum**, not "keyring or else file":

| Value | What codex does | What kae switches |
|---|---|---|
| absent (the upstream default) or `file` | `CODEX_HOME/auth.json` | the file |
| `keyring` | the `Codex Auth` keychain item only | the item — **macOS only**: kae reads a keyring through the `security` CLI, so elsewhere this store is refused as unsupported (exit `5`) instead of yielding a spec whose every operation fails |
| `auto` | the item when it exists, `auth.json` when it does not | whichever one is live: kae probes for the item (attributes only) and resolves the same store codex will read. Off macOS it resolves to `auth.json` with no probe — that is where codex falls back with no keyring, but kae **cannot verify** that a Linux Secret Service is not holding the credential instead (an open row in [VALIDATION.md](VALIDATION.md)) |
| `ephemeral` | keeps it in memory for one process | nothing — refused as unsupported (exit `5`) |

Anything else, an unreadable/unparseable `config.toml`, and
`[features] secret_auth_storage = true` (which moves the credential into an
encrypted secrets file whose key alone is in the keyring) are **refused** rather
than treated as the file store. Folding every non-`keyring` value into the file
store is the codex-shaped version of the macOS pin defect: kae writes
`auth.json`, codex reads the item first, and every offline guard stays green.

With `auto` and neither store populated, "not logged in" is indistinguishable
from "not yet captured", so `capture` fails with `auth_missing` (exit 3) while
`switch` proceeds — switching to a captured account legitimately creates the live
state. `doctor` and `status` carry the same hint as a warning.

#### Keyring item contract (source-confirmed 2026-07-30 against `rust-v0.145.0`)

- **service** `Codex Auth` — shared by **every** codex home.
- **account** `cli|` + the first **16** hex characters of
  `sha256(canonical CODEX_HOME)`, where canonical means symlink-resolved and
  absolute (`codex-rs/login/src/auth/storage.rs`, `compute_store_key`). kae
  **derives** it (`codex.storeKey`) and never captures it from the live item.
- **payload** the whole `auth.json` JSON (`tokens`, `OPENAI_API_KEY`,
  `auth_mode`, `last_refresh`) — file-mode-equivalent content.

**Every** keychain artifact kae ships is identified by **service + account**, and
kae derives the account from the environment rather than reading it off the live
item: claude's is `$USER` (its own validated rule, falling back to
`claude-code-user`), cursor's the build-time constant `cursor-user`, agy's the
literal `antigravity`, codex's the hash below. Preferring the live item's account
— which kae did for claude and cursor until 2026-07-31 — makes one item created
under a stale account capture every later write while the tool reads the account
its rule names. `TestKeychainSpecsAreAccountScoped` refuses a spec that is not
scoped.

For codex specifically the item is identified by **service + account**, and one
service legitimately holds one item per codex home. Every read, write and delete is scoped to this
home's account (`KeychainMatchAccount`); the structure guard is that the payload
parses as a JSON object containing `/tokens`. Identity auto-detection reads that
payload's `id_token` email / `account_id` just like the file store.

Modelling the account as an opaque per-login id kae captured verbatim, under a
service holding one item, is what made a switch **destructive**: kae deleted the
item by service alone before writing, so switching codex removed another
`CODEX_HOME`'s login (shipped through v0.12.0). A codex switch now issues no
delete at all — an upsert of one item — and a `Codex Auth` item belonging to
another home is not capturable as this home's credential.

Also confirmed in the same file: after a successful keyring write codex
**deletes its `auth.json` fallback**, so a plaintext copy left beside a live item
is a credential nothing reads.

### Preserved

```text
~/.codex/config.toml  ~/.codex/*.config.toml  ~/.codex/hooks.json
~/.codex/history.jsonl  ~/.codex/logs/  ~/.codex/cache
project/.codex/  AGENTS.md  rules / hooks / MCP / skills
```

## Antigravity CLI (`agy`)

> **Note:** The `gemini` adapter was removed in v0.6.0 after upstream retired
> Gemini CLI for Antigravity on 2026-05-19. Captured gemini accounts remain on
> disk untouched; use `agy` going forward.

Antigravity CLI keeps its state under `~/.gemini/antigravity-cli/`
(`settings.json`, `log/`, `skills/`). On macOS the credential lives in the login
Keychain; on Linux/WSL headless setups it falls back to a credential file.

### Drivers

| Driver | Platform | Switched artifacts |
|--------|----------|--------------------|
| `agy-keychain` | macOS | Keychain item service `gemini`, account `antigravity`, captured and restored verbatim (matched by service **and** account) |
| `agy-file-snapshot` | Linux/WSL | whole files `credentials.enc`, `credentials.json`, `oauth_creds.json` under `~/.gemini/antigravity-cli/` (whichever exist; names cover observed versions) |

#### Keychain item contract (discovery 2026-06-18; driver shipped v0.8.6)

Real-machine discovery (an Antigravity login on macOS) found the credential in
the **login Keychain**, not a file:

- **service** `gemini` — **shared with the Gemini ecosystem**, so it alone does
  not identify agy's item.
- **account** `antigravity` — a **fixed literal** (not derived per tool home like
  codex's), so kae matches by **service *and* account** and never reads,
  writes, or deletes a `gemini` item under a different account.
- **payload** a single opaque ~686-byte token (single line, not JSON, not a
  JWT) — captured and applied **verbatim**. Structure guard = non-empty,
  single line (no JSON parse, unlike codex).

The `agy-keychain` driver reuses the verbatim-keychain pattern (as claude /
cursor / codex): capture reads the live `gemini`/`antigravity` item's payload;
apply upserts it back (`security add-generic-password -U -s gemini -a
antigravity`, matched on service+account, so a sibling item is never touched).
No delete-replace and no account reuse — the same shape codex's keyring item now
uses, with a literal account instead of a derived one.
`Detect`/`doctor` report the keychain item's presence on macOS; the
"kae cannot switch agy yet" warning is gone there. On Linux/WSL the file driver
is unchanged: when no credential file exists, `capture` fails with
`auth_missing`, and `doctor` warns the keyring may be in use.

**The keychain is not unconditional on macOS**, which the platform-only driver
choice above cannot express. Read from the 1.0.10 binary: agy's auth package
holds a keyring store *and* a file store behind one chooser, and the keyring half
is skipped or abandoned three ways — a `shouldBypassKeyring` decision next to an
ssh / wsl / container detector (`SSH_TTY`, `SSH_CONNECTION`, `SSH_CLIENT`,
`WSL_DISTRO_NAME`, `WSL_INTEROP`, `/.dockerenv`, `/proc/1/cgroup`), a **1s
timeout** on every keyring operation, and an explicit fallback on failure
("Failed to save token to keyring, falling back to file", with load and remove
variants). A remote-shell session on a Mac therefore reaches the file store.

kae **warns** (`env_conflict`) when one of those variables is set on macOS and
does not model the file store there: the fallback file's path is not derivable
from the binary — none of the three `credentialFiles` names occurs in it — so
declaring an artifact for it would write where nothing reads. The names in the
Linux row are the 2026-06-18 discovery, unverified against 1.0.10.

`kae add agy` is **`--no-login` only**: agy has no kae-drivable login
(authentication is GUI/browser OAuth via the Antigravity app — no
`login`/`auth`/`whoami` subcommand). agy's `Identity` reads the active Google
account email from `~/.gemini/google_accounts.json` (`.active`). **Caveat
(current Antigravity, 1.0.x): this file is legacy and may be stale.** Antigravity
resolves the live account from the opaque keychain token server-side and renders
it only in the interactive banner; it no longer writes the account to disk
(`google_accounts.json` is left at its old Gemini-CLI value, and the keychain
token cannot be decoded). So auto-detection may record an out-of-date identity or
none at all — this is expected, not a failure, and `kae add agy` succeeds either
way. To record the real identity, pass it explicitly:

```bash
kae add --no-login --identity <email> agy <name>   # at capture time
kae account set-identity agy <name> <email>         # backfill an existing account
```

identity is optional metadata (switching works without it); a missing one is
reported as a calm note, never an error. An explicit `kae add agy <name>` still
wins for the account name. agy home isolation (`use -i agy`) stays unsupported
(no redirectable home env var); only credential switching is added.
`ANTIGRAVITY_API_KEY` can be handled through env profiles (`kae env set agy ...`).

### Preserved

```text
~/.gemini/antigravity-cli/settings.json
~/.gemini/antigravity-cli/skills/  ~/.gemini/antigravity-cli/log/
plugins / MCP / hooks / subagents
```

## OpenCode

### Live auth locations

OpenCode keeps credentials in `$XDG_DATA_HOME/opencode/auth.json` (default
`~/.local/share/opencode/auth.json`, mode `0600`), one top-level key per
provider. The ChatGPT-subscription login (native since the OpenAI
partnership; the Claude subscription login was removed upstream in 2026-01)
is the `openai` key: `{type, refresh, access, expires, accountId}`.

This file is **mixed state**: sibling keys are independent provider
credentials (API keys added via `opencode auth login`), which belong to env
mode and must survive an account switch. It is patched via JSON Pointer
only, never replaced.

If `XDG_DATA_HOME` is already set in the environment, the adapter uses it as
the live base path (absolute values only — a relative value is ignored per
the XDG spec, as everywhere in kae). `auth` mode never sets or changes it.

Two ways that file stops being what opencode reads, both **warned** on
(`kae doctor`, `env_conflict`) rather than followed:

- **`OPENCODE_AUTH_CONTENT`** carries an entire auth.json body inline and is
  consulted *before* the file — `Auth.all()` returns
  `JSON.parse(process.env.OPENCODE_AUTH_CONTENT)` and never opens auth.json.
  opencode sets it itself for a workspace child process, so a session can
  inherit one.
- **a relative `XDG_DATA_HOME`**, which opencode joins against its own working
  directory (it has no absolute-path check). kae keeps the spec-correct reading
  and warns, because following upstream verbatim would resolve the value against
  *kae's* cwd instead — trading a visible divergence for an invisible one.

kae switches `auth.json` and nothing else. Two other stores exist and are
deliberately not modelled: `account.json` (`{version, accounts, active}`), which
1.17.3 **derives** from auth.json on every run and 1.17.4 stopped referencing at
all (the filename appears in 1.16.2 too, but a bare `auth list` there does not
produce the file), and the `credential` table in `opencode.db`, which 1.17.4
populates from auth.json exactly once behind a `data_migration` marker and then
leaves alone. Measured across 1.16.2 / 1.17.3 / 1.17.4 / 1.18.5: `auth list` and
`auth logout` read and write **auth.json**, so it is the live store — a logout
empties auth.json and leaves the imported DB row in place. That balance is what a
version bump has to re-check ([VALIDATION.md](VALIDATION.md)).

### Driver

| Driver | Platform | Switched artifacts |
|--------|----------|--------------------|
| `opencode-file-patch` | all | `auth.json` pointer `/openai` |

An `auth.json` that does not parse as JSON fails the structure guard on
read, and one whose root is not a JSON object is refused on write (both
exit 10) — the file is never replaced wholesale. An `auth.json` without an
`openai` entry is "not logged in" for kae: `capture` fails with
`auth_missing` (exit 3), and `doctor` / `status` explain that only the
ChatGPT subscription login is switched.

### Preserved

```text
~/.local/share/opencode/auth.json -> all keys except /openai
~/.config/opencode/               -> settings, skills, plugins
~/.local/share/opencode/storage/  -> projects, sessions
```

## Cursor CLI (`cursor-agent`)

### Live auth locations

| Platform | Credential storage |
|----------|--------------------|
| macOS | Keychain generic passwords, all under account `cursor-user`: `cursor-access-token`, `cursor-refresh-token`, `cursor-api-key`; each payload is an opaque token (the access token is a raw JWT), not JSON |
| Linux | `${XDG_CONFIG_HOME:-~/.config}/cursor/auth.json`, one JSON object `{accessToken, refreshToken, apiKey, bedrockCredentials}` — known but **not implemented** ([ROADMAP.md](ROADMAP.md)) |
| Windows | `%APPDATA%/Cursor/auth.json`, same object; unsupported |

cursor-agent picks the store by platform alone — keychain on darwin, the file
everywhere else — so there is **no file fallback on macOS** to check.
`AGENT_CLI_CREDENTIAL_STORE=memory` makes a single invocation persist nothing;
kae never sets it, and a shell that exports it makes any switch invisible to that
process.

`cursor-agent login` (browser flow) creates the access and refresh items;
`cursor-api-key` appears only for an api-key login (or after an api-key
exchange). There is no mixed-state file to patch.

**The three items are one credential.** cursor-agent's `setAuthentication` writes
access + refresh (+ the api key when the login had one) together and its
`clearAuthentication` deletes all three, so kae switches the set. Switching a
subset is not merely untidy:

- `cursor-agent status` reports `authenticated` only when the access **and** the
  refresh item exist (access alone is `partially-authenticated`), so a leftover
  refresh token from the previous account makes a mixed pair look consistent;
- with an api key present, cursor-agent re-mints an expiring access token from it
  — the **only** way it mints one, see the Freshness note below — and writes all
  three items back, so an unswitched api key silently restores the previous
  account.

Three further services exist under the same account —
`cursor-bedrock-access-key`, `cursor-bedrock-secret-key`,
`cursor-bedrock-session-token` — and kae deliberately **does not** switch them:
upstream writes them through a separate path (`setBedrockCredentials`, behind the
`cli_bedrock` feature), and they hold AWS keys rather than a cursor identity, so
one set can legitimately serve several cursor accounts of the same organization.
They are preserved, not captured. Their behaviour is unverified; switching them
would be widening the contract on an assumption.

`~/.cursor/agent-cli-state.json` holds only UI tip flags, not auth, and the
rest of `~/.cursor` belongs to the Cursor IDE (extensions, hooks); all of it
is preserved. The separate `Cursor Safe Storage` keychain item is the IDE's
Electron safeStorage key and is never touched.

### Driver

| Driver | Platform | Switched artifacts |
|--------|----------|--------------------|
| `cursor-keychain` | macOS | Keychain items `cursor-access-token` (`access_token`), `cursor-refresh-token` (`refresh_token`), `cursor-api-key` (`api_key`), captured and restored verbatim |

Each payload round-trips verbatim through the `security` CLI, ACL-preserving,
exactly as for claude — but they are opaque (raw tokens, not JSON), so there is no
JSON-pointer structure guard (an empty pointer marks the opaque payload; see
docs/DATA-MODEL.md). On a non-darwin platform capture / switch refuse with
exit `5` (unsupported).

None of the three is `IdentityOnly`: all are credentials, so an artifact absent at
capture (the usual case for `api_key`) is applied as absent and the live item is
**removed**. That is what stops a switch leaving the previous account's token
behind, and it is why a snapshot captured before kae switched the set — which has
no `refresh_token` or `api_key` entry at all — is refused with
`kae add --no-login cursor <account>` rather than partially applied. Recapturing
requires being logged in as that account, so migrating means one
`cursor-agent login` per account.

`access_token` is `specs[0]` by contract: `Detect` reads it as the artifact whose
presence means "logged in" (the api key is normally absent, and a refresh token
alone is not a login).

### Preserved

```text
~/.cursor/                     -> IDE extensions, hooks, agent-cli-state.json
Cursor Safe Storage (keychain) -> the IDE's Electron key, never touched
cursor-bedrock-* (keychain)    -> AWS keys behind the cli_bedrock feature,
                                  written by a separate upstream path
```

The Cursor IDE does not use the `cursor-*` services at all (it stores its login
through Electron safeStorage), so the switch reaches the CLI only.

## GitHub Copilot CLI (`copilot`)

Copilot is the odd one out: each account's OAuth token lives in its **own**
OS-keychain item (service `copilot-cli`, account `<host>:<user>`, e.g.
`https://github.com:main`) and the items **coexist** — logging into a second
account does not evict the first. "Switching accounts" is therefore not a
credential swap; it repoints the active account recorded in
`~/.copilot/config.json`.

### Live auth locations

`~/.copilot/config.json` (mode `0600`) is **JSONC** (a leading `//` comment
block) and mixed-state:

```jsonc
// User settings belong in settings.json.
// This file is managed automatically.
{
  "trustedFolders": ["/workspaces"],
  "lastLoggedInUser": {"host": "https://github.com", "login": "main"},
  "loggedInUsers": [{"host": "https://github.com", "login": "main"}]
}
```

**`COPILOT_HOME` moves that directory**, and kae honors it: the value replaces
`~/.copilot` outright — it is the config directory itself, not a parent — and it
is copilot's own sanctioned mechanism (the deprecated, hidden `--config-dir`
flag, which outranks it, tells users to "use COPILOT_HOME env var" instead).
kae passes it through verbatim, with no normalization and no absolute-path
check, because copilot applies none either
(`process.env.COPILOT_HOME ? process.env.COPILOT_HOME : join(homedir(), ".copilot")`).
Setting it also disables copilot's one-way `$XDG_CONFIG_HOME/.copilot` →
`~/.copilot` migration.

A **relative** value is followed but **warned** about (`kae doctor`,
`env_conflict`): copilot resolves it against *its* working directory and kae is
invoked from anywhere in the project, so the file kae writes is the file copilot
reads only while both run from the same directory. Verbatim is still the closest
kae can get — with the variable set there is no default to fall back to, unlike
opencode's XDG case — so the warning is the guard, and `kae rollback` inherits
the same condition because the backup records the target as given.

One branch of the precedence is **invisible to kae**: `--config-dir` is a CLI
flag, so a session that passes it reads a directory no environment can reveal.
That is recorded here rather than turned into a warning on `COPILOT_HOME`
([AGENTS.md](../AGENTS.md)).

kae targets `config.json` and not the bare `config` file copilot's
settings-migration loader falls back to when `config.json` is absent: no auth
path writes to that fallback — a login's `writeKey("lastLoggedInUser", …)` always
goes to `config.json` — so kae writes exactly where an upstream login does.

The CLI has no native account-switch or logout command (only `copilot login`,
an OAuth device flow). Tokens are env-overridable, precedence
`COPILOT_GITHUB_TOKEN` → `GH_TOKEN` → `GITHUB_TOKEN`.

### Driver

| Driver | Platform | Switched artifacts |
|--------|----------|--------------------|
| `copilot-config-pointer` | all | `$COPILOT_HOME/config.json` (default `~/.copilot/config.json`) pointer `/lastLoggedInUser` (JSONC; comments preserved) |

Only `/lastLoggedInUser` is switched. The per-account keychain tokens are
**never touched** (they coexist), so a switch only works between accounts
already present in `loggedInUsers` (logged in once via `copilot login`); kae
does not move tokens. The file is patched as JSONC so the leading comments,
trailing commas, and formatting survive (docs/DATA-MODEL.md). An unparseable
config refuses with exit `10`; a config without `/lastLoggedInUser` is
"not logged in" (`auth_missing`, exit 3).

Multi-account switching is verified only on a single account so far; the
cross-account behaviour (does repointing `/lastLoggedInUser` make copilot use
the other keychain token) is a v0.7.0 acceptance item (docs/ROADMAP.md).

### Preserved

```text
~/.copilot/config.json -> /loggedInUsers, /trustedFolders, /firstLaunchAt
~/.copilot/settings.json (hooks), AGENTS.md, hooks/, skills/, ide/, mcp config
the per-account keychain items (service copilot-cli) — never touched
```

### Environment conflicts

`COPILOT_GITHUB_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN` outrank the keychain login;
`kae doctor` warns when any is set. The gh CLI's own auth is separate and out
of scope.

## Isolation

`kae` provides two isolation scopes: **per-directory** (`kae pin -s|-i`) and
**global** (`kae use -i` / `kae run -i`). Overlay and home modes are retired
as of v0.8.0. `kae mise init` renders auth mode only; bind a directory with
`kae pin -s|-i`.

### Isolation env vars

The env var that redirects a tool to an alternate home directory:

| Tool | Isolation env var |
|------|-------------------|
| claude | `CLAUDE_CONFIG_DIR` |
| codex | `CODEX_HOME` |
| agy | none stable |
| opencode | none stable |
| cursor | none stable |
| copilot | none stable |

Tools with no stable isolation env var are skipped with an inline warning
comment in `kae pin` (they keep the real home). For `kae use -i` / `kae run
-i` with a **profile**, tools with no isolation env var are also skipped with
a warning and claude/codex are still isolated (exit `0`). A single-tool
explicit invocation on an unsupported tool exits `5`.

### Real-home resolution

When resolving the **real** home for shared-bind linking, an isolation env var
that points inside kae's own isolation data dirs is ignored (that is kae's own
redirection — e.g. exported by a pinned directory's mise fragment). Honoring
it would create self-referential symlinks (ELOOP); re-running `kae pin` repairs
any such stale links. A global command run inside a bound directory (`kae use`
/ `kae add`) resolves the real home automatically — it ignores the directory's
isolation env vars — and `kae use` warns that the change is global.

### Per-directory credential store

Every isolation mechanism — `kae pin -s`, `kae pin -i`, `kae use -i`,
`kae run -i` — works by pointing the tool's isolation env var at a kae-owned
directory. For a tool whose credential store is namespaced by that variable
(claude on macOS, see "Credential storage resolution"), the credential belongs in
**that directory's own store**, and one helper (`writeDirCredential`) is the only
thing that writes it — plus the identity cache that names it — for all four:

- the location comes from the adapter, resolved against an env whose isolation
  variable already points at the bound directory — never recomputed;
- on a keychain platform the per-directory **item** is written and the plaintext
  copy in the directory is removed, because nothing reads it once the item exists
  and the tool only deletes it itself when it finds no item;
- a failed keychain write is an error. It is never downgraded to a file write:
  that would report success while leaving the directory reading something else;
- the payload comes from the **account's snapshot**, never the live store — the
  live store holds whichever account is globally active, which is the account
  being bound only by coincidence;
- the snapshot's payload **shape** must match the artifact being written.
  `KindFile` and `KindKeychain` hold a whole document, `KindJSONPointer` holds only
  the value under its pointer, and the two are not interchangeable: applying a
  whole document through a pointer spec nests it under its own key
  (`{"claudeAiOauth":{"claudeAiOauth":…}}`) and succeeds, which the tool reads as
  malformed. Capturing under one claude driver and applying under the other is
  enough to reach it, so a mismatch is refused (exit `10`) with recapture guidance,
  on this path and on the global switch alike. Rollback needs no check: it rebuilds
  the spec from the backup record, so the kind is the captured one by construction;
- a keychain item is written **only** when the spec marks it
  `KeychainDirBindable`, i.e. the adapter declares that the **identity** of the
  item kae resolves — its service name, its account attribute, or both — moves
  with the isolation env var. Anything else is reported as unisolatable and
  nothing is written to it;
- the account's **identity-only** artifacts follow, last and only once the
  credential write has succeeded, so the directory names the account it is
  authenticated as. The order is not interchangeable: a directory labelled with an
  account whose credential kae could not put there is worse than an unlabelled one,
  because the label is what a user checks. Every early exit above skips the identity
  too, and so does a target that resolves outside the store (see "Identity cache"
  under claude); that one warns;
- an identity write that fails for any *other* reason — a malformed mixed-state
  file the tool left behind, a momentarily unreadable secret store — also warns
  rather than failing. The credential is already written and correct, an identity is
  a label the tool can rebuild, and returning here would abandon the caller
  mid-bind: `kae pin` gives up before writing its mise fragment, leaving a fresh
  private credential with no binding pointing at it.

That last rule is a hard safety boundary, not a nicety, and the declaration is
per-adapter with the safe default (`false`). codex is why: `kae pin -s` symlinks
`config.toml` from the real home into the bound directory, so a user who set
`cli_auth_credentials_store = "keyring"` resolves the keyring there too. Its item
*is* scoped by `CODEX_HOME` — through the account attribute rather than the
service name — but kae has never verified a bound directory end to end there (does
codex canonicalize the bond dir to the same path kae hashes?), so the capability
stays **undeclared rather than assumed**. Nothing is written, not even the
credential *file*: codex reads the keyring first and deletes that file on its next
write, so a file would be a plaintext secret nothing reads. The warning says the
directory may have no codex login until you log in inside it — which is what
actually happens, since the bound directory resolves a different item.

Two situations are tolerable rather than fatal, and both warn: an account with no
captured credential, and a tool whose credential store kae cannot bind per
directory. Binding a set
of tools resolved from a profile continues past either — the other tools bind,
the tool's own settings and sessions are still isolated, and the directory works
once the account is captured. An operation naming one tool and account
(`kae pin <tool> <account>`, or `kae run -i <tool> <account>`) fails instead:
there the unisolatable tool is the whole request, not one row of it.

### Per-directory shared bind (`kae pin -s`)

Uses a *denylist*: every real-home entry is symlinked into the shared directory
(`isolation/<pin-id>/<tool>/shared/`) **except** the entries below. Those are
private-copied (not symlinked), so who the directory is logged in as is private to
it while all other files — settings, sessions, memory, MCP configs — stay shared
with the real home.

Hard-coded denylist (always excluded from symlink sharing) — one table in the code,
`constants.PrivateBindItems`, shared with the config validation below so a name
cannot be denied here and permitted there:

```text
claude: .credentials.json  (Linux-only; macOS uses keychain — harmless to list)
        .claude.json       (the /oauthAccount identity cache — see "Identity
                            cache"; only an entry of the real home when the user
                            sets CLAUDE_CONFIG_DIR, denied on all platforms so
                            both configurations behave the same)
codex:  auth.json
```

Unknown files a future tool version adds are **shared by default** (the inverse
of isolated-bind's fail-safe), because shared-bind's purpose is sharing — a new
file is more likely config or memory than an auth secret.

To add extra items to the denylist:
`tools.<tool>.shared_denylist_extra` (bare file names; the hard-coded entries
above are refused at config load to avoid confusion).

A real file already present in the shared directory is treated as a private
override and is never replaced or linked over. A **symlink** by the name of a
denied entry is retracted instead — otherwise the denylist would govern only new
bond dirs, and a directory bound before an entry was denied (by an upgrade, or by
`shared_denylist_extra` gaining a name) would go on sharing what it was told to
stop sharing.

### Per-directory isolated bind (`kae pin -i`)

Uses a per-account *private config dir*
(`isolation/<pin-id>/<tool>/isolated/<account>/config/`): nothing is shared
with the real home by default. Items explicitly listed in
`tools.<tool>.isolated_shared_items` (bare file names) are symlinked from the real
home; the credential and identity are private-copied.

The opt-in list refuses **the same set the shared bind denies** — the same table, not
a copy of it — and for the same two reasons: `.credentials.json` / `auth.json`
because the directory must authenticate as its own account, and `.claude.json`
because it must be able to *name* its own account. That second one is the
correction: this field's rule used to be about credentials only, so the identity
cache — which is not one — could be opted in, and a link back to the real home then
made every isolated directory display whatever the real home displayed, whichever
account it was logged in as.

`isolated_shared_items` is the opt-in share list: default is empty (full
isolation). Re-running `kae pin` refreshes opt-in shared-item links and the
credential copy. It does **not** retract a link for an entry you *remove* from the
list, and neither does the shared bind for a stale link whose name is no longer an
entry of the real home ([ROADMAP.md](ROADMAP.md)).

Re-bind one tool to another account with `kae pin <tool> <account>`:

- **shared (`pin -s`)**: the credential file is overwritten in the
  account-agnostic shared dir (`isolation/<pin-id>/<tool>/shared/`); the new
  account is recorded in the kae-owned mise fragment.
- **isolated (`pin -i`)**: a new per-account config dir is prepared
  (`isolation/<pin-id>/<tool>/isolated/<account>/config/`) with opt-in shared
  links and the new credential; the kae-owned mise fragment's env entry is
  updated to point at it.

In both cases the tool picks up the new account on next launch with no change
to sessions or settings, and `KAE_PROFILE` is recomputed (ad-hoc when no
profile matches).

### Global isolated home (`kae use -i` / `kae run -i`)

Both `kae use -i` and `kae run -i` use the same per-account store:
`isolation/global/<tool>/<account>/`. State written by one is visible to the
other, so the shared location is never invisible: `kae run -i` prints the exact
home and that it is shared with `kae use -i <account>`, and `kae status`
surfaces the global-isolated homes.

`kae run -i` runs the child in this home with no lock and no live mutation —
concurrent `kae use` in other terminals is never blocked and never seen by the
isolated process. `kae run -s` (default) uses the real home, holds the per-tool
lock for the full child run, and restores the previous login.

## Login Commands

`kae add` launches the official flow and captures the result:

| Tool | Command |
|------|---------|
| claude | `claude /login` |
| codex | `codex login` |
| agy | unsupported |
| opencode | `opencode auth login` |
| cursor | `cursor-agent login` |
| copilot | `copilot login` |

The opencode flow is a provider picker; picking a provider other than the
OpenAI subscription leaves `/openai` unchanged, so `kae add` correctly
refuses with `auth_unchanged` (exit 11) — kae switches only the
subscription login.

## Account Identity (auto-detection)

`kae add <tool>` with no account name defaults it to the live login identity,
read through the optional `adapter.Identifier` capability
(`Identity(ctx, env) (string, error)`). The raw identity is sanitized into an
account name by `cmd` (`[a-zA-Z0-9._-]`, email → local part before `@`, capped
at 64); an explicit name always wins. The per-tool source:

| Tool | Identity source |
|------|-----------------|
| claude | `~/.claude.json` `oauthAccount.emailAddress` — also a switched artifact, so it names the account kae last applied, not the one that logged in last |
| codex | `auth.json` `id_token` email claim (JWT), else `tokens.account_id` |
| opencode | the `/openai` access token's `https://api.openai.com/profile` email claim (JWT), else `/openai` `accountId` (an opaque UUID; v0.8.8 prefers the email) |
| copilot | `config.json` (JSONC) `/lastLoggedInUser.login` |
| agy | `~/.gemini/google_accounts.json` `.active` — the active Google account email the Antigravity login writes (v0.8.7; the keychain token itself is opaque) |
| cursor | `cursor-agent status` prints `✓ Logged in as <email>` (discovery 2026-06-16: single line, no ANSI, exit 0); the `Identifier` (v0.8.3) parses the text after `Logged in as ` through the runner seam. A non-zero exit, a missing marker, or an empty identity is a detection failure. `cursor-agent status` may hit the network — acceptable on the interactive `kae add` path. |

A detection failure (logged out, unreadable, or sanitizes to empty) is a usage
error naming the explicit form, never a silent fallback. Identity reads only
already-trusted live state; it never verifies a JWT signature (the shared JWT
claims decoder is `internal/jwt`).

## Adding A New Adapter

1. Document the live auth locations, drivers, preserved paths, and environment
   conflicts in this file first.
2. Implement `adapter.Adapter` with capture/apply/verify built from `artifact`
   primitives (`json-pointer`, `file`, `keychain`) so backup/rollback and
   redaction come for free.
3. Add structure guards: refuse unknown layouts instead of writing.
4. If the credential is a refreshable OAuth/JWT token, implement
   `adapter.Fresher` (`Freshness(payload) freshness.Info`) using the primitives
   in `internal/freshness` (JWTExpiry / EpochToTime / DecodeObject / …) so the
   switch-time stale warning and `doctor credential_stale` can read its expiry
   and refresh-token state; a static API key or a pointer-only artifact stays
   not-datable — just omit the method (Known=false). Fill
   `RefreshExpiresAt` when the payload publishes the refresh token's own expiry,
   and set `Revoked` for a payload the tool has explicitly emptied or revoked.
   Both are per-tool readings and belong here, not in `internal/freshness`, which
   holds no per-tool knowledge. See the per-tool field map in
   [DATA-MODEL.md](DATA-MODEL.md).
5. Optionally implement `adapter.Identifier` so `kae add <tool>` can default the
   account name (above). Skip it when the tool exposes no readable identity.
6. Implement `VerifiedVersion() string` with the upstream version you verified
   the adapter against, and add that tool's behaviour assumptions to the table in
   [VALIDATION.md](VALIDATION.md) "Upstream Behaviour Assumptions". This one is a
   method of `adapter.Adapter`, not an optional interface — see "Verified Upstream
   Versions" below. Then measure that tool's literals into
   [VALIDATION.md](VALIDATION.md) "Upstream Literal Fingerprints" — or, if the tool
   has no readable artifact, record why in `fingerprintExclusions`. This is enforced:
   `TestUpstreamLiteralFingerprints` fails for a tool that has neither.
7. Add fake-runner / temp-HOME tests for capture, apply, missing-auth, and
   guard-refusal paths.

## Verified Upstream Versions

Each adapter declares, via the `Adapter` method `VerifiedVersion()`, the upstream
release its behaviour assumptions were last verified against. `kae doctor`
compares the installed `<binary> --version` against it and warns
(`upstream_version`) when the installed tool is a newer **major or minor**; a
patch bump is silent.

This table is **machine-checked**: `TestVerifiedVersionsMatchTheDocs` parses it
and fails when a cell disagrees with the adapter, so the lockstep below cannot be
half-done. Do not reformat the rows without updating that test.

| Tool | `VerifiedVersion()` | `VerifiedOn()` | `--version` output shape |
|------|---------------------|----------------|--------------------------|
| claude | `2.1.220` | `2026-07-31` | `2.1.220 (Claude Code)` |
| codex | `0.145.0` | `2026-07-31` | `codex-cli 0.145.0` |
| agy | `1.0.10` | `2026-07-31` | `1.0.10` |
| opencode | `1.17.4` | `2026-07-31` | `1.17.4` |
| cursor | `""` (no signal — see below) | `2026-07-30` | `2026.06.16-20-30-07-<sha>` (date-versioned) |
| copilot | `1.0.61` | `2026-07-31` | `GitHub Copilot CLI 1.0.61.` (note the trailing period) |

`VerifiedOn()` is the half the version cannot supply. `upstream_version` only
fires when the installed tool moves past the verified release, so a user who
never upgrades gets **no signal at all** — and cursor, which declares no usable
version, would get none ever. The date closes both: `kae doctor` warns once an
assumption set has gone unchecked for six months. Six, not one or three, because
the version check already covers the case where something actually changed; this
one exists for the case where nothing did, and a warning that fires while the
answer is still right is the kind users learn to scroll past.

The parser takes the leftmost `<major>.<minor>.<patch>` triple of stdout, which
reads all of the shapes above; `TestParseUpstreamVersion` pins the real outputs so
a new shape cannot silently start reporting the wrong version.

**cursor is deliberately exempt.** Its date version parses as a triple, so the
comparison reads the *month* as the minor: the first build of any new month is
"past" the verified one, and doctor would warn every month about a tool built
daily until a human edited the constant. A monthly nag is exactly what the silent
patch bump exists to avoid — a false warning trains the user to ignore the real
ones — so cursor returns `""` and doctor skips it. Its verified date stays
recorded in [VALIDATION.md](VALIDATION.md), which is where the re-verification
actually happens.

**Every other adapter must declare one**: `VerifiedVersion()` is a method of
`adapter.Adapter`, so the compiler stops a new tool that omits it, and
`TestVerifiedVersionFormat` (driven off `constants.Tools`) rejects a value that is
neither a triple nor `""`. kae depends on undocumented upstream *behaviour* as
well as layout, and a behaviour-only change passes every structure guard. When you
re-verify a tool's rows in [VALIDATION.md](VALIDATION.md), bump
`VerifiedVersion()` and the recorded version there in the same commit — the check
is only as honest as those two staying in step.
