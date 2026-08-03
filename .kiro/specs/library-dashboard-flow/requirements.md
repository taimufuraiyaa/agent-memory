# Library Dashboard Flow Requirements

## Context

The backend implements the Living Knowledge Library through release Task I4, while the merged notebook dashboard has no user-facing path to those APIs. This slice connects the existing import, structure, grounded-query, citation, and memory-review contracts without changing source-retention policy or pretending that retrieved evidence is model-generated interpretation.

## Requirements

### R1 — Discoverable library workspace

The notebook shall expose Library as a primary destination without replacing Notes as the default destination.

Acceptance criteria:

- Library is reachable from desktop and compact navigation.
- Changing the selected project changes the library API workspace.
- Existing Notes, Search, Ask, Activity, and System destinations remain reachable.
- When the left sidebar is collapsed, every destination is represented by a recognizable icon with an accessible name.
- When the left sidebar is expanded, every destination shows its icon with the destination name below it.

### R2 — Portable reader identity and library scope

The dashboard shall collect a reader principal and library identifier at runtime and retain those preferences locally on that client.

Acceptance criteria:

- No developer username, home directory, or device path is compiled into the dashboard.
- Personal libraries work without organization fields.
- Organization libraries require an organization identifier and include it in authorized queries.

### R3 — Whole-source Markdown import

The first dashboard slice shall import an entire Markdown or plain-text source through the existing ingestion endpoint.

Acceptance criteria:

- A user can select a local Markdown/text file or paste text.
- The request includes title, edition label, language, principal, library, and workspace.
- The completed job exposes work, edition, asset, passage, and structural-node counts.
- Import failures remain visible and preserve the user's form input.

### R4 — Contents and index visibility

After import, the dashboard shall load the edition structure and show that the complete source became navigable structural nodes and passages.

Acceptance criteria:

- Structural nodes display in source order with kind, title, and offsets.
- The interface distinguishes source storage, rebuildable passage indexes, and durable memory.
- Re-import results can identify reused editions/assets without presenting duplicate books.

### R5 — Grounded book conversation

The dashboard shall let the reader write a remembered quote, statement, or question and retrieve authorized book evidence.

Acceptance criteria:

- A question such as a remembered quotation plus an interpretation can be submitted in one conversation composer.
- Results show source passages, scores, structural identifiers, and locators returned by the API.
- Empty evidence is presented as unanswerable, not as an invented answer.
- The UI labels retrieved source evidence separately from the reader's interpretation and from any proposed agent memory.

### R6 — Suggested-memory review

The user shall be able to turn a grounded interpretation into a suggested memory and explicitly accept or reject it.

Acceptance criteria:

- Proposal creation is opt-in and requires editable memory content.
- A proposal displays status and provenance before review.
- Accept and reject actions use the existing review endpoint and visibly update status.
- Source text is not silently copied into durable memory when the user did not request retention.

### R7 — Verification and release safety

The integration shall remain compatible with the embedded dashboard and all existing backend behavior.

Acceptance criteria:

- Dashboard contract tests cover navigation, API methods, import, structure, evidence, and review surfaces.
- Dashboard test, typecheck, and production build pass.
- Embedded dashboard assets are rebuilt from source.
- Repository-wide Go tests and the developer-path portability test pass.
