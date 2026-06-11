# Roadmap

Long-term ordering beyond the active release ([RELEASE.md](RELEASE.md)).
Implementation history lives in git log.

The original phase plan (Phase 2 login/run/env/mise, Phase 3 home, Phase 4
overlay, Phase 5 agy) shipped together in v0.2.0; what remains is hardening
and platform coverage, reordered below by user impact.

## v0.3.x candidates — daily-use hardening

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
