package application

import (
	"context"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphActivationAndRollbackUseAtomicPointers(t *testing.T) {
	t.Parallel()
	repository := &graphActivationFakeRepository{active: "revision-1", previous: "revision-0", ready: map[string]bool{"revision-0": true, "revision-1": true, "revision-2": true}}
	service := NewGraphActivationService(repository)
	scope := core.GraphScope{WorkspaceID: "workspace-a"}
	if err := service.Activate(context.Background(), core.GraphActivation{Scope: scope, ConfigurationID: "configuration-1", ExpectedRevision: "revision-1", CandidateRevision: "revision-2"}); err != nil {
		t.Fatal(err)
	}
	if repository.active != "revision-2" || repository.previous != "revision-1" {
		t.Fatalf("activation pointers = %q/%q", repository.active, repository.previous)
	}
	if err := service.Rollback(context.Background(), scope, "configuration-1"); err != nil {
		t.Fatal(err)
	}
	if repository.active != "revision-1" || repository.previous != "revision-2" {
		t.Fatalf("rollback pointers = %q/%q", repository.active, repository.previous)
	}
}

func TestGraphActivationFreshnessExposesStaleWatermark(t *testing.T) {
	t.Parallel()
	indexed := core.GraphWatermark{Sequence: 10, Digest: "sha256:indexed"}
	current := core.GraphWatermark{Sequence: 12, Digest: "sha256:current"}
	status := GraphWatermarkFreshness(indexed, current)
	if !status.Stale || status.PendingChanges != 2 {
		t.Fatalf("freshness = %#v", status)
	}
}

type graphActivationFakeRepository struct {
	active, previous string
	ready            map[string]bool
}

func (r *graphActivationFakeRepository) ActivateGraphRevision(_ context.Context, activation core.GraphActivation) error {
	if r.active != activation.ExpectedRevision || !r.ready[activation.CandidateRevision] {
		return errGraphActivationConflict
	}
	r.previous, r.active = r.active, activation.CandidateRevision
	return nil
}

func (r *graphActivationFakeRepository) ActiveGraphRevisions(_ context.Context, _ core.GraphScope, _ string) (string, string, error) {
	return r.active, r.previous, nil
}
