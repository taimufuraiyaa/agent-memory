---
name: lexical-retrieval-comparison
description: Run, interpret and extend Agent Memory's BM25/FTS5 lexical retrieval comparison harness, and avoid the FTS5 pitfalls found while building it (SQLITE_BUSY on cold connections, slow OR queries, rowid drift).
---

# Lexical (BM25) retrieval comparison

Use this skill when changing the FTS5 lexical index, the lexical stream in `SearchHybrid`, BM25 passage ranking, or when someone asks whether lexical retrieval is worth enabling.

## Where things live

- Spec: `.kiro/specs/bm25-lexical-candidates/` (requirements, design, tasks, comparison report).
- Index, triggers, queries: `internal/storage/sqlite/lexical_index.go` (migration 32).
- Hybrid stream and signal: `internal/application/lexical_search.go`, `internal/application/hybrid_search.go`, `internal/engine/lexical_signal.go`.
- Passage ranking: `internal/retrieval/passages.go` (`WithRanker`), wired in `internal/api/library_handlers.go`.
- Harness: `internal/application/lexical_eval_test.go` (skipped unless requested).
- Flags: `AGENT_MEMORY_LEXICAL_MODE=on|off` (default off), `AGENT_MEMORY_LEXICAL_WEIGHT` (default 0.25, 0 = candidates only).

## Run the comparison

1. `AGENT_MEMORY_LEXICAL_EVAL=1 AGENT_MEMORY_LEXICAL_EVAL_REPORT=<file> go test ./internal/application -run 'TestLexicalComparison$' -count=1 -timeout 25m -v` (about 6 minutes; do not run other heavy jobs at the same time, latency is measured).
2. Optional real model: add `AGENT_MEMORY_LEXICAL_EVAL_MODEL_DIR=<local ONNX model dir>`. Without it the harness uses a hashed n-gram stand-in embedder that is not a semantic model; say so in any report.
3. Read the report by family (identifier, keywords, heading), not only the overall row.

## Interpretation rules

- Candidate-only (weight 0) adds BM25 hits that are then ranked by the old signals; measured to be inert for top-10. The value comes from the lexical signal.
- Queries are built from corpus tokens, which favors lexical methods; the stand-in embedder cannot judge paraphrase quality. Do not claim end-to-end quality from this harness.
- The exact-term arm only has auto-extracted terms (no curated keywords) in this corpus.
- Latency: arms run interleaved per query so write-side drift is shared; still compare p50 and p95, not single runs.

## Pitfalls already paid for

- **SQLITE_BUSY on cold connections.** FTS5 reads its config while the schema is parsed. Inside a deferred write transaction that opens a read snapshot, and the later write upgrade fails immediately under concurrent writers (`TestConcurrentUpserts`). `registerLexicalConnectionWarmup` loads the schema on every new connection. Keep it when touching `Open`.
- **Do not key FTS5 by `memories.rowid`.** The table has a text primary key, so rowids can change under `VACUUM`. The mapping table gives stable integer doc ids.
- **OR queries over common terms are linear in matching documents** (about 10 microseconds per matched document at 3,000 documents). Rank top-N inside the FTS query, then resolve canonical rows, and widen only when filtering empties the page. Cap query terms.
- **Filter columns inside the index.** `workspace` and `edition_id` are UNINDEXED FTS columns so other workspaces or unauthorized editions never consume result slots. Authorization is still decided before the index is consulted.
- **Canonical resolution is mandatory.** Superseded memories and `source_deletions` assets are excluded after ranking; never return index rows without that check.
- **Keep the legacy scan as fallback.** Missing FTS5, a dropped table or a query error must degrade to the previous behavior, never fail the request.

## Before declaring the stream default-on

Follow `docs/retrieval-ablation-decision.md`: a versioned report with a real embedding model, paraphrase queries and a recorded decision. Removal of the Bloom machinery or `memory_terms` is a separate change that needs its own evidence.
