// Package billing owns content-free usage, subscriptions, and entitlements.
package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
)

type Webhook struct {
	Provider, EventID, TenantID, EventType, PlanID, State, CustomerRef, SubscriptionRef string
	ProviderCreatedAt, PeriodEndsAt                                                     time.Time
}
type Entitlements struct {
	PlanID, Version, BillingState                                                 string
	SourceUploadEnabled                                                           bool
	MaxSourceBytes                                                                int64
	MaxSourceCount, MaxConcurrentUploads, MaxConcurrentJobs, MaxRequestsPerMinute int
	MaxTokensPerMonth, MaxStorageBytes                                            int64
}
type Repository struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewRepository(pool *pgxpool.Pool, now func() time.Time) *Repository {
	if now == nil {
		now = time.Now
	}
	return &Repository{pool: pool, now: now}
}

func (r *Repository) ApplyVerifiedWebhook(ctx context.Context, value Webhook) (bool, error) {
	if r == nil || r.pool == nil || value.Provider == "" || value.EventID == "" || value.TenantID == "" || value.ProviderCreatedAt.IsZero() {
		return false, errors.New("verified billing webhook is incomplete")
	}
	tx, err := r.begin(ctx, value.TenantID)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	payload, _ := json.Marshal(value)
	hash := sha256.Sum256(payload)
	tag, err := tx.Exec(ctx, `INSERT INTO saas_billing_webhook_events(provider,provider_event_id,tenant_id,event_type,provider_created_at,payload_sha256,applied,received_at) VALUES($1,$2,$3,$4,$5,$6,false,$7) ON CONFLICT DO NOTHING`, value.Provider, value.EventID, value.TenantID, value.EventType, value.ProviderCreatedAt, hex.EncodeToString(hash[:]), r.now().UTC())
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	var currentEvent time.Time
	if err = tx.QueryRow(ctx, `SELECT last_provider_event_at FROM saas_subscriptions WHERE tenant_id=$1 FOR UPDATE`, value.TenantID).Scan(&currentEvent); err != nil {
		return false, err
	}
	applied := value.ProviderCreatedAt.After(currentEvent)
	if applied {
		if value.PlanID != "trial" && value.PlanID != "individual" {
			return false, errors.New("unknown billing plan")
		}
		if value.State != "trialing" && value.State != "active" && value.State != "past_due" && value.State != "canceled" {
			return false, errors.New("invalid subscription state")
		}
		grace := any(nil)
		if value.State == "past_due" {
			grace = value.ProviderCreatedAt.Add(7 * 24 * time.Hour)
		}
		var periodEnd any
		if !value.PeriodEndsAt.IsZero() {
			periodEnd = value.PeriodEndsAt
		}
		_, err = tx.Exec(ctx, `UPDATE saas_subscriptions SET provider_customer_ref=$2,provider_subscription_ref=$3,plan_id=$4,state=$5,grace_expires_at=$6,current_period_ends_at=$7,last_provider_event_at=$8,updated_at=$9 WHERE tenant_id=$1`, value.TenantID, value.CustomerRef, value.SubscriptionRef, value.PlanID, value.State, grace, periodEnd, value.ProviderCreatedAt, r.now().UTC())
		if err == nil {
			err = r.applyEntitlements(ctx, tx, value)
		}
		if err != nil {
			return false, err
		}
		_, err = tx.Exec(ctx, `UPDATE saas_billing_webhook_events SET applied=true WHERE provider=$1 AND provider_event_id=$2`, value.Provider, value.EventID)
		if err != nil {
			return false, err
		}
	}
	request, trace := audit.NewRequestIDs()
	if err = audit.Append(ctx, tx, audit.Event{TenantID: value.TenantID, OccurredAt: r.now().UTC(), ActorType: "system", ActorID: "billing-webhook", Service: "billing", Operation: "billing.webhook.apply", Outcome: "success", RequestID: request, TraceID: trace, TargetType: "subscription", TargetID: value.SubscriptionRef, PolicyVersion: "billing-v1", ReasonCode: value.EventType, SafeMetadata: map[string]any{"provider": value.Provider, "plan_id": value.PlanID, "state": value.State, "applied": applied}}); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return applied, nil
}

func (r *Repository) applyEntitlements(ctx context.Context, tx pgx.Tx, value Webhook) error {
	var limits []byte
	if err := tx.QueryRow(ctx, `SELECT limits FROM saas_plans WHERE id=$1 AND state='active'`, value.PlanID).Scan(&limits); err != nil {
		return err
	}
	var raw struct {
		MaxSourceBytes       int64 `json:"max_source_bytes"`
		MaxSourceCount       int   `json:"max_source_count"`
		MaxConcurrentUploads int   `json:"max_concurrent_uploads"`
		MaxConcurrentJobs    int   `json:"max_concurrent_jobs"`
		MaxRequestsPerMinute int   `json:"max_requests_per_minute"`
		MaxTokensPerMonth    int64 `json:"max_tokens_per_month"`
		MaxStorageBytes      int64 `json:"max_storage_bytes"`
	}
	if err := json.Unmarshal(limits, &raw); err != nil {
		return err
	}
	uploadEnabled := value.State == "trialing" || value.State == "active" || value.State == "past_due"
	_, err := tx.Exec(ctx, `UPDATE saas_tenant_entitlements SET plan_id=$2,entitlement_version='plan-v1',source_upload_enabled=$3,max_source_bytes=$4,max_source_count=$5,max_concurrent_uploads=$6,max_concurrent_jobs=$7,max_requests_per_minute=$8,max_tokens_per_month=$9,max_storage_bytes=$10,billing_state=$11,updated_at=$12 WHERE tenant_id=$1`, value.TenantID, value.PlanID, uploadEnabled, raw.MaxSourceBytes, raw.MaxSourceCount, raw.MaxConcurrentUploads, raw.MaxConcurrentJobs, raw.MaxRequestsPerMinute, raw.MaxTokensPerMonth, raw.MaxStorageBytes, value.State, r.now().UTC())
	return err
}

func (r *Repository) Entitlements(ctx context.Context, tenantID string) (Entitlements, error) {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return Entitlements{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var value Entitlements
	err = tx.QueryRow(ctx, `SELECT plan_id,entitlement_version,billing_state,source_upload_enabled,max_source_bytes,max_source_count,max_concurrent_uploads,max_concurrent_jobs,max_requests_per_minute,max_tokens_per_month,max_storage_bytes FROM saas_tenant_entitlements WHERE tenant_id=$1`, tenantID).Scan(&value.PlanID, &value.Version, &value.BillingState, &value.SourceUploadEnabled, &value.MaxSourceBytes, &value.MaxSourceCount, &value.MaxConcurrentUploads, &value.MaxConcurrentJobs, &value.MaxRequestsPerMinute, &value.MaxTokensPerMonth, &value.MaxStorageBytes)
	return value, err
}

func (r *Repository) ReconcileUsage(ctx context.Context, tenantID string, period time.Time) (map[string]int64, error) {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	start := time.Date(period.Year(), period.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	statements := []string{
		`INSERT INTO saas_usage_events(tenant_id,id,usage_key,metric,quantity,source_type,source_id,occurred_at) SELECT tenant_id,gen_random_uuid(),'storage:'||source_id::text,'storage_bytes',expected_size,'source',source_id::text,created_at FROM saas_upload_grants WHERE tenant_id=$1 AND created_at>=$2 AND created_at<$3 ON CONFLICT(tenant_id,usage_key) DO NOTHING`,
		`INSERT INTO saas_usage_events(tenant_id,id,usage_key,metric,quantity,source_type,source_id,occurred_at) SELECT p.tenant_id,gen_random_uuid(),'passages:'||p.source_id::text||':'||p.source_version,'passages',count(*),'source',p.source_id::text,min(v.created_at) FROM saas_source_passages p JOIN saas_source_versions v ON v.tenant_id=p.tenant_id AND v.source_id=p.source_id AND v.version=p.source_version WHERE p.tenant_id=$1 AND v.created_at>=$2 AND v.created_at<$3 GROUP BY p.tenant_id,p.source_id,p.source_version ON CONFLICT(tenant_id,usage_key) DO UPDATE SET quantity=EXCLUDED.quantity`,
		`INSERT INTO saas_usage_events(tenant_id,id,usage_key,metric,quantity,source_type,source_id,occurred_at) SELECT tenant_id,gen_random_uuid(),'embeddings:'||source_id::text||':'||source_version,'embeddings',count(*),'source',source_id::text,min(projected_at) FROM saas_vector_documents WHERE tenant_id=$1 AND projected_at>=$2 AND projected_at<$3 GROUP BY tenant_id,source_id,source_version ON CONFLICT(tenant_id,usage_key) DO UPDATE SET quantity=EXCLUDED.quantity`,
		`INSERT INTO saas_usage_events(tenant_id,id,usage_key,metric,quantity,source_type,source_id,occurred_at) SELECT tenant_id,gen_random_uuid(),'model:'||id::text,CASE WHEN operation='generate' THEN 'generation_tokens' ELSE 'embedding_tokens' END,input_tokens+output_tokens,'model_usage',id::text,occurred_at FROM saas_model_usage WHERE tenant_id=$1 AND occurred_at>=$2 AND occurred_at<$3 ON CONFLICT(tenant_id,usage_key) DO NOTHING`,
		`INSERT INTO saas_usage_events(tenant_id,id,usage_key,metric,quantity,source_type,source_id,occurred_at) SELECT tenant_id,gen_random_uuid(),'api:'||id::text,'api_requests',1,'audit',id::text,occurred_at FROM saas_audit_events WHERE tenant_id=$1 AND service='api' AND occurred_at>=$2 AND occurred_at<$3 ON CONFLICT(tenant_id,usage_key) DO NOTHING`,
		`INSERT INTO saas_usage_events(tenant_id,id,usage_key,metric,quantity,source_type,source_id,occurred_at) SELECT tenant_id,gen_random_uuid(),'job:'||id::text,'jobs',1,'job',id::text,created_at FROM saas_jobs WHERE tenant_id=$1 AND created_at>=$2 AND created_at<$3 ON CONFLICT(tenant_id,usage_key) DO NOTHING`,
		`INSERT INTO saas_usage_events(tenant_id,id,usage_key,metric,quantity,source_type,source_id,occurred_at) SELECT tenant_id,gen_random_uuid(),'export:'||id::text,'exports',1,'export',id::text,requested_at FROM saas_exports WHERE tenant_id=$1 AND requested_at>=$2 AND requested_at<$3 ON CONFLICT(tenant_id,usage_key) DO NOTHING`,
	}
	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement, tenantID, start, end); err != nil {
			return nil, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO saas_usage_aggregates(tenant_id,period_start,metric,quantity,reconciled_at) SELECT tenant_id,$2::timestamptz::date,metric,sum(quantity),$3 FROM saas_usage_events WHERE tenant_id=$1 AND occurred_at>=$2::timestamptz AND occurred_at<$4 GROUP BY tenant_id,metric ON CONFLICT(tenant_id,period_start,metric) DO UPDATE SET quantity=EXCLUDED.quantity,reconciled_at=EXCLUDED.reconciled_at`, tenantID, start, r.now().UTC(), end)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT metric,quantity FROM saas_usage_aggregates WHERE tenant_id=$1 AND period_start=$2::timestamptz::date`, tenantID, start)
	if err != nil {
		return nil, err
	}
	result := map[string]int64{}
	for rows.Next() {
		var metric string
		var quantity int64
		if err := rows.Scan(&metric, &quantity); err != nil {
			rows.Close()
			return nil, err
		}
		result[metric] = quantity
	}
	rows.Close()
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
func (r *Repository) begin(ctx context.Context, tenantID string) (pgx.Tx, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
