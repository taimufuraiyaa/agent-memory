package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestHowRecallRanksValidatedPathsAndBoundsSections(t *testing.T) {
	ctx := context.Background()
	store := openHowRecallStore(t)
	defer store.Close()
	success := seedHowSummary(t, store, "success", core.SolutionEpisodeCompleted, "Configure retries with exponential backoff.")
	partial := seedHowSummary(t, store, "partial", core.SolutionEpisodePartial, "Retry setup still needs timeout verification.")
	failed := seedHowSummary(t, store, "failed", core.SolutionEpisodeAbandoned, "Avoid disabling retry validation.")
	lesson, _, err := store.PutSolutionToolLesson(ctx, core.SolutionToolLesson{Workspace: "ws", ToolName: "go-test", Capability: "Verify retry behavior", Preconditions: []string{"test"}, Fallback: "Run manually.", Confidence: .9, Validation: core.SolutionValidationVerified, SourceEpisodeIDs: []string{"episode-source"}, SourceStepIDs: []string{"step-source"}, SourceEventIDs: []string{"event-source"}, SuccessCount: 2, CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertMemory(ctx, &core.MemoryEntry{ID: "procedure-1", Workspace: "ws", Type: core.ProceduralMemory, Content: "Configure retry backoff, then run focused tests.", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutDistilledSkillMetadata(ctx, core.DistilledSkillMetadata{ID: "skill-1", Workspace: "ws", Name: "retry-verifier", Path: ".agents/skills/retry-verifier/SKILL.md", MemoryIDs: []string{"procedure-1"}, EpisodeIDs: []string{success.EpisodeID}}); err != nil {
		t.Fatal(err)
	}
	service := NewHowRecallService(store)
	result, err := service.Recall(ctx, HowRecallInput{Workspace: "ws", PrincipalID: "p1", SessionID: "s1", Task: "How to configure and verify retries?", TokenBudget: 180})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Paths) < 2 || result.Paths[0].Summary.ID != success.ID || result.Paths[1].Summary.ID != partial.ID {
		t.Fatalf("unexpected path ranking: %+v", result.Paths)
	}
	if result.Paths[0].Confidence <= 0 || result.Paths[0].Recency.IsZero() || result.Paths[0].Provenance.EpisodeID != success.EpisodeID {
		t.Fatalf("path retrieval metadata is incomplete: %+v", result.Paths[0])
	}
	if len(result.ToolLessons) != 1 || result.ToolLessons[0].Lesson.ID != lesson.ID || len(result.Procedures) != 1 || len(result.Skills) != 1 {
		t.Fatalf("missing reusable guidance: %+v", result)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].TargetID != failed.ID {
		t.Fatalf("missing failed-path warning: %+v", result.Warnings)
	}
	if result.TokensUsed > 180 || result.ContextBlock == "" {
		t.Fatalf("budget violation: tokens=%d context=%q", result.TokensUsed, result.ContextBlock)
	}
}

func TestHowRecallHarmfulFeedbackSuppressesPathIndependently(t *testing.T) {
	ctx := context.Background()
	store := openHowRecallStore(t)
	defer store.Close()
	summary := seedHowSummary(t, store, "harmful", core.SolutionEpisodeCompleted, "Use the unsafe retry workaround.")
	service := NewHowRecallService(store)
	if err := service.Feedback(ctx, HowFeedbackInput{Workspace: "ws", TargetKind: HowTargetPath, TargetID: summary.ID, Outcome: core.FeedbackHarmful}); err != nil {
		t.Fatal(err)
	}
	result, err := service.Recall(ctx, HowRecallInput{Workspace: "ws", PrincipalID: "p1", SessionID: "s1", Task: "retry workaround", TokenBudget: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Paths) != 0 {
		t.Fatalf("harmful path was returned: %+v", result.Paths)
	}
	if _, err := store.GetSolutionSummary(ctx, summary.ID); err != nil {
		t.Fatalf("feedback must not delete source summary: %v", err)
	}
}

func TestHowRecallRejectedFeedbackSuppressesPath(t *testing.T) {
	ctx := context.Background()
	store := openHowRecallStore(t)
	defer store.Close()
	summary := seedHowSummary(t, store, "rejected", core.SolutionEpisodeCompleted, "Use a rejected retry approach.")
	service := NewHowRecallService(store)
	if err := service.Feedback(ctx, HowFeedbackInput{Workspace: "ws", TargetKind: HowTargetPath, TargetID: summary.ID, Outcome: core.FeedbackRejected}); err != nil {
		t.Fatal(err)
	}
	result, err := service.Recall(ctx, HowRecallInput{Workspace: "ws", Task: "retry approach", TokenBudget: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Paths) != 0 {
		t.Fatalf("rejected path was returned: %+v", result.Paths)
	}
}

func TestHowRecallCurrentStateIsPrincipalAndSessionPrivate(t *testing.T) {
	ctx := context.Background()
	store := openHowRecallStore(t)
	defer store.Close()
	episode, _, err := store.CreateSolutionEpisode(ctx, sqlite.SolutionEpisodeInsert{Workspace: "ws", SessionID: "s1", PrincipalID: "p1", ClientID: "codex", GoalSummary: "Continue private work.", CapturePolicy: core.SolutionCaptureStructured, RetentionClass: core.SolutionRetentionStandard, IdempotencyKey: "private"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.PutSolutionWorkingState(ctx, core.SolutionWorkingState{EpisodeID: episode.ID, Workspace: "ws", SessionID: "s1", PrincipalID: "p1", GoalSummary: "Continue private work.", NextAction: "Run the private check.", Generation: 1, Sensitivity: core.SolutionSensitivityInternal, UpdatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	service := NewHowRecallService(store)
	owner, err := service.Recall(ctx, HowRecallInput{Workspace: "ws", PrincipalID: "p1", SessionID: "s1", Task: "continue work", TokenBudget: 100})
	if err != nil {
		t.Fatal(err)
	}
	if owner.CurrentState == nil {
		t.Fatal("owner current state missing")
	}
	other, err := service.Recall(ctx, HowRecallInput{Workspace: "ws", PrincipalID: "p2", SessionID: "s1", Task: "continue work", TokenBudget: 100})
	if err != nil {
		t.Fatal(err)
	}
	if other.CurrentState != nil {
		t.Fatalf("current state leaked: %+v", other.CurrentState)
	}
}

func openHowRecallStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "how.db"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func seedHowSummary(t *testing.T, store *sqlite.Store, key string, status core.SolutionEpisodeStatus, text string) core.SolutionSummary {
	t.Helper()
	ctx := context.Background()
	episode, _, err := store.CreateSolutionEpisode(ctx, sqlite.SolutionEpisodeInsert{Workspace: "ws", SessionID: "session-" + key, PrincipalID: "p1", ClientID: "client-" + key, GoalSummary: text, CapturePolicy: core.SolutionCaptureStructured, RetentionClass: core.SolutionRetentionStandard, IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := store.TransitionSolutionEpisode(ctx, sqlite.SolutionEpisodeTransition{EpisodeID: episode.ID, Workspace: "ws", PrincipalID: "p1", ExpectedVersion: 1, Status: status, IdempotencyKey: "terminal-" + key, UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	outcome := core.OutcomeFailure
	if status == core.SolutionEpisodeCompleted {
		outcome = core.OutcomeSuccess
	} else if status == core.SolutionEpisodePartial {
		outcome = core.OutcomePartial
	}
	summary, _, err := store.CreateSolutionSummary(ctx, sqlite.SolutionSummaryInsert{EpisodeID: episode.ID, ExpectedEpisodeVersion: terminal.Version, Outcome: outcome, Summary: text, NextGuidance: text, Validation: core.SolutionValidationVerified, SnapshotHash: "snapshot-" + key, IdempotencyKey: "summary-" + key})
	if err != nil {
		t.Fatal(err)
	}
	return summary
}
