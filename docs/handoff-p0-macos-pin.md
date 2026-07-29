# Handoff — P0: a pinned directory drifts out of kae's control on macOS

**Status**: not started. Release gate for v0.12.0 ([RELEASE.md](RELEASE.md)).
**Branch**: start a new one off `main` (or off `feat/claude-oauth-account-switch`
if that has not merged yet — check `git log --oneline -1 main`).
**Why this file exists**: the investigation that found this ran in a very long
session. Everything needed to start is here; nothing else from that session is
required.

## The defect

`kae pin claude side` inside a pinned directory reports success, updates the mise
fragment, updates what `kae status` shows — and **claude keeps running the
previous account**. The credential is wrong, silently, and nothing offline
compares the two. This is reachable by normal `kae pin` use on macOS.

## Why it happens (measured on Claude Code 2.1.220)

kae models claude's credential location as a constant per platform: macOS =
keychain service `Claude Code-credentials`, Linux = a file. The real rule is
different, and kae's own isolation modes trigger the difference.

| # | Fact | Where in the bundle |
|---|---|---|
| U6 | The keychain service name is `Claude Code-credentials` **only while `CLAUDE_CONFIG_DIR` is unset**. With it set, the service is `Claude Code-credentials-<sha8>` where `<sha8>` is the first 8 hex chars of `sha256(configDir)`. `CLAUDE_SECURESTORAGE_CONFIG_DIR`, when set, is used instead of `CLAUDE_CONFIG_DIR`; set to the empty string it disables the suffix entirely. | `oq()` |
| U7 | Reads try keychain first and fall back to `<configDir>/.credentials.json`. A write goes to keychain — and **when the keychain item was absent immediately before the write, it deletes that file**. | `HUc()` (composed keychain + plaintext store) |

So the sequence in a pinned directory is:

1. `kae pin` writes `<pinDir>/.credentials.json` and points `CLAUDE_CONFIG_DIR` at
   `<pinDir>`. claude starts, finds no per-dir keychain item, and authenticates
   from kae's file. **kae's isolation works, as designed.**
2. The access token expires (hours) and claude refreshes it. The refresh writes
   the **per-dir keychain item** and deletes `<pinDir>/.credentials.json`.
3. From then on claude reads the per-dir keychain item. Nothing kae writes into
   that directory is ever read again.
4. `kae pin claude side` rewrites the file, updates the fragment, reports
   success. claude still runs the account from step 1.

Linux is unaffected: the same hash namespacing applies but there is no keychain
backend, so the refresh writes the file and kae stays in the loop.

### Verify it yourself first

```bash
# Reproduce (temp HOME, never the real one)
export HOME=$(mktemp -d)   # in a subshell
# ... kae init, capture two claude accounts, kae pin -i one of them ...
# run claude in the pinned dir, wait for / force a token refresh, then:
python3 -c 'import hashlib,sys; print(hashlib.sha256(sys.argv[1].encode()).hexdigest()[:8])' "<pinDir>"
security find-generic-password -s "Claude Code-credentials-<sha8>"   # now exists
ls -la "<pinDir>/.credentials.json"                                  # now gone
```

The bundle is readable with `strings` on
`~/.local/share/claude/versions/<version>` — `strings -n 6 <path> | grep -o
'.\{400\}oq(\|.\{400\}SecureStorage.\{400\}'` and similar get you to `oq()` and
the composed store. **Re-measure before building on U6/U7**: they are upstream
behaviour, and the whole point of the machinery added in v0.12.0 is that such
facts expire. `docs/VALIDATION.md` § "Upstream Behaviour Assumptions" has the row
for this rule; update its verified version when you confirm it.

## A second, independent defect in the same code

`preparePinConfig` / `prepareBond` (`internal/cmd/miseinit.go`) copy the
credential from `app.realToolHome(tool)` — the **live** store — not from the
account's snapshot. Pinning an account that is not currently globally active
therefore seeds the directory with whichever credential happens to be live. Only
the re-bind path (`swapDirCredential`, `internal/cmd/rebind.go`) uses the
snapshot. `docs/CLI.md` reads as if the account's own credential were always
used. Fix it alongside the above; both live in the materializers.

## Suggested order

1. **Detect** (small, offline, no secret access). Compute the per-dir service
   name and check whether that item **exists** (`security find-generic-password
   -s <service>` through `internal/runner`, existence only — never read the
   payload). Warn from `kae pin`, `kae status`, and `doctor`: "this directory's
   claude auth has moved into a per-directory keychain item; kae's credential copy
   is no longer read — recreate the binding with `kae unpin && kae pin`". This
   makes the wrong-account state visible immediately, which matters more than the
   full fix.
2. **Then decide the real fix.** The cheapest correct one looks like:
   `swapDirCredential` writes the **per-dir keychain item** directly — the
   existing keychain driver works unchanged if the spec's `Target` becomes
   `Claude Code-credentials-<sha8>` — and deletes the now-stale file copy. That
   also opens a recapture path out of pinned directories, which today do not
   refresh their snapshot at all (see the audit note in ROADMAP.md).
3. **Fix the snapshot-vs-live copy** in `preparePinConfig` / `prepareBond`.

Recorded and rejected: pinning `CLAUDE_SECURESTORAGE_CONFIG_DIR` to the empty
string would collapse every pin onto the shared keychain item. It would "work"
and it destroys per-directory isolation.

## Constraints (from AGENTS.md)

- `mise run check` must pass — the only gate. Tests use `t.TempDir()` HOME/XDG
  roots; **never touch the real `$HOME`**.
- All subprocesses go through `internal/runner` (argv arrays, no shell strings).
- Adapters declare specs; capture/apply IO stays in `internal/artifact`.
- JSON contract tokens in `internal/constants`; check codes are additive and
  `schema_version` stays `1`.
- No secret in stdout/stderr/JSON/logs. Existence checks only for the keychain
  probe.
- Docs to update in the same commit: `docs/ADAPTERS.md` (the isolation section),
  `docs/CLI.md` (`kae pin -i`'s credential-copy note), `docs/VALIDATION.md` (the
  storage-resolution row), `docs/ROADMAP.md` (move the entry out of the backlog),
  `docs/RELEASE.md` (clear the v0.12.0 release gate).

## Where the current state is written down

- `docs/ROADMAP.md` — both defects, in the hardening backlog.
- `docs/RELEASE.md` — v0.12.0's release gate.
- `docs/VALIDATION.md` § "Upstream Behaviour Assumptions" — the storage-resolution
  rule, with the note that kae itself violates it today.
- `internal/cmd/miseinit.go` — a `KNOWN GAP` comment where the old, wrong comment
  ("CLAUDE_CONFIG_DIR forces file-based auth even on macOS") used to be.
