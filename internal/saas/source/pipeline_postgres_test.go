package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/taimufuraiyaa/agent-memory/internal/attestation"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/attestationstore"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type testObjects struct {
	mu                sync.Mutex
	quarantine, vault map[string][]byte
}

func (o *testObjects) Put(_ context.Context, key string, body io.Reader, size int64, _ string) error {
	value, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(value)) != size {
		return fmt.Errorf("size mismatch")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.quarantine[key] = value
	return nil
}
func (o *testObjects) Get(_ context.Context, key string) ([]byte, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	value, ok := o.quarantine[key]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return append([]byte(nil), value...), nil
}
func (o *testObjects) Delete(_ context.Context, key string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.quarantine, key)
	return nil
}
func (o *testObjects) PutVault(_ context.Context, key string, value []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.vault[key] = append([]byte(nil), value...)
	return nil
}
func (o *testObjects) GetVault(_ context.Context, tenantID, key string) ([]byte, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !strings.HasPrefix(key, "vault/"+tenantID+"/") {
		return nil, errors.New("outside tenant capability")
	}
	value, ok := o.vault[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), value...), nil
}
func TestSourceGrantValidationPromotionRejectionExpiryAndQuota(t *testing.T) {
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
	now := time.Date(2026, 8, 5, 17, 0, 0, 0, time.UTC)
	account, err := control.NewPostgresStore(pool).ProvisionPersonalAccount(ctx, control.ProvisionCommand{AccountID: uuid.NewString(), TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), ExternalSubject: "provider|source", VerifiedEmail: "source@example.test", RequestID: uuid.NewString(), OccurredAt: now})
	if err != nil {
		t.Fatal(err)
	}
	request := auth.RequestContext{AccountID: account.AccountID, SubjectID: "provider|source", TenantID: account.TenantID, Capabilities: map[string]struct{}{"source:write": {}}, RequestID: uuid.NewString(), TraceID: uuid.NewString()}
	authenticated := auth.WithRequestContext(ctx, request)
	attestations := attestation.NewService(attestationstore.NewPostgresStore(pool), attestation.WithClock(func() time.Time { return now }))
	policy := attestation.CurrentPolicy()
	ids := []string{}
	for _, statement := range policy.Statements {
		ids = append(ids, statement.ID)
	}
	if _, err := attestations.Accept(authenticated, account.AccountID, attestation.AcceptCommand{PolicyVersion: policy.Version, AcceptedStatementIDs: ids, RequestID: request.RequestID}); err != nil {
		t.Fatal(err)
	}
	repository := NewPostgresRepository(pool)
	objects := &testObjects{quarantine: map[string][]byte{}, vault: map[string][]byte{}}
	uploads := NewService(repository, attestations, objects, func() time.Time { return now })
	processor, err := NewProcessor(repository, objects, "test-vault-key", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	valid := []byte("%PDF-1.7\nvalid private source")
	grant := issueTestGrant(t, authenticated, uploads, account.WorkspaceID, "valid.pdf", "application/pdf", valid, "")
	if err := uploads.Upload(ctx, grant.ID, grant.token, "application/pdf", int64(len(valid)), bytes.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
	if err := uploads.Upload(ctx, grant.ID, grant.token, "application/pdf", int64(len(valid)), bytes.NewReader(valid)); err == nil {
		t.Fatal("single-use grant accepted replay")
	}
	promoted, err := processor.ProcessOnce(ctx)
	if err != nil || promoted != 1 {
		t.Fatalf("promotion count=%d err=%v", promoted, err)
	}
	if len(objects.quarantine) != 0 || len(objects.vault) != 1 {
		t.Fatalf("object state quarantine=%d vault=%d", len(objects.quarantine), len(objects.vault))
	}
	for _, encrypted := range objects.vault {
		if bytes.Contains(encrypted, valid) {
			t.Fatal("vault stored plaintext source")
		}
	}
	var sourceState, grantState, encryptionVersion string
	if err := tenantQuery(ctx, pool, account.TenantID, `SELECT s.state,g.state,v.vault_encryption_version FROM saas_sources s JOIN saas_upload_grants g ON g.tenant_id=s.tenant_id AND g.source_id=s.id JOIN saas_source_versions v ON v.tenant_id=s.tenant_id AND v.source_id=s.id WHERE s.id=$1`, grant.SourceID).Scan(&sourceState, &grantState, &encryptionVersion); err != nil {
		t.Fatal(err)
	}
	if sourceState != "processing" || grantState != "promoted" || encryptionVersion != "aes-256-gcm-v1" {
		t.Fatalf("promoted state source=%s grant=%s encryption=%s", sourceState, grantState, encryptionVersion)
	}
	malformed := []byte("not actually a pdf")
	bad := issueTestGrant(t, authenticated, uploads, account.WorkspaceID, "bad.pdf", "application/pdf", malformed, "")
	if err := uploads.Upload(ctx, bad.ID, bad.token, "application/pdf", int64(len(malformed)), bytes.NewReader(malformed)); err != nil {
		t.Fatal(err)
	}
	if count, err := processor.ProcessOnce(ctx); err != nil || count != 0 {
		t.Fatalf("malformed count=%d err=%v", count, err)
	}
	if err := tenantQuery(ctx, pool, account.TenantID, `SELECT state FROM saas_sources WHERE id=$1`, bad.SourceID).Scan(&sourceState); err != nil || sourceState != "failed" {
		t.Fatalf("malformed state=%s err=%v", sourceState, err)
	}
	if len(objects.quarantine) != 0 {
		t.Fatal("rejected quarantine object was retained")
	}
	malware := []byte("X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*")
	infected := issueTestGrant(t, authenticated, uploads, account.WorkspaceID, "eicar.txt", "text/plain", malware, "")
	if err := uploads.Upload(ctx, infected.ID, infected.token, "text/plain", int64(len(malware)), bytes.NewReader(malware)); err != nil {
		t.Fatal(err)
	}
	if count, err := processor.ProcessOnce(ctx); err != nil || count != 0 {
		t.Fatalf("malware count=%d err=%v", count, err)
	}
	var errorCode string
	if err := tenantQuery(ctx, pool, account.TenantID, `SELECT safe_error_code FROM saas_upload_grants WHERE id=$1`, infected.ID).Scan(&errorCode); err != nil || errorCode != "malware_detected" {
		t.Fatalf("malware error=%s err=%v", errorCode, err)
	}
	uncertainValue := []byte("plain scanner uncertainty")
	uncertain := issueTestGrant(t, authenticated, uploads, account.WorkspaceID, "uncertain.txt", "text/plain", uncertainValue, "")
	if err := uploads.Upload(ctx, uncertain.ID, uncertain.token, "text/plain", int64(len(uncertainValue)), bytes.NewReader(uncertainValue)); err != nil {
		t.Fatal(err)
	}
	objects.mu.Lock()
	for key := range objects.quarantine {
		delete(objects.quarantine, key)
	}
	objects.mu.Unlock()
	if count, err := processor.ProcessOnce(ctx); err == nil || count != 0 {
		t.Fatalf("scanner uncertainty count=%d err=%v", count, err)
	}
	if err := tenantQuery(ctx, pool, account.TenantID, `SELECT state FROM saas_sources WHERE id=$1`, uncertain.SourceID).Scan(&sourceState); err != nil || sourceState != "failed" {
		t.Fatalf("uncertain source state=%s err=%v", sourceState, err)
	}
	expiring := issueTestGrant(t, authenticated, uploads, account.WorkspaceID, "expire.txt", "text/plain", []byte("expires"), "")
	lateProcessor, _ := NewProcessor(repository, objects, "test-vault-key", func() time.Time { return now.Add(11 * time.Minute) })
	if _, err := lateProcessor.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tenantQuery(ctx, pool, account.TenantID, `SELECT state FROM saas_upload_grants WHERE id=$1`, expiring.ID).Scan(&grantState); err != nil || grantState != "expired" {
		t.Fatalf("expired grant state=%s err=%v", grantState, err)
	}
	if _, err := tenantExec(ctx, pool, account.TenantID, `UPDATE saas_tenant_entitlements SET max_concurrent_uploads=1 WHERE tenant_id=$1`, account.TenantID); err != nil {
		t.Fatal(err)
	}
	_ = issueTestGrant(t, authenticated, uploads, account.WorkspaceID, "quota-one.txt", "text/plain", []byte("one"), "")
	sum := sha256.Sum256([]byte("two"))
	if _, err := uploads.Issue(authenticated, GrantRequest{WorkspaceID: account.WorkspaceID, Filename: "quota-two.txt", MediaType: "text/plain", SizeBytes: 3, ChecksumSHA256: fmt.Sprintf("%x", sum), RightsBasis: "author_owned"}); err == nil {
		t.Fatal("concurrent upload quota was not enforced")
	}
	if _, err := tenantExec(ctx, pool, account.TenantID, `UPDATE saas_tenant_entitlements SET source_upload_enabled=false WHERE tenant_id=$1`, account.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := uploads.Issue(authenticated, GrantRequest{WorkspaceID: account.WorkspaceID, Filename: "blocked.txt", MediaType: "text/plain", SizeBytes: 1, ChecksumSHA256: strings.Repeat("0", 64), RightsBasis: "author_owned"}); err == nil {
		t.Fatal("disabled entitlement issued upload grant")
	}
}
func issueTestGrant(t *testing.T, ctx context.Context, service *Service, workspace, filename, media string, value []byte, checksum string) Grant {
	t.Helper()
	if checksum == "" {
		sum := sha256.Sum256(value)
		checksum = fmt.Sprintf("%x", sum)
	}
	grant, err := service.Issue(ctx, GrantRequest{WorkspaceID: workspace, Filename: filename, MediaType: media, SizeBytes: int64(len(value)), ChecksumSHA256: checksum, RightsBasis: "lawfully_acquired_private_use"})
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

type scanRow interface{ Scan(...any) error }

func tenantQuery(ctx context.Context, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, tenant, query string, args ...any) scanRow {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return errorRow{err}
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenant); err != nil {
		return errorRow{err}
	}
	row := tx.QueryRow(ctx, query, args...)
	return &txRow{Row: row, tx: tx, ctx: ctx}
}

type txRow struct {
	pgx.Row
	tx  pgx.Tx
	ctx context.Context
}

func (r *txRow) Scan(values ...any) error {
	err := r.Row.Scan(values...)
	_ = r.tx.Rollback(r.ctx)
	return err
}

type errorRow struct{ err error }

func (r errorRow) Scan(...any) error { return r.err }
func tenantExec(ctx context.Context, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, tenant, query string, args ...any) (pgconn.CommandTag, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenant); err != nil {
		return pgconn.CommandTag{}, err
	}
	tag, err := tx.Exec(ctx, query, args...)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return tag, err
}
