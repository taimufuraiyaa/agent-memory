package attestation

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStorePersistsAppendOnlyReceiptsAndAcceptanceAudit(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteStore(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	acceptedAt := time.Date(2026, time.August, 5, 8, 0, 0, 0, time.UTC)
	receipt := Receipt{
		ID: "receipt-1", SubjectID: "account-1", PolicyVersion: "policy-v1",
		StatementDigest: "digest", AcceptedStatementIDs: []string{"ownership", "private-use"},
		AcceptedAt: acceptedAt, ExpiresAt: acceptedAt.Add(30 * 24 * time.Hour), RequestID: "request-1",
	}
	written, err := store.AppendAcceptance(ctx, receipt, AuditEvent{ID: "audit-1", SubjectID: "account-1", Operation: "rights_attestation_accept", Outcome: "success", PolicyVersion: "policy-v1", ReceiptID: "receipt-1", OccurredAt: acceptedAt})
	if err != nil {
		t.Fatal(err)
	}
	if written.ID != receipt.ID {
		t.Fatalf("written receipt = %+v", written)
	}

	latest, err := store.LatestReceipt(ctx, "account-1")
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.ID != "receipt-1" || len(latest.AcceptedStatementIDs) != 2 {
		t.Fatalf("latest receipt = %+v", latest)
	}

	var receiptCount, auditCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attestation_receipts`).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attestation_audit_events`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if receiptCount != 1 || auditCount != 1 {
		t.Fatalf("receipt count=%d audit count=%d", receiptCount, auditCount)
	}
}

func TestSQLiteStoreRollsBackReceiptWhenAcceptanceAuditFails(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteStore(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER reject_attestation_audit BEFORE INSERT ON attestation_audit_events BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 5, 8, 0, 0, 0, time.UTC)
	_, err = store.AppendAcceptance(ctx,
		Receipt{ID: "receipt-1", SubjectID: "account-1", PolicyVersion: "policy-v1", StatementDigest: "digest", AcceptedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)},
		AuditEvent{ID: "audit-1", SubjectID: "account-1", Operation: "rights_attestation_accept", Outcome: "success", OccurredAt: now},
	)
	if err == nil {
		t.Fatal("expected audit insert failure")
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attestation_receipts`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("receipt committed despite audit failure: %d", count)
	}
}

func TestSQLiteStoreAllowsRenewalWhenTransportRequestIDIsReused(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteStore(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	now := time.Date(2026, time.August, 5, 8, 0, 0, 0, time.UTC)
	service := NewService(store, WithClock(func() time.Time { return now }))
	command := AcceptCommand{PolicyVersion: CurrentPolicy().Version, AcceptedStatementIDs: requiredStatementIDs(CurrentPolicy()), RequestID: "transport-request"}
	first, err := service.Accept(ctx, "account-1", command)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(RenewalPeriod)
	second, err := service.Accept(ctx, "account-1", command)
	if err != nil {
		t.Fatal(err)
	}
	if first.Receipt.ID == second.Receipt.ID {
		t.Fatal("expired acceptance must renew even when a transport request ID is reused")
	}
}
