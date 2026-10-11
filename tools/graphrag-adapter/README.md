# Agent Memory GraphRAG Adapter

This isolated Python project is the replaceable indexing boundary between Agent Memory and Microsoft GraphRAG. It consumes the published `graphrag==3.1.2` package and never vendors, clones, or patches upstream source.

The adapter exposes only Agent Memory-owned readiness, full-index, incremental-update, cancellation, and artifact-inspection commands. The production image runs the pinned Python adapter directly; queue coordination, artifact validation, activation, and online retrieval remain local Agent Memory responsibilities. GraphRAG query and answer-generation APIs are intentionally outside this runtime contract.

Production builds install with the committed lock and wheelhouse in frozen/offline mode. Runtime package downloads are prohibited. The adapter has no canonical database credential and only writes its declared artifact output; local Agent Memory owns validation, normalized import, activation, and retrieval.
