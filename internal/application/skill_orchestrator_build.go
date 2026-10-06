package application

import (
	"context"
	"errors"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func SkillLifecycleSignalForCandidate(candidate core.SkillCandidate, configuration SkillSignalConfiguration) (SkillLifecycleSignal, error) {
	if err := configuration.Validate(); err != nil {
		return SkillLifecycleSignal{}, err
	}
	if err := candidate.Validate(); err != nil {
		return SkillLifecycleSignal{}, err
	}
	if candidate.State != core.SkillCandidateProposed && candidate.State != core.SkillCandidateAccepted {
		return SkillLifecycleSignal{}, errors.New("skill candidate is not buildable")
	}
	skillID := ""
	if len(candidate.TargetSkillIDs) == 1 {
		skillID = candidate.TargetSkillIDs[0]
	}
	return SkillLifecycleSignal{
		ID: candidate.ID, Kind: SkillSignalCandidate,
		Scope:   core.SkillOrchestratorScope{WorkspaceID: candidate.Workspace, Environment: configuration.Environment},
		SkillID: skillID, ReferenceID: candidate.ID, EvidenceDigest: candidate.DeduplicationHash,
		Verified: true, Authorized: true, ConfigurationVersion: configuration.ConfigurationVersion,
		PolicyVersion: configuration.PolicyVersion, PolicyDigest: configuration.PolicyDigest, OccurredAt: candidate.UpdatedAt,
	}, nil
}

type SkillDraftAuthorRequest struct {
	Candidate    core.SkillCandidate
	MaximumFiles int
	MaximumBytes int
	MaximumText  int
	RequiredRoot string
	Contract     string
}

type SkillDraftAuthorResult struct {
	SkillName         string
	Description       string
	OwnerGroup        string
	ProposedFiles     map[string][]byte
	RemovalReasons    map[string]string
	Compatibility     core.SkillCompatibility
	ProtectedSections []string
}

type SkillDraftAuthor interface {
	Author(context.Context, SkillDraftAuthorRequest) (SkillDraftAuthorResult, error)
}

type SkillRevisionBuildAdapterRepository interface {
	SkillRevisionBuilderRepository
	GetSkillWorkflow(context.Context, core.SkillOrchestratorScope, string) (core.SkillWorkflow, error)
}

type SkillRevisionBuildAdapter struct {
	repository     SkillRevisionBuildAdapterRepository
	builder        *SkillRevisionBuilder
	author         SkillDraftAuthor
	configuration  SkillSignalConfiguration
	registeredRoot string
	downstream     SkillLessonSignalRouter
}

func (a *SkillRevisionBuildAdapter) WithDownstreamRouter(router SkillLessonSignalRouter) *SkillRevisionBuildAdapter {
	a.downstream = router
	return a
}

func NewSkillRevisionBuildAdapter(repository SkillRevisionBuildAdapterRepository, bundles SkillRevisionBundleStore, author SkillDraftAuthor, configuration SkillSignalConfiguration, registeredRoot string) (*SkillRevisionBuildAdapter, error) {
	if repository == nil || bundles == nil || author == nil || strings.TrimSpace(registeredRoot) == "" {
		return nil, errors.New("skill revision build adapter dependencies and registered root are required")
	}
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	return &SkillRevisionBuildAdapter{repository: repository, builder: NewSkillRevisionBuilder(repository, bundles), author: author, configuration: configuration, registeredRoot: registeredRoot}, nil
}

func (a *SkillRevisionBuildAdapter) Execute(ctx context.Context, job core.SkillJob) (SkillStageResult, error) {
	if a == nil || a.repository == nil || job.Stage != core.SkillStageBuild {
		return SkillStageResult{}, skillBuildStageError(core.SkillFailurePermanentValidation, "invalid_build_job", errors.New("invalid build job"))
	}
	workflow, err := a.repository.GetSkillWorkflow(ctx, job.Scope, job.WorkflowID)
	if err != nil {
		return SkillStageResult{}, skillBuildStageError(core.SkillFailureDependencyUnavailable, "build_workflow_unavailable", err)
	}
	if workflow.OriginKind != core.SkillWorkflowOriginLifecycleSignal || workflow.OriginID == "" {
		return SkillStageResult{}, skillBuildStageError(core.SkillFailurePermanentValidation, "invalid_candidate_origin", errors.New("invalid candidate workflow origin"))
	}
	candidate, err := a.repository.GetSkillCandidate(ctx, job.Scope.WorkspaceID, workflow.OriginID)
	if err != nil {
		return SkillStageResult{}, skillBuildStageError(core.SkillFailurePermanentValidation, "candidate_unavailable", err)
	}
	signal, err := SkillLifecycleSignalForCandidate(candidate, a.configuration)
	if err != nil {
		return SkillStageResult{}, skillBuildStageError(core.SkillFailureSafetyRejection, "candidate_ineligible", err)
	}
	expectedDigest := digestSkillLifecycleSignal(signal, nil)
	if job.InputDigest != expectedDigest || workflow.InputDigest != expectedDigest || job.PolicyVersion != a.configuration.PolicyVersion || workflow.ConfigurationVersion != a.configuration.ConfigurationVersion || workflow.PolicyDigest != a.configuration.PolicyDigest {
		return SkillStageResult{}, skillBuildStageError(core.SkillFailurePermanentValidation, "candidate_digest_mismatch", errors.New("candidate binding mismatch"))
	}
	authored, err := a.author.Author(ctx, SkillDraftAuthorRequest{Candidate: candidate, MaximumFiles: core.MaxSkillBundleFiles,
		MaximumBytes: maxSkillDraftTotalBytes, MaximumText: maxSkillDraftTextBytes, RequiredRoot: a.registeredRoot,
		Contract: core.SkillOrchestratorContractVersion})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return SkillStageResult{}, skillBuildStageError(core.SkillFailureCancellation, "draft_author_cancelled", err)
		}
		return SkillStageResult{}, skillBuildStageError(core.SkillFailureDependencyUnavailable, "draft_author_unavailable", err)
	}
	result, err := a.builder.Build(ctx, SkillRevisionBuildInput{Workspace: candidate.Workspace, CandidateID: candidate.ID,
		SkillName: authored.SkillName, Description: authored.Description, OwnerGroup: authored.OwnerGroup,
		CreatedBy: "skill-build-worker", ProposedFiles: authored.ProposedFiles, RemovalReasons: authored.RemovalReasons,
		Compatibility: authored.Compatibility, ProtectedSections: authored.ProtectedSections})
	if err != nil {
		if errors.Is(err, ErrSkillBundleUnavailable) {
			return SkillStageResult{}, skillBuildStageError(core.SkillFailureDependencyUnavailable, "bundle_unavailable", err)
		}
		class, code := core.SkillFailurePermanentValidation, "draft_build_rejected"
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "deleted evidence") || strings.Contains(message, "prompt_injection") || strings.Contains(message, "protected section") || strings.Contains(message, "unsafe") {
			class, code = core.SkillFailureSafetyRejection, "draft_safety_rejected"
		}
		return SkillStageResult{}, skillBuildStageError(class, code, err)
	}
	if a.downstream != nil {
		next, signalErr := SkillLifecycleSignalForRevision(result.Revision, a.configuration)
		if signalErr != nil {
			return SkillStageResult{}, skillBuildStageError(core.SkillFailurePermanentValidation, "revision_signal_invalid", signalErr)
		}
		if _, routeErr := a.downstream.Route(ctx, next); routeErr != nil {
			return SkillStageResult{}, skillBuildStageError(core.SkillFailureDependencyUnavailable, "revision_signal_unavailable", routeErr)
		}
	}
	return SkillStageResult{ResultKind: core.SkillJobResultSucceeded, References: []core.SkillOrchestratorReference{{Kind: core.SkillReferenceRevision, ID: result.Revision.ID}}}, nil
}

func skillBuildStageError(class core.SkillJobFailureClass, code string, err error) error {
	return &SkillStageError{Failure: SkillStageFailure{Class: class, Code: code}, Err: err}
}

type SkillCandidateParityRepository interface {
	ListBuildableSkillCandidatesAfter(context.Context, string, string, int) ([]core.SkillCandidate, error)
}

type SkillCandidateParitySweep struct {
	repository    SkillCandidateParityRepository
	router        SkillLessonSignalRouter
	configuration SkillSignalConfiguration
}

type SkillLifecycleParitySweep struct {
	lessons    *SkillLessonParitySweep
	candidates *SkillCandidateParitySweep
}

func NewSkillLifecycleParitySweep(lessonRepository SkillLessonParityRepository, candidateRepository SkillCandidateParityRepository, router SkillLessonSignalRouter, configuration SkillSignalConfiguration) (*SkillLifecycleParitySweep, error) {
	lessons, err := NewSkillLessonParitySweep(lessonRepository, router, configuration)
	if err != nil {
		return nil, err
	}
	candidates, err := NewSkillCandidateParitySweep(candidateRepository, router, configuration)
	if err != nil {
		return nil, err
	}
	return &SkillLifecycleParitySweep{lessons: lessons, candidates: candidates}, nil
}

func (s *SkillLifecycleParitySweep) Sweep(ctx context.Context, request SkillReconciliationRequest) (SkillReconciliationSweepResult, error) {
	if s == nil || request.Domain != core.SkillReconcileLifecycleJobParity || request.Limit < 1 {
		return SkillReconciliationSweepResult{}, errors.New("invalid lifecycle parity request")
	}
	phase, cursor := "lessons", request.Cursor
	if strings.HasPrefix(cursor, "lessons:") {
		cursor = strings.TrimPrefix(cursor, "lessons:")
	} else if strings.HasPrefix(cursor, "candidates:") {
		phase, cursor = "candidates", strings.TrimPrefix(cursor, "candidates:")
	} else if cursor != "" {
		return SkillReconciliationSweepResult{}, errors.New("invalid lifecycle parity cursor")
	}
	combined := SkillReconciliationSweepResult{}
	remaining := request.Limit
	if phase == "lessons" {
		lessonRequest := request
		lessonRequest.Cursor, lessonRequest.Limit = cursor, remaining
		lessonResult, err := s.lessons.Sweep(ctx, lessonRequest)
		if err != nil {
			return SkillReconciliationSweepResult{}, err
		}
		combined.Counters = addSkillReconciliationCounters(combined.Counters, lessonResult.Counters)
		remaining -= int(lessonResult.Counters.Scanned)
		if !lessonResult.Complete {
			combined.NextCursor = "lessons:" + lessonResult.NextCursor
			return combined, nil
		}
		phase, cursor = "candidates", ""
	}
	if phase == "candidates" {
		if remaining < 1 {
			combined.NextCursor = "candidates:" + cursor
			return combined, nil
		}
		candidateRequest := request
		candidateRequest.Cursor, candidateRequest.Limit = cursor, remaining
		candidateResult, err := s.candidates.Sweep(ctx, candidateRequest)
		if err != nil {
			return SkillReconciliationSweepResult{}, err
		}
		combined.Counters = addSkillReconciliationCounters(combined.Counters, candidateResult.Counters)
		if candidateResult.Complete {
			combined.Complete = true
			return combined, nil
		}
		combined.NextCursor = "candidates:" + candidateResult.NextCursor
	}
	return combined, nil
}

func addSkillReconciliationCounters(left, right core.SkillReconciliationCounters) core.SkillReconciliationCounters {
	left.Scanned += right.Scanned
	left.Repaired += right.Repaired
	left.Skipped += right.Skipped
	left.Blocked += right.Blocked
	left.Failed += right.Failed
	return left
}

func NewSkillCandidateParitySweep(repository SkillCandidateParityRepository, router SkillLessonSignalRouter, configuration SkillSignalConfiguration) (*SkillCandidateParitySweep, error) {
	if repository == nil || router == nil {
		return nil, errors.New("skill candidate parity dependencies are required")
	}
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	return &SkillCandidateParitySweep{repository: repository, router: router, configuration: configuration}, nil
}

func (s *SkillCandidateParitySweep) Sweep(ctx context.Context, request SkillReconciliationRequest) (SkillReconciliationSweepResult, error) {
	if request.Domain != core.SkillReconcileLifecycleJobParity || request.Limit < 1 {
		return SkillReconciliationSweepResult{}, errors.New("invalid skill candidate parity request")
	}
	candidates, err := s.repository.ListBuildableSkillCandidatesAfter(ctx, request.Scope.WorkspaceID, request.Cursor, request.Limit)
	if err != nil {
		return SkillReconciliationSweepResult{}, err
	}
	result := SkillReconciliationSweepResult{Complete: len(candidates) < request.Limit}
	for _, candidate := range candidates {
		result.Counters.Scanned++
		signal, signalErr := SkillLifecycleSignalForCandidate(candidate, s.configuration)
		if signalErr != nil {
			result.Counters.Blocked++
			continue
		}
		routed, routeErr := s.router.Route(ctx, signal)
		if routeErr != nil {
			result.Counters.Failed++
			continue
		}
		if routed.Created {
			result.Counters.Repaired++
		} else {
			result.Counters.Skipped++
		}
		result.NextCursor = candidate.ID
	}
	if result.Complete {
		result.NextCursor = ""
	}
	return result, nil
}
