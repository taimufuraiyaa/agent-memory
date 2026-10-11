package application

import (
	"context"
	"errors"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphOperationRequiresIdempotencyAndRevisionIntent(t *testing.T) {
	service := NewGraphOperationService(rejectingGraphOperationStore{})
	_, err := service.Operate(context.Background(), GraphOperationRequest{Scope: core.GraphScope{WorkspaceID: "ws"}, ConfigurationID: "default", Action: GraphOperationUpdate})
	if !errors.Is(err, ErrGraphOperationInvalid) {
		t.Fatalf("expected invalid operation, got %v", err)
	}
}

type rejectingGraphOperationStore struct{}

func (rejectingGraphOperationStore) GraphIndexReadiness(context.Context, core.GraphScope, string) (GraphIndexReadiness, error) {
	return GraphIndexReadiness{}, nil
}
func (rejectingGraphOperationStore) GraphIndexStatus(context.Context, core.GraphScope, string) (GraphIndexStatus, error) {
	return GraphIndexStatus{}, nil
}
func (rejectingGraphOperationStore) ApplyGraphOperation(context.Context, GraphOperationRequest) (GraphOperationResult, error) {
	return GraphOperationResult{}, errors.New("unexpected store call")
}
