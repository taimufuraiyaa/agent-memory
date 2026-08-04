package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type TermOperator string

const (
	TermOperatorAND TermOperator = "and"
	TermOperatorOR  TermOperator = "or"
)

type TermSearchQuery struct {
	Workspace            string
	Terms                []string
	Operator             TermOperator
	NormalizationVersion string
	Limit                int
}

type TermMatch struct {
	MemoryID     string
	MatchedTerms []string
	MatchCount   int
	SourceWeight int
}

type TermIndexStatus string

const (
	TermIndexReady    TermIndexStatus = "ready"
	TermIndexDirty    TermIndexStatus = "dirty"
	TermIndexBuilding TermIndexStatus = "building"
	TermIndexCorrupt  TermIndexStatus = "corrupt"
)

// TermIndexState is the persisted, project-local Bloom snapshot metadata.
type TermIndexState struct {
	Workspace            string
	Bitmap               []byte
	State                TermIndexStatus
	FormatVersion        string
	NormalizationVersion string
	ExtractorVersion     string
	HashVersion          string
	BitCount             int64
	HashCount            int
	DistinctItemCount    int64
	PlannedCapacity      int64
	EstimatedFPP         float64
	StaleDeleteCount     int64
	CorpusGeneration     int64
	FilterGeneration     int64
	Checksum             string
	RebuildReason        string
	BuiltAt              time.Time
	UpdatedAt            time.Time
}

// ReplaceMemoryTerms atomically replaces the canonical locator terms for one memory.
func (s *Store) ReplaceMemoryTerms(ctx context.Context, workspace, memoryID string, terms []core.MemoryTerm) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := replaceMemoryTermsTx(ctx, tx, workspace, memoryID, terms); err != nil {
		return err
	}
	return tx.Commit()
}

func replaceMemoryTermsTx(ctx context.Context, tx *sql.Tx, workspace, memoryID string, terms []core.MemoryTerm) error {
	workspace = strings.TrimSpace(workspace)
	memoryID = strings.TrimSpace(memoryID)
	if workspace == "" || memoryID == "" {
		return errors.New("workspace and memory id are required")
	}
	if len(terms) > 3 {
		return fmt.Errorf("%w: memory terms must contain at most 3 terms", core.ErrInvalidInput)
	}

	var storedWorkspace string
	if err := tx.QueryRowContext(ctx, `SELECT workspace FROM memories WHERE id = ?`, memoryID).Scan(&storedWorkspace); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("memory %s not found", memoryID)
		}
		return err
	}
	if storedWorkspace != workspace {
		return errors.New("memory does not belong to workspace")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_terms WHERE workspace = ? AND memory_id = ?`, workspace, memoryID); err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	seen := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		normalized := strings.TrimSpace(term.Term)
		if normalized == "" || strings.TrimSpace(term.NormalizationVersion) == "" || strings.TrimSpace(term.ExtractorVersion) == "" {
			return errors.New("term, normalization version, and extractor version are required")
		}
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO memory_terms (
	workspace, memory_id, normalized_term, display_term, source, ordinal,
	normalization_version, extractor_version, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			workspace, memoryID, normalized, term.Display, string(term.Source), term.Ordinal,
			term.NormalizationVersion, term.ExtractorVersion, now,
		); err != nil {
			return err
		}
	}
	// R3: incremental bitmap update — add new term bits to the live Bloom snapshot
	// so gate mode can stay eligible between rebuilds. Failures leave state dirty
	// (the delete trigger already dirtied it; fail-open is safe).
	if len(terms) > 0 {
		_ = tryIncrementalTermIndexUpdate(ctx, tx, workspace, terms, now)
	}
	return nil
}

// ListMemoryTerms returns canonical terms ordered by their selection ordinal.
func (s *Store) ListMemoryTerms(ctx context.Context, workspace, memoryID string) ([]core.MemoryTerm, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT normalized_term, display_term, source, ordinal, normalization_version, extractor_version
FROM memory_terms
WHERE workspace = ? AND memory_id = ?
ORDER BY ordinal ASC, normalized_term ASC`, strings.TrimSpace(workspace), strings.TrimSpace(memoryID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]core.MemoryTerm, 0, 3)
	for rows.Next() {
		var term core.MemoryTerm
		var source string
		if err := rows.Scan(&term.Term, &term.Display, &source, &term.Ordinal, &term.NormalizationVersion, &term.ExtractorVersion); err != nil {
			return nil, err
		}
		term.Source = core.TermSource(source)
		out = append(out, term)
	}
	return out, rows.Err()
}

// SearchMemoryTerms returns deterministic canonical matches without embeddings.
func (s *Store) SearchMemoryTerms(ctx context.Context, query TermSearchQuery) ([]TermMatch, error) {
	query.Workspace = strings.TrimSpace(query.Workspace)
	query.NormalizationVersion = strings.TrimSpace(query.NormalizationVersion)
	if query.Workspace == "" || query.NormalizationVersion == "" {
		return nil, errors.New("workspace and normalization version are required")
	}
	if len(query.Terms) == 0 || len(query.Terms) > 3 {
		return nil, errors.New("term search requires between 1 and 3 terms")
	}
	if query.Operator == "" {
		query.Operator = TermOperatorAND
	}
	if query.Operator != TermOperatorAND && query.Operator != TermOperatorOR {
		return nil, errors.New("term operator must be and or or")
	}
	if query.Limit <= 0 {
		query.Limit = 10
	}
	if query.Limit > 200 {
		query.Limit = 200
	}

	terms := append([]string(nil), query.Terms...)
	sort.Strings(terms)
	placeholders := strings.TrimRight(strings.Repeat("?,", len(terms)), ",")
	statement := `
SELECT
	mt.memory_id,
	COUNT(DISTINCT mt.normalized_term) AS match_count,
	COALESCE(SUM(CASE mt.source
		WHEN 'explicit' THEN 50
		WHEN 'hashtag' THEN 40
		WHEN 'entity' THEN 30
		WHEN 'tag' THEN 20
		ELSE 10
	END), 0) AS source_weight,
	GROUP_CONCAT(DISTINCT mt.normalized_term) AS matched_terms
FROM memory_terms mt
JOIN memories m ON m.id = mt.memory_id AND m.workspace = mt.workspace
WHERE mt.workspace = ?
	AND mt.normalization_version = ?
	AND mt.normalized_term IN (` + placeholders + `)
GROUP BY mt.memory_id
`
	args := make([]any, 0, len(terms)+4)
	args = append(args, query.Workspace, query.NormalizationVersion)
	for _, term := range terms {
		args = append(args, term)
	}
	if query.Operator == TermOperatorAND {
		statement += `HAVING COUNT(DISTINCT mt.normalized_term) = ?
`
		args = append(args, len(terms))
	}
	statement += `ORDER BY match_count DESC, source_weight DESC, m.confidence DESC, m.updated_at DESC, mt.memory_id ASC
LIMIT ?`
	args = append(args, query.Limit)

	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]TermMatch, 0)
	for rows.Next() {
		var match TermMatch
		var joined string
		if err := rows.Scan(&match.MemoryID, &match.MatchCount, &match.SourceWeight, &joined); err != nil {
			return nil, err
		}
		if joined != "" {
			match.MatchedTerms = strings.Split(joined, ",")
			sort.Strings(match.MatchedTerms)
		}
		out = append(out, match)
	}
	return out, rows.Err()
}

// UpsertTermIndexState persists one complete project filter snapshot and metadata row.
func (s *Store) UpsertTermIndexState(ctx context.Context, state TermIndexState) error {
	state.Workspace = strings.TrimSpace(state.Workspace)
	if state.Workspace == "" {
		return errors.New("workspace is required")
	}
	if state.State == "" {
		state.State = TermIndexDirty
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO term_index_state (
	workspace, bitmap, state, format_version, normalization_version, extractor_version, hash_version,
	bit_count, hash_count, distinct_item_count, planned_capacity, estimated_fpp,
	stale_delete_count, corpus_generation, filter_generation, checksum, rebuild_reason, built_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(workspace) DO UPDATE SET
	bitmap = excluded.bitmap,
	state = excluded.state,
	format_version = excluded.format_version,
	normalization_version = excluded.normalization_version,
	extractor_version = excluded.extractor_version,
	hash_version = excluded.hash_version,
	bit_count = excluded.bit_count,
	hash_count = excluded.hash_count,
	distinct_item_count = excluded.distinct_item_count,
	planned_capacity = excluded.planned_capacity,
	estimated_fpp = excluded.estimated_fpp,
	stale_delete_count = excluded.stale_delete_count,
	corpus_generation = excluded.corpus_generation,
	filter_generation = excluded.filter_generation,
	checksum = excluded.checksum,
	rebuild_reason = excluded.rebuild_reason,
	built_at = excluded.built_at,
	updated_at = excluded.updated_at`,
		state.Workspace, state.Bitmap, string(state.State), state.FormatVersion, state.NormalizationVersion, state.ExtractorVersion, state.HashVersion,
		state.BitCount, state.HashCount, state.DistinctItemCount, state.PlannedCapacity, state.EstimatedFPP,
		state.StaleDeleteCount, state.CorpusGeneration, state.FilterGeneration, state.Checksum, state.RebuildReason,
		timeStringOrEmpty(state.BuiltAt), state.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

// GetTermIndexState loads project Bloom metadata, returning nil when no index exists.
func (s *Store) GetTermIndexState(ctx context.Context, workspace string) (*TermIndexState, error) {
	var state TermIndexState
	var status, builtAt, updatedAt string
	err := s.db.QueryRowContext(ctx, `
SELECT workspace, bitmap, state, format_version, normalization_version, extractor_version, hash_version,
	bit_count, hash_count, distinct_item_count, planned_capacity, estimated_fpp,
	stale_delete_count, corpus_generation, filter_generation, checksum, rebuild_reason, built_at, updated_at
FROM term_index_state WHERE workspace = ?`, strings.TrimSpace(workspace)).Scan(
		&state.Workspace, &state.Bitmap, &status, &state.FormatVersion, &state.NormalizationVersion, &state.ExtractorVersion, &state.HashVersion,
		&state.BitCount, &state.HashCount, &state.DistinctItemCount, &state.PlannedCapacity, &state.EstimatedFPP,
		&state.StaleDeleteCount, &state.CorpusGeneration, &state.FilterGeneration, &state.Checksum, &state.RebuildReason, &builtAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	state.State = TermIndexStatus(status)
	state.BuiltAt = parseTermIndexTime(builtAt)
	state.UpdatedAt = parseTermIndexTime(updatedAt)
	return &state, nil
}

// ListMemoriesForTermBackfill returns a lightweight ID-ordered page.
func (s *Store) ListMemoriesForTermBackfill(ctx context.Context, workspace, afterID string, limit int) ([]core.MemoryEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, content, entities_json, tags_json
FROM memories
WHERE workspace = ? AND id > ?
ORDER BY id ASC
LIMIT ?`, strings.TrimSpace(workspace), afterID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]core.MemoryEntry, 0, limit)
	for rows.Next() {
		var memory core.MemoryEntry
		var entitiesJSON, tagsJSON string
		if err := rows.Scan(&memory.ID, &memory.Content, &entitiesJSON, &tagsJSON); err != nil {
			return nil, err
		}
		memory.Workspace = strings.TrimSpace(workspace)
		if err := json.Unmarshal([]byte(entitiesJSON), &memory.Entities); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(tagsJSON), &memory.Tags); err != nil {
			return nil, err
		}
		out = append(out, memory)
	}
	return out, rows.Err()
}

func (s *Store) CountDistinctMemoryTerms(ctx context.Context, workspace, normalizationVersion string) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(DISTINCT normalized_term)
FROM memory_terms
WHERE workspace = ? AND normalization_version = ?`, strings.TrimSpace(workspace), strings.TrimSpace(normalizationVersion)).Scan(&count)
	return count, err
}

// ListDistinctMemoryTerms returns an ordered page suitable for streaming rebuilds.
func (s *Store) ListDistinctMemoryTerms(ctx context.Context, workspace, normalizationVersion, afterTerm string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT normalized_term
FROM memory_terms
WHERE workspace = ? AND normalization_version = ? AND normalized_term > ?
ORDER BY normalized_term ASC
LIMIT ?`, strings.TrimSpace(workspace), strings.TrimSpace(normalizationVersion), afterTerm, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]string, 0, limit)
	for rows.Next() {
		var term string
		if err := rows.Scan(&term); err != nil {
			return nil, err
		}
		out = append(out, term)
	}
	return out, rows.Err()
}

func parseTermIndexTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

// tryIncrementalTermIndexUpdate adds term bits to the live Bloom snapshot so gate
// mode stays eligible between rebuilds. Fails silently on any error — the delete
// trigger already dirtied the state, so fail-open is safe.
func tryIncrementalTermIndexUpdate(ctx context.Context, tx *sql.Tx, workspace string, terms []core.MemoryTerm, now string) error {
	var state TermIndexState
	var status string
	err := tx.QueryRowContext(ctx,
		`SELECT workspace, bitmap, state, bit_count, hash_count, corpus_generation, filter_generation, checksum
		 FROM term_index_state WHERE workspace = ?`, workspace).Scan(
		&state.Workspace, &state.Bitmap, &status, &state.BitCount, &state.HashCount,
		&state.CorpusGeneration, &state.FilterGeneration, &state.Checksum,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // no state row — nothing to update
		}
		return err
	}
	state.State = TermIndexStatus(status)

	// Update the snapshot when we have a valid bitmap to add to, regardless of
	// whether the delete trigger fired (dirty) or no trigger fired (ready).
	if len(state.Bitmap) == 0 || state.HashCount == 0 || state.BitCount == 0 {
		return nil
	}
	if state.State != TermIndexReady && state.State != TermIndexDirty {
		return nil // building / corrupt — rebuild machinery handles it
	}

	bitmap := append([]byte(nil), state.Bitmap...)
	for _, term := range terms {
		addTermToBloomBitmap(bitmap, term.Term, state.BitCount, state.HashCount)
	}
	checksum := bloomChecksum(bitmap)

	// Persist the updated bitmap with advanced generations (both bumped so they
	// stay in sync) and ready state.
	nextGen := state.CorpusGeneration + 1
	_, err = tx.ExecContext(ctx,
		`UPDATE term_index_state
		 SET bitmap = ?, state = 'ready', checksum = ?,
		     corpus_generation = ?, filter_generation = ?, updated_at = ?
		 WHERE workspace = ?`,
		bitmap, checksum, nextGen, nextGen, now, workspace,
	)
	return err
}

// addTermToBloomBitmap adds one term's k bits to the byteslice bitmap using the
// same double-hashing as engine.TermBloom (sha256 double-hash with coprime guard).
func addTermToBloomBitmap(bitmap []byte, term string, bitCount int64, hashCount int) {
	sum := sha256.Sum256([]byte(term))
	h1 := binary.LittleEndian.Uint64(sum[0:8])
	h2 := binary.LittleEndian.Uint64(sum[8:16])
	if h2 == 0 {
		h2 = 0x9e3779b97f4a7c15
	}
	m := uint64(bitCount)
	if m < 2 {
		return
	}
	h1 %= m
	h2 %= m
	for h2%m == 0 || gcd64(h2, m) != 1 {
		h2 = (h2 + 1) % m
	}
	for i := 0; i < hashCount; i++ {
		pos := (h1 + uint64(i)*h2) % m
		bitmap[pos/8] |= byte(1 << (pos % 8))
	}
}

// bloomChecksum returns the hex-encoded SHA-256 of the bitmap.
func bloomChecksum(bitmap []byte) string {
	sum := sha256.Sum256(bitmap)
	return hex.EncodeToString(sum[:])
}

func gcd64(a, b uint64) uint64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
