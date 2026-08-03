package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestAuditHandlerFiltersAndExportsNDJSON(t *testing.T) {
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir()}
	assets, err := svc.resolve(context.Background(), "ws")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	_, _ = assets.Store.AppendAuditEvent(context.Background(), sqlite.AuditEventInput{Workspace: "ws", Operation: "delete", Outcome: "success", Actor: "cli", RequestID: "r1"})
	_, _ = assets.Store.AppendAuditEvent(context.Background(), sqlite.AuditEventInput{Workspace: "ws", Operation: "write", Outcome: "success", Actor: "api", RequestID: "r2"})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/audit?workspace=ws&operation=delete", nil)
	recorder := httptest.NewRecorder()
	auditHandler(svc).ServeHTTP(recorder, request)
	var envelope envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data := envelope.Data.(map[string]any)
	if len(data["events"].([]any)) != 1 {
		t.Fatalf("unexpected filtered audit: %+v", data)
	}

	exportRequest := httptest.NewRequest(http.MethodGet, "/api/v1/audit?workspace=ws&format=ndjson", nil)
	exportRecorder := httptest.NewRecorder()
	auditHandler(svc).ServeHTTP(exportRecorder, exportRequest)
	if exportRecorder.Header().Get("content-type") != "application/x-ndjson" {
		t.Fatalf("unexpected content type: %s", exportRecorder.Header().Get("content-type"))
	}
}

func TestReplayHandlersListSessionsAndLoadOrderedEvents(t *testing.T) {
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir()}
	assets, err := svc.resolve(context.Background(), "ws")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	now := time.Now().UTC()
	for index, summary := range []string{"first", "second"} {
		_, _, err := assets.Store.InsertObservationDedupWindow(context.Background(), sqlite.ObservationInsert{Workspace: "ws", SessionID: "s1", OccurredAt: now.Add(time.Duration(index) * time.Second), Kind: "prompt", Summary: summary, SourceAgent: "codex", SchemaVersion: "v1"}, time.Minute)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		_ = assets.Store.UpsertSessionFromObservation(context.Background(), sqlite.ObserveUpsertSessionInput{Workspace: "ws", SessionID: "s1", OccurredAt: now.Add(time.Duration(index) * time.Second), Kind: "prompt"})
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/replay/events?workspace=ws&session_id=s1&limit=1", nil)
	recorder := httptest.NewRecorder()
	replayEventsHandler(svc).ServeHTTP(recorder, request)
	var response envelope
	_ = json.Unmarshal(recorder.Body.Bytes(), &response)
	data := response.Data.(map[string]any)
	events := data["events"].([]any)
	if len(events) != 1 || data["next_cursor"] == "" {
		t.Fatalf("missing page/cursor: %+v", data)
	}

	sessionsRequest := httptest.NewRequest(http.MethodGet, "/api/v1/replay/sessions?workspace=ws", nil)
	sessionsRecorder := httptest.NewRecorder()
	replaySessionsHandler(svc).ServeHTTP(sessionsRecorder, sessionsRequest)
	if sessionsRecorder.Code != http.StatusOK {
		t.Fatalf("sessions status: %d", sessionsRecorder.Code)
	}
}
