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

No retrieval stream becomes a default without a versioned benchmark report
showing improved continuation value within privacy, latency, and payload SLOs.
