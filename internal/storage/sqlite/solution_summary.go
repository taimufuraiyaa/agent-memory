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

type SolutionSummaryInsert struct {
	EpisodeID              string
	ExpectedEpisodeVersion int64
	Outcome                core.OutcomeResult
	Summary                string
	DecisiveStepIDs        []string
	UsefulFailureStepIDs   []string
	Evidence               []core.SolutionReference
	Risks                  []string
	NextGuidance           string
	Validation             core.SolutionValidationState
	SnapshotHash           string
	IdempotencyKey         string
	CreatedAt              time.Time
}

func (s *Store) CreateSolutionSummary(ctx context.Context, input SolutionSummaryInsert) (core.SolutionSummary, bool, error) {
	if err := requireSolutionIdempotencyKey(input.IdempotencyKey); err != nil {
		return core.SolutionSummary{}, false, err
	}
	requestHash, err := hashSolutionRequest(struct {
		EpisodeID                             string
		ExpectedEpisodeVersion                int64
		Outcome                               core.OutcomeResult
		Summary                               string
		DecisiveStepIDs, UsefulFailureStepIDs []string
		Evidence                              []core.SolutionReference
		Risks                                 []string
		NextGuidance                          string
		Validation                            core.SolutionValidationState
		SnapshotHash                          string
	}{strings.TrimSpace(input.EpisodeID), input.ExpectedEpisodeVersion, input.Outcome, input.Summary, input.DecisiveStepIDs,
		input.UsefulFailureStepIDs, input.Evidence, input.Risks, input.NextGuidance, input.Validation, input.SnapshotHash})
	if err != nil {
		return core.SolutionSummary{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.SolutionSummary{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, existingHash, getErr := getSolutionSummaryByKeyTx(ctx, tx, input.EpisodeID, input.IdempotencyKey); getErr == nil {
		if existingHash != requestHash {
			return core.SolutionSummary{}, false, errors.New("solution summary idempotency key was already used with different input")
		}
		if err := tx.Commit(); err != nil {
			return core.SolutionSummary{}, false, err
		}
		return existing, true, nil
	} else if !errors.Is(getErr, sql.ErrNoRows) {
		return core.SolutionSummary{}, false, getErr
	}
	var episodeVersion int64
	var status core.SolutionEpisodeStatus
	if err := tx.QueryRowContext(ctx, `SELECT version, status FROM solution_episodes WHERE id = ?`, strings.TrimSpace(input.EpisodeID)).Scan(&episodeVersion, &status); err != nil {
		return core.SolutionSummary{}, false, err
	}
	if episodeVersion != input.ExpectedEpisodeVersion {
		return core.SolutionSummary{}, false, errors.New("solution episode version conflict")
	}
	if !status.Terminal() {
		return core.SolutionSummary{}, false, errors.New("solution episode must be terminal before finalization")
	}
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM solution_summaries WHERE episode_id = ?`, input.EpisodeID).Scan(&version); err != nil {
		return core.SolutionSummary{}, false, err
	}
	var previousID string
	previousErr := tx.QueryRowContext(ctx, `SELECT id FROM solution_summaries WHERE episode_id = ? ORDER BY version DESC LIMIT 1`, input.EpisodeID).Scan(&previousID)
	if previousErr != nil && !errors.Is(previousErr, sql.ErrNoRows) {
		return core.SolutionSummary{}, false, previousErr
	}
	createdAt := input.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	summary := core.SolutionSummary{ID: uuid.NewString(), EpisodeID: strings.TrimSpace(input.EpisodeID), Version: version, Outcome: input.Outcome,
		Summary: strings.TrimSpace(input.Summary), DecisiveStepIDs: input.DecisiveStepIDs, UsefulFailureStepIDs: input.UsefulFailureStepIDs,
		Evidence: input.Evidence, Risks: input.Risks, NextGuidance: strings.TrimSpace(input.NextGuidance), Validation: input.Validation, CreatedAt: createdAt}
	if err := summary.Validate(); err != nil {
		return core.SolutionSummary{}, false, err
	}
	decisiveJSON, _ := json.Marshal(summary.DecisiveStepIDs)
	failuresJSON, _ := json.Marshal(summary.UsefulFailureStepIDs)
	evidenceJSON, _ := json.Marshal(summary.Evidence)
	risksJSON, _ := json.Marshal(summary.Risks)
	_, err = tx.ExecContext(ctx, `INSERT INTO solution_summaries (id, episode_id, version, episode_version, outcome, summary,
		decisive_step_ids_json, useful_failure_step_ids_json, evidence_json, risks_json, next_guidance, validation,
		superseded_by, snapshot_hash, idempotency_key, request_hash, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?)`,
		summary.ID, summary.EpisodeID, summary.Version, input.ExpectedEpisodeVersion, summary.Outcome, summary.Summary,
		string(decisiveJSON), string(failuresJSON), string(evidenceJSON), string(risksJSON), summary.NextGuidance,
		summary.Validation, input.SnapshotHash, strings.TrimSpace(input.IdempotencyKey), requestHash, summary.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return core.SolutionSummary{}, false, err
	}
	if previousID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE solution_summaries SET superseded_by = ? WHERE id = ? AND superseded_by = ''`, summary.ID, previousID); err != nil {
			return core.SolutionSummary{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return core.SolutionSummary{}, false, err
	}
	return summary, false, nil
}

func (s *Store) GetSolutionSummary(ctx context.Context, id string) (core.SolutionSummary, error) {
	summary, _, err := scanSolutionSummary(s.db.QueryRowContext(ctx, solutionSummarySelect+` WHERE id = ?`, strings.TrimSpace(id)))
	return summary, err
}

func (s *Store) LatestSolutionSummary(ctx context.Context, episodeID string) (core.SolutionSummary, error) {
	summary, _, err := scanSolutionSummary(s.db.QueryRowContext(ctx, solutionSummarySelect+` WHERE episode_id = ? ORDER BY version DESC LIMIT 1`, strings.TrimSpace(episodeID)))
	return summary, err
}

const solutionSummarySelect = `SELECT id, episode_id, version, outcome, summary, decisive_step_ids_json,
	useful_failure_step_ids_json, evidence_json, risks_json, next_guidance, validation, superseded_by, created_at, request_hash FROM solution_summaries`

func getSolutionSummaryByKeyTx(ctx context.Context, tx *sql.Tx, episodeID, key string) (core.SolutionSummary, string, error) {
	return scanSolutionSummary(tx.QueryRowContext(ctx, solutionSummarySelect+` WHERE episode_id = ? AND idempotency_key = ?`, strings.TrimSpace(episodeID), strings.TrimSpace(key)))
}

func scanSolutionSummary(row solutionRowScanner) (core.SolutionSummary, string, error) {
	var summary core.SolutionSummary
	var decisive, failures, evidence, risks, createdAt, requestHash string
	err := row.Scan(&summary.ID, &summary.EpisodeID, &summary.Version, &summary.Outcome, &summary.Summary, &decisive,
		&failures, &evidence, &risks, &summary.NextGuidance, &summary.Validation, &summary.SupersededBy, &createdAt, &requestHash)
	if err != nil {
		return core.SolutionSummary{}, "", err
	}
	if err := json.Unmarshal([]byte(decisive), &summary.DecisiveStepIDs); err != nil {
		return core.SolutionSummary{}, "", err
	}
	if err := json.Unmarshal([]byte(failures), &summary.UsefulFailureStepIDs); err != nil {
		return core.SolutionSummary{}, "", err
	}
	if err := json.Unmarshal([]byte(evidence), &summary.Evidence); err != nil {
		return core.SolutionSummary{}, "", err
	}
	if err := json.Unmarshal([]byte(risks), &summary.Risks); err != nil {
		return core.SolutionSummary{}, "", err
	}
	summary.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return summary, requestHash, nil
}
