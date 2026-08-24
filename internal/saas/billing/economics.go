package billing

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type UnitCosts struct {
	StorageByteMicros, PassageMicros, EmbeddingMicros, GenerationTokenMicros int64
	EmbeddingTokenMicros, APIRequestMicros, JobMicros, ExportMicros          int64
}

type EconomicsReport struct {
	PeriodStart, PeriodEnd                                          time.Time
	ActiveMembers, Sources, Queries, ModelCalls                     int64
	TotalCostMicros, CostPerActiveMemberMicros, CostPerSourceMicros int64
	CostPerQueryMicros, CostPerModelCallMicros                      int64
	UsageQuantities                                                 map[string]int64
}

type EconomicsService struct {
	pool  *pgxpool.Pool
	costs UnitCosts
}

type WorstCaseEstimate struct {
	MaximumStorageBytes, MaximumMonthlyTokens, MaximumMonthlyRequests int64
	MaximumSources, MaximumConcurrentJobs                             int
	EstimatedCostMicros, ApprovedCeilingMicros                        int64
	Bounded                                                           bool
}

func NewEconomicsService(pool *pgxpool.Pool, costs UnitCosts) *EconomicsService {
	return &EconomicsService{pool: pool, costs: costs}
}

func (s *EconomicsService) Report(ctx context.Context, start, end time.Time) (EconomicsReport, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("billing:read") || s == nil || s.pool == nil || !end.After(start) {
		return EconomicsReport{}, errors.New("unit economics report is unavailable")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EconomicsReport{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", request.TenantID); err != nil {
		return EconomicsReport{}, err
	}
	report := EconomicsReport{PeriodStart: start.UTC(), PeriodEnd: end.UTC(), UsageQuantities: map[string]int64{}}
	rows, err := tx.Query(ctx, `SELECT metric,sum(quantity) FROM saas_usage_events WHERE tenant_id=$1 AND occurred_at>=$2 AND occurred_at<$3 GROUP BY metric`, request.TenantID, start.UTC(), end.UTC())
	if err != nil {
		return report, err
	}
	for rows.Next() {
		var metric string
		var quantity int64
		if err := rows.Scan(&metric, &quantity); err != nil {
			rows.Close()
			return report, err
		}
		report.UsageQuantities[metric] = quantity
	}
	rows.Close()
	if err := tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM saas_memberships WHERE tenant_id=$1 AND state='active'),
		(SELECT count(*) FROM saas_sources WHERE tenant_id=$1 AND state<>'deleted'),
		(SELECT count(*) FROM saas_audit_events WHERE tenant_id=$1 AND operation='retrieval.query' AND occurred_at>=$2 AND occurred_at<$3),
		(SELECT count(*) FROM saas_model_usage WHERE tenant_id=$1 AND occurred_at>=$2 AND occurred_at<$3)`, request.TenantID, start.UTC(), end.UTC()).Scan(&report.ActiveMembers, &report.Sources, &report.Queries, &report.ModelCalls); err != nil {
		return report, err
	}
	report.TotalCostMicros = report.UsageQuantities["storage_bytes"]*s.costs.StorageByteMicros + report.UsageQuantities["passages"]*s.costs.PassageMicros + report.UsageQuantities["embeddings"]*s.costs.EmbeddingMicros + report.UsageQuantities["generation_tokens"]*s.costs.GenerationTokenMicros + report.UsageQuantities["embedding_tokens"]*s.costs.EmbeddingTokenMicros + report.UsageQuantities["api_requests"]*s.costs.APIRequestMicros + report.UsageQuantities["jobs"]*s.costs.JobMicros + report.UsageQuantities["exports"]*s.costs.ExportMicros
	report.CostPerActiveMemberMicros = divide(report.TotalCostMicros, report.ActiveMembers)
	report.CostPerSourceMicros = divide(report.TotalCostMicros, report.Sources)
	report.CostPerQueryMicros = divide(report.TotalCostMicros, report.Queries)
	report.CostPerModelCallMicros = divide(report.TotalCostMicros, report.ModelCalls)
	return report, tx.Commit(ctx)
}

func (s *EconomicsService) WorstCase(ctx context.Context, approvedCeilingMicros int64) (WorstCaseEstimate, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("billing:read") || s == nil || s.pool == nil || approvedCeilingMicros <= 0 {
		return WorstCaseEstimate{}, errors.New("worst-case estimate is unavailable")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WorstCaseEstimate{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", request.TenantID); err != nil {
		return WorstCaseEstimate{}, err
	}
	var maxRequestsPerMinute int64
	result := WorstCaseEstimate{ApprovedCeilingMicros: approvedCeilingMicros}
	if err := tx.QueryRow(ctx, `SELECT max_storage_bytes,max_tokens_per_month,max_requests_per_minute,max_source_count,max_concurrent_jobs FROM saas_tenant_entitlements WHERE tenant_id=$1`, request.TenantID).Scan(&result.MaximumStorageBytes, &result.MaximumMonthlyTokens, &maxRequestsPerMinute, &result.MaximumSources, &result.MaximumConcurrentJobs); err != nil {
		return result, err
	}
	result.MaximumMonthlyRequests = maxRequestsPerMinute * 60 * 24 * 31
	result.EstimatedCostMicros = result.MaximumStorageBytes*s.costs.StorageByteMicros + result.MaximumMonthlyTokens*max64(s.costs.GenerationTokenMicros, s.costs.EmbeddingTokenMicros) + result.MaximumMonthlyRequests*s.costs.APIRequestMicros + int64(result.MaximumSources)*s.costs.PassageMicros + int64(result.MaximumConcurrentJobs)*s.costs.JobMicros
	result.Bounded = result.MaximumStorageBytes > 0 && result.MaximumMonthlyTokens > 0 && result.MaximumMonthlyRequests > 0 && result.MaximumSources > 0 && result.MaximumConcurrentJobs > 0 && result.EstimatedCostMicros <= approvedCeilingMicros
	return result, tx.Commit(ctx)
}

func divide(value, count int64) int64 {
	if count == 0 {
		return 0
	}
	return value / count
}

func max64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
