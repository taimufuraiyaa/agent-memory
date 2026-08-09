// Package readiness records content-free rollout, drill, ownership, and GA evidence.
package readiness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

var allowedAnalytics = map[string]struct{}{
	"signup_completed": {}, "attestation_completed": {}, "upload_accepted": {}, "source_ready": {},
	"query_completed": {}, "memory_accepted": {}, "export_completed": {}, "deletion_completed": {},
}

var RequiredFailureClasses = []string{
	"authentication", "database", "queue", "object_storage", "model_provider", "source_processing",
	"retrieval", "billing", "deletion", "security_alert", "notice", "support_escalation",
}

var RequiredGameDays = []string{
	"database_failover", "queue_backlog", "model_provider_outage", "credential_leak",
	"cross_tenant_attempt", "incomplete_deletion", "database_restore", "source_deletion",
	"account_deletion", "rights_notice",
}

var GateMetrics = map[string][]string{
	"private_beta": {"critical_findings", "tenant_isolation", "restore_rto_minutes", "deletion_drill", "cost_per_active_tenant"},
	"public_beta":  {"api_availability", "search_p95_ms", "write_p95_ms", "critical_findings", "tenant_isolation", "deletion_slo", "billing_reconciliation", "support_response_slo"},
	"ga":           {"api_availability", "search_p95_ms", "write_p95_ms", "critical_findings", "tenant_isolation", "deletion_slo", "audit_integrity", "billing_reconciliation", "restore_rpo_minutes", "restore_rto_minutes", "cost_per_active_tenant", "support_response_slo"},
}

type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewService(pool *pgxpool.Pool, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, now: now}
}

type AnalyticsEvent struct {
	Name       string
	Outcome    string
	Dimensions map[string]any
}

func (s *Service) RecordAnalytics(ctx context.Context, event AnalyticsEvent) error {
	request, ok := auth.FromContext(ctx)
	if !ok || s == nil || s.pool == nil {
		return errors.New("analytics context is unavailable")
	}
	if _, ok := allowedAnalytics[event.Name]; !ok || (event.Outcome != "success" && event.Outcome != "failure") {
		return errors.New("analytics event is not allowlisted")
	}
	if err := audit.ValidateMetadata(event.Dimensions); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", request.TenantID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO saas_product_analytics(tenant_id,id,event_name,outcome,safe_dimensions,occurred_at) VALUES($1,$2,$3,$4,$5,$6)`, request.TenantID, uuid.NewString(), event.Name, event.Outcome, event.Dimensions, s.now().UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) AssignFailure(ctx context.Context, failureClass, owner string, target time.Duration, escalation string) error {
	failureClass, owner, escalation = strings.TrimSpace(failureClass), strings.TrimSpace(owner), strings.TrimSpace(escalation)
	if !contains(RequiredFailureClasses, failureClass) || owner == "" || target <= 0 || escalation == "" {
		return errors.New("invalid failure ownership")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO saas_failure_ownership(failure_class,owner,resolution_target_seconds,escalation_policy,updated_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(failure_class) DO UPDATE SET owner=EXCLUDED.owner,resolution_target_seconds=EXCLUDED.resolution_target_seconds,escalation_policy=EXCLUDED.escalation_policy,updated_at=EXCLUDED.updated_at`, failureClass, owner, int64(target.Seconds()), escalation, s.now().UTC())
	return err
}

func (s *Service) MissingFailureOwners(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT failure_class FROM saas_failure_ownership`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	present := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		present[name] = true
	}
	missing := []string{}
	for _, name := range RequiredFailureClasses {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	return missing, rows.Err()
}

func (s *Service) MissingGameDays(ctx context.Context, since time.Time) ([]string, error) {
	return s.MissingRequiredGameDays(ctx, RequiredGameDays, since, s.now().UTC())
}

type Drill struct {
	Scenario string
	Owner    string
	Checks   []func(context.Context) error
	Summary  map[string]any
}

type DrillResult struct {
	ID, Scenario, Outcome, EvidenceSHA256 string
	StartedAt, CompletedAt                time.Time
}

func (s *Service) RunDrill(ctx context.Context, drill Drill) (DrillResult, error) {
	if !contains(RequiredGameDays, drill.Scenario) || strings.TrimSpace(drill.Owner) == "" || len(drill.Checks) == 0 {
		return DrillResult{}, errors.New("invalid game-day drill")
	}
	if err := audit.ValidateMetadata(drill.Summary); err != nil {
		return DrillResult{}, err
	}
	started := s.now().UTC()
	outcome := "passed"
	failedCheck := -1
	for index, check := range drill.Checks {
		if check == nil || check(ctx) != nil {
			outcome, failedCheck = "failed", index
			break
		}
	}
	completed := s.now().UTC()
	material, _ := json.Marshal(map[string]any{"scenario": drill.Scenario, "owner": drill.Owner, "outcome": outcome, "failed_check": failedCheck, "started_at": started, "completed_at": completed, "summary": drill.Summary})
	hash := sha256.Sum256(material)
	result := DrillResult{ID: uuid.NewString(), Scenario: drill.Scenario, Outcome: outcome, EvidenceSHA256: hex.EncodeToString(hash[:]), StartedAt: started, CompletedAt: completed}
	_, err := s.pool.Exec(ctx, `INSERT INTO saas_game_day_drills(id,scenario,owner,outcome,started_at,completed_at,evidence_sha256,safe_summary) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, result.ID, result.Scenario, strings.TrimSpace(drill.Owner), result.Outcome, started, completed, result.EvidenceSHA256, drill.Summary)
	if err != nil {
		return DrillResult{}, err
	}
	if outcome == "failed" {
		return result, fmt.Errorf("game-day drill failed at check %d", failedCheck)
	}
	return result, nil
}

type Evidence struct {
	Gate, Metric, Comparator, Owner, SourceRef string
	Value, Threshold                           float64
	ObservedAt, WindowStart, WindowEnd         time.Time
}

func (s *Service) RecordEvidence(ctx context.Context, value Evidence) error {
	if strings.TrimSpace(value.Gate) == "" || strings.TrimSpace(value.Metric) == "" || strings.TrimSpace(value.Owner) == "" || strings.TrimSpace(value.SourceRef) == "" || !value.WindowEnd.After(value.WindowStart) || value.ObservedAt.Before(value.WindowStart) || value.ObservedAt.After(value.WindowEnd) {
		return errors.New("invalid release evidence")
	}
	if value.Comparator != "lte" && value.Comparator != "gte" && value.Comparator != "eq" {
		return errors.New("invalid evidence comparator")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO saas_release_evidence(id,gate,metric,value,threshold,comparator,owner,observed_at,window_start,window_end,source_ref) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(gate,metric,window_start,window_end) DO UPDATE SET value=EXCLUDED.value,threshold=EXCLUDED.threshold,comparator=EXCLUDED.comparator,owner=EXCLUDED.owner,observed_at=EXCLUDED.observed_at,source_ref=EXCLUDED.source_ref`, uuid.NewString(), value.Gate, value.Metric, value.Value, value.Threshold, value.Comparator, value.Owner, value.ObservedAt, value.WindowStart, value.WindowEnd, value.SourceRef)
	return err
}

type GateReport struct {
	Gate              string          `json:"gate"`
	Ready             bool            `json:"ready"`
	SharedWindowValid bool            `json:"shared_window_valid"`
	WindowStart       time.Time       `json:"window_start"`
	WindowEnd         time.Time       `json:"window_end"`
	MissingMetrics    []string        `json:"missing_metrics"`
	FailedMetrics     []string        `json:"failed_metrics"`
	Evidence          map[string]bool `json:"evidence"`
}

func (s *Service) Evaluate(ctx context.Context, gate string, required []string, minimumWindow time.Duration) (GateReport, error) {
	report := GateReport{Gate: gate, MissingMetrics: []string{}, FailedMetrics: []string{}, Evidence: map[string]bool{}}
	rows, err := s.pool.Query(ctx, `SELECT metric,value,threshold,comparator,window_start,window_end FROM saas_release_evidence WHERE gate=$1 ORDER BY window_end DESC,metric`, gate)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	type sample struct {
		value, threshold float64
		comparator       string
		start, end       time.Time
	}
	latest := map[string]sample{}
	for rows.Next() {
		var metric string
		var value sample
		if err := rows.Scan(&metric, &value.value, &value.threshold, &value.comparator, &value.start, &value.end); err != nil {
			return report, err
		}
		if _, exists := latest[metric]; !exists {
			latest[metric] = value
		}
	}
	for _, metric := range required {
		value, ok := latest[metric]
		if !ok {
			report.MissingMetrics = append(report.MissingMetrics, metric)
			continue
		}
		passed := compare(value.value, value.threshold, value.comparator) && value.end.Sub(value.start) >= minimumWindow
		report.Evidence[metric] = passed
		if !passed {
			report.FailedMetrics = append(report.FailedMetrics, metric)
		}
		if report.WindowStart.IsZero() || value.start.After(report.WindowStart) {
			report.WindowStart = value.start
		}
		if report.WindowEnd.IsZero() || value.end.Before(report.WindowEnd) {
			report.WindowEnd = value.end
		}
	}
	sort.Strings(report.MissingMetrics)
	sort.Strings(report.FailedMetrics)
	report.SharedWindowValid = !report.WindowStart.IsZero() && !report.WindowEnd.IsZero() && report.WindowEnd.Sub(report.WindowStart) >= minimumWindow
	report.Ready = len(required) > 0 && len(report.MissingMetrics) == 0 && len(report.FailedMetrics) == 0 && report.SharedWindowValid
	return report, rows.Err()
}

func compare(value, threshold float64, comparator string) bool {
	switch comparator {
	case "lte":
		return value <= threshold
	case "gte":
		return value >= threshold
	case "eq":
		return value == threshold
	default:
		return false
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
