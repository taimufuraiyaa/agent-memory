package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/attestation"
)

type rightsAttestationAcceptRequest struct {
	PolicyVersion        string   `json:"policy_version"`
	AcceptedStatementIDs []string `json:"accepted_statement_ids"`
}

func rightsAttestationStatusHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		subjectID, ok := resolveRightsSubject(w, r, svc)
		if !ok {
			return
		}
		status, err := svc.RightsAttestation.Status(r.Context(), subjectID)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "rights_attestation_unavailable", "rights attestation status is temporarily unavailable")
			return
		}
		writeOK(w, http.StatusOK, status)
	}
}

func rightsAttestationAcceptHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		subjectID, ok := resolveRightsSubject(w, r, svc)
		if !ok {
			return
		}
		var request rightsAttestationAcceptRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "invalid rights attestation request")
			return
		}
		status, err := svc.RightsAttestation.Accept(r.Context(), subjectID, attestation.AcceptCommand{
			PolicyVersion: request.PolicyVersion, AcceptedStatementIDs: request.AcceptedStatementIDs,
			RequestID: strings.TrimSpace(r.Header.Get("X-Request-ID")), UserAgent: boundedHeader(r.UserAgent(), 512),
		})
		if err == nil {
			writeOK(w, http.StatusOK, status)
			return
		}
		code, message, statusCode := "rights_attestation_failed", "rights attestation could not be recorded", http.StatusBadRequest
		switch {
		case errors.Is(err, attestation.ErrPolicyVersion):
			code, message, statusCode = "rights_policy_changed", "the rights policy changed; reload and review the current version", http.StatusConflict
		case errors.Is(err, attestation.ErrIncompleteAcceptance):
			code, message = "incomplete_rights_attestation", "every required rights statement must be accepted"
		default:
			statusCode = http.StatusServiceUnavailable
		}
		_ = svc.RightsAttestation.RecordDecision(r.Context(), attestation.AuditEvent{
			SubjectID: subjectID, Operation: "rights_attestation_accept", Outcome: "rejected",
			PolicyVersion: request.PolicyVersion, RequestID: strings.TrimSpace(r.Header.Get("X-Request-ID")), Reason: code,
		})
		writeErr(w, statusCode, code, message)
	}
}

func resolveRightsSubject(w http.ResponseWriter, r *http.Request, svc *Service) (string, bool) {
	if svc == nil || svc.RightsAttestation == nil {
		writeErr(w, http.StatusServiceUnavailable, "rights_attestation_unavailable", "rights attestation is not configured")
		return "", false
	}
	if svc.RightsSubjectResolver == nil {
		writeErr(w, http.StatusServiceUnavailable, "identity_unavailable", "account identity is unavailable")
		return "", false
	}
	subjectID, err := svc.RightsSubjectResolver(r)
	if err != nil || strings.TrimSpace(subjectID) == "" {
		writeErr(w, http.StatusServiceUnavailable, "identity_unavailable", "account identity is unavailable")
		return "", false
	}
	return strings.TrimSpace(subjectID), true
}

func requireActiveRightsAttestation(w http.ResponseWriter, r *http.Request, svc *Service) (attestation.Receipt, string, bool) {
	if svc == nil || svc.RightsAttestation == nil {
		return attestation.Receipt{}, "", true
	}
	subjectID, ok := resolveRightsSubject(w, r, svc)
	if !ok {
		return attestation.Receipt{}, "", false
	}
	status, err := svc.RightsAttestation.Status(r.Context(), subjectID)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "rights_attestation_unavailable", "rights attestation status is temporarily unavailable")
		return attestation.Receipt{}, "", false
	}
	if status.State == attestation.StatusActive && status.Receipt != nil {
		return *status.Receipt, subjectID, true
	}
	_ = svc.RightsAttestation.RecordDecision(r.Context(), attestation.AuditEvent{
		SubjectID: subjectID, Operation: "source_upload", Outcome: "blocked", PolicyVersion: status.Policy.Version,
		RequestID: strings.TrimSpace(r.Header.Get("X-Request-ID")), Reason: string(status.Reason),
	})
	writeErrDetails(w, http.StatusPreconditionRequired, "rights_attestation_required", "review and accept the current rights policy before uploading sources", map[string]any{
		"policy_version": status.Policy.Version,
		"reason":         status.Reason,
	})
	return attestation.Receipt{}, "", false
}

func boundedHeader(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
