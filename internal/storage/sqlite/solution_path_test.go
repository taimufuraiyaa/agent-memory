package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSolutionEpisodeMigrationAndRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "solution.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	created, deduplicated, err := store.CreateSolutionEpisode(ctx, SolutionEpisodeInsert{
		Workspace: "agent-memory", SessionID: "session-1", PrincipalID: "principal-1",
		ClientID: "codex", GoalSummary: "Implement safe solution-path storage.",
		CapturePolicy: core.SolutionCaptureStructured, RetentionClass: core.SolutionRetentionStandard,
		IdempotencyKey: "start-1", CreatedAt: time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	if deduplicated {
		t.Fatal("first create must not be deduplicated")
	}
	if created.ID == "" || created.Version != 1 || created.Status != core.SolutionEpisodeActive {
		t.Fatalf("unexpected created episode: %+v", created)
	}

	got, err := store.GetSolutionEpisode(ctx, created.ID)
	if err != nil {
		t.Fatalf("get episode: %v", err)
	}
	if got.GoalSummary != created.GoalSummary || got.Workspace != "agent-memory" || got.SessionID != "session-1" {
		t.Fatalf("unexpected episode round trip: %+v", got)
	}
}

func TestCreateSolutionEpisodeIsIdempotentAndRejectsKeyReuse(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "solution-idempotent.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	input := SolutionEpisodeInsert{
		Workspace: "ws", SessionID: "session-1", PrincipalID: "principal-1", ClientID: "codex",
		GoalSummary: "Preserve an ordered solution path.", CapturePolicy: core.SolutionCaptureStructured,
		RetentionClass: core.SolutionRetentionStandard, IdempotencyKey: "same-key",
	}
	first, deduplicated, err := store.CreateSolutionEpisode(ctx, input)
	if err != nil || deduplicated {
		t.Fatalf("first create: episode=%+v deduplicated=%v err=%v", first, deduplicated, err)
	}
	second, deduplicated, err := store.CreateSolutionEpisode(ctx, input)
	if err != nil || !deduplicated {
		t.Fatalf("retry create: episode=%+v deduplicated=%v err=%v", second, deduplicated, err)
	}
	if second.ID != first.ID {
		t.Fatalf("idempotent retry changed ID: first=%s second=%s", first.ID, second.ID)
	}

	input.GoalSummary = "A different goal must not reuse the same key."
	if _, _, err := store.CreateSolutionEpisode(ctx, input); err == nil {
		t.Fatal("expected mismatched idempotency-key reuse to fail")
	}
}

func TestAppendSolutionStepAssignsStableOrdinalAndRoundTripsReferences(t *testing.T) {
	ctx := context.Background()
	store, episodeID := openSolutionTestStore(t, "solution-step.db")
	defer func() { _ = store.Close() }()

	input := SolutionStepInsert{
		EpisodeID: episodeID, Kind: core.SolutionStepDecision, Status: core.SolutionStepCompleted,
		Summary: "Use a separate episode table.", RationaleSummary: "Ordered children do not fit one memory row.",
		Source: "agent", Confidence: 0.9, Sensitivity: core.SolutionSensitivityInternal,
		References:     []core.SolutionReference{{Kind: core.SolutionReferenceMemory, TargetID: "memory-1"}},
		IdempotencyKey: "step-1",
	}
	first, deduplicated, err := store.AppendSolutionStep(ctx, input)
	if err != nil || deduplicated {
		t.Fatalf("append first step: step=%+v deduplicated=%v err=%v", first, deduplicated, err)
	}
	if first.Ordinal != 1 || len(first.References) != 1 || first.References[0].TargetID != "memory-1" {
		t.Fatalf("unexpected first step: %+v", first)
	}

	retry, deduplicated, err := store.AppendSolutionStep(ctx, input)
	if err != nil || !deduplicated || retry.ID != first.ID || retry.Ordinal != 1 {
		t.Fatalf("append retry: step=%+v deduplicated=%v err=%v", retry, deduplicated, err)
	}

	input.IdempotencyKey = "step-2"
	input.Kind = core.SolutionStepResult
	input.Summary = "Focused tests pass."
	input.RationaleSummary = ""
	input.References = []core.SolutionReference{{Kind: core.SolutionReferenceTest, TargetID: "core-tests"}}
	second, deduplicated, err := store.AppendSolutionStep(ctx, input)
	if err != nil || deduplicated || second.Ordinal != 2 {
		t.Fatalf("append second step: step=%+v deduplicated=%v err=%v", second, deduplicated, err)
	}
}

func TestSolutionObservationReferenceBecomesTombstonedAfterEvidenceDeletion(t *testing.T) {
	ctx := context.Background()
	store, episodeID := openSolutionTestStore(t, "solution-tombstone.db")
	defer func() { _ = store.Close() }()
	observation, _, err := store.InsertObservationDedupWindow(ctx, ObservationInsert{Workspace: "ws", SessionID: "session-1", Kind: "tool_result", Summary: "The test passed."}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.AppendSolutionStep(ctx, SolutionStepInsert{EpisodeID: episodeID, Kind: core.SolutionStepResult, Status: core.SolutionStepCompleted,
		Summary: "Verified the result.", Source: "agent", Confidence: 0.9, Sensitivity: core.SolutionSensitivityInternal, IdempotencyKey: "tombstone-step",
		References: []core.SolutionReference{{Kind: core.SolutionReferenceObservation, TargetID: observation.ID, Workspace: "ws", SessionID: "session-1", Resolution: core.SolutionReferenceVerified}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM observations WHERE id = ?`, observation.ID); err != nil {
		t.Fatal(err)
	}
	steps, err := store.ListSolutionSteps(ctx, episodeID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := steps[0].References[0].Resolution; got != core.SolutionReferenceTombstoned {
		t.Fatalf("expected tombstoned reference, got %q", got)
	}
}

func TestAppendSolutionStepsConcurrentOrderingAndPaging(t *testing.T) {
	ctx := context.Background()
	store, episodeID := openSolutionTestStore(t, "solution-concurrent.db")
	defer func() { _ = store.Close() }()

	const count = 20
	var wg sync.WaitGroup
	errCh := make(chan error, count)
	for i := 0; i < count; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := store.AppendSolutionStep(ctx, SolutionStepInsert{
				EpisodeID: episodeID, Kind: core.SolutionStepAction, Status: core.SolutionStepCompleted,
				Summary: fmt.Sprintf("Concurrent action %d", i), Source: "agent", Confidence: 0.8,
				Sensitivity: core.SolutionSensitivityInternal, IdempotencyKey: fmt.Sprintf("step-%d", i),
			})
			if err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent append: %v", err)
	}

	firstPage, err := store.ListSolutionSteps(ctx, episodeID, 0, 7)
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	secondPage, err := store.ListSolutionSteps(ctx, episodeID, firstPage[len(firstPage)-1].Ordinal, count)
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	all := append(firstPage, secondPage...)
	if len(all) != count {
		t.Fatalf("expected %d steps, got %d", count, len(all))
	}
	ordinals := make([]int, 0, len(all))
	for _, step := range all {
		ordinals = append(ordinals, int(step.Ordinal))
	}
	sort.Ints(ordinals)
	for i, ordinal := range ordinals {
		if ordinal != i+1 {
			t.Fatalf("expected contiguous ordinals 1..%d, got %v", count, ordinals)
		}
	}
}

func openSolutionTestStore(t *testing.T, name string) (*Store, string) {
	t.Helper()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	episode, _, err := store.CreateSolutionEpisode(ctx, SolutionEpisodeInsert{
		Workspace: "ws", SessionID: "session-1", PrincipalID: "principal-1", ClientID: "codex",
		GoalSummary: "Test solution step persistence.", CapturePolicy: core.SolutionCaptureStructured,
		RetentionClass: core.SolutionRetentionStandard, IdempotencyKey: "episode-1",
	})
	if err != nil {
		_ = store.Close()
		t.Fatalf("create episode: %v", err)
	}
	return store, episode.ID
}
