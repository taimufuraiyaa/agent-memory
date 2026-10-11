package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func (s *Store) GetLatestSkillOrchestratorConfiguration(ctx context.Context, scope core.SkillOrchestratorScope) (core.SkillOrchestratorConfiguration, error) {
	if s == nil || s.db == nil {
		return core.SkillOrchestratorConfiguration{}, errors.New("sqlite store is required")
	}
	if err := scope.Validate(); err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	return scanSkillOrchestratorConfiguration(s.db.QueryRowContext(ctx, `SELECT configuration_json FROM skill_orchestrator_configurations WHERE tenant_id=? AND workspace_id=? AND environment=? ORDER BY version DESC LIMIT 1`, scope.TenantID, scope.WorkspaceID, scope.Environment))
}

func (s *Store) GetSkillOrchestratorConfiguration(ctx context.Context, scope core.SkillOrchestratorScope, version int64) (core.SkillOrchestratorConfiguration, error) {
	if s == nil || s.db == nil || version < 1 {
		return core.SkillOrchestratorConfiguration{}, errors.New("sqlite store and positive configuration version are required")
	}
	if err := scope.Validate(); err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	return scanSkillOrchestratorConfiguration(s.db.QueryRowContext(ctx, `SELECT configuration_json FROM skill_orchestrator_configurations WHERE tenant_id=? AND workspace_id=? AND environment=? AND version=?`, scope.TenantID, scope.WorkspaceID, scope.Environment, version))
}

func (s *Store) StoreSkillOrchestratorConfiguration(ctx context.Context, configuration core.SkillOrchestratorConfiguration, audit core.SkillOrchestratorConfigurationAudit) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("sqlite store is required")
	}
	if err := configuration.Validate(); err != nil {
		return false, err
	}
	encoded, err := json.Marshal(configuration)
	if err != nil {
		return false, err
	}
	event, idsJSON, metadataJSON, err := prepareAuditEvent(AuditEventInput{
		Workspace: configuration.Scope.WorkspaceID, Operation: audit.Operation, Outcome: "success", Actor: audit.ActorID,
		Source: "skill_orchestrator_configuration", RequestID: audit.RequestID, TargetType: "skill_orchestrator_configuration",
		TargetIDs: []string{strconv.FormatInt(configuration.Version, 10)}, Reason: audit.ReasonCode,
		Metadata: map[string]any{"from_version": audit.FromVersion, "to_version": audit.ToVersion, "mode": configuration.Mode}, OccurredAt: audit.OccurredAt,
	})
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO skill_orchestrator_configurations(tenant_id,workspace_id,environment,version,contract_version,digest,mode,configuration_json,approval_reference,release_evidence_reference,signature_reference,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(tenant_id,workspace_id,environment,version) DO NOTHING`,
		configuration.Scope.TenantID, configuration.Scope.WorkspaceID, configuration.Scope.Environment, configuration.Version,
		configuration.ContractVersion, configuration.Digest, configuration.Mode, string(encoded), configuration.ApprovalReference,
		configuration.ReleaseEvidenceReference, configuration.SignatureReference, configuration.CreatedBy, formatSkillOrchestratorTime(configuration.CreatedAt))
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows == 0 {
		stored, getErr := scanSkillOrchestratorConfiguration(tx.QueryRowContext(ctx, `SELECT configuration_json FROM skill_orchestrator_configurations WHERE tenant_id=? AND workspace_id=? AND environment=? AND version=?`, configuration.Scope.TenantID, configuration.Scope.WorkspaceID, configuration.Scope.Environment, configuration.Version))
		if getErr != nil {
			return false, getErr
		}
		if stored.Digest != configuration.Digest {
			return false, errors.New("skill orchestrator configuration version conflict")
		}
		return false, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, auditInsertSQL, event.ID, event.SchemaVersion, event.Workspace, event.Operation, event.Outcome, event.Actor, event.Source, event.RequestID, event.SessionID, event.TargetType, idsJSON, event.TargetCount, event.Reason, metadataJSON, event.OccurredAt.Format(timeFormatNano)); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

const timeFormatNano = "2006-01-02T15:04:05.999999999Z07:00"

type skillOrchestratorConfigurationScanner interface{ Scan(...any) error }

func scanSkillOrchestratorConfiguration(row skillOrchestratorConfigurationScanner) (core.SkillOrchestratorConfiguration, error) {
	var encoded string
	if err := row.Scan(&encoded); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.SkillOrchestratorConfiguration{}, core.ErrSkillOrchestratorConfigurationNotFound
		}
		return core.SkillOrchestratorConfiguration{}, err
	}
	var configuration core.SkillOrchestratorConfiguration
	if err := json.Unmarshal([]byte(encoded), &configuration); err != nil {
		return core.SkillOrchestratorConfiguration{}, fmt.Errorf("decode skill orchestrator configuration: %w", err)
	}
	if err := configuration.Validate(); err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	return configuration, nil
}
