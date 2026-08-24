package application

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestSolutionActivityInspectionAndReviewLifecycle(t *testing.T) {
	ctx := context.Background()
	store := openSolutionServiceStore(t)
	defer store.Close()
	service := NewSolutionService(store, engine.NewSolutionAdmissionPolicy())

	episode, _, err := service.Start(ctx, safeSolutionStart("review-source"))
	if err != nil {
		t.Fatal(err)
	}
	step, _, err := service.AppendStep(ctx, SolutionAppendStepInput{
		Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID,
		Kind: core.SolutionStepDecision, Status: core.SolutionStepCompleted,
		Summary: "Use the first migration strategy.", RationaleSummary: "It looked compatible.", Source: "agent",
		Confidence: .7, Sensitivity: core.SolutionSensitivityInternal, IdempotencyKey: "review-step",
	})
	if err != nil {
		t.Fatal(err)
	}
	episode, err = service.Transition(ctx, SolutionTransitionInput{Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID, ExpectedVersion: 2, Status: core.SolutionEpisodeCompleted, IdempotencyKey: "review-complete"})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := service.Finalize(ctx, SolutionFinalizeInput{Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID, ExpectedVersion: episode.Version, IdempotencyKey: "review-finalize"})
	if err != nil {
		t.Fatal(err)
	}

	items, err := service.ListActivityEpisodes(ctx, "ws", 20)
	if err != nil || len(items) != 1 || items[0].Episode.ID != episode.ID || items[0].Summary == nil {
		t.Fatalf("unexpected activity episodes: items=%+v err=%v", items, err)
	}
	if _, err := service.GetActivityEpisode(ctx, "other", episode.ID); err == nil {
		t.Fatal("cross-workspace episode detail must be denied")
	}

	if err := service.SetEpisodePinned(ctx, SolutionEpisodePinInput{Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID, Pinned: true}); err != nil {
		t.Fatal(err)
	}
	if err := service.MarkStepMisleading(ctx, SolutionStepReviewInput{Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID, StepID: step.ID, Reason: "The compatibility assumption was incomplete."}); err != nil {
		t.Fatal(err)
	}
	if err := service.RedactStep(ctx, SolutionStepRedactInput{Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID, StepID: step.ID, ReasonClass: "user_request"}); err != nil {
		t.Fatal(err)
	}
	corrected, err := service.CorrectSummary(ctx, SolutionSummaryCorrectionInput{Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID, Summary: "Use the compatible migration after verifying rollback.", IdempotencyKey: "review-correction"})
	if err != nil {
		t.Fatal(err)
	}
	if corrected.Version != summary.Version+1 || corrected.Summary == summary.Summary {
		t.Fatalf("summary correction did not supersede prior output: old=%+v new=%+v", summary, corrected)
	}
	detail, err := service.GetActivityEpisode(ctx, "ws", episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !detail.Pinned || len(detail.Steps) != 1 || detail.Steps[0].Summary != "[REDACTED: user_request]" || len(detail.StepReviews) != 1 || !detail.StepReviews[0].Misleading || !detail.StepReviews[0].Redacted || detail.Summary == nil || detail.Summary.ID != corrected.ID {
		t.Fatalf("review state missing from detail: %+v", detail)
	}
}

func TestSolutionActivitySupersessionDeletionAndAudit(t *testing.T) {
	ctx := context.Background()
	store := openSolutionServiceStore(t)
	defer store.Close()
	service := NewSolutionService(store, engine.NewSolutionAdmissionPolicy())

	source := seedTerminalReviewEpisode(t, service, "supersede-source", "session-1")
	successor := seedTerminalReviewEpisode(t, service, "supersede-target", "session-2")
	if err := service.SupersedeEpisode(ctx, SolutionEpisodeSupersedeInput{Workspace: "ws", PrincipalID: "principal-1", EpisodeID: source.ID, SuccessorEpisodeID: successor.ID}); err != nil {
		t.Fatal(err)
	}
	detail, err := service.GetActivityEpisode(ctx, "ws", source.ID)
	if err != nil || detail.Episode.SupersededBy != successor.ID {
		t.Fatalf("supersession missing: detail=%+v err=%v", detail, err)
	}
	if err := service.DeleteEpisode(ctx, SolutionEpisodeDeleteInput{Workspace: "other", PrincipalID: "principal-1", EpisodeID: source.ID, Reason: "wrong scope"}); err == nil {
		t.Fatal("cross-workspace deletion must be denied")
	}
	if err := service.DeleteEpisode(ctx, SolutionEpisodeDeleteInput{Workspace: "ws", PrincipalID: "principal-1", EpisodeID: source.ID, Reason: "user_request"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSolutionEpisode(ctx, source.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("episode was not deleted: %v", err)
	}
	events, err := store.ListAuditEvents(ctx, sqlite.AuditFilter{Workspace: "ws", Operation: "solution_delete", Limit: 10})
	if err != nil || len(events) != 1 || events[0].TargetIDs[0] != source.ID || events[0].Reason != "user_request" {
		t.Fatalf("content-free deletion audit missing: events=%+v err=%v", events, err)
	}
}

func seedTerminalReviewEpisode(t *testing.T, service *SolutionService, key, session string) core.SolutionEpisode {
	t.Helper()
	input := safeSolutionStart(key)
	input.SessionID = session
	episode, _, err := service.Start(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	episode, err = service.Transition(context.Background(), SolutionTransitionInput{Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID, ExpectedVersion: 1, Status: core.SolutionEpisodeCompleted, IdempotencyKey: key + "-complete"})
	if err != nil {
		t.Fatal(err)
	}
	return episode
}
