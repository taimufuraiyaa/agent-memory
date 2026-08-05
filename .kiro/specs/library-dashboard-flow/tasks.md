# Library Dashboard Flow Tasks

## Task L1 — Define and test dashboard contracts

- [x] Add a failing dashboard contract test for the Library destination.
- [x] Add failing assertions for import, structure, query, and memory-review API methods.
- [x] Add typed client request/response contracts for the existing backend endpoints.
- [x] Verify dashboard tests and typecheck.

## Task L2 — Implement import and indexed-contents flow

- [x] Add a dedicated Library workspace component.
- [x] Collect portable reader/library scope at runtime.
- [x] Support whole Markdown/plain-text file selection and pasted source.
- [x] Display import identity, passage counts, and ordered structural nodes.
- [x] Cover the surface with dashboard contract tests.
- [x] Verify dashboard tests, typecheck, and production build.

## Task L3 — Implement grounded conversation and memory review

- [x] Add remembered-quote/question conversation input.
- [x] Display source evidence, locators, and explicit unanswerable state.
- [x] Keep reader interpretation visually distinct from source evidence.
- [x] Add opt-in suggested-memory creation and accept/reject actions.
- [x] Cover conversation and review with dashboard contract tests.
- [x] Verify dashboard tests, typecheck, and production build.

## Task L4 — Embed and release-gate

- [x] Rebuild embedded dashboard assets from tested source.
- [x] Run embedded-asset tests.
- [x] Run repository-wide Go tests.
- [x] Run the developer-specific-path regression test.
- [x] Confirm no unresolved merge state or uncommitted files remain.

## Task L5 — Present responsive sidebar destination icons

- [x] Add failing contract coverage for icon-only collapsed navigation and icon-plus-name expanded navigation.
- [x] Replace letter abbreviations with recognizable, accessible destination icons.
- [x] Bind the rail's expanded presentation to the existing explorer-open state.
- [ ] Verify dashboard tests, typecheck, production build, and embedded assets.

## Task L6 — Remove phantom notebook tracks and widen Library

- [x] Add failing structural coverage for mounted-panel grid classes, Notes-only context controls, and the responsive Library content track.
- [x] Make explorer and context grid tracks conditional without changing tablet or mobile overlays.
- [x] Center and widen the Library content track so wide screens do not show a dead right-side strip.
- [x] Synchronize embedded assets and verify dashboard tests, CSS/bundle checks, and the production binary.

## Task L7 — Replace the Library pipeline with a reading room

- [x] Add failing structural coverage for the import-first empty state, editable preparation surface, background indexing state, book reader, right-side index, and bottom chat composer.
- [x] Replace the numbered workflow cards with explicit empty, preparing, indexing, ready, and error presentations.
- [x] Keep file selection separate from confirmation and preserve supported formats, editable title, edition, language, and scope.
- [x] Present grounded evidence as a conversation inside the reading body and progressively disclose interpretation and memory review.
- [x] Add responsive reading-room styling with a right-side desktop index and a stacked narrow-screen layout.
- [x] Synchronize embedded assets and verify dashboard tests, bundle syntax, asset tests, and the production binary.

## Task L8 — Create a removable note for each imported book

- [x] Add failing dashboard coverage for the post-import note callback, `Library/` path allocation, explorer refresh, Open note action, and standard Trash reuse.
- [x] Add a typed successful-import callback from `LibraryWorkspace` to `NotebookWorkspace`.
- [x] Create a readable note with traceability properties after import and structure loading succeed.
- [x] Refresh the explorer and let the reader open the generated note without automatically leaving Library.
- [x] Preserve the ready book state when secondary note creation fails and report a note-specific error.
- [x] Synchronize embedded assets and verify dashboard tests, typecheck, asset tests, and the production binary.
