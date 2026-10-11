# Native graph and hybrid retrieval verification

## Requirement coverage

| Requirement | Implementation and evidence |
| --- | --- |
| 1. Native enabled workspace execution | `native_graph_daemon.go`, `NativeGraphWorker.RunWorkspace`; runtime/workspace opt-out regression and isolated daemon write/index proof. |
| 2. Coalescing and immutable cutoffs | `LoadNativeGraphWork` and `ScheduleNativeGraphWork`; concurrent insert publication and remaining-journal regressions; private raw-state copy/hash regression. |
| 3. Leases, cancellation, finite retries | Native heartbeat, SQLite publication fences and bounded scheduling; cancellation, shutdown, disabled/configuration/expired lease and attempt-limit regressions; race suite. |
| 4. Projection and validated artifacts | Canonical projection builder, full correlation map, manifest/hash/schema/model/prompt checks; malformed artifact, unknown ID and fingerprint regressions. |
| 5. Complete import and atomic activation | Scoped import with stable entity identity/review carry-forward, occurrence-scoped communities, job/configuration/lease/cutoff publication transaction; stable-ID and concurrent-write regressions. |
| 6. Truthful controls/readiness | Explicit workspace enable, actual local adapter readiness, configuration-version freshness, fresh retry revisions; enable/prompt/retry regressions. Explicit persisted-file selection tests and private daemon smoke. |
| 7. Shared modes and transports | `MemoryService.SearchHybrid`, ordinary CLI and HTTP handlers; forwarding/required-failure tests, isolated required-graph CLI/HTTP result parity; existing legacy retrieval suite. |
| 8. Independent candidate coverage | Fresh unindexed direct memory, graph-only low-cosine support, independent explicit-term hit and auto relationship/direct route regressions. |
| 9. Bounded/current canonical resolution | Bounded snapshot, term candidates and traversal; batched current ID/vector reads; workspace/fingerprint/supersession/deletion/tier/suppression/filter regressions. Queries do not start Python. |
| 10. Actual similarity and graph ranking | Candidate scoring with semantic vectors and lifecycle signals, graph path trust/evidence, preserved diversity and top-k; graph-only/filters/top-k regressions. |
| 11. Visible fallback and cache identity | Scoped active graph freshness/cache epoch, explicit stale policy, optional fallback and required failure; no-evidence/stale/current-record/global-support regressions. |
| 12. End-to-end proof and delivery | Isolated native daemon smoke passed automatic insert processing, unindexed direct hit, operator update and CLI/HTTP parity with real ONNX. Real GraphRAG full activation passed; actual incremental proof and final installed rollout are recorded below when complete. |

## Evidence recorded on 2026-10-11

- Native daemon smoke passed with candidate SHA256 `3610a8fd965a906b0642e972e3de1e8ea9d57bbd93de01eb72c7f0a430a62c9f`, active revision `gr-33c700396bce0776e8829bcd`, pending zero. Synthetic adapter; real installed ONNX provider; private registered synthetic workspace; own daemon stopped afterward.
- Go application/storage race tests passed after writer-reservation and true-create fixture corrections (application 54.277s, storage 16.028s).
- Full `go test ./...`, `go vet ./...` and build passed in a clean worktree at feature commit `ee42639`, including all graph/application/CLI/API/storage and baseline terminal tests. Its isolated native-daemon smoke also passed (SHA256 `dcd2f2ec24b18eb88b43dd382802b1b4340b688a0d90465b5de53a470df7236f`). The latest shared-checkout run has unrelated terminal UX/hygiene failures while that work is changing; the clean verification does not modify that work.
- Adapter offline full/incremental document/chunk/merge regression reproduces and fixes missing numeric document ordinals when direct DataFrames bypass upstream loaders. Safe workflow/error-class diagnostics preserve the stage without exception text. Final adapter suite: 60 tests passed.
- A second offline real-workflow regression proves that upstream community merging retains delta entity UUIDs after reused entities merge to previous UUIDs. The adapter applies the explicit upstream entity mapping and validates current memberships before strict artifact normalization; it does not guess aliases or relax reference checks.
- Real packaged GraphRAG/Ollama full activation has passed. The prior incremental run exposed the document ordinal merge error; the corrected real run is underway. No incremental success or user-workspace activation is inferred from readiness or daemon startup.
