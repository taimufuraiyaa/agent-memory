package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/deploymentprofile"
)

type deploymentProfileEnvelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestDeploymentProfileAPIReadsDefaultsAndUpdatesWithRevision(t *testing.T) {
	store, err := deploymentprofile.Open(t.TempDir(), func() time.Time {
		return time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewMux(&Service{DeploymentProfile: store}))
	defer server.Close()

	initial := requestDeploymentProfile(t, http.MethodGet, server.URL+"/api/v1/deployment-profile", nil, http.StatusOK)
	if initial.MonthlyInfrastructureOperationsBudgetUSD != 1_000 || initial.DecisionStatus != deploymentprofile.StatusAssumed || initial.Revision != 1 {
		t.Fatalf("unexpected API defaults: %+v", initial)
	}
	updated := requestDeploymentProfile(t, http.MethodPut, server.URL+"/api/v1/deployment-profile", map[string]any{
		"monthly_infrastructure_operations_budget_usd": 750,
		"decision_status":   "operator_confirmed",
		"expected_revision": initial.Revision,
	}, http.StatusOK)
	if updated.Revision != 2 || updated.MonthlyInfrastructureOperationsBudgetUSD != 750 || updated.DecisionStatus != deploymentprofile.StatusOperatorConfirmed {
		t.Fatalf("unexpected update response: %+v", updated)
	}
	requestDeploymentProfileError(t, http.MethodPut, server.URL+"/api/v1/deployment-profile", map[string]any{
		"monthly_infrastructure_operations_budget_usd": 1_000,
		"decision_status": "assumed", "expected_revision": 1,
	}, http.StatusConflict, "deployment_profile_revision_conflict")
}

func TestDeploymentProfileAPIRejectsInvalidPayloadAndMethods(t *testing.T) {
	store, err := deploymentprofile.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewMux(&Service{DeploymentProfile: store}))
	defer server.Close()

	requestDeploymentProfileError(t, http.MethodPut, server.URL+"/api/v1/deployment-profile", map[string]any{
		"monthly_infrastructure_operations_budget_usd": -1,
		"decision_status": "assumed", "expected_revision": 1,
	}, http.StatusBadRequest, "deployment_profile_validation")
	requestDeploymentProfileError(t, http.MethodPut, server.URL+"/api/v1/deployment-profile", map[string]any{
		"monthly_infrastructure_operations_budget_usd": 1_000,
		"decision_status": "assumed", "expected_revision": 1, "cloud_provider": "aws",
	}, http.StatusBadRequest, "deployment_profile_validation")
	requestDeploymentProfileError(t, http.MethodPost, server.URL+"/api/v1/deployment-profile", nil, http.StatusMethodNotAllowed, "method_not_allowed")
}

func TestDeploymentProfileAPIUnavailable(t *testing.T) {
	server := httptest.NewServer(NewMux(&Service{}))
	defer server.Close()
	requestDeploymentProfileError(t, http.MethodGet, server.URL+"/api/v1/deployment-profile", nil, http.StatusServiceUnavailable, "deployment_profile_unavailable")
}

func requestDeploymentProfile(t *testing.T, method, url string, body any, wantStatus int) deploymentprofile.Profile {
	t.Helper()
	envelope := requestDeploymentProfileEnvelope(t, method, url, body, wantStatus)
	for _, forbidden := range [][]byte{[]byte("cloud_provider"), []byte("paid_infrastructure_authorized"), []byte("monthly_staging_budget_usd")} {
		if bytes.Contains(envelope.Data, forbidden) {
			t.Fatalf("deployment profile response contains legacy field %q: %s", forbidden, envelope.Data)
		}
	}
	var result struct {
		Profile deploymentprofile.Profile `json:"profile"`
	}
	if err := json.Unmarshal(envelope.Data, &result); err != nil {
		t.Fatalf("decode profile response: %v", err)
	}
	return result.Profile
}

func requestDeploymentProfileError(t *testing.T, method, url string, body any, wantStatus int, wantCode string) {
	t.Helper()
	envelope := requestDeploymentProfileEnvelope(t, method, url, body, wantStatus)
	if envelope.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q (message %q)", envelope.Error.Code, wantCode, envelope.Error.Message)
	}
}

func requestDeploymentProfileEnvelope(t *testing.T, method, url string, body any, wantStatus int) deploymentProfileEnvelope {
	t.Helper()
	var content []byte
	if body != nil {
		var err error
		content, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequest(method, url, bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("content-type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d", response.StatusCode, wantStatus)
	}
	var envelope deploymentProfileEnvelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	return envelope
}
