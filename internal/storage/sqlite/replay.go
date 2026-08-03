package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *Store) ReplayImportCheckpoint(ctx context.Context, workspace, sourcePath string) (int, error) {
	var line int
	err := s.db.QueryRowContext(ctx, `SELECT line_number FROM replay_import_checkpoints WHERE workspace = ? AND source_path = ?`, workspace, sourcePath).Scan(&line)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return line, err
}

func (s *Store) SetReplayImportCheckpoint(ctx context.Context, workspace, sourcePath string, line int) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO replay_import_checkpoints (workspace, source_path, line_number, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(workspace, source_path) DO UPDATE SET line_number = excluded.line_number, updated_at = excluded.updated_at`, workspace, sourcePath, line, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

type ReplayEvent struct {
	EventID               string    `json:"event_id"`
	SessionID             string    `json:"session_id"`
	OccurredAt            time.Time `json:"occurred_at"`
	Kind                  string    `json:"kind"`
	Actor                 string    `json:"actor,omitempty"`
	Summary               string    `json:"summary"`
	ToolName              string    `json:"tool_name,omitempty"`
	RelatedObservationIDs []string  `json:"related_observation_ids,omitempty"`
	RelatedMemoryIDs      []string  `json:"related_memory_ids,omitempty"`
	SchemaVersion         string    `json:"schema_version,omitempty"`
	CaptureMode           string    `json:"capture_mode,omitempty"`
}

func (s *Store) LinkMemoryObservations(ctx context.Context, memoryID string, observationIDs []string) error {
	if strings.TrimSpace(memoryID) == "" {
		return errors.New("memory id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, observationID := range observationIDs {
		if observationID = strings.TrimSpace(observationID); observationID != "" {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_observation_provenance (memory_id, observation_id, created_at) VALUES (?, ?, ?)`, memoryID, observationID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) LoadReplayEvents(ctx context.Context, workspace, sessionID string, limit int, after string) ([]ReplayEvent, error) {
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("workspace and session id are required")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	where := "o.workspace = ? AND o.session_id = ?"
	args := []any{workspace, sessionID}
	if strings.TrimSpace(after) != "" {
		where += " AND o.occurred_at > ?"
		args = append(args, after)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT o.id, o.session_id, o.occurred_at, o.kind, o.source_agent, o.summary, o.tool_name, o.schema_version, o.capture_mode, COALESCE(GROUP_CONCAT(p.memory_id, ','), '') FROM observations o LEFT JOIN memory_observation_provenance p ON p.observation_id = o.id WHERE `+where+` GROUP BY o.id ORDER BY o.occurred_at ASC, o.id ASC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []ReplayEvent
	for rows.Next() {
		var event ReplayEvent
		var occurred, memoryIDs string
		if err := rows.Scan(&event.EventID, &event.SessionID, &occurred, &event.Kind, &event.Actor, &event.Summary, &event.ToolName, &event.SchemaVersion, &event.CaptureMode, &memoryIDs); err != nil {
			return nil, err
		}
		event.OccurredAt, _ = time.Parse(time.RFC3339Nano, occurred)
		event.RelatedObservationIDs = []string{event.EventID}
		if memoryIDs != "" {
			event.RelatedMemoryIDs = strings.Split(memoryIDs, ",")
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
