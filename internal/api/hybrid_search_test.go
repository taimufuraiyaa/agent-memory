package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
)

func TestSearchAPIExposesHybridFallbackAndRequiredFailure(t *testing.T) {
	provider, err := embeddings.NewLocalProvider(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir(), EmbeddingProvider: provider}
	defer svc.Close()
	mux := NewMux(svc)
	for _, required := range []bool{false, true} {
		payload, _ := json.Marshal(map[string]any{"workspace": "ws", "query": "How does pricing depend on runtime?", "graph_mode": "local_graph", "graph_required": required, "top_k": 4})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/memories/search", bytes.NewReader(payload))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if required {
			if response.Code != http.StatusBadRequest {
				t.Fatalf("required graph status=%d body=%s", response.Code, response.Body.String())
			}
			continue
		}
		if response.Code != http.StatusOK {
			t.Fatalf("optional fallback status=%d body=%s", response.Code, response.Body.String())
		}
		var envelope struct {
			Data struct {
				Route struct {
					Selected string `json:"selected_mode"`
					Fallback bool   `json:"fallback"`
					Reason   string `json:"reason_code"`
				} `json:"graph_route"`
			} `json:"data"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Data.Route.Selected != "basic" || !envelope.Data.Route.Fallback || envelope.Data.Route.Reason != "graph_index_unavailable" {
			t.Fatalf("fallback absent: %s", response.Body.String())
		}
	}
}
