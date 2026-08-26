package deletion

import (
	"context"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphDeletionUsesAtomicEvidenceRevocationAndDurableTombstone(t *testing.T) {
	now := time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC)
	scope := core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}
	repository := &atomicGraphLifecycleRepository{}
	service := application.NewGraphLifecycleService(repository)
	result, err := service.DeleteCanonicalEvidence(context.Background(), contracts.GraphDeletionRequest{
		Scope: scope, CanonicalKind: "source_text", CanonicalID: "source-a", DeletedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !repository.atomicCalled || repository.splitCalled || result.Tombstone.Scope != scope || result.Tombstone.CanonicalID != "source-a" {
		t.Fatalf("deletion did not use atomic hosted boundary: result=%+v repository=%+v", result, repository)
	}
	if result.RepairDeadline != now.Add(30*time.Minute) || result.AffectedEntities != 2 || result.AffectedEdges != 1 || result.AffectedReports != 1 {
		t.Fatalf("unexpected graph deletion result: %+v", result)
	}
	// A tombstone is an owned lifecycle record, not a native artifact. Simulated
	// artifact loss therefore cannot remove the deletion decision.
	restoredTombstone := result.Tombstone
	if !restoredTombstone.Blocks("source_text", "source-a", now.Add(-time.Hour)) {
		t.Fatal("restored tombstone allowed an older graph artifact to resurrect evidence")
	}
}

type atomicGraphLifecycleRepository struct {
	atomicCalled bool
	splitCalled  bool
}

func (r *atomicGraphLifecycleRepository) DeleteGraphEvidenceAtomic(context.Context, contracts.GraphDeletionRequest) (contracts.GraphDeletionImpact, error) {
	r.atomicCalled = true
	return contracts.GraphDeletionImpact{AffectedEntities: 2, AffectedEdges: 1, AffectedReports: 1}, nil
}
func (r *atomicGraphLifecycleRepository) RevokeGraphEvidence(context.Context, contracts.GraphDeletionRequest) (contracts.GraphDeletionImpact, error) {
	r.splitCalled = true
	return contracts.GraphDeletionImpact{}, nil
}
func (r *atomicGraphLifecycleRepository) RecordGraphDeletionAndScheduleRepair(context.Context, contracts.GraphDeletionRequest, contracts.GraphDeletionImpact) error {
	r.splitCalled = true
	return nil
}
