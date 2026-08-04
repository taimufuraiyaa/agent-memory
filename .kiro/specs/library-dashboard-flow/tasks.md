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
