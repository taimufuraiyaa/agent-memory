package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/deploymentprofile"
)

type deploymentProfileUpdateRequest struct {
	MonthlyInfrastructureOperationsBudgetUSD int64  `json:"monthly_infrastructure_operations_budget_usd"`
	DecisionStatus                           string `json:"decision_status"`
	ExpectedRevision                         int64  `json:"expected_revision"`
}

func ConfigureLocalDeploymentProfile(svc *Service) error {
	if svc == nil {
		return errors.New("service is required")
	}
	store, err := deploymentprofile.Open(svc.BaseDir, time.Now)
	if err != nil {
		return err
	}
	svc.DeploymentProfile = store
	return nil
}

func deploymentProfileHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil || svc.DeploymentProfile == nil {
			writeErr(w, http.StatusServiceUnavailable, "deployment_profile_unavailable", "deployment profile is not configured")
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeOK(w, http.StatusOK, map[string]any{"profile": svc.DeploymentProfile.Get()})
		case http.MethodPut:
			var request deploymentProfileUpdateRequest
			if !decodeDeploymentProfileJSON(w, r, &request) {
				return
			}
			profile, err := svc.DeploymentProfile.Update(request.ExpectedRevision, deploymentprofile.Input{
				MonthlyInfrastructureOperationsBudgetUSD: request.MonthlyInfrastructureOperationsBudgetUSD,
				DecisionStatus:                           request.DecisionStatus,
			})
			if err != nil {
				writeDeploymentProfileError(w, err)
				return
			}
			writeOK(w, http.StatusOK, map[string]any{"profile": profile})
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		}
	}
}

func decodeDeploymentProfileJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeErr(w, http.StatusBadRequest, "deployment_profile_validation", "invalid deployment profile request")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "deployment_profile_validation", "request must contain one JSON object")
		return false
	}
	return true
}

func writeDeploymentProfileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, deploymentprofile.ErrValidation):
		writeErr(w, http.StatusBadRequest, "deployment_profile_validation", err.Error())
	case errors.Is(err, deploymentprofile.ErrRevisionConflict):
		writeErr(w, http.StatusConflict, "deployment_profile_revision_conflict", "deployment profile changed; reload and try again")
	default:
		writeErr(w, http.StatusServiceUnavailable, "deployment_profile_unavailable", "deployment profile is temporarily unavailable")
	}
}
