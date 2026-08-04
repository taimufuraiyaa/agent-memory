---
name: retrieval-integrity-audit
description: Verify or restore the correctness invariants of agent-memory retrieval/decay (decay clock, score mixing, Bloom trigger accounting, turbovec index mirroring, CLI workspace validation, eviction ordering). Use when debugging why decay/eviction seems dead, when Bloom gate or exact-term search misbehaves, when retrieval scores look wrong, or before shipping changes to internal/engine, internal/storage/sqlite, or internal/cli/transport.go.
---

# Retrieval & Decay Integrity Audit

Six invariants were broken in the pre-2026-08-04 codebase and fixed in the P0 package
(see `.kiro/audits/2026-08-04-retrieval-integrity-audit.md` and
`.kiro/specs/retrieval-integrity/`). Each invariant has a canonical regression test.
Use this skill to (a) verify the invariants still hold after any change, or
(b) debug a symptom that maps to one of them.

## Invariants and canonical tests

1. **Decay age base is monotone under maintenance.**
   `SetDecayScores` must NEVER write `updated_at` (it did — resetting the age base on
   every run, capping decay at ~0.094 and making the 0.65 eviction gate unreachable).
   `ComputeDecayScore` ages from `m.UpdatedAt` (`internal/engine/decay.go`).
   Test: `TestDecayAccumulatesAcrossRuns` (`internal/engine/decay_test.go`) — two runs
   with an advancing clock must strictly increase `decay_score` and leave `updated_at`
   unchanged.

2. **Bloom gate fails open on any term change; rebuild pressure = true evictions only.**
   Triggers on `memory_terms` (INSERT/DELETE) dirty `term_index_state` and bump
   `corpus_generation`. `stale_delete_count` must be incremented ONLY by
   `trg_memories_delete_pressure` (AFTER DELETE ON memories) — never by routine term
   replacement (that inflated it and caused spurious rebuilds at threshold 100).
   Tests: `TestRoutineTermReplacementDoesNotInflateStaleDeleteCount`,
   `TestCascadeDeleteDirtiesTermIndexGeneration` (`internal/storage/sqlite/term_index_test.go`).

3. **In-memory turbovec index mirrors committed vector writes/deletes.**
   Every vector write path (`UpsertMemoryVector`, `InsertMemoryByHashWithVector`) and
   delete path (`DeleteByIDs`, `DeleteByIDsAudited`) must update `turbovecIndex`
   AFTER commit (post-commit keeps rollback consistent).
   Test: `TestTurbovecIndexIntegration` (`internal/storage/sqlite/turbovec_test.go`).

4. **CLI workspace validation at the boundary.**
   `resolveWorkspace` (`internal/cli/transport.go`) validates flag/env names with
   `validation.ValidateWorkspaceName`; cwd-derived names are exempt (`filepath.Base`
   cannot contain separators). Without this, `--workspace ../../x` reaches
   `~/.agent-memory/../../x.db` on read commands.
   Test: `TestResolveWorkspaceValidation` (`internal/cli/transport_test.go`).

5. **Retrieval mixes range-consistent signals.**
   Cosine ∈ [-1,1] must be clamped to [0,1] before the weighted activation in BOTH
   `Retrieve` and `retrieveGraphExpand` (`internal/engine/retrieval.go`); the
   breakdown keeps raw cosine. Under default policy the 0.30 `MinSemanticScore` gate
   filters negative cosine anyway — the clamp is defense-in-depth + calibration.
   Test: `TestRetrievalClampsNegativeSemanticContribution` (`internal/engine/retrieval_test.go`)
   — stub provider returns negative-cosine; total must equal the non-semantic sum.

6. **Eviction targets most-decayed first.**
   `applyEvictionPromotion` (`internal/engine/lifecycle.go`) selects candidates via a
   stable sort by `DecayScore` DESC; gate 0.65 and pin exemption unchanged.
   Test: `TestEvictionPrefersMostDecayed` (`internal/engine/lifecycle_test.go`) —
   drive `applyEvictionPromotion` directly (NOT `Run`, which recomputes decay first
   and erases seeded scores).

## Verification workflow

1. Run the targeted tests:
   `go test ./internal/engine/ -run 'TestDecayAccumulatesAcrossRuns|TestEvictionPrefersMostDecayed|TestRetrievalClampsNegativeSemanticContribution' -v`
   `go test ./internal/storage/sqlite/ -run 'TestRoutineTermReplacement|TestCascadeDelete|TestTurbovecIndexIntegration' -v`
   `go test ./internal/cli/ -run TestResolveWorkspaceValidation -v`
2. Run the packages: `go test ./internal/engine/ ./internal/storage/sqlite/... ./internal/cli/`
3. If a full run fails, check the root `install_test.go` first: it scans
   `tools/agent-memory/menubar/.build/` for developer-specific home paths and fails on
   stale build artifacts — unrelated to engine/storage/cli code.

## Debugging symptom map

| Symptom | Likely invariant | Check |
|---|---|---|
| decay_score ~0.000x for old memories; eviction never fires | #1 | `SetDecayScores` SQL must not contain `updated_at` |
| Spurious rebuilds / `stale_delete_pressure` after ~34 writes | #2 | trigger still counts term replacement |
| New memory invisible to search (turbovec backend) | #3 | index update after commit in insert path |
| `--workspace ../x` opens files outside data dir | #4 | validation in `resolveWorkspace` |
| Totals look dragged by irrelevant hits; threshold games | #5 | clamp present in both paths |
| Eviction picks fresh memories | #6 | sort key = `DecayScore` DESC |

## Known accepted behaviors (do not "fix" blindly)

- `SetPinned` / `UpdateTier` still write `updated_at` (pin/tier actions reset decay
  age — intended strengthening semantics; a future `last_activity_at` clock may split it).
- Every term change dirties the Bloom snapshot, so gate mode is dead in write-active
  workspaces — conservative by design; incremental bitmap updates are FUTURE work.
- `h2 % m == 0` Bloom degeneracy guard is FUTURE work (`term_bloom.go`).
