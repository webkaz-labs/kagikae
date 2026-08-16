# Measuring an upstream tool's auth behaviour without logging in

Every technique here has been used on this repository's tools. Each entry says
what it proves, which tools it applies to, and the trap that cost a session.

## Which technique proves what

| Technique | Proves | Applies to |
|---|---|---|
| Subprocess PATH shim | the exact store name and attribute the tool asks for | **only tools that shell out** — see the table below, and establish it per tool |
| Live keychain *attribute* read | which items exist and their account attributes | any macOS keychain tool, read-only, no payload |
| Literal-count fingerprint over the bundle | every name kae models still exists and is referenced as often | any bundled tool |
| Identifier-normalized behaviour-site hash | the control flow around a semantic anchor is unchanged even when minified names churn | any bundled tool |
| Bundle-pair diff (old vs new version on disk) | a targeted re-verify list for this specific upgrade | tools that keep old versions (claude does) |
| Behavioural run in a temp HOME | what the tool actually does with a value | any tool that runs without a login for the path in question |

## Does this tool shell out to `/usr/bin/security`?

**Establish this per tool before assuming the shim applies.** An audit once
proposed generalizing it to "every keychain-backed tool" and was wrong about one.

**Shelling out is necessary but not sufficient**, and reading this table as the
shim's precondition is how agy's row got believed for a release: a `PATH` shim
only intercepts a tool that *resolves* the name through `PATH`. One that spells
`/usr/bin/security` out reaches the real binary with a shim first on `PATH` and
leaves an empty log — indistinguishable from a tool that never asked. Check for the
absolute path in the binary before reading an empty log as "the keyring was not
consulted".

| Tool | Shells out | Shim reaches it | Evidence |
|---|---|---|---|
| claude | **yes** | **yes** | the shim log fills with `find-generic-password` argv |
| agy | **yes** | **no** | zalando/go-keyring: `find`/`add`/`delete-generic-password` in the binary, no `SecItemAdd`/`SecItemCopyMatching`, `go-keyring-base64:` prefix — but its `keyring_darwin.go` carries the literal `/usr/bin/security` (once in each of 1.0.10 and 1.1.12), and a shim run on 1.1.12 left the log empty |
| codex | **no** | n/a | the Rust `keyring` crate calls Security.framework directly; with a shim on PATH the log stays empty and codex fails with "Platform secure storage failure: A default keychain could not be found" |
| cursor | unverified | unverified | |

The shim itself: an executable early on `PATH` named `security` that logs `"$*"`
and exits non-zero, then
`env -i HOME=<temp> <VAR>=<dir> <tool> -p hi </dev/null`. The `-s <service>` in
the log is the name the tool actually asks for. This replaced a recipe that
needed a real account and a token refresh.

## Reading a bundle

**Node CLI shipped as a launcher + package** (copilot): the binary on `PATH` is a
launcher and the real CLI is plain minified JS on disk at
`~/.copilot/pkg/universal/<newest>/app.js`. Look for that layout *first* — it
turns a window-read into `grep -o`. It also means `copilot --version` reports the
package's version, not the launcher's, so a version manager pinning an old
launcher does not make `VerifiedVersion()` wrong.

**JS inlined in a single Mach-O** (claude, ~266 MB):

```sh
B=~/.local/share/claude/versions/<ver>
grep -oab -- '<literal>' "$B" | cut -d: -f1                       # offsets
dd if="$B" bs=1 skip=$((off-330)) count=700 2>/dev/null \
  | LC_ALL=C tr -c '\11\12\15\40-\176' '.'                        # window
```

Traps, all hit for real:
- `grep -oa '.\{450\}<literal>.\{250\}'` backtracks and times out at two minutes
  on a multi-MB single line. Use offsets plus `dd`.
- `strings -n 6` is useless on a minified bundle: it is one enormous "string".
- `tr` fails with `Illegal byte sequence` without `LC_ALL=C`.
- The constant-pool region (low offsets) is not readable as text; the JS body is
  far in. A literal that appears both in the pool and in code will give you
  several useless hits before a useful one — read them all.

**Go binary** (agy): symbol names survive. Extract printable runs
(`re.finditer(rb'[\x20-\x7e]{6,}')`) and grep those — you get package paths like
`…/code_assist_client/codeassistclient.(*KeyringTokenStorage).SaveToken`, which
enumerate the store types, the chooser and the detectors. **Anchor the grep on the
type, not the package**: between 1.0.10 and 1.1.12 that whole group moved from
`jetski/cli/backend/auth/auth` to
`jetski/language_server/code_assist_client/codeassistclient` and was renamed with
it, so a package-anchored diff of the two binaries reports the entire storage layer
as deleted and its replacement as unrelated additions. That is what it looked like
here before the literal counts contradicted it.

**Two-modules red herring.** A symbol can exist twice: codex has
`compute_store_key` in both `codex_login::auth::storage` (the CLI credential,
the one you want) and `codex_rmcp_client::oauth` (MCP OAuth), and a stripped
binary's symbol grep finds the MCP one first. cursor has the same shape with
`grant_type=refresh_token`. This is the strongest argument for reading public
source when there is any.

## Behavioural runs that need no login

- **Does the tool resolve a relative path variable against its own cwd?** Create
  the directory under a temp cwd, put a *deliberately invalid* config in it, and
  run any subcommand that parses config from two different working directories.
  The error message names the path the tool resolved — and often shows whether it
  canonicalized (`/var` → `/private/var`).
- **Which keychain item does a login create?** Some tools have a purely local
  login: `printf 'sk-not-a-real-key' | CODEX_HOME=<temp> codex login --with-api-key`
  writes without a network call. Verify with an attributes-only
  `security find-generic-password`, computing the expected account **outside kae**
  (`shasum`), and clean up with the tool's own `logout`.
- **Is a file still the live store?** Write a dummy store in a temp XDG root, run
  the tool's `list`-shaped subcommand, rewrite the file with different contents
  and run again. If the second run reports the new contents, the file wins over
  any cache or database. Then run the tool's `logout` and see what it empties —
  which is how opencode's `auth.json` was confirmed to still be the live store
  while `account.json` turned out to be a derived leftover and a DB row a dormant
  one-shot import.

## The rule the measurements keep proving

**Never declare an artifact for a location you could not measure.** A guessed
path is a write nothing reads, and it reports success. When the rule leaves the
environment, warn if there is a proxy and record it in docs/ADAPTERS.md if there
is not.
