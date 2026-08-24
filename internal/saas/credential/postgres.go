package credential

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Create(ctx context.Context, request auth.RequestContext, credential Credential, verifier []byte) error {
	tx, err := r.begin(ctx, request.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO saas_api_credentials
		(tenant_id,id,account_id,verifier_hash,label,scopes,expires_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, request.TenantID, credential.ID, request.AccountID, verifier,
		credential.Label, credential.Scopes, credential.ExpiresAt, credential.CreatedAt); err != nil {
		return err
	}
	if err := auditCredential(ctx, tx, request, "credential.create", credential.ID, credential.CreatedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) List(ctx context.Context, request auth.RequestContext) ([]Credential, error) {
	tx, err := r.begin(ctx, request.TenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT id::text,label,scopes,expires_at,revoked_at,created_at
		FROM saas_api_credentials WHERE tenant_id=$1 AND account_id=$2 ORDER BY created_at,id`, request.TenantID, request.AccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Credential, 0)
	for rows.Next() {
		var value Credential
		if err := rows.Scan(&value.ID, &value.Label, &value.Scopes, &value.ExpiresAt, &value.RevokedAt, &value.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) Revoke(ctx context.Context, request auth.RequestContext, credentialID string, at time.Time) error {
	tx, err := r.begin(ctx, request.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE saas_api_credentials SET revoked_at=COALESCE(revoked_at,$4)
		WHERE tenant_id=$1 AND account_id=$2 AND id=$3`, request.TenantID, request.AccountID, credentialID, at)
	if err != nil || tag.RowsAffected() != 1 {
		return ErrInvalidCredential
	}
	if err := auditCredential(ctx, tx, request, "credential.revoke", credentialID, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) Verify(ctx context.Context, tenantID, credentialID string, verifier []byte, now time.Time) (auth.Identity, auth.Membership, error) {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return auth.Identity{}, auth.Membership{}, ErrInvalidCredential
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var accountID, subject, role, accountState, tenantState, memberState string
	var scopes []string
	var expected []byte
	var expires time.Time
	var revoked *time.Time
	err = tx.QueryRow(ctx, `SELECT c.account_id::text,a.external_subject,m.role,c.scopes,c.verifier_hash,c.expires_at,c.revoked_at,a.state,t.state,m.state
		FROM saas_api_credentials c JOIN saas_accounts a ON a.id=c.account_id
		JOIN saas_memberships m ON m.tenant_id=c.tenant_id AND m.account_id=c.account_id
		JOIN saas_tenants t ON t.id=c.tenant_id WHERE c.tenant_id=$1 AND c.id=$2`, tenantID, credentialID).Scan(
		&accountID, &subject, &role, &scopes, &expected, &expires, &revoked, &accountState, &tenantState, &memberState)
	if errors.Is(err, pgx.ErrNoRows) || err != nil || revoked != nil || !now.Before(expires) || !verifierEqual(expected, verifier) || accountState != "active" || tenantState != "active" || memberState != "active" {
		return auth.Identity{}, auth.Membership{}, ErrInvalidCredential
	}
	return auth.Identity{SubjectID: subject, CredentialID: credentialID}, auth.Membership{AccountID: accountID, TenantID: tenantID, Role: role, Capabilities: scopes}, nil
}

func (r *PostgresRepository) begin(ctx context.Context, tenantID string) (pgx.Tx, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("credential store is not configured")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func auditCredential(ctx context.Context, tx pgx.Tx, request auth.RequestContext, operation, credentialID string, at time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO saas_audit_events
		(tenant_id,id,actor_type,actor_id,operation,outcome,request_id,correlation_id,target_type,target_id,occurred_at)
		VALUES ($1,$2,'member',$3,$4,'success',$5,$6,'api_credential',$7,$8)`, request.TenantID, uuid.NewString(), request.AccountID, operation, request.RequestID, request.TraceID, credentialID, at)
	return err
}
