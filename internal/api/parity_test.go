package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	apipkg "github.com/time/timebooks/agent-memory/internal/api"
	clicmd "github.com/time/timebooks/agent-memory/internal/cli"
	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/embeddings"
	"github.com/time/timebooks/agent-memory/internal/engine"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

func TestSearchParityHTTPVsEngine(t *testing.T) {
	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "ws.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	pipe := engine.NewWritePipeline(store)
	_, _ = pipe.Write(context.Background(), engine.WriteInput{Workspace: "ws", Type: core.SemanticMemory, Content: "orders service emits order.created", Source: core.MemorySource{Type: core.SourceUserInput}})
	_, _ = pipe.Write(context.Background(), engine.WriteInput{Workspace: "ws", Type: core.SemanticMemory, Content: "payments retries timeouts", Source: core.MemorySource{Type: core.SourceUserInput}})

	searcher := engine.NewVectorSearcher(store, provider)
	retrieval := engine.NewRetrievalEngine(searcher)
	direct, err := retrieval.Retrieve(context.Background(), engine.RetrievalOptions{
		Workspace: "ws",
		Query:     "order events",
		TopK:      2,
		Mode:      engine.ModeSearch,
		Policy: engine.RetrievalPolicy{
			MinSemanticScore: float64Ptr(0),
		},
	})
	if err != nil {
		t.Fatalf("direct retrieval: %v", err)
	}

	svc := &apipkg.Service{Workspace: "ws", BaseDir: baseDir, EmbeddingProvider: provider}
	ts := httptest.NewServer(apipkg.NewMux(svc))
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{
		"query":     "order events",
		"workspace": "ws",
		"top_k":     2,
		"mode":      "search",
		"explain":   true,
		"filters": map[string]any{
			"min_semantic_score": 0.0,
		},
	})
	resp, err := http.Post(ts.URL+"/api/v1/memories/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("http post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var env map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode api response: %v", err)
	}
	if ok, _ := env["ok"].(bool); !ok {
		t.Fatalf("expected ok=true envelope, got %+v", env)
	}
	data, _ := env["data"].(map[string]any)
	results, _ := data["results"].([]any)
	if len(direct.Hits) != len(results) {
		t.Fatalf("parity mismatch hit length: direct=%d api=%d", len(direct.Hits), len(results))
	}
	for i := range direct.Hits {
		row, _ := results[i].(map[string]any)
		id, _ := row["id"].(string)
		score, _ := row["score"].(float64)
		if direct.Hits[i].Memory.ID != id {
			t.Fatalf("parity mismatch at %d: direct=%s api=%s", i, direct.Hits[i].Memory.ID, id)
		}
		if math.Abs(direct.Hits[i].Score-score) > 1e-6 {
			t.Fatalf("score mismatch at %d: direct=%f api=%f", i, direct.Hits[i].Score, score)
		}
	}
}

func TestRecallPreviewParityWithRawRecall(t *testing.T) {
	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "ws.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	pipe := engine.NewWritePipeline(store)
	_, _ = pipe.Write(context.Background(), engine.WriteInput{Workspace: "ws", Type: core.SemanticMemory, Content: "orders service emits order.created events", Source: core.MemorySource{Type: core.SourceUserInput}})
	_, _ = pipe.Write(context.Background(), engine.WriteInput{Workspace: "ws", Type: core.ProceduralMemory, Content: "when order queue fails, inspect retry DLQ first", Source: core.MemorySource{Type: core.SourceUserInput}})

	svc := &apipkg.Service{Workspace: "ws", BaseDir: baseDir, EmbeddingProvider: provider}
	ts := httptest.NewServer(apipkg.NewMux(svc))
	defer ts.Close()

	recallBody, _ := json.Marshal(map[string]any{
		"task":   "investigate order queue incident",
		"top_k":  4,
		"budget": 120,
		"format": "raw",
	})
	recallRes, err := http.Post(ts.URL+"/api/v1/memories/recall", "application/json", bytes.NewReader(recallBody))
	if err != nil {
		t.Fatalf("raw recall post: %v", err)
	}
	defer func() { _ = recallRes.Body.Close() }()
	var rawEnv map[string]any
	if err := json.NewDecoder(recallRes.Body).Decode(&rawEnv); err != nil {
		t.Fatalf("decode raw recall: %v", err)
	}
	rawData, _ := rawEnv["data"].(map[string]any)
	rawText, _ := rawData["text"].(string)
	if rawText == "" {
		t.Fatalf("expected non-empty raw recall text")
	}

	previewBody, _ := json.Marshal(map[string]any{
		"workspace":        "ws",
		"task_description": "investigate order queue incident",
		"top_k":            4,
		"token_budget":     120,
		"explain":          true,
	})
	previewRes, err := http.Post(ts.URL+"/api/v1/memories/recall/preview", "application/json", bytes.NewReader(previewBody))
	if err != nil {
		t.Fatalf("recall-preview post: %v", err)
	}
	defer func() { _ = previewRes.Body.Close() }()
	var previewEnv map[string]any
	if err := json.NewDecoder(previewRes.Body).Decode(&previewEnv); err != nil {
		t.Fatalf("decode recall-preview: %v", err)
	}
	previewData, _ := previewEnv["data"].(map[string]any)
	contextBlock, _ := previewData["context_block"].(string)
	if contextBlock == "" {
		t.Fatalf("expected recall-preview context block")
	}
	if contextBlock != rawText {
		t.Fatalf("context parity mismatch between raw recall and recall-preview")
	}
}

func TestRecallParityHTTPVsCLIWithStagedGating(t *testing.T) {
	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "ws.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}

	pipe := engine.NewWritePipeline(store)
	_, _ = pipe.Write(context.Background(), engine.WriteInput{Workspace: "ws", Type: core.SemanticMemory, Content: "redis config path is config/redis.conf", Source: core.MemorySource{Type: core.SourceUserInput}})

	svc := &apipkg.Service{Workspace: "ws", BaseDir: baseDir, EmbeddingProvider: provider}
	ts := httptest.NewServer(apipkg.NewMux(svc))
	defer ts.Close()

	cmd := clicmd.NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"recall",
		"--db", dbPath,
		"--workspace", "ws",
		"--model-dir", modelDir,
		"--task", "find redis config path",
		"--top-k", "2",
		"--budget", "50",
		"--format", "json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cli recall execute: %v", err)
	}
	var cliEnv map[string]any
	if err := json.Unmarshal(out.Bytes(), &cliEnv); err != nil {
		t.Fatalf("decode cli recall envelope: %v raw=%q", err, out.String())
	}
	cliData, _ := cliEnv["data"].(map[string]any)

	body, _ := json.Marshal(map[string]any{
		"task":   "find redis config path",
		"top_k":  2,
		"budget": 50,
	})
	resp, err := http.Post(ts.URL+"/api/v1/memories/recall", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("http post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var apiEnv map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&apiEnv); err != nil {
		t.Fatalf("decode api recall envelope: %v", err)
	}
	apiData, _ := apiEnv["data"].(map[string]any)

	if cliData["retrieval_strategy"] != apiData["retrieval_strategy"] {
		t.Fatalf("strategy mismatch cli=%v api=%v", cliData["retrieval_strategy"], apiData["retrieval_strategy"])
	}
	if cliData["recall_trigger"] != apiData["recall_trigger"] {
		t.Fatalf("trigger mismatch cli=%v api=%v", cliData["recall_trigger"], apiData["recall_trigger"])
	}
	if cliData["retrieval_mode"] != apiData["retrieval_mode"] {
		t.Fatalf("retrieval mode mismatch cli=%v api=%v", cliData["retrieval_mode"], apiData["retrieval_mode"])
	}
	if cliData["context_block"] != apiData["context_block"] {
		t.Fatalf("context mismatch cli=%v api=%v", cliData["context_block"], apiData["context_block"])
	}
}

func TestSearchParityHTTPVsCLI(t *testing.T) {
	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "ws.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}

	pipe := engine.NewWritePipeline(store)
	_, _ = pipe.Write(context.Background(), engine.WriteInput{Workspace: "ws", Type: core.SemanticMemory, Content: "orders service emits order.created", Source: core.MemorySource{Type: core.SourceUserInput}})
	_, _ = pipe.Write(context.Background(), engine.WriteInput{Workspace: "ws", Type: core.SemanticMemory, Content: "payments retries timeouts", Source: core.MemorySource{Type: core.SourceUserInput}})

	svc := &apipkg.Service{Workspace: "ws", BaseDir: baseDir, EmbeddingProvider: provider}
	ts := httptest.NewServer(apipkg.NewMux(svc))
	defer ts.Close()

	cmd := clicmd.NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"search",
		"--db", dbPath,
		"--workspace", "ws",
		"--model-dir", modelDir,
		"--query", "order events",
		"--top-k", "2",
		"--mode", "search",
		"--min-semantic-score", "0",
		"--explain",
		"--format", "json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cli search execute: %v", err)
	}
	var cliEnv map[string]any
	if err := json.Unmarshal(out.Bytes(), &cliEnv); err != nil {
		t.Fatalf("decode cli envelope: %v raw=%q", err, out.String())
	}
	if ok, _ := cliEnv["ok"].(bool); !ok {
		t.Fatalf("expected ok=true cli envelope: %+v", cliEnv)
	}
	cliData, _ := cliEnv["data"].(map[string]any)
	cliHits, _ := cliData["hits"].([]any)
	if len(cliHits) != 2 {
		t.Fatalf("expected 2 cli hits, got %d", len(cliHits))
	}

	body, _ := json.Marshal(map[string]any{
		"query":     "order events",
		"workspace": "ws",
		"top_k":     2,
		"mode":      "search",
		"explain":   true,
		"filters": map[string]any{
			"min_semantic_score": 0.0,
		},
	})
	resp, err := http.Post(ts.URL+"/api/v1/memories/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("http post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var apiEnv map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&apiEnv); err != nil {
		t.Fatalf("decode api envelope: %v", err)
	}
	if ok, _ := apiEnv["ok"].(bool); !ok {
		t.Fatalf("expected ok=true api envelope: %+v", apiEnv)
	}
	apiData, _ := apiEnv["data"].(map[string]any)
	apiResults, _ := apiData["results"].([]any)
	if len(apiResults) != 2 {
		t.Fatalf("expected 2 api results, got %d", len(apiResults))
	}

	for i := range apiResults {
		cliHit, _ := cliHits[i].(map[string]any)
		cliMem, _ := cliHit["memory"].(map[string]any)
		cliID, _ := cliMem["id"].(string)
		cliScore, _ := cliHit["score"].(float64)

		apiRow, _ := apiResults[i].(map[string]any)
		apiID, _ := apiRow["id"].(string)
		apiScore, _ := apiRow["score"].(float64)

		if cliID != apiID {
			t.Fatalf("id mismatch at %d: cli=%s api=%s", i, cliID, apiID)
		}
		if math.Abs(cliScore-apiScore) > 1e-6 {
			t.Fatalf("score mismatch at %d: cli=%f api=%f", i, cliScore, apiScore)
		}
	}
}

func TestSearchParityHTTPVsCLIWithTierFilter(t *testing.T) {
	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "ws.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}

	ctx := context.Background()
	insert := func(id string, tier core.StorageTier, content string) {
		if err := store.UpsertMemory(ctx, &core.MemoryEntry{
			ID:          id,
			Type:        core.SemanticMemory,
			Content:     content,
			Workspace:   "ws",
			Source:      core.MemorySource{Type: core.SourceUserInput},
			Confidence:  0.9,
			StorageTier: tier,
		}); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
		vec, err := provider.Embed(ctx, content)
		if err != nil {
			t.Fatalf("embed %s: %v", id, err)
		}
		if err := store.UpsertMemoryVector(ctx, id, "ws", provider.Name(), vec); err != nil {
			t.Fatalf("vector %s: %v", id, err)
		}
	}

	insert("m_vec", core.TierVector, "order created event")
	insert("m_md", core.TierMarkdown, "order created event")
	insert("m_doc", core.TierDocument, "payments timeout remediation")

	svc := &apipkg.Service{Workspace: "ws", BaseDir: baseDir, EmbeddingProvider: provider}
	ts := httptest.NewServer(apipkg.NewMux(svc))
	defer ts.Close()

	cmd := clicmd.NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"search",
		"--db", dbPath,
		"--workspace", "ws",
		"--model-dir", modelDir,
		"--query", "order created",
		"--top-k", "10",
		"--mode", "search",
		"--tier", "vector",
		"--min-semantic-score", "0",
		"--explain",
		"--format", "json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cli search execute: %v", err)
	}
	var cliEnv map[string]any
	if err := json.Unmarshal(out.Bytes(), &cliEnv); err != nil {
		t.Fatalf("decode cli envelope: %v raw=%q", err, out.String())
	}
	cliData, _ := cliEnv["data"].(map[string]any)
	cliHits, _ := cliData["hits"].([]any)
	if len(cliHits) == 0 {
		t.Fatalf("expected cli hits")
	}
	cliFirst, _ := cliHits[0].(map[string]any)
	cliMem, _ := cliFirst["memory"].(map[string]any)
	cliID, _ := cliMem["id"].(string)
	if cliID != "m_vec" {
		t.Fatalf("expected tier-filtered cli result m_vec, got %s", cliID)
	}

	body, _ := json.Marshal(map[string]any{
		"query":     "order created",
		"workspace": "ws",
		"top_k":     10,
		"mode":      "search",
		"explain":   true,
		"filters": map[string]any{
			"tiers":              []string{"vector"},
			"min_semantic_score": 0.0,
		},
	})
	resp, err := http.Post(ts.URL+"/api/v1/memories/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("http post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var apiEnv map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&apiEnv); err != nil {
		t.Fatalf("decode api envelope: %v", err)
	}
	apiData, _ := apiEnv["data"].(map[string]any)
	apiResults, _ := apiData["results"].([]any)
	if len(apiResults) == 0 {
		t.Fatalf("expected api results")
	}
	apiFirst, _ := apiResults[0].(map[string]any)
	apiID, _ := apiFirst["id"].(string)
	if apiID != "m_vec" {
		t.Fatalf("expected tier-filtered api result m_vec, got %s", apiID)
	}
}

func TestRecallPreviewParityHTTPVsCLIRaw(t *testing.T) {
	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "ws.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}

	pipe := engine.NewWritePipeline(store)
	_, _ = pipe.Write(context.Background(), engine.WriteInput{Workspace: "ws", Type: core.SemanticMemory, Content: "orders service emits order.created events", Source: core.MemorySource{Type: core.SourceUserInput}})
	_, _ = pipe.Write(context.Background(), engine.WriteInput{Workspace: "ws", Type: core.ProceduralMemory, Content: "when order queue fails, inspect retry DLQ first", Source: core.MemorySource{Type: core.SourceUserInput}})

	svc := &apipkg.Service{Workspace: "ws", BaseDir: baseDir, EmbeddingProvider: provider}
	ts := httptest.NewServer(apipkg.NewMux(svc))
	defer ts.Close()

	cmd := clicmd.NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"recall",
		"--db", dbPath,
		"--workspace", "ws",
		"--model-dir", modelDir,
		"--task", "investigate order queue incident",
		"--top-k", "4",
		"--budget", "120",
		"--format", "raw",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cli recall execute: %v", err)
	}
	cliText := out.String()
	if cliText == "" {
		t.Fatalf("expected non-empty cli recall raw output")
	}

	previewBody, _ := json.Marshal(map[string]any{
		"workspace":        "ws",
		"task_description": "investigate order queue incident",
		"top_k":            4,
		"token_budget":     120,
		"explain":          true,
	})
	previewRes, err := http.Post(ts.URL+"/api/v1/memories/recall/preview", "application/json", bytes.NewReader(previewBody))
	if err != nil {
		t.Fatalf("recall/preview post: %v", err)
	}
	defer func() { _ = previewRes.Body.Close() }()
	var previewEnv map[string]any
	if err := json.NewDecoder(previewRes.Body).Decode(&previewEnv); err != nil {
		t.Fatalf("decode recall/preview: %v", err)
	}
	previewData, _ := previewEnv["data"].(map[string]any)
	contextBlock, _ := previewData["context_block"].(string)
	if contextBlock == "" {
		t.Fatalf("expected recall preview context block")
	}
	if contextBlock != cliText {
		t.Fatalf("context parity mismatch between cli raw recall and recall preview")
	}
}

func float64Ptr(v float64) *float64 {
	return &v
}
