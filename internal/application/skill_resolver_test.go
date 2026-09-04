package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillResolverAppliesDeterministicPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*SkillResolutionRequest)
		wantID     string
		wantReason core.SkillResolutionReason
	}{
		{name: "explicit pin", mutate: func(r *SkillResolutionRequest) { r.ExplicitRevisionID = "revision-previous" }, wantID: "revision-previous", wantReason: core.SkillResolutionExplicitPin},
		{name: "environment pin", mutate: func(r *SkillResolutionRequest) { r.EnvironmentRevisionID = "revision-previous" }, wantID: "revision-previous", wantReason: core.SkillResolutionEnvironment},
		{name: "canary", mutate: func(r *SkillResolutionRequest) { r.CanaryBasisPoints = 10_000 }, wantID: "revision-canary", wantReason: core.SkillResolutionCanary},
		{name: "active", mutate: func(_ *SkillResolutionRequest) {}, wantID: "revision-active", wantReason: core.SkillResolutionActive},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newResolverFixture()
			request := fixture.request()
			test.mutate(&request)
			result, err := fixture.resolver.Resolve(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if result.Resolution.RevisionID != test.wantID || result.Resolution.Reason != test.wantReason {
				t.Fatalf("resolution = %+v", result.Resolution)
			}
			if result.AcknowledgementToken == "" || fixture.repository.saved.ID != result.Resolution.ID {
				t.Fatal("resolution acknowledgement was not persisted")
			}
		})
	}
}

func TestSkillResolverFallsBackWhenActiveIsIncompatible(t *testing.T) {
	fixture := newResolverFixture()
	active := fixture.repository.revisions["revision-active"]
	active.Compatibility.Platforms = []string{"linux"}
	fixture.repository.revisions[active.ID] = active
	request := fixture.request()
	request.Platform = "darwin"
	result, err := fixture.resolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Resolution.RevisionID != "revision-previous" || result.Resolution.Reason != core.SkillResolutionFallback {
		t.Fatalf("fallback resolution = %+v", result.Resolution)
	}
}

func TestSkillResolverFailsWhenNoCompatibleRevisionExists(t *testing.T) {
	fixture := newResolverFixture()
	for id, revision := range fixture.repository.revisions {
		revision.Compatibility.Platforms = []string{"linux"}
		fixture.repository.revisions[id] = revision
	}
	request := fixture.request()
	request.Platform = "darwin"
	if _, err := fixture.resolver.Resolve(context.Background(), request); !errors.Is(err, ErrNoCompatibleSkillRevision) {
		t.Fatalf("expected no-compatible error, got %v", err)
	}
}

func TestSkillResolverRejectsDisabledSkillAndPinnedRevision(t *testing.T) {
	fixture := newResolverFixture()
	fixture.repository.skill.Status = core.SkillStatusArchived
	if _, err := fixture.resolver.Resolve(context.Background(), fixture.request()); err == nil {
		t.Fatal("disabled skill was resolved")
	}
	fixture.repository.skill.Status = core.SkillStatusActive
	disabled := fixture.repository.revisions["revision-previous"]
	disabled.State = core.SkillRevisionDisabled
	fixture.repository.revisions[disabled.ID] = disabled
	request := fixture.request()
	request.ExplicitRevisionID = disabled.ID
	if _, err := fixture.resolver.Resolve(context.Background(), request); err == nil {
		t.Fatal("disabled explicit pin was resolved")
	}
}

func TestSkillResolverFailsClosedOnStaleActiveMaterialization(t *testing.T) {
	fixture := newResolverFixture()
	fixture.artifacts.activeError = errors.New("digest mismatch")
	if _, err := fixture.resolver.Resolve(context.Background(), fixture.request()); err == nil {
		t.Fatal("stale materialized active revision was resolved")
	}
}

func TestSkillResolverEnforcesWorkspaceAuthorizationAndIsolation(t *testing.T) {
	fixture := newResolverFixture()
	fixture.authorizer.resolveError = errors.New("denied")
	if _, err := fixture.resolver.Resolve(context.Background(), fixture.request()); err == nil {
		t.Fatal("unauthorized resolution succeeded")
	}
	fixture.authorizer.resolveError = nil
	request := fixture.request()
	request.Workspace = "other"
	if _, err := fixture.resolver.Resolve(context.Background(), request); err == nil {
		t.Fatal("cross-workspace resolution succeeded")
	}
}

type resolverRepository struct {
	skill      core.LogicalSkill
	activation core.SkillActivation
	revisions  map[string]core.SkillRevision
	saved      core.SkillResolution
}

func (r *resolverRepository) GetLogicalSkill(_ context.Context, workspace, skillID string) (core.LogicalSkill, error) {
	if workspace != r.skill.Workspace || skillID != r.skill.ID {
		return core.LogicalSkill{}, errors.New("skill not found")
	}
	return r.skill, nil
}

func (r *resolverRepository) GetSkillActivation(_ context.Context, workspace, environment, skillID string) (core.SkillActivation, error) {
	if workspace != r.activation.Workspace || environment != r.activation.Environment || skillID != r.activation.SkillID {
		return core.SkillActivation{}, errors.New("activation not found")
	}
	return r.activation, nil
}

func (r *resolverRepository) GetSkillRevision(_ context.Context, workspace, revisionID string) (core.SkillRevision, error) {
	revision, exists := r.revisions[revisionID]
	if !exists || revision.Workspace != workspace {
		return core.SkillRevision{}, errors.New("revision not found")
	}
	return revision, nil
}

func (r *resolverRepository) CreateSkillResolution(_ context.Context, resolution core.SkillResolution) error {
	if r.saved.ID != "" {
		return errors.New("duplicate resolution id")
	}
	r.saved = resolution
	return nil
}

type resolverAuthorizer struct {
	resolveError error
	pinError     error
}

func (a *resolverAuthorizer) AuthorizeSkillResolution(context.Context, string, string, string, string) error {
	return a.resolveError
}

func (a *resolverAuthorizer) AuthorizeSkillPin(context.Context, string, string, string, string) error {
	return a.pinError
}

type resolverArtifacts struct {
	activeError    error
	immutableError error
}

func (a *resolverArtifacts) VerifyActive(context.Context, core.LogicalSkill, core.SkillRevision) error {
	return a.activeError
}

func (a *resolverArtifacts) VerifyImmutable(context.Context, core.SkillRevision) error {
	return a.immutableError
}

type resolverFixture struct {
	repository *resolverRepository
	authorizer *resolverAuthorizer
	artifacts  *resolverArtifacts
	resolver   *SkillResolver
	now        time.Time
}

func newResolverFixture() resolverFixture {
	now := time.Date(2026, 8, 29, 17, 0, 0, 0, time.UTC)
	skill := core.LogicalSkill{ID: "skill-1", Workspace: "ws", Name: "example", Description: "Example", RiskTier: core.SkillRiskLow, OwnerGroup: "test", Status: core.SkillStatusActive, Generation: 1, CreatedAt: now, UpdatedAt: now}
	revisions := map[string]core.SkillRevision{
		"revision-active":   resolverRevision("revision-active", 2, core.SkillRevisionActive, skill, now),
		"revision-previous": resolverRevision("revision-previous", 1, core.SkillRevisionPrevious, skill, now),
		"revision-canary":   resolverRevision("revision-canary", 3, core.SkillRevisionCanary, skill, now),
	}
	activation := core.SkillActivation{ID: "activation-1", Workspace: "ws", Environment: "local", SkillID: skill.ID, ActiveRevisionID: "revision-active", ActiveDigest: revisions["revision-active"].BundleDigest, LastKnownGoodRevisionID: "revision-previous", LastKnownGoodDigest: revisions["revision-previous"].BundleDigest, CanaryRevisionID: "revision-canary", CanaryDigest: revisions["revision-canary"].BundleDigest, Generation: 2, PolicyDecisionID: "decision-1", Materialization: core.SkillMaterializationReady, ActivatedBy: "operator", ActivatedAt: now, UpdatedAt: now}
	repository := &resolverRepository{skill: skill, activation: activation, revisions: revisions}
	authorizer := &resolverAuthorizer{}
	artifacts := &resolverArtifacts{}
	resolver := NewSkillResolver(repository, authorizer, artifacts, func() time.Time { return now })
	return resolverFixture{repository: repository, authorizer: authorizer, artifacts: artifacts, resolver: resolver, now: now}
}

func (f resolverFixture) request() SkillResolutionRequest {
	return SkillResolutionRequest{Workspace: "ws", Environment: "local", PrincipalID: "agent-1", TaskID: "task-1", SkillID: "skill-1", Platform: "darwin", Architecture: "arm64", RuntimeVersion: "1.0.0", Capabilities: []string{"filesystem.read"}, PolicyVersion: 1, AcknowledgementSupported: true}
}

func resolverRevision(id string, number int64, state core.SkillRevisionState, skill core.LogicalSkill, now time.Time) core.SkillRevision {
	character := byte('a' + number)
	revision := core.SkillRevision{ID: id, Workspace: skill.Workspace, SkillID: skill.ID, Number: number, State: state, BundleDigest: "sha256:" + repeatByte(character+1, 64), ManifestVersion: 1, Files: []core.SkillBundleFile{{Path: "SKILL.md", Digest: "sha256:" + repeatByte(character, 64), SizeBytes: 10}}, Compatibility: core.SkillCompatibility{Platforms: []string{"darwin"}, Architectures: []string{"arm64"}, RequiredCapabilities: []string{"filesystem.read"}, MinimumRuntime: "1.0.0"}, RiskTier: core.SkillRiskLow, CreatedBy: "test", CreatedAt: now}
	if number > 1 {
		revision.ParentRevisionIDs = []string{"revision-previous"}
	}
	return revision
}

func repeatByte(value byte, count int) string {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return string(result)
}
