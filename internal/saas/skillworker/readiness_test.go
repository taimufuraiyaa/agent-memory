package skillworker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillWorkerReadinessDistinguishesMissingTargetAndEvaluatorOutageFromLiveness(t *testing.T) {
	configuration, registry := readinessConfiguration(t)
	probe := &stageReadinessProbe{}
	targets := SkillOrchestratorSLOTargets{ReadyQueueStuckAfter: time.Minute, LeaseChurnWindow: time.Minute, LeaseFailureCount: 5, CanaryStaleAfter: time.Hour, RollbackFailureAfter: time.Minute}
	report := EvaluateSkillWorkerReadiness(context.Background(), configuration, registry, probe, SkillOrchestratorSLOTargets{}, false)
	if report.Ready || !report.ProcessLive || firstUnreadyCode(report) != "missing_slo_target" {
		t.Fatalf("missing target report = %+v", report)
	}
	probe.fail = core.SkillStageEvaluate
	report = EvaluateSkillWorkerReadiness(context.Background(), configuration, registry, probe, targets, false)
	if report.Ready || !report.ProcessLive || firstUnreadyCode(report) != "executor_unavailable" {
		t.Fatalf("evaluator outage report = %+v", report)
	}
}

func TestSkillWorkerReadinessTreatsPolicyBlockAsDegradedButReady(t *testing.T) {
	configuration, registry := readinessConfiguration(t)
	probe := &stageReadinessProbe{}
	targets := SkillOrchestratorSLOTargets{ReadyQueueStuckAfter: time.Minute, LeaseChurnWindow: time.Minute, LeaseFailureCount: 5, CanaryStaleAfter: time.Hour, RollbackFailureAfter: time.Minute}
	report := EvaluateSkillWorkerReadiness(context.Background(), configuration, registry, probe, targets, true)
	if !report.Ready || !report.Degraded || !report.ProcessLive {
		t.Fatalf("policy-blocked report = %+v", report)
	}
	found := false
	for _, stage := range report.Stages {
		if stage.Stage == core.SkillStageActivate && stage.Code == "policy_blocked" {
			found = true
		}
	}
	if !found {
		t.Fatal("activation policy degradation was not reported")
	}
}

func readinessConfiguration(t *testing.T) (core.SkillOrchestratorConfiguration, *application.SkillStageRegistry) {
	t.Helper()
	now := time.Now().UTC()
	configuration := core.SkillOrchestratorConfiguration{
		Scope: core.SkillOrchestratorScope{TenantID: "tenant", WorkspaceID: "workspace", Environment: "production"}, Version: 1,
		ContractVersion: core.SkillOrchestratorContractVersion, Digest: "sha256:" + strings.Repeat("a", 64), PolicyDigest: "sha256:" + strings.Repeat("b", 64), Mode: core.SkillOrchestratorAutomaticLowRisk,
		PollInterval: time.Second, ReconciliationInterval: time.Minute, ClaimBatch: 2, WorkerConcurrency: 2, TenantConcurrency: 2, WorkspaceConcurrency: 1,
		DrainTimeout: time.Minute, StaleReadinessThreshold: time.Minute, EvaluationBudgetUnits: 10,
		AlertTargets:      core.SkillOrchestratorAlertTargets{ReadyQueueStuckAfter: time.Minute, LeaseChurnWindow: time.Minute, LeaseFailureCount: 5, CanaryStaleAfter: time.Hour, RollbackFailureAfter: time.Minute},
		ApprovalReference: "approval", ReleaseEvidenceReference: "release", SignatureReference: "signature", CreatedBy: "operator", CreatedAt: now,
	}
	for _, stage := range []core.SkillOrchestratorStage{core.SkillStageEvaluate, core.SkillStageActivate, core.SkillStageRollback} {
		configuration.StagePolicies = append(configuration.StagePolicies, core.SkillOrchestratorStagePolicy{Stage: stage, Enabled: true, LeaseDuration: time.Minute, RenewalInterval: time.Second, Timeout: time.Minute, MaxAttempts: 1, InitialBackoff: time.Second, MaximumBackoff: time.Second})
	}
	registry := application.NewSkillStageRegistry()
	for _, policy := range configuration.StagePolicies {
		if err := registry.Register(configuration.ContractVersion, policy.Stage, application.SkillStageAdapterFunc(func(context.Context, core.SkillJob) (application.SkillStageResult, error) {
			return application.SkillStageResult{}, nil
		})); err != nil {
			t.Fatal(err)
		}
	}
	return configuration, registry
}

type stageReadinessProbe struct{ fail core.SkillOrchestratorStage }

func (p *stageReadinessProbe) CheckSkillStageReadiness(_ context.Context, _ core.SkillOrchestratorScope, stage core.SkillOrchestratorStage) error {
	if stage == p.fail {
		return errors.New("dependency unavailable")
	}
	return nil
}
