package contracts

import (
	"context"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type graphIndexProviderFixture struct{}

func (graphIndexProviderFixture) Readiness(context.Context, GraphReadinessRequest) (GraphReadiness, error) {
	return GraphReadiness{State: GraphProviderReady}, nil
}

func (graphIndexProviderFixture) FullIndex(context.Context, GraphIndexRequest) (GraphIndexOperation, error) {
	return GraphIndexOperation{JobID: "job-1", RevisionID: "revision-1"}, nil
}

func (graphIndexProviderFixture) IncrementalUpdate(context.Context, GraphUpdateRequest) (GraphIndexOperation, error) {
	return GraphIndexOperation{JobID: "job-2", RevisionID: "revision-2"}, nil
}

func (graphIndexProviderFixture) Cancel(context.Context, GraphCancelRequest) error { return nil }

func (graphIndexProviderFixture) InspectArtifacts(context.Context, GraphArtifactRequest) (GraphArtifactInspection, error) {
	return GraphArtifactInspection{RevisionID: "revision-1", Complete: true}, nil
}

func TestGraphIndexProviderContractIsOptional(t *testing.T) {
	t.Parallel()

	var provider GraphIndexProvider = graphIndexProviderFixture{}
	readiness, err := ReadGraphProviderReadiness(context.Background(), provider, GraphReadinessRequest{
		Scope: core.GraphScope{WorkspaceID: "workspace-a"},
	})
	if err != nil || readiness.State != GraphProviderReady {
		t.Fatalf("configured provider readiness = %#v, %v", readiness, err)
	}

	readiness, err = ReadGraphProviderReadiness(context.Background(), nil, GraphReadinessRequest{
		Scope: core.GraphScope{WorkspaceID: "workspace-a"},
	})
	if err != nil {
		t.Fatalf("nil provider must degrade without error: %v", err)
	}
	if readiness.State != GraphProviderDisabled {
		t.Fatalf("nil provider state = %q, want %q", readiness.State, GraphProviderDisabled)
	}
}

func TestGraphRequestsRequireScope(t *testing.T) {
	t.Parallel()

	request := GraphIndexRequest{JobID: "job-1", RevisionID: "revision-1", ManifestPath: "manifest.json"}
	if err := request.Validate(); err == nil {
		t.Fatal("index request without workspace scope must be rejected")
	}
	request.Scope = core.GraphScope{WorkspaceID: "workspace-a"}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid index request rejected: %v", err)
	}
}
