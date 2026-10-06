package application

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillSafetyObservation struct {
	ID          string                     `json:"id"`
	Workspace   string                     `json:"workspace"`
	Environment string                     `json:"environment"`
	SkillID     string                     `json:"skill_id"`
	RevisionID  string                     `json:"revision_id"`
	Kind        core.SkillSafetySignalKind `json:"kind"`
	Verified    bool                       `json:"verified"`
}

type SkillSafetyResult struct {
	Signal     core.SkillSafetySignal `json:"signal"`
	Activation *core.SkillActivation  `json:"activation,omitempty"`
}

type skillSafetyRepositoryContract interface {
	GetSkillActivation(context.Context, string, string, string) (core.SkillActivation, error)
	GetSkillRevision(context.Context, string, string) (core.SkillRevision, error)
	GetSkillSafetySignal(context.Context, string, string) (core.SkillSafetySignal, error)
	CreateSkillSafetySignal(context.Context, core.SkillSafetySignal) error
	UpdateSkillSafetySignal(context.Context, core.SkillSafetySignal) error
	DisableSkillRevisionForSafety(context.Context, string, string, string, string) error
}

type SkillSafetyObserver struct {
	repository skillSafetyRepositoryContract
	activator  skillRevisionActivator
	cooldown   time.Duration
	now        func() time.Time
}

func NewSkillSafetyObserver(repository skillSafetyRepositoryContract, activator skillRevisionActivator, cooldown time.Duration, now func() time.Time) *SkillSafetyObserver {
	if now == nil {
		now = time.Now
	}
	return &SkillSafetyObserver{repository: repository, activator: activator, cooldown: cooldown, now: now}
}

func (o *SkillSafetyObserver) Observe(ctx context.Context, observation SkillSafetyObservation) (SkillSafetyResult, error) {
	if o == nil || o.repository == nil || o.activator == nil || o.cooldown <= 0 {
		return SkillSafetyResult{}, errors.New("skill safety observer dependencies and cooldown are required")
	}
	if strings.TrimSpace(observation.ID) == "" || strings.TrimSpace(observation.Workspace) == "" || strings.TrimSpace(observation.Environment) == "" || strings.TrimSpace(observation.SkillID) == "" || strings.TrimSpace(observation.RevisionID) == "" || !observation.Kind.Valid() || !observation.Verified {
		return SkillSafetyResult{}, errors.New("skill safety observation is invalid or unverified")
	}
	existing, err := o.repository.GetSkillSafetySignal(ctx, observation.Workspace, observation.ID)
	if err == nil && existing.State == core.SkillSafetyResolved {
		return SkillSafetyResult{Signal: existing}, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SkillSafetyResult{}, err
	}
	now := o.now().UTC()
	signal := existing
	if signal.ID == "" {
		signal = core.SkillSafetySignal{ID: observation.ID, Workspace: observation.Workspace, Environment: observation.Environment, SkillID: observation.SkillID, RevisionID: observation.RevisionID, Kind: observation.Kind, Verified: true, Occurrences: 1, CreatedAt: now, UpdatedAt: now}
		if observation.Kind.Hard() {
			signal.State = core.SkillSafetyRollbackPending
		} else {
			signal.State, signal.CooldownUntil = core.SkillSafetyCooldown, now.Add(o.cooldown)
		}
		if err := signal.Validate(); err != nil {
			return SkillSafetyResult{}, err
		}
		if err := o.repository.CreateSkillSafetySignal(ctx, signal); err != nil {
			return SkillSafetyResult{}, err
		}
	}
	if !signal.Kind.Hard() {
		return SkillSafetyResult{Signal: signal}, nil
	}
	revision, err := o.repository.GetSkillRevision(ctx, observation.Workspace, observation.RevisionID)
	if err != nil || revision.SkillID != observation.SkillID {
		return SkillSafetyResult{}, errors.New("skill safety revision is unavailable")
	}
	activation, err := o.repository.GetSkillActivation(ctx, observation.Workspace, observation.Environment, observation.SkillID)
	if err != nil {
		return SkillSafetyResult{}, err
	}
	if err := o.repository.DisableSkillRevisionForSafety(ctx, observation.Workspace, observation.Environment, observation.SkillID, observation.RevisionID); err != nil {
		return SkillSafetyResult{}, err
	}
	if activation.ActiveRevisionID != observation.RevisionID {
		signal.State, signal.UpdatedAt, signal.LastError = core.SkillSafetyResolved, now, ""
		if err := o.repository.UpdateSkillSafetySignal(ctx, signal); err != nil {
			return SkillSafetyResult{}, err
		}
		return SkillSafetyResult{Signal: signal}, nil
	}
	if activation.LastKnownGoodRevisionID == "" || activation.LastKnownGoodRevisionID == observation.RevisionID {
		return SkillSafetyResult{Signal: signal}, errors.New("no distinct last-known-good revision is available")
	}
	rolledBack, rollbackErr := o.activator.Activate(ctx, SkillActivationRequest{OperationID: "safety-" + observation.ID, IdempotencyKey: "safety-" + observation.ID, Workspace: observation.Workspace, Environment: observation.Environment, SkillID: observation.SkillID, TargetRevisionID: activation.LastKnownGoodRevisionID, ExpectedGeneration: activation.Generation, PolicyDecisionID: "automatic-hard-failure", Actor: "skill-safety-observer", Rollback: true, Automatic: true, ReasonCode: string(observation.Kind)})
	signal.UpdatedAt = now
	if rollbackErr != nil {
		signal.State, signal.LastError = core.SkillSafetyRollbackFailed, boundedActivationError(rollbackErr)
		_ = o.repository.UpdateSkillSafetySignal(ctx, signal)
		return SkillSafetyResult{Signal: signal}, rollbackErr
	}
	signal.State, signal.LastError = core.SkillSafetyResolved, ""
	if err := o.repository.UpdateSkillSafetySignal(ctx, signal); err != nil {
		return SkillSafetyResult{}, err
	}
	return SkillSafetyResult{Signal: signal, Activation: &rolledBack}, nil
}
