package importer

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/attestation"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/attestationstore"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/billing"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	exportservice "github.com/taimufuraiyaa/agent-memory/internal/saas/export"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/memory"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
	sourceservice "github.com/taimufuraiyaa/agent-memory/internal/saas/source"
)

type importQuarantine struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (q *importQuarantine) Put(_ context.Context, key string, body io.Reader, size int64, _ string) error {
	value, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(value)) != size {
		return errors.New("unexpected object size")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.values[key] = value
	return nil
}

func (q *importQuarantine) Delete(_ context.Context, key string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.values, key)
	return nil
}

type cancelAfterMemoryWrite struct {
	next   memory.Repository
	cancel context.CancelFunc
	once   sync.Once
}

func (r *cancelAfterMemoryWrite) WriteMemory(ctx context.Context, write memory.Write) (core.MemoryEntry, bool, error) {
	entry, duplicate, err := r.next.WriteMemory(ctx, write)
	if err == nil {
		r.once.Do(r.cancel)
	}
	return entry, duplicate, err
}

func TestPortableImportPrevalidatesAndResumesWithoutDuplicates(t *testing.T) {
	connectionURL := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if connectionURL == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	root, cancelRoot := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancelRoot()
	pool, err := saaspostgres.Open(root, connectionURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = saaspostgres.Apply(root, pool); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(root, "TRUNCATE saas_accounts CASCADE"); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 5, 18, 0, 0, 0, time.UTC)
	account, err := control.NewPostgresStore(pool).ProvisionPersonalAccount(root, control.ProvisionCommand{
		AccountID: uuid.NewString(), TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(),
		ExternalSubject: "provider|portable", VerifiedEmail: "portable@example.test", RequestID: uuid.NewString(), OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := auth.RequestContext{AccountID: account.AccountID, SubjectID: "provider|portable", TenantID: account.TenantID, RequestID: uuid.NewString(), TraceID: uuid.NewString(), Capabilities: map[string]struct{}{"memory:write": {}, "source:write": {}}}
	authenticated := auth.WithRequestContext(root, request)
	attestations := attestation.NewService(attestationstore.NewPostgresStore(pool), attestation.WithClock(func() time.Time { return now }))
	status, err := attestations.Status(authenticated, account.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	statementIDs := make([]string, 0, len(status.Policy.Statements))
	for _, statement := range status.Policy.Statements {
		statementIDs = append(statementIDs, statement.ID)
	}
	if _, err = attestations.Accept(authenticated, account.AccountID, attestation.AcceptCommand{PolicyVersion: status.Policy.Version, AcceptedStatementIDs: statementIDs, RequestID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}

	repository := memory.NewPostgresRepository(pool)
	quarantine := &importQuarantine{values: map[string][]byte{}}
	sources := sourceservice.NewService(sourceservice.NewPostgresRepository(pool), attestations, quarantine, func() time.Time { return now })
	newImporter := func(memoryRepository memory.Repository) *Service {
		return NewService(pool, memory.NewService(memoryRepository, func() time.Time { return now }), memory.NewWorkflowService(repository, func() time.Time { return now }), sources, attestations, billing.NewRepository(pool, func() time.Time { return now }), func() time.Time { return now })
	}

	valid := portableBundle(t, []byte("# Portable source\nDurable text."))
	bad := valid
	bad.SourceObjects = append([]exportservice.SourceObject{}, valid.SourceObjects...)
	bad.SourceObjects[0].ChecksumSHA256 = strings.Repeat("0", 64)
	if err = bad.SealManifest(); err != nil {
		t.Fatal(err)
	}
	badEncrypted := encryptBundle(t, bad)
	if _, err = newImporter(repository).Import(authenticated, account.WorkspaceID, "portable-invalid-0001", "correct horse battery", badEncrypted); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("corrupt import error = %v", err)
	}
	assertTenantCount(t, root, pool, account.TenantID, "saas_memories", 0)
	assertTenantCount(t, root, pool, account.TenantID, "saas_notes", 0)
	assertTenantCount(t, root, pool, account.TenantID, "saas_sources", 0)

	oversized := valid
	oversized.SourceObjects = append([]exportservice.SourceObject{}, valid.SourceObjects...)
	oversized.SourceObjects[0].SizeBytes = MaxBundleBytes + 1
	if err = oversized.SealManifest(); err != nil {
		t.Fatal(err)
	}
	if _, err = newImporter(repository).Import(authenticated, account.WorkspaceID, "portable-oversize-001", "correct horse battery", encryptBundle(t, oversized)); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("oversized import error = %v", err)
	}

	encrypted := encryptBundle(t, valid)
	interrupted, cancelImport := context.WithCancel(root)
	interrupted = auth.WithRequestContext(interrupted, request)
	first := newImporter(&cancelAfterMemoryWrite{next: repository, cancel: cancelImport})
	partial, err := first.Import(interrupted, account.WorkspaceID, "portable-resume-0001", "correct horse battery", encrypted)
	if !errors.Is(err, context.Canceled) || partial.State != "running" {
		t.Fatalf("interrupted result=%+v err=%v", partial, err)
	}
	assertTenantCount(t, root, pool, account.TenantID, "saas_memories", 1)
	assertTenantCount(t, root, pool, account.TenantID, "saas_sources", 0)

	completed, err := newImporter(repository).Import(authenticated, account.WorkspaceID, "portable-resume-0001", "correct horse battery", encrypted)
	if err != nil || completed.State != "completed" || len(completed.Report.Merged) != 1 || len(completed.Report.Imported) != 2 {
		t.Fatalf("completed result=%+v err=%v", completed, err)
	}
	if completed.Report.Merged[0].Type != "memory" || completed.Report.Merged[0].ExternalID != "memory-local-1" {
		t.Fatalf("unexpected merge report: %+v", completed.Report)
	}
	assertTenantCount(t, root, pool, account.TenantID, "saas_memories", 1)
	assertTenantCount(t, root, pool, account.TenantID, "saas_notes", 1)
	assertTenantCount(t, root, pool, account.TenantID, "saas_sources", 1)

	duplicate, err := newImporter(repository).Import(authenticated, account.WorkspaceID, "portable-other-key-1", "correct horse battery", encrypted)
	if err != nil || !duplicate.Duplicate || duplicate.ID != completed.ID {
		t.Fatalf("duplicate result=%+v err=%v", duplicate, err)
	}
	assertTenantCount(t, root, pool, account.TenantID, "saas_sources", 1)
	reportJSON, err := json.Marshal(completed.Report)
	if err != nil || !strings.Contains(string(reportJSON), `"external_id":"memory-local-1"`) || strings.Count(string(reportJSON), `"imported"`) != 1 {
		t.Fatalf("reconciliation JSON=%s err=%v", reportJSON, err)
	}
}

func portableBundle(t *testing.T, sourceBytes []byte) exportservice.Bundle {
	t.Helper()
	sum := sha256.Sum256(sourceBytes)
	checksum := hex.EncodeToString(sum[:])
	bundle := exportservice.Bundle{
		Format: "agent-memory-portable", Version: "2.0", MinReaderVersion: "2.0", ExportedAt: time.Date(2026, 8, 5, 17, 0, 0, 0, time.UTC),
		Memories:       []map[string]any{{"id": "memory-local-1", "type": "semantic", "content": "Portable memory content."}},
		Notes:          []map[string]any{{"id": "note-local-1", "path": "portable.md", "title": "Portable", "body": "# Portable", "properties": map[string]any{"origin": "local"}}},
		Sources:        []map[string]any{{"id": "source-local-1", "rights_basis": "lawfully_acquired_private_use"}},
		SourceVersions: []map[string]any{{"source_id": "source-local-1", "version": 1, "content_sha256": checksum}},
		Lineage:        []map[string]any{}, Attestations: []map[string]any{}, Policies: []map[string]any{{"version": "portable-v2"}},
		SourceBytesIncluded: true,
		SourceObjects:       []exportservice.SourceObject{{SourceID: "source-local-1", Filename: "portable.md", MediaType: "text/markdown", SizeBytes: int64(len(sourceBytes)), ChecksumSHA256: checksum, BytesBase64: base64.StdEncoding.EncodeToString(sourceBytes)}},
	}
	if err := bundle.SealManifest(); err != nil {
		t.Fatal(err)
	}
	return bundle
}

func encryptBundle(t *testing.T, bundle exportservice.Bundle) []byte {
	t.Helper()
	plain, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := exportservice.EncryptPortable("correct horse battery", plain)
	if err != nil {
		t.Fatal(err)
	}
	return encrypted
}

func assertTenantCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant, table string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE tenant_id=$1", tenant).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s count=%d want=%d", table, count, want)
	}
}
