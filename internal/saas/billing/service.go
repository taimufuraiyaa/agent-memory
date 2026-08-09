package billing

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type MetricView struct {
	Metric   string `json:"metric"`
	Used     int64  `json:"used"`
	Limit    int64  `json:"limit"`
	Forecast int64  `json:"forecast"`
}
type InvoiceView struct {
	ID              string    `json:"id"`
	State           string    `json:"state"`
	Currency        string    `json:"currency"`
	HostedURL       string    `json:"hosted_url"`
	AmountDueMicros int64     `json:"amount_due_micros"`
	IssuedAt        time.Time `json:"issued_at"`
}
type BillingOverview struct {
	PlanID              string        `json:"plan_id"`
	PlanName            string        `json:"plan_name"`
	SubscriptionState   string        `json:"subscription_state"`
	MeteringDisclosure  string        `json:"metering_disclosure"`
	CurrentPeriodEndsAt *time.Time    `json:"current_period_ends_at,omitempty"`
	Metrics             []MetricView  `json:"metrics"`
	Invoices            []InvoiceView `json:"invoices"`
	RecoveryActions     []string      `json:"recovery_actions"`
}
type Service struct{ repository *Repository }

func NewService(repository *Repository) *Service { return &Service{repository: repository} }
func (s *Service) Overview(ctx context.Context) (BillingOverview, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("account:manage") || s == nil || s.repository == nil {
		return BillingOverview{}, errors.New("billing overview unavailable")
	}
	tx, err := s.repository.begin(ctx, request.TenantID)
	if err != nil {
		return BillingOverview{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var result BillingOverview
	var periodEnd *time.Time
	err = tx.QueryRow(ctx, `SELECT s.plan_id,p.name,s.state,s.current_period_ends_at FROM saas_subscriptions s JOIN saas_plans p ON p.id=s.plan_id WHERE s.tenant_id=$1`, request.TenantID).Scan(&result.PlanID, &result.PlanName, &result.SubscriptionState, &periodEnd)
	if err != nil {
		return result, err
	}
	result.CurrentPeriodEndsAt = periodEnd
	result.MeteringDisclosure = "Usage can be delayed by up to five minutes while authoritative systems reconcile."
	result.Metrics = []MetricView{}
	result.Invoices = []InvoiceView{}
	result.RecoveryActions = []string{"retry_after_window", "delete_unused_sources", "request_plan_upgrade"}
	entitlements, err := s.repository.Entitlements(ctx, request.TenantID)
	if err != nil {
		return result, err
	}
	limits := map[string]int64{"storage_bytes": entitlements.MaxStorageBytes, "generation_tokens": entitlements.MaxTokensPerMonth, "embedding_tokens": entitlements.MaxTokensPerMonth}
	start := time.Date(time.Now().UTC().Year(), time.Now().UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	rows, err := tx.Query(ctx, `SELECT metric,quantity FROM saas_usage_aggregates WHERE tenant_id=$1 AND period_start=$2::date ORDER BY metric`, request.TenantID, start)
	if err != nil {
		return result, err
	}
	elapsed := math.Max(1, float64(time.Now().UTC().Day()))
	days := float64(start.AddDate(0, 1, 0).Sub(start) / (24 * time.Hour))
	for rows.Next() {
		var metric string
		var used int64
		if err := rows.Scan(&metric, &used); err != nil {
			rows.Close()
			return result, err
		}
		result.Metrics = append(result.Metrics, MetricView{Metric: metric, Used: used, Limit: limits[metric], Forecast: int64(math.Ceil(float64(used) * days / elapsed))})
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT id::text,state,currency,hosted_url,amount_due_micros,issued_at FROM saas_invoices WHERE tenant_id=$1 ORDER BY issued_at DESC LIMIT 24`, request.TenantID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var v InvoiceView
		if err := rows.Scan(&v.ID, &v.State, &v.Currency, &v.HostedURL, &v.AmountDueMicros, &v.IssuedAt); err != nil {
			rows.Close()
			return result, err
		}
		result.Invoices = append(result.Invoices, v)
	}
	rows.Close()
	return result, nil
}

func (s *Service) RequestPlanChange(ctx context.Context, action, planID, idempotencyKey string) (string, bool, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("account:manage") || idempotencyKey == "" || (action != "upgrade" && action != "cancel") {
		return "", false, errors.New("plan change unavailable")
	}
	if action == "upgrade" && planID != "individual" {
		return "", false, errors.New("unknown upgrade plan")
	}
	if action == "cancel" {
		planID = "trial"
	}
	tx, err := s.repository.begin(ctx, request.TenantID)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var existing string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM saas_plan_change_requests WHERE tenant_id=$1 AND idempotency_key=$2`, request.TenantID, idempotencyKey).Scan(&existing); err == nil {
		return existing, true, nil
	}
	id := uuid.NewString()
	at := s.repository.now().UTC()
	_, err = tx.Exec(ctx, `INSERT INTO saas_plan_change_requests(tenant_id,id,account_id,action,requested_plan_id,state,idempotency_key,requested_at,updated_at) VALUES($1,$2,$3,$4,$5,'queued',$6,$7,$7)`, request.TenantID, id, request.AccountID, action, planID, idempotencyKey, at)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO saas_outbox(tenant_id,id,event_type,spec_version,aggregate_type,aggregate_id,payload,occurred_at,next_attempt_at) VALUES($1,$2,'billing.plan_change_requested','1.0','plan_change',$3,jsonb_build_object('action',$4,'plan_id',$5),$6,$6)`, request.TenantID, uuid.NewString(), id, action, planID, at)
	}
	if err == nil {
		err = audit.Append(ctx, tx, audit.Event{TenantID: request.TenantID, OccurredAt: at, ActorType: "member", ActorID: request.AccountID, CredentialRef: request.CredentialID, SessionRef: request.SessionID, Service: "billing", Operation: "billing.plan_change.request", Outcome: "success", RequestID: request.RequestID, TraceID: request.TraceID, TargetType: "plan_change", TargetID: id, PolicyVersion: "billing-v1", ReasonCode: action, SafeMetadata: map[string]any{"plan_id": planID}})
	}
	if err != nil {
		return "", false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", false, err
	}
	return id, false, nil
}
