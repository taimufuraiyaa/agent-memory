package connectors

import (
	"context"

	sqlitestore "github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type SQLiteCheckpoints struct{ Store *sqlitestore.Store }

func (s SQLiteCheckpoints) Load(ctx context.Context, id string) (Checkpoint, error) {
	cp, err := s.Store.LoadConnectorCheckpoint(ctx, id)
	return Checkpoint{ConnectorID: cp.ConnectorID, State: cp.State, UpdatedAt: cp.UpdatedAt, LastError: cp.LastError, EmittedCount: cp.EmittedCount, CoalescedCount: cp.CoalescedCount, RescannedCount: cp.RescannedCount}, err
}
func (s SQLiteCheckpoints) Save(ctx context.Context, cp Checkpoint) error {
	return s.Store.SaveConnectorCheckpoint(ctx, sqlitestore.ConnectorCheckpoint{ConnectorID: cp.ConnectorID, State: cp.State, UpdatedAt: cp.UpdatedAt, LastError: cp.LastError, EmittedCount: cp.EmittedCount, CoalescedCount: cp.CoalescedCount, RescannedCount: cp.RescannedCount})
}
