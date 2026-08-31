package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillCanaryStageConfiguration struct {
	Signal           SkillSignalConfiguration
	Enabled          bool
	PolicyID         string
	Actor            string
	MinimumSamples   int64
	MinimumWindowAge time.Duration
	MaximumWindowAge time.Duration
	RecheckInterval  time.Duration
}

func (c SkillCanaryStageConfiguration) Validate() error {
	if err := c.Signal.Validate(); err != nil {
		return err
	}
	if !validSkillSignalIdentifier(c.PolicyID) || !validSkillSignalIdentifier(c.Actor) || c.MinimumSamples < 1 || c.MinimumSamples > 1_000_000 || c.MinimumWindowAge <= 0 || c.MaximumWindowAge < c.MinimumWindowAge || c.MaximumWindowAge > 30*24*time.Hour || c.RecheckInterval <= 0 || c.RecheckInterval > c.MaximumWindowAge {
		return errors.New("skill canary stage configuration is invalid")
	}
	return nil
}

func SkillLifecycleSignalForDecision(decision core.SkillPolicyDecision, configuration SkillSignalConfiguration) (SkillLifecycleSignal, error) {
	if err := configuration.Validate(); err != nil {
		return SkillLifecycleSignal{}, err
	}
	if err := decision.Validate(); err != nil {
		return SkillLifecycleSignal{}, err
	}
	canonical := strings.Join([]string{decision.ID, decision.Workspace, decision.SkillID, decision.RevisionID, decision.PolicyID, strconv.FormatInt(decision.PolicyVersion, 10), string(decision.RiskTier), string(decision.Decision), strings.Join(decision.EvaluationRunIDs, ","), strings.Join(decision.ReasonCodes, ",")}, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return SkillLifecycleSignal{ID: decision.ID, Kind: SkillSignalDecision,
		Scope:   core.SkillOrchestratorScope{WorkspaceID: decision.Workspace, Environment: configuration.Environment},
		SkillID: decision.SkillID, RevisionID: decision.RevisionID, ReferenceID: decision.ID,
		EvidenceDigest: "sha256:" + hex.EncodeToString(digest[:]), Verified: true, Authorized: true,
		ConfigurationVersion: configuration.ConfigurationVersion, PolicyVersion: configuration.PolicyVersion,
		PolicyDigest: configuration.PolicyDigest, OccurredAt: decision.DecidedAt}, nil
}

type SkillCanaryStageRepository interface {
	skillCanaryStartRepository
	GetSkillWorkflow(context.Context, core.SkillOrchestratorScope, string) (core.SkillWorkflow, error)
	GetSkillActivation(context.Context, string, string, string) (core.SkillActivation, error)
}

type SkillCanaryStartAdapter struct {
	repository    SkillCanaryStageRepository
	service       *SkillCanaryStartService
	configuration SkillCanaryStageConfiguration
	downstream    *SkillCanaryDueScheduler
}

func (a *SkillCanaryStartAdapter) WithDownstreamRouter(router SkillLessonSignalRouter) error {
	scheduler, err := NewSkillCanaryDueScheduler(router, a.configuration)
	if err != nil {
		return err
	}
	a.downstream = scheduler
	return nil
}

func NewSkillCanaryStartAdapter(repository SkillCanaryStageRepository, configuration SkillCanaryStageConfiguration, now func() time.Time) (*SkillCanaryStartAdapter, error) {
	if repository == nil {
		return nil, errors.New("skill canary start adapter repository is required")
	}
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	return &SkillCanaryStartAdapter{repository: repository, service: NewSkillCanaryStartService(repository, now), configuration: configuration}, nil
}

func (a *SkillCanaryStartAdapter) Execute(ctx context.Context, job core.SkillJob) (SkillStageResult, error) {
	if a == nil || a.repository == nil || job.Stage != core.SkillStageStartCanary {
		return SkillStageResult{}, canaryStageError(core.SkillFailurePermanentValidation, "invalid_canary_start_job", errors.New("invalid canary start job"))
	}
	if !a.configuration.Enabled {
		return SkillStageResult{}, canaryStageError(core.SkillFailurePolicyBlock, "canary_policy_disabled", errors.New("canary policy is disabled"))
	}
	workflow, err := a.repository.GetSkillWorkflow(ctx, job.Scope, job.WorkflowID)
	if err != nil {
		return SkillStageResult{}, canaryStageError(core.SkillFailureDependencyUnavailable, "canary_workflow_unavailable", err)
	}
	decision, err := a.repository.GetSkillPolicyDecision(ctx, job.Scope.WorkspaceID, workflow.OriginID)
	if err != nil {
		return SkillStageResult{}, canaryStageError(core.SkillFailureDependencyUnavailable, "canary_decision_unavailable", err)
	}
	signal, err := SkillLifecycleSignalForDecision(decision, a.configuration.Signal)
	if err != nil {
		return SkillStageResult{}, canaryStageError(core.SkillFailurePermanentValidation, "canary_decision_invalid", err)
	}
	expectedDigest := digestSkillLifecycleSignal(signal, nil)
	if workflow.OriginKind != core.SkillWorkflowOriginLifecycleSignal || job.InputDigest != expectedDigest || workflow.InputDigest != expectedDigest || job.PolicyVersion != a.configuration.Signal.PolicyVersion || decision.PolicyID != a.configuration.PolicyID || decision.PolicyVersion != job.PolicyVersion {
		return SkillStageResult{}, canaryStageError(core.SkillFailurePermanentValidation, "canary_binding_mismatch", errors.New("canary workflow binding mismatch"))
	}
	revision, err := a.repository.GetSkillRevision(ctx, job.Scope.WorkspaceID, decision.RevisionID)
	if err != nil {
		return SkillStageResult{}, canaryStageError(core.SkillFailureDependencyUnavailable, "canary_revision_unavailable", err)
	}
	activation, err := a.repository.GetSkillActivation(ctx, job.Scope.WorkspaceID, job.Scope.Environment, decision.SkillID)
	if err != nil {
		return SkillStageResult{}, canaryStageError(core.SkillFailureDependencyUnavailable, "canary_activation_unavailable", err)
	}
	if revision.State == core.SkillRevisionCanary && activation.CanaryRevisionID == revision.ID && activation.CanaryDigest == revision.BundleDigest && activation.PolicyDecisionID == decision.ID {
		return a.completeCanaryStart(ctx, activation, revision)
	}
	activation, err = a.service.Start(ctx, SkillCanaryStartInput{Workspace: job.Scope.WorkspaceID, Environment: job.Scope.Environment,
		SkillID: decision.SkillID, CandidateRevisionID: revision.ID, PolicyDecisionID: decision.ID,
		ExpectedGeneration: activation.Generation, Actor: a.configuration.Actor})
	if err != nil {
		class, code := core.SkillFailurePermanentValidation, "canary_start_rejected"
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "generation") || strings.Contains(message, "occupied") {
			class, code = core.SkillFailureContention, "canary_generation_stale"
		}
		if strings.Contains(message, "approval") || strings.Contains(message, "high-risk") {
			class, code = core.SkillFailurePolicyBlock, "canary_approval_required"
		}
		return SkillStageResult{}, canaryStageError(class, code, err)
	}
	revision.State = core.SkillRevisionCanary
	return a.completeCanaryStart(ctx, activation, revision)
}

func (a *SkillCanaryStartAdapter) completeCanaryStart(ctx context.Context, activation core.SkillActivation, revision core.SkillRevision) (SkillStageResult, error) {
	if a.downstream != nil {
		if _, err := a.downstream.Schedule(ctx, SkillCanaryDueRequest{Activation: activation, Revision: revision, WindowStarted: activation.UpdatedAt, Now: activation.UpdatedAt}); err != nil {
			return SkillStageResult{}, canaryStageError(core.SkillFailureDependencyUnavailable, "canary_wakeup_unavailable", err)
		}
	}
	return canaryStartResult(activation), nil
}

func canaryStartResult(activation core.SkillActivation) SkillStageResult {
	return SkillStageResult{ResultKind: core.SkillJobResultSucceeded, References: []core.SkillOrchestratorReference{{Kind: core.SkillReferenceActivation, ID: activation.ID}}}
}

type SkillCanaryDueRequest struct {
	Activation      core.SkillActivation
	Revision        core.SkillRevision
	WindowStarted   time.Time
	VerifiedSamples int64
	Now             time.Time
}

type SkillCanaryDueResult struct {
	Due        bool
	MaximumAge bool
	NextAt     time.Time
	Route      SkillSignalRouteResult
}

type SkillCanaryDueScheduler struct {
	router        SkillLessonSignalRouter
	configuration SkillCanaryStageConfiguration
}

func NewSkillCanaryDueScheduler(router SkillLessonSignalRouter, configuration SkillCanaryStageConfiguration) (*SkillCanaryDueScheduler, error) {
	if router == nil {
		return nil, errors.New("skill canary due scheduler router is required")
	}
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	return &SkillCanaryDueScheduler{router: router, configuration: configuration}, nil
}

func (s *SkillCanaryDueScheduler) Schedule(ctx context.Context, request SkillCanaryDueRequest) (SkillCanaryDueResult, error) {
	if s == nil || request.WindowStarted.IsZero() || request.Now.IsZero() || request.Now.Before(request.WindowStarted) || request.VerifiedSamples < 0 {
		return SkillCanaryDueResult{}, errors.New("skill canary due request is invalid")
	}
	if !s.configuration.Enabled {
		return SkillCanaryDueResult{}, canaryStageError(core.SkillFailurePolicyBlock, "canary_policy_disabled", errors.New("canary policy is disabled"))
	}
	if request.Activation.CanaryRevisionID != request.Revision.ID || request.Activation.CanaryDigest != request.Revision.BundleDigest || request.Revision.State != core.SkillRevisionCanary {
		return SkillCanaryDueResult{}, errors.New("skill canary due binding is stale")
	}
	age := request.Now.Sub(request.WindowStarted)
	maximumAge := age >= s.configuration.MaximumWindowAge
	due := maximumAge || (age >= s.configuration.MinimumWindowAge && request.VerifiedSamples >= s.configuration.MinimumSamples)
	next := request.Now.Add(s.configuration.RecheckInterval)
	if !due {
		minimumAt := request.WindowStarted.Add(s.configuration.MinimumWindowAge)
		maximumAt := request.WindowStarted.Add(s.configuration.MaximumWindowAge)
		if request.Now.Before(minimumAt) && minimumAt.Before(next) {
			next = minimumAt
		}
		if maximumAt.Before(next) {
			next = maximumAt
		}
		signal, err := SkillLifecycleSignalForCanary(request.Activation, request.Revision, request.WindowStarted, next, s.configuration.Signal)
		if err != nil {
			return SkillCanaryDueResult{}, err
		}
		routed, err := s.router.Route(ctx, signal)
		if err != nil {
			return SkillCanaryDueResult{}, err
		}
		return SkillCanaryDueResult{NextAt: next, Route: routed}, nil
	}
	signal, err := SkillLifecycleSignalForCanary(request.Activation, request.Revision, request.WindowStarted, request.Now, s.configuration.Signal)
	if err != nil {
		return SkillCanaryDueResult{}, err
	}
	routed, err := s.router.Route(ctx, signal)
	if err != nil {
		return SkillCanaryDueResult{}, err
	}
	return SkillCanaryDueResult{Due: true, MaximumAge: maximumAge, Route: routed}, nil
}

func SkillLifecycleSignalForCanary(activation core.SkillActivation, revision core.SkillRevision, windowStarted, dueAt time.Time, configuration SkillSignalConfiguration) (SkillLifecycleSignal, error) {
	if err := configuration.Validate(); err != nil {
		return SkillLifecycleSignal{}, err
	}
	if err := activation.Validate(); err != nil {
		return SkillLifecycleSignal{}, err
	}
	if err := revision.Validate(); err != nil {
		return SkillLifecycleSignal{}, err
	}
	if windowStarted.IsZero() || dueAt.IsZero() || dueAt.Before(windowStarted) || activation.CanaryRevisionID != revision.ID || activation.CanaryDigest != revision.BundleDigest {
		return SkillLifecycleSignal{}, errors.New("canary signal binding or time is invalid")
	}
	bucket := dueAt.Sub(windowStarted) / time.Minute
	canonical := strings.Join([]string{activation.ID, activation.Workspace, activation.Environment, activation.SkillID, revision.ID, revision.BundleDigest, strconv.FormatInt(activation.Generation, 10), activation.PolicyDecisionID, windowStarted.UTC().Format(time.RFC3339Nano), strconv.FormatInt(int64(bucket), 10)}, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return SkillLifecycleSignal{ID: activation.ID, Kind: SkillSignalCanary,
		Scope:   core.SkillOrchestratorScope{WorkspaceID: activation.Workspace, Environment: activation.Environment},
		SkillID: activation.SkillID, RevisionID: revision.ID, ReferenceID: activation.ID,
		EvidenceDigest: "sha256:" + hex.EncodeToString(digest[:]), Verified: true, Authorized: true,
		ConfigurationVersion: configuration.ConfigurationVersion, PolicyVersion: configuration.PolicyVersion,
		PolicyDigest: configuration.PolicyDigest, OccurredAt: dueAt}, nil
}

type SkillCanaryAnalysisStageRepository interface {
	skillCanaryAnalysisRepository
	GetSkillWorkflow(context.Context, core.SkillOrchestratorScope, string) (core.SkillWorkflow, error)
	GetSkillActivation(context.Context, string, string, string) (core.SkillActivation, error)
	GetSkillPolicyDecision(context.Context, string, string) (core.SkillPolicyDecision, error)
}

type SkillCanaryAnalysisAdapter struct {
	repository    SkillCanaryAnalysisStageRepository
	policy        skillPolicyDecider
	configuration SkillCanaryStageConfiguration
	now           func() time.Time
	downstream    SkillLessonSignalRouter
	due           *SkillCanaryDueScheduler
}

func (a *SkillCanaryAnalysisAdapter) WithDownstreamRouter(router SkillLessonSignalRouter) error {
	scheduler, err := NewSkillCanaryDueScheduler(router, a.configuration)
	if err != nil {
		return err
	}
	a.downstream, a.due = router, scheduler
	return nil
}

func NewSkillCanaryAnalysisAdapter(repository SkillCanaryAnalysisStageRepository, policy skillPolicyDecider, configuration SkillCanaryStageConfiguration, now func() time.Time) (*SkillCanaryAnalysisAdapter, error) {
	if repository == nil || policy == nil {
		return nil, errors.New("skill canary analysis adapter dependencies are required")
	}
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &SkillCanaryAnalysisAdapter{repository: repository, policy: policy, configuration: configuration, now: now}, nil
}

func (a *SkillCanaryAnalysisAdapter) Execute(ctx context.Context, job core.SkillJob) (SkillStageResult, error) {
	if a == nil || job.Stage != core.SkillStageAnalyzeCanary {
		return SkillStageResult{}, canaryStageError(core.SkillFailurePermanentValidation, "invalid_canary_analysis_job", errors.New("invalid canary analysis job"))
	}
	if !a.configuration.Enabled {
		return SkillStageResult{}, canaryStageError(core.SkillFailurePolicyBlock, "canary_policy_disabled", errors.New("canary policy is disabled"))
	}
	workflow, err := a.repository.GetSkillWorkflow(ctx, job.Scope, job.WorkflowID)
	if err != nil {
		return SkillStageResult{}, canaryStageError(core.SkillFailureDependencyUnavailable, "canary_analysis_workflow_unavailable", err)
	}
	activation, err := a.repository.GetSkillActivation(ctx, job.Scope.WorkspaceID, job.Scope.Environment, job.SkillID)
	if err != nil || activation.ID != workflow.OriginID {
		return SkillStageResult{}, canaryStageError(core.SkillFailureDependencyUnavailable, "canary_analysis_activation_unavailable", errors.Join(err, errors.New("activation origin mismatch")))
	}
	revision, err := a.repository.GetSkillRevision(ctx, job.Scope.WorkspaceID, activation.CanaryRevisionID)
	if err != nil {
		return SkillStageResult{}, canaryStageError(core.SkillFailureDependencyUnavailable, "canary_analysis_revision_unavailable", err)
	}
	windowStarted := activation.UpdatedAt
	now := a.now().UTC()
	signal, err := SkillLifecycleSignalForCanary(activation, revision, windowStarted, workflow.CreatedAt, a.configuration.Signal)
	if err != nil || digestSkillLifecycleSignal(signal, nil) != job.InputDigest || workflow.InputDigest != job.InputDigest || job.PolicyVersion != a.configuration.Signal.PolicyVersion {
		return SkillStageResult{}, canaryStageError(core.SkillFailurePermanentValidation, "canary_analysis_binding_mismatch", errors.Join(err, errors.New("canary analysis binding mismatch")))
	}
	aggregates, err := a.repository.ListVerifiedSkillExecutionAggregates(ctx, job.Scope.WorkspaceID, job.Scope.Environment, job.SkillID, windowStarted)
	if err != nil {
		return SkillStageResult{}, canaryStageError(core.SkillFailureDependencyUnavailable, "canary_evidence_unavailable", err)
	}
	candidate := findSkillAggregate(aggregates, revision.ID)
	baseline := findSkillAggregate(aggregates, activation.ActiveRevisionID)
	if candidate.VerifiedSamples == 0 || baseline.VerifiedSamples == 0 {
		return SkillStageResult{}, canaryStageError(core.SkillFailureInsufficientEvidence, "canary_evidence_ambiguous", errors.New("candidate and baseline verified evidence are required"))
	}
	prior, err := a.repository.GetSkillPolicyDecision(ctx, job.Scope.WorkspaceID, activation.PolicyDecisionID)
	if err != nil || len(prior.EvaluationRunIDs) != 2 {
		return SkillStageResult{}, canaryStageError(core.SkillFailureDependencyUnavailable, "canary_policy_evidence_unavailable", err)
	}
	decision, err := a.policy.Decide(ctx, SkillPolicyInput{DecisionID: job.ID + "-decision", Workspace: job.Scope.WorkspaceID,
		SkillID: job.SkillID, RevisionID: revision.ID, PolicyID: a.configuration.PolicyID,
		PolicyVersion: job.PolicyVersion, CandidateRunID: prior.EvaluationRunIDs[0], BaselineRunID: prior.EvaluationRunIDs[1],
		CanarySamples: int(candidate.VerifiedSamples), CanaryVerifiedSuccessRate: aggregateSuccessRate(candidate),
		BaselineCanaryVerifiedSuccessRate: aggregateSuccessRate(baseline), CanaryFailureRate: aggregateFailureRate(candidate),
		HarmfulFeedbackCount: int(candidate.HarmfulFeedback), EfficiencyImprovement: aggregateEfficiency(candidate, baseline),
		MaximumCanaryAgeExceeded: now.Sub(windowStarted) >= a.configuration.MaximumWindowAge})
	if err != nil {
		return SkillStageResult{}, canaryStageError(core.SkillFailureDependencyUnavailable, "canary_policy_analysis_failed", err)
	}
	if a.downstream != nil && decision.Decision == core.SkillDecisionPromote {
		signal, signalErr := SkillLifecycleSignalForPromotion(decision, a.configuration.Signal)
		if signalErr != nil {
			return SkillStageResult{}, canaryStageError(core.SkillFailurePermanentValidation, "promotion_signal_invalid", signalErr)
		}
		if _, routeErr := a.downstream.Route(ctx, signal); routeErr != nil {
			return SkillStageResult{}, canaryStageError(core.SkillFailureDependencyUnavailable, "promotion_signal_unavailable", routeErr)
		}
	} else if a.due != nil && decision.Decision == core.SkillDecisionCanary {
		if _, scheduleErr := a.due.Schedule(ctx, SkillCanaryDueRequest{Activation: activation, Revision: revision, WindowStarted: windowStarted, VerifiedSamples: candidate.VerifiedSamples, Now: now}); scheduleErr != nil {
			return SkillStageResult{}, canaryStageError(core.SkillFailureDependencyUnavailable, "canary_recheck_unavailable", scheduleErr)
		}
	}
	return SkillStageResult{ResultKind: core.SkillJobResultSucceeded, References: []core.SkillOrchestratorReference{{Kind: core.SkillReferencePolicyDecision, ID: decision.ID}}}, nil
}

func canaryStageError(class core.SkillJobFailureClass, code string, err error) error {
	return &SkillStageError{Failure: SkillStageFailure{Class: class, Code: code}, Err: err}
}
