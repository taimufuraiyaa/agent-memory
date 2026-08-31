package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillLessonSignalConfiguration struct {
	Environment          string
	ConfigurationVersion int64
	PolicyVersion        int64
	PolicyDigest         string
}

func (c SkillLessonSignalConfiguration) Validate() error {
	scope := core.SkillOrchestratorScope{WorkspaceID: "validation", Environment: c.Environment}
	if err := scope.Validate(); err != nil {
		return err
	}
	if c.ConfigurationVersion < 1 || c.PolicyVersion < 1 || !skillLifecycleSignalDigestPattern.MatchString(c.PolicyDigest) {
		return errors.New("skill lesson signal configuration is invalid")
	}
	return nil
}

type SkillLessonSignalRouter interface {
	Route(context.Context, SkillLifecycleSignal) (SkillSignalRouteResult, error)
}

func SkillLifecycleSignalForLesson(lesson core.SolutionToolLesson, configuration SkillLessonSignalConfiguration) (SkillLifecycleSignal, error) {
	if err := configuration.Validate(); err != nil {
		return SkillLifecycleSignal{}, err
	}
	if err := lesson.Validate(); err != nil {
		return SkillLifecycleSignal{}, err
	}
	if lesson.Validation != core.SolutionValidationVerified || lesson.SupersededBy != "" {
		return SkillLifecycleSignal{}, errors.New("skill lesson must be current and verified")
	}
	eventIDs := append([]string(nil), lesson.SourceEventIDs...)
	episodeIDs := append([]string(nil), lesson.SourceEpisodeIDs...)
	stepIDs := append([]string(nil), lesson.SourceStepIDs...)
	sort.Strings(eventIDs)
	sort.Strings(episodeIDs)
	sort.Strings(stepIDs)
	canonical := strings.Join([]string{lesson.ID, lesson.Workspace, lesson.ToolName, lesson.Capability,
		strconv.FormatInt(lesson.Version, 10), string(lesson.Validation), strconv.FormatFloat(lesson.Confidence, 'g', -1, 64),
		lesson.CreatedAt.UTC().Format(time.RFC3339Nano), strings.Join(eventIDs, ","), strings.Join(episodeIDs, ","), strings.Join(stepIDs, ",")}, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return SkillLifecycleSignal{
		ID: lesson.ID, Kind: SkillSignalLesson,
		Scope:       core.SkillOrchestratorScope{WorkspaceID: lesson.Workspace, Environment: configuration.Environment},
		ReferenceID: lesson.ID, EvidenceDigest: "sha256:" + hex.EncodeToString(digest[:]), Verified: true, Authorized: true,
		ConfigurationVersion: configuration.ConfigurationVersion, PolicyVersion: configuration.PolicyVersion,
		PolicyDigest: configuration.PolicyDigest, OccurredAt: lesson.CreatedAt,
	}, nil
}

type SkillDetectionAdapterRepository interface {
	SkillRecurrenceSchedulerRepository
	GetSkillWorkflow(context.Context, core.SkillOrchestratorScope, string) (core.SkillWorkflow, error)
	GetSolutionToolLesson(context.Context, string) (core.SolutionToolLesson, error)
}

type SkillDetectionAdapter struct {
	repository    SkillDetectionAdapterRepository
	scheduler     *SkillRecurrenceScheduler
	configuration SkillLessonSignalConfiguration
}

func NewSkillDetectionAdapter(repository SkillDetectionAdapterRepository, recurrencePolicy SkillRecurrencePolicy, configuration SkillLessonSignalConfiguration) (*SkillDetectionAdapter, error) {
	if repository == nil {
		return nil, errors.New("skill detection repository is required")
	}
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	return &SkillDetectionAdapter{repository: repository, scheduler: NewSkillRecurrenceScheduler(repository, recurrencePolicy), configuration: configuration}, nil
}

func (a *SkillDetectionAdapter) Execute(ctx context.Context, job core.SkillJob) (SkillStageResult, error) {
	if a == nil || a.repository == nil || job.Stage != core.SkillStageDetect {
		return SkillStageResult{}, &SkillStageError{Failure: SkillStageFailure{Class: core.SkillFailurePermanentValidation, Code: "invalid_detection_job"}, Err: errors.New("invalid detection job")}
	}
	workflow, err := a.repository.GetSkillWorkflow(ctx, job.Scope, job.WorkflowID)
	if err != nil {
		return SkillStageResult{}, &SkillStageError{Failure: SkillStageFailure{Class: core.SkillFailureDependencyUnavailable, Code: "workflow_unavailable"}, Err: err}
	}
	if workflow.OriginKind != core.SkillWorkflowOriginLifecycleSignal || workflow.OriginID == "" {
		return SkillStageResult{}, &SkillStageError{Failure: SkillStageFailure{Class: core.SkillFailurePermanentValidation, Code: "invalid_lesson_origin"}, Err: errors.New("invalid lesson workflow origin")}
	}
	lesson, err := a.repository.GetSolutionToolLesson(ctx, workflow.OriginID)
	if err != nil {
		return SkillStageResult{}, &SkillStageError{Failure: SkillStageFailure{Class: core.SkillFailureDependencyUnavailable, Code: "lesson_unavailable"}, Err: err}
	}
	signal, err := SkillLifecycleSignalForLesson(lesson, a.configuration)
	if err != nil {
		return SkillStageResult{}, &SkillStageError{Failure: SkillStageFailure{Class: core.SkillFailureSafetyRejection, Code: "lesson_ineligible"}, Err: err}
	}
	expectedDigest := digestSkillLifecycleSignal(signal, nil)
	if workflow.InputDigest != expectedDigest || job.InputDigest != expectedDigest || job.PolicyVersion != a.configuration.PolicyVersion || workflow.ConfigurationVersion != a.configuration.ConfigurationVersion || workflow.PolicyDigest != a.configuration.PolicyDigest {
		return SkillStageResult{}, &SkillStageError{Failure: SkillStageFailure{Class: core.SkillFailurePermanentValidation, Code: "lesson_digest_mismatch"}, Err: errors.New("lesson binding mismatch")}
	}
	if len(lesson.SourceEpisodeIDs) == 0 {
		return SkillStageResult{}, &SkillStageError{Failure: SkillStageFailure{Class: core.SkillFailurePermanentValidation, Code: "lesson_missing_episode"}, Err: errors.New("lesson source episode missing")}
	}
	episode, err := a.repository.GetSolutionEpisode(ctx, lesson.SourceEpisodeIDs[0])
	if err != nil || episode.Workspace != lesson.Workspace {
		return SkillStageResult{}, &SkillStageError{Failure: SkillStageFailure{Class: core.SkillFailureSafetyRejection, Code: "lesson_source_unauthorized"}, Err: errors.New("lesson source is not authorized")}
	}
	result, err := a.scheduler.Run(ctx, SkillRecurrenceInput{Workspace: lesson.Workspace, PrincipalID: episode.PrincipalID, CreatedBy: "skill-detection-worker"})
	if err != nil {
		return SkillStageResult{}, &SkillStageError{Failure: SkillStageFailure{Class: core.SkillFailureDependencyUnavailable, Code: "recurrence_detection_failed"}, Err: err}
	}
	references := make([]core.SkillOrchestratorReference, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		references = append(references, core.SkillOrchestratorReference{Kind: core.SkillReferenceCandidate, ID: candidate.ID})
	}
	return SkillStageResult{ResultKind: core.SkillJobResultSucceeded, References: references}, nil
}

type SkillLessonParityRepository interface {
	ListCurrentVerifiedSolutionToolLessonsAfter(context.Context, string, string, int) ([]core.SolutionToolLesson, error)
}

type SkillLessonParitySweep struct {
	repository    SkillLessonParityRepository
	router        SkillLessonSignalRouter
	configuration SkillLessonSignalConfiguration
}

func NewSkillLessonParitySweep(repository SkillLessonParityRepository, router SkillLessonSignalRouter, configuration SkillLessonSignalConfiguration) (*SkillLessonParitySweep, error) {
	if repository == nil || router == nil {
		return nil, errors.New("skill lesson parity repository and router are required")
	}
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	return &SkillLessonParitySweep{repository: repository, router: router, configuration: configuration}, nil
}

func (s *SkillLessonParitySweep) Sweep(ctx context.Context, request SkillReconciliationRequest) (SkillReconciliationSweepResult, error) {
	if request.Domain != core.SkillReconcileLifecycleJobParity || request.Limit < 1 {
		return SkillReconciliationSweepResult{}, errors.New("invalid skill lesson parity request")
	}
	lessons, err := s.repository.ListCurrentVerifiedSolutionToolLessonsAfter(ctx, request.Scope.WorkspaceID, request.Cursor, request.Limit)
	if err != nil {
		return SkillReconciliationSweepResult{}, err
	}
	result := SkillReconciliationSweepResult{Complete: len(lessons) < request.Limit}
	for _, lesson := range lessons {
		result.Counters.Scanned++
		signal, err := SkillLifecycleSignalForLesson(lesson, s.configuration)
		if err != nil {
			result.Counters.Blocked++
			continue
		}
		routed, err := s.router.Route(ctx, signal)
		if err != nil {
			result.Counters.Failed++
			continue
		}
		if routed.Created {
			result.Counters.Repaired++
		} else {
			result.Counters.Skipped++
		}
		result.NextCursor = lesson.ID
	}
	if result.Complete {
		result.NextCursor = ""
	}
	return result, nil
}
