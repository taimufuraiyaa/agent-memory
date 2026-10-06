package application

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillSafetyObserverDisablesAndRollsBackHardFailures(t *testing.T) {
	for _, kind := range []core.SkillSafetySignalKind{core.SkillSafetyViolation, core.SkillHarmfulFeedback, core.SkillDigestMismatch} {
		fixture := newSkillSafetyFixture()
		result, err := fixture.observer.Observe(context.Background(), fixture.input("signal-"+string(kind), kind))
		if err != nil || result.Signal.State != core.SkillSafetyResolved || result.Activation == nil {
			t.Fatalf("kind %s result = %+v, %v", kind, result, err)
		}
		if !fixture.repository.disabled || fixture.activator.calls != 1 {
			t.Fatalf("kind %s disabled=%v calls=%d", kind, fixture.repository.disabled, fixture.activator.calls)
		}
	}
}

func TestSkillSafetyObserverAppliesCooldownWithoutOscillation(t *testing.T) {
	fixture := newSkillSafetyFixture()
	first, err := fixture.observer.Observe(context.Background(), fixture.input("soft-1", core.SkillSoftRegression))
	if err != nil || first.Signal.State != core.SkillSafetyCooldown {
		t.Fatalf("soft signal = %+v, %v", first, err)
	}
	second, err := fixture.observer.Observe(context.Background(), fixture.input("soft-2", core.SkillSoftRegression))
	if err != nil || second.Signal.State != core.SkillSafetyCooldown || fixture.activator.calls != 0 || fixture.repository.disabled {
		t.Fatalf("repeated soft signal = %+v, calls %d, disabled %v, err %v", second, fixture.activator.calls, fixture.repository.disabled, err)
	}
}

func TestSkillSafetyObserverRecoversFailedRollbackOnReplay(t *testing.T) {
	fixture := newSkillSafetyFixture()
	fixture.activator.err = errors.New("filesystem unavailable")
	input := fixture.input("hard-1", core.SkillSafetyViolation)
	result, err := fixture.observer.Observe(context.Background(), input)
	if err == nil || result.Signal.State != core.SkillSafetyRollbackFailed || !fixture.repository.disabled {
		t.Fatalf("failed rollback = %+v, %v", result, err)
	}
	fixture.activator.err = nil
	result, err = fixture.observer.Observe(context.Background(), input)
	if err != nil || result.Signal.State != core.SkillSafetyResolved || result.Activation == nil {
		t.Fatalf("recovered rollback = %+v, %v", result, err)
	}
}

type skillSafetyRepository struct {
	activation core.SkillActivation
	revision   core.SkillRevision
	signals    map[string]core.SkillSafetySignal
	disabled   bool
}

func (r *skillSafetyRepository) GetSkillActivation(context.Context, string, string, string) (core.SkillActivation, error) {
	return r.activation, nil
}

func (r *skillSafetyRepository) GetSkillRevision(context.Context, string, string) (core.SkillRevision, error) {
	return r.revision, nil
}

func (r *skillSafetyRepository) GetSkillSafetySignal(_ context.Context, workspace, signalID string) (core.SkillSafetySignal, error) {
	signal, exists := r.signals[signalID]
	if !exists || signal.Workspace != workspace {
		return core.SkillSafetySignal{}, sql.ErrNoRows
	}
	return signal, nil
}

func (r *skillSafetyRepository) CreateSkillSafetySignal(_ context.Context, signal core.SkillSafetySignal) error {
	r.signals[signal.ID] = signal
	return nil
}

func (r *skillSafetyRepository) UpdateSkillSafetySignal(_ context.Context, signal core.SkillSafetySignal) error {
	r.signals[signal.ID] = signal
	return nil
}

func (r *skillSafetyRepository) DisableSkillRevisionForSafety(context.Context, string, string, string, string) error {
	r.disabled = true
	r.revision.State = core.SkillRevisionDisabled
	return nil
}

type skillSafetyActivator struct {
	calls      int
	err        error
	activation core.SkillActivation
}

func (a *skillSafetyActivator) Activate(context.Context, SkillActivationRequest) (core.SkillActivation, error) {
	a.calls++
	return a.activation, a.err
}

type skillSafetyFixture struct {
	repository *skillSafetyRepository
	activator  *skillSafetyActivator
	observer   *SkillSafetyObserver
	now        time.Time
}

func newSkillSafetyFixture() skillSafetyFixture {
	now := time.Date(2026, 8, 30, 1, 0, 0, 0, time.UTC)
	revision := resolverRevision("revision-2", 2, core.SkillRevisionActive, core.LogicalSkill{ID: "skill-1", Workspace: "ws"}, now)
	activation := core.SkillActivation{ID: "activation-1", Workspace: "ws", Environment: "local", SkillID: "skill-1", ActiveRevisionID: revision.ID, ActiveDigest: revision.BundleDigest, LastKnownGoodRevisionID: "revision-1", LastKnownGoodDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Generation: 2, PolicyDecisionID: "decision-1", Materialization: core.SkillMaterializationReady, ActivatedBy: "operator", ActivatedAt: now, UpdatedAt: now}
	repository := &skillSafetyRepository{activation: activation, revision: revision, signals: map[string]core.SkillSafetySignal{}}
	rolledBack := activation
	rolledBack.ActiveRevisionID, rolledBack.ActiveDigest, rolledBack.Generation = activation.LastKnownGoodRevisionID, activation.LastKnownGoodDigest, 3
	activator := &skillSafetyActivator{activation: rolledBack}
	observer := NewSkillSafetyObserver(repository, activator, 15*time.Minute, func() time.Time { return now })
	return skillSafetyFixture{repository: repository, activator: activator, observer: observer, now: now}
}

func (f skillSafetyFixture) input(id string, kind core.SkillSafetySignalKind) SkillSafetyObservation {
	return SkillSafetyObservation{ID: id, Workspace: "ws", Environment: "local", SkillID: "skill-1", RevisionID: "revision-2", Kind: kind, Verified: true}
}
