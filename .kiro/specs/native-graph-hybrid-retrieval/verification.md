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
| 12. End-to-end proof and delivery | Isolated native daemon smoke passed automatic insert processing, unindexed direct hit, operator update and CLI/HTTP parity with real ONNX. Real packaged GraphRAG/Ollama full and incremental activation, base/artifact-mode assertions and original canonical hybrid retrieval passed. Installed runtime and sustained detached native daemon verified. |

## Evidence recorded on 2026-10-11

- Native daemon smoke passed with candidate SHA256 `3610a8fd965a906b0642e972e3de1e8ea9d57bbd93de01eb72c7f0a430a62c9f`, active revision `gr-33c700396bce0776e8829bcd`, pending zero. Synthetic adapter; real installed ONNX provider; private registered synthetic workspace; own daemon stopped afterward.
- Go application/storage race tests passed after writer-reservation and true-create fixture corrections (application 54.277s, storage 16.028s).
- Full `go test ./...`, `go vet ./...` and build passed in a clean worktree at feature commit `ee42639`, including all graph/application/CLI/API/storage and baseline terminal tests. Its isolated native-daemon smoke also passed (SHA256 `dcd2f2ec24b18eb88b43dd382802b1b4340b688a0d90465b5de53a470df7236f`). Final full tests and vet also passed in the shared checkout after the daemon detachment fix; terminal changes are preserved. A machine-specific path in the shared terminal verification document was normalized to a home-relative path for the repository hygiene check.
- Adapter offline full/incremental document/chunk/merge regression reproduces and fixes missing numeric document ordinals when direct DataFrames bypass upstream loaders. Safe workflow/error-class diagnostics preserve the stage without exception text. Final adapter suite: 60 tests passed.
- A second offline real-workflow regression proves that upstream community merging retains delta entity UUIDs after reused entities merge to previous UUIDs. The adapter applies the explicit upstream entity mapping and validates current memberships before strict artifact normalization; it does not guess aliases or relax reference checks.
- Real packaged GraphRAG/Ollama test passed in 428.92s: full `gr-a608bc6f-d909-55c1-9235-c4fe87e931d8`, genuine incremental `gr-0774f877-2bcb-51fd-9068-7e48b818674e`, base revision retained, artifact mode incremental, all three original synthetic memories returned with 28 graph paths.
- Installed PATH CLI smoke passed with SHA256 `16d6a715610d54881b418145797ef95fe63af0c8180446030a63fead04199afd`. Final detached-daemon wiring rebuilt both existing PATH executables to SHA256 `dd7b48d8d78953f47c2bbee945bd79235bc896f4fb69222da88c7087a46d4aa7`. Both are backed up for recovery. The daemon remains healthy across independent commands at loopback port 3211, PID 86370, with its own POSIX session/process group and parent 1.
- Operational rollout: the already-enabled `agent-memory` workspace is executing native job `gj-75ec391c-1bd7-5b62-b2f3-c40a843b8107`. The sequential worker processes one adapter at a time. At the recorded status check, `in2blockchain_v2` adapter readiness is true, but its initial graph has no active revision and 257 pending changes; it is waiting behind earlier enabled workspaces. This is a truthful operational snapshot, not an assertion that its existing corpus has finished indexing. Disabled workspaces were not opted in.
