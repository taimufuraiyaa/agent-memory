package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var (
	ErrSkillEvaluatorUnavailable    = errors.New("skill evaluator unavailable")
	ErrSkillEvaluationSandboxDenied = errors.New("skill evaluation sandbox denied")
)

type SkillEvaluationInput struct {
	ID                     string        `json:"id"`
	Workspace              string        `json:"workspace"`
	SkillID                string        `json:"skill_id"`
	CandidateRevisionID    string        `json:"candidate_revision_id"`
	BaselineRevisionID     string        `json:"baseline_revision_id"`
	SuiteID                string        `json:"suite_id"`
	SuiteVersion           int64         `json:"suite_version"`
	SuiteDigest            string        `json:"suite_digest"`
	Evaluator              string        `json:"evaluator"`
	EvaluatorVersion       string        `json:"evaluator_version"`
	EnvironmentFingerprint string        `json:"environment_fingerprint"`
	Timeout                time.Duration `json:"timeout"`
	MaximumCases           int           `json:"maximum_cases"`
}

type RestrictedSkillEvaluationRequest struct {
	Revision               core.SkillRevision        `json:"revision"`
	Suite                  core.SkillEvaluationSuite `json:"suite"`
	EnvironmentFingerprint string                    `json:"environment_fingerprint"`
	MaximumCases           int                       `json:"maximum_cases"`
}

type SkillEvaluationResult struct {
	Candidate core.SkillEvaluationRun `json:"candidate"`
	Baseline  core.SkillEvaluationRun `json:"baseline"`
}

type skillEvaluationRepositoryContract interface {
	GetSkillEvaluationSuite(context.Context, string, string, int64) (core.SkillEvaluationSuite, error)
	GetSkillRevision(context.Context, string, string) (core.SkillRevision, error)
	CreateSkillEvaluationRun(context.Context, core.SkillEvaluationRun) error
}

type RestrictedSkillEvaluationRunner interface {
	Run(context.Context, RestrictedSkillEvaluationRequest) ([]core.SkillEvaluationCaseResult, error)
}

type SkillEvaluationOrchestrator struct {
	repository skillEvaluationRepositoryContract
	runner     RestrictedSkillEvaluationRunner
	now        func() time.Time
}

func NewSkillEvaluationOrchestrator(repository skillEvaluationRepositoryContract, runner RestrictedSkillEvaluationRunner, now func() time.Time) *SkillEvaluationOrchestrator {
	if now == nil {
		now = time.Now
	}
	return &SkillEvaluationOrchestrator{repository: repository, runner: runner, now: now}
}

func (o *SkillEvaluationOrchestrator) Evaluate(ctx context.Context, input SkillEvaluationInput) (SkillEvaluationResult, error) {
	if o == nil || o.repository == nil || o.runner == nil {
		return SkillEvaluationResult{}, errors.New("skill evaluation dependencies are required")
	}
	if err := validateSkillEvaluationInput(input); err != nil {
		return SkillEvaluationResult{}, err
	}
	suite, err := o.repository.GetSkillEvaluationSuite(ctx, input.Workspace, input.SuiteID, input.SuiteVersion)
	if err != nil {
		return SkillEvaluationResult{}, err
	}
	if suite.SkillID != input.SkillID || suite.Digest != input.SuiteDigest || len(suite.Cases) > input.MaximumCases {
		return SkillEvaluationResult{}, errors.New("skill evaluation suite is stale or exceeds execution bound")
	}
	candidate, err := o.repository.GetSkillRevision(ctx, input.Workspace, input.CandidateRevisionID)
	if err != nil {
		return SkillEvaluationResult{}, err
	}
	baseline, err := o.repository.GetSkillRevision(ctx, input.Workspace, input.BaselineRevisionID)
	if err != nil {
		return SkillEvaluationResult{}, err
	}
	if candidate.SkillID != input.SkillID || baseline.SkillID != input.SkillID || candidate.ID == baseline.ID {
		return SkillEvaluationResult{}, errors.New("skill evaluation revision binding is invalid")
	}
	candidateRun := o.runRevision(ctx, input.ID+"-candidate", input, suite, candidate, baseline)
	baselineRun := o.runRevision(ctx, input.ID+"-baseline", input, suite, baseline, core.SkillRevision{})
	if err := o.repository.CreateSkillEvaluationRun(ctx, candidateRun); err != nil {
		return SkillEvaluationResult{}, err
	}
	if err := o.repository.CreateSkillEvaluationRun(ctx, baselineRun); err != nil {
		return SkillEvaluationResult{}, err
	}
	return SkillEvaluationResult{Candidate: candidateRun, Baseline: baselineRun}, nil
}

func (o *SkillEvaluationOrchestrator) runRevision(ctx context.Context, id string, input SkillEvaluationInput, suite core.SkillEvaluationSuite, revision, comparison core.SkillRevision) core.SkillEvaluationRun {
	started := o.now().UTC()
	runCtx, cancel := context.WithTimeout(ctx, input.Timeout)
	results, runErr := o.runner.Run(runCtx, RestrictedSkillEvaluationRequest{Revision: revision, Suite: suite, EnvironmentFingerprint: input.EnvironmentFingerprint, MaximumCases: input.MaximumCases})
	cancel()
	if runErr != nil {
		failure := classifySkillEvaluationFailure(runErr)
		results = make([]core.SkillEvaluationCaseResult, 0, len(suite.Cases))
		for _, item := range suite.Cases {
			results = append(results, core.SkillEvaluationCaseResult{CaseID: item.ID, FailureClass: failure})
		}
	}
	verdict := skillEvaluationVerdict(suite, results, runErr)
	completed := o.now().UTC()
	if completed.Before(started) {
		completed = started
	}
	run := core.SkillEvaluationRun{
		ID: id, Workspace: input.Workspace, SkillID: input.SkillID, RevisionID: revision.ID, RevisionDigest: revision.BundleDigest,
		SuiteID: suite.ID, SuiteVersion: suite.Version, SuiteDigest: suite.Digest, Evaluator: input.Evaluator,
		EvaluatorVersion: input.EvaluatorVersion, EnvironmentFingerprint: input.EnvironmentFingerprint,
		Verdict: verdict, CaseResults: results, StartedAt: started, CompletedAt: completed,
	}
	if comparison.ID != "" {
		run.BaselineRevisionID, run.BaselineDigest = comparison.ID, comparison.BundleDigest
	}
	return run
}

func skillEvaluationVerdict(suite core.SkillEvaluationSuite, results []core.SkillEvaluationCaseResult, runErr error) core.SkillEvaluationVerdict {
	if runErr != nil || len(results) != len(suite.Cases) {
		return core.SkillEvaluationInconclusive
	}
	byID := make(map[string]core.SkillEvaluationCaseResult, len(results))
	for _, result := range results {
		if _, exists := byID[result.CaseID]; exists {
			return core.SkillEvaluationInconclusive
		}
		byID[result.CaseID] = result
	}
	for _, item := range suite.Cases {
		result, exists := byID[item.ID]
		if !exists {
			return core.SkillEvaluationInconclusive
		}
		if item.Required && !result.Passed {
			return core.SkillEvaluationFail
		}
		if item.Required && !result.IndependentlyVerified {
			return core.SkillEvaluationInconclusive
		}
	}
	return core.SkillEvaluationPass
}

func classifySkillEvaluationFailure(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrSkillEvaluationSandboxDenied):
		return "sandbox_denied"
	case errors.Is(err, ErrSkillEvaluatorUnavailable):
		return "evaluator_unavailable"
	default:
		return "runner_failure"
	}
}

func validateSkillEvaluationInput(input SkillEvaluationInput) error {
	for field, value := range map[string]string{"id": input.ID, "workspace": input.Workspace, "skill_id": input.SkillID, "candidate_revision_id": input.CandidateRevisionID, "baseline_revision_id": input.BaselineRevisionID, "suite_id": input.SuiteID, "suite_digest": input.SuiteDigest, "evaluator": input.Evaluator, "evaluator_version": input.EvaluatorVersion, "environment_fingerprint": input.EnvironmentFingerprint} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return fmt.Errorf("skill evaluation %s is required and bounded", field)
		}
	}
	if input.SuiteVersion < 1 || input.Timeout <= 0 || input.Timeout > 10*time.Minute || input.MaximumCases < 1 || input.MaximumCases > core.MaxSkillListItems {
		return errors.New("skill evaluation version or resource bounds are invalid")
	}
	return nil
}
