package source

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/attestation"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"time"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}
func (r *PostgresRepository) Issue(ctx context.Context, request auth.RequestContext, input GrantRequest, receipt attestation.Receipt, verifier []byte, objectKey string, at time.Time) (Grant, error) {
	tx, err := r.begin(ctx, request.TenantID)
	if err != nil {
		return Grant{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var quarantined bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_tenant_security_controls WHERE tenant_id=$1 AND uploads_quarantined_until>$2)`, request.TenantID, at).Scan(&quarantined); err != nil || quarantined {
		return Grant{}, errors.New("source uploads are temporarily quarantined")
	}
	var enabled bool
	var maxBytes, maxStorage int64
	var maxSources, maxConcurrent, maxJobs int
	if err := tx.QueryRow(ctx, `SELECT source_upload_enabled,max_source_bytes,max_source_count,max_concurrent_uploads,max_concurrent_jobs,max_storage_bytes FROM saas_tenant_entitlements WHERE tenant_id=$1`, request.TenantID).Scan(&enabled, &maxBytes, &maxSources, &maxConcurrent, &maxJobs, &maxStorage); err != nil || !enabled || input.SizeBytes > maxBytes {
		return Grant{}, errors.New("source upload entitlement or size limit exceeded")
	}
	var sourceCount, concurrent, activeJobs int
	var storageBytes int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM saas_sources WHERE tenant_id=$1 AND state NOT IN('deleted','deleting')`, request.TenantID).Scan(&sourceCount); err != nil {
		return Grant{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM saas_upload_grants WHERE tenant_id=$1 AND state IN('issued','uploading') AND expires_at>=$2`, request.TenantID, at).Scan(&concurrent); err != nil {
		return Grant{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(expected_size),0) FROM saas_upload_grants WHERE tenant_id=$1 AND state NOT IN('failed','expired')`, request.TenantID).Scan(&storageBytes); err != nil {
		return Grant{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM saas_jobs WHERE tenant_id=$1 AND state IN('queued','running')`, request.TenantID).Scan(&activeJobs); err != nil {
		return Grant{}, err
	}
	if sourceCount >= maxSources || concurrent >= maxConcurrent || activeJobs >= maxJobs || storageBytes+input.SizeBytes > maxStorage {
		return Grant{}, errors.New("source upload quota exceeded")
	}
	var workspace bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_workspaces WHERE tenant_id=$1 AND id=$2 AND state='active')`, request.TenantID, input.WorkspaceID).Scan(&workspace); err != nil || !workspace {
		return Grant{}, auth.ErrTenantUnavailable
	}
	sourceID, grantID := uuid.NewString(), uuid.NewString()
	expires := at.Add(10 * time.Minute)
	if _, err := tx.Exec(ctx, `INSERT INTO saas_sources(tenant_id,id,workspace_id,state,rights_basis,attestation_receipt_id,created_at,updated_at) VALUES($1,$2,$3,'pending',$4,$5,$6,$6)`, request.TenantID, sourceID, input.WorkspaceID, input.RightsBasis, receipt.ID, at); err != nil {
		return Grant{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_upload_grants(tenant_id,id,source_id,workspace_id,account_id,filename,media_type,expected_size,expected_sha256,rights_basis,attestation_receipt_id,token_hash,quarantine_object_key,state,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'issued',$14,$15)`, request.TenantID, grantID, sourceID, input.WorkspaceID, request.AccountID, input.Filename, input.MediaType, input.SizeBytes, input.ChecksumSHA256, input.RightsBasis, receipt.ID, verifier, objectKey, expires, at); err != nil {
		return Grant{}, err
	}
	if err := sourceAudit(ctx, tx, request.TenantID, request.AccountID, request.RequestID, request.TraceID, "source.upload_grant", sourceID, at); err != nil {
		return Grant{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Grant{}, err
	}
	return Grant{ID: grantID, SourceID: sourceID, TenantID: request.TenantID, ExpiresAt: expires, ExpectedSize: input.SizeBytes, MediaType: input.MediaType}, nil
}
func (r *PostgresRepository) ClaimUpload(ctx context.Context, tenantID, grantID string, verifier []byte, at time.Time) (UploadClaim, error) {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return UploadClaim{}, auth.ErrTenantUnavailable
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var quarantined bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_tenant_security_controls WHERE tenant_id=$1 AND uploads_quarantined_until>$2)`, tenantID, at).Scan(&quarantined); err != nil || quarantined {
		return UploadClaim{}, auth.ErrTenantUnavailable
	}
	var claim UploadClaim
	var expected []byte
	var state string
	var expires time.Time
	err = tx.QueryRow(ctx, `SELECT id::text,source_id::text,tenant_id::text,quarantine_object_key,media_type,expected_sha256,expected_size,token_hash,state,expires_at FROM saas_upload_grants WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, grantID).Scan(&claim.GrantID, &claim.SourceID, &claim.TenantID, &claim.ObjectKey, &claim.MediaType, &claim.Checksum, &claim.ExpectedSize, &expected, &state, &expires)
	if err != nil || state != "issued" || !at.Before(expires) || len(expected) != len(verifier) || subtle.ConstantTimeCompare(expected, verifier) != 1 {
		return UploadClaim{}, auth.ErrTenantUnavailable
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_upload_grants SET state='uploading',consumed_at=$3 WHERE tenant_id=$1 AND id=$2`, tenantID, grantID, at); err != nil {
		return UploadClaim{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_sources SET state='uploading',updated_at=$3 WHERE tenant_id=$1 AND id=$2`, tenantID, claim.SourceID, at); err != nil {
		return UploadClaim{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return UploadClaim{}, err
	}
	return claim, nil
}
func (r *PostgresRepository) CompleteUpload(ctx context.Context, claim UploadClaim, at time.Time) error {
	tx, err := r.begin(ctx, claim.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE saas_upload_grants SET state='uploaded' WHERE tenant_id=$1 AND id=$2 AND state='uploading'`, claim.TenantID, claim.GrantID)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("upload claim lost")
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_sources SET state='validating',updated_at=$3 WHERE tenant_id=$1 AND id=$2`, claim.TenantID, claim.SourceID, at); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"source_id": claim.SourceID, "grant_id": claim.GrantID})
	if _, err := tx.Exec(ctx, `INSERT INTO saas_outbox(tenant_id,id,event_type,spec_version,aggregate_type,aggregate_id,payload,occurred_at,next_attempt_at) VALUES($1,$2,'source.uploaded','1.0','source',$3,$4,$5,$5)`, claim.TenantID, uuid.NewString(), claim.SourceID, payload, at); err != nil {
		return err
	}
	if err := sourceAudit(ctx, tx, claim.TenantID, "upload-grant", claim.GrantID, claim.GrantID, "source.upload_complete", claim.SourceID, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *PostgresRepository) FailUpload(ctx context.Context, claim UploadClaim, code string, at time.Time) error {
	tx, err := r.begin(ctx, claim.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `UPDATE saas_upload_grants SET state='failed',safe_error_code=$3 WHERE tenant_id=$1 AND id=$2`, claim.TenantID, claim.GrantID, code)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE saas_sources SET state='failed',updated_at=$3 WHERE tenant_id=$1 AND id=$2`, claim.TenantID, claim.SourceID, at)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *PostgresRepository) ActiveTenantIDs(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT id::text FROM saas_tenants WHERE state='active' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (r *PostgresRepository) VaultReferences(ctx context.Context, tenant string) (map[string]int, error) {
	tx, err := r.begin(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT vault_object_key,count(*) FROM saas_source_versions WHERE tenant_id=$1 AND vault_object_key<>'' GROUP BY vault_object_key`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]int{}
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return nil, err
		}
		result[key] = count
	}
	return result, rows.Err()
}
func (r *PostgresRepository) ClaimValidation(ctx context.Context, tenant string, at time.Time) (*ValidationClaim, error) {
	tx, err := r.begin(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var claim ValidationClaim
	err = tx.QueryRow(ctx, `WITH candidate AS(SELECT id FROM saas_upload_grants WHERE tenant_id=$1 AND state='uploaded' ORDER BY consumed_at,id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE saas_upload_grants g SET state='validating' FROM candidate c WHERE g.tenant_id=$1 AND g.id=c.id RETURNING g.id::text,g.source_id::text,g.tenant_id::text,g.quarantine_object_key,g.media_type,g.expected_sha256,g.expected_size,g.filename`, tenant).Scan(&claim.GrantID, &claim.SourceID, &claim.TenantID, &claim.ObjectKey, &claim.MediaType, &claim.Checksum, &claim.ExpectedSize, &claim.Filename)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &claim, nil
}
func (r *PostgresRepository) Promote(ctx context.Context, claim ValidationClaim, vaultKey string, at time.Time) error {
	tx, err := r.begin(ctx, claim.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO saas_source_versions(tenant_id,source_id,version,content_sha256,media_type,parser_version,normalization_version,vault_object_key,vault_encryption_version,created_at) VALUES($1,$2,1,$3,$4,'pending','pending',$5,'aes-256-gcm-v1',$6)`, claim.TenantID, claim.SourceID, claim.Checksum, claim.MediaType, vaultKey, at); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_sources SET state='processing',active_version=1,updated_at=$3 WHERE tenant_id=$1 AND id=$2`, claim.TenantID, claim.SourceID, at); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_upload_grants SET state='promoted',safe_error_code='' WHERE tenant_id=$1 AND id=$2 AND state='validating'`, claim.TenantID, claim.GrantID); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"source_id": claim.SourceID})
	if _, err := tx.Exec(ctx, `INSERT INTO saas_outbox(tenant_id,id,event_type,spec_version,aggregate_type,aggregate_id,payload,occurred_at,next_attempt_at) VALUES($1,$2,'source.promoted','1.0','source',$3,$4,$5,$5)`, claim.TenantID, uuid.NewString(), claim.SourceID, payload, at); err != nil {
		return err
	}
	if err := sourceAudit(ctx, tx, claim.TenantID, "source-validator", claim.GrantID, claim.GrantID, "source.promote", claim.SourceID, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *PostgresRepository) Reject(ctx context.Context, claim ValidationClaim, code string, at time.Time) error {
	tx, err := r.begin(ctx, claim.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE saas_upload_grants SET state='failed',safe_error_code=$3 WHERE tenant_id=$1 AND id=$2`, claim.TenantID, claim.GrantID, code); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_sources SET state='failed',updated_at=$3 WHERE tenant_id=$1 AND id=$2`, claim.TenantID, claim.SourceID, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *PostgresRepository) ExpireGrants(ctx context.Context, tenant string, at time.Time) ([]string, error) {
	tx, err := r.begin(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `UPDATE saas_upload_grants SET state='expired',safe_error_code='grant_expired' WHERE tenant_id=$1 AND state='issued' AND expires_at<=$2 RETURNING source_id::text,quarantine_object_key`, tenant, at)
	if err != nil {
		return nil, err
	}
	type expired struct{ source, key string }
	items := []expired{}
	for rows.Next() {
		var item expired
		if err := rows.Scan(&item.source, &item.key); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	rows.Close()
	keys := []string{}
	for _, item := range items {
		if _, err := tx.Exec(ctx, `UPDATE saas_sources SET state='failed',updated_at=$3 WHERE tenant_id=$1 AND id=$2`, tenant, item.source, at); err != nil {
			return nil, err
		}
		keys = append(keys, item.key)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return keys, nil
}
func (r *PostgresRepository) ConfirmQuarantineDeleted(ctx context.Context, tenant, grantID string) error {
	tx, err := r.begin(ctx, tenant)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE saas_upload_grants SET quarantine_object_key='' WHERE tenant_id=$1 AND id=$2 AND state='promoted'`, tenant, grantID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *PostgresRepository) begin(ctx context.Context, tenant string) (pgx.Tx, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("source repository is not configured")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenant); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
func sourceAudit(ctx context.Context, tx pgx.Tx, tenant, actor, requestID, correlation, operation, target string, at time.Time) error {
	actorType := "member"
	if actor == "source-validator" || actor == "upload-grant" {
		actorType = "system"
	}
	return sourceAuditWithType(ctx, tx, tenant, actorType, actor, requestID, correlation, operation, target, at)
}
func sourceAuditWithType(ctx context.Context, tx pgx.Tx, tenant, actorType, actor, requestID, correlation, operation, target string, at time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO saas_audit_events(tenant_id,id,actor_type,actor_id,operation,outcome,request_id,correlation_id,target_type,target_id,occurred_at) VALUES($1,$2,$3,$4,$5,'success',$6,$7,'source',$8,$9)`, tenant, uuid.NewString(), actorType, actor, operation, requestID, correlation, target, at)
	return err
}
