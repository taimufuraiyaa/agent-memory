package postgres

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestPostgresSkillReconciliationPartitionLeasePauseAndDeletion(t *testing.T) {
	pool := openSkillOrchestratorPostgres(t)
	scope := createSkillOrchestratorHostedScope(t, pool)
	repository := NewSkillOrchestratorRepository(pool)
	ctx := context.Background()
	now := time.Now().UTC()

	lease, claimed, err := repository.ClaimSkillReconciliationPartition(ctx, scope, "replica-a", time.Minute, now)
	if err != nil || !claimed || lease.Fence != 1 {
		t.Fatalf("first claim=%+v claimed=%v err=%v", lease, claimed, err)
	}
	contended, claimed, err := repository.ClaimSkillReconciliationPartition(ctx, scope, "replica-b", time.Minute, now.Add(time.Second))
	if err != nil || claimed || contended.Owner != "replica-a" {
		t.Fatalf("contended claim=%+v claimed=%v err=%v", contended, claimed, err)
	}
	if err := repository.ReleaseSkillReconciliationPartition(ctx, lease, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	lease, claimed, err = repository.ClaimSkillReconciliationPartition(ctx, scope, "replica-b", time.Minute, now.Add(3*time.Second))
	if err != nil || !claimed || lease.Fence != 2 {
		t.Fatalf("takeover claim=%+v claimed=%v err=%v", lease, claimed, err)
	}
	if err := repository.ReleaseSkillReconciliationPartition(ctx, lease, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetSkillReconciliationRestorePaused(ctx, scope, true, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	paused, claimed, err := repository.ClaimSkillReconciliationPartition(ctx, scope, "replica-a", time.Minute, now.Add(6*time.Second))
	if err != nil || claimed || !paused.RestorePaused {
		t.Fatalf("paused claim=%+v claimed=%v err=%v", paused, claimed, err)
	}
	if err := repository.SetSkillReconciliationRestorePaused(ctx, scope, false, now.Add(7*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM saas_workspaces WHERE tenant_id=$1::uuid AND id=$2::uuid`, scope.TenantID, scope.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	_, claimed, err = repository.ClaimSkillReconciliationPartition(ctx, scope, "replica-a", time.Minute, now.Add(8*time.Second))
	if err != nil || claimed {
		t.Fatalf("deleted workspace claimed=%v err=%v", claimed, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM saas_skill_orchestrator_reconciliation_partitions WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid`, scope.TenantID, scope.WorkspaceID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted partition count=%d err=%v", count, err)
	}
}

func TestPostgresSkillReconciliationPartitionAllowsOneConcurrentReplica(t *testing.T) {
	pool := openSkillOrchestratorPostgres(t)
	scope := createSkillOrchestratorHostedScope(t, pool)
	repository := NewSkillOrchestratorRepository(pool)
	now := time.Now().UTC()
	start := make(chan struct{})
	type result struct {
		claimed bool
		err     error
	}
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, owner := range []string{"replica-a", "replica-b"} {
		owner := owner
		go func() {
			ready.Done()
			<-start
			_, claimed, err := repository.ClaimSkillReconciliationPartition(context.Background(), scope, owner, time.Minute, now)
			results <- result{claimed: claimed, err: err}
		}()
	}
	ready.Wait()
	close(start)
	claimedCount := 0
	for range 2 {
		item := <-results
		if item.err != nil {
			t.Fatal(item.err)
		}
		if item.claimed {
			claimedCount++
		}
	}
	if claimedCount != 1 {
		t.Fatalf("concurrent claimed count=%d, want 1", claimedCount)
	}
}
