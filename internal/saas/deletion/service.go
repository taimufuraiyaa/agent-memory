// Package deletion owns resumable, verified hosted deletion operations.
package deletion

import (
	"context"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

var ErrUnavailable = errors.New("deletion target is unavailable")
var Subsystems = []string{"object", "database", "index", "cache", "queue"}

type Operation struct {
	TenantID       string   `json:"-"`
	ID             string   `json:"id"`
	TargetType     string   `json:"target_type"`
	TargetID       string   `json:"target_id"`
	PolicyVersion  string   `json:"policy_version"`
	State          string   `json:"state"`
	VaultKeys      []string `json:"-"`
	QuarantineKeys []string `json:"-"`
	Pending        []string `json:"pending_subsystems"`
	Attempts       int      `json:"attempts"`
}
type Repository interface {
	RequestSource(context.Context, auth.RequestContext, string, string, string, time.Time) (Operation, bool, error)
	Claim(context.Context, string, time.Time) (*Operation, error)
	Confirm(context.Context, Operation, string, string, time.Time) error
	Fail(context.Context, Operation, string, string, time.Time) error
	Get(context.Context, string, string) (Operation, error)
}

func (s *Service) Get(ctx context.Context, operationID string) (Operation, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("source:delete") {
		return Operation{}, ErrUnavailable
	}
	return s.repository.Get(ctx, request.TenantID, operationID)
}

type Purger interface {
	Purge(context.Context, Operation) error
}
type Service struct {
	repository Repository
	purgers    map[string]Purger
	now        func() time.Time
}

func NewService(repository Repository, purgers map[string]Purger, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, purgers: purgers, now: now}
}
func (s *Service) RequestSource(ctx context.Context, sourceID, idempotencyKey string) (Operation, bool, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("source:delete") || idempotencyKey == "" {
		return Operation{}, false, ErrUnavailable
	}
	return s.repository.RequestSource(ctx, request, sourceID, idempotencyKey, "retention-v1", s.now().UTC())
}
func (s *Service) RunOnce(ctx context.Context, tenantID string) (bool, error) {
	op, err := s.repository.Claim(ctx, tenantID, s.now().UTC())
	if err != nil || op == nil {
		return false, err
	}
	pending := map[string]bool{}
	for _, subsystem := range op.Pending {
		pending[subsystem] = true
	}
	for _, subsystem := range Subsystems {
		if !pending[subsystem] {
			continue
		}
		purger := s.purgers[subsystem]
		if purger == nil {
			if err := s.repository.Fail(ctx, *op, subsystem, "purger_unavailable", s.now().UTC()); err != nil {
				return true, err
			}
			return true, nil
		}
		if err := purger.Purge(ctx, *op); err != nil {
			if markErr := s.repository.Fail(ctx, *op, subsystem, "purge_failed", s.now().UTC()); markErr != nil {
				return true, markErr
			}
			return true, nil
		}
		if err := s.repository.Confirm(ctx, *op, subsystem, "verified", s.now().UTC()); err != nil {
			return true, err
		}
	}
	return true, nil
}
