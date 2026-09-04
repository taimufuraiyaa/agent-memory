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

func (s *Store) PutSkillCandidate(ctx context.Context, candidate core.SkillCandidate) (core.SkillCandidate, bool, error) {
	if err := candidate.Validate(); err != nil {
		return core.SkillCandidate{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.SkillCandidate{}, false, err
	}
	defer tx.Rollback()
	for kind, ids := range map[string][]string{"memory": candidate.SourceMemoryIDs, "episode": candidate.SourceEpisodeIDs, "tool_lesson": candidate.SourceToolLessonIDs, "execution": candidate.SourceExecutionIDs} {
		for _, id := range ids {
			var tombstoned int
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM skill_evidence_tombstones WHERE workspace=? AND evidence_kind=? AND evidence_id=?)`, candidate.Workspace, kind, id).Scan(&tombstoned); err != nil {
				return core.SkillCandidate{}, false, err
			}
			if tombstoned != 0 {
				return core.SkillCandidate{}, false, errors.New("skill candidate references deleted evidence")
			}
		}
	}
	var existingID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM skill_candidates WHERE workspace = ? AND deduplication_hash = ?`, candidate.Workspace, candidate.DeduplicationHash).Scan(&existingID)
	if err == nil {
		existing, getErr := getSkillCandidateWith(ctx, tx, existingID)
		if getErr != nil {
			return core.SkillCandidate{}, false, getErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return core.SkillCandidate{}, false, commitErr
		}
		return existing, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return core.SkillCandidate{}, false, err
	}
	targets, _ := json.Marshal(candidate.TargetSkillIDs)
	risks, _ := json.Marshal(candidate.Risks)
	if _, err = tx.ExecContext(ctx, `INSERT INTO skill_candidates(id,workspace,kind,summary,expected_benefit,risks_json,risk_tier,confidence,state,target_skill_ids_json,deduplication_hash,created_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		candidate.ID, candidate.Workspace, candidate.Kind, candidate.Summary, candidate.ExpectedBenefit, string(risks), candidate.RiskTier, candidate.Confidence, candidate.State, string(targets), candidate.DeduplicationHash, candidate.CreatedBy, formatSkillTime(candidate.CreatedAt), formatSkillTime(candidate.UpdatedAt)); err != nil {
		return core.SkillCandidate{}, false, err
	}
	for kind, ids := range map[string][]string{"memory": candidate.SourceMemoryIDs, "episode": candidate.SourceEpisodeIDs, "tool_lesson": candidate.SourceToolLessonIDs, "execution": candidate.SourceExecutionIDs} {
		for _, id := range ids {
			if _, err = tx.ExecContext(ctx, `INSERT INTO skill_candidate_sources(candidate_id,source_kind,source_id) VALUES(?,?,?)`, candidate.ID, kind, id); err != nil {
				return core.SkillCandidate{}, false, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return core.SkillCandidate{}, false, err
	}
	return candidate, false, nil
}

func (s *Store) GetSkillCandidate(ctx context.Context, workspace, id string) (core.SkillCandidate, error) {
	candidate, err := getSkillCandidateWith(ctx, s.db, strings.TrimSpace(id))
	if err != nil {
		return core.SkillCandidate{}, err
	}
	if candidate.Workspace != strings.TrimSpace(workspace) {
		return core.SkillCandidate{}, errors.New("skill candidate not found")
	}
	return candidate, nil
}

type skillCandidateQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func getSkillCandidateWith(ctx context.Context, queryer skillCandidateQueryer, id string) (core.SkillCandidate, error) {
	var candidate core.SkillCandidate
	var targets, risks, created, updated string
	err := queryer.QueryRowContext(ctx, `SELECT id,workspace,kind,summary,expected_benefit,risks_json,risk_tier,confidence,state,target_skill_ids_json,deduplication_hash,created_by,created_at,updated_at FROM skill_candidates WHERE id = ?`, id).Scan(
		&candidate.ID, &candidate.Workspace, &candidate.Kind, &candidate.Summary, &candidate.ExpectedBenefit, &risks, &candidate.RiskTier, &candidate.Confidence, &candidate.State, &targets, &candidate.DeduplicationHash, &candidate.CreatedBy, &created, &updated)
	if err != nil {
		return core.SkillCandidate{}, err
	}
	_ = json.Unmarshal([]byte(targets), &candidate.TargetSkillIDs)
	_ = json.Unmarshal([]byte(risks), &candidate.Risks)
	candidate.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	candidate.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	rows, err := queryer.QueryContext(ctx, `SELECT source_kind,source_id FROM skill_candidate_sources WHERE candidate_id = ? ORDER BY source_kind,source_id`, id)
	if err != nil {
		return core.SkillCandidate{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, sourceID string
		if err := rows.Scan(&kind, &sourceID); err != nil {
			return core.SkillCandidate{}, err
		}
		switch kind {
		case "memory":
			candidate.SourceMemoryIDs = append(candidate.SourceMemoryIDs, sourceID)
		case "episode":
			candidate.SourceEpisodeIDs = append(candidate.SourceEpisodeIDs, sourceID)
		case "tool_lesson":
			candidate.SourceToolLessonIDs = append(candidate.SourceToolLessonIDs, sourceID)
		case "execution":
			candidate.SourceExecutionIDs = append(candidate.SourceExecutionIDs, sourceID)
		}
	}
	return candidate, rows.Err()
}
