package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var (
	ErrSolutionWorkingStateConflict = errors.New("solution working state generation conflict")
	ErrSolutionWorkingStateExpired  = errors.New("solution working state expired")
)

func (s *Store) PutSolutionWorkingState(ctx context.Context, state core.SolutionWorkingState, expectedGeneration int64) (core.SolutionWorkingState, error) {
	state.Generation = expectedGeneration + 1
	if err := state.Validate(); err != nil {
		return core.SolutionWorkingState{}, err
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return core.SolutionWorkingState{}, err
	}
	if expectedGeneration == 0 {
		result, err := s.db.ExecContext(ctx, `INSERT INTO solution_working_state (
			episode_id, workspace, session_id, principal_id, state_json, generation,
			sensitivity, updated_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(episode_id) DO NOTHING`,
			state.EpisodeID, state.Workspace, state.SessionID, state.PrincipalID, string(payload), state.Generation,
			state.Sensitivity, state.UpdatedAt.Format(time.RFC3339Nano), state.ExpiresAt.Format(time.RFC3339Nano))
		if err != nil {
			return core.SolutionWorkingState{}, err
		}
		rows, _ := result.RowsAffected()
		if rows != 1 {
			return core.SolutionWorkingState{}, ErrSolutionWorkingStateConflict
		}
		return state, nil
	}
	result, err := s.db.ExecContext(ctx, `UPDATE solution_working_state SET
		workspace = ?, session_id = ?, principal_id = ?, state_json = ?, generation = ?,
		sensitivity = ?, updated_at = ?, expires_at = ?
		WHERE episode_id = ? AND workspace = ? AND principal_id = ? AND generation = ?`,
		state.Workspace, state.SessionID, state.PrincipalID, string(payload), state.Generation,
		state.Sensitivity, state.UpdatedAt.Format(time.RFC3339Nano), state.ExpiresAt.Format(time.RFC3339Nano),
		state.EpisodeID, state.Workspace, state.PrincipalID, expectedGeneration)
	if err != nil {
		return core.SolutionWorkingState{}, err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return core.SolutionWorkingState{}, ErrSolutionWorkingStateConflict
	}
	return state, nil
}

func (s *Store) GetSolutionWorkingState(ctx context.Context, workspace, principalID, episodeID string, now time.Time) (core.SolutionWorkingState, error) {
	var payload, expiresAt string
	err := s.db.QueryRowContext(ctx, `SELECT state_json, expires_at FROM solution_working_state
		WHERE episode_id = ? AND workspace = ? AND principal_id = ?`, strings.TrimSpace(episodeID),
		strings.TrimSpace(workspace), strings.TrimSpace(principalID)).Scan(&payload, &expiresAt)
	if err != nil {
		return core.SolutionWorkingState{}, err
	}
	expires, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return core.SolutionWorkingState{}, err
	}
	if !expires.After(now.UTC()) {
		return core.SolutionWorkingState{}, ErrSolutionWorkingStateExpired
	}
	var state core.SolutionWorkingState
	if err := json.Unmarshal([]byte(payload), &state); err != nil {
		return core.SolutionWorkingState{}, err
	}
	return state, nil
}

func (s *Store) ClearSolutionWorkingState(ctx context.Context, workspace, principalID, episodeID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM solution_working_state
		WHERE episode_id = ? AND workspace = ? AND principal_id = ?`, strings.TrimSpace(episodeID),
		strings.TrimSpace(workspace), strings.TrimSpace(principalID))
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CleanupExpiredSolutionWorkingState(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM solution_working_state WHERE episode_id IN (
		SELECT episode_id FROM solution_working_state WHERE expires_at <= ? ORDER BY expires_at LIMIT ?
	)`, now.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return 0, err
	}
	rows, err := result.RowsAffected()
	return int(rows), err
}
