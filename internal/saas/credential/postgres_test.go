package credential

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestPostgresCredentialLifecycleScopesExpiryRotationAndRevocation(t *testing.T) {
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
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	account, err := control.NewPostgresStore(pool).ProvisionPersonalAccount(ctx, control.ProvisionCommand{
		AccountID: uuid.NewString(), TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), ExternalSubject: "provider|credential",
		VerifiedEmail: "credential@example.test", RequestID: uuid.NewString(), OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := auth.RequestContext{AccountID: account.AccountID, SubjectID: "provider|credential", TenantID: account.TenantID, Role: "owner",
		Capabilities: map[string]struct{}{"credential:manage": {}, "memory:read": {}, "memory:write": {}}, RequestID: uuid.NewString(), TraceID: uuid.NewString()}
	authenticated := auth.WithRequestContext(ctx, request)
	service := NewService(NewPostgresRepository(pool), func() time.Time { return now })
	if _, err := service.Create(authenticated, "too broad", []string{"tenant:export"}, now.Add(time.Hour)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("scope escalation error=%v, want ErrForbidden", err)
	}
	issued, err := service.Create(authenticated, "agent one", []string{"memory:read", "memory:write"}, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if issued.Secret == "" {
		t.Fatal("secret was not returned at creation")
	}
	identity, membership, err := service.Verify(ctx, issued.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if identity.CredentialID != issued.Credential.ID || membership.TenantID != account.TenantID || len(membership.Capabilities) != 2 {
		t.Fatalf("verified identity/membership=%+v %+v", identity, membership)
	}
	rotated, err := service.Rotate(authenticated, issued.Credential.ID, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Verify(ctx, issued.Secret); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("old secret after rotation error=%v", err)
	}
	if _, _, err := service.Verify(ctx, rotated.Secret); err != nil {
		t.Fatalf("rotated secret error=%v", err)
	}
	if err := service.Revoke(authenticated, rotated.Credential.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Verify(ctx, rotated.Secret); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("revoked secret error=%v", err)
	}

	expiring, err := service.Create(authenticated, "short", []string{"memory:read"}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	expiredService := NewService(NewPostgresRepository(pool), func() time.Time { return now.Add(2 * time.Minute) })
	if _, _, err := expiredService.Verify(ctx, expiring.Secret); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("expired secret error=%v", err)
	}

	listed, err := service.List(authenticated)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range listed {
		if strings.Contains(value.Label, issued.Secret) {
			t.Fatal("list exposed secret material")
		}
	}
}
