package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SolutionEpisodeInsert struct {
	Workspace      string
	SessionID      string
	PrincipalID    string
	ClientID       string
	GoalSummary    string
	CapturePolicy  core.SolutionCapturePolicy
	RetentionClass core.SolutionRetentionClass
	IdempotencyKey string
	CreatedAt      time.Time
}

type SolutionStepInsert struct {
	EpisodeID        string
	Kind             core.SolutionStepKind
	Status           core.SolutionStepStatus
	Summary          string
	RationaleSummary string
	Source           string
	ParentStepIDs    []string
	References       []core.SolutionReference
	Confidence       float64
	Sensitivity      core.SolutionSensitivity
	IdempotencyKey   string
	CreatedAt        time.Time
}

type SolutionEpisodeTransition struct {
	EpisodeID         string
	Workspace         string
	PrincipalID       string
	ExpectedVersion   int64
	Status            core.SolutionEpisodeStatus
	TargetPrincipalID string
	TargetSessionID   string
	UpdatedAt         time.Time
}

func (s *Store) CreateSolutionEpisode(ctx context.Context, in SolutionEpisodeInsert) (core.SolutionEpisode, bool, error) {
	if s == nil || s.db == nil {
		return core.SolutionEpisode{}, false, errors.New("solution episode store is unavailable")
	}
	if err := requireSolutionIdempotencyKey(in.IdempotencyKey); err != nil {
		return core.SolutionEpisode{}, false, err
	}
	now := in.CreatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	episode := core.SolutionEpisode{
		ID: uuid.NewString(), Workspace: strings.TrimSpace(in.Workspace), SessionID: strings.TrimSpace(in.SessionID),
		PrincipalID: strings.TrimSpace(in.PrincipalID), ClientID: strings.TrimSpace(in.ClientID),
		GoalSummary: strings.TrimSpace(in.GoalSummary), Status: core.SolutionEpisodeActive,
		CapturePolicy: in.CapturePolicy, RetentionClass: in.RetentionClass, Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := episode.Validate(); err != nil {
		return core.SolutionEpisode{}, false, err
	}
	requestHash, err := hashSolutionRequest(struct {
		Workspace, SessionID, PrincipalID, ClientID, GoalSummary string
		CapturePolicy                                            core.SolutionCapturePolicy
		RetentionClass                                           core.SolutionRetentionClass
	}{episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID, episode.GoalSummary, episode.CapturePolicy, episode.RetentionClass})
	if err != nil {
		return core.SolutionEpisode{}, false, err
	}

	result, err := s.db.ExecContext(ctx, `INSERT INTO solution_episodes (
		id, workspace, session_id, principal_id, client_id, goal_summary, status,
		capture_policy, retention_class, version, next_step_ordinal, superseded_by,
		idempotency_key, request_hash, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 1, '', ?, ?, ?, ?)
	ON CONFLICT(workspace, client_id, idempotency_key) DO NOTHING`,
		episode.ID, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID,
		episode.GoalSummary, episode.Status, episode.CapturePolicy, episode.RetentionClass,
		strings.TrimSpace(in.IdempotencyKey), requestHash, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return core.SolutionEpisode{}, false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return core.SolutionEpisode{}, false, err
	}
	if inserted == 1 {
		return episode, false, nil
	}

	existing, existingHash, err := s.getSolutionEpisodeByIdempotency(ctx, episode.Workspace, episode.ClientID, strings.TrimSpace(in.IdempotencyKey))
	if err != nil {
		return core.SolutionEpisode{}, false, err
	}
	if existingHash != requestHash {
		return core.SolutionEpisode{}, false, errors.New("solution episode idempotency key was already used with different input")
	}
	return existing, true, nil
}

func (s *Store) GetSolutionEpisode(ctx context.Context, id string) (core.SolutionEpisode, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, workspace, session_id, principal_id, client_id,
		goal_summary, status, capture_policy, retention_class, version, superseded_by, created_at, updated_at
		FROM solution_episodes WHERE id = ?`, strings.TrimSpace(id))
	episode, _, err := scanSolutionEpisode(row, false)
	return episode, err
}

func (s *Store) TransitionSolutionEpisode(ctx context.Context, in SolutionEpisodeTransition) (core.SolutionEpisode, error) {
	updatedAt := in.UpdatedAt.UTC()
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	principalID := strings.TrimSpace(in.TargetPrincipalID)
	if principalID == "" {
		principalID = strings.TrimSpace(in.PrincipalID)
	}
	sessionID := strings.TrimSpace(in.TargetSessionID)
	if sessionID == "" {
		episode, err := s.GetSolutionEpisode(ctx, in.EpisodeID)
		if err != nil {
			return core.SolutionEpisode{}, err
		}
		sessionID = episode.SessionID
	}
	row := s.db.QueryRowContext(ctx, `UPDATE solution_episodes SET
		status = ?, principal_id = ?, session_id = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND workspace = ? AND principal_id = ? AND version = ?
		RETURNING id, workspace, session_id, principal_id, client_id, goal_summary, status,
		capture_policy, retention_class, version, superseded_by, created_at, updated_at`,
		in.Status, principalID, sessionID, updatedAt.Format(time.RFC3339Nano), strings.TrimSpace(in.EpisodeID),
		strings.TrimSpace(in.Workspace), strings.TrimSpace(in.PrincipalID), in.ExpectedVersion)
	episode, _, err := scanSolutionEpisode(row, false)
	return episode, err
}

func (s *Store) FindActiveSolutionEpisode(ctx context.Context, workspace, sessionID, principalID string) (core.SolutionEpisode, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, workspace, session_id, principal_id, client_id,
		goal_summary, status, capture_policy, retention_class, version, superseded_by, created_at, updated_at
		FROM solution_episodes WHERE workspace = ? AND session_id = ? AND principal_id = ?
		AND status IN (?, ?) ORDER BY updated_at DESC LIMIT 1`, strings.TrimSpace(workspace),
		strings.TrimSpace(sessionID), strings.TrimSpace(principalID), core.SolutionEpisodeActive, core.SolutionEpisodePaused)
	episode, _, err := scanSolutionEpisode(row, false)
	return episode, err
}

func (s *Store) getSolutionEpisodeByIdempotency(ctx context.Context, workspace, clientID, key string) (core.SolutionEpisode, string, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, workspace, session_id, principal_id, client_id,
		goal_summary, status, capture_policy, retention_class, version, superseded_by, created_at, updated_at, request_hash
		FROM solution_episodes WHERE workspace = ? AND client_id = ? AND idempotency_key = ?`, workspace, clientID, key)
	return scanSolutionEpisode(row, true)
}

type solutionRowScanner interface {
	Scan(dest ...any) error
}

func scanSolutionEpisode(row solutionRowScanner, includeHash bool) (core.SolutionEpisode, string, error) {
	var episode core.SolutionEpisode
	var createdAt, updatedAt, requestHash string
	dest := []any{&episode.ID, &episode.Workspace, &episode.SessionID, &episode.PrincipalID, &episode.ClientID,
		&episode.GoalSummary, &episode.Status, &episode.CapturePolicy, &episode.RetentionClass,
		&episode.Version, &episode.SupersededBy, &createdAt, &updatedAt}
	if includeHash {
		dest = append(dest, &requestHash)
	}
	if err := row.Scan(dest...); err != nil {
		return core.SolutionEpisode{}, "", err
	}
	episode.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	episode.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return episode, requestHash, nil
}

func (s *Store) AppendSolutionStep(ctx context.Context, in SolutionStepInsert) (core.SolutionStep, bool, error) {
	if s == nil || s.db == nil {
		return core.SolutionStep{}, false, errors.New("solution step store is unavailable")
	}
	if err := requireSolutionIdempotencyKey(in.IdempotencyKey); err != nil {
		return core.SolutionStep{}, false, err
	}
	if strings.TrimSpace(in.EpisodeID) == "" {
		return core.SolutionStep{}, false, errors.New("solution step episode_id is required")
	}
	requestHash, err := hashSolutionRequest(struct {
		EpisodeID, Summary, RationaleSummary, Source string
		Kind                                         core.SolutionStepKind
		Status                                       core.SolutionStepStatus
		ParentStepIDs                                []string
		References                                   []core.SolutionReference
		Confidence                                   float64
		Sensitivity                                  core.SolutionSensitivity
	}{strings.TrimSpace(in.EpisodeID), strings.TrimSpace(in.Summary), strings.TrimSpace(in.RationaleSummary), strings.TrimSpace(in.Source), in.Kind, in.Status, in.ParentStepIDs, in.References, in.Confidence, in.Sensitivity})
	if err != nil {
		return core.SolutionStep{}, false, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.SolutionStep{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	stepID := uuid.NewString()
	createdAt := in.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO solution_step_requests (
		episode_id, idempotency_key, request_hash, step_id, created_at
	) VALUES (?, ?, ?, ?, ?) ON CONFLICT(episode_id, idempotency_key) DO NOTHING`,
		strings.TrimSpace(in.EpisodeID), strings.TrimSpace(in.IdempotencyKey), requestHash, stepID, createdAt.Format(time.RFC3339Nano))
	if err != nil {
		return core.SolutionStep{}, false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return core.SolutionStep{}, false, err
	}
	if inserted == 0 {
		var existingID, existingHash string
		if err := tx.QueryRowContext(ctx, `SELECT step_id, request_hash FROM solution_step_requests
			WHERE episode_id = ? AND idempotency_key = ?`, strings.TrimSpace(in.EpisodeID), strings.TrimSpace(in.IdempotencyKey)).Scan(&existingID, &existingHash); err != nil {
			return core.SolutionStep{}, false, err
		}
		if existingHash != requestHash {
			return core.SolutionStep{}, false, errors.New("solution step idempotency key was already used with different input")
		}
		step, err := getSolutionStepTx(ctx, tx, existingID)
		if err != nil {
			return core.SolutionStep{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return core.SolutionStep{}, false, err
		}
		return step, true, nil
	}

	var ordinal int64
	if err := tx.QueryRowContext(ctx, `UPDATE solution_episodes
		SET version = version + 1, next_step_ordinal = next_step_ordinal + 1, updated_at = ?
		WHERE id = ? AND status IN (?, ?)
		RETURNING next_step_ordinal - 1`, createdAt.Format(time.RFC3339Nano), strings.TrimSpace(in.EpisodeID),
		core.SolutionEpisodeActive, core.SolutionEpisodePaused).Scan(&ordinal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.SolutionStep{}, false, errors.New("solution episode is missing or terminal")
		}
		return core.SolutionStep{}, false, err
	}
	parentsJSON, err := json.Marshal(in.ParentStepIDs)
	if err != nil {
		return core.SolutionStep{}, false, err
	}
	step := core.SolutionStep{
		ID: stepID, EpisodeID: strings.TrimSpace(in.EpisodeID), Ordinal: ordinal, Kind: in.Kind, Status: in.Status,
		Summary: strings.TrimSpace(in.Summary), RationaleSummary: strings.TrimSpace(in.RationaleSummary),
		Source: strings.TrimSpace(in.Source), ParentStepIDs: in.ParentStepIDs, References: in.References,
		Confidence: in.Confidence, Sensitivity: in.Sensitivity, CreatedAt: createdAt,
	}
	if err := step.Validate(); err != nil {
		return core.SolutionStep{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO solution_steps (
		id, episode_id, ordinal, kind, status, summary, rationale_summary, source,
		parent_step_ids_json, confidence, sensitivity, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, step.ID, step.EpisodeID, step.Ordinal,
		step.Kind, step.Status, step.Summary, step.RationaleSummary, step.Source, string(parentsJSON),
		step.Confidence, step.Sensitivity, step.CreatedAt.Format(time.RFC3339Nano)); err != nil {
		return core.SolutionStep{}, false, err
	}
	for i, reference := range step.References {
		if _, err := tx.ExecContext(ctx, `INSERT INTO solution_step_references
			(step_id, ordinal, kind, target_id, locator) VALUES (?, ?, ?, ?, ?)`,
			step.ID, i+1, reference.Kind, reference.TargetID, reference.Locator); err != nil {
			return core.SolutionStep{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return core.SolutionStep{}, false, err
	}
	return step, false, nil
}

func (s *Store) ListSolutionSteps(ctx context.Context, episodeID string, afterOrdinal int64, limit int) ([]core.SolutionStep, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, episode_id, ordinal, kind, status, summary,
		rationale_summary, source, parent_step_ids_json, confidence, sensitivity, created_at
		FROM solution_steps WHERE episode_id = ? AND ordinal > ? ORDER BY ordinal ASC LIMIT ?`,
		strings.TrimSpace(episodeID), afterOrdinal, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	steps := make([]core.SolutionStep, 0)
	for rows.Next() {
		step, err := scanSolutionStep(rows)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range steps {
		references, err := s.listSolutionStepReferences(ctx, steps[i].ID)
		if err != nil {
			return nil, err
		}
		steps[i].References = references
	}
	return steps, nil
}

func getSolutionStepTx(ctx context.Context, tx *sql.Tx, id string) (core.SolutionStep, error) {
	row := tx.QueryRowContext(ctx, `SELECT id, episode_id, ordinal, kind, status, summary,
		rationale_summary, source, parent_step_ids_json, confidence, sensitivity, created_at
		FROM solution_steps WHERE id = ?`, id)
	step, err := scanSolutionStep(row)
	if err != nil {
		return core.SolutionStep{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT kind, target_id, locator FROM solution_step_references WHERE step_id = ? ORDER BY ordinal`, id)
	if err != nil {
		return core.SolutionStep{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var reference core.SolutionReference
		if err := rows.Scan(&reference.Kind, &reference.TargetID, &reference.Locator); err != nil {
			return core.SolutionStep{}, err
		}
		step.References = append(step.References, reference)
	}
	return step, rows.Err()
}

func scanSolutionStep(row solutionRowScanner) (core.SolutionStep, error) {
	var step core.SolutionStep
	var parentsJSON, createdAt string
	if err := row.Scan(&step.ID, &step.EpisodeID, &step.Ordinal, &step.Kind, &step.Status,
		&step.Summary, &step.RationaleSummary, &step.Source, &parentsJSON, &step.Confidence,
		&step.Sensitivity, &createdAt); err != nil {
		return core.SolutionStep{}, err
	}
	if err := json.Unmarshal([]byte(parentsJSON), &step.ParentStepIDs); err != nil {
		return core.SolutionStep{}, fmt.Errorf("decode solution step parents: %w", err)
	}
	step.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return step, nil
}

func (s *Store) listSolutionStepReferences(ctx context.Context, stepID string) ([]core.SolutionReference, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, target_id, locator FROM solution_step_references
		WHERE step_id = ? ORDER BY ordinal`, stepID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	references := make([]core.SolutionReference, 0)
	for rows.Next() {
		var reference core.SolutionReference
		if err := rows.Scan(&reference.Kind, &reference.TargetID, &reference.Locator); err != nil {
			return nil, err
		}
		references = append(references, reference)
	}
	return references, rows.Err()
}

func requireSolutionIdempotencyKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("solution idempotency key is required")
	}
	if len(key) > 256 {
		return errors.New("solution idempotency key exceeds 256 bytes")
	}
	return nil
}

func hashSolutionRequest(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
