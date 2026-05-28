# Search Quality Improvement Audit

## Scope

This audit records the final defect inventory, chosen solutions, validation, and
preventive measures for the search-quality upgrade completed in `agent-memory`.

The audit covers defects `D-01` through `D-15`, which together formed one
causal chain:

- weak semantic gating allowed irrelevant memories to look correct
- placeholder embeddings produced poor semantic signal
- provider rollout lacked provenance, migration, and parity guarantees
- new writes depended on lazy vector creation instead of owning correctness
- the dashboard overstated blended score instead of surfacing semantic truth

## Final Outcome

The project now has:

- mode-aware semantic floors
- real ONNX MiniLM embeddings
- provider-aware vector storage and `re-embed`
- one shared provider factory across production surfaces
- eager write-path embedding with rollback safety
- semantic-primary dashboard relevance UX

## Defect Inventory

| ID | Symptom | Root Cause | Chosen Solution | Final Status |
| :--- | :--- | :--- | :--- | :--- |
| `D-01` | Irrelevant memories surfaced with plausible total score | No semantic floor before rerank | Added mode-aware semantic floors and caller override support in retrieval | Fixed |
| `D-02` | "Embeddings" behaved like noisy lexical fingerprints | Placeholder hash-based provider | Implemented real ONNX MiniLM-L6-v2 with tokenizer, pooling, normalization | Fixed |
| `D-03` | ONNX runtime unavailable in restricted environments | Native runtime dependency not handled operationally | Hardened install flow and fallback handling instead of assuming package-manager access | Fixed |
| `D-04` | Model download path failed behind proxy/interception | HuggingFace-only download path and weak validation | Added fallback acquisition path and stronger artifact validation | Fixed |
| `D-05` | Go dependency resolution was blocked in corporate network | Environment-specific proxy restrictions | Used direct dependency acquisition when required and documented the constraint | Fixed |
| `D-06` | Older tests encoded broken retrieval behavior | Test suite matched previous bug, not intended behavior | Updated tests to assert floor behavior and valid explain output | Fixed |
| `D-07` | ONNX fallback test became environment-sensitive | Test behavior depended on runtime installation state | Made fallback tests assert provider selection semantics rather than local environment | Fixed |
| `D-08` | Dashboard appeared stale after rebuilds | Operational mismatch between rebuilt assets and running process | Restarted the serving process when dashboard artifacts changed | Fixed |
| `D-09` | Existing databases lacked vector provenance | No `embedding_provider` column in stored vectors | Added backward-safe SQLite migration and provider-aware storage helpers | Fixed |
| `D-10` | Workspace coverage was almost empty despite many memories | Old rows had no vectors and no migration path | Added `re-embed` command and provider-aware inventory/reporting | Fixed |
| `D-11` | Dashboard search used a different embedder than CLI/API | Provider construction was ad hoc across surfaces | Centralized provider construction behind one shared factory | Fixed |
| `D-12` | New writes were invisible until manual migration | Write path did not persist vectors eagerly | Injected embedder into `WritePipeline` and eagerly upserted vectors | Fixed |
| `D-13` | Background dashboard process died during iterative work | Operational instability during repeated restarts | Restarted safely and validated after each rebuild | Fixed |
| `D-14` | Early floor values were too lax for real embeddings | Threshold tuning still reflected scaffold-era behavior | Finalized mode-aware tuned defaults by retrieval mode | Fixed |
| `D-15` | Dashboard Mermaid nodes rendered without visible text | CSS reset interfered with rendered SVG text | Scoped diagram styling so Mermaid output remained legible | Fixed |

## Critical Defects And Final Solutions

### `D-01` Honest Semantic Gating

Observed problem:

- weighted total score let recency and other signals rescue semantically weak
  candidates

Chosen solution:

- apply semantic-floor filtering before rerank
- keep explicit caller override support for diagnostics and parity testing
- expose retrieval policy and score breakdowns in explain output

Why this was the correct fix:

- weight tuning alone could not stop obviously irrelevant results
- hard floor behavior is debuggable and maps cleanly to operator controls

Preventive measure:

- every score-bearing response must retain `score_breakdown`

### `D-11` Provider Parity Across Surfaces

Observed problem:

- CLI, API, and dashboard could resolve different embedding providers for the
  same logical query path

Chosen solution:

- route all production provider construction through one shared factory
- use readiness probing before accepting the ONNX provider
- fall back to local only when ONNX truly fails readiness

Why this was the correct fix:

- parity bugs are cross-cutting and cannot be solved safely with local patches
- constructor success alone was not enough; real readiness had to be probed

Preventive measure:

- reject production code that constructs providers outside the shared factory

### `D-12` Write-Path Ownership Of Searchability

Observed problem:

- a successful write did not guarantee the memory was searchable
- lazy vector creation only appeared to work when the workspace was otherwise
  empty or queried in a specific way

Chosen solution:

- inject an embedder into `WritePipeline`
- eagerly embed and upsert vector rows during successful writes
- delete the just-written memory row if eager embedding or vector upsert fails

Why this was the correct fix:

- the write path is the only place that can guarantee immediate searchability
- lazy cache behavior is a fallback, not a correctness contract

Preventive measure:

- require write-to-search regression tests for any future changes to write flow

## Validation Performed

### Backend Validation

- Verified retrieval semantics, provider parity, and write-path correctness
  through package tests across:
  - `internal/engine`
  - `internal/api`
  - `internal/cli`
  - `internal/embeddings`
- Confirmed eager-write guarantees with focused regressions:
  - fresh writes immediately create vector rows
  - fresh writes are immediately searchable
  - eager embedding failures roll back memory and vector state
- Confirmed ONNX readiness probing fixes the factory fallback bug that previously
  accepted ONNX too early

### Frontend Validation

- Implemented dashboard semantic-primary UX in:
  - `tools/agent-memory/dashboard/src/ui/App.tsx`
  - `tools/agent-memory/dashboard/src/ui/styles.css`
- Verified the dashboard with:
  - `npm run typecheck`
  - `npm run build`

### Behavioral Validation

- Default search behavior now reflects the tuned semantic floor.
- Diagnostic lowering of `min_semantic_score` remains available without changing
  engine defaults.
- Dashboard result cards and detail drawer now surface
  `score_breakdown.semantic_similarity` as the primary relevance signal.

## Preventive Measures

- Keep one provider factory for all production entry points.
- Persist `embedding_provider` on every vector row.
- Treat eager write-path embedding as required behavior, not optional polish.
- Do not publish user-facing scores without component breakdown.
- Do not use `min_semantic_score = 0` as a production workaround.
- Validate runtime/model readiness before accepting a provider as healthy.
- Re-run parity validation whenever a new search surface is added.

## Lessons Learned

- Search quality regressions often come from multiple layers that reinforce one
  another; fixing only ranking weights usually hides the real issue.
- Placeholder embeddings create misleading confidence because the rest of the
  pipeline still looks production-like.
- Provider parity must be designed in, not cleaned up later.
- Lazy search-time repair is not a substitute for write-time correctness.
- UI honesty matters: blended score should not outrank semantic similarity in
  the operator's mental model.

## Follow-Ups

- Keep the canonical guide and this audit in sync when future search stack work
  changes rollout or validation expectations.
- Consider adding CI coverage for dashboard semantic-primary rendering behavior
  if frontend regression risk increases.
- Treat any future provider addition as a migration event requiring provenance,
  parity review, and write-path validation.
