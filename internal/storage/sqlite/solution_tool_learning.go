package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SolutionToolEventInsert struct {
	Record         core.SolutionToolInvocationRecord
	IdempotencyKey string
}

func (s *Store) InsertSolutionToolEvent(ctx context.Context, input SolutionToolEventInsert) (core.SolutionToolInvocationRecord, bool, error) {
	if err := requireSolutionIdempotencyKey(input.IdempotencyKey); err != nil {
		return core.SolutionToolInvocationRecord{}, false, err
	}
	record := input.Record
	requestHash, err := hashSolutionRequest(record)
	if err != nil {
		return core.SolutionToolInvocationRecord{}, false, err
	}
	if record.ID == "" {
		record.ID = uuid.NewString()
	}
	if record.OccurredAt.IsZero() {
		record.OccurredAt = time.Now().UTC()
	}
	if err := record.Validate(); err != nil {
		return core.SolutionToolInvocationRecord{}, false, err
	}
	evidenceJSON, _ := json.Marshal(record.Evidence)
	result, err := s.db.ExecContext(ctx, `INSERT INTO solution_tool_events (id, workspace, episode_id, step_id, kind, tool_name,
		tool_version, operation, capability, input_summary, result_class, task_verified, duration_ms, evidence_json,
		idempotency_key, request_hash, occurred_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(episode_id, idempotency_key) DO NOTHING`, record.ID, record.Workspace, record.EpisodeID, record.StepID,
		record.Kind, record.ToolName, record.ToolVersion, record.Operation, record.Capability, record.InputSummary, record.ResultClass,
		record.TaskVerified, record.DurationMS, string(evidenceJSON), strings.TrimSpace(input.IdempotencyKey), requestHash, record.OccurredAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return core.SolutionToolInvocationRecord{}, false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return core.SolutionToolInvocationRecord{}, false, err
	}
	if inserted == 1 {
		return record, false, nil
	}
	existing, existingHash, err := s.getSolutionToolEventByKey(ctx, record.EpisodeID, input.IdempotencyKey)
	if err != nil {
		return core.SolutionToolInvocationRecord{}, false, err
	}
	if existingHash != requestHash {
		return core.SolutionToolInvocationRecord{}, false, errors.New("solution tool event idempotency key was already used with different input")
	}
	return existing, true, nil
}

func (s *Store) GetSolutionToolEvent(ctx context.Context, id string) (core.SolutionToolInvocationRecord, error) {
	record, _, err := scanSolutionToolEvent(s.db.QueryRowContext(ctx, solutionToolEventSelect+` WHERE id = ?`, strings.TrimSpace(id)))
	return record, err
}

func (s *Store) getSolutionToolEventByKey(ctx context.Context, episodeID, key string) (core.SolutionToolInvocationRecord, string, error) {
	return scanSolutionToolEvent(s.db.QueryRowContext(ctx, solutionToolEventSelect+` WHERE episode_id = ? AND idempotency_key = ?`, strings.TrimSpace(episodeID), strings.TrimSpace(key)))
}

const solutionToolEventSelect = `SELECT id, workspace, episode_id, step_id, kind, tool_name, tool_version, operation,
	capability, input_summary, result_class, task_verified, duration_ms, evidence_json, occurred_at, request_hash FROM solution_tool_events`

func scanSolutionToolEvent(row *sql.Row) (core.SolutionToolInvocationRecord, string, error) {
	var record core.SolutionToolInvocationRecord
	var verified bool
	var evidenceJSON, occurredAt, requestHash string
	err := row.Scan(&record.ID, &record.Workspace, &record.EpisodeID, &record.StepID, &record.Kind, &record.ToolName,
		&record.ToolVersion, &record.Operation, &record.Capability, &record.InputSummary, &record.ResultClass, &verified,
		&record.DurationMS, &evidenceJSON, &occurredAt, &requestHash)
	if err != nil {
		return core.SolutionToolInvocationRecord{}, "", err
	}
	record.TaskVerified = verified
	record.OccurredAt, _ = time.Parse(time.RFC3339Nano, occurredAt)
	if err := json.Unmarshal([]byte(evidenceJSON), &record.Evidence); err != nil {
		return core.SolutionToolInvocationRecord{}, "", err
	}
	return record, requestHash, nil
}

func (s *Store) PutSolutionToolLesson(ctx context.Context, lesson core.SolutionToolLesson) (core.SolutionToolLesson, bool, error) {
	eventIDs := append([]string(nil), lesson.SourceEventIDs...)
	sort.Strings(eventIDs)
	sum := sha256.Sum256([]byte(strings.Join(eventIDs, "\x00")))
	sourceHash := hex.EncodeToString(sum[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.SolutionToolLesson{}, false, err
	}
	defer tx.Rollback()
	var existingJSON string
	if err := tx.QueryRowContext(ctx, `SELECT lesson_json FROM solution_tool_lessons WHERE workspace = ? AND source_hash = ?`, lesson.Workspace, sourceHash).Scan(&existingJSON); err == nil {
		var existing core.SolutionToolLesson
		if err := json.Unmarshal([]byte(existingJSON), &existing); err != nil {
			return core.SolutionToolLesson{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return core.SolutionToolLesson{}, false, err
		}
		return existing, true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return core.SolutionToolLesson{}, false, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM solution_tool_lessons WHERE workspace = ? AND tool_name = ? AND capability = ?`, lesson.Workspace, lesson.ToolName, lesson.Capability).Scan(&lesson.Version); err != nil {
		return core.SolutionToolLesson{}, false, err
	}
	var previousID string
	previousErr := tx.QueryRowContext(ctx, `SELECT id FROM solution_tool_lessons WHERE workspace = ? AND tool_name = ? AND capability = ? ORDER BY version DESC LIMIT 1`, lesson.Workspace, lesson.ToolName, lesson.Capability).Scan(&previousID)
	if previousErr != nil && !errors.Is(previousErr, sql.ErrNoRows) {
		return core.SolutionToolLesson{}, false, previousErr
	}
	if lesson.ID == "" {
		lesson.ID = uuid.NewString()
	}
	if lesson.CreatedAt.IsZero() {
		lesson.CreatedAt = time.Now().UTC()
	}
	if err := lesson.Validate(); err != nil {
		return core.SolutionToolLesson{}, false, err
	}
	lessonJSON, _ := json.Marshal(lesson)
	_, err = tx.ExecContext(ctx, `INSERT INTO solution_tool_lessons (id, workspace, tool_name, capability, version, lesson_json, source_hash, superseded_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, '', ?)`, lesson.ID, lesson.Workspace, lesson.ToolName, lesson.Capability, lesson.Version, string(lessonJSON), sourceHash, lesson.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return core.SolutionToolLesson{}, false, err
	}
	if previousID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE solution_tool_lessons SET superseded_by = ? WHERE id = ?`, lesson.ID, previousID); err != nil {
			return core.SolutionToolLesson{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return core.SolutionToolLesson{}, false, err
	}
	return lesson, false, nil
}

func (s *Store) GetSolutionToolLesson(ctx context.Context, id string) (core.SolutionToolLesson, error) {
	var lessonJSON, supersededBy string
	err := s.db.QueryRowContext(ctx, `SELECT lesson_json, superseded_by FROM solution_tool_lessons WHERE id = ?`, strings.TrimSpace(id)).Scan(&lessonJSON, &supersededBy)
	if err != nil {
		return core.SolutionToolLesson{}, err
	}
	var lesson core.SolutionToolLesson
	if err := json.Unmarshal([]byte(lessonJSON), &lesson); err != nil {
		return core.SolutionToolLesson{}, err
	}
	lesson.SupersededBy = supersededBy
	return lesson, nil
}

func (s *Store) BeginToolLessonPromotion(ctx context.Context, lessonID, episodeID, key, policy string, at time.Time) (core.SolutionPromotion, bool, error) {
	if err := requireSolutionIdempotencyKey(key); err != nil {
		return core.SolutionPromotion{}, false, err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	promotion := core.SolutionPromotion{ID: uuid.NewString(), EpisodeID: episodeID, ToolLessonID: lessonID, Kind: core.SolutionPromotionMemory,
		MemoryType: core.ProceduralMemory, State: core.SolutionPromotionPending, PolicyIdentity: policy, CreatedAt: at}
	result, err := s.db.ExecContext(ctx, `INSERT INTO solution_tool_lesson_promotions (id, lesson_id, episode_id, memory_id, state, error,
		idempotency_key, policy_identity, created_at, updated_at) VALUES (?, ?, ?, '', ?, '', ?, ?, ?, ?)
		ON CONFLICT(lesson_id, idempotency_key) DO NOTHING`, promotion.ID, lessonID, episodeID, promotion.State, key, policy,
		at.UTC().Format(time.RFC3339Nano), at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return core.SolutionPromotion{}, false, err
	}
	inserted, _ := result.RowsAffected()
	if inserted == 1 {
		return promotion, false, nil
	}
	existing, err := s.GetToolLessonPromotionByKey(ctx, lessonID, key)
	return existing, true, err
}

func (s *Store) CompleteToolLessonPromotion(ctx context.Context, id, memoryID string, state core.SolutionPromotionState, failure string) (core.SolutionPromotion, error) {
	_, err := s.db.ExecContext(ctx, `UPDATE solution_tool_lesson_promotions SET memory_id = ?, state = ?, error = ?, updated_at = ? WHERE id = ?`,
		memoryID, state, failure, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return core.SolutionPromotion{}, err
	}
	return s.GetToolLessonPromotion(ctx, id)
}

func (s *Store) GetToolLessonPromotion(ctx context.Context, id string) (core.SolutionPromotion, error) {
	return scanToolLessonPromotion(s.db.QueryRowContext(ctx, toolLessonPromotionSelect+` WHERE id = ?`, id))
}

func (s *Store) GetToolLessonPromotionByKey(ctx context.Context, lessonID, key string) (core.SolutionPromotion, error) {
	return scanToolLessonPromotion(s.db.QueryRowContext(ctx, toolLessonPromotionSelect+` WHERE lesson_id = ? AND idempotency_key = ?`, lessonID, key))
}

const toolLessonPromotionSelect = `SELECT id, lesson_id, episode_id, memory_id, state, error, policy_identity, created_at FROM solution_tool_lesson_promotions`

func scanToolLessonPromotion(row *sql.Row) (core.SolutionPromotion, error) {
	promotion := core.SolutionPromotion{Kind: core.SolutionPromotionMemory, MemoryType: core.ProceduralMemory}
	var createdAt string
	err := row.Scan(&promotion.ID, &promotion.ToolLessonID, &promotion.EpisodeID, &promotion.TargetID, &promotion.State, &promotion.Error, &promotion.PolicyIdentity, &createdAt)
	if err != nil {
		return core.SolutionPromotion{}, err
	}
	promotion.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return promotion, nil
}

func (s *Store) ListPublishedToolLessonPromotionMemoryIDs(ctx context.Context, lessonIDs []string) ([]string, error) {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(lessonIDs))
	for _, lessonID := range lessonIDs {
		rows, err := s.db.QueryContext(ctx, `SELECT memory_id FROM solution_tool_lesson_promotions WHERE lesson_id = ? AND state = ? AND memory_id != '' ORDER BY created_at`, strings.TrimSpace(lessonID), core.SolutionPromotionPublished)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				result = append(result, id)
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return result, nil
}
