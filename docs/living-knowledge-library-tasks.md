# Living Knowledge Library: Implementation Backlog

**Status:** Implemented through Task I4  
**Source specification:** [Living Knowledge Library design](living-knowledge-library-design.md)  
**Model specification:** [Agent Memory Model design](agent-memory-model-design.md)  
**Execution rule:** Complete tasks in dependency order. A task is not complete until its focused tests, `go vet ./...`, and `go build ./...` pass.

## 1. Delivery Strategy

The backlog is organized around observable outcomes rather than horizontal subsystem completion:

```text
Trust contracts
    ↓
Markdown book → stable edition → cited passage
    ↓
Personal/organization authorization
    ↓
Grounded question → verified answer → accepted memory
    ↓
Reading session and progress
    ↓
Role-based seminar
    ↓
EPUB → PDF → web
    ↓
Cross-book graph and living wiki
```

Evaluation fixtures are added with each feature, not deferred to the end. The public behavior remained behind a disabled-by-default `library` feature flag through the release drills; Task I4 enabled it by default while preserving an emergency-off switch.

## 2. Definition of Done

Every implementation task must:

- begin with a failing behavioral test
- preserve source, attribution, derivation, and authorization lineage
- touch no more than five files unless the task is split before implementation
- pass its focused test command, `go vet ./...`, and `go build ./...`
- update this backlog and the design specification if the contract changes

Repository-wide `go test ./...` is the release gate. The existing missing benchmark fixture used by `internal/api.TestRunScorer` must be restored or the test made hermetic before the first milestone release.

## 3. Phase A — Trust Foundation

### Task A1: Separate attribution from knowledge form ✅

**Status:** Completed

**Description:** Replace the overloaded `KnowledgeRole` concept with independent attribution, knowledge-form, and derivation contracts before any database schema depends on it.

**Acceptance criteria:**

- [x] A record separately identifies who is represented, what form the content takes, and how it was produced.
- [x] Invalid combinations such as a direct quote without a source attribution are rejected.
- [x] Existing memory types remain unchanged.

**Verification:** `go test ./internal/core -run 'Test(Knowledge|Attribution|Derivation)'`

**Dependencies:** None  
**Files:** `internal/core/knowledge.go`, `internal/core/knowledge_test.go`, `internal/readingroom/contracts.go`, `internal/readingroom/contracts_test.go`  
**Scope:** M

### Task A2: Define principals, ownership, and visibility ✅

**Status:** Completed

**Description:** Add user, agent, and organization principals plus resource ownership and visibility contracts usable by storage and retrieval.

**Acceptance criteria:**

- [x] Personal, organization, and private-to-user resources are representable.
- [x] Every protected resource has an owner and visibility policy.
- [x] Missing ownership fails closed.

**Verification:** `go test ./internal/core -run 'Test(Principal|Ownership|Visibility)'`

**Dependencies:** A1  
**Files:** `internal/core/authorization.go`, `internal/core/authorization_test.go`  
**Scope:** S

### Task A3: Define authorization-scoped access decisions ✅

**Status:** Completed

**Description:** Introduce an authorization scope and capability evaluation contract that candidate retrieval and graph traversal must receive explicitly.

**Acceptance criteria:**

- [x] Capabilities cover reading, searching, quoting, annotating, discussing, approving, managing, and exporting.
- [x] No scope or unknown capability produces a deny decision.
- [x] Decisions include the policy version needed for audit and cache isolation.

**Verification:** `go test ./internal/core -run 'TestAuthorization'`

**Dependencies:** A2  
**Files:** `internal/core/authorization.go`, `internal/core/authorization_test.go`  
**Scope:** S

### Task A4: Define source-retention and quotation policy ✅

**Status:** Completed

**Description:** Encode how original files, normalized text, passages, and short quotes may be stored and used.

**Acceptance criteria:**

- [x] Policies represent retained, on-demand, session-only, and deleted-source modes.
- [x] Search, quote, share, export, and delete permissions are independently expressible.
- [x] Quote-length enforcement is policy-driven rather than hard-coded globally.

**Verification:** `go test ./internal/core -run 'TestSourcePolicy'`

**Dependencies:** A2  
**Files:** `internal/core/source_policy.go`, `internal/core/source_policy_test.go`  
**Scope:** S

### Task A5: Replace generic locators with format-specific payloads ✅

**Status:** Completed

**Description:** Make locator validation depend on source format and define the coordinate space for offsets.

**Acceptance criteria:**

- [x] PDF requires a page; EPUB requires CFI or spine position; Markdown/text requires normalized offsets; web requires capture identity.
- [x] Parser and normalization versions accompany machine locations.
- [x] Display breadcrumbs cannot substitute for a machine-resolvable location.

**Verification:** `go test ./internal/core -run 'Test.*Locator'`

**Dependencies:** A1, A4  
**Files:** `internal/core/knowledge.go`, `internal/core/knowledge_test.go`  
**Scope:** S

### Task A6: Model claim-evidence verification records ✅

**Status:** Completed

**Description:** Replace self-asserted citation verification with immutable quote-match and entailment verdicts.

**Acceptance criteria:**

- [x] Verdicts distinguish `supports`, `partial`, `challenges`, `contradicts`, and `insufficient`.
- [x] Verification records identify evidence fingerprint, verifier, method, version, and timestamp.
- [x] Author claims require supporting verification; direct quotes require exact source-text verification.

**Verification:** `go test ./internal/core ./internal/readingroom -run 'Test.*Verif'`

**Dependencies:** A1, A5  
**Files:** `internal/core/verification.go`, `internal/core/verification_test.go`, `internal/readingroom/contracts.go`, `internal/readingroom/contracts_test.go`  
**Scope:** M

### Task A7: Enforce versioned agent profiles during contribution validation ✅

**Status:** Completed

**Description:** Make profile constraints mandatory instead of relying on callers to invoke `Accepts` separately.

**Acceptance criteria:**

- [x] Contribution validation requires a profile ID and version.
- [x] A critic contribution containing a final synthesis is rejected.
- [x] Unknown or changed profiles cannot validate cached contributions.

**Verification:** `go test ./internal/readingroom -run 'Test(Profile|Contribution)'`

**Dependencies:** A1, A6  
**Files:** `internal/readingroom/contracts.go`, `internal/readingroom/contracts_test.go`  
**Scope:** S

### Checkpoint A: Trust contracts ✅

- [x] Invalid attribution and evidence combinations fail closed.
- [x] Authorization scope is required at protected boundaries.
- [x] Quotes cannot become verified by setting a boolean.
- [x] `go test ./internal/core ./internal/readingroom` passes.

## 4. Phase B — Markdown Import-to-Citation Slice

This phase implements the first vertical slice of the [Whole-Book Ingestion and Study Pipeline](whole-book-ingestion-and-study.md).

### Task B1: Add work, edition, source-asset, and structural-node identities ✅

**Status:** Completed

**Description:** Define the minimum library domain needed to represent one imported Markdown book without persistence.

**Acceptance criteria:**

- [x] Work and edition identities are distinct.
- [x] Source assets include byte and normalized-text fingerprints.
- [x] Structural nodes form an ordered parent-child hierarchy.

**Verification:** `go test ./internal/library -run 'Test(Book|Edition|SourceAsset|StructuralNode)'`

**Dependencies:** Checkpoint A  
**Files:** `internal/library/types.go`, `internal/library/types_test.go`  
**Scope:** S

### Task B2: Persist library identities additively ✅

**Status:** Completed

**Description:** Add SQLite tables and repository methods for works, editions, assets, and structural nodes without changing existing memory behavior.

**Acceptance criteria:**

- [x] Opening an existing database creates new tables without modifying existing memory rows.
- [x] Stable IDs survive close and reopen.
- [x] Foreign keys prevent structures from referencing unknown editions.

**Verification:** `go test ./internal/storage/sqlite -run 'TestLibrary(Migration|Identity)'`

**Dependencies:** B1  
**Files:** `internal/storage/sqlite/store.go`, `internal/storage/sqlite/library.go`, `internal/storage/sqlite/library_test.go`  
**Scope:** M

### Task B3: Parse Markdown into normalized text and structure ✅

**Status:** Completed

**Description:** Implement the first source adapter with headings, normalized offsets, and source mappings.

**Acceptance criteria:**

- [x] Heading levels create a deterministic structural tree.
- [x] Every normalized passage maps back to source offsets.
- [x] Repeated headings and Unicode text preserve distinct, stable locations.

**Verification:** `go test ./internal/ingestion -run 'TestMarkdown'`

**Dependencies:** A5, B1  
**Files:** `internal/ingestion/adapter.go`, `internal/ingestion/markdown.go`, `internal/ingestion/markdown_test.go`, `internal/ingestion/testdata/book.md`  
**Scope:** M

### Task B4: Import one Markdown edition idempotently ✅

**Status:** Completed

**Description:** Connect fingerprinting, parsing, identity matching, and persistence into a resumable import operation.

**Acceptance criteria:**

- [x] Importing the same bytes twice returns the same edition and asset IDs.
- [x] Changed bytes create a new asset version without silently invalidating the old edition.
- [x] A failed import can resume without duplicate structural nodes.

**Verification:** `go test ./internal/library -run 'TestMarkdownImport'`

**Dependencies:** B2, B3  
**Files:** `internal/library/importer.go`, `internal/library/importer_test.go`, `internal/storage/sqlite/library.go`, `internal/ingestion/adapter.go`  
**Scope:** M

### Task B5: Resolve and persist Markdown citations ✅

**Status:** Completed

**Description:** Produce citations from imported passage locations and resolve them back to source evidence.

**Acceptance criteria:**

- [x] A citation resolves to the expected edition, heading, and normalized span.
- [x] A mismatched passage fingerprint is detected as stale rather than silently accepted.
- [x] Historical citations remain resolvable after a new asset version is imported.

**Verification:** `go test ./internal/library ./internal/storage/sqlite -run 'TestMarkdownCitation'`

**Dependencies:** B4  
**Files:** `internal/library/citations.go`, `internal/library/citations_test.go`, `internal/storage/sqlite/citations.go`, `internal/storage/sqlite/citations_test.go`  
**Scope:** M

### Checkpoint B: Import and citation ✅

- [x] A Markdown book imports twice without duplicate identity.
- [x] Its structure can be listed and citations resolve to exact evidence.
- [x] Existing memory storage tests remain green.

## 5. Phase C — Personal and Organization Security

### Task C1: Persist libraries, memberships, and resource policies ✅

**Status:** Completed

**Description:** Add personal and organization libraries with versioned membership and policy records.

**Acceptance criteria:**

- [x] A personal library has exactly one owning user.
- [x] Organization membership grants only explicit capabilities.
- [x] Membership removal invalidates new access decisions immediately.

**Verification:** `go test ./internal/storage/sqlite -run 'Test(LibraryMembership|ResourcePolicy)'`

**Dependencies:** A3, B2  
**Files:** `internal/library/access.go`, `internal/storage/sqlite/access.go`, `internal/storage/sqlite/access_test.go`, `internal/storage/sqlite/store.go`  
**Scope:** M

### Task C2: Enforce authorization in library repositories ✅

**Status:** Completed

**Description:** Require authorization scope for protected reads and writes at the repository boundary.

**Acceptance criteria:**

- [x] Missing or unauthorized scopes return indistinguishable not-found responses.
- [x] Authorized users can access only editions belonging to permitted libraries.
- [x] Repository count and list operations do not reveal unauthorized resources.

**Verification:** `go test ./internal/library ./internal/storage/sqlite -run 'TestAuthorizedLibrary'`

**Dependencies:** C1  
**Files:** `internal/library/repository.go`, `internal/library/repository_test.go`, `internal/storage/sqlite/library.go`, `internal/storage/sqlite/library_test.go`  
**Scope:** M

### Task C3: Store private annotations on shared editions ✅

**Status:** Completed

**Description:** Allow a user to annotate an organization-owned edition without exposing the annotation to other members.

**Acceptance criteria:**

- [x] Two users can cite the same edition while seeing only their own private annotations.
- [x] An annotation can be promoted to organization visibility only through an explicit operation.
- [x] Unauthorized annotation IDs do not reveal their existence.

**Verification:** `go test ./internal/library ./internal/storage/sqlite -run 'TestPrivateAnnotation'`

**Dependencies:** C2, B5  
**Files:** `internal/library/annotations.go`, `internal/library/annotations_test.go`, `internal/storage/sqlite/annotations.go`, `internal/storage/sqlite/annotations_test.go`  
**Scope:** M

### Checkpoint C: Shared-library privacy ✅

- [x] Personal and organization libraries use the same domain model.
- [x] Private notes remain private on shared source material.
- [x] Negative authorization tests cover reads, lists, counts, and annotations; citation access is enforced through source policy and protected repository scope.

## 6. Phase D — Grounded Question-to-Memory Slice

### Task D1: Build authorization-scoped lexical passage search ✅

**Description:** Search imported normalized passages while applying access constraints during candidate generation.

**Acceptance criteria:**

- [x] Search returns structurally diverse passages with citation IDs.
- [x] Unauthorized editions never enter the candidate set.
- [x] Search results are deterministic for a fixed index version and query.

**Verification:** `go test ./internal/retrieval -run 'TestAuthorizedLexicalSearch'`

**Dependencies:** B5, C2  
**Files:** `internal/retrieval/passages.go`, `internal/retrieval/passages_test.go`, `internal/storage/sqlite/passages.go`, `internal/storage/sqlite/passages_test.go`  
**Scope:** M

### Task D2: Create a grounded-answer contract ✅

**Description:** Define answer statements as individually attributed claims with evidence and verification state.

**Acceptance criteria:**

- [x] Every answer statement distinguishes author, reader, organization, and agent origin.
- [x] Unsupported statements are labeled interpretation or omitted according to policy.
- [x] Conflicting source evidence remains visible.

**Verification:** `go test ./internal/readingroom -run 'TestGroundedAnswer'`

**Dependencies:** A6, D1  
**Files:** `internal/readingroom/answer.go`, `internal/readingroom/answer_test.go`  
**Scope:** S

### Task D3: Implement the direct-study workflow ✅

**Description:** Run retrieval, scholar generation, verification, and answer assembly behind model-independent interfaces.

**Acceptance criteria:**

- [x] The workflow receives one immutable authorized evidence packet.
- [x] Unsupported author claims cannot enter the final answer as supported claims.
- [x] Cancellation and per-run budgets propagate through every stage.

**Verification:** `go test ./internal/readingroom -run 'TestDirectStudy'`

**Dependencies:** A7, D2  
**Files:** `internal/readingroom/direct_study.go`, `internal/readingroom/direct_study_test.go`, `internal/readingroom/runner.go`  
**Scope:** M

### Task D4: Persist proposed and accepted book memories ✅

**Description:** Connect grounded contributions to the existing memory lifecycle without putting source text into ordinary memories.

**Acceptance criteria:**

- [x] Suggested retention creates a proposal, not a durable semantic memory.
- [x] Acceptance creates a memory with attribution, citations, and derivation lineage.
- [x] Rejection remains auditable and does not create durable knowledge.

**Verification:** `go test ./internal/engine ./internal/storage/sqlite -run 'TestBookMemoryProposal'`

**Dependencies:** D3  
**Files:** `internal/engine/book_memory.go`, `internal/engine/book_memory_test.go`, `internal/storage/sqlite/book_memories.go`, `internal/storage/sqlite/book_memories_test.go`  
**Scope:** M

### Task D5: Expose import, structure, query, and memory-review APIs ✅

**Description:** Provide the first disabled-by-default HTTP path for the complete Markdown workflow.

**Acceptance criteria:**

- [x] An authorized client can import, inspect structure, query, and accept a proposed memory.
- [x] Long-running import returns a job identity and observable state.
- [x] Existing API behavior is unchanged when the feature flag is disabled.

**Verification:** `go test ./internal/api -run 'TestLibrary(Import|Query|MemoryReview)'`

**Dependencies:** D4  
**Files:** `internal/api/library_handlers.go`, `internal/api/library_handlers_test.go`, `internal/api/server.go`, `internal/api/dto.go`, `internal/engine/runtime_flags.go`  
**Scope:** M

### Checkpoint D: First user outcome ✅

- [x] Import Markdown → ask question → inspect citation → accept memory works end to end.
- [x] The same flow works in a personal or authorized organization library.
- [x] Unauthorized and unanswerable questions have regression tests.

## 7. Phase E — Reading Sessions and Progress

### Task E1: Persist study sessions and attributed turns ✅

**Description:** Store human and agent turns as episodic learning events with library scope and retention state.

**Acceptance criteria:**

- [x] Every turn identifies its principal, session, scope, and evidence packet when applicable.
- [x] Raw turns do not become semantic memory automatically.
- [x] Private sessions remain inaccessible to organization peers.

**Verification:** `go test ./internal/readingroom ./internal/storage/sqlite -run 'TestStudySession'`

**Dependencies:** C3, D3  
**Files:** `internal/readingroom/session.go`, `internal/readingroom/session_test.go`, `internal/storage/sqlite/study_sessions.go`, `internal/storage/sqlite/study_sessions_test.go`  
**Scope:** M

### Task E2: Track edition-specific reading progress ✅

**Description:** Record reading position and learning state without conflating scrolling with study mastery.

**Acceptance criteria:**

- [x] Progress distinguishes seen, studied, mastered, and completed states.
- [x] Position references a resolvable edition locator.
- [x] A new edition does not silently inherit progress from an old edition.

**Verification:** `go test ./internal/library ./internal/storage/sqlite -run 'TestReadingProgress'`

**Dependencies:** B5, C2  
**Files:** `internal/library/progress.go`, `internal/library/progress_test.go`, `internal/storage/sqlite/progress.go`, `internal/storage/sqlite/progress_test.go`  
**Scope:** M

### Task E3: Resume a study session with scoped recall ✅

**Description:** Assemble prior questions, accepted insights, open questions, and progress into a bounded session-resumption context.

**Acceptance criteria:**

- [x] Recall includes only authorized session and library records.
- [x] Accepted knowledge and raw conversation are labeled separately.
- [x] Token clipping preserves citations for every retained claim.

**Verification:** `go test ./internal/readingroom -run 'TestSessionResume'`

**Dependencies:** E1, E2, D4  
**Files:** `internal/readingroom/resume.go`, `internal/readingroom/resume_test.go`, `internal/engine/token_clipper.go`, `internal/engine/token_clipper_test.go`  
**Scope:** M

### Checkpoint E: Continuous study ✅

- [x] A reader can stop and resume a book study session.
- [x] Progress, conversation, and accepted knowledge remain distinct.
- [x] Session recall cannot expose another user's notes or turns.

## 8. Phase F — Multi-Agent Seminar

### Task F1: Canonicalize immutable evidence packets ✅

**Description:** Define deterministic packet fingerprints over authorized evidence, question, profile versions, and retrieval versions.

**Acceptance criteria:**

- [x] Equivalent packets produce the same fingerprint regardless of map ordering.
- [x] Authorization, evidence, or profile changes produce a different fingerprint.
- [x] Volatile timestamps do not affect semantic identity.

**Verification:** `go test ./internal/readingroom -run 'TestEvidencePacket'`

**Dependencies:** A7, D1  
**Files:** `internal/readingroom/evidence_packet.go`, `internal/readingroom/evidence_packet_test.go`  
**Scope:** S

### Task F2: Add model-independent role execution ✅

**Description:** Introduce a role runner interface and validated run result envelope without selecting a hosted provider.

**Acceptance criteria:**

- [x] Runner input contains the profile and evidence-packet fingerprints.
- [x] Output is validated before the coordinator can consume it.
- [x] Timeout, cancellation, token usage, and model metadata are recorded.

**Verification:** `go test ./internal/readingroom -run 'TestRoleRunner'`

**Dependencies:** F1  
**Files:** `internal/readingroom/runner.go`, `internal/readingroom/runner_test.go`  
**Scope:** S

### Task F3: Execute a bounded workflow dependency graph ✅

**Description:** Add deterministic workflow nodes and transitions with concurrent execution only for independent roles.

**Acceptance criteria:**

- [x] Dependencies, maximum fan-out, retry count, and budgets are validated before execution.
- [x] Cancellation stops pending and active nodes.
- [x] Model output cannot create workflow nodes or privileged transitions.

**Verification:** `go test ./internal/readingroom -run 'TestWorkflow'`

**Dependencies:** F2  
**Files:** `internal/readingroom/workflow.go`, `internal/readingroom/workflow_test.go`  
**Scope:** S

### Task F4: Gate contributions through citation and entailment verification ✅

**Description:** Run exact quote checks and claim-evidence verdicts before contributions reach synthesis.

**Acceptance criteria:**

- [x] Failed contributions are excluded or returned for bounded revision.
- [x] Verification uses source evidence rather than contribution-provided quote fields.
- [x] Partial and contradictory verdicts remain available to synthesis.

**Verification:** `go test ./internal/readingroom -run 'TestVerifierGate'`

**Dependencies:** A6, F3  
**Files:** `internal/readingroom/verifier.go`, `internal/readingroom/verifier_test.go`, `internal/library/citations.go`  
**Scope:** M

### Task F5: Synthesize without collapsing disagreement ✅

**Description:** Produce a derivation-complete synthesis from verified role contributions.

**Acceptance criteria:**

- [x] Every synthesis statement references its input contributions.
- [x] Contradictions and unresolved questions are preserved explicitly.
- [x] Agreement can adjust confidence but cannot change attribution.

**Verification:** `go test ./internal/readingroom -run 'TestSynthesis'`

**Dependencies:** F4  
**Files:** `internal/readingroom/synthesis.go`, `internal/readingroom/synthesis_test.go`  
**Scope:** S

### Task F6: Ship the seminar workflow template ✅

**Description:** Compose librarian, scholar, critic, connector, questioner, verifier, and synthesizer into the first configurable seminar.

**Acceptance criteria:**

- [x] Independent analysis roles run concurrently against the same packet.
- [x] The verifier gates synthesis and retention.
- [x] Partial failure returns a labeled partial answer instead of losing completed work.

**Verification:** `go test ./internal/readingroom -run 'TestSeminar'`

**Dependencies:** F5  
**Files:** `internal/readingroom/seminar.go`, `internal/readingroom/seminar_test.go`, `internal/readingroom/profiles.go`  
**Scope:** M

### Task F7: Expose seminar progress and cancellation ✅

**Description:** Add API endpoints for starting, observing, and cancelling a seminar run.

**Acceptance criteria:**

- [x] Progress events expose role status without restricted source text.
- [x] Cancellation is authorized and idempotent.
- [x] Stored contributions remain canonical if streaming disconnects.

**Verification:** `go test ./internal/api -run 'TestSeminar'`

**Dependencies:** F6, E1  
**Files:** `internal/api/seminar_handlers.go`, `internal/api/seminar_handlers_test.go`, `internal/api/server.go`, `internal/readingroom/seminar.go`  
**Scope:** M

### Checkpoint F: Multi-agent reliability ✅

- [x] Unsupported author claims and inaccurate quotes cannot reach synthesis.
- [x] Criticism and disagreement survive synthesis.
- [x] Role execution is bounded, cancellable, attributable, and provider-independent.

## 9. Phase G — Additional Formats

### Task G1: Import and cite EPUB editions ✅

**Description:** Add EPUB metadata, spine, navigation, XHTML extraction, and CFI-capable citation resolution.

**Acceptance criteria:**

- [x] EPUB structure and reading order match fixture expectations.
- [x] Citations resolve after close/reopen and identical re-import.
- [x] Malformed containers fail without publishing partial editions.

**Verification:** `go test ./internal/ingestion ./internal/library -run 'TestEPUB'`

**Dependencies:** Checkpoint D  
**Files:** `internal/ingestion/epub.go`, `internal/ingestion/epub_test.go`, `internal/ingestion/testdata/book.epub`, `internal/library/importer.go`  
**Scope:** M

### Task G2: Import and cite native-text PDFs ✅

**Description:** Add page-aware PDF extraction behind an adapter boundary selected through fixture evaluation.

**Acceptance criteria:**

- [x] Text retains page and block coordinates needed for citations.
- [x] Headers, footers, and multi-column fixtures preserve readable order.
- [x] Scanned pages are marked as requiring OCR rather than treated as empty truth.

**Verification:** `go test ./internal/ingestion ./internal/library -run 'TestPDF'`

**Dependencies:** Checkpoint D, approved PDF dependency  
**Files:** `internal/ingestion/pdf.go`, `internal/ingestion/pdf_test.go`, `internal/ingestion/testdata/book.pdf`, `internal/library/importer.go`  
**Scope:** M

### Task G3: Add explicit PDF OCR fallback ✅

**Description:** Process scanned pages through a configurable OCR boundary and record lower-confidence provenance.

**Acceptance criteria:**

- [x] OCR output is labeled with provider/version and confidence.
- [x] Quotes cannot be auto-verified below the configured OCR threshold.
- [x] Native and OCR text never silently overwrite one another.

**Verification:** `go test ./internal/ingestion -run 'TestPDFOCR'`

**Dependencies:** G2, approved OCR provider  
**Files:** `internal/ingestion/ocr.go`, `internal/ingestion/ocr_test.go`, `internal/ingestion/pdf.go`  
**Scope:** M

### Task G4: Capture and cite versioned web books ✅

**Description:** Store captured web source identity so citations refer to a historical version rather than a mutable URL.

**Acceptance criteria:**

- [x] Every web citation contains capture ID, canonical URL, selector, and content fingerprint.
- [x] Recapture with changed content creates a new source version.
- [x] Robots, access, and source-retention policy failures prevent publication.

**Verification:** `go test ./internal/ingestion ./internal/library -run 'TestWebCapture'`

**Dependencies:** Checkpoint D  
**Files:** `internal/ingestion/web.go`, `internal/ingestion/web_test.go`, `internal/library/importer.go`, `internal/library/citations.go`  
**Scope:** M

### Checkpoint G: Format parity ✅

- [x] Markdown, EPUB, PDF, and web fixtures pass the same identity, citation, authorization, and deletion contract.
- [x] Every format has re-import and malformed-source regression tests.
- [x] Parser dependencies and versions are documented.

## 10. Phase H — Graph, Wiki, and Governance

### Task H1: Persist provenance-rich claim and concept edges ✅

**Description:** Add graph records whose relationships are reviewable claims rather than unqualified facts.

**Acceptance criteria:**

- [x] Every inferred edge carries evidence, creator, confidence, review state, and timestamps.
- [x] Conflicting edges coexist.
- [x] Authorization is enforced during every traversal expansion.

**Verification:** `go test ./internal/engine ./internal/storage/sqlite -run 'TestKnowledgeGraph'`

**Dependencies:** D4, C2  
**Files:** `internal/core/knowledge_graph.go`, `internal/engine/knowledge_graph.go`, `internal/engine/knowledge_graph_test.go`, `internal/storage/sqlite/knowledge_graph.go`, `internal/storage/sqlite/knowledge_graph_test.go`  
**Scope:** M

### Task H2: Compare claims across selected books ✅

**Description:** Add a retrieval plan that requires balanced evidence from every selected edition.

**Acceptance criteria:**

- [x] Comparison output identifies each book's claims separately before synthesis.
- [x] Missing evidence for one book is reported rather than filled from another.
- [x] Private and unauthorized sources cannot influence comparison.

**Verification:** `go test ./internal/retrieval ./internal/readingroom -run 'TestCrossBookComparison'`

**Dependencies:** H1, D3  
**Files:** `internal/retrieval/comparison.go`, `internal/retrieval/comparison_test.go`, `internal/readingroom/comparison.go`, `internal/readingroom/comparison_test.go`  
**Scope:** M

### Task H3: Generate evidence-expandable wiki projections ✅

**Description:** Render book, chapter, and concept pages from authorized knowledge records without creating another source of truth.

**Acceptance criteria:**

- [x] Every generated statement exposes attribution, evidence, derivation, and review state.
- [x] Regeneration reflects corrections without mutating historical knowledge records.
- [x] Projection caching is separated by authorization-policy fingerprint.

**Verification:** `go test ./internal/api -run 'TestWikiProjection'`

**Dependencies:** H1, H2  
**Files:** `internal/api/wiki_projection.go`, `internal/api/wiki_projection_test.go`, `internal/api/server.go`  
**Scope:** M

### Task H4: Add organization knowledge review and audit history ✅

**Description:** Promote proposed knowledge through review without modifying personal notes or losing prior versions.

**Acceptance criteria:**

- [x] State transitions allow proposed, reviewed, approved, rejected, and superseded.
- [x] Only authorized curators can approve or supersede organization knowledge.
- [x] Every transition stores actor, reason, time, and prior state.

**Verification:** `go test ./internal/library ./internal/storage/sqlite -run 'TestKnowledgeReview'`

**Dependencies:** C1, D4  
**Files:** `internal/library/review.go`, `internal/library/review_test.go`, `internal/storage/sqlite/review.go`, `internal/storage/sqlite/review_test.go`  
**Scope:** M

### Task H5: Reconsolidate without destroying provenance ✅

**Description:** Extend memory consolidation so book-derived knowledge can be clarified or superseded while retaining all source and derivation links.

**Acceptance criteria:**

- [x] Consolidation creates a new version or lineage edge instead of overwriting provenance.
- [x] Contradictions are not merged into false consensus.
- [x] Historical citations remain queryable after supersession.

**Verification:** `go test ./internal/engine -run 'TestBookReconsolidation'`

**Dependencies:** H1, H4  
**Files:** `internal/engine/book_consolidation.go`, `internal/engine/book_consolidation_test.go`, `internal/engine/consolidation.go`, `internal/engine/deep_consolidation.go`  
**Scope:** M

### Checkpoint H: Living organizational knowledge ✅

- [x] Cross-book comparisons remain attributable and balanced.
- [x] Wiki pages are authorization-safe projections.
- [x] Organization review, correction, and reconsolidation retain complete history.

## 11. Phase I — Release Readiness

### Task I1: Create the cross-format evaluation corpus ✅

**Description:** Assemble licensed or synthetic fixtures with known-answer, conflicting-answer, and unanswerable questions.

**Acceptance criteria:**

- [x] Every supported format includes citation and re-import expectations.
- [x] Evaluation includes attribution, contradiction, and unanswerable cases.
- [x] Fixtures have documented redistribution rights.

**Verification:** `go test ./evaluation/library -run 'TestCorpus'`

**Dependencies:** Checkpoint G  
**Files:** `evaluation/library/corpus_test.go`, `evaluation/library/README.md`, `evaluation/library/testdata/manifest.json`  
**Scope:** M

### Task I2: Add authorization-leak regression suite ✅

**Description:** Test content, existence, ranking, graph, cache, count, timing-class, and streaming boundaries across principals.

**Acceptance criteria:**

- [x] Unauthorized sources and notes never appear in results or metadata.
- [x] Cache keys include authorization-policy fingerprints.
- [x] Graph traversal and wiki projections pass negative multi-user cases.

**Verification:** `go test ./internal/... -run 'TestAuthorizationLeak'`

**Dependencies:** Checkpoint H  
**Files:** `internal/api/library_security_test.go`, `internal/retrieval/security_test.go`, `internal/engine/graph_security_test.go`, `internal/readingroom/security_test.go`  
**Scope:** M

### Task I3: Add quality, cost, and latency release gates ✅

**Description:** Measure citation precision, quote match, entailment, attribution, recall utility, role revisions, latency, and token cost.

**Acceptance criteria:**

- [x] Metric definitions and minimum thresholds are versioned.
- [x] Regressions fail CI with per-format and per-workflow diagnostics.
- [x] Restricted source text is excluded from default traces and reports.

**Verification:** `go test ./evaluation/library ./internal/observability -run 'TestLibraryMetrics'`

**Dependencies:** I1, I2, F7  
**Files:** `evaluation/library/metrics.go`, `evaluation/library/metrics_test.go`, `internal/observability/library.go`, `internal/observability/library_test.go`  
**Scope:** M

### Task I4: Enable the feature flag after migration and recovery drills ✅

**Description:** Validate upgrades, rollbacks, interrupted ingestion, parser upgrades, deletion, and index rebuild before enabling library functionality by default.

**Acceptance criteria:**

- [x] Existing workspaces upgrade without memory changes.
- [x] Interrupted jobs resume and rebuildable indexes can be recreated.
- [x] Source deletion behavior matches every retention mode.

**Verification:** `go test ./internal/... -run 'TestLibrary(Migration|Recovery|Deletion)' && go test ./...`

**Dependencies:** I3, restored hermetic repository-wide test suite  
**Files:** `internal/storage/sqlite/library_migration_test.go`, `internal/library/recovery_test.go`, `internal/engine/runtime_flags.go`, `docs/living-knowledge-library-operations.md`  
**Scope:** M

## 12. Parallelization Rules

Safe after contracts are frozen:

- B3 Markdown parsing can proceed alongside B2 persistence.
- E1 sessions and E2 progress can proceed independently.
- G1 EPUB and G4 web adapters can proceed independently.
- H3 wiki projection and H4 review workflow can proceed after their distinct dependencies.
- Evaluation fixtures should be authored alongside, but not in the same commits as, adapters.

Must remain sequential:

- A1–A7 trust contracts
- schema migrations affecting the same SQLite store
- B4 import before B5 citation resolution
- F1–F6 orchestration dependency chain
- authorization contracts before any protected retrieval or projection

## 13. Decisions Required Before Specific Tasks

These do not block Phase A:

- Quote-length policy defaults before A4 is finalized
- PDF parser dependency before G2
- OCR provider and confidence policy before G3
- Web capture networking and robots policy before G4
- Hosted model providers before production implementations of `RoleRunner`
- Organization identity provider before external membership administration; local principal contracts are sufficient for C1
