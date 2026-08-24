package operations

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/credential"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/modelgateway"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/security"
)

func TestCredentialLeakDetectionAndRevocation(t *testing.T) {
	connectionURL := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if connectionURL == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := saaspostgres.Open(ctx, connectionURL)
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

	now := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
	account, err := control.NewPostgresStore(pool).ProvisionPersonalAccount(ctx, control.ProvisionCommand{
		AccountID: uuid.NewString(), TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(),
		ExternalSubject: "synthetic|credential-game-day", VerifiedEmail: "credential-game-day@example.test",
		RequestID: uuid.NewString(), OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := auth.RequestContext{
		AccountID: account.AccountID, SubjectID: "synthetic|credential-game-day", TenantID: account.TenantID, Role: "owner",
		Capabilities: map[string]struct{}{"credential:manage": {}, "memory:read": {}},
		RequestID:    uuid.NewString(), TraceID: uuid.NewString(),
	}
	authenticated := auth.WithRequestContext(ctx, request)
	credentialService := credential.NewService(credential.NewPostgresRepository(pool), func() time.Time { return now })
	issued, err := credentialService.Create(authenticated, "game-day", []string{"memory:read"}, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := credentialService.Verify(ctx, issued.Secret); err != nil {
		t.Fatal("newly issued credential did not verify")
	}

	auditService := audit.NewService(pool, func() time.Time { return now })
	credentialRequest := request
	credentialRequest.CredentialID = issued.Credential.ID
	for index := 0; index < 3; index++ {
		credentialRequest.RequestID = uuid.NewString()
		credentialRequest.TraceID = uuid.NewString()
		if err := auditService.Record(ctx, credentialRequest, "api", "memory.search", "denied", "", "", "policy_denied", map[string]any{"attempt": index + 1}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := audit.NewPostgresRepository(pool).Search(ctx, account.TenantID, audit.Filter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	findings := security.Evaluate(events, now.Add(time.Second))
	credentialFindings := make([]security.Finding, 0, 1)
	for _, finding := range findings {
		if finding.RuleID == "credential_abuse" {
			credentialFindings = append(credentialFindings, finding)
		}
	}
	if len(credentialFindings) != 1 {
		t.Fatalf("credential finding count=%d, want 1", len(credentialFindings))
	}
	securityRepository := security.NewPostgresRepository(pool)
	created, err := securityRepository.StoreFindings(ctx, account.TenantID, credentialFindings, now.Add(time.Second))
	if err != nil || created != 1 {
		t.Fatalf("stored finding count=%d err=%v", created, err)
	}
	var findingID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM saas_security_findings WHERE tenant_id=$1 AND rule_id='credential_abuse'`, account.TenantID).Scan(&findingID); err != nil {
		t.Fatal(err)
	}
	if err := securityRepository.PutPolicy(ctx, account.TenantID, "security-admin", security.Policy{
		Action: security.CredentialRevoke, Enabled: true, MinimumSeverity: security.Critical,
		ApprovalRequired: true, Version: "credential-game-day-v1",
	}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	containment := security.ContainmentRequest{
		TenantID: account.TenantID, FindingID: findingID, Action: security.CredentialRevoke,
		TargetType: "credential", TargetID: issued.Credential.ID, RequestedBy: "incident-commander",
		ReasonCode: "credential_compromise",
	}
	if _, err := securityRepository.Contain(ctx, containment, now.Add(3*time.Second)); !errors.Is(err, security.ErrApprovalRequired) {
		t.Fatalf("unapproved containment error=%v", err)
	}
	containment.ApprovedBy = "security-approver"
	if _, err := securityRepository.Contain(ctx, containment, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := credentialService.Verify(ctx, issued.Secret); !errors.Is(err, credential.ErrInvalidCredential) {
		t.Fatal("revoked credential remained valid")
	}
	var containmentAudits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM saas_audit_events WHERE tenant_id=$1 AND operation='security.containment.execute'`, account.TenantID).Scan(&containmentAudits); err != nil || containmentAudits != 1 {
		t.Fatalf("containment audit count=%d err=%v", containmentAudits, err)
	}
	t.Log("credential_leak denied_events=3 findings=1 independent_approval=1 credential_revoked=1 post_revoke_denied=1 audit_events=1")
}

func TestModelProviderOutageFailsSafeWithEvidence(t *testing.T) {
	const upstreamSecret = "upstream-body-must-not-escape"
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		response.WriteHeader(http.StatusServiceUnavailable)
		_, _ = response.Write([]byte(upstreamSecret))
	}))
	defer server.Close()
	provider, err := modelgateway.NewHTTPProvider(modelgateway.HTTPProviderConfig{
		Name: "outage-provider", Endpoint: server.URL, APIKey: "synthetic-provider-key",
		Model: "outage-model-v1", Dimension: 3, Retention: "zero-retention",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
	usage := &usageRecorder{}
	gateway, err := modelgateway.New(modelgateway.Config{
		Providers: []modelgateway.Provider{provider},
		Policies: []modelgateway.ProviderPolicy{{
			Provider: "outage-provider", Models: []string{"outage-model-v1"}, RetentionPolicies: []string{"zero-retention"},
			MaxInputTokens: 100, Timeout: time.Second, MaxRetries: 1, FailureThreshold: 1, Cooldown: time.Minute,
		}}}, usage, identityRedactor{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	evidence := []modelgateway.Evidence{{SourceID: "synthetic-source", PassageID: "synthetic-passage", Text: "sensitive evidence text"}}
	request := modelgateway.GenerateRequest{
		TenantID: "synthetic-tenant", Provider: "outage-provider", Model: "outage-model-v1",
		Prompt: "sensitive prompt text", Evidence: evidence,
	}
	first, err := gateway.Generate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Generated || first.Text != "" || first.FailureCode != "generation_unavailable" || len(first.Evidence) != 1 || first.Evidence[0].PassageID != evidence[0].PassageID {
		t.Fatal("provider outage did not return the evidence-only fallback")
	}
	if attempts.Load() != 2 {
		t.Fatalf("upstream attempt count=%d, want 2", attempts.Load())
	}
	second, err := gateway.Generate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Generated || second.Text != "" || second.FailureCode != "generation_unavailable" || len(second.Evidence) != 1 {
		t.Fatal("open circuit did not preserve the evidence-only fallback")
	}
	if attempts.Load() != 2 {
		t.Fatal("open circuit made another upstream call")
	}
	if usage.count() != 1 || usage.lastOutcome() != "failed" {
		t.Fatalf("usage records=%d outcome=%q", usage.count(), usage.lastOutcome())
	}
	t.Log("model_provider_outage upstream_attempts=2 circuit_open=1 evidence_preserved=1 fabricated_generation=0")
}

type identityRedactor struct{}

func (identityRedactor) Redact(value string) string { return value }

type usageRecorder struct {
	mu     sync.Mutex
	values []modelgateway.Usage
}

func (r *usageRecorder) RecordUsage(_ context.Context, usage modelgateway.Usage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values = append(r.values, usage)
	return nil
}

func (r *usageRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.values)
}

func (r *usageRecorder) lastOutcome() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.values) == 0 {
		return ""
	}
	return r.values[len(r.values)-1].Outcome
}
