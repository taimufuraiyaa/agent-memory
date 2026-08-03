package readingroom

import (
	"context"
	"sync"
	"testing"
	"time"
)

type workflowRunner struct {
	mu                sync.Mutex
	active, maxActive int
}

func (r *workflowRunner) Run(ctx context.Context, input RoleRunInput) (RoleRunResult, error) {
	r.mu.Lock()
	r.active++
	if r.active > r.maxActive {
		r.maxActive = r.active
	}
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		return RoleRunResult{}, ctx.Err()
	case <-time.After(5 * time.Millisecond):
	}
	r.mu.Lock()
	r.active--
	r.mu.Unlock()
	now := time.Now().UTC()
	return RoleRunResult{RunID: input.RunID, NodeID: input.NodeID, ProfileID: input.Profile.ID, ProfileVersion: input.Profile.Version, PacketFingerprint: input.EvidencePacketFingerprint, Model: ModelMetadata{Provider: "test", Model: "fake"}, StartedAt: now, FinishedAt: now}, nil
}
func TestWorkflowRunsIndependentNodesConcurrentlyAndDependenciesLater(t *testing.T) {
	profiles := DefaultProfiles()
	runner := &workflowRunner{}
	workflow := Workflow{MaxFanOut: 2, MaxTotalTokens: 300, Nodes: []WorkflowNode{{ID: "critic", Profile: profiles[RoleCritic], MaxOutputTokens: 100}, {ID: "questioner", Profile: profiles[RoleQuestioner], MaxOutputTokens: 100}, {ID: "connector", Profile: profiles[RoleConnector], DependsOn: []string{"critic", "questioner"}, MaxOutputTokens: 100}}}
	result, err := NewWorkflowExecutor(runner).Execute(context.Background(), "run", workflow, testPacket())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 3 || runner.maxActive != 2 {
		t.Fatalf("unexpected execution: %+v concurrency=%d", result, runner.maxActive)
	}
	if result.Order[2] != "connector" {
		t.Fatalf("dependency ran too early: %v", result.Order)
	}
}
func TestWorkflowRejectsCyclesAndHonorsCancellation(t *testing.T) {
	p := DefaultProfiles()[RoleCritic]
	bad := Workflow{MaxFanOut: 1, MaxTotalTokens: 20, Nodes: []WorkflowNode{{ID: "a", Profile: p, DependsOn: []string{"b"}, MaxOutputTokens: 10}, {ID: "b", Profile: p, DependsOn: []string{"a"}, MaxOutputTokens: 10}}}
	if err := bad.Validate(); err == nil {
		t.Fatal("cycle accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	good := Workflow{MaxFanOut: 1, MaxTotalTokens: 10, Nodes: []WorkflowNode{{ID: "a", Profile: p, MaxOutputTokens: 10}}}
	if _, err := NewWorkflowExecutor(&workflowRunner{}).Execute(ctx, "run", good, testPacket()); err == nil {
		t.Fatal("cancelled workflow continued")
	}
}
