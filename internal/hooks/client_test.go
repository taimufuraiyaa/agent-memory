package hooks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientRetriesAndBoundsNormalizedHookDelivery(t *testing.T) {
	attempts := 0
	var delivered Event
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&delivered); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"version":"v1","data":{"observation_id":"o1"}}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{ServiceURL: server.URL, Timeout: time.Second, Retries: 1, MaxSummaryBytes: 64})

	err := client.Deliver(context.Background(), Event{Workspace: "ws", SessionID: "s1", OccurredAt: time.Now().UTC().Format(time.RFC3339Nano), Kind: "tool_result", Summary: string(make([]byte, 200)), SourceAgent: "codex", HookEvent: "PostToolUse"})

	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if attempts != 2 || len(delivered.Summary) > 64 || delivered.SchemaVersion != "v1" || delivered.ExternalEventID == "" {
		t.Fatalf("unexpected delivery attempts=%d event=%+v", attempts, delivered)
	}
}
