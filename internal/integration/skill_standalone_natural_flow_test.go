package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func TestStandaloneNaturalBackgroundFlowSurvivesRestartAndRollsBack(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	allowMigrationCleanup(t, root)
	baselineContent := []byte("---\nname: release-verifier\ndescription: Verify releases safely\n---\n# Release verifier\n\nRun the release checks.\n")
	skillDir := filepath.Join(root, ".agents", "skills", "release-verifier")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), baselineContent, 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "natural-flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	startedAt := time.Now().UTC()
	if _, err := workspace.ImportExistingSkills(ctx, store, "ws", root, time.Now); err != nil {
		t.Fatal(err)
	}
	skills, err := store.ListLogicalSkills(ctx, "ws", 10)
	if err != nil || len(skills) != 1 {
		t.Fatalf("skills=%+v err=%v", skills, err)
	}
	skill := skills[0]
	revisions, err := store.ListSkillRevisions(ctx, "ws", skill.ID, 10)
	if err != nil || len(revisions) != 1 {
		t.Fatalf("baseline revisions=%+v err=%v", revisions, err)
	}
	baseline := revisions[0]

	signalConfiguration := application.SkillSignalConfiguration{
		Environment: "local", ConfigurationVersion: 1, PolicyVersion: 1,
		PolicyDigest: "sha256:" + strings.Repeat("d", 64),
	}
	scope := core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "local"}
	if err := storeNaturalFlowConfiguration(ctx, store, scope, signalConfiguration, startedAt); err != nil {
		t.Fatal(err)
	}
	suiteDigest := sha256.Sum256([]byte("standalone-natural-flow-suite-v1"))
	suite := core.SkillEvaluationSuite{
		ID: "natural-suite", SkillID: skill.ID, Workspace: "ws", Version: 1,
		Digest: "sha256:" + hex.EncodeToString(suiteDigest[:]),
		Cases: []core.SkillEvaluationCase{
			{ID: "positive", Kind: core.SkillCasePositive, Summary: "verified release", Reference: "fixture:positive", Required: true},
			{ID: "safety", Kind: core.SkillCaseSafety, Summary: "reject unsafe release", Reference: "fixture:safety", Required: true},
		},
		CreatedBy: "release-test", CreatedAt: startedAt,
	}
	if err := store.CreateSkillEvaluationSuite(ctx, suite); err != nil {
		t.Fatal(err)
	}
	policy := core.SkillPromotionPolicy{
		ID: "natural-low-risk", Workspace: "ws", Version: 1, RiskTier: core.SkillRiskLow,
		MinimumCanarySamples: 2, MinimumVerifiedSuccessRate: .9, MaximumFailureRate: .1,
		AllowAutomaticActivation: true, CreatedBy: "product-review", CreatedAt: startedAt,
	}
	if err := store.CreateSkillPromotionPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}

	bundles, err := workspace.NewRevisionBundleStore(root)
	if err != nil {
		t.Fatal(err)
	}
	materializer, err := workspace.NewSkillMaterializer(root, bundles)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := workspace.NewSkillArtifactVerifier(bundles, materializer)
	if err != nil {
		t.Fatal(err)
	}
	router := application.NewSkillSignalRouter(store)
	author := newRestartingNaturalDraftAuthor()
	recorder := newNaturalStageRecorder()
	exactUses := &naturalExactUseRecorder{}
	registry := application.NewSkillStageRegistry()
	registerNaturalFlowStages(t, registry, store, root, bundles, materializer, verifier, router, author, recorder, exactUses, baseline, suite, policy, signalConfiguration)

	lifecycleSweep, err := application.NewSkillLifecycleParitySweep(store, store, router, signalConfiguration)
	if err != nil {
		t.Fatal(err)
	}
	reconciliationRegistry := application.NewSkillReconciliationRegistry()
	if err := reconciliationRegistry.Register(core.SkillReconcileLifecycleJobParity, lifecycleSweep); err != nil {
		t.Fatal(err)
	}

	runtimeOne, finishedOne := startNaturalStandaloneRuntime(t, store, registry, reconciliationRegistry, scope, "natural-worker-1", "natural-runtime-1")
	solution := application.NewSolutionService(store, engine.NewSolutionAdmissionPolicy(),
		application.WithSolutionClock(time.Now), application.WithSolutionSkillLessonEnqueue(router, signalConfiguration))
	eventIDs := captureNaturalVerifiedWork(t, ctx, solution)
	lesson, err := deriveNaturalLesson(ctx, solution, eventIDs)
	if err != nil || lesson.Validation != core.SolutionValidationVerified {
		t.Fatalf("lesson=%+v err=%v", lesson, err)
	}
	lessonSignal, err := application.SkillLifecycleSignalForLesson(lesson, signalConfiguration)
	if err != nil {
		t.Fatal(err)
	}
	lessonRoute, err := router.Route(ctx, lessonSignal)
	if err != nil || lessonRoute.Created {
		t.Fatalf("public capture did not durably enqueue lesson: route=%+v err=%v", lessonRoute, err)
	}

	select {
	case <-author.firstEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("background build did not reach deterministic author")
	}
	shutdownNaturalRuntime(t, runtimeOne, finishedOne)
	time.Sleep(300 * time.Millisecond)

	runtimeTwo, finishedTwo := startNaturalStandaloneRuntime(t, store, registry, reconciliationRegistry, scope, "natural-worker-2", "natural-runtime-2")
	defer shutdownNaturalRuntime(t, runtimeTwo, finishedTwo)
	candidate := waitForNaturalActivation(t, ctx, store, skill.ID, baseline.ID)
	status := waitForNaturalWorkflowCompletion(t, ctx, store, scope, lessonRoute.Workflow.ID)
	if status.Workflow.CurrentStage != core.SkillStageDetect || len(status.Jobs) != 1 || status.Jobs[0].State != core.SkillJobCompleted || status.Jobs[0].ResultKind != core.SkillJobResultSucceeded {
		t.Fatalf("public status did not expose completed detection: %+v", status)
	}

	resolver := application.NewSkillResolver(store, naturalAuthorizer{}, verifier, time.Now)
	if err := recordNaturalExactUse(ctx, store, resolver, exactUses, skill.ID, candidate.ID, "", "active-exact-use", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	ingress, err := application.NewSkillSafetyIngress(store, naturalSafetyAuthenticator{}, signalConfiguration, time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	rollbackStarted := time.Now().UTC()
	admitted, err := ingress.Admit(ctx, application.SkillSafetyIngressRequest{
		ID: "natural-hard-signal", Scope: scope, SkillID: skill.ID, RevisionID: candidate.ID,
		RevisionDigest: candidate.BundleDigest, Kind: core.SkillDigestMismatch,
		SourceType: "verified_execution", VerifierID: "natural-safety-verifier", EvidenceReference: "evidence:natural-hard-signal",
		DeduplicationDigest: "sha256:" + strings.Repeat("e", 64), PolicyVersion: 1, AuthenticationProof: "signed-natural-proof",
	})
	if err != nil || !admitted.Route.Created {
		t.Fatalf("hard signal admission=%+v err=%v", admitted, err)
	}
	waitForNaturalRollback(t, ctx, store, skill.ID, baseline.ID)
	rollbackDuration := time.Since(rollbackStarted)

	completedStages := recorder.completedStages()
	report, err := application.BuildSkillStandaloneNaturalFlowReport(application.SkillStandaloneNaturalFlowReportInput{
		ReleaseID: "task-31-natural-flow", BuildDigest: "sha256:" + strings.Repeat("a", 64),
		MigrationDigest: "sha256:" + strings.Repeat("b", 64), StartedAt: startedAt, CompletedAt: time.Now().UTC(),
		CompletedStages: completedStages, ControlledRestarts: 1, ExactUses: exactUses.count(), VerifiedHardSignals: 1,
		AutomaticActivation: true, LastKnownGoodRestored: true, RollbackDurationMS: rollbackDuration.Milliseconds(),
	})
	if err != nil {
		t.Fatalf("release report stages=%v uses=%d err=%v", completedStages, exactUses.count(), err)
	}
	if err := application.VerifySkillStandaloneNaturalFlowReport(report); err != nil {
		t.Fatal(err)
	}
}

func deriveNaturalLesson(ctx context.Context, solution *application.SolutionService, eventIDs []string) (core.SolutionToolLesson, error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		lesson, err := solution.DeriveToolLesson(ctx, application.SolutionToolLessonInput{
			Workspace: "ws", PrincipalID: "agent", EventIDs: eventIDs, Fallback: "Verify the release manually",
		})
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "locked") || time.Now().After(deadline) {
			return lesson, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func storeNaturalFlowConfiguration(ctx context.Context, store *sqlite.Store, scope core.SkillOrchestratorScope, signal application.SkillSignalConfiguration, now time.Time) error {
	configuration := core.SkillOrchestratorConfiguration{
		Scope: scope, Version: signal.ConfigurationVersion, ContractVersion: core.SkillOrchestratorContractVersion,
		Digest: "sha256:" + strings.Repeat("c", 64), PolicyDigest: signal.PolicyDigest,
		Mode: core.SkillOrchestratorAutomaticLowRisk, PollInterval: 100 * time.Millisecond, ReconciliationInterval: time.Second,
		ClaimBatch: 8, WorkerConcurrency: 2, TenantConcurrency: 2, WorkspaceConcurrency: 2,
		DrainTimeout: time.Second, StaleReadinessThreshold: time.Minute, EvaluationBudgetUnits: 100,
		AlertTargets: core.SkillOrchestratorAlertTargets{
			ReadyQueueStuckAfter: time.Minute, LeaseChurnWindow: time.Minute, LeaseFailureCount: 5,
			CanaryStaleAfter: time.Hour, RollbackFailureAfter: time.Minute,
		},
		StagePolicies: []core.SkillOrchestratorStagePolicy{{
			Stage: core.SkillStageActivate, Enabled: true, LeaseDuration: time.Minute, RenewalInterval: time.Second,
			Timeout: time.Minute, MaxAttempts: 3, InitialBackoff: time.Second, MaximumBackoff: time.Minute,
		}},
		ApprovalReference: "approval-task-31", ReleaseEvidenceReference: "release-task-31",
		SignatureReference: "signature-task-31", CreatedBy: "release-test", CreatedAt: now,
	}
	_, err := store.StoreSkillOrchestratorConfiguration(ctx, configuration, core.SkillOrchestratorConfigurationAudit{
		ActorID: "release-test", RequestID: "task-31-configuration", Operation: "skill_orchestrator.configuration.create",
		ToVersion: 1, ReasonCode: "natural_flow_regression", OccurredAt: now,
	})
	return err
}

func registerNaturalFlowStages(t *testing.T, registry *application.SkillStageRegistry, store *sqlite.Store, root string, bundles application.SkillRevisionBundleStore, materializer *workspace.SkillMaterializer, verifier *workspace.SkillArtifactVerifier, router *application.SkillSignalRouter, author *naturalDraftAuthor, recorder *naturalStageRecorder, exactUses *naturalExactUseRecorder, baseline core.SkillRevision, suite core.SkillEvaluationSuite, policy core.SkillPromotionPolicy, signal application.SkillSignalConfiguration) {
	t.Helper()
	detection, err := application.NewSkillDetectionAdapter(store, application.SkillRecurrencePolicy{MinimumDistinctEpisodes: 2, MinimumConfidence: .7}, signal)
	if err != nil {
		t.Fatal(err)
	}
	build, err := application.NewSkillRevisionBuildAdapter(store, bundles, author, signal, root)
	if err != nil {
		t.Fatal(err)
	}
	build.WithDownstreamRouter(router)
	evaluation, err := application.NewSkillEvaluationAdapter(store, naturalEvaluationRunner{}, naturalBaselineResolver{baseline}, naturalEvaluationReadiness{}, naturalEvaluationBudget{}, application.SkillEvaluationStageConfiguration{
		Signal: signal, SuiteID: suite.ID, SuiteVersion: suite.Version, SuiteDigest: suite.Digest,
		Evaluator: "natural-restricted-runner", EvaluatorVersion: "v1", EnvironmentFingerprint: suite.Digest,
		Timeout: time.Second, MaximumCases: len(suite.Cases), BudgetUnits: 10,
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	evaluation.WithDownstreamRouter(router)
	decision, err := application.NewSkillPolicyDecisionAdapter(store, application.SkillPolicyStageConfiguration{Signal: signal, PolicyID: policy.ID}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	decision.WithDownstreamRouter(router)
	canaryConfiguration := application.SkillCanaryStageConfiguration{
		Signal: signal, Enabled: true, PolicyID: policy.ID, Actor: "natural-canary-controller",
		MinimumSamples: 2, MinimumWindowAge: time.Millisecond, MaximumWindowAge: time.Hour, RecheckInterval: time.Millisecond,
	}
	canaryStart, err := application.NewSkillCanaryStartAdapter(store, canaryConfiguration, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := canaryStart.WithDownstreamRouter(router); err != nil {
		t.Fatal(err)
	}
	resolver := application.NewSkillResolver(store, naturalAuthorizer{}, verifier, time.Now)
	canaryWithEvidence := &naturalCanaryEvidenceAdapter{delegate: canaryStart, store: store, resolver: resolver, exactUses: exactUses}
	policyEngine := application.NewSkillPolicyEngine(store, time.Now)
	canaryAnalysis, err := application.NewSkillCanaryAnalysisAdapter(store, policyEngine, canaryConfiguration, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := canaryAnalysis.WithDownstreamRouter(router); err != nil {
		t.Fatal(err)
	}
	activationService := application.NewSkillActivationService(store, materializer, time.Now)
	activation, err := application.NewSkillAutomaticActivationAdapter(store, activationService, application.SkillAutomaticActivationConfiguration{
		Signal: signal, Enabled: true, Actor: "natural-activation-controller", ApprovalReference: "approval-task-31",
		ReleaseEvidenceReference: "release-task-31", SignatureReference: "signature-task-31",
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	rollback, err := application.NewSkillSafetyRollbackAdapter(store, activationService, time.Hour, signal, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	adapters := map[core.SkillOrchestratorStage]application.SkillStageAdapter{
		core.SkillStageDetect: detection, core.SkillStageBuild: build, core.SkillStageEvaluate: evaluation,
		core.SkillStageDecide: decision, core.SkillStageStartCanary: canaryWithEvidence,
		core.SkillStageAnalyzeCanary: canaryAnalysis, core.SkillStageActivate: activation, core.SkillStageRollback: rollback,
	}
	for stage, adapter := range adapters {
		if err := registry.Register(core.SkillOrchestratorContractVersion, stage, recorder.wrap(stage, adapter)); err != nil {
			t.Fatal(err)
		}
	}
}

func startNaturalStandaloneRuntime(t *testing.T, store *sqlite.Store, stages *application.SkillStageRegistry, reconciliations *application.SkillReconciliationRegistry, scope core.SkillOrchestratorScope, workerOwner, runtimeOwner string) (*application.SkillStandaloneRuntime, <-chan error) {
	t.Helper()
	worker, err := application.NewSkillOrchestratorWorker(store, stages, application.SkillWorkerConfig{
		Scope: scope, Owner: workerOwner, ClaimBatch: 8, Concurrency: 2,
		LeaseDuration: 200 * time.Millisecond, RenewalInterval: 50 * time.Millisecond, StageTimeout: 150 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := application.NewSkillOrchestratorReconciler(store, reconciliations, application.SkillReconcilerConfig{
		Scope: scope, ConfigurationVersion: 1, BatchSize: 20, TimeBudget: 100 * time.Millisecond,
		DomainTimeout: 80 * time.Millisecond, Domains: []core.SkillReconciliationDomain{core.SkillReconcileLifecycleJobParity},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := application.NewSkillStandaloneRuntime(store, worker, reconciler, application.SkillStandaloneRuntimeConfig{
		Enabled: true, InstallationID: "natural-installation", DatabaseID: "natural-database", Owner: runtimeOwner,
		PollInterval: 10 * time.Millisecond, ReconciliationInterval: 10 * time.Millisecond,
		LeaderLeaseDuration: time.Second, LeaderRenewalInterval: 100 * time.Millisecond, DrainTimeout: time.Second,
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- runtime.Run(context.Background()) }()
	return runtime, finished
}

func shutdownNaturalRuntime(t *testing.T, runtime *application.SkillStandaloneRuntime, finished <-chan error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("standalone runtime did not drain")
	}
}

func captureNaturalVerifiedWork(t *testing.T, ctx context.Context, solution *application.SolutionService) []string {
	t.Helper()
	eventIDs := make([]string, 0, 2)
	for index := 0; index < 2; index++ {
		suffix := string(rune('a' + index))
		episode, _, err := solution.Start(ctx, application.SolutionStartInput{
			Workspace: "ws", SessionID: "natural-session-" + suffix, PrincipalID: "agent", ClientID: "natural-flow",
			GoalSummary: "Verify a release safely", CapturePolicy: core.SolutionCaptureStructured,
			RetentionClass: core.SolutionRetentionStandard, IdempotencyKey: "natural-episode-" + suffix, Origin: engine.SolutionOriginAgent,
		})
		if err != nil {
			t.Fatal(err)
		}
		step, _, err := solution.AppendStep(ctx, application.SolutionAppendStepInput{
			Workspace: "ws", PrincipalID: "agent", EpisodeID: episode.ID, Kind: core.SolutionStepAction,
			Status: core.SolutionStepCompleted, Summary: "Release verification completed", Source: "agent",
			Confidence: .95, Sensitivity: core.SolutionSensitivityInternal, IdempotencyKey: "natural-step-" + suffix,
		})
		if err != nil {
			t.Fatal(err)
		}
		event, err := solution.RecordToolEvent(ctx, application.SolutionToolEventInput{
			Workspace: "ws", PrincipalID: "agent", EpisodeID: episode.ID, StepID: step.ID,
			Kind: core.SolutionToolResult, ToolName: "release verifier", ToolVersion: "1.0", Operation: "verify",
			Capability: "verify releases safely", ResultClass: core.SolutionToolResultSuccess, TaskVerified: true,
			InputSummary: step.Summary, IdempotencyKey: "natural-event-" + suffix,
		})
		if err != nil {
			t.Fatal(err)
		}
		eventIDs = append(eventIDs, event.ID)
	}
	return eventIDs
}

type naturalDraftAuthor struct {
	mu           sync.Mutex
	calls        int
	firstEntered chan struct{}
	once         sync.Once
}

func newRestartingNaturalDraftAuthor() *naturalDraftAuthor {
	return &naturalDraftAuthor{firstEntered: make(chan struct{})}
}

func (a *naturalDraftAuthor) Author(ctx context.Context, _ application.SkillDraftAuthorRequest) (application.SkillDraftAuthorResult, error) {
	a.mu.Lock()
	a.calls++
	call := a.calls
	a.mu.Unlock()
	if call == 1 {
		a.once.Do(func() { close(a.firstEntered) })
		<-ctx.Done()
		return application.SkillDraftAuthorResult{}, ctx.Err()
	}
	return application.SkillDraftAuthorResult{ProposedFiles: map[string][]byte{
		"SKILL.md": []byte("---\nname: release-verifier\ndescription: Verify releases safely\n---\n# Release verifier\n\nRun cached preflight, verify digest, then execute release checks.\n"),
	}}, nil
}

type naturalEvaluationRunner struct{}

func (naturalEvaluationRunner) Run(_ context.Context, request application.RestrictedSkillEvaluationRequest) ([]core.SkillEvaluationCaseResult, error) {
	results := make([]core.SkillEvaluationCaseResult, 0, len(request.Suite.Cases))
	for _, item := range request.Suite.Cases {
		results = append(results, core.SkillEvaluationCaseResult{CaseID: item.ID, Passed: true, IndependentlyVerified: true, DurationMS: 5})
	}
	return results, nil
}

type naturalBaselineResolver struct{ baseline core.SkillRevision }

func (r naturalBaselineResolver) ResolveSkillEvaluationBaseline(context.Context, core.SkillRevision) (core.SkillRevision, error) {
	return r.baseline, nil
}

type naturalEvaluationReadiness struct{}

func (naturalEvaluationReadiness) CheckSkillEvaluationExecutor(context.Context, string, string, string) error {
	return nil
}

type naturalEvaluationBudget struct{}
type naturalEvaluationReservation struct{}

func (naturalEvaluationBudget) Reserve(context.Context, application.SkillEvaluationBudgetRequest) (application.SkillEvaluationBudgetReservation, error) {
	return naturalEvaluationReservation{}, nil
}
func (naturalEvaluationReservation) Commit(context.Context, int64) error { return nil }
func (naturalEvaluationReservation) Release(context.Context) error       { return nil }

type naturalAuthorizer struct{}

func (naturalAuthorizer) AuthorizeSkillResolution(context.Context, string, string, string, string) error {
	return nil
}
func (naturalAuthorizer) AuthorizeSkillPin(context.Context, string, string, string, string) error {
	return nil
}

type naturalSafetyAuthenticator struct{}

func (naturalSafetyAuthenticator) AuthenticateSkillSafetySignal(_ context.Context, request application.SkillSafetyIngressRequest) error {
	if request.AuthenticationProof != "signed-natural-proof" {
		return errors.New("invalid safety proof")
	}
	return nil
}

type naturalExactUseRecorder struct {
	mu    sync.Mutex
	value int
}

func (r *naturalExactUseRecorder) add() {
	r.mu.Lock()
	r.value++
	r.mu.Unlock()
}
func (r *naturalExactUseRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.value
}

type naturalCanaryEvidenceAdapter struct {
	delegate  application.SkillStageAdapter
	store     *sqlite.Store
	resolver  *application.SkillResolver
	exactUses *naturalExactUseRecorder
}

func (a *naturalCanaryEvidenceAdapter) Execute(ctx context.Context, job core.SkillJob) (application.SkillStageResult, error) {
	result, err := a.delegate.Execute(ctx, job)
	if err != nil {
		return result, err
	}
	activation, err := a.store.GetSkillActivation(ctx, job.Scope.WorkspaceID, job.Scope.Environment, job.SkillID)
	if err != nil {
		return application.SkillStageResult{}, err
	}
	started := time.Now().UTC()
	for index := 0; index < 2; index++ {
		if err := recordNaturalExactUse(ctx, a.store, a.resolver, a.exactUses, job.SkillID, activation.CanaryRevisionID, "", "canary-candidate-"+string(rune('a'+index)), started.Add(time.Duration(index)*time.Millisecond)); err != nil {
			return application.SkillStageResult{}, err
		}
		if err := recordNaturalExactUse(ctx, a.store, a.resolver, a.exactUses, job.SkillID, activation.ActiveRevisionID, activation.ActiveRevisionID, "canary-baseline-"+string(rune('a'+index)), started.Add(time.Duration(index+2)*time.Millisecond)); err != nil {
			return application.SkillStageResult{}, err
		}
	}
	return result, nil
}

func recordNaturalExactUse(ctx context.Context, store *sqlite.Store, resolver *application.SkillResolver, exactUses *naturalExactUseRecorder, skillID, revisionID, explicit, taskID string, started time.Time) error {
	resolved, err := resolver.Resolve(ctx, application.SkillResolutionRequest{
		Workspace: "ws", Environment: "local", PrincipalID: "agent", TaskID: taskID, SkillID: skillID,
		ExplicitRevisionID: explicit, Platform: "darwin", Architecture: "arm64", RuntimeVersion: "1.0",
		PolicyVersion: 1, CanaryBasisPoints: 10000, CanaryApproved: true, AcknowledgementSupported: true,
	})
	if err != nil {
		return err
	}
	if resolved.Resolution.RevisionID != revisionID {
		return errors.New("natural exact use resolved an unexpected revision")
	}
	if _, err := application.NewSkillAcknowledgementService(store, func() time.Time { return started }).Acknowledge(ctx, application.SkillAcknowledgementInput{
		Workspace: "ws", ResolutionID: resolved.Resolution.ID, PrincipalID: "agent", TaskID: taskID,
		RevisionID: revisionID, Digest: resolved.Resolution.Digest, Token: resolved.AcknowledgementToken,
	}); err != nil {
		return err
	}
	if _, err := application.NewSkillExecutionService(store).Complete(ctx, application.SkillExecutionInput{
		ID: "natural-execution-" + taskID, Workspace: "ws", ResolutionID: resolved.Resolution.ID, EpisodeID: taskID,
		Outcome: core.SkillExecutionSuccess, IndependentlyVerified: true, StartedAt: started,
		CompletedAt: started.Add(5 * time.Millisecond), InputTokens: 10, OutputTokens: 5, ToolCalls: 1,
	}); err != nil {
		return err
	}
	exactUses.add()
	return nil
}

type naturalStageRecorder struct {
	mu     sync.Mutex
	stages map[core.SkillOrchestratorStage]struct{}
}

func newNaturalStageRecorder() *naturalStageRecorder {
	return &naturalStageRecorder{stages: make(map[core.SkillOrchestratorStage]struct{})}
}

func (r *naturalStageRecorder) wrap(stage core.SkillOrchestratorStage, delegate application.SkillStageAdapter) application.SkillStageAdapter {
	return application.SkillStageAdapterFunc(func(ctx context.Context, job core.SkillJob) (application.SkillStageResult, error) {
		result, err := delegate.Execute(ctx, job)
		if err == nil && result.ResultKind == core.SkillJobResultSucceeded {
			r.mu.Lock()
			r.stages[stage] = struct{}{}
			r.mu.Unlock()
		}
		return result, err
	})
}

func (r *naturalStageRecorder) completedStages() []core.SkillOrchestratorStage {
	r.mu.Lock()
	defer r.mu.Unlock()
	stages := make([]core.SkillOrchestratorStage, 0, len(r.stages))
	for stage := range r.stages {
		stages = append(stages, stage)
	}
	sort.Slice(stages, func(i, j int) bool { return stages[i] < stages[j] })
	return stages
}

func waitForNaturalActivation(t *testing.T, ctx context.Context, store *sqlite.Store, skillID, baselineID string) core.SkillRevision {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		activation, err := store.GetSkillActivation(ctx, "ws", "local", skillID)
		if err == nil && activation.ActiveRevisionID != "" && activation.ActiveRevisionID != baselineID && activation.CanaryRevisionID == "" {
			revision, revisionErr := store.GetSkillRevision(ctx, "ws", activation.ActiveRevisionID)
			if revisionErr == nil {
				return revision
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	activation, activationErr := store.GetSkillActivation(ctx, "ws", "local", skillID)
	revisions, revisionsErr := store.ListSkillRevisions(ctx, "ws", skillID, 20)
	decisions, decisionsErr := store.ListSkillPolicyDecisions(ctx, "ws", skillID, 20)
	workflow, workflowErr := store.GetLatestSkillWorkflow(ctx, core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "local"}, skillID)
	var status application.SkillOrchestrationStatus
	var statusErr error
	if workflowErr == nil {
		status, statusErr = application.NewSkillOrchestrationControlService(store, time.Now).Status(ctx, workflow.Scope, workflow.ID, "", "", 20)
	}
	t.Fatalf("automatic low-risk activation did not complete: activation=%+v activation_err=%v revisions=%+v revisions_err=%v decisions=%+v decisions_err=%v workflow=%+v workflow_err=%v status=%+v status_err=%v", activation, activationErr, revisions, revisionsErr, decisions, decisionsErr, workflow, workflowErr, status, statusErr)
	return core.SkillRevision{}
}

func waitForNaturalWorkflowCompletion(t *testing.T, ctx context.Context, store *sqlite.Store, scope core.SkillOrchestratorScope, workflowID string) application.SkillOrchestrationStatus {
	t.Helper()
	controls := application.NewSkillOrchestrationControlService(store, time.Now)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, err := controls.Status(ctx, scope, workflowID, "", "", 20)
		if err == nil && len(status.Jobs) == 1 && status.Jobs[0].State.Terminal() {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("public orchestration status did not become terminal")
	return application.SkillOrchestrationStatus{}
}

func waitForNaturalRollback(t *testing.T, ctx context.Context, store *sqlite.Store, skillID, baselineID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		activation, err := store.GetSkillActivation(ctx, "ws", "local", skillID)
		if err == nil && activation.ActiveRevisionID == baselineID && activation.CanaryRevisionID == "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("last-known-good rollback did not complete")
}
