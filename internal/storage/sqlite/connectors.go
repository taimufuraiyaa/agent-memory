package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type ConnectorCheckpoint struct {
	ConnectorID    string            `json:"connector_id"`
	State          map[string]string `json:"state"`
	UpdatedAt      time.Time         `json:"updated_at"`
	LastError      string            `json:"last_error,omitempty"`
	EmittedCount   int64             `json:"emitted_count"`
	CoalescedCount int64             `json:"coalesced_count"`
	RescannedCount int64             `json:"rescanned_count"`
}

func (s *Store) LoadConnectorCheckpoint(ctx context.Context, id string) (ConnectorCheckpoint, error) {
	var cp ConnectorCheckpoint
	var raw, updated string
	err := s.db.QueryRowContext(ctx, `SELECT connector_id, state_json, updated_at, last_error,
		emitted_count, coalesced_count, rescanned_count FROM connector_checkpoints WHERE connector_id = ?`, id).
		Scan(&cp.ConnectorID, &raw, &updated, &cp.LastError, &cp.EmittedCount, &cp.CoalescedCount, &cp.RescannedCount)
	if err != nil {
		return cp, err
	}
	if err := json.Unmarshal([]byte(raw), &cp.State); err != nil {
		return cp, fmt.Errorf("decode connector checkpoint: %w", err)
	}
	cp.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return cp, nil
}

func (s *Store) SaveConnectorCheckpoint(ctx context.Context, cp ConnectorCheckpoint) error {
	raw, err := json.Marshal(cp.State)
	if err != nil {
		return err
	}
	if cp.UpdatedAt.IsZero() {
		cp.UpdatedAt = time.Now().UTC()
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO connector_checkpoints
		(connector_id, state_json, updated_at, last_error, emitted_count, coalesced_count, rescanned_count)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(connector_id) DO UPDATE SET state_json=excluded.state_json, updated_at=excluded.updated_at,
		last_error=excluded.last_error, emitted_count=excluded.emitted_count,
		coalesced_count=excluded.coalesced_count, rescanned_count=excluded.rescanned_count`,
		cp.ConnectorID, string(raw), cp.UpdatedAt.Format(time.RFC3339Nano), cp.LastError,
		cp.EmittedCount, cp.CoalescedCount, cp.RescannedCount)
	return err
}
