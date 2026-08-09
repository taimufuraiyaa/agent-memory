package source

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

func (r *PostgresRepository) ListSources(ctx context.Context, tenantID, workspaceID string) ([]SourceRecord, error) {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, sourceCatalogQuery+`
		AND (NULLIF($2,'') IS NULL OR s.workspace_id=NULLIF($2,'')::uuid)
		ORDER BY s.created_at DESC,s.id DESC`, tenantID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SourceRecord{}
	for rows.Next() {
		record, err := scanSourceRecord(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) GetSource(ctx context.Context, tenantID, sourceID string) (SourceRecord, error) {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return SourceRecord{}, auth.ErrTenantUnavailable
	}
	defer func() { _ = tx.Rollback(ctx) }()
	record, err := scanSourceRecord(tx.QueryRow(ctx, sourceCatalogQuery+` AND s.id=$2::uuid`, tenantID, sourceID))
	if err != nil {
		return SourceRecord{}, auth.ErrTenantUnavailable
	}
	return record, nil
}

const sourceCatalogQuery = `SELECT
	s.id::text,s.workspace_id::text,COALESCE(u.filename,''),COALESCE(v.media_type,u.media_type,''),s.state,s.rights_basis,
	a.id::text,a.policy_version,a.accepted_at,a.expires_at,s.active_version,
	COALESCE(v.content_sha256,''),COALESCE(v.parser_version,''),COALESCE(v.normalization_version,''),
	COALESCE(v.vault_encryption_version,''),v.published_at,
	COALESCE(NULLIF(j.safe_error_code,''),u.safe_error_code,''),
	COALESCE(v.vault_object_key,'')<>'',
	CASE WHEN s.state='deleted' THEN 'deleted'
	     WHEN s.state='deleting' THEN 'deleting'
	     WHEN COALESCE(v.vault_object_key,'')<>'' THEN 'retained_private_vault'
	     ELSE 'pending' END,
	s.created_at,s.updated_at
	FROM saas_sources s
	JOIN saas_attestation_receipts a ON a.tenant_id=s.tenant_id AND a.id=s.attestation_receipt_id
	LEFT JOIN saas_source_versions v ON v.tenant_id=s.tenant_id AND v.source_id=s.id AND v.version=s.active_version
	LEFT JOIN LATERAL (
		SELECT filename,media_type,safe_error_code FROM saas_upload_grants
		WHERE tenant_id=s.tenant_id AND source_id=s.id ORDER BY created_at DESC LIMIT 1
	) u ON true
	LEFT JOIN LATERAL (
		SELECT safe_error_code FROM saas_jobs
		WHERE tenant_id=s.tenant_id AND subject_type='source' AND subject_id=s.id ORDER BY created_at DESC LIMIT 1
	) j ON true
	WHERE s.tenant_id=$1`

type sourceScanner interface{ Scan(...any) error }

func scanSourceRecord(scanner sourceScanner) (SourceRecord, error) {
	var record SourceRecord
	err := scanner.Scan(
		&record.ID, &record.WorkspaceID, &record.Filename, &record.MediaType, &record.State, &record.RightsBasis,
		&record.AttestationReceiptID, &record.AttestationPolicyVersion, &record.AttestationAcceptedAt, &record.AttestationExpiresAt,
		&record.ActiveVersion, &record.ContentSHA256, &record.ParserVersion, &record.NormalizationVersion,
		&record.VaultEncryptionVersion, &record.PublishedAt, &record.SafeErrorCode, &record.HasRetainedOriginal,
		&record.RetentionState, &record.CreatedAt, &record.UpdatedAt,
	)
	return record, err
}

func (r *PostgresRepository) RetrySource(ctx context.Context, tenantID, sourceID string, at time.Time) error {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return auth.ErrTenantUnavailable
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var state, vaultKey, jobID, errorCode string
	err = tx.QueryRow(ctx, `SELECT s.state,v.vault_object_key,j.id::text,j.safe_error_code
		FROM saas_sources s
		JOIN saas_source_versions v ON v.tenant_id=s.tenant_id AND v.source_id=s.id AND v.version=s.active_version
		JOIN LATERAL (SELECT id,safe_error_code FROM saas_jobs WHERE tenant_id=s.tenant_id AND subject_type='source' AND subject_id=s.id AND job_type='source.extract' ORDER BY created_at DESC LIMIT 1) j ON true
		WHERE s.tenant_id=$1 AND s.id=$2::uuid FOR UPDATE OF s,v`, tenantID, sourceID).Scan(&state, &vaultKey, &jobID, &errorCode)
	if err != nil || state != "failed" || vaultKey == "" || (errorCode != "extraction_failed" && errorCode != "source_unavailable") {
		return auth.ErrTenantUnavailable
	}
	if _, err = tx.Exec(ctx, `UPDATE saas_jobs SET state='queued',available_at=$3,started_at=NULL,finished_at=NULL,lease_expires_at=NULL,safe_error_code='',updated_at=$3
		WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, jobID, at); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE saas_sources SET state='processing',updated_at=$3 WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, sourceID, at); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"source_id": sourceID})
	if _, err = tx.Exec(ctx, `INSERT INTO saas_outbox(tenant_id,id,event_type,spec_version,aggregate_type,aggregate_id,payload,occurred_at,next_attempt_at)
		VALUES($1,$2,'source.extraction_retry_requested','1.0','source',$3::uuid,$4,$5,$5)`, tenantID, uuid.NewString(), sourceID, payload, at); err != nil {
		return err
	}
	if err := sourceAuditWithType(ctx, tx, tenantID, "member", "source-owner", uuid.NewString(), sourceID, "source.retry_extraction", sourceID, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
