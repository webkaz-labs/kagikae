# Measuring an upstream tool's auth behaviour without logging in

Every technique here has been used on this repository's tools. Each entry says
what it proves, which tools it applies to, and the trap that cost a session.

## Which technique proves what

| Technique | Proves | Applies to |
|---|---|---|
| Subprocess PATH shim | the exact store name and attribute the tool asks for | **only tools a `PATH` shim reaches**, which is narrower than "tools that shell out" — see the table below, and establish it per tool |
| Live keychain *attribute* read | which items exist and their account attributes | any macOS keychain tool, read-only, no payload |
| Literal-count fingerprint over the bundle | every name kae models still exists and is referenced as often | any bundled tool |
| Identifier-normalized behaviour-site hash | the control flow around a semantic anchor is unchanged even when minified names churn | any bundled tool |
| Bundle-pair diff (old vs new version on disk) | a targeted re-verify list for this specific upgrade | tools that keep old versions (claude does) |
| Behavioural run in a temp HOME | what the tool actually does with a value | any tool that runs without a login for the path in question |

## Does a `PATH` shim reach this tool?

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
| agy | **yes** | **no** | zalando/go-keyring: `find`/`add`/`delete-generic-password` and `keyring_darwin.go` in the binary, `SecItemAdd` and `SecItemCopyMatching` zero times, `go-keyring-base64:` prefix — measured 2026-08-17 on 1.0.10, 1.1.12 and 1.1.13, then re-read unchanged on 1.1.22 and 1.1.23 on 2026-09-04. **What decides the second column is that the binary also spells `/usr/bin/security` out** (once in every one of those builds), an absolute path no `PATH` entry precedes. A shim run on 1.1.12 left the log empty, which by the paragraph above proves nothing on its own — that run's own agy log said `You are not logged into Antigravity`, which the never-reached-the-keyring hypothesis predicts just as well, and as of 2026-08-17 no positive discriminator like codex's had been found for agy |
| codex | **no** | n/a | the Rust `keyring` crate calls Security.framework directly; with a shim on PATH the log stays empty and codex fails with "Platform secure storage failure: A default keychain could not be found" |
| cursor | unverified | unverified | |

The shim itself: an executable early on `PATH` named `security` that logs `"$*"`
and exits non-zero, then
`env -i HOME=<temp> <VAR>=<dir> <tool> -p hi </dev/null`. The `-s <service>` in
the log is the name the tool actually asks for. This replaced a recipe that
needed a real account and a token refresh.

## Reading a bundle

**Node CLI shipped as a launcher + package** (copilot): the binary on `PATH` is a
launcher and the real CLI is plain minified JS on disk. Look for that layout
*first* — it turns a window-read into `grep -o`. It also means `copilot --version`
reports the package's version, not the launcher's, so a version manager pinning an
old launcher does not make `VerifiedVersion()` wrong.

**Find the package by reading the launcher's search list, never by remembering the
path.** It moved between 1.0.61 and 1.0.79 — the old `~/.copilot/pkg/universal/…`
is now the *last* candidate, so it keeps answering with a stale package while a
newer one runs; docs/VALIDATION.md § Upstream Behaviour Assumptions has the list as
read. Two traps in reading it: a path assembled from `join("Library","Caches",…)`
greps as **0**, so search an env-var name and read the function around it; and the
launcher auto-updates the package, so a behavioural run against an already identified
package needs `--no-auto-update`, `--prefer-version <v>`, or
`COPILOT_AUTO_UPDATE=false`. **That disabled-auto-update probe does not prove normal local selection**:
in 1.0.82, `COPILOT_AUTO_UPDATE=false` makes `Ir()` false and skips `lh()`, the branch
that looks for a newer installed package. Reproduce `lh()` from the launcher source
without executing it: collect readable `index.js` packages under `universal` and the
measured architecture for every `ui()` root, sort them by `fi()`'s descending semver
order (path ascending on a tie), skip the directory exactly equal to the launcher's
embedded version, and select the first other version only when it is not older. If
none qualifies, use the built-in version under the primary cache root only when
`ch()` / `Js()` can read `index.js`, `app.js`,
`prebuilds/darwin-arm64/runtime.node`, and `.extraction-complete`; fail rather than
letting the audit extract it when any is absent. Do not impose that marker on a newer
installed package: `lh()` selects those from readable `index.js`, after which the
audit separately validates the three payload files. Reject `COPILOT_CLI_DIST_DIR`,
which bypasses this selection, and duplicate selected-version trees whose
locale-dependent path tie-break cannot be reproduced safely. Count only that selected
tree; do not run the launcher.

**JS inlined in a single Mach-O** (claude, ~266 MB):

```sh
B=$(realpath "$(command -v claude)")
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

**Resolve a bundled CLI from the command the shell will run before counting it.**
For cursor, `dirname(realpath(command -v cursor-agent))` is the package tree, but
first require regular-file siblings `node` and `index.js`. mise 2026.9.0's
brew-cask installer copied only `cursor-agent` and discarded those siblings; that
launcher-only tree was an incomplete installation, not evidence that the upstream
literals disappeared. mise 2026.9.1 repaired the installer. A count over an
incomplete tree is not a moved fingerprint — fail before counting.

**Go binary** (agy): symbol names survive. Extract printable runs
(`re.finditer(rb'[\x20-\x7e]{6,}')`) and grep those — you get package paths like
`…/code_assist_client/codeassistclient.(*KeyringTokenStorage).SaveToken`, which
enumerate the store types, the chooser and the detectors. **Anchor a version diff
on neither the package nor the type name, because both move**: between 1.0.10 and
1.1.12 `shouldBypassKeyring`, the detectors and a keyring/file/chooser trio left
`jetski/cli/backend/auth/auth` for
`jetski/language_server/code_assist_client/codeassistclient` *and* were renamed, so
a package-anchored diff reports the whole storage layer as deleted and a
type-anchored one reports the same for every renamed member. The move was also not
wholesale — `cliTokenStorage` went 13 → 0 while `cliFileTokenStorage` went 5 → 11
and stayed where it was — so neither anchor even fails uniformly. **Literal counts
are what settle it**, which is the argument for measuring those first: they are
what contradicted the "storage layer deleted" reading here.

**Probing agy upgrades it**, so a symbol diff can lose its own subject: a temp-`HOME`
`agy models` run on 2026-08-17 sits in the same minute as a new binary appearing in
agy's mise install directory, and `agy --version` moved 1.1.12 → 1.1.13 across it.
The previous build survives beside it as `agy.<digits>.old`, which is what made the
pair diff still possible. Copy the binary out before any behavioural probe.

**A copied `agy --version` does not identify the bytes selected by another execution
environment.** On 2026-09-04 the interactive shell selected mise's retained 1.1.22,
while `mise exec` and `mise run audit` selected the already-installed 1.1.23. The
apparently inconsistent version outputs came from those two PATHs, not an update of
the 1.1.22 source: its before/after hash and metadata were unchanged, and the retained
probe copy stayed byte-identical. Resolve the command inside the environment that will
run the audit; a `command -v` in the surrounding shell is evidence only for that
shell.

For a fingerprint whose version-to-bytes pair is already known, compare the selected
real file to its recorded SHA-256 and count only an exact match; do not execute it or
a copy. The 1.1.22 and 1.1.23 artifacts' Go build info names only their compiler
build. Their bytes contain their respective exact version, but 1.1.23 also contains
1.1.22 in its changelog, so those strings are corroboration, not a general version
parser. For a new pair, package materialization needs explicit user approval: retain
both artifacts, record their hashes and metadata, and compare them read-only. An
install directory's version is also corroboration rather than identity, because agy
can replace itself without renaming it.

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
