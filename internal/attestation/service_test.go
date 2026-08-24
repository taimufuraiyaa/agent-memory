package attestation

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceStatusLifecycle(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 5, 8, 0, 0, 0, time.UTC)
	repository := &memoryRepository{}
	service := NewService(repository, WithClock(func() time.Time { return now }))

	missing, err := service.Status(ctx, "account-1")
	if err != nil {
		t.Fatal(err)
	}
	if missing.State != StatusRequired || missing.Reason != ReasonMissing {
		t.Fatalf("missing receipt must require consent: %+v", missing)
	}

	accepted, err := service.Accept(ctx, "account-1", AcceptCommand{
		PolicyVersion:        CurrentPolicy().Version,
		AcceptedStatementIDs: requiredStatementIDs(CurrentPolicy()),
		RequestID:            "request-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.State != StatusActive || accepted.Receipt == nil {
		t.Fatalf("acceptance must activate receipt: %+v", accepted)
	}
	if got, want := accepted.Receipt.ExpiresAt, now.Add(30*24*time.Hour); !got.Equal(want) {
		t.Fatalf("expiration = %v, want %v", got, want)
	}

	now = now.Add(30*24*time.Hour - time.Nanosecond)
	active, err := service.Status(ctx, "account-1")
	if err != nil {
		t.Fatal(err)
	}
	if active.State != StatusActive {
		t.Fatalf("receipt must remain active before exact expiration: %+v", active)
	}

	now = now.Add(time.Nanosecond)
	expired, err := service.Status(ctx, "account-1")
	if err != nil {
		t.Fatal(err)
	}
	if expired.State != StatusExpired || expired.Reason != ReasonExpired {
		t.Fatalf("receipt must expire exactly at 30 days: %+v", expired)
	}
}

func TestServiceRequiresCurrentPolicyAndEveryStatement(t *testing.T) {
	now := time.Date(2026, time.August, 5, 8, 0, 0, 0, time.UTC)
	repository := &memoryRepository{}
	service := NewService(repository, WithClock(func() time.Time { return now }))

	_, err := service.Accept(context.Background(), "account-1", AcceptCommand{
		PolicyVersion:        "obsolete-policy",
		AcceptedStatementIDs: requiredStatementIDs(CurrentPolicy()),
	})
	if !errors.Is(err, ErrPolicyVersion) {
		t.Fatalf("obsolete policy error = %v, want ErrPolicyVersion", err)
	}

	ids := requiredStatementIDs(CurrentPolicy())
	_, err = service.Accept(context.Background(), "account-1", AcceptCommand{
		PolicyVersion:        CurrentPolicy().Version,
		AcceptedStatementIDs: ids[:len(ids)-1],
	})
	if !errors.Is(err, ErrIncompleteAcceptance) {
		t.Fatalf("incomplete acceptance error = %v, want ErrIncompleteAcceptance", err)
	}
	if len(repository.receipts) != 0 {
		t.Fatalf("invalid acceptance wrote %d receipts", len(repository.receipts))
	}
}

func TestServicePolicyChangeRequiresImmediateReconsent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 5, 8, 0, 0, 0, time.UTC)
	repository := &memoryRepository{}
	oldPolicy := CurrentPolicy()
	oldPolicy.Version = "rights-attestation-v0"
	oldPolicy.StatementDigest = policyDigest(oldPolicy)
	oldService := NewService(repository, WithPolicy(oldPolicy), WithClock(func() time.Time { return now }))
	if _, err := oldService.Accept(ctx, "account-1", AcceptCommand{PolicyVersion: oldPolicy.Version, AcceptedStatementIDs: requiredStatementIDs(oldPolicy)}); err != nil {
		t.Fatal(err)
	}

	status, err := NewService(repository, WithClock(func() time.Time { return now })).Status(ctx, "account-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StatusRequired || status.Reason != ReasonPolicyChanged {
		t.Fatalf("policy change must require immediate consent: %+v", status)
	}
}

func TestServiceIdempotentAcceptanceDoesNotShortenOrDuplicateActiveReceipt(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 5, 8, 0, 0, 0, time.UTC)
	repository := &memoryRepository{}
	service := NewService(repository, WithClock(func() time.Time { return now }))
	command := AcceptCommand{PolicyVersion: CurrentPolicy().Version, AcceptedStatementIDs: requiredStatementIDs(CurrentPolicy()), RequestID: "retry-1"}

	first, err := service.Accept(ctx, "account-1", command)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	second, err := service.Accept(ctx, "account-1", command)
	if err != nil {
		t.Fatal(err)
	}
	if first.Receipt.ID != second.Receipt.ID || len(repository.receipts) != 1 {
		t.Fatalf("retry duplicated receipt: first=%+v second=%+v count=%d", first.Receipt, second.Receipt, len(repository.receipts))
	}
}

type memoryRepository struct {
	receipts []Receipt
}

func (r *memoryRepository) LatestReceipt(_ context.Context, subjectID string) (*Receipt, error) {
	for index := len(r.receipts) - 1; index >= 0; index-- {
		if r.receipts[index].SubjectID == subjectID {
			receipt := r.receipts[index]
			return &receipt, nil
		}
	}
	return nil, nil
}

func (r *memoryRepository) AppendAcceptance(_ context.Context, receipt Receipt, _ AuditEvent) (Receipt, error) {
	r.receipts = append(r.receipts, receipt)
	return receipt, nil
}

func (r *memoryRepository) AppendAuditEvent(context.Context, AuditEvent) error { return nil }

func requiredStatementIDs(policy Policy) []string {
	ids := make([]string, 0, len(policy.Statements))
	for _, statement := range policy.Statements {
		ids = append(ids, statement.ID)
	}
	return ids
}
