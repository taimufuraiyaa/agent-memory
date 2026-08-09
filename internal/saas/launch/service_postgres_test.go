package launch

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestInvitationAdmissionEnforcesAgeGeographyCapsAndStoresNoRawIdentity(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `TRUNCATE saas_accounts CASCADE; TRUNCATE saas_signup_attempts,saas_signup_reservations,saas_launch_invitations,saas_launch_policy_history; UPDATE saas_launch_policy SET phase='private_beta',signup_enabled=true,invitation_required=true,allowed_countries=ARRAY['VN'],account_cap=1,signup_rate_per_hour=10,abuse_rejection_limit=5,updated_at=clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	service := NewService(pool, func() time.Time { return now })
	signup := control.NewSignupServiceWithAdmission(control.NewPostgresStore(pool), service, func() time.Time { return now })
	identity := control.VerifiedIdentity{ExternalSubject: "private-subject", Email: "owner@example.test", EmailVerified: true}

	if _, err := signup.SignupWithContext(ctx, identity, control.SignupContext{Country: "VN", NetworkAddress: "127.0.0.1:2000"}); !errors.Is(err, ErrAgeRestricted) {
		t.Fatalf("missing age confirmation err=%v", err)
	}
	token, err := service.CreateInvitation(ctx, identity.Email, "launch-owner", 1, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signup.SignupWithContext(ctx, identity, control.SignupContext{InvitationToken: token, AgeConfirmed: true, Country: "US", CountryVerified: true, NetworkAddress: "127.0.0.1:2001"}); !errors.Is(err, ErrGeographyBlocked) {
		t.Fatalf("blocked geography err=%v", err)
	}
	account, err := signup.SignupWithContext(ctx, identity, control.SignupContext{InvitationToken: token, AgeConfirmed: true, Country: "vn", CountryVerified: true, NetworkAddress: "127.0.0.1:2002"})
	if err != nil {
		t.Fatal(err)
	}
	var sourceCap int
	var trial time.Time
	if err := tenantQuery(ctx, pool, account.TenantID, `SELECT source_cap,trial_expires_at FROM saas_tenant_launch_controls WHERE tenant_id=$1`, account.TenantID).Scan(&sourceCap, &trial); err != nil || sourceCap != 5 || !trial.Equal(now.Add(30*24*time.Hour)) {
		t.Fatalf("launch controls cap=%d trial=%s err=%v", sourceCap, trial, err)
	}
	var rawLeaks int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM saas_signup_attempts WHERE email_sha256 IN ('owner@example.test','private-subject') OR network_sha256 LIKE '%127.0.0.1%'`).Scan(&rawLeaks); err != nil || rawLeaks != 0 {
		t.Fatalf("raw signup identity leaked rows=%d err=%v", rawLeaks, err)
	}
	second := control.VerifiedIdentity{ExternalSubject: "second", Email: "second@example.test", EmailVerified: true}
	secondToken, err := service.CreateInvitation(ctx, second.Email, "launch-owner", 1, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signup.SignupWithContext(ctx, second, control.SignupContext{InvitationToken: secondToken, AgeConfirmed: true, Country: "VN", CountryVerified: true, NetworkAddress: "127.0.0.2"}); !errors.Is(err, ErrAccountCapReached) {
		t.Fatalf("account cap err=%v", err)
	}
}

func TestPublicSignupRateLimitAndTenantFeatureFlag(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `TRUNCATE saas_accounts CASCADE; TRUNCATE saas_signup_attempts,saas_signup_reservations,saas_launch_invitations,saas_launch_policy_history; UPDATE saas_launch_policy SET phase='public_beta',signup_enabled=true,invitation_required=false,allowed_countries=ARRAY['VN'],account_cap=100,signup_rate_per_hour=1,abuse_rejection_limit=5,updated_at=clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	service := NewService(pool, func() time.Time { return now })
	signup := control.NewSignupServiceWithAdmission(control.NewPostgresStore(pool), service, func() time.Time { return now })
	account, err := signup.SignupWithContext(ctx, control.VerifiedIdentity{ExternalSubject: "public-one", Email: "one@example.test", EmailVerified: true}, control.SignupContext{AgeConfirmed: true, Country: "VN", CountryVerified: true, NetworkAddress: "10.0.0.1:1000"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signup.SignupWithContext(ctx, control.VerifiedIdentity{ExternalSubject: "public-two", Email: "two@example.test", EmailVerified: true}, control.SignupContext{AgeConfirmed: true, Country: "VN", CountryVerified: true, NetworkAddress: "10.0.0.1:2000"}); !errors.Is(err, ErrSignupRateLimited) {
		t.Fatalf("rate limit err=%v", err)
	}
	request := controlRequest(account)
	enabled, err := service.FeatureEnabled(ctx, request, "source_upload")
	if err != nil || !enabled {
		t.Fatalf("source upload enabled=%v err=%v", enabled, err)
	}
	request.Capabilities["account:manage"] = struct{}{}
	operatorContext := auth.WithRequestContext(ctx, request)
	if err := service.SetWorkloadMode(operatorContext, "uploads_paused"); err != nil {
		t.Fatal(err)
	}
	enabled, err = service.FeatureEnabled(ctx, request, "source_upload")
	if err != nil || enabled {
		t.Fatalf("paused source upload enabled=%v err=%v", enabled, err)
	}
	var auditCount int
	if err := tenantQuery(ctx, pool, account.TenantID, `SELECT count(*) FROM saas_audit_events WHERE tenant_id=$1 AND operation='launch.workload_mode.update'`, account.TenantID).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("workload-mode audit count=%d err=%v", auditCount, err)
	}
	if err := service.SetSignupEnabled(ctx, "release-manager", "incident_signup_freeze", false); err != nil {
		t.Fatal(err)
	}
	if _, err := signup.SignupWithContext(ctx, control.VerifiedIdentity{ExternalSubject: "frozen", Email: "frozen@example.test", EmailVerified: true}, control.SignupContext{AgeConfirmed: true, Country: "VN", NetworkAddress: "10.0.0.2"}); !errors.Is(err, ErrSignupClosed) {
		t.Fatalf("signup freeze err=%v", err)
	}
	var policyAudits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM saas_launch_policy_history WHERE actor_id='release-manager' AND reason_code='incident_signup_freeze' AND new_signup_enabled=false`).Scan(&policyAudits); err != nil || policyAudits != 1 {
		t.Fatalf("policy audit count=%d err=%v", policyAudits, err)
	}
	if err := service.SetSignupEnabled(ctx, "release-manager", "test_cleanup", true); err != nil {
		t.Fatal(err)
	}
}

func controlRequest(account control.PersonalAccount) auth.RequestContext {
	return auth.RequestContext{AccountID: account.AccountID, SubjectID: account.AccountID, TenantID: account.TenantID, Role: "owner", Capabilities: map[string]struct{}{"source:write": {}}, RequestID: "request", TraceID: "trace"}
}

func tenantQuery(ctx context.Context, pool *pgxpool.Pool, tenantID, query string, args ...any) pgx.Row {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return errorRow{err}
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		_ = tx.Rollback(ctx)
		return errorRow{err}
	}
	row := tx.QueryRow(ctx, query, args...)
	return committingRow{row: row, tx: tx, ctx: ctx}
}

type errorRow struct{ err error }

func (r errorRow) Scan(...any) error { return r.err }

type committingRow struct {
	row pgx.Row
	tx  pgx.Tx
	ctx context.Context
}

func (r committingRow) Scan(dest ...any) error {
	if err := r.row.Scan(dest...); err != nil {
		_ = r.tx.Rollback(r.ctx)
		return err
	}
	return r.tx.Commit(r.ctx)
}
