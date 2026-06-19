# CLI

Describe commands, flags, exit codes, text output, JSON contracts, TTY behavior,
and localization.

## Commands

```bash
dotfiles-tool
dotfiles-tool check
dotfiles-tool check --format json
```

## Output

- Human text writes normal reports to stdout.
- Long human detail should wrap by terminal width when a TTY width is
  available; use deterministic fallback widths for non-TTY output and tests.
- Complex item sets should use grouped list views with filters, expandable
  evidence, compact status/action badges, and item-scoped actions where safe.
- Usage and runtime errors write to stderr.
- JSON output is stable, non-localized, and includes `schema_version`.
- TTY behavior must be an interaction layer over report data, not the only
  place behavior is computed.

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | success |
| `1` | runtime error |
| `2` | read-only report found drift, findings, or review-needed state |
| `64` | usage/config/flag error |
