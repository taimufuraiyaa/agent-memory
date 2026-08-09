package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/attestation"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/attestationstore"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/billing"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/credential"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/deletion"
	exportservice "github.com/taimufuraiyaa/agent-memory/internal/saas/export"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/importer"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/memory"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/privacy"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retention"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retrieval"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/review"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/security"
	sourceservice "github.com/taimufuraiyaa/agent-memory/internal/saas/source"
)

type testIdentityProvider struct {
	profiles map[string]auth.VerifiedProfile
}
type memoryObjects struct {
	mu     sync.Mutex
	values map[string][]byte
}
type memoryQuarantine struct {
	mu     sync.Mutex
	values map[string][]byte
}

type sourceQueryFixture struct{}

func (sourceQueryFixture) Query(_ context.Context, query retrieval.Query) (retrieval.Result, error) {
	return retrieval.Result{Answerable: true, Evidence: []retrieval.Evidence{{SourceID: query.AuthorizedSourceIDs[0], SourceVersion: 1, PassageID: "passage", CitationID: "citation", Text: "Citable source evidence."}}}, nil
}

type memoryReviewFixture struct{ proposals map[string]review.Proposal }

func (f *memoryReviewFixture) Create(_ context.Context, command review.CreateCommand) (review.Proposal, error) {
	proposal := review.Proposal{ID: uuid.NewString(), WorkspaceID: command.WorkspaceID, MemoryType: command.MemoryType, Content: command.Content, Transformation: command.Transformation, Evidence: command.Evidence, Status: "suggested"}
	f.proposals[proposal.ID] = proposal
	return proposal, nil
}
func (f *memoryReviewFixture) Get(_ context.Context, id string) (review.Proposal, error) {
	proposal, ok := f.proposals[id]
	if !ok {
		return review.Proposal{}, fmt.Errorf("not found")
	}
	return proposal, nil
}
func (f *memoryReviewFixture) Update(_ context.Context, id string, command review.UpdateCommand) (review.Proposal, error) {
	proposal, err := f.Get(context.Background(), id)
	if err != nil {
		return review.Proposal{}, err
	}
	proposal.Content, proposal.Transformation = command.Content, command.Transformation
	f.proposals[id] = proposal
	return proposal, nil
}
func (f *memoryReviewFixture) Accept(_ context.Context, id string) (review.Proposal, error) {
	proposal, err := f.Get(context.Background(), id)
	if err != nil {
		return review.Proposal{}, err
	}
	proposal.Status, proposal.MemoryID = "accepted", uuid.NewString()
	f.proposals[id] = proposal
	return proposal, nil
}
func (f *memoryReviewFixture) Reject(_ context.Context, id string) (review.Proposal, error) {
	proposal, err := f.Get(context.Background(), id)
	if err != nil {
		return review.Proposal{}, err
	}
	proposal.Status = "rejected"
	f.proposals[id] = proposal
	return proposal, nil
}

func (m *memoryQuarantine) Put(_ context.Context, key string, body io.Reader, size int64, _ string) error {
	value, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(value)) != size {
		return fmt.Errorf("size mismatch")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
	return nil
}
func (m *memoryQuarantine) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, key)
	return nil
}

func (m *memoryObjects) Put(_ context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = append([]byte(nil), value...)
	return nil
}
func (m *memoryObjects) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.values[key]...), nil
}

func (p testIdentityProvider) Verify(_ context.Context, token string) (auth.Identity, error) {
	profile, ok := p.profiles[token]
	if !ok {
		return auth.Identity{}, fmt.Errorf("invalid token")
	}
	return auth.Identity{SubjectID: profile.SubjectID, SessionID: "session-" + token}, nil
}

func (p testIdentityProvider) Profile(ctx context.Context, token string) (auth.VerifiedProfile, error) {
	if _, err := p.Verify(ctx, token); err != nil {
		return auth.VerifiedProfile{}, err
	}
	return p.profiles[token], nil
}

func TestHostedHTTPFlowIsAuthenticatedAndTenantIsolated(t *testing.T) {
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

	provider := testIdentityProvider{profiles: map[string]auth.VerifiedProfile{
		"token-one": {SubjectID: "provider|one", Email: "one@example.test", DisplayName: "One"},
		"token-two": {SubjectID: "provider|two", Email: "two@example.test", DisplayName: "Two"},
	}}
	accounts := control.NewPostgresStore(pool)
	memoryRepository := memory.NewPostgresRepository(pool)
	credentials := credential.NewService(credential.NewPostgresRepository(pool), nil)
	objects := &memoryObjects{values: map[string][]byte{}}
	exports, err := exportservice.NewService(exportservice.NewPostgresRepository(pool), objects, "test-export-encryption-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	attestations := attestation.NewService(attestationstore.NewPostgresStore(pool))
	quarantine := &memoryQuarantine{values: map[string][]byte{}}
	sourceUploads := sourceservice.NewService(sourceservice.NewPostgresRepository(pool), attestations, quarantine, nil)
	sourceCatalog := sourceservice.NewCatalogService(sourceservice.NewPostgresRepository(pool), nil)
	memoryReviews := &memoryReviewFixture{proposals: map[string]review.Proposal{}}
	deletionRegistry := retention.NewRegistry(pool)
	deletionRepository := deletion.NewPostgresRepository(pool, deletionRegistry)
	hostedMemories := memory.NewService(memoryRepository, nil)
	hostedWorkflows := memory.NewWorkflowService(memoryRepository, nil)
	billingRepository := billing.NewRepository(pool, nil)
	handler, err := NewHandler(Dependencies{
		Readiness:     func(context.Context) error { return nil },
		Authenticator: auth.NewCompositeAuthenticator(provider, credential.NewTokenAuthenticator(credentials)), Profiles: provider, Memberships: accounts,
		Signup:          control.NewSignupService(accounts, nil),
		Attestations:    attestations,
		Memories:        hostedMemories,
		Credentials:     credentials,
		Workflows:       hostedWorkflows,
		Exports:         exports,
		SourceUploads:   sourceUploads,
		SourceCatalog:   sourceCatalog,
		SourceQueries:   sourceQueryFixture{},
		MemoryReviews:   memoryReviews,
		Audit:           audit.NewService(pool, nil),
		Deletions:       deletion.NewService(deletionRepository, nil, nil),
		AccountDeletion: deletion.NewAccountService(pool, deletionRepository, deletionRegistry, nil),
		SecurityGate:    security.NewGate(pool),
		Privacy:         privacy.NewService(pool),
		Billing:         billing.NewService(billingRepository),
		Imports:         importer.NewService(pool, hostedMemories, hostedWorkflows, sourceUploads, attestations, billingRepository, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	one := signupHTTP(t, server.URL, "token-one")
	two := signupHTTP(t, server.URL, "token-two")
	if one.TenantID == two.TenantID || one.WorkspaceID == two.WorkspaceID {
		t.Fatal("signup did not create isolated personal resources")
	}
	querySourceID := uuid.NewString()
	response := requestHTTP(t, server.URL+"/v1/source-queries", "token-one", one.TenantID, "POST", map[string]any{"source_ids": []string{querySourceID}, "query": "What does it say?", "provider": "local", "model": "embed-v1"}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("source query status=%d body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()
	response = requestHTTP(t, server.URL+"/v1/memory-proposals", "token-one", one.TenantID, "POST", map[string]any{
		"workspace_id": one.WorkspaceID, "memory_type": "semantic", "content": "My interpretation of the cited passage.", "transformation": "interpretation",
		"evidence": []map[string]any{{"source_id": querySourceID, "source_version": 1, "passage_id": "passage", "citation_id": "citation"}},
	}, nil)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("proposal create status=%d body=%s", response.StatusCode, readBody(response))
	}
	var proposalEnvelope struct {
		Data review.Proposal `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&proposalEnvelope); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	response = requestHTTP(t, server.URL+"/v1/memory-proposals/"+proposalEnvelope.Data.ID, "token-one", one.TenantID, "PATCH", map[string]any{"content": "My edited and reviewed interpretation.", "transformation": "user_edit"}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("proposal edit status=%d body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()
	response = requestHTTP(t, server.URL+"/v1/memory-proposals/"+proposalEnvelope.Data.ID+"/accept", "token-one", one.TenantID, "POST", map[string]any{}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("proposal accept status=%d body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()
	uploadBytes := []byte("%PDF-1.7\nprivate test document")
	uploadHash := sha256.Sum256(uploadBytes)
	uploadRequest := map[string]any{"workspace_id": one.WorkspaceID, "filename": "book.pdf", "media_type": "application/pdf", "size_bytes": len(uploadBytes), "checksum_sha256": fmt.Sprintf("%x", uploadHash), "rights_basis": "lawfully_acquired_private_use"}
	response = requestHTTP(t, server.URL+"/v1/sources/uploads", "token-one", one.TenantID, "POST", uploadRequest, nil)
	if response.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("unattested upload grant status=%d body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()

	policy := attestation.CurrentPolicy()
	statementIDs := make([]string, 0, len(policy.Statements))
	for _, statement := range policy.Statements {
		statementIDs = append(statementIDs, statement.ID)
	}
	response = requestHTTP(t, server.URL+"/v1/attestations/rights", "token-one", one.TenantID, "POST", map[string]any{
		"policy_version": policy.Version, "accepted_statement_ids": statementIDs,
	}, map[string]string{"Idempotency-Key": "attestation-key-0001"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("attestation status = %d, body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()
	portable := exportservice.Bundle{
		Format: "agent-memory-portable", Version: "2.0", MinReaderVersion: "2.0", ExportedAt: time.Now().UTC(),
		Memories: []map[string]any{{"id": "api-portable-memory", "type": "semantic", "content": "Imported through the hosted portable API."}},
		Notes:    []map[string]any{}, Sources: []map[string]any{}, SourceVersions: []map[string]any{}, Lineage: []map[string]any{}, Attestations: []map[string]any{}, Policies: []map[string]any{}, SourceObjects: []exportservice.SourceObject{},
	}
	if err := portable.SealManifest(); err != nil {
		t.Fatal(err)
	}
	portableJSON, _ := json.Marshal(portable)
	portableEncrypted, err := exportservice.EncryptPortable("api portable passphrase", portableJSON)
	if err != nil {
		t.Fatal(err)
	}
	portableRequest, err := http.NewRequest(http.MethodPost, server.URL+"/v1/imports", bytes.NewReader(portableEncrypted))
	if err != nil {
		t.Fatal(err)
	}
	portableRequest.Header.Set("Authorization", "Bearer token-one")
	portableRequest.Header.Set("X-Agent-Memory-Tenant", one.TenantID)
	portableRequest.Header.Set("X-Agent-Memory-Workspace", one.WorkspaceID)
	portableRequest.Header.Set("X-Agent-Memory-Bundle-Passphrase", "api portable passphrase")
	portableRequest.Header.Set("Idempotency-Key", "api-portable-import-0001")
	portableResponse, err := http.DefaultClient.Do(portableRequest)
	if err != nil {
		t.Fatal(err)
	}
	if portableResponse.StatusCode != http.StatusOK {
		t.Fatalf("portable import status=%d body=%s", portableResponse.StatusCode, readBody(portableResponse))
	}
	var portableEnvelope struct {
		Data importer.Result `json:"data"`
	}
	if err := json.NewDecoder(portableResponse.Body).Decode(&portableEnvelope); err != nil {
		t.Fatal(err)
	}
	_ = portableResponse.Body.Close()
	response = requestHTTP(t, server.URL+"/v1/imports/"+portableEnvelope.Data.ID, "token-one", one.TenantID, "GET", nil, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("portable import report status=%d body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()
	response = requestHTTP(t, server.URL+"/v1/sources/uploads", "token-one", one.TenantID, "POST", uploadRequest, nil)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("upload grant status=%d body=%s", response.StatusCode, readBody(response))
	}
	var grantEnvelope struct {
		Data sourceservice.Grant `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&grantEnvelope); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	uploadHTTP, err := http.NewRequest(http.MethodPut, server.URL+grantEnvelope.Data.UploadPath, bytes.NewReader(uploadBytes))
	if err != nil {
		t.Fatal(err)
	}
	uploadHTTP.Header.Set("Content-Type", "application/pdf")
	uploadResponse, err := http.DefaultClient.Do(uploadHTTP)
	if err != nil {
		t.Fatal(err)
	}
	if uploadResponse.StatusCode != http.StatusOK {
		t.Fatalf("source upload status=%d body=%s", uploadResponse.StatusCode, readBody(uploadResponse))
	}
	_ = uploadResponse.Body.Close()
	replayHTTP, err := http.NewRequest(http.MethodPut, server.URL+grantEnvelope.Data.UploadPath, bytes.NewReader(uploadBytes))
	if err != nil {
		t.Fatal(err)
	}
	replayHTTP.Header.Set("Content-Type", "application/pdf")
	replayResponse, err := http.DefaultClient.Do(replayHTTP)
	if err != nil {
		t.Fatal(err)
	}
	if replayResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("upload replay status=%d", replayResponse.StatusCode)
	}
	_ = replayResponse.Body.Close()
	var sourceState string
	if err := pool.QueryRow(ctx, "SELECT state FROM saas_sources WHERE id=$1", grantEnvelope.Data.SourceID).Scan(&sourceState); err != nil || sourceState != "validating" {
		t.Fatalf("uploaded source state=%s err=%v", sourceState, err)
	}

	response = requestHTTP(t, server.URL+"/v1/credentials", "token-one", one.TenantID, "POST", map[string]any{
		"label": "test agent", "scopes": []string{"memory:write"}, "expires_at": time.Now().UTC().Add(time.Hour),
	}, nil)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("credential creation status = %d, body=%s", response.StatusCode, readBody(response))
	}
	var issuedEnvelope struct {
		Data credential.Issued `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&issuedEnvelope); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	response = requestHTTP(t, server.URL+"/v1/memories", issuedEnvelope.Data.Secret, one.TenantID, "POST", map[string]any{
		"workspace_id": one.WorkspaceID, "type": "semantic", "content": "A scoped agent fact.",
		"source": map[string]any{"type": "agent_observation"},
	}, map[string]string{"Idempotency-Key": "memory-write-key-agent"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("agent credential write status = %d, body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()
	response = requestHTTP(t, server.URL+"/v1/current-credential", issuedEnvelope.Data.Secret, one.TenantID, "DELETE", nil, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("agent credential self-revoke status=%d body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()
	response = requestHTTP(t, server.URL+"/v1/memories", issuedEnvelope.Data.Secret, one.TenantID, "POST", map[string]any{
		"workspace_id": one.WorkspaceID, "type": "semantic", "content": "This revoked credential must fail.", "source": map[string]any{"type": "agent_observation"},
	}, map[string]string{"Idempotency-Key": "revoked-memory-write-1"})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked agent credential status=%d body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()

	response = requestHTTP(t, server.URL+"/v1/memories", "token-one", one.TenantID, "POST", map[string]any{
		"workspace_id": one.WorkspaceID, "type": "semantic", "content": "A tenant-private fact.",
		"source": map[string]any{"type": "user_input"}, "keywords": []string{"private"},
	}, map[string]string{"Idempotency-Key": "memory-write-key-0001"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("memory write status = %d, body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", one.TenantID); err != nil {
		t.Fatal(err)
	}
	var receiptID string
	if err = tx.QueryRow(ctx, "SELECT id::text FROM saas_attestation_receipts WHERE tenant_id=$1 ORDER BY accepted_at DESC LIMIT 1", one.TenantID).Scan(&receiptID); err != nil {
		t.Fatal(err)
	}
	sourceID := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO saas_sources(tenant_id,id,workspace_id,state,rights_basis,attestation_receipt_id,active_version,created_at,updated_at) VALUES($1,$2,$3,'ready','lawfully_acquired_private_use',$4,1,clock_timestamp(),clock_timestamp())`, one.TenantID, sourceID, one.WorkspaceID, receiptID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO saas_source_versions(tenant_id,source_id,version,content_sha256,media_type,parser_version,normalization_version,vault_object_key,published_at,created_at) VALUES($1,$2,1,$3,'application/pdf','pdf-v1','text-v1','private/vault/secret-key',clock_timestamp(),clock_timestamp())`, one.TenantID, sourceID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO saas_lineage_edges(tenant_id,id,from_type,from_id,to_type,to_id,transformation,transformation_version,created_at) VALUES($1,$2,'source',$3,'source',$3,'extract','v1',clock_timestamp())`, one.TenantID, uuid.NewString(), sourceID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	response = requestHTTP(t, server.URL+"/v1/sources?workspace_id="+one.WorkspaceID, "token-one", one.TenantID, "GET", nil, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("source list status=%d body=%s", response.StatusCode, readBody(response))
	}
	var sourceListEnvelope struct {
		Data []sourceservice.SourceView `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&sourceListEnvelope); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if len(sourceListEnvelope.Data) != 2 || sourceListEnvelope.Data[0].Attestation.PolicyVersion != policy.Version || sourceListEnvelope.Data[0].RetentionState != "retained_private_vault" {
		t.Fatalf("source list=%+v", sourceListEnvelope.Data)
	}
	encodedSources, _ := json.Marshal(sourceListEnvelope.Data)
	if bytes.Contains(encodedSources, []byte("private/vault/secret-key")) {
		t.Fatal("source details leaked the vault object key")
	}
	response = requestHTTP(t, server.URL+"/v1/sources/"+sourceID, "token-two", two.TenantID, "GET", nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant source detail status=%d body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", one.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE saas_sources SET state='failed' WHERE tenant_id=$1 AND id=$2`, one.TenantID, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO saas_jobs(tenant_id,id,job_type,subject_type,subject_id,deterministic_key,state,attempts,available_at,finished_at,safe_error_code,created_at,updated_at)
		VALUES($1,$2,'source.extract','source',$3,$4,'failed',1,clock_timestamp(),clock_timestamp(),'extraction_failed',clock_timestamp(),clock_timestamp())`, one.TenantID, uuid.NewString(), sourceID, "source:"+sourceID+":version:1:extract"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	response = requestHTTP(t, server.URL+"/v1/sources/"+sourceID, "token-one", one.TenantID, "GET", nil, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("failed source detail status=%d body=%s", response.StatusCode, readBody(response))
	}
	var failedSourceEnvelope struct {
		Data sourceservice.SourceView `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&failedSourceEnvelope); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if !failedSourceEnvelope.Data.Failure.RetryAllowed || failedSourceEnvelope.Data.Failure.Code != "extraction_failed" {
		t.Fatalf("failed source guidance=%+v", failedSourceEnvelope.Data.Failure)
	}
	response = requestHTTP(t, server.URL+"/v1/sources/"+sourceID+"/retry", "token-one", one.TenantID, "POST", nil, nil)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("source retry status=%d body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()
	var retryJobState string
	var retryAudit, retryOutbox int
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", one.TenantID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT
		(SELECT state FROM saas_sources WHERE id=$1),
		(SELECT state FROM saas_jobs WHERE subject_id=$1 AND job_type='source.extract'),
		(SELECT count(*) FROM saas_audit_events WHERE target_id=$1::text AND operation='source.retry_extraction'),
		(SELECT count(*) FROM saas_outbox WHERE aggregate_id=$1 AND event_type='source.extraction_retry_requested')`, sourceID).Scan(&sourceState, &retryJobState, &retryAudit, &retryOutbox); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	if sourceState != "processing" || retryJobState != "queued" || retryAudit != 1 || retryOutbox != 1 {
		t.Fatalf("retry state source=%s job=%s audit=%d outbox=%d", sourceState, retryJobState, retryAudit, retryOutbox)
	}

	response = requestHTTP(t, server.URL+"/v1/exports", "token-one", one.TenantID, "POST", map[string]any{"workspace_id": one.WorkspaceID}, nil)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("export request status=%d body=%s", response.StatusCode, readBody(response))
	}
	var exportEnvelope struct {
		Data exportservice.Operation `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&exportEnvelope); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if completed, err := exports.ProcessOnce(ctx); err != nil || completed != 1 {
		t.Fatalf("export processing completed=%d err=%v", completed, err)
	}
	for _, encrypted := range objects.values {
		if bytes.Contains(encrypted, []byte("A tenant-private fact.")) {
			t.Fatal("object storage contains plaintext export content")
		}
	}
	response = requestHTTP(t, server.URL+"/v1/exports/"+exportEnvelope.Data.ID+"/download", "token-one", one.TenantID, "GET", nil, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("export download status=%d body=%s", response.StatusCode, readBody(response))
	}
	var bundle exportservice.Bundle
	if err := json.NewDecoder(response.Body).Decode(&bundle); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if bundle.TenantID != one.TenantID || len(bundle.Memories) != 3 || len(bundle.Sources) != 2 || len(bundle.SourceVersions) != 1 || len(bundle.Lineage) != 1 {
		t.Fatalf("export bundle=%+v", bundle)
	}
	bundleJSON, _ := json.Marshal(bundle)
	if bytes.Contains(bundleJSON, []byte("private/vault/secret-key")) {
		t.Fatal("export leaked internal vault object key")
	}
	response = requestHTTP(t, server.URL+"/v1/exports/"+exportEnvelope.Data.ID+"/download", "token-two", two.TenantID, "GET", nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant export status=%d body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()
	var objectKey string
	var original []byte
	for key, value := range objects.values {
		objectKey = key
		original = append([]byte(nil), value...)
	}
	objects.values[objectKey][0] ^= 0xff
	response = requestHTTP(t, server.URL+"/v1/exports/"+exportEnvelope.Data.ID+"/download", "token-one", one.TenantID, "GET", nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("tampered export status=%d", response.StatusCode)
	}
	_ = response.Body.Close()
	objects.values[objectKey] = original
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", one.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "UPDATE saas_exports SET expires_at=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND id=$2", one.TenantID, exportEnvelope.Data.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	response = requestHTTP(t, server.URL+"/v1/exports/"+exportEnvelope.Data.ID+"/download", "token-one", one.TenantID, "GET", nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("expired export status=%d", response.StatusCode)
	}
	_ = response.Body.Close()

	response = requestHTTP(t, server.URL+"/v1/whoami", "token-two", one.TenantID, "GET", nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant selection status = %d, want 404; body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()

	response = requestHTTP(t, server.URL+"/v1/memories", "token-two", two.TenantID, "POST", map[string]any{
		"workspace_id": one.WorkspaceID, "type": "semantic", "content": "Cross-tenant attempt.",
		"source": map[string]any{"type": "user_input"},
	}, map[string]string{"Idempotency-Key": "memory-write-key-0002"})
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant workspace status = %d, want 404; body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()
}

func signupHTTP(t *testing.T, baseURL, token string) control.PersonalAccount {
	t.Helper()
	response := requestHTTP(t, baseURL+"/v1/signup", token, "", "POST", map[string]any{}, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("signup status = %d, body=%s", response.StatusCode, readBody(response))
	}
	var envelope struct {
		Data control.PersonalAccount `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

func requestHTTP(t *testing.T, url, token, tenantID, method string, body any, headers map[string]string) *http.Response {
	t.Helper()
	var encoded bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequest(method, url, &encoded)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	if tenantID != "" {
		request.Header.Set("X-Agent-Memory-Tenant", tenantID)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func readBody(response *http.Response) string {
	var value any
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		return err.Error()
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
