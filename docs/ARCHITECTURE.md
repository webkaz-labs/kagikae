# Architecture

Describe package layout, runner boundaries, provider/backend seams, cache
strategy, and known traps.

When a provider exposes machine-readable native state, prefer that native
interpretation for inventory/report data. Keep source-file parsing for
validation, mutation targets, fallback roots, and file/line evidence.

If provider-derived state is cached persistently, bump the cache schema/version
when provider resolution semantics or included providers change.

If the tool adds a TTY, keep it as a routed view over typed reports. Dashboard,
list, detail, filter/query, and confirmation screens should share explicit
route state so Back/Home, focused row identity, and item-scoped actions return
to the expected place.

For slow providers, render the cheap shell first and refresh stable
loading/progress rows as evidence arrives. Do not restart a second TTY program
just to show a prepared detail view.
