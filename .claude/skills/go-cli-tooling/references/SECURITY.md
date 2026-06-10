# Go CLI Security Standard

Repository-local CLIs often orchestrate package managers, scanners, config
files, and shell commands. Treat security as part of the command contract, not
as an optional afterthought.

## Secret Handling

- Never print secrets, tokens, credentials, or full authorization headers in
  text or JSON output.
- Redact sensitive values before they enter report structs, logs, cache files,
  or errors.
- Do not store secrets in TOML config. Use environment variables, platform
  keychains, or provider-native credential stores.
- Include paths to credential sources only when the path itself is not
  sensitive and helps remediation.

## Subprocesses

- Run all subprocesses through `internal/runner`.
- Use `exec.CommandContext` through the runner and apply bounded timeouts for
  provider probes that can hang.
- Pass arguments as separate argv values. Do not build shell command strings
  from user input.
- Sanitize user-controlled names before using them in filenames, cache keys,
  temporary paths, or external command arguments that expect identifiers.
- Keep stdin closed or explicit unless an interactive command requires it.
- Capture stdout/stderr separately and decide which parts are safe to display.

## Config And Files

- Normal user policy lives in TOML under `$XDG_CONFIG_HOME/<tool>/`.
- Config precedence should be documented in the tool's `DATA-MODEL.md`:
  defaults, config file, environment overrides, then CLI flags.
- Unknown config keys should default to warning for early releases and may
  become errors after the schema is stable.
- Validate file permissions when reading credentials, executable hooks, or
  writeable config that can affect mutation/security decisions.
- Snapshot file-backed manifests before mutation where rollback is feasible.

## Reports And Evidence

- JSON reports should include machine-readable evidence state when security
  decisions depend on external data: `available`, `stale`, `unavailable`, or
  `skipped`.
- Include unavailable reasons rather than silently omitting a scanner/provider.
- Include active policy and config path in JSON when policy affects security
  or mutation decisions.
- Keep scanner findings structured. Human text can summarize, but agents must
  not need to parse prose to identify a finding.

## External Tools And APIs

- External scanners and APIs should be opt-in unless they are fast, local,
  deterministic, and clearly part of the command's daily workflow.
- Cache slow or rate-limited evidence with age metadata.
- Detect tool/API contract drift where feasible and report it as a maintenance
  finding rather than misclassifying results.
- Prefer official scanner output formats over parsing human text.
