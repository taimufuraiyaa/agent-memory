package search

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
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

func (r *PostgresRepository) ProjectNextFullText(ctx context.Context, tenant, projectionVersion string, at time.Time) (bool, error) {
	tx, err := r.begin(ctx, tenant)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var sourceID string
	var sourceVersion int64
	err = tx.QueryRow(ctx, `SELECT s.id::text,s.active_version
		FROM saas_sources s
		JOIN saas_source_versions v ON v.tenant_id=s.tenant_id AND v.source_id=s.id AND v.version=s.active_version AND v.published_at IS NOT NULL
		LEFT JOIN saas_source_projections p ON p.tenant_id=s.tenant_id AND p.source_id=s.id AND p.source_version=s.active_version AND p.projection_kind='fulltext'
		WHERE s.tenant_id=$1 AND s.state IN('indexing','ready')
		  AND (p.source_id IS NULL OR p.projection_version<>$2 OR p.state<>'ready')
		ORDER BY s.updated_at,s.id FOR UPDATE OF s SKIP LOCKED LIMIT 1`, tenant, projectionVersion).Scan(&sourceID, &sourceVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM saas_fulltext_documents WHERE tenant_id=$1 AND source_id=$2`, tenant, sourceID); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO saas_fulltext_documents
		(tenant_id,source_id,source_version,passage_id,structural_node_id,text_content,locator,projected_at)
		SELECT tenant_id,source_id,source_version,id,structural_node_id,text_content,locator,$4
		FROM saas_source_passages WHERE tenant_id=$1 AND source_id=$2 AND source_version=$3`, tenant, sourceID, sourceVersion, at)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, errors.New("published source has no passages")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO saas_source_projections
		(tenant_id,source_id,source_version,projection_kind,projection_version,state,document_count,projected_at)
		VALUES($1,$2,$3,'fulltext',$4,'ready',$5,$6)
		ON CONFLICT(tenant_id,source_id,source_version,projection_kind) DO UPDATE
		SET projection_version=EXCLUDED.projection_version,state='ready',document_count=EXCLUDED.document_count,safe_error_code='',projected_at=EXCLUDED.projected_at`,
		tenant, sourceID, sourceVersion, projectionVersion, tag.RowsAffected(), at); err != nil {
		return false, err
	}
	if err := markSourceReady(ctx, tx, tenant, sourceID, sourceVersion, at); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (r *PostgresRepository) PurgeUnauthorizedFullText(ctx context.Context, tenant string) (int64, error) {
	tx, err := r.begin(ctx, tenant)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `DELETE FROM saas_fulltext_documents d
		USING saas_sources s WHERE d.tenant_id=$1 AND s.tenant_id=d.tenant_id AND s.id=d.source_id
		AND (s.state IN('disabled','deleting','deleted','failed') OR d.source_version<>s.active_version)`, tenant)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}

func (r *PostgresRepository) FullTextProjectionStats(ctx context.Context, tenant, version string) (ProjectionStats, error) {
	tx, err := r.begin(ctx, tenant)
	if err != nil {
		return ProjectionStats{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var stats ProjectionStats
	err = tx.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE p.source_id IS NULL),
		count(*) FILTER (WHERE p.state='ready' AND p.projection_version=$2),
		count(*) FILTER (WHERE p.source_id IS NOT NULL AND (p.state<>'ready' OR p.projection_version<>$2))
		FROM saas_sources s
		LEFT JOIN saas_source_projections p ON p.tenant_id=s.tenant_id AND p.source_id=s.id AND p.source_version=s.active_version AND p.projection_kind='fulltext'
		WHERE s.tenant_id=$1 AND s.state IN('indexing','ready')`, tenant, version).Scan(&stats.Pending, &stats.Ready, &stats.Stale)
	return stats, err
}

func (r *PostgresRepository) ResetFullTextProjection(ctx context.Context, tenant, sourceID string) error {
	tx, err := r.begin(ctx, tenant)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `DELETE FROM saas_fulltext_documents WHERE tenant_id=$1 AND source_id=$2`, tenant, sourceID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM saas_source_projections WHERE tenant_id=$1 AND source_id=$2 AND projection_kind='fulltext'`, tenant, sourceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) begin(ctx context.Context, tenant string) (pgx.Tx, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("full-text repository is not configured")
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
