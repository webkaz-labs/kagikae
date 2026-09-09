# Development tools

Shared maintainer commands and verification libraries. This directory uses the
repository's existing Go module; it is not a separately published module. Use a
trusted, revision-pinned checkout, with Go and each command's prerequisites already
available. Run the examples from this directory, passing the inspected project
explicitly so source lookup never follows its Go workspace.

| Entry | Purpose | Prerequisites |
|---|---|---|
| `bash shell/check-docs.sh /absolute/project` | Recognized Markdown links and section citations | Bash, Go |
| `go run ./cmd/docscan -root /absolute/project` | Duplicate prose anchored on Go identifiers; glossary options are explicit | Go |
| `bash shell/check-go-format.sh --portable /absolute/module` | Formatting and import checks | Bash, Go; pinned analyzers may populate caches |
| `shell/sweep-quantities.sh` | Report quantity wording in a Git diff; invoke with Bash from the inspected Git tree | Bash, Git |
| `go run ./cmd/distributionverify` | Trusted-spec distribution and installer checks | Go, gh, Packslip; execution modes are explicit |
| `go run ./cmd/completionverify /absolute/spec.json` | Actual Bash/Zsh candidates and registration | Go, Bash, Zsh |
| `go run ./cmd/docrefs /absolute/project` | Reference records for policy adapters | Go |

[Maintenance guidance](../../docs/DOCUMENTATION.md) owns invocation details for
reference, prose and formatting checks. [Distribution verification](../../docs/DISTRIBUTION-VERIFY.md)
owns specs, execution/trust boundaries and shell observation limits. Those contracts
are linked rather than copied here.

The libraries `commandrun`, `distribution`, `completioncheck`, `docrefs` and
`glossary` are importable by repository adapters. They do not import the application's
packages. `portable` exercises foreign projects; product-specific document policies,
real-document tests and release lifecycles remain under `scripts`. In particular,
`scripts/check-docs.sh` adds required documents, the Documentation Map and coverage
floors; mise supplies the product glossary; the quantity wrapper supplies the
historical control. No product gate is weakened by the portable entrypoints.
