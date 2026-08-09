package search

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrStaleVectorClaim = errors.New("vector projection claim is stale")

func (r *PostgresRepository) ClaimNextVector(ctx context.Context, tenant, projectionVersion string, at time.Time, lease time.Duration) (*VectorCandidate, error) {
	tx, err := r.begin(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var sourceID string
	var sourceVersion int64
	err = tx.QueryRow(ctx, `SELECT s.id::text,s.active_version
		FROM saas_sources s
		JOIN saas_source_versions v ON v.tenant_id=s.tenant_id AND v.source_id=s.id AND v.version=s.active_version AND v.published_at IS NOT NULL
		LEFT JOIN saas_source_projections p ON p.tenant_id=s.tenant_id AND p.source_id=s.id AND p.source_version=s.active_version AND p.projection_kind='vector'
		WHERE s.tenant_id=$1 AND s.state IN('indexing','ready') AND (
			p.source_id IS NULL OR p.projection_version<>$2 OR p.state='failed' AND (p.claimed_until IS NULL OR p.claimed_until<=$3) OR p.state='processing' AND p.claimed_until<=$3)
		ORDER BY s.updated_at,s.id FOR UPDATE OF s SKIP LOCKED LIMIT 1`, tenant, projectionVersion, at).Scan(&sourceID, &sourceVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tx.Commit(ctx)
	}
	if err != nil {
		return nil, err
	}
	claim := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO saas_source_projections
		(tenant_id,source_id,source_version,projection_kind,projection_version,state,document_count,safe_error_code,projected_at,claim_token,claimed_until)
		VALUES($1,$2,$3,'vector',$4,'processing',0,'',$5,$6,$7)
		ON CONFLICT(tenant_id,source_id,source_version,projection_kind) DO UPDATE SET
		projection_version=EXCLUDED.projection_version,state='processing',document_count=0,safe_error_code='',projected_at=EXCLUDED.projected_at,claim_token=EXCLUDED.claim_token,claimed_until=EXCLUDED.claimed_until`,
		tenant, sourceID, sourceVersion, projectionVersion, at, claim, at.Add(lease)); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,structural_node_id,text_content FROM saas_source_passages
		WHERE tenant_id=$1 AND source_id=$2 AND source_version=$3 ORDER BY id`, tenant, sourceID, sourceVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	passages := []VectorPassage{}
	for rows.Next() {
		var passage VectorPassage
		if err := rows.Scan(&passage.ID, &passage.StructuralNodeID, &passage.Text); err != nil {
			return nil, err
		}
		passages = append(passages, passage)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(passages) == 0 {
		return nil, errors.New("published source has no passages")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &VectorCandidate{TenantID: tenant, SourceID: sourceID, SourceVersion: sourceVersion, ClaimToken: claim, Passages: passages}, nil
}

func (r *PostgresRepository) CompleteVectorProjection(ctx context.Context, candidate VectorCandidate, provider, model string, dimensions int, records []VectorRecord, at time.Time) error {
	if dimensions != VectorDimensions || len(records) != len(candidate.Passages) || len(records) == 0 {
		return errors.New("vector projection result is incomplete")
	}
	tx, err := r.begin(ctx, candidate.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_sources s JOIN saas_source_versions v
		ON v.tenant_id=s.tenant_id AND v.source_id=s.id AND v.version=s.active_version
		WHERE s.tenant_id=$1 AND s.id=$2 AND s.active_version=$3 AND s.state IN('indexing','ready') AND v.published_at IS NOT NULL)`,
		candidate.TenantID, candidate.SourceID, candidate.SourceVersion).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrStaleVectorClaim
	}
	if _, err := tx.Exec(ctx, `DELETE FROM saas_vector_documents WHERE tenant_id=$1 AND source_id=$2`, candidate.TenantID, candidate.SourceID); err != nil {
		return err
	}
	for _, record := range records {
		literal, err := vectorLiteral(record.Embedding)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO saas_vector_documents
			(tenant_id,source_id,source_version,passage_id,structural_node_id,embedding,provider,model,dimensions,projected_at)
			VALUES($1,$2,$3,$4,$5,$6::vector,$7,$8,$9,$10)`, candidate.TenantID, candidate.SourceID, candidate.SourceVersion,
			record.PassageID, record.StructuralNodeID, literal, provider, model, dimensions, at); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE saas_source_projections SET state='ready',document_count=$6,safe_error_code='',projected_at=$7,claim_token=NULL,claimed_until=NULL
		WHERE tenant_id=$1 AND source_id=$2 AND source_version=$3 AND projection_kind='vector' AND state='processing' AND claim_token=$4 AND projection_version=$5`,
		candidate.TenantID, candidate.SourceID, candidate.SourceVersion, candidate.ClaimToken, vectorProjectionVersion(provider, model, dimensions), len(records), at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleVectorClaim
	}
	if err := markSourceReady(ctx, tx, candidate.TenantID, candidate.SourceID, candidate.SourceVersion, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func markSourceReady(ctx context.Context, tx pgx.Tx, tenantID, sourceID string, sourceVersion int64, at time.Time) error {
	ready, err := tx.Exec(ctx, `UPDATE saas_sources s SET state='ready',updated_at=$4
		WHERE s.tenant_id=$1 AND s.id=$2 AND s.active_version=$3 AND s.state='indexing'
		AND EXISTS(SELECT 1 FROM saas_source_projections p WHERE p.tenant_id=s.tenant_id AND p.source_id=s.id AND p.source_version=s.active_version AND p.projection_kind='fulltext' AND p.state='ready')
		AND EXISTS(SELECT 1 FROM saas_source_projections p WHERE p.tenant_id=s.tenant_id AND p.source_id=s.id AND p.source_version=s.active_version AND p.projection_kind='vector' AND p.state='ready')`,
		tenantID, sourceID, sourceVersion, at)
	if err != nil {
		return err
	}
	if ready.RowsAffected() == 1 {
		payload := fmt.Sprintf(`{"source_id":%q,"source_version":%d}`, sourceID, sourceVersion)
		if _, err := tx.Exec(ctx, `INSERT INTO saas_outbox(tenant_id,id,event_type,spec_version,aggregate_type,aggregate_id,payload,occurred_at,next_attempt_at)
			VALUES($1,$2,'source.ready','1.0','source',$3,$4::jsonb,$5,$5)`, tenantID, uuid.NewString(), sourceID, payload, at); err != nil {
			return err
		}
	}
	return nil
}

func (r *PostgresRepository) FailVectorProjection(ctx context.Context, candidate VectorCandidate, safeCode string, retryAt time.Time) error {
	tx, err := r.begin(ctx, candidate.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE saas_source_projections SET state='failed',safe_error_code=$5,claim_token=NULL,claimed_until=$6
		WHERE tenant_id=$1 AND source_id=$2 AND source_version=$3 AND projection_kind='vector' AND claim_token=$4`,
		candidate.TenantID, candidate.SourceID, candidate.SourceVersion, candidate.ClaimToken, safeCode, retryAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleVectorClaim
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) PurgeUnauthorizedVectors(ctx context.Context, tenant string) (int64, error) {
	tx, err := r.begin(ctx, tenant)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `DELETE FROM saas_vector_documents d USING saas_sources s
		WHERE d.tenant_id=$1 AND s.tenant_id=d.tenant_id AND s.id=d.source_id
		AND (s.state IN('disabled','deleting','deleted','failed') OR d.source_version<>s.active_version)`, tenant)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}

func (r *PostgresRepository) ResetVectorProjection(ctx context.Context, tenant, sourceID string) error {
	tx, err := r.begin(ctx, tenant)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM saas_vector_documents WHERE tenant_id=$1 AND source_id=$2`, tenant, sourceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM saas_source_projections WHERE tenant_id=$1 AND source_id=$2 AND projection_kind='vector'`, tenant, sourceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) SearchVectors(ctx context.Context, tenant string, authorizedSourceIDs []string, query []float32, limit int) ([]VectorHit, error) {
	if tenant == "" || len(authorizedSourceIDs) == 0 {
		return nil, errors.New("tenant and authorized source filters are required")
	}
	if len(query) != VectorDimensions || limit <= 0 || limit > 100 {
		return nil, errors.New("vector query shape or limit is invalid")
	}
	sourceIDs := make([]uuid.UUID, len(authorizedSourceIDs))
	for index, value := range authorizedSourceIDs {
		parsed, err := uuid.Parse(value)
		if err != nil {
			return nil, errors.New("authorized source ID is invalid")
		}
		sourceIDs[index] = parsed
	}
	literal, err := vectorLiteral(query)
	if err != nil {
		return nil, err
	}
	tx, err := r.begin(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT d.source_id::text,d.source_version,d.passage_id,p.text_content,1-(d.embedding <=> $3::vector) AS score
		FROM saas_vector_documents d
		JOIN saas_sources s ON s.tenant_id=d.tenant_id AND s.id=d.source_id AND s.active_version=d.source_version AND s.state='ready'
		JOIN saas_source_passages p ON p.tenant_id=d.tenant_id AND p.source_id=d.source_id AND p.source_version=d.source_version AND p.id=d.passage_id
		WHERE d.tenant_id=$1 AND d.source_id=ANY($2::uuid[])
		ORDER BY d.embedding <=> $3::vector,d.passage_id LIMIT $4`, tenant, sourceIDs, literal, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hits := []VectorHit{}
	for rows.Next() {
		var hit VectorHit
		if err := rows.Scan(&hit.SourceID, &hit.SourceVersion, &hit.PassageID, &hit.Text, &hit.Score); err != nil {
			return nil, err
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func vectorLiteral(values []float32) (string, error) {
	if len(values) != VectorDimensions {
		return "", fmt.Errorf("embedding dimensions=%d, want %d", len(values), VectorDimensions)
	}
	var builder strings.Builder
	builder.Grow(len(values) * 8)
	builder.WriteByte('[')
	for index, value := range values {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return "", errors.New("embedding contains a non-finite value")
		}
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(strconv.FormatFloat(float64(value), 'g', -1, 32))
	}
	builder.WriteByte(']')
	return builder.String(), nil
}
