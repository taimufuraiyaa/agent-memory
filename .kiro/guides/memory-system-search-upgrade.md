# Memory System Search Upgrade Playbook

## Purpose

This guide captures the repeatable diagnosis-and-fix flow used to repair the
search-quality stack in `agent-memory`. It is written as a reusable playbook for
similar memory systems where "search feels wrong" actually comes from several
stacked defects instead of one bad ranking tweak.

## What This Upgrade Fixes

The full repair has seven goals:

1. make semantic relevance an explicit gate instead of a weak contributor
2. preserve explainability through score breakdowns and policy snapshots
3. track vector provenance so provider migrations are auditable
4. replace placeholder embeddings with a real local semantic model
5. ensure every production surface resolves embeddings through one factory
6. eagerly persist vectors during writes so new memories are searchable at once
7. expose semantic similarity honestly in the dashboard

## Core Invariants

Keep these invariants true before, during, and after rollout:

- Semantic similarity gates visibility. Non-semantic signals may refine order,
  but they must not rescue semantic noise.
- One provider factory controls every production entry point.
- Every persisted vector records its `embedding_provider`.
- A fresh write is discoverable on the next search without `re-embed`.
- Every score-bearing response exposes a `score_breakdown`.
- Fallback behavior is visible enough that operators notice degraded quality.
- The dashboard reflects semantic similarity as the primary relevance signal.

## Recommended Rollout Order

Do the work in this order. Skipping ahead makes the system harder to diagnose.

1. Normalize the spec and resolve contradictions.
2. Verify retrieval floors, overrides, and explain output.
3. Add vector provenance (`embedding_provider`) and migration-safe storage.
4. Add `re-embed` so corpus migrations are repairable.
5. Replace the placeholder embedder with a real ONNX MiniLM provider.
6. Harden runtime and model install flow for restricted environments.
7. Centralize provider construction behind one shared factory.
8. Add eager embedding in the write pipeline and update write call sites.
9. Update the dashboard to make semantic similarity primary.
10. Publish the final playbook and audit.

## Repair Pattern By Layer

### 1. Retrieval Baseline

Symptoms:

- irrelevant memories appear with respectable total scores
- lowering the floor returns noisy results, but default behavior still "looks"
  acceptable in the UI

Required fixes:

- apply a mode-aware semantic floor in the retrieval engine
- keep caller override support for diagnostics
- return retrieval policy snapshots and score breakdowns

Final tuned defaults used here:

- `search = 0.30`
- `recall = 0.25`
- `relate = 0.35`
- `outcomes = 0.15`

### 2. Vector Provenance And Migration

Symptoms:

- mixed corpora cannot be audited
- provider swap quality is impossible to reason about

Required fixes:

- add `embedding_provider` to `memory_vectors`
- make vector upserts provider-aware
- add inventory/report helpers for migration and validation
- ship `re-embed` before changing providers in production

### 3. Real Embeddings

Symptoms:

- topically similar text scores like weak lexical overlap
- strict floors collapse result count, while loose floors admit junk

Required fixes:

- use a real local model: ONNX MiniLM-L6-v2
- implement tokenizer, forward pass, mean pooling, and L2 normalization
- use a stable provider name such as `onnx-minilm`
- keep fallback explicit, not silent

### 4. Provider Parity

Symptoms:

- CLI, API, dashboard, or migration routes produce different semantic values
- one surface silently keeps using a placeholder provider

Required fixes:

- route all production provider construction through one factory
- reject direct `NewLocalProvider(...)` style production wiring
- validate readiness before accepting the real provider
- fall back only when readiness genuinely fails

### 5. Write-Path Ownership

Symptoms:

- newly written memories do not show up until manual `re-embed`
- vector cache behavior depends on what else already exists in the workspace

Required fixes:

- inject an embedder into `WritePipeline`
- eagerly embed and persist vector rows on successful writes
- roll back the memory row if eager embedding fails
- update every production write surface together

### 6. Dashboard Honesty

Symptoms:

- users trust a blended score that hides weak semantic similarity
- operators cannot easily experiment with floor behavior

Required fixes:

- show `semantic_similarity` as the primary signal
- keep total/blended score secondary
- add semantic-only relevance buckets:
  - `High >= 0.55`
  - `Medium >= 0.40`
  - `Low >= 0.30`
  - `Weak < 0.30`
- expose the `min_semantic_score` control as a slider aligned with backend
  behavior

## Validation Checklist

Run these checks after each rollout phase and again before closeout.

### Provider parity

- Same query and same memory id produce matching semantic similarity across CLI
  and API when using the same overrides.
- Dashboard-backed search does not drift from in-process CLI search.

### Vector coverage

- No vector rows are missing `embedding_provider`.
- Mixed-provider states are either expected during migration or repaired
  immediately after.

### Self-similarity

- A short prefix query for a known memory retrieves that same memory with strong
  semantic similarity under the real provider.

### Honest floor

- A nonsense query returns no default-floor results.
- The same query with `min_semantic_score = 0` reveals only weak candidates for
  diagnostics.

### Write round-trip

- A newly written sentinel memory is immediately searchable without `re-embed`.
- Eager embedding failure does not leave orphaned memory or vector rows.

### Dashboard surface

- Relevance pill matches `score_breakdown.semantic_similarity`.
- Slider default matches the effective backend default.
- Lowering the slider exposes the same weak matches seen through CLI/API
  diagnostics.

## Anti-Patterns To Reject

- A new production entry point constructs its own provider.
- A user-facing score appears without its component breakdown.
- New writes rely on lazy-only vector persistence.
- Production defaults set `min_semantic_score = 0` to "get results back".
- Vector rows omit provider provenance.
- Fallback to a scaffold provider is quiet enough to go unnoticed.
- Tests pass only because they disable the semantic floor.

## Reuse Checklist

Use this when porting the playbook to another memory system.

- Storage can join memory rows and vector rows by a stable id.
- Vector rows store provider provenance and update time.
- Retrieval has per-mode defaults plus explicit request override.
- Every score-bearing response returns a breakdown object.
- CLI and API both expose semantic-floor controls.
- Write pipeline supports eager vector persistence.
- Migration tooling exists for provider changes.
- Installer/runtime flow validates external artifacts beyond HTTP status alone.
- Dashboard or operator UI exposes semantic similarity directly.

## Final State In Agent-Memory

This project now ships the full chain:

- retrieval-side semantic floors with explainability
- provider-aware vector storage and migration support
- real ONNX MiniLM embeddings with tokenizer/runtime integration
- shared provider resolution across CLI, API, dashboard, and migration flows
- eager write-path embeddings with rollback on failure
- semantic-primary dashboard UX with relevance buckets and slider control

Use this guide as the canonical cross-cutting reference for future memory-system
search upgrades.
