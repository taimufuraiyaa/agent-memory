package core

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var ErrSkillEvaluationBudgetExhausted = errors.New("skill evaluation budget exhausted")

const (
	SkillOrchestratorContractVersion     = "skill-orchestrator/v1"
	MaxSkillOrchestratorReferences       = 32
	MaxSkillOrchestratorReferenceBytes   = 256
	MaxSkillOrchestratorFailureCodeBytes = 128
	MaxSkillOrchestratorStagePolicies    = 16
	MaxSkillReconciliationCursorBytes    = 512
)

var (
	skillOrchestratorIdentifierPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,255}$`)
	skillOrchestratorCodePattern              = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	ErrSkillOrchestratorConfigurationNotFound = errors.New("skill orchestrator configuration not found")
)

type SkillOrchestratorScope struct {
	TenantID    string `json:"tenant_id,omitempty"`
	WorkspaceID string `json:"workspace_id"`
	Environment string `json:"environment"`
}

func (s SkillOrchestratorScope) Validate() error {
	if s.TenantID != "" && !validSkillOrchestratorIdentifier(s.TenantID) {
		return errors.New("skill orchestrator tenant_id is invalid")
	}
	if !validSkillOrchestratorIdentifier(s.WorkspaceID) {
		return errors.New("skill orchestrator workspace_id is invalid")
	}
	if !validSkillOrchestratorCode(s.Environment, 64) {
		return errors.New("skill orchestrator environment is invalid")
	}
	return nil
}

type SkillWorkflowKind string

const (
	SkillWorkflowAutomaticRevision       SkillWorkflowKind = "automatic_revision"
	SkillWorkflowSafetyRollback          SkillWorkflowKind = "safety_rollback"
	SkillWorkflowMaterializationRecovery SkillWorkflowKind = "materialization_recovery"
)

func (k SkillWorkflowKind) Valid() bool {
	return k == SkillWorkflowAutomaticRevision || k == SkillWorkflowSafetyRollback || k == SkillWorkflowMaterializationRecovery
}

type SkillWorkflowOriginKind string

const (
	SkillWorkflowOriginSolutionEpisode SkillWorkflowOriginKind = "solution_episode"
	SkillWorkflowOriginToolLesson      SkillWorkflowOriginKind = "tool_lesson"
	SkillWorkflowOriginSafetySignal    SkillWorkflowOriginKind = "safety_signal"
	SkillWorkflowOriginOperator        SkillWorkflowOriginKind = "operator"
	SkillWorkflowOriginReconciliation  SkillWorkflowOriginKind = "reconciliation"
	SkillWorkflowOriginLifecycleSignal SkillWorkflowOriginKind = "lifecycle_signal"
)

func (k SkillWorkflowOriginKind) Valid() bool {
	switch k {
	case SkillWorkflowOriginSolutionEpisode, SkillWorkflowOriginToolLesson, SkillWorkflowOriginSafetySignal, SkillWorkflowOriginOperator, SkillWorkflowOriginReconciliation, SkillWorkflowOriginLifecycleSignal:
		return true
	default:
		return false
	}
}

type SkillWorkflowState string

const (
	SkillWorkflowOpen         SkillWorkflowState = "open"
	SkillWorkflowPaused       SkillWorkflowState = "paused"
	SkillWorkflowCompleted    SkillWorkflowState = "completed"
	SkillWorkflowCancelled    SkillWorkflowState = "cancelled"
	SkillWorkflowRejected     SkillWorkflowState = "rejected"
	SkillWorkflowDeadLettered SkillWorkflowState = "dead_lettered"
)

func (s SkillWorkflowState) Valid() bool {
	switch s {
	case SkillWorkflowOpen, SkillWorkflowPaused, SkillWorkflowCompleted, SkillWorkflowCancelled, SkillWorkflowRejected, SkillWorkflowDeadLettered:
		return true
	default:
		return false
	}
}

func (s SkillWorkflowState) Terminal() bool {
	return s == SkillWorkflowCompleted || s == SkillWorkflowCancelled || s == SkillWorkflowRejected || s == SkillWorkflowDeadLettered
}

func CanTransitionSkillWorkflow(from, to SkillWorkflowState) bool {
	if !from.Valid() || !to.Valid() || from == to || from.Terminal() {
		return false
	}
	if from == SkillWorkflowPaused {
		return to == SkillWorkflowOpen || to == SkillWorkflowCancelled || to == SkillWorkflowDeadLettered
	}
	return to == SkillWorkflowPaused || to.Terminal()
}

type SkillOrchestratorStage string

const (
	SkillStageDetect                   SkillOrchestratorStage = "detect"
	SkillStageBuild                    SkillOrchestratorStage = "build"
	SkillStageEvaluate                 SkillOrchestratorStage = "evaluate"
	SkillStageDecide                   SkillOrchestratorStage = "decide"
	SkillStageStartCanary              SkillOrchestratorStage = "start_canary"
	SkillStageAnalyzeCanary            SkillOrchestratorStage = "analyze_canary"
	SkillStageActivate                 SkillOrchestratorStage = "activate"
	SkillStageObserveSafety            SkillOrchestratorStage = "observe_safety"
	SkillStageRollback                 SkillOrchestratorStage = "rollback"
	SkillStageReconcileMaterialization SkillOrchestratorStage = "reconcile_materialization"
)

func (s SkillOrchestratorStage) Valid() bool {
	switch s {
	case SkillStageDetect, SkillStageBuild, SkillStageEvaluate, SkillStageDecide, SkillStageStartCanary, SkillStageAnalyzeCanary, SkillStageActivate, SkillStageObserveSafety, SkillStageRollback, SkillStageReconcileMaterialization:
		return true
	default:
		return false
	}
}

type SkillWorkflow struct {
	ID                   string                  `json:"id"`
	Scope                SkillOrchestratorScope  `json:"scope"`
	SkillID              string                  `json:"skill_id,omitempty"`
	OriginKind           SkillWorkflowOriginKind `json:"origin_kind"`
	OriginID             string                  `json:"origin_id"`
	Kind                 SkillWorkflowKind       `json:"workflow_kind"`
	ContractVersion      string                  `json:"contract_version"`
	InputDigest          string                  `json:"input_digest"`
	State                SkillWorkflowState      `json:"state"`
	CurrentStage         SkillOrchestratorStage  `json:"current_stage"`
	Generation           int64                   `json:"generation"`
	ConfigurationVersion int64                   `json:"configuration_version"`
	PolicyDigest         string                  `json:"policy_digest"`
	CreatedAt            time.Time               `json:"created_at"`
	UpdatedAt            time.Time               `json:"updated_at"`
	TerminalAt           time.Time               `json:"terminal_at,omitempty"`
}

type SkillOrchestrationEvent struct {
	ID         int64     `json:"id"`
	WorkflowID string    `json:"workflow_id"`
	JobID      string    `json:"job_id,omitempty"`
	Kind       string    `json:"kind"`
	FromState  string    `json:"from_state,omitempty"`
	ToState    string    `json:"to_state,omitempty"`
	ActorID    string    `json:"actor_id"`
	Fence      int64     `json:"fence"`
	ReasonCode string    `json:"reason_code,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func (w SkillWorkflow) Validate() error {
	if !validSkillOrchestratorIdentifier(w.ID) {
		return errors.New("skill workflow id is invalid")
	}
	if err := w.Scope.Validate(); err != nil {
		return err
	}
	if w.SkillID != "" && !validSkillOrchestratorIdentifier(w.SkillID) {
		return errors.New("skill workflow skill_id is invalid")
	}
	if !w.OriginKind.Valid() || !validSkillOrchestratorIdentifier(w.OriginID) {
		return errors.New("skill workflow origin is invalid")
	}
	if !w.Kind.Valid() {
		return errors.New("skill workflow workflow_kind is invalid")
	}
	if w.ContractVersion != SkillOrchestratorContractVersion {
		return errors.New("skill workflow contract_version is unsupported")
	}
	if !validSkillDigest(w.InputDigest) {
		return errors.New("skill workflow input_digest is invalid")
	}
	if !w.State.Valid() || !w.CurrentStage.Valid() {
		return errors.New("skill workflow state or current_stage is invalid")
	}
	if w.Generation < 1 {
		return errors.New("skill workflow generation must be at least 1")
	}
	if w.ConfigurationVersion < 1 {
		return errors.New("skill workflow configuration_version must be at least 1")
	}
	if !validSkillDigest(w.PolicyDigest) {
		return errors.New("skill workflow policy_digest is invalid")
	}
	if err := validateSkillOrchestratorTimes(w.CreatedAt, w.UpdatedAt); err != nil {
		return fmt.Errorf("skill workflow: %w", err)
	}
	if w.State.Terminal() {
		if w.TerminalAt.IsZero() || w.TerminalAt.Before(w.CreatedAt) || w.TerminalAt.Before(w.UpdatedAt) {
			return errors.New("skill workflow terminal_at must follow updated_at")
		}
	} else if !w.TerminalAt.IsZero() {
		return errors.New("skill workflow terminal_at is only valid for terminal state")
	}
	return nil
}

type SkillJobState string

const (
	SkillJobQueued       SkillJobState = "queued"
	SkillJobBlocked      SkillJobState = "blocked"
	SkillJobRunning      SkillJobState = "running"
	SkillJobRetryWait    SkillJobState = "retry_wait"
	SkillJobCompleted    SkillJobState = "completed"
	SkillJobCancelled    SkillJobState = "cancelled"
	SkillJobDeadLettered SkillJobState = "dead_lettered"
)

func (s SkillJobState) Valid() bool {
	switch s {
	case SkillJobQueued, SkillJobBlocked, SkillJobRunning, SkillJobRetryWait, SkillJobCompleted, SkillJobCancelled, SkillJobDeadLettered:
		return true
	default:
		return false
	}
}

func (s SkillJobState) Terminal() bool {
	return s == SkillJobCompleted || s == SkillJobCancelled || s == SkillJobDeadLettered
}

func CanTransitionSkillJob(from, to SkillJobState) bool {
	if !from.Valid() || !to.Valid() || from == to || from.Terminal() {
		return false
	}
	allowed := map[SkillJobState]map[SkillJobState]bool{
		SkillJobQueued:    {SkillJobRunning: true, SkillJobBlocked: true, SkillJobCancelled: true},
		SkillJobRunning:   {SkillJobQueued: true, SkillJobCompleted: true, SkillJobRetryWait: true, SkillJobBlocked: true, SkillJobCancelled: true, SkillJobDeadLettered: true},
		SkillJobRetryWait: {SkillJobQueued: true, SkillJobCancelled: true},
		SkillJobBlocked:   {SkillJobQueued: true, SkillJobCancelled: true},
	}
	return allowed[from][to]
}

type SkillJobFailureClass string

const (
	SkillFailureNone                  SkillJobFailureClass = ""
	SkillFailureContention            SkillJobFailureClass = "contention"
	SkillFailureDependencyUnavailable SkillJobFailureClass = "dependency_unavailable"
	SkillFailureInsufficientEvidence  SkillJobFailureClass = "insufficient_evidence"
	SkillFailurePolicyBlock           SkillJobFailureClass = "policy_block"
	SkillFailurePermanentValidation   SkillJobFailureClass = "permanent_validation"
	SkillFailureSafetyRejection       SkillJobFailureClass = "safety_rejection"
	SkillFailureCancellation          SkillJobFailureClass = "cancellation"
	SkillFailureUnknownInternal       SkillJobFailureClass = "unknown_internal"
)

func (c SkillJobFailureClass) Valid() bool {
	switch c {
	case SkillFailureNone, SkillFailureContention, SkillFailureDependencyUnavailable, SkillFailureInsufficientEvidence, SkillFailurePolicyBlock, SkillFailurePermanentValidation, SkillFailureSafetyRejection, SkillFailureCancellation, SkillFailureUnknownInternal:
		return true
	default:
		return false
	}
}

type SkillJobResultKind string

const (
	SkillJobResultNone      SkillJobResultKind = ""
	SkillJobResultSucceeded SkillJobResultKind = "succeeded"
	SkillJobResultRejected  SkillJobResultKind = "rejected"
	SkillJobResultCancelled SkillJobResultKind = "cancelled"
)

func (k SkillJobResultKind) Valid() bool {
	return k == SkillJobResultNone || k == SkillJobResultSucceeded || k == SkillJobResultRejected || k == SkillJobResultCancelled
}

type SkillOrchestratorReferenceKind string

const (
	SkillReferenceCandidate      SkillOrchestratorReferenceKind = "candidate"
	SkillReferenceRevision       SkillOrchestratorReferenceKind = "revision"
	SkillReferenceEvaluationRun  SkillOrchestratorReferenceKind = "evaluation_run"
	SkillReferencePolicyDecision SkillOrchestratorReferenceKind = "policy_decision"
	SkillReferenceActivation     SkillOrchestratorReferenceKind = "activation"
	SkillReferenceSafetySignal   SkillOrchestratorReferenceKind = "safety_signal"
	SkillReferenceOperation      SkillOrchestratorReferenceKind = "operation"
)

func (k SkillOrchestratorReferenceKind) Valid() bool {
	switch k {
	case SkillReferenceCandidate, SkillReferenceRevision, SkillReferenceEvaluationRun, SkillReferencePolicyDecision, SkillReferenceActivation, SkillReferenceSafetySignal, SkillReferenceOperation:
		return true
	default:
		return false
	}
}

type SkillOrchestratorReference struct {
	Kind SkillOrchestratorReferenceKind `json:"kind"`
	ID   string                         `json:"id"`
}

func (r SkillOrchestratorReference) Validate() error {
	if !r.Kind.Valid() || !validSkillOrchestratorIdentifier(r.ID) || len(r.ID) > MaxSkillOrchestratorReferenceBytes {
		return errors.New("skill orchestrator reference is invalid or content-bearing")
	}
	return nil
}

type SkillJob struct {
	ID                string                       `json:"id"`
	WorkflowID        string                       `json:"workflow_id"`
	Scope             SkillOrchestratorScope       `json:"scope"`
	SkillID           string                       `json:"skill_id,omitempty"`
	Stage             SkillOrchestratorStage       `json:"stage"`
	ContractVersion   string                       `json:"contract_version"`
	InputDigest       string                       `json:"input_digest"`
	PolicyVersion     int64                        `json:"policy_version"`
	State             SkillJobState                `json:"state"`
	Priority          int                          `json:"priority"`
	ReadyAt           time.Time                    `json:"ready_at"`
	DependencyCount   int                          `json:"dependency_count"`
	BlockedReason     string                       `json:"blocked_reason,omitempty"`
	Attempt           int                          `json:"attempt"`
	MaxAttempts       int                          `json:"max_attempts"`
	LeaseOwner        string                       `json:"lease_owner,omitempty"`
	LeaseExpiresAt    time.Time                    `json:"lease_expires_at,omitempty"`
	Fence             int64                        `json:"fence"`
	TimeoutAt         time.Time                    `json:"timeout_at,omitempty"`
	CancelRequestedAt time.Time                    `json:"cancel_requested_at,omitempty"`
	ResultKind        SkillJobResultKind           `json:"result_kind,omitempty"`
	ResultReferences  []SkillOrchestratorReference `json:"result_references,omitempty"`
	FailureClass      SkillJobFailureClass         `json:"failure_class,omitempty"`
	FailureCode       string                       `json:"failure_code,omitempty"`
	ReplayOfJobID     string                       `json:"replay_of_job_id,omitempty"`
	CreatedAt         time.Time                    `json:"created_at"`
	UpdatedAt         time.Time                    `json:"updated_at"`
	CompletedAt       time.Time                    `json:"completed_at,omitempty"`
}

type SkillOrchestratorQueueSnapshot struct {
	Stage        SkillOrchestratorStage
	State        SkillJobState
	FailureClass SkillJobFailureClass
	Depth        int
	OldestAt     time.Time
}

func (j SkillJob) Validate() error {
	if !validSkillOrchestratorIdentifier(j.ID) || !validSkillOrchestratorIdentifier(j.WorkflowID) {
		return errors.New("skill job id or workflow_id is invalid")
	}
	if err := j.Scope.Validate(); err != nil {
		return err
	}
	if j.SkillID != "" && !validSkillOrchestratorIdentifier(j.SkillID) {
		return errors.New("skill job skill_id is invalid")
	}
	if !j.Stage.Valid() {
		return errors.New("skill job stage is invalid")
	}
	if j.ContractVersion != SkillOrchestratorContractVersion {
		return errors.New("skill job contract_version is unsupported")
	}
	if !validSkillDigest(j.InputDigest) {
		return errors.New("skill job input_digest is invalid")
	}
	if j.PolicyVersion < 1 || !j.State.Valid() || j.Priority < 0 || j.Priority > 1_000_000 {
		return errors.New("skill job policy_version, state, or priority is invalid")
	}
	if j.ReadyAt.IsZero() || j.DependencyCount < 0 || j.DependencyCount > MaxSkillOrchestratorReferences {
		return errors.New("skill job ready_at or dependency_count is invalid")
	}
	if j.BlockedReason != "" && !validSkillOrchestratorCode(j.BlockedReason, MaxSkillOrchestratorFailureCodeBytes) {
		return errors.New("skill job blocked_reason must be a bounded safe code")
	}
	if j.Attempt < 0 || j.MaxAttempts < 1 || j.MaxAttempts > 100 || j.Attempt > j.MaxAttempts {
		return errors.New("skill job attempt bounds are invalid")
	}
	if !j.ResultKind.Valid() || !j.FailureClass.Valid() {
		return errors.New("skill job result_kind or failure_class is invalid")
	}
	if j.FailureCode != "" && !validSkillOrchestratorCode(j.FailureCode, MaxSkillOrchestratorFailureCodeBytes) {
		return errors.New("skill job failure_code must be a bounded safe code")
	}
	if j.ReplayOfJobID != "" && (!validSkillOrchestratorIdentifier(j.ReplayOfJobID) || j.ReplayOfJobID == j.ID) {
		return errors.New("skill job replay_of_job_id is invalid")
	}
	if len(j.ResultReferences) > MaxSkillOrchestratorReferences {
		return errors.New("skill job result_references exceed bound")
	}
	for _, reference := range j.ResultReferences {
		if err := reference.Validate(); err != nil {
			return err
		}
	}
	if err := validateSkillOrchestratorTimes(j.CreatedAt, j.UpdatedAt); err != nil {
		return fmt.Errorf("skill job: %w", err)
	}
	if !j.CancelRequestedAt.IsZero() && j.CancelRequestedAt.Before(j.CreatedAt) {
		return errors.New("skill job cancel_requested_at precedes created_at")
	}
	if j.State == SkillJobRunning {
		if j.Attempt < 1 || j.Fence < 1 || !validSkillOrchestratorIdentifier(j.LeaseOwner) {
			return errors.New("skill job running state requires attempt, fence, and lease_owner")
		}
		if j.LeaseExpiresAt.IsZero() || !j.LeaseExpiresAt.After(j.UpdatedAt) {
			return errors.New("skill job running lease_expires_at must follow updated_at")
		}
		if j.TimeoutAt.IsZero() || j.TimeoutAt.After(j.LeaseExpiresAt) || !j.TimeoutAt.After(j.UpdatedAt) {
			return errors.New("skill job timeout_at must be within the active lease")
		}
	} else if j.LeaseOwner != "" || !j.LeaseExpiresAt.IsZero() || !j.TimeoutAt.IsZero() {
		return errors.New("skill job lease fields are only valid while running")
	}
	if j.State.Terminal() {
		if j.CompletedAt.IsZero() || j.CompletedAt.Before(j.UpdatedAt) {
			return errors.New("skill job completed_at must follow updated_at")
		}
		if j.ResultKind == SkillJobResultNone {
			return errors.New("skill job terminal state requires result_kind")
		}
	} else {
		if !j.CompletedAt.IsZero() || j.ResultKind != SkillJobResultNone || len(j.ResultReferences) != 0 {
			return errors.New("skill job non-terminal state cannot contain terminal result")
		}
	}
	if j.State == SkillJobBlocked && j.BlockedReason == "" {
		return errors.New("skill job blocked state requires blocked_reason")
	}
	return nil
}

type SkillJobDependency struct {
	JobID               string               `json:"job_id"`
	ParentJobID         string               `json:"parent_job_id"`
	AcceptedResultKinds []SkillJobResultKind `json:"accepted_result_kinds"`
	CreatedAt           time.Time            `json:"created_at"`
}

func (d SkillJobDependency) Validate() error {
	if !validSkillOrchestratorIdentifier(d.JobID) || !validSkillOrchestratorIdentifier(d.ParentJobID) {
		return errors.New("skill job dependency identifiers are invalid")
	}
	if d.JobID == d.ParentJobID {
		return errors.New("skill job cannot depend on itself")
	}
	if len(d.AcceptedResultKinds) == 0 || len(d.AcceptedResultKinds) > 3 {
		return errors.New("skill job dependency accepted_result_kinds are required and bounded")
	}
	seen := make(map[SkillJobResultKind]struct{}, len(d.AcceptedResultKinds))
	for _, kind := range d.AcceptedResultKinds {
		if kind == SkillJobResultNone || !kind.Valid() {
			return errors.New("skill job dependency result kind is invalid")
		}
		if _, ok := seen[kind]; ok {
			return errors.New("skill job dependency result kinds must be unique")
		}
		seen[kind] = struct{}{}
	}
	if d.CreatedAt.IsZero() {
		return errors.New("skill job dependency created_at is required")
	}
	return nil
}

type SkillJobAttempt struct {
	ID             string               `json:"id"`
	JobID          string               `json:"job_id"`
	Attempt        int                  `json:"attempt"`
	Owner          string               `json:"owner"`
	Fence          int64                `json:"fence"`
	StartedAt      time.Time            `json:"started_at"`
	LeaseExpiresAt time.Time            `json:"lease_expires_at"`
	EndedAt        time.Time            `json:"ended_at,omitempty"`
	ResultKind     SkillJobResultKind   `json:"result_kind,omitempty"`
	FailureClass   SkillJobFailureClass `json:"failure_class,omitempty"`
	FailureCode    string               `json:"failure_code,omitempty"`
	DurationMS     int64                `json:"duration_ms"`
	RenewalCount   int                  `json:"renewal_count"`
}

func (a SkillJobAttempt) Validate() error {
	if !validSkillOrchestratorIdentifier(a.ID) || !validSkillOrchestratorIdentifier(a.JobID) || !validSkillOrchestratorIdentifier(a.Owner) {
		return errors.New("skill job attempt identifiers are invalid")
	}
	if a.Attempt < 1 || a.Fence < 1 || a.StartedAt.IsZero() || !a.LeaseExpiresAt.After(a.StartedAt) {
		return errors.New("skill job attempt number, fence, and lease timestamps are invalid")
	}
	if !a.ResultKind.Valid() || !a.FailureClass.Valid() || a.DurationMS < 0 || a.RenewalCount < 0 || a.RenewalCount > 1_000_000 {
		return errors.New("skill job attempt result or counters are invalid")
	}
	if a.FailureCode != "" && !validSkillOrchestratorCode(a.FailureCode, MaxSkillOrchestratorFailureCodeBytes) {
		return errors.New("skill job attempt failure_code must be a bounded safe code")
	}
	if a.EndedAt.IsZero() {
		if a.ResultKind != SkillJobResultNone || a.DurationMS != 0 {
			return errors.New("active skill job attempt cannot contain terminal result")
		}
	} else if a.EndedAt.Before(a.StartedAt) || a.ResultKind == SkillJobResultNone {
		return errors.New("terminal skill job attempt requires ordered ended_at and result_kind")
	}
	return nil
}

type SkillSafetySource string

const (
	SkillSafetySourceDigestCustody      SkillSafetySource = "digest_custody"
	SkillSafetySourceCapabilityAudit    SkillSafetySource = "capability_audit"
	SkillSafetySourceVerifiedExecution  SkillSafetySource = "verified_execution"
	SkillSafetySourceMaterialization    SkillSafetySource = "materialization"
	SkillSafetySourceCriticalRegression SkillSafetySource = "critical_regression"
)

func (s SkillSafetySource) Valid() bool {
	switch s {
	case SkillSafetySourceDigestCustody, SkillSafetySourceCapabilityAudit, SkillSafetySourceVerifiedExecution, SkillSafetySourceMaterialization, SkillSafetySourceCriticalRegression:
		return true
	default:
		return false
	}
}

type SkillSafetySeverity string

const (
	SkillSafetySeveritySoft SkillSafetySeverity = "soft"
	SkillSafetySeverityHard SkillSafetySeverity = "hard"
)

func (s SkillSafetySeverity) Valid() bool {
	return s == SkillSafetySeveritySoft || s == SkillSafetySeverityHard
}

type SkillSafetyDisposition string

const (
	SkillSafetyDispositionPending  SkillSafetyDisposition = "pending"
	SkillSafetyDispositionAccepted SkillSafetyDisposition = "accepted"
	SkillSafetyDispositionRejected SkillSafetyDisposition = "rejected"
)

func (d SkillSafetyDisposition) Valid() bool {
	return d == SkillSafetyDispositionPending || d == SkillSafetyDispositionAccepted || d == SkillSafetyDispositionRejected
}

type SkillOrchestratorSafetySignal struct {
	ID                  string                 `json:"id"`
	Scope               SkillOrchestratorScope `json:"scope"`
	SkillID             string                 `json:"skill_id"`
	RevisionID          string                 `json:"revision_id"`
	Source              SkillSafetySource      `json:"source"`
	VerifierID          string                 `json:"verifier_id"`
	Severity            SkillSafetySeverity    `json:"severity"`
	EvidenceReference   string                 `json:"evidence_reference"`
	DeduplicationDigest string                 `json:"deduplication_digest"`
	PolicyVersion       int64                  `json:"policy_version"`
	Disposition         SkillSafetyDisposition `json:"disposition"`
	CreatedAt           time.Time              `json:"created_at"`
	UpdatedAt           time.Time              `json:"updated_at"`
	AcceptedAt          time.Time              `json:"accepted_at,omitempty"`
}

func (s SkillOrchestratorSafetySignal) Validate() error {
	if !validSkillOrchestratorIdentifier(s.ID) {
		return errors.New("skill safety signal id is invalid")
	}
	if err := s.Scope.Validate(); err != nil {
		return err
	}
	for field, value := range map[string]string{"skill_id": s.SkillID, "revision_id": s.RevisionID, "verifier_id": s.VerifierID, "evidence_reference": s.EvidenceReference} {
		if !validSkillOrchestratorIdentifier(value) {
			return fmt.Errorf("skill safety signal %s is invalid or content-bearing", field)
		}
	}
	if !s.Source.Valid() || !s.Severity.Valid() || !s.Disposition.Valid() {
		return errors.New("skill safety signal source, severity, or disposition is invalid")
	}
	if !validSkillDigest(s.DeduplicationDigest) || s.PolicyVersion < 1 {
		return errors.New("skill safety signal deduplication_digest or policy_version is invalid")
	}
	if err := validateSkillOrchestratorTimes(s.CreatedAt, s.UpdatedAt); err != nil {
		return fmt.Errorf("skill safety signal: %w", err)
	}
	if s.Disposition == SkillSafetyDispositionAccepted {
		if s.AcceptedAt.IsZero() || s.AcceptedAt.Before(s.CreatedAt) {
			return errors.New("accepted skill safety signal requires ordered accepted_at")
		}
	} else if !s.AcceptedAt.IsZero() {
		return errors.New("skill safety signal accepted_at requires accepted disposition")
	}
	return nil
}

type SkillOrchestratorMode string

const (
	SkillOrchestratorDisabled         SkillOrchestratorMode = "disabled"
	SkillOrchestratorShadow           SkillOrchestratorMode = "shadow"
	SkillOrchestratorManual           SkillOrchestratorMode = "manual"
	SkillOrchestratorCanary           SkillOrchestratorMode = "canary"
	SkillOrchestratorAutomaticLowRisk SkillOrchestratorMode = "automatic_low_risk"
)

func (m SkillOrchestratorMode) Valid() bool {
	switch m {
	case SkillOrchestratorDisabled, SkillOrchestratorShadow, SkillOrchestratorManual, SkillOrchestratorCanary, SkillOrchestratorAutomaticLowRisk:
		return true
	default:
		return false
	}
}

type SkillOrchestratorStagePolicy struct {
	Stage           SkillOrchestratorStage `json:"stage"`
	Enabled         bool                   `json:"enabled"`
	LeaseDuration   time.Duration          `json:"lease_duration"`
	RenewalInterval time.Duration          `json:"renewal_interval"`
	Timeout         time.Duration          `json:"timeout"`
	MaxAttempts     int                    `json:"max_attempts"`
	InitialBackoff  time.Duration          `json:"initial_backoff"`
	MaximumBackoff  time.Duration          `json:"maximum_backoff"`
}

func (p SkillOrchestratorStagePolicy) Validate() error {
	if !p.Stage.Valid() {
		return errors.New("skill orchestrator stage policy stage is invalid")
	}
	if p.LeaseDuration < time.Second || p.LeaseDuration > 24*time.Hour {
		return errors.New("skill orchestrator stage policy lease_duration is outside bounds")
	}
	if p.RenewalInterval <= 0 || p.RenewalInterval >= p.LeaseDuration {
		return errors.New("skill orchestrator stage policy renewal_interval must be below lease_duration")
	}
	if p.Timeout <= 0 || p.Timeout > p.LeaseDuration {
		return errors.New("skill orchestrator stage policy timeout must not exceed lease_duration")
	}
	if p.MaxAttempts < 1 || p.MaxAttempts > 100 {
		return errors.New("skill orchestrator stage policy max_attempts is outside bounds")
	}
	if p.InitialBackoff <= 0 || p.MaximumBackoff < p.InitialBackoff || p.MaximumBackoff > 24*time.Hour {
		return errors.New("skill orchestrator stage policy backoff bounds are invalid")
	}
	return nil
}

type SkillOrchestratorConfiguration struct {
	Scope                    SkillOrchestratorScope         `json:"scope"`
	Version                  int64                          `json:"version"`
	ContractVersion          string                         `json:"contract_version"`
	Digest                   string                         `json:"digest"`
	PolicyDigest             string                         `json:"policy_digest"`
	Mode                     SkillOrchestratorMode          `json:"mode"`
	PollInterval             time.Duration                  `json:"poll_interval"`
	ReconciliationInterval   time.Duration                  `json:"reconciliation_interval"`
	ClaimBatch               int                            `json:"claim_batch"`
	WorkerConcurrency        int                            `json:"worker_concurrency"`
	TenantConcurrency        int                            `json:"tenant_concurrency"`
	WorkspaceConcurrency     int                            `json:"workspace_concurrency"`
	DrainTimeout             time.Duration                  `json:"drain_timeout"`
	StaleReadinessThreshold  time.Duration                  `json:"stale_readiness_threshold"`
	EvaluationBudgetUnits    int64                          `json:"evaluation_budget_units"`
	AlertTargets             SkillOrchestratorAlertTargets  `json:"alert_targets"`
	StagePolicies            []SkillOrchestratorStagePolicy `json:"stage_policies"`
	ApprovalReference        string                         `json:"approval_reference,omitempty"`
	ReleaseEvidenceReference string                         `json:"release_evidence_reference,omitempty"`
	SignatureReference       string                         `json:"signature_reference,omitempty"`
	CreatedBy                string                         `json:"created_by"`
	CreatedAt                time.Time                      `json:"created_at"`
}

type SkillOrchestratorAlertTargets struct {
	ReadyQueueStuckAfter time.Duration `json:"ready_queue_stuck_after"`
	LeaseChurnWindow     time.Duration `json:"lease_churn_window"`
	LeaseFailureCount    int           `json:"lease_failure_count"`
	CanaryStaleAfter     time.Duration `json:"canary_stale_after"`
	RollbackFailureAfter time.Duration `json:"rollback_failure_after"`
}

type SkillEvaluationBudgetReservationRecord struct {
	Scope          SkillOrchestratorScope `json:"scope"`
	JobID          string                 `json:"job_id"`
	PolicyVersion  int64                  `json:"policy_version"`
	PeriodStart    time.Time              `json:"period_start"`
	ReservedUnits  int64                  `json:"reserved_units"`
	CommittedUnits int64                  `json:"committed_units"`
	State          string                 `json:"state"`
	ExpiresAt      time.Time              `json:"expires_at"`
}

type SkillEvaluationBudgetReservationRequest struct {
	Scope         SkillOrchestratorScope
	JobID         string
	PolicyVersion int64
	PeriodStart   time.Time
	LimitUnits    int64
	Units         int64
	ExpiresAt     time.Time
	Now           time.Time
}

type SkillMigrationInventoryKind string

const (
	SkillMigrationCandidate           SkillMigrationInventoryKind = "candidate"
	SkillMigrationTestingRevision     SkillMigrationInventoryKind = "testing_revision"
	SkillMigrationCanary              SkillMigrationInventoryKind = "canary"
	SkillMigrationActivationOperation SkillMigrationInventoryKind = "activation_operation"
)

type SkillMigrationInventoryItem struct {
	Kind                 SkillMigrationInventoryKind `json:"kind"`
	ID                   string                      `json:"id"`
	SkillID              string                      `json:"skill_id,omitempty"`
	State                string                      `json:"state"`
	EvidenceDigest       string                      `json:"evidence_digest"`
	ExistingOpenWorkflow bool                        `json:"existing_open_workflow"`
}

type SkillMigrationInventory struct {
	Scope                SkillOrchestratorScope        `json:"scope"`
	SchemaVersion        string                        `json:"schema_version"`
	RestorePaused        bool                          `json:"restore_paused"`
	ConfigurationMode    SkillOrchestratorMode         `json:"configuration_mode"`
	Items                []SkillMigrationInventoryItem `json:"items"`
	UnsupportedContracts []string                      `json:"unsupported_contracts,omitempty"`
	ExistingWorkflows    int64                         `json:"existing_workflows"`
	Truncated            bool                          `json:"truncated"`
}

type SkillChaosRuntime string

const (
	SkillChaosStandalone SkillChaosRuntime = "standalone"
	SkillChaosHosted     SkillChaosRuntime = "hosted"
)

type SkillChaosFaultPoint string

const (
	SkillChaosBeforeSideEffect SkillChaosFaultPoint = "before_side_effect"
	SkillChaosAfterSideEffect  SkillChaosFaultPoint = "after_side_effect"
)

type SkillChaosObservation struct {
	CaseID            string                 `json:"case_id"`
	Runtime           SkillChaosRuntime      `json:"runtime"`
	Stage             SkillOrchestratorStage `json:"stage,omitempty"`
	FaultPoint        SkillChaosFaultPoint   `json:"fault_point,omitempty"`
	Passed            bool                   `json:"passed"`
	Converged         bool                   `json:"converged"`
	DomainSideEffects int                    `json:"domain_side_effects"`
	UnsafeActivations int                    `json:"unsafe_activations"`
	DurationMillis    int64                  `json:"duration_millis"`
}

func RequiredSkillChaosCaseIDs() []string {
	stages := []SkillOrchestratorStage{
		SkillStageDetect, SkillStageBuild, SkillStageEvaluate, SkillStageDecide,
		SkillStageStartCanary, SkillStageAnalyzeCanary, SkillStageActivate,
		SkillStageObserveSafety, SkillStageRollback, SkillStageReconcileMaterialization,
	}
	result := make([]string, 0, len(stages)*2+7)
	for _, stage := range stages {
		result = append(result, "crash_before:"+string(stage), "crash_after:"+string(stage))
	}
	return append(result, "renewal_loss", "duplicate_enqueue", "stale_fence", "database_outage", "evaluator_timeout", "cancellation", "worker_restart")
}

func (t SkillOrchestratorAlertTargets) Validate() error {
	if t.ReadyQueueStuckAfter < time.Second || t.ReadyQueueStuckAfter > 30*24*time.Hour || t.LeaseChurnWindow < time.Second || t.LeaseChurnWindow > 24*time.Hour || t.LeaseFailureCount < 1 || t.LeaseFailureCount > 1_000 || t.CanaryStaleAfter < time.Second || t.CanaryStaleAfter > 30*24*time.Hour || t.RollbackFailureAfter < time.Second || t.RollbackFailureAfter > 24*time.Hour {
		return errors.New("skill orchestrator alert targets are missing or outside bounds")
	}
	return nil
}

type SkillOrchestratorConfigurationAudit struct {
	ActorID     string    `json:"actor_id"`
	RequestID   string    `json:"request_id"`
	Operation   string    `json:"operation"`
	FromVersion int64     `json:"from_version"`
	ToVersion   int64     `json:"to_version"`
	ReasonCode  string    `json:"reason_code"`
	OccurredAt  time.Time `json:"occurred_at"`
}

type SkillReconciliationDomain string

const (
	SkillReconcileLeaseRecovery        SkillReconciliationDomain = "lease_recovery"
	SkillReconcileDependencyReadiness  SkillReconciliationDomain = "dependency_readiness"
	SkillReconcileLifecycleJobParity   SkillReconciliationDomain = "lifecycle_job_parity"
	SkillReconcileBlockedRechecks      SkillReconciliationDomain = "blocked_rechecks"
	SkillReconcileSafetyRollbackParity SkillReconciliationDomain = "safety_rollback_parity"
	SkillReconcileMaterializationDrift SkillReconciliationDomain = "materialization_drift"
	SkillReconcileTerminalCleanup      SkillReconciliationDomain = "terminal_cleanup"
)

func (d SkillReconciliationDomain) Valid() bool {
	switch d {
	case SkillReconcileLeaseRecovery, SkillReconcileDependencyReadiness, SkillReconcileLifecycleJobParity,
		SkillReconcileBlockedRechecks, SkillReconcileSafetyRollbackParity, SkillReconcileMaterializationDrift,
		SkillReconcileTerminalCleanup:
		return true
	default:
		return false
	}
}

type SkillReconciliationCounters struct {
	Scanned  int64 `json:"scanned"`
	Repaired int64 `json:"repaired"`
	Skipped  int64 `json:"skipped"`
	Blocked  int64 `json:"blocked"`
	Failed   int64 `json:"failed"`
}

func (c SkillReconciliationCounters) Validate() error {
	if c.Scanned < 0 || c.Repaired < 0 || c.Skipped < 0 || c.Blocked < 0 || c.Failed < 0 {
		return errors.New("skill reconciliation counters cannot be negative")
	}
	if c.Repaired+c.Skipped+c.Blocked+c.Failed > c.Scanned {
		return errors.New("skill reconciliation outcomes exceed scanned count")
	}
	return nil
}

type SkillReconciliationCursor struct {
	Scope                SkillOrchestratorScope      `json:"scope"`
	Domain               SkillReconciliationDomain   `json:"domain"`
	Cursor               string                      `json:"cursor,omitempty"`
	ConfigurationVersion int64                       `json:"configuration_version"`
	LastCompletedAt      time.Time                   `json:"last_completed_at,omitempty"`
	Counters             SkillReconciliationCounters `json:"counters"`
	UpdatedAt            time.Time                   `json:"updated_at"`
}

func (c SkillReconciliationCursor) Validate() error {
	if err := c.Scope.Validate(); err != nil {
		return err
	}
	if !c.Domain.Valid() || c.ConfigurationVersion < 1 || c.UpdatedAt.IsZero() {
		return errors.New("skill reconciliation domain, configuration version, and updated_at are required")
	}
	if len(c.Cursor) > MaxSkillReconciliationCursorBytes || strings.TrimSpace(c.Cursor) != c.Cursor || strings.ContainsAny(c.Cursor, "\r\n\t") {
		return errors.New("skill reconciliation cursor is invalid")
	}
	if !c.LastCompletedAt.IsZero() && c.LastCompletedAt.After(c.UpdatedAt) {
		return errors.New("skill reconciliation completion follows updated_at")
	}
	return c.Counters.Validate()
}

func (c SkillOrchestratorConfiguration) Validate() error {
	if err := c.Scope.Validate(); err != nil {
		return err
	}
	if c.Version < 1 || c.ContractVersion != SkillOrchestratorContractVersion || !validSkillDigest(c.Digest) || !validSkillDigest(c.PolicyDigest) {
		return errors.New("skill orchestrator configuration version, contract_version, or digest is invalid")
	}
	if !c.Mode.Valid() {
		return errors.New("skill orchestrator configuration mode is invalid")
	}
	if c.PollInterval < 100*time.Millisecond || c.PollInterval > time.Hour || c.ReconciliationInterval < time.Second || c.ReconciliationInterval > 24*time.Hour {
		return errors.New("skill orchestrator configuration polling intervals are outside bounds")
	}
	if c.ClaimBatch < 1 || c.ClaimBatch > 1_000 || c.WorkerConcurrency < 1 || c.WorkerConcurrency > 1_000 || c.TenantConcurrency < 1 || c.TenantConcurrency > c.WorkerConcurrency || c.WorkspaceConcurrency < 1 || c.WorkspaceConcurrency > c.TenantConcurrency {
		return errors.New("skill orchestrator configuration concurrency bounds are invalid")
	}
	if c.DrainTimeout < time.Second || c.DrainTimeout > time.Hour || c.StaleReadinessThreshold < time.Second || c.StaleReadinessThreshold > 7*24*time.Hour || c.EvaluationBudgetUnits < 0 {
		return errors.New("skill orchestrator configuration timeout or budget bounds are invalid")
	}
	if c.Mode != SkillOrchestratorDisabled {
		if err := c.AlertTargets.Validate(); err != nil {
			return err
		}
	}
	if len(c.StagePolicies) == 0 || len(c.StagePolicies) > MaxSkillOrchestratorStagePolicies {
		return errors.New("skill orchestrator configuration stage_policies are required and bounded")
	}
	seen := make(map[SkillOrchestratorStage]struct{}, len(c.StagePolicies))
	for _, policy := range c.StagePolicies {
		if err := policy.Validate(); err != nil {
			return err
		}
		if _, ok := seen[policy.Stage]; ok {
			return errors.New("skill orchestrator configuration stage_policies must be unique")
		}
		seen[policy.Stage] = struct{}{}
	}
	if !validSkillOrchestratorIdentifier(c.CreatedBy) || c.CreatedAt.IsZero() {
		return errors.New("skill orchestrator configuration created_by and created_at are required")
	}
	for field, value := range map[string]string{"approval_reference": c.ApprovalReference, "release_evidence_reference": c.ReleaseEvidenceReference, "signature_reference": c.SignatureReference} {
		if value != "" && !validSkillOrchestratorIdentifier(value) {
			return fmt.Errorf("skill orchestrator configuration %s is invalid", field)
		}
	}
	if c.Mode == SkillOrchestratorAutomaticLowRisk && (c.ApprovalReference == "" || c.ReleaseEvidenceReference == "" || c.SignatureReference == "") {
		return errors.New("automatic low-risk mode requires approval, release evidence, and signature references")
	}
	return nil
}

// ClaimsEnabled reports whether this immutable configuration permits new work
// for a stage. Disabled configurations stop all new claims; stage flags then
// narrow the progressively more capable master modes.
func (c SkillOrchestratorConfiguration) ClaimsEnabled(stage SkillOrchestratorStage) bool {
	if !stage.Valid() || c.Mode == SkillOrchestratorDisabled {
		return false
	}
	for _, policy := range c.StagePolicies {
		if policy.Stage != stage || !policy.Enabled {
			continue
		}
		switch c.Mode {
		case SkillOrchestratorShadow:
			return stage == SkillStageDetect || stage == SkillStageEvaluate || stage == SkillStageDecide || stage == SkillStageObserveSafety
		case SkillOrchestratorManual:
			return stage != SkillStageActivate && stage != SkillStageRollback
		case SkillOrchestratorCanary:
			return stage != SkillStageActivate
		case SkillOrchestratorAutomaticLowRisk:
			return true
		}
	}
	return false
}

// MayDrainRunning preserves only recovery work after master disablement. New
// claims remain stopped by ClaimsEnabled; an already leased safety or rollback
// job may finish so disablement cannot strand recovery halfway through.
func (c SkillOrchestratorConfiguration) MayDrainRunning(stage SkillOrchestratorStage) bool {
	if c.Mode == SkillOrchestratorDisabled {
		return stage == SkillStageObserveSafety || stage == SkillStageRollback || stage == SkillStageReconcileMaterialization
	}
	return c.ClaimsEnabled(stage)
}

func validSkillOrchestratorIdentifier(value string) bool {
	return len(value) <= MaxSkillOrchestratorReferenceBytes && skillOrchestratorIdentifierPattern.MatchString(value) && !strings.Contains(value, "..")
}

func validSkillOrchestratorCode(value string, maxBytes int) bool {
	return value != "" && len(value) <= maxBytes && skillOrchestratorCodePattern.MatchString(value)
}

func validateSkillOrchestratorTimes(createdAt, updatedAt time.Time) error {
	if createdAt.IsZero() || updatedAt.IsZero() || updatedAt.Before(createdAt) {
		return errors.New("created_at and ordered updated_at are required")
	}
	return nil
}
