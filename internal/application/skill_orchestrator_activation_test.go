package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillAutomaticActivationAdapterRequiresExplicitApprovedLowRiskPromotion(t *testing.T) {
	fixture := newAutomaticActivationFixture(t)
	result, err := fixture.adapter.Execute(context.Background(), fixture.job)
	if err != nil || result.ResultKind != core.SkillJobResultSucceeded || fixture.activator.calls != 1 || !fixture.activator.last.Automatic {
		t.Fatalf("activation = %+v calls=%d request=%+v err=%v", result, fixture.activator.calls, fixture.activator.last, err)
	}
	fixture.repository.activation = fixture.activator.result
	if _, err := fixture.adapter.Execute(context.Background(), fixture.job); err != nil || fixture.activator.calls != 1 {
		t.Fatalf("duplicate activation calls=%d err=%v", fixture.activator.calls, err)
	}

	fixture = newAutomaticActivationFixture(t)
	fixture.repository.policy.AllowAutomaticActivation = false
	_, err = fixture.adapter.Execute(context.Background(), fixture.job)
	assertSkillStageFailure(t, err, core.SkillFailurePolicyBlock, "automatic_activation_not_approved")
	if fixture.activator.calls != 0 {
		t.Fatal("unapproved policy reached activation saga")
	}
}

func TestSkillAutomaticActivationAdapterFailsClosedOnEnablementAndBinding(t *testing.T) {
	fixture := newAutomaticActivationFixture(t)
	fixture.adapter.configuration.Enabled = false
	_, err := fixture.adapter.Execute(context.Background(), fixture.job)
	assertSkillStageFailure(t, err, core.SkillFailurePolicyBlock, "automatic_activation_disabled")

	configuration := automaticActivationTestConfiguration()
	configuration.ApprovalReference = ""
	if _, err := NewSkillAutomaticActivationAdapter(fixture.repository, fixture.activator, configuration); err == nil {
		t.Fatal("missing approval reference was accepted")
	}

	fixture = newAutomaticActivationFixture(t)
	fixture.job.InputDigest = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	_, err = fixture.adapter.Execute(context.Background(), fixture.job)
	assertSkillStageFailure(t, err, core.SkillFailurePermanentValidation, "activation_binding_mismatch")

	fixture = newAutomaticActivationFixture(t)
	fixture.job.PolicyVersion++
	_, err = fixture.adapter.Execute(context.Background(), fixture.job)
	assertSkillStageFailure(t, err, core.SkillFailurePermanentValidation, "activation_binding_mismatch")
}

func TestSkillAutomaticActivationAdapterRevalidatesMidFlightDisable(t *testing.T) {
	fixture := newAutomaticActivationFixture(t)
	configuration := automaticActivationTestConfiguration()
	active := core.SkillOrchestratorConfiguration{
		Scope: fixture.job.Scope, Version: configuration.Signal.ConfigurationVersion, ContractVersion: core.SkillOrchestratorContractVersion,
		Digest: "sha256:" + strings.Repeat("f", 64), PolicyDigest: configuration.Signal.PolicyDigest, Mode: core.SkillOrchestratorDisabled,
		PollInterval: time.Second, ReconciliationInterval: time.Minute, ClaimBatch: 1, WorkerConcurrency: 1, TenantConcurrency: 1, WorkspaceConcurrency: 1,
		DrainTimeout: time.Second, StaleReadinessThreshold: time.Minute,
		StagePolicies:     []core.SkillOrchestratorStagePolicy{{Stage: core.SkillStageActivate, Enabled: true, LeaseDuration: time.Minute, RenewalInterval: time.Second, Timeout: time.Minute, MaxAttempts: 1, InitialBackoff: time.Second, MaximumBackoff: time.Second}},
		ApprovalReference: configuration.ApprovalReference, ReleaseEvidenceReference: configuration.ReleaseEvidenceReference, SignatureReference: configuration.SignatureReference,
		CreatedBy: "operator", CreatedAt: time.Now().UTC(),
	}
	adapter, err := NewSkillAutomaticActivationAdapter(fixture.repository, fixture.activator, configuration, skillActiveConfigurationFixture{configuration: active})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Execute(context.Background(), fixture.job)
	assertSkillStageFailure(t, err, core.SkillFailurePolicyBlock, "automatic_activation_configuration_disabled")
	if fixture.activator.calls != 0 {
		t.Fatal("disabled activation must not call activator")
	}
}

type skillActiveConfigurationFixture struct {
	configuration core.SkillOrchestratorConfiguration
}

func (f skillActiveConfigurationFixture) GetLatestSkillOrchestratorConfiguration(context.Context, core.SkillOrchestratorScope) (core.SkillOrchestratorConfiguration, error) {
	return f.configuration, nil
}

func TestSkillAutomaticActivationAdapterClassifiesStaleGenerationAndCrashReplay(t *testing.T) {
	fixture := newAutomaticActivationFixture(t)
	fixture.activator.err = errors.New("activation generation is stale")
	_, err := fixture.adapter.Execute(context.Background(), fixture.job)
	assertSkillStageFailure(t, err, core.SkillFailureContention, "activation_generation_stale")

	fixture = newAutomaticActivationFixture(t)
	fixture.activator.err = errors.New("materialization root is read-only")
	_, err = fixture.adapter.Execute(context.Background(), fixture.job)
	assertSkillStageFailure(t, err, core.SkillFailureDependencyUnavailable, "automatic_activation_failed")
	if fixture.activator.last.IdempotencyKey != fixture.job.ID {
		t.Fatalf("crash-recovery key = %q", fixture.activator.last.IdempotencyKey)
	}
}

type automaticActivationRepository struct {
	workflow   core.SkillWorkflow
	decision   core.SkillPolicyDecision
	policy     core.SkillPromotionPolicy
	activation core.SkillActivation
}

func (r *automaticActivationRepository) GetSkillWorkflow(_ context.Context, scope core.SkillOrchestratorScope, id string) (core.SkillWorkflow, error) {
	if scope == r.workflow.Scope && id == r.workflow.ID {
		return r.workflow, nil
	}
	return core.SkillWorkflow{}, errors.New("workflow not found")
}

func (r *automaticActivationRepository) GetSkillPolicyDecision(_ context.Context, workspace, id string) (core.SkillPolicyDecision, error) {
	if workspace == r.decision.Workspace && id == r.decision.ID {
		return r.decision, nil
	}
	return core.SkillPolicyDecision{}, errors.New("decision not found")
}

func (r *automaticActivationRepository) GetSkillPromotionPolicy(_ context.Context, workspace, id string, version int64) (core.SkillPromotionPolicy, error) {
	if workspace == r.policy.Workspace && id == r.policy.ID && version == r.policy.Version {
		return r.policy, nil
	}
	return core.SkillPromotionPolicy{}, errors.New("policy not found")
}

func (r *automaticActivationRepository) GetSkillActivation(_ context.Context, workspace, environment, skillID string) (core.SkillActivation, error) {
	if workspace == r.activation.Workspace && environment == r.activation.Environment && skillID == r.activation.SkillID {
		return r.activation, nil
	}
	return core.SkillActivation{}, errors.New("activation not found")
}

type automaticRevisionActivator struct {
	calls  int
	last   SkillActivationRequest
	result core.SkillActivation
	err    error
}

func (a *automaticRevisionActivator) Activate(_ context.Context, input SkillActivationRequest) (core.SkillActivation, error) {
	a.calls++
	a.last = input
	return a.result, a.err
}

type automaticActivationFixture struct {
	repository *automaticActivationRepository
	activator  *automaticRevisionActivator
	adapter    *SkillAutomaticActivationAdapter
	job        core.SkillJob
}

func newAutomaticActivationFixture(t *testing.T) automaticActivationFixture {
	t.Helper()
	now := time.Date(2026, 9, 1, 5, 0, 0, 0, time.UTC)
	configuration := automaticActivationTestConfiguration()
	decision := core.SkillPolicyDecision{ID: "decision-promote", Workspace: "ws", SkillID: "skill-1", RevisionID: "revision-2",
		PolicyID: "policy-1", PolicyVersion: configuration.Signal.PolicyVersion, EvaluationRunIDs: []string{"candidate-run", "baseline-run"},
		RiskTier: core.SkillRiskLow, Decision: core.SkillDecisionPromote, ReasonCodes: []string{"all_policy_gates_passed"}, DecidedAt: now}
	signal, err := SkillLifecycleSignalForPromotion(decision, configuration.Signal)
	if err != nil {
		t.Fatal(err)
	}
	digest := digestSkillLifecycleSignal(signal, nil)
	workflow := core.SkillWorkflow{ID: "workflow-activate", Scope: signal.Scope, OriginKind: core.SkillWorkflowOriginLifecycleSignal,
		OriginID: decision.ID, InputDigest: digest, ConfigurationVersion: configuration.Signal.ConfigurationVersion, PolicyDigest: configuration.Signal.PolicyDigest}
	policy := core.SkillPromotionPolicy{ID: decision.PolicyID, Workspace: decision.Workspace, Version: decision.PolicyVersion,
		RiskTier: core.SkillRiskLow, MinimumCanarySamples: 10, MinimumVerifiedSuccessRate: .95, MaximumFailureRate: .02,
		AllowAutomaticActivation: true, CreatedBy: "operator", CreatedAt: now}
	activation := core.SkillActivation{ID: "activation-1", Workspace: "ws", Environment: "local", SkillID: decision.SkillID,
		ActiveRevisionID: "revision-1", ActiveDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		LastKnownGoodRevisionID: "revision-1", LastKnownGoodDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CanaryRevisionID: decision.RevisionID, CanaryDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Generation: 2, PolicyDecisionID: "decision-canary", Materialization: core.SkillMaterializationReady,
		ActivatedBy: "controller", ActivatedAt: now, UpdatedAt: now}
	result := activation
	result.ActiveRevisionID, result.ActiveDigest = decision.RevisionID, activation.CanaryDigest
	result.CanaryRevisionID, result.CanaryDigest = "", ""
	result.Generation, result.PolicyDecisionID = activation.Generation+1, decision.ID
	repository := &automaticActivationRepository{workflow: workflow, decision: decision, policy: policy, activation: activation}
	activator := &automaticRevisionActivator{result: result}
	adapter, err := NewSkillAutomaticActivationAdapter(repository, activator, configuration, skillActiveConfigurationFixture{configuration: automaticActiveConfiguration(configuration, signal.Scope, now)})
	if err != nil {
		t.Fatal(err)
	}
	job := core.SkillJob{ID: "job-activate", WorkflowID: workflow.ID, Scope: signal.Scope, SkillID: decision.SkillID,
		Stage: core.SkillStageActivate, InputDigest: digest, PolicyVersion: decision.PolicyVersion}
	return automaticActivationFixture{repository: repository, activator: activator, adapter: adapter, job: job}
}

func automaticActiveConfiguration(configuration SkillAutomaticActivationConfiguration, scope core.SkillOrchestratorScope, now time.Time) core.SkillOrchestratorConfiguration {
	return core.SkillOrchestratorConfiguration{
		Scope: scope, Version: configuration.Signal.ConfigurationVersion, ContractVersion: core.SkillOrchestratorContractVersion,
		Digest: "sha256:" + strings.Repeat("f", 64), PolicyDigest: configuration.Signal.PolicyDigest, Mode: core.SkillOrchestratorAutomaticLowRisk,
		PollInterval: time.Second, ReconciliationInterval: time.Minute, ClaimBatch: 1, WorkerConcurrency: 1, TenantConcurrency: 1, WorkspaceConcurrency: 1,
		DrainTimeout: time.Second, StaleReadinessThreshold: time.Minute,
		StagePolicies:     []core.SkillOrchestratorStagePolicy{{Stage: core.SkillStageActivate, Enabled: true, LeaseDuration: time.Minute, RenewalInterval: time.Second, Timeout: time.Minute, MaxAttempts: 1, InitialBackoff: time.Second, MaximumBackoff: time.Second}},
		ApprovalReference: configuration.ApprovalReference, ReleaseEvidenceReference: configuration.ReleaseEvidenceReference, SignatureReference: configuration.SignatureReference,
		CreatedBy: "operator", CreatedAt: now,
	}
}

func automaticActivationTestConfiguration() SkillAutomaticActivationConfiguration {
	return SkillAutomaticActivationConfiguration{Signal: SkillSignalConfiguration{Environment: "local", ConfigurationVersion: 4, PolicyVersion: 7,
		PolicyDigest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}, Enabled: true,
		Actor: "automatic-activation-controller", ApprovalReference: "approval-low-risk-v1",
		ReleaseEvidenceReference: "release-evidence-v1", SignatureReference: "signature-v1"}
}
