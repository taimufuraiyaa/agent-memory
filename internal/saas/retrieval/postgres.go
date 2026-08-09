package retrieval

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) AuthorizedSourceIDs(ctx context.Context, tenant string, requested []string) ([]string, error) {
	if tenant == "" || len(requested) == 0 || len(requested) > 100 {
		return nil, errors.New("source authorization requires tenant and bounded source IDs")
	}
	ids, err := parseSourceIDs(requested)
	if err != nil {
		return nil, err
	}
	tx, err := r.begin(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT id::text FROM saas_sources WHERE tenant_id=$1 AND id=ANY($2::uuid[]) AND state='ready' ORDER BY id`, tenant, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	authorized := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		authorized = append(authorized, id)
	}
	return authorized, rows.Err()
}

func (r *PostgresRepository) LexicalCandidates(ctx context.Context, tenant string, authorizedSourceIDs []string, query string, limit int) ([]Candidate, error) {
	if tenant == "" || len(authorizedSourceIDs) == 0 || strings.TrimSpace(query) == "" || limit <= 0 || limit > 150 {
		return nil, errors.New("lexical query requires tenant, authorized sources, text, and a bounded limit")
	}
	sourceIDs, err := parseSourceIDs(authorizedSourceIDs)
	if err != nil {
		return nil, err
	}
	tx, err := r.begin(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT d.source_id::text,d.source_version,d.passage_id,d.structural_node_id,c.id,d.text_content,d.locator,
		CASE WHEN position(lower($3) in lower(d.text_content))>0 THEN 1::float8 ELSE 0::float8 END,
		ts_rank_cd(d.search_vector,websearch_to_tsquery('simple',$3))::float8,
		COALESCE(g.decay_score,0),COALESCE(g.salience_score,0),COALESCE(g.suppression_score,0),
		COALESCE(g.useful_count,0),COALESCE(g.rejected_count,0),COALESCE(g.harmful_count,0),g.last_helpful_at,g.last_rejected_at,g.suppression_until
		FROM saas_fulltext_documents d
		JOIN saas_sources s ON s.tenant_id=d.tenant_id AND s.id=d.source_id AND s.active_version=d.source_version AND s.state='ready'
		JOIN saas_source_citations c ON c.tenant_id=d.tenant_id AND c.source_id=d.source_id AND c.source_version=d.source_version AND c.passage_id=d.passage_id
		LEFT JOIN saas_passage_signals g ON g.tenant_id=d.tenant_id AND g.source_id=d.source_id AND g.source_version=d.source_version AND g.passage_id=d.passage_id
		WHERE d.tenant_id=$1 AND d.source_id=ANY($2::uuid[]) AND
		(position(lower($3) in lower(d.text_content))>0 OR d.search_vector @@ websearch_to_tsquery('simple',$3))
		ORDER BY 7 DESC,8 DESC,d.passage_id LIMIT $4`, tenant, sourceIDs, query, limit)
	if err != nil {
		return nil, err
	}
	return scanCandidates(rows)
}

func (r *PostgresRepository) EvidenceByPassageIDs(ctx context.Context, tenant string, authorizedSourceIDs []string, keys []EvidenceKey) ([]Candidate, error) {
	if tenant == "" || len(authorizedSourceIDs) == 0 || len(keys) == 0 || len(keys) > 150 {
		return nil, errors.New("evidence hydration requires tenant, authorized sources, and bounded keys")
	}
	sourceIDs, err := parseSourceIDs(authorizedSourceIDs)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	tx, err := r.begin(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT p.source_id::text,p.source_version,p.id,p.structural_node_id,c.id,p.text_content,p.locator,
		0::float8,0::float8,COALESCE(g.decay_score,0),COALESCE(g.salience_score,0),COALESCE(g.suppression_score,0),
		COALESCE(g.useful_count,0),COALESCE(g.rejected_count,0),COALESCE(g.harmful_count,0),g.last_helpful_at,g.last_rejected_at,g.suppression_until
		FROM jsonb_to_recordset($3::jsonb) AS k(source_id uuid,passage_id text)
		JOIN saas_source_passages p ON p.tenant_id=$1 AND p.source_id=k.source_id AND p.id=k.passage_id
		JOIN saas_sources s ON s.tenant_id=p.tenant_id AND s.id=p.source_id AND s.active_version=p.source_version AND s.state='ready'
		JOIN saas_source_citations c ON c.tenant_id=p.tenant_id AND c.source_id=p.source_id AND c.source_version=p.source_version AND c.passage_id=p.id
		LEFT JOIN saas_passage_signals g ON g.tenant_id=p.tenant_id AND g.source_id=p.source_id AND g.source_version=p.source_version AND g.passage_id=p.id
		WHERE p.source_id=ANY($2::uuid[]) ORDER BY p.source_id,p.id`, tenant, sourceIDs, encoded)
	if err != nil {
		return nil, err
	}
	return scanCandidates(rows)
}

func (r *PostgresRepository) RecordPassageFeedback(ctx context.Context, tenant string, key EvidenceKey, sourceVersion int64, rating, actorID, requestID string, at time.Time) error {
	if tenant == "" || key.SourceID == "" || key.PassageID == "" || sourceVersion <= 0 || !oneOf(rating, "helpful", "rejected", "harmful") || actorID == "" || requestID == "" || at.IsZero() {
		return errors.New("passage feedback is incomplete")
	}
	tx, err := r.begin(ctx, tenant)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO saas_passage_feedback(tenant_id,id,source_id,source_version,passage_id,rating,actor_id,request_id,occurred_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, tenant, uuid.NewString(), key.SourceID, sourceVersion, key.PassageID, rating, actorID, requestID, at); err != nil {
		return err
	}
	useful, rejected, harmful := 0, 0, 0
	var helpfulAt, rejectedAt *time.Time
	switch rating {
	case "helpful":
		useful, helpfulAt = 1, &at
	case "rejected":
		rejected, rejectedAt = 1, &at
	case "harmful":
		harmful, rejectedAt = 1, &at
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_passage_signals
		(tenant_id,source_id,source_version,passage_id,useful_count,rejected_count,harmful_count,last_helpful_at,last_rejected_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT(tenant_id,source_id,source_version,passage_id) DO UPDATE SET
		useful_count=saas_passage_signals.useful_count+EXCLUDED.useful_count,
		rejected_count=saas_passage_signals.rejected_count+EXCLUDED.rejected_count,
		harmful_count=saas_passage_signals.harmful_count+EXCLUDED.harmful_count,
		last_helpful_at=COALESCE(EXCLUDED.last_helpful_at,saas_passage_signals.last_helpful_at),
		last_rejected_at=COALESCE(EXCLUDED.last_rejected_at,saas_passage_signals.last_rejected_at),updated_at=EXCLUDED.updated_at`,
		tenant, key.SourceID, sourceVersion, key.PassageID, useful, rejected, harmful, helpfulAt, rejectedAt, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func scanCandidates(rows pgx.Rows) ([]Candidate, error) {
	defer rows.Close()
	values := []Candidate{}
	for rows.Next() {
		var candidate Candidate
		var locator []byte
		if err := rows.Scan(&candidate.SourceID, &candidate.SourceVersion, &candidate.PassageID, &candidate.StructuralNodeID, &candidate.CitationID, &candidate.Text, &locator,
			&candidate.Breakdown.Exact, &candidate.Breakdown.FullText, &candidate.DecayScore, &candidate.SalienceScore, &candidate.SuppressionScore,
			&candidate.UsefulCount, &candidate.RejectedCount, &candidate.HarmfulCount, &candidate.LastHelpfulAt, &candidate.LastRejectedAt, &candidate.SuppressionUntil); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(locator, &candidate.Locator); err != nil {
			return nil, err
		}
		values = append(values, candidate)
	}
	return values, rows.Err()
}

func parseSourceIDs(values []string) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, len(values))
	for index, value := range values {
		parsed, err := uuid.Parse(value)
		if err != nil {
			return nil, errors.New("authorized source ID is invalid")
		}
		ids[index] = parsed
	}
	return ids, nil
}

func (r *PostgresRepository) begin(ctx context.Context, tenant string) (pgx.Tx, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("retrieval repository is not configured")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenant); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
