package api

import "net/http"

const (
	integrationContractVersion  = "v1"
	maxIntegrationRequestBytes  = 1 << 20
	maxIntegrationResponseBytes = 256 << 10
)

var coreIntegrationOperations = []string{
	"health",
	"write",
	"search",
	"recall",
	"feedback",
	"sessions",
	"session_end",
}

func capabilitiesHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		writeOK(w, http.StatusOK, map[string]any{
			"contract_version": integrationContractVersion,
			"operations":       coreIntegrationOperations,
			"result_formats":   []string{"compact", "full"},
			"limits": map[string]int{
				"max_request_bytes":  maxIntegrationRequestBytes,
				"max_response_bytes": maxIntegrationResponseBytes,
			},
			"features": map[string]bool{
				"request_ids":          true,
				"score_explanations":   true,
				"clipping_metadata":    true,
				"observation_sessions": true,
			},
		})
	}
}
