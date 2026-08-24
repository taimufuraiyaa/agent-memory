package security

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestContainmentRequiresPolicyApprovalAndSupportsFalsePositiveReview(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if url == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := saaspostgres.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := saaspostgres.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "TRUNCATE saas_accounts CASCADE"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 4, 0, 0, 0, time.UTC)
	accountID, tenantID := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO saas_accounts(id,external_subject,verified_email,state,created_at,updated_at) VALUES($1,$2,$3,'active',$4,$4)`, accountID, accountID, accountID+"@example.test", now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_tenants(id,kind,state,personal_owner_account_id,created_at,updated_at) VALUES($1,'personal','active',$2,$3,$3)`, tenantID, accountID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_tenant_entitlements(tenant_id,updated_at) VALUES($1,$2)`, tenantID, now); err != nil {
		t.Fatal(err)
	}
	if allowed, err := NewGate(pool).Allow(ctx, tenantID, now); err != nil || !allowed {
		t.Fatalf("initial request gate allowed=%v err=%v", allowed, err)
	}
	repository := NewPostgresRepository(pool)
	finding := Finding{TenantID: tenantID, RuleID: "cross_tenant_authorization", Severity: Critical, SummaryCode: "cross_tenant_attempt", Evidence: []EvidenceRef{{EventID: uuid.NewString(), ReasonCode: "cross_tenant", OccurredAt: now}}, FirstObservedAt: now, LastObservedAt: now}
	created, err := repository.StoreFindings(ctx, tenantID, []Finding{finding}, now)
	if err != nil || created != 1 {
		t.Fatalf("StoreFindings created=%d err=%v", created, err)
	}
	var findingID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM saas_security_findings WHERE tenant_id=$1`, tenantID).Scan(&findingID); err != nil {
		t.Fatal(err)
	}
	request := ContainmentRequest{TenantID: tenantID, FindingID: findingID, Action: RateLimit, TargetType: "tenant", TargetID: tenantID, RequestedBy: "operator-a", ReasonCode: "credential_abuse", Duration: time.Hour}
	if _, err := repository.Contain(ctx, request, now); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("contain without policy err=%v", err)
	}
	if err := repository.PutPolicy(ctx, tenantID, "security-admin", Policy{Action: RateLimit, Enabled: true, MinimumSeverity: High, ApprovalRequired: true, Version: "contain-v1"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Contain(ctx, request, now); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("contain without approval err=%v", err)
	}
	request.ApprovedBy = "operator-b"
	if _, err := repository.Contain(ctx, request, now); err != nil {
		t.Fatal(err)
	}
	var until time.Time
	if err := pool.QueryRow(ctx, `SELECT rate_limited_until FROM saas_tenant_security_controls WHERE tenant_id=$1`, tenantID).Scan(&until); err != nil || !until.Equal(now.Add(time.Hour)) {
		t.Fatalf("rate limit until=%v err=%v", until, err)
	}

	second := finding
	second.FirstObservedAt = now.Add(time.Minute)
	second.LastObservedAt = second.FirstObservedAt
	if created, err := repository.StoreFindings(ctx, tenantID, []Finding{second}, now.Add(time.Minute)); err != nil || created != 1 {
		t.Fatalf("second finding created=%d err=%v", created, err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM saas_security_findings WHERE tenant_id=$1 AND state='open' ORDER BY first_observed_at DESC LIMIT 1`, tenantID).Scan(&findingID); err != nil {
		t.Fatal(err)
	}
	if err := repository.ReviewFinding(ctx, tenantID, findingID, "operator-c", "false_positive", "known_pen_test", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM saas_audit_events WHERE tenant_id=$1 AND service='operator'`, tenantID).Scan(&audits); err != nil || audits < 4 {
		t.Fatalf("operator audit count=%d err=%v", audits, err)
	}
}
