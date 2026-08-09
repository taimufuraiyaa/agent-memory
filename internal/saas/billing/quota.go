package billing

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type Quota struct{ pool *pgxpool.Pool }

func NewQuota(pool *pgxpool.Pool) *Quota { return &Quota{pool: pool} }
func (q *Quota) AllowModel(ctx context.Context, tenantID string, pendingTokens int, at time.Time) (bool, error) {
	if q == nil || q.pool == nil {
		return false, errors.New("billing quota is not configured")
	}
	start := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	var maximum, used int64
	err := q.pool.QueryRow(ctx, `SELECT e.max_tokens_per_month,COALESCE((SELECT sum(input_tokens+output_tokens) FROM saas_model_usage u WHERE u.tenant_id=e.tenant_id AND u.occurred_at>=$2),0) FROM saas_tenant_entitlements e WHERE e.tenant_id=$1`, tenantID, start).Scan(&maximum, &used)
	return err == nil && used+int64(pendingTokens) <= maximum, err
}
