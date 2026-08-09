package attestation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrPolicyVersion        = errors.New("rights attestation policy version is not current")
	ErrIncompleteAcceptance = errors.New("every rights attestation statement must be accepted")
	ErrAttestationRequired  = errors.New("current rights attestation is required")
)

type StatusState string
type StatusReason string

const (
	StatusRequired StatusState = "required"
	StatusActive   StatusState = "active"
	StatusExpired  StatusState = "expired"

	ReasonMissing       StatusReason = "missing"
	ReasonActive        StatusReason = "active"
	ReasonExpired       StatusReason = "expired"
	ReasonPolicyChanged StatusReason = "policy_changed"
)

type Receipt struct {
	ID                   string    `json:"id"`
	SubjectID            string    `json:"-"`
	PolicyVersion        string    `json:"policy_version"`
	StatementDigest      string    `json:"statement_digest"`
	AcceptedStatementIDs []string  `json:"accepted_statement_ids"`
	AcceptedAt           time.Time `json:"accepted_at"`
	ExpiresAt            time.Time `json:"expires_at"`
	RequestID            string    `json:"-"`
	UserAgent            string    `json:"-"`
}

type AuditEvent struct {
	ID            string
	SubjectID     string
	Operation     string
	Outcome       string
	PolicyVersion string
	ReceiptID     string
	RequestID     string
	Reason        string
	OccurredAt    time.Time
}

type Repository interface {
	LatestReceipt(context.Context, string) (*Receipt, error)
	AppendAcceptance(context.Context, Receipt, AuditEvent) (Receipt, error)
	AppendAuditEvent(context.Context, AuditEvent) error
}

type Status struct {
	State   StatusState  `json:"status"`
	Reason  StatusReason `json:"reason"`
	Policy  Policy       `json:"policy"`
	Receipt *Receipt     `json:"receipt,omitempty"`
}

type AcceptCommand struct {
	PolicyVersion        string
	AcceptedStatementIDs []string
	RequestID            string
	UserAgent            string
}

type Service struct {
	repository Repository
	policy     Policy
	now        func() time.Time
}

type Option func(*Service)

func WithClock(clock func() time.Time) Option { return func(service *Service) { service.now = clock } }
func WithPolicy(policy Policy) Option {
	return func(service *Service) {
		policy.StatementDigest = policyDigest(policy)
		service.policy = policy
	}
}

func NewService(repository Repository, options ...Option) *Service {
	service := &Service{repository: repository, policy: CurrentPolicy(), now: time.Now}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) Status(ctx context.Context, subjectID string) (Status, error) {
	if s == nil || s.repository == nil {
		return Status{}, errors.New("rights attestation repository is not configured")
	}
	subjectID = strings.TrimSpace(subjectID)
	if subjectID == "" {
		return Status{}, errors.New("rights attestation subject is required")
	}
	receipt, err := s.repository.LatestReceipt(ctx, subjectID)
	if err != nil {
		return Status{}, err
	}
	if receipt == nil {
		return Status{State: StatusRequired, Reason: ReasonMissing, Policy: s.policy}, nil
	}
	if receipt.PolicyVersion != s.policy.Version || receipt.StatementDigest != s.policy.StatementDigest {
		return Status{State: StatusRequired, Reason: ReasonPolicyChanged, Policy: s.policy}, nil
	}
	if !s.now().UTC().Before(receipt.ExpiresAt.UTC()) {
		return Status{State: StatusExpired, Reason: ReasonExpired, Policy: s.policy, Receipt: receipt}, nil
	}
	return Status{State: StatusActive, Reason: ReasonActive, Policy: s.policy, Receipt: receipt}, nil
}

func (s *Service) Accept(ctx context.Context, subjectID string, command AcceptCommand) (Status, error) {
	if strings.TrimSpace(command.PolicyVersion) != s.policy.Version {
		return Status{}, ErrPolicyVersion
	}
	if !acceptsExactly(s.policy, command.AcceptedStatementIDs) {
		return Status{}, ErrIncompleteAcceptance
	}
	current, err := s.Status(ctx, subjectID)
	if err != nil {
		return Status{}, err
	}
	if current.State == StatusActive {
		return current, nil
	}
	now := s.now().UTC()
	acceptedIDs := append([]string(nil), command.AcceptedStatementIDs...)
	sort.Strings(acceptedIDs)
	receipt := Receipt{
		ID: uuid.NewString(), SubjectID: strings.TrimSpace(subjectID), PolicyVersion: s.policy.Version,
		StatementDigest: s.policy.StatementDigest, AcceptedStatementIDs: acceptedIDs,
		AcceptedAt: now, ExpiresAt: now.Add(RenewalPeriod), RequestID: strings.TrimSpace(command.RequestID),
		UserAgent: strings.TrimSpace(command.UserAgent),
	}
	written, err := s.repository.AppendAcceptance(ctx, receipt, AuditEvent{
		ID: uuid.NewString(), SubjectID: receipt.SubjectID, Operation: "rights_attestation_accept",
		Outcome: "success", PolicyVersion: receipt.PolicyVersion, ReceiptID: receipt.ID,
		RequestID: receipt.RequestID, OccurredAt: now,
	})
	if err != nil {
		return Status{}, fmt.Errorf("append rights attestation: %w", err)
	}
	return Status{State: StatusActive, Reason: ReasonActive, Policy: s.policy, Receipt: &written}, nil
}

func (s *Service) RequireActive(ctx context.Context, subjectID string) (Receipt, error) {
	status, err := s.Status(ctx, subjectID)
	if err != nil {
		return Receipt{}, err
	}
	if status.State != StatusActive || status.Receipt == nil {
		return Receipt{}, fmt.Errorf("%w: %s", ErrAttestationRequired, status.Reason)
	}
	return *status.Receipt, nil
}

func (s *Service) RecordDecision(ctx context.Context, event AuditEvent) error {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = s.now().UTC()
	}
	return s.repository.AppendAuditEvent(ctx, event)
}

func acceptsExactly(policy Policy, accepted []string) bool {
	required := make(map[string]struct{}, len(policy.Statements))
	for _, statement := range policy.Statements {
		required[statement.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(accepted))
	for _, id := range accepted {
		id = strings.TrimSpace(id)
		if _, ok := required[id]; !ok {
			return false
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
	}
	return len(seen) == len(required)
}
