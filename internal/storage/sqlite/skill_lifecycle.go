package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillRevisionOneImport struct {
	Skill      core.LogicalSkill
	Revision   core.SkillRevision
	Activation core.SkillActivation
}

func (s *Store) ImportSkillRevisionOne(ctx context.Context, input SkillRevisionOneImport) (bool, error) {
	if err := input.Skill.Validate(); err != nil {
		return false, err
	}
	if err := input.Revision.Validate(); err != nil {
		return false, err
	}
	if err := input.Activation.Validate(); err != nil {
		return false, err
	}
	if input.Revision.Number != 1 || input.Revision.State != core.SkillRevisionActive || input.Revision.SkillID != input.Skill.ID || input.Activation.SkillID != input.Skill.ID || input.Activation.ActiveRevisionID != input.Revision.ID || input.Activation.ActiveDigest != input.Revision.BundleDigest {
		return false, errors.New("invalid revision-one import binding")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var existingID, existingDigest string
	err = tx.QueryRowContext(ctx, `SELECT s.id, COALESCE(r.bundle_digest, '') FROM skills s LEFT JOIN skill_revisions r ON r.skill_id = s.id AND r.revision_number = 1 WHERE s.workspace = ? AND s.name = ?`, input.Skill.Workspace, input.Skill.Name).Scan(&existingID, &existingDigest)
	if err == nil {
		if existingID != input.Skill.ID || existingDigest != input.Revision.BundleDigest {
			return false, fmt.Errorf("skill %s already imported with different identity or digest", input.Skill.Name)
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	triggersJSON, _ := json.Marshal(input.Skill.TriggerConditions)
	capabilitiesJSON, _ := json.Marshal(input.Skill.Capabilities)
	if _, err := tx.ExecContext(ctx, `INSERT INTO skills(id,workspace,name,description,trigger_conditions_json,capabilities_json,risk_tier,owner_group,status,generation,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		input.Skill.ID, input.Skill.Workspace, input.Skill.Name, input.Skill.Description, string(triggersJSON), string(capabilitiesJSON), input.Skill.RiskTier, input.Skill.OwnerGroup, input.Skill.Status, input.Skill.Generation, formatSkillTime(input.Skill.CreatedAt), formatSkillTime(input.Skill.UpdatedAt)); err != nil {
		return false, err
	}
	for _, alias := range input.Skill.Aliases {
		if _, err := tx.ExecContext(ctx, `INSERT INTO skill_aliases(workspace,skill_id,alias) VALUES(?,?,?)`, input.Skill.Workspace, input.Skill.ID, alias); err != nil {
			return false, err
		}
	}
	compatibilityJSON, _ := json.Marshal(input.Revision.Compatibility)
	protectedJSON, _ := json.Marshal(input.Revision.ProtectedSections)
	provenanceJSON, _ := json.Marshal(map[string][]string{
		"memory_ids": input.Revision.SourceMemoryIDs, "tool_lesson_ids": input.Revision.SourceToolLessonIDs, "episode_ids": input.Revision.SourceEpisodeIDs,
	})
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_revisions(id,workspace,skill_id,revision_number,state,bundle_digest,manifest_version,compatibility_json,risk_tier,candidate_id,protected_sections_json,provenance_json,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		input.Revision.ID, input.Revision.Workspace, input.Revision.SkillID, input.Revision.Number, input.Revision.State, input.Revision.BundleDigest, input.Revision.ManifestVersion, string(compatibilityJSON), input.Revision.RiskTier, input.Revision.CandidateID, string(protectedJSON), string(provenanceJSON), input.Revision.CreatedBy, formatSkillTime(input.Revision.CreatedAt)); err != nil {
		return false, err
	}
	for _, file := range input.Revision.Files {
		if _, err := tx.ExecContext(ctx, `INSERT INTO skill_revision_files(revision_id,path,digest,size_bytes) VALUES(?,?,?,?)`, input.Revision.ID, file.Path, file.Digest, file.SizeBytes); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_activations(id,workspace,environment,skill_id,active_revision_id,active_digest,last_known_good_revision_id,last_known_good_digest,canary_revision_id,canary_digest,generation,policy_decision_id,materialization,activated_by,activated_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		input.Activation.ID, input.Activation.Workspace, input.Activation.Environment, input.Activation.SkillID, input.Activation.ActiveRevisionID, input.Activation.ActiveDigest, input.Activation.LastKnownGoodRevisionID, input.Activation.LastKnownGoodDigest, input.Activation.CanaryRevisionID, input.Activation.CanaryDigest, input.Activation.Generation, input.Activation.PolicyDecisionID, input.Activation.Materialization, input.Activation.ActivatedBy, formatSkillTime(input.Activation.ActivatedAt), formatSkillTime(input.Activation.UpdatedAt)); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return false, nil
}

func (s *Store) ListLogicalSkills(ctx context.Context, workspace string, limit int) ([]core.LogicalSkill, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,workspace,name,description,trigger_conditions_json,capabilities_json,risk_tier,owner_group,status,generation,created_at,updated_at FROM skills WHERE workspace = ? ORDER BY updated_at DESC, name LIMIT ?`, strings.TrimSpace(workspace), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.LogicalSkill, 0)
	for rows.Next() {
		var item core.LogicalSkill
		var triggers, capabilities, created, updated string
		if err := rows.Scan(&item.ID, &item.Workspace, &item.Name, &item.Description, &triggers, &capabilities, &item.RiskTier, &item.OwnerGroup, &item.Status, &item.Generation, &created, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(triggers), &item.TriggerConditions)
		_ = json.Unmarshal([]byte(capabilities), &item.Capabilities)
		item.Aliases, err = listSkillAliases(ctx, s.db, item.Workspace, item.ID)
		if err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) GetLogicalSkill(ctx context.Context, workspace, skillID string) (core.LogicalSkill, error) {
	var item core.LogicalSkill
	var triggers, capabilities, created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,workspace,name,description,trigger_conditions_json,capabilities_json,risk_tier,owner_group,status,generation,created_at,updated_at FROM skills WHERE workspace = ? AND id = ?`, strings.TrimSpace(workspace), strings.TrimSpace(skillID)).Scan(
		&item.ID, &item.Workspace, &item.Name, &item.Description, &triggers, &capabilities, &item.RiskTier, &item.OwnerGroup, &item.Status, &item.Generation, &created, &updated)
	if err != nil {
		return core.LogicalSkill{}, err
	}
	_ = json.Unmarshal([]byte(triggers), &item.TriggerConditions)
	_ = json.Unmarshal([]byte(capabilities), &item.Capabilities)
	item.Aliases, err = listSkillAliases(ctx, s.db, item.Workspace, item.ID)
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return item, err
}

func (s *Store) CreateSkillRevision(ctx context.Context, revision core.SkillRevision) error {
	if err := revision.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	compatibility, _ := json.Marshal(revision.Compatibility)
	protected, _ := json.Marshal(revision.ProtectedSections)
	provenance, _ := json.Marshal(map[string][]string{"memory_ids": revision.SourceMemoryIDs, "tool_lesson_ids": revision.SourceToolLessonIDs, "episode_ids": revision.SourceEpisodeIDs})
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_revisions(id,workspace,skill_id,revision_number,state,bundle_digest,manifest_version,compatibility_json,risk_tier,candidate_id,protected_sections_json,provenance_json,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		revision.ID, revision.Workspace, revision.SkillID, revision.Number, revision.State, revision.BundleDigest, revision.ManifestVersion, string(compatibility), revision.RiskTier, revision.CandidateID, string(protected), string(provenance), revision.CreatedBy, formatSkillTime(revision.CreatedAt)); err != nil {
		return err
	}
	for _, parentID := range revision.ParentRevisionIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO skill_revision_parents(revision_id,parent_revision_id) VALUES(?,?)`, revision.ID, parentID); err != nil {
			return err
		}
	}
	for _, file := range revision.Files {
		if _, err := tx.ExecContext(ctx, `INSERT INTO skill_revision_files(revision_id,path,digest,size_bytes) VALUES(?,?,?,?)`, revision.ID, file.Path, file.Digest, file.SizeBytes); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetSkillRevision(ctx context.Context, workspace, revisionID string) (core.SkillRevision, error) {
	var item core.SkillRevision
	var compatibility, protected, provenance, created string
	err := s.db.QueryRowContext(ctx, `SELECT id,workspace,skill_id,revision_number,state,bundle_digest,manifest_version,compatibility_json,risk_tier,candidate_id,protected_sections_json,provenance_json,created_by,created_at FROM skill_revisions WHERE workspace = ? AND id = ?`, strings.TrimSpace(workspace), strings.TrimSpace(revisionID)).Scan(
		&item.ID, &item.Workspace, &item.SkillID, &item.Number, &item.State, &item.BundleDigest, &item.ManifestVersion, &compatibility, &item.RiskTier, &item.CandidateID, &protected, &provenance, &item.CreatedBy, &created)
	if err != nil {
		return core.SkillRevision{}, err
	}
	_ = json.Unmarshal([]byte(compatibility), &item.Compatibility)
	_ = json.Unmarshal([]byte(protected), &item.ProtectedSections)
	var source struct {
		MemoryIDs     []string `json:"memory_ids"`
		ToolLessonIDs []string `json:"tool_lesson_ids"`
		EpisodeIDs    []string `json:"episode_ids"`
	}
	_ = json.Unmarshal([]byte(provenance), &source)
	item.SourceMemoryIDs, item.SourceToolLessonIDs, item.SourceEpisodeIDs = source.MemoryIDs, source.ToolLessonIDs, source.EpisodeIDs
	item.ParentRevisionIDs, err = listSkillRevisionParents(ctx, s.db, item.ID)
	if err != nil {
		return core.SkillRevision{}, err
	}
	item.Files, err = listSkillRevisionFiles(ctx, s.db, item.ID)
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return item, err
}

func (s *Store) TransitionSkillRevisionState(ctx context.Context, workspace, revisionID string, from, to core.SkillRevisionState) (core.SkillRevision, error) {
	if !core.CanTransitionSkillRevision(from, to) {
		return core.SkillRevision{}, errors.New("invalid skill revision transition")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE skill_revisions SET state = ? WHERE workspace = ? AND id = ? AND state = ?`, to, strings.TrimSpace(workspace), strings.TrimSpace(revisionID), from)
	if err != nil {
		return core.SkillRevision{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return core.SkillRevision{}, errors.New("skill revision transition is stale")
	}
	return s.GetSkillRevision(ctx, workspace, revisionID)
}

func (s *Store) ListSkillRevisions(ctx context.Context, workspace, skillID string, limit int) ([]core.SkillRevision, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,workspace,skill_id,revision_number,state,bundle_digest,manifest_version,compatibility_json,risk_tier,candidate_id,protected_sections_json,provenance_json,created_by,created_at FROM skill_revisions WHERE workspace = ? AND skill_id = ? ORDER BY revision_number DESC LIMIT ?`, strings.TrimSpace(workspace), strings.TrimSpace(skillID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.SkillRevision, 0)
	for rows.Next() {
		var item core.SkillRevision
		var compatibility, protected, provenance, created string
		if err := rows.Scan(&item.ID, &item.Workspace, &item.SkillID, &item.Number, &item.State, &item.BundleDigest, &item.ManifestVersion, &compatibility, &item.RiskTier, &item.CandidateID, &protected, &provenance, &item.CreatedBy, &created); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(compatibility), &item.Compatibility)
		_ = json.Unmarshal([]byte(protected), &item.ProtectedSections)
		var source struct {
			MemoryIDs     []string `json:"memory_ids"`
			ToolLessonIDs []string `json:"tool_lesson_ids"`
			EpisodeIDs    []string `json:"episode_ids"`
		}
		_ = json.Unmarshal([]byte(provenance), &source)
		item.SourceMemoryIDs, item.SourceToolLessonIDs, item.SourceEpisodeIDs = source.MemoryIDs, source.ToolLessonIDs, source.EpisodeIDs
		item.ParentRevisionIDs, err = listSkillRevisionParents(ctx, s.db, item.ID)
		if err != nil {
			return nil, err
		}
		item.Files, err = listSkillRevisionFiles(ctx, s.db, item.ID)
		if err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) GetSkillActivation(ctx context.Context, workspace, environment, skillID string) (core.SkillActivation, error) {
	var item core.SkillActivation
	var activated, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,workspace,environment,skill_id,active_revision_id,active_digest,last_known_good_revision_id,last_known_good_digest,canary_revision_id,canary_digest,generation,policy_decision_id,materialization,activated_by,activated_at,updated_at FROM skill_activations WHERE workspace = ? AND environment = ? AND skill_id = ?`, strings.TrimSpace(workspace), strings.TrimSpace(environment), strings.TrimSpace(skillID)).Scan(
		&item.ID, &item.Workspace, &item.Environment, &item.SkillID, &item.ActiveRevisionID, &item.ActiveDigest, &item.LastKnownGoodRevisionID, &item.LastKnownGoodDigest, &item.CanaryRevisionID, &item.CanaryDigest, &item.Generation, &item.PolicyDecisionID, &item.Materialization, &item.ActivatedBy, &activated, &updated)
	if err != nil {
		return core.SkillActivation{}, err
	}
	item.ActivatedAt, _ = time.Parse(time.RFC3339Nano, activated)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return item, nil
}

func (s *Store) CreateSkillActivationOperation(ctx context.Context, operation core.SkillActivationOperation) (bool, error) {
	if err := operation.Validate(); err != nil {
		return false, err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO skill_activation_operations(id,workspace,environment,skill_id,from_revision_id,to_revision_id,expected_generation,state,error,idempotency_key,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		operation.ID, operation.Workspace, operation.Environment, operation.SkillID, operation.FromRevisionID, operation.ToRevisionID,
		operation.ExpectedGeneration, operation.State, operation.Error, operation.IdempotencyKey, formatSkillTime(operation.CreatedAt), formatSkillTime(operation.UpdatedAt))
	if err == nil {
		return false, nil
	}
	existing, getErr := s.GetSkillActivationOperationByKey(ctx, operation.Workspace, operation.Environment, operation.SkillID, operation.IdempotencyKey)
	if getErr != nil {
		return false, err
	}
	if existing.ID != operation.ID || existing.FromRevisionID != operation.FromRevisionID || existing.ToRevisionID != operation.ToRevisionID || existing.ExpectedGeneration != operation.ExpectedGeneration {
		return false, errors.New("skill activation idempotency key is already bound to another operation")
	}
	return true, nil
}

func (s *Store) GetSkillActivationOperation(ctx context.Context, workspace, operationID string) (core.SkillActivationOperation, error) {
	return scanSkillActivationOperation(s.db.QueryRowContext(ctx, `SELECT id,workspace,environment,skill_id,from_revision_id,to_revision_id,expected_generation,state,error,idempotency_key,created_at,updated_at FROM skill_activation_operations WHERE workspace = ? AND id = ?`, strings.TrimSpace(workspace), strings.TrimSpace(operationID)))
}

func (s *Store) GetSkillActivationOperationByKey(ctx context.Context, workspace, environment, skillID, idempotencyKey string) (core.SkillActivationOperation, error) {
	return scanSkillActivationOperation(s.db.QueryRowContext(ctx, `SELECT id,workspace,environment,skill_id,from_revision_id,to_revision_id,expected_generation,state,error,idempotency_key,created_at,updated_at FROM skill_activation_operations WHERE workspace = ? AND environment = ? AND skill_id = ? AND idempotency_key = ?`, strings.TrimSpace(workspace), strings.TrimSpace(environment), strings.TrimSpace(skillID), strings.TrimSpace(idempotencyKey)))
}

func (s *Store) TransitionSkillActivationOperation(ctx context.Context, workspace, operationID string, from, to core.SkillActivationOperationState, failure string, updatedAt time.Time) (core.SkillActivationOperation, error) {
	if !core.CanTransitionSkillActivationOperation(from, to) {
		return core.SkillActivationOperation{}, errors.New("invalid skill activation operation transition")
	}
	failure = strings.TrimSpace(failure)
	if to == core.SkillActivationOperationFailed && failure == "" {
		return core.SkillActivationOperation{}, errors.New("failed skill activation operation requires error")
	}
	if to != core.SkillActivationOperationFailed && failure != "" {
		return core.SkillActivationOperation{}, errors.New("non-failed skill activation operation cannot contain error")
	}
	if updatedAt.IsZero() {
		return core.SkillActivationOperation{}, errors.New("skill activation operation updated_at is required")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE skill_activation_operations SET state = ?, error = ?, updated_at = ? WHERE workspace = ? AND id = ? AND state = ?`, to, failure, formatSkillTime(updatedAt), strings.TrimSpace(workspace), strings.TrimSpace(operationID), from)
	if err != nil {
		return core.SkillActivationOperation{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return core.SkillActivationOperation{}, err
	}
	if changed != 1 {
		return core.SkillActivationOperation{}, errors.New("skill activation operation transition is stale")
	}
	return s.GetSkillActivationOperation(ctx, workspace, operationID)
}

func (s *Store) CompleteSkillActivation(ctx context.Context, operationID, policyDecisionID, actor string, rollback, automatic bool, reasonCode string, now time.Time) (core.SkillActivation, error) {
	if strings.TrimSpace(policyDecisionID) == "" || strings.TrimSpace(actor) == "" || now.IsZero() {
		return core.SkillActivation{}, errors.New("activation decision, actor, and timestamp are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.SkillActivation{}, err
	}
	defer tx.Rollback()
	operation, err := scanSkillActivationOperation(tx.QueryRowContext(ctx, `SELECT id,workspace,environment,skill_id,from_revision_id,to_revision_id,expected_generation,state,error,idempotency_key,created_at,updated_at FROM skill_activation_operations WHERE id = ?`, strings.TrimSpace(operationID)))
	if err != nil {
		return core.SkillActivation{}, err
	}
	if operation.State != core.SkillActivationOperationMaterializing {
		return core.SkillActivation{}, errors.New("activation operation is not materializing")
	}
	var activation core.SkillActivation
	var activated, updated string
	err = tx.QueryRowContext(ctx, `SELECT id,workspace,environment,skill_id,active_revision_id,active_digest,last_known_good_revision_id,last_known_good_digest,canary_revision_id,canary_digest,generation,policy_decision_id,materialization,activated_by,activated_at,updated_at FROM skill_activations WHERE workspace = ? AND environment = ? AND skill_id = ?`, operation.Workspace, operation.Environment, operation.SkillID).Scan(
		&activation.ID, &activation.Workspace, &activation.Environment, &activation.SkillID, &activation.ActiveRevisionID, &activation.ActiveDigest, &activation.LastKnownGoodRevisionID, &activation.LastKnownGoodDigest, &activation.CanaryRevisionID, &activation.CanaryDigest, &activation.Generation, &activation.PolicyDecisionID, &activation.Materialization, &activation.ActivatedBy, &activated, &updated)
	if err != nil {
		return core.SkillActivation{}, err
	}
	if activation.Generation != operation.ExpectedGeneration || activation.ActiveRevisionID != operation.FromRevisionID {
		return core.SkillActivation{}, errors.New("activation generation changed before completion")
	}
	var targetState core.SkillRevisionState
	var targetDigest string
	if err := tx.QueryRowContext(ctx, `SELECT state,bundle_digest FROM skill_revisions WHERE workspace = ? AND skill_id = ? AND id = ?`, operation.Workspace, operation.SkillID, operation.ToRevisionID).Scan(&targetState, &targetDigest); err != nil {
		return core.SkillActivation{}, err
	}
	if !core.CanTransitionSkillRevision(targetState, core.SkillRevisionActive) {
		return core.SkillActivation{}, errors.New("target skill revision is not activatable")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skill_revisions SET state = ? WHERE id = ? AND state = ?`, core.SkillRevisionPrevious, activation.ActiveRevisionID, core.SkillRevisionActive); err != nil {
		return core.SkillActivation{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE skill_revisions SET state = ? WHERE id = ? AND state = ?`, core.SkillRevisionActive, operation.ToRevisionID, targetState)
	if err != nil {
		return core.SkillActivation{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return core.SkillActivation{}, errors.New("target skill revision transition is stale")
	}
	lastKnownID, lastKnownDigest := activation.ActiveRevisionID, activation.ActiveDigest
	if rollback {
		lastKnownID, lastKnownDigest = operation.ToRevisionID, targetDigest
	}
	result, err = tx.ExecContext(ctx, `UPDATE skill_activations SET active_revision_id=?,active_digest=?,last_known_good_revision_id=?,last_known_good_digest=?,canary_revision_id='',canary_digest='',generation=generation+1,policy_decision_id=?,materialization=?,activated_by=?,activated_at=?,updated_at=? WHERE id=? AND generation=? AND active_revision_id=?`,
		operation.ToRevisionID, targetDigest, lastKnownID, lastKnownDigest, policyDecisionID, core.SkillMaterializationReady, actor, formatSkillTime(now), formatSkillTime(now), activation.ID, operation.ExpectedGeneration, operation.FromRevisionID)
	if err != nil {
		return core.SkillActivation{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return core.SkillActivation{}, errors.New("activation completion lost optimistic race")
	}
	result, err = tx.ExecContext(ctx, `UPDATE skill_activation_operations SET state=?,error='',updated_at=? WHERE id=? AND state=?`, core.SkillActivationOperationCompleted, formatSkillTime(now), operation.ID, core.SkillActivationOperationMaterializing)
	if err != nil {
		return core.SkillActivation{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return core.SkillActivation{}, errors.New("activation operation completion is stale")
	}
	if rollback {
		if strings.TrimSpace(reasonCode) == "" {
			return core.SkillActivation{}, errors.New("rollback reason_code is required")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO skill_rollback_events(id,workspace,environment,skill_id,from_revision_id,to_revision_id,reason_code,automatic,operation_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			"rollback:"+operation.ID, operation.Workspace, operation.Environment, operation.SkillID, operation.FromRevisionID, operation.ToRevisionID, reasonCode, automatic, operation.ID, formatSkillTime(now)); err != nil {
			return core.SkillActivation{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return core.SkillActivation{}, err
	}
	return s.GetSkillActivation(ctx, operation.Workspace, operation.Environment, operation.SkillID)
}

func (s *Store) ListSkillRollbackEvents(ctx context.Context, workspace, environment, skillID string, limit int) ([]core.SkillRollbackEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,workspace,environment,skill_id,from_revision_id,to_revision_id,reason_code,automatic,operation_id,created_at FROM skill_rollback_events WHERE workspace=? AND environment=? AND skill_id=? ORDER BY created_at DESC LIMIT ?`, strings.TrimSpace(workspace), strings.TrimSpace(environment), strings.TrimSpace(skillID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]core.SkillRollbackEvent, 0)
	for rows.Next() {
		var event core.SkillRollbackEvent
		var automatic int
		var created string
		if err := rows.Scan(&event.ID, &event.Workspace, &event.Environment, &event.SkillID, &event.FromRevisionID, &event.ToRevisionID, &event.ReasonCode, &automatic, &event.OperationID, &created); err != nil {
			return nil, err
		}
		event.Automatic = automatic != 0
		event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) CreateSkillResolution(ctx context.Context, resolution core.SkillResolution) error {
	if err := resolution.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO skill_resolutions(id,workspace,environment,principal_id,task_id,skill_id,revision_id,revision_number,digest,reason,policy_version,fallback_revision_id,fallback_digest,acknowledgement_token_hash,expires_at,resolved_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		resolution.ID, resolution.Workspace, resolution.Environment, resolution.PrincipalID, resolution.TaskID, resolution.SkillID,
		resolution.RevisionID, resolution.RevisionNumber, resolution.Digest, resolution.Reason, resolution.PolicyVersion,
		resolution.FallbackRevisionID, resolution.FallbackDigest, resolution.AcknowledgementTokenHash, formatSkillTime(resolution.ExpiresAt), formatSkillTime(resolution.ResolvedAt))
	return err
}

func (s *Store) CreateSkillEvaluationSuite(ctx context.Context, suite core.SkillEvaluationSuite) error {
	if err := suite.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var nextVersion int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM skill_evaluation_suites WHERE workspace=? AND skill_id=?`, suite.Workspace, suite.SkillID).Scan(&nextVersion); err != nil {
		return err
	}
	if suite.Version != nextVersion {
		return errors.New("evaluation suite version is stale")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_evaluation_suites(id,workspace,skill_id,version,digest,created_by,created_at) VALUES(?,?,?,?,?,?,?)`, suite.ID, suite.Workspace, suite.SkillID, suite.Version, suite.Digest, suite.CreatedBy, formatSkillTime(suite.CreatedAt)); err != nil {
		return err
	}
	for _, item := range suite.Cases {
		if _, err := tx.ExecContext(ctx, `INSERT INTO skill_evaluation_cases(suite_id,case_id,kind,summary,reference,required) VALUES(?,?,?,?,?,?)`, suite.ID, item.ID, item.Kind, item.Summary, item.Reference, item.Required); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetLatestSkillEvaluationSuite(ctx context.Context, workspace, skillID string) (core.SkillEvaluationSuite, error) {
	var suite core.SkillEvaluationSuite
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT id,skill_id,workspace,version,digest,created_by,created_at FROM skill_evaluation_suites WHERE workspace=? AND skill_id=? ORDER BY version DESC LIMIT 1`, strings.TrimSpace(workspace), strings.TrimSpace(skillID)).Scan(&suite.ID, &suite.SkillID, &suite.Workspace, &suite.Version, &suite.Digest, &suite.CreatedBy, &created)
	if err != nil {
		return core.SkillEvaluationSuite{}, err
	}
	suite.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	rows, err := s.db.QueryContext(ctx, `SELECT case_id,kind,summary,reference,required FROM skill_evaluation_cases WHERE suite_id=? ORDER BY case_id`, suite.ID)
	if err != nil {
		return core.SkillEvaluationSuite{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item core.SkillEvaluationCase
		if err := rows.Scan(&item.ID, &item.Kind, &item.Summary, &item.Reference, &item.Required); err != nil {
			return core.SkillEvaluationSuite{}, err
		}
		suite.Cases = append(suite.Cases, item)
	}
	if err := rows.Err(); err != nil {
		return core.SkillEvaluationSuite{}, err
	}
	return suite, suite.Validate()
}

func (s *Store) GetSkillEvaluationSuite(ctx context.Context, workspace, suiteID string, version int64) (core.SkillEvaluationSuite, error) {
	var suite core.SkillEvaluationSuite
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT id,skill_id,workspace,version,digest,created_by,created_at FROM skill_evaluation_suites WHERE workspace=? AND id=? AND version=?`, strings.TrimSpace(workspace), strings.TrimSpace(suiteID), version).Scan(&suite.ID, &suite.SkillID, &suite.Workspace, &suite.Version, &suite.Digest, &suite.CreatedBy, &created)
	if err != nil {
		return core.SkillEvaluationSuite{}, err
	}
	suite.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	rows, err := s.db.QueryContext(ctx, `SELECT case_id,kind,summary,reference,required FROM skill_evaluation_cases WHERE suite_id=? ORDER BY case_id`, suite.ID)
	if err != nil {
		return core.SkillEvaluationSuite{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item core.SkillEvaluationCase
		if err := rows.Scan(&item.ID, &item.Kind, &item.Summary, &item.Reference, &item.Required); err != nil {
			return core.SkillEvaluationSuite{}, err
		}
		suite.Cases = append(suite.Cases, item)
	}
	if err := rows.Err(); err != nil {
		return core.SkillEvaluationSuite{}, err
	}
	return suite, suite.Validate()
}

func (s *Store) CreateSkillEvaluationRun(ctx context.Context, run core.SkillEvaluationRun) error {
	if err := run.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_evaluation_runs(id,workspace,skill_id,revision_id,revision_digest,baseline_revision_id,baseline_digest,suite_id,suite_version,suite_digest,evaluator,evaluator_version,environment_fingerprint,verdict,started_at,completed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, run.Workspace, run.SkillID, run.RevisionID, run.RevisionDigest, run.BaselineRevisionID, run.BaselineDigest, run.SuiteID, run.SuiteVersion, run.SuiteDigest, run.Evaluator, run.EvaluatorVersion, run.EnvironmentFingerprint, run.Verdict, formatSkillTime(run.StartedAt), formatSkillTime(run.CompletedAt)); err != nil {
		return err
	}
	for _, result := range run.CaseResults {
		if _, err := tx.ExecContext(ctx, `INSERT INTO skill_evaluation_case_results(run_id,case_id,passed,independently_verified,failure_class,duration_ms) VALUES(?,?,?,?,?,?)`, run.ID, result.CaseID, result.Passed, result.IndependentlyVerified, result.FailureClass, result.DurationMS); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type skillActivationOperationScanner interface {
	Scan(...any) error
}

func scanSkillActivationOperation(row skillActivationOperationScanner) (core.SkillActivationOperation, error) {
	var operation core.SkillActivationOperation
	var created, updated string
	if err := row.Scan(&operation.ID, &operation.Workspace, &operation.Environment, &operation.SkillID, &operation.FromRevisionID, &operation.ToRevisionID, &operation.ExpectedGeneration, &operation.State, &operation.Error, &operation.IdempotencyKey, &created, &updated); err != nil {
		return core.SkillActivationOperation{}, err
	}
	operation.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	operation.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	if err := operation.Validate(); err != nil {
		return core.SkillActivationOperation{}, err
	}
	return operation, nil
}

type skillQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func listSkillAliases(ctx context.Context, queryer skillQueryer, workspace, skillID string) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT alias FROM skill_aliases WHERE workspace = ? AND skill_id = ? ORDER BY alias`, workspace, skillID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func listSkillRevisionParents(ctx context.Context, queryer skillQueryer, revisionID string) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT parent_revision_id FROM skill_revision_parents WHERE revision_id = ? ORDER BY parent_revision_id`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func listSkillRevisionFiles(ctx context.Context, queryer skillQueryer, revisionID string) ([]core.SkillBundleFile, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT path,digest,size_bytes FROM skill_revision_files WHERE revision_id = ? ORDER BY path`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.SkillBundleFile, 0)
	for rows.Next() {
		var value core.SkillBundleFile
		if err := rows.Scan(&value.Path, &value.Digest, &value.SizeBytes); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func formatSkillTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
