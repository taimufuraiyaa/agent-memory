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

func TestListMemoryLightweightForInferenceRecent(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "inference-recent.db")
	ctx := context.Background()

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed := func(id string, createdAt time.Time, tier core.StorageTier, entities []string) {
		if err := store.UpsertMemory(ctx, &core.MemoryEntry{
			ID:          id,
			Type:        core.SemanticMemory,
			Content:     "content " + id,
			Workspace:   "ws",
			Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
			StorageTier: tier,
			Confidence:  0.9,
			Entities:    entities,
			CreatedAt:   createdAt,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	// m3 is cold-tier and should never be returned. The rest are ordered
	// oldest (m1) to newest (m4).
	seed("m1", base, core.TierVector, []string{"alpha"})
	seed("m2", base.Add(time.Minute), core.TierVector, []string{"beta"})
	seed("m3", base.Add(2*time.Minute), core.TierCold, []string{"gamma"})
	seed("m4", base.Add(3*time.Minute), core.TierVector, []string{"delta"})

	// A limit of 2 should return the 2 most recently created non-cold
	// memories, most-recent first, skipping the cold-tier one entirely.
	out, err := store.ListMemoryLightweightForInferenceRecent(ctx, "ws", 2)
	if err != nil {
		t.Fatalf("list recent: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 results, got %d", len(out))
	}
	if out[0].ID != "m4" || out[1].ID != "m2" {
		t.Fatalf("expected [m4, m2], got [%s, %s]", out[0].ID, out[1].ID)
	}
	for _, m := range out {
		if m.StorageTier == core.TierCold {
			t.Fatalf("expected cold-tier memory to be excluded, got %+v", m)
		}
	}

	// A non-positive limit falls back to the default cap, which still
	// excludes cold-tier memories.
	all, err := store.ListMemoryLightweightForInferenceRecent(ctx, "ws", 0)
	if err != nil {
		t.Fatalf("list recent with default limit: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 non-cold results, got %d", len(all))
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

func TestWorkspaceActivitySummaryAndSchedulerRetention(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "scheduler.db")
	ctx := context.Background()

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	entry := &core.MemoryEntry{
		ID:          "scheduler-memory",
		Type:        core.SemanticMemory,
		Content:     "scheduler status uses lightweight workspace activity",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceAgentObservation},
		Confidence:  0.9,
		StorageTier: core.TierVector,
	}
	if err := store.UpsertMemory(ctx, entry); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	accessedAt := time.Date(2026, 6, 8, 10, 30, 0, 0, time.UTC)
	if err := store.MarkAccessed(ctx, []string{entry.ID}, accessedAt); err != nil {
		t.Fatalf("mark accessed: %v", err)
	}

	summary, err := store.GetWorkspaceActivitySummary(ctx, "ws")
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.MemoryCount != 1 {
		t.Fatalf("expected 1 memory, got %+v", summary)
	}
	if !summary.LastAccessedAt.Equal(accessedAt) {
		t.Fatalf("expected last accessed %s, got %s", accessedAt, summary.LastAccessedAt)
	}

	completedAt := time.Date(2026, 6, 8, 11, 0, 0, 0, time.UTC)
	if err := store.UpsertSchedulerWorkspaceState(ctx, SchedulerWorkspaceState{
		Workspace:       "ws",
		LastScheduledAt: completedAt.Add(-2 * time.Minute),
		LastCompletedAt: completedAt,
		LastResult:      "completed",
		LastDurationMS:  1234,
		UpdatedAt:       completedAt,
	}); err != nil {
		t.Fatalf("upsert scheduler state: %v", err)
	}
	state, err := store.GetSchedulerWorkspaceState(ctx, "ws")
	if err != nil {
		t.Fatalf("get scheduler state: %v", err)
	}
	if state == nil || state.LastResult != "completed" || !state.LastCompletedAt.Equal(completedAt) {
		t.Fatalf("unexpected scheduler state: %+v", state)
	}

	for i := 0; i < 35; i++ {
		started := completedAt.Add(time.Duration(i) * time.Minute)
		if err := store.InsertSchedulerRunRecord(ctx, SchedulerRunRecord{
			ID:          fmt.Sprintf("run-%02d", i),
			Workspace:   "ws",
			StartedAt:   started,
			CompletedAt: started.Add(2 * time.Second),
			Trigger:     "daily_tick",
			Result:      "completed",
			DurationMS:  2000,
		}, 30); err != nil {
			t.Fatalf("insert scheduler run %d: %v", i, err)
		}
	}
	runs, err := store.ListSchedulerRunHistory(ctx, "ws", 100)
	if err != nil {
		t.Fatalf("list scheduler run history: %v", err)
	}
	if len(runs) != 30 {
		t.Fatalf("expected 30 retained scheduler runs, got %d", len(runs))
	}
	if runs[0].ID != "run-34" || runs[len(runs)-1].ID != "run-05" {
		t.Fatalf("unexpected retained run window: first=%s last=%s", runs[0].ID, runs[len(runs)-1].ID)
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

func TestStorePopulateSupersedesRelations(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "supersedes-populate.db")
	ctx := context.Background()

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	base := &core.MemoryEntry{
		ID:          "m1",
		Type:        core.SemanticMemory,
		Content:     "Original fact.",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceAgentObservation},
		Confidence:  0.8,
		StorageTier: core.TierVector,
	}
	successor := &core.MemoryEntry{
		ID:          "m2",
		Type:        core.SemanticMemory,
		Content:     "Corrected fact.",
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
	// Apply reconsolidation to create supersedes relation and superseded_by pointer
	_, err = store.ApplyReconsolidation(ctx, base.ID, core.ReconsolidateSuperseded, successor.ID, at)
	if err != nil {
		t.Fatalf("apply reconsolidation: %v", err)
	}

	// 1. GetMemoriesByIDs test
	resMap, err := store.GetMemoriesByIDs(ctx, []string{"m1", "m2"})
	if err != nil {
		t.Fatalf("GetMemoriesByIDs: %v", err)
	}
	m1 := resMap["m1"]
	m2 := resMap["m2"]

	if m1.SupersededBy == nil || *m1.SupersededBy != "m2" {
		t.Errorf("expected m1.SupersededBy to be 'm2', got %+v", m1.SupersededBy)
	}
	if len(m2.Relations) != 1 || m2.Relations[0].Type != core.RelSupersedes || m2.Relations[0].TargetID != "m1" {
		t.Errorf("expected m2 to have one 'supersedes' relation to 'm1', got: %+v", m2.Relations)
	}

	// 2. ListRecentMemoriesByWorkspace test
	recent, err := store.ListRecentMemoriesByWorkspace(ctx, "ws", 10)
	if err != nil {
		t.Fatalf("ListRecentMemoriesByWorkspace: %v", err)
	}
	var foundM2 bool
	for _, m := range recent {
		if m.ID == "m2" {
			foundM2 = true
			if len(m.Relations) != 1 || m.Relations[0].Type != core.RelSupersedes || m.Relations[0].TargetID != "m1" {
				t.Errorf("expected listed m2 to have one 'supersedes' relation to 'm1', got: %+v", m.Relations)
			}
		}
	}
	if !foundM2 {
		t.Errorf("expected to find m2 in recent memories list")
	}
}

func TestListRecentMemoriesUsesInsertionOrderWhenCreatedAtTies(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	createdAt := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	for _, memory := range []*core.MemoryEntry{
		{ID: "first", Type: core.SemanticMemory, Content: "first", Workspace: "ws", Source: core.MemorySource{Type: core.SourceUserInput}, Confidence: 0.8, StorageTier: core.TierVector, CreatedAt: createdAt},
		{ID: "second", Type: core.SemanticMemory, Content: "second", Workspace: "ws", Source: core.MemorySource{Type: core.SourceUserInput}, Confidence: 0.8, StorageTier: core.TierVector, CreatedAt: createdAt},
	} {
		if err := store.UpsertMemory(ctx, memory); err != nil {
			t.Fatal(err)
		}
	}

	recent, err := store.ListRecentMemoriesByWorkspace(ctx, "ws", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].ID != "second" {
		t.Fatalf("expected later inserted tied memory first, got %+v", recent)
	}
}
