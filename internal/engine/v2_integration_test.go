package engine

// T35 - V2 Integration Tests
// Covers the full hippocampus enforcement loop introduced in v2:
//   - Recall → work → consolidation gate write-back
//   - Failure bypass (always written regardless of confidence)
//   - Confidence gate routing (high / medium / low)
//   - Deep consolidation cross-session pattern detection

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// TestV2_RecallThenConsolidateLoop simulates the full hippocampus loop:
// 1. Agent learns a fact (consolidation gate writes it)
// 2. Next session: recall gate finds it and injects it
func TestV2_RecallThenConsolidateLoop(t *testing.T) {
	ctx := context.Background()
	store := mustOpenStore(t)
	t.Cleanup(func() { _ = store.Close() })

	pipe := NewWritePipeline(store)

	// --- Session 1: agent learns a fact (consolidation gate fires) ---
	res, err := pipe.Write(ctx, WriteInput{
		Workspace: "ws",
		Type:      core.SemanticMemory,
		Content:   "OPS service publishes to decisions.events after processing orders",
		Source:    core.MemorySource{Type: core.SourceAgentObservation},
	})
	if err != nil {
		t.Fatalf("session 1 write: %v", err)
	}
	if res.Rejected {
		t.Fatalf("session 1 write rejected: %s", res.RejectReason)
	}
	if res.ID == "" {
		t.Fatalf("expected memory ID after write")
	}

	// --- Session 2: recall gate fires, should find the fact ---
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := ensureEmbeddingsDir(modelDir); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	retrieval := NewRetrievalEngine(NewVectorSearcher(store, provider))
	hits, err := retrieval.Retrieve(ctx, RetrievalOptions{
		Workspace: "ws",
		Query:     "OPS service decisions events",
		TopK:      8,
		Mode:      ModeSearch,
	})
	if err != nil {
		t.Fatalf("recall gate search: %v", err)
	}
	if len(hits.Hits) == 0 {
		t.Fatalf("recall gate: expected at least one hit for the written fact")
	}

	found := false
	for _, h := range hits.Hits {
		if strings.Contains(h.Memory.Content, "decisions.events") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("recall gate: expected to find the written fact in results")
	}
}

// TestV2_FailureAlwaysWritten verifies that failure outcomes bypass the
// confidence gate and are always stored regardless of confidence score.
func TestV2_FailureAlwaysWritten(t *testing.T) {
	ctx := context.Background()
	store := mustOpenStore(t)
	t.Cleanup(func() { _ = store.Close() })

	pipe := NewWritePipeline(store)

	// Write a failure outcome — should always be stored
	res, err := pipe.Write(ctx, WriteInput{
		Workspace: "ws",
		Type:      core.OutcomeMemory,
		Content:   "tried direct binary serializer approach",
		Source:    core.MemorySource{Type: core.SourceAgentObservation},
		Outcome: &core.Outcome{
			Result:   core.OutcomeFailure,
			Approach: "direct binary serializer",
			Reason:   "causes reflection overhead and breaks under generics",
		},
	})
	if err != nil {
		t.Fatalf("failure write: %v", err)
	}
	if res.Rejected {
		t.Fatalf("failure outcome must never be rejected, got: %s", res.RejectReason)
	}
	if res.ID == "" {
		t.Fatalf("expected memory ID for failure outcome")
	}
	// Confidence should be 1.0 (failure bypass)
	if res.Confidence != 1.0 {
		t.Fatalf("expected confidence=1.0 for failure bypass, got %.2f", res.Confidence)
	}

	// Verify it's actually in the store
	memories, err := store.ListMemoriesByWorkspace(ctx, "ws")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, m := range memories {
		if m.ID == res.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("failure outcome not found in store after write")
	}
}

// TestV2_ConfidenceGateRouting verifies the three confidence bands.
func TestV2_ConfidenceGateRouting(t *testing.T) {
	ctx := context.Background()

	t.Run("high confidence stored without tag", func(t *testing.T) {
		store := mustOpenStore(t)
		t.Cleanup(func() { _ = store.Close() })
		pipe := NewWritePipeline(store)

		// Direct observation from code analysis → base 0.8 → high band
		res, err := pipe.Write(ctx, WriteInput{
			Workspace: "ws",
			Type:      core.SemanticMemory,
			Content:   "auth service validates JWT tokens using RS256",
			Source:    core.MemorySource{Type: core.SourceCodeAnalysis},
		})
		if err != nil {
			t.Fatalf("write: %v", err)
		}
		if res.Rejected {
			t.Fatalf("high-confidence write should not be rejected")
		}
		for _, tag := range getStoredTags(t, store, ctx, res.ID) {
			if tag == tagLowConfidence {
				t.Fatalf("high-confidence write should not have low-confidence tag")
			}
		}
	})

	t.Run("reconstruction source starts at medium band", func(t *testing.T) {
		store := mustOpenStore(t)
		t.Cleanup(func() { _ = store.Close() })
		pipe := NewWritePipeline(store)

		// Reconstruction source → base 0.5 → medium band → stored with low-confidence tag
		res, err := pipe.Write(ctx, WriteInput{
			Workspace: "ws",
			Type:      core.SemanticMemory,
			Content:   "payment service might use stripe integration",
			Source:    core.MemorySource{Type: core.SourceReconstruction},
		})
		if err != nil {
			t.Fatalf("write: %v", err)
		}
		if res.Rejected {
			t.Fatalf("medium-confidence write should be stored (not rejected)")
		}
		tags := getStoredTags(t, store, ctx, res.ID)
		hasLowConf := false
		for _, tag := range tags {
			if tag == tagLowConfidence {
				hasLowConf = true
				break
			}
		}
		if !hasLowConf {
			t.Fatalf("medium-confidence write should have low-confidence tag, got tags: %v", tags)
		}
	})

	t.Run("failure outcome bypasses confidence gate", func(t *testing.T) {
		store := mustOpenStore(t)
		t.Cleanup(func() { _ = store.Close() })
		pipe := NewWritePipeline(store)

		res, err := pipe.Write(ctx, WriteInput{
			Workspace: "ws",
			Type:      core.OutcomeMemory,
			Content:   "attempted approach X",
			Source:    core.MemorySource{Type: core.SourceReconstruction}, // low base
			Outcome: &core.Outcome{
				Result:   core.OutcomeFailure,
				Approach: "approach X",
				Reason:   "caused deadlock",
			},
		})
		if err != nil {
			t.Fatalf("write: %v", err)
		}
		if res.Rejected {
			t.Fatalf("failure outcome must never be rejected regardless of source type")
		}
		if res.Confidence != 1.0 {
			t.Fatalf("expected confidence=1.0 for failure bypass, got %.2f", res.Confidence)
		}
	})
}

// TestV2_DeepConsolidationPromotesRepeatedFailures verifies that 3+ failures
// with the same approach across sessions are promoted to a procedural rule.
func TestV2_DeepConsolidationPromotesRepeatedFailures(t *testing.T) {
	ctx := context.Background()
	store := mustOpenStore(t)
	t.Cleanup(func() { _ = store.Close() })

	pipe := NewWritePipeline(store)

	// Write 3 failure outcomes with the same approach but unique content
	// (unique content avoids dedup — each is a distinct session observation)
	approach := "direct SQL string concatenation"
	reasons := []string{
		"SQL injection vulnerability found in session 1",
		"SQL injection vulnerability found in session 2",
		"SQL injection vulnerability found in session 3",
	}
	for i, reason := range reasons {
		sid := sessionID(i)
		_, err := pipe.Write(ctx, WriteInput{
			Workspace: "ws",
			Type:      core.OutcomeMemory,
			Content:   reason, // unique per write to avoid dedup
			Source:    core.MemorySource{Type: core.SourceAgentObservation, SessionID: sid},
			Outcome: &core.Outcome{
				Result:   core.OutcomeFailure,
				Approach: approach,
				Reason:   "SQL injection vulnerability",
			},
		})
		if err != nil {
			t.Fatalf("write failure %d: %v", i, err)
		}
	}

	// Run deep consolidation
	dc := NewDeepConsolidationEngine(store, pipe)
	result, err := dc.Run(ctx, DeepConsolidationOptions{
		Workspace: "ws",
		DaysBack:  30,
		DryRun:    false,
		Mode:      MergeFast,
	})
	if err != nil {
		t.Fatalf("deep consolidation: %v", err)
	}
	if result.ProceduralPromoted == 0 {
		t.Fatalf("expected at least one procedural promotion from repeated failures, got 0")
	}

	// Verify the procedural rule was written to the store
	memories, err := store.ListMemoriesByWorkspace(ctx, "ws")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, m := range memories {
		if m.Type == core.ProceduralMemory && strings.Contains(m.Content, "Avoid approach") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected procedural 'Avoid approach' rule in store after deep consolidation")
	}
}

// TestV2_DeepConsolidationDryRun verifies --dry-run produces output without writing.
func TestV2_DeepConsolidationDryRun(t *testing.T) {
	ctx := context.Background()
	store := mustOpenStore(t)
	t.Cleanup(func() { _ = store.Close() })

	pipe := NewWritePipeline(store)

	// Seed 3 failures
	for i := 0; i < 3; i++ {
		_, _ = pipe.Write(ctx, WriteInput{
			Workspace: "ws",
			Type:      core.OutcomeMemory,
			Content:   "tried approach Y",
			Source:    core.MemorySource{Type: core.SourceAgentObservation},
			Outcome: &core.Outcome{
				Result:   core.OutcomeFailure,
				Approach: "approach Y",
				Reason:   "too slow",
			},
		})
	}

	beforeCount := countMemories(t, store, ctx, "ws")

	dc := NewDeepConsolidationEngine(store, pipe)
	result, err := dc.Run(ctx, DeepConsolidationOptions{
		Workspace: "ws",
		DaysBack:  30,
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !result.DryRun {
		t.Fatalf("expected DryRun=true in result")
	}

	afterCount := countMemories(t, store, ctx, "ws")
	if afterCount != beforeCount {
		t.Fatalf("dry-run must not write anything: before=%d after=%d", beforeCount, afterCount)
	}
}

// TestV2_NoV1Regressions runs a quick smoke test of the core V1 write/recall
// path to ensure v2 changes did not break existing behaviour.
func TestV2_NoV1Regressions(t *testing.T) {
	ctx := context.Background()
	store := mustOpenStore(t)
	t.Cleanup(func() { _ = store.Close() })

	pipe := NewWritePipeline(store)

	// Dedup still works
	in := WriteInput{
		Workspace: "ws",
		Type:      core.SemanticMemory,
		Content:   "kafka topic orders.events is consumed by OPS",
		Source:    core.MemorySource{Type: core.SourceCodeAnalysis},
	}
	first, err := pipe.Write(ctx, in)
	if err != nil || first.Rejected {
		t.Fatalf("first write: err=%v rejected=%v", err, first.Rejected)
	}
	second, err := pipe.Write(ctx, in)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if !second.Deduplicated {
		t.Fatalf("dedup regression: expected deduplicated=true")
	}
	if first.ID != second.ID {
		t.Fatalf("dedup regression: expected same ID")
	}

	// Secret redaction still works (secrets are now redacted, not rejected,
	// since redaction runs before validation in the write pipeline).
	secret, err := pipe.Write(ctx, WriteInput{
		Workspace: "ws",
		Type:      core.SemanticMemory,
		Content:   "api_key = AKIAIOSFODNN7EXAMPLE",
		Source:    core.MemorySource{Type: core.SourceUserInput},
	})
	if err != nil {
		t.Fatalf("secret write: %v", err)
	}
	if secret.Rejected {
		t.Fatalf("secret should be redacted and written, not rejected: %s", secret.RejectReason)
	}
	mem, err := store.GetMemoryByHash(ctx, "ws", secret.ContentHash)
	if err != nil {
		t.Fatalf("get memory: %v", err)
	}
	if mem == nil {
		t.Fatal("expected redacted memory to exist")
	}
	if strings.Contains(mem.Content, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("expected AWS key to be redacted, got: %s", mem.Content)
	}

	// Session-end extraction still works
	extractor := NewSessionEndExtractor(pipe)
	out, err := extractor.ExtractAndStore(ctx, "ws", "fixed the retry logic, result was success")
	if err != nil {
		t.Fatalf("session-end: %v", err)
	}
	if out == nil {
		t.Fatalf("session-end returned nil")
	}
}

// --- helpers ---

func getStoredTags(t *testing.T, store *sqlite.Store, ctx context.Context, id string) []string {
	t.Helper()
	if id == "" {
		return nil
	}
	memories, err := store.ListMemoriesByWorkspace(ctx, "ws")
	if err != nil {
		t.Fatalf("list for tags: %v", err)
	}
	for _, m := range memories {
		if m.ID == id {
			return m.Tags
		}
	}
	return nil
}

func countMemories(t *testing.T, store *sqlite.Store, ctx context.Context, ws string) int {
	t.Helper()
	memories, err := store.ListMemoriesByWorkspace(ctx, ws)
	if err != nil {
		t.Fatalf("count memories: %v", err)
	}
	return len(memories)
}

func sessionID(i int) string {
	return strings.Repeat("0", 7) + string(rune('a'+i))
}
