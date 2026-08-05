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

### R2 — System-managed reader identity and library scope

The dashboard shall let the server derive stable reader and library identifiers while retaining user control over personal versus organization scope.

Acceptance criteria:

- No developer username, home directory, or device path is compiled into the dashboard.
- Reader and library identifiers are not exposed as editable dashboard fields.
- Personal libraries work without organization fields or client-stored identifiers.
- Organization libraries require an organization identifier and include it in authorized queries.

### R3 — Customer-facing whole-book import

The Library shall begin with one clear import action and reveal editable book metadata only after the reader selects a supported source.

Acceptance criteria:

- The empty Library presents “Import a book” as its primary action rather than exposing the complete ingestion form.
- A user can select a local PDF, EPUB, Markdown, or text file.
- After selection, the interface shows the source filename, format, size, editable title, edition, language, and library scope before submission.
- The request includes title, edition label, a selected language tag, and workspace; the server derives omitted reader and library identifiers.
- Indexing runs after explicit confirmation and is represented as background work rather than as a numbered foreground step.
- Import failures remain visible, preserve the selected source and metadata, and offer a retry path.

### R4 — Book reading surface and index visibility

After import, the dashboard shall become a reading workspace with a book-like content body and an adjacent table of contents.

Acceptance criteria:

- Structural nodes display in source order as a right-side book index on wide screens and a compact navigable panel on narrow screens.
- The body prioritizes readable excerpts, citations, and conversation instead of internal work, edition, asset, and offset identifiers.
- Technical metadata remains available through restrained details without dominating the reading experience.
- Re-import results can identify reused editions/assets without presenting duplicate books.

### R5 — Persistent grounded book conversation

The dashboard shall let the reader write a remembered quote, statement, or question and retrieve authorized book evidence.

Acceptance criteria:

- A persistent chat composer sits at the bottom of the reading workspace and accepts a free-form question or remembered quotation.
- The composer remains available once a book is ready; background indexing does not appear as a numbered prerequisite flow.
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

### R8 — Adaptive notebook canvas

The notebook shell shall reserve columns only for panels that are mounted and let non-note destinations use the available canvas without a dead strip on the right.

Acceptance criteria:

- Closing the explorer removes its grid track instead of leaving an empty column.
- The note context track exists only while Notes is active and the context panel is open.
- The context-toggle control is shown only where a note context panel is available.
- Library content uses a centered responsive track that expands beyond the former narrow card cap without becoming edge-to-edge on wide displays.
- Existing tablet overlays and mobile bottom navigation retain their current behavior.

### R9 — Result-driven Library states

The Library shall communicate outcomes and availability rather than exposing implementation stages as a progress wizard.

Acceptance criteria:

- Empty, book-selected, indexing, ready, and error states each have a distinct customer-readable presentation.
- Indexing status appears in a compact status element while work continues in the background.
- Ready state foregrounds the book title, metadata, reading body, index, and chat composer.
- No numbered “Import”, “Book contents”, or “Talk with the book” cards remain.
- The layout remains usable at desktop, tablet, and mobile widths, including keyboard focus and reduced-motion behavior.
