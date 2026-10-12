# BM25 lexical comparison with a real embedding model

Run date: 2026-10-12. Branch `feature/bm25-lexical-candidates`. Companion to `comparison-report.md`, which used a hashed n-gram stand-in embedder.

## Decision

| Surface | Decision | Basis |
|---|---|---|
| Memory lexical stream (`AGENT_MEMORY_LEXICAL_MODE`) | **Keep flagged (default off). Do not promote. Do not reject.** | Large gain on token-reuse queries, but on the hand-written paraphrase family the weighted signal lowers R@5/R@10 versus the current hybrid. |
| `AGENT_MEMORY_LEXICAL_WEIGHT` default | **Keep 0.25.** | 0.25 has the best paraphrase R@5/R@10 of the weighted arms (32%/42% vs 28%/36% at 0.5 and 26%/36% at 1.0) and still reaches 91% identifier R@1. Higher weights buy at most +7 points identifier R@1 and cost paraphrase recall. |
| Passage ranking (`AGENT_MEMORY_PASSAGE_RANKING_MODE`) | Evidence supports promotion (recommendation only). | BM25 beats the legacy scan on every family, including paraphrase (0% to 14% R@1), at lower latency. It is lexical against lexical, so the embedder does not enter it. |
| Exact-term stream and term Bloom filter | Keep. Recommendation only. | See the curated and Bloom sections. |

Nothing in this change alters a default, removes the Bloom machinery or `memory_terms`, or adds workers.

## What was run

- Embedder: **real** `onnx-minilm-l6-v2` (`minilm-l6-v2-fp32`, 384 dimensions), model directory `~/.agent-memory/models/all-MiniLM-L6-v2`, found through `docs/manual-installation.md` and `embeddings.model_path` in `docs/configuration.md`; an ONNX Runtime 1.25.0 build is installed under `~/.agent-memory/onnxruntime` (the provider's documented search path; the exact library it loaded was not logged). The default semantic floor applied (no stand-in floor).
- Command: `AGENT_MEMORY_LEXICAL_EVAL=1 AGENT_MEMORY_LEXICAL_EVAL_MODEL_DIR=$HOME/.agent-memory/models/all-MiniLM-L6-v2 AGENT_MEMORY_LEXICAL_EVAL_REPORT=<file> go test ./internal/application -run 'TestLexicalComparison$' -count=1 -timeout 25m -v`. Test time 118.6 s.
- Corpus: 2,517 chunks of this repository's Markdown (the earlier report had 2,509; docs changed since). 450 generated queries (identifier, keywords, heading, 150 each) plus 50 hand-written paraphrases.
- Machine: load average about 10 at start and end of the run (it was above 100 earlier in the session, so the run waited for it to drop). Latency is single-run and measured under that load; compare ratios, not absolutes.

## Results by family (memory retrieval, top-10, real embedder)

| Arm | identifier R@1 / R@10 / MRR | keywords R@1 / R@10 / MRR | heading R@1 / R@10 / MRR | paraphrase R@1 / R@10 / MRR |
|---|---|---|---|---|
| vector | 37.3 / 60.0 / 0.445 | 18.0 / 30.0 / 0.218 | 11.3 / 44.0 / 0.211 | 20.0 / 52.0 / 0.298 |
| exact-term (auto-extracted) | 8.0 / 9.3 / 0.082 | 5.3 / 6.0 / 0.057 | 0.7 / 4.0 / 0.014 | 4.0 / 6.0 / 0.043 |
| BM25 only | 98.7 / 98.7 / 0.987 | 95.3 / 100 / 0.972 | 10.0 / 38.7 / 0.174 | 14.0 / 24.0 / 0.168 |
| hybrid current (lexical off) | 37.3 / 60.7 / 0.446 | 18.0 / 31.3 / 0.221 | 11.3 / 44.0 / 0.211 | 20.0 / 52.0 / 0.298 |
| hybrid + BM25, weight 0 | 38.7 / 64.7 / 0.469 | 22.0 / 56.0 / 0.312 | 11.3 / 46.0 / 0.214 | 20.0 / 52.0 / 0.298 |
| hybrid + BM25, weight 0.25 | 91.3 / 98.7 / 0.941 | 88.7 / 100 / 0.935 | 13.3 / 49.3 / 0.244 | 22.0 / 42.0 / 0.278 |
| hybrid + BM25, weight 0.5 | 98.0 / 98.7 / 0.982 | 94.7 / 100 / 0.970 | 12.0 / 48.0 / 0.221 | 22.0 / 36.0 / 0.252 |
| hybrid + BM25, weight 0.75 | 98.7 / 98.7 / 0.987 | 94.7 / 100 / 0.970 | 12.0 / 47.3 / 0.214 | 20.0 / 36.0 / 0.234 |
| hybrid + BM25, weight 1.0 | 98.7 / 98.7 / 0.987 | 96.0 / 100 / 0.976 | 12.7 / 46.0 / 0.211 | 20.0 / 36.0 / 0.232 |

Overall row (generated queries only): current hybrid R@1 22.2%, R@10 45.3%; weight 0.25 R@1 64.4%, R@10 82.7%; weight 0.5 R@1 68.2%.

Latency (p50 / p95, ms): vector 0.17 / 0.21; current hybrid 1.93 / 3.75; hybrid + BM25 4.36 / 7.11 at weight 0.25. The lexical stream adds about 2.4 ms p50 per hybrid search on this corpus.

Write cost: 0.135 ms to 0.482 ms per memory (+0.35 ms), database 4.55 to 7.21 MiB (+58.5% for memory rows without vectors, about 1.1 KiB per memory). The earlier report's +1.5 ms was measured under much higher machine load.

## Findings

1. **The real model changes two earlier conclusions.** Candidate-only (weight 0) is not inert: on keyword queries R@10 rises from 31.3% to 56.0% and identifier R@10 from 60.7% to 64.7%, with paraphrase results identical to the baseline. With the stand-in it looked inert because that embedder already shared tokens with the query. The signal weight is still what delivers R@1.
2. **Paraphrase queries expose the cost.** BM25 alone gets 14% R@1 and 24% R@10 there, below the vector arm (20% / 52%). Blended at 0.25 the hybrid gains 1 query at R@1 (22% vs 20%) and loses 10 points at R@10 (42% vs 52%); at 1.0 it loses 16 points. Lexical overlap with unrelated chunks pushes the correct chunk out of the top 10. With 50 queries one query is 2 points and the 95% interval is roughly ±13 points, so the R@1 difference is noise but the R@10 drop is consistent across all four weights.
3. **Heading queries gain little** (R@1 11.3 to 13.3 at 0.25); BM25 alone is below vector there.
4. **Generated-query gains are real but flattered.** Identifier and keyword queries reuse corpus tokens by construction. The honest summary is: when the agent's query contains the literal identifier or rare term, BM25 fixes retrieval; when it paraphrases, BM25 slightly hurts.
5. **Passage ranking** (lexical against lexical): overall R@1 36.2% to 68.0%, p50 4.35 ms to 0.82 ms; paraphrase 0% to 14% R@1 and 0% to 24% R@10.

## Curated exact-term experiment (task 13)

Gold chunks of the paraphrase family were loaded with three hand-chosen curated keywords each (`locator.Input.Explicit`, stored as source `explicit`). The same author wrote queries and keywords, so curated quality here is an upper bound. "Tag-lookup" queries are the first two curated terms; they test the mechanism, they are not an independent quality claim.

| Query set | DB | vector R@1/R@10 | exact-term R@1/R@10 | BM25 R@1/R@10 | hybrid current R@1/R@10 |
|---|---|---|---|---|---|
| paraphrase | auto only | 20 / 52 | 4 / 6 | 14 / 24 | 20 / 52 |
| paraphrase | curated | 20 / 52 | 4 / 4 | 14 / 24 | 20 / 52 |
| tag-lookup | auto only | 34 / 62 | 12 / 16 | 32 / 54 | 34 / 62 |
| tag-lookup | curated | 34 / 62 | 100 / 100 | 32 / 54 | 34 / 68 |

Reading: curated keywords make the exact-term stream perfect for queries that use the tag, which neither vector (34%) nor BM25 (32%) achieves, because BM25 indexes memory content and not `memory_terms`. They do nothing for paraphrased questions, since the paraphrase does not contain the tag. Even with curated terms the current hybrid only reaches 34% R@1 / 68% R@10 on tag queries, so exact-term candidates are not strongly promoted in the final ranking when they conflict with vector results. Recommendation (not implemented): keep the exact-term stream if agents are expected to search by their own tags; if the lexical stream is promoted, consider indexing explicit keywords in the FTS index so a single stream covers both.

## Term Bloom pre-check (task 13)

Term index: 3,198 distinct terms, 61,312 bits, 7 hashes, estimated FPP 0.0003. `SearchTerms` per call, 5 rounds, modes interleaved:

| Query set | off mean µs | shadow mean µs | gate mean µs |
|---|---|---|---|
| realistic seed queries (2,500 calls per mode) | 1548.6 | 1578.5 | 984.2 (990 of 2,500 short-circuited) |
| definite-miss queries (1,000 calls per mode) | 1238.4 | 1270.1 | 33.8 (all short-circuited) |

Reading: the probe itself costs about 30 µs (shadow versus off). In gate mode a definite miss costs 34 µs instead of about 1.24 ms, so the Bloom filter does save measurable time when queries have no matching term; 40% of the seed queries here were definite misses because the production seed takes the first three words, which are often not indexed terms. Because the exact-term arm contributes almost nothing on generated or paraphrase queries, the saving is the main value of the exact-term path on this corpus. Recommendation only: keep the filter; the shadow default pays 30 µs per call for no latency benefit, so gate mode is where the benefit appears once its shadow mismatch counter stays at zero.

## Caveats

- One repository's documentation, one embedding model (MiniLM-L6-v2), one machine, a single latency run under load average about 10.
- 50 paraphrases is small; the interval is wide. Several gold chunks have near-duplicates in other files (for example `SECURITY.md` and `docs/security.md`); where I found a second legitimate answer both were listed, but undiscovered duplicates depress every arm's absolute numbers, including the vector arm's 20% R@1.
- The paraphrase set was written by one author who also saw the corpus; a guard in the harness rejects any query that repeats a gold-chunk token occurring in three or fewer chunks, but common words still overlap.
- The exact-term arm uses the production seed (first three words), which is dominated by function words on natural-language questions; its low recall is partly that seed, not the index.
- Curated keywords were written by the same author as the queries (upper bound).

## Reproduce

See `.agents/skills/lexical-retrieval-comparison/SKILL.md`. Model directory and runtime location: `docs/manual-installation.md`.

## Raw harness output

The block below is the unedited report the harness wrote from this run.

`````markdown
# BM25 lexical comparison report

Generated by `TestLexicalComparison` on 2026-10-12.

## Setup

- Corpus: 2517 chunks cut from this repository's own Markdown (docs, specs, skills, README, SECURITY), excluding this spec.
- Generated queries: 450 (identifier 150, keywords 150, heading 150), deterministic from the corpus; gold is every chunk that contains the query tokens (identifier/keywords) or sits under the unique heading (heading). Top-k = 10. The overall row of each table covers only these.
- Paraphrase queries: 50 hand-written questions that avoid the gold chunk's distinctive tokens; gold chunks were located by anchor text and verified by reading. Reported as their own family and excluded from the overall row.
- Embedder: real local model provider `onnx-minilm-l6-v2`; default semantic floor.
- The main run's exact-term index is populated only by automatic extraction (`locator.Extract`); curated keywords are measured separately in the curated experiment below.
- Generated-query construction favors lexical methods (queries reuse corpus tokens); the paraphrase family is the counterweight. Treat absolute numbers as a retrieval-component comparison on one repository's documentation, not an end-to-end quality claim.
- Corpus load (memories, vectors, terms, triggers): 2.007s.

## Memory retrieval arms

| Arm | R@1 | R@5 | R@10 | MRR@10 | p50 ms | p95 ms | errors |
|---|---|---|---|---|---|---|---|
| vector | 22.2% | 38.2% | 44.7% | 0.291 | 0.17 | 0.21 | 0 |
| exact-term (locator, <=3 terms) | 4.7% | 5.6% | 6.4% | 0.051 | 1.55 | 1.93 | 0 |
| BM25 only (FTS5) | 68.0% | 75.6% | 79.1% | 0.711 | 0.51 | 1.91 | 0 |
| hybrid current (vector + exact-term, lexical off) | 22.2% | 38.4% | 45.3% | 0.293 | 1.93 | 3.75 | 0 |
| hybrid + BM25 candidates only (weight 0) | 24.0% | 44.7% | 55.6% | 0.332 | 4.37 | 7.15 | 0 |
| hybrid + BM25 weighted (weight 0.25) | 64.4% | 79.1% | 82.7% | 0.707 | 4.36 | 7.11 | 0 |
| hybrid + BM25 weighted (weight 0.5) | 68.2% | 78.4% | 82.2% | 0.725 | 4.38 | 7.27 | 0 |
| hybrid + BM25 weighted (weight 0.75) | 68.4% | 77.1% | 82.0% | 0.723 | 4.33 | 7.18 | 0 |
| hybrid + BM25 weighted (weight 1) | 69.1% | 76.7% | 81.6% | 0.725 | 4.36 | 7.16 | 0 |

**identifier queries**

| Arm | R@1 | R@5 | R@10 | MRR@10 |
|---|---|---|---|---|
| vector | 37.3% | 53.3% | 60.0% | 0.445 |
| exact-term (locator, <=3 terms) | 8.0% | 8.7% | 9.3% | 0.082 |
| BM25 only (FTS5) | 98.7% | 98.7% | 98.7% | 0.987 |
| hybrid current (vector + exact-term, lexical off) | 37.3% | 53.3% | 60.7% | 0.446 |
| hybrid + BM25 candidates only (weight 0) | 38.7% | 57.3% | 64.7% | 0.469 |
| hybrid + BM25 weighted (weight 0.25) | 91.3% | 97.3% | 98.7% | 0.941 |
| hybrid + BM25 weighted (weight 0.5) | 98.0% | 98.7% | 98.7% | 0.982 |
| hybrid + BM25 weighted (weight 0.75) | 98.7% | 98.7% | 98.7% | 0.987 |
| hybrid + BM25 weighted (weight 1) | 98.7% | 98.7% | 98.7% | 0.987 |

**keywords queries**

| Arm | R@1 | R@5 | R@10 | MRR@10 |
|---|---|---|---|---|
| vector | 18.0% | 26.7% | 30.0% | 0.218 |
| exact-term (locator, <=3 terms) | 5.3% | 6.0% | 6.0% | 0.057 |
| BM25 only (FTS5) | 95.3% | 99.3% | 100.0% | 0.972 |
| hybrid current (vector + exact-term, lexical off) | 18.0% | 27.3% | 31.3% | 0.221 |
| hybrid + BM25 candidates only (weight 0) | 22.0% | 41.3% | 56.0% | 0.312 |
| hybrid + BM25 weighted (weight 0.25) | 88.7% | 99.3% | 100.0% | 0.935 |
| hybrid + BM25 weighted (weight 0.5) | 94.7% | 99.3% | 100.0% | 0.970 |
| hybrid + BM25 weighted (weight 0.75) | 94.7% | 100.0% | 100.0% | 0.970 |
| hybrid + BM25 weighted (weight 1) | 96.0% | 100.0% | 100.0% | 0.976 |

**heading queries**

| Arm | R@1 | R@5 | R@10 | MRR@10 |
|---|---|---|---|---|
| vector | 11.3% | 34.7% | 44.0% | 0.211 |
| exact-term (locator, <=3 terms) | 0.7% | 2.0% | 4.0% | 0.014 |
| BM25 only (FTS5) | 10.0% | 28.7% | 38.7% | 0.174 |
| hybrid current (vector + exact-term, lexical off) | 11.3% | 34.7% | 44.0% | 0.211 |
| hybrid + BM25 candidates only (weight 0) | 11.3% | 35.3% | 46.0% | 0.214 |
| hybrid + BM25 weighted (weight 0.25) | 13.3% | 40.7% | 49.3% | 0.244 |
| hybrid + BM25 weighted (weight 0.5) | 12.0% | 37.3% | 48.0% | 0.221 |
| hybrid + BM25 weighted (weight 0.75) | 12.0% | 32.7% | 47.3% | 0.214 |
| hybrid + BM25 weighted (weight 1) | 12.7% | 31.3% | 46.0% | 0.211 |

**paraphrase queries**

| Arm | R@1 | R@5 | R@10 | MRR@10 |
|---|---|---|---|---|
| vector | 20.0% | 44.0% | 52.0% | 0.298 |
| exact-term (locator, <=3 terms) | 4.0% | 4.0% | 6.0% | 0.043 |
| BM25 only (FTS5) | 14.0% | 20.0% | 24.0% | 0.168 |
| hybrid current (vector + exact-term, lexical off) | 20.0% | 44.0% | 52.0% | 0.298 |
| hybrid + BM25 candidates only (weight 0) | 20.0% | 44.0% | 52.0% | 0.298 |
| hybrid + BM25 weighted (weight 0.25) | 22.0% | 32.0% | 42.0% | 0.278 |
| hybrid + BM25 weighted (weight 0.5) | 22.0% | 28.0% | 36.0% | 0.252 |
| hybrid + BM25 weighted (weight 0.75) | 20.0% | 28.0% | 36.0% | 0.234 |
| hybrid + BM25 weighted (weight 1) | 20.0% | 26.0% | 36.0% | 0.232 |

## Passage search arms

| Arm | R@1 | R@5 | R@10 | MRR@10 | p50 ms | p95 ms | errors |
|---|---|---|---|---|---|---|---|
| passages: legacy scan (substring counts) | 36.2% | 48.4% | 55.8% | 0.413 | 4.35 | 10.66 | 0 |
| passages: BM25 (FTS5) | 68.0% | 75.3% | 79.1% | 0.711 | 0.82 | 2.21 | 0 |

**identifier queries**

| Arm | R@1 | R@5 | R@10 | MRR@10 |
|---|---|---|---|---|
| passages: legacy scan (substring counts) | 12.0% | 40.7% | 58.7% | 0.232 |
| passages: BM25 (FTS5) | 98.7% | 98.7% | 98.7% | 0.987 |

**keywords queries**

| Arm | R@1 | R@5 | R@10 | MRR@10 |
|---|---|---|---|---|
| passages: legacy scan (substring counts) | 95.3% | 100.0% | 100.0% | 0.976 |
| passages: BM25 (FTS5) | 95.3% | 99.3% | 100.0% | 0.971 |

**heading queries**

| Arm | R@1 | R@5 | R@10 | MRR@10 |
|---|---|---|---|---|
| passages: legacy scan (substring counts) | 1.3% | 4.7% | 8.7% | 0.032 |
| passages: BM25 (FTS5) | 10.0% | 28.0% | 38.7% | 0.175 |

**paraphrase queries**

| Arm | R@1 | R@5 | R@10 | MRR@10 |
|---|---|---|---|---|
| passages: legacy scan (substring counts) | 0.0% | 0.0% | 0.0% | 0.000 |
| passages: BM25 (FTS5) | 14.0% | 22.0% | 24.0% | 0.169 |

## Write overhead and storage

| | total | per memory | database file |
|---|---|---|---|
| triggers removed | 341ms | 0.135 ms | 4.55 MiB |
| lexical index maintained | 1.214s | 0.482 ms | 7.21 MiB |

Index storage growth on this corpus: 58.5% (memory rows only, no vectors).

## Embedding cost

Embedding the whole corpus with the configured provider took 9.293s.

## Curated exact-term experiment

Same corpus and vectors, loaded twice: once with automatic term extraction only, once with explicit curated keywords (core.MemoryTerm source `explicit`) added to the gold chunks of the paraphrase family. Exact-term queries use the production seed (first three words of at least three characters) with the OR operator.

**automatic extraction only — paraphrase queries**

| Arm | R@1 | R@5 | R@10 | MRR@10 | p50 ms | p95 ms |
|---|---|---|---|---|---|---|
| vector | 20.0% | 44.0% | 52.0% | 0.298 | 0.12 | 0.18 |
| exact-term (locator, <=3 terms) | 4.0% | 4.0% | 6.0% | 0.043 | 1.70 | 2.01 |
| BM25 only (FTS5) | 14.0% | 20.0% | 24.0% | 0.168 | 1.44 | 2.23 |
| hybrid current (vector + exact-term, lexical off) | 20.0% | 44.0% | 52.0% | 0.298 | 2.15 | 4.12 |
| hybrid + BM25 weighted (weight 0.25) | 22.0% | 32.0% | 42.0% | 0.278 | 6.03 | 7.57 |

**automatic extraction only — tag-lookup queries (two curated terms)**

| Arm | R@1 | R@5 | R@10 | MRR@10 | p50 ms | p95 ms |
|---|---|---|---|---|---|---|
| vector | 34.0% | 58.0% | 62.0% | 0.441 | 0.12 | 0.19 |
| exact-term (locator, <=3 terms) | 12.0% | 16.0% | 16.0% | 0.140 | 1.42 | 1.72 |
| BM25 only (FTS5) | 32.0% | 48.0% | 54.0% | 0.382 | 0.46 | 1.46 |
| hybrid current (vector + exact-term, lexical off) | 34.0% | 58.0% | 62.0% | 0.441 | 1.72 | 3.87 |
| hybrid + BM25 weighted (weight 0.25) | 36.0% | 56.0% | 62.0% | 0.457 | 4.37 | 5.92 |

**curated explicit keywords on gold chunks — paraphrase queries**

| Arm | R@1 | R@5 | R@10 | MRR@10 | p50 ms | p95 ms |
|---|---|---|---|---|---|---|
| vector | 20.0% | 44.0% | 52.0% | 0.298 | 0.14 | 0.25 |
| exact-term (locator, <=3 terms) | 4.0% | 4.0% | 4.0% | 0.040 | 1.81 | 2.16 |
| BM25 only (FTS5) | 14.0% | 20.0% | 24.0% | 0.168 | 1.59 | 2.42 |
| hybrid current (vector + exact-term, lexical off) | 20.0% | 44.0% | 52.0% | 0.298 | 2.22 | 3.88 |
| hybrid + BM25 weighted (weight 0.25) | 22.0% | 32.0% | 42.0% | 0.278 | 6.52 | 8.31 |

**curated explicit keywords on gold chunks — tag-lookup queries (two curated terms)**

| Arm | R@1 | R@5 | R@10 | MRR@10 | p50 ms | p95 ms |
|---|---|---|---|---|---|---|
| vector | 34.0% | 58.0% | 62.0% | 0.441 | 0.13 | 0.19 |
| exact-term (locator, <=3 terms) | 100.0% | 100.0% | 100.0% | 1.000 | 1.46 | 1.73 |
| BM25 only (FTS5) | 32.0% | 48.0% | 54.0% | 0.382 | 0.46 | 1.33 |
| hybrid current (vector + exact-term, lexical off) | 34.0% | 62.0% | 68.0% | 0.455 | 2.66 | 3.70 |
| hybrid + BM25 weighted (weight 0.25) | 36.0% | 56.0% | 62.0% | 0.457 | 4.84 | 6.40 |

## Term Bloom pre-check cost

`SearchTerms` latency per call in microseconds, five rounds, mode order rotated per query. `shadow` probes and always runs the indexed lookup; `gate` skips the lookup on a definite miss.

| Query set | Mode | calls | mean µs | p50 µs | p95 µs | negative | maybe | bypassed | short-circuited |
|---|---|---|---|---|---|---|---|---|---|
| realistic seed queries | off | 2500 | 1548.6 | 1555.0 | 1928.2 | 0 | 0 | 2500 | 0 |
| realistic seed queries | shadow | 2500 | 1578.5 | 1581.4 | 1962.6 | 990 | 1510 | 0 | 0 |
| realistic seed queries | gate | 2500 | 984.2 | 1381.4 | 1942.5 | 990 | 1510 | 0 | 990 |
| definite-miss queries | off | 1000 | 1238.4 | 1231.2 | 1328.0 | 0 | 0 | 1000 | 0 |
| definite-miss queries | shadow | 1000 | 1270.1 | 1259.6 | 1364.5 | 1000 | 0 | 0 | 0 |
| definite-miss queries | gate | 1000 | 33.8 | 30.3 | 55.2 | 1000 | 0 | 0 | 1000 |

`````
