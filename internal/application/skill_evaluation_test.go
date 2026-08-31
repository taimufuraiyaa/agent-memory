package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillEvaluationOrchestratorRunsCandidateAndBaselineComparably(t *testing.T) {
	fixture := newSkillEvaluationFixture()
	result, err := fixture.orchestrator.Evaluate(context.Background(), fixture.input())
	if err != nil {
		t.Fatal(err)
	}
	if result.Candidate.Verdict != core.SkillEvaluationPass || result.Baseline.Verdict != core.SkillEvaluationPass {
		t.Fatalf("evaluation result = %+v", result)
	}
	if len(fixture.runner.requests) != 2 || fixture.runner.requests[0].Suite.Digest != fixture.runner.requests[1].Suite.Digest || fixture.runner.requests[0].EnvironmentFingerprint != fixture.runner.requests[1].EnvironmentFingerprint {
		t.Fatalf("runner requests are not comparable: %+v", fixture.runner.requests)
	}
	if len(fixture.repository.runs) != 2 {
		t.Fatal("candidate and baseline runs were not persisted")
	}
}

func TestSkillEvaluationOrchestratorClassifiesRestrictedRunnerFailures(t *testing.T) {
	tests := []struct {
		name        string
		runnerError error
		wantFailure string
	}{
		{name: "evaluator outage", runnerError: ErrSkillEvaluatorUnavailable, wantFailure: "evaluator_unavailable"},
		{name: "sandbox denial", runnerError: ErrSkillEvaluationSandboxDenied, wantFailure: "sandbox_denied"},
		{name: "timeout", runnerError: context.DeadlineExceeded, wantFailure: "timeout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSkillEvaluationFixture()
			fixture.runner.err = test.runnerError
			result, err := fixture.orchestrator.Evaluate(context.Background(), fixture.input())
			if err != nil {
				t.Fatal(err)
			}
			if result.Candidate.Verdict != core.SkillEvaluationInconclusive || result.Candidate.CaseResults[0].FailureClass != test.wantFailure {
				t.Fatalf("classified result = %+v", result.Candidate)
			}
		})
	}
}

func TestSkillEvaluationOrchestratorRejectsStaleSuite(t *testing.T) {
	fixture := newSkillEvaluationFixture()
	input := fixture.input()
	input.SuiteDigest = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if _, err := fixture.orchestrator.Evaluate(context.Background(), input); err == nil {
		t.Fatal("stale suite digest was accepted")
	}
	if len(fixture.runner.requests) != 0 || len(fixture.repository.runs) != 0 {
		t.Fatal("stale suite reached execution or persistence")
	}
}

func TestSkillEvaluationOrchestratorTreatsPartialOrUnverifiedResultsAsInconclusive(t *testing.T) {
	fixture := newSkillEvaluationFixture()
	fixture.runner.results = []core.SkillEvaluationCaseResult{{CaseID: "positive", Passed: true, IndependentlyVerified: false}}
	result, err := fixture.orchestrator.Evaluate(context.Background(), fixture.input())
	if err != nil {
		t.Fatal(err)
	}
	if result.Candidate.Verdict != core.SkillEvaluationInconclusive {
		t.Fatalf("partial result verdict = %s", result.Candidate.Verdict)
	}
}

type skillEvaluationRepository struct {
	suite     core.SkillEvaluationSuite
	revisions map[string]core.SkillRevision
	runs      []core.SkillEvaluationRun
}

func (r *skillEvaluationRepository) GetSkillEvaluationSuite(_ context.Context, workspace, suiteID string, version int64) (core.SkillEvaluationSuite, error) {
	if workspace != r.suite.Workspace || suiteID != r.suite.ID || version != r.suite.Version {
		return core.SkillEvaluationSuite{}, errors.New("suite not found")
	}
	return r.suite, nil
}

func (r *skillEvaluationRepository) GetSkillRevision(_ context.Context, workspace, revisionID string) (core.SkillRevision, error) {
	revision, exists := r.revisions[revisionID]
	if !exists || revision.Workspace != workspace {
		return core.SkillRevision{}, errors.New("revision not found")
	}
	return revision, nil
}

func (r *skillEvaluationRepository) CreateSkillEvaluationRuns(_ context.Context, candidate, baseline core.SkillEvaluationRun) error {
	r.runs = append(r.runs, candidate, baseline)
	return nil
}

type skillEvaluationRunner struct {
	requests []RestrictedSkillEvaluationRequest
	results  []core.SkillEvaluationCaseResult
	err      error
}

func (r *skillEvaluationRunner) Run(_ context.Context, request RestrictedSkillEvaluationRequest) ([]core.SkillEvaluationCaseResult, error) {
	r.requests = append(r.requests, request)
	if r.err != nil {
		return nil, r.err
	}
	if r.results != nil {
		return append([]core.SkillEvaluationCaseResult(nil), r.results...), nil
	}
	results := make([]core.SkillEvaluationCaseResult, 0, len(request.Suite.Cases))
	for _, item := range request.Suite.Cases {
		results = append(results, core.SkillEvaluationCaseResult{CaseID: item.ID, Passed: true, IndependentlyVerified: true, DurationMS: 10})
	}
	return results, nil
}

type skillEvaluationFixture struct {
	repository   *skillEvaluationRepository
	runner       *skillEvaluationRunner
	orchestrator *SkillEvaluationOrchestrator
	now          time.Time
}

func newSkillEvaluationFixture() skillEvaluationFixture {
	now := time.Date(2026, 8, 29, 19, 0, 0, 0, time.UTC)
	suite := core.SkillEvaluationSuite{ID: "suite-1", SkillID: "skill-1", Workspace: "ws", Version: 1, Digest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", Cases: []core.SkillEvaluationCase{{ID: "positive", Kind: core.SkillCasePositive, Summary: "Positive case.", Reference: "fixture:positive", Required: true}, {ID: "safety", Kind: core.SkillCaseSafety, Summary: "Safety case.", Reference: "fixture:safety", Required: true}}, CreatedBy: "reviewer", CreatedAt: now}
	revisions := map[string]core.SkillRevision{
		"revision-2": resolverRevision("revision-2", 2, core.SkillRevisionTesting, core.LogicalSkill{ID: "skill-1", Workspace: "ws"}, now),
		"revision-1": resolverRevision("revision-1", 1, core.SkillRevisionActive, core.LogicalSkill{ID: "skill-1", Workspace: "ws"}, now),
	}
	repository := &skillEvaluationRepository{suite: suite, revisions: revisions}
	runner := &skillEvaluationRunner{}
	orchestrator := NewSkillEvaluationOrchestrator(repository, runner, func() time.Time { return now })
	return skillEvaluationFixture{repository: repository, runner: runner, orchestrator: orchestrator, now: now}
}

func (f skillEvaluationFixture) input() SkillEvaluationInput {
	return SkillEvaluationInput{ID: "evaluation", Workspace: "ws", SkillID: "skill-1", CandidateRevisionID: "revision-2", BaselineRevisionID: "revision-1", SuiteID: f.repository.suite.ID, SuiteVersion: f.repository.suite.Version, SuiteDigest: f.repository.suite.Digest, Evaluator: "restricted-runner", EvaluatorVersion: "v1", EnvironmentFingerprint: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Timeout: time.Second, MaximumCases: 20}
}
