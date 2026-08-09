package control

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

func (s *PostgresStore) StartSession(ctx context.Context, session Session) (Session, error) {
	tx, err := s.beginTenant(ctx, session.TenantID)
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireActiveMembership(ctx, tx, session.TenantID, session.AccountID); err != nil {
		return Session{}, err
	}
	var sessionID string
	err = tx.QueryRow(ctx, `INSERT INTO saas_sessions
		(tenant_id,id,account_id,provider_session_id,expires_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (tenant_id,provider_session_id) DO NOTHING
		RETURNING id::text`, session.TenantID, session.ID, session.AccountID, session.ProviderSessionID, session.ExpiresAt, session.OccurredAt).Scan(&sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id::text, expires_at, revoked_at, created_at FROM saas_sessions
			WHERE tenant_id=$1 AND provider_session_id=$2`, session.TenantID, session.ProviderSessionID).Scan(
			&session.ID, &session.ExpiresAt, &session.RevokedAt, &session.OccurredAt,
		)
		if err != nil {
			return Session{}, fmt.Errorf("load idempotent session: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return Session{}, err
		}
		return session, nil
	}
	if err != nil {
		return Session{}, fmt.Errorf("insert session: %w", err)
	}
	if err := appendControlAudit(ctx, tx, session.TenantID, session.AccountID, "session.login", session.RequestID, "session", sessionID, session.OccurredAt); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, err
	}
	session.ID = sessionID
	return session, nil
}

func (s *PostgresStore) ValidateSession(ctx context.Context, tenantID, sessionID string, now time.Time) (Session, error) {
	tx, err := s.beginTenant(ctx, tenantID)
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var session Session
	err = tx.QueryRow(ctx, `SELECT id::text, tenant_id::text, account_id::text, provider_session_id, expires_at, revoked_at, created_at
		FROM saas_sessions WHERE tenant_id=$1 AND id=$2`, tenantID, sessionID).Scan(
		&session.ID, &session.TenantID, &session.AccountID, &session.ProviderSessionID, &session.ExpiresAt, &session.RevokedAt, &session.OccurredAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, auth.ErrTenantUnavailable
	}
	if err != nil {
		return Session{}, err
	}
	if err := requireActiveMembership(ctx, tx, tenantID, session.AccountID); err != nil {
		return Session{}, err
	}
	if session.RevokedAt != nil {
		return Session{}, ErrSessionRevoked
	}
	if !now.UTC().Before(session.ExpiresAt.UTC()) {
		return Session{}, ErrSessionExpired
	}
	return session, nil
}

func (s *PostgresStore) RevokeSession(ctx context.Context, tenantID, sessionID, requestID string, occurredAt time.Time) error {
	tx, err := s.beginTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var accountID string
	var revokedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT account_id::text, revoked_at FROM saas_sessions
		WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, sessionID).Scan(&accountID, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.ErrTenantUnavailable
	}
	if err != nil {
		return err
	}
	if revokedAt != nil {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_sessions SET revoked_at=$3 WHERE tenant_id=$1 AND id=$2`, tenantID, sessionID, occurredAt); err != nil {
		return err
	}
	if err := appendControlAudit(ctx, tx, tenantID, accountID, "session.logout", requestID, "session", sessionID, occurredAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) ChangeVerifiedEmail(ctx context.Context, tenantID, accountID, email, requestID string, occurredAt time.Time) error {
	tx, err := s.beginTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireActiveMembership(ctx, tx, tenantID, accountID); err != nil {
		return err
	}
	if tag, err := tx.Exec(ctx, `UPDATE saas_accounts SET verified_email=$2, updated_at=$3 WHERE id=$1 AND state='active'`, accountID, email, occurredAt); err != nil || tag.RowsAffected() != 1 {
		return auth.ErrTenantUnavailable
	}
	if err := appendControlAudit(ctx, tx, tenantID, accountID, "account.email_changed", requestID, "account", accountID, occurredAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) RecoverAccount(ctx context.Context, session Session) (Session, error) {
	tx, err := s.beginTenant(ctx, session.TenantID)
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireActiveMembership(ctx, tx, session.TenantID, session.AccountID); err != nil {
		return Session{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_sessions SET revoked_at=COALESCE(revoked_at,$3)
		WHERE tenant_id=$1 AND account_id=$2`, session.TenantID, session.AccountID, session.OccurredAt); err != nil {
		return Session{}, fmt.Errorf("revoke sessions during recovery: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_sessions
		(tenant_id,id,account_id,provider_session_id,expires_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6)`, session.TenantID, session.ID, session.AccountID, session.ProviderSessionID, session.ExpiresAt, session.OccurredAt); err != nil {
		return Session{}, fmt.Errorf("insert recovery session: %w", err)
	}
	if err := appendControlAudit(ctx, tx, session.TenantID, session.AccountID, "account.recovered", session.RequestID, "account", session.AccountID, session.OccurredAt); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *PostgresStore) beginTenant(ctx context.Context, tenantID string) (pgx.Tx, error) {
	if s == nil || s.pool == nil || tenantID == "" {
		return nil, errors.New("PostgreSQL store and tenant context are required")
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

func requireActiveMembership(ctx context.Context, tx pgx.Tx, tenantID, accountID string) error {
	var active bool
	err := tx.QueryRow(ctx, `SELECT a.state='active' AND t.state='active' AND m.state='active'
		FROM saas_memberships m JOIN saas_accounts a ON a.id=m.account_id JOIN saas_tenants t ON t.id=m.tenant_id
		WHERE m.tenant_id=$1 AND m.account_id=$2`, tenantID, accountID).Scan(&active)
	if err != nil || !active {
		return auth.ErrTenantUnavailable
	}
	return nil
}

func appendControlAudit(ctx context.Context, tx pgx.Tx, tenantID, accountID, operation, requestID, targetType, targetID string, occurredAt time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO saas_audit_events
		(tenant_id,id,actor_type,actor_id,operation,outcome,request_id,correlation_id,target_type,target_id,occurred_at)
		VALUES ($1,$2,'member',$3,$4,'success',$5,$5,$6,$7,$8)`, tenantID, uuid.NewString(), accountID, operation, requestID, targetType, targetID, occurredAt)
	if err != nil {
		return fmt.Errorf("append control audit event: %w", err)
	}
	return nil
}
