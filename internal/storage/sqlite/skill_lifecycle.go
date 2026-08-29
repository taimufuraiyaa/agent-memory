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
