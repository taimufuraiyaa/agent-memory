package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/attestation"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
)

func TestRightsAttestationStatusAcceptAndRenewalContract(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 5, 9, 0, 0, 0, time.UTC)
	store, err := attestation.OpenSQLiteStore(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := &Service{
		RightsAttestation: attestation.NewService(store, attestation.WithClock(func() time.Time { return now })),
		RightsSubjectResolver: func(*http.Request) (string, error) {
			return "account-1", nil
		},
	}
	mux := NewMux(svc)

	missing := attestationRequest(t, mux, http.MethodGet, "/api/v1/rights-attestation/status", nil, http.StatusOK)
	if missing["status"] != "required" || missing["reason"] != "missing" {
		t.Fatalf("unexpected missing status: %+v", missing)
	}
	policy := missing["policy"].(map[string]any)
	statements := policy["statements"].([]any)
	acceptedIDs := make([]string, 0, len(statements))
	for _, value := range statements {
		acceptedIDs = append(acceptedIDs, value.(map[string]any)["id"].(string))
	}

	incomplete := attestationRequestRaw(t, mux, http.MethodPost, "/api/v1/rights-attestation/accept", map[string]any{
		"policy_version": policy["version"], "accepted_statement_ids": acceptedIDs[:len(acceptedIDs)-1],
	})
	if incomplete.Code != http.StatusBadRequest || incomplete.ErrorCode != "incomplete_rights_attestation" {
		t.Fatalf("unexpected incomplete response: %+v", incomplete)
	}

	active := attestationRequest(t, mux, http.MethodPost, "/api/v1/rights-attestation/accept", map[string]any{
		"policy_version": policy["version"], "accepted_statement_ids": acceptedIDs,
	}, http.StatusOK)
	if active["status"] != "active" {
		t.Fatalf("unexpected active status: %+v", active)
	}

	now = now.Add(30 * 24 * time.Hour)
	expired := attestationRequest(t, mux, http.MethodGet, "/api/v1/rights-attestation/status", nil, http.StatusOK)
	if expired["status"] != "expired" || expired["reason"] != "expired" {
		t.Fatalf("unexpected expired status: %+v", expired)
	}
}

func TestRightsAttestationRejectsObsoletePolicyAndResolverFailure(t *testing.T) {
	store, err := attestation.OpenSQLiteStore(context.Background(), filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	resolverError := NewMux(&Service{
		RightsAttestation: attestation.NewService(store),
		RightsSubjectResolver: func(*http.Request) (string, error) {
			return "", errors.New("identity unavailable")
		},
	})
	failed := attestationRequestRaw(t, resolverError, http.MethodGet, "/api/v1/rights-attestation/status", nil)
	if failed.Code != http.StatusServiceUnavailable || failed.ErrorCode != "identity_unavailable" {
		t.Fatalf("unexpected resolver failure: %+v", failed)
	}

	working := NewMux(&Service{
		RightsAttestation:     attestation.NewService(store),
		RightsSubjectResolver: func(*http.Request) (string, error) { return "account-1", nil },
	})
	obsolete := attestationRequestRaw(t, working, http.MethodPost, "/api/v1/rights-attestation/accept", map[string]any{
		"policy_version": "obsolete", "accepted_statement_ids": []string{"anything"},
	})
	if obsolete.Code != http.StatusConflict || obsolete.ErrorCode != "rights_policy_changed" {
		t.Fatalf("unexpected obsolete policy response: %+v", obsolete)
	}
}

func TestConfigureLocalRightsAttestationUsesOneAccountAcrossWorkspaces(t *testing.T) {
	svc := &Service{BaseDir: t.TempDir()}
	if err := ConfigureLocalRightsAttestation(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	first, err := svc.RightsSubjectResolver(httptest.NewRequest(http.MethodGet, "/?workspace=one", nil))
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.RightsSubjectResolver(httptest.NewRequest(http.MethodGet, "/?workspace=two", nil))
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second {
		t.Fatalf("local account identity must be stable across workspaces: first=%q second=%q", first, second)
	}
}

func TestLibraryImportRequiresRightsAttestationAndRecordsProvenance(t *testing.T) {
	t.Setenv("AGENT_MEMORY_LIBRARY_ENABLED", "true")
	ctx := context.Background()
	root := t.TempDir()
	modelDir := filepath.Join(root, "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatal(err)
	}
	control, err := attestation.OpenSQLiteStore(ctx, filepath.Join(root, "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close() })
	now := time.Date(2026, time.August, 5, 9, 0, 0, 0, time.UTC)
	service := &Service{
		Workspace: "books", BaseDir: root, EmbeddingProvider: provider,
		RightsAttestation:     attestation.NewService(control, attestation.WithClock(func() time.Time { return now })),
		RightsSubjectResolver: func(*http.Request) (string, error) { return "account-1", nil },
	}
	mux := NewMux(service)

	blocked := attestationRequestRaw(t, mux, http.MethodPost, "/api/v1/library/imports", map[string]any{
		"workspace": "books", "title": "Private Book", "edition_label": "First", "language": "en",
		"rights_basis": "author_owned", "markdown": "# Start\nA retained private source.",
	})
	if blocked.Code != http.StatusPreconditionRequired || blocked.ErrorCode != "rights_attestation_required" {
		t.Fatalf("unexpected blocked import: %+v", blocked)
	}
	if blocked.Details["policy_version"] != attestation.CurrentPolicy().Version || blocked.Details["reason"] != "missing" {
		t.Fatalf("blocked import omitted current policy details: %+v", blocked.Details)
	}

	policy := attestation.CurrentPolicy()
	acceptedIDs := make([]string, 0, len(policy.Statements))
	for _, statement := range policy.Statements {
		acceptedIDs = append(acceptedIDs, statement.ID)
	}
	accepted := attestationRequest(t, mux, http.MethodPost, "/api/v1/rights-attestation/accept", map[string]any{
		"policy_version": policy.Version, "accepted_statement_ids": acceptedIDs,
	}, http.StatusOK)
	receiptID := accepted["receipt"].(map[string]any)["id"].(string)

	imported := attestationRequest(t, mux, http.MethodPost, "/api/v1/library/imports", map[string]any{
		"workspace": "books", "title": "Private Book", "edition_label": "First", "language": "en",
		"rights_basis": "author_owned", "markdown": "# Start\nA retained private source.",
	}, http.StatusAccepted)
	assetID := imported["result"].(map[string]any)["asset_id"].(string)
	assets, err := service.resolve(ctx, "books")
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := assets.Store.GetSourceAttestation(ctx, assetID)
	if err != nil {
		t.Fatal(err)
	}
	if provenance.ReceiptID != receiptID || provenance.RightsBasis != "author_owned" || provenance.SubjectID != "account-1" {
		t.Fatalf("unexpected source attestation provenance: %+v", provenance)
	}

	now = now.Add(30 * 24 * time.Hour)
	expiredImport := attestationRequestRaw(t, mux, http.MethodPost, "/api/v1/library/imports", map[string]any{
		"workspace": "books", "title": "Second Book", "edition_label": "First", "language": "en",
		"rights_basis": "licensed", "markdown": "# Renewal\nThis upload waits for renewal.",
	})
	if expiredImport.Code != http.StatusPreconditionRequired || expiredImport.ErrorCode != "rights_attestation_required" {
		t.Fatalf("expired receipt did not block import: %+v", expiredImport)
	}

	renewed := attestationRequest(t, mux, http.MethodPost, "/api/v1/rights-attestation/accept", map[string]any{
		"policy_version": policy.Version, "accepted_statement_ids": acceptedIDs,
	}, http.StatusOK)
	if renewed["receipt"].(map[string]any)["id"] == receiptID {
		t.Fatal("renewal must create a new immutable receipt")
	}
	attestationRequest(t, mux, http.MethodPost, "/api/v1/library/imports", map[string]any{
		"workspace": "books", "title": "Second Book", "edition_label": "First", "language": "en",
		"rights_basis": "licensed", "markdown": "# Renewal\nThis upload waits for renewal.",
	}, http.StatusAccepted)
	events, err := control.ListAuditEvents(ctx, "account-1")
	if err != nil {
		t.Fatal(err)
	}
	var acceptedEvents, blockedUploads, successfulUploads int
	for _, event := range events {
		switch {
		case event.Operation == "rights_attestation_accept" && event.Outcome == "success":
			acceptedEvents++
		case event.Operation == "source_upload" && event.Outcome == "blocked":
			blockedUploads++
		case event.Operation == "source_upload" && event.Outcome == "success":
			successfulUploads++
		}
	}
	if acceptedEvents != 2 || blockedUploads != 2 || successfulUploads != 2 {
		t.Fatalf("unexpected consent audit events: accepted=%d blocked=%d successful=%d events=%+v", acceptedEvents, blockedUploads, successfulUploads, events)
	}
}

type attestationHTTPResponse struct {
	Code      int
	Data      map[string]any
	ErrorCode string
	Message   string
	Details   map[string]any
}

func attestationRequest(t *testing.T, handler http.Handler, method, path string, body any, status int) map[string]any {
	t.Helper()
	response := attestationRequestRaw(t, handler, method, path, body)
	if response.Code != status {
		t.Fatalf("%s %s: got %d data=%+v error=%s message=%s", method, path, response.Code, response.Data, response.ErrorCode, response.Message)
	}
	return response.Data
}

func attestationRequestRaw(t *testing.T, handler http.Handler, method, path string, body any) attestationHTTPResponse {
	t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		request.Header.Set("content-type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var envelope struct {
		Data  map[string]any `json:"data"`
		Error *struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response %d %q: %v", recorder.Code, recorder.Body.String(), err)
	}
	response := attestationHTTPResponse{Code: recorder.Code, Data: envelope.Data}
	if envelope.Error != nil {
		response.ErrorCode = envelope.Error.Code
		response.Message = envelope.Error.Message
		response.Details = envelope.Error.Details
	}
	return response
}
