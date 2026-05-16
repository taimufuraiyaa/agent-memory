package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/time/timebooks/agent-memory/internal/embeddings"
	"github.com/time/timebooks/agent-memory/internal/engine"
)

func TestServerWriteSearchRecall(t *testing.T) {
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

	writeBody := map[string]any{"type": "semantic", "content": "orders service publishes order.created"}
	postJSON(t, ts.URL+"/api/v1/memories/write", writeBody)

	searchResp := postJSON(t, ts.URL+"/api/v1/memories/search", map[string]any{"query": "order event", "top_k": 1, "mode": "search"})
	if len(searchResp["results"].([]any)) == 0 {
		t.Fatalf("expected search hits")
	}
	searchExplainResp := postJSON(t, ts.URL+"/api/v1/memories/search", map[string]any{
		"query":     "order event",
		"workspace": "ws",
		"top_k":     1,
		"mode":      "search",
		"explain":   true,
		"filters": map[string]any{
			"tiers": []string{"vector"},
		},
	})
	hits, _ := searchExplainResp["results"].([]any)
	if len(hits) == 0 {
		t.Fatalf("expected explain hits")
	}
	firstHit, _ := hits[0].(map[string]any)
	if _, ok := firstHit["tier"]; !ok {
		t.Fatalf("expected explain tier in search hit")
	}
	if _, ok := firstHit["score_breakdown"]; !ok {
		t.Fatalf("expected explain score_breakdown in search hit")
	}
	searchFilteredEmpty := postJSON(t, ts.URL+"/api/v1/memories/search", map[string]any{
		"query":     "order event",
		"workspace": "ws",
		"top_k":     1,
		"mode":      "search",
		"filters": map[string]any{
			"tiers": []string{"markdown"},
		},
	})
	filteredHits, _ := searchFilteredEmpty["results"].([]any)
	if len(filteredHits) != 0 {
		t.Fatalf("expected zero hits for unmatched tier filter")
	}
	recallResp := postJSON(t, ts.URL+"/api/v1/memories/recall", map[string]any{"task": "investigate order event", "top_k": 2, "budget": 20})
	if _, ok := recallResp["clipping"]; !ok {
		t.Fatalf("expected clipping metadata")
	}
	rawResp := postJSON(t, ts.URL+"/api/v1/memories/recall", map[string]any{"task": "investigate order event", "top_k": 2, "budget": 20, "format": "raw"})
	if _, ok := rawResp["text"]; !ok {
		t.Fatalf("expected raw recall text")
	}
	previewResp := postJSON(t, ts.URL+"/api/v1/memories/recall/preview", map[string]any{
		"workspace":        "ws",
		"task_description": "investigate order event",
		"top_k":            2,
		"token_budget":     20,
		"explain":          true,
	})
	if _, ok := previewResp["context_block"]; !ok {
		t.Fatalf("expected recall preview context block")
	}
	if _, ok := previewResp["memories_included"]; !ok {
		t.Fatalf("expected recall preview included memories")
	}
	if _, ok := previewResp["tier_distribution"]; !ok {
		t.Fatalf("expected recall preview tier distribution")
	}
	previewFull := postJSON(t, ts.URL+"/api/v1/memories/recall/preview", map[string]any{
		"workspace":        "ws",
		"task_description": "investigate order event",
		"top_k":            2,
		"token_budget":     20,
		"include_memories": true,
	})
	if _, ok := previewFull["memories_included_full"]; !ok {
		t.Fatalf("expected recall preview full included memories when include_memories is true")
	}
	sessionResp := postJSON(t, ts.URL+"/api/v1/memories/session-end", map[string]any{"transcript": "we should always run migrations\nresult was success"})
	if _, ok := sessionResp["total_extracted"]; !ok {
		t.Fatalf("expected session-end extraction response")
	}

	exportJSON := getJSON(t, ts.URL+"/api/v1/memories/export?format=json")
	if exportJSON["version"] != engine.ExportVersion {
		t.Fatalf("expected export bundle version, got %+v", exportJSON)
	}
	exportMD := getJSON(t, ts.URL+"/api/v1/memories/export?format=markdown")
	if _, ok := exportMD["markdown"]; !ok {
		t.Fatalf("expected markdown export payload")
	}
	importResp := postRawJSON(t, ts.URL+"/api/v1/memories/import", exportJSON)
	if imported, _ := importResp["imported"].(float64); imported <= 0 {
		t.Fatalf("expected import response with imported count, got %+v", importResp)
	}
	stats := getJSON(t, ts.URL+"/api/v1/stats")
	if _, ok := stats["memory_count"]; !ok {
		t.Fatalf("expected stats payload")
	}
	if _, ok := stats["token_metrics"]; !ok {
		t.Fatalf("expected token metrics payload in stats")
	}
	dashboard := getJSON(t, ts.URL+"/api/v1/dashboard")
	if _, ok := dashboard["totals"]; !ok {
		t.Fatalf("expected dashboard payload")
	}
	if workspace, _ := dashboard["workspace"].(string); workspace != "ws" {
		t.Fatalf("expected default workspace in dashboard payload, got %q", workspace)
	}
	dashboardWithWS := getJSON(t, ts.URL+"/api/v1/dashboard?workspace=ws")
	if workspace, _ := dashboardWithWS["workspace"].(string); workspace != "ws" {
		t.Fatalf("expected workspace query override to apply")
	}

	reconstructBody := map[string]any{"query": "old config", "workspace": "ws"}
	b, _ := json.Marshal(reconstructBody)
	res, err := http.Post(ts.URL+"/api/v1/memories/reconstruct", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("post reconstruct: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for reconstruct response, got %d", res.StatusCode)
	}

	res, err = http.Get(ts.URL + "/dashboard/")
	if err != nil {
		t.Fatalf("get dashboard html: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected dashboard shell to load, got %d", res.StatusCode)
	}
	html, _ := io.ReadAll(res.Body)
	if !bytes.Contains(html, []byte("Agent Memory Dashboard")) {
		t.Fatalf("expected dashboard html title")
	}
}

func TestServerProjectLifecycleRoutes(t *testing.T) {
	baseDir := t.TempDir()
	cwd := t.TempDir()
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	svc := &Service{
		Workspace:         "svc",
		BaseDir:           baseDir,
		EmbeddingProvider: provider,
	}
	ts := httptest.NewServer(NewMux(svc))
	defer ts.Close()

	initResp := postJSON(t, ts.URL+"/api/v1/projects/init", map[string]any{
		"cwd":          cwd,
		"project_name": "api-proj",
	})
	if initResp["project"] != "api-proj" {
		t.Fatalf("unexpected init project: %+v", initResp)
	}
	listResp := getJSON(t, ts.URL+"/api/v1/projects/list")
	projects, _ := listResp["projects"].([]any)
	if len(projects) == 0 {
		t.Fatalf("expected at least one project in list")
	}
	renameResp := postJSON(t, ts.URL+"/api/v1/projects/rename", map[string]any{
		"cwd":  cwd,
		"from": "api-proj",
		"to":   "api-proj-v2",
	})
	if renameResp["to"] != "api-proj-v2" {
		t.Fatalf("rename failed: %+v", renameResp)
	}
	deleteResp := postJSON(t, ts.URL+"/api/v1/projects/delete", map[string]any{
		"project_name": "api-proj-v2",
		"keep_data":    true,
		"yes":          true,
	})
	if _, ok := deleteResp["archived_path"]; !ok {
		t.Fatalf("expected archived path on keep_data delete")
	}
}

func postJSON(t *testing.T, url string, payload map[string]any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(payload)
	res, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	var env map[string]any
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ok, _ := env["ok"].(bool); !ok {
		t.Fatalf("expected ok=true, got %+v", env)
	}
	data, _ := env["data"].(map[string]any)
	return data
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	var env map[string]any
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("decode get json: %v body=%s", err, string(b))
	}
	if ok, _ := env["ok"].(bool); !ok {
		t.Fatalf("expected ok=true, got %+v", env)
	}
	data, _ := env["data"].(map[string]any)
	return data
}

func postRawJSON(t *testing.T, url string, payload any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(payload)
	res, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("post raw: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	var env map[string]any
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		t.Fatalf("decode post raw json: %v", err)
	}
	if ok, _ := env["ok"].(bool); !ok {
		t.Fatalf("expected ok=true, got %+v", env)
	}
	data, _ := env["data"].(map[string]any)
	return data
}
