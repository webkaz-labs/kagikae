# Medium-term plan after v0.20.0

## Agreed direction

Ship bounded daily-use improvements while investigating authentication questions
in a separate lane. Research findings may change later scope; they do not authorize
credential policy changes by themselves.

| Order | Destination | Admission and completion |
|---|---|---|
| v0.20.1 — released and verified | One owned global mise integration file | [mise-global-integration.md](mise-global-integration.md) owns implementation and acceptance |
| Following release candidate | Make diagnostic findings lead to appropriate explicit recovery | Compare global login/capture guidance and metadata-list recovery journeys; agree scope before adding commands or automatic repair |
| Parallel research | Credential attribution, refresh races and moved bound directories | Reproduce the ROADMAP counterexamples and establish observable evidence before choosing a new policy; no implementation scheduled yet |
| Later research | Behaviour-site comparisons for upstream drift | Demonstrate detection and cost alongside existing fingerprints before automating |
| Demand-gated | Completion routing generation and additional platforms | Existing ROADMAP demand/evidence gates remain; no standalone rewrite |

Global mise tasks are excluded from priority work: direct `kae` commands and their
completion already serve the agreed usage. Authentication research retains the
prerequisites in [ROADMAP.md](../ROADMAP.md) § Current work order. No release dates
or later version numbers are promised by this ordering.
