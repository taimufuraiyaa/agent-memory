package application_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func TestSkillActivationServicePromotesAndReplaysIdempotently(t *testing.T) {
	fixture := newSkillActivationFixture(t)
	request := fixture.promotionRequest("promote-2", fixture.revisionTwo, 1)
	activation, err := fixture.service.Activate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if activation.ActiveRevisionID != fixture.revisionTwo.ID || activation.Generation != 2 || activation.LastKnownGoodRevisionID != fixture.revisionOne.ID {
		t.Fatalf("activation = %+v", activation)
	}
	replayed, err := fixture.service.Activate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Generation != activation.Generation || replayed.ActiveRevisionID != activation.ActiveRevisionID {
		t.Fatalf("replay changed activation: %+v", replayed)
	}
	content, err := os.ReadFile(filepath.Join(fixture.projectRoot, ".agents", "skills", "example", "SKILL.md"))
	if err != nil || string(content) != "revision two" {
		t.Fatalf("active content = %q, err %v", content, err)
	}
}

func TestSkillActivationServiceObservesMaterializationWithoutIdentifiers(t *testing.T) {
	fixture := newSkillActivationFixture(t)
	observer := &materializationObserver{}
	fixture.service.WithMaterializationObserver(observer)
	if _, err := fixture.service.Activate(context.Background(), fixture.promotionRequest("observed", fixture.revisionTwo, 1)); err != nil {
		t.Fatal(err)
	}
	if observer.outcome != "success" || observer.duration < 0 {
		t.Fatalf("materialization observation = %+v", observer)
	}
}

type materializationObserver struct {
	outcome  string
	duration time.Duration
}

func (o *materializationObserver) ObserveSkillMaterialization(outcome string, duration time.Duration) {
	o.outcome, o.duration = outcome, duration
}

func TestSkillActivationServiceRejectsStaleGeneration(t *testing.T) {
	fixture := newSkillActivationFixture(t)
	request := fixture.promotionRequest("stale", fixture.revisionTwo, 0)
	if _, err := fixture.service.Activate(context.Background(), request); err == nil {
		t.Fatal("stale activation generation was accepted")
	}
	activation, err := fixture.store.GetSkillActivation(context.Background(), "ws", "local", fixture.skill.ID)
	if err != nil || activation.ActiveRevisionID != fixture.revisionOne.ID {
		t.Fatalf("stale request changed activation: %+v, %v", activation, err)
	}
}

func TestSkillActivationServiceSerializesConcurrentPromotion(t *testing.T) {
	fixture := newSkillActivationFixture(t)
	revisionThree := fixture.addRevision(t, "revision-3", 3, "revision three")
	requests := []application.SkillActivationRequest{
		fixture.promotionRequest("concurrent-2", fixture.revisionTwo, 1),
		fixture.promotionRequest("concurrent-3", revisionThree, 1),
	}
	var wait sync.WaitGroup
	wait.Add(2)
	results := make(chan error, 2)
	for _, request := range requests {
		go func(input application.SkillActivationRequest) {
			defer wait.Done()
			_, err := fixture.service.Activate(context.Background(), input)
			results <- err
		}(request)
	}
	wait.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful concurrent promotions = %d, want 1", succeeded)
	}
	activation, err := fixture.store.GetSkillActivation(context.Background(), "ws", "local", fixture.skill.ID)
	if err != nil || activation.Generation != 2 {
		t.Fatalf("concurrent activation = %+v, %v", activation, err)
	}
}

func TestSkillActivationServiceResumesMaterializingOperation(t *testing.T) {
	fixture := newSkillActivationFixture(t)
	request := fixture.promotionRequest("resume-2", fixture.revisionTwo, 1)
	operation := core.SkillActivationOperation{
		ID: request.OperationID, Workspace: request.Workspace, Environment: request.Environment, SkillID: request.SkillID,
		FromRevisionID: fixture.revisionOne.ID, ToRevisionID: request.TargetRevisionID, ExpectedGeneration: request.ExpectedGeneration,
		State: core.SkillActivationOperationReserved, IdempotencyKey: request.IdempotencyKey, CreatedAt: fixture.now, UpdatedAt: fixture.now,
	}
	if _, err := fixture.store.CreateSkillActivationOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.TransitionSkillActivationOperation(context.Background(), "ws", operation.ID, core.SkillActivationOperationReserved, core.SkillActivationOperationMaterializing, "", fixture.now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	activation, err := fixture.service.Activate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if activation.ActiveRevisionID != fixture.revisionTwo.ID || activation.Generation != 2 {
		t.Fatalf("resumed activation = %+v", activation)
	}
}

func TestSkillActivationServiceAutomaticallyRollsBackThroughSameBoundary(t *testing.T) {
	fixture := newSkillActivationFixture(t)
	if _, err := fixture.service.Activate(context.Background(), fixture.promotionRequest("promote-2", fixture.revisionTwo, 1)); err != nil {
		t.Fatal(err)
	}
	rollback := application.SkillActivationRequest{
		OperationID: "rollback-1", IdempotencyKey: "hard-failure-rollback-1", Workspace: "ws", Environment: "local",
		SkillID: fixture.skill.ID, TargetRevisionID: fixture.revisionOne.ID, ExpectedGeneration: 2,
		PolicyDecisionID: "automatic-hard-failure", Actor: "health-controller", Rollback: true,
		Automatic: true, ReasonCode: "verified_safety_failure",
	}
	activation, err := fixture.service.Activate(context.Background(), rollback)
	if err != nil {
		t.Fatal(err)
	}
	if activation.ActiveRevisionID != fixture.revisionOne.ID || activation.Generation != 3 {
		t.Fatalf("rollback activation = %+v", activation)
	}
	events, err := fixture.store.ListSkillRollbackEvents(context.Background(), "ws", "local", fixture.skill.ID, 10)
	if err != nil || len(events) != 1 || !events[0].Automatic || events[0].ReasonCode != rollback.ReasonCode {
		t.Fatalf("rollback events = %+v, err %v", events, err)
	}
}

func TestSkillActivationServiceManuallyRollsBackWithoutReusingPromotionDecision(t *testing.T) {
	fixture := newSkillActivationFixture(t)
	if _, err := fixture.service.Activate(context.Background(), fixture.promotionRequest("promote-2-manual", fixture.revisionTwo, 1)); err != nil {
		t.Fatal(err)
	}
	rollback := application.SkillActivationRequest{
		OperationID: "rollback-manual", IdempotencyKey: "rollback-manual", Workspace: "ws", Environment: "local",
		SkillID: fixture.skill.ID, TargetRevisionID: fixture.revisionOne.ID, ExpectedGeneration: 2,
		PolicyDecisionID: "manual-rollback", Actor: "operator", Rollback: true, ReasonCode: "operator_requested",
	}
	activation, err := fixture.service.Activate(context.Background(), rollback)
	if err != nil {
		t.Fatal(err)
	}
	if activation.ActiveRevisionID != fixture.revisionOne.ID || activation.Generation != 3 {
		t.Fatalf("manual rollback activation = %+v", activation)
	}
}

func TestSkillActivationServiceRequiresEffectiveApprovalForMediumRisk(t *testing.T) {
	fixture := newSkillActivationFixture(t)
	revision, files := activationRevision(t, fixture.skill, "revision-medium", 3, core.SkillRevisionCanary, "medium revision", fixture.now.Add(3*time.Minute))
	revision.RiskTier = core.SkillRiskMedium
	if err := fixture.store.CreateSkillRevision(context.Background(), revision); err != nil {
		t.Fatal(err)
	}
	policy := core.SkillPromotionPolicy{ID: "policy-medium", Workspace: "ws", Version: 1, RiskTier: core.SkillRiskMedium, MinimumCanarySamples: 1, MinimumVerifiedSuccessRate: .9, MaximumFailureRate: .1, CreatedBy: "operator", CreatedAt: fixture.now}
	if err := fixture.store.CreateSkillPromotionPolicy(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	decision := core.SkillPolicyDecision{ID: "decision-medium", Workspace: "ws", SkillID: fixture.skill.ID, RevisionID: revision.ID, PolicyID: policy.ID, PolicyVersion: 1, EvaluationRunIDs: []string{"verified-run"}, RiskTier: core.SkillRiskMedium, Decision: core.SkillDecisionApprovalRequired, ReasonCodes: []string{"accountable_approval_required"}, DecidedAt: fixture.now}
	if err := fixture.store.CreateSkillPolicyDecision(context.Background(), decision); err != nil {
		t.Fatal(err)
	}
	bundles, err := workspace.NewRevisionBundleStore(fixture.projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundles.Publish(context.Background(), revision, files); err != nil {
		t.Fatal(err)
	}
	request := application.SkillActivationRequest{OperationID: "medium-activate", IdempotencyKey: "medium-activate", Workspace: "ws", Environment: "local", SkillID: fixture.skill.ID, TargetRevisionID: revision.ID, ExpectedGeneration: 1, PolicyDecisionID: decision.ID, Actor: "operator"}
	if _, err := fixture.service.Activate(context.Background(), request); err == nil {
		t.Fatal("medium-risk revision activated without approval")
	}
	approval := core.SkillApproval{ID: "approval-medium", Workspace: "ws", RevisionID: revision.ID, PolicyDecisionID: decision.ID, ApproverID: "independent-reviewer", Approved: true, Reason: "verified", CreatedAt: fixture.now}
	if err := fixture.store.CreateSkillApproval(context.Background(), approval); err != nil {
		t.Fatal(err)
	}
	activation, err := fixture.service.Activate(context.Background(), request)
	if err != nil || activation.ActiveRevisionID != revision.ID {
		t.Fatalf("approved medium activation = %+v, %v", activation, err)
	}
}

type skillActivationFixture struct {
	projectRoot string
	store       *sqlite.Store
	service     *application.SkillActivationService
	skill       core.LogicalSkill
	revisionOne core.SkillRevision
	revisionTwo core.SkillRevision
	now         time.Time
}

func newSkillActivationFixture(t *testing.T) *skillActivationFixture {
	t.Helper()
	ctx := context.Background()
	projectRoot := t.TempDir()
	t.Cleanup(func() {
		for _, relative := range []string{".agent-memory", ".agents"} {
			_ = filepath.Walk(filepath.Join(projectRoot, relative), func(path string, info os.FileInfo, err error) error {
				if err == nil && info.IsDir() {
					_ = os.Chmod(path, 0o700)
				} else if err == nil {
					_ = os.Chmod(path, 0o600)
				}
				return nil
			})
		}
	})
	now := time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "activation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	skill := core.LogicalSkill{ID: "skill-1", Workspace: "ws", Name: "example", Description: "Example", RiskTier: core.SkillRiskLow, OwnerGroup: "test", Status: core.SkillStatusActive, Generation: 1, CreatedAt: now, UpdatedAt: now}
	revisionOne, filesOne := activationRevision(t, skill, "revision-1", 1, core.SkillRevisionActive, "revision one", now)
	activation := core.SkillActivation{ID: "activation-1", Workspace: "ws", Environment: "local", SkillID: skill.ID, ActiveRevisionID: revisionOne.ID, ActiveDigest: revisionOne.BundleDigest, LastKnownGoodRevisionID: revisionOne.ID, LastKnownGoodDigest: revisionOne.BundleDigest, Generation: 1, PolicyDecisionID: "import", Materialization: core.SkillMaterializationReady, ActivatedBy: "import", ActivatedAt: now, UpdatedAt: now}
	if _, err := store.ImportSkillRevisionOne(ctx, sqlite.SkillRevisionOneImport{Skill: skill, Revision: revisionOne, Activation: activation}); err != nil {
		t.Fatal(err)
	}
	revisionTwo, filesTwo := activationRevision(t, skill, "revision-2", 2, core.SkillRevisionCanary, "revision two", now.Add(time.Minute))
	if err := store.CreateSkillRevision(ctx, revisionTwo); err != nil {
		t.Fatal(err)
	}
	policy := core.SkillPromotionPolicy{ID: "policy-low", Workspace: "ws", Version: 1, RiskTier: core.SkillRiskLow, MinimumCanarySamples: 1, MinimumVerifiedSuccessRate: .9, MaximumFailureRate: .1, AllowAutomaticActivation: true, CreatedBy: "operator", CreatedAt: now}
	if err := store.CreateSkillPromotionPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	createActivationDecision(t, store, revisionTwo, now)
	bundles, err := workspace.NewRevisionBundleStore(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundles.Publish(ctx, revisionOne, filesOne); err != nil {
		t.Fatal(err)
	}
	if _, err := bundles.Publish(ctx, revisionTwo, filesTwo); err != nil {
		t.Fatal(err)
	}
	materializer, err := workspace.NewSkillMaterializer(projectRoot, bundles)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := materializer.Materialize(ctx, workspace.SkillMaterializationRequest{OperationID: "fixture-one", Skill: skill, Revision: revisionOne}); err != nil {
		t.Fatal(err)
	}
	service := application.NewSkillActivationService(store, materializer, func() time.Time { return now.Add(2 * time.Minute) })
	return &skillActivationFixture{projectRoot: projectRoot, store: store, service: service, skill: skill, revisionOne: revisionOne, revisionTwo: revisionTwo, now: now}
}

func (f *skillActivationFixture) promotionRequest(id string, revision core.SkillRevision, generation int64) application.SkillActivationRequest {
	return application.SkillActivationRequest{OperationID: id, IdempotencyKey: id, Workspace: "ws", Environment: "local", SkillID: f.skill.ID, TargetRevisionID: revision.ID, ExpectedGeneration: generation, PolicyDecisionID: "decision-" + revision.ID, Actor: "operator"}
}

func (f *skillActivationFixture) addRevision(t *testing.T, id string, number int64, content string) core.SkillRevision {
	t.Helper()
	revision, files := activationRevision(t, f.skill, id, number, core.SkillRevisionCanary, content, f.now.Add(time.Duration(number)*time.Minute))
	if err := f.store.CreateSkillRevision(context.Background(), revision); err != nil {
		t.Fatal(err)
	}
	createActivationDecision(t, f.store, revision, f.now)
	bundles, err := workspace.NewRevisionBundleStore(f.projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundles.Publish(context.Background(), revision, files); err != nil {
		t.Fatal(err)
	}
	return revision
}

func createActivationDecision(t *testing.T, store *sqlite.Store, revision core.SkillRevision, now time.Time) {
	t.Helper()
	decision := core.SkillPolicyDecision{ID: "decision-" + revision.ID, Workspace: revision.Workspace, SkillID: revision.SkillID, RevisionID: revision.ID, PolicyID: "policy-low", PolicyVersion: 1, EvaluationRunIDs: []string{"verified-run"}, RiskTier: revision.RiskTier, Decision: core.SkillDecisionPromote, ReasonCodes: []string{"all_policy_gates_passed"}, DecidedAt: now}
	if err := store.CreateSkillPolicyDecision(context.Background(), decision); err != nil {
		t.Fatal(err)
	}
}

func activationRevision(t *testing.T, skill core.LogicalSkill, id string, number int64, state core.SkillRevisionState, content string, now time.Time) (core.SkillRevision, map[string][]byte) {
	t.Helper()
	raw := []byte(content)
	fileDigest := sha256.Sum256(raw)
	file := core.SkillBundleFile{Path: "SKILL.md", Digest: "sha256:" + hex.EncodeToString(fileDigest[:]), SizeBytes: int64(len(raw))}
	files := map[string][]byte{"SKILL.md": raw}
	revision := core.SkillRevision{ID: id, Workspace: skill.Workspace, SkillID: skill.ID, Number: number, State: state, ManifestVersion: 1, Files: []core.SkillBundleFile{file}, RiskTier: core.SkillRiskLow, CreatedBy: "test", CreatedAt: now}
	if number > 1 {
		revision.ParentRevisionIDs = []string{"revision-1"}
	}
	revision.BundleDigest = applicationBundleDigest(revision.Files)
	return revision, files
}

func applicationBundleDigest(files []core.SkillBundleFile) string {
	ordered := append([]core.SkillBundleFile(nil), files...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	hash := sha256.New()
	for _, file := range ordered {
		hash.Write([]byte(file.Path))
		hash.Write([]byte{0})
		hash.Write([]byte(file.Digest))
		hash.Write([]byte{0})
		hash.Write([]byte(strconv.FormatInt(file.SizeBytes, 10)))
		hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}
