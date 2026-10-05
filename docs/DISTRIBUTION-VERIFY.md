# Distribution verification reuse

Use these maintainer commands from a trusted, revision-pinned kagikae checkout.
The product specification, installer and completion scripts are trusted code or
expectations supplied by the operator, never downloaded expectations from the
release being checked. Go runs the shared implementation; gh and Packslip are
required for published verification. Prerequisite tools must already be available. Installer checks install the tested
product only inside their temporary directory.

## Staged release verification

`go run ./tools/devtools/cmd/distributionverify -spec /absolute/spec.json -dir /absolute/assets -bundle /absolute/packslip.sigstore.json`

The spec is strict JSON with `schema_version: 1`, `repository`, `project`, `tag`,
`version`, `commit`, `workflow`, `identity`, `issuer`, `artifacts` and `resources`.
The workflow identity must exactly match that repository, workflow and tag, with
GitHub's OIDC issuer. Each artifact supplies `name`, `os`, `arch`, optional `libc`,
`binary` and the exact `members` array. Names are explicit; no archive naming
convention is inferred. `resources` lists exact completion maps with `kind`,
`shell` and `archive`. Use an empty array when none are expected.

For example, a product's artifact can be:

```json
{"name":"side_1.2.3_darwin_arm64.tar.gz","os":"darwin","arch":"aarch64","binary":"bin/side","members":["bin/side","NOTICE"]}
```

The engine copies regular inputs into its private staging directory, checks the
exact tar.gz/checksum/member sets, verifies GitHub provenance against the source
commit/ref/workflow, verifies the Packslip signature before decoding it, and
compares its project, version, source, subjects, artifacts and completion resources.
It owns that order. `requires` metadata remains accepted as in the kagikae adapter;
it is not an execution sandbox. Other archive formats and resource kinds are not
supported by this verifier.

Add `-native ARCHIVE -expect EXPECTED_STDOUT -- version` to execute the verified
native payload. EXPECTED_STDOUT includes its trailing newline. Execution uses a
fresh HOME/XDG environment and bounded process group. This is configuration
isolation, not a network or macOS keychain sandbox; a process leaving its group
is outside the cleanup guarantee. The caller selects a runnable native artifact.

## Installer and synthetic fixtures

Add `-installer /absolute/install.sh -repository-env SIDE_REPO -installed-binary side -expect EXPECTED_STDOUT -- version`
to run a trusted installer and check its installed executable. The supported
installer contract is `--version TAG --install-dir DIRECTORY`, with the repository
passed in the explicitly named `*_REPO` variable. Downloads use the fixed curl
argv accepted by the shared fixture, which maps exact release URLs to an owned
loopback HTTP server with immutable response bytes. Unknown URLs or malformed argv remain a
failure even if the installer ignores the curl exit. Installer-specific receipts,
initialization and retained-data semantics stay in product adapters.

`-fixture-key /absolute/fixture.pub` explicitly selects synthetic fixture trust.
The signature is verified with that key and permits an unlogged signature; build
provenance is not verified in this mode and the report says so. It never relaxes
the production mode. A checksum alone does not authorize native/installer execution.
The fixture source metadata is an asserted fixture expectation, not build evidence.

`PACKSLIP_BIN=/absolute/verified/packslip go test -count=1 ./tools/devtools/cmd/distributionverify`
runs the real key-generation/signing and wrong-key control using a differently
named fixture product. Without that variable the signature acceptance test is
explicitly skipped. Ordinary shared tests still check order, archive faults,
installer rejection, HOME isolation and fixture request refusals; their command
seams are not evidence of a real cryptographic verification.

## Completion verification

`go run ./tools/devtools/cmd/completionverify /absolute/completion-spec.json`

The strict JSON spec contains `schema_version: 1`, `tool`, `function`, absolute
`bash` and `zsh` script paths, absolute `binary_dir`, and `cases`. Each case has
`words` (including the tool and current word), `required`, `forbidden`, and optional
`empty: true`. A positive case needs a required candidate; an empty case must not
also require a candidate. Candidate comparison preserves spaces and flag values.
For example: `{"words":["side","--flag=value"],"required":["with space"],"forbidden":["secret"]}`.

Both real shells are required. Bash currently expects the exact registration
`complete -F FUNCTION TOOL`; registration options require another adapter. Zsh
checks registration separately and captures only `compadd -- CANDIDATES`; other
option forms fail explicitly, even if the completion function ignores that failure.
This observes candidates without driving a terminal editor. Fish runtime behavior
is not verified. Scripts can execute programs from `binary_dir` inside the fresh HOME;
they must be trusted.

The shared same-shell mise lifecycle additionally checks static resource version,
dynamic target version, manual reload and restored registration. Product adapters
supply tool, command, prefix, candidate and version expectations; the current
version-log contract is `TOOL vVERSION`. It is used by kagikae's Packslip smoke.

## Ownership

`releaseverify` supplies kagikae's historical archive policy and release identity;
`packslipverify` supplies product lifecycle/setup; `installverify` supplies lock and
receipt checks. They use `tools/devtools/distribution`, `completioncheck` and
`commandrun`. Product branches and loops do not become a lifecycle DSL. Fixture
HTTP routes are fixed and non-forwarding; unexpected paths and authorization
headers fail the final verdict. Declared missing-asset routes are explicit negative
controls. Query strings on a known route do not select different fixture content; query
matching and release-list pagination are not checked.

See [DOCUMENTATION.md](DOCUMENTATION.md) for the other portable maintenance checks.
