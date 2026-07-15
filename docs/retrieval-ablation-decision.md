# Retrieval Ablation Decision

The stable default remains the current vector retrieval pipeline. The existing
benchmark records continuation quality, precision, recall, latency, and token
cost for current retrieval. Graph expansion is available only in explicit
`graph-expand` mode. BM25 and candidate fusion are not implemented and are
therefore reported as unavailable rather than simulated.

No retrieval stream becomes a default without a versioned benchmark report
showing improved continuation value within privacy, latency, and payload SLOs.
