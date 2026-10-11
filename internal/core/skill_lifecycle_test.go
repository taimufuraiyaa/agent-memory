package core

import (
	"strings"
	"testing"
	"time"
)

func TestLogicalSkillValidateAcceptsWorkspaceScopedIdentity(t *testing.T) {
	now := time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC)
	skill := LogicalSkill{
		ID: "skill-1", Workspace: "agent-memory", Name: "postgres-restore",
		Description: "Restore and verify a PostgreSQL database.", RiskTier: SkillRiskHigh,
		OwnerGroup: "platform", Status: SkillStatusActive, Generation: 1,
		TriggerConditions: []string{"A PostgreSQL restore or restore drill is requested."},
		Capabilities:      []string{"database.restore", "database.verify"},
		CreatedAt:         now, UpdatedAt: now,
	}

	if err := skill.Validate(); err != nil {
		t.Fatalf("expected valid logical skill, got %v", err)
	}
}

func TestLogicalSkillValidateRejectsInvalidNameAndRisk(t *testing.T) {
	now := time.Now().UTC()
	skill := LogicalSkill{ID: "skill-1", Workspace: "ws", Name: "Bad Name", Description: "desc", RiskTier: SkillRiskTier("unknown"), OwnerGroup: "owner", Status: SkillStatusActive, Generation: 1, CreatedAt: now, UpdatedAt: now}

	if err := skill.Validate(); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("expected invalid name error, got %v", err)
	}
	skill.Name = "safe-name"
	if err := skill.Validate(); err == nil || !strings.Contains(err.Error(), "risk_tier") {
		t.Fatalf("expected invalid risk error, got %v", err)
	}
}

func TestSkillRevisionValidateAcceptsImmutableBundle(t *testing.T) {
	now := time.Date(2026, 8, 29, 13, 5, 0, 0, time.UTC)
	revision := validSkillRevision(now)

	if err := revision.Validate(); err != nil {
		t.Fatalf("expected valid revision, got %v", err)
	}
}

func TestSkillRevisionValidateRejectsUnsafeManifestAndMissingParent(t *testing.T) {
	now := time.Now().UTC()
	revision := validSkillRevision(now)
	revision.Number = 2
	if err := revision.Validate(); err == nil || !strings.Contains(err.Error(), "parent") {
		t.Fatalf("expected missing parent error, got %v", err)
	}

	revision.ParentRevisionIDs = []string{"revision-1"}
	revision.Files[0].Path = "../SKILL.md"
	if err := revision.Validate(); err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("expected unsafe path error, got %v", err)
	}
}

func TestSkillRevisionTransitionsAreFailClosed(t *testing.T) {
	allowed := [][2]SkillRevisionState{
		{SkillRevisionDraft, SkillRevisionTesting},
		{SkillRevisionTesting, SkillRevisionCanary},
		{SkillRevisionCanary, SkillRevisionActive},
		{SkillRevisionActive, SkillRevisionPrevious},
		{SkillRevisionPrevious, SkillRevisionActive},
		{SkillRevisionDraft, SkillRevisionRejected},
		{SkillRevisionTesting, SkillRevisionDisabled},
		{SkillRevisionCanary, SkillRevisionDisabled},
		{SkillRevisionActive, SkillRevisionDisabled},
	}
	for _, transition := range allowed {
		if !CanTransitionSkillRevision(transition[0], transition[1]) {
			t.Errorf("expected %s -> %s to be allowed", transition[0], transition[1])
		}
	}
	for _, transition := range [][2]SkillRevisionState{{SkillRevisionDraft, SkillRevisionActive}, {SkillRevisionRejected, SkillRevisionActive}, {SkillRevisionDisabled, SkillRevisionCanary}} {
		if CanTransitionSkillRevision(transition[0], transition[1]) {
			t.Errorf("expected %s -> %s to be rejected", transition[0], transition[1])
		}
	}
}

func TestSkillCandidateValidateRequiresTargetForRevision(t *testing.T) {
	now := time.Now().UTC()
	candidate := SkillCandidate{
		ID: "candidate-1", Workspace: "ws", Kind: SkillCandidateRevise,
		Summary: "Add verified rollback checks.", ExpectedBenefit: "Reduce failed restore drills.",
		RiskTier: SkillRiskMedium, Confidence: .8, State: SkillCandidateProposed,
		SourceEpisodeIDs: []string{"episode-1"}, DeduplicationHash: sha256Digest('b'),
		CreatedBy: "agent", CreatedAt: now, UpdatedAt: now,
	}
	if err := candidate.Validate(); err == nil || !strings.Contains(err.Error(), "target_skill_id") {
		t.Fatalf("expected target skill error, got %v", err)
	}
	candidate.TargetSkillIDs = []string{"skill-1"}
	if err := candidate.Validate(); err != nil {
		t.Fatalf("expected valid revision candidate, got %v", err)
	}
}

func TestSkillEvaluationContractsBindExactRevisionAndSuite(t *testing.T) {
	now := time.Now().UTC()
	suite := SkillEvaluationSuite{
		ID: "suite-1", SkillID: "skill-1", Workspace: "ws", Version: 1,
		Digest: sha256Digest('c'), CreatedBy: "reviewer", CreatedAt: now,
		Cases: []SkillEvaluationCase{{ID: "case-1", Kind: SkillCasePositive, Summary: "Restore succeeds and verification passes.", Reference: "fixture:restore-success", Required: true}},
	}
	if err := suite.Validate(); err != nil {
		t.Fatalf("expected valid suite, got %v", err)
	}
	run := SkillEvaluationRun{
		ID: "run-1", Workspace: "ws", SkillID: "skill-1", RevisionID: "revision-2",
		RevisionDigest: sha256Digest('d'), BaselineRevisionID: "revision-1", BaselineDigest: sha256Digest('a'),
		SuiteID: suite.ID, SuiteVersion: suite.Version, SuiteDigest: suite.Digest,
		Evaluator: "deterministic-runner", EvaluatorVersion: "v1", EnvironmentFingerprint: sha256Digest('e'),
		Verdict: SkillEvaluationPass, StartedAt: now, CompletedAt: now.Add(time.Minute),
		CaseResults: []SkillEvaluationCaseResult{{CaseID: "case-1", Passed: true, IndependentlyVerified: true}},
	}
	if err := run.Validate(); err != nil {
		t.Fatalf("expected valid evaluation run, got %v", err)
	}
	run.CompletedAt = now.Add(-time.Second)
	if err := run.Validate(); err == nil || !strings.Contains(err.Error(), "completed_at") {
		t.Fatalf("expected invalid evaluation ordering, got %v", err)
	}
}

func TestSkillPromotionPolicyAndDecisionEnforceApprovalByRisk(t *testing.T) {
	now := time.Now().UTC()
	policy := SkillPromotionPolicy{
		ID: "policy-1", Workspace: "ws", Version: 1, RiskTier: SkillRiskLow,
		MinimumCanarySamples: 10, MinimumVerifiedSuccessRate: .95, MaximumFailureRate: .02,
		AllowAutomaticActivation: true, CreatedBy: "operator", CreatedAt: now,
	}
	if err := policy.Validate(); err != nil {
		t.Fatalf("expected valid low-risk policy, got %v", err)
	}
	policy.RiskTier = SkillRiskHigh
	if err := policy.Validate(); err == nil || !strings.Contains(err.Error(), "automatic") {
		t.Fatalf("expected high-risk auto activation rejection, got %v", err)
	}

	decision := SkillPolicyDecision{
		ID: "decision-1", Workspace: "ws", SkillID: "skill-1", RevisionID: "revision-2",
		PolicyID: "policy-1", PolicyVersion: 1, EvaluationRunIDs: []string{"run-1"},
		RiskTier: SkillRiskMedium, Decision: SkillDecisionApprovalRequired,
		ReasonCodes: []string{"medium_risk_requires_approval"}, DecidedAt: now,
	}
	if err := decision.Validate(); err != nil {
		t.Fatalf("expected valid approval-required decision, got %v", err)
	}
}

func TestSkillActivationResolutionAndExecutionBindExactDigest(t *testing.T) {
	now := time.Now().UTC()
	activation := SkillActivation{
		ID: "activation-1", Workspace: "ws", Environment: "production", SkillID: "skill-1",
		ActiveRevisionID: "revision-2", ActiveDigest: sha256Digest('b'),
		LastKnownGoodRevisionID: "revision-1", LastKnownGoodDigest: sha256Digest('a'),
		Generation: 2, PolicyDecisionID: "decision-1", Materialization: SkillMaterializationReady,
		ActivatedBy: "promotion-controller", ActivatedAt: now, UpdatedAt: now,
	}
	if err := activation.Validate(); err != nil {
		t.Fatalf("expected valid activation, got %v", err)
	}
	resolution := SkillResolution{
		ID: "resolution-1", Workspace: "ws", Environment: "production", PrincipalID: "agent-1",
		TaskID: "episode-1", SkillID: "skill-1", RevisionID: "revision-2", RevisionNumber: 2,
		Digest: activation.ActiveDigest, Reason: SkillResolutionActive, PolicyVersion: 1,
		FallbackRevisionID: "revision-1", FallbackDigest: activation.LastKnownGoodDigest,
		AcknowledgementTokenHash: sha256Digest('f'), ExpiresAt: now.Add(time.Minute), ResolvedAt: now,
	}
	if err := resolution.Validate(); err != nil {
		t.Fatalf("expected valid resolution, got %v", err)
	}
	execution := SkillExecution{
		ID: "execution-1", Workspace: "ws", Environment: "production", EpisodeID: "episode-1",
		SkillID: "skill-1", RevisionID: "revision-2", RevisionDigest: activation.ActiveDigest,
		ResolutionID: resolution.ID, Acknowledged: true, AcknowledgedAt: now.Add(time.Second),
		Outcome: SkillExecutionSuccess, IndependentlyVerified: true,
		StartedAt: now.Add(time.Second), CompletedAt: now.Add(time.Minute),
		DurationMS: 59000, InputTokens: 100, OutputTokens: 50, ToolCalls: 2,
	}
	if err := execution.Validate(); err != nil {
		t.Fatalf("expected valid execution, got %v", err)
	}
	execution.Acknowledged = false
	if err := execution.Validate(); err == nil || !strings.Contains(err.Error(), "acknowledged") {
		t.Fatalf("expected completed unacknowledged execution rejection, got %v", err)
	}
}

func validSkillRevision(now time.Time) SkillRevision {
	return SkillRevision{
		ID: "revision-1", Workspace: "agent-memory", SkillID: "skill-1", Number: 1,
		State: SkillRevisionDraft, BundleDigest: sha256Digest('a'), ManifestVersion: 1,
		Files:         []SkillBundleFile{{Path: "SKILL.md", Digest: sha256Digest('b'), SizeBytes: 128}},
		Compatibility: SkillCompatibility{Platforms: []string{"darwin", "linux"}, RequiredCapabilities: []string{"filesystem.read"}},
		RiskTier:      SkillRiskLow, CreatedBy: "agent", CreatedAt: now,
	}
}

func sha256Digest(char byte) string {
	return "sha256:" + strings.Repeat(string(char), 64)
}
