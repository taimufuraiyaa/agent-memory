package application

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestSolutionHowHistoryDetailResolvesPromotedWhatAndPathFeedback(t *testing.T) {
	ctx := context.Background()
	store := openSolutionServiceStore(t)
	defer store.Close()
	service := NewSolutionService(store, engine.NewSolutionAdmissionPolicy())

	episode, _, err := service.Start(ctx, safeSolutionStart("how-history"))
	if err != nil {
		t.Fatal(err)
	}
	step, _, err := service.AppendStep(ctx, SolutionAppendStepInput{
		Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID,
		Kind: core.SolutionStepResult, Status: core.SolutionStepCompleted,
		Summary: "Verified the history tree.", Source: "agent", Confidence: .9,
		Sensitivity: core.SolutionSensitivityInternal, IdempotencyKey: "how-history-step",
	})
	if err != nil {
		t.Fatal(err)
	}
	episode, err = service.Transition(ctx, SolutionTransitionInput{Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID, ExpectedVersion: 2, Status: core.SolutionEpisodeCompleted, IdempotencyKey: "how-history-complete"})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := service.Finalize(ctx, SolutionFinalizeInput{Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID, ExpectedVersion: episode.Version, IdempotencyKey: "how-history-finalize"})
	if err != nil {
		t.Fatal(err)
	}
	promoted, err := service.Promote(ctx, SolutionPromoteInput{
		Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID, SummaryID: summary.ID, IdempotencyKey: "how-history-promote",
		Targets: []SolutionPromotionTarget{{MemoryType: core.ProceduralMemory, Content: "Open How History and inspect explicit provenance.", SourceStepIDs: []string{step.ID}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	feedbackAt := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	if err := store.PutHowRetrievalFeedback(ctx, "ws", string(engine.HowTargetPath), summary.ID, core.FeedbackHelpful, feedbackAt); err != nil {
		t.Fatal(err)
	}

	detail, err := service.GetActivityEpisode(ctx, "ws", episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.PromotionTargets) != 1 || detail.PromotionTargets[0].Promotion.ID != promoted.Promotions[0].ID || detail.PromotionTargets[0].Memory == nil {
		t.Fatalf("resolved promoted memory missing: %+v", detail.PromotionTargets)
	}
	if detail.PromotionTargets[0].Memory.Workspace != "ws" || detail.PromotionTargets[0].Memory.Content != "Open How History and inspect explicit provenance." || detail.PromotionTargets[0].Availability != "available" {
		t.Fatalf("unexpected promoted memory target: %+v", detail.PromotionTargets[0])
	}
	if len(detail.PathFeedback) != 1 || detail.PathFeedback[0].TargetID != summary.ID || detail.PathFeedback[0].Outcome != core.FeedbackHelpful || !detail.PathFeedback[0].CreatedAt.Equal(feedbackAt) {
		t.Fatalf("path feedback missing: %+v", detail.PathFeedback)
	}
	grouped, err := store.ListPublishedSolutionPromotionMemoryIDs(ctx, "ws")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := grouped[promoted.Promotions[0].TargetID]; !ok {
		t.Fatalf("published promotion target was not classified as grouped: %+v", grouped)
	}
}

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
