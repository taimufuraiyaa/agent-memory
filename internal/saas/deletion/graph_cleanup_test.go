package deletion

import (
	"context"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphDeletionPurgerUsesTenantScopedTargetAndVerifiesAbsence(t *testing.T) {
	t.Parallel()
	store := &graphCleanupFakeStore{}
	purger := NewGraphDeletionPurger(store)
	op := Operation{TenantID: "tenant-a", ID: "operation-a", TargetType: "source", TargetID: "source-a"}
	if err := purger.Purge(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	if store.request.Scope != (core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}) || !store.verified {
		t.Fatalf("graph cleanup was unscoped or unverified: %#v", store)
	}
}

type graphCleanupFakeStore struct {
	request  GraphCleanupRequest
	verified bool
}

func (s *graphCleanupFakeStore) CleanupGraph(_ context.Context, request GraphCleanupRequest) (GraphCleanupResult, error) {
	s.request = request
	return GraphCleanupResult{WorkspaceID: "workspace-a", RemainingRecords: 0, RemainingArtifacts: 0}, nil
}

func (s *graphCleanupFakeStore) VerifyGraphAbsence(_ context.Context, request GraphCleanupRequest) error {
	s.request = request
	s.verified = true
	return nil
}
