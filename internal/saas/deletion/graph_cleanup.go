package deletion

import (
	"context"
	"fmt"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type GraphCleanupRequest struct {
	Scope       core.GraphScope
	OperationID string
	TargetType  string
	TargetID    string
}

type GraphCleanupResult struct {
	WorkspaceID        string
	RemainingRecords   int
	RemainingArtifacts int
}

type GraphCleanupStore interface {
	CleanupGraph(context.Context, GraphCleanupRequest) (GraphCleanupResult, error)
	VerifyGraphAbsence(context.Context, GraphCleanupRequest) error
}

type GraphDeletionPurger struct{ store GraphCleanupStore }

func NewGraphDeletionPurger(store GraphCleanupStore) *GraphDeletionPurger {
	return &GraphDeletionPurger{store: store}
}

func (p *GraphDeletionPurger) Purge(ctx context.Context, operation Operation) error {
	if p == nil || p.store == nil || strings.TrimSpace(operation.TenantID) == "" || strings.TrimSpace(operation.ID) == "" || strings.TrimSpace(operation.TargetType) == "" || strings.TrimSpace(operation.TargetID) == "" {
		return fmt.Errorf("invalid graph deletion operation")
	}
	request := GraphCleanupRequest{Scope: core.GraphScope{TenantID: operation.TenantID, WorkspaceID: "pending"}, OperationID: operation.ID, TargetType: operation.TargetType, TargetID: operation.TargetID}
	result, err := p.store.CleanupGraph(ctx, request)
	if err != nil {
		return err
	}
	if strings.TrimSpace(result.WorkspaceID) == "" || result.RemainingRecords != 0 || result.RemainingArtifacts != 0 {
		return fmt.Errorf("graph deletion absence is not verified")
	}
	request.Scope.WorkspaceID = result.WorkspaceID
	return p.store.VerifyGraphAbsence(ctx, request)
}
