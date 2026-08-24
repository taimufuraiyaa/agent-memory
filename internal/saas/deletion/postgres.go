package deletion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retention"
)

type PostgresRepository struct {
	pool     *pgxpool.Pool
	policies *retention.Registry
}

func NewPostgresRepository(pool *pgxpool.Pool, policies *retention.Registry) *PostgresRepository {
	return &PostgresRepository{pool: pool, policies: policies}
}

func (r *PostgresRepository) PendingTenantIDs(ctx context.Context) ([]string, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("deletion PostgreSQL repository is not configured")
	}
	rows, err := r.pool.Query(ctx, `SELECT DISTINCT tenant_id::text
		FROM saas_deletion_operations
		WHERE target_type='source' AND state IN ('revoked','purging','failed')
		ORDER BY tenant_id::text`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			return nil, err
		}
		result = append(result, tenantID)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) RequestSource(ctx context.Context, request auth.RequestContext, sourceID, idempotencyKey, policyVersion string, at time.Time) (Operation, bool, error) {
	tx, err := r.begin(ctx, request.TenantID)
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
	if !errors.Is(err, pgx.ErrNoRows) {
		return Operation{}, false, err
	}
	var state string
	if err = tx.QueryRow(ctx, `SELECT state FROM saas_sources WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, request.TenantID, sourceID).Scan(&state); err != nil || state == "deleted" {
		return Operation{}, false, ErrUnavailable
	}
	var held bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_legal_holds WHERE tenant_id=$1 AND target_type='source' AND target_id=$2 AND state='active')`, request.TenantID, sourceID).Scan(&held); err != nil {
		return Operation{}, false, err
	}
	id := uuid.NewString()
	operationState := "revoked"
	if held {
		operationState = "held"
	}
	_, err = tx.Exec(ctx, `INSERT INTO saas_deletion_operations(tenant_id,id,target_type,target_id,policy_version,state,requested_at,idempotency_key,next_attempt_at,access_revoked_at,updated_at) VALUES($1,$2,'source',$3,$4,$5,$6,$7,$6,$6,$6)`, request.TenantID, id, sourceID, policyVersion, operationState, at, idempotencyKey)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE saas_sources SET state='deleting',updated_at=$3 WHERE tenant_id=$1 AND id=$2`, request.TenantID, sourceID, at)
	}
	for _, subsystem := range Subsystems {
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO saas_deletion_confirmations(tenant_id,operation_id,subsystem,state,updated_at) VALUES($1,$2,$3,'pending',$4)`, request.TenantID, id, subsystem, at)
		}
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE saas_jobs SET state='cancelled',finished_at=$3,safe_error_code='source_deleting' WHERE tenant_id=$1 AND subject_id=$2 AND state IN ('queued','running')`, request.TenantID, sourceID, at)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO saas_outbox(tenant_id,id,event_type,spec_version,aggregate_type,aggregate_id,payload,occurred_at,next_attempt_at) VALUES($1,$2,'deletion.requested','1.0','source',$3,'{}',$4,$4)`, request.TenantID, uuid.NewString(), sourceID, at)
	}
	if err == nil {
		err = audit.Append(ctx, tx, audit.Event{TenantID: request.TenantID, OccurredAt: at, ActorType: "member", ActorID: request.AccountID, CredentialRef: request.CredentialID, SessionRef: request.SessionID, Service: "deletion", Operation: "deletion.request", Outcome: "success", RequestID: request.RequestID, TraceID: request.TraceID, TargetType: "source", TargetID: sourceID, PolicyVersion: policyVersion, ReasonCode: "user_requested", SafeMetadata: map[string]any{"legal_hold": held}})
	}
	if err != nil {
		return Operation{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Operation{}, false, err
	}
	return Operation{TenantID: request.TenantID, ID: id, TargetType: "source", TargetID: sourceID, PolicyVersion: policyVersion, State: operationState, Pending: append([]string(nil), Subsystems...)}, false, nil
}

func (r *PostgresRepository) Claim(ctx context.Context, tenantID string, at time.Time) (*Operation, error) {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var op Operation
	op.TenantID = tenantID
	err = tx.QueryRow(ctx, `WITH candidate AS(SELECT id FROM saas_deletion_operations WHERE tenant_id=$1 AND state IN ('revoked','purging','failed') AND next_attempt_at<=$2 ORDER BY requested_at FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE saas_deletion_operations d SET state='purging',attempts=attempts+1,updated_at=$2,next_attempt_at=$2+interval '5 minutes' FROM candidate c WHERE d.tenant_id=$1 AND d.id=c.id RETURNING d.id::text,d.target_type,d.target_id::text,d.policy_version,d.state,d.attempts`, tenantID, at).Scan(&op.ID, &op.TargetType, &op.TargetID, &op.PolicyVersion, &op.State, &op.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT subsystem FROM saas_deletion_confirmations WHERE tenant_id=$1 AND operation_id=$2 AND state<>'confirmed' ORDER BY subsystem`, tenantID, op.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		op.Pending = append(op.Pending, name)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT vault_object_key FROM saas_source_versions WHERE tenant_id=$1 AND source_id=$2 AND vault_object_key<>''`, tenantID, op.TargetID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		op.VaultKeys = append(op.VaultKeys, key)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT quarantine_object_key FROM saas_upload_grants WHERE tenant_id=$1 AND source_id=$2 AND quarantine_object_key<>''`, tenantID, op.TargetID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		op.QuarantineKeys = append(op.QuarantineKeys, key)
	}
	rows.Close()
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &op, nil
}

func (r *PostgresRepository) Get(ctx context.Context, tenantID, operationID string) (Operation, error) {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var op Operation
	op.TenantID = tenantID
	err = tx.QueryRow(ctx, `SELECT id::text,target_type,target_id::text,policy_version,state,attempts FROM saas_deletion_operations WHERE tenant_id=$1 AND id=$2`, tenantID, operationID).Scan(&op.ID, &op.TargetType, &op.TargetID, &op.PolicyVersion, &op.State, &op.Attempts)
	if err != nil {
		return Operation{}, ErrUnavailable
	}
	rows, err := tx.Query(ctx, `SELECT subsystem FROM saas_deletion_confirmations WHERE tenant_id=$1 AND operation_id=$2 AND state<>'confirmed' ORDER BY subsystem`, tenantID, operationID)
	if err != nil {
		return Operation{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return Operation{}, err
		}
		op.Pending = append(op.Pending, name)
	}
	return op, rows.Err()
}

func (r *PostgresRepository) Confirm(ctx context.Context, op Operation, subsystem, evidence string, at time.Time) error {
	tx, err := r.begin(ctx, op.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE saas_deletion_confirmations SET state='confirmed',evidence_code=$4,confirmed_at=$5,updated_at=$5 WHERE tenant_id=$1 AND operation_id=$2 AND subsystem=$3 AND state<>'confirmed'`, op.TenantID, op.ID, subsystem, evidence, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	var pending int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM saas_deletion_confirmations WHERE tenant_id=$1 AND operation_id=$2 AND state<>'confirmed'`, op.TenantID, op.ID).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return tx.Commit(ctx)
	}
	rows, err := tx.Query(ctx, `SELECT subsystem,evidence_code FROM saas_deletion_confirmations WHERE tenant_id=$1 AND operation_id=$2 ORDER BY subsystem`, op.TenantID, op.ID)
	if err != nil {
		return err
	}
	parts := []string{}
	for rows.Next() {
		var name, code string
		if err := rows.Scan(&name, &code); err != nil {
			rows.Close()
			return err
		}
		parts = append(parts, name+":"+code)
	}
	rows.Close()
	hash := sha256.Sum256([]byte(strings.Join(parts, "|")))
	receipt := hex.EncodeToString(hash[:])
	backupPolicy, err := r.policies.Active(ctx, "backups")
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE saas_deletion_operations SET state='completed',completed_at=$3,receipt_sha256=$4,safe_error_code='',updated_at=$3 WHERE tenant_id=$1 AND id=$2`, op.TenantID, op.ID, at, receipt)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE saas_sources SET state='deleted',updated_at=$3 WHERE tenant_id=$1 AND id=$2`, op.TenantID, op.TargetID, at)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO saas_deletion_tombstones(tenant_id,target_type,target_id,operation_id,deleted_at,receipt_sha256,backup_expires_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(tenant_id,target_type,target_id) DO UPDATE SET operation_id=EXCLUDED.operation_id,deleted_at=EXCLUDED.deleted_at,receipt_sha256=EXCLUDED.receipt_sha256,backup_expires_at=EXCLUDED.backup_expires_at`, op.TenantID, op.TargetType, op.TargetID, op.ID, at, receipt, at.Add(backupPolicy.Duration))
	}
	if err == nil {
		err = audit.Append(ctx, tx, audit.Event{TenantID: op.TenantID, OccurredAt: at, ActorType: "system", ActorID: "deletion-orchestrator", Service: "deletion", Operation: "deletion.complete", Outcome: "success", RequestID: uuid.NewString(), TraceID: uuid.NewString(), TargetType: op.TargetType, TargetID: op.TargetID, PolicyVersion: op.PolicyVersion, ReasonCode: "all_subsystems_verified", SafeMetadata: map[string]any{"receipt_sha256": receipt, "confirmation_count": len(parts)}})
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) Fail(ctx context.Context, op Operation, subsystem, code string, at time.Time) error {
	tx, err := r.begin(ctx, op.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `UPDATE saas_deletion_confirmations SET state='failed',evidence_code=$4,updated_at=$5 WHERE tenant_id=$1 AND operation_id=$2 AND subsystem=$3 AND state<>'confirmed'`, op.TenantID, op.ID, subsystem, code, at)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE saas_deletion_operations SET state='failed',safe_error_code=$3,next_attempt_at=$4,updated_at=$2 WHERE tenant_id=$1 AND id=$5`, op.TenantID, at, code, at.Add(time.Minute), op.ID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *PostgresRepository) begin(ctx context.Context, tenantID string) (pgx.Tx, error) {
	if r == nil || r.pool == nil || r.policies == nil {
		return nil, errors.New("deletion repository is not configured")
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

type DatabasePurger struct {
	pool *pgxpool.Pool
	kind string
}

func NewDatabasePurger(pool *pgxpool.Pool, kind string) *DatabasePurger {
	return &DatabasePurger{pool: pool, kind: kind}
}
func (p *DatabasePurger) Purge(ctx context.Context, op Operation) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", op.TenantID); err != nil {
		return err
	}
	switch p.kind {
	case "database":
		statements := []string{
			`DELETE FROM saas_passage_feedback WHERE tenant_id=$1 AND source_id=$2`,
			`DELETE FROM saas_passage_signals WHERE tenant_id=$1 AND source_id=$2`,
			`DELETE FROM saas_memory_proposals WHERE tenant_id=$1 AND evidence::text LIKE '%'||$2::text||'%'`,
			`DELETE FROM saas_lineage_edges WHERE tenant_id=$1 AND ((from_type='source' AND from_id=$2) OR (to_type='source' AND to_id=$2))`,
			`DELETE FROM saas_source_citations WHERE tenant_id=$1 AND source_id=$2`,
			`DELETE FROM saas_source_passages WHERE tenant_id=$1 AND source_id=$2`,
			`DELETE FROM saas_source_nodes WHERE tenant_id=$1 AND source_id=$2`,
			`DELETE FROM saas_source_versions WHERE tenant_id=$1 AND source_id=$2`,
			`DELETE FROM saas_upload_grants WHERE tenant_id=$1 AND source_id=$2`,
		}
		for _, statement := range statements {
			if _, err = tx.Exec(ctx, statement, op.TenantID, op.TargetID); err != nil {
				return err
			}
		}
	case "index":
		for _, statement := range []string{`DELETE FROM saas_vector_documents WHERE tenant_id=$1 AND source_id=$2`, `DELETE FROM saas_fulltext_documents WHERE tenant_id=$1 AND source_id=$2`, `DELETE FROM saas_source_projections WHERE tenant_id=$1 AND source_id=$2`} {
			if _, err = tx.Exec(ctx, statement, op.TenantID, op.TargetID); err != nil {
				return err
			}
		}
	case "queue":
		_, err = tx.Exec(ctx, `UPDATE saas_jobs SET state='cancelled',finished_at=clock_timestamp(),safe_error_code='source_deleted' WHERE tenant_id=$1 AND subject_id=$2 AND state IN ('queued','running')`, op.TenantID, op.TargetID)
	case "cache":
	default:
		return errors.New("unknown deletion subsystem")
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type ObjectStore interface {
	Delete(context.Context, string) error
	DeleteVault(context.Context, string) error
}
type ObjectPurger struct{ store ObjectStore }

func NewObjectPurger(store ObjectStore) *ObjectPurger { return &ObjectPurger{store: store} }
func (p *ObjectPurger) Purge(ctx context.Context, op Operation) error {
	for _, key := range op.QuarantineKeys {
		if err := p.store.Delete(ctx, key); err != nil {
			return err
		}
	}
	for _, key := range op.VaultKeys {
		if err := p.store.DeleteVault(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func sorted(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
