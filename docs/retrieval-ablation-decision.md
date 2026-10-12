# Retrieval Ablation Decision

The stable default remains the current vector retrieval pipeline. The existing
benchmark records continuation quality, precision, recall, latency, and token
cost for current retrieval. Graph expansion is available only in explicit
`graph-expand` mode. A BM25 lexical candidate stream and lexical ranking signal
exist behind `AGENT_MEMORY_LEXICAL_MODE=on` (default off). BM25 library
passage ranking has its own flag, `AGENT_MEMORY_PASSAGE_RANKING_MODE=on`
(default off), so it can be promoted independently. Reciprocal-rank fusion is
not implemented and remains reported as unavailable rather than simulated. The
first comparison is recorded in
`.kiro/specs/bm25-lexical-candidates/comparison-report.md`.

## Decision: BM25 memory lexical stream (2026-10-12)

`AGENT_MEMORY_LEXICAL_MODE` stays `off` by default and is not promoted. A run
with the real `onnx-minilm-l6-v2` embedder
(`.kiro/specs/bm25-lexical-candidates/comparison-report-real-embedder.md`)
showed large gains only on queries that reuse identifiers or rare terms
(identifier R@1 37% to 91%, keywords 18% to 89% at weight 0.25). On
hand-written paraphrased natural questions the weighted signal lowered R@10
from 52% to 42% (36% at weight 1.0), added about 2.4 ms p50 per hybrid search
and about 58% database growth for memory rows. Because natural-language
questions are effectively all of the observed usage, the trade is negative.
The weight default stays 0.25; no gating or further tuning is planned. The
code remains behind the flag and can be re-evaluated if usage changes.

Passage ranking (`AGENT_MEMORY_PASSAGE_RANKING_MODE`) beat the legacy scan on
every family, but its paraphrase recall is only 14% R@1, so it is promoted only
if library search is actively used; it remains off by default.

No retrieval stream becomes a default without a versioned benchmark report
showing improved continuation value within privacy, latency, and payload SLOs.
