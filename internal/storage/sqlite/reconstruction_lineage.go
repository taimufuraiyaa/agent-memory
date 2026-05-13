package sqlite

import (
	"context"
	"time"
)

// AddReconstructionLineage links a reconstructed memory to source tombstones.
func (s *Store) AddReconstructionLineage(ctx context.Context, reconstructedID string, tombstoneIDs []string) error {
	if reconstructedID == "" || len(tombstoneIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `
INSERT OR IGNORE INTO reconstruction_lineage (reconstructed_id, tombstone_id, created_at)
VALUES (?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer func() { _ = stmt.Close() }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, tid := range tombstoneIDs {
		if _, err := stmt.ExecContext(ctx, reconstructedID, tid, now); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// CountReconstructionLineage returns number of lineage links for one reconstructed memory.
func (s *Store) CountReconstructionLineage(ctx context.Context, reconstructedID string) (int, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM reconstruction_lineage WHERE reconstructed_id = ?`, reconstructedID)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
