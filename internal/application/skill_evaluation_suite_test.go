package application

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillEvaluationSuiteServiceCreatesImmutableSupersedingVersions(t *testing.T) {
	fixture := newEvaluationSuiteFixture()
	input := fixture.input(allEvaluationCaseKinds())
	first, err := fixture.service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.ID = "suite-2"
	input.Cases[0], input.Cases[len(input.Cases)-1] = input.Cases[len(input.Cases)-1], input.Cases[0]
	input.Cases[0].Summary = "Updated artifact verification."
	second, err := fixture.service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || second.Version != 2 || first.Digest == second.Digest {
		t.Fatalf("suite versions = %+v then %+v", first, second)
	}
	if fixture.repository.suites[0].ID != "suite-1" {
		t.Fatal("creating a new version mutated historical suite")
	}
}

func TestSkillEvaluationSuiteDigestIsIndependentOfCaseOrder(t *testing.T) {
	cases := allEvaluationCaseKinds()
	forward, err := SkillEvaluationSuiteDigest(cases)
	if err != nil {
		t.Fatal(err)
	}
	cases[0], cases[len(cases)-1] = cases[len(cases)-1], cases[0]
	reversed, err := SkillEvaluationSuiteDigest(cases)
	if err != nil || forward != reversed {
		t.Fatalf("digests = %s and %s, err %v", forward, reversed, err)
	}
}

func TestSkillEvaluationSuiteServiceRejectsMissingReference(t *testing.T) {
	fixture := newEvaluationSuiteFixture()
	fixture.references.missing = "fixture:safety"
	if _, err := fixture.service.Create(context.Background(), fixture.input(allEvaluationCaseKinds())); err == nil {
		t.Fatal("missing case reference was accepted")
	}
	if len(fixture.repository.suites) != 0 {
		t.Fatal("invalid suite was persisted")
	}
}

func TestSkillEvaluationSuiteServiceEnforcesAuthorizationAndWorkspace(t *testing.T) {
	fixture := newEvaluationSuiteFixture()
	fixture.authorizer.err = errors.New("denied")
	if _, err := fixture.service.Create(context.Background(), fixture.input(allEvaluationCaseKinds())); err == nil {
		t.Fatal("unauthorized suite creation succeeded")
	}
	fixture.authorizer.err = nil
	input := fixture.input(allEvaluationCaseKinds())
	input.Workspace = "other"
	if _, err := fixture.service.Create(context.Background(), input); err == nil {
		t.Fatal("cross-workspace suite creation succeeded")
	}
}

func TestSkillEvaluationSuiteServiceRejectsInvalidCaseInventory(t *testing.T) {
	fixture := newEvaluationSuiteFixture()
	input := fixture.input(allEvaluationCaseKinds())
	input.Cases[1].ID = input.Cases[0].ID
	if _, err := fixture.service.Create(context.Background(), input); err == nil {
		t.Fatal("duplicate case id was accepted")
	}
}

type evaluationSuiteRepository struct {
	skill  core.LogicalSkill
	suites []core.SkillEvaluationSuite
}

func (r *evaluationSuiteRepository) GetLogicalSkill(_ context.Context, workspace, skillID string) (core.LogicalSkill, error) {
	if workspace != r.skill.Workspace || skillID != r.skill.ID {
		return core.LogicalSkill{}, errors.New("skill not found")
	}
	return r.skill, nil
}

func (r *evaluationSuiteRepository) GetLatestSkillEvaluationSuite(_ context.Context, workspace, skillID string) (core.SkillEvaluationSuite, error) {
	if workspace != r.skill.Workspace || skillID != r.skill.ID || len(r.suites) == 0 {
		return core.SkillEvaluationSuite{}, sql.ErrNoRows
	}
	return r.suites[len(r.suites)-1], nil
}

func (r *evaluationSuiteRepository) CreateSkillEvaluationSuite(_ context.Context, suite core.SkillEvaluationSuite) error {
	r.suites = append(r.suites, suite)
	return nil
}

type evaluationSuiteAuthorizer struct{ err error }

func (a *evaluationSuiteAuthorizer) AuthorizeEvaluationSuite(context.Context, string, string, string) error {
	return a.err
}

type evaluationReferenceValidator struct{ missing string }

func (v *evaluationReferenceValidator) ValidateEvaluationReference(_ context.Context, workspace, reference string) error {
	if workspace != "ws" || reference == v.missing {
		return errors.New("reference not found")
	}
	return nil
}

type evaluationSuiteFixture struct {
	repository *evaluationSuiteRepository
	authorizer *evaluationSuiteAuthorizer
	references *evaluationReferenceValidator
	service    *SkillEvaluationSuiteService
	now        time.Time
}

func newEvaluationSuiteFixture() evaluationSuiteFixture {
	now := time.Date(2026, 8, 29, 18, 0, 0, 0, time.UTC)
	skill := core.LogicalSkill{ID: "skill-1", Workspace: "ws", Name: "example", Description: "Example", RiskTier: core.SkillRiskLow, OwnerGroup: "test", Status: core.SkillStatusActive, Generation: 1, CreatedAt: now, UpdatedAt: now}
	repository := &evaluationSuiteRepository{skill: skill}
	authorizer := &evaluationSuiteAuthorizer{}
	references := &evaluationReferenceValidator{}
	service := NewSkillEvaluationSuiteService(repository, authorizer, references, func() time.Time { return now })
	return evaluationSuiteFixture{repository: repository, authorizer: authorizer, references: references, service: service, now: now}
}

func (f evaluationSuiteFixture) input(cases []core.SkillEvaluationCase) CreateSkillEvaluationSuiteInput {
	return CreateSkillEvaluationSuiteInput{ID: "suite-1", Workspace: "ws", SkillID: "skill-1", Cases: cases, CreatedBy: "reviewer"}
}

func allEvaluationCaseKinds() []core.SkillEvaluationCase {
	return []core.SkillEvaluationCase{
		{ID: "positive", Kind: core.SkillCasePositive, Summary: "Expected trigger succeeds.", Reference: "fixture:positive", Required: true},
		{ID: "negative", Kind: core.SkillCaseNegative, Summary: "Unrelated task does not trigger.", Reference: "fixture:negative", Required: true},
		{ID: "regression", Kind: core.SkillCaseRegression, Summary: "Prior behavior remains correct.", Reference: "fixture:regression", Required: true},
		{ID: "safety", Kind: core.SkillCaseSafety, Summary: "Unsafe action is rejected.", Reference: "fixture:safety", Required: true},
		{ID: "compatibility", Kind: core.SkillCaseCompatibility, Summary: "Runtime requirements are enforced.", Reference: "fixture:compatibility", Required: true},
		{ID: "artifact", Kind: core.SkillCaseArtifact, Summary: "Expected artifact is verified.", Reference: "fixture:artifact", Required: true},
	}
}
