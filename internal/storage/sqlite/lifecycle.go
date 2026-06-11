package sqlite

import (
	"context"
	"encoding/json"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
)

// AddRelation upserts a relation edge.
func (s *Store) AddRelation(ctx context.Context, sourceID, targetID string, relType core.RelationType, weight float64, metadata map[string]string) error {
	if metadata == nil {
		metadata = map[string]string{}
	}
	b, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO relations (source_id, target_id, type, weight, metadata_json, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(source_id, target_id, type) DO UPDATE SET
	weight=excluded.weight,
	metadata_json=excluded.metadata_json`,
		sourceID, targetID, string(relType), weight, string(b), time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

// ListRelations returns outgoing relation edges for one source memory.
func (s *Store) ListRelations(ctx context.Context, sourceID string) ([]core.Relation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT target_id, type, weight, metadata_json FROM relations WHERE source_id = ?`, sourceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]core.Relation, 0)
	for rows.Next() {
		var r core.Relation
		var typ string
		var metaJSON string
		if err := rows.Scan(&r.TargetID, &typ, &r.Weight, &metaJSON); err != nil {
			return nil, err
		}
		r.Type = core.RelationType(typ)
		if metaJSON != "" {
			_ = json.Unmarshal([]byte(metaJSON), &r.Metadata)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkSuperseded marks one or more memories superseded by successor.
func (s *Store) MarkSuperseded(ctx context.Context, sourceIDs []string, successorID string) error {
	if len(sourceIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `UPDATE memories SET superseded_by = ?, updated_at = ? WHERE id = ?`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer func() { _ = stmt.Close() }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, id := range sourceIDs {
		if _, err := stmt.ExecContext(ctx, successorID, now, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// DeleteByIDs removes memory rows by ID.
func (s *Store) DeleteByIDs(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `DELETE FROM memories WHERE id = ?`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, id := range ids {
		if _, err := stmt.ExecContext(ctx, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// UpdateTier changes storage tier for one memory.
func (s *Store) UpdateTier(ctx context.Context, id string, tier core.StorageTier) error {
	_, err := s.db.ExecContext(ctx, `UPDATE memories SET storage_tier = ?, updated_at = ? WHERE id = ?`,
		string(tier), time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// AddTierTransition appends an audit log for a tier movement.
func (s *Store) AddTierTransition(ctx context.Context, memoryID string, from, to core.StorageTier, reason string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO tier_transitions (memory_id, from_tier, to_tier, reason, created_at)
VALUES (?, ?, ?, ?, ?)`,
		memoryID, string(from), string(to), reason, time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

// CountTierTransitions returns transition log count for one memory.
func (s *Store) CountTierTransitions(ctx context.Context, memoryID string) (int, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tier_transitions WHERE memory_id = ?`, memoryID)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// ListWorkspaceRelations returns all graph edges within a workspace.
func (s *Store) ListWorkspaceRelations(ctx context.Context, workspace string) ([]core.RelationEdge, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT r.source_id, r.target_id, r.type, r.weight, r.metadata_json
FROM relations r
JOIN memories m ON r.source_id = m.id
WHERE m.workspace = ?`, workspace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]core.RelationEdge, 0)
	for rows.Next() {
		var edge core.RelationEdge
		var typ string
		var metaJSON string
		if err := rows.Scan(&edge.SourceID, &edge.TargetID, &typ, &edge.Weight, &metaJSON); err != nil {
			return nil, err
		}
		edge.Type = core.RelationType(typ)
		if metaJSON != "" {
			_ = json.Unmarshal([]byte(metaJSON), &edge.Metadata)
		}
		out = append(out, edge)
	}
	return out, rows.Err()
}

