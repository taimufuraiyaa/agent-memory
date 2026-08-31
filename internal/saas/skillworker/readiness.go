package skillworker

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/observability"
)

type PostgresReadiness struct {
	Pool           *pgxpool.Pool
	Registry       *application.SkillStageRegistry
	Configurations SkillWorkerConfigurationSource
	Executors      SkillWorkerStageReadinessProbe
	PolicyGate     SkillWorkerPolicyGateProbe
}

type SkillWorkerConfigurationSource interface {
	GetLatestSkillOrchestratorConfiguration(context.Context, core.SkillOrchestratorScope) (core.SkillOrchestratorConfiguration, error)
}

type SkillWorkerStageReadinessProbe interface {
	CheckSkillStageReadiness(context.Context, core.SkillOrchestratorScope, core.SkillOrchestratorStage) error
}

type SkillWorkerPolicyGateProbe interface {
	SkillAutomaticPromotionBlocked(context.Context, core.SkillOrchestratorScope, core.SkillOrchestratorConfiguration) (bool, error)
}

type SkillOrchestratorSLOTargets = core.SkillOrchestratorAlertTargets

type SkillStageReadinessState string

const (
	SkillStageReady    SkillStageReadinessState = "ready"
	SkillStageDegraded SkillStageReadinessState = "degraded"
	SkillStageUnready  SkillStageReadinessState = "unready"
)

type SkillStageReadiness struct {
	Stage core.SkillOrchestratorStage
	State SkillStageReadinessState
	Code  string
}

type SkillWorkerReadinessReport struct {
	Ready       bool
	Degraded    bool
	ProcessLive bool
	Stages      []SkillStageReadiness
}

func (r *PostgresReadiness) CheckSkillWorkerReadiness(ctx context.Context, configuration RuntimeConfig) error {
	if r == nil || r.Pool == nil || r.Registry == nil || r.Configurations == nil || r.Executors == nil || r.PolicyGate == nil {
		return errors.New("hosted skill worker readiness dependencies are required")
	}
	if err := r.Pool.Ping(ctx); err != nil {
		return err
	}
	var role string
	var orchestrationTable bool
	if err := r.Pool.QueryRow(ctx, `SELECT current_user,to_regclass('public.saas_skill_orchestrator_jobs') IS NOT NULL`).Scan(&role, &orchestrationTable); err != nil {
		return err
	}
	if role != configuration.DatabaseRole || role != DatabaseRole {
		return errors.New("hosted skill worker database role is not least-privilege worker role")
	}
	if !orchestrationTable {
		return errors.New("hosted skill worker orchestration migration is unavailable")
	}
	if !r.Registry.Supports(core.SkillOrchestratorContractVersion, core.SkillStageRollback) {
		return errors.New("hosted skill worker rollback executor is unavailable")
	}
	for _, scope := range configuration.Assignments {
		active, err := r.Configurations.GetLatestSkillOrchestratorConfiguration(ctx, scope)
		if err != nil {
			return errors.New("hosted skill worker active configuration is unavailable")
		}
		metrics := observability.DefaultSkillOrchestratorMetrics()
		metrics.ObserveConfiguration(active.Mode, active.Scope.Environment)
		metrics.ObserveTarget("ready_queue_stuck_seconds", active.Scope.Environment, active.AlertTargets.ReadyQueueStuckAfter.Seconds())
		metrics.ObserveTarget("lease_failure_count", active.Scope.Environment, float64(active.AlertTargets.LeaseFailureCount))
		metrics.ObserveTarget("canary_stale_seconds", active.Scope.Environment, active.AlertTargets.CanaryStaleAfter.Seconds())
		metrics.ObserveTarget("rollback_failure_seconds", active.Scope.Environment, active.AlertTargets.RollbackFailureAfter.Seconds())
		blocked, err := r.PolicyGate.SkillAutomaticPromotionBlocked(ctx, scope, active)
		if err != nil {
			return errors.New("hosted skill worker policy gate is unavailable")
		}
		report := EvaluateSkillWorkerReadiness(ctx, active, r.Registry, r.Executors, active.AlertTargets, blocked)
		if !report.Ready {
			return fmt.Errorf("hosted skill worker stage readiness failed: %s", firstUnreadyCode(report))
		}
	}
	return nil
}

func EvaluateSkillWorkerReadiness(ctx context.Context, configuration core.SkillOrchestratorConfiguration, registry *application.SkillStageRegistry, executors SkillWorkerStageReadinessProbe, targets SkillOrchestratorSLOTargets, policyBlocked bool) SkillWorkerReadinessReport {
	report := SkillWorkerReadinessReport{Ready: true, ProcessLive: true, Stages: []SkillStageReadiness{}}
	if err := configuration.Validate(); err != nil || registry == nil || executors == nil {
		report.Ready = false
		report.Stages = append(report.Stages, SkillStageReadiness{State: SkillStageUnready, Code: "invalid_configuration"})
		return report
	}
	for _, policy := range configuration.StagePolicies {
		if !configuration.ClaimsEnabled(policy.Stage) {
			continue
		}
		stage := SkillStageReadiness{Stage: policy.Stage, State: SkillStageReady, Code: "ready"}
		switch {
		case targets.ReadyQueueStuckAfter <= 0 || targets.LeaseChurnWindow <= 0 || targets.LeaseFailureCount < 1:
			stage.State, stage.Code = SkillStageUnready, "missing_slo_target"
		case (policy.Stage == core.SkillStageStartCanary || policy.Stage == core.SkillStageAnalyzeCanary) && targets.CanaryStaleAfter <= 0:
			stage.State, stage.Code = SkillStageUnready, "missing_canary_target"
		case policy.Stage == core.SkillStageRollback && targets.RollbackFailureAfter <= 0:
			stage.State, stage.Code = SkillStageUnready, "missing_rollback_target"
		case !registry.Supports(configuration.ContractVersion, policy.Stage):
			stage.State, stage.Code = SkillStageUnready, "unsupported_executor"
		case executors.CheckSkillStageReadiness(ctx, configuration.Scope, policy.Stage) != nil:
			stage.State, stage.Code = SkillStageUnready, "executor_unavailable"
		case policy.Stage == core.SkillStageActivate && policyBlocked:
			stage.State, stage.Code = SkillStageDegraded, "policy_blocked"
		}
		if stage.State == SkillStageUnready {
			report.Ready = false
		}
		if stage.State == SkillStageDegraded {
			report.Degraded = true
		}
		report.Stages = append(report.Stages, stage)
	}
	return report
}

func firstUnreadyCode(report SkillWorkerReadinessReport) string {
	for _, stage := range report.Stages {
		if stage.State == SkillStageUnready {
			return stage.Code
		}
	}
	return "unknown"
}
