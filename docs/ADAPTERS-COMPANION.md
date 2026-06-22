# Companion Adapters

This document defines, per companion tool, what companion-auth lockstep
**switches** and what it must **preserve**. The allowlists here are the
normative contract; the companion specs in `internal/companion` implement
exactly this, and any change requires updating this document in the same commit.

Companions are auth-lockstep targets — `git`, `gh`, and cloud CLIs — whose
identity kae binds **per profile** so that an AI coding agent and the tools it
shells out to (git, gh, …) act under the same account. Unlike Tools
(see [ADAPTERS.md](ADAPTERS.md)), kae does **not** capture a companion's
credentials into a snapshot. It only **drives environment and config** to point
each companion at the profile's identity. The binding is:

- **opt-in** — a profile binds a companion only when its `[profiles.<name>.companions]`
  table names it; nothing is switched otherwise;
- **auth-only** — kae never reimplements git/gh/cloud behaviour, only sets the
  env/config those tools already read;
- **per-directory and reversible** — the binding is delivered through the
  kae-owned mise fragment (`kae pin`), scoped to the pinned directory, and
  removed by `kae unpin`.

## Override kinds

How a companion's identity reaches the tool. One kind per companion
(`internal/constants` `Override*`).

| Kind | Delivery | Secret on disk? |
|------|----------|-----------------|
| `git-config` | Render a kae-owned git config file and point `GIT_CONFIG_GLOBAL` at it. The file `[include]`s the user's own `~/.gitconfig`, then overrides identity fields. | No (identity fields are not secrets) |
| `token` | Set the env var(s) to a secret resolved at mise eval time via an `exec()` lookup against the secret backend. | No — only the lookup invocation is written; the token never lands on disk |
| `config-dir` | Set the env var(s) to a user-provided config path kae only references. | No — kae neither generates nor copies the file |

### Secret handling

`token`-kind values are stored in the secret backend under
`companion/<profile>/<id>/<knob>` (base64-encoded, like every kae secret) and
are **never written to a fragment, stdout, JSON, metadata, or logs**. The mise
fragment carries an `exec()` template that invokes kae's credential-helper
subcommand at environment-evaluation time; kae reads the backend, decodes, and
prints the raw token to stdout **only** on that helper path — the documented,
narrow exception (a git-credential-helper-style seam invoked by mise), excluded
from every human/JSON reporting path. `mise env` dumping the resolved value is
inherent to requesting the environment; the token is still absent from disk.
Token knobs are added to the fragment's `redactions` so mise masks them in task
logs. See [SECURITY.md](SECURITY.md).

### mise trust

A `kae pin` fragment must be trusted before mise loads it (`mise trust`) — this
is the existing requirement for any kae pin fragment, not new to companions. A
token binding adds an `exec()` template to that fragment, so trusting it also
authorizes mise to run kae's `__companion-token` helper at eval time. The
fragment is kae-owned and git-ignored (never attacker-controlled), and the
helper only reads the secret backend, so this rides on the same trust the user
already grants the pin.

## Binding health (`kae doctor`)

`kae doctor` (unfiltered) reports companion binding health, config-level and
deterministic — it does not run git/gh to compare live identity:

| Check | Meaning |
|-------|---------|
| `companion_missing` | a bound token knob has no stored secret; the mise `exec()` lookup would fail at eval time (run `kae companion add <profile> <id> <knob>`) |
| `companion_binary` | a bound companion's CLI is absent from PATH; the binding has no effect until it is installed |

Because companions are profile-scoped and delivered per-directory, re-running
`kae pin <profile>` is what refreshes a bound directory after the binding
changes; a single-tool `kae pin <tool> <account>` re-bind leaves the companion
lines intact.

## git (`git-config`)

### Switched

| Knob | git config key | Notes |
|------|----------------|-------|
| `email` | `user.email` | commit/author identity |
| `name` | `user.name` | commit/author identity |
| `signingkey` | `user.signingkey` | optional; omitted when empty |

Delivered by pointing `GIT_CONFIG_GLOBAL` at a kae-owned file under
`DataDir/companion/<profile>/git/config`.

### Preserved

- `~/.gitconfig` is **never modified**. The kae-owned file `[include]`s it, so
  aliases, `core.*`, and every other global setting survive in the bound
  directory.
- Outside the pinned directory `GIT_CONFIG_GLOBAL` is unset, so git reads the
  real `~/.gitconfig` unchanged. `kae unpin` removes the fragment and reverts.
- Repository-local config (`.git/config`) and any `GIT_CONFIG_*` the user sets
  themselves take precedence as git defines; the env conflict is reported by
  doctor.

## gh (`token`)

### Switched

| Knob | Env var | Notes |
|------|---------|-------|
| `GH_TOKEN` | `GH_TOKEN` | GitHub CLI token; secret, resolved via `exec()` lookup |

### Preserved

- `~/.config/gh/hosts.yml` is **never written**; kae does not touch gh's own
  config dir. The token is delivered purely through `GH_TOKEN`.
- doctor warns when `GH_TOKEN`, `GITHUB_TOKEN`, or `GH_ENTERPRISE_TOKEN` is
  already set in the environment, since those override the bound login.

## cloudflare (`token`)

### Switched

| Knob | Env var | Notes |
|------|---------|-------|
| `CLOUDFLARE_API_TOKEN` | `CLOUDFLARE_API_TOKEN` | used by wrangler/flarectl/terraform-cloudflare; secret, resolved via `exec()` lookup |

The upstream env-var contract is version-fluid (`RuntimeProbe`), so doctor
probes the binary before trusting the binding.

### Preserved

- No cloudflare config file is generated or copied.
- doctor warns when `CLOUDFLARE_API_TOKEN` or `CF_API_TOKEN` is already set.

## kubectl (`config-dir`)

### Switched

| Knob | Env var | Notes |
|------|---------|-------|
| `KUBECONFIG` | `KUBECONFIG` | path to a user-provided kubeconfig; non-secret, set directly |

### Preserved

- kae **references** the user's kubeconfig path; it never generates, copies, or
  reads the file (which may hold cluster credentials).
- doctor warns when `KUBECONFIG` is already set in the environment.
