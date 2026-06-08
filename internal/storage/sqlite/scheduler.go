package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const defaultSchedulerHistoryLimit = 30

type WorkspaceActivitySummary struct {
	Workspace      string    `json:"workspace"`
	MemoryCount    int       `json:"memory_count"`
	LastUpdatedAt  time.Time `json:"last_updated_at,omitempty"`
	LastAccessedAt time.Time `json:"last_accessed_at,omitempty"`
}

type SchedulerWorkspaceState struct {
	Workspace       string    `json:"workspace"`
	LastScheduledAt time.Time `json:"last_scheduled_at,omitempty"`
	LastCompletedAt time.Time `json:"last_completed_at,omitempty"`
	LastResult      string    `json:"last_result,omitempty"`
	LastSkipReason  string    `json:"last_skip_reason,omitempty"`
	LastDurationMS  int       `json:"last_duration_ms,omitempty"`
	LastImpacts     int       `json:"last_impacts,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}

type SchedulerRunRecord struct {
	ID             string    `json:"id"`
	Workspace      string    `json:"workspace"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at,omitempty"`
	Trigger        string    `json:"trigger"`
	Result         string    `json:"result"`
	SkipReason     string    `json:"skip_reason,omitempty"`
	DurationMS     int       `json:"duration_ms,omitempty"`
	DecayUpdated   int       `json:"decay_updated,omitempty"`
	Consolidated   int       `json:"consolidated,omitempty"`
	ConflictsFound int       `json:"conflicts_found,omitempty"`
	Evicted        int       `json:"evicted,omitempty"`
	Promoted       int       `json:"promoted,omitempty"`
	Demoted        int       `json:"demoted,omitempty"`
	Error          string    `json:"error,omitempty"`
}

func (s *Store) GetWorkspaceActivitySummary(ctx context.Context, workspace string) (*WorkspaceActivitySummary, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT COUNT(*), COALESCE(MAX(updated_at), ''), COALESCE(MAX(last_accessed), '')
FROM memories
WHERE workspace = ?`, workspace)

	var summary WorkspaceActivitySummary
	var lastUpdated string
	var lastAccessed string
	summary.Workspace = strings.TrimSpace(workspace)
	if err := row.Scan(&summary.MemoryCount, &lastUpdated, &lastAccessed); err != nil {
		return nil, fmt.Errorf("workspace activity summary: %w", err)
	}
	if t, err := time.Parse(time.RFC3339Nano, lastUpdated); err == nil {
		summary.LastUpdatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, lastAccessed); err == nil {
		summary.LastAccessedAt = t
	}
	return &summary, nil
}

func (s *Store) GetSchedulerWorkspaceState(ctx context.Context, workspace string) (*SchedulerWorkspaceState, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT workspace, last_scheduled_at, last_completed_at, last_result, last_skip_reason, last_duration_ms, last_impacts, last_error, updated_at
FROM scheduler_workspace_state
WHERE workspace = ?`, workspace)

	var state SchedulerWorkspaceState
	var lastScheduled string
	var lastCompleted string
	var updatedAt string
	if err := row.Scan(
		&state.Workspace,
		&lastScheduled,
		&lastCompleted,
		&state.LastResult,
		&state.LastSkipReason,
		&state.LastDurationMS,
		&state.LastImpacts,
		&state.LastError,
		&updatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get scheduler workspace state: %w", err)
	}
	if t, err := time.Parse(time.RFC3339Nano, lastScheduled); err == nil {
		state.LastScheduledAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, lastCompleted); err == nil {
		state.LastCompletedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
		state.UpdatedAt = t
	}
	return &state, nil
}

func (s *Store) UpsertSchedulerWorkspaceState(ctx context.Context, state SchedulerWorkspaceState) error {
	now := state.UpdatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO scheduler_workspace_state (
	workspace,
	last_scheduled_at,
	last_completed_at,
	last_result,
	last_skip_reason,
	last_duration_ms,
	last_impacts,
	last_error,
	updated_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(workspace) DO UPDATE SET
	last_scheduled_at = excluded.last_scheduled_at,
	last_completed_at = excluded.last_completed_at,
	last_result = excluded.last_result,
	last_skip_reason = excluded.last_skip_reason,
	last_duration_ms = excluded.last_duration_ms,
	last_impacts = excluded.last_impacts,
	last_error = excluded.last_error,
	updated_at = excluded.updated_at`,
		strings.TrimSpace(state.Workspace),
		timeStringOrEmpty(state.LastScheduledAt),
		timeStringOrEmpty(state.LastCompletedAt),
		strings.TrimSpace(state.LastResult),
		strings.TrimSpace(state.LastSkipReason),
		state.LastDurationMS,
		state.LastImpacts,
		strings.TrimSpace(state.LastError),
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert scheduler workspace state: %w", err)
	}
	return nil
}

func (s *Store) InsertSchedulerRunRecord(ctx context.Context, run SchedulerRunRecord, retain int) error {
	if retain <= 0 {
		retain = defaultSchedulerHistoryLimit
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("insert scheduler run begin: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO scheduler_run_history (
	id,
	workspace,
	started_at,
	completed_at,
	trigger,
	result,
	skip_reason,
	duration_ms,
	decay_updated,
	consolidated,
	conflicts_found,
	evicted,
	promoted,
	demoted,
	error
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(run.ID),
		strings.TrimSpace(run.Workspace),
		run.StartedAt.UTC().Format(time.RFC3339Nano),
		timeStringOrEmpty(run.CompletedAt),
		strings.TrimSpace(run.Trigger),
		strings.TrimSpace(run.Result),
		strings.TrimSpace(run.SkipReason),
		run.DurationMS,
		run.DecayUpdated,
		run.Consolidated,
		run.ConflictsFound,
		run.Evicted,
		run.Promoted,
		run.Demoted,
		strings.TrimSpace(run.Error),
	)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("insert scheduler run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM scheduler_run_history
WHERE workspace = ?
  AND id NOT IN (
    SELECT id
    FROM scheduler_run_history
    WHERE workspace = ?
    ORDER BY started_at DESC, id DESC
    LIMIT ?
  )`,
		strings.TrimSpace(run.Workspace),
		strings.TrimSpace(run.Workspace),
		retain,
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("prune scheduler run history: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("insert scheduler run commit: %w", err)
	}
	return nil
}

func (s *Store) ListSchedulerRunHistory(ctx context.Context, workspace string, limit int) ([]SchedulerRunRecord, error) {
	if limit <= 0 {
		limit = defaultSchedulerHistoryLimit
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, workspace, started_at, completed_at, trigger, result, skip_reason, duration_ms, decay_updated, consolidated, conflicts_found, evicted, promoted, demoted, error
FROM scheduler_run_history
WHERE workspace = ?
ORDER BY started_at DESC, id DESC
LIMIT ?`, workspace, limit)
	if err != nil {
		return nil, fmt.Errorf("list scheduler run history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]SchedulerRunRecord, 0, limit)
	for rows.Next() {
		var run SchedulerRunRecord
		var startedAt string
		var completedAt string
		if err := rows.Scan(
			&run.ID,
			&run.Workspace,
			&startedAt,
			&completedAt,
			&run.Trigger,
			&run.Result,
			&run.SkipReason,
			&run.DurationMS,
			&run.DecayUpdated,
			&run.Consolidated,
			&run.ConflictsFound,
			&run.Evicted,
			&run.Promoted,
			&run.Demoted,
			&run.Error,
		); err != nil {
			return nil, fmt.Errorf("scan scheduler run history: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, startedAt); err == nil {
			run.StartedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, completedAt); err == nil {
			run.CompletedAt = t
		}
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduler run history: %w", err)
	}
	return out, nil
}
