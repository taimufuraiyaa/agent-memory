package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SolutionSummaryCandidate struct {
	Summary core.SolutionSummary
	Episode core.SolutionEpisode
}

func (s *Store) ListCurrentSolutionSummaries(ctx context.Context, workspace string, limit int) ([]SolutionSummaryCandidate, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT s.id FROM solution_summaries s JOIN solution_episodes e ON e.id = s.episode_id
		WHERE e.workspace = ? AND s.superseded_by = '' ORDER BY s.created_at DESC LIMIT ?`, strings.TrimSpace(workspace), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	result := make([]SolutionSummaryCandidate, 0, len(ids))
	for _, id := range ids {
		summary, err := s.GetSolutionSummary(ctx, id)
		if err != nil {
			return nil, err
		}
		episode, err := s.GetSolutionEpisode(ctx, summary.EpisodeID)
		if err != nil {
			return nil, err
		}
		result = append(result, SolutionSummaryCandidate{summary, episode})
	}
	return result, nil
}

func (s *Store) ListCurrentSolutionToolLessons(ctx context.Context, workspace string, limit int) ([]core.SolutionToolLesson, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT lesson_json, superseded_by FROM solution_tool_lessons WHERE workspace = ? AND superseded_by = '' ORDER BY created_at DESC LIMIT ?`, strings.TrimSpace(workspace), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lessons := make([]core.SolutionToolLesson, 0)
	for rows.Next() {
		var raw, superseded string
		if err := rows.Scan(&raw, &superseded); err != nil {
			return nil, err
		}
		var lesson core.SolutionToolLesson
		if err := json.Unmarshal([]byte(raw), &lesson); err != nil {
			return nil, err
		}
		lesson.SupersededBy = superseded
		lessons = append(lessons, lesson)
	}
	return lessons, rows.Err()
}

func (s *Store) ListCurrentVerifiedSolutionToolLessonsAfter(ctx context.Context, workspace, afterID string, limit int) ([]core.SolutionToolLesson, error) {
	if limit < 1 || limit > 1_000 {
		return nil, errors.New("invalid verified tool lesson page")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT lesson_json,superseded_by FROM solution_tool_lessons WHERE workspace=? AND superseded_by='' AND id>? AND json_extract(lesson_json,'$.validation')='verified' ORDER BY id LIMIT ?`, strings.TrimSpace(workspace), strings.TrimSpace(afterID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lessons := make([]core.SolutionToolLesson, 0, limit)
	for rows.Next() {
		var raw, superseded string
		if err := rows.Scan(&raw, &superseded); err != nil {
			return nil, err
		}
		var lesson core.SolutionToolLesson
		if err := json.Unmarshal([]byte(raw), &lesson); err != nil {
			return nil, err
		}
		lesson.SupersededBy = superseded
		lessons = append(lessons, lesson)
	}
	return lessons, rows.Err()
}

func (s *Store) FindSolutionWorkingStateForSession(ctx context.Context, workspace, principalID, sessionID string, now time.Time) (core.SolutionWorkingState, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT state_json FROM solution_working_state WHERE workspace = ? AND principal_id = ? AND session_id = ? AND expires_at > ? ORDER BY updated_at DESC LIMIT 1`,
		strings.TrimSpace(workspace), strings.TrimSpace(principalID), strings.TrimSpace(sessionID), now.UTC().Format(time.RFC3339Nano)).Scan(&raw)
	if err != nil {
		return core.SolutionWorkingState{}, err
	}
	var state core.SolutionWorkingState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return core.SolutionWorkingState{}, err
	}
	return state, nil
}

func (s *Store) PutHowRetrievalFeedback(ctx context.Context, workspace, targetKind, targetID string, outcome core.RetrievalFeedback, at time.Time) error {
	if outcome != core.FeedbackHelpful && outcome != core.FeedbackIgnored && outcome != core.FeedbackRejected && outcome != core.FeedbackHarmful {
		return errors.New("invalid how retrieval feedback")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO solution_retrieval_feedback (id, workspace, target_kind, target_id, outcome, created_at) VALUES (?, ?, ?, ?, ?, ?)`, uuid.NewString(), workspace, targetKind, targetID, outcome, at.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) HowRetrievalFeedbackOutcome(ctx context.Context, workspace, targetKind, targetID string) (core.RetrievalFeedback, error) {
	var outcome core.RetrievalFeedback
	err := s.db.QueryRowContext(ctx, `SELECT outcome FROM solution_retrieval_feedback WHERE workspace = ? AND target_kind = ? AND target_id = ? ORDER BY created_at DESC LIMIT 1`, workspace, targetKind, targetID).Scan(&outcome)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return outcome, err
}

func (s *Store) ListHowRetrievalFeedback(ctx context.Context, workspace, targetKind, targetID string, limit int) ([]core.SolutionRetrievalFeedbackRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, workspace, target_kind, target_id, outcome, created_at
		FROM solution_retrieval_feedback WHERE workspace = ? AND target_kind = ? AND target_id = ?
		ORDER BY created_at DESC, id DESC LIMIT ?`, strings.TrimSpace(workspace), strings.TrimSpace(targetKind), strings.TrimSpace(targetID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.SolutionRetrievalFeedbackRecord, 0)
	for rows.Next() {
		var item core.SolutionRetrievalFeedbackRecord
		var createdAt string
		if err := rows.Scan(&item.ID, &item.Workspace, &item.TargetKind, &item.TargetID, &item.Outcome, &createdAt); err != nil {
			return nil, err
		}
		item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) PutDistilledSkillMetadata(ctx context.Context, metadata core.DistilledSkillMetadata) error {
	if metadata.ID == "" {
		metadata.ID = uuid.NewString()
	}
	if metadata.CreatedAt.IsZero() {
		metadata.CreatedAt = time.Now().UTC()
	}
	memoryJSON, _ := json.Marshal(metadata.MemoryIDs)
	lessonJSON, _ := json.Marshal(metadata.ToolLessonIDs)
	episodeJSON, _ := json.Marshal(metadata.EpisodeIDs)
	_, err := s.db.ExecContext(ctx, `INSERT INTO distilled_skill_metadata (id, workspace, name, path, memory_ids_json, tool_lesson_ids_json, episode_ids_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(workspace, name) DO UPDATE SET path = excluded.path, memory_ids_json = excluded.memory_ids_json,
		tool_lesson_ids_json = excluded.tool_lesson_ids_json, episode_ids_json = excluded.episode_ids_json, created_at = excluded.created_at`, metadata.ID,
		metadata.Workspace, metadata.Name, metadata.Path, string(memoryJSON), string(lessonJSON), string(episodeJSON), metadata.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) ListDistilledSkillMetadata(ctx context.Context, workspace string, limit int) ([]core.DistilledSkillMetadata, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, workspace, name, path, memory_ids_json, tool_lesson_ids_json, episode_ids_json, created_at FROM distilled_skill_metadata WHERE workspace = ? ORDER BY created_at DESC LIMIT ?`, workspace, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.DistilledSkillMetadata, 0)
	for rows.Next() {
		var item core.DistilledSkillMetadata
		var memories, lessons, episodes, created string
		if err := rows.Scan(&item.ID, &item.Workspace, &item.Name, &item.Path, &memories, &lessons, &episodes, &created); err != nil {
			return nil, err
		}
		if err := decodeDistilledSkillMetadata(&item, memories, lessons, episodes, created); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) GetDistilledSkillMetadata(ctx context.Context, workspace, id string) (core.DistilledSkillMetadata, error) {
	var item core.DistilledSkillMetadata
	var memories, lessons, episodes, created string
	err := s.db.QueryRowContext(ctx, `SELECT id, workspace, name, path, memory_ids_json, tool_lesson_ids_json, episode_ids_json, created_at
		FROM distilled_skill_metadata WHERE workspace = ? AND id = ?`, workspace, id).Scan(
		&item.ID, &item.Workspace, &item.Name, &item.Path, &memories, &lessons, &episodes, &created)
	if err != nil {
		return core.DistilledSkillMetadata{}, err
	}
	if err := decodeDistilledSkillMetadata(&item, memories, lessons, episodes, created); err != nil {
		return core.DistilledSkillMetadata{}, err
	}
	return item, nil
}

func decodeDistilledSkillMetadata(item *core.DistilledSkillMetadata, memories, lessons, episodes, created string) error {
	if err := json.Unmarshal([]byte(memories), &item.MemoryIDs); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(lessons), &item.ToolLessonIDs); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(episodes), &item.EpisodeIDs); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return err
	}
	item.CreatedAt = parsed
	return nil
}
