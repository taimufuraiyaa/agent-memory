package application

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestSessionEndStructuredEpisodeFinalizesWithoutHeuristicDuplication(t *testing.T) {
	ctx := context.Background()
	store := openSolutionServiceStore(t)
	defer store.Close()
	solutions := NewSolutionService(store, engine.NewSolutionAdmissionPolicy())
	episode, _, err := solutions.Start(ctx, safeSolutionStart("session-end-start"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = solutions.AppendStep(ctx, SolutionAppendStepInput{Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID,
		Kind: core.SolutionStepResult, Status: core.SolutionStepCompleted, Summary: "Focused tests passed.", Source: "agent",
		Confidence: .9, Sensitivity: core.SolutionSensitivityInternal, IdempotencyKey: "session-end-step"})
	if err != nil {
		t.Fatal(err)
	}
	transcript := "Always run database migrations before deploying because this procedure successfully prevented schema failures."
	result, err := RunSessionEnd(ctx, SessionEndInput{Workspace: "ws", SessionID: "session-1", PrincipalID: "principal-1",
		Transcript: transcript, TerminalStatus: core.SolutionEpisodeCompleted, IdempotencyKey: "session-end-finish"}, store, engine.NewWritePipeline(store))
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "structured_episode" || result.Partial || result.Summary == nil || result.Episode == nil || result.Episode.Status != core.SolutionEpisodeCompleted || result.TotalExtracted != 0 {
		t.Fatalf("unexpected structured result: %+v", result)
	}
	memories, err := store.ListMemoriesByWorkspace(ctx, "ws")
	if err != nil || len(memories) != 0 {
		t.Fatalf("structured session-end ran heuristic extraction: memories=%d err=%v", len(memories), err)
	}

	retry, err := RunSessionEnd(ctx, SessionEndInput{Workspace: "ws", SessionID: "session-1", PrincipalID: "principal-1",
		Transcript: transcript, TerminalStatus: core.SolutionEpisodeCompleted, IdempotencyKey: "session-end-finish"}, store, engine.NewWritePipeline(store))
	if err != nil || retry.Summary == nil || retry.Summary.ID != result.Summary.ID || retry.Partial {
		t.Fatalf("structured retry was not idempotent: first=%+v retry=%+v err=%v", result, retry, err)
	}
}

func TestSessionEndMixedClientWithoutEpisodeIdentityKeepsHeuristicFallback(t *testing.T) {
	ctx := context.Background()
	store := openSolutionServiceStore(t)
	defer store.Close()
	result, err := RunSessionEnd(ctx, SessionEndInput{Workspace: "ws", Transcript: "Always run database migrations before deploying because this procedure successfully prevented schema failures."}, store, engine.NewWritePipeline(store))
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "heuristic_fallback" || result.TotalExtracted == 0 || !result.LifecycleRan {
		t.Fatalf("legacy fallback changed: %+v", result)
	}
}

func TestSessionEndStructuredFinalizationDoesNotRequireEmbeddingProvider(t *testing.T) {
	ctx := context.Background()
	store := openSolutionServiceStore(t)
	defer store.Close()
	episode, _, err := NewSolutionService(store, engine.NewSolutionAdmissionPolicy()).Start(ctx, safeSolutionStart("no-provider-start"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunSessionEnd(ctx, SessionEndInput{Workspace: "ws", SessionID: episode.SessionID, PrincipalID: episode.PrincipalID}, store, engine.NewWritePipeline(store))
	if err != nil || result.Summary == nil || result.Mode != "structured_episode" {
		t.Fatalf("provider-free finalization failed: result=%+v err=%v", result, err)
	}
}

func TestSessionEndHeuristicWriteFailureReturnsExplicitPartialResult(t *testing.T) {
	ctx := context.Background()
	store := openSolutionServiceStore(t)
	pipeline := engine.NewWritePipeline(store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := RunSessionEnd(ctx, SessionEndInput{Workspace: "ws", Transcript: "Always run database migrations before deploying because this procedure successfully prevented schema failures."}, store, pipeline)
	if err != nil {
		t.Fatalf("partial failure should be represented in the result: %v", err)
	}
	if result == nil || !result.Partial || result.TotalFailed == 0 || len(result.Failures) != 1 || result.Failures[0] != "heuristic_extraction_failed" {
		t.Fatalf("unexpected partial result: %+v", result)
	}
}

func TestSessionEndStructuredPartialFailureCanRetryAfterTerminalTransition(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "session-end-partial.db")
	store, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	episode, _, err := NewSolutionService(store, engine.NewSolutionAdmissionPolicy()).Start(ctx, safeSolutionStart("partial-start"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err = raw.Exec(`CREATE TRIGGER fail_session_end_summary BEFORE INSERT ON solution_summaries BEGIN SELECT RAISE(FAIL, 'injected summary failure'); END`); err != nil {
		t.Fatal(err)
	}
	input := SessionEndInput{Workspace: "ws", SessionID: episode.SessionID, PrincipalID: episode.PrincipalID, IdempotencyKey: "partial-finish"}
	partial, err := RunSessionEnd(ctx, input, store, engine.NewWritePipeline(store))
	if err != nil || !partial.Partial || partial.Episode == nil || !partial.Episode.Status.Terminal() || len(partial.Failures) != 1 || partial.Failures[0] != "episode_finalization_failed" {
		t.Fatalf("unexpected structured partial result: result=%+v err=%v", partial, err)
	}
	if _, err = raw.Exec(`DROP TRIGGER fail_session_end_summary`); err != nil {
		t.Fatal(err)
	}
	retry, err := RunSessionEnd(ctx, input, store, engine.NewWritePipeline(store))
	if err != nil || retry.Partial || retry.Summary == nil {
		t.Fatalf("structured partial retry failed: result=%+v err=%v", retry, err)
	}
}
