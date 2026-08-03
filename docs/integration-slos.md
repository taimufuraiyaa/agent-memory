# Integration SLOs

Release reports record sample count, p50/p95 latency, maximum payload bytes,
and clipping count per operation. Initial non-blocking thresholds are hooks
1,000 ms, MCP degraded response 1,500 ms, compact recall 750 ms, imports 2,000
ms per reduced fixture, and filesystem rescan 1,500 ms per reduced fixture.
Baselines are separated by cold/warm and local-service/degraded modes before
thresholds become blocking. Compact responses remain bounded at 256 KiB and
connector previews default to 1 KiB.
