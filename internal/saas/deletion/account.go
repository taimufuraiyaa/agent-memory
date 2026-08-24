package deletion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retention"
)

type AccountService struct {
	pool     *pgxpool.Pool
	sources  *PostgresRepository
	policies *retention.Registry
	now      func() time.Time
}

func NewAccountService(pool *pgxpool.Pool, sources *PostgresRepository, policies *retention.Registry, now func() time.Time) *AccountService {
	if now == nil {
		now = time.Now
	}
	return &AccountService{pool: pool, sources: sources, policies: policies, now: now}
}
func (s *AccountService) PendingTenantIDs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT tenant_id::text FROM saas_deletion_operations WHERE target_type='tenant' AND state IN ('revoked','purging','failed') ORDER BY tenant_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}
func (s *AccountService) Request(ctx context.Context, idempotencyKey string) (Operation, bool, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("account:manage") || idempotencyKey == "" {
		return Operation{}, false, ErrUnavailable
	}
	tx, err := s.begin(ctx, request.TenantID)
	if err != nil {
		return Operation{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var existing Operation
	err = tx.QueryRow(ctx, `SELECT id::text,target_type,target_id::text,policy_version,state,attempts FROM saas_deletion_operations WHERE tenant_id=$1 AND idempotency_key=$2`, request.TenantID, idempotencyKey).Scan(&existing.ID, &existing.TargetType, &existing.TargetID, &existing.PolicyVersion, &existing.State, &existing.Attempts)
	if err == nil {
		existing.TenantID = request.TenantID
		return existing, true, nil
	}
	var version, mode string
	var cooling int64
	if err = tx.QueryRow(ctx, `SELECT version,mode,cooling_off_seconds FROM saas_account_deletion_policies WHERE state='active'`).Scan(&version, &mode, &cooling); err != nil {
		return Operation{}, false, err
	}
	executeAt := s.now().UTC()
	if mode == "cooling_off" {
		executeAt = executeAt.Add(time.Duration(cooling) * time.Second)
	}
	id := uuid.NewString()
	at := s.now().UTC()
	_, err = tx.Exec(ctx, `INSERT INTO saas_deletion_operations(tenant_id,id,target_type,target_id,policy_version,state,requested_at,idempotency_key,next_attempt_at,access_revoked_at,updated_at,execute_after) VALUES($1,$2,'tenant',$1,$3,'revoked',$4,$5,$6,$4,$4,$6)`, request.TenantID, id, version, at, idempotencyKey, executeAt)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE saas_sessions SET revoked_at=COALESCE(revoked_at,$2) WHERE tenant_id=$1`, request.TenantID, at)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE saas_api_credentials SET revoked_at=COALESCE(revoked_at,$2) WHERE tenant_id=$1`, request.TenantID, at)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE saas_memberships SET state='suspended',updated_at=$2 WHERE tenant_id=$1`, request.TenantID, at)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE saas_accounts SET state='deleting',updated_at=$2 WHERE id=$1`, request.AccountID, at)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE saas_tenants SET state='deleting',updated_at=$2 WHERE id=$1`, request.TenantID, at)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE saas_subscriptions SET state='canceled',provider_customer_ref='',provider_subscription_ref='',updated_at=$2 WHERE tenant_id=$1`, request.TenantID, at)
	}
	if err == nil {
		err = audit.Append(ctx, tx, audit.Event{TenantID: request.TenantID, OccurredAt: at, ActorType: "member", ActorID: request.AccountID, SessionRef: request.SessionID, CredentialRef: request.CredentialID, Service: "deletion", Operation: "deletion.account.request", Outcome: "success", RequestID: request.RequestID, TraceID: request.TraceID, TargetType: "tenant", TargetID: request.TenantID, PolicyVersion: version, ReasonCode: mode, SafeMetadata: map[string]any{"execute_after": executeAt.Format(time.RFC3339)}})
	}
	if err != nil {
		return Operation{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Operation{}, false, err
	}
	return Operation{TenantID: request.TenantID, ID: id, TargetType: "tenant", TargetID: request.TenantID, PolicyVersion: version, State: "revoked"}, false, nil
}

func (s *AccountService) RunOnce(ctx context.Context, tenantID string) (bool, error) {
	tx, err := s.begin(ctx, tenantID)
	if err != nil {
		return false, err
	}
	var op Operation
	op.TenantID = tenantID
	var accountID string
	err = tx.QueryRow(ctx, `SELECT d.id::text,d.policy_version,t.personal_owner_account_id::text FROM saas_deletion_operations d JOIN saas_tenants t ON t.id=d.tenant_id WHERE d.tenant_id=$1 AND d.target_type='tenant' AND d.state IN ('revoked','purging','failed') AND d.execute_after<=$2 FOR UPDATE`, tenantID, s.now().UTC()).Scan(&op.ID, &op.PolicyVersion, &accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = tx.Rollback(ctx)
		return false, nil
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE saas_deletion_operations SET state='purging',attempts=attempts+1,updated_at=$2 WHERE tenant_id=$1 AND id=$3`, tenantID, s.now().UTC(), op.ID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return false, err
	}
	rows, err := tx.Query(ctx, `SELECT s.id::text,s.state FROM saas_sources s WHERE s.tenant_id=$1 AND NOT EXISTS(SELECT 1 FROM saas_legal_holds h WHERE h.tenant_id=s.tenant_id AND h.target_type='source' AND h.target_id=s.id AND h.state='active')`, tenantID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return false, err
	}
	type sourceState struct{ id, state string }
	sources := []sourceState{}
	for rows.Next() {
		var v sourceState
		if err := rows.Scan(&v.id, &v.state); err != nil {
			rows.Close()
			_ = tx.Rollback(ctx)
			return false, err
		}
		sources = append(sources, v)
	}
	rows.Close()
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	waiting := false
	for _, source := range sources {
		if source.state == "deleted" {
			continue
		}
		waiting = true
		if source.state != "deleting" {
			request := auth.RequestContext{TenantID: tenantID, AccountID: accountID, RequestID: uuid.NewString(), TraceID: uuid.NewString()}
			_, _, err = s.sources.RequestSource(ctx, request, source.id, "account-delete:"+op.ID+":"+source.id, "retention-v1", s.now().UTC())
			if err != nil {
				return true, err
			}
		}
	}
	if waiting {
		return true, nil
	}
	return true, s.finalize(ctx, op, accountID)
}

func (s *AccountService) finalize(ctx context.Context, op Operation, accountID string) error {
	tx, err := s.begin(ctx, op.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	at := s.now().UTC()
	statements := []string{`DELETE FROM saas_memory_proposals WHERE tenant_id=$1`, `DELETE FROM saas_passage_feedback WHERE tenant_id=$1`, `DELETE FROM saas_passage_signals WHERE tenant_id=$1`, `DELETE FROM saas_lineage_edges WHERE tenant_id=$1`, `DELETE FROM saas_feedback WHERE tenant_id=$1`, `DELETE FROM saas_memories WHERE tenant_id=$1`, `DELETE FROM saas_notes WHERE tenant_id=$1`, `DELETE FROM saas_sessions_memory WHERE tenant_id=$1`, `DELETE FROM saas_exports WHERE tenant_id=$1`, `DELETE FROM saas_plan_change_requests WHERE tenant_id=$1`}
	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement, op.TenantID); err != nil {
			return err
		}
	}
	hash := sha256.Sum256([]byte("deleted:" + accountID))
	pseudonym := "deleted-" + hex.EncodeToString(hash[:16])
	_, err = tx.Exec(ctx, `INSERT INTO saas_audit_pseudonymization(tenant_id,actor_id,pseudonym,operation_id,applied_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(tenant_id,actor_id) DO NOTHING`, op.TenantID, accountID, pseudonym, op.ID, at)
	receipt := sha256.Sum256([]byte(op.ID + "|" + pseudonym + "|" + at.UTC().Format(time.RFC3339Nano)))
	receiptText := hex.EncodeToString(receipt[:])
	backupPolicy, err := s.policies.Active(ctx, "backups")
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE saas_accounts SET external_subject=$2,verified_email=$3,state='deleted',updated_at=$4 WHERE id=$1`, accountID, pseudonym, pseudonym+"@invalid", at)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE saas_memberships SET state='revoked',updated_at=$2 WHERE tenant_id=$1`, op.TenantID, at)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE saas_tenants SET state='deleted',updated_at=$2 WHERE id=$1`, op.TenantID, at)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE saas_deletion_operations SET state='completed',completed_at=$3,receipt_sha256=$4,updated_at=$3 WHERE tenant_id=$1 AND id=$2`, op.TenantID, op.ID, at, receiptText)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO saas_deletion_tombstones(tenant_id,target_type,target_id,operation_id,deleted_at,receipt_sha256,backup_expires_at) VALUES($1,'tenant',$1,$2,$3,$4,$5)`, op.TenantID, op.ID, at, receiptText, at.Add(backupPolicy.Duration))
	}
	if err == nil {
		err = audit.Append(ctx, tx, audit.Event{TenantID: op.TenantID, OccurredAt: at, ActorType: "system", ActorID: "account-deletion", Service: "deletion", Operation: "deletion.account.complete", Outcome: "success", RequestID: uuid.NewString(), TraceID: uuid.NewString(), TargetType: "tenant", TargetID: op.TenantID, PolicyVersion: op.PolicyVersion, ReasonCode: "tenant_data_purged", SafeMetadata: map[string]any{"receipt_sha256": receiptText, "held_sources_preserved": true}})
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *AccountService) begin(ctx context.Context, tenantID string) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
