// Package attestationstore adapts hosted PostgreSQL to the attestation domain.
package attestationstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/attestation"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) LatestReceipt(ctx context.Context, subjectID string) (*attestation.Receipt, error) {
	request, err := authorize(ctx, subjectID)
	if err != nil {
		return nil, err
	}
	tx, err := s.beginTenant(ctx, request.TenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var receipt attestation.Receipt
	var accepted []byte
	err = tx.QueryRow(ctx, `SELECT id::text, subject_id::text, policy_version, statement_digest,
		accepted_statement_ids, accepted_at, expires_at, request_id, user_agent
		FROM saas_attestation_receipts
		WHERE tenant_id=$1 AND subject_id=$2
		ORDER BY accepted_at DESC, id DESC LIMIT 1`, request.TenantID, request.AccountID).Scan(
		&receipt.ID, &receipt.SubjectID, &receipt.PolicyVersion, &receipt.StatementDigest,
		&accepted, &receipt.AcceptedAt, &receipt.ExpiresAt, &receipt.RequestID, &receipt.UserAgent,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select hosted attestation receipt: %w", err)
	}
	if err := json.Unmarshal(accepted, &receipt.AcceptedStatementIDs); err != nil {
		return nil, fmt.Errorf("decode hosted accepted statements: %w", err)
	}
	return &receipt, nil
}

func (s *PostgresStore) AppendAcceptance(ctx context.Context, receipt attestation.Receipt, event attestation.AuditEvent) (attestation.Receipt, error) {
	request, err := authorize(ctx, receipt.SubjectID)
	if err != nil {
		return attestation.Receipt{}, err
	}
	accepted, err := json.Marshal(receipt.AcceptedStatementIDs)
	if err != nil {
		return attestation.Receipt{}, err
	}
	tx, err := s.beginTenant(ctx, request.TenantID)
	if err != nil {
		return attestation.Receipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO saas_attestation_receipts
		(tenant_id,id,subject_id,policy_version,statement_digest,accepted_statement_ids,accepted_at,expires_at,request_id,user_agent)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, request.TenantID, receipt.ID, request.AccountID,
		receipt.PolicyVersion, receipt.StatementDigest, accepted, receipt.AcceptedAt, receipt.ExpiresAt, receipt.RequestID, receipt.UserAgent); err != nil {
		return attestation.Receipt{}, fmt.Errorf("insert hosted attestation receipt: %w", err)
	}
	if err := insertAudit(ctx, tx, request.TenantID, event); err != nil {
		return attestation.Receipt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return attestation.Receipt{}, err
	}
	return receipt, nil
}

func (s *PostgresStore) AppendAuditEvent(ctx context.Context, event attestation.AuditEvent) error {
	request, err := authorize(ctx, event.SubjectID)
	if err != nil {
		return err
	}
	tx, err := s.beginTenant(ctx, request.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := insertAudit(ctx, tx, request.TenantID, event); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) beginTenant(ctx context.Context, tenantID string) (pgx.Tx, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("hosted attestation PostgreSQL store is not configured")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func authorize(ctx context.Context, subjectID string) (auth.RequestContext, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || request.AccountID == "" || request.TenantID == "" || request.AccountID != subjectID {
		return auth.RequestContext{}, errors.New("hosted attestation requires authenticated account and tenant context")
	}
	return request, nil
}

func insertAudit(ctx context.Context, tx pgx.Tx, tenantID string, event attestation.AuditEvent) error {
	var receiptID any
	if event.ReceiptID != "" {
		receiptID = event.ReceiptID
	}
	_, err := tx.Exec(ctx, `INSERT INTO saas_attestation_audit_events
		(tenant_id,id,subject_id,operation,outcome,policy_version,receipt_id,request_id,reason,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, tenantID, event.ID, event.SubjectID, event.Operation,
		event.Outcome, event.PolicyVersion, receiptID, event.RequestID, event.Reason, event.OccurredAt)
	if err != nil {
		return fmt.Errorf("insert hosted attestation audit event: %w", err)
	}
	return nil
}
