package skillworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	hostedpostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
	"github.com/taimufuraiyaa/agent-memory/internal/testkit/skillorchestrator"
)

func TestHostedHorizontalNaturalFlowMatchesStandaloneAndSurvivesTakeover(t *testing.T) {
	connectionURL := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if connectionURL == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx := context.Background()
	pool, err := hostedpostgres.Open(ctx, connectionURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := hostedpostgres.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	scopes := []core.SkillOrchestratorScope{createHostedNaturalScope(t, pool), createHostedNaturalScope(t, pool)}
	repository := hostedpostgres.NewSkillOrchestratorRepository(pool)
	startedAt := time.Now().UTC()
	fixture := newHostedNaturalFixture(repository, scopes)
	registry := application.NewSkillStageRegistry()
	for _, stage := range hostedNaturalStages() {
		if err := registry.Register(core.SkillOrchestratorContractVersion, stage, fixture); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range scopes {
		if _, err := fixture.route(scope, application.SkillSignalLesson); err != nil {
			t.Fatal(err)
		}
	}

	configA := hostedNaturalRuntimeConfig(connectionURL, scopes, "hosted-natural-a")
	runtimeA := startHostedNaturalRuntime(t, repository, registry, configA)
	blockedOwner := waitHostedNaturalBuildBlock(t, fixture)
	if !strings.HasPrefix(blockedOwner, configA.WorkerIdentity) {
		t.Fatalf("first leased build owner=%q", blockedOwner)
	}
	configB := configA
	configB.WorkerIdentity = "hosted-natural-b"
	runtimeB := startHostedNaturalRuntime(t, repository, registry, configB)
	waitHostedNaturalTenantProgress(t, fixture, scopes[1])
	drainHostedNaturalRuntime(t, runtimeA)
	time.Sleep(configA.LeaseDuration + 150*time.Millisecond)
	waitHostedNaturalJourneys(t, fixture, scopes)
	drainHostedNaturalRuntime(t, runtimeB)

	buildJob := fixture.job(scopes[0], core.SkillStageBuild)
	storedBuild, err := repository.GetSkillJob(ctx, scopes[0], buildJob.ID)
	if err != nil || storedBuild.State != core.SkillJobCompleted || storedBuild.Attempt < 2 || storedBuild.Fence < 2 || !strings.HasPrefix(fixture.owner(scopes[0], core.SkillStageBuild), configB.WorkerIdentity) {
		t.Fatalf("takeover build=%+v err=%v", storedBuild, err)
	}
	isolation, err := skillorchestrator.RunSecurityIsolationReview(ctx, repository, scopes[0], scopes[1])
	if err != nil || !isolation.Passed() {
		t.Fatalf("RLS isolation=%+v err=%v", isolation, err)
	}

	apiStatuses := make([]application.SkillOrchestrationStatus, 0, len(scopes))
	for _, scope := range scopes {
		workflow := fixture.workflow(scope, core.SkillStageRollback)
		storedWorkflow, err := repository.GetSkillWorkflow(ctx, scope, workflow.ID)
		if err != nil {
			t.Fatal(err)
		}
		jobs, _, err := repository.ListSkillJobs(ctx, scope, workflow.ID, "", 20)
		if err != nil {
			t.Fatal(err)
		}
		apiStatuses = append(apiStatuses, application.SkillOrchestrationStatus{Workflow: storedWorkflow, Jobs: jobs, Events: []core.SkillOrchestrationEvent{}})
	}
	apiDigest, dashboardDigest, err := hostedNaturalStatusParityDigests(apiStatuses)
	if err != nil || apiDigest != dashboardDigest {
		t.Fatalf("status parity api=%s dashboard=%s err=%v", apiDigest, dashboardDigest, err)
	}
	standaloneOutcome, err := application.ComputeSkillNaturalFlowOutcomeDigest(application.SkillNaturalFlowOutcome{
		CompletedStages: hostedNaturalStages(), ExactUsesPerJourney: 5, AutomaticActivation: true, LastKnownGoodRestored: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := application.BuildSkillHostedNaturalFlowReport(application.SkillHostedNaturalFlowReportInput{
		ReleaseID: "task-32-hosted-natural-flow", BuildDigest: "sha256:" + strings.Repeat("a", 64),
		MigrationDigest: "sha256:" + strings.Repeat("b", 64), StandaloneOutcomeDigest: standaloneOutcome,
		APIStatusDigest: apiDigest, DashboardStatusDigest: dashboardDigest, StartedAt: startedAt, CompletedAt: time.Now().UTC(),
		CompletedStages: fixture.completedStages(), WorkerReplicas: 2, TenantJourneys: 2,
		ControlledTakeovers: 1, ExactUses: fixture.exactUses(), AutomaticActivations: fixture.activations(),
		LastKnownGoodRestores: fixture.restores(), FairTenantClaims: fixture.fair(scopes),
		RLSIsolation: isolation.Passed(), PolicyEnablement: fixture.policyEnabled(scopes), RollbackPriority: fixture.rollbackPriority(),
	})
	if err != nil {
		t.Fatalf("hosted report state=%+v err=%v", fixture.snapshot(), err)
	}
	if err := application.VerifySkillHostedNaturalFlowReport(report); err != nil {
		t.Fatal(err)
	}
}

type hostedNaturalRuntime struct {
	runtime  *Runtime
	finished <-chan error
}

func startHostedNaturalRuntime(t *testing.T, repository *hostedpostgres.SkillOrchestratorRepository, registry *application.SkillStageRegistry, configuration RuntimeConfig) hostedNaturalRuntime {
	t.Helper()
	laneWorker, err := NewPostgresLaneWorker(repository, registry, configuration)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(configuration, hostedNaturalReadiness{}, laneWorker)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- runtime.Run(context.Background()) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !runtime.Ready() {
		time.Sleep(time.Millisecond)
	}
	if !runtime.Ready() {
		t.Fatal("hosted runtime did not become ready")
	}
	return hostedNaturalRuntime{runtime: runtime, finished: finished}
}

func drainHostedNaturalRuntime(t *testing.T, running hostedNaturalRuntime) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := running.runtime.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-running.finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("hosted runtime did not drain")
	}
}

type hostedNaturalReadiness struct{}

func (hostedNaturalReadiness) CheckSkillWorkerReadiness(context.Context, RuntimeConfig) error {
	return nil
}

func hostedNaturalRuntimeConfig(connectionURL string, scopes []core.SkillOrchestratorScope, identity string) RuntimeConfig {
	return RuntimeConfig{
		Enabled: true, DatabaseURL: connectionURL, DatabaseRole: DatabaseRole, WorkerIdentity: identity,
		TelemetryAddress: ":9090", Assignments: scopes, ClaimBatch: 2, Concurrency: 2, RollbackReserved: 1,
		TenantConcurrency: 2, WorkspaceConcurrency: 1, LeaseDuration: time.Second, StageTimeout: 800 * time.Millisecond,
		PollInterval: 10 * time.Millisecond, DrainTimeout: 2 * time.Second,
	}
}

type hostedNaturalJourney struct {
	skillID       string
	revisionID    string
	policyEnabled bool
	effects       map[core.SkillOrchestratorStage]int
	owners        map[core.SkillOrchestratorStage]string
	workflows     map[core.SkillOrchestratorStage]core.SkillWorkflow
	jobs          map[core.SkillOrchestratorStage]core.SkillJob
	exactUses     int
	activations   int
	restores      int
	rollbackDone  chan struct{}
	rollbackOnce  sync.Once
}

type hostedNaturalFixture struct {
	repository *hostedpostgres.SkillOrchestratorRepository
	scopes     []core.SkillOrchestratorScope
	mu         sync.Mutex
	journeys   map[core.SkillOrchestratorScope]*hostedNaturalJourney
	buildBlock chan string
	blockOnce  sync.Once
	priority   bool
	nonce      atomic.Int64
}

func newHostedNaturalFixture(repository *hostedpostgres.SkillOrchestratorRepository, scopes []core.SkillOrchestratorScope) *hostedNaturalFixture {
	journeys := make(map[core.SkillOrchestratorScope]*hostedNaturalJourney, len(scopes))
	for _, scope := range scopes {
		journeys[scope] = &hostedNaturalJourney{
			skillID: uuid.NewString(), revisionID: uuid.NewString(), policyEnabled: true,
			effects: make(map[core.SkillOrchestratorStage]int), owners: make(map[core.SkillOrchestratorStage]string), workflows: make(map[core.SkillOrchestratorStage]core.SkillWorkflow),
			jobs: make(map[core.SkillOrchestratorStage]core.SkillJob), rollbackDone: make(chan struct{}),
		}
	}
	return &hostedNaturalFixture{repository: repository, scopes: scopes, journeys: journeys, buildBlock: make(chan string, 1)}
}

func (f *hostedNaturalFixture) Execute(ctx context.Context, job core.SkillJob) (application.SkillStageResult, error) {
	f.mu.Lock()
	journey := f.journeys[job.Scope]
	f.mu.Unlock()
	if journey == nil {
		return application.SkillStageResult{}, errors.New("hosted natural journey scope is unknown")
	}
	if job.Scope == f.scopes[0] && job.Stage == core.SkillStageBuild {
		blocked := false
		f.blockOnce.Do(func() {
			blocked = true
			f.buildBlock <- job.LeaseOwner
		})
		if blocked {
			<-ctx.Done()
			return application.SkillStageResult{}, ctx.Err()
		}
	}
	if job.Stage == core.SkillStageDecide && !journey.policyEnabled {
		return application.SkillStageResult{}, errors.New("hosted policy is disabled")
	}
	first := f.recordEffect(job.Scope, job.Stage, job.LeaseOwner)
	if job.Stage == core.SkillStageRollback {
		f.mu.Lock()
		if journey.activations == 1 {
			f.priority = true
		}
		if first {
			journey.restores++
		}
		f.mu.Unlock()
		journey.rollbackOnce.Do(func() { close(journey.rollbackDone) })
		return application.SkillStageResult{ResultKind: core.SkillJobResultSucceeded}, nil
	}
	if first {
		if job.Stage == core.SkillStageActivate {
			f.mu.Lock()
			journey.activations++
			journey.exactUses += 5
			f.mu.Unlock()
			if _, err := f.route(job.Scope, application.SkillSignalSafety); err != nil {
				return application.SkillStageResult{}, err
			}
			select {
			case <-journey.rollbackDone:
			case <-ctx.Done():
				return application.SkillStageResult{}, ctx.Err()
			}
		} else if next, ok := hostedNaturalNextSignal(job.Stage); ok {
			if _, err := f.route(job.Scope, next); err != nil {
				return application.SkillStageResult{}, err
			}
		}
	}
	return application.SkillStageResult{ResultKind: core.SkillJobResultSucceeded}, nil
}

func (f *hostedNaturalFixture) recordEffect(scope core.SkillOrchestratorScope, stage core.SkillOrchestratorStage, owner string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	journey := f.journeys[scope]
	if journey.effects[stage] != 0 {
		return false
	}
	journey.effects[stage] = 1
	journey.owners[stage] = owner
	return true
}

func (f *hostedNaturalFixture) route(scope core.SkillOrchestratorScope, kind application.SkillLifecycleSignalKind) (application.SkillSignalRouteResult, error) {
	f.mu.Lock()
	journey := f.journeys[scope]
	f.mu.Unlock()
	now := time.Now().UTC().Add(time.Duration(f.nonce.Add(1)) * time.Nanosecond)
	signal := application.SkillLifecycleSignal{
		ID: uuid.NewString(), Kind: kind, Scope: scope, SkillID: journey.skillID, RevisionID: journey.revisionID,
		ReferenceID: uuid.NewString(), EvidenceDigest: "sha256:" + strings.Repeat("e", 64), Verified: true, Authorized: true,
		ConfigurationVersion: 1, PolicyVersion: 1, PolicyDigest: "sha256:" + strings.Repeat("d", 64), OccurredAt: now,
	}
	result, err := application.NewSkillSignalRouter(f.repository).Route(context.Background(), signal)
	if err != nil {
		return result, err
	}
	f.mu.Lock()
	journey.workflows[result.Job.Stage] = result.Workflow
	journey.jobs[result.Job.Stage] = result.Job
	f.mu.Unlock()
	return result, nil
}

func hostedNaturalNextSignal(stage core.SkillOrchestratorStage) (application.SkillLifecycleSignalKind, bool) {
	signals := map[core.SkillOrchestratorStage]application.SkillLifecycleSignalKind{
		core.SkillStageDetect: application.SkillSignalCandidate, core.SkillStageBuild: application.SkillSignalRevision,
		core.SkillStageEvaluate: application.SkillSignalEvaluation, core.SkillStageDecide: application.SkillSignalDecision,
		core.SkillStageStartCanary: application.SkillSignalCanary, core.SkillStageAnalyzeCanary: application.SkillSignalPromotion,
	}
	next, ok := signals[stage]
	return next, ok
}

func hostedNaturalStages() []core.SkillOrchestratorStage {
	return []core.SkillOrchestratorStage{
		core.SkillStageDetect, core.SkillStageBuild, core.SkillStageEvaluate, core.SkillStageDecide,
		core.SkillStageStartCanary, core.SkillStageAnalyzeCanary, core.SkillStageActivate, core.SkillStageRollback,
	}
}

func (f *hostedNaturalFixture) workflow(scope core.SkillOrchestratorScope, stage core.SkillOrchestratorStage) core.SkillWorkflow {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.journeys[scope].workflows[stage]
}

func (f *hostedNaturalFixture) job(scope core.SkillOrchestratorScope, stage core.SkillOrchestratorStage) core.SkillJob {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.journeys[scope].jobs[stage]
}

func (f *hostedNaturalFixture) owner(scope core.SkillOrchestratorScope, stage core.SkillOrchestratorStage) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.journeys[scope].owners[stage]
}

func (f *hostedNaturalFixture) completedStages() []core.SkillOrchestratorStage {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := make(map[core.SkillOrchestratorStage]struct{})
	for _, journey := range f.journeys {
		for stage, count := range journey.effects {
			if count == 1 {
				seen[stage] = struct{}{}
			}
		}
	}
	stages := make([]core.SkillOrchestratorStage, 0, len(seen))
	for stage := range seen {
		stages = append(stages, stage)
	}
	sort.Slice(stages, func(i, j int) bool { return stages[i] < stages[j] })
	return stages
}

func (f *hostedNaturalFixture) exactUses() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, journey := range f.journeys {
		total += journey.exactUses
	}
	return total
}

func (f *hostedNaturalFixture) activations() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, journey := range f.journeys {
		total += journey.activations
	}
	return total
}

func (f *hostedNaturalFixture) restores() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, journey := range f.journeys {
		total += journey.restores
	}
	return total
}

func (f *hostedNaturalFixture) fair(scopes []core.SkillOrchestratorScope) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, scope := range scopes {
		if len(f.journeys[scope].effects) != len(hostedNaturalStages()) {
			return false
		}
	}
	return true
}

func (f *hostedNaturalFixture) policyEnabled(scopes []core.SkillOrchestratorScope) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, scope := range scopes {
		if !f.journeys[scope].policyEnabled || f.journeys[scope].effects[core.SkillStageDecide] != 1 {
			return false
		}
	}
	return true
}

func (f *hostedNaturalFixture) rollbackPriority() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.priority
}

func (f *hostedNaturalFixture) snapshot() map[string]any {
	return map[string]any{"stages": f.completedStages(), "exact_uses": f.exactUses(), "activations": f.activations(), "restores": f.restores(), "priority": f.rollbackPriority()}
}

func waitHostedNaturalBuildBlock(t *testing.T, fixture *hostedNaturalFixture) string {
	t.Helper()
	select {
	case owner := <-fixture.buildBlock:
		return owner
	case <-time.After(5 * time.Second):
		t.Fatal("hosted build did not enter controlled takeover point")
		return ""
	}
}

func waitHostedNaturalTenantProgress(t *testing.T, fixture *hostedNaturalFixture, scope core.SkillOrchestratorScope) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		fixture.mu.Lock()
		progress := fixture.journeys[scope].effects[core.SkillStageBuild]
		fixture.mu.Unlock()
		if progress == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("peer tenant did not progress while first tenant worker was blocked")
}

func waitHostedNaturalJourneys(t *testing.T, fixture *hostedNaturalFixture, scopes []core.SkillOrchestratorScope) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if fixture.fair(scopes) && fixture.activations() == len(scopes) && fixture.restores() == len(scopes) {
			time.Sleep(50 * time.Millisecond)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("hosted journeys did not complete: %+v", fixture.snapshot())
}

func createHostedNaturalScope(t *testing.T, pool *pgxpool.Pool) core.SkillOrchestratorScope {
	t.Helper()
	account, tenant, workspace := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	if _, err := pool.Exec(context.Background(), `INSERT INTO saas_accounts(id,external_subject,verified_email,state,created_at,updated_at) VALUES($1,$2,$3,'active',$4,$4)`, account, account.String(), account.String()+"@example.test", now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO saas_tenants(id,kind,state,personal_owner_account_id,created_at,updated_at) VALUES($1,'personal','active',$2,$3,$3)`, tenant, account, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO saas_workspaces(tenant_id,id,name,state,created_at,updated_at) VALUES($1,$2,'hosted-natural','active',$3,$3)`, tenant, workspace, now); err != nil {
		t.Fatal(err)
	}
	return core.SkillOrchestratorScope{TenantID: tenant.String(), WorkspaceID: workspace.String(), Environment: "production"}
}

type hostedDashboardStatus struct {
	Workflow core.SkillWorkflow             `json:"workflow"`
	Jobs     []core.SkillJob                `json:"jobs"`
	Events   []core.SkillOrchestrationEvent `json:"events"`
}

func hostedNaturalStatusParityDigests(statuses []application.SkillOrchestrationStatus) (string, string, error) {
	apiPayload, err := json.Marshal(statuses)
	if err != nil {
		return "", "", err
	}
	var dashboard []hostedDashboardStatus
	if err := json.Unmarshal(apiPayload, &dashboard); err != nil {
		return "", "", err
	}
	dashboardPayload, err := json.Marshal(dashboard)
	if err != nil {
		return "", "", err
	}
	apiHash, dashboardHash := sha256.Sum256(apiPayload), sha256.Sum256(dashboardPayload)
	return "sha256:" + hex.EncodeToString(apiHash[:]), "sha256:" + hex.EncodeToString(dashboardHash[:]), nil
}
