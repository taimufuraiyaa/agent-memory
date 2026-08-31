package application

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillSafetyIngressRequest struct {
	ID                  string
	Scope               core.SkillOrchestratorScope
	SkillID             string
	RevisionID          string
	RevisionDigest      string
	Kind                core.SkillSafetySignalKind
	SourceType          string
	VerifierID          string
	EvidenceReference   string
	DeduplicationDigest string
	PolicyVersion       int64
	AuthenticationProof string
}

type SkillSafetyIngressAuthenticator interface {
	AuthenticateSkillSafetySignal(context.Context, SkillSafetyIngressRequest) error
}

type SkillSafetyIngressRepository interface {
	GetSkillRevision(context.Context, string, string) (core.SkillRevision, error)
	GetSkillSafetySignal(context.Context, string, string) (core.SkillSafetySignal, error)
	CreateSkillSafetySignal(context.Context, core.SkillSafetySignal) error
	DisableSkillRevisionForSafety(context.Context, string, string, string, string) error
	SkillSignalRouteRepository
}

type SkillSafetyIngressResult struct {
	Signal    core.SkillSafetySignal
	Route     SkillSignalRouteResult
	Duplicate bool
}

type SkillSafetyIngress struct {
	repository    SkillSafetyIngressRepository
	authenticator SkillSafetyIngressAuthenticator
	router        *SkillSignalRouter
	configuration SkillSignalConfiguration
	cooldown      time.Duration
	now           func() time.Time
}

func NewSkillSafetyIngress(repository SkillSafetyIngressRepository, authenticator SkillSafetyIngressAuthenticator, configuration SkillSignalConfiguration, cooldown time.Duration, now func() time.Time) (*SkillSafetyIngress, error) {
	if repository == nil || authenticator == nil || cooldown <= 0 || cooldown > 30*24*time.Hour {
		return nil, errors.New("skill safety ingress dependencies and cooldown are required")
	}
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &SkillSafetyIngress{repository: repository, authenticator: authenticator, router: NewSkillSignalRouter(repository), configuration: configuration, cooldown: cooldown, now: now}, nil
}

func (i *SkillSafetyIngress) Admit(ctx context.Context, request SkillSafetyIngressRequest) (SkillSafetyIngressResult, error) {
	if i == nil || !validSkillSignalIdentifier(request.ID) || !validSkillSignalIdentifier(request.SkillID) || !validSkillSignalIdentifier(request.RevisionID) || !request.Kind.Valid() || !skillLifecycleSignalDigestPattern.MatchString(request.RevisionDigest) || !skillLifecycleSignalDigestPattern.MatchString(request.DeduplicationDigest) || request.PolicyVersion != i.configuration.PolicyVersion || strings.TrimSpace(request.SourceType) == "" || strings.TrimSpace(request.VerifierID) == "" || strings.TrimSpace(request.EvidenceReference) == "" || strings.TrimSpace(request.AuthenticationProof) == "" {
		return SkillSafetyIngressResult{}, errors.New("skill safety ingress request is invalid")
	}
	if err := request.Scope.Validate(); err != nil {
		return SkillSafetyIngressResult{}, err
	}
	if request.Scope.Environment != i.configuration.Environment {
		return SkillSafetyIngressResult{}, errors.New("skill safety ingress environment is not configured")
	}
	if err := i.authenticator.AuthenticateSkillSafetySignal(ctx, request); err != nil {
		return SkillSafetyIngressResult{}, err
	}
	revision, err := i.repository.GetSkillRevision(ctx, request.Scope.WorkspaceID, request.RevisionID)
	if err != nil || revision.Workspace != request.Scope.WorkspaceID || revision.SkillID != request.SkillID || revision.BundleDigest != request.RevisionDigest {
		return SkillSafetyIngressResult{}, errors.New("skill safety revision binding is invalid")
	}
	now := i.now().UTC()
	signal := core.SkillSafetySignal{ID: request.ID, Workspace: request.Scope.WorkspaceID, Environment: request.Scope.Environment,
		SkillID: request.SkillID, RevisionID: request.RevisionID, Kind: request.Kind, Verified: true,
		SourceType: request.SourceType, VerifierID: request.VerifierID, EvidenceRef: request.EvidenceReference,
		DedupDigest: request.DeduplicationDigest, PolicyVersion: request.PolicyVersion, Occurrences: 1, CreatedAt: now, UpdatedAt: now}
	if request.Kind.Hard() {
		signal.State = core.SkillSafetyRollbackPending
	} else {
		signal.State, signal.CooldownUntil = core.SkillSafetyCooldown, now.Add(i.cooldown)
	}
	if err := signal.Validate(); err != nil {
		return SkillSafetyIngressResult{}, err
	}
	existing, getErr := i.repository.GetSkillSafetySignal(ctx, signal.Workspace, signal.ID)
	if getErr == nil {
		if !sameSkillSafetyIngress(existing, signal) {
			return SkillSafetyIngressResult{}, errors.New("skill safety signal identity is bound to different evidence")
		}
		signal = existing
	} else if !errors.Is(getErr, sql.ErrNoRows) {
		return SkillSafetyIngressResult{}, getErr
	} else if err := i.repository.CreateSkillSafetySignal(ctx, signal); err != nil {
		return SkillSafetyIngressResult{}, err
	}
	result := SkillSafetyIngressResult{Signal: signal, Duplicate: getErr == nil}
	if !signal.Kind.Hard() {
		return result, nil
	}
	// Allocation is disabled before rollback routing. If routing fails, the
	// safety-parity reconciler can repair the missing highest-priority job while
	// the affected revision remains unavailable.
	if err := i.repository.DisableSkillRevisionForSafety(ctx, signal.Workspace, signal.Environment, signal.SkillID, signal.RevisionID); err != nil {
		return SkillSafetyIngressResult{}, err
	}
	lifecycleSignal, err := SkillLifecycleSignalForSafety(signal, i.configuration)
	if err != nil {
		return SkillSafetyIngressResult{}, err
	}
	routed, err := i.router.Route(ctx, lifecycleSignal)
	if err != nil {
		return SkillSafetyIngressResult{}, err
	}
	result.Route = routed
	return result, nil
}

func sameSkillSafetyIngress(left, right core.SkillSafetySignal) bool {
	return left.Workspace == right.Workspace && left.Environment == right.Environment && left.SkillID == right.SkillID && left.RevisionID == right.RevisionID && left.Kind == right.Kind && left.SourceType == right.SourceType && left.VerifierID == right.VerifierID && left.EvidenceRef == right.EvidenceRef && left.DedupDigest == right.DedupDigest && left.PolicyVersion == right.PolicyVersion
}

func SkillLifecycleSignalForSafety(signal core.SkillSafetySignal, configuration SkillSignalConfiguration) (SkillLifecycleSignal, error) {
	if err := configuration.Validate(); err != nil {
		return SkillLifecycleSignal{}, err
	}
	if err := signal.Validate(); err != nil {
		return SkillLifecycleSignal{}, err
	}
	if !signal.Kind.Hard() || signal.DedupDigest == "" {
		return SkillLifecycleSignal{}, errors.New("only authenticated hard safety signals can route rollback")
	}
	return SkillLifecycleSignal{ID: signal.ID, Kind: SkillSignalSafety,
		Scope:   core.SkillOrchestratorScope{WorkspaceID: signal.Workspace, Environment: signal.Environment},
		SkillID: signal.SkillID, RevisionID: signal.RevisionID, ReferenceID: signal.ID,
		EvidenceDigest: signal.DedupDigest, Verified: signal.Verified, Authorized: true,
		ConfigurationVersion: configuration.ConfigurationVersion, PolicyVersion: signal.PolicyVersion,
		PolicyDigest: configuration.PolicyDigest, OccurredAt: signal.CreatedAt}, nil
}

type SkillSafetyRollbackRepository interface {
	skillSafetyRepositoryContract
	GetSkillWorkflow(context.Context, core.SkillOrchestratorScope, string) (core.SkillWorkflow, error)
}

type SkillSafetyRollbackAdapter struct {
	repository    SkillSafetyRollbackRepository
	observer      *SkillSafetyObserver
	configuration SkillSignalConfiguration
}

func NewSkillSafetyRollbackAdapter(repository SkillSafetyRollbackRepository, activator skillRevisionActivator, cooldown time.Duration, configuration SkillSignalConfiguration, now func() time.Time) (*SkillSafetyRollbackAdapter, error) {
	if repository == nil || activator == nil || cooldown <= 0 {
		return nil, errors.New("skill safety rollback adapter dependencies are required")
	}
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	return &SkillSafetyRollbackAdapter{repository: repository, observer: NewSkillSafetyObserver(repository, activator, cooldown, now), configuration: configuration}, nil
}

func (a *SkillSafetyRollbackAdapter) Execute(ctx context.Context, job core.SkillJob) (SkillStageResult, error) {
	if a == nil || job.Stage != core.SkillStageRollback {
		return SkillStageResult{}, safetyStageError(core.SkillFailurePermanentValidation, "invalid_safety_rollback_job", errors.New("invalid safety rollback job"))
	}
	workflow, err := a.repository.GetSkillWorkflow(ctx, job.Scope, job.WorkflowID)
	if err != nil {
		return SkillStageResult{}, safetyStageError(core.SkillFailureDependencyUnavailable, "safety_workflow_unavailable", err)
	}
	signal, err := a.repository.GetSkillSafetySignal(ctx, job.Scope.WorkspaceID, workflow.OriginID)
	if err != nil {
		return SkillStageResult{}, safetyStageError(core.SkillFailureDependencyUnavailable, "safety_signal_unavailable", err)
	}
	lifecycleSignal, err := SkillLifecycleSignalForSafety(signal, a.configuration)
	if err != nil || workflow.OriginKind != core.SkillWorkflowOriginLifecycleSignal || job.InputDigest != digestSkillLifecycleSignal(lifecycleSignal, nil) || workflow.InputDigest != job.InputDigest || job.PolicyVersion != signal.PolicyVersion {
		return SkillStageResult{}, safetyStageError(core.SkillFailurePermanentValidation, "safety_rollback_binding_mismatch", errors.Join(err, errors.New("safety rollback binding mismatch")))
	}
	result, err := a.observer.Observe(ctx, SkillSafetyObservation{ID: signal.ID, Workspace: signal.Workspace, Environment: signal.Environment, SkillID: signal.SkillID, RevisionID: signal.RevisionID, Kind: signal.Kind, Verified: true})
	if err != nil {
		return SkillStageResult{}, safetyStageError(core.SkillFailureDependencyUnavailable, "safety_rollback_failed", err)
	}
	references := []core.SkillOrchestratorReference{{Kind: core.SkillReferenceSafetySignal, ID: result.Signal.ID}}
	if result.Activation != nil {
		references = append(references, core.SkillOrchestratorReference{Kind: core.SkillReferenceActivation, ID: result.Activation.ID})
	}
	return SkillStageResult{ResultKind: core.SkillJobResultSucceeded, References: references}, nil
}

func safetyStageError(class core.SkillJobFailureClass, code string, err error) error {
	return &SkillStageError{Failure: SkillStageFailure{Class: class, Code: code}, Err: err}
}
