package memory

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type PostgresSearchRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresSearchRepository(pool *pgxpool.Pool) *PostgresSearchRepository {
	return &PostgresSearchRepository{pool: pool}
}

func (r *PostgresSearchRepository) SearchMemories(ctx context.Context, tenantID string, query SearchQuery) ([]SearchRow, error) {
	if r == nil || r.pool == nil || tenantID == "" || query.WorkspaceID == "" || query.Text == "" || query.Limit < 1 || query.Limit > maximumSearchLimit+1 {
		return nil, ErrInvalidSearch
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		return nil, err
	}
	var workspaceExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM saas_workspaces WHERE tenant_id=$1 AND id=$2 AND state='active'
	)`, tenantID, query.WorkspaceID).Scan(&workspaceExists); err != nil {
		return nil, err
	}
	if !workspaceExists {
		return nil, auth.ErrTenantUnavailable
	}
	var afterScore any
	var afterTime any
	var afterID any
	if query.After != nil {
		if !validPosition(query.After.Score, query.After.UpdatedAt, query.After.ID) {
			return nil, ErrInvalidSearch
		}
		afterScore, afterTime, afterID = query.After.Score, query.After.UpdatedAt.UTC(), query.After.ID
	}
	rows, err := tx.Query(ctx, `WITH search_query AS (
		SELECT websearch_to_tsquery('simple', $3) AS value
	), matched AS (
		SELECT m.id::text, m.workspace_id::text, m.memory_type, m.content, m.source_kind,
			m.entities, m.tags, m.keywords, m.outcome, m.confidence, m.storage_tier,
			m.created_at, m.updated_at,
			ts_rank_cd(m.search_document, q.value)::double precision AS score
		FROM saas_memories m CROSS JOIN search_query q
		WHERE m.tenant_id=$1 AND m.workspace_id=$2 AND m.deleted_at IS NULL
			AND m.search_document @@ q.value
	)
	SELECT id, workspace_id, memory_type, content, source_kind, entities, tags,
		keywords, outcome, confidence, storage_tier, created_at, updated_at, score
	FROM matched
	WHERE $4::double precision IS NULL
		OR score < $4
		OR (score = $4 AND updated_at < $5::timestamptz)
		OR (score = $4 AND updated_at = $5::timestamptz AND id::uuid < $6::uuid)
	ORDER BY score DESC, updated_at DESC, id::uuid DESC
	LIMIT $7`, tenantID, query.WorkspaceID, query.Text, afterScore, afterTime, afterID, query.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]SearchRow, 0, query.Limit)
	for rows.Next() {
		var row SearchRow
		var keywordsJSON []byte
		var outcomeJSON []byte
		if err := rows.Scan(
			&row.Item.ID, &row.Item.WorkspaceID, &row.Item.Type, &row.Item.Content,
			&row.Item.SourceKind, &row.Item.Entities, &row.Item.Tags, &keywordsJSON,
			&outcomeJSON, &row.Item.Confidence, &row.Item.StorageTier,
			&row.Item.CreatedAt, &row.Item.UpdatedAt, &row.Score,
		); err != nil {
			return nil, err
		}
		var terms []core.MemoryTerm
		if err := json.Unmarshal(keywordsJSON, &terms); err != nil {
			return nil, err
		}
		row.Item.Keywords = make([]string, 0, len(terms))
		for _, term := range terms {
			row.Item.Keywords = append(row.Item.Keywords, term.Term)
		}
		if len(outcomeJSON) > 0 {
			row.Item.Outcome = &core.Outcome{}
			if err := json.Unmarshal(outcomeJSON, row.Item.Outcome); err != nil {
				return nil, err
			}
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
