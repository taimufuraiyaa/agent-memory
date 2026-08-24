package audit

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestPostgresAuditContractImmutabilitySearchAndArchiveReplay(t *testing.T) {
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

	now := time.Date(2026, 8, 5, 2, 0, 0, 0, time.UTC)
	tenantID := seedAuditTenant(t, ctx, pool, now)
	repository := NewPostgresRepository(pool)
	for index := 0; index < 2; index++ {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		err = Append(ctx, tx, Event{TenantID: tenantID, ID: uuid.NewString(), OccurredAt: now.Add(time.Duration(index) * time.Second),
			ActorType: "member", ActorID: "account", Service: "source", Operation: "source.read",
			Outcome: "success", RequestID: "request", TraceID: "trace", TargetType: "source",
			TargetID: "opaque-source", PolicyVersion: "policy-v1", ReasonCode: "authorized",
			SafeMetadata: map[string]any{"job_state": "ready"}})
		if err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	events, err := repository.Search(ctx, tenantID, Filter{Operation: "source.read", Limit: 10})
	if err != nil || len(events) != 2 {
		t.Fatalf("Search count=%d err=%v", len(events), err)
	}
	if events[0].EventHash == "" || events[0].PreviousHash != events[1].EventHash {
		t.Fatalf("hash chain is not linked: newest=%+v older=%+v", events[0], events[1])
	}
	if _, err := pool.Exec(ctx, "UPDATE saas_audit_events SET outcome='rewritten' WHERE tenant_id=$1", tenantID); err == nil {
		t.Fatal("audit update unexpectedly succeeded")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_audit_events(tenant_id,id,actor_type,actor_id,operation,outcome,request_id,correlation_id,target_type,target_id,safe_metadata,occurred_at)
		VALUES($1,$2,'member','account','source.read','success','request','trace','source','opaque',$3,$4)`,
		tenantID, uuid.NewString(), `{"content":"private book text"}`, now); err == nil {
		t.Fatal("unsafe audit metadata unexpectedly succeeded")
	}
	archiveNow := time.Now().UTC().Add(time.Minute)
	records, err := repository.ClaimArchive(ctx, tenantID, 10, archiveNow)
	if err != nil || len(records) != 2 {
		t.Fatalf("ClaimArchive count=%d err=%v", len(records), err)
	}
	for _, record := range records {
		value, _ := record.Event.JSON()
		if err := repository.MarkArchived(ctx, record, ArchiveKey(record.Event), SHA256(value), archiveNow); err != nil {
			t.Fatal(err)
		}
	}
	replayed, err := repository.ClaimArchive(ctx, tenantID, 10, archiveNow.Add(time.Minute))
	if err != nil || len(replayed) != 0 {
		t.Fatalf("archive replay count=%d err=%v", len(replayed), err)
	}
}

func seedAuditTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, now time.Time) string {
	t.Helper()
	accountID, tenantID := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO saas_accounts(id,external_subject,verified_email,state,created_at,updated_at)
		VALUES($1,$2,$3,'active',$4,$4)`, accountID, accountID, accountID+"@example.test", now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_tenants(id,kind,state,personal_owner_account_id,created_at,updated_at)
		VALUES($1,'personal','active',$2,$3,$3)`, tenantID, accountID, now); err != nil {
		t.Fatal(err)
	}
	return tenantID
}
