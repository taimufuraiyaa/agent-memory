package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillSafetyIngressAuthenticatesDisablesAndPrioritizesEveryHardSignal(t *testing.T) {
	for _, kind := range []core.SkillSafetySignalKind{core.SkillSafetyViolation, core.SkillHarmfulFeedback, core.SkillDigestMismatch} {
		t.Run(string(kind), func(t *testing.T) {
			fixture := newSafetyOrchestratorFixture(t)
			request := fixture.request("signal-"+string(kind), kind)
			result, err := fixture.ingress.Admit(context.Background(), request)
			if err != nil || !result.Route.Created || !fixture.repository.disabledBeforeRoute || fixture.repository.job.Priority != SkillOrchestratorSafetyPriority || fixture.repository.job.Stage != core.SkillStageRollback {
				t.Fatalf("ingress = %+v job=%+v disabledFirst=%v err=%v", result, fixture.repository.job, fixture.repository.disabledBeforeRoute, err)
			}
			replay, err := fixture.ingress.Admit(context.Background(), request)
			if err != nil || !replay.Duplicate || replay.Route.Created || fixture.repository.routeCalls != 2 {
				t.Fatalf("repeated signal = %+v calls=%d err=%v", replay, fixture.repository.routeCalls, err)
			}
		})
	}
}

func TestSkillSafetyIngressRejectsForgeryCrossScopeAndConflictingEvidence(t *testing.T) {
	fixture := newSafetyOrchestratorFixture(t)
	fixture.authenticator.err = errors.New("invalid signature")
	if _, err := fixture.ingress.Admit(context.Background(), fixture.request("forged", core.SkillSafetyViolation)); err == nil {
		t.Fatal("forged safety signal was accepted")
	}
	if fixture.repository.disabled || fixture.repository.routeCalls != 0 {
		t.Fatal("forged signal changed allocation or queue")
	}

	fixture = newSafetyOrchestratorFixture(t)
	request := fixture.request("cross-scope", core.SkillSafetyViolation)
	request.Scope.WorkspaceID = "other"
	if _, err := fixture.ingress.Admit(context.Background(), request); err == nil {
		t.Fatal("cross-scope signal was accepted")
	}

	fixture = newSafetyOrchestratorFixture(t)
	request = fixture.request("conflict", core.SkillSafetyViolation)
	if _, err := fixture.ingress.Admit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.DeduplicationDigest = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if _, err := fixture.ingress.Admit(context.Background(), request); err == nil {
		t.Fatal("conflicting duplicate evidence was accepted")
	}
}

func TestSkillSafetyIngressKeepsSoftSignalsInCooldownWithoutRollback(t *testing.T) {
	fixture := newSafetyOrchestratorFixture(t)
	result, err := fixture.ingress.Admit(context.Background(), fixture.request("soft", core.SkillSoftRegression))
	if err != nil || result.Signal.State != core.SkillSafetyCooldown || !result.Signal.CooldownUntil.After(result.Signal.UpdatedAt) {
		t.Fatalf("soft ingress = %+v, %v", result, err)
	}
	if fixture.repository.disabled || fixture.repository.routeCalls != 0 || fixture.activator.calls != 0 {
		t.Fatal("soft signal disabled allocation or scheduled rollback")
	}
}

func TestSkillSafetyRollbackAdapterRecoversFailureAndReplaysResolvedLease(t *testing.T) {
	fixture := newSafetyOrchestratorFixture(t)
	request := fixture.request("hard-recovery", core.SkillSafetyViolation)
	if _, err := fixture.ingress.Admit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	adapter, err := NewSkillSafetyRollbackAdapter(fixture.repository, fixture.activator, 15*time.Minute, fixture.configuration, func() time.Time { return fixture.now })
	if err != nil {
		t.Fatal(err)
	}
	fixture.activator.err = errors.New("materialization unavailable")
	_, err = adapter.Execute(context.Background(), fixture.repository.job)
	assertSkillStageFailure(t, err, core.SkillFailureDependencyUnavailable, "safety_rollback_failed")
	if fixture.repository.signals[request.ID].State != core.SkillSafetyRollbackFailed || !fixture.repository.disabled {
		t.Fatal("failed rollback did not preserve disabled pending state")
	}
	fixture.activator.err = nil
	result, err := adapter.Execute(context.Background(), fixture.repository.job)
	if err != nil || len(result.References) != 2 || fixture.repository.signals[request.ID].State != core.SkillSafetyResolved {
		t.Fatalf("rollback recovery = %+v signal=%+v err=%v", result, fixture.repository.signals[request.ID], err)
	}
	calls := fixture.activator.calls
	if _, err := adapter.Execute(context.Background(), fixture.repository.job); err != nil || fixture.activator.calls != calls {
		t.Fatalf("resolved lease replay calls=%d want=%d err=%v", fixture.activator.calls, calls, err)
	}
}

type safetyIngressAuthenticator struct{ err error }

func (a *safetyIngressAuthenticator) AuthenticateSkillSafetySignal(context.Context, SkillSafetyIngressRequest) error {
	return a.err
}

type safetyOrchestratorRepository struct {
	*skillSafetyRepository
	workflow            core.SkillWorkflow
	job                 core.SkillJob
	routeCalls          int
	disabledBeforeRoute bool
}

func (r *safetyOrchestratorRepository) RouteSkillSignal(_ context.Context, workflow core.SkillWorkflow, job core.SkillJob, dependencies []core.SkillJobDependency) (SkillSignalRouteResult, error) {
	r.routeCalls++
	r.disabledBeforeRoute = r.disabled
	if r.workflow.ID == workflow.ID {
		return SkillSignalRouteResult{Workflow: r.workflow, Job: r.job}, nil
	}
	r.workflow, r.job = workflow, job
	return contracts.SkillSignalRouteResult{Workflow: workflow, Job: job, Dependencies: dependencies, Created: true}, nil
}

func (r *safetyOrchestratorRepository) GetSkillWorkflow(_ context.Context, scope core.SkillOrchestratorScope, id string) (core.SkillWorkflow, error) {
	if scope == r.workflow.Scope && id == r.workflow.ID {
		return r.workflow, nil
	}
	return core.SkillWorkflow{}, errors.New("workflow not found")
}

type safetyOrchestratorFixture struct {
	repository    *safetyOrchestratorRepository
	authenticator *safetyIngressAuthenticator
	activator     *skillSafetyActivator
	ingress       *SkillSafetyIngress
	configuration SkillSignalConfiguration
	now           time.Time
}

func newSafetyOrchestratorFixture(t *testing.T) safetyOrchestratorFixture {
	t.Helper()
	base := newSkillSafetyFixture()
	repository := &safetyOrchestratorRepository{skillSafetyRepository: base.repository}
	authenticator := &safetyIngressAuthenticator{}
	configuration := SkillSignalConfiguration{Environment: "local", ConfigurationVersion: 4, PolicyVersion: 7,
		PolicyDigest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}
	ingress, err := NewSkillSafetyIngress(repository, authenticator, configuration, 15*time.Minute, func() time.Time { return base.now })
	if err != nil {
		t.Fatal(err)
	}
	return safetyOrchestratorFixture{repository: repository, authenticator: authenticator, activator: base.activator, ingress: ingress, configuration: configuration, now: base.now}
}

func (f safetyOrchestratorFixture) request(id string, kind core.SkillSafetySignalKind) SkillSafetyIngressRequest {
	return SkillSafetyIngressRequest{ID: id, Scope: core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "local"},
		SkillID: "skill-1", RevisionID: f.repository.revision.ID, RevisionDigest: f.repository.revision.BundleDigest,
		Kind: kind, SourceType: "verified_execution", VerifierID: "safety-verifier", EvidenceReference: "evidence-1",
		DeduplicationDigest: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		PolicyVersion:       f.configuration.PolicyVersion, AuthenticationProof: "signed-proof"}
}
