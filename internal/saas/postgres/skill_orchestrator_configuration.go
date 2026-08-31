package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	saasaudit "github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
)

func (r *SkillOrchestratorRepository) GetLatestSkillOrchestratorConfiguration(ctx context.Context, scope core.SkillOrchestratorScope) (core.SkillOrchestratorConfiguration, error) {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	defer tx.Rollback(ctx)
	configuration, err := scanHostedSkillOrchestratorConfiguration(tx.QueryRow(ctx, `SELECT configuration FROM saas_skill_orchestrator_configurations WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 ORDER BY version DESC LIMIT 1`, scope.TenantID, scope.WorkspaceID, scope.Environment))
	if err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	return configuration, nil
}

func (r *SkillOrchestratorRepository) GetSkillOrchestratorConfiguration(ctx context.Context, scope core.SkillOrchestratorScope, version int64) (core.SkillOrchestratorConfiguration, error) {
	if version < 1 {
		return core.SkillOrchestratorConfiguration{}, errors.New("positive configuration version is required")
	}
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	defer tx.Rollback(ctx)
	configuration, err := scanHostedSkillOrchestratorConfiguration(tx.QueryRow(ctx, `SELECT configuration FROM saas_skill_orchestrator_configurations WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND version=$4`, scope.TenantID, scope.WorkspaceID, scope.Environment, version))
	if err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	return configuration, nil
}

func (r *SkillOrchestratorRepository) StoreSkillOrchestratorConfiguration(ctx context.Context, configuration core.SkillOrchestratorConfiguration, audit core.SkillOrchestratorConfigurationAudit) (bool, error) {
	if err := configuration.Validate(); err != nil {
		return false, err
	}
	encoded, err := json.Marshal(configuration)
	if err != nil {
		return false, err
	}
	tx, err := r.begin(ctx, configuration.Scope)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO saas_skill_orchestrator_configurations(tenant_id,workspace_id,environment,version,contract_version,digest,mode,configuration,approval_reference,release_evidence_reference,signature_reference,created_by,created_at) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11,$12,$13) ON CONFLICT(tenant_id,workspace_id,environment,version) DO NOTHING`,
		configuration.Scope.TenantID, configuration.Scope.WorkspaceID, configuration.Scope.Environment, configuration.Version,
		configuration.ContractVersion, configuration.Digest, configuration.Mode, encoded, configuration.ApprovalReference,
		configuration.ReleaseEvidenceReference, configuration.SignatureReference, configuration.CreatedBy, configuration.CreatedAt)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		stored, getErr := scanHostedSkillOrchestratorConfiguration(tx.QueryRow(ctx, `SELECT configuration FROM saas_skill_orchestrator_configurations WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND version=$4`, configuration.Scope.TenantID, configuration.Scope.WorkspaceID, configuration.Scope.Environment, configuration.Version))
		if getErr != nil {
			return false, getErr
		}
		if stored.Digest != configuration.Digest {
			return false, ErrSkillOrchestratorConflict
		}
		return false, tx.Commit(ctx)
	}
	if err := saasaudit.Append(ctx, tx, saasaudit.Event{
		TenantID: configuration.Scope.TenantID, ID: uuid.NewString(), OccurredAt: audit.OccurredAt,
		ActorType: "account", ActorID: audit.ActorID, Service: "skill-orchestrator", Operation: audit.Operation, Outcome: "success",
		RequestID: audit.RequestID, TraceID: audit.RequestID, TargetType: "skill_orchestrator_configuration", TargetID: strconv.FormatInt(configuration.Version, 10),
		PolicyVersion: configuration.PolicyDigest, ReasonCode: audit.ReasonCode,
		SafeMetadata: map[string]any{"workspace_id": configuration.Scope.WorkspaceID, "environment": configuration.Scope.Environment, "from_version": audit.FromVersion, "to_version": audit.ToVersion, "mode": configuration.Mode},
	}); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

type hostedSkillOrchestratorConfigurationScanner interface{ Scan(...any) error }

func scanHostedSkillOrchestratorConfiguration(row hostedSkillOrchestratorConfigurationScanner) (core.SkillOrchestratorConfiguration, error) {
	var encoded []byte
	if err := row.Scan(&encoded); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return core.SkillOrchestratorConfiguration{}, core.ErrSkillOrchestratorConfigurationNotFound
		}
		return core.SkillOrchestratorConfiguration{}, err
	}
	var configuration core.SkillOrchestratorConfiguration
	if err := json.Unmarshal(encoded, &configuration); err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	if err := configuration.Validate(); err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	return configuration, nil
}
