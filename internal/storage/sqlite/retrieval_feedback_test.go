package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func insertTestMemory(t *testing.T, store *Store, id, workspace, content string) {
	t.Helper()
	now := time.Now().UTC()
	mem := &core.MemoryEntry{
		ID:          id,
		Type:        core.SemanticMemory,
		Content:     content,
		Workspace:   workspace,
		Source:      core.MemorySource{Type: core.SourceUserInput},
		StorageTier: core.TierVector,
		Confidence:  0.5,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	err := store.InsertMemoryByHash(context.Background(), mem, "hash-"+id)
	if err != nil {
		t.Fatalf("insert test memory %s: %v", id, err)
	}
}

// TestRetrievalFeedbackConcurrent verifies that N goroutines each sending M
// helpful feedback events on a single memory produces exactly N×M useful_count.
func TestRetrievalFeedbackConcurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrency test in short mode")
	}

	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "feedback_concurrent.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	memID := "mem-concurrent"
	insertTestMemory(t, store, memID, "ws", "concurrent feedback test")

	const numGoroutines = 8
	const eventsPerGoroutine = 20

	var wg sync.WaitGroup
	at := time.Now()

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < eventsPerGoroutine; i++ {
				_, err := store.ApplyRetrievalFeedback(ctx, memID, core.FeedbackHelpful, at)
				if err != nil {
					t.Errorf("ApplyRetrievalFeedback failed: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	// Verify final counter is exactly N*M.
	mem, err := store.GetMemory(ctx, memID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	expected := numGoroutines * eventsPerGoroutine
	if mem.UsefulCount != expected {
		t.Errorf("UsefulCount = %d, want %d", mem.UsefulCount, expected)
	}
	// SalienceScore should have been bumped by cumulative delta (capped at 1).
	if mem.SalienceScore > 1.0 {
		t.Errorf("SalienceScore = %f, want <= 1.0", mem.SalienceScore)
	}
}

// TestRetrievalFeedbackSerializedVsParallel verifies that serialized and parallel
// application of feedback yields equivalent counter values.
func TestRetrievalFeedbackSerializedVsParallel(t *testing.T) {
	ctx := context.Background()
	at := time.Now()

	// Serialized path.
	store1, err := Open(ctx, filepath.Join(t.TempDir(), "feedback_serial.db"))
	if err != nil {
		t.Fatalf("open store1: %v", err)
	}
	defer func() { _ = store1.Close() }()
	insertTestMemory(t, store1, "m1", "ws", "serial test")
	const total = 50
	for i := 0; i < total; i++ {
		_, err := store1.ApplyRetrievalFeedback(ctx, "m1", core.FeedbackHelpful, at)
		if err != nil {
			t.Fatalf("serial feedback %d: %v", i, err)
		}
	}
	serial, err := store1.GetMemory(ctx, "m1")
	if err != nil {
		t.Fatalf("GetMemory serial: %v", err)
	}

	// Parallel path.
	store2, err := Open(ctx, filepath.Join(t.TempDir(), "feedback_parallel.db"))
	if err != nil {
		t.Fatalf("open store2: %v", err)
	}
	defer func() { _ = store2.Close() }()
	insertTestMemory(t, store2, "m1", "ws", "parallel test")
	const numG = 5
	const perG = total / numG // 10 each
	var wg sync.WaitGroup
	for g := 0; g < numG; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				_, err := store2.ApplyRetrievalFeedback(ctx, "m1", core.FeedbackHelpful, at)
				if err != nil {
					t.Errorf("parallel feedback: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	parallel, err := store2.GetMemory(ctx, "m1")
	if err != nil {
		t.Fatalf("GetMemory parallel: %v", err)
	}

	if serial.UsefulCount != parallel.UsefulCount {
		t.Errorf("UsefulCount serial=%d parallel=%d", serial.UsefulCount, parallel.UsefulCount)
	}
}

// TestRetrievalFeedbackOnMissingMemory verifies that applying feedback to a
// non-existent memory returns no error (RowAffected==0 is benign).
func TestRetrievalFeedbackOnMissingMemory(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "feedback_missing.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	_, err = store.ApplyRetrievalFeedback(ctx, "non-existent-id", core.FeedbackHelpful, time.Now())
	if err != nil {
		t.Fatalf("feedback on missing memory should not error, got: %v", err)
	}
}

// TestReconsolidationSupersededInTransaction verifies superseded reconsolidation
// atomically updates counters, marks superseded, and adds a relation.
func TestReconsolidationSupersededInTransaction(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "recon_superseded.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Create both old (to be superseded) and successor memories.
	insertTestMemory(t, store, "old-mem", "ws", "old content")
	insertTestMemory(t, store, "successor-mem", "ws", "new content")

	at := time.Now()
	mem, err := store.ApplyReconsolidation(ctx, "old-mem", core.ReconsolidateSuperseded, "successor-mem", at)
	if err != nil {
		t.Fatalf("ApplyReconsolidation superseded: %v", err)
	}

	// Verify the old memory has superseded_by set.
	if mem.SupersededBy == nil || *mem.SupersededBy != "successor-mem" {
		t.Errorf("SupersededBy = %v, want successor-mem", mem.SupersededBy)
	}

	// Verify relation exists.
	rels, err := store.ListRelations(ctx, "successor-mem")
	if err != nil {
		t.Fatalf("GetRelations: %v", err)
	}
	found := false
	for _, r := range rels {
		if r.TargetID == "old-mem" && r.Type == core.RelSupersedes {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected supersedes relation from successor to old memory")
	}
}

// TestReconsolidationContradictedWithRelation verifies contradicted reconsolidation
// with a successor adds a contradicted relation.
func TestReconsolidationContradictedWithRelation(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "recon_contradict.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	insertTestMemory(t, store, "bad-mem", "ws", "stale fact")
	insertTestMemory(t, store, "good-mem", "ws", "corrected fact")

	_, err = store.ApplyReconsolidation(ctx, "bad-mem", core.ReconsolidateContradicted, "good-mem", time.Now())
	if err != nil {
		t.Fatalf("ApplyReconsolidation contradicted: %v", err)
	}

	rels, err := store.ListRelations(ctx, "good-mem")
	if err != nil {
		t.Fatalf("GetRelations: %v", err)
	}
	found := false
	for _, r := range rels {
		if r.TargetID == "bad-mem" && r.Type == core.RelContradicts {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected contradicts relation from successor to old memory")
	}
}

// TestFeedbackAllTypes verifies each feedback type applies correctly.
func TestFeedbackAllTypes(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "feedback_all.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	at := time.Now()

	tests := []struct {
		name     string
		feedback core.RetrievalFeedback
		checkFn  func(*testing.T, *core.MemoryEntry)
	}{
		{
			name:     "helpful",
			feedback: core.FeedbackHelpful,
			checkFn: func(t *testing.T, m *core.MemoryEntry) {
				if m.UsefulCount != 1 {
					t.Errorf("UsefulCount = %d, want 1", m.UsefulCount)
				}
				if m.FamiliarityBandLast != "strong_recall" {
					t.Errorf("FamiliarityBandLast = %s, want strong_recall", m.FamiliarityBandLast)
				}
			},
		},
		{
			name:     "ignored",
			feedback: core.FeedbackIgnored,
			checkFn: func(t *testing.T, m *core.MemoryEntry) {
				if m.IgnoredCount != 1 {
					t.Errorf("IgnoredCount = %d, want 1", m.IgnoredCount)
				}
				if m.FamiliarityBandLast != "weak_familiarity" {
					t.Errorf("FamiliarityBandLast = %s, want weak_familiarity", m.FamiliarityBandLast)
				}
			},
		},
		{
			name:     "rejected",
			feedback: core.FeedbackRejected,
			checkFn: func(t *testing.T, m *core.MemoryEntry) {
				if m.RejectedCount != 1 {
					t.Errorf("RejectedCount = %d, want 1", m.RejectedCount)
				}
				if m.FamiliarityBandLast != "suppressed" {
					t.Errorf("FamiliarityBandLast = %s, want suppressed", m.FamiliarityBandLast)
				}
			},
		},
		{
			name:     "harmful",
			feedback: core.FeedbackHarmful,
			checkFn: func(t *testing.T, m *core.MemoryEntry) {
				if m.HarmfulCount != 1 {
					t.Errorf("HarmfulCount = %d, want 1", m.HarmfulCount)
				}
				if m.FamiliarityBandLast != "suppressed" {
					t.Errorf("FamiliarityBandLast = %s, want suppressed", m.FamiliarityBandLast)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), fmt.Sprintf("feedback_%s.db", tc.name))
			s, err := Open(ctx, dbPath)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = s.Close() }()

			insertTestMemory(t, s, tc.name, "ws", "test "+tc.name)
			mem, err := s.ApplyRetrievalFeedback(ctx, tc.name, tc.feedback, at)
			if err != nil {
				t.Fatalf("ApplyRetrievalFeedback: %v", err)
			}
			tc.checkFn(t, mem)
		})
	}
}
