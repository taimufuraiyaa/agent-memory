package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
)

func TestObserveIngestListDedupAndRedaction(t *testing.T) {
	t.Setenv("AGENT_MEMORY_OBSERVE_ENABLED", "true")

	baseDir := t.TempDir()
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	svc := &Service{
		Workspace:         "ws",
		BaseDir:           baseDir,
		EmbeddingProvider: provider,
	}
	ts := httptest.NewServer(NewMux(svc))
	defer ts.Close()

	occurredAt := time.Now().UTC().Format(time.RFC3339Nano)
	body := map[string]any{
		"workspace":    "ws",
		"session_id":   "s1",
		"occurred_at":  occurredAt,
		"kind":         "tool_use",
		"tool_name":    "grep",
		"tool_input":   "sk-ant-abcdefghijklmnopqrstuvwxyz0123456789",
		"project_root": "/tmp/proj",
		"cwd":          "/tmp/proj",
	}

	observeResp := postJSON(t, ts.URL+"/api/v1/observe", body)
	if observeResp["deduplicated"] != false {
		t.Fatalf("expected deduplicated=false, got %+v", observeResp)
	}
	if observeResp["stored"] != true {
		t.Fatalf("expected stored=true, got %+v", observeResp)
	}

	observeResp2 := postJSON(t, ts.URL+"/api/v1/observe", body)
	if observeResp2["deduplicated"] != true {
		t.Fatalf("expected deduplicated=true, got %+v", observeResp2)
	}
	if observeResp2["stored"] != false {
		t.Fatalf("expected stored=false, got %+v", observeResp2)
	}

	list := getJSON(t, ts.URL+"/api/v1/observations?workspace=ws&session_id=s1&limit=10")
	items, _ := list["observations"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 observation, got %+v", items)
	}
	first, _ := items[0].(map[string]any)
	summary, _ := first["summary"].(string)
	if summary == "" {
		t.Fatalf("expected non-empty summary")
	}
	if !bytes.Contains([]byte(summary), []byte("[REDACTED_SECRET]")) {
		t.Fatalf("expected redacted secret in summary, got %q", summary)
	}

	sessions := getJSON(t, ts.URL+"/api/v1/sessions?workspace=ws&limit=10")
	ss, _ := sessions["sessions"].([]any)
	if len(ss) != 1 {
		t.Fatalf("expected 1 session, got %+v", ss)
	}
	s0, _ := ss[0].(map[string]any)
	if s0["session_id"] != "s1" {
		t.Fatalf("expected session_id s1, got %+v", s0)
	}
	if count, ok := s0["observation_count"].(float64); !ok || int(count) != 1 {
		t.Fatalf("expected observation_count=1, got %+v", s0["observation_count"])
	}
}

func TestObserveRejectsInvalidTimestamp(t *testing.T) {
	t.Setenv("AGENT_MEMORY_OBSERVE_ENABLED", "true")

	baseDir := t.TempDir()
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	svc := &Service{
		Workspace:         "ws",
		BaseDir:           baseDir,
		EmbeddingProvider: provider,
	}
	ts := httptest.NewServer(NewMux(svc))
	defer ts.Close()

	req := map[string]any{
		"workspace":   "ws",
		"session_id":  "s1",
		"occurred_at": "not-a-time",
		"kind":        "tool_use",
		"tool_name":   "grep",
		"tool_input":  "hello",
	}
	b, _ := json.Marshal(req)
	res, err := http.Post(ts.URL+"/api/v1/observe", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", res.StatusCode)
	}
}

func TestObservePromotionIsIdempotent(t *testing.T) {
	t.Setenv("AGENT_MEMORY_OBSERVE_ENABLED", "true")

	baseDir := t.TempDir()
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	svc := &Service{
		Workspace:         "ws",
		BaseDir:           baseDir,
		EmbeddingProvider: provider,
	}
	ts := httptest.NewServer(NewMux(svc))
	defer ts.Close()

	occurredAt := time.Now().UTC().Format(time.RFC3339Nano)
	postJSON(t, ts.URL+"/api/v1/observe", map[string]any{
		"workspace":   "ws",
		"session_id":  "s2",
		"occurred_at": occurredAt,
		"kind":        "tool_use",
		"tool_name":   "read",
		"tool_input":  map[string]any{"file": "main.go"},
	})

	p1 := postJSON(t, ts.URL+"/api/v1/observations/promote", map[string]any{
		"workspace":   "ws",
		"session_id":  "s2",
		"max_items":   50,
		"type":        "episodic",
	})
	if p1["created_id"] == "" {
		t.Fatalf("expected created_id, got %+v", p1)
	}
	if p1["deduplicated"] != false {
		t.Fatalf("expected deduplicated=false on first promote, got %+v", p1)
	}

	p2 := postJSON(t, ts.URL+"/api/v1/observations/promote", map[string]any{
		"workspace":   "ws",
		"session_id":  "s2",
		"max_items":   50,
		"type":        "episodic",
	})
	if p2["created_id"] != p1["created_id"] {
		t.Fatalf("expected same created_id due to dedup, got p1=%v p2=%v", p1["created_id"], p2["created_id"])
	}
	if p2["deduplicated"] != true {
		t.Fatalf("expected deduplicated=true on second promote, got %+v", p2)
	}
}

func TestRecallIncludesRecentObservations(t *testing.T) {
	t.Setenv("AGENT_MEMORY_OBSERVE_ENABLED", "true")

	baseDir := t.TempDir()
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	svc := &Service{
		Workspace:         "ws",
		BaseDir:           baseDir,
		EmbeddingProvider: provider,
	}
	ts := httptest.NewServer(NewMux(svc))
	defer ts.Close()

	occurredAt := time.Now().UTC().Format(time.RFC3339Nano)
	postJSON(t, ts.URL+"/api/v1/observe", map[string]any{
		"workspace":   "ws",
		"session_id":  "s3",
		"occurred_at": occurredAt,
		"kind":        "tool_use",
		"tool_name":   "read",
		"tool_input":  "hello",
	})

	resp := postJSON(t, ts.URL+"/api/v1/memories/recall", map[string]any{
		"workspace":             "ws",
		"task_description":      "what happened?",
		"token_budget":          200,
		"include_observations":  true,
		"observation_limit":     5,
		"observation_session_id": "s3",
	})

	cb, _ := resp["context_block"].(string)
	if !strings.Contains(cb, "## Recent Observations") {
		t.Fatalf("expected Recent Observations section, got %q", cb)
	}
	if !strings.Contains(cb, "Session: s3") {
		t.Fatalf("expected Session: s3, got %q", cb)
	}
	if used, ok := resp["tokens_used"].(float64); ok {
		if int(used) > 200 {
			t.Fatalf("expected tokens_used <= 200, got %v", used)
		}
	}
}
