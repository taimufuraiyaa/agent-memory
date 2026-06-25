package sqlite

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
)

type MemoryVectorRow struct {
	MemoryID              string
	Workspace             string
	Embedding             []float32
	EmbeddingProvider     string
	EmbeddingModelVersion string
	UpdatedAt             time.Time
}

// UpsertMemoryVector writes or updates an embedding for a memory row.
func (s *Store) UpsertMemoryVector(ctx context.Context, memoryID, workspace, provider, modelVersion string, embedding []float32) error {
	_start := time.Now()
	defer func() { s.logSlowQuery(ctx, "upsert_memory_vector", workspace, time.Since(_start)) }()
	if strings.TrimSpace(provider) == "" {
		return errors.New("embedding provider is required")
	}
	if strings.TrimSpace(modelVersion) == "" {
		modelVersion = "unknown"
	}
	b, err := json.Marshal(embedding)
	if err != nil {
		return err
	}
	blob := encodeFloat32Slice(embedding)
	_, err = s.db.ExecContext(ctx, `
INSERT INTO memory_vectors (memory_id, workspace, embedding_json, embedding_blob, embedding_provider, embedding_model_version, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(memory_id) DO UPDATE SET
	workspace=excluded.workspace,
	embedding_json=excluded.embedding_json,
	embedding_blob=excluded.embedding_blob,
	embedding_provider=excluded.embedding_provider,
	embedding_model_version=excluded.embedding_model_version,
	updated_at=excluded.updated_at
`, memoryID, workspace, string(b), blob, strings.TrimSpace(provider), strings.TrimSpace(modelVersion), time.Now().UTC().Format(time.RFC3339Nano))

	if err == nil && s.useTurbovec && s.turbovecIndex != nil {
		_ = s.turbovecIndex.Upsert(memoryID, embedding)
	}

	return err
}

// ListMemoryVectorsByWorkspace returns cached embeddings keyed by memory ID.
func (s *Store) ListMemoryVectorsByWorkspace(ctx context.Context, workspace string) (map[string][]float32, error) {
	rows, err := s.ListMemoryVectorRowsByWorkspace(ctx, workspace)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]float32, len(rows))
	for _, row := range rows {
		out[row.MemoryID] = row.Embedding
	}
	return out, nil
}

// ListMemoryVectorRowsByWorkspace returns cached embeddings plus provenance data.
func (s *Store) ListMemoryVectorRowsByWorkspace(ctx context.Context, workspace string) ([]MemoryVectorRow, error) {
	_start := time.Now()
	defer func() { s.logSlowQuery(ctx, "list_memory_vector_rows", workspace, time.Since(_start)) }()
	rows, err := s.db.QueryContext(ctx, `
SELECT memory_id, workspace, embedding_json, embedding_blob, embedding_provider, embedding_model_version, updated_at
FROM memory_vectors
WHERE workspace = ?
`, workspace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]MemoryVectorRow, 0)
	for rows.Next() {
		var row MemoryVectorRow
		var embJSON string
		var embBlob []byte
		var updatedAtRaw string
		if err := rows.Scan(&row.MemoryID, &row.Workspace, &embJSON, &embBlob, &row.EmbeddingProvider, &row.EmbeddingModelVersion, &updatedAtRaw); err != nil {
			return nil, err
		}
		if len(embBlob) > 0 {
			var err error
			row.Embedding, err = decodeFloat32Slice(embBlob)
			if err != nil {
				return nil, err
			}
		} else if len(embJSON) > 0 {
			if err := json.Unmarshal([]byte(embJSON), &row.Embedding); err != nil {
				return nil, err
			}
		}
		if ts, err := time.Parse(time.RFC3339Nano, updatedAtRaw); err == nil {
			row.UpdatedAt = ts
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func encodeFloat32Slice(slice []float32) []byte {
	buf := make([]byte, len(slice)*4)
	for i, f := range slice {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func decodeFloat32Slice(buf []byte) ([]float32, error) {
	if len(buf)%4 != 0 {
		return nil, fmt.Errorf("invalid blob length: %d", len(buf))
	}
	slice := make([]float32, len(buf)/4)
	for i := 0; i < len(slice); i++ {
		slice[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[i*4:]))
	}
	return slice, nil
}

// CountMemoryVectorsByProvider returns provider counts for one workspace.
func (s *Store) CountMemoryVectorsByProvider(ctx context.Context, workspace string) (map[string]int, error) {
	_start := time.Now()
	defer func() { s.logSlowQuery(ctx, "count_memory_vectors_by_provider", workspace, time.Since(_start)) }()
	rows, err := s.db.QueryContext(ctx, `
SELECT embedding_provider, COUNT(*)
FROM memory_vectors
WHERE workspace = ?
GROUP BY embedding_provider
`, workspace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]int)
	for rows.Next() {
		var provider string
		var count int
		if err := rows.Scan(&provider, &count); err != nil {
			return nil, err
		}
		provider = strings.TrimSpace(provider)
		if provider == "" {
			provider = "<missing>"
		}
		out[provider] = count
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
	provider string,
	queryVec []float32,
	topK int,
	types []core.MemoryType,
	tiers []core.StorageTier,
) ([]VectorScore, error) {
	_start := time.Now()
	defer func() { s.logSlowQuery(ctx, "search_memory_vectors_sql", workspace, time.Since(_start)) }()
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
	WHERE mv.workspace = ?
	  AND mv.embedding_provider = ?`)
	args = append(args, string(qb), workspace, strings.TrimSpace(provider))
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

// SearchMemoryVectorsGo computes cosine similarity in Go using binary float32 blobs or turbovec index.
func (s *Store) SearchMemoryVectorsGo(
	ctx context.Context,
	workspace string,
	provider string,
	queryVec []float32,
	topK int,
	types []core.MemoryType,
	tiers []core.StorageTier,
) ([]VectorScore, error) {
	_start := time.Now()
	defer func() { s.logSlowQuery(ctx, "search_memory_vectors_go", workspace, time.Since(_start)) }()
	if topK <= 0 {
		topK = 5
	}

	var b strings.Builder
	args := make([]any, 0, 2+len(types)+len(tiers))
	b.WriteString(`
SELECT mv.memory_id, mv.embedding_blob, mv.embedding_json
FROM memory_vectors mv
JOIN memories m ON m.id = mv.memory_id
WHERE mv.workspace = ?
  AND mv.embedding_provider = ?`)
	args = append(args, workspace, strings.TrimSpace(provider))
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

	rows, err := s.db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("query memory vectors: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// If using turbovec index, gather matching candidate IDs first
	var candidateIDs []string
	type rawItem struct {
		id   string
		blob []byte
		json string
	}
	var rawItems []rawItem

	for rows.Next() {
		var memoryID string
		var blob []byte
		var jsonStr string
		if err := rows.Scan(&memoryID, &blob, &jsonStr); err != nil {
			return nil, err
		}
		if s.useTurbovec && s.turbovecIndex != nil {
			candidateIDs = append(candidateIDs, memoryID)
		} else {
			rawItems = append(rawItems, rawItem{id: memoryID, blob: blob, json: jsonStr})
		}
	}
	_ = rows.Close()

	if s.useTurbovec && s.turbovecIndex != nil {
		// Run fast quantized search in Go
		scores := s.turbovecIndex.SearchQuantized(queryVec, candidateIDs)
		sort.Slice(scores, func(i, j int) bool {
			return scores[i].Score > scores[j].Score
		})
		if len(scores) > topK {
			scores = scores[:topK]
		}
		return scores, nil
	}

	var scores []VectorScore
	for _, item := range rawItems {
		var vec []float32
		if len(item.blob) > 0 {
			var err error
			vec, err = decodeFloat32Slice(item.blob)
			if err != nil {
				return nil, err
			}
		} else if len(item.json) > 0 {
			if err := json.Unmarshal([]byte(item.json), &vec); err != nil {
				continue
			}
		}

		if len(vec) == 0 {
			continue
		}

		score := computeCosineSimilarity(queryVec, vec)
		scores = append(scores, VectorScore{
			MemoryID: item.id,
			Score:    score,
		})
	}

	// Sort descending by score
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].Score > scores[j].Score
	})

	if len(scores) > topK {
		scores = scores[:topK]
	}

	return scores, nil
}

func computeCosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, qnorm, enorm float64
	for i := 0; i < len(a); i++ {
		av := float64(a[i])
		bv := float64(b[i])
		dot += av * bv
		qnorm += av * av
		enorm += bv * bv
	}
	if qnorm == 0 || enorm == 0 {
		return 0
	}
	return dot / (math.Sqrt(qnorm) * math.Sqrt(enorm))
}
