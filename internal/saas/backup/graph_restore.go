package backup

import (
	"fmt"
	"sort"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

// GraphRestoreRequest deliberately contains no native GraphRAG artifact bytes.
// Native artifacts are caches and are never a disaster-recovery dependency.
type GraphRestoreRequest struct {
	Scope           core.GraphScope
	BackupCreatedAt time.Time
	Projection      application.GraphProjectionRequest
	Tombstones      []contracts.GraphDeletionTombstone
}

type GraphRestorePlan struct {
	Projection        application.GraphProjection
	AppliedTombstones []contracts.GraphDeletionTombstone
	RebuildRequired   bool
	NativeArtifacts   int
}

// PlanGraphRestore replays post-backup tombstones before deriving a fresh
// projection from restored canonical records.
func PlanGraphRestore(request GraphRestoreRequest) (GraphRestorePlan, error) {
	if err := request.Scope.Validate(); err != nil || request.Scope.TenantID == "" || request.Projection.Scope != request.Scope || request.BackupCreatedAt.IsZero() {
		return GraphRestorePlan{}, fmt.Errorf("invalid hosted graph restore request")
	}
	deleted := make(map[string]contracts.GraphDeletionTombstone)
	for _, tombstone := range request.Tombstones {
		if tombstone.Scope != request.Scope || tombstone.DeletedAt.IsZero() || !tombstone.DeletedAt.After(request.BackupCreatedAt) {
			continue
		}
		key := tombstone.CanonicalKind + "\x00" + tombstone.CanonicalID
		if current, ok := deleted[key]; !ok || tombstone.DeletedAt.After(current.DeletedAt) {
			deleted[key] = tombstone
		}
	}
	projectionRequest := request.Projection
	projectionRequest.Records = append([]application.GraphProjectionRecord(nil), request.Projection.Records...)
	for index := range projectionRequest.Records {
		record := &projectionRequest.Records[index]
		if _, ok := deleted[string(record.Kind)+"\x00"+record.ID]; ok {
			record.Deleted = true
		}
	}
	projection, err := application.RebuildGraphFromCanonical(projectionRequest)
	if err != nil {
		return GraphRestorePlan{}, err
	}
	applied := make([]contracts.GraphDeletionTombstone, 0, len(deleted))
	for _, tombstone := range deleted {
		applied = append(applied, tombstone)
	}
	sort.Slice(applied, func(i, j int) bool {
		if applied[i].CanonicalKind != applied[j].CanonicalKind {
			return applied[i].CanonicalKind < applied[j].CanonicalKind
		}
		return applied[i].CanonicalID < applied[j].CanonicalID
	})
	return GraphRestorePlan{Projection: projection, AppliedTombstones: applied, RebuildRequired: true, NativeArtifacts: 0}, nil
}
