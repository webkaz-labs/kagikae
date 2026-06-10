# Release Target

Describe the active release target, scope, non-goals, and release-ready
criteria.

Use the shared version policy from `docs/go-cli/RELEASE.md`: command/product
release labels use `<tool> vMAJOR.MINOR.PATCH`; JSON `schema_version` remains
an integer schema contract and is not the tool release version.

Keep this file to the active target plus, at most, the immediately previous
completed baseline. Move durable behavior to stable docs and future ordering to
`ROADMAP.md`.
