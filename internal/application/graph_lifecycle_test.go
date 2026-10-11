package application

import (
	"context"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphDeletionRevokesQueryabilityAndSchedulesBoundedRepair(t *testing.T) {
	t.Parallel()
	store := &graphLifecycleFakeStore{}
	service := NewGraphLifecycleService(store)
	request := GraphDeletionRequest{Scope: core.GraphScope{WorkspaceID: "workspace-a"}, CanonicalKind: "memory", CanonicalID: "memory-10", DeletedAt: time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)}
	result, err := service.DeleteCanonicalEvidence(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !store.revoked || !store.repairScheduled || result.AffectedEdges != 1 {
		t.Fatalf("deletion did not revoke then repair: %#v store=%#v", result, store)
	}
	if !result.Tombstone.Blocks("memory", "memory-10", request.DeletedAt.Add(-time.Minute)) || result.Tombstone.Blocks("memory", "other", request.DeletedAt) {
		t.Fatal("deletion tombstone resurrection guard is incorrect")
	}
}

func TestGraphRetentionHonorsHoldsAndClassTTLs(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	artifacts := []GraphRetainedArtifact{
		{ID: "projection-old", Class: GraphArtifactProjection, RetentionStartedAt: now.Add(-25 * time.Hour)},
		{ID: "cache-old-held", Class: GraphArtifactCache, RetentionStartedAt: now.Add(-8 * 24 * time.Hour), Hold: true},
		{ID: "native-old", Class: GraphArtifactNative, RetentionStartedAt: now.Add(-31 * 24 * time.Hour)},
	}
	plan := PlanGraphRetention(now, artifacts)
	if len(plan.DeleteIDs) != 2 || plan.DeleteIDs[0] != "native-old" || plan.DeleteIDs[1] != "projection-old" || len(plan.HeldIDs) != 1 {
		t.Fatalf("retention plan = %#v", plan)
	}
}

func TestGraphRebuildUsesEligibleCanonicalRecordsWithoutNativeArtifacts(t *testing.T) {
	t.Parallel()
	request := graphProjectionRequestFixture()
	request.Records = []GraphProjectionRecord{
		graphProjectionRecord("memory-1", "Book A"),
		withGraphProjectionFlag(graphProjectionRecord("deleted", "Deleted"), func(record *GraphProjectionRecord) { record.Deleted = true }),
	}
	projection, err := RebuildGraphFromCanonical(request)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Manifest.DocumentCount != 1 {
		t.Fatalf("rebuild included ineligible canonical data: %#v", projection.Manifest)
	}
}

type graphLifecycleFakeStore struct {
	revoked, repairScheduled bool
}

func (s *graphLifecycleFakeStore) RevokeGraphEvidence(_ context.Context, _ GraphDeletionRequest) (GraphDeletionImpact, error) {
	s.revoked = true
	return GraphDeletionImpact{AffectedEntities: 1, AffectedEdges: 1}, nil
}

func (s *graphLifecycleFakeStore) RecordGraphDeletionAndScheduleRepair(_ context.Context, _ GraphDeletionRequest, _ GraphDeletionImpact) error {
	if !s.revoked {
		panic("repair scheduled before query revocation")
	}
	s.repairScheduled = true
	return nil
}
