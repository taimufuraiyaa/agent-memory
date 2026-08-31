package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/taimufuraiyaa/agent-memory/internal/api/dashboard"
	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/attestation"
	"github.com/taimufuraiyaa/agent-memory/internal/clientprofile"
	"github.com/taimufuraiyaa/agent-memory/internal/deploymentprofile"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/localllm"
	"github.com/taimufuraiyaa/agent-memory/internal/observability"
	"github.com/taimufuraiyaa/agent-memory/internal/readingroom"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type Service struct {
	Workspace   string
	BaseDir     string
	ProjectRoot string
	// DBPath binds a fixed standalone workspace to an exact database file.
	// It is ignored for daemon and alternate-workspace resolution.
	DBPath                 string
	EmbeddingProvider      embeddings.Provider
	Scheduler              Scheduler
	LibraryRoleRunner      readingroom.RoleRunner
	RightsAttestation      *attestation.Service
	RightsAttestationStore *attestation.SQLiteStore
	RightsSubjectResolver  func(*http.Request) (string, error)
	ClientProfiles         *clientprofile.Store
	DeploymentProfile      *deploymentprofile.Store
	LocalLLMStore          *localllm.Store
	LocalLLMChecker        *localllm.Checker
	// GraphOperations is optional and primarily supports embedding/tests. When
	// nil, handlers bind a controller to the resolved workspace store.
	GraphOperations           application.GraphOperationController
	SkillEvaluationRunner     application.RestrictedSkillEvaluationRunner
	SkillApprovalAuthorizer   application.SkillApprovalAuthorizer
	SkillResolutionAuthorizer application.SkillResolutionAuthorizer
	SkillMutationAuthorizer   SkillMutationAuthorizer
	SkillOrchestrationDrainer interface{ Drain(context.Context) error }

	mu             sync.RWMutex
	stores         map[string]*workspaceAssets
	libraryJobs    map[string]LibraryImportJob
	seminarRuns    map[string]*SeminarRunState
	wikiProjectors map[string]*WikiProjector
}

const allProjectsScope = "__all_projects__"

type workspaceAssets struct {
	DBPath      string
	Store       *sqlite.Store
	Writer      *engine.WritePipeline
	Retrieval   *engine.RetrievalEngine
	Application *application.MemoryService
	Notes       *application.NoteService
	Solutions   *application.SolutionService
	Clipper     *engine.TokenClipper
}

func (s *Service) resolve(ctx context.Context, ws string) (*workspaceAssets, error) {
	if ws == "" {
		ws = s.Workspace
	}
	if strings.TrimSpace(ws) == "" {
		return nil, errors.New("workspace is required")
	}
	dbPath := filepath.Join(s.BaseDir, ws+".db")
	if ws == strings.TrimSpace(s.Workspace) && strings.TrimSpace(s.DBPath) != "" {
		dbPath = filepath.Clean(s.DBPath)
	}
	// A daemon has no fixed workspace and routes exclusively through the
	// registry. A fixed-workspace embedded service preserves its legacy path.
	if strings.TrimSpace(s.Workspace) == "" || ws != s.Workspace {
		manager, err := workspace.NewManager(s.BaseDir)
		if err != nil {
			return nil, err
		}
		project, err := manager.Project(ws)
		if err != nil {
			return nil, err
		}
		dbPath = project.DBPath
	}
	s.mu.RLock()
	assets, ok := s.stores[ws]
	s.mu.RUnlock()
	if ok && assets.DBPath == dbPath {
		return assets, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if assets, ok := s.stores[ws]; ok && assets.DBPath == dbPath {
		return assets, nil
	} else if ok && assets.Store != nil {
		_ = assets.Store.Close()
		delete(s.stores, ws)
	}
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return nil, err
	}

	// Create shared cache for efficient query reuse
	cache := engine.NewQueryCache(engine.DefaultQueryCacheConfig())
	searcher := engine.NewVectorSearcher(store, s.EmbeddingProvider)
	retrieval := engine.NewRetrievalEngineWithSharedCache(searcher, cache)

	writer := engine.NewWritePipelineWithOptions(store, engine.WritePipelineOptions{
		Embedder: s.EmbeddingProvider,
		Cache:    cache,
	})
	assets = &workspaceAssets{
		DBPath:      dbPath,
		Store:       store,
		Writer:      writer,
		Retrieval:   retrieval,
		Application: application.NewMemoryService(store, writer, retrieval),
		Notes:       application.NewNoteService(store, writer),
		Solutions:   application.NewSolutionService(store, engine.NewSolutionAdmissionPolicy(), application.WithSolutionWriter(writer)),
		Clipper:     engine.NewTokenClipper(nil),
	}
	if s.stores == nil {
		s.stores = make(map[string]*workspaceAssets)
	}
	s.stores[ws] = assets
	return assets, nil
}

// Close releases all lazily opened workspace stores. It is safe to call more
// than once during daemon shutdown.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var closeErr error
	for name, assets := range s.stores {
		if assets != nil && assets.Store != nil {
			if err := assets.Store.Close(); err != nil {
				closeErr = errors.Join(closeErr, fmt.Errorf("close workspace %s: %w", name, err))
			}
		}
		delete(s.stores, name)
	}
	if s.RightsAttestationStore != nil {
		if err := s.RightsAttestationStore.Close(); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close rights attestation store: %w", err))
		}
		s.RightsAttestationStore = nil
	}
	return closeErr
}

func (s *Service) evictWorkspace(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	assets, ok := s.stores[strings.TrimSpace(name)]
	if !ok {
		return nil
	}
	delete(s.stores, strings.TrimSpace(name))
	if assets != nil && assets.Store != nil {
		return assets.Store.Close()
	}
	return nil
}

func writeWorkspaceResolveError(w http.ResponseWriter, err error) {
	if errors.Is(err, workspace.ErrProjectNotRegistered) || strings.Contains(err.Error(), "workspace is required") {
		writeErr(w, http.StatusBadRequest, "workspace", err.Error())
		return
	}
	writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
}

func (s *Service) listProjectNames(ctx context.Context) ([]string, error) {
	_ = ctx
	mgr, err := workspace.NewManager(s.BaseDir)
	if err != nil {
		return nil, err
	}
	return mgr.ProjectNames()
}

func (s *Service) loadedWorkspaceCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.stores)
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
	mux.HandleFunc("/dashboard/runtime.json", dashboardRuntime("standalone", "/api/v1", "notebook", "memory", "portable_export"))
	mux.HandleFunc("/api/v1/capabilities", capabilitiesHandler())
	mux.HandleFunc("/api/v1/client-profiles", clientProfilesHandler(svc))
	mux.HandleFunc("/api/v1/client-profiles/", clientProfileHandler(svc))
	mux.HandleFunc("/api/v1/deployment-profile", deploymentProfileHandler(svc))
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

	mux.HandleFunc("/api/v1/notes", notesListHandler(svc))
	mux.HandleFunc("/api/v1/notes/get", noteGetHandler(svc))
	mux.HandleFunc("/api/v1/notes/create", noteCreateHandler(svc))
	mux.HandleFunc("/api/v1/notes/update", noteUpdateHandler(svc))
	mux.HandleFunc("/api/v1/notes/trash", noteTrashHandler(svc))
	mux.HandleFunc("/api/v1/notes/restore", noteRestoreHandler(svc))
	mux.HandleFunc("/api/v1/notes/delete", noteDeleteHandler(svc))
	mux.HandleFunc("/api/v1/notes/revisions", noteRevisionsHandler(svc))
	mux.HandleFunc("/api/v1/notes/revisions/restore", noteRevisionRestoreHandler(svc))
	mux.HandleFunc("/api/v1/notes/backlinks", noteBacklinksHandler(svc))
	mux.HandleFunc("/api/v1/notes/index/retry", noteIndexRetryHandler(svc))

	mux.HandleFunc("/api/v1/memories/search", searchHandler(svc))
	mux.HandleFunc("/api/v1/memories/recent", memoriesRecentHandler(svc))
	mux.HandleFunc("/api/v1/memories/recall", memoriesRecallHandler(svc))

	recallPreview := memoriesRecallPreviewHandler(svc)
	mux.HandleFunc("/api/v1/memories/recall/preview", recallPreview)
	mux.HandleFunc("/api/v1/memories/recall-preview", recallPreview)

	sessionEnd := sessionEndHandler(svc)
	mux.HandleFunc("/api/v1/memories/session-end", sessionEnd)
	mux.HandleFunc("/api/v1/sessions/end", sessionEnd)
	mux.HandleFunc("/api/v1/solutions/start", solutionStartHandler(svc))
	mux.HandleFunc("/api/v1/solutions/recall", solutionRecallHandler(svc))
	mux.HandleFunc("/api/v1/solutions/promote", solutionPromoteHandler(svc))
	mux.HandleFunc("/api/v1/solutions/tool-events", solutionToolEventHandler(svc))
	mux.HandleFunc("/api/v1/solutions/tool-lessons/derive", solutionToolLessonDeriveHandler(svc))
	mux.HandleFunc("/api/v1/solutions/tool-lessons/promote", solutionToolLessonPromoteHandler(svc))
	mux.HandleFunc("/api/v1/solutions/steps", solutionStepHandler(svc))
	mux.HandleFunc("/api/v1/solutions/checkpoint", solutionCheckpointHandler(svc))
	mux.HandleFunc("/api/v1/solutions/state", solutionStateHandler(svc))
	mux.HandleFunc("/api/v1/solutions/transition", solutionTransitionHandler(svc))
	mux.HandleFunc("/api/v1/solutions/handoff", solutionHandoffHandler(svc))
	mux.HandleFunc("/api/v1/solutions/activity", solutionActivityHandler(svc))
	mux.HandleFunc("/api/v1/solutions/review", solutionReviewHandler(svc))
	mux.HandleFunc("/api/v1/skills/lifecycle/list", skillListHandler(svc))
	mux.HandleFunc("/api/v1/skills/inspect", skillInspectHandler(svc))
	mux.HandleFunc("/api/v1/skills/lifecycle", skillLifecycleHandler(svc))
	mux.HandleFunc("/api/v1/skills/orchestration/status", skillOrchestrationStatusHandler(svc))
	mux.HandleFunc("/api/v1/skills/orchestration/control", skillOrchestrationControlHandler(svc))
	mux.HandleFunc("/api/v1/projects/init", projectsInitHandler(svc))
	mux.HandleFunc("/api/v1/projects/rename", projectsRenameHandler(svc))
	mux.HandleFunc("/api/v1/projects/list", projectsListHandler(svc))
	mux.HandleFunc("/api/v1/projects/study", projectsStudyHandler(svc))
	mux.HandleFunc("/api/v1/projects/delete", projectsDeleteHandler(svc))
	mux.HandleFunc("/api/v1/memories/export", memoriesExportHandler(svc))
	mux.HandleFunc("/api/v1/memories/import", memoriesImportHandler(svc))
	mux.HandleFunc("/api/v1/migrations/portable-export", portableMigrationExportHandler(svc))
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
	mux.HandleFunc("/api/v1/graph-index/readiness", graphIndexReadinessHandler(svc))
	mux.HandleFunc("/api/v1/graph-index/status", graphIndexStatusHandler(svc))
	mux.HandleFunc("/api/v1/graph-index/operations", graphIndexOperationHandler(svc))
	mux.HandleFunc("/api/v1/graph-index/explorer", graphExplorerHandler(svc))
	mux.HandleFunc("/api/v1/graph-index/review", graphReviewHandler(svc))
	mux.HandleFunc("/api/v1/graph-index/feedback", graphFeedbackHandler(svc))
	mux.HandleFunc("/api/v1/stats", workspaceStatsHandler(svc))
	mux.HandleFunc("/api/v1/advisor", advisorHandler(svc))
	mux.HandleFunc("/api/v1/requests/feedback", requestsFeedbackHandler(svc))
	mux.HandleFunc("/api/v1/feedback/stats", feedbackStatsHandler(svc))
	mux.HandleFunc("/api/v1/feedback", listFeedbackHandler(svc))
	mux.HandleFunc("/api/v1/audit", auditHandler(svc))
	mux.HandleFunc("/api/v1/replay/import-jsonl", replayImportJSONLHandler(svc))
	mux.HandleFunc("/api/v1/replay/sessions", replaySessionsHandler(svc))
	mux.HandleFunc("/api/v1/replay/events", replayEventsHandler(svc))
	mux.HandleFunc("/api/v1/skills", workspaceSkillsHandler(svc))
	mux.HandleFunc("/api/v1/rights-attestation/status", rightsAttestationStatusHandler(svc))
	mux.HandleFunc("/api/v1/rights-attestation/accept", rightsAttestationAcceptHandler(svc))

	mux.HandleFunc("/api/v1/library/imports", libraryImportHandler(svc))
	mux.HandleFunc("/api/v1/library/local-llm", libraryLocalLLMHandler(svc))
	mux.HandleFunc("/api/v1/library/local-llm/test", libraryLocalLLMTestHandler(svc))
	mux.HandleFunc("/api/v1/library/local-llm/translate", libraryLocalLLMTranslateHandler(svc))
	mux.HandleFunc("/api/v1/library/jobs", libraryJobHandler(svc))
	mux.HandleFunc("/api/v1/library/structure", libraryStructureHandler(svc))
	mux.HandleFunc("/api/v1/library/query", libraryQueryHandler(svc))
	mux.HandleFunc("/api/v1/library/memory-review", libraryMemoryReviewHandler(svc))
	mux.HandleFunc("/api/v1/library/seminars/start", seminarStartHandler(svc))
	mux.HandleFunc("/api/v1/library/seminars/status", seminarStatusHandler(svc))
	mux.HandleFunc("/api/v1/library/seminars/cancel", seminarCancelHandler(svc))
	mux.HandleFunc("/api/v1/library/wiki", wikiProjectionHandler(svc))

	// Visualization endpoints
	mux.HandleFunc("/api/v1/visualizations/graph", handleMemoryGraph(svc))
	mux.HandleFunc("/api/v1/visualizations/decay-timeline", handleDecayTimeline(svc))
	mux.HandleFunc("/api/v1/visualizations/entity-network", handleEntityNetwork(svc))

	// Serve embedded dashboard assets
	mux.Handle("/dashboard/", serveDashboard())
	mux.Handle("/w/", serveWorkspaceDashboard())

	return mux
}

// LocalRequestBoundary prevents browser and DNS-rebinding attackers from
// bridging the unauthenticated standalone API to local data.
func LocalRequestBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackRequestHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && !isLoopbackOrigin(origin) {
				http.Error(w, "cross-site request forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackRequestHost(value string) bool {
	host := value
	if parsed, _, err := net.SplitHostPort(value); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLoopbackOrigin(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	return isLoopbackRequestHost(parsed.Host)
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

func serveWorkspaceDashboard() http.Handler {
	assets := dashboard.GetEmbeddedHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		clone := r.Clone(r.Context())
		urlCopy := *r.URL
		urlCopy.Path = "/"
		clone.URL = &urlCopy
		assets.ServeHTTP(w, clone)
	})
}
