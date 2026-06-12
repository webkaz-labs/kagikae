# Roadmap

Long-term ordering beyond the active release ([RELEASE.md](RELEASE.md)).
Implementation history lives in git log.

The active target (v0.5.0, the use/pin/run command system and overlay
directory isolation) lives in [RELEASE.md](RELEASE.md); what remains beyond
it is hardening and platform coverage, ordered below by user impact.

## Hardening backlog — daily-use robustness

- **Global commands inside pinned directories**: with a pinned `.mise.toml`
  exporting `CLAUDE_CONFIG_DIR`/`CODEX_HOME`, the adapters treat the
  overlay/home as the live base path, so `kae use` / `kae add` run there
  operate on the directory's isolated state while recording into the global
  state.json belief. Decide and enforce the intended semantics (operate on
  the overlay without polluting global belief, or refuse with guidance).

- **Codex keyring driver**: pin down the OS-credential-store item contract
  used by `cli_auth_credentials_store = "keyring"`, add structure guards,
  lift the detect-only restriction.
- **agy keyring support**: same problem as Codex — the default macOS/Linux
  storage is the keyring; today only the file-based fallback is switchable.
- **Login UX polish**: verify `claude /login` behavior across versions,
  support agy. (The "login flow exited without changing auth" case is now
  detected and refused with exit `11`.)
- **`kae env export --dotenv --reveal`**: explicit-flag value export for CI
  bootstrapping (today values are injection-only by design).
- **Performance polish**: combine/cache the multiple `security` subprocess
  calls per macOS switch; run per-tool `Detect` concurrently in `status`.
- **claude driver override for isolated smoke checks**: on macOS the
  keychain driver ignores temp `$HOME`s, so claude switch smoke checks can
  only run safely on Linux today; provide an explicit file-driver override
  (env var or config) so containers and smoke environments never touch the
  real login keychain.

## Platform coverage

- **Windows**: `%APPDATA%` layout, Credential Manager secret backend, lock
  implementation, `%USERPROFILE%\.claude` file-patch driver.
- **gemini / agy home isolation**: revisit once upstream exposes a stable
  home/config env var; until then `home` / `overlay` modes refuse them.

## Exploratory

- richer TTY (routed review surface) if daily use shows the need
- shell completion
- localized human output (Japanese)
- `kae shell init` convenience wrappers

## Review Triggers

- Antigravity transition date (2026-06-18): once personal Gemini CLI serving
  ends, demote the gemini adapter's default prominence and promote agy.
- First credential-layout change in any upstream tool: add a regression
  fixture and bump the adapter guard before widening support.
