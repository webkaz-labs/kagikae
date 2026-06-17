# Go CLI standards

This file is the stable entrypoint for Go CLI standards in this skill bundle.
The detailed standards are split under [references/](.) so the root
document stays small.

These standards are derived from the two production Go CLIs in the source repository:

- `macos-settings`
- `updev`

## Standard Documents

| Document | Purpose |
|----------|---------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | Package layout, runner/provider boundaries, cache, JSON contracts, TTY separation. |
| [CLI-SPEC.md](CLI-SPEC.md) | CLI behavior contract: commands, flags, exit codes, text/JSON/TUI output, localization. |
| [DOCUMENTATION.md](DOCUMENTATION.md) | Required README/AGENTS/docs structure and maintenance rules. |
| [LIBRARIES.md](LIBRARIES.md) | Recommended Go libraries and when not to add dependencies. |
| [SECURITY.md](SECURITY.md) | Secret handling, subprocess safety, config/file security, report evidence, external tool/API rules. |
| [TESTING.md](TESTING.md) | Unit, JSON regression, text/TTY, integration, and smoke testing standards. |
| [RELEASE.md](RELEASE.md) | Release-ready, compatibility, deprecation, and generated help/docs policy. |
| [TEMPLATE.md](TEMPLATE.md) | How to copy and adapt the template project. |
| [PATTERNS.md](PATTERNS.md) | Opt-in reusable patterns (mise integration, did-you-mean hints). |
| [assets/template-project/](../assets/template-project/) | Minimal new Go CLI skeleton. |

## Quick Rules

- Keep `main.go` thin. It dispatches only.
- Put command parsing, report builders, output formatting, and TTY routing in
  `internal/cmd`.
- Put subprocess execution behind a `runner.Runner` test seam.
- Keep JSON output as a versioned contract with stable status vocabulary.
- Keep report builders independent from TTY interaction. TTY consumes reports;
  it does not compute unique behavior.
- Use TOML for normal user configuration. Keep secrets, test endpoints, and
  one-off CI/debug overrides in environment variables.
- Keep security evidence and unavailable states structured in JSON.
- Use the local `check` task for the normal pre-commit validation path:
  low-noise lint, formatting/import checks, tests, vet, module verification,
  and build.
- Keep vulnerability, supply-chain, SAST, and agent-code-quality checks in a
  separate `audit` task unless a project documents a narrow promoted blocker.
- Add docs and validation before treating a new command as shipped.
- For mise integration or did-you-mean hints, follow the opt-in patterns in
  [PATTERNS.md](PATTERNS.md) instead of reinventing them.

When a tool needs project-specific details, keep them in the tool's own docs.
