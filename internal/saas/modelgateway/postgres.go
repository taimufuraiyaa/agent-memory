package modelgateway

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
)

type PostgresUsageSink struct {
	pool     *pgxpool.Pool
	observer func(Usage)
}

func NewPostgresUsageSink(pool *pgxpool.Pool) *PostgresUsageSink {
	return &PostgresUsageSink{pool: pool}
}

func NewObservedPostgresUsageSink(pool *pgxpool.Pool, observer func(Usage)) *PostgresUsageSink {
	return &PostgresUsageSink{pool: pool, observer: observer}
}

func (s *PostgresUsageSink) RecordUsage(ctx context.Context, value Usage) error {
	if s == nil || s.pool == nil || value.TenantID == "" || value.Provider == "" || value.Model == "" || value.Operation == "" || value.OccurredAt.IsZero() {
		return errors.New("model usage record is incomplete")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", value.TenantID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO saas_model_usage
		(tenant_id,id,source_id,source_version,operation,provider,model,dimensions,input_tokens,output_tokens,estimated_cost_micros,outcome,occurred_at)
		VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		value.TenantID, uuid.NewString(), value.SourceID, value.SourceVersion, value.Operation, value.Provider, value.Model,
		value.Dimensions, value.InputTokens, value.OutputTokens, value.EstimatedCostMicros, value.Outcome, value.OccurredAt)
	if err != nil {
		return err
	}
	riskSignals := []string{}
	if value.EstimatedCostMicros >= 1_000_000 {
		riskSignals = append(riskSignals, "cost_above_baseline")
	}
	requestID, traceID := audit.NewRequestIDs()
	if err := audit.Append(ctx, tx, audit.Event{TenantID: value.TenantID, OccurredAt: value.OccurredAt, ActorType: "system", ActorID: "model-gateway", Service: "retrieval", Operation: "model.usage", Outcome: value.Outcome, RequestID: requestID, TraceID: traceID, TargetType: "source", TargetID: value.SourceID, PolicyVersion: "model-gateway-v1", ReasonCode: string(value.Operation), RiskSignals: riskSignals, SafeMetadata: map[string]any{"provider": value.Provider, "model": value.Model, "input_tokens": value.InputTokens, "output_tokens": value.OutputTokens, "estimated_cost_micros": value.EstimatedCostMicros}}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if s.observer != nil {
		s.observer(value)
	}
	return nil
}
