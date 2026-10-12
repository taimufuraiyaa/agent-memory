# BM25 lexical candidates

## Objective and authorization

The user's 2026-10-12 instruction asks to branch from the current work, implement a BM25 lexical retrieval solution, and compare the result with the current system. Implementation is authorized on `feature/bm25-lexical-candidates`. Nothing in this spec changes the default retrieval behavior until a versioned comparison report supports it, per `docs/retrieval-ablation-decision.md`.

## Problem statement (verified in source on 2026-10-12)

- Default search is brute-force cosine over stored embeddings followed by a weighted rerank (`internal/engine/vector_search.go`, `internal/engine/retrieval.go`).
- Exact lexical lookup covers at most three locator terms per memory and is an equality match on `memory_terms` (`internal/locator`, `internal/storage/sqlite/term_index.go`). Prose words are not searchable lexically.
- Library passage search (`internal/retrieval/passages.go`) loads every authorized passage and scores with raw substring counts: no inverse document frequency, no length normalization, full scan per query.
- FTS5 is described in doc comments but no FTS5 table or query exists. BM25 and candidate fusion are declared unimplemented in `docs/retrieval-ablation-decision.md`; audit finding 6.1 records the missing lexical baseline.

## Scope

In scope:

1. A per-database FTS5 lexical index over memory content with BM25 ranking, maintained synchronously with canonical memory writes.
2. A flag-gated lexical candidate stream in the shared hybrid search that unions with vector, exact-term and graph discoveries and contributes an explainable lexical signal.
3. BM25 ranking for library passage search behind the existing authorization filter, with the previous scan kept as fail-open fallback.
4. A repeatable comparison harness and a written report comparing the current system with the new streams.

Out of scope for this change (decided by the comparison, tracked separately):

- Removing the term Bloom filter machinery or `memory_terms`/locator.
- Changing default modes, default weights of existing signals, or embedding behavior.
- Any new background worker, queue, lease or daemon.

## Functional requirements

1. **Index scope.** Every canonical memory row is indexed by workspace and memory ID. Content, optional diagram code, tags and entities are indexed; superseded memories remain stored but are never returned.
2. **Synchronous maintenance.** Insert, content/tag/entity update, delete (including audit eviction and lifecycle deletion) and cascade deletion update the index inside the same transaction as the canonical write. Counter-only updates (access, feedback, pin, tier) do not touch the index.
3. **Backfill.** Existing databases receive the index through one recorded migration that populates it in a single bounded statement sequence. Unavailable FTS5 support never prevents the database from opening.
4. **Query safety.** User text is never passed to the FTS5 query grammar raw. Queries are reduced to bounded quoted terms; empty or all-stopword queries return no lexical candidates without error.
5. **Workspace isolation.** A lexical query only returns rows of the requested workspace and rechecks canonical rows, filters, supersession and suppression exactly like exact-term and graph candidates.
6. **Flag and rollback.** A single environment-controlled mode (`off` default, `on`) selects the stream. `off` produces byte-identical results to the pre-change system. Any lexical failure falls back to the existing behavior and never fails a search.
7. **Explainability.** Lexical contribution is visible per hit through the signal breakdown and the retrieval explanation fields; vector, exact-term and lexical provenance remain distinguishable.
8. **Bounds.** The stream contributes at most the same candidate budget as the exact-term stream; query term count, row limits and passage limits are capped.
9. **Passage search.** With the index available and passage ranking enabled, BM25 ranks passages among authorized editions only; authorization is evaluated before the index is consulted and results never include unauthorized editions. Existing per-node diversity limits and result contract remain.
10. **Independent passage flag.** Passage ranking has its own environment-controlled mode (`AGENT_MEMORY_PASSAGE_RANKING_MODE`, `off` default, `on`) that does not depend on `AGENT_MEMORY_LEXICAL_MODE`. The memory stream and passage ranking can each be enabled or rolled back without affecting the other. With `off`, library passage search is byte-identical to the legacy authorized scan. The shared FTS5 index is maintained regardless of either flag.
11. **No regression of contracts.** CLI, HTTP and MCP request/response shapes remain backward compatible. `terms` mode, `graph-expand` and graph routing keep their contracts.

## Non-functional requirements

- No new third-party dependency; the existing `modernc.org/sqlite` FTS5 module is used.
- Index maintenance adds no measurable cost to counter-only updates and bounded cost to content writes.
- Migration is idempotent, recorded in `schema_migrations`, and refuses nothing on older databases.
- No private content is written outside the database; reports use synthetic or repository documentation corpora only.

## Acceptance criteria

- Focused SQLite tests prove sync on insert, update, delete, supersede, cascade and counter-only updates, workspace isolation and idempotent migration/backfill.
- Application tests prove: lexical-only discovery below the semantic floor, `off` parity, failure fallback, stale/deleted/superseded rejection, and bounded candidate count.
- Passage tests prove authorized-only results, BM25 ordering over the legacy scan on a controlled corpus, and fallback.
- `go test ./...`, `go vet ./...` and `go build` pass for touched packages and the full tree.
- A comparison report exists under this spec with recall@k, MRR, latency and index overhead for each arm, states its corpus and embedder limits, and ends with an explicit promote / keep-flagged / reject recommendation.
