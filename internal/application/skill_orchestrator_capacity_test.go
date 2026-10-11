package application

import (
	"context"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillCapacityCoordinatorPreservesRollbackAndSkipsNoisyTenantHead(t *testing.T) {
	coordinator, err := NewSkillCapacityCoordinator(SkillCapacityLimits{Global: 3, Tenant: 1, Workspace: 1, RollbackReserved: 1, Stages: map[core.SkillOrchestratorStage]int{core.SkillStageEvaluate: 1}})
	if err != nil {
		t.Fatal(err)
	}
	ordinaryA := capacityTestJob("tenant-a", "workspace-a", core.SkillStageBuild)
	permitA, err := coordinator.Acquire(context.Background(), ordinaryA)
	if err != nil {
		t.Fatal(err)
	}
	blockedA := make(chan SkillCapacityPermit, 1)
	go func() {
		permit, _ := coordinator.Acquire(context.Background(), capacityTestJob("tenant-a", "workspace-b", core.SkillStageBuild))
		blockedA <- permit
	}()
	permitB, err := coordinator.Acquire(context.Background(), capacityTestJob("tenant-b", "workspace-b", core.SkillStageEvaluate))
	if err != nil {
		t.Fatal(err)
	}
	rollback, err := coordinator.Acquire(context.Background(), capacityTestJob("tenant-a", "workspace-a", core.SkillStageRollback))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-blockedA:
		t.Fatal("noisy tenant exceeded its concurrency quota")
	default:
	}
	permitA.Release()
	select {
	case released := <-blockedA:
		released.Release()
	case <-time.After(time.Second):
		t.Fatal("eligible tenant waiter was not admitted")
	}
	permitB.Release()
	rollback.Release()
}

func TestSkillCapacityCoordinatorCancellationDoesNotLeakPermit(t *testing.T) {
	coordinator, _ := NewSkillCapacityCoordinator(SkillCapacityLimits{Global: 2, Tenant: 1, Workspace: 1, RollbackReserved: 1})
	held, _ := coordinator.Acquire(context.Background(), capacityTestJob("tenant-a", "workspace-a", core.SkillStageBuild))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := coordinator.Acquire(ctx, capacityTestJob("tenant-b", "workspace-b", core.SkillStageBuild)); err == nil {
		t.Fatal("cancelled capacity request succeeded")
	}
	held.Release()
	permit, err := coordinator.Acquire(context.Background(), capacityTestJob("tenant-b", "workspace-b", core.SkillStageBuild))
	if err != nil {
		t.Fatal(err)
	}
	permit.Release()
}

func capacityTestJob(tenant, workspace string, stage core.SkillOrchestratorStage) core.SkillJob {
	return core.SkillJob{Scope: core.SkillOrchestratorScope{TenantID: tenant, WorkspaceID: workspace, Environment: "production"}, Stage: stage}
}
