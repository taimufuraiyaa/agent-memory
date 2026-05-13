package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
)

// UpsertMemoryVector writes or updates an embedding for a memory row.
func (s *Store) UpsertMemoryVector(ctx context.Context, memoryID, workspace string, embedding []float32) error {
	b, err := json.Marshal(embedding)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO memory_vectors (memory_id, workspace, embedding_json, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(memory_id) DO UPDATE SET
	workspace=excluded.workspace,
	embedding_json=excluded.embedding_json,
	updated_at=excluded.updated_at
`, memoryID, workspace, string(b), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// ListMemoryVectorsByWorkspace returns cached embeddings keyed by memory ID.
func (s *Store) ListMemoryVectorsByWorkspace(ctx context.Context, workspace string) (map[string][]float32, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT memory_id, embedding_json
FROM memory_vectors
WHERE workspace = ?
`, workspace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string][]float32)
	for rows.Next() {
		var id, embJSON string
		if err := rows.Scan(&id, &embJSON); err != nil {
			return nil, err
		}
		var vec []float32
		if err := json.Unmarshal([]byte(embJSON), &vec); err != nil {
			return nil, err
		}
		out[id] = vec
	}
	return out, rows.Err()
}

// VectorScore represents a ranked memory ID and cosine score.
type VectorScore struct {
	MemoryID string
	Score    float64
}

// SearchMemoryVectorsSQL computes cosine similarity inside SQLite using JSON vectors.
func (s *Store) SearchMemoryVectorsSQL(
	ctx context.Context,
	workspace string,
	queryVec []float32,
	topK int,
	types []core.MemoryType,
	tiers []core.StorageTier,
) ([]VectorScore, error) {
	if topK <= 0 {
		topK = 5
	}
	qb, err := json.Marshal(queryVec)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	args := make([]any, 0, 4+len(types)+len(tiers))
	b.WriteString(`
WITH query_vec AS (
	SELECT key AS idx, CAST(value AS REAL) AS qv
	FROM json_each(?)
),
candidate AS (
	SELECT mv.memory_id, mv.embedding_json
	FROM memory_vectors mv
	JOIN memories m ON m.id = mv.memory_id
	WHERE mv.workspace = ?`)
	args = append(args, string(qb), workspace)
	if len(types) > 0 {
		b.WriteString(" AND m.type IN (")
		for i, t := range types {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString("?")
			args = append(args, string(t))
		}
		b.WriteString(")")
	}
	if len(tiers) > 0 {
		b.WriteString(" AND m.storage_tier IN (")
		for i, t := range tiers {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString("?")
			args = append(args, string(t))
		}
		b.WriteString(")")
	}
	b.WriteString(`
),
scores AS (
	SELECT
		c.memory_id AS memory_id,
		SUM(q.qv * CAST(e.value AS REAL)) AS dot,
		SQRT(SUM(q.qv * q.qv)) AS qnorm,
		SQRT(SUM(CAST(e.value AS REAL) * CAST(e.value AS REAL))) AS enorm
	FROM candidate c
	JOIN json_each(c.embedding_json) e
	JOIN query_vec q ON q.idx = e.key
	GROUP BY c.memory_id
)
SELECT
	memory_id,
	CASE
		WHEN qnorm = 0 OR enorm = 0 THEN 0
		ELSE dot / (qnorm * enorm)
	END AS score
FROM scores
ORDER BY score DESC
LIMIT ?`)
	args = append(args, topK)

	rows, err := s.db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("search memory vectors sql: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]VectorScore, 0, topK)
	for rows.Next() {
		var item VectorScore
		if err := rows.Scan(&item.MemoryID, &item.Score); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
