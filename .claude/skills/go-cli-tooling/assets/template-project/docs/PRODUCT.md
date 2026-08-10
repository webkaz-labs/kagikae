# Product

This document is the stable product entrypoint. Keep implementation structure
in `ARCHITECTURE.md`, command syntax in `CLI.md`, interactive behavior in
`UX.md`, visual identity in `DESIGN.md`, and executable checks in
`VALIDATION.md`.

## Mission And Completion Goal

- **User:** Who runs the tool and in what environment?
- **Problem:** What repeated decision or task is currently difficult?
- **Normal workflow:** What is the shortest successful path?
- **Completion:** What observable state tells the user they are done?
- **Non-goals:** What adjacent work does this tool intentionally not own?

## Product Boundaries

- Typed reports are the source for text, JSON, and optional TTY output.
- Interactive views review or apply report-backed actions; they do not invent
  hidden behavior.
- Mutations are item-scoped, previewable, and explicit. State what remains
  read-only.
- External tools, credentials, and platform support belong here only as user
  constraints; implementation seams belong in `ARCHITECTURE.md`.

## Primary Journeys

Record only the journeys that determine the product shape.

| Journey | Entry | Success state | Escape or failure |
|---------|-------|---------------|-------------------|
| inspect | bare command | user finds the relevant item and evidence | command exits without changing state |
| act | explicit mutation command or focused item | one confirmed action runs and refreshed state is shown | cancel/error preserves the prior state |

## Domain Index

Keep this file small. When product detail grows, retain this file as the stable
index and move independently owned topics under `docs/product/`.

| Document | Owns | Read when |
|----------|------|-----------|
| `product/SCOPE.md` | optional detailed scope and non-goals | multiple product areas need separate scope decisions |
| `product/JOURNEYS.md` | optional detailed journey contracts | the journey table above no longer answers common questions |

Do not create these files until their topics can stand alone. Link every child
here, and do not repeat its long-form content in this index.
