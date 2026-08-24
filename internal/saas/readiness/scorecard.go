package readiness

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type TenantScorecard struct {
	GeneratedAt      time.Time        `json:"generated_at"`
	Funnel           map[string]int64 `json:"funnel"`
	SourceStates     map[string]int64 `json:"source_states"`
	JobStates        map[string]int64 `json:"job_states"`
	Usage            map[string]int64 `json:"usage"`
	PendingDeletions int64            `json:"pending_deletions"`
	OpenFindings     int64            `json:"open_security_findings"`
	OpenLegalCases   int64            `json:"open_legal_cases"`
	OldestJobAgeSec  int64            `json:"oldest_job_age_seconds"`
}

// Scorecard returns only categorical counts and age signals. It deliberately
// never selects source names, memory/note text, prompts, or customer identity.
func (s *Service) Scorecard(ctx context.Context, since time.Time) (TenantScorecard, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("account:manage") || s == nil || s.pool == nil {
		return TenantScorecard{}, errors.New("launch scorecard is unavailable")
	}
	now := s.now().UTC()
	result := TenantScorecard{GeneratedAt: now, Funnel: map[string]int64{}, SourceStates: map[string]int64{}, JobStates: map[string]int64{}, Usage: map[string]int64{}}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", request.TenantID); err != nil {
		return result, err
	}
	if err := scanCounts(ctx, tx, `SELECT event_name,count(*) FROM saas_product_analytics WHERE tenant_id=$1 AND occurred_at>=$2 GROUP BY event_name`, result.Funnel, request.TenantID, since.UTC()); err != nil {
		return result, err
	}
	if err := scanCounts(ctx, tx, `SELECT state,count(*) FROM saas_sources WHERE tenant_id=$1 GROUP BY state`, result.SourceStates, request.TenantID); err != nil {
		return result, err
	}
	if err := scanCounts(ctx, tx, `SELECT state,count(*) FROM saas_jobs WHERE tenant_id=$1 GROUP BY state`, result.JobStates, request.TenantID); err != nil {
		return result, err
	}
	if err := scanCounts(ctx, tx, `SELECT metric,sum(quantity) FROM saas_usage_events WHERE tenant_id=$1 AND occurred_at>=$2 GROUP BY metric`, result.Usage, request.TenantID, since.UTC()); err != nil {
		return result, err
	}
	if err := tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM saas_deletion_operations WHERE tenant_id=$1 AND state NOT IN ('completed','failed')),
		(SELECT count(*) FROM saas_security_findings WHERE tenant_id=$1 AND state IN ('open','acknowledged')),
		(SELECT count(*) FROM saas_legal_cases WHERE tenant_id=$1 AND state NOT IN ('closed','deleted','restored')),
		COALESCE((SELECT extract(epoch FROM ($2-min(created_at)))::bigint FROM saas_jobs WHERE tenant_id=$1 AND state IN ('queued','running','retry')),0)`, request.TenantID, now).Scan(&result.PendingDeletions, &result.OpenFindings, &result.OpenLegalCases, &result.OldestJobAgeSec); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func scanCounts(ctx context.Context, tx pgx.Tx, query string, target map[string]int64, args ...any) error {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var value int64
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		target[key] = value
	}
	return rows.Err()
}
