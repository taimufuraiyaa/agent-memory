package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
)

func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "memory.db")
	ctx := context.Background()

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Run migration a second time: should be a no-op.
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate second run should be idempotent: %v", err)
	}
}

func TestConcurrentUpserts(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "memory.db")
	ctx := context.Background()

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry := &core.MemoryEntry{
				ID:          fmt.Sprintf("m_%02d", i),
				Type:        core.SemanticMemory,
				Content:     "concurrent write",
				Workspace:   "ws",
				Source:      core.MemorySource{Type: core.SourceAgentObservation},
				Confidence:  0.9,
				StorageTier: core.TierVector,
			}
			if err := store.UpsertMemory(ctx, entry); err != nil {
				t.Errorf("upsert %d failed: %v", i, err)
			}
		}()
	}
	wg.Wait()

	count, err := store.CountMemories(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 20 {
		t.Fatalf("expected 20 rows, got %d", count)
	}
}

func TestRetrievalStateRoundTrip(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "memory.db")
	ctx := context.Background()

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	lastHelpful := time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC)
	lastRejected := lastHelpful.Add(2 * time.Hour)
	suppressionUntil := lastRejected.Add(24 * time.Hour)
	entry := &core.MemoryEntry{
		ID:                  "retrieval-state",
		Type:                core.SemanticMemory,
		Content:             "retrieval state should persist",
		Workspace:           "ws",
		Source:              core.MemorySource{Type: core.SourceAgentObservation},
		Confidence:          0.91,
		StorageTier:         core.TierVector,
		AccessCount:         7,
		DecayScore:          0.15,
		SalienceScore:       0.72,
		SuppressionScore:    0.18,
		UsefulCount:         3,
		IgnoredCount:        1,
		RejectedCount:       2,
		HarmfulCount:        1,
		LastHelpfulAt:       lastHelpful,
		LastRejectedAt:      lastRejected,
		SuppressionUntil:    &suppressionUntil,
		FamiliarityBandLast: "weak_familiarity",
	}
	if err := store.UpsertMemory(ctx, entry); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, getErr := store.GetMemory(ctx, entry.ID)
	if getErr != nil {
		t.Fatalf("get memory: %v", getErr)
	}
	if got.SalienceScore != entry.SalienceScore {
		t.Fatalf("expected salience score %f, got %f", entry.SalienceScore, got.SalienceScore)
	}
	if got.SuppressionScore != entry.SuppressionScore {
		t.Fatalf("expected suppression score %f, got %f", entry.SuppressionScore, got.SuppressionScore)
	}
	if got.UsefulCount != entry.UsefulCount || got.IgnoredCount != entry.IgnoredCount || got.RejectedCount != entry.RejectedCount || got.HarmfulCount != entry.HarmfulCount {
		t.Fatalf("unexpected retrieval counters: %+v", got)
	}
	if !got.LastHelpfulAt.Equal(lastHelpful) {
		t.Fatalf("expected last helpful %s, got %s", lastHelpful, got.LastHelpfulAt)
	}
	if !got.LastRejectedAt.Equal(lastRejected) {
		t.Fatalf("expected last rejected %s, got %s", lastRejected, got.LastRejectedAt)
	}
	if got.SuppressionUntil == nil || !got.SuppressionUntil.Equal(suppressionUntil) {
		t.Fatalf("expected suppression until %s, got %+v", suppressionUntil, got.SuppressionUntil)
	}
	if got.FamiliarityBandLast != entry.FamiliarityBandLast {
		t.Fatalf("expected familiarity band %q, got %q", entry.FamiliarityBandLast, got.FamiliarityBandLast)
	}
}

func TestMarkAccessedDoesNotRefreshUpdatedAt(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "memory.db")
	ctx := context.Background()

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	entry := &core.MemoryEntry{
		ID:          "access-split",
		Type:        core.SemanticMemory,
		Content:     "access should not change updated_at",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceAgentObservation},
		Confidence:  0.8,
		StorageTier: core.TierVector,
	}
	if err := store.UpsertMemory(ctx, entry); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	before, getErr := store.GetMemory(ctx, entry.ID)
	if getErr != nil {
		t.Fatalf("get before: %v", getErr)
	}

	accessedAt := before.UpdatedAt.Add(2 * time.Hour)
	if err := store.MarkAccessed(ctx, []string{entry.ID}, accessedAt); err != nil {
		t.Fatalf("mark accessed: %v", err)
	}
	after, afterErr := store.GetMemory(ctx, entry.ID)
	if afterErr != nil {
		t.Fatalf("get after: %v", afterErr)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("expected updated_at to stay at %s, got %s", before.UpdatedAt, after.UpdatedAt)
	}
	if !after.LastAccessedAt.Equal(accessedAt) {
		t.Fatalf("expected last_accessed_at %s, got %s", accessedAt, after.LastAccessedAt)
	}
	if after.AccessCount != before.AccessCount+1 {
		t.Fatalf("expected access_count %d, got %d", before.AccessCount+1, after.AccessCount)
	}
}

func TestApplyRetrievalFeedbackAndReconsolidation(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "feedback.db")
	ctx := context.Background()

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	base := &core.MemoryEntry{
		ID:          "feedback-memory",
		Type:        core.SemanticMemory,
		Content:     "orders rollout guide",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceAgentObservation},
		Confidence:  0.8,
		StorageTier: core.TierVector,
	}
	successor := &core.MemoryEntry{
		ID:          "feedback-successor",
		Type:        core.SemanticMemory,
		Content:     "orders rollout guide v2",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		Confidence:  0.95,
		StorageTier: core.TierVector,
	}
	for _, entry := range []*core.MemoryEntry{base, successor} {
		if err := store.UpsertMemory(ctx, entry); err != nil {
			t.Fatalf("upsert %s: %v", entry.ID, err)
		}
	}

	at := time.Date(2026, 5, 21, 14, 0, 0, 0, time.UTC)
	afterHelpful, err := store.ApplyRetrievalFeedback(ctx, base.ID, core.FeedbackHelpful, at)
	if err != nil {
		t.Fatalf("helpful feedback: %v", err)
	}
	if afterHelpful.UsefulCount != 1 || afterHelpful.SalienceScore <= 0 {
		t.Fatalf("expected helpful feedback to raise useful_count and salience, got %+v", afterHelpful)
	}

	afterRejected, err := store.ApplyRetrievalFeedback(ctx, base.ID, core.FeedbackRejected, at.Add(time.Hour))
	if err != nil {
		t.Fatalf("rejected feedback: %v", err)
	}
	if afterRejected.RejectedCount != 1 || afterRejected.SuppressionScore <= 0 {
		t.Fatalf("expected rejected feedback to raise suppression, got %+v", afterRejected)
	}
	if afterRejected.SuppressionUntil == nil {
		t.Fatalf("expected rejection cooldown")
	}

	recon, err := store.ApplyReconsolidation(ctx, base.ID, core.ReconsolidateSuperseded, successor.ID, at.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("superseded reconsolidation: %v", err)
	}
	if recon.SupersededBy == nil || *recon.SupersededBy != successor.ID {
		t.Fatalf("expected superseded_by to be set, got %+v", recon.SupersededBy)
	}
	rels, err := store.ListRelations(ctx, successor.ID)
	if err != nil {
		t.Fatalf("list relations: %v", err)
	}
	found := false
	for _, rel := range rels {
		if rel.TargetID == base.ID && rel.Type == core.RelSupersedes {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected supersedes relation from successor to original memory")
	}
}

func TestFeedbackSafeguardsForPinnedAndFailureMemories(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "feedback-safeguards.db")
	ctx := context.Background()

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	pinned := &core.MemoryEntry{
		ID:          "pinned-memory",
		Type:        core.SemanticMemory,
		Content:     "critical deployment checklist",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		Confidence:  0.9,
		StorageTier: core.TierMarkdown,
		Pinned:      true,
	}
	failure := &core.MemoryEntry{
		ID:          "failure-memory",
		Type:        core.OutcomeMemory,
		Content:     "rollback order caused outage",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceAgentObservation},
		Confidence:  1,
		StorageTier: core.TierVector,
		Outcome: &core.Outcome{
			Result: core.OutcomeFailure,
			Reason: "bad rollback sequence",
		},
	}
	for _, entry := range []*core.MemoryEntry{pinned, failure} {
		if err := store.UpsertMemory(ctx, entry); err != nil {
			t.Fatalf("upsert %s: %v", entry.ID, err)
		}
	}

	at := time.Date(2026, 5, 21, 18, 0, 0, 0, time.UTC)
	pinnedAfter, err := store.ApplyRetrievalFeedback(ctx, pinned.ID, core.FeedbackHarmful, at)
	if err != nil {
		t.Fatalf("pinned feedback: %v", err)
	}
	if pinnedAfter.SuppressionUntil != nil {
		t.Fatalf("pinned memory should not receive cooldown suppression, got %+v", pinnedAfter.SuppressionUntil)
	}

	failureAfter, err := store.ApplyRetrievalFeedback(ctx, failure.ID, core.FeedbackRejected, at)
	if err != nil {
		t.Fatalf("failure feedback: %v", err)
	}
	if failureAfter.SuppressionUntil != nil {
		t.Fatalf("failure memory should not receive cooldown suppression, got %+v", failureAfter.SuppressionUntil)
	}
	if failureAfter.RejectedCount != 1 {
		t.Fatalf("expected failure rejected count to increment, got %d", failureAfter.RejectedCount)
	}
}

func TestFeedbackCooldownRuntimeOverrides(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "feedback-runtime-overrides.db")
	ctx := context.Background()

	t.Setenv("AGENT_MEMORY_ADAPTIVE_FEEDBACK_COOLDOWNS", `{"rejected_cooldown":"2h","contradicted_cooldown":"45m"}`)

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	base := &core.MemoryEntry{
		ID:          "runtime-base",
		Type:        core.SemanticMemory,
		Content:     "rollout checklist",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceAgentObservation},
		Confidence:  0.8,
		StorageTier: core.TierVector,
	}
	successor := &core.MemoryEntry{
		ID:          "runtime-successor",
		Type:        core.SemanticMemory,
		Content:     "rollout checklist corrected",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		Confidence:  0.95,
		StorageTier: core.TierVector,
	}
	for _, entry := range []*core.MemoryEntry{base, successor} {
		if err := store.UpsertMemory(ctx, entry); err != nil {
			t.Fatalf("upsert %s: %v", entry.ID, err)
		}
	}

	at := time.Date(2026, 5, 21, 20, 0, 0, 0, time.UTC)
	rejected, err := store.ApplyRetrievalFeedback(ctx, base.ID, core.FeedbackRejected, at)
	if err != nil {
		t.Fatalf("rejected feedback: %v", err)
	}
	if rejected.SuppressionUntil == nil || !rejected.SuppressionUntil.Equal(at.Add(2*time.Hour)) {
		t.Fatalf("expected rejected cooldown override, got %+v", rejected.SuppressionUntil)
	}

	contradictedAt := at.Add(time.Hour)
	contradicted, err := store.ApplyReconsolidation(ctx, base.ID, core.ReconsolidateContradicted, successor.ID, contradictedAt)
	if err != nil {
		t.Fatalf("contradicted reconsolidation: %v", err)
	}
	if contradicted.SuppressionUntil == nil || !contradicted.SuppressionUntil.Equal(contradictedAt.Add(45*time.Minute)) {
		t.Fatalf("expected contradicted cooldown override, got %+v", contradicted.SuppressionUntil)
	}
}
