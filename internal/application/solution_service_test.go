package application

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestSolutionServiceAdmissionIsMandatoryAndAuditedWithoutContent(t *testing.T) {
	ctx := context.Background()
	store := openSolutionServiceStore(t)
	defer func() { _ = store.Close() }()
	svc := NewSolutionService(store, engine.NewSolutionAdmissionPolicy())

	_, _, err := svc.Start(ctx, SolutionStartInput{
		Workspace: "ws", SessionID: "session-1", PrincipalID: "principal-1", ClientID: "codex",
		GoalSummary:   "Chain of thought: persist my hidden internal token trace.",
		CapturePolicy: core.SolutionCaptureStructured, RetentionClass: core.SolutionRetentionStandard,
		IdempotencyKey: "unsafe-start",
	})
	if err == nil || !strings.Contains(err.Error(), "raw_reasoning") {
		t.Fatalf("expected raw reasoning rejection, got %v", err)
	}

	events, err := store.ListAuditEvents(ctx, sqlite.AuditFilter{Workspace: "ws", Operation: "solution_admission", Limit: 10})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(events) != 1 || events[0].Outcome != "reject" || events[0].Reason != "raw_reasoning" {
		t.Fatalf("unexpected admission audit: %+v", events)
	}
	auditText := events[0].Reason + events[0].Actor + events[0].Source
	if encoded := strings.TrimSpace(string(stringMustJSON(t, events[0].Metadata))); strings.Contains(encoded, "hidden internal") || strings.Contains(auditText, "hidden internal") {
		t.Fatalf("audit must be content-free: %+v", events[0])
	}
}

func TestSolutionServiceStartAppendAndCrossScopeProtection(t *testing.T) {
	ctx := context.Background()
	store := openSolutionServiceStore(t)
	defer func() { _ = store.Close() }()
	svc := NewSolutionService(store, engine.NewSolutionAdmissionPolicy())

	episode, dedup, err := svc.Start(ctx, safeSolutionStart("start-1"))
	if err != nil || dedup {
		t.Fatalf("start: episode=%+v dedup=%v err=%v", episode, dedup, err)
	}
	step, dedup, err := svc.AppendStep(ctx, SolutionAppendStepInput{
		Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID,
		Kind: core.SolutionStepDecision, Status: core.SolutionStepCompleted,
		Summary: "Use additive tables.", RationaleSummary: "Existing memory types remain stable.",
		Source: "agent", Confidence: 0.9, Sensitivity: core.SolutionSensitivityInternal,
		IdempotencyKey: "step-1",
	})
	if err != nil || dedup || step.Ordinal != 1 {
		t.Fatalf("append: step=%+v dedup=%v err=%v", step, dedup, err)
	}

	_, _, err = svc.AppendStep(ctx, SolutionAppendStepInput{
		Workspace: "other", PrincipalID: "principal-1", EpisodeID: episode.ID,
		Kind: core.SolutionStepAction, Status: core.SolutionStepCompleted,
		Summary: "Attempt cross-workspace append.", Source: "agent", Confidence: 0.5,
		Sensitivity: core.SolutionSensitivityInternal, IdempotencyKey: "cross-workspace",
	})
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("expected cross-workspace denial, got %v", err)
	}
}

func TestSolutionServiceLifecycleUsesOptimisticVersions(t *testing.T) {
	ctx := context.Background()
	store := openSolutionServiceStore(t)
	defer func() { _ = store.Close() }()
	svc := NewSolutionService(store, engine.NewSolutionAdmissionPolicy())
	episode, _, err := svc.Start(ctx, safeSolutionStart("lifecycle"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	paused, err := svc.Transition(ctx, SolutionTransitionInput{
		Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID,
		ExpectedVersion: 1, Status: core.SolutionEpisodePaused,
	})
	if err != nil || paused.Version != 2 || paused.Status != core.SolutionEpisodePaused {
		t.Fatalf("pause: episode=%+v err=%v", paused, err)
	}
	if _, err := svc.Transition(ctx, SolutionTransitionInput{
		Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID,
		ExpectedVersion: 1, Status: core.SolutionEpisodeActive,
	}); err == nil || !strings.Contains(err.Error(), "version conflict") {
		t.Fatalf("expected stale transition conflict, got %v", err)
	}
	resumed, err := svc.Transition(ctx, SolutionTransitionInput{
		Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID,
		ExpectedVersion: 2, Status: core.SolutionEpisodeActive,
	})
	if err != nil || resumed.Version != 3 {
		t.Fatalf("resume: episode=%+v err=%v", resumed, err)
	}
	completed, err := svc.Transition(ctx, SolutionTransitionInput{
		Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID,
		ExpectedVersion: 3, Status: core.SolutionEpisodeCompleted,
	})
	if err != nil || !completed.Status.Terminal() {
		t.Fatalf("complete: episode=%+v err=%v", completed, err)
	}
	if _, err := svc.Transition(ctx, SolutionTransitionInput{
		Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID,
		ExpectedVersion: 4, Status: core.SolutionEpisodeActive,
	}); err == nil {
		t.Fatal("expected terminal episode to reject resume")
	}
}

func TestSolutionServiceHandoffTransfersOwnershipPaused(t *testing.T) {
	ctx := context.Background()
	store := openSolutionServiceStore(t)
	defer func() { _ = store.Close() }()
	svc := NewSolutionService(store, engine.NewSolutionAdmissionPolicy())
	episode, _, err := svc.Start(ctx, safeSolutionStart("handoff"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	handed, err := svc.Handoff(ctx, SolutionHandoffInput{
		Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID, ExpectedVersion: 1,
		TargetPrincipalID: "principal-2", TargetSessionID: "session-2",
	})
	if err != nil || handed.PrincipalID != "principal-2" || handed.SessionID != "session-2" || handed.Status != core.SolutionEpisodePaused {
		t.Fatalf("handoff: episode=%+v err=%v", handed, err)
	}
	if _, err := svc.Transition(ctx, SolutionTransitionInput{
		Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID,
		ExpectedVersion: 2, Status: core.SolutionEpisodeActive,
	}); err == nil {
		t.Fatal("expected previous owner to lose transition access")
	}
	if _, err := svc.Transition(ctx, SolutionTransitionInput{
		Workspace: "ws", PrincipalID: "principal-2", EpisodeID: episode.ID,
		ExpectedVersion: 2, Status: core.SolutionEpisodeActive,
	}); err != nil {
		t.Fatalf("new owner resume: %v", err)
	}
}

func TestSolutionServiceWorkingStateCASPrivacyAndExpiry(t *testing.T) {
	ctx := context.Background()
	store := openSolutionServiceStore(t)
	defer func() { _ = store.Close() }()
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	svc := NewSolutionService(store, engine.NewSolutionAdmissionPolicy(), WithSolutionClock(func() time.Time { return now }))
	episode, _, err := svc.Start(ctx, safeSolutionStart("working-state"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	state, err := svc.Checkpoint(ctx, SolutionCheckpointInput{
		Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID, ExpectedGeneration: 0,
		GoalSummary: "Finish lifecycle persistence.", Constraints: []string{"Keep old recall unchanged."},
		PlanItems:     []core.SolutionPlanItem{{ID: "p1", Summary: "Add working-state CAS", Status: core.SolutionPlanInProgress}},
		OpenQuestions: []string{"How recall remains opt-in?"}, NextAction: "Run focused tests.",
		Sensitivity: core.SolutionSensitivityInternal,
	})
	if err != nil || state.Generation != 1 || !state.ExpiresAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("checkpoint: state=%+v err=%v", state, err)
	}
	if _, err := svc.Checkpoint(ctx, SolutionCheckpointInput{
		Workspace: "ws", PrincipalID: "principal-1", EpisodeID: episode.ID, ExpectedGeneration: 0,
		GoalSummary: "Stale update", Sensitivity: core.SolutionSensitivityInternal,
	}); err == nil || !strings.Contains(err.Error(), "generation conflict") {
		t.Fatalf("expected stale generation conflict, got %v", err)
	}
	if _, err := svc.GetWorkingState(ctx, "ws", "principal-2", episode.ID); err == nil {
		t.Fatal("expected another principal to be denied")
	}
	if got, err := svc.GetWorkingState(ctx, "ws", "principal-1", episode.ID); err != nil || got.Generation != 1 {
		t.Fatalf("get current state: state=%+v err=%v", got, err)
	}

	now = now.Add(25 * time.Hour)
	if _, err := svc.GetWorkingState(ctx, "ws", "principal-1", episode.ID); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected query-time expiry, got %v", err)
	}
	removed, err := svc.CleanupExpiredWorkingState(ctx, 10)
	if err != nil || removed != 1 {
		t.Fatalf("cleanup: removed=%d err=%v", removed, err)
	}
}

func openSolutionServiceStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "solution-service.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return store
}

func safeSolutionStart(key string) SolutionStartInput {
	return SolutionStartInput{
		Workspace: "ws", SessionID: "session-1", PrincipalID: "principal-1", ClientID: "codex",
		GoalSummary: "Implement structured solution episodes.", CapturePolicy: core.SolutionCaptureStructured,
		RetentionClass: core.SolutionRetentionStandard, IdempotencyKey: key,
	}
}

func stringMustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return encoded
}
