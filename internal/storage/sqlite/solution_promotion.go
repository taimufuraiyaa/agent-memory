package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SolutionPromotionInsert struct {
	EpisodeID, SummaryID, IdempotencyKey, PolicyIdentity string
	MemoryType                                           core.MemoryType
	SourceStepIDs, ObservationIDs                        []string
	CreatedAt                                            time.Time
}

func (s *Store) BeginSolutionPromotion(ctx context.Context, input SolutionPromotionInsert) (core.SolutionPromotion, bool, error) {
	if err := requireSolutionIdempotencyKey(input.IdempotencyKey); err != nil {
		return core.SolutionPromotion{}, false, err
	}
	createdAt := input.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	sourceJSON, _ := json.Marshal(input.SourceStepIDs)
	observationJSON, _ := json.Marshal(input.ObservationIDs)
	promotion := core.SolutionPromotion{ID: uuid.NewString(), EpisodeID: strings.TrimSpace(input.EpisodeID), SummaryID: strings.TrimSpace(input.SummaryID),
		Kind: core.SolutionPromotionMemory, MemoryType: input.MemoryType, SourceStepIDs: input.SourceStepIDs, ObservationIDs: input.ObservationIDs,
		State: core.SolutionPromotionPending, PolicyIdentity: strings.TrimSpace(input.PolicyIdentity), CreatedAt: createdAt}
	result, err := s.db.ExecContext(ctx, `INSERT INTO solution_promotions (id, episode_id, summary_id, kind, memory_type, target_id,
		source_step_ids_json, observation_ids_json, state, error, policy_identity, idempotency_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, '', ?, ?, ?, '', ?, ?, ?, ?) ON CONFLICT(summary_id, idempotency_key) DO NOTHING`, promotion.ID,
		promotion.EpisodeID, promotion.SummaryID, promotion.Kind, promotion.MemoryType, string(sourceJSON), string(observationJSON), promotion.State,
		promotion.PolicyIdentity, strings.TrimSpace(input.IdempotencyKey), createdAt.Format(time.RFC3339Nano), createdAt.Format(time.RFC3339Nano))
	if err != nil {
		return core.SolutionPromotion{}, false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return core.SolutionPromotion{}, false, err
	}
	if inserted == 1 {
		return promotion, false, nil
	}
	existing, err := s.GetSolutionPromotionByKey(ctx, input.SummaryID, input.IdempotencyKey)
	if err != nil {
		return core.SolutionPromotion{}, false, err
	}
	if existing.EpisodeID != promotion.EpisodeID || existing.MemoryType != promotion.MemoryType || strings.Join(existing.SourceStepIDs, "\x00") != strings.Join(input.SourceStepIDs, "\x00") {
		return core.SolutionPromotion{}, false, errors.New("solution promotion idempotency key was already used with different input")
	}
	return existing, true, nil
}

func (s *Store) CompleteSolutionPromotion(ctx context.Context, id, targetID string, state core.SolutionPromotionState, failure string) (core.SolutionPromotion, error) {
	if state != core.SolutionPromotionPublished && state != core.SolutionPromotionFailed {
		return core.SolutionPromotion{}, errors.New("invalid solution promotion completion state")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE solution_promotions SET target_id = ?, state = ?, error = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(targetID), state, strings.TrimSpace(failure), time.Now().UTC().Format(time.RFC3339Nano), strings.TrimSpace(id))
	if err != nil {
		return core.SolutionPromotion{}, err
	}
	return s.GetSolutionPromotion(ctx, id)
}

func (s *Store) GetSolutionPromotion(ctx context.Context, id string) (core.SolutionPromotion, error) {
	return scanSolutionPromotion(s.db.QueryRowContext(ctx, solutionPromotionSelect+` WHERE id = ?`, strings.TrimSpace(id)))
}

func (s *Store) GetSolutionPromotionByKey(ctx context.Context, summaryID, key string) (core.SolutionPromotion, error) {
	return scanSolutionPromotion(s.db.QueryRowContext(ctx, solutionPromotionSelect+` WHERE summary_id = ? AND idempotency_key = ?`, strings.TrimSpace(summaryID), strings.TrimSpace(key)))
}

const solutionPromotionSelect = `SELECT id, episode_id, summary_id, kind, memory_type, target_id, source_step_ids_json,
	observation_ids_json, state, error, policy_identity, created_at FROM solution_promotions`

func scanSolutionPromotion(row solutionRowScanner) (core.SolutionPromotion, error) {
	var promotion core.SolutionPromotion
	var sourceJSON, observationJSON, createdAt string
	err := row.Scan(&promotion.ID, &promotion.EpisodeID, &promotion.SummaryID, &promotion.Kind, &promotion.MemoryType,
		&promotion.TargetID, &sourceJSON, &observationJSON, &promotion.State, &promotion.Error, &promotion.PolicyIdentity, &createdAt)
	if err != nil {
		return core.SolutionPromotion{}, err
	}
	if err := json.Unmarshal([]byte(sourceJSON), &promotion.SourceStepIDs); err != nil {
		return core.SolutionPromotion{}, err
	}
	if err := json.Unmarshal([]byte(observationJSON), &promotion.ObservationIDs); err != nil {
		return core.SolutionPromotion{}, err
	}
	promotion.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return promotion, nil
}

func (s *Store) ListMemoryObservationIDs(ctx context.Context, memoryID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT observation_id FROM memory_observation_provenance WHERE memory_id = ? ORDER BY observation_id`, strings.TrimSpace(memoryID))
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
	return ids, rows.Err()
}
