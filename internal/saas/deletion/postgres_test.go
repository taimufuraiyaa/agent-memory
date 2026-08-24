package deletion

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retention"
)

type flakyObjects struct {
	fail              int
	vault, quarantine map[string]bool
}

func (f *flakyObjects) Delete(_ context.Context, key string) error {
	if f.fail > 0 {
		f.fail--
		return errors.New("injected object failure")
	}
	delete(f.quarantine, key)
	return nil
}
func (f *flakyObjects) DeleteVault(_ context.Context, key string) error {
	if f.fail > 0 {
		f.fail--
		return errors.New("injected object failure")
	}
	delete(f.vault, key)
	return nil
}

func TestSourceDeletionRevokesImmediatelyRetriesAndRequiresEveryConfirmation(t *testing.T) {
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
	now := time.Date(2026, 8, 5, 6, 0, 0, 0, time.UTC)
	account, tenant, workspace, source, receipt, grant := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO saas_accounts(id,external_subject,verified_email,state,created_at,updated_at) VALUES($1,$2,$3,'active',$4,$4)`, []any{account, account, account + "@example.test", now}},
		{`INSERT INTO saas_tenants(id,kind,state,personal_owner_account_id,created_at,updated_at) VALUES($1,'personal','active',$2,$3,$3)`, []any{tenant, account, now}},
		{`INSERT INTO saas_memberships(tenant_id,account_id,role,state,created_at,updated_at) VALUES($1,$2,'owner','active',$3,$3)`, []any{tenant, account, now}},
		{`INSERT INTO saas_workspaces(tenant_id,id,name,state,created_at,updated_at) VALUES($1,$2,'private','active',$3,$3)`, []any{tenant, workspace, now}},
		{`INSERT INTO saas_attestation_receipts(tenant_id,id,subject_id,policy_version,statement_digest,accepted_statement_ids,accepted_at,expires_at,request_id,user_agent) VALUES($1,$2,$3,'rights-v1','digest','[]',$4,$5,'request','test')`, []any{tenant, receipt, account, now, now.Add(24 * time.Hour)}},
		{`INSERT INTO saas_sources(tenant_id,id,workspace_id,state,rights_basis,attestation_receipt_id,active_version,created_at,updated_at) VALUES($1,$2,$3,'ready','licensed',$4,1,$5,$5)`, []any{tenant, source, workspace, receipt, now}},
		{`INSERT INTO saas_source_versions(tenant_id,source_id,version,content_sha256,media_type,parser_version,normalization_version,vault_object_key,created_at) VALUES($1,$2,1,$3,'text/plain','text-v1','norm-v1',$4,$5)`, []any{tenant, source, strings.Repeat("a", 64), "vault/" + tenant + "/source", now}},
		{`INSERT INTO saas_upload_grants(tenant_id,id,source_id,workspace_id,account_id,filename,media_type,expected_size,expected_sha256,rights_basis,attestation_receipt_id,token_hash,quarantine_object_key,state,expires_at,created_at) VALUES($1,$2,$3,$4,$5,'book.txt','text/plain',4,$6,'licensed',$7,$8,$9,'uploaded',$10,$11)`, []any{tenant, grant, source, workspace, account, strings.Repeat("a", 64), receipt, []byte("hash"), "quarantine/" + tenant + "/source", now.Add(time.Hour), now}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	registry := retention.NewRegistry(pool)
	if err := registry.ValidateCoverage(ctx); err != nil {
		t.Fatal(err)
	}
	repository := NewPostgresRepository(pool, registry)
	clock := now
	objects := &flakyObjects{fail: 1, vault: map[string]bool{"vault/" + tenant + "/source": true}, quarantine: map[string]bool{"quarantine/" + tenant + "/source": true}}
	service := NewService(repository, map[string]Purger{"object": NewObjectPurger(objects), "database": NewDatabasePurger(pool, "database"), "index": NewDatabasePurger(pool, "index"), "cache": NewDatabasePurger(pool, "cache"), "queue": NewDatabasePurger(pool, "queue")}, func() time.Time { return clock })
	requestCtx := auth.WithRequestContext(ctx, auth.RequestContext{TenantID: tenant, AccountID: account, RequestID: uuid.NewString(), TraceID: uuid.NewString(), Capabilities: map[string]struct{}{"source:delete": {}}})
	op, duplicate, err := service.RequestSource(requestCtx, source, "delete-source-0001")
	if err != nil || duplicate {
		t.Fatalf("RequestSource op=%+v duplicate=%v err=%v", op, duplicate, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE saas_tenants SET state='deleting' WHERE id=$1`, tenant); err != nil {
		t.Fatal(err)
	}
	pendingTenants, err := repository.PendingTenantIDs(ctx)
	if err != nil || len(pendingTenants) != 1 || pendingTenants[0] != tenant {
		t.Fatalf("pending deletion tenants=%v err=%v", pendingTenants, err)
	}
	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM saas_sources WHERE tenant_id=$1 AND id=$2`, tenant, source).Scan(&state); err != nil || state != "deleting" {
		t.Fatalf("immediate state=%s err=%v", state, err)
	}
	if processed, err := service.RunOnce(ctx, tenant); err != nil || !processed {
		t.Fatalf("first RunOnce processed=%v err=%v", processed, err)
	}
	var versions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM saas_source_versions WHERE tenant_id=$1 AND source_id=$2`, tenant, source).Scan(&versions); err != nil || versions != 1 {
		t.Fatalf("failed object purge removed database data versions=%d err=%v", versions, err)
	}
	clock = clock.Add(2 * time.Minute)
	if processed, err := service.RunOnce(ctx, tenant); err != nil || !processed {
		t.Fatalf("retry RunOnce processed=%v err=%v", processed, err)
	}
	var operationState, receiptHash string
	if err := pool.QueryRow(ctx, `SELECT state,receipt_sha256 FROM saas_deletion_operations WHERE tenant_id=$1 AND id=$2`, tenant, op.ID).Scan(&operationState, &receiptHash); err != nil || operationState != "completed" || len(receiptHash) != 64 {
		t.Fatalf("operation state=%s receipt=%s err=%v", operationState, receiptHash, err)
	}
	var confirmations int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM saas_deletion_confirmations WHERE tenant_id=$1 AND operation_id=$2 AND state='confirmed'`, tenant, op.ID).Scan(&confirmations); err != nil || confirmations != len(Subsystems) {
		t.Fatalf("confirmations=%d err=%v", confirmations, err)
	}
	if objects.vault["vault/"+tenant+"/source"] || objects.quarantine["quarantine/"+tenant+"/source"] {
		t.Fatal("source objects remain after verified deletion")
	}
	if replay, duplicate, err := service.RequestSource(requestCtx, source, "delete-source-0001"); err != nil || !duplicate || replay.ID != op.ID {
		t.Fatalf("idempotent replay=%+v duplicate=%v err=%v", replay, duplicate, err)
	}
}
