# Living Knowledge Library: Repository Design Specification

**Status:** Approved direction; implementation started  
**Audience:** Individual readers and organizations  
**Formats:** EPUB, PDF, Markdown/plain text, and captured web books  
**Content policy:** Original sources remain policy-controlled; durable memory stores only short verified quotes, citations, summaries, and authored notes

The provider-independent reasoning and training layer is specified in [Agent Memory Model: System and Training Design](agent-memory-model-design.md).

## 1. Objective

Extend Agent Memory from a code-project memory engine into a provenance-aware library and wiki system. An agent must be able to ingest a whole book, answer arbitrary grounded questions, run individual or multi-agent study sessions, retain useful learning, compare ideas across books and projects, and always distinguish source statements from reader or agent interpretation.

Success means:

- Every quote and author claim is traceable to an edition-specific location.
- A reader can query a chapter, book, selected collection, personal library, or authorized organizational library.
- Conversations are retained as learning events without automatically becoming accepted knowledge.
- Multiple agents can specialize without losing attribution or manufacturing consensus.
- Re-importing or re-indexing a source preserves stable identity and historical citations.
- Access control is applied to evidence before retrieval ranking and answer generation.

## 2. Architectural Decisions

### 2.1 One system, three storage planes

| Plane | Purpose | Authority | Rebuildable |
|---|---|---|---|
| Source vault | Original files, captured web versions, normalized text, extraction artifacts | Evidence | Partly |
| Retrieval index | Chunks, lexical index, embeddings, structural lookup | Search acceleration | Yes |
| Memory and graph | Short quotes, summaries, claims, notes, conversations, derivations, relationships | Durable learned knowledge | No |

Original book text is not an ordinary memory. Retrieval may use vault passages transiently, subject to source policy, while durable memory retains only permitted excerpts and derived knowledge.

### 2.2 Preserve existing memory types

`MemoryType` continues to describe how knowledge behaves:

- `episodic`: conversation turns and study events
- `semantic`: claims, definitions, summaries, and insights
- `procedural`: methods and practices learned from sources
- `outcome`: results of applying knowledge

Three independent dimensions preserve epistemic provenance:

- `AttributionKind` identifies whose position is represented: author, reader, agent, organization, or external source.
- `KnowledgeForm` identifies what the record contains: claim, quote, summary, note, question, explanation, synthesis, definition, recollection, or insight.
- `DerivationKind` identifies how it was produced: extracted, interpreted, discussed, consolidated, applied, or recalled.

For example, an author claim can be `author + claim + extracted`, while an agent explanation can be `agent + explanation + interpreted`. This prevents representation, origin, and transformation from being collapsed into one label.

### 2.3 Work identity differs from edition identity

A `BookWork` identifies the abstract work. A `BookEdition` identifies a particular publication and content fingerprint. Citations always target an edition and structural location. Cross-book concepts may target the work.

### 2.4 The wiki is a projection

Wiki pages are generated views over memories, citations, graph edges, source metadata, and review state. They do not become an independent source of truth. Every sentence can expose origin, evidence, confidence, opposing evidence, derivation, and review state.

## 3. Domain Model

```text
Principal (user | agent | organization)
  └── Library
      ├── Membership + Policy
      ├── Collection
      └── BookWork
          └── BookEdition
              ├── SourceAsset
              ├── StructuralNode (part | chapter | section)
              └── SourceLocator

StudySession
  ├── Participants + AgentProfiles
  ├── ConversationTurns
  ├── EvidencePackets
  ├── RoleContributions
  └── ProposedMemories

KnowledgeRecord
  ├── MemoryType
  ├── Attribution
  ├── KnowledgeForm
  ├── Derivation
  ├── Citations
  └── ReviewState
```

### 3.1 Source identity

`SourceAsset` records format, media type, byte fingerprint, normalized-text fingerprint, acquisition time, capture URL when relevant, parser version, and retention policy. Identity matching should use bibliographic identifiers when present, then fingerprints and normalized metadata. It must never use filename alone.

### 3.2 Citations and locators

A citation contains:

- edition ID and source asset ID
- structural node ID and human-readable breadcrumb
- format-specific locator: PDF page plus text offsets; EPUB CFI or spine/offset; Markdown heading and byte/line offsets; web capture ID and selector/offset
- normalized passage fingerprint
- optional permitted short quote
- verification status and verifier version

Locators are dual: a stable machine locator plus a display locator. If re-imported content moves, fingerprint-based relocation can create a new locator while retaining the historical locator.

### 3.3 Knowledge and graph provenance

Every knowledge record and inferred graph edge carries creator, creation time, confidence, review state, supporting citation IDs, and derivation IDs. Contradictory claims coexist. Consolidation may create a synthesis but never delete attribution or lineage.

## 4. Ingestion Architecture

The detailed extraction, structure, indexing, citation, verification, hierarchical-study, publication, and remembered-quote flows are specified in [Whole-Book Ingestion and Study Pipeline](whole-book-ingestion-and-study.md).

Adapters implement a common staged contract:

```go
type SourceAdapter interface {
    Probe(ctx context.Context, asset SourceAsset) (ProbeResult, error)
    Extract(ctx context.Context, asset SourceAsset) (ExtractedDocument, error)
}
```

Initial adapters:

- EPUB: package metadata, spine, navigation, XHTML text, EPUB CFI-compatible locations
- PDF: text blocks, page coordinates, outline, OCR fallback marker
- Markdown/plain text: heading tree, lines, byte offsets
- Web: captured content, canonical URL, capture timestamp, content hash, DOM-aware locations

The ingestion pipeline is resumable and idempotent:

1. acquire and fingerprint
2. identify work and edition
3. extract normalized text with source mapping
4. detect hierarchy
5. chunk at structural and semantic boundaries
6. build lexical and embedding indexes
7. extract provisional claims, concepts, entities, definitions, questions, and short quotes
8. create section, chapter, part, and book summaries
9. resolve concepts against authorized existing knowledge
10. propose graph edges
11. verify citations and attribution
12. publish searchable edition; queue uncertain artifacts for review

Each stage stores its version and status. Failed stages can retry without duplicating durable records. Parser or embedding upgrades rebuild derived artifacts while stable source, annotation, citation, and memory identities remain intact.

## 5. Retrieval and Answering

Questions first become a `RetrievalPlan` containing intent, authorized scopes, evidence kinds, recency needs, and token budgets. Retrieval then combines:

1. memory candidates
2. graph traversal
3. lexical and semantic source-passage candidates
4. authorization filtering
5. evidence reranking and diversity selection
6. answer generation
7. citation and quote verification

Intent changes ranking:

| Question | Preferred evidence |
|---|---|
| What does the author claim? | verified passages and author-claim records |
| What did I think? | private reader notes and study sessions |
| Compare these books | concept graph plus evidence from every edition |
| How does this help the project? | book knowledge joined with project memory |
| Quiz me | claims, concepts, questions, and observed learning gaps |
| Where did this come from? | citation and derivation lineage |

Answers label author statements, quotations, reader views, and agent interpretations. Missing or conflicting evidence is surfaced rather than smoothed away.

## 6. Multi-Agent Reading Room

### 6.1 Why roles are protocol participants

Roles are configurable `AgentProfile` data, not hard-coded services. A model can fill one or several roles, and several model providers can participate. Reliability comes from typed inputs, typed outputs, provenance checks, and workflow policy—not from giving agents names.

Default roles:

| Role | Mandate | Must not do |
|---|---|---|
| Librarian | Select relevant sources and passages | Interpret beyond evidence |
| Summarizer/Scholar | Faithfully state the source position | Present criticism as source content |
| Critic | Test assumptions and identify counterarguments | Rewrite the author's position into a straw man |
| Questioner | Produce comprehension, Socratic, and reflection questions | Assert answers without evidence |
| Domain expert | Connect claims to domain knowledge with explicit external attribution | Attribute domain knowledge to the book |
| Connector | Find cross-book and project relationships | Treat similarity as support |
| Synthesizer | Reconcile contributions and propose retained knowledge | erase disagreement or provenance |
| Citation verifier | Check entailment, quote text, and locations | invent or repair unsupported claims silently |

### 6.2 Evidence packet

Every role run receives an immutable packet:

- session, user, organization, and authorized scope IDs
- the user's question and desired study mode
- bounded source passages with citation IDs
- authorized memory and graph context
- role instructions and output schema
- explicit separation of source evidence and prior interpretations

The packet is fingerprinted. Contributions record that fingerprint, model/provider, prompt/profile version, and timestamps, so a result can be reproduced or audited.

### 6.3 Typed contribution

A role does not return an unstructured answer alone. It returns contributions containing:

- kind: claim, summary, critique, question, connection, synthesis, or proposed memory
- statement and epistemic origin
- citation IDs and derivation contribution IDs
- confidence and uncertainty
- stance: supports, challenges, contradicts, elaborates, or neutral
- retention recommendation

An `author_claim` or `direct_quote` contribution is invalid without citations. A quote additionally requires exact text verification. A synthesis must reference the contributions it derives from.

### 6.4 Orchestration patterns

The coordinator runs a dependency graph rather than an uncontrolled conversation:

```text
Question
   ↓
Librarian → immutable evidence packet
   ↓
Summarizer ─┬─ Critic
            ├─ Questioner
            ├─ Domain expert
            └─ Connector
                    ↓
             Citation verifier
                    ↓
               Synthesizer
                    ↓
        Answer + proposed memories
                    ↓
          retention policy/review
```

Independent roles can run concurrently after evidence selection. The verifier gates the synthesizer. A failed contribution is returned for revision or excluded; it is never silently promoted.

Supported workflow templates:

- direct study: librarian → scholar → verifier
- seminar: scholar + critic + connector + questioner → verifier → synthesizer
- adversarial debate: advocate + critic → cross-examination → verifier → synthesizer
- cross-book synthesis: librarian per book → scholars → connector → verifier → synthesizer
- assessment: questioner → learner response → evaluator → learning-gap memories

### 6.5 Retention policy

Raw turns and role contributions are episodic records. Durable semantic or procedural memory is created only by policy:

- `automatic`: accept contributions passing configured confidence, verification, and policy thresholds
- `suggested`: present proposed memories for human edit/acceptance
- `manual`: retain no durable knowledge without an explicit save

Organization policy may require curator review. Consensus affects confidence but never changes origin. Five agents agreeing on an interpretation does not make it an author claim.

### 6.6 Failure handling and cost control

- Per-role time, token, and model budgets
- Maximum revision count and fan-out
- Cancellation propagated through the workflow
- Partial results retained with explicit status
- Deterministic workflow transitions; model output is data, not control flow
- Idempotency key per session, role, packet, and profile version
- Cheap models for extraction/question generation; stronger models for synthesis only when policy permits
- Cache evidence packets and verified contributions by fingerprint

## 7. Permissions and Organizational Governance

Authorization is evaluated independently for source assets, retrieval passages, citations, memories, notes, conversations, and generated wiki projections. Required capabilities include read source, search source, quote source, annotate, discuss, propose knowledge, approve knowledge, manage collection, and export.

Personal notes remain private even when attached to an organization-owned book. Organization knowledge progresses through `proposed`, `reviewed`, `approved`, `rejected`, and `superseded` states with audit history. Retrieval must filter unauthorized objects before scoring to prevent existence and ranking leaks.

## 8. API Surface

Versioned endpoints should evolve in vertical slices:

```text
POST   /api/v1/libraries
POST   /api/v1/libraries/{id}/sources
GET    /api/v1/ingestions/{id}
GET    /api/v1/books/{id}
GET    /api/v1/editions/{id}/structure
POST   /api/v1/library-query

POST   /api/v1/study-sessions
POST   /api/v1/study-sessions/{id}/turns
POST   /api/v1/study-sessions/{id}/runs
GET    /api/v1/study-sessions/{id}/contributions
POST   /api/v1/contributions/{id}/review
POST   /api/v1/proposed-memories/{id}/accept
```

Long-running ingestion and seminars return job IDs and expose state transitions. Streaming may deliver role progress, but stored contributions remain the canonical result.

## 9. UI Surfaces

- Library: personal/organization switcher, collections, ingestion, processing status
- Book: edition metadata, contents, reading progress, search, annotations
- Reading room: mode and roles, scoped conversation, evidence drawer, agent contribution timeline
- Memory review: proposed knowledge, attribution, citations, edit/accept/reject
- Graph: claims and concepts with confidence and evidence filters
- Wiki: book, chapter, concept, comparison, open-question, and learning-path projections
- Governance: permissions, retention/licensing policy, curator queue, audit history

The reading-room answer UI must make evidence expandable and visually distinguish author, reader, and agent statements.

## 10. Evaluation and Observability

Evaluation sets must include known-answer and unanswerable questions across all formats and editions. Metrics:

- citation precision and locator resolution rate
- quote exact-match rate
- claim entailment and attribution accuracy
- answer faithfulness, completeness, and cross-source balance
- authorization leakage rate (target: zero)
- duplicate/stale identity rate after re-import
- graph edge acceptance and contradiction preservation
- useful-memory acceptance and later retrieval usefulness
- role contribution rejection/revision rates
- latency, token cost, and cache effectiveness per pipeline stage and role

Trace IDs connect question, retrieval plan, evidence packet, contributions, verification, final answer, and retained memories without logging restricted source text by default.

## 11. Migration Strategy

1. Add optional book provenance beside existing memory records; old memories remain valid with origin `unspecified`.
2. Add library/source/edition/citation tables without changing current write and recall behavior.
3. Backfill code/document sources only when identity can be established safely.
4. Introduce book-aware endpoints and retrieval behind a disabled-by-default feature flag.
5. Move graph edges to provenance-rich records while continuing to read legacy memory relations.
6. Generate wiki projections from both legacy and new records, clearly marking records without citations.

No migration fabricates citations for existing memories.

## 12. Commands, Structure, and Engineering Boundaries

Current commands:

```bash
go test ./...
go test ./internal/core/... ./internal/readingroom/...
go vet ./...
go build ./...
```

Planned structure:

```text
internal/core/          shared memory and provenance contracts
internal/library/       works, editions, sources, citations, policies
internal/ingestion/     adapter contracts and format implementations
internal/readingroom/   profiles, workflows, contributions, verification
internal/retrieval/     book-aware planning and evidence assembly
internal/storage/sqlite additive persistence and migrations
internal/api/           versioned transport handlers
docs/                   design, operational, and user documentation
```

Code follows existing Go conventions: small packages, explicit interfaces at boundaries, string enums with validation, JSON contracts, contextual errors, and table-driven tests.

Always:

- preserve provenance and authorization through every transformation
- write failing tests before behavioral code
- make ingestion and orchestration idempotent and cancellation-aware
- use additive, rollback-friendly schema changes

Ask first:

- introduce external parsers or hosted model providers
- change source retention defaults
- enable automatic durable retention for users or organizations
- make breaking API or destructive schema changes

Never:

- persist unrestricted full-book text as ordinary memory
- invent citations or infer access from a related object
- let model output directly drive privileged workflow transitions
- merge author, reader, and agent statements into an unattributed record
- delete lineage when consolidating, re-importing, or superseding

## 13. Ordered Implementation Plan

The executable task breakdown, dependencies, acceptance criteria, and verification commands are maintained in [Living Knowledge Library: Implementation Backlog](living-knowledge-library-tasks.md). The phases below remain the strategic summary.

### Phase 1: Contracts and evidence identity

- [x] Document architecture, invariants, APIs, permissions, UI, evaluation, migration, and orchestration.
- [x] Add validated knowledge-role, citation, agent-profile, and contribution contracts.
- [ ] Add immutable evidence-packet fingerprinting and explicit attribution contracts.
- [ ] Persist work, edition, source asset, structure, and citation identity additively.
- [ ] Implement Markdown/plain-text adapter as the first end-to-end ingestion slice.

**Checkpoint:** Existing tests pass; a Markdown book can be imported twice without identity changes and cited by heading/offset.

### Phase 2: Grounded single-agent study

- [ ] Add ingestion jobs, structural chunking, and hierarchical summaries.
- [ ] Add authorized hybrid book retrieval and attributed answer contracts.
- [ ] Add study sessions, turns, and suggested-memory review.
- [ ] Expose the first library, import, query, and reading-room APIs.

**Checkpoint:** A user can import a Markdown book, ask a grounded question, inspect citations, and accept a proposed memory.

### Phase 3: Formats and role workflows

- [ ] Add EPUB, PDF, and captured-web adapters with locator verification fixtures.
- [ ] Implement configurable profiles and dependency-graph workflow execution.
- [ ] Add verifier gates, revision limits, budgets, streaming progress, and cancellation.
- [ ] Ship direct-study and seminar templates before adversarial variants.

**Checkpoint:** A seminar produces attributed contributions and cannot retain an unsupported author claim or inaccurate quote.

### Phase 4: Graph, wiki, and organizations

- [ ] Add provenance-rich graph nodes and edges plus reconciliation.
- [ ] Add cross-book comparison and project/application retrieval plans.
- [ ] Add wiki projections and evidence expansion.
- [ ] Add organization membership, private annotations, curator workflow, and audits.

**Checkpoint:** Two users can share approved organizational knowledge without exposing either user's private notes.

### Phase 5: Long-term quality

- [ ] Build evaluation corpora and regression gates.
- [ ] Add reconsolidation that preserves citation lineage.
- [ ] Add parser/index version migration and relocation tools.
- [ ] Tune cost, latency, retrieval quality, and graph pollution controls.

## 14. Initial Slice Acceptance Criteria

The first code slice is complete when:

- all specified knowledge origins and default reading-room roles are validated enums
- role profiles constrain accepted output kinds
- author claims require at least one citation
- direct quotes require a verified citation containing the exact quoted text
- synthesis contributions require derivation links
- existing repository tests, build, and vet remain green

## 15. Open Decisions

These do not block the contract slice:

- maximum quote length and whether it varies by source policy or jurisdiction
- preferred EPUB and PDF extraction libraries after fixture-based evaluation
- default organization retention mode (`suggested` is recommended)
- whether source vault encryption is application-managed or delegated to the filesystem
- initial identity provider and organization membership model
