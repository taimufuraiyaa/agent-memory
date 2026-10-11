package sqlite

import (
	"context"
	"database/sql"
)

// beginGraphWriteTx reserves SQLite's writer before taking a read snapshot.
// A deferred read-to-write upgrade can fail with BUSY_SNAPSHOT when a lease
// heartbeat commits between validation and publication; busy_timeout cannot
// repair that stale snapshot. The no-row write obtains the reservation while
// preserving the existing driver and connection policy.
func (s *Store) beginGraphWriteTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE graph_jobs SET id=id WHERE 0`); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}
