package outbox

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

type recordingBroker struct {
	mu     sync.Mutex
	events [][]byte
	fail   bool
}

func (b *recordingBroker) Publish(_ context.Context, _ string, body []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		return context.DeadlineExceeded
	}
	b.events = append(b.events, append([]byte(nil), body...))
	return nil
}
func (b *recordingBroker) count() int { b.mu.Lock(); defer b.mu.Unlock(); return len(b.events) }

func TestPublisherRecoversAfterPublishBeforeMarkAndDeadLettersPoisonEvents(t *testing.T) {
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
	now := time.Date(2026, 8, 5, 15, 0, 0, 0, time.UTC)
	account, err := control.NewPostgresStore(pool).ProvisionPersonalAccount(ctx, control.ProvisionCommand{AccountID: uuid.NewString(), TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), ExternalSubject: "provider|outbox", VerifiedEmail: "outbox@example.test", RequestID: uuid.NewString(), OccurredAt: now})
	if err != nil {
		t.Fatal(err)
	}
	repository := NewPostgresRepository(pool)
	clearTenantOutbox(t, ctx, pool, account.TenantID)
	goodID := insertEvent(t, ctx, pool, account.TenantID, "memory.created", now)
	broker := &recordingBroker{}
	clock := now
	publisher := NewPublisher(repository, broker, func() time.Time { return clock })
	claimed, err := repository.Claim(ctx, account.TenantID, 1, time.Second, clock)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if err := publisher.publish(ctx, claimed[0]); err != nil {
		t.Fatal(err)
	} // Simulate process death before MarkPublished.
	clock = clock.Add(2 * time.Second)
	published, err := publisher.RunOnce(ctx)
	if err != nil || published != 1 || broker.count() != 2 {
		t.Fatalf("restart published=%d broker=%d err=%v", published, broker.count(), err)
	}
	var publishedAt *time.Time
	if err := pool.QueryRow(ctx, "SELECT published_at FROM saas_outbox WHERE id=$1", goodID).Scan(&publishedAt); err != nil || publishedAt == nil {
		t.Fatalf("published_at=%v err=%v", publishedAt, err)
	}
	poisonID := insertEvent(t, ctx, pool, account.TenantID, "INVALID EVENT", clock)
	for attempt := 0; attempt < MaxAttempts; attempt++ {
		clock = clock.Add(10 * time.Minute)
		if _, err := publisher.RunOnce(ctx); !errors.Is(err, ErrInvalidEvent) {
			t.Fatalf("poison attempt error=%v, want ErrInvalidEvent", err)
		}
	}
	var attempts int
	var dead *time.Time
	var code string
	if err := pool.QueryRow(ctx, "SELECT attempts,dead_lettered_at,last_error_code FROM saas_outbox WHERE id=$1", poisonID).Scan(&attempts, &dead, &code); err != nil {
		t.Fatal(err)
	}
	if attempts != MaxAttempts || dead == nil || code != "invalid_event" {
		t.Fatalf("poison attempts=%d dead=%v code=%s", attempts, dead, code)
	}
	duplicate, err := repository.RecordCheckpoint(ctx, account.TenantID, "test-consumer", goodID, clock)
	if err != nil || duplicate {
		t.Fatalf("first checkpoint duplicate=%v err=%v", duplicate, err)
	}
	duplicate, err = repository.RecordCheckpoint(ctx, account.TenantID, "test-consumer", goodID, clock)
	if err != nil || !duplicate {
		t.Fatalf("replayed checkpoint duplicate=%v err=%v", duplicate, err)
	}
	stats, err := repository.Stats(ctx, account.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Published != 1 || stats.DeadLetters != 1 || stats.Checkpoints != 1 || stats.Pending != 0 {
		t.Fatalf("delivery stats=%+v", stats)
	}
}
func clearTenantOutbox(t *testing.T, ctx context.Context, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, tenantID string) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM saas_outbox WHERE tenant_id=$1", tenantID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
func insertEvent(t *testing.T, ctx context.Context, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, tenantID, eventType string, at time.Time) string {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	aggregate := uuid.NewString()
	if _, err := tx.Exec(ctx, `INSERT INTO saas_outbox(tenant_id,id,event_type,spec_version,aggregate_type,aggregate_id,payload,occurred_at,next_attempt_at) VALUES($1,$2,$3,'1.0','memory',$4,'{}',$5,$5)`, tenantID, id, eventType, aggregate, at); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}
