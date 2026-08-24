package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/config"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

// ApplyRetrievalFeedback issues a single atomic UPDATE to increment counters
// and adjust scores without a prior read, avoiding lost updates.
// Returns the updated MemoryEntry after the write.
func (s *Store) ApplyRetrievalFeedback(ctx context.Context, memoryID string, feedback core.RetrievalFeedback, at time.Time) (*core.MemoryEntry, error) {
	if s == nil {
		return nil, errors.New("store is nil")
	}
	at = at.UTC()
	tuning := config.ResolveAdaptiveFeedbackTuning()

	result, err := s.db.ExecContext(ctx, feedbackUpdateSQL(feedback), feedbackUpdateArgs(feedback, tuning, at, memoryID)...)
	if err != nil {
		return nil, err
	}
	// RowAffected == 0 is benign: memory may not exist; no-op.
	if rowsAffected(result) == 0 {
		return nil, nil
	}
	return s.GetMemory(ctx, memoryID)
}

// feedbackUpdateSQL returns the appropriate atomic UPDATE for each feedback type.
func feedbackUpdateSQL(feedback core.RetrievalFeedback) string {
	cooldownSQL := `CASE WHEN pinned = 0 AND (outcome_json IS NULL OR json_extract(outcome_json, '$.result') != 'failure') THEN ? ELSE suppression_until END`
	clampScore := `MAX(0, MIN(1, salience_score + ?))`
	clampSuppression := `MAX(0, MIN(1, suppression_score + ?))`

	switch feedback {
	case core.FeedbackHelpful:
		return `UPDATE memories SET
			useful_count = MAX(0, useful_count + 1),
			salience_score = ` + clampScore + `,
			suppression_score = ` + clampSuppression + `,
			last_helpful_at = ?,
			suppression_until = '',
			familiarity_band_last = 'strong_recall'
		WHERE id = ?`

	case core.FeedbackIgnored:
		return `UPDATE memories SET
			ignored_count = MAX(0, ignored_count + 1),
			salience_score = ` + clampScore + `,
			suppression_score = ` + clampSuppression + `,
			familiarity_band_last = 'weak_familiarity'
		WHERE id = ?`

	case core.FeedbackRejected:
		return `UPDATE memories SET
			rejected_count = MAX(0, rejected_count + 1),
			salience_score = ` + clampScore + `,
			suppression_score = ` + clampSuppression + `,
			last_rejected_at = ?,
			suppression_until = ` + cooldownSQL + `,
			familiarity_band_last = 'suppressed'
		WHERE id = ?`

	case core.FeedbackHarmful:
		return `UPDATE memories SET
			harmful_count = MAX(0, harmful_count + 1),
			salience_score = ` + clampScore + `,
			suppression_score = ` + clampSuppression + `,
			last_rejected_at = ?,
			suppression_until = ` + cooldownSQL + `,
			familiarity_band_last = 'suppressed'
		WHERE id = ?`
	}
	return ""
}

// feedbackUpdateArgs returns the parameter slice matching feedbackUpdateSQL.
func feedbackUpdateArgs(feedback core.RetrievalFeedback, tuning core.AdaptiveFeedbackTuning, at time.Time, memoryID string) []any {
	atStr := at.Format(time.RFC3339Nano)
	cooldownRejected := at.Add(tuning.RejectedCooldown).Format(time.RFC3339Nano)
	cooldownHarmful := at.Add(tuning.HarmfulCooldown).Format(time.RFC3339Nano)

	switch feedback {
	case core.FeedbackHelpful:
		return []any{tuning.HelpfulSalienceDelta, tuning.HelpfulSuppressionDelta, atStr, memoryID}
	case core.FeedbackIgnored:
		return []any{tuning.IgnoredSalienceDelta, tuning.IgnoredSuppressionDelta, memoryID}
	case core.FeedbackRejected:
		return []any{tuning.RejectedSalienceDelta, tuning.RejectedSuppressionDelta, atStr, cooldownRejected, memoryID}
	case core.FeedbackHarmful:
		return []any{tuning.HarmfulSalienceDelta, tuning.HarmfulSuppressionDelta, atStr, cooldownHarmful, memoryID}
	}
	return nil
}

// ApplyReconsolidation runs the reconsolidation action. For superseded, the
// counter update + superseded mark + relation creation all run inside a single
// transaction. For other actions, a single atomic UPDATE is issued (with an
// optional relation insert for contradicted).
// Returns the updated MemoryEntry after the write.
func (s *Store) ApplyReconsolidation(ctx context.Context, memoryID string, action core.ReconsolidationAction, successorID string, at time.Time) (*core.MemoryEntry, error) {
	if s == nil {
		return nil, errors.New("store is nil")
	}
	at = at.UTC()
	tuning := config.ResolveAdaptiveFeedbackTuning()

	switch action {
	case core.ReconsolidateSuperseded:
		if successorID == "" {
			return nil, errors.New("successor_memory_id is required for superseded reconsolidation")
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback() }()

		// 1. Atomic counter/score update.
		_, err = tx.ExecContext(ctx, reconsolidationUpdateSQL(action), reconsolidationUpdateArgs(action, tuning, at, memoryID)...)
		if err != nil {
			return nil, err
		}

		// 2. Mark superseded.
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `UPDATE memories SET superseded_by = ?, updated_at = ? WHERE id = ?`, successorID, now, memoryID); err != nil {
			return nil, err
		}

		// 3. Add relation.
		if err := addRelationTx(ctx, tx, successorID, memoryID, core.RelSupersedes, 1, map[string]string{"source": "reconsolidation"}); err != nil {
			return nil, err
		}

		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return s.GetMemory(ctx, memoryID)

	case core.ReconsolidateContradicted:
		result, err := s.db.ExecContext(ctx, reconsolidationUpdateSQL(action), reconsolidationUpdateArgs(action, tuning, at, memoryID)...)
		if err != nil {
			return nil, err
		}
		_ = rowsAffected(result)
		if successorID != "" {
			if err := s.AddRelation(ctx, successorID, memoryID, core.RelContradicts, 1, map[string]string{"source": "reconsolidation"}); err != nil {
				return nil, err
			}
		}
		return s.GetMemory(ctx, memoryID)

	case core.ReconsolidateConfirmed, core.ReconsolidateClarified:
		result, err := s.db.ExecContext(ctx, reconsolidationUpdateSQL(action), reconsolidationUpdateArgs(action, tuning, at, memoryID)...)
		if err != nil {
			return nil, err
		}
		_ = rowsAffected(result)
		return s.GetMemory(ctx, memoryID)

	default:
		return nil, fmt.Errorf("invalid reconsolidation action: %s", action)
	}
}

// reconsolidationUpdateSQL returns the atomic UPDATE for a reconsolidation action.
func reconsolidationUpdateSQL(action core.ReconsolidationAction) string {
	clampScore := `MAX(0, MIN(1, salience_score + ?))`
	clampSuppression := `MAX(0, MIN(1, suppression_score + ?))`
	cooldownSQL := `CASE WHEN pinned = 0 THEN ? ELSE suppression_until END`

	switch action {
	case core.ReconsolidateConfirmed:
		return `UPDATE memories SET
			useful_count = MAX(0, useful_count + 1),
			salience_score = ` + clampScore + `,
			last_helpful_at = ?,
			familiarity_band_last = 'strong_recall'
		WHERE id = ?`

	case core.ReconsolidateClarified:
		return `UPDATE memories SET
			salience_score = ` + clampScore + `,
			last_helpful_at = ?
		WHERE id = ?`

	case core.ReconsolidateContradicted:
		return `UPDATE memories SET
			rejected_count = MAX(0, rejected_count + 1),
			salience_score = ` + clampScore + `,
			suppression_score = ` + clampSuppression + `,
			last_rejected_at = ?,
			suppression_until = ` + cooldownSQL + `
		WHERE id = ?`

	case core.ReconsolidateSuperseded:
		return `UPDATE memories SET
			salience_score = ` + clampScore + `,
			suppression_score = ` + clampSuppression + `,
			last_rejected_at = ?
		WHERE id = ?`
	}
	return ""
}

// reconsolidationUpdateArgs returns the parameter slice matching reconsolidationUpdateSQL.
func reconsolidationUpdateArgs(action core.ReconsolidationAction, tuning core.AdaptiveFeedbackTuning, at time.Time, memoryID string) []any {
	atStr := at.Format(time.RFC3339Nano)
	cooldownContradicted := at.Add(tuning.ContradictedCooldown).Format(time.RFC3339Nano)

	switch action {
	case core.ReconsolidateConfirmed:
		return []any{tuning.ConfirmedSalienceDelta, atStr, memoryID}
	case core.ReconsolidateClarified:
		return []any{tuning.ClarifiedSalienceDelta, atStr, memoryID}
	case core.ReconsolidateContradicted:
		return []any{tuning.ContradictedSalienceDelta, tuning.ContradictedSuppressionDelta, atStr, cooldownContradicted, memoryID}
	case core.ReconsolidateSuperseded:
		return []any{tuning.SupersededSalienceDelta, tuning.SupersededSuppressionDelta, atStr, memoryID}
	}
	return nil
}

// addRelationTx inserts a relation within an existing transaction.
func addRelationTx(ctx context.Context, tx *sql.Tx, sourceID, targetID string, relType core.RelationType, weight float64, metadata map[string]string) error {
	if metadata == nil {
		metadata = map[string]string{}
	}
	b, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT OR IGNORE INTO relations (source_id, target_id, type, weight, metadata_json, created_at)
VALUES (?, ?, ?, ?, ?, ?)
`, sourceID, targetID, string(relType), weight, string(b), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// persistRetrievalState uses merge semantics: only increment/set when values
// are provided, never blanket-overwrite existing data.
func (s *Store) persistRetrievalState(ctx context.Context, mem *core.MemoryEntry) error {
	if mem == nil {
		return errors.New("memory is nil")
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE memories
SET salience_score = MAX(0, MIN(1, COALESCE(?, salience_score))),
	suppression_score = MAX(0, MIN(1, COALESCE(?, suppression_score))),
	useful_count = MAX(0, useful_count + COALESCE(?, 0)),
	ignored_count = MAX(0, ignored_count + COALESCE(?, 0)),
	rejected_count = MAX(0, rejected_count + COALESCE(?, 0)),
	harmful_count = MAX(0, harmful_count + COALESCE(?, 0)),
	last_helpful_at = COALESCE(NULLIF(?, ''), last_helpful_at),
	last_rejected_at = COALESCE(NULLIF(?, ''), last_rejected_at),
	suppression_until = COALESCE(NULLIF(?, ''), suppression_until),
	familiarity_band_last = COALESCE(NULLIF(?, ''), familiarity_band_last)
WHERE id = ?`,
		mem.SalienceScore,
		mem.SuppressionScore,
		mem.UsefulCount,
		mem.IgnoredCount,
		mem.RejectedCount,
		mem.HarmfulCount,
		timeStringOrEmpty(mem.LastHelpfulAt),
		timeStringOrEmpty(mem.LastRejectedAt),
		nullTimeString(mem.SuppressionUntil),
		mem.FamiliarityBandLast,
		mem.ID,
	)
	return err
}

func rowsAffected(result sql.Result) int64 {
	if result == nil {
		return 0
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0
	}
	return n
}
