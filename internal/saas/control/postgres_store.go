package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) ProvisionPersonalAccount(ctx context.Context, command ProvisionCommand) (PersonalAccount, error) {
	if s == nil || s.pool == nil {
		return PersonalAccount{}, errors.New("PostgreSQL account store is not configured")
	}
	if strings.TrimSpace(command.WorkspaceID) == "" {
		command.WorkspaceID = uuid.NewString()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PersonalAccount{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var accountID string
	var createdAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO saas_accounts
		(id, external_subject, verified_email, display_name, state, created_at, updated_at)
		VALUES ($1,$2,$3,$4,'active',$5,$5)
		ON CONFLICT (external_subject) DO UPDATE SET
			verified_email=EXCLUDED.verified_email,
			display_name=EXCLUDED.display_name,
			updated_at=EXCLUDED.updated_at
		RETURNING id::text, created_at`, command.AccountID, command.ExternalSubject, command.VerifiedEmail, command.DisplayName, command.OccurredAt).Scan(&accountID, &createdAt)
	if err != nil {
		return PersonalAccount{}, fmt.Errorf("upsert account: %w", err)
	}
	inserted := accountID == command.AccountID
	if !inserted {
		var tenantID string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM saas_tenants WHERE personal_owner_account_id=$1`, accountID).Scan(&tenantID); err != nil {
			return PersonalAccount{}, fmt.Errorf("load existing personal tenant id: %w", err)
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
			return PersonalAccount{}, fmt.Errorf("set existing tenant transaction context: %w", err)
		}
		var existing PersonalAccount
		err := tx.QueryRow(ctx, `SELECT a.id::text, t.id::text,
			COALESCE((SELECT w.id::text FROM saas_workspaces w WHERE w.tenant_id=t.id AND w.state='active' ORDER BY w.created_at LIMIT 1), ''),
			m.role, t.state, a.created_at
			FROM saas_accounts a
			JOIN saas_tenants t ON t.personal_owner_account_id=a.id
			JOIN saas_memberships m ON m.tenant_id=t.id AND m.account_id=a.id
			WHERE a.id=$1`, accountID).Scan(&existing.AccountID, &existing.TenantID, &existing.WorkspaceID, &existing.Role, &existing.State, &existing.CreatedAt)
		if err != nil {
			return PersonalAccount{}, fmt.Errorf("load existing personal tenant: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return PersonalAccount{}, err
		}
		return existing, nil
	}

	if _, err := tx.Exec(ctx, `INSERT INTO saas_tenants
		(id, kind, state, personal_owner_account_id, created_at, updated_at)
		VALUES ($1,'personal','active',$2,$3,$3)`, command.TenantID, command.AccountID, command.OccurredAt); err != nil {
		return PersonalAccount{}, fmt.Errorf("insert personal tenant: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", command.TenantID); err != nil {
		return PersonalAccount{}, fmt.Errorf("set tenant transaction context: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_memberships
		(tenant_id, account_id, role, state, created_at, updated_at)
		VALUES ($1,$2,'owner','active',$3,$3)`, command.TenantID, command.AccountID, command.OccurredAt); err != nil {
		return PersonalAccount{}, fmt.Errorf("insert owner membership: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_onboarding_states
		(tenant_id, account_id, step, updated_at) VALUES ($1,$2,'rights_attestation',$3)`, command.TenantID, command.AccountID, command.OccurredAt); err != nil {
		return PersonalAccount{}, fmt.Errorf("insert onboarding state: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_workspaces
		(tenant_id,id,name,state,created_at,updated_at) VALUES ($1,$2,'private','active',$3,$3)`,
		command.TenantID, command.WorkspaceID, command.OccurredAt); err != nil {
		return PersonalAccount{}, fmt.Errorf("insert private workspace: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_tenant_entitlements(tenant_id,updated_at) VALUES($1,$2)`, command.TenantID, command.OccurredAt); err != nil {
		return PersonalAccount{}, fmt.Errorf("insert tenant entitlements: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_subscriptions(tenant_id,plan_id,state,last_provider_event_at,updated_at) VALUES($1,'trial','trialing',$2,$2)`, command.TenantID, command.OccurredAt); err != nil {
		return PersonalAccount{}, fmt.Errorf("create trial subscription: %w", err)
	}
	payload, err := json.Marshal(map[string]string{"account_id": command.AccountID, "tenant_id": command.TenantID})
	if err != nil {
		return PersonalAccount{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_outbox
		(tenant_id,id,event_type,spec_version,aggregate_type,aggregate_id,payload,occurred_at,next_attempt_at)
		VALUES ($1,$2,'tenant.created','1.0','tenant',$1,$3,$4,$4)`, command.TenantID, uuid.NewString(), payload, command.OccurredAt); err != nil {
		return PersonalAccount{}, fmt.Errorf("append tenant outbox event: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_audit_events
		(tenant_id,id,actor_type,actor_id,operation,outcome,request_id,correlation_id,target_type,target_id,occurred_at)
		VALUES ($1::uuid,$2,'member',$3,'account.signup','success',$4,$4,'tenant',$1::text,$5)`, command.TenantID, uuid.NewString(), command.AccountID, command.RequestID, command.OccurredAt); err != nil {
		return PersonalAccount{}, fmt.Errorf("append signup audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PersonalAccount{}, err
	}
	return PersonalAccount{AccountID: command.AccountID, TenantID: command.TenantID, WorkspaceID: command.WorkspaceID, Role: "owner", State: "active", CreatedAt: createdAt}, nil
}

func (s *PostgresStore) FindPersonalAccount(ctx context.Context, externalSubject string) (PersonalAccount, error) {
	if s == nil || s.pool == nil {
		return PersonalAccount{}, auth.ErrTenantUnavailable
	}
	externalSubject = strings.TrimSpace(externalSubject)
	var accountID, tenantID string
	var createdAt time.Time
	err := s.pool.QueryRow(ctx, `SELECT a.id::text,t.id::text,a.created_at
		FROM saas_accounts a JOIN saas_tenants t ON t.personal_owner_account_id=a.id
		WHERE a.external_subject=$1 AND a.state='active' AND t.state='active'`, externalSubject).Scan(&accountID, &tenantID, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersonalAccount{}, auth.ErrTenantUnavailable
	}
	if err != nil {
		return PersonalAccount{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PersonalAccount{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		return PersonalAccount{}, err
	}
	account := PersonalAccount{AccountID: accountID, TenantID: tenantID, CreatedAt: createdAt, State: "active"}
	err = tx.QueryRow(ctx, `SELECT m.role,w.id::text
		FROM saas_memberships m JOIN saas_workspaces w ON w.tenant_id=m.tenant_id
		WHERE m.tenant_id=$1 AND m.account_id=$2 AND m.state='active' AND w.state='active'
		ORDER BY w.created_at LIMIT 1`, tenantID, accountID).Scan(&account.Role, &account.WorkspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersonalAccount{}, auth.ErrTenantUnavailable
	}
	if err != nil {
		return PersonalAccount{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PersonalAccount{}, err
	}
	return account, nil
}

func (s *PostgresStore) Resolve(ctx context.Context, externalSubject, selectedTenantID string) (auth.Membership, error) {
	if s == nil || s.pool == nil {
		return auth.Membership{}, auth.ErrTenantUnavailable
	}
	externalSubject = strings.TrimSpace(externalSubject)
	selectedTenantID = strings.TrimSpace(selectedTenantID)
	var tenantID string
	err := s.pool.QueryRow(ctx, `SELECT t.id::text
		FROM saas_accounts a JOIN saas_tenants t ON t.personal_owner_account_id=a.id
		WHERE a.external_subject=$1 AND ($2='' OR t.id::text=$2)`, externalSubject, selectedTenantID).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) || err != nil {
		return auth.Membership{}, auth.ErrTenantUnavailable
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return auth.Membership{}, auth.ErrTenantUnavailable
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		return auth.Membership{}, auth.ErrTenantUnavailable
	}
	var membership auth.Membership
	var accountState, tenantState, membershipState string
	err = tx.QueryRow(ctx, `SELECT m.account_id::text, m.tenant_id::text, m.role, a.state, t.state, m.state
		FROM saas_memberships m
		JOIN saas_accounts a ON a.id=m.account_id
		JOIN saas_tenants t ON t.id=m.tenant_id
		WHERE a.external_subject=$1 AND m.tenant_id=$2`, externalSubject, tenantID).Scan(
		&membership.AccountID, &membership.TenantID, &membership.Role, &accountState, &tenantState, &membershipState,
	)
	if err != nil || accountState != "active" || tenantState != "active" || membershipState != "active" {
		return auth.Membership{}, auth.ErrTenantUnavailable
	}
	membership.Capabilities = capabilitiesForRole(membership.Role)
	return membership, nil
}

func capabilitiesForRole(role string) []string {
	if role != "owner" {
		return nil
	}
	return []string{
		"account:manage", "credential:manage", "memory:read", "memory:write",
		"source:read", "source:write", "source:delete", "tenant:export",
		"graph:read", "graph:review", "graph:operate",
	}
}
