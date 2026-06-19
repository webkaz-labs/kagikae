# Data Model

Describe config files, desired/live state, cache files, reports, and status
vocabulary.

## Config

Normal user policy should live at:

```text
${XDG_CONFIG_HOME:-~/.config}/dotfiles-tool/config.toml
```

Precedence:

1. defaults
2. config file
3. environment overrides for CI/debug/secrets
4. CLI flags

Document unknown-key behavior here before shipping config support.

## Reports

Stable JSON reports include `schema_version`. Agent-facing arrays should encode
as `[]`, not `null`.

## Status Vocabulary

Keep status and decision tokens in constants before multiple commands depend on
them.
