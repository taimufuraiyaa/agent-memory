package skillworker

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type PostgresReadiness struct {
	Pool     *pgxpool.Pool
	Registry *application.SkillStageRegistry
}

func (r *PostgresReadiness) CheckSkillWorkerReadiness(ctx context.Context, configuration RuntimeConfig) error {
	if r == nil || r.Pool == nil || r.Registry == nil {
		return errors.New("hosted skill worker readiness dependencies are required")
	}
	if err := r.Pool.Ping(ctx); err != nil {
		return err
	}
	var role string
	var orchestrationTable bool
	if err := r.Pool.QueryRow(ctx, `SELECT current_user,to_regclass('public.saas_skill_orchestrator_jobs') IS NOT NULL`).Scan(&role, &orchestrationTable); err != nil {
		return err
	}
	if role != configuration.DatabaseRole || role != DatabaseRole {
		return errors.New("hosted skill worker database role is not least-privilege worker role")
	}
	if !orchestrationTable {
		return errors.New("hosted skill worker orchestration migration is unavailable")
	}
	if !r.Registry.Supports(core.SkillOrchestratorContractVersion, core.SkillStageRollback) {
		return errors.New("hosted skill worker rollback executor is unavailable")
	}
	return nil
}
