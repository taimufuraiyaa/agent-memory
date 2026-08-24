package readiness

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestReadinessOwnsFailuresRejectsContentAndRecordsDeterministicDrills(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if url == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx := context.Background()
	pool, err := saaspostgres.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := saaspostgres.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE saas_accounts CASCADE; TRUNCATE saas_failure_ownership,saas_game_day_drills,saas_release_evidence`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	service := NewService(pool, func() time.Time { return now })
	for _, failureClass := range RequiredFailureClasses {
		if err := service.AssignFailure(ctx, failureClass, "oncall-"+failureClass, time.Hour, "page-primary-then-incident-commander"); err != nil {
			t.Fatal(err)
		}
	}
	missing, err := service.MissingFailureOwners(ctx)
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing owners=%v err=%v", missing, err)
	}
	account, err := control.NewSignupService(control.NewPostgresStore(pool), func() time.Time { return now }).Signup(ctx, control.VerifiedIdentity{ExternalSubject: "readiness-subject", Email: "readiness@example.test", EmailVerified: true})
	if err != nil {
		t.Fatal(err)
	}
	authenticated := auth.WithRequestContext(ctx, auth.RequestContext{AccountID: account.AccountID, SubjectID: account.AccountID, TenantID: account.TenantID, Role: "owner", Capabilities: map[string]struct{}{"account:manage": {}}, RequestID: "request", TraceID: "trace"})
	if err := service.RecordAnalytics(authenticated, AnalyticsEvent{Name: "source_ready", Outcome: "success", Dimensions: map[string]any{"format": "pdf", "duration_band": "under_60s"}}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordAnalytics(authenticated, AnalyticsEvent{Name: "source_ready", Outcome: "success", Dimensions: map[string]any{"content": "book text"}}); err == nil {
		t.Fatal("expected content-bearing analytics to be rejected")
	}
	scorecard, err := service.Scorecard(authenticated, now.Add(-time.Hour))
	if err != nil || scorecard.Funnel["source_ready"] != 1 || scorecard.GeneratedAt != now {
		t.Fatalf("scorecard=%+v err=%v", scorecard, err)
	}
	result, err := service.RunDrill(ctx, Drill{Scenario: "credential_leak", Owner: "security-oncall", Checks: []func(context.Context) error{func(context.Context) error { return nil }}, Summary: map[string]any{"credential_revoked": true, "containment_seconds": 30}})
	if err != nil || result.Outcome != "passed" || len(result.EvidenceSHA256) != 64 {
		t.Fatalf("drill=%+v err=%v", result, err)
	}
	result, err = service.RunDrill(ctx, Drill{Scenario: "incomplete_deletion", Owner: "privacy-oncall", Checks: []func(context.Context) error{func(context.Context) error { return errors.New("projection pending") }}, Summary: map[string]any{"pending_subsystems": 1}})
	if err == nil || result.Outcome != "failed" {
		t.Fatalf("failed drill=%+v err=%v", result, err)
	}
}

func TestEvidenceGateRequiresEveryMetricToPassForTheWholeWindow(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if url == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx := context.Background()
	pool, err := saaspostgres.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := saaspostgres.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE saas_release_evidence`); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, nil)
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(30 * 24 * time.Hour)
	values := []Evidence{
		{Gate: "ga", Metric: "api_availability", Value: 99.95, Threshold: 99.9, Comparator: "gte", Owner: "sre", SourceRef: "dashboard:api", ObservedAt: end, WindowStart: start, WindowEnd: end},
		{Gate: "ga", Metric: "critical_findings", Value: 0, Threshold: 0, Comparator: "eq", Owner: "security", SourceRef: "report:security", ObservedAt: end, WindowStart: start, WindowEnd: end},
	}
	for _, value := range values {
		if err := service.RecordEvidence(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
	report, err := service.Evaluate(ctx, "ga", []string{"api_availability", "critical_findings", "deletion_slo"}, 28*24*time.Hour)
	if err != nil || report.Ready || len(report.MissingMetrics) != 1 || report.MissingMetrics[0] != "deletion_slo" {
		t.Fatalf("incomplete report=%+v err=%v", report, err)
	}
	if err := service.RecordEvidence(ctx, Evidence{Gate: "ga", Metric: "deletion_slo", Value: 100, Threshold: 99, Comparator: "gte", Owner: "privacy", SourceRef: "dashboard:deletion", ObservedAt: end, WindowStart: start, WindowEnd: end}); err != nil {
		t.Fatal(err)
	}
	report, err = service.Evaluate(ctx, "ga", []string{"api_availability", "critical_findings", "deletion_slo"}, 28*24*time.Hour)
	if err != nil || !report.Ready || !report.SharedWindowValid {
		t.Fatalf("ready report=%+v err=%v", report, err)
	}
}

func TestEvidenceGateRequiresOneSharedObservationWindow(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if url == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx := context.Background()
	pool, err := saaspostgres.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := saaspostgres.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE saas_release_evidence`); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, nil)
	firstStart := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	secondStart := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for _, value := range []Evidence{
		{Gate: "ga", Metric: "api_availability", Value: 100, Threshold: 99.9, Comparator: "gte", Owner: "sre", SourceRef: "dashboard:api", ObservedAt: firstStart.Add(28 * 24 * time.Hour), WindowStart: firstStart, WindowEnd: firstStart.Add(28 * 24 * time.Hour)},
		{Gate: "ga", Metric: "deletion_slo", Value: 100, Threshold: 99, Comparator: "gte", Owner: "privacy", SourceRef: "dashboard:deletion", ObservedAt: secondStart.Add(28 * 24 * time.Hour), WindowStart: secondStart, WindowEnd: secondStart.Add(28 * 24 * time.Hour)},
	} {
		if err := service.RecordEvidence(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
	report, err := service.Evaluate(ctx, "ga", []string{"api_availability", "deletion_slo"}, 28*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready || report.SharedWindowValid {
		t.Fatalf("disjoint metric windows must not satisfy one release window: %+v", report)
	}
}

func TestRecordEvidenceRejectsObservationOutsideWindow(t *testing.T) {
	service := NewService(nil, nil)
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	value := Evidence{Gate: "ga", Metric: "api_availability", Value: 100, Threshold: 99.9, Comparator: "gte", Owner: "sre", SourceRef: "dashboard:api", ObservedAt: start.Add(25 * time.Hour), WindowStart: start, WindowEnd: start.Add(24 * time.Hour)}
	if err := service.RecordEvidence(context.Background(), value); err == nil {
		t.Fatal("observation after its evidence window must be rejected before database access")
	}
}
