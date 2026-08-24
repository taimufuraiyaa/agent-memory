package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func (s *Store) ListSolutionEpisodes(ctx context.Context, workspace string, limit int) ([]core.SolutionEpisode, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, workspace, session_id, principal_id, client_id,
		goal_summary, status, capture_policy, retention_class, version, superseded_by, created_at, updated_at
		FROM solution_episodes WHERE workspace = ? ORDER BY updated_at DESC, id DESC LIMIT ?`, strings.TrimSpace(workspace), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.SolutionEpisode, 0)
	for rows.Next() {
		episode, _, err := scanSolutionEpisode(rows, false)
		if err != nil {
			return nil, err
		}
		result = append(result, episode)
	}
	return result, rows.Err()
}

func (s *Store) CountSolutionSteps(ctx context.Context, episodeID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM solution_steps WHERE episode_id = ?`, strings.TrimSpace(episodeID)).Scan(&count)
	return count, err
}

func (s *Store) ListSolutionPromotionsByEpisode(ctx context.Context, episodeID string) ([]core.SolutionPromotion, error) {
	rows, err := s.db.QueryContext(ctx, solutionPromotionSelect+` WHERE episode_id = ? ORDER BY created_at, id`, strings.TrimSpace(episodeID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.SolutionPromotion, 0)
	for rows.Next() {
		promotion, err := scanSolutionPromotion(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, promotion)
	}
	return result, rows.Err()
}

func (s *Store) SolutionEpisodePinned(ctx context.Context, workspace, episodeID string) (bool, error) {
	var pinned bool
	err := s.db.QueryRowContext(ctx, `SELECT pinned FROM solution_episode_reviews WHERE workspace = ? AND episode_id = ?`, strings.TrimSpace(workspace), strings.TrimSpace(episodeID)).Scan(&pinned)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return pinned, err
}

func (s *Store) SetSolutionEpisodePinned(ctx context.Context, workspace, episodeID string, pinned bool, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO solution_episode_reviews (episode_id, workspace, pinned, updated_at)
		VALUES (?, ?, ?, ?) ON CONFLICT(episode_id) DO UPDATE SET pinned = excluded.pinned, updated_at = excluded.updated_at
		WHERE solution_episode_reviews.workspace = excluded.workspace`, strings.TrimSpace(episodeID), strings.TrimSpace(workspace), pinned, at.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) PutSolutionStepReview(ctx context.Context, review core.SolutionStepReview) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO solution_step_reviews
		(step_id, episode_id, workspace, misleading, redacted, reason, reason_class, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(step_id) DO UPDATE SET
		misleading = MAX(solution_step_reviews.misleading, excluded.misleading),
		redacted = MAX(solution_step_reviews.redacted, excluded.redacted),
		reason = CASE WHEN excluded.reason = '' THEN solution_step_reviews.reason ELSE excluded.reason END,
		reason_class = CASE WHEN excluded.reason_class = '' THEN solution_step_reviews.reason_class ELSE excluded.reason_class END,
		updated_at = excluded.updated_at WHERE solution_step_reviews.workspace = excluded.workspace`,
		review.StepID, review.EpisodeID, review.Workspace, review.Misleading, review.Redacted,
		strings.TrimSpace(review.Reason), strings.TrimSpace(review.ReasonClass), review.UpdatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) ListSolutionStepReviews(ctx context.Context, workspace, episodeID string) ([]core.SolutionStepReview, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT step_id, episode_id, workspace, misleading, redacted, reason, reason_class, updated_at
		FROM solution_step_reviews WHERE workspace = ? AND episode_id = ? ORDER BY updated_at, step_id`, strings.TrimSpace(workspace), strings.TrimSpace(episodeID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.SolutionStepReview, 0)
	for rows.Next() {
		var review core.SolutionStepReview
		var updated string
		if err := rows.Scan(&review.StepID, &review.EpisodeID, &review.Workspace, &review.Misleading, &review.Redacted, &review.Reason, &review.ReasonClass, &updated); err != nil {
			return nil, err
		}
		review.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, err
		}
		result = append(result, review)
	}
	return result, rows.Err()
}

func (s *Store) RedactSolutionStep(ctx context.Context, workspace, episodeID, stepID, marker, reasonClass string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE solution_steps SET summary = ?, rationale_summary = ''
		WHERE id = ? AND episode_id = ? AND EXISTS (SELECT 1 FROM solution_episodes WHERE id = ? AND workspace = ?)`,
		marker, strings.TrimSpace(stepID), strings.TrimSpace(episodeID), strings.TrimSpace(episodeID), strings.TrimSpace(workspace))
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return sql.ErrNoRows
	}
	if _, err = tx.ExecContext(ctx, `UPDATE solution_summaries SET validation = ? WHERE episode_id = ? AND superseded_by = ''`, core.SolutionValidationRejected, strings.TrimSpace(episodeID)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO solution_step_reviews
		(step_id, episode_id, workspace, misleading, redacted, reason, reason_class, updated_at)
		VALUES (?, ?, ?, 0, 1, '', ?, ?) ON CONFLICT(step_id) DO UPDATE SET redacted = 1,
		reason_class = excluded.reason_class, updated_at = excluded.updated_at WHERE solution_step_reviews.workspace = excluded.workspace`,
		strings.TrimSpace(stepID), strings.TrimSpace(episodeID), strings.TrimSpace(workspace), strings.TrimSpace(reasonClass), at.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SupersedeSolutionEpisode(ctx context.Context, workspace, episodeID, successorID string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE solution_episodes SET superseded_by = ?, updated_at = ? WHERE id = ? AND workspace = ? AND superseded_by = ''`,
		strings.TrimSpace(successorID), at.UTC().Format(time.RFC3339Nano), strings.TrimSpace(episodeID), strings.TrimSpace(workspace))
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteSolutionEpisode(ctx context.Context, workspace, episodeID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM solution_episodes WHERE id = ? AND workspace = ?`, strings.TrimSpace(episodeID), strings.TrimSpace(workspace))
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return sql.ErrNoRows
	}
	return nil
}
