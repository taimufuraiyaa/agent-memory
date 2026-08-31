package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const (
	SkillOrchestratorNormalPriority = 100
	SkillOrchestratorSafetyPriority = 1_000
)

var skillLifecycleSignalDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type SkillLifecycleSignalKind string

const (
	SkillSignalLesson     SkillLifecycleSignalKind = "lesson"
	SkillSignalCandidate  SkillLifecycleSignalKind = "candidate"
	SkillSignalRevision   SkillLifecycleSignalKind = "revision"
	SkillSignalEvaluation SkillLifecycleSignalKind = "evaluation"
	SkillSignalDecision   SkillLifecycleSignalKind = "decision"
	SkillSignalCanary     SkillLifecycleSignalKind = "canary"
	SkillSignalPromotion  SkillLifecycleSignalKind = "promotion"
	SkillSignalExecution  SkillLifecycleSignalKind = "execution"
	SkillSignalSafety     SkillLifecycleSignalKind = "safety"
)

func (k SkillLifecycleSignalKind) Valid() bool {
	switch k {
	case SkillSignalLesson, SkillSignalCandidate, SkillSignalRevision, SkillSignalEvaluation, SkillSignalDecision, SkillSignalCanary, SkillSignalPromotion, SkillSignalExecution, SkillSignalSafety:
		return true
	default:
		return false
	}
}

type SkillLifecycleSignal struct {
	ID                   string
	Kind                 SkillLifecycleSignalKind
	Scope                core.SkillOrchestratorScope
	SkillID              string
	RevisionID           string
	ReferenceID          string
	EvidenceDigest       string
	Verified             bool
	Authorized           bool
	Tombstoned           bool
	ParentJobIDs         []string
	ConfigurationVersion int64
	PolicyVersion        int64
	PolicyDigest         string
	OccurredAt           time.Time
}

type SkillSignalRouteResult = contracts.SkillSignalRouteResult

type SkillSignalRouteRepository interface {
	RouteSkillSignal(context.Context, core.SkillWorkflow, core.SkillJob, []core.SkillJobDependency) (SkillSignalRouteResult, error)
}

type SkillSignalRouter struct{ repository SkillSignalRouteRepository }

func NewSkillSignalRouter(repository SkillSignalRouteRepository) *SkillSignalRouter {
	return &SkillSignalRouter{repository: repository}
}

func (r *SkillSignalRouter) Route(ctx context.Context, signal SkillLifecycleSignal) (SkillSignalRouteResult, error) {
	if r == nil || r.repository == nil {
		return SkillSignalRouteResult{}, errors.New("skill signal route repository is required")
	}
	if err := signal.Scope.Validate(); err != nil {
		return SkillSignalRouteResult{}, err
	}
	if signal.Tombstoned {
		return SkillSignalRouteResult{Ignored: true}, nil
	}
	if !signal.Verified || !signal.Authorized {
		return SkillSignalRouteResult{}, errors.New("skill lifecycle signal must be verified and authorized")
	}
	if !signal.Kind.Valid() || !validSkillSignalIdentifier(signal.ID) || !validSkillSignalIdentifier(signal.ReferenceID) {
		return SkillSignalRouteResult{}, errors.New("skill lifecycle signal identity or kind is invalid")
	}
	if signal.SkillID != "" && !validSkillSignalIdentifier(signal.SkillID) {
		return SkillSignalRouteResult{}, errors.New("skill lifecycle signal skill_id is invalid")
	}
	if signal.RevisionID != "" && !validSkillSignalIdentifier(signal.RevisionID) {
		return SkillSignalRouteResult{}, errors.New("skill lifecycle signal revision_id is invalid")
	}
	if !skillLifecycleSignalDigestPattern.MatchString(signal.EvidenceDigest) || !skillLifecycleSignalDigestPattern.MatchString(signal.PolicyDigest) {
		return SkillSignalRouteResult{}, errors.New("skill lifecycle signal evidence or policy digest is invalid")
	}
	if signal.ConfigurationVersion < 1 || signal.PolicyVersion < 1 || signal.OccurredAt.IsZero() {
		return SkillSignalRouteResult{}, errors.New("skill lifecycle signal versions and occurred_at are required")
	}
	parents := append([]string(nil), signal.ParentJobIDs...)
	sort.Strings(parents)
	for index, parent := range parents {
		if !validSkillSignalIdentifier(parent) || (index > 0 && parents[index-1] == parent) {
			return SkillSignalRouteResult{}, errors.New("skill lifecycle signal parent_job_ids are invalid or duplicated")
		}
	}
	stage := stageForSkillSignal(signal.Kind)
	inputDigest := digestSkillLifecycleSignal(signal, parents)
	workflowKind := core.SkillWorkflowAutomaticRevision
	priority := SkillOrchestratorNormalPriority
	if signal.Kind == SkillSignalSafety {
		workflowKind = core.SkillWorkflowSafetyRollback
		priority = SkillOrchestratorSafetyPriority
	}
	stableKey := strings.Join([]string{signal.Scope.TenantID, signal.Scope.WorkspaceID, signal.Scope.Environment, string(signal.Kind), signal.ID, inputDigest}, "\x00")
	workflowID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("skill-workflow\x00"+stableKey)).String()
	jobID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("skill-job\x00"+stableKey+"\x00"+string(stage))).String()
	workflow := core.SkillWorkflow{
		ID: workflowID, Scope: signal.Scope, SkillID: signal.SkillID,
		OriginKind: core.SkillWorkflowOriginLifecycleSignal, OriginID: signal.ID,
		Kind: workflowKind, ContractVersion: core.SkillOrchestratorContractVersion, InputDigest: inputDigest,
		State: core.SkillWorkflowOpen, CurrentStage: stage, Generation: 1,
		ConfigurationVersion: signal.ConfigurationVersion, PolicyDigest: signal.PolicyDigest,
		CreatedAt: signal.OccurredAt.UTC(), UpdatedAt: signal.OccurredAt.UTC(),
	}
	job := core.SkillJob{
		ID: jobID, WorkflowID: workflowID, Scope: signal.Scope, SkillID: signal.SkillID, Stage: stage,
		ContractVersion: core.SkillOrchestratorContractVersion, InputDigest: inputDigest, PolicyVersion: signal.PolicyVersion,
		State: core.SkillJobQueued, Priority: priority, ReadyAt: signal.OccurredAt.UTC(), MaxAttempts: 3,
		CreatedAt: signal.OccurredAt.UTC(), UpdatedAt: signal.OccurredAt.UTC(),
	}
	dependencies := make([]core.SkillJobDependency, 0, len(parents))
	for _, parent := range parents {
		dependencies = append(dependencies, core.SkillJobDependency{
			JobID: jobID, ParentJobID: parent, AcceptedResultKinds: []core.SkillJobResultKind{core.SkillJobResultSucceeded},
			CreatedAt: signal.OccurredAt.UTC(),
		})
	}
	result, err := r.repository.RouteSkillSignal(ctx, workflow, job, dependencies)
	if err != nil {
		return SkillSignalRouteResult{}, fmt.Errorf("route skill lifecycle signal: %w", err)
	}
	return result, nil
}

func stageForSkillSignal(kind SkillLifecycleSignalKind) core.SkillOrchestratorStage {
	switch kind {
	case SkillSignalLesson:
		return core.SkillStageDetect
	case SkillSignalCandidate:
		return core.SkillStageBuild
	case SkillSignalRevision:
		return core.SkillStageEvaluate
	case SkillSignalEvaluation:
		return core.SkillStageDecide
	case SkillSignalDecision:
		return core.SkillStageStartCanary
	case SkillSignalPromotion:
		return core.SkillStageActivate
	case SkillSignalCanary, SkillSignalExecution:
		return core.SkillStageAnalyzeCanary
	case SkillSignalSafety:
		return core.SkillStageObserveSafety
	default:
		return ""
	}
}

func digestSkillLifecycleSignal(signal SkillLifecycleSignal, parents []string) string {
	canonical := strings.Join([]string{
		string(signal.Kind), signal.ID, signal.Scope.TenantID, signal.Scope.WorkspaceID, signal.Scope.Environment,
		signal.SkillID, signal.RevisionID, signal.ReferenceID, signal.EvidenceDigest,
		fmt.Sprintf("%d", signal.ConfigurationVersion), fmt.Sprintf("%d", signal.PolicyVersion), signal.PolicyDigest,
		strings.Join(parents, ","),
	}, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validSkillSignalIdentifier(value string) bool {
	return value != "" && len(value) <= 256 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "/\\\r\n\t") && !strings.Contains(value, "..")
}
