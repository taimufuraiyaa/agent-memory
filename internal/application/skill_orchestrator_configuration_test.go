package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillOrchestratorConfigurationServiceRequiresBoundIndependentEvidence(t *testing.T) {
	fixture := newSkillConfigurationFixture(t, core.SkillOrchestratorAutomaticLowRisk)
	withoutVerifier := NewSkillOrchestratorConfigurationService(fixture.repository, skillConfigurationAuthorizer{}, nil, func() time.Time { return fixture.now })
	if _, err := withoutVerifier.Create(context.Background(), fixture.change); err == nil || !strings.Contains(err.Error(), "signed evidence verifier") {
		t.Fatalf("expected missing signed evidence rejection, got %v", err)
	}
	fixture.verifier.evidence.ConfigurationDigest = "sha256:" + strings.Repeat("f", 64)
	if _, err := fixture.service.Create(context.Background(), fixture.change); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected evidence digest mismatch, got %v", err)
	}
	fixture.verifier.evidence.ConfigurationDigest = fixture.change.Configuration.Digest
	fixture.verifier.evidence.ApproverID = fixture.change.ActorID
	if _, err := fixture.service.Create(context.Background(), fixture.change); err == nil || !strings.Contains(err.Error(), "separation") {
		t.Fatalf("expected separation-of-duty rejection, got %v", err)
	}
	fixture.verifier.evidence.ApproverID = "product-approver"
	stored, err := fixture.service.Create(context.Background(), fixture.change)
	if err != nil || stored.Version != 1 || len(fixture.repository.audits) != 1 {
		t.Fatalf("expected audited configuration, got stored=%+v audits=%d err=%v", stored, len(fixture.repository.audits), err)
	}
}

func TestSkillOrchestratorConfigurationServiceRejectsInvalidBoundsAndStaleVersion(t *testing.T) {
	fixture := newSkillConfigurationFixture(t, core.SkillOrchestratorManual)
	fixture.change.Configuration.ClaimBatch = 0
	fixture.change.Configuration.Digest, _ = ComputeSkillOrchestratorConfigurationDigest(fixture.change.Configuration)
	if _, err := fixture.service.Create(context.Background(), fixture.change); err == nil || !strings.Contains(err.Error(), "bounds") {
		t.Fatalf("expected invalid bound rejection, got %v", err)
	}
	fixture = newSkillConfigurationFixture(t, core.SkillOrchestratorManual)
	fixture.change.ExpectedLatestVersion = 1
	if _, err := fixture.service.Create(context.Background(), fixture.change); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected stale version rejection, got %v", err)
	}
}

func TestSkillOrchestratorConfigurationRollbackCreatesNewImmutableVersion(t *testing.T) {
	fixture := newSkillConfigurationFixture(t, core.SkillOrchestratorManual)
	first, err := fixture.service.Create(context.Background(), fixture.change)
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.Version = 2
	second.Mode = core.SkillOrchestratorCanary
	second.CreatedAt = fixture.now.Add(time.Minute)
	second.Digest, _ = ComputeSkillOrchestratorConfigurationDigest(second)
	if _, err := fixture.service.Create(context.Background(), SkillOrchestratorConfigurationChange{Configuration: second, ActorID: second.CreatedBy, RequestID: "request-2", ExpectedLatestVersion: 1, ReasonCode: "advance"}); err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(2 * time.Minute)
	fixture.service.now = func() time.Time { return fixture.now }
	rolled, err := fixture.service.Rollback(context.Background(), first.Scope, 1, "operator-2", "request-3", "rollback")
	if err != nil || rolled.Version != 3 || rolled.Mode != core.SkillOrchestratorManual || rolled.Digest == first.Digest {
		t.Fatalf("expected immutable rollback version, got %+v err=%v", rolled, err)
	}
	if fixture.repository.configurations[1].Mode != core.SkillOrchestratorManual || fixture.repository.configurations[2].Mode != core.SkillOrchestratorCanary {
		t.Fatal("rollback must not mutate prior versions")
	}
}

type skillConfigurationFixture struct {
	service    *SkillOrchestratorConfigurationService
	repository *skillConfigurationRepository
	verifier   *skillConfigurationVerifier
	change     SkillOrchestratorConfigurationChange
	now        time.Time
}

func newSkillConfigurationFixture(t *testing.T, mode core.SkillOrchestratorMode) *skillConfigurationFixture {
	t.Helper()
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	configuration := core.SkillOrchestratorConfiguration{
		Scope: core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"}, Version: 1,
		ContractVersion: core.SkillOrchestratorContractVersion, PolicyDigest: "sha256:" + strings.Repeat("a", 64), Mode: mode,
		PollInterval: time.Second, ReconciliationInterval: time.Minute, ClaimBatch: 10, WorkerConcurrency: 4, TenantConcurrency: 4, WorkspaceConcurrency: 2,
		DrainTimeout: 30 * time.Second, StaleReadinessThreshold: 5 * time.Minute, EvaluationBudgetUnits: 100,
		AlertTargets:  core.SkillOrchestratorAlertTargets{ReadyQueueStuckAfter: 5 * time.Minute, LeaseChurnWindow: 15 * time.Minute, LeaseFailureCount: 5, CanaryStaleAfter: 24 * time.Hour, RollbackFailureAfter: 5 * time.Minute},
		StagePolicies: []core.SkillOrchestratorStagePolicy{{Stage: core.SkillStageDetect, Enabled: true, LeaseDuration: time.Minute, RenewalInterval: 20 * time.Second, Timeout: 45 * time.Second, MaxAttempts: 3, InitialBackoff: time.Second, MaximumBackoff: time.Minute}},
		CreatedBy:     "operator-1", CreatedAt: now,
	}
	if mode == core.SkillOrchestratorAutomaticLowRisk {
		configuration.ApprovalReference, configuration.ReleaseEvidenceReference, configuration.SignatureReference = "approval-1", "release-1", "signature-1"
	}
	configuration.Digest, _ = ComputeSkillOrchestratorConfigurationDigest(configuration)
	repository := &skillConfigurationRepository{configurations: map[int64]core.SkillOrchestratorConfiguration{}}
	verifier := &skillConfigurationVerifier{evidence: SkillOrchestratorEnablementEvidence{ApprovalReference: configuration.ApprovalReference, ReleaseEvidenceReference: configuration.ReleaseEvidenceReference, SignatureReference: configuration.SignatureReference, ConfigurationDigest: configuration.Digest, PolicyDigest: configuration.PolicyDigest, ApproverID: "product-approver", ReleaseReviewerID: "release-reviewer", SignerID: "release-signer", BuildVersion: "build-1", MigrationVersion: "migration-35", VerifiedAt: now}}
	service := NewSkillOrchestratorConfigurationService(repository, skillConfigurationAuthorizer{}, verifier, func() time.Time { return now })
	return &skillConfigurationFixture{service: service, repository: repository, verifier: verifier, change: SkillOrchestratorConfigurationChange{Configuration: configuration, ActorID: "operator-1", RequestID: "request-1", ReasonCode: "enable"}, now: now}
}

type skillConfigurationRepository struct {
	configurations map[int64]core.SkillOrchestratorConfiguration
	audits         []SkillOrchestratorConfigurationAudit
}

func (r *skillConfigurationRepository) GetLatestSkillOrchestratorConfiguration(_ context.Context, _ core.SkillOrchestratorScope) (core.SkillOrchestratorConfiguration, error) {
	var latest core.SkillOrchestratorConfiguration
	for version, configuration := range r.configurations {
		if version > latest.Version {
			latest = configuration
		}
	}
	if latest.Version == 0 {
		return latest, ErrSkillOrchestratorConfigurationNotFound
	}
	return latest, nil
}
func (r *skillConfigurationRepository) GetSkillOrchestratorConfiguration(_ context.Context, _ core.SkillOrchestratorScope, version int64) (core.SkillOrchestratorConfiguration, error) {
	configuration, ok := r.configurations[version]
	if !ok {
		return configuration, ErrSkillOrchestratorConfigurationNotFound
	}
	return configuration, nil
}
func (r *skillConfigurationRepository) StoreSkillOrchestratorConfiguration(_ context.Context, configuration core.SkillOrchestratorConfiguration, audit SkillOrchestratorConfigurationAudit) (bool, error) {
	if existing, ok := r.configurations[configuration.Version]; ok {
		return false, nilOrConflict(existing.Digest == configuration.Digest)
	}
	r.configurations[configuration.Version] = configuration
	r.audits = append(r.audits, audit)
	return true, nil
}
func nilOrConflict(equal bool) error {
	if equal {
		return nil
	}
	return errors.New("conflict")
}

type skillConfigurationAuthorizer struct{}

func (skillConfigurationAuthorizer) AuthorizeSkillOrchestratorConfiguration(context.Context, string, core.SkillOrchestratorScope, core.SkillOrchestratorMode) error {
	return nil
}

type skillConfigurationVerifier struct {
	evidence SkillOrchestratorEnablementEvidence
}

func (v *skillConfigurationVerifier) VerifySkillOrchestratorEnablement(context.Context, core.SkillOrchestratorScope, string, string, string) (SkillOrchestratorEnablementEvidence, error) {
	return v.evidence, nil
}
