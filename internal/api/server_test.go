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

	postJSON(t, ts.URL+"/api/v1/memories/write", map[string]any{"type": "semantic", "content": "orders service publishes order.created"})
	postJSON(t, ts.URL+"/api/v1/memories/write", map[string]any{"type": "semantic", "content": "orders service publishes order.cancelled"})

	recent := getJSON(t, ts.URL+"/api/v1/memories/recent?workspace=ws&limit=1")
	recentResults, _ := recent["results"].([]any)
	if len(recentResults) != 1 {
		t.Fatalf("expected 1 recent result, got %+v", recentResults)
	}
	recentFirst, _ := recentResults[0].(map[string]any)
	if content, _ := recentFirst["content"].(string); content != "orders service publishes order.cancelled" {
		t.Fatalf("expected most recent memory content to be returned first, got %+v", recentFirst)
	}

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
	if strategy, _ := recallResp["retrieval_strategy"].(string); strategy == "" {
		t.Fatalf("expected retrieval strategy metadata")
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
	if strategy, _ := previewResp["retrieval_strategy"].(string); strategy == "" {
		t.Fatalf("expected preview retrieval strategy metadata")
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
	continueResp := postJSON(t, ts.URL+"/api/v1/memories/recall", map[string]any{"task": "continue previous work on order event", "top_k": 2, "budget": 20})
	if got, _ := continueResp["retrieval_strategy"].(string); got != "direct_recall" {
		t.Fatalf("expected direct_recall for continuation prompt, got %+v", continueResp)
	}
	if got, _ := continueResp["recall_trigger"].(string); got != "continuation_prompt" {
		t.Fatalf("expected continuation trigger, got %+v", continueResp)
	}
	emptyResp := postJSON(t, ts.URL+"/api/v1/memories/recall", map[string]any{"task": "investigate zebra quantum archive", "top_k": 2, "budget": 20})
	if got, _ := emptyResp["retrieval_strategy"].(string); got != "escalated_recall" {
		t.Fatalf("expected escalated_recall for empty search probe, got %+v", emptyResp)
	}
	if got, _ := emptyResp["recall_trigger"].(string); got != "search_empty" && got != "weak_results" {
		t.Fatalf("expected empty/weak recall trigger, got %+v", emptyResp)
	}
	searchBandResp := postJSON(t, ts.URL+"/api/v1/memories/search", map[string]any{
		"query":     "order event",
		"workspace": "ws",
		"top_k":     2,
		"mode":      "search",
		"explain":   true,
		"filters": map[string]any{
			"min_semantic_score": 0.01,
			"min_total_score":    0.01,
			"relative_cutoff":    0.01,
		},
	})
	if _, ok := searchBandResp["weak_results"]; !ok {
		t.Fatalf("expected weak_results in search response")
	}
	if _, ok := searchBandResp["retrieval_policy"]; !ok {
		t.Fatalf("expected retrieval_policy in search response")
	}
	recentResults = getJSON(t, ts.URL+"/api/v1/memories/recent?workspace=ws&limit=1")["results"].([]any)
	firstRecent, _ := recentResults[0].(map[string]any)
	memoryID, _ := firstRecent["id"].(string)
	feedbackResp := postJSON(t, ts.URL+"/api/v1/memories/feedback", map[string]any{
		"workspace":   "ws",
		"memory_id":   memoryID,
		"outcome":     "helpful",
		"validator":   "ai-agent",
		"occurred_at": "2026-05-21T15:00:00Z",
	})
	updatedMemory, _ := feedbackResp["updated_memory"].(map[string]any)
	if useful, _ := updatedMemory["useful_count"].(float64); useful < 1 {
		t.Fatalf("expected helpful feedback to increment useful_count, got %+v", updatedMemory)
	}
	pinResp := postJSON(t, ts.URL+"/api/v1/memories/pin", map[string]any{
		"workspace": "ws",
		"memory_id": memoryID,
		"pinned":    true,
	})
	pinnedMemory, _ := pinResp["updated_memory"].(map[string]any)
	if pinned, _ := pinnedMemory["pinned"].(bool); !pinned {
		t.Fatalf("expected memory to be pinned, got %+v", pinnedMemory)
	}
	unpinResp := postJSON(t, ts.URL+"/api/v1/memories/pin", map[string]any{
		"workspace": "ws",
		"memory_id": memoryID,
		"pinned":    false,
	})
	unpinnedMemory, _ := unpinResp["updated_memory"].(map[string]any)
	if pinned, _ := unpinnedMemory["pinned"].(bool); pinned {
		t.Fatalf("expected memory to be unpinned, got %+v", unpinnedMemory)
	}
	deleteResp := postJSON(t, ts.URL+"/api/v1/memories/delete", map[string]any{
		"workspace":  "ws",
		"memory_ids": []string{memoryID},
	})
	if deletedCount, _ := deleteResp["deleted_count"].(float64); deletedCount != 1 {
		t.Fatalf("expected one deleted memory, got %+v", deleteResp)
	}
	postDeleteResults := getJSON(t, ts.URL+"/api/v1/memories/recent?workspace=ws&limit=5")["results"].([]any)
	for _, raw := range postDeleteResults {
		entry, _ := raw.(map[string]any)
		if entryID, _ := entry["id"].(string); entryID == memoryID {
			t.Fatalf("expected deleted memory %q to be removed from recent results", memoryID)
		}
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
	if _, ok := stats["memory_type_counts"]; !ok {
		t.Fatalf("expected memory type distribution in stats")
	}
	if _, ok := stats["storage_tier_counts"]; !ok {
		t.Fatalf("expected storage tier distribution in stats")
	}
	if _, ok := stats["last_activity"]; !ok {
		t.Fatalf("expected last activity in stats")
	}
	if _, ok := stats["retrieve_count_total"]; !ok {
		t.Fatalf("expected retrieve_count_total in stats")
	}
	if _, ok := stats["retrieved_memory_count"]; !ok {
		t.Fatalf("expected retrieved_memory_count in stats")
	}
	if _, ok := stats["never_reached_memory_count"]; !ok {
		t.Fatalf("expected never_reached_memory_count in stats")
	}
	if _, ok := stats["retrieval_coverage_percent"]; !ok {
		t.Fatalf("expected retrieval_coverage_percent in stats")
	}
	if _, ok := stats["never_reached_percent"]; !ok {
		t.Fatalf("expected never_reached_percent in stats")
	}
	if _, ok := stats["low_reach_percentile"]; !ok {
		t.Fatalf("expected low_reach_percentile in stats")
	}
	if _, ok := stats["low_reach_threshold"]; !ok {
		t.Fatalf("expected low_reach_threshold in stats")
	}
	if _, ok := stats["low_reach_memory_count"]; !ok {
		t.Fatalf("expected low_reach_memory_count in stats")
	}
	if _, ok := stats["top_retrieved_memories"]; !ok {
		t.Fatalf("expected top_retrieved_memories in stats")
	}
	if _, ok := stats["token_metrics"]; !ok {
		t.Fatalf("expected token metrics payload in stats")
	}
	if _, ok := stats["token_metrics_by_operation"]; !ok {
		t.Fatalf("expected token metrics by operation in stats")
	}
	if _, ok := stats["token_metrics_by_group"]; !ok {
		t.Fatalf("expected grouped token metrics payload in stats")
	}
	if _, ok := stats["raw_token_metrics_by_group"]; !ok {
		t.Fatalf("expected raw grouped token metrics payload in stats")
	}
	if _, ok := stats["token_metrics_by_group_all"]; !ok {
		t.Fatalf("expected full grouped token metrics payload in stats")
	}
	if _, ok := stats["recall_token_metrics"]; !ok {
		t.Fatalf("expected recall token metrics payload in stats")
	}
	if _, ok := stats["overall_token_savings_percent"]; !ok {
		t.Fatalf("expected overall token savings percent in stats")
	}
	if _, ok := stats["recall_token_savings_percent"]; !ok {
		t.Fatalf("expected recall token savings percent in stats")
	}
	if _, ok := stats["llm_usage_totals"]; !ok {
		t.Fatalf("expected llm usage totals payload in stats")
	}
	if _, ok := stats["llm_usage_by_group"]; !ok {
		t.Fatalf("expected llm usage grouped payload in stats")
	}
	if _, ok := stats["raw_llm_usage_by_group"]; !ok {
		t.Fatalf("expected raw llm usage grouped payload in stats")
	}
	if _, ok := stats["llm_usage_by_group_all"]; !ok {
		t.Fatalf("expected full llm usage grouped payload in stats")
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
	if res.StatusCode == http.StatusOK {
		t.Fatalf("expected /dashboard/ to not be served by API server in standalone dashboard mode")
	}
}

func TestServerBenchmarkRunsAPI(t *testing.T) {
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

	ingest := postJSON(t, ts.URL+"/api/v1/benchmark/ingest", map[string]any{
		"workspace":           "ws",
		"run_id":              "benchmark-001",
		"seed_count":          200,
		"case_count":          10000,
		"top_k":               20,
		"budget":              400,
		"precision":           0.61,
		"recall":              0.58,
		"gold_recall":         0.52,
		"keyword_coverage":    0.67,
		"ndcg":                0.72,
		"f1":                  0.59,
		"token_efficiency":    0.44,
		"baseline_tokens":     9000,
		"returned_tokens":     5000,
		"saved_tokens":        4000,
		"cost_with_memory":    0.15,
		"cost_without_memory": 0.27,
		"cost_saved":          0.12,
		"cost_saved_pct":      0.4444,
		"combined_score":      0.63,
		"verdict":             "GOOD BENEFIT",
		"off_cases":           10000,
		"off_disabled_count":  10000,
		"off_all_disabled":    true,
		"task_success_rate":   0.7,
		"off_task_success_rate": 0.2,
		"task_success_delta":  0.5,
		"answer_fact_coverage": 0.8,
		"off_answer_fact_coverage": 0.25,
		"answer_fact_coverage_delta": 0.55,
		"answer_completeness": 0.65,
		"off_answer_completeness": 0.1,
		"answer_completeness_delta": 0.55,
		"avg_on_runtime_ms":   900,
		"avg_off_runtime_ms":  1500,
		"runtime_delta_ms":    600,
		"avg_on_investigation_effort": 3,
		"avg_off_investigation_effort": 5,
		"investigation_effort_delta": 2,
		"continuation_score":  0.48,
		"continuation_verdict": "GOOD BENEFIT",
		"clusters": []map[string]any{
			{
				"cluster_id":              "api_server",
				"cluster_title":           "API Server",
				"cases":                   400,
				"task_success_delta":      0.5,
				"answer_fact_coverage":    0.8,
				"answer_completeness":     0.7,
				"continuation_score":      0.52,
				"continuation_verdict":    "GOOD BENEFIT",
				"precision":               0.7,
				"combined_score":          0.68,
				"verdict":                 "GOOD BENEFIT",
			},
		},
		"generator_manifest": map[string]any{"test_case_count": 10000},
		"run_manifest":       map[string]any{"run_id": "benchmark-001"},
		"created_at":         "2026-05-28T17:00:00Z",
	})
	run, _ := ingest["run"].(map[string]any)
	if runID, _ := run["run_id"].(string); runID != "benchmark-001" {
		t.Fatalf("expected run id to round-trip, got %+v", ingest)
	}

	listed := getJSON(t, ts.URL+"/api/v1/benchmark/runs?workspace=ws&limit=5")
	runs, _ := listed["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("expected 1 benchmark run, got %+v", listed)
	}
	first, _ := runs[0].(map[string]any)
	if combined, _ := first["combined_score"].(float64); combined <= 0 {
		t.Fatalf("expected combined score in response, got %+v", first)
	}
	if continuation, _ := first["continuation_score"].(float64); continuation <= 0 {
		t.Fatalf("expected continuation score in response, got %+v", first)
	}
	clusters, _ := first["clusters"].([]any)
	if len(clusters) != 1 {
		t.Fatalf("expected cluster breakdown in response, got %+v", first)
	}
}

func TestServerDisabledNoops(t *testing.T) {
	t.Setenv("AGENT_MEMORY_ENABLED", "0")

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

	writeResp := postJSON(t, ts.URL+"/api/v1/memories/write", map[string]any{"type": "semantic", "content": "should not be stored"})
	if skipped, _ := writeResp["skipped"].(bool); !skipped {
		t.Fatalf("expected skipped=true when disabled, got %+v", writeResp)
	}

	recallResp := postJSON(t, ts.URL+"/api/v1/memories/recall", map[string]any{"task_description": "hello", "format": "raw"})
	if disabled, _ := recallResp["disabled"].(bool); !disabled {
		t.Fatalf("expected disabled=true when disabled, got %+v", recallResp)
	}

	stats := getJSON(t, ts.URL+"/api/v1/stats")
	if mc, ok := stats["memory_count"].(float64); !ok || int(mc) != 0 {
		t.Fatalf("expected memory_count=0 when disabled, got %+v", stats)
	}
	if rc, ok := stats["retrieve_count_total"].(float64); !ok || int(rc) != 0 {
		t.Fatalf("expected retrieve_count_total=0 when disabled, got %+v", stats)
	}
	if lr, ok := stats["low_reach_memory_count"].(float64); !ok || int(lr) != 0 {
		t.Fatalf("expected low_reach_memory_count=0 when disabled, got %+v", stats)
	}
}

func TestServerSearchMinSemanticOverrideAffectsResults(t *testing.T) {
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

	postJSON(t, ts.URL+"/api/v1/memories/write", map[string]any{"type": "semantic", "content": "orders service publishes order.created"})

	strict := postJSON(t, ts.URL+"/api/v1/memories/search", map[string]any{
		"query":     "order event",
		"workspace": "ws",
		"top_k":     5,
		"mode":      "search",
		"explain":   true,
		"filters": map[string]any{
			"min_semantic_score": 0.95,
		},
	})
	strictResults, _ := strict["results"].([]any)
	if len(strictResults) != 0 {
		t.Fatalf("expected strict semantic floor to suppress results, got %+v", strictResults)
	}

	relaxed := postJSON(t, ts.URL+"/api/v1/memories/search", map[string]any{
		"query":     "order event",
		"workspace": "ws",
		"top_k":     5,
		"mode":      "search",
		"explain":   true,
		"filters": map[string]any{
			"min_semantic_score": 0.0,
		},
	})
	relaxedResults, _ := relaxed["results"].([]any)
	if len(relaxedResults) == 0 {
		t.Fatalf("expected explicit zero semantic floor override to return results")
	}
}

func TestServerLLMUsageIngest(t *testing.T) {
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

	postJSON(t, ts.URL+"/api/v1/llm-usage", map[string]any{
		"workspace":         "ws",
		"provider":          "openai",
		"model":             "gpt-x",
		"prompt_tokens":     100,
		"completion_tokens": 50,
		"total_tokens":      150,
		"run_label":         "on",
		"memory_enabled":    true,
	})

	stats := getJSON(t, ts.URL+"/api/v1/stats")
	totals, _ := stats["llm_usage_totals"].(map[string]any)
	if totals == nil {
		t.Fatalf("expected llm usage totals map, got %+v", stats["llm_usage_totals"])
	}
	if total, ok := totals["total_tokens"].(float64); !ok || int(total) != 150 {
		t.Fatalf("expected total_tokens=150, got %+v", totals)
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

func TestServerSearchAllProjects(t *testing.T) {
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

	cwdA := filepath.Join(t.TempDir(), "proj-a")
	cwdB := filepath.Join(t.TempDir(), "proj-b")
	if err := os.MkdirAll(cwdA, 0o755); err != nil {
		t.Fatalf("mkdir proj-a: %v", err)
	}
	if err := os.MkdirAll(cwdB, 0o755); err != nil {
		t.Fatalf("mkdir proj-b: %v", err)
	}
	postJSON(t, ts.URL+"/api/v1/projects/init", map[string]any{"cwd": cwdA, "project_name": "proj-a"})
	postJSON(t, ts.URL+"/api/v1/projects/init", map[string]any{"cwd": cwdB, "project_name": "proj-b"})
	postJSON(t, ts.URL+"/api/v1/memories/write", map[string]any{"workspace": "proj-a", "type": "semantic", "content": "redis fallback policy for ranking"})
	postJSON(t, ts.URL+"/api/v1/memories/write", map[string]any{"workspace": "proj-b", "type": "semantic", "content": "redis fallback policy for dashboard search"})

	searchResp := postJSON(t, ts.URL+"/api/v1/memories/search", map[string]any{
		"workspace": allProjectsScope,
		"query":     "redis fallback ranking",
		"top_k":     10,
		"mode":      "search",
	})
	results, _ := searchResp["results"].([]any)
	if len(results) < 2 {
		t.Fatalf("expected aggregated results across projects, got %+v", results)
	}
	workspaces := map[string]bool{}
	for _, item := range results {
		row, _ := item.(map[string]any)
		if ws, _ := row["workspace"].(string); ws != "" {
			workspaces[ws] = true
		}
	}
	if !workspaces["proj-a"] || !workspaces["proj-b"] {
		t.Fatalf("expected all-projects search to include both workspaces, got %+v", workspaces)
	}
	if workspaceValue, _ := searchResp["workspace"].(string); workspaceValue != allProjectsScope {
		t.Fatalf("expected aggregated workspace marker, got %+v", searchResp)
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
