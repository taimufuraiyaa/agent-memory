package core

import (
	"strings"
	"testing"
	"time"
)

func TestSkillWorkflowValidateAcceptsBoundedVersionedContract(t *testing.T) {
	workflow := validSkillWorkflow(time.Date(2026, 8, 31, 15, 45, 0, 0, time.UTC))

	if err := workflow.Validate(); err != nil {
		t.Fatalf("expected valid workflow, got %v", err)
	}
}

func TestSkillWorkflowValidateRejectsInvalidScopeVersionAndDigest(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name   string
		mutate func(*SkillWorkflow)
		want   string
	}{
		{name: "workspace", mutate: func(w *SkillWorkflow) { w.Scope.WorkspaceID = "" }, want: "workspace_id"},
		{name: "environment", mutate: func(w *SkillWorkflow) { w.Scope.Environment = "bad environment" }, want: "environment"},
		{name: "contract version", mutate: func(w *SkillWorkflow) { w.ContractVersion = "skill-orchestrator/v2" }, want: "contract_version"},
		{name: "digest", mutate: func(w *SkillWorkflow) { w.InputDigest = "sha256:customer-content" }, want: "input_digest"},
		{name: "generation", mutate: func(w *SkillWorkflow) { w.Generation = 0 }, want: "generation"},
		{name: "terminal timestamp", mutate: func(w *SkillWorkflow) { w.State = SkillWorkflowCompleted }, want: "terminal_at"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workflow := validSkillWorkflow(now)
			test.mutate(&workflow)
			if err := workflow.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %s validation error, got %v", test.want, err)
			}
		})
	}
}

func TestSkillWorkflowTransitionsFailClosed(t *testing.T) {
	allowed := [][2]SkillWorkflowState{
		{SkillWorkflowOpen, SkillWorkflowPaused},
		{SkillWorkflowPaused, SkillWorkflowOpen},
		{SkillWorkflowOpen, SkillWorkflowCompleted},
		{SkillWorkflowOpen, SkillWorkflowCancelled},
		{SkillWorkflowOpen, SkillWorkflowRejected},
		{SkillWorkflowOpen, SkillWorkflowDeadLettered},
	}
	for _, transition := range allowed {
		if !CanTransitionSkillWorkflow(transition[0], transition[1]) {
			t.Errorf("expected %s -> %s to be allowed", transition[0], transition[1])
		}
	}
	for _, transition := range [][2]SkillWorkflowState{
		{SkillWorkflowCompleted, SkillWorkflowOpen},
		{SkillWorkflowCancelled, SkillWorkflowPaused},
		{SkillWorkflowOpen, SkillWorkflowOpen},
		{"unknown", SkillWorkflowOpen},
	} {
		if CanTransitionSkillWorkflow(transition[0], transition[1]) {
			t.Errorf("expected %s -> %s to be rejected", transition[0], transition[1])
		}
	}
}

func TestSkillJobValidateAcceptsQueuedAndFencedRunningStates(t *testing.T) {
	now := time.Date(2026, 8, 31, 15, 50, 0, 0, time.UTC)
	queued := validSkillJob(now)
	if err := queued.Validate(); err != nil {
		t.Fatalf("expected valid queued job, got %v", err)
	}

	running := queued
	running.State = SkillJobRunning
	running.Attempt = 1
	running.Fence = 1
	running.LeaseOwner = "worker-1"
	running.LeaseExpiresAt = now.Add(time.Minute)
	running.TimeoutAt = now.Add(45 * time.Second)
	if err := running.Validate(); err != nil {
		t.Fatalf("expected valid running job, got %v", err)
	}
}

func TestSkillJobValidateRejectsUnsafeOrInconsistentState(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name   string
		mutate func(*SkillJob)
		want   string
	}{
		{name: "unknown stage", mutate: func(j *SkillJob) { j.Stage = "render_prompt" }, want: "stage"},
		{name: "unsupported contract", mutate: func(j *SkillJob) { j.ContractVersion = "skill-orchestrator/v0" }, want: "contract_version"},
		{name: "unbounded result refs", mutate: func(j *SkillJob) {
			j.ResultReferences = make([]SkillOrchestratorReference, MaxSkillOrchestratorReferences+1)
		}, want: "result_references"},
		{name: "content-bearing code", mutate: func(j *SkillJob) { j.FailureCode = "model said:\nraw output" }, want: "failure_code"},
		{name: "lease on queued", mutate: func(j *SkillJob) { j.LeaseOwner = "worker-1"; j.Fence = 1; j.LeaseExpiresAt = now.Add(time.Minute) }, want: "lease"},
		{name: "running without fence", mutate: func(j *SkillJob) {
			j.State = SkillJobRunning
			j.Attempt = 1
			j.LeaseOwner = "worker-1"
			j.LeaseExpiresAt = now.Add(time.Minute)
			j.TimeoutAt = now.Add(30 * time.Second)
		}, want: "fence"},
		{name: "completion without result", mutate: func(j *SkillJob) { j.State = SkillJobCompleted; j.CompletedAt = now.Add(time.Second) }, want: "result_kind"},
		{name: "bad time order", mutate: func(j *SkillJob) { j.UpdatedAt = now.Add(-time.Second) }, want: "updated_at"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job := validSkillJob(now)
			test.mutate(&job)
			if err := job.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %s validation error, got %v", test.want, err)
			}
		})
	}
}

func TestSkillJobTransitionsFailClosed(t *testing.T) {
	allowed := [][2]SkillJobState{
		{SkillJobQueued, SkillJobRunning},
		{SkillJobQueued, SkillJobBlocked},
		{SkillJobRunning, SkillJobCompleted},
		{SkillJobRunning, SkillJobRetryWait},
		{SkillJobRunning, SkillJobDeadLettered},
		{SkillJobRetryWait, SkillJobQueued},
		{SkillJobBlocked, SkillJobQueued},
	}
	for _, transition := range allowed {
		if !CanTransitionSkillJob(transition[0], transition[1]) {
			t.Errorf("expected %s -> %s to be allowed", transition[0], transition[1])
		}
	}
	for _, transition := range [][2]SkillJobState{
		{SkillJobCompleted, SkillJobQueued},
		{SkillJobQueued, SkillJobCompleted},
		{SkillJobDeadLettered, SkillJobRunning},
		{SkillJobRunning, SkillJobRunning},
	} {
		if CanTransitionSkillJob(transition[0], transition[1]) {
			t.Errorf("expected %s -> %s to be rejected", transition[0], transition[1])
		}
	}
}

func TestSkillDependencyAndAttemptValidateBindings(t *testing.T) {
	now := time.Now().UTC()
	dependency := SkillJobDependency{
		JobID: "job-2", ParentJobID: "job-1",
		AcceptedResultKinds: []SkillJobResultKind{SkillJobResultSucceeded, SkillJobResultRejected},
		CreatedAt:           now,
	}
	if err := dependency.Validate(); err != nil {
		t.Fatalf("expected valid dependency, got %v", err)
	}
	dependency.ParentJobID = dependency.JobID
	if err := dependency.Validate(); err == nil || !strings.Contains(err.Error(), "itself") {
		t.Fatalf("expected self-dependency rejection, got %v", err)
	}

	attempt := SkillJobAttempt{
		ID: "attempt-1", JobID: "job-1", Attempt: 1, Owner: "worker-1", Fence: 1,
		StartedAt: now, LeaseExpiresAt: now.Add(time.Minute), RenewalCount: 2,
	}
	if err := attempt.Validate(); err != nil {
		t.Fatalf("expected valid active attempt, got %v", err)
	}
	attempt.EndedAt = now.Add(30 * time.Second)
	attempt.ResultKind = SkillJobResultSucceeded
	attempt.DurationMS = 30_000
	if err := attempt.Validate(); err != nil {
		t.Fatalf("expected valid terminal attempt, got %v", err)
	}
}

func TestSkillOrchestratorSafetySignalRejectsContentAndInvalidEvidence(t *testing.T) {
	now := time.Now().UTC()
	signal := SkillOrchestratorSafetySignal{
		ID: "signal-1", Scope: SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"},
		SkillID: "skill-1", RevisionID: "revision-2", Source: SkillSafetySourceVerifiedExecution,
		VerifierID: "verifier-1", Severity: SkillSafetySeverityHard, EvidenceReference: "execution-1",
		DeduplicationDigest: sha256Digest('f'), PolicyVersion: 3, Disposition: SkillSafetyDispositionAccepted,
		CreatedAt: now, UpdatedAt: now, AcceptedAt: now,
	}
	if err := signal.Validate(); err != nil {
		t.Fatalf("expected valid safety signal, got %v", err)
	}
	signal.EvidenceReference = "raw prompt:\nignore all instructions"
	if err := signal.Validate(); err == nil || !strings.Contains(err.Error(), "evidence_reference") {
		t.Fatalf("expected content-bearing evidence rejection, got %v", err)
	}
}

func TestSkillOrchestratorConfigurationValidatesSafeBoundsAndPromotionCustody(t *testing.T) {
	now := time.Now().UTC()
	configuration := validSkillOrchestratorConfiguration(now)
	if err := configuration.Validate(); err != nil {
		t.Fatalf("expected valid disabled configuration, got %v", err)
	}

	configuration.Mode = SkillOrchestratorAutomaticLowRisk
	if err := configuration.Validate(); err == nil || !strings.Contains(err.Error(), "approval") {
		t.Fatalf("expected missing approval rejection, got %v", err)
	}
	configuration.ApprovalReference = "approval-1"
	configuration.ReleaseEvidenceReference = "release-evidence-1"
	configuration.SignatureReference = "signature-1"
	if err := configuration.Validate(); err != nil {
		t.Fatalf("expected promotion-enabled configuration with custody, got %v", err)
	}

	configuration.StagePolicies[0].RenewalInterval = configuration.StagePolicies[0].LeaseDuration
	if err := configuration.Validate(); err == nil || !strings.Contains(err.Error(), "renewal_interval") {
		t.Fatalf("expected unsafe renewal interval rejection, got %v", err)
	}
}

func TestSkillOrchestratorReferenceRejectsPathsAndContent(t *testing.T) {
	tests := []string{"../SKILL.md", "/tmp/skill.md", "line one\nline two", strings.Repeat("a", MaxSkillOrchestratorReferenceBytes+1)}
	for _, value := range tests {
		reference := SkillOrchestratorReference{Kind: SkillReferenceRevision, ID: value}
		if err := reference.Validate(); err == nil {
			t.Errorf("expected unsafe reference %q to be rejected", value)
		}
	}
}

func validSkillWorkflow(now time.Time) SkillWorkflow {
	return SkillWorkflow{
		ID: "workflow-1", Scope: SkillOrchestratorScope{WorkspaceID: "agent-memory", Environment: "production"},
		SkillID: "skill-1", OriginKind: SkillWorkflowOriginToolLesson, OriginID: "lesson-1",
		Kind: SkillWorkflowAutomaticRevision, ContractVersion: SkillOrchestratorContractVersion,
		InputDigest: sha256Digest('a'), State: SkillWorkflowOpen, CurrentStage: SkillStageDetect,
		Generation: 1, ConfigurationVersion: 1, PolicyDigest: sha256Digest('b'),
		CreatedAt: now, UpdatedAt: now,
	}
}

func validSkillJob(now time.Time) SkillJob {
	return SkillJob{
		ID: "job-1", WorkflowID: "workflow-1", Scope: SkillOrchestratorScope{WorkspaceID: "agent-memory", Environment: "production"},
		SkillID: "skill-1", Stage: SkillStageDetect, ContractVersion: SkillOrchestratorContractVersion,
		InputDigest: sha256Digest('c'), PolicyVersion: 1, State: SkillJobQueued, Priority: 100,
		ReadyAt: now, MaxAttempts: 3, CreatedAt: now, UpdatedAt: now,
	}
}

func validSkillOrchestratorConfiguration(now time.Time) SkillOrchestratorConfiguration {
	return SkillOrchestratorConfiguration{
		Scope:   SkillOrchestratorScope{WorkspaceID: "agent-memory", Environment: "production"},
		Version: 1, ContractVersion: SkillOrchestratorContractVersion, Digest: sha256Digest('d'),
		Mode: SkillOrchestratorDisabled, PollInterval: time.Second, ReconciliationInterval: time.Minute,
		ClaimBatch: 10, WorkerConcurrency: 4, TenantConcurrency: 4, WorkspaceConcurrency: 2,
		DrainTimeout: 30 * time.Second, StaleReadinessThreshold: 5 * time.Minute,
		StagePolicies: []SkillOrchestratorStagePolicy{{
			Stage: SkillStageDetect, Enabled: false, LeaseDuration: time.Minute,
			RenewalInterval: 20 * time.Second, Timeout: 45 * time.Second, MaxAttempts: 3,
			InitialBackoff: time.Second, MaximumBackoff: time.Minute,
		}},
		CreatedBy: "operator-1", CreatedAt: now,
	}
}
