# Roadmap

Long-term ordering beyond the active release ([RELEASE.md](RELEASE.md)).
Implementation history lives in git log.

The active target (v0.6.0, adapter coverage — copilot/cursor/opencode, the
gemini → agy transition — and the pinned-directory guard) lives in
[RELEASE.md](RELEASE.md); what remains beyond it is hardening and platform
coverage, ordered below by user impact.

## Hardening backlog — daily-use robustness

- **TUI**: an interactive mode (profiles/accounts browser, pin status,
  config maintenance) on top of the stable JSON surface, so daily
  switching does not require remembering flags. Candidate once the
  v0.5.0 command system has settled.
- **Remote share-list definitions (ship)**: implement the v0.6.0 design if
  it holds — published defaults for the overlay share list, explicit
  fetch, diff-before-adopt, hard-coded auth denylist.
- **Codex keyring driver**: pin down the OS-credential-store item contract
  used by `cli_auth_credentials_store = "keyring"`, add structure guards,
  lift the detect-only restriction.
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
- **agy home isolation**: revisit once upstream exposes a stable
  home/config env var; until then `home` / `overlay` modes refuse it (the
  same applies to the v0.6.0 adapters until their env vars are verified).

## Exploratory

- richer TTY (routed review surface) if daily use shows the need
- shell completion
- localized human output (Japanese)
- `kae shell init` convenience wrappers

## Review Triggers

- First credential-layout change in any upstream tool: add a regression
  fixture and bump the adapter guard before widening support.
