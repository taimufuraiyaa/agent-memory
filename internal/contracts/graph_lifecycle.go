package contracts

import (
	"context"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type GraphDeletionRequest struct {
	Scope          core.GraphScope
	CanonicalKind  string
	CanonicalID    string
	DeletedAt      time.Time
	RepairDeadline time.Time
}

type GraphDeletionImpact struct {
	AffectedEntities int
	AffectedEdges    int
	AffectedReports  int
}

type GraphDeletionTombstone struct {
	Scope         core.GraphScope
	CanonicalKind string
	CanonicalID   string
	DeletedAt     time.Time
}

func (t GraphDeletionTombstone) Blocks(kind, id string, artifactCreatedAt time.Time) bool {
	return t.CanonicalKind == kind && t.CanonicalID == id && !artifactCreatedAt.After(t.DeletedAt)
}

type GraphDeletionResult struct {
	GraphDeletionImpact
	Tombstone      GraphDeletionTombstone
	RepairDeadline time.Time
}

type GraphLifecycleRepository interface {
	RevokeGraphEvidence(context.Context, GraphDeletionRequest) (GraphDeletionImpact, error)
	RecordGraphDeletionAndScheduleRepair(context.Context, GraphDeletionRequest, GraphDeletionImpact) error
}

// GraphLifecycleAtomicRepository is the production-strength deletion boundary.
// Implementations revoke evidence, write the tombstone, and schedule repair in
// one transaction. GraphLifecycleRepository remains supported for stores that
// have not yet adopted the atomic boundary.
type GraphLifecycleAtomicRepository interface {
	DeleteGraphEvidenceAtomic(context.Context, GraphDeletionRequest) (GraphDeletionImpact, error)
}
