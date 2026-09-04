# Agent Memory GraphRAG Adapter

This isolated Python project is the replaceable indexing boundary between Agent Memory and Microsoft GraphRAG. It consumes the published `graphrag==3.1.2` package and never vendors, clones, or patches upstream source.

The adapter exposes only Agent Memory-owned readiness, full-index, incremental-update, cancellation, and artifact-inspection commands. The production image adds a small Go queue/object-custody supervisor as its entrypoint; the Python package remains the replaceable indexing library inside that isolated worker. GraphRAG query and answer-generation APIs are intentionally outside this runtime contract; all online retrieval remains in Agent Memory.

Production builds install with the committed lock and wheelhouse in frozen/offline mode. Runtime package downloads are prohibited. The graph worker has queue and scoped object-storage capabilities but no canonical database credential; validation, normalized import, activation, and online retrieval remain Go-owned Agent Memory responsibilities.
