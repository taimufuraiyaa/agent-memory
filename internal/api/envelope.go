package api

import (
	"encoding/json"
	"net/http"
)

const envelopeVersion = "v1"

type envelope struct {
	OK      bool          `json:"ok"`
	Version string        `json:"version"`
	Data    any           `json:"data,omitempty"`
	Error   *errorPayload `json:"error,omitempty"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func writeOK(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{
		OK:      true,
		Version: envelopeVersion,
		Data:    data,
	})
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeErrDetails(w, status, code, message, nil)
}

func writeErrDetails(w http.ResponseWriter, status int, code, message string, details any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{
		OK:      false,
		Version: envelopeVersion,
		Error: &errorPayload{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}
