package api

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/taimufuraiyaa/agent-memory/internal/api/dashboard"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/observability"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type Service struct {
	Workspace         string
	BaseDir           string
	EmbeddingProvider embeddings.Provider
	Scheduler         Scheduler

	mu     sync.RWMutex
	stores map[string]*workspaceAssets
}

const allProjectsScope = "__all_projects__"

type workspaceAssets struct {
	Store     *sqlite.Store
	Writer    *engine.WritePipeline
	Retrieval *engine.RetrievalEngine
	Clipper   *engine.TokenClipper
}

func (s *Service) resolve(ctx context.Context, ws string) (*workspaceAssets, error) {
	if ws == "" {
		ws = s.Workspace
	}
	s.mu.RLock()
	assets, ok := s.stores[ws]
	s.mu.RUnlock()
	if ok {
		return assets, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if assets, ok := s.stores[ws]; ok {
		return assets, nil
	}

	dbPath := filepath.Join(s.BaseDir, ws+".db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	
	// Create shared cache for efficient query reuse
	cache := engine.NewQueryCache(engine.DefaultQueryCacheConfig())
	searcher := engine.NewVectorSearcher(store, s.EmbeddingProvider)
	retrieval := engine.NewRetrievalEngineWithSharedCache(searcher, cache)
	
	assets = &workspaceAssets{
		Store:     store,
		Writer:    engine.NewWritePipelineWithOptions(store, engine.WritePipelineOptions{
			Embedder: s.EmbeddingProvider,
			Cache:    cache,
		}),
		Retrieval: retrieval,
		Clipper:   engine.NewTokenClipper(nil),
	}
	if s.stores == nil {
		s.stores = make(map[string]*workspaceAssets)
	}
	s.stores[ws] = assets
	return assets, nil
}

func (s *Service) listProjectNames(ctx context.Context) ([]string, error) {
	mgr, err := workspace.NewManager(s.BaseDir)
	if err != nil {
		return nil, err
	}
	items, err := mgr.List(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		names = append(names, item.Name)
	}
	sort.Strings(names)
	return names, nil
}

func rankRetrievalHits(hits []engine.RetrievalHit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		left := hits[i].Memory
		right := hits[j].Memory
		if left.AccessCount != right.AccessCount {
			return left.AccessCount > right.AccessCount
		}
		if left.UpdatedAt != right.UpdatedAt {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		if left.Workspace != right.Workspace {
			return left.Workspace < right.Workspace
		}
		return left.ID < right.ID
	})
}

func trimRetrievalHits(hits []engine.RetrievalHit, limit int) []engine.RetrievalHit {
	if limit <= 0 || len(hits) <= limit {
		return hits
	}
	return hits[:limit]
}

// instrumentedResponseWriter wraps http.ResponseWriter to capture status code and bytes written.
type instrumentedResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (rw *instrumentedResponseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *instrumentedResponseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}

// NewMux returns the HTTP ServeMux with all route handlers registered.
// Wrap the returned mux with InstrumentedHandler to add Prometheus metrics.
func NewMux(svc *Service) *http.ServeMux {
	mux := http.NewServeMux()
	// Prometheus metrics scrape endpoint
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", healthHandler(svc))
	mux.HandleFunc("/ops/dashboard", opsDashboardHandler(svc))
	mux.HandleFunc("/api/v1/scheduler/status", schedulerStatusHandler(svc))
	mux.HandleFunc("/api/v1/scheduler/history", schedulerHistoryHandler(svc))
	mux.HandleFunc("/api/v1/scheduler/run", schedulerRunHandler(svc))

	writeHandler := writeMemoryHandler(svc)
	mux.HandleFunc("/api/v1/memories", writeHandler)
	mux.HandleFunc("/api/v1/memories/write", writeHandler)
	mux.HandleFunc("/api/v1/memories/feedback", memoriesFeedbackHandler(svc))
	mux.HandleFunc("/api/v1/memories/pin", memoriesPinHandler(svc))
	mux.HandleFunc("/api/v1/memories/delete", memoriesDeleteHandler(svc))

	mux.HandleFunc("/api/v1/memories/search", searchHandler(svc))
	mux.HandleFunc("/api/v1/memories/recent", memoriesRecentHandler(svc))
	mux.HandleFunc("/api/v1/memories/recall", memoriesRecallHandler(svc))

	recallPreview := memoriesRecallPreviewHandler(svc)
	mux.HandleFunc("/api/v1/memories/recall/preview", recallPreview)
	mux.HandleFunc("/api/v1/memories/recall-preview", recallPreview)

	sessionEnd := sessionEndHandler(svc)
	mux.HandleFunc("/api/v1/memories/session-end", sessionEnd)
	mux.HandleFunc("/api/v1/sessions/end", sessionEnd)
	mux.HandleFunc("/api/v1/projects/init", projectsInitHandler(svc))
	mux.HandleFunc("/api/v1/projects/rename", projectsRenameHandler(svc))
	mux.HandleFunc("/api/v1/projects/list", projectsListHandler(svc))
	mux.HandleFunc("/api/v1/projects/delete", projectsDeleteHandler(svc))
	mux.HandleFunc("/api/v1/memories/export", memoriesExportHandler(svc))
	mux.HandleFunc("/api/v1/memories/import", memoriesImportHandler(svc))
	mux.HandleFunc("/api/v1/memories/reconstruct", memoriesReconstructHandler(svc))

	mux.HandleFunc("/api/v1/observe", observeHandler(svc))

	mux.HandleFunc("/api/v1/observations", observationsHandler(svc))

	mux.HandleFunc("/api/v1/sessions", sessionsHandler(svc))

	mux.HandleFunc("/api/v1/llm-usage", llmUsageHandler(svc))

	mux.HandleFunc("/api/v1/benchmark/ingest", benchmarkIngestHandler(svc))

	mux.HandleFunc("/api/v1/benchmark/runs", benchmarkRunsHandler(svc))

	mux.HandleFunc("/api/v1/observations/promote", observationsPromoteHandler(svc))

	mux.HandleFunc("/api/v1/dashboard", workspaceDashboardHandler(svc))
	mux.HandleFunc("/api/v1/graph", workspaceGraphHandler(svc))
	mux.HandleFunc("/api/v1/stats", workspaceStatsHandler(svc))
	mux.HandleFunc("/api/v1/requests/feedback", requestsFeedbackHandler(svc))
	mux.HandleFunc("/api/v1/feedback/stats", feedbackStatsHandler(svc))
	mux.HandleFunc("/api/v1/feedback", listFeedbackHandler(svc))
	mux.HandleFunc("/api/v1/skills", workspaceSkillsHandler(svc))

	// Visualization endpoints
	mux.HandleFunc("/api/v1/visualizations/graph", handleMemoryGraph(svc))
	mux.HandleFunc("/api/v1/visualizations/decay-timeline", handleDecayTimeline(svc))
	mux.HandleFunc("/api/v1/visualizations/entity-network", handleEntityNetwork(svc))

	// Serve embedded dashboard assets
	mux.Handle("/dashboard/", serveDashboard())

	return mux
}

// InstrumentedHandler wraps the given handler with Prometheus HTTP instrumentation
// middleware that records request count, duration, size, and in-flight gauges.
func InstrumentedHandler(h http.Handler) http.Handler {
	metrics := observability.GetRegistry()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics.HTTPRequestsInFlight.Inc()
		defer metrics.HTTPRequestsInFlight.Dec()

		irw := &instrumentedResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		timer := observability.NewTimer()

		// Record request size
		if r.ContentLength > 0 {
			metrics.HTTPRequestSize.WithLabelValues(r.Method, r.URL.Path).Observe(float64(r.ContentLength))
		}

		h.ServeHTTP(irw, r)

		duration := timer.Duration()
		statusStr := fmt.Sprintf("%d", irw.statusCode)
		metrics.HTTPRequestsTotal.WithLabelValues(r.Method, r.URL.Path, statusStr).Inc()
		timer.ObserveDuration(metrics.HTTPRequestDuration.WithLabelValues(r.Method, r.URL.Path))
		if irw.bytesWritten > 0 {
			metrics.HTTPResponseSize.WithLabelValues(r.Method, r.URL.Path).Observe(float64(irw.bytesWritten))
		}
		_ = duration
	})
}

// serveDashboard returns an HTTP handler that serves the embedded dashboard
// assets (built via `make build-dashboard`/`make embed-dashboard` and
// embedded into the binary with go:embed in internal/api/dashboard).
// If this binary was built without embedded assets, it returns a handler
// that explains how to build them instead of a bare 404.
func serveDashboard() http.Handler {
	if !dashboard.HasEmbeddedAssets() {
		return http.StripPrefix("/dashboard/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Dashboard assets not embedded in this binary. Run: make build-with-dashboard", http.StatusNotFound)
		}))
	}
	return http.StripPrefix("/dashboard/", dashboard.GetEmbeddedHandler())
}

