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
- Harness: `internal/application/lexical_eval_test.go` (main run), `lexical_eval_paraphrase_test.go` (50 hand-written paraphrases with anchor-resolved gold, curated keywords) and `lexical_eval_experiments_test.go` (weight sweep arms, curated exact-term experiment, Bloom timing). Skipped unless requested.
- Flags (independent, both default off): `AGENT_MEMORY_LEXICAL_MODE=on|off` for the memory stream, `AGENT_MEMORY_LEXICAL_WEIGHT` (default 0.25, 0 = candidates only), `AGENT_MEMORY_PASSAGE_RANKING_MODE=on|off` for library passage ranking. Passage evidence is lexical-vs-lexical and needs no embedder; memory-stream evidence needs a real embedder before promotion.

## Run the comparison

1. `AGENT_MEMORY_LEXICAL_EVAL=1 AGENT_MEMORY_LEXICAL_EVAL_REPORT=<file> go test ./internal/application -run 'TestLexicalComparison$' -count=1 -timeout 25m -v` (about 2 minutes with a real model on a quiet machine, 6 with the stand-in; run `uptime` first and do not run other heavy jobs, latency is measured). Add `-timeout 25m`.
2. Real model: add `AGENT_MEMORY_LEXICAL_EVAL_MODEL_DIR=$HOME/.agent-memory/models/all-MiniLM-L6-v2` (install flow in `docs/manual-installation.md`). Without it the harness uses a hashed n-gram stand-in embedder that is not a semantic model; say so in any report.
3. Read the report by family (identifier, keywords, heading, paraphrase), not only the overall row; paraphrase is excluded from the overall row. The harness fails if an anchor matches no chunk, is ambiguous, or a paraphrase repeats a rare gold token.

## Interpretation rules

- Candidate-only (weight 0) adds BM25 hits ranked by the old signals. With the stand-in it was inert; with real MiniLM it lifts keyword R@10 from 31% to 56% and leaves paraphrase results unchanged. The lexical signal still delivers R@1, and on paraphrases it costs R@10 (52% to 42% at 0.25). See `comparison-report-real-embedder.md`.
- Queries are built from corpus tokens, which favors lexical methods; the stand-in embedder cannot judge paraphrase quality. Do not claim end-to-end quality from this harness.
- The main run's exact-term arm has auto-extracted terms only; the curated experiment adds explicit keywords for the paraphrase gold chunks (same author as the queries, so an upper bound).
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
