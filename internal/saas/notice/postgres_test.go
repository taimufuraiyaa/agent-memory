package notice

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

func TestNoticeTabletopValidInvalidConflictingUrgentAndRepeatDecision(t *testing.T) {
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
	now := time.Date(2026, 8, 5, 7, 0, 0, 0, time.UTC)
	account, tenant, workspace, source, receipt := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO saas_accounts(id,external_subject,verified_email,state,created_at,updated_at) VALUES($1,$2,$3,'active',$4,$4)`, []any{account, account, account + "@example.test", now}},
		{`INSERT INTO saas_tenants(id,kind,state,personal_owner_account_id,created_at,updated_at) VALUES($1,'personal','active',$2,$3,$3)`, []any{tenant, account, now}},
		{`INSERT INTO saas_workspaces(tenant_id,id,name,state,created_at,updated_at) VALUES($1,$2,'private','active',$3,$3)`, []any{tenant, workspace, now}},
		{`INSERT INTO saas_attestation_receipts(tenant_id,id,subject_id,policy_version,statement_digest,accepted_statement_ids,accepted_at,expires_at,request_id,user_agent) VALUES($1,$2,$3,'rights-v1','digest','[]',$4,$5,'request','test')`, []any{tenant, receipt, account, now, now.Add(24 * time.Hour)}},
		{`INSERT INTO saas_sources(tenant_id,id,workspace_id,state,rights_basis,attestation_receipt_id,created_at,updated_at) VALUES($1,$2,$3,'ready','licensed',$4,$5,$5)`, []any{tenant, source, workspace, receipt, now}},
		{`INSERT INTO saas_operator_assignments(tenant_id,operator_id,role,state,granted_by,granted_at) VALUES($1,'trust-a','trust','active','bootstrap',$2)`, []any{tenant, now}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	clock := now
	repository := NewRepository(pool, func() time.Time { return clock })
	invalidCase, err := repository.Intake(ctx, tenant, "trust-a", source, "US", "claimant@example.test", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Transition(ctx, tenant, invalidCase.ID, "trust-a", "invalid", "missing_required_elements", nil); err != nil {
		t.Fatal(err)
	}
	urgent, err := repository.Intake(ctx, tenant, "trust-a", source, "US", "urgent@example.test", true)
	if err != nil || !urgent.ValidationDueAt.Equal(now.Add(4*time.Hour)) {
		t.Fatalf("urgent=%+v err=%v", urgent, err)
	}
	for _, step := range []struct{ state, reason string }{{"validated", "required_elements_present"}, {"source_disabled", "urgent_disable"}, {"user_notified", "notice_sent"}, {"counter_notice_received", "counter_received"}, {"restored", "counter_period_complete"}} {
		clock = clock.Add(time.Minute)
		if _, err := repository.Transition(ctx, tenant, urgent.ID, "trust-a", step.state, step.reason, []string{"evidence-ref"}); err != nil {
			t.Fatalf("transition %s: %v", step.state, err)
		}
	}
	if _, err := repository.Transition(ctx, tenant, urgent.ID, "trust-a", "deletion_requested", "conflicting_late_delete", nil); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("conflicting transition err=%v", err)
	}
	var sourceState string
	if err := pool.QueryRow(ctx, `SELECT state FROM saas_sources WHERE tenant_id=$1 AND id=$2`, tenant, source).Scan(&sourceState); err != nil || sourceState != "ready" {
		t.Fatalf("restored source state=%s err=%v", sourceState, err)
	}
	decision, err := repository.DecideRepeatAbuse(ctx, tenant, "trust-a", account, "warning", "repeat_valid_notices", []string{urgent.ID})
	if err != nil || decision == "" {
		t.Fatalf("repeat decision=%s err=%v", decision, err)
	}
	var transitions, audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM saas_legal_case_transitions WHERE tenant_id=$1`, tenant).Scan(&transitions); err != nil || transitions != 8 {
		t.Fatalf("transitions=%d err=%v", transitions, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM saas_audit_events WHERE tenant_id=$1 AND operation LIKE 'notice.%'`, tenant).Scan(&audits); err != nil || audits != 9 {
		t.Fatalf("notice audits=%d err=%v", audits, err)
	}
}
