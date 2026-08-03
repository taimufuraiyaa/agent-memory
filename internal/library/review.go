package library

import (
	"context"
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"strings"
	"time"
)

type KnowledgeReview struct {
	ID             string            `json:"id"`
	OrganizationID string            `json:"organization_id"`
	ProposalID     string            `json:"proposal_id"`
	State          core.ReviewState  `json:"state"`
	Version        int               `json:"version"`
	Policy         core.AccessPolicy `json:"policy"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

func (r KnowledgeReview) Validate() error {
	if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.OrganizationID) == "" || strings.TrimSpace(r.ProposalID) == "" || r.Version < 1 || r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
		return errors.New("knowledge review identity, organization, proposal, version, and timestamps are required")
	}
	if r.State != core.ReviewProposed && r.State != core.ReviewReviewed && r.State != core.ReviewApproved && r.State != core.ReviewRejected && r.State != core.ReviewSuperseded {
		return errors.New("invalid knowledge review state")
	}
	return r.Policy.Validate()
}

type ReviewTransition struct {
	ReviewID string           `json:"review_id"`
	From     core.ReviewState `json:"from"`
	To       core.ReviewState `json:"to"`
	Actor    core.Principal   `json:"actor"`
	Reason   string           `json:"reason"`
	At       time.Time        `json:"at"`
	Version  int              `json:"version"`
}
type KnowledgeReviewRepository interface {
	PutKnowledgeReview(context.Context, KnowledgeReview) error
	GetKnowledgeReview(context.Context, string) (KnowledgeReview, error)
	AppendReviewTransition(context.Context, ReviewTransition) error
	ListReviewTransitions(context.Context, string) ([]ReviewTransition, error)
}
type KnowledgeReviewService struct{ repository KnowledgeReviewRepository }

func NewKnowledgeReviewService(r KnowledgeReviewRepository) *KnowledgeReviewService {
	return &KnowledgeReviewService{repository: r}
}
func (s *KnowledgeReviewService) Create(ctx context.Context, r KnowledgeReview) error {
	if s == nil || s.repository == nil {
		return errors.New("review repository is required")
	}
	if r.State != core.ReviewProposed {
		return errors.New("new review must be proposed")
	}
	if err := r.Validate(); err != nil {
		return err
	}
	return s.repository.PutKnowledgeReview(ctx, r)
}
func (s *KnowledgeReviewService) Transition(ctx context.Context, scope core.AuthorizationScope, id string, to core.ReviewState, reason string, at time.Time) (KnowledgeReview, error) {
	r, err := s.repository.GetKnowledgeReview(ctx, id)
	if err != nil {
		return KnowledgeReview{}, err
	}
	if !core.Authorize(scope, r.Policy, core.CapabilityApproveKnowledge).Allowed {
		return KnowledgeReview{}, errors.New("knowledge review not found")
	}
	if strings.TrimSpace(reason) == "" || at.IsZero() || !validReviewTransition(r.State, to) {
		return KnowledgeReview{}, errors.New("invalid knowledge review transition")
	}
	transition := ReviewTransition{ReviewID: id, From: r.State, To: to, Actor: scope.Principal, Reason: reason, At: at, Version: r.Version + 1}
	if err := s.repository.AppendReviewTransition(ctx, transition); err != nil {
		return KnowledgeReview{}, err
	}
	r.State, r.Version, r.UpdatedAt = to, r.Version+1, at
	if err := s.repository.PutKnowledgeReview(ctx, r); err != nil {
		return KnowledgeReview{}, err
	}
	return r, nil
}
func validReviewTransition(from, to core.ReviewState) bool {
	return (from == core.ReviewProposed && (to == core.ReviewReviewed || to == core.ReviewRejected)) || (from == core.ReviewReviewed && (to == core.ReviewApproved || to == core.ReviewRejected)) || (from == core.ReviewApproved && to == core.ReviewSuperseded)
}
