package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSQLiteSkillOrchestratorConfigurationIsVersionedScopedAndAudited(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	configuration := sqliteSkillConfiguration(now)
	created, err := store.StoreSkillOrchestratorConfiguration(ctx, configuration, core.SkillOrchestratorConfigurationAudit{ActorID: "operator-1", RequestID: "request-1", Operation: "skill_orchestrator.configuration.create", ToVersion: 1, ReasonCode: "enable", OccurredAt: now})
	if err != nil || !created {
		t.Fatalf("store configuration: created=%v err=%v", created, err)
	}
	stored, err := store.GetLatestSkillOrchestratorConfiguration(ctx, configuration.Scope)
	if err != nil || stored.Digest != configuration.Digest || stored.PolicyDigest != configuration.PolicyDigest {
		t.Fatalf("load configuration: %+v err=%v", stored, err)
	}
	events, err := store.ListAuditEvents(ctx, AuditFilter{Workspace: configuration.Scope.WorkspaceID, Operation: "skill_orchestrator.configuration.create", Limit: 10})
	if err != nil || len(events) != 1 || events[0].Metadata["mode"] != string(core.SkillOrchestratorManual) {
		t.Fatalf("expected content-free audit event, got %+v err=%v", events, err)
	}
	other := configuration.Scope
	other.WorkspaceID = "other"
	if _, err := store.GetLatestSkillOrchestratorConfiguration(ctx, other); err != core.ErrSkillOrchestratorConfigurationNotFound {
		t.Fatalf("expected scoped not found, got %v", err)
	}
	created, err = store.StoreSkillOrchestratorConfiguration(ctx, configuration, core.SkillOrchestratorConfigurationAudit{ActorID: "operator-1", RequestID: "request-1", Operation: "skill_orchestrator.configuration.create", ToVersion: 1, ReasonCode: "enable", OccurredAt: now})
	if err != nil || created {
		t.Fatalf("expected idempotent replay, created=%v err=%v", created, err)
	}
}

func sqliteSkillConfiguration(now time.Time) core.SkillOrchestratorConfiguration {
	return core.SkillOrchestratorConfiguration{
		Scope: core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"}, Version: 1,
		ContractVersion: core.SkillOrchestratorContractVersion, Digest: "sha256:" + strings.Repeat("b", 64), PolicyDigest: "sha256:" + strings.Repeat("a", 64), Mode: core.SkillOrchestratorManual,
		PollInterval: time.Second, ReconciliationInterval: time.Minute, ClaimBatch: 10, WorkerConcurrency: 4, TenantConcurrency: 4, WorkspaceConcurrency: 2,
		DrainTimeout: 30 * time.Second, StaleReadinessThreshold: 5 * time.Minute, EvaluationBudgetUnits: 100,
		AlertTargets:  core.SkillOrchestratorAlertTargets{ReadyQueueStuckAfter: 5 * time.Minute, LeaseChurnWindow: 15 * time.Minute, LeaseFailureCount: 5, CanaryStaleAfter: 24 * time.Hour, RollbackFailureAfter: 5 * time.Minute},
		StagePolicies: []core.SkillOrchestratorStagePolicy{{Stage: core.SkillStageDetect, Enabled: true, LeaseDuration: time.Minute, RenewalInterval: 20 * time.Second, Timeout: 45 * time.Second, MaxAttempts: 3, InitialBackoff: time.Second, MaximumBackoff: time.Minute}},
		CreatedBy:     "operator-1", CreatedAt: now,
	}
}
