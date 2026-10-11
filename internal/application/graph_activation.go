package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var errGraphActivationConflict = errors.New("graph activation conflict")

type GraphActivationRepository interface {
	ActivateGraphRevision(context.Context, core.GraphActivation) error
	ActiveGraphRevisions(context.Context, core.GraphScope, string) (string, string, error)
}

type GraphActivationService struct {
	repository GraphActivationRepository
}

func NewGraphActivationService(repository GraphActivationRepository) *GraphActivationService {
	return &GraphActivationService{repository: repository}
}

func (s *GraphActivationService) Activate(ctx context.Context, activation core.GraphActivation) error {
	if s == nil || s.repository == nil {
		return fmt.Errorf("graph activation repository is required")
	}
	return s.repository.ActivateGraphRevision(ctx, activation)
}

func (s *GraphActivationService) Rollback(ctx context.Context, scope core.GraphScope, configurationID string) error {
	if s == nil || s.repository == nil {
		return fmt.Errorf("graph activation repository is required")
	}
	active, previous, err := s.repository.ActiveGraphRevisions(ctx, scope, configurationID)
	if err != nil {
		return err
	}
	if active == "" || previous == "" {
		return fmt.Errorf("no rollback graph revision is available")
	}
	return s.repository.ActivateGraphRevision(ctx, core.GraphActivation{
		Scope: scope, ConfigurationID: configurationID, ExpectedRevision: active, CandidateRevision: previous,
	})
}

type GraphFreshnessStatus struct {
	IndexedWatermark core.GraphWatermark
	CurrentWatermark core.GraphWatermark
	PendingChanges   int64
	Stale            bool
}

func GraphWatermarkFreshness(indexed, current core.GraphWatermark) GraphFreshnessStatus {
	pending := current.Sequence - indexed.Sequence
	if pending < 0 {
		pending = 0
	}
	return GraphFreshnessStatus{
		IndexedWatermark: indexed, CurrentWatermark: current, PendingChanges: pending,
		Stale: current.Sequence > indexed.Sequence || (current.Digest != "" && indexed.Digest != current.Digest),
	}
}
