package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillSignalRouterMapsVerifiedLifecycleSignalsToStableJobs(t *testing.T) {
	now := time.Date(2026, 8, 31, 17, 0, 0, 0, time.UTC)
	tests := []struct {
		kind  SkillLifecycleSignalKind
		stage core.SkillOrchestratorStage
	}{
		{SkillSignalLesson, core.SkillStageDetect},
		{SkillSignalCandidate, core.SkillStageBuild},
		{SkillSignalRevision, core.SkillStageEvaluate},
		{SkillSignalEvaluation, core.SkillStageDecide},
		{SkillSignalDecision, core.SkillStageStartCanary},
		{SkillSignalCanary, core.SkillStageAnalyzeCanary},
		{SkillSignalPromotion, core.SkillStageActivate},
		{SkillSignalExecution, core.SkillStageAnalyzeCanary},
		{SkillSignalSafety, core.SkillStageRollback},
	}
	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			repository := &skillSignalRouteRepository{}
			router := NewSkillSignalRouter(repository)
			signal := validSkillLifecycleSignal(now, test.kind)

			first, err := router.Route(context.Background(), signal)
			if err != nil || first.Ignored || first.Job.Stage != test.stage {
				t.Fatalf("route=%+v err=%v", first, err)
			}
			second, err := router.Route(context.Background(), signal)
			if err != nil || second.Workflow.ID != first.Workflow.ID || second.Job.ID != first.Job.ID {
				t.Fatalf("duplicate route=%+v err=%v", second, err)
			}
			if repository.executed {
				t.Fatal("request-path router executed a long-running stage")
			}
		})
	}
}

func TestSkillSignalRouterBindsDependenciesAndSafetyPriority(t *testing.T) {
	now := time.Now().UTC()
	repository := &skillSignalRouteRepository{}
	router := NewSkillSignalRouter(repository)
	signal := validSkillLifecycleSignal(now, SkillSignalSafety)
	signal.ParentJobIDs = []string{"parent-job-1", "parent-job-2"}

	result, err := router.Route(context.Background(), signal)
	if err != nil {
		t.Fatal(err)
	}
	if result.Job.Priority != SkillOrchestratorSafetyPriority || len(result.Dependencies) != 2 {
		t.Fatalf("unexpected safety route %+v", result)
	}
	for _, dependency := range result.Dependencies {
		if dependency.JobID != result.Job.ID || len(dependency.AcceptedResultKinds) != 1 || dependency.AcceptedResultKinds[0] != core.SkillJobResultSucceeded {
			t.Fatalf("invalid dependency %+v", dependency)
		}
	}
}

func TestSkillSignalRouterFailsClosedForUnauthorizedTombstonedAndInvalidSignals(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name   string
		mutate func(*SkillLifecycleSignal)
		ignore bool
	}{
		{name: "unauthorized", mutate: func(s *SkillLifecycleSignal) { s.Authorized = false }},
		{name: "unverified", mutate: func(s *SkillLifecycleSignal) { s.Verified = false }},
		{name: "invalid digest", mutate: func(s *SkillLifecycleSignal) { s.EvidenceDigest = "raw customer content" }},
		{name: "unsupported policy", mutate: func(s *SkillLifecycleSignal) { s.PolicyDigest = "" }},
		{name: "tombstoned", mutate: func(s *SkillLifecycleSignal) { s.Tombstoned = true }, ignore: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &skillSignalRouteRepository{}
			signal := validSkillLifecycleSignal(now, SkillSignalLesson)
			test.mutate(&signal)
			result, err := NewSkillSignalRouter(repository).Route(context.Background(), signal)
			if test.ignore {
				if err != nil || !result.Ignored || repository.calls != 0 {
					t.Fatalf("tombstone result=%+v calls=%d err=%v", result, repository.calls, err)
				}
				return
			}
			if err == nil || repository.calls != 0 {
				t.Fatalf("expected fail closed calls=%d err=%v", repository.calls, err)
			}
		})
	}
}

func TestSkillSignalRouterPropagatesAtomicRollback(t *testing.T) {
	repository := &skillSignalRouteRepository{err: errors.New("transaction rolled back")}
	_, err := NewSkillSignalRouter(repository).Route(context.Background(), validSkillLifecycleSignal(time.Now().UTC(), SkillSignalRevision))
	if err == nil || !strings.Contains(err.Error(), "transaction rolled back") || repository.committed {
		t.Fatalf("expected atomic rollback, committed=%v err=%v", repository.committed, err)
	}
}

type skillSignalRouteRepository struct {
	calls, routeCount int
	err               error
	committed         bool
	executed          bool
	byWorkflow        map[string]SkillSignalRouteResult
}

func (r *skillSignalRouteRepository) RouteSkillSignal(_ context.Context, workflow core.SkillWorkflow, job core.SkillJob, dependencies []core.SkillJobDependency) (SkillSignalRouteResult, error) {
	r.calls++
	if r.err != nil {
		return SkillSignalRouteResult{}, r.err
	}
	if r.byWorkflow == nil {
		r.byWorkflow = map[string]SkillSignalRouteResult{}
	}
	if existing, ok := r.byWorkflow[workflow.ID]; ok {
		existing.Created = false
		return existing, nil
	}
	result := SkillSignalRouteResult{Workflow: workflow, Job: job, Dependencies: dependencies, Created: true}
	r.byWorkflow[workflow.ID] = result
	r.committed = true
	r.routeCount++
	return result, nil
}

func validSkillLifecycleSignal(now time.Time, kind SkillLifecycleSignalKind) SkillLifecycleSignal {
	digest := "sha256:" + strings.Repeat("d", 64)
	return SkillLifecycleSignal{
		ID: "signal-1", Kind: kind, Scope: core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"},
		SkillID: "skill-1", RevisionID: "revision-2", ReferenceID: "reference-1", EvidenceDigest: digest,
		Verified: true, Authorized: true, ConfigurationVersion: 1, PolicyVersion: 1, PolicyDigest: digest,
		OccurredAt: now,
	}
}
