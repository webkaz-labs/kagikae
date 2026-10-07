# Release Acceptance

Release-specific assessments distinguish isolated fixtures, installed-tool
observations and live account checks. Read the applicable section before running
it: a fixture result does not establish live credential health. Account
combinations outside the release run are classified in
§ Optional account-combination checks. [VALIDATION.md](VALIDATION.md) owns the
commit gate and the smoke procedures beside the surfaces they check.

**Every result is recorded here, under the check it settles, naming the exact
candidate revision it was run against and the release tag when one exists.** This
document owns the results; results recorded elsewhere are invisible to the next run.

## codex resident processes — local acceptance (2026-10-06)

Observed on 2026-10-06 on one macOS machine with codex 0.160.1, the managed daemon
of the real codex home, a live ChatGPT login and the ChatGPT app installed, while
main was at `d3bbd47`; the kae build the operator ran is not part of the record.
This is the first part of the acceptance [ROADMAP.md](ROADMAP.md) § Current work
order tracks for codex resident processes; unlike the 0.160.0 observation in
[ADAPTERS.md](ADAPTERS.md) § Resident processes, it ran daemon commands. No
re-executor reaches it.

| Question | Observed | Not measured |
|---|---|---|
| What `daemon version` and `daemon restart` print | `codex app-server daemon version` printed one JSON object: `status` (`running`), `backend`, `managedCodexPath`, `managedCodexVersion`, `socketPath`, `cliVersion`, `appServerVersion`. `restart` printed the same shape with `status` `restarted` and a `pid`. | |
| Does `daemon version` start a daemon | Under a `CODEX_HOME` with no daemon it exited 1 with `failed to connect to <home>/app-server-control/app-server-control.sock`, and no daemon started. | |
| Does `daemon version` need the network | Under `sandbox-exec` with IP send and receive denied it printed the same output in 0.07 s. | Whether it attempts a connection that the sandbox refused. |
| What a daemon holds after a switch | A daemon started hours before kae switched `auth.json` answered `account/read` with `refreshToken: false`, some time after the switch, with `account` null, `workspaceRouting` null and `requiresOpenaiAuth` present. It held no account, not the previous one. The value of `requiresOpenaiAuth` in that answer was not recorded; the restarted daemon, under the default `model_provider`, answered `true`, and upstream derives it from the provider rather than the login. The live credential was a ChatGPT login with `tokens.account_id`; kae read the daemon as `unknown` and only warned. | Whether `account/read` itself contacts the network. |
| Restart and its stdio | `codex app-server daemon restart 2>&1 \| cat` finished in 0.45 s, so the daemon it starts closes the stdio it inherits. After it, `account/read` named `account` (`type`, `email`, `planType`) and `workspaceRouting` (`chatgptAccountId`, `backendOrigin`, `accountRoutingOverride`), and kae doctor's daemon warning was gone. | |
| Daemon and credential namespace | `workspaceRouting.chatgptAccountId` after the restart equalled the live `tokens.account_id`. | |
| Rollout and credential namespace | In 7 of the shared home's 60 newest rollouts, the first line's `session_meta.payload.creator_account_id` equalled the live `tokens.account_id`; the rest did not. | |
| `osascript` and Automation (TCC) | `osascript -e 'application id "com.openai.codex" is running'` returned `true` in 0.06 s without a permission dialog. | Whether `quit` needs the Automation permission, which the second part below answers for this machine; the app's y / n paths and its quit confirmation dialog, of which the third part below records the y path and the dialog. |
| Daemon and `PATH` versions | The daemon runs from its own copy of codex under `~/.codex/packages/app-server-daemon/`, apart from the `codex` on `PATH`; both were 0.160.1. `codex app-server daemon update` exists. | What a restart does when the two versions differ, which the fourth part below answers. |
| Which sessions use the daemon | An existing `codex resume` TUI process was not connected to the daemon's socket. | Whether a newly started TUI connects, which the second part below answers. |

Also not measured in this first part: whether a resident process still on the old
account writes a refreshed token back to `auth.json`, which the fifth part below
answers; the second part below records what a round trip of switches did to the
account left behind.

### Second part: switch round trips

Measured by the operator's agent on 2026-10-06 on the same machine with codex
0.160.1, switching with kae between two ChatGPT accounts, main and side, while main
was at `cba31a9`; the kae build the operator ran is not part of the record. Each row
is bounded to what was inspected; an inference is marked as one.

| Question | Observed | Not measured |
|---|---|---|
| What happens to the account a switch leaves | Over round trips between main and side, upstream invalidated the refresh token of the account a switch had left: the daemon's log showed `token_revoked` / `refresh_token_invalidated`, while kae's credential for that account still matched its account id and its access token had not expired. With the daemon, the ChatGPT app and a `codex resume` TUI running since 2026-09-30 and not connected to the daemon all resident, it happened twice in 3 round trips. With the daemon alone it did not happen in 4 round trips, nor with the daemon and the app quit and relaunched by `--yes` each time in 4. What invalidated it is not established: the fifth part below shows that a process on the old account neither refreshes nor writes while the disk holds another account; a refresh in flight across a switch is one mechanism that would supersede the snapshot, but it was not shown to be the one. Logging in again with `kae add --restore codex <account>` (or `kae add codex <account>` while it is live), which updates the snapshot, recovered the account. | What invalidated the token. |
| Does a newly started TUI use the daemon | A `codex` TUI started with this version was connected to the daemon: the peer of the socket the daemon had accepted was the TUI's socket. After a switch restarted the daemon, the TUI's `/status` showed the new account and it still answered. | |
| What `--no-restart` leaves running | After a switch with `--no-restart` the daemon still used the previous account 0, 20, 40 and 60 s later: it did not load the new `auth.json`. A TUI connected to it showed the previous account meanwhile. | |
| Does the ChatGPT app follow a switch without a relaunch | After one switch without a relaunch the app showed and used the account on disk; after the next it created a new task on the previous account (the new rollout's creator did not match the live credential). It does not follow a switch reliably. | |
| `quit` and Automation (TCC) | Quitting the app (`osascript` quit) and relaunching it (`open -b`) showed no Automation permission dialog: by the operator's recollection, and in 4 `--yes` runs without a terminal that each settled `relaunched`. | Whether a permission granted earlier was in effect. |
| Does the daemon contact the network | Right after it started, the daemon connected to chatgpt.com, to fetch the model list and refresh the token (its log showed a 401). | Whether `account/read` right after a restart connects, which the fourth part below answers. |
| Which client name the daemon's threads carry | After a restart, kae's probe was the first client to initialize (`clientInfo.name` `kae_probe`), and a thread a TUI then created through the daemon recorded `kae_probe` as its rollout's `originator` (one case). Inferred, not measured: the daemon gives later threads the name of the first client that initialized. | Whether the name is the first client's, which the fourth part below answers. |

### Third part: idle reads, running tasks and the quit dialog

Measured by the operator and the operator's agent on 2026-10-06 on the same machine
with codex 0.160.1 on macOS, while main was at `843a981`; the kae build the operator
ran is not part of the record. It answers what the first two parts left open about
`account/read` in a steady state, a task running across a restart and the app's
quit dialog. Each row is bounded to what was inspected; a source reading and an
inference are marked as such.

| Question | Observed | Not measured |
|---|---|---|
| Does `account/read` contact the network | While three `account/read` requests with `refreshToken: false` went to a daemon in its steady state, `lsof -i` on the daemon's IP sockets every 50 ms saw no connection. The same watch caught 26 TCP connections of the daemon during a `codex app-server daemon restart`, so it does see them. | `account/read` right after a restart, when the daemon's routing cache is presumably empty: its traffic cannot be told apart from the daemon's start-up traffic; the fourth part below answers it. |
| What a restart does to a task running in a connected client | A `codex` TUI started on a pseudo-terminal, connected through the daemon, ran a task containing `sleep 30`. A `codex app-server daemon restart` called meanwhile did not return until the task finished (about 22 s); the task completed and answered, and the new daemon started right after it finished (by the processes' start times). Read in upstream's source, not measured (rust-v0.160.1's `codex-rs/app-server-daemon`: README lines 70 to 74, `restart_with_settings` in `lib.rs`, `DEFAULT_SHUTDOWN_GRACE_SECONDS` in `settings.rs`, and `stop_with_grace` in `backend/pid.rs`; read 2026-10-06): the restart stops the old daemon with `stop_with_grace(shutdownGraceSeconds)`, which waits up to that grace (60 s by default, 0 to 300 s) for running tasks and then forces the daemon down, and the restart command itself then starts the new daemon. Inferred from that reading: a restart command killed between the stop and the start leaves no daemon running. | A task that runs past the grace; whether a restart command kae has stopped waiting for goes on to start the new daemon, which the fourth part below answers. |
| Does `codex exec` use the daemon | `codex exec` did not connect to the daemon: it was the peer of none of the sockets the daemon had accepted, and it ran on its own. | |
| What the ChatGPT app does with a quit while a task runs | With a task running in the app, answering `y` to `kae use` made the app show a quit confirmation dialog. After 20 s kae warned that the app had not quit in time and settled `quit_timeout`; the daemon had restarted. When the user then chose to quit in the dialog, the app quit and was not started again. | The `n` answer on a real app, which the fourth part below answers. |

### Fourth part: a restart past kae's limit, the app's n and an isolated daemon

Measured by the operator and the operator's agent on 2026-10-06 on macOS with codex
0.160.1, while main was at `52750d2`. The first three rows ran the `kae` at
`~/.local/bin/kae`, built from main at `52750d2`, on the same machine, switching between
main and side; the last three in an isolated codex home, described below. It
answers what the first three parts and [ROADMAP.md](ROADMAP.md) § Current work
order left open, except whether a resident process still on the old account writes
a refreshed token back. Each row is bounded to what was inspected; a source reading and an inference are marked as such. Source
readings are of rust-v0.160.1, with paths relative to `codex-rs/`, read 2026-10-06.

**The isolated codex home.** Under `env -i`, `CODEX_HOME` and `HOME` pointed to a
temporary directory holding a made-up ChatGPT-shaped `auth.json` (an unsigned JWT
and a made-up account id, file store); `HTTP_PROXY`, `HTTPS_PROXY` and `ALL_PROXY`
pointed to a local recording proxy that logged each `CONNECT` and answered 403; and
`sandbox-exec` denied outbound traffic other than to localhost, and DNS. The daemon
inherited both: `CONNECT`s reached the proxy, and the sandbox logged denials under
the daemon's pid. Limits: a stand-in for `ps` was on `PATH`, and the daemon's
updater did not run because of the socket path's length.

| Question | Observed | Not measured |
|---|---|---|
| Does a restart kae stopped waiting for (`restart_pending`) go on to start the new daemon | A newly started `codex` TUI, connected through the daemon, was running a task containing `sleep 90` when `kae use codex main` ran. kae warned `restart_pending` at 30 s and returned (47 s in all, a prompt included). The restart command kept running after kae returned: 60 s after it started the old daemon was gone, 1 s later the new daemon started, and the command exited (pids recorded by `pgrep` every 0.5 s). `kae doctor` then gave no daemon warning: the new daemon held the live account. In an earlier run in the same TUI, with `sleep 45` and `kae use codex side`, kae also warned `restart_pending` at 30 s; the command stopped the old daemon, started the new one and exited 30 s after it started, as the task finished (the TUI showed "Worked for 52s" and answered). That coincided with kae's limit, so it does not show the command going on alone. | |
| What a restart does to a task that runs past the grace | In the `sleep 90` run above, the TUI's task ended with "Conversation interrupted" when the old daemon went down, 60 s after the restart started (upstream's default grace). The TUI then went on with the conversation; inferred, not measured, that it reconnected to the new daemon. The interrupted `sleep` left no state to inspect; running `sleep 90` again completed. | How the TUI reconnected. |
| What the ChatGPT app does with the `n` answer | With the app running during both runs above, the operator answered `n` to kae's prompt both times. kae gave its `declined` warning, that the app keeps the codex account it started with until it is quit and reopened, did nothing to the app, and the app stayed running. | |
| Does `account/read` contact the network right after a restart | In the isolated home, after a restart and once the daemon's start-up traffic had stopped, the outbound attempts per window were 0 with no client (the control), 1 after `initialize` alone and 2 after `initialize` and `account/read` with `refreshToken: false`, the same in each of 3 runs. While those attempts failed, `account/read` answered in about 6 ms with the JSON-RPC error `-32603` `workspace routing discovery failed`, not with a null `account`. With `chatgpt_base_url` pointed at the proxy the request was `GET /backend-api/wham/accounts/check`, and the daemon sent it itself right after it started, with no client connected. Read in upstream's source: `read_account` in `app-server/src/request_processors/account_processor/workspace_routing.rs` answers from a cache (lines 278 to 288), otherwise calls `BackendClient::get_accounts_check` (lines 295 to 301; `backend-client/src/client.rs` lines 398 to 408), caches only a success (lines 357 to 371) and turns a failure into `DiscoveryFailed` (lines 320 to 325); `connection_initialized` in `app-server/src/message_processor.rs` (declared at line 811, the call at lines 816 to 817) calls `notify_workspace_routing_to_connection` (lines 131 to 155 of the same `workspace_routing.rs`), so `initialize` alone runs the discovery. The daemon spawns a `read_account` of its own when it starts (`app-server/src/request_processors/account_processor.rs` lines 136 to 139), and discoveries for the same key share one request (`workspace_routing.rs` lines 259 to 277 and 374). The cache key includes the daemon's auth generation (lines 253 to 258, compared at line 286, and a discovery that recovered from a 401 takes the new generation at lines 309 to 313; the cache is cleared at line 377 when there is no ChatGPT account to route), so after the daemon's own auth changes the cache no longer answers. On a 401 the discovery runs the daemon's unauthorized recovery and retries (lines 303 to 319): it reloads its stored login (`auth.json` or the keyring item) only when that names the account the daemon holds, and otherwise stops without refreshing; a 401 after that reload refreshes the token (`login/src/auth/manager.rs` lines 1976 to 2017, with the account check in `reload_if_account_id_matches`). The cache is held only in the process's memory. So the daemon's own discovery at start-up fills the cache when it succeeds, and a later `account/read` then makes no connection, as the third part saw in a steady state; when that discovery has not succeeded — offline, for example — or the daemon's auth has changed since, even `initialize` makes the daemon connect, and a failing discovery makes `account/read` answer with the error. Read in kae's source: kae makes no connection itself, and `askResidentDaemon` in `internal/cmd/resident_probe.go` reads an error answer as `unknown`, on which kae does not restart. | Whether the start-up discovery succeeds on a real network. |
| Which client name the daemon's threads carry | In the isolated home, right after a restart, a client named `first_client` initialized and closed, then `second_client` sent `thread/start` and `turn/start`: the rollout's `session_meta.payload.originator` and the `threads` table of codex's state named `first_client`. With the order reversed they named `second_client`; with one client alone, `solo_client`. The `userAgent` that `initialize` answered to the second client also began with the first client's name. The rollout was written on `turn/start`, not on `thread/start` alone. Read in upstream's source: the originator is set once per process (`login/src/auth/default_client.rs` line 54 and lines 85 to 98); `app-server/src/request_processors/initialize_processor.rs` sets it from the first client's `clientInfo.name` (lines 130 to 132 and 165 to 180), except that the reserved names `codex_app_server_daemon` and `codex-backend` (line 19) leave it unchanged; threads take it in `core/src/thread_manager.rs` (lines 372 to 384). The first originating client also settles the legacy automatic login: unless it asks for explicit gateway OAuth, it allows automatic login, and a later client that asks for explicit login overrides that (`initialize_processor.rs` lines 153 to 164). | What allowing automatic login changes in practice. |
| What a restart does when the `codex` on `PATH` and the daemon's copy differ in version | Read in upstream's source: `restart_with_settings` in `app-server-daemon/src/lib.rs` (lines 459 to 506) calls `prepare_install::prepare` with `InstallMode::Missing`, which does nothing when a daemon is running or a `current` package exists (`prepare_install.rs` lines 104 to 117 and 136 to 138); the crate's README (lines 127 to 128) says "lifecycle commands use the selected daemon package, regardless of the invoking CLI version; they do not implicitly replace an existing package". With no managed copy it copies the invoking CLI's package rather than downloading one (`prepare_install.rs` lines 146 to 262). Replacing the package is the explicit `daemon update --from-cli`; fetching the newest is `daemon update` and the updater (`update_loop.rs` lines 63 to 66, `https://chatgpt.com/codex/install.sh`). Observed in the isolated home: the first `daemon start` copied the 0.160.1 CLI's package to `releases/0.160.1-…`, with no outbound attempt from the CLI. With `current` pointed at a wrapper that answered `--version` with 0.159.9 and ran the real 0.160.1 otherwise, `restart` printed `managedCodexVersion` 0.159.9 and `cliVersion` 0.160.1 and started the app-server through the wrapper; `current` and `releases` were unchanged, and the CLI made no outbound attempt. | A real other version; what the updater does. |

### Fifth part: an old-account process refreshing its token

Measured by the operator's agent on 2026-10-07 on macOS with the installed codex-cli
0.160.1, in isolated codex homes, while main was at `4a18a5c`; no real credential or
account took part, and kae did not run. It answers what the first four parts left
open: what a resident process still on the old account does about that account's
token. Each row is bounded to what was inspected; a source reading and an inference
are marked as such. Source readings are of rust-v0.160.1, with paths relative to
`codex-rs/`, read 2026-10-07; without a path, the file is
`login/src/auth/manager.rs`. No re-executor reaches it.

**The isolated codex homes.** Each case ran in a temporary directory of its own,
which `HOME`, `CODEX_HOME`, the XDG roots and `TMPDIR` all pointed to, under a minimal
allow-listed environment, with `cli_auth_credentials_store = "file"`: the login was
`auth.json` and nothing else (`login/src/auth/storage.rs` lines 517 to 518). Its
tokens were made up — unsigned JWTs and made-up refresh tokens for main, side and
alt, alt with main's account id and another email. `CODEX_REFRESH_TOKEN_URL_OVERRIDE`
(line 214, read at lines 1726 to 1729) and `chatgpt_base_url` pointed to a local mock
that recorded each refresh request and answered with a new access token and a new
refresh token, after a delay when a case asked for one. `HTTP_PROXY`, `HTTPS_PROXY`
and `ALL_PROXY` pointed to a local proxy that recorded each `CONNECT` and answered
403. A client drove `codex app-server --listen stdio://` over JSON-RPC (`initialize`,
`account/read`, then the case's steps) and replaced `auth.json` as kae does, by
renaming a temporary file over it. No model request was sent. `lsof` on the codex
process every 0.2 s saw no connection other than loopback in any case; the proxy
refused 4 or 5 `CONNECT chatgpt.com:443` at start-up in each case, before any TLS.
Each case recorded the sha256, inode and mtime of `auth.json`, the mock's requests
and codex's log.

| Question | Observed | Not measured |
|---|---|---|
| Does a process on main refresh and write back while the disk still holds main (the control) | `account/read` with `refreshToken: true` sent main's refresh token to the mock once, and `auth.json` then held the rotated tokens and a new `last_refresh`, written in place (same inode). With main's access token inside its 5-minute window, a request that needs the token (`account/rateLimits/read`) did the same. | |
| Does it refresh once the disk holds side | No. After the rename to side, `account/read` with `refreshToken: true`, and `account/rateLimits/read` inside the 5-minute window, sent no refresh request to the mock, and `auth.json` kept its sha256 and mtime. codex logged `Skipping auth reload due to account id mismatch` and, on the second path, `ERROR Failed to refresh token: Your access token could not be refreshed because you have since logged out or signed in to another account. Please sign in again.` The rate-limit request went out with main's old access token. `account/read` still answered success naming main (`workspaceRouting.chatgptAccountId` main's): the mismatch appears only in the log. Read in upstream's source: `refresh_token` (lines 2848 to 2882) first re-reads the stored login through `reload_if_account_id_matches` (lines 2487 to 2520), which compares the account id on disk (`tokens.account_id` for a ChatGPT login: `get_account_id`, lines 614 to 626, its fallback arm at line 624) with the one the process holds; on a mismatch it returns the mismatch error (line 211) without a network request or a write, and keeps the cached login. `auth()` logs that error and goes on with the old login (lines 2393 to 2406); `refresh_token_if_requested` in `app-server/src/request_processors/account_processor.rs` (lines 1026 to 1039) drops it from `account/read`'s answer. A refresh is due when the access token's `exp` is within 5 minutes (`should_refresh_proactively`, lines 3004 to 3026), or only when there is no `exp`, when `last_refresh` is over 8 days old; it is checked when a request needs the token, with no timer. So the process keeps main's access token until it expires, and then fails. | The 401 path, read only: its first step, `UnauthorizedRecovery::next` (lines 1976 to 2010), applies the same account check and stops on a mismatch; only after a match does it refresh with the token it holds (lines 2012 to 2017). |
| Does it adopt another login of the same account | Yes, silently. With alt's file (main's account id, alt's tokens and email) renamed in, `account/read` with `refreshToken: true` sent no refresh request to the mock and wrote nothing; codex logged `Skipping token refresh because auth changed after guarded reload`, and from then on answered alt's email and sent alt's access token. | |
| What a refresh in flight across a switch writes (the race) | With the mock answering 5 s late, side was renamed in 1 s after main's refresh request reached it. When the answer came, codex wrote main's rotated tokens into side's file in place (side's inode): `tokens.account_id` stayed side's, while the id_token (email you@example.com), the access token and the refresh token were main's. It then adopted that file: `account/read` named you@example.com with `workspaceRouting.chatgptAccountId` side's, and its next backend request carried main's new access token with side's account id. side's refresh token was no longer on disk, and main's new refresh token was only in that file. Read in upstream's source: the account check precedes the network round trip, and `persist_tokens` (lines 1599 to 1622) re-reads the stored login, replaces only the three tokens and `last_refresh`, and saves it, after which `reload()` (line 2481) adopts it without a check (`refresh_and_persist_chatgpt_token`, lines 3092 to 3108). The window is that round trip. | How often the window is met on a real network. |
| What switching back to the old account does | With main's original file renamed back in, byte for byte what the process held, `account/read` with `refreshToken: true` refreshed (one request with main's refresh token) and wrote the rotated tokens to `auth.json`. A copy of main's login taken before that refresh then holds the superseded refresh token. Read in upstream's source: without such a request the process refreshes when a refresh is next due, by the 5-minute rule above, since the account check then matches. | Switching back without a request, observed. |

What kae does with these, read in kae's source at `4a18a5c` and not run:

- **A refresh after a switch back is kept.** kae's global switch recaptures the
  account it switches away from: when the live `auth.json` differs from that
  account's snapshot, it rewrites the snapshot from it first
  (`recaptureActiveBeforeSwitch` in `internal/cmd/freshness.go`;
  [ARCHITECTURE.md](ARCHITECTURE.md) § Switch Transaction, step 5). So a rotation
  the old process wrote while main was live reaches main's snapshot on the next
  switch away, unless the refresh is still in flight then (the
  [CREDENTIAL-RULES.md](CREDENTIAL-RULES.md) § Harvesting before a write or a delete
  harvest does not apply; it is per-directory and claude-only).
- **A mixed file is filed under the active account.** For codex the recapture
  compares bytes and attributes nothing: codex declares no identity-only artifact,
  so `keepSnapshotIdentity` has nothing to check, and `recaptureWouldDowngrade`
  orders copies for claude only. Switching away from side with the race's file live
  therefore rewrites side's snapshot with main's tokens and side's account id, under
  side's recorded identity, and the switch to main applies main's snapshot, whose
  refresh token the race superseded. kae reads the two halves of that file
  differently: `Identity` in `internal/adapter/codex/codex.go`, which names the
  account `kae add codex` captures when no name is given, takes the id_token's email
  (main's), while `CredentialAccount` in `internal/adapter/codex/resident.go`, which
  the daemon probe and the account-change check compare, takes `tokens.account_id`
  (side's). Nothing in kae detects the file;
  [ROADMAP.md](ROADMAP.md) § Hardening backlog — daily-use robustness carries that.

Inferred, not measured: that the real token server rotates the refresh token on
each refresh and rejects the superseded one — the mock did, and upstream's source
has an error for a reused refresh token (`refresh_token_reused`, line 1687, message at
line 207) and replaces the stored refresh token when an answer carries one
(lines 1616 to 1618); and that the ChatGPT desktop app's embedded codex and a TUI
refresh through this same `AuthManager` as the app-server measured here. Not
established: whether the ChatGPT app gives its codex externally managed tokens,
which upstream keeps in memory and never writes to `auth.json` (lines 1128 to 1146).

## v0.23.0 candidate

Assessed on 2026-10-05 (JST). Application commits since published v0.22.1 include
the localization work (stages 0 to 5, accepted by the operator), place navigation
and the claude 2.1.284 re-verification (`0a7f81c`); the reported version is
`v0.23.0`.

`mise run check` passed in the default locale and under `LC_ALL=C.UTF-8
LANG=C.UTF-8`; the Japanese run was `KAE_LANG=ja LC_ALL=ja_JP.UTF-8 mise run
test-fresh`, and `GOOS=linux go vet ./...` and `git diff --check` passed.
`mise run goreleaser-check`, `mise run release-evidence`, `mise run release-smoke`
and `mise run naming-agreement` passed. The last needed the new reviewed digest: the
installed Claude was 2.1.284, and a login-free temp-HOME `security` shim showed it
reads the same service names as kae writes (see [VALIDATION.md](VALIDATION.md)
§ Upstream Behaviour Assumptions, claude).

Not run, by the operator's decision: the real-machine account-switching checks of
§ Real-Machine Acceptance and the fingerprint-table re-recording. `mise run audit`
fails: its fingerprint test refuses the installed claude (2.1.284, recorded
2.1.282, three literal counts moved: `CLAUDE_CONFIG_DIR`, `claudeAiOauth`,
`oauthAccount`), agy (digest differs from the recorded 1.2.10 build), copilot
(built-in package not materialized), cursor and opencode (artifact not readable).
Authentication behaviour other than claude's naming is therefore carried by the
v0.22.1 evidence and not re-measured here.

### v0.23.0 publication result

On 2026-10-05 (JST), main CI succeeded at tag source `ceda21f55b07cc94d709257b1d96fe6593df6e5c`,
and the release workflow succeeded for tag `v0.23.0`, including the job that signed
the published archives.

`go run ./scripts/releaseverify v0.23.0` returned `status: success` with
`KAE_RELEASE_VERIFY_FRESH=1`: archive contents, checksums, signer/source, Packslip
resources, native version `kae v0.23.0`, the verified-assets installer and the mise
consumer passed. Signature checks stayed enabled. This is not a pass under the
default age policy. The command used Packslip 1.1.1 and mise 2026.9.3, each fetched
into a temporary directory after its GitHub attestation verified; the verifier fixes
both versions, and the machine's own mise is 2026.10.2. The first runs failed on
GitHub's unauthenticated API rate limit at the consumer's release lookup, and the run
after the limit reset passed. No live login was part of this check.

Re-run on 2026-10-05 (JST) with the minimum tested mise, 2026.9.3, and Packslip 1.6.0 (each
fetched into a temporary directory after its GitHub attestation verified),
`go run ./scripts/releaseverify v0.23.0` returned `status: success` with
`KAE_RELEASE_VERIFY_FRESH=1`, and its `toolchain` field recorded Packslip 1.6.0 and mise
2026.9.3. The foreign-signer refusals and the native mise consumer passed. This
is again an explicit isolated zero-age exception, not a pass under the default
age policy.

The direct local installation used the published v0.23.0 archive and checksum
through `scripts/install.sh --version v0.23.0`, after `gh attestation verify` passed
for the darwin/arm64 archive. PATH resolved `~/.local/bin/kae`, which reported
`kae v0.23.0`. `config.toml` and `state.json` compared byte-identical before and
after. No credential migration was performed.

## v0.22.1 candidate

Assessed on 2026-09-29 (JST). Application commits since published v0.22.0 are
`50a11ff` and `a167ee4` (reset countdowns in the `Limit` cell, narrow-terminal
stacked tables, display-width alignment and table emphasis) and `c2d8f59`
(reported version `v0.22.1`). The change is human-text rendering only: JSON
reports, credential IO, adapters and upstream verification metadata are
unchanged, so the v0.22.0 upstream remeasure and credential-switching evidence
stand without a re-run. No live login was part of this assessment.

On `c2d8f59`: `mise run check` and `git diff --check` passed. `mise run audit`
passed: govulncheck reported no vulnerabilities, and the fingerprint read matched
the installed Claude 2.1.282, agy 1.2.10, OpenCode 1.18.32 and Copilot 1.0.88
builds and the installed Cursor bundle (the run named its path, not its build
string), with Codex excluded by design. `mise run naming-agreement` matched
every case. `mise run goreleaser-check`, `mise run release-evidence` and
`mise run release-smoke` passed. A GoReleaser snapshot was not built for this
patch.

The rendering was also run by hand in an isolated `scripts/smoke-env.sh` HOME
with the file secret backend and a fixture `usage-exact.json`, under a
pseudo-terminal: `kae ls` printed the stacked layout at 60 columns and the table
at 200 columns, with the documented emphasis colors.

### v0.22.1 publication result

On 2026-09-29 (JST), [main CI](https://github.com/webkaz-labs/kagikae/actions/runs/36462863879)
succeeded at tag source `474896bc84a213f6c7c68f5c561d931c211ffb9c`, and the
[release workflow](https://github.com/webkaz-labs/kagikae/actions/runs/36463150912)
succeeded for tag `v0.22.1`, including the job that signed the published
archives.

With `KAE_RELEASE_VERIFY_FRESH=1`, `go run ./scripts/releaseverify v0.22.1`
returned `status: success`: archive contents, checksums, signer/source, Packslip
resources, native version `kae v0.22.1`, the verified-assets installer and the
mise consumer passed. Signature checks stayed enabled. This result is not a pass
under the default age policy; the default policy was not tried for this tag. The
command used Packslip 1.1.1 and mise 2026.9.3, fetched into a temporary
directory and placed first on `PATH` after their GitHub attestations verified.
The first run failed because GitHub's unauthenticated API rate limit refused
the consumer's release lookup; the run after the limit reset passed. No live
login was part of this check.

The direct local installation used the published v0.22.1 archive and checksum
through `scripts/install.sh`, after `gh attestation verify` passed for the
darwin/arm64 archive. PATH resolved `~/.local/bin/kae`, which reported
`kae v0.22.1`. `config.toml` and `state.json` compared byte-identical before
and after. No credential migration was performed.

## v0.22.0 candidate

Assessed on 2026-09-29 (JST). Application commits since published v0.21.0 are
`d20d078` and `a7c02be` (a bound directory's store path), `8800ce8` (subscription
windows on `kae`, `kae ls` and `kae accounts`) and `cd2c4a0` (reported version
`v0.22.0`). The upstream remeasure is the commit that adds this section.

Listings read a local usage file, then the usage cache, then at most one usage
request. They do not refresh or rotate tokens. `kae ls --pins` does not show
windows. The new pin field is the store path already used for that binding.

Live switch, rollback and bind were not re-run. The 2026-09-06 Claude global
switch/rollback and bound-directory results, and the Copilot same-account
apply/rollback, remain the evidence for credential switching. That reuse is not
a new live login and not a statement that a current login is healthy. Optional
account-combination checks stay optional. Usage display and the pin store link
were checked by the commit gate, not by a live account.

Rows re-read on 2026-09-29, and only those rows: Claude 2.1.282 login-free shim
and bundle reads; Codex 0.157.1 declarations from tag `rust-v0.147.0` to
`rust-v0.157.1`; agy 1.2.10 binary read, not executed, and no official-archive
checksum was re-fetched; OpenCode 1.18.32 temp-root auth probes; Cursor bundle
`2026.09.08-6caf4ff` for the three-item write and the refresh helper; Copilot
1.0.88 directory rule, including a temp-HOME package extract. Login-gated rows
keep their earlier provenance. Claude's reviewed naming digest is the 2.1.282
file hash after a PATH shim reached both service families. No behaviour-site
hash checker was added.

On this tree, before the commit that adds this section: `mise run check` and
`git diff --check` passed. `mise run audit` passed: govulncheck reported no
vulnerabilities, and the fingerprint read matched Claude 2.1.282, agy 1.2.10,
OpenCode 1.18.32, Cursor `2026.09.08-6caf4ff` and Copilot package 1.0.88. Codex
stays excluded. `mise run naming-agreement` matched the reviewed 2.1.282 digest
for the default, config, trailing, unicode, secure, relative and invalid-user
cases. `mise run goreleaser-check`, `mise run release-evidence` and
`mise run release-smoke` (completion and per-account store) passed. A local
GoReleaser snapshot built darwin/linux × amd64/arm64 archives containing the
binary, README, LICENSE and generated completions; the archive name stayed
`0.21.0-SNAPSHOT` because tag `v0.22.0` does not exist yet, and the binary
reported `kae v0.22.0`.

### v0.22.0 publication result

On 2026-09-29 (JST), [main CI](https://github.com/webkaz-labs/kagikae/actions/runs/36454876914)
succeeded at tag source `23bda1b2a558d1cad76ef3a69a4dd8a9eee48df8`, and the
[release workflow](https://github.com/webkaz-labs/kagikae/actions/runs/36455065063)
succeeded for tag `v0.22.0`, including the job that signed the published
archives. That tag contains the remeasure commit `43b0789` and the CI zsh
install.

The published consumer refused installation under mise 2026.9.3's default
minimum age. The error dated the transparency-log record
`2026-09-28T17:03:36Z`, after its allowed cutoff
`2026-09-27T17:22:09.322994Z`. With `KAE_RELEASE_VERIFY_FRESH=1`,
`go run ./scripts/releaseverify v0.22.0` returned `status: success`: archive
contents, checksums, exact GitHub OIDC signer/source, Packslip resources,
native version `kae v0.22.0`, the verified-assets installer and the mise
consumer passed. Signature checks stayed enabled. This result is not a pass
under the default age policy. The command used Packslip 1.1.1 and mise 2026.9.3
placed first on `PATH`, because the operator mise was 2026.9.15 and `mise run`
would have selected that one. No live login was part of this check.

The direct local installation then used the published v0.22.0 archive and
checksum through `scripts/install.sh`. PATH resolved the regular file
`~/.local/bin/kae`, which reported `kae v0.22.0`. The removal receipt replaced
the v0.21.0 receipt and kept that previous receipt in installation history.
`config.toml` and `state.json` compared byte-identical before and after. No
credential migration was performed.

## Uninstall and Packslip assessment

Assessed on 2026-09-09 (JST) for candidate `c99ccc5`, including the implementation
commits `f71d4d6`, `e5c64a2`, `7fa65ac` and `342140f`. These checks used isolated
HOME/XDG roots or fake credential runners; no live login or operator uninstall
was part of acceptance. The credential-switching contract is unchanged by this
scope, so additional account-combination runs are not required for these delivery
changes.

| Surface | Evidence and boundary |
|---|---|
| Initialization and teardown | `mise run check` passed on `c99ccc5`. Command tests cover shared initialization locking, existing configuration preservation, bounded ownership, customized/unreadable content, partial reports and retained data. The tests exercise fake credential runners rather than live credentials. |
| Direct installation | [VALIDATION.md](VALIDATION.md) § Direct installation receipt smoke passed against `e5c64a2`: staged native install, reinstall, teardown, self-removal and retained configuration. Receipt tests in the full gate cover invalid metadata, image replacement, permissions and interrupted states. Native self-image validation ran on macOS arm64; other release targets were cross-compiled, not executed on this machine. |
| Legacy installer and locks | [VALIDATION.md](VALIDATION.md) § Installer compatibility smoke passed against `7fa65ac`, using the real shell installer with synthetic archives. Both shell/Go lock directions, receipt/history refusal, legacy downgrade and new-protocol failure without fallback passed. Transport is fixture-only. |
| Signed backend lifecycle | [VALIDATION.md](VALIDATION.md) § Packslip consumer smoke passed on `c99ccc5` with mise 2026.9.3 and Packslip 1.1.1 on macOS arm64. Temporary v0.21.0/v0.21.1 binaries and ephemeral key/unlogged trust exercise actual backend install, opt-in postinstall, no-op/repeated init, upgrade preview/apply, project selection, offline reuse, teardown, manager removal and reinstall. Configuration and dummy credential bytes remain unchanged. Wrong key/project/digest/platform and missing assets are refused. This is not a published-history upgrade test. |
| Completion registration | The same fixture runs real Bash 3.2 and Zsh shells across a project/version switch. Distinct fixture-only static candidates expose stale registration; logged executable versions prove dynamic profile queries use the selected binary. Automatic mise loading restores a prior custom registration on deactivation. Manual loading stays stale until repeated, as documented. Zsh captures `compadd` without driving an interactive TTY. Fish resources are retrieved and compared; fish runtime behavior is not verified. |
| Packaging and publication checks | `mise run goreleaser-check` and a GoReleaser snapshot passed on the `342140f` implementation scope; archive inspection found the binary, documentation and generated static completion resources. Verifier unit tests cover exact source/signer/metadata checks and bounded matching/conflicting/interrupted upload paths. Packslip 1.1.1's generated Linux selector is GNU/Linux; musl selection is outside this backend coverage. Published-tag and native consumer results are recorded below. |
| Existing release gates | `mise run audit`, `mise run release-evidence` and `mise run naming-agreement` passed on the `342140f` implementation scope. `mise run release-smoke` passed on `c99ccc5`, including the saved completion and per-account store fixtures. These results do not extend live account acceptance. |
| Conditional upstream lane | Installed fingerprints passed with the existing Codex exclusion. The reviewed Claude 2.1.261 naming comparison passed. The inspected installed-artifact location provided no reviewed old/new pair, so no new detector, artifact cache or verified-version/date change was accepted. [ROADMAP.md](ROADMAP.md) § Upstream-drift automation — what is left retains the reopening conditions. |

### v0.21.0 publication result

On 2026-09-09 (JST), [main CI](https://github.com/webkaz-labs/kagikae/actions/runs/34302652241)
and the [release workflow](https://github.com/webkaz-labs/kagikae/actions/runs/34302756917)
succeeded at tag source `0295772e451393ac66e781cc2d67316aa9ab3a86`, including the
separate job signing verified published archives. Linux tests ran in these jobs;
that is separate from the native macOS lifecycle runs above.

The published consumer refused installation under mise 2026.9.3's
default minimum age: the error dated the transparency-log record
`2026-09-09T02:22:10Z`, after its allowed cutoff
`2026-09-08T02:23:35.350584Z`. The operator then explicitly approved zero minimum
age in the isolated consumer HOME only. With `KAE_RELEASE_VERIFY_FRESH=1`,
`mise run release-verify -- v0.21.0` returned `status: success`: archive contents,
checksums, exact GitHub OIDC signer/source, Packslip resources, native version,
verified-assets installer and actual mise consumer passed. Signature checks stayed
enabled, with no fixture key, unlogged option or URL redirection. This result is
not a pass under the default age policy; the verifier reports the exception.
Fish and non-native lifecycle limits above remain unchanged.

The approved direct local installation then used the published v0.21.0 archive
and checksum through `scripts/install.sh`. PATH resolved the regular file
`~/.local/bin/kae`, which reported `kae v0.21.0`; an installation receipt was
recorded. Existing configuration and state files compared byte-identical before
and after the install. No live login or credential migration was performed.

### Go consumer verification

On 2026-09-09 (JST), maintainer revision `6c08b89` replaced the Packslip Python
consumers with Go. The full commit gate and the isolated Packslip fixture smoke
passed, including Bash/Zsh version transitions, retained data, expected backend
refusals and shared subprocess lifecycle tests. The default published consumer
still refused v0.21.0 under mise's minimum-release-age policy.

With the previously approved isolated `KAE_RELEASE_VERIFY_FRESH=1` exception,
`mise run release-verify -- v0.21.0` returned `status: success` using the Go
consumer: published archives, provenance, exact Packslip signer/source, native
version, installer and completion checks passed. This changes the maintainer
verification runtime, not the signed v0.21.0 assets or the operator's installation.
The native-platform and fish-runtime coverage limits above remain in force.

## Offline recovery and validation assessment

Assessed on 2026-09-09 (JST) for candidate `b0dd08c` against the v0.20.2
application baseline.
The candidate adds synthetic regression controls and corrects Cursor's unsupported
platform explanation; credential IO, attribution, preservation admission, deadline
classification and capability gates are unchanged. The affected diagnostic is
checked with a Linux-shaped fixture. The existing v0.19.1 preservation live result
and v0.20.0/v0.20.1 application results below remain the evidence for unchanged
mutation paths; this is not a new live-account acceptance or a claim about current
login health. No additional real login or live credential investigation was used.

| Candidate | Verdict and evidence |
|---|---|
| Unknown-format preservation and explicit recovery | Accepted command regressions and recovery documentation. `TestPreservationUnknownFormatInterruptedLoginAndRecovery` covers unfamiliar objects and missing/nonnumeric deadlines, interrupted retries, displaced-copy retention, unchanged snapshots and redaction. `TestReloginRefusesUnreadableAndMalformedCredentialBeforeFlow` separates malformed containing JSON from readable unknown members and unreadable files. Whole-document rescue remains deferred: the file driver's declared unit is a JSON pointer, and bypassing that read would change the restoration contract. |
| Failure and retry safety | Accepted `TestReloginPreservationWriteFailureAndExplicitRetry`, covering backend write failure, visible pending metadata, refused automatic retry, explicit removal and retried flow with source intact. Existing capacity, protected-source, concurrent credential/mapping changes and interrupted-storage controls were included in the focused run. No automatic incomplete-record repair was added. |
| State-specific diagnostics | Accepted the observation/recovery table in CLI and the Cursor verification-boundary message. Existing `unsafe_refused`, `auth_unchanged`, metadata diagnostics and preservation states remain the contract; no new JSON tokens or validity claims were added. |
| Numeric zero versus unknown deadline | Accepted conservative characterization in `TestClaudeFreshnessDeadlineUncertaintyDoesNotRevokeTokens`: missing, null, nonnumeric, zero, negative and positive deadlines with populated tokens. Representation change deferred because preservation admission uses artifact bytes, not deadline ordering; a separate field currently has no accepted new consumer. Revocation/deletion changes need separate tool evidence. |
| Upstream behaviour-site detection | Deferred implementation. The installed Homebrew Claude directory and its download-cache filename search provided no old/new pair for this run; the installed 2.1.261 artifact matched the existing reviewed naming digest. A read-only scan found the `profileFetchedAt` anchor, but a single artifact cannot measure identifier-normalized detection against literal-count controls across upgrades. Reuse the existing fingerprint audit; obtain a reproducible pair before designing another checker. |
| Moved directories and aliases | Accepted temporary-fixture controls for refusal while the original directory is missing, recovery after the original path returns, and distinct pin IDs for symlink aliases. Existing symlink-retarget refusal controls were rerun. Migration deferred: neither restoring the old path in a fixture nor canonicalizing an ID establishes a complete reader set or migrates credential/session stores. |
| Codex per-directory keyring preparation | Accepted a teardown continuation in `TestKeychainCodexHomesCoexist`: removing the selected item leaves the other home's bytes intact. Existing canonical-path addressing and capability-refusal controls were rerun. The per-directory capability remains disabled pending the optional live capability check below. |
| Cursor Linux preparation | Accepted `TestCursorLinuxFixtureDoesNotEnableCredentialAccess`, with access/refresh/API-key and preserved Bedrock fields at the documented XDG path. Adapter artifacts remain unsupported and the fixture remains untouched. Linux enablement is deferred pending the actual file-store round trip; synthetic storage is not upstream compatibility evidence. |
| CI placement and delivery | Accepted reuse of the existing `go test ./...` step for the new regressions. No workflow steps or cache policy were added; additional CI admission still requires the Linux cost and distinct-control evidence in ROADMAP. |

The focused Go JSON report contained passing test events and no failed or skipped
test events for preservation/relogin, capability, addressing, deadline and alias
controls. The full `mise run check` passed after correcting an extra blank line
reported by the formatter. The fixture tests do not reproduce OS keychain failures,
real login interruption or upstream refresh validity; their failed writes and
interrupted flows are injected at existing seams.

The v0.20.3 preparation changes only the version literal and report expectation
relative to `b0dd08c` in application code. Its full commit gate passed, along with
the audit, GoReleaser configuration/snapshot build, login-free naming agreement,
release-evidence and saved completion/per-account-store smokes. The native snapshot
binary reported `kae v0.20.3`. Correctness and quality reviews passed in that order;
the quality fix tightened the test's login environment assertion to exact element
matching and passed targeted verification and correctness re-review. Both reviews
were performed by the same agent under the user's no-subagent instruction.

Release tag `v0.20.3` points to `d52573c`. Its
[main CI](https://github.com/webkaz-labs/kagikae/actions/runs/34249532520) and
[release workflow](https://github.com/webkaz-labs/kagikae/actions/runs/34249730538)
succeeded, including the Linux test step. On 2026-09-09 (JST), the formal
`mise run release-verify -- v0.20.3` run returned structured `status: success`
for the darwin/linux × amd64/arm64 archives, checksums and provenance. Its native
binary reported `kae v0.20.3`; installer verification used a verified-assets
fixture, not live HTTP transport. No local installation was performed; the
operator's installed binary still reported `kae v0.20.2`.

## Recovery guidance validation boundary

Assessed on 2026-09-09 (JST) for implementation candidate `a4daa3d` against
`dd8bc69`. The full `mise run check` gate and the built-binary block in
[VALIDATION.md](VALIDATION.md) § Automatic selection and diagnostic lists passed.
The focused command acceptance run reported passing tests with no skips, including
unreadable metadata. These results use isolated synthetic credentials.

Review of this candidate's application diff found advice changes, with credential
mutation and attribution predicates unchanged. No new live login or credential
mutation was performed for this work; this is not a new live-account acceptance
result or release certification. A future release still follows
[RELEASE.md](RELEASE.md) § Release procedure.

### v0.20.2 release assessment

Assessed on 2026-09-09 (JST) against implementation candidate `a4daa3d` and
its acceptance record at `deb1d97`. The release preparation changes only the
version literal and report expectation in application code. Recovery advice
changes are covered by the isolated acceptance above; credential mutation and
attribution behavior are not changed by this release. No additional live login
or credential write was performed, and no current login validity is claimed.

The release candidate passed the full commit gate, audit, GoReleaser configuration
check and snapshot, login-free naming agreement, release-evidence mutations,
saved completion/store smokes and the automatic-selection/diagnostic-list smoke.
The initial sandboxed gate could not exercise the intentional info/exclude write;
the full gate passed when rerun with that filesystem permission. Correctness and
quality reviews passed without subagents, following the user's prohibition.
Release tag `v0.20.2` points to candidate `0a95a08`. Main CI and the release
workflow succeeded. On 2026-09-09 (JST), `mise run release-verify -- v0.20.2`
returned `status: success` for the darwin/linux × amd64/arm64 archives, checksums
and provenance attestations. The native archive and isolated installer reported
`kae v0.20.2`; the installer used verified-asset fixtures, not live HTTP transport.
The local `mise run install` and completion refresh succeeded, and the installed
binary reported `kae v0.20.2`.

## Applicability for a maintainer-only release

A release that changes only maintainer tooling may reuse recorded live-account
acceptance for unchanged application behavior. Compare the candidate with the
accepted revision: relevant application sources, dependencies and build inputs
must be unchanged, apart from the reviewed version literal and its report test.
Confirm the reviewed upstream version and driver mechanism, and run the installed
behavior audit. Compare artifact digests when the earlier record contains them;
without an earlier digest, record version-level agreement rather than claiming
identical bytes. A contrary incident, relevant drift, changed assertion or unclear
impact requires the affected live check again, with the session protections below.

Record which original results are reused, their original revision/date, the
comparison and its limits. Reuse is not a new live run or assurance that a current
login is healthy. Re-run affected maintainer checks and isolated smokes; verify the
newly published artifacts and installer. Application changes still require the
affected live acceptance before release.

### v0.18.5 applicability result

Assessed on 2026-09-07 for candidate `a2ad1c0`. Comparison with accepted
candidate `64c4ec3` found only the version literal and its report test changed
in application sources, dependencies and GoReleaser inputs. The 2026-09-06
Claude global switch/rollback, Copilot same-account apply/rollback and Claude
bound-directory results below are reused under the applicability rule above.
This is not a new live run or a statement about current login health; optional
account combinations retain their recorded limits.

The installed behavior and vulnerability audit, GoReleaser configuration check,
and naming agreement passed. Claude remains 2.1.261 with the naming harness's
reviewed digest; Copilot remains 1.0.83, with version-level agreement because
its earlier record has no digest. No contrary authentication incident was
identified in this assessment. Full commit checks, release-evidence and both
saved release smokes passed. The store smoke exposed doubled separators from a
trailing-slash TMPDIR; the candidate normalizes the allocation prefix and the
same smoke passed afterward.

After publication on 2026-09-07 (JST), `mise run release-verify -- v0.18.5`
returned `status: success` for the darwin/linux × amd64/arm64 archives,
checksums and attestations. The macOS arm64 binary and isolated installer both
reported `kae v0.18.5`. The installer used verified-asset fixtures rather than
exercising its HTTP transport.

### v0.18.4 applicability result

Assessed on 2026-09-07 for candidate `fe5e3c1`. Comparing application sources,
dependencies and GoReleaser inputs with accepted candidate `64c4ec3` found only
the version literal and its report-test expectation changed. The 2026-09-06
Claude global switch/rollback, Copilot same-account apply/rollback and Claude
bound-directory isolation results below are reused for that unchanged behavior.
This is not a new live-account run or a current-login health check; the optional
two-account Copilot case remains unmeasured.

The installed behavior audit passed with Claude 2.1.261 and Copilot 1.0.83.
Claude's digest matched the reviewed binary used by the naming harness; the
earlier Copilot record has no digest, so its agreement is version-level only.
No contrary incident was identified in this release's assessment.
The updated `mise run naming-agreement` passed its controls and address cases
against the installed Claude binary. `mise run release-smoke` passed both saved
completion and credential-store blocks in isolated environments. Neither command
substitutes for the reused live-account results.

The new Go verifier passed against published v0.18.3, checking its archive
manifest, checksums, attestations, native version and isolated installer with
verified assets; that result is the verifier's pre-release baseline.
After publication on 2026-09-07 (JST), `mise run release-verify -- v0.18.4`
returned `status: success` for the darwin/linux × amd64/arm64 archives,
checksums and attestations. The native macOS arm64 binary and isolated installer
reported `kae v0.18.4`. The installer used verified-asset fixtures, so this did
not exercise its HTTP transport.

## Credential-expiry observation — does `refreshTokenExpiresAt` predict the login's death? (**open**)

**If claude has just asked for a login it did not ask for before: read that account's
`relogin_by` out of `kae ls --json` before you log back in** ([CLI.md](CLI.md)
§ Credential freshness in listings). Logging in first destroys the measurement. What to
record is below; everything between here and it is why this section exists.

Opened 2026-07-31, then briefly closed on a documentation citation and reopened the
same day. The reason it was reopened is the useful part.

**What the vendor documents, and it is worth having:**

> "Claude Code tried to renew your saved claude.ai or Claude Console login and the
> OAuth service rejected the stored refresh token, so Claude Code cleared the saved
> credentials. After that, each request stops locally before it reaches the API,
> because **only `/login` can create new credentials**."
> — [Login expired](https://code.claude.com/docs/en/errors#login-expired)

That settles the **consequence** half: a rejected refresh token ends in an interactive
login, with no automatic recovery, and the failed refresh clears the credential —
which is the behaviour kae already models as the tombstone (`Revoked`). So
`credential_stale`'s *remedy* (name the tool's login flow first, re-capture second) is
corroborated.

**What it does not settle, and what this gate is actually for:** that kae's locally
cached `refreshTokenExpiresAt` accurately predicts *when* that rejection happens. That
timing claim is what `credential_stale` and `credential_expiring` both rest on, and no
vendor page states it. A documentation citation is also a weaker instrument than every
sibling measurement in this file, each of which is settled only by a dated run — and
[ADAPTERS.md](ADAPTERS.md) § Verified Upstream Versions is explicit that kae depends
on undocumented upstream *behaviour*, and that where docs and the binary disagree the
binary wins. Closing this
one on a citation would have set a precedent the rest of the file does not follow.

**This is an observation to record when it happens, not a run to schedule** (changed
2026-08-16; the scheduled form and why it was withdrawn are below). It blocks no
release. It keeps the date it was opened and stays open; what it no longer has is
anything to schedule.

**Do not apply § Real-Machine Acceptance's account precondition here.** This observation
applies nothing and provokes nothing, and its "re-capture with `kae add` immediately
before the run" half destroys the record: the deadline under test is the one the
*existing* capture holds, and re-capturing replaces it.

**Either of two moments produces a record — whichever happens, it is the only one.**

1. **claude asks for a login it did not ask for before.** Record which account; the
   deadline kae was reporting for it; the moment of the refusal; and whether that copy
   had been switched to, or run under, since it was captured.
2. **the reported deadline passes and claude keeps serving.** Record the same first
   two, and how far past it the session went on working.

**The deadline is not in `kae doctor --json`**, which prints one only inside the two
freshness codes' messages — so doctor is silent about a snapshot still reading `ok`,
and that is the refusal earlier than the lead-time window, the under-warn outcome below.

The last item of record 1 is what decides whether it measures anything: a copy the tool
refreshed after capture is not the copy whose deadline was recorded, so the interval
compares two different things. Note the claude version, since this is an undocumented
upstream behaviour like every other in
[VALIDATION.md](VALIDATION.md) § Upstream Behaviour Assumptions.

**Every one of these is a result, and the under-warn case is the one with user harm.**
A refusal near the recorded deadline confirms the timing claim `credential_stale` and
`credential_expiring` rest on. A refusal well *past* it, or record 2, means
`refreshTokenExpiresAt` is pessimistic and kae **over**-warns. A refusal well *before*
it means kae **under**-warns — it was reporting `ok` for a login already dead, which is
the direction a user cannot see coming and cannot work around.

**Why the scheduled form was withdrawn.** It asked for an aged specimen: capture
immediately after a `/login`, then leave that account untouched past `relogin_by` while
working in another. Nothing about it was wrong; it is that **the specimen is an account
nobody works in**, and an account in rotation is refreshed before its deadline arrives.
Whether a machine has one to spare is the operator's state and not this repository's:
on the machine this was withdrawn for, none was spare (2026-08-16), so every candidate
got refreshed first. A third account nobody works in would restore the scheduled form,
and that is the whole of what it costs; short of that, the opportunistic record above is
what is available.

This matters only to whoever restores it. **Which branch of it could cost a working
credential was never established** — an earlier note named the branch where the snapshot
still works, which is backwards on the rotation row in
[VALIDATION.md](VALIDATION.md) § Upstream Behaviour Assumptions, and the reasoning
pointing at the other branch is no better measured. Neither record above has such a
branch.

## Real-Machine Acceptance (release only)

This run doubles as the re-verification pass for the **Upstream Behaviour
Assumptions** table: work each installed tool's rows, then re-record in the
same commit — [VALIDATION.md](VALIDATION.md) § Upstream Behaviour Assumptions
carries what that means and routes on to the full copy set. `kae doctor` naming
`upstream_version` for a tool is the
signal that its rows are due.

**Which accounts to run any of this with, and it is the whole section's
precondition rather than one procedure's.** Every check below applies a
*capture-time* snapshot to a live credential store, and so does every check in
§ Optional account-combination checks. The one exception is § Bound-directory credential store's **shim
procedure**, which needs no account at all — but not that subsection as a whole:
its closing two-account pin needs the real keychain and real accounts, and so needs
this precondition like everything else. [VALIDATION.md](VALIDATION.md)
§ Upstream Behaviour Assumptions records the measurement that makes it
consequential: a
refresh rotates the refresh token, the superseded one is single-use, so two stores
holding copies of one account's credential invalidate each other — the copy that
did not refresh last is dead, and offline it is indistinguishable from a healthy
one. So: **use a throwaway or second account, never one you are working in, and
re-capture with `kae add` immediately before the run** so the snapshot being
applied is live. This is not hypothetical caution. It is the lesson of the v0.8.0
gate, which broke the maintainer's real login by applying a snapshot captured days
earlier and needed `claude /login` to recover.

Manual, on macOS, with real logged-in accounts and a fresh backup of
`~/.claude.json`:

1. `kae add --no-login claude <current-account>`
2. log in to the other account with the official CLI, `kae add --no-login` it
3. `kae use claude <first>` / back, verifying upstream CLI identity each
   time and `git`-diffing `~/.claude.json` for non-allowlist drift
   (`/oauthAccount` **is** the allowlist there; `projects`, `mcpServers`,
   onboarding and cache keys must be byte-identical)
4. `kae rollback` and verify identity returns

For claude, also confirm the identity cache tracks the credential, because a
correct token with a stale cache is the exact failure this switch exists to
prevent: after step 3, `~/.claude.json` `oauthAccount.emailAddress` must name
the account just applied, and the live token must resolve to the same account —
`curl -s -H "Authorization: Bearer <accessToken>" \
https://api.anthropic.com/api/oauth/profile` reports `account.email`. Do not
substitute "claude will fix it on the next run": it will not while its 24h
profile cache is warm (docs/ADAPTERS.md).

**Verifying identity means launching a fresh tool process and confirming it is
actually authenticated** — e.g. `claude -p "say hi" </dev/null` returns a
reply, not "Not logged in". Hash-comparing the stored credential or relying on
a still-running session is **not** sufficient: the payload can be byte-correct
yet unreadable by the tool (a re-serialized keychain payload, or one written by
a process outside the item's ACL, makes Claude Code report "not logged in"
despite an intact token). A past acceptance pass that skipped the fresh-process
check missed exactly this class of bug.

Confirmed 2026-09-05 on the v0.18.0 candidate at `31e3594`, with Claude Code
2.1.260 and two freshly captured accounts. Both switch directions preserved every
byte outside the `oauthAccount` value, including the trailing-newline state. After
each switch a fresh prompt authenticated and the identity cache matched the official
profile endpoint; rollback returned to `side`. The run then applied `main` again,
where a fresh prompt and profile comparison passed.

Reconfirmed 2026-09-05 on the installed v0.18.1 candidate at `b5e5815`, with
Claude Code 2.1.260 and two freshly captured accounts. A stale `side` snapshot first
failed the required fresh-process check, so the account was logged in through the
official flow and immediately recaptured before the measured run. `side` → `main` →
`side`, rollback to `main`, and the final apply of `side` each authenticated in a
fresh prompt and matched the official profile endpoint. Every transition preserved
every byte outside the `oauthAccount` value; the final active account was `side`.

Reconfirmed 2026-09-06 (JST) on the v0.18.2 implementation candidate at
`67b6ff2`, tested from `3269b67` with the same executable code, with Claude Code
2.1.261 and two freshly captured accounts. `main` → `side` → `main` and an
explicit rollback to `side` each authenticated in a fresh process and matched the
identity cache against the official profile endpoint. Every kae mutation preserved
every byte outside `/oauthAccount`; the accounts had distinct identities.

Reconfirmed 2026-09-06 on the v0.18.3 executable candidate at `64c4ec3`,
with Claude Code 2.1.261 after the operator stopped the active `side` session.
`side` → `main` → `side` → `main`, then an explicit rollback to `side`, each
passed a fresh-process prompt and profile/cache identity comparison. The measured
global switches and rollback preserved every byte outside `/oauthAccount`.
Captures were updated after fresh authentication rather than rejecting a stored
access token before the CLI could refresh it.

For copilot (active-account pointer, all platforms — kae never touches the
per-account keychain tokens, only `~/.copilot/config.json` `/lastLoggedInUser`,
so it is safe on macOS):

1. `kae add --no-login copilot <current-account>`
2. `kae use copilot <account>`, then `git`-diff `~/.copilot/config.json`: only
   the `/lastLoggedInUser` value changes (re-compacted to one line is expected
   and harmless); the leading `//` comments, `trustedFolders`, and
   `loggedInUsers` must survive.
3. `kae rollback` and confirm the leading `//` comments still survive — this
   exercises the JSONC restore path (a backup whose JSONC flag was dropped
   patches through the plain-JSON path and fails on the comments).

copilot has no `whoami`/`status` subcommand, so the fresh-process auth check is
a non-interactive prompt: `copilot -p "say AUTH-OK" --no-color --allow-all-tools`
returns a reply when authenticated, an error/login prompt when not. The CLI
emits ANSI/spinner control codes, so strip them
(`sed 's/\033\[[0-9;]*[a-zA-Z]//g'`) before asserting on the output. Switching
between two accounts is the optional account-combination check below; with a single
account this release acceptance verifies the verbatim round-trip and comment
preservation.

Confirmed 2026-09-05 on the v0.18.0 candidate at `4d78206` with the one
available account: a same-account apply changed only `/lastLoggedInUser`, preserved
the leading comments and all values outside that pointer, and did not rewrite the
per-account keychain item. A fresh non-interactive prompt returned a reply. The
optional two-account switch remains unmeasured.

Reconfirmed 2026-09-05 on the installed v0.18.0 candidate at `9360bed`, after the
JSON/JSONC pointer rewrite at `31e3594`. A freshly captured same-account apply
changed only the representation of `/lastLoggedInUser`; every byte outside that
pointer and the leading comments remained unchanged. The keychain item's attributes
were unchanged, a fresh non-interactive prompt returned the requested reply, and
rollback preserved the comments and restored the same pointer value. The optional
two-account switch remains unmeasured.

Reconfirmed 2026-09-05 on the installed v0.18.1 candidate at `b5e5815`, with the
one available Copilot account. Capture, apply, fresh non-interactive prompt and
rollback passed; the leading JSONC comments and every value outside
`/lastLoggedInUser` survived, and the per-account keychain item was not rewritten.
The optional two-account switch remains unmeasured.

Reconfirmed 2026-09-06 (JST) on the v0.18.2 implementation candidate at
`67b6ff2`, tested from `3269b67` with the same executable code, with Copilot
1.0.83 and one account captured immediately before the run. Same-account apply and
explicit rollback preserved every byte outside `/lastLoggedInUser`, including the
leading JSONC comments, and the addressed keychain item's attributes were unchanged.
A fresh non-interactive prompt returned the requested reply with no tools enabled.
The optional two-account switch remains unmeasured.

Reconfirmed 2026-09-06 on the v0.18.3 executable candidate at `64c4ec3`,
with Copilot 1.0.83 and one freshly captured account. Same-account apply, a fresh
prompt with no tools enabled, and explicit rollback passed. Bytes outside
`/lastLoggedInUser`, leading JSONC comments and keychain item attributes were
unchanged. The optional two-account Copilot switch remains unmeasured.

### Bound-directory credential store (macOS, no login needed)

kae and claude must agree on the keychain service and account for a bound directory.
Run `mise run naming-agreement` from the repository with Go, Python 3.11 or newer,
and the reviewed macOS Claude binary installed. The task runs its refusal and
mismatch controls before comparing independently observed addresses.

The harness in `scripts/namingagreement/` reuses `scripts/smoke-env.sh` inside an
owned temporary directory. It intercepts the production adapter/artifact writer
through `internal/runner`, and observes Claude's reads through a non-forwarding
`security` shim. It refuses missing or unreviewed Claude bytes before launching
them, checks the shim and isolated roots before each case, and rejects missing
observations, mismatches and plaintext credential files. The digest and its
reachability provenance live beside the executable check; updating them requires
the [upstream measuring procedure](../.claude/skills/upstream-auth-drift/references/measuring.md).
An empty log does not establish containment.

This checks the production credential writer, not the full `kae pin` command or
its generated environment. The real-account bind below supplies that wiring and
payload/ACL evidence. Supply **both** `CLAUDE_CONFIG_DIR` and
`CLAUDE_SECURESTORAGE_CONFIG_DIR` from the generated fragment there: passing only
the former asks Claude about a different credential store. The harness does not
relax `smoke-run.sh`'s file-driver guard and is not an OS network sandbox.

Confirmed 2026-09-06 on the v0.18.3 candidate at `64c4ec3` with
`mise run naming-agreement`: the production writer and Claude 2.1.261 read
addresses matched for the default, explicit config, trailing slash, decomposed
Unicode, separate credential directory, relative directory and invalid-USER cases.
The task's refusal/mismatch controls passed first; no plaintext credential fallback
was observed. This is the harness's adapter/artifact observation, not a fresh
real-account authentication or a generated-fragment wiring result.

Confirmed 2026-09-04 on the v0.18.0 candidate at `495141f`, with Claude Code
2.1.246: the production-equivalent two-variable path matched the independently
derived service and left no plaintext credential file. The run used a temp file
backend and a `security` shim; it did not test a real account, payload round-trip or
keychain ACL. The same run against a pre-fix build writes no keychain item at all and
reads only the unsuffixed shared one, which is the original defect.

This is a naming-agreement check. That the payload itself round-trips is the
separate verbatim/ACL assumption in [VALIDATION.md](VALIDATION.md)
§ Upstream Behaviour Assumptions, and it needs the real keychain and a real
account — so the release run includes the two-account pin: re-bind in a pinned
directory, then launch claude there and confirm it reports the account kae bound.

Confirmed 2026-09-05 on the v0.18.0 candidate at `4d78206` with two real accounts.
A scratch directory was bound to `side`, then rebound to `main`; a fresh Claude
process authenticated as the bound account after each operation, and neither
isolated store contained a plaintext `.credentials.json`. `kae unpin --purge`
removed the binding and isolated credential, while the global `main` login remained
authenticated.

Reconfirmed 2026-09-05 on the installed v0.18.1 candidate at `b5e5815`. The
login-free security shim produced the same independently derived service name for
kae and Claude. In a separate real-keychain scratch directory, `side` was bound and
then rebound to `main`; each fresh Claude process authenticated and its cache matched
the official profile endpoint, neither store contained plaintext credentials, and
`kae unpin --purge` removed the isolated credential. The global `side` login remained
authenticated and active afterwards.

The login-free naming procedure passed 2026-09-05 on the v0.18.2 candidate at
`67b6ff2`, with Claude Code 2.1.260. In a temporary HOME with a file secret backend
and simulated `security` commands, kae's write and a fresh Claude process's read
matched the independently computed credential-directory service name. Both
generated Claude variables were supplied, and neither directory contained a
plaintext credential file before or after Claude ran. The shim handled the
interactive stdin form of `security` as well as its ordinary argument form;
refusing that write in an earlier fixture caused Claude to fall back to plaintext.
Claude exited 1 with the synthetic credential. This result establishes naming
agreement only; the real-account run below supplies the authentication evidence.

Reconfirmed 2026-09-06 (JST) on the v0.18.2 implementation candidate at
`67b6ff2`, tested from `3269b67` with the same executable code, with Claude Code
2.1.261. A separate scratch inventory captured both live accounts through
`kae add --no-login`. Binding `side` and rebinding `main` each authenticated in a
fresh process using both generated Claude environment variables; the cache matched
the official profile endpoint and neither store contained plaintext credentials.
Purging the `main` binding removed its keychain item. The earlier `side` item
remained, so it was rebound and purged separately; its subsequent lookup returned
exit 44. The resulting account captures were returned through normal CLI operations,
with global `side` active and authenticated.

Reconfirmed 2026-09-06 on the v0.18.3 executable candidate at `64c4ec3`,
with Claude Code 2.1.261 and a temporary file-backend inventory. An isolated
`side` binding and its rebind to `main` each authenticated in a fresh process using
both generated environment variables; profile/cache identities matched and no
plaintext credential file was observed in either bound store. Purge removed the
`main` keychain item. Cleanup recreated a profile binding for the retained `side`
item and purged it too; both removed-item lookups returned exit 44. The latest
accounts were captured back through normal CLI operations, global `side` was
authenticated, and the temporary file-backend inventory was removed. A cleanup
attempt to use the one-tool rebind form after purge was refused because the
fragment was already gone; the profile form recreated the binding as required.

## Optional account-combination checks

These checks are optional for v0.18.0 and for future releases. They are retained as
repeatable coverage for an operator who has the required accounts; an incomplete or
unrun checklist does not block a release. Each check's result paragraph owns what
has and has not been measured.

For the v0.18.1 candidate at `b5e5815`, the Copilot two-account switch, Codex
two-account keyring round-trip, Cursor two-account/API-key directions, and Codex
per-directory keyring check were not run. Their existing partial results below remain
the available evidence. The last check is optional only as release evidence: Codex
per-directory keyring support remains fail-closed and is not enabled.

One boundary is not relaxed: the codex per-directory check is optional for a
release, but it remains the mandatory evidence for enabling that capability. Codex
stays in `bindableNotYetDeclared`, so kae warns and writes nothing to a bound
directory's keychain store, until the check passes.

**Copilot two-account pointer selection** (all platforms, two live accounts).
Repeat the Copilot procedure above for both accounts and verify a fresh prompt after
each switch. Only `/lastLoggedInUser` may change; the per-account keychain items and
all other config values remain untouched.

**Codex keyring two-account round-trip** (macOS, real `Codex Auth` keychain).
Set `cli_auth_credentials_store = "keyring"` in `~/.codex/config.toml`, then:

- [ ] `kae add codex` (no name) on a live keyring login captures under the
      detected account (the `id_token` email / `account_id`), and `account.toml`
      holds no payload — nor a keychain account, which it deliberately no longer
      records (v0.16.0; [DATA-MODEL.md](DATA-MODEL.md)). The derived item account
      (`cli|` + 16 hex) is observable on the item itself:
      `security find-generic-password -s "Codex Auth"`, attributes only.
- [ ] Log in as a second account; `kae add codex` it.
- [ ] `kae use codex <first>`: a fresh `codex app-server --listen stdio://`
      answers `account/read` (after `initialize` and `initialized`) with the first
      account's email — the verbatim keyring round-trip restored it. `codex login
      status` prints only the login mode (rust-v0.160.1 `codex-rs/cli/src/login.rs`
      lines 475 to 478), so it cannot show which account. The item's account attribute is unchanged
      (`security find-generic-password -s "Codex Auth"`, attributes only): one
      codex home has one item whichever account is logged into it.
- [ ] A **second `CODEX_HOME`** logged in at the same time still is afterwards:
      `account/read` on a fresh `CODEX_HOME=<other> codex app-server --listen
      stdio://` reports its own account (not `codex login status`, which shows only
      the login mode). This is the
      regression that shipped through v0.12.0 (a switch deleted the service's item
      by service name alone). The login-free half is covered by
      `TestKeychainCodexHomesCoexist`; this checks the real keychain.
- [ ] The payload read works without a keychain prompt. **Open**: codex writes its
      item through the Rust `keyring` crate (Security.framework directly, not
      `/usr/bin/security`), so whether `security -w` is in the item's ACL
      trusted-application list is unverified — if it prompts, the keyring driver
      needs the prompt documented or an ACL fix.
- [ ] No token value ever appeared in `kae` output, `--json`, or `account.toml`.

Partially measured 2026-09-05 on the v0.18.0 candidate at `4d78206`, using one
account and an isolated temporary `CODEX_HOME` because the normal config is
host-managed. The isolated home used the keyring store, captured without payload in
`account.toml`, authenticated in a fresh `codex login status`, and held no plaintext
`auth.json`. The normal home remained authenticated before and after the isolated
home logged out, and that logout removed only the isolated keychain item. This does
not settle the two-account switch, item-attribute stability across accounts, or the
per-directory gate below.

**Cursor full credential set** (macOS, two live `cursor-agent` logins):

- [ ] `kae add cursor <name>` records `access_token` and `refresh_token` present;
      `api_key` present only for an api-key login.
- [ ] After `kae use cursor <other>`, `cursor-agent status` reports
      `authenticated` (not `partially-authenticated`) **and** the other account,
      and `security find-generic-password -s cursor-refresh-token` (attributes
      only) shows an `mdat` newer than the switch.
- [x] A snapshot captured before the set was switched (no `refresh_token` entry,
      e.g. by deleting that key from `account.toml`) refuses the
      switch naming `kae add --no-login cursor <account>`, and the live items are
      unchanged afterwards.
- [ ] With an api key configured on one account only: after switching to the
      account **without** one, `cursor-api-key` is absent (kae removed it) rather
      than still holding the other account's key.

Partially measured 2026-09-05 with the one available no-API-key account. On
`4d78206`, a normal apply restored the access and refresh items, kept the API-key
item absent, advanced the refresh item's modification time, and left
`cursor-agent status` authenticated. At `807ea5b`, an incomplete-snapshot preflight
refused with exit 10 before mutation; the snapshot, state, backup count, credential
digests, and keychain modification times all remained unchanged. The account stayed
authenticated after the fixture was restored. A second account and the API-key
removal direction remain unmeasured.

**codex per-directory keyring bind** (macOS, two codex homes; this is the
capability-enablement check that must pass **before** codex is dropped from
`bindableNotYetDeclared` in
`TestKeychainDirBindableMatchesTheItemIdentity`). Everything else is in place: the
account derivation is measured ([VALIDATION.md](VALIDATION.md)
§ Upstream Behaviour Assumptions), the flag now
measures item identity, and the teardown ships. What has never run is the whole
round-trip, and the failure it would hide is kae writing an item under an account
codex does not look up from that directory:

- [ ] With `cli_auth_credentials_store = "keyring"` and two captured accounts,
      `kae pin -i <profile>` in a scratch directory reports no unisolatable-credential
      warning for codex, and
      `security find-generic-password -s "Codex Auth" -a "cli|<16 hex of sha256 of
      the realpath of the pin config dir>"` (attributes only, hash computed with
      `shasum` outside kae) finds the item.
- [ ] In that directory, with mise active, `account/read` on a fresh
      `codex app-server --listen stdio://` names the bound account — the check
      that kae's account and codex's agree (not `codex login status`, which shows
      only the login mode).
- [ ] The **global** `Codex Auth` item is untouched: its account attribute still
      resolves from `~/.codex` and its login still works outside the directory.
- [ ] `kae pin -s <profile>` in the same directory: the isolated store's item is
      gone (attributes probe returns not-found) and codex in the directory now reads
      the shared store's item.
- [ ] `kae unpin --purge`: both are gone, the global item survives, and the store
      directories remain.

Account choice for every optional check is § Real-Machine Acceptance's precondition
above, not a separate rule.

Never run real-machine acceptance or an optional account-combination check with
uncommitted work in progress in the live tool sessions.

## Original-store preservation

Use the session protections in § Real-Machine Acceptance (release only). For an
application change to preservation, test the candidate against a bound Claude
credential on macOS after the relevant sessions have stopped. Keep secret bytes
out of transcripts and repository files; compare payloads privately or by digest.

1. Record the original binding, addressed keychain item and credential digest.
   Run the candidate's `kae relogin claude` in that bound directory. Confirm that
   its preservation record exists before the upstream login can replace the item.
2. Complete the login or abort it deliberately, recording which case was exercised.
   Compare the stored pre-login copy with the original digest. An unchanged retry
   must reuse the matching record rather than consume another generation.
3. If login changed the credential, restore the original record without launching
   the upstream tool against it, confirm the original bytes are in the original
   store, then restore the displaced current copy. Leave the latest login in place.
   If login was aborted without a change, a same-copy restore checks addressing
   without replacing the credential with another generation.
4. Check list JSON for non-secret metadata and unknown ownership, preview deletion,
   and verify non-interactive deletion requires explicit acknowledgment. Do not
   delete the only record needed to return the operator to the pre-test state.
5. Record the candidate revision, upstream version, backend, exercised login outcome
   and restoration result here. Failed/preflight-refused paths, history pruning,
   quota and interrupted persistence also have isolated synthetic tests; do not
   describe those as a live upstream login. Codex's dynamic store resolution is
   covered by its synthetic adapter/command controls unless a live Codex run is
   explicitly recorded.

### v0.19.0 preservation result

Run on 2026-09-07 (JST) against candidate `1a9b4d6`, using Claude Code
2.1.261 and the macOS Keychain backend. The operator confirmed both Claude
account sessions were stopped. The current `side` credential was recaptured
immediately before creating a temporary isolated binding.

The candidate saved a ready record before starting the installed Claude login
process. Login was deliberately aborted at its initial setup screen: the child
was terminated after Ctrl-C left it running, and kae returned `11`
(`auth_unchanged`). A repeated run reused the same record ID and again returned
`11`. The preserved credential subtree matched the pre-login digest; the whole
preserved payload matched the addressed live keychain item after the abort.
Both restore dry-run and same-copy restore succeeded, and the restored whole
payload matched the preserved digest. No fresh login or token rotation was tested.

List JSON reported one ready record with `identity: unknown`. Deletion dry-run
succeeded; non-interactive deletion without acknowledgment returned `10` and
retained the record. After confirming that the restored live copy matched, explicit
`--yes` deletion removed only the test record and the list became empty.
Secret bytes were neither printed nor added to the repository.

The full commit gate, release-evidence, both saved release smokes, installed
behavior/vulnerability audit, GoReleaser configuration check and naming agreement
passed for this candidate. History pruning, quota, persistence failures and Codex
store resolution retain their isolated synthetic coverage; no live Codex account
was available for this run. These results do not establish current token validity.

After publication on 2026-09-07 (JST), `mise run release-verify -- v0.19.0`
returned `status: success` for the darwin/linux × amd64/arm64 archives,
checksums and attestations. The macOS arm64 binary and isolated installer both
reported `kae v0.19.0`. The installer used verified-asset fixtures; this result
does not exercise its HTTP transport.

### v0.19.1 candidate preservation result

Run on 2026-09-07 (JST) against candidate `e158087`, using Claude Code 2.1.261
and the macOS Keychain backend. Both Claude sessions were confirmed stopped. The
`side` credential was recaptured with `--no-login` immediately before creating a
temporary isolated pin.

The candidate's `relogin` flow preserved the same record across two attempts before
the initial setup. The upstream process was deliberately terminated on both attempts;
kae returned exit `11` (`auth_unchanged`) and the terminated process returned `143`.
The pre-login and preserved raw SHA-256 digests matched, and the preserved and restored
digests matched after restore. Restore dry-run and real restore returned `0`; removal
dry-run returned `0`. Non-interactive removal with stdin closed and without `--yes`
returned `10` and retained the record; explicit `--yes` returned `0` and the list
became empty. `unpin` returned `0`.

This is bounded same-copy preservation evidence. It does not establish a new login or
refresh-token result; Codex was not run live. Secret bytes, private paths and private
identifiers were not recorded.

The final CI/documentation tree passed the full commit gate and both saved release
smokes. Release-evidence, the installed-tool/vulnerability audit, GoReleaser
configuration check and naming agreement passed for the same implementation.
The authentication source remains unchanged from the live candidate above.

After publication on 2026-09-07 (JST), `mise run release-verify -- v0.19.1`
returned `status: success` for the darwin/linux × amd64/arm64 archives,
checksums and attestations. The macOS arm64 binary and isolated installer both
reported `kae v0.19.1`. The installer used verified-asset fixtures, so this
result does not exercise its HTTP transport.

### v0.19.2 profile and metadata assessment

Assessed on 2026-09-07 (JST) for candidate `a91e519` against v0.19.1.
The application changes affect profile configuration decisions and metadata listing;
this is not a maintainer-only release. Review confirmed that the preservation
list returns before backend selection, while restore/removal retain their backend
selection and ID-validation order. Relogin, preservation saving, credential IO,
adapters and dependencies are unchanged in this comparison.

The affected surfaces were checked with isolated fixtures instead of repeating
the unchanged live login/restore sequence recorded for v0.19.1 above. Profile
controls cover concurrent mappings, default protection, refusal, force and dry-run.
The metadata-list control simulates incompatible backend selection, with empty,
incomplete and malformed inventories; it does not simulate a locked macOS keychain.
These results do not establish the health of a current upstream login.

The full commit gate, audit, naming agreement, GoReleaser configuration check,
release-evidence and both saved release smokes passed. The onboarding smoke passed
the registration → profile mapping → first switch sequence with synthetic auth
and a preconfigured file backend; it does not claim a fresh official login or
exercise the default OS backend.

After publication on 2026-09-07 (JST), `mise run release-verify -- v0.19.2`
returned `status: success` for the darwin/linux × amd64/arm64 archives,
checksums and attestations. The macOS arm64 binary and isolated installer both
reported `kae v0.19.2`. The installer used verified-asset fixtures, so this
result does not exercise its HTTP transport.

### v0.20.0 automatic selection and diagnostic-list assessment

Assessed on 2026-09-07–08 (JST) for candidate `5cec709`. Application changes affect
profile target selection, manual global-isolation teardown and metadata-only
listing. They do not change adapter credential addressing or preservation
restore/removal validation. This is an application release; affected live
acceptance is recorded below under the session protections above.

Fixture checks on the candidate's application sources passed: retained isolated
selection without a backup, mixed-profile shared-only backup, restoration after
a mixed apply's state-recording failure, explicit shared teardown even when the
active account matches, lock-busy and missing-store/fragment refusals, quiet and
preview output, parser exclusions, owned-hook migration and separate local/global
binding fragments. The directory fixture observes environment values and fragment
contents; it does not constitute a real interactive mise shell session.

Diagnostic controls passed for readable rows with broken metadata, unreadable
metadata, an unenumerable directory, unexpected symlinks, empty inventories and
invalid TOML. Backend-incompatible fixtures still listed metadata. Diagnostics
kept raw parse errors and private entry names out of JSON and text; mutation controls kept
invalid-config refusal and prevented automatic rollback past a broken backup.
Pending/deleting preservation metadata remains visible and explicitly removable.

The full commit gate, audit, naming agreement, GoReleaser configuration and snapshot
build, release-evidence, and both saved release smokes passed. The additional
[VALIDATION.md](VALIDATION.md) § Automatic selection and diagnostic lists block
passed against the built candidate with synthetic credentials and a file backend.
One parallel run overlapped the smoke selftest's deliberate checkout mutation and
was discarded by the leak guard; its subsequent standalone run passed. These
results do not establish the health of a current upstream login. Live acceptance
and published-asset verification are recorded below.


Live acceptance on 2026-09-08 (JST) used candidate `a9f6619` and Claude Code
2.1.261 after the operator confirmed related sessions were stopped. The current
`side` login authenticated in a fresh process and was immediately recaptured.
`use -i claude side` selected its isolated home; `use --auto -P main` reported
`changed: false`, retained `side` and returned no shared results. Quiet mode
succeeded. The same command inside `mise exec` preserved the global fragment bytes
and backup inventory. A fresh Claude process under mise authenticated successfully.

Explicit `use -s -P side` removed the global fragment and its mise environment
entries even though the active account already matched. A controlled repeat
confirmed byte identity outside `oauthAccount` and equal identity values inside
it; whole-file bytes differed because that permitted member was reformatted.
Fresh-process authentication passed after returning to shared mode. The final
selection is the original shared `side`. Live backup and preservation lists both
reported complete inventories without issues or warnings. Corruption controls
remain fixture-only; no live metadata was deliberately damaged. The hook's
rendering/migration remains covered by fixtures, while this live run exercised
its command under mise rather than an interactive directory-enter event.


After publication on 2026-09-08 (JST), `mise run release-verify -- v0.20.0`
returned `status: success` for the darwin/linux × amd64/arm64 archives,
checksums and attestations. The release workflow passed for tagged revision
`b8376f1`. The macOS arm64 binary and isolated installer both reported
`kae v0.20.0`. The installer used verified-asset fixtures, so this result does
not exercise its HTTP transport.


### v0.20.1 global mise integration assessment

Assessed on 2026-09-08 (JST) for candidate `25a8026`. The changes cover global
mise file ownership, pre-preparation validation, completion migration and shared
teardown. Credential storage/attribution and command selection contracts are
unchanged; this application change received affected live acceptance below.

The full commit gate passed after review fixes and their controls. Audit,
naming agreement, GoReleaser configuration/snapshot, release-evidence and the
saved completion, per-account-store and automatic-selection smokes passed.
Fixtures cover registration ordering, combined-file auto no-op, shared teardown,
account lifecycle refusals and subsequent rename, source symlink/mode retention,
foreign content, marker text inside a string, custom mise directory, state-lock
contention, destination-write failure/source restoration, interrupted migration
resumption, incomplete-journal refusal and previous isolated-header compatibility.

The live global config was symlink-backed and already had a generated zsh
registration. Completion refresh moved that exact block to `conf.d/kagikae.toml`,
retaining every source byte outside the block, its symlink and target permissions.
Repeating refresh left the fragment unchanged. A fresh zsh loaded `_kae` and its
command registration through mise. This machine's `experimental` setting was
false: that hook check enabled `MISE_EXPERIMENTAL=1` for its process only. Normal
shells still need the documented hook prerequisites; no global setting was enabled.

With related sessions confirmed stopped, Claude Code 2.1.261 authenticated in a
fresh process under shared `side`, which was immediately recaptured. `use -i`
retained completion alongside isolated settings. `use --auto -P main` preserved
`side`; completion refresh and auto both retained the combined file byte-for-byte.
A fresh process under mise authenticated. Explicit `use -s -P side` removed only
the isolated settings; completion remained, and fresh-process shared authentication
passed. The final account selection is the original shared `side`.

After publication on 2026-09-08 (JST), `mise run release-verify -- v0.20.1`
returned `status: success` for the darwin/linux × amd64/arm64 archives, checksums
and attestations. The tag points to `3130aae`; its release workflow passed.
The native archive and isolated installer reported `kae v0.20.1`. The installer
used verified-asset fixtures, so its HTTP transport was not exercised.
`mise run install` then installed the candidate locally; `kae version` reported
`kae v0.20.1` and the migrated completion-only fragment remained intact.
