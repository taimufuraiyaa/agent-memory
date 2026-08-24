package attestation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func OpenSQLiteStore(ctx context.Context, path string) (*SQLiteStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("attestation control database path is required")
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, fmt.Errorf("open attestation control database: %w", err)
	}
	store := &SQLiteStore{db: db}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping attestation control database: %w", err)
	}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS attestation_receipts (
			id TEXT PRIMARY KEY,
			subject_id TEXT NOT NULL,
			policy_version TEXT NOT NULL,
			statement_digest TEXT NOT NULL,
			accepted_statement_ids_json TEXT NOT NULL,
			accepted_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			request_id TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_attestation_receipts_subject_accepted ON attestation_receipts(subject_id, accepted_at DESC)`,
		`DROP INDEX IF EXISTS idx_attestation_receipts_request`,
		`CREATE TABLE IF NOT EXISTS attestation_audit_events (
			id TEXT PRIMARY KEY,
			subject_id TEXT NOT NULL,
			operation TEXT NOT NULL,
			outcome TEXT NOT NULL,
			policy_version TEXT NOT NULL DEFAULT '',
			receipt_id TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			occurred_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_attestation_audit_subject_occurred ON attestation_audit_events(subject_id, occurred_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate attestation control database: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) LatestReceipt(ctx context.Context, subjectID string) (*Receipt, error) {
	var receipt Receipt
	var statementIDsJSON, acceptedAt, expiresAt string
	err := s.db.QueryRowContext(ctx, `SELECT id, subject_id, policy_version, statement_digest, accepted_statement_ids_json, accepted_at, expires_at, request_id, user_agent
		FROM attestation_receipts WHERE subject_id = ? ORDER BY accepted_at DESC, id DESC LIMIT 1`, strings.TrimSpace(subjectID)).Scan(
		&receipt.ID, &receipt.SubjectID, &receipt.PolicyVersion, &receipt.StatementDigest, &statementIDsJSON,
		&acceptedAt, &expiresAt, &receipt.RequestID, &receipt.UserAgent,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(statementIDsJSON), &receipt.AcceptedStatementIDs); err != nil {
		return nil, fmt.Errorf("decode accepted statement IDs: %w", err)
	}
	if receipt.AcceptedAt, err = time.Parse(time.RFC3339Nano, acceptedAt); err != nil {
		return nil, fmt.Errorf("parse accepted timestamp: %w", err)
	}
	if receipt.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt); err != nil {
		return nil, fmt.Errorf("parse expiration timestamp: %w", err)
	}
	return &receipt, nil
}

func (s *SQLiteStore) AppendAcceptance(ctx context.Context, receipt Receipt, event AuditEvent) (Receipt, error) {
	statementIDsJSON, err := json.Marshal(receipt.AcceptedStatementIDs)
	if err != nil {
		return Receipt{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Receipt{}, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO attestation_receipts
		(id, subject_id, policy_version, statement_digest, accepted_statement_ids_json, accepted_at, expires_at, request_id, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, receipt.ID, receipt.SubjectID, receipt.PolicyVersion, receipt.StatementDigest,
		string(statementIDsJSON), receipt.AcceptedAt.UTC().Format(time.RFC3339Nano), receipt.ExpiresAt.UTC().Format(time.RFC3339Nano), receipt.RequestID, receipt.UserAgent)
	if err != nil {
		return Receipt{}, fmt.Errorf("insert attestation receipt: %w", err)
	}
	if err := insertAuditEvent(ctx, tx, event); err != nil {
		return Receipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func (s *SQLiteStore) AppendAuditEvent(ctx context.Context, event AuditEvent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO attestation_audit_events
		(id, subject_id, operation, outcome, policy_version, receipt_id, request_id, reason, occurred_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, event.SubjectID, event.Operation, event.Outcome,
		event.PolicyVersion, event.ReceiptID, event.RequestID, event.Reason, event.OccurredAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) ListAuditEvents(ctx context.Context, subjectID string) ([]AuditEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, subject_id, operation, outcome, policy_version, receipt_id, request_id, reason, occurred_at
		FROM attestation_audit_events WHERE subject_id = ? ORDER BY occurred_at, id`, strings.TrimSpace(subjectID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	events := make([]AuditEvent, 0)
	for rows.Next() {
		var event AuditEvent
		var occurredAt string
		if err := rows.Scan(&event.ID, &event.SubjectID, &event.Operation, &event.Outcome, &event.PolicyVersion, &event.ReceiptID, &event.RequestID, &event.Reason, &occurredAt); err != nil {
			return nil, err
		}
		event.OccurredAt, err = time.Parse(time.RFC3339Nano, occurredAt)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertAuditEvent(ctx context.Context, executor sqlExecutor, event AuditEvent) error {
	_, err := executor.ExecContext(ctx, `INSERT INTO attestation_audit_events
		(id, subject_id, operation, outcome, policy_version, receipt_id, request_id, reason, occurred_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, event.SubjectID, event.Operation, event.Outcome,
		event.PolicyVersion, event.ReceiptID, event.RequestID, event.Reason, event.OccurredAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("insert attestation audit event: %w", err)
	}
	return nil
}
