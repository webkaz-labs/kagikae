# Security

Document security-sensitive behavior here.

## Baseline

- Secrets, tokens, and credentials must not appear in text output, JSON output,
  cache files, or reports.
- Subprocesses run through `internal/runner` and use context-aware execution.
- User-controlled identifiers are sanitized before use in filenames, cache
  keys, or external command arguments.
- Scanner or provider evidence should be reported as available, stale,
  unavailable, or skipped when security decisions depend on it.
- Normal user policy belongs in TOML config; secrets stay in environment
  variables or provider-native credential stores.

## External Tools And APIs

List external scanners, package managers, APIs, required versions, output
formats, cache policy, and known drift detection here.
