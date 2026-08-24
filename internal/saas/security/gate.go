package security

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Gate struct{ pool *pgxpool.Pool }

func NewGate(pool *pgxpool.Pool) *Gate { return &Gate{pool: pool} }
func (g *Gate) Allow(ctx context.Context, tenantID string, at time.Time) (bool, error) {
	if g == nil || g.pool == nil {
		return false, errors.New("security gate is not configured")
	}
	tx, err := g.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		return false, err
	}
	var state string
	var limited bool
	var maximum int
	err = tx.QueryRow(ctx, `SELECT t.state,COALESCE(c.rate_limited_until>$2,false),e.max_requests_per_minute FROM saas_tenants t JOIN saas_tenant_entitlements e ON e.tenant_id=t.id LEFT JOIN saas_tenant_security_controls c ON c.tenant_id=t.id WHERE t.id=$1`, tenantID, at).Scan(&state, &limited, &maximum)
	if err != nil {
		return false, err
	}
	window := at.Truncate(time.Minute)
	var count int
	err = tx.QueryRow(ctx, `INSERT INTO saas_request_rate_windows(tenant_id,window_start,request_count) VALUES($1,$2,1) ON CONFLICT(tenant_id,window_start) DO UPDATE SET request_count=saas_request_rate_windows.request_count+1 RETURNING request_count`, tenantID, window).Scan(&count)
	if err != nil {
		return false, err
	}
	id := uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO saas_usage_events(tenant_id,id,usage_key,metric,quantity,source_type,source_id,occurred_at) VALUES($1,$2,$3,'api_requests',1,'api_request',$4,$5)`, tenantID, id, "api:"+id, id, at)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return state == "active" && !limited && count <= maximum, nil
}
