package application

import (
	"context"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type graphCoordinatorStoreFixture struct {
	requests []GraphScheduleRequest
	err      error
}

func (s *graphCoordinatorStoreFixture) ScheduleGraphRevision(_ context.Context, request GraphScheduleRequest) error {
	if s.err != nil {
		return s.err
	}
	s.requests = append(s.requests, request)
	return nil
}

func TestGraphCoordinatorCoalescesThresholdAndAge(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	store := &graphCoordinatorStoreFixture{}
	coordinator := NewGraphCoordinator(store, DefaultGraphLimits())
	base := GraphCoordinationSnapshot{
		Scope: core.GraphScope{WorkspaceID: "ws"}, ConfigurationID: "config", ConfigurationVersion: 1,
		ProjectionVersion: "projection-v1", PendingChanges: 49, OldestChangeAt: now.Add(-14 * time.Minute),
		NewestChangeAt: now, CutoffSequence: 49, CutoffFingerprints: []string{"a", "b"}, Now: now,
	}
	decision, err := coordinator.Coordinate(context.Background(), base)
	if err != nil || decision.Action != GraphCoordinationWait || len(store.requests) != 0 {
		t.Fatalf("early decision=%+v requests=%d err=%v", decision, len(store.requests), err)
	}
	base.PendingChanges = 50
	decision, err = coordinator.Coordinate(context.Background(), base)
	if err != nil || decision.Action != GraphCoordinationSchedule || len(store.requests) != 1 {
		t.Fatalf("threshold decision=%+v requests=%d err=%v", decision, len(store.requests), err)
	}
	if store.requests[0].IdempotencyKey == "" || store.requests[0].Cutoff.Digest == "" {
		t.Fatalf("unstable schedule request=%+v", store.requests[0])
	}

	agedStore := &graphCoordinatorStoreFixture{}
	coordinator = NewGraphCoordinator(agedStore, DefaultGraphLimits())
	base.PendingChanges = 1
	base.OldestChangeAt = now.Add(-15 * time.Minute)
	decision, err = coordinator.Coordinate(context.Background(), base)
	if err != nil || decision.Action != GraphCoordinationSchedule || len(agedStore.requests) != 1 {
		t.Fatalf("aged decision=%+v requests=%d err=%v", decision, len(agedStore.requests), err)
	}
}

func TestGraphCoordinatorAllowsOnlyOneSuccessor(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	coordinator := NewGraphCoordinator(&graphCoordinatorStoreFixture{}, DefaultGraphLimits())
	snapshot := GraphCoordinationSnapshot{
		Scope: core.GraphScope{WorkspaceID: "ws"}, ConfigurationID: "config", ConfigurationVersion: 1,
		ProjectionVersion: "v1", PendingChanges: 50, OldestChangeAt: now.Add(-time.Hour), NewestChangeAt: now,
		CutoffSequence: 50, RunningRevisions: 1, SuccessorQueued: true, Now: now,
	}
	decision, err := coordinator.Coordinate(context.Background(), snapshot)
	if err != nil || decision.Action != GraphCoordinationCoalesce {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestGraphCoordinatorBackpressure(t *testing.T) {
	limits := DefaultGraphLimits()
	now := time.Now().UTC()
	coordinator := NewGraphCoordinator(&graphCoordinatorStoreFixture{}, limits)
	decision, err := coordinator.Coordinate(context.Background(), GraphCoordinationSnapshot{
		Scope: core.GraphScope{WorkspaceID: "ws"}, ConfigurationID: "config", ConfigurationVersion: 1,
		ProjectionVersion: "v1", PendingChanges: 50, ProjectionBytes: limits.MaxProjectionBytes + 1,
		OldestChangeAt: now.Add(-time.Hour), NewestChangeAt: now, Now: now,
	})
	if err != nil || decision.Action != GraphCoordinationBackpressure || decision.Reason != "projection_too_large" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}
