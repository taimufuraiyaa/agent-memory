package backup

import (
	"context"
	"github.com/google/uuid"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRestoreRecordIdentityIsExplicit(t *testing.T) {
	record := Record{TargetType: "source", TargetID: "opaque"}
	if record.TargetType+":"+record.TargetID != "source:opaque" {
		t.Fatal("restore identity changed")
	}
}

func TestRestoreReplaysLaterTombstonesBeforeServing(t *testing.T) {
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
	backupAt := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	deletedAt := backupAt.Add(24 * time.Hour)
	account, tenant, target, operation := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO saas_accounts(id,external_subject,verified_email,state,created_at,updated_at) VALUES($1,$2,$3,'active',$4,$4)`, account, account, account+"@example.test", backupAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_tenants(id,kind,state,personal_owner_account_id,created_at,updated_at) VALUES($1,'personal','active',$2,$3,$3)`, tenant, account, backupAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_deletion_operations(tenant_id,id,target_type,target_id,policy_version,state,requested_at,completed_at,receipt_sha256,updated_at) VALUES($1,$2,'source',$3,'retention-v1','completed',$4,$5,$6,$5)`, tenant, operation, target, backupAt, deletedAt, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_deletion_tombstones(tenant_id,target_type,target_id,operation_id,deleted_at,receipt_sha256,backup_expires_at) VALUES($1,'source',$2,$3,$4,$5,$6)`, tenant, target, operation, deletedAt, strings.Repeat("a", 64), deletedAt.Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	guard := NewGuard(pool, func() time.Time { return deletedAt.Add(time.Hour) })
	records := []Record{{TargetType: "source", TargetID: target}, {TargetType: "source", TargetID: uuid.NewString()}}
	safe, err := guard.Filter(ctx, tenant, backupAt, records)
	if err != nil || len(safe) != 1 || safe[0].TargetID == target {
		t.Fatalf("safe=%+v err=%v", safe, err)
	}
	if err := guard.RecordDrill(ctx, tenant, backupAt, records, safe); err != nil {
		t.Fatal(err)
	}
	var outcome string
	var exposed int
	if err := pool.QueryRow(ctx, `SELECT outcome,exposed_deleted_count FROM saas_backup_restore_drills WHERE tenant_id=$1`, tenant).Scan(&outcome, &exposed); err != nil || outcome != "passed" || exposed != 0 {
		t.Fatalf("drill outcome=%s exposed=%d err=%v", outcome, exposed, err)
	}
}
