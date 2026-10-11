package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func TestAutomaticSkillLearningNaturalClosedLoop(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	allowMigrationCleanup(t, root)
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	skillDir := filepath.Join(root, ".agents", "skills", "release-verifier")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	baselineContent := []byte("---\nname: release-verifier\ndescription: Verify releases safely\n---\n# Release verifier\n\nRun the release checks.\n")
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), baselineContent, 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := workspace.ImportExistingSkills(ctx, store, "ws", root, func() time.Time { return now }); err != nil {
		t.Fatal(err)
	}
	skills, _ := store.ListLogicalSkills(ctx, "ws", 10)
	skill := skills[0]
	baselineRevisions, _ := store.ListSkillRevisions(ctx, "ws", skill.ID, 10)
	baseline := baselineRevisions[0]

	solution := application.NewSolutionService(store, engine.NewSolutionAdmissionPolicy(), application.WithSolutionClock(func() time.Time { return now }))
	eventIDs := []string{}
	for index := 0; index < 2; index++ {
		episode, _, startErr := solution.Start(ctx, application.SolutionStartInput{Workspace: "ws", SessionID: "session-" + string(rune('a'+index)), PrincipalID: "agent", ClientID: "closed-loop", GoalSummary: "Verify a release safely", CapturePolicy: core.SolutionCaptureStructured, RetentionClass: core.SolutionRetentionStandard, IdempotencyKey: "episode-" + string(rune('a'+index)), Origin: engine.SolutionOriginAgent})
		if startErr != nil {
			t.Fatal(startErr)
		}
		step, _, stepErr := solution.AppendStep(ctx, application.SolutionAppendStepInput{Workspace: "ws", PrincipalID: "agent", EpisodeID: episode.ID, Kind: core.SolutionStepAction, Status: core.SolutionStepCompleted, Summary: "Release verification completed", Source: "agent", Confidence: .95, Sensitivity: core.SolutionSensitivityInternal, IdempotencyKey: "step-" + string(rune('a'+index))})
		if stepErr != nil {
			t.Fatal(stepErr)
		}
		event, eventErr := solution.RecordToolEvent(ctx, application.SolutionToolEventInput{Workspace: "ws", PrincipalID: "agent", EpisodeID: episode.ID, StepID: step.ID, Kind: core.SolutionToolResult, ToolName: "release verifier", ToolVersion: "1.0", Operation: "verify", Capability: "verify releases safely", ResultClass: core.SolutionToolResultSuccess, TaskVerified: true, InputSummary: step.Summary, IdempotencyKey: "event-" + string(rune('a'+index))})
		if eventErr != nil {
			t.Fatal(eventErr)
		}
		eventIDs = append(eventIDs, event.ID)
	}
	lesson, err := solution.DeriveToolLesson(ctx, application.SolutionToolLessonInput{Workspace: "ws", PrincipalID: "agent", EventIDs: eventIDs, Fallback: "Verify the release manually"})
	if err != nil || lesson.Validation != core.SolutionValidationVerified {
		t.Fatalf("captured lesson = %+v, %v", lesson, err)
	}
	detected, err := application.NewSkillRecurrenceScheduler(store, application.SkillRecurrencePolicy{MinimumDistinctEpisodes: 2, MinimumConfidence: .7}).Run(ctx, application.SkillRecurrenceInput{Workspace: "ws", PrincipalID: "agent", CreatedBy: "scheduler"})
	if err != nil || len(detected.Candidates) != 1 || detected.Candidates[0].Kind != core.SkillCandidateRevise {
		t.Fatalf("recurrence = %+v, %v", detected, err)
	}
	bundles, err := workspace.NewRevisionBundleStore(root)
	if err != nil {
		t.Fatal(err)
	}
	candidateContent := []byte("---\nname: release-verifier\ndescription: Verify releases safely\n---\n# Release verifier\n\nRun cached preflight, verify digest, then execute release checks.\n")
	built, err := application.NewSkillRevisionBuilder(store, bundles).Build(ctx, application.SkillRevisionBuildInput{Workspace: "ws", CandidateID: detected.Candidates[0].ID, CreatedBy: "agent", ProposedFiles: map[string][]byte{"SKILL.md": candidateContent}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.TransitionSkillRevisionState(ctx, "ws", built.Revision.ID, core.SkillRevisionDraft, core.SkillRevisionTesting); err != nil {
		t.Fatal(err)
	}

	suiteDigest := sha256.Sum256([]byte("closed-loop-suite-v1"))
	suite := core.SkillEvaluationSuite{ID: "suite-1", SkillID: skill.ID, Workspace: "ws", Version: 1, Digest: "sha256:" + hex.EncodeToString(suiteDigest[:]), Cases: []core.SkillEvaluationCase{{ID: "positive", Kind: core.SkillCasePositive, Summary: "verified release", Reference: "fixture:positive", Required: true}, {ID: "safety", Kind: core.SkillCaseSafety, Summary: "reject unsafe release", Reference: "fixture:safety", Required: true}}, CreatedBy: "reviewer", CreatedAt: now}
	if err := store.CreateSkillEvaluationSuite(ctx, suite); err != nil {
		t.Fatal(err)
	}
	evaluation, err := application.NewSkillEvaluationOrchestrator(store, closedLoopEvaluationRunner{}, func() time.Time { return now.Add(2 * time.Minute) }).Evaluate(ctx, application.SkillEvaluationInput{ID: "evaluation-1", Workspace: "ws", SkillID: skill.ID, CandidateRevisionID: built.Revision.ID, BaselineRevisionID: baseline.ID, SuiteID: suite.ID, SuiteVersion: 1, SuiteDigest: suite.Digest, Evaluator: "restricted-fixture", EvaluatorVersion: "v1", EnvironmentFingerprint: suite.Digest, Timeout: time.Second, MaximumCases: 2})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Candidate.Verdict != core.SkillEvaluationPass || evaluation.Baseline.Verdict != core.SkillEvaluationPass {
		t.Fatalf("evaluation = %+v", evaluation)
	}
	policy := core.SkillPromotionPolicy{ID: "policy-low", Workspace: "ws", Version: 1, RiskTier: core.SkillRiskLow, MinimumCanarySamples: 2, MinimumVerifiedSuccessRate: .9, MaximumFailureRate: .1, AllowAutomaticActivation: true, CreatedBy: "product-review", CreatedAt: now}
	if err := store.CreateSkillPromotionPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	policyEngine := application.NewSkillPolicyEngine(store, func() time.Time { return now.Add(3 * time.Minute) })
	canaryDecision, err := policyEngine.Decide(ctx, application.SkillPolicyInput{DecisionID: "decision-canary", Workspace: "ws", SkillID: skill.ID, RevisionID: built.Revision.ID, PolicyID: policy.ID, PolicyVersion: 1, CandidateRunID: evaluation.Candidate.ID, BaselineRunID: evaluation.Baseline.ID})
	if err != nil || canaryDecision.Decision != core.SkillDecisionCanary {
		t.Fatalf("canary decision = %+v, %v", canaryDecision, err)
	}
	canaryActivation, err := application.NewSkillCanaryStartService(store, func() time.Time { return now.Add(4 * time.Minute) }).Start(ctx, application.SkillCanaryStartInput{Workspace: "ws", Environment: "local", SkillID: skill.ID, CandidateRevisionID: built.Revision.ID, PolicyDecisionID: canaryDecision.ID, ExpectedGeneration: 1, Actor: "controller"})
	if err != nil || canaryActivation.CanaryRevisionID != built.Revision.ID || canaryActivation.Generation != 2 {
		t.Fatalf("canary activation = %+v, %v", canaryActivation, err)
	}

	materializer, err := workspace.NewSkillMaterializer(root, bundles)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := workspace.NewSkillArtifactVerifier(bundles, materializer)
	if err != nil {
		t.Fatal(err)
	}
	resolver := application.NewSkillResolver(store, closedLoopAuthorizer{}, verifier, func() time.Time { return now.Add(5 * time.Minute) })
	for index := 0; index < 4; index++ {
		revisionID := built.Revision.ID
		explicit := ""
		duration := 10 * time.Millisecond
		if index >= 2 {
			revisionID = baseline.ID
			explicit = baseline.ID
			duration = 20 * time.Millisecond
		}
		closedLoopExecute(t, ctx, store, resolver, skill.ID, revisionID, explicit, "canary-task-"+string(rune('a'+index)), now.Add(5*time.Minute), duration)
	}
	activationService := application.NewSkillActivationService(store, materializer, func() time.Time { return now.Add(7 * time.Minute) })
	analyzed, err := application.NewSkillCanaryAnalyzer(store, policyEngine, activationService).Analyze(ctx, application.SkillCanaryAnalysisInput{DecisionID: "decision-promote", OperationID: "promote-auto", IdempotencyKey: "promote-auto", Workspace: "ws", Environment: "local", SkillID: skill.ID, CandidateRevisionID: built.Revision.ID, BaselineRevisionID: baseline.ID, PolicyID: policy.ID, PolicyVersion: 1, CandidateRunID: evaluation.Candidate.ID, BaselineRunID: evaluation.Baseline.ID, ExpectedGeneration: 2, Actor: "controller", WindowStartedAt: now})
	if err != nil || analyzed.Decision.Decision != core.SkillDecisionPromote || analyzed.Activation == nil || analyzed.Activation.ActiveRevisionID != built.Revision.ID {
		t.Fatalf("automatic promotion = %+v, %v", analyzed, err)
	}

	activeResolver := application.NewSkillResolver(store, closedLoopAuthorizer{}, verifier, func() time.Time { return now.Add(8 * time.Minute) })
	closedLoopExecute(t, ctx, store, activeResolver, skill.ID, built.Revision.ID, "", "active-task", now.Add(8*time.Minute), 8*time.Millisecond)
	safety := application.NewSkillSafetyObserver(store, activationService, time.Hour, func() time.Time { return now.Add(9 * time.Minute) })
	recovered, err := safety.Observe(ctx, application.SkillSafetyObservation{ID: "safety-1", Workspace: "ws", Environment: "local", SkillID: skill.ID, RevisionID: built.Revision.ID, Kind: core.SkillDigestMismatch, Verified: true})
	if err != nil || recovered.Activation == nil || recovered.Activation.ActiveRevisionID != baseline.ID || recovered.Signal.State != core.SkillSafetyResolved {
		t.Fatalf("safety recovery = %+v, %v", recovered, err)
	}
}

type closedLoopEvaluationRunner struct{}

func (closedLoopEvaluationRunner) Run(_ context.Context, request application.RestrictedSkillEvaluationRequest) ([]core.SkillEvaluationCaseResult, error) {
	result := make([]core.SkillEvaluationCaseResult, 0, len(request.Suite.Cases))
	for _, item := range request.Suite.Cases {
		result = append(result, core.SkillEvaluationCaseResult{CaseID: item.ID, Passed: true, IndependentlyVerified: true, DurationMS: 5})
	}
	return result, nil
}

type closedLoopAuthorizer struct{}

func (closedLoopAuthorizer) AuthorizeSkillResolution(context.Context, string, string, string, string) error {
	return nil
}
func (closedLoopAuthorizer) AuthorizeSkillPin(context.Context, string, string, string, string) error {
	return nil
}

func closedLoopExecute(t *testing.T, ctx context.Context, store *sqlite.Store, resolver *application.SkillResolver, skillID, revisionID, explicit, taskID string, started time.Time, duration time.Duration) {
	t.Helper()
	resolved, err := resolver.Resolve(ctx, application.SkillResolutionRequest{Workspace: "ws", Environment: "local", PrincipalID: "agent", TaskID: taskID, SkillID: skillID, ExplicitRevisionID: explicit, Platform: "darwin", Architecture: "arm64", RuntimeVersion: "1.0", PolicyVersion: 1, CanaryBasisPoints: 10000, CanaryApproved: true, AcknowledgementSupported: true})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Resolution.RevisionID != revisionID {
		t.Fatalf("resolved %s, want %s", resolved.Resolution.RevisionID, revisionID)
	}
	_, err = application.NewSkillAcknowledgementService(store, func() time.Time { return started }).Acknowledge(ctx, application.SkillAcknowledgementInput{Workspace: "ws", ResolutionID: resolved.Resolution.ID, PrincipalID: "agent", TaskID: taskID, RevisionID: revisionID, Digest: resolved.Resolution.Digest, Token: resolved.AcknowledgementToken})
	if err != nil {
		t.Fatal(err)
	}
	_, err = application.NewSkillExecutionService(store).Complete(ctx, application.SkillExecutionInput{ID: "execution-" + taskID, Workspace: "ws", ResolutionID: resolved.Resolution.ID, EpisodeID: taskID, Outcome: core.SkillExecutionSuccess, IndependentlyVerified: true, StartedAt: started, CompletedAt: started.Add(duration), InputTokens: 10, OutputTokens: 5, ToolCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
}
