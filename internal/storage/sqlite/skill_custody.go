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

var ErrSkillEvidenceHeld = errors.New("skill lifecycle evidence is under legal hold")

func (s *Store) PlaceSkillLegalHold(ctx context.Context, hold core.SkillLegalHold) error {
	for _, value := range []string{hold.ID, hold.Workspace, hold.TargetKind, hold.TargetID, hold.Reason} {
		if strings.TrimSpace(value) == "" || len(value) > 512 {
			return errors.New("skill legal hold fields are required and bounded")
		}
	}
	if hold.CreatedAt.IsZero() {
		return errors.New("skill legal hold created_at is required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO skill_legal_holds(id,workspace,target_kind,target_id,reason,state,created_at,released_at) VALUES(?,?,?,?,?,'active',?,'')`, hold.ID, hold.Workspace, hold.TargetKind, hold.TargetID, hold.Reason, formatSkillTime(hold.CreatedAt))
	return err
}

func (s *Store) ReleaseSkillLegalHold(ctx context.Context, workspace, holdID string, at time.Time) error {
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(holdID) == "" || at.IsZero() {
		return errors.New("skill legal hold release scope is required")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE skill_legal_holds SET state='released',released_at=? WHERE workspace=? AND id=? AND state='active'`, formatSkillTime(at), workspace, holdID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) IsSkillEvidenceTombstoned(ctx context.Context, workspace, kind, id string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM skill_evidence_tombstones WHERE workspace=? AND evidence_kind=? AND evidence_id=?`, strings.TrimSpace(workspace), strings.TrimSpace(kind), strings.TrimSpace(id)).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) DeleteSkillEvidence(ctx context.Context, workspace, kind, id string, at time.Time) (core.SkillEvidenceDeletionResult, error) {
	workspace, kind, id = strings.TrimSpace(workspace), strings.TrimSpace(kind), strings.TrimSpace(id)
	result := core.SkillEvidenceDeletionResult{Workspace: workspace, EvidenceKind: kind, EvidenceID: id}
	if workspace == "" || id == "" || at.IsZero() || !validSkillEvidenceKind(kind) {
		return result, errors.New("skill evidence deletion scope is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var held int
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM skill_legal_holds WHERE workspace=? AND state='active' AND ((target_kind=? AND target_id=?) OR (target_kind='workspace' AND target_id=?)))`, workspace, kind, id, workspace).Scan(&held)
	if err != nil {
		return result, err
	}
	if held != 0 {
		return result, ErrSkillEvidenceHeld
	}
	var existing int
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM skill_evidence_tombstones WHERE workspace=? AND evidence_kind=? AND evidence_id=?)`, workspace, kind, id).Scan(&existing)
	if err != nil {
		return result, err
	}
	if existing != 0 {
		result.Replayed = true
		return result, tx.Commit()
	}
	deleted, err := tx.ExecContext(ctx, `DELETE FROM skill_candidate_sources WHERE source_kind=? AND source_id=? AND candidate_id IN (SELECT id FROM skill_candidates WHERE workspace=?)`, kind, id, workspace)
	if err != nil {
		return result, err
	}
	result.CandidateReferences, _ = deleted.RowsAffected()
	rows, err := tx.QueryContext(ctx, `SELECT id,provenance_json FROM skill_revisions WHERE workspace=?`, workspace)
	if err != nil {
		return result, err
	}
	type revisionProvenance struct {
		MemoryIDs     []string `json:"memory_ids"`
		ToolLessonIDs []string `json:"tool_lesson_ids"`
		EpisodeIDs    []string `json:"episode_ids"`
	}
	updates := map[string]string{}
	for rows.Next() {
		var revisionID, raw string
		if err := rows.Scan(&revisionID, &raw); err != nil {
			rows.Close()
			return result, err
		}
		var provenance revisionProvenance
		_ = json.Unmarshal([]byte(raw), &provenance)
		changed := false
		switch kind {
		case "memory":
			provenance.MemoryIDs, changed = removeSkillEvidenceID(provenance.MemoryIDs, id)
		case "tool_lesson":
			provenance.ToolLessonIDs, changed = removeSkillEvidenceID(provenance.ToolLessonIDs, id)
		case "episode":
			provenance.EpisodeIDs, changed = removeSkillEvidenceID(provenance.EpisodeIDs, id)
		}
		if changed {
			encoded, _ := json.Marshal(provenance)
			updates[revisionID] = string(encoded)
		}
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	for revisionID, raw := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE skill_revisions SET provenance_json=? WHERE workspace=? AND id=?`, raw, workspace, revisionID); err != nil {
			return result, err
		}
		result.RevisionReferences++
	}
	if kind == "execution" {
		removed, deleteErr := tx.ExecContext(ctx, `DELETE FROM skill_executions WHERE workspace=? AND id=?`, workspace, id)
		if deleteErr != nil {
			return result, deleteErr
		}
		result.ExecutionsDeleted, _ = removed.RowsAffected()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_evidence_tombstones(workspace,evidence_kind,evidence_id,deleted_at) VALUES(?,?,?,?)`, workspace, kind, id, formatSkillTime(at)); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func (s *Store) PruneSkillTelemetry(ctx context.Context, workspace string, before time.Time) (int64, error) {
	if strings.TrimSpace(workspace) == "" || before.IsZero() {
		return 0, errors.New("skill telemetry retention scope is required")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM skill_executions AS execution WHERE workspace=? AND completed_at<? AND NOT EXISTS (
		SELECT 1 FROM skill_legal_holds hold WHERE hold.workspace=execution.workspace AND hold.state='active' AND (
			(hold.target_kind='workspace' AND hold.target_id=execution.workspace) OR
			(hold.target_kind='skill' AND hold.target_id=execution.skill_id) OR
			(hold.target_kind='revision' AND hold.target_id=execution.revision_id) OR
			(hold.target_kind='execution' AND hold.target_id=execution.id)))`, strings.TrimSpace(workspace), formatSkillTime(before))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func validSkillEvidenceKind(kind string) bool {
	return kind == "memory" || kind == "episode" || kind == "tool_lesson" || kind == "execution"
}

func removeSkillEvidenceID(values []string, id string) ([]string, bool) {
	result := values[:0]
	changed := false
	for _, value := range values {
		if value == id {
			changed = true
		} else {
			result = append(result, value)
		}
	}
	return result, changed
}

func (s *Store) ExportSkillLifecycle(ctx context.Context, workspace string) (map[string][]map[string]any, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, errors.New("skill lifecycle export workspace is required")
	}
	queries := map[string]string{
		"skills":                  `SELECT * FROM skills WHERE workspace=? ORDER BY id`,
		"aliases":                 `SELECT * FROM skill_aliases WHERE workspace=? ORDER BY skill_id,alias`,
		"candidates":              `SELECT * FROM skill_candidates WHERE workspace=? ORDER BY id`,
		"candidate_sources":       `SELECT source.* FROM skill_candidate_sources source JOIN skill_candidates candidate ON candidate.id=source.candidate_id WHERE candidate.workspace=? ORDER BY source.candidate_id,source.source_kind,source.source_id`,
		"revisions":               `SELECT * FROM skill_revisions WHERE workspace=? ORDER BY skill_id,revision_number`,
		"revision_parents":        `SELECT parent.* FROM skill_revision_parents parent JOIN skill_revisions revision ON revision.id=parent.revision_id WHERE revision.workspace=? ORDER BY parent.revision_id,parent.parent_revision_id`,
		"revision_files":          `SELECT file.* FROM skill_revision_files file JOIN skill_revisions revision ON revision.id=file.revision_id WHERE revision.workspace=? ORDER BY file.revision_id,file.path`,
		"evaluation_suites":       `SELECT * FROM skill_evaluation_suites WHERE workspace=? ORDER BY id,version`,
		"evaluation_cases":        `SELECT item.* FROM skill_evaluation_cases item JOIN skill_evaluation_suites suite ON suite.id=item.suite_id WHERE suite.workspace=? ORDER BY item.suite_id,item.case_id`,
		"evaluation_runs":         `SELECT * FROM skill_evaluation_runs WHERE workspace=? ORDER BY id`,
		"evaluation_case_results": `SELECT item.* FROM skill_evaluation_case_results item JOIN skill_evaluation_runs run ON run.id=item.run_id WHERE run.workspace=? ORDER BY item.run_id,item.case_id`,
		"promotion_policies":      `SELECT * FROM skill_promotion_policies WHERE workspace=? ORDER BY id,version`,
		"policy_decisions":        `SELECT * FROM skill_policy_decisions WHERE workspace=? ORDER BY id`,
		"approvals":               `SELECT * FROM skill_approvals WHERE workspace=? ORDER BY id`,
		"approval_events":         `SELECT event.* FROM skill_approval_events event JOIN skill_approvals approval ON approval.id=event.approval_id WHERE approval.workspace=? ORDER BY event.id`,
		"activations":             `SELECT * FROM skill_activations WHERE workspace=? ORDER BY environment,skill_id`,
		"activation_operations":   `SELECT * FROM skill_activation_operations WHERE workspace=? ORDER BY id`,
		"resolutions":             `SELECT * FROM skill_resolutions WHERE workspace=? ORDER BY id`,
		"executions":              `SELECT * FROM skill_executions WHERE workspace=? ORDER BY id`,
		"rollback_events":         `SELECT * FROM skill_rollback_events WHERE workspace=? ORDER BY id`,
		"safety_signals":          `SELECT * FROM skill_safety_signals WHERE workspace=? ORDER BY id`,
		"legal_holds":             `SELECT * FROM skill_legal_holds WHERE workspace=? ORDER BY id`,
		"evidence_tombstones":     `SELECT * FROM skill_evidence_tombstones WHERE workspace=? ORDER BY evidence_kind,evidence_id`,
	}
	archive := make(map[string][]map[string]any, len(queries))
	for name, query := range queries {
		rows, err := s.db.QueryContext(ctx, query, workspace)
		if err != nil {
			return nil, err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		items := []map[string]any{}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				return nil, err
			}
			item := make(map[string]any, len(columns))
			for index, column := range columns {
				if raw, ok := values[index].([]byte); ok {
					item[column] = string(raw)
				} else {
					item[column] = values[index]
				}
			}
			items = append(items, item)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		archive[name] = items
	}
	return archive, nil
}
