package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/taimufuraiyaa/agent-memory/internal/ingestion"
)

func extractionJobKey(sourceID string, version int64) string {
	return fmt.Sprintf("source:%s:version:%d:extract", sourceID, version)
}

func (r *PostgresRepository) ClaimExtraction(ctx context.Context, tenant string, at time.Time, lease time.Duration) (*ExtractionClaim, error) {
	tx, err := r.begin(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		INSERT INTO saas_jobs(tenant_id,id,job_type,subject_type,subject_id,deterministic_key,state,available_at,created_at,updated_at)
		SELECT s.tenant_id,gen_random_uuid(),'source.extract','source',s.id,
		       'source:'||s.id::text||':version:'||v.version::text||':extract','queued',$2,$2,$2
		FROM saas_sources s
		JOIN saas_source_versions v ON v.tenant_id=s.tenant_id AND v.source_id=s.id AND v.version=s.active_version
		WHERE s.tenant_id=$1 AND s.state='processing' AND v.published_at IS NULL
		ON CONFLICT(tenant_id,deterministic_key) DO NOTHING`, tenant, at)
	if err != nil {
		return nil, err
	}

	var claim ExtractionClaim
	err = tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT j.id,s.id source_id,v.version,v.media_type,v.vault_object_key,v.vault_encryption_version
			FROM saas_jobs j
			JOIN saas_sources s ON s.tenant_id=j.tenant_id AND s.id=j.subject_id
			JOIN saas_source_versions v ON v.tenant_id=s.tenant_id AND v.source_id=s.id AND v.version=s.active_version
			WHERE j.tenant_id=$1 AND j.job_type='source.extract' AND s.state='processing' AND v.published_at IS NULL
			  AND ((j.state IN ('queued','failed') AND j.available_at<=$2)
			       OR (j.state='running' AND j.lease_expires_at<=$2))
			ORDER BY j.available_at,j.created_at,j.id
			FOR UPDATE OF j SKIP LOCKED LIMIT 1
		)
		UPDATE saas_jobs j SET state='running',attempts=j.attempts+1,started_at=$2,
		       lease_expires_at=$3,updated_at=$2,safe_error_code=''
		FROM candidate c WHERE j.tenant_id=$1 AND j.id=c.id
		RETURNING $1::text,c.source_id::text,c.version,c.media_type,c.vault_object_key,c.vault_encryption_version`, tenant, at, at.Add(lease)).
		Scan(&claim.TenantID, &claim.SourceID, &claim.Version, &claim.MediaType, &claim.VaultObjectKey, &claim.EncryptionVersion)
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

func (r *PostgresRepository) PublishExtraction(ctx context.Context, claim ExtractionClaim, extraction ingestion.BookExtraction, at time.Time) error {
	tx, err := r.begin(ctx, claim.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var publishedAt *time.Time
	var sourceState string
	err = tx.QueryRow(ctx, `
		SELECT v.published_at,s.state
		FROM saas_source_versions v
		JOIN saas_sources s ON s.tenant_id=v.tenant_id AND s.id=v.source_id
		WHERE v.tenant_id=$1 AND v.source_id=$2 AND v.version=$3
		FOR UPDATE OF v,s`, claim.TenantID, claim.SourceID, claim.Version).Scan(&publishedAt, &sourceState)
	if err != nil {
		return err
	}
	if publishedAt != nil {
		_, err = tx.Exec(ctx, `UPDATE saas_jobs SET state='succeeded',finished_at=$4,lease_expires_at=NULL,updated_at=$4
			WHERE tenant_id=$1 AND deterministic_key=$2 AND subject_id=$3`, claim.TenantID, extractionJobKey(claim.SourceID, claim.Version), claim.SourceID, at)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if sourceState != "processing" {
		return errors.New("source is not publishable")
	}

	for _, node := range extraction.Nodes {
		_, err = tx.Exec(ctx, `INSERT INTO saas_source_nodes
			(tenant_id,source_id,source_version,id,parent_id,kind,ordinal,title,start_offset,end_offset,explicit)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			claim.TenantID, claim.SourceID, claim.Version, node.ID, node.ParentID, node.Kind, node.Ordinal, node.Title, node.StartOffset, node.EndOffset, node.Explicit)
		if err != nil {
			return err
		}
	}
	for _, passage := range extraction.Passages {
		locator, err := json.Marshal(passage.Locator)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO saas_source_passages
			(tenant_id,source_id,source_version,id,structural_node_id,text_content,fingerprint,locator)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, claim.TenantID, claim.SourceID, claim.Version, passage.ID, passage.StructuralNodeID, passage.Text, passage.Fingerprint, locator)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO saas_source_citations
			(tenant_id,source_id,source_version,id,passage_id,structural_node_id,passage_fingerprint,locator)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, claim.TenantID, claim.SourceID, claim.Version, "citation:"+passage.ID, passage.ID, passage.StructuralNodeID, passage.Fingerprint, locator)
		if err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE saas_source_versions SET parser_version=$4,normalization_version=$5,published_at=$6
		WHERE tenant_id=$1 AND source_id=$2 AND version=$3 AND published_at IS NULL`, claim.TenantID, claim.SourceID, claim.Version, extraction.ParserVersion, extraction.NormalizationVersion, at); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE saas_sources SET state='indexing',updated_at=$3
		WHERE tenant_id=$1 AND id=$2 AND state='processing'`, claim.TenantID, claim.SourceID, at); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE saas_jobs SET state='succeeded',finished_at=$4,lease_expires_at=NULL,updated_at=$4
		WHERE tenant_id=$1 AND deterministic_key=$2 AND subject_id=$3 AND state='running'`, claim.TenantID, extractionJobKey(claim.SourceID, claim.Version), claim.SourceID, at); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"source_id": claim.SourceID, "source_version": claim.Version, "passage_count": len(extraction.Passages)})
	for _, eventType := range []string{"source.passages_published", "source.indexing_requested"} {
		if _, err = tx.Exec(ctx, `INSERT INTO saas_outbox
			(tenant_id,id,event_type,spec_version,aggregate_type,aggregate_id,payload,occurred_at,next_attempt_at)
			VALUES($1,$2,$3,'1.0','source',$4,$5,$6,$6)`, claim.TenantID, uuid.NewString(), eventType, claim.SourceID, payload, at); err != nil {
			return err
		}
	}
	if err := sourceAuditWithType(ctx, tx, claim.TenantID, "system", "source-extractor", extractionJobKey(claim.SourceID, claim.Version), claim.SourceID, "source.publish_passages", claim.SourceID, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) FailExtraction(ctx context.Context, claim ExtractionClaim, code string, at time.Time) error {
	tx, err := r.begin(ctx, claim.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `UPDATE saas_jobs SET state='failed',finished_at=$4,lease_expires_at=NULL,safe_error_code=$5,updated_at=$4
		WHERE tenant_id=$1 AND deterministic_key=$2 AND subject_id=$3`, claim.TenantID, extractionJobKey(claim.SourceID, claim.Version), claim.SourceID, at, code); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE saas_sources SET state='failed',updated_at=$3 WHERE tenant_id=$1 AND id=$2 AND state='processing'`, claim.TenantID, claim.SourceID, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
