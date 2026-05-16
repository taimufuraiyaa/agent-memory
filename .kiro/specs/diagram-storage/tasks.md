# Diagram Storage - Tasks

## Checklist

- [x] Add `diagram_lang` and `diagram_code` columns to `memories` with idempotent migration.
- [x] Extend `core.MemoryEntry` to carry an optional `diagram` payload.
- [x] Update write pipeline to extract diagram fences into the diagram payload and keep `content` clean.
- [x] Update session-end extraction to store diagram blocks as a single memory item with diagram payload.
- [x] Update recall assembly + token budgeting to include diagrams in the rendered recall output.
- [x] Update embedding input to include diagram code for retrieval ranking.
- [x] Add/adjust unit tests and run `go test ./...`.
