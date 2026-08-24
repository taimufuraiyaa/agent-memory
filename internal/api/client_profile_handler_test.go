package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/clientprofile"
)

type clientProfileEnvelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestClientProfileAPIContract(t *testing.T) {
	store, err := clientprofile.Open(t.TempDir(), func() time.Time {
		return time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	server := httptest.NewServer(NewMux(&Service{ClientProfiles: store}))
	defer server.Close()

	created := requestClientProfile(t, http.MethodPost, server.URL+"/api/v1/client-profiles", map[string]any{
		"id":           "codex-main",
		"display_name": "Codex Desktop",
		"client_kind":  "codex",
		"tool_profile": "default",
	}, http.StatusCreated)
	if created.ID != "codex-main" || created.Revision != 1 {
		t.Fatalf("unexpected create response: %#v", created)
	}

	got := requestClientProfile(t, http.MethodGet, server.URL+"/api/v1/client-profiles/codex-main", nil, http.StatusOK)
	if got != created {
		t.Fatalf("get response = %#v, want %#v", got, created)
	}

	profiles := requestClientProfileList(t, server.URL+"/api/v1/client-profiles", http.StatusOK)
	if len(profiles) != 1 || profiles[0] != created {
		t.Fatalf("unexpected list response: %#v", profiles)
	}

	updated := requestClientProfile(t, http.MethodPut, server.URL+"/api/v1/client-profiles/codex-main", map[string]any{
		"display_name":      "Codex Expanded",
		"client_kind":       "codex",
		"tool_profile":      "expanded",
		"expected_revision": created.Revision,
	}, http.StatusOK)
	if updated.Revision != 2 || updated.ToolProfile != clientprofile.ProfileExpanded {
		t.Fatalf("unexpected update response: %#v", updated)
	}

	requestClientProfileDelete(t, server.URL+"/api/v1/client-profiles/codex-main?expected_revision="+strconv.FormatInt(updated.Revision, 10), http.StatusOK)
	requestClientProfileError(t, http.MethodGet, server.URL+"/api/v1/client-profiles/codex-main", nil, http.StatusNotFound, "client_profile_not_found")
}

func TestClientProfileAPIErrors(t *testing.T) {
	store, err := clientprofile.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	server := httptest.NewServer(NewMux(&Service{ClientProfiles: store}))
	defer server.Close()

	requestClientProfileError(t, http.MethodPost, server.URL+"/api/v1/client-profiles", map[string]any{
		"id": "UPPER", "display_name": "Bad", "client_kind": "codex", "tool_profile": "default",
	}, http.StatusBadRequest, "client_profile_validation")
	requestClientProfileError(t, http.MethodGet, server.URL+"/api/v1/client-profiles/nested/path", nil, http.StatusBadRequest, "client_profile_path")
	requestClientProfileError(t, http.MethodPatch, server.URL+"/api/v1/client-profiles", nil, http.StatusMethodNotAllowed, "method_not_allowed")

	created := requestClientProfile(t, http.MethodPost, server.URL+"/api/v1/client-profiles", map[string]any{
		"id": "cursor", "display_name": "Cursor", "client_kind": "cursor", "tool_profile": "default",
	}, http.StatusCreated)
	requestClientProfileError(t, http.MethodPut, server.URL+"/api/v1/client-profiles/cursor", map[string]any{
		"display_name": "Cursor", "client_kind": "cursor", "tool_profile": "expanded", "expected_revision": created.Revision + 1,
	}, http.StatusConflict, "client_profile_revision_conflict")
}

func TestClientProfileAPIUnavailable(t *testing.T) {
	server := httptest.NewServer(NewMux(&Service{}))
	defer server.Close()
	requestClientProfileError(t, http.MethodGet, server.URL+"/api/v1/client-profiles", nil, http.StatusServiceUnavailable, "client_profiles_unavailable")
}

func requestClientProfile(t *testing.T, method, url string, body any, wantStatus int) clientprofile.Profile {
	t.Helper()
	envelope := requestClientProfileEnvelope(t, method, url, body, wantStatus)
	var result struct {
		Profile clientprofile.Profile `json:"profile"`
	}
	if err := json.Unmarshal(envelope.Data, &result); err != nil {
		t.Fatalf("decode profile response: %v", err)
	}
	return result.Profile
}

func requestClientProfileList(t *testing.T, url string, wantStatus int) []clientprofile.Profile {
	t.Helper()
	envelope := requestClientProfileEnvelope(t, http.MethodGet, url, nil, wantStatus)
	var result struct {
		Profiles []clientprofile.Profile `json:"profiles"`
	}
	if err := json.Unmarshal(envelope.Data, &result); err != nil {
		t.Fatalf("decode profile list: %v", err)
	}
	return result.Profiles
}

func requestClientProfileDelete(t *testing.T, url string, wantStatus int) {
	t.Helper()
	requestClientProfileEnvelope(t, http.MethodDelete, url, nil, wantStatus)
}

func requestClientProfileError(t *testing.T, method, url string, body any, wantStatus int, wantCode string) {
	t.Helper()
	envelope := requestClientProfileEnvelope(t, method, url, body, wantStatus)
	if envelope.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q (message %q)", envelope.Error.Code, wantCode, envelope.Error.Message)
	}
}

func requestClientProfileEnvelope(t *testing.T, method, url string, body any, wantStatus int) clientProfileEnvelope {
	t.Helper()
	var requestBody *bytes.Reader
	if body == nil {
		requestBody = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request: %v", err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, requestBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("content-type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("perform request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d", response.StatusCode, wantStatus)
	}
	var envelope clientProfileEnvelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}
