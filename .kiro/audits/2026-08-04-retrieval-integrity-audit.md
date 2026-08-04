# Audit: Retrieval, Decay, and Workflow Integrity

- **Date:** 2026-08-04
- **Method:** Static analysis of source; critical findings verified directly against code; subsystem reviews (storage, lifecycle, embeddings/Bloom, CLI, learning workflows, benchmarks) with file:line evidence.
- **Scope:** `internal/engine`, `internal/storage/sqlite`, `internal/embeddings`, `internal/cli`, `internal/application`, `internal/advisor`, `benchmark/`, `evaluation/`.
- **Status legend:** [FIXED] shipped in this audit round · [PLANNED] spec task · [FUTURE] enhancement candidate.

## 1. Critical Math Findings (verified in source)

### 1.1 Decay clock self-reset — [FIXED]
`SetDecayScores` (`internal/storage/sqlite/store.go:1639`) executed `UPDATE memories SET decay_score = ?, updated_at = ?`, while `ComputeDecayScore` (`internal/engine/decay.go:29`) reads age from `m.UpdatedAt`. Every maintenance run reset the age base to zero, so decay never accumulated. With a 24h cadence and a 7-day half-life, decay could never exceed ~0.094; the eviction gate of 0.65 (`internal/engine/lifecycle.go:142`) was unreachable. Recency (`retrieval.go:633-642`) and conflict `pickWinner` (`conflict.go:82-94`) were flattened by the same corruption.
**Fix:** `SetDecayScores` no longer writes `updated_at`; decay age now reflects last content update/access. Two-run accumulation test added.

### 1.2 Score-range mixing — [FIXED]
`retrieval.go:307-308` mixed cosine similarity in [-1,1] with signals in [0,1] under weights summing to 1.0. Negative-cosine items dragged totals down when the semantic gate is loosened via config, and absolute thresholds were not range-consistent. Graph-expand mode (`retrieval.go:799`) multiplied raw cosine into the total.
**Fix:** semantic component clamped to [0,1] before mixing, in both the standard and graph-expand paths. Breakdown still reports raw cosine for explainability.

### 1.3 Quantized vs float path-dependent scores — [PLANNED]
The same pair can cross `MinSemanticScore = 0.30` on one fast path and not the other (`vectors.go:392` vs `turbovec.go:93`). FWHT quantization itself verified correct. Needs a tolerance band or per-query normalization.

## 2. Term-Index (Bloom) Findings

### 2.1 Stale-snapshot false-negative hole — RETRACTED
Earlier review claimed `replaceMemoryTermsTx` never dirties `term_index_state`. This is incorrect. Triggers `trg_memory_terms_insert_state` / `trg_memory_terms_delete_state` (`store.go:272-294`, recreated at `store.go:858-873`) fire on every `memory_terms` change, setting `state='dirty'` and bumping `corpus_generation`. `Probe` (`internal/application/term_index_runtime.go:49-56`) fails open on any of these. Gate mode cannot short-circuit on a stale snapshot. Verified: the delete trigger fires on both routine replacement deletes and cascade deletes (SQLite fires triggers on cascade actions; `term_index_test.go:169-201` proves it).

### 2.2 Stale-delete pressure counts routine updates — [FIXED]
`trg_memory_terms_delete_state` incremented `stale_delete_count` on every term replacement (DELETE+INSERT on write), so ~34 memory updates triggered a spurious "stale_delete_pressure" rebuild (threshold 100, `term_status.go:99-104`). Rebuilds themselves inflated the counter further. True evictions became indistinguishable from routine writes.
**Fix:** the `memory_terms` delete trigger only dirties + bumps generation; a new `trg_memories_delete_pressure` trigger on `memories` DELETE increments `stale_delete_count` (true corpus reductions only). Cascade still dirties via the `memory_terms` trigger.

### 2.3 Gate mode dead in write-active workspaces — [FUTURE]
Every term change dirties the snapshot (correct but coarse). Incremental bitmap updates on write would let gate mode stay eligible between rebuilds.

### 2.4 Hash degeneracy guard — [FUTURE]
If h2 ≡ 0 (mod m) or gcd(h2, m) > 1, all k probes collapse to a subset of slots. Probability 1/m per term; guard `h2 % m == 0` before m grows near powers of two (`term_bloom.go:70,126-134`).

## 3. Storage & Lifecycle Findings

| # | Severity | Finding | Evidence | Status |
|---|---|---|---|---|
| 3.1 | High | Eviction not decay-ordered; scans `updated_at DESC` and takes from the end | `lifecycle.go:137-147` | [FIXED] ordered by decay_score DESC |
| 3.2 | High | Unversioned migrations; DDL + trigger re-run every Open; JSON-to-blob scan every Open; invalid-JSON rows skipped forever | `store.go:131-887`, `2042-2079` | [PLANNED] |
| 3.3 | High | turbovec index desync: `InsertMemoryByHashWithVector` never updates the in-memory index; `DeleteByIDsAudited` never removes | `store.go:1125-1134`, `audit.go:91-124` | [FIXED] |
| 3.4 | Medium | Feedback/reconsolidation read-modify-write outside tx; lost updates | `retrieval_feedback.go:13-81` | [PLANNED] |
| 3.5 | Medium | Observation dedup check-then-insert, no unique constraint (TOCTOU) | `observations.go:88-114` | [PLANNED] |
| 3.6 | Medium | `upsertMemory` overwrites lifecycle counters; import paths clobber learned state | `store.go:1158-1258` | [PLANNED] |
| 3.7 | Medium | Missing (workspace, created_at/updated_at) composite indexes; session_id column declared but absent | `store.go:1366,1450,1993`, `core/types.go:91` | [PLANNED] |
| 3.8 | Medium | Tombstone cooldown dead code (`cooldown := evictedAt`) | `tombstones.go:19-20` | [PLANNED] |
| 3.9 | Medium | Deep-consolidation pass 2 re-creates the procedural rule every run | `deep_consolidation.go:106-130` | [PLANNED] |
| 3.10 | Low | Archive path traversal via workspace named `..` | `cold_archive.go:48` | [PLANNED] |
| 3.11 | Low | Rune-unsafe byte truncation | `redact.go:48`, `deep_consolidation.go:196` | [PLANNED] |

## 4. CLI, Security, and Contract Findings

| # | Severity | Finding | Evidence | Status |
|---|---|---|---|---|
| 4.1 | High | Workspace path traversal: `--workspace ../../x` reaches `~/.agent-memory/../../x.db` on search/recall/feedback/reindex-terms; write validates, read paths don't | `cli/transport.go:60-85`, `cli/recall_runtime.go:108-115` | [FIXED] validated in resolveWorkspace |
| 4.2 | High | CLI import bypasses API sanitization (no length cap, no workspace check, mid-bundle abort) | `cli/data_commands.go:249-260` | [PLANNED] |
| 4.3 | High | Exit codes derived from string-matching messages; typed sentinels unused; init on existing project exits 1 not 5 | `cli/execute.go:25-39` | [PLANNED] |
| 4.4 | Medium | Registry lock: no stale-lock recovery; Reinstall reads registry without lock | `workspace/manager.go:589-634` | [PLANNED] |
| 4.5 | Medium | README examples mismatch implementation (write --outcome-result, session-end stdin, upgrade --to) | `README.md:140-166` | [PLANNED] |
| 4.6 | Medium | doctor exits 0 when unhealthy | `cli/doctor_command.go:95-107` | [PLANNED] |
| 4.7 | Medium | --toggle-on/off writes agent-memory.env the CLI never loads | `cli/root.go:26-68` | [PLANNED] |
| 4.8 | Low | rename leaves stale names in IDE rule files | `workspace/manager.go:469-507` | [PLANNED] |

## 5. Learning Workflow Findings

| # | Severity | Finding | Evidence | Status |
|---|---|---|---|---|
| 5.1 | High | session-end: every non-empty line becomes a memory; fabricated provenance; failure outcomes bypass confidence gate; first error discards whole batch | `engine/session_end.go:28-114` | [PLANNED] |
| 5.2 | High | study: MaxFiles defaults 0 (unlimited); no per-file cap; 8/20/60-line truncation mid-structure; errors swallowed | `engine/study.go:44-177` | [PLANNED] |
| 5.3 | High | Embedding cost paid before content-hash dedup | `engine/write_pipeline.go:311-393` | [PLANNED] |
| 5.4 | High | Advisor renormalizes missing dimensions; grade A from 3 self-reported samples | `advisor/advisor.go:199-213` | [PLANNED] |
| 5.5 | Medium | Redaction: inconsistent rule sets; quoted multi-word secrets leak; phone-number false positives; promotion output not sanitized | `engine/redact.go`, `engine/security.go` | [PLANNED] |
| 5.6 | Medium | Query-cache keys are raw query text; lifecycle changes not invalidating result cache | `engine/query_cache.go:187-254` | [PLANNED] |
| 5.7 | Medium | Prometheus workspace label cardinality on ~20 metric families | `observability/metrics.go:98-332` | [PLANNED] |

## 6. Benchmark & Evaluation Findings

| # | Severity | Finding | Evidence | Status |
|---|---|---|---|---|
| 6.1 | Critical | No baseline (BM25/plain-vector); only memory-ON vs OFF | `docs/retrieval-ablation-decision.md:6-7` | [PLANNED] |
| 6.2 | Critical | Single-run point estimates; no variance/significance | `benchmark/run_benchmark.sh:577-578` | [PLANNED] |
| 6.3 | High | Lookup effort hardcoded 1 if enabled else 0 | `run_benchmark.sh:502` | [PLANNED] |
| 6.4 | High | Token cost = whitespace word count x hardcoded price | `score.py:15-16,370-375` | [PLANNED] |
| 6.5 | High | Queries embed gold keywords verbatim (lexical leakage) | `generate_benchmark.py:22-66` | [PLANNED] |
| 6.6 | Medium | combined_score labeled with a different verdict | `score.py:499-500` | [PLANNED] |
| 6.7 | Medium | No MRR / recall@k at fixed k | `score.py` | [PLANNED] |
| 6.8 | Medium | Benchmarks never run in CI; results gitignored | `.github/workflows/ci.yml:51-52` | [PLANNED] |
| 6.9 | Medium | Feedback loop (core claim) never exercised in fixtures | `search_upgrade_results.json` | [PLANNED] |

## 7. Corrections to the Original Review

1. **Retracted:** the Bloom "stale-snapshot false-negative hole in gate mode". The memory_terms delete/insert triggers dirty the state on every change; gate mode fails open correctly. The real defect was counter inflation (2.2).
2. **Refined:** the negative-cosine drag (1.2) is gated by MinSemanticScore = 0.30 under default policy; the clamp is defense-in-depth for loosened configs and range-consistency for the weighted mix.
3. **Noted:** SetPinned and UpdateTier also write updated_at (pin/tier actions reset decay age). Kept for now (arguably intended strengthening semantics); tracked as a future "activity vs content-update clock" decision.

## 8. Immediate Follow-ups

- Spec: `.kiro/specs/retrieval-integrity/` (requirements/design/tasks) created for the P0 work package.
- Versioned migrations (3.2) is the next highest-leverage item after this round.
