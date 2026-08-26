// Package api implements the hosted HTTP transport without owning domain state.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/attestation"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/billing"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/credential"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/dashboard"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/deletion"
	exportservice "github.com/taimufuraiyaa/agent-memory/internal/saas/export"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/importer"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/launch"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/memory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/privacy"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retrieval"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/review"
	sourceservice "github.com/taimufuraiyaa/agent-memory/internal/saas/source"
)

type ProfileVerifier interface {
	Profile(context.Context, string) (auth.VerifiedProfile, error)
}

type SignupCountryVerifier interface {
	Verify(country, timestamp, signature string) bool
}

type HTTPObserver interface {
	Wrap(http.Handler) http.Handler
	MetricsHandler() http.Handler
	EvidenceHandler() http.Handler
}

type SourceQueryService interface {
	Query(context.Context, retrieval.Query) (retrieval.Result, error)
}

type MemoryReviewService interface {
	Create(context.Context, review.CreateCommand) (review.Proposal, error)
	Get(context.Context, string) (review.Proposal, error)
	Update(context.Context, string, review.UpdateCommand) (review.Proposal, error)
	Accept(context.Context, string) (review.Proposal, error)
	Reject(context.Context, string) (review.Proposal, error)
}

type MemorySearchService interface {
	Search(context.Context, memory.SearchCommand) (memory.SearchResult, error)
}

type AuditRecorder interface {
	Record(context.Context, auth.RequestContext, string, string, string, string, string, string, map[string]any) error
}

type Dependencies struct {
	Readiness         func(context.Context) error
	Authenticator     auth.Authenticator
	Profiles          ProfileVerifier
	Memberships       auth.MembershipResolver
	Signup            *control.SignupService
	Attestations      *attestation.Service
	Memories          *memory.Service
	MemorySearch      MemorySearchService
	Credentials       *credential.Service
	Workflows         *memory.WorkflowService
	Exports           *exportservice.Service
	SourceUploads     *sourceservice.Service
	SourceCatalog     *sourceservice.CatalogService
	SourceQueries     SourceQueryService
	MemoryReviews     MemoryReviewService
	Audit             *audit.Service
	Deletions         *deletion.Service
	AccountDeletion   *deletion.AccountService
	SecurityGate      auth.RequestGate
	Privacy           *privacy.Service
	Billing           *billing.Service
	Imports           *importer.Service
	CountryVerifier   SignupCountryVerifier
	Telemetry         HTTPObserver
	LocalOwner        LocalOwnerService
	LocalSessionToken string
	LocalProjects     LocalProjectService
	GraphOperations   application.GraphOperationController
	GraphAuthorizer   GraphWorkspaceAuthorizer
	GraphExperience   GraphExperienceStore
}

func NewHandler(deps Dependencies) (http.Handler, error) {
	if deps.Readiness == nil || deps.Authenticator == nil || deps.Profiles == nil || deps.Memberships == nil || deps.Signup == nil || deps.Attestations == nil || deps.Memories == nil || deps.MemorySearch == nil || deps.Credentials == nil || deps.Workflows == nil || deps.Exports == nil || deps.SourceUploads == nil || deps.SourceCatalog == nil || deps.SourceQueries == nil || deps.MemoryReviews == nil || deps.Audit == nil || deps.Deletions == nil || deps.AccountDeletion == nil || deps.SecurityGate == nil || deps.Privacy == nil || deps.Billing == nil || deps.Imports == nil {
		return nil, errors.New("hosted API dependencies are incomplete")
	}
	root := http.NewServeMux()
	registerOperationalRoutes(root, deps.Readiness, deps.Telemetry)
	observe := func(handler http.Handler) http.Handler {
		if deps.Telemetry == nil {
			return handler
		}
		return deps.Telemetry.Wrap(handler)
	}
	features := hostedDashboardFeatures(deps)
	if deps.LocalOwner != nil {
		if strings.TrimSpace(deps.LocalSessionToken) == "" {
			return nil, errors.New("local onboarding session token is required")
		}
		root.Handle("GET /v1/local-session", observe(localSessionStatus(deps.LocalOwner, deps.LocalSessionToken)))
		root.Handle("POST /v1/local-session/signup", observe(localOwnerSignup(deps.LocalOwner, deps.LocalSessionToken)))
		root.HandleFunc("DELETE /v1/local-session", localSessionLogout)
	}
	root.HandleFunc("/dashboard/runtime.json", dashboardRuntime("hosted", "/v1", features...))
	root.Handle("POST /v1/signup", observe(signup(deps)))
	root.Handle("PUT /v1/source-uploads/{grant_id}/content", observe(uploadSource(deps.SourceUploads)))
	root.Handle("/dashboard/", dashboard.Handler())

	protected := http.NewServeMux()
	protected.HandleFunc("GET /v1/whoami", whoami)
	protected.HandleFunc("GET /v1/attestations/rights", attestationStatus(deps.Attestations))
	protected.HandleFunc("POST /v1/attestations/rights", attestationAccept(deps.Attestations))
	protected.HandleFunc("POST /v1/memories", writeMemory(deps.Memories))
	protected.Handle("POST /v1/search", searchMemories(deps.MemorySearch, deps.Audit))
	protected.HandleFunc("GET /v1/credentials", listCredentials(deps.Credentials))
	protected.HandleFunc("POST /v1/credentials", createCredential(deps.Credentials))
	protected.HandleFunc("DELETE /v1/current-credential", revokeCurrentCredential(deps.Credentials))
	protected.HandleFunc("POST /v1/credentials/{credential_id}/rotate", rotateCredential(deps.Credentials))
	protected.HandleFunc("DELETE /v1/credentials/{credential_id}", revokeCredential(deps.Credentials))
	protected.HandleFunc("POST /v1/notes", createNote(deps.Workflows))
	protected.HandleFunc("PATCH /v1/notes/{note_id}", updateNote(deps.Workflows))
	protected.HandleFunc("POST /v1/memories/{memory_id}/feedback", recordFeedback(deps.Workflows))
	protected.HandleFunc("POST /v1/sessions/{session_id}/end", endSession(deps.Workflows))
	protected.HandleFunc("POST /v1/exports", requestExport(deps.Exports))
	protected.HandleFunc("GET /v1/exports/{export_id}", exportStatus(deps.Exports))
	protected.HandleFunc("GET /v1/exports/{export_id}/download", downloadExport(deps.Exports))
	protected.HandleFunc("POST /v1/sources/uploads", issueSourceUpload(deps.SourceUploads))
	protected.HandleFunc("GET /v1/sources", listSources(deps.SourceCatalog))
	protected.HandleFunc("GET /v1/source-statuses", listSourceStatuses(deps.SourceCatalog))
	protected.HandleFunc("GET /v1/processing-tasks", listProcessingTasks(deps.SourceCatalog))
	if deps.GraphOperations != nil && deps.GraphAuthorizer != nil {
		protected.HandleFunc("GET /v1/graph-index/readiness", hostedGraphReadiness(deps.GraphOperations, deps.GraphAuthorizer))
		protected.HandleFunc("GET /v1/graph-index/status", hostedGraphStatus(deps.GraphOperations, deps.GraphAuthorizer))
		protected.HandleFunc("POST /v1/graph-index/operations", hostedGraphOperation(deps.GraphOperations, deps.GraphAuthorizer))
	}
	if deps.GraphExperience != nil && deps.GraphAuthorizer != nil {
		var graphObserver GraphObserver
		if observer, ok := deps.Telemetry.(GraphObserver); ok {
			graphObserver = observer
		}
		protected.HandleFunc("GET /v1/graph-index/explorer", hostedGraphExplorer(deps.GraphExperience, deps.GraphAuthorizer))
		protected.HandleFunc("POST /v1/graph-index/recall", hostedGraphRecall(deps.GraphExperience, deps.MemorySearch, deps.GraphAuthorizer, deps.Audit, graphObserver))
		protected.HandleFunc("POST /v1/graph-index/review", hostedGraphReview(deps.GraphExperience, deps.GraphAuthorizer))
		protected.HandleFunc("POST /v1/graph-index/feedback", hostedGraphFeedback(deps.GraphExperience, deps.GraphAuthorizer))
	}
	if deps.LocalOwner != nil && deps.LocalProjects != nil {
		protected.Handle("GET /v1/local-projects", localProjectBoundary("memory:read", listLocalProjects(deps.LocalProjects)))
		protected.Handle("POST /v1/local-projects/study", localProjectBoundary("memory:write", studyLocalProject(deps.LocalProjects)))
		protected.Handle("POST /v1/local-projects/search", localProjectBoundary("memory:read", searchLocalProject(deps.LocalProjects)))
		protected.Handle("GET /v1/local-projects/memories", localProjectBoundary("memory:read", browseLocalProject(deps.LocalProjects)))
		protected.Handle("GET /v1/local-projects/memories/{memory_id}", localProjectBoundary("memory:read", getLocalProjectMemory(deps.LocalProjects)))
		protected.Handle("GET /v1/local-project-feedback", localProjectBoundary("memory:read", listLocalProjectFeedback(deps.LocalProjects)))
		protected.Handle("POST /v1/local-project-feedback", localProjectBoundary("memory:write", recordLocalProjectFeedback(deps.LocalProjects)))
		protected.Handle("GET /v1/local-project-solutions", localProjectBoundary("memory:read", listLocalProjectSolutions(deps.LocalProjects)))
		protected.Handle("POST /v1/local-project-solutions/review", localProjectBoundary("memory:write", reviewLocalProjectSolution(deps.LocalProjects)))
		protected.Handle("POST /v1/local-project-solutions/start", localProjectBoundary("memory:write", startLocalProjectSolution(deps.LocalProjects)))
		protected.Handle("POST /v1/local-project-solutions/steps", localProjectBoundary("memory:write", appendLocalProjectSolutionStep(deps.LocalProjects)))
		protected.Handle("POST /v1/local-project-solutions/checkpoint", localProjectBoundary("memory:write", checkpointLocalProjectSolution(deps.LocalProjects)))
		protected.Handle("POST /v1/local-project-solutions/transition", localProjectBoundary("memory:write", transitionLocalProjectSolution(deps.LocalProjects)))
		protected.Handle("POST /v1/local-project-solutions/handoff", localProjectBoundary("memory:write", handoffLocalProjectSolution(deps.LocalProjects)))
		protected.Handle("POST /v1/local-project-solutions/finalize", localProjectBoundary("memory:write", finalizeLocalProjectSolution(deps.LocalProjects)))
		protected.Handle("POST /v1/local-project-solutions/recall", localProjectBoundary("memory:read", recallLocalProjectSolutions(deps.LocalProjects)))
		protected.Handle("POST /v1/local-project-solutions/promote", localProjectBoundary("memory:write", promoteLocalProjectSolution(deps.LocalProjects)))
		protected.Handle("GET /v1/local-project-solutions/export", localProjectBoundary("memory:read", exportLocalProjectSolution(deps.LocalProjects)))
		if system, ok := deps.LocalProjects.(LocalProjectSystemService); ok {
			protected.Handle("GET /v1/local-projects/lifecycle", localProjectBoundary("memory:read", listLocalProjectLifecycle(system)))
			protected.Handle("GET /v1/local-projects/skills", localProjectBoundary("memory:read", listLocalProjectSkills(system)))
		}
		if clients, ok := deps.LocalProjects.(LocalClientProfileService); ok {
			protected.Handle("GET /v1/local-client-profiles", localProjectBoundary("memory:read", localClientProfiles(clients)))
			protected.Handle("POST /v1/local-client-profiles", localProjectBoundary("memory:write", localClientProfiles(clients)))
			protected.Handle("PUT /v1/local-client-profiles/{client_id}", localProjectBoundary("memory:write", localClientProfile(clients)))
			protected.Handle("DELETE /v1/local-client-profiles/{client_id}", localProjectBoundary("memory:write", localClientProfile(clients)))
		}
	}
	protected.HandleFunc("GET /v1/sources/{source_id}", getSource(deps.SourceCatalog))
	protected.HandleFunc("POST /v1/sources/{source_id}/retry", retrySource(deps.SourceCatalog))
	protected.HandleFunc("POST /v1/source-queries", querySources(deps.SourceQueries, deps.Audit))
	protected.HandleFunc("POST /v1/memory-proposals", createMemoryProposal(deps.MemoryReviews))
	protected.HandleFunc("GET /v1/memory-proposals/{proposal_id}", getMemoryProposal(deps.MemoryReviews))
	protected.HandleFunc("PATCH /v1/memory-proposals/{proposal_id}", updateMemoryProposal(deps.MemoryReviews))
	protected.HandleFunc("POST /v1/memory-proposals/{proposal_id}/accept", acceptMemoryProposal(deps.MemoryReviews))
	protected.HandleFunc("POST /v1/memory-proposals/{proposal_id}/reject", rejectMemoryProposal(deps.MemoryReviews))
	protected.HandleFunc("DELETE /v1/sources/{source_id}", requestSourceDeletion(deps.Deletions))
	protected.HandleFunc("GET /v1/deletions/{operation_id}", deletionStatus(deps.Deletions))
	protected.HandleFunc("DELETE /v1/account", requestAccountDeletion(deps.AccountDeletion))
	protected.HandleFunc("GET /v1/privacy", privacyOverview(deps.Privacy))
	protected.HandleFunc("GET /v1/billing", billingOverview(deps.Billing))
	protected.HandleFunc("POST /v1/billing/plan-changes", requestPlanChange(deps.Billing))
	protected.HandleFunc("POST /v1/imports", importPortableBundle(deps.Imports))
	protected.HandleFunc("GET /v1/imports/{import_id}", importStatus(deps.Imports))
	protectedMiddleware := auth.MiddlewareWithGuards(deps.Authenticator, deps.Memberships, deps.Audit, deps.SecurityGate)
	if deps.LocalOwner != nil {
		protectedMiddleware = auth.MiddlewareWithGuardsAndTokenSource(deps.Authenticator, deps.Memberships, deps.Audit, deps.SecurityGate, auth.LocalBrowserTokenSource(localSessionCookieName))
	}
	root.Handle("/v1/", protectedMiddleware(observe(protected)))
	return root, nil
}

func localSystemToolsAvailable(projects LocalProjectService) bool {
	if projects == nil {
		return false
	}
	_, hasSystemTools := projects.(LocalProjectSystemService)
	_, hasClientProfiles := projects.(LocalClientProfileService)
	return hasSystemTools && hasClientProfiles
}

func hostedDashboardFeatures(deps Dependencies) []string {
	features := []string{"sources", "memory", "portable_import", "privacy", "billing"}
	if deps.LocalOwner == nil {
		return features
	}
	features = append(features, "local_onboarding")
	if localSystemToolsAvailable(deps.LocalProjects) {
		features = append(features, "local_system_tools")
	}
	return features
}

func registerOperationalRoutes(root *http.ServeMux, readiness func(context.Context) error, telemetry HTTPObserver) {
	root.HandleFunc("GET /health/live", probe)
	ready := http.Handler(http.HandlerFunc(readyProbe(readiness)))
	if telemetry != nil {
		ready = telemetry.Wrap(ready)
	}
	root.Handle("GET /health/ready", ready)
	if telemetry != nil {
		root.Handle("GET /metrics", telemetry.MetricsHandler())
		root.Handle("GET /internal/evidence/requests/{request_id}", telemetry.EvidenceHandler())
	}
}

func importPortableBundle(service *importer.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		body := http.MaxBytesReader(w, r.Body, importer.MaxBundleBytes+1)
		encrypted, err := io.ReadAll(body)
		if err != nil || len(encrypted) > importer.MaxBundleBytes {
			writeError(w, http.StatusRequestEntityTooLarge, request.RequestID, "bundle_too_large", "The portable bundle exceeds the import limit.")
			return
		}
		result, err := service.Import(r.Context(), strings.TrimSpace(r.Header.Get("X-Agent-Memory-Workspace")), strings.TrimSpace(r.Header.Get("Idempotency-Key")), r.Header.Get("X-Agent-Memory-Bundle-Passphrase"), encrypted)
		if err != nil {
			status, code := http.StatusBadRequest, "import_invalid"
			if errors.Is(err, attestation.ErrAttestationRequired) {
				status, code = http.StatusPreconditionRequired, "attestation_required"
			}
			writeError(w, status, request.RequestID, code, "The portable bundle could not be imported.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, result)
	}
}

func importStatus(service *importer.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		result, err := service.Status(r.Context(), r.PathValue("import_id"))
		if err != nil {
			writeError(w, http.StatusNotFound, request.RequestID, "resource_not_found", "The import operation was not found.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, result)
	}
}
func requestAccountDeletion(service *deletion.AccountService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		operation, duplicate, err := service.Request(r.Context(), strings.TrimSpace(r.Header.Get("Idempotency-Key")))
		if err != nil {
			writeError(w, http.StatusBadRequest, request.RequestID, "invalid_request", "Account deletion could not be started.")
			return
		}
		status := http.StatusAccepted
		if duplicate {
			status = http.StatusOK
		}
		writeSuccess(w, status, request.RequestID, operation)
	}
}

func privacyOverview(service *privacy.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		result, err := service.Overview(r.Context())
		if err != nil {
			writeError(w, http.StatusNotFound, request.RequestID, "resource_not_found", "Privacy controls are unavailable.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, result)
	}
}
func billingOverview(service *billing.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		result, err := service.Overview(r.Context())
		if err != nil {
			writeError(w, http.StatusNotFound, request.RequestID, "resource_not_found", "Billing status is unavailable.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, result)
	}
}
func requestPlanChange(service *billing.Service) http.HandlerFunc {
	type input struct {
		Action string `json:"action"`
		PlanID string `json:"plan_id"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		var body input
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, request.RequestID, "invalid_request", "The plan change is invalid.")
			return
		}
		id, duplicate, err := service.RequestPlanChange(r.Context(), body.Action, body.PlanID, strings.TrimSpace(r.Header.Get("Idempotency-Key")))
		if err != nil {
			writeError(w, http.StatusBadRequest, request.RequestID, "invalid_request", "The plan change is invalid.")
			return
		}
		writeSuccess(w, map[bool]int{true: http.StatusOK, false: http.StatusAccepted}[duplicate], request.RequestID, map[string]any{"id": id, "duplicate": duplicate, "state": "queued"})
	}
}

func requestSourceDeletion(service *deletion.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		operation, duplicate, err := service.RequestSource(r.Context(), r.PathValue("source_id"), strings.TrimSpace(r.Header.Get("Idempotency-Key")))
		if err != nil {
			writeError(w, http.StatusNotFound, request.RequestID, "resource_not_found", "The source is unavailable.")
			return
		}
		status := http.StatusAccepted
		if duplicate && operation.State == "completed" {
			status = http.StatusOK
		}
		writeSuccess(w, status, request.RequestID, operation)
	}
}

func deletionStatus(service *deletion.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		operation, err := service.Get(r.Context(), r.PathValue("operation_id"))
		if err != nil {
			writeError(w, http.StatusNotFound, request.RequestID, "resource_not_found", "The deletion operation is unavailable.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, operation)
	}
}

func querySources(service SourceQueryService, auditor *audit.Service) http.HandlerFunc {
	type input struct {
		SourceIDs          []string `json:"source_ids"`
		Query              string   `json:"query"`
		Limit              int      `json:"limit"`
		Offset             int      `json:"offset"`
		ContextTokenBudget int      `json:"context_token_budget"`
		Generate           bool     `json:"generate"`
		Provider           string   `json:"provider"`
		Model              string   `json:"model"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		if !request.Can("source:read") {
			writeError(w, http.StatusForbidden, request.RequestID, "insufficient_scope", "The source query requires source:read.")
			return
		}
		var body input
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, request.RequestID, "invalid_request", "The source query is invalid.")
			return
		}
		result, err := service.Query(r.Context(), retrieval.Query{TenantID: request.TenantID, AuthorizedSourceIDs: body.SourceIDs, Text: body.Query, Limit: body.Limit, Offset: body.Offset, ContextTokenBudget: body.ContextTokenBudget, Generate: body.Generate, Provider: body.Provider, Model: body.Model})
		if err != nil {
			_ = auditor.Record(r.Context(), request, "retrieval", "retrieval.query", "denied", "source_set", "authorized-selection", "evidence_unavailable", map[string]any{"source_count": len(body.SourceIDs)})
			writeError(w, http.StatusNotFound, request.RequestID, "resource_not_found", "The requested source evidence is unavailable.")
			return
		}
		if err := auditor.Record(r.Context(), request, "retrieval", "retrieval.query", "success", "source_set", "authorized-selection", "authorized", map[string]any{"source_count": len(body.SourceIDs), "evidence_count": len(result.Evidence), "generated": result.Generated}); err != nil {
			writeError(w, http.StatusServiceUnavailable, request.RequestID, "audit_unavailable", "The request could not be durably audited.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, result)
	}
}

func createMemoryProposal(service MemoryReviewService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		var body review.CreateCommand
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, request.RequestID, "invalid_request", "The memory proposal is invalid.")
			return
		}
		proposal, err := service.Create(r.Context(), body)
		if err != nil {
			writeError(w, http.StatusBadRequest, request.RequestID, "proposal_invalid", "The memory proposal could not be created.")
			return
		}
		writeSuccess(w, http.StatusCreated, request.RequestID, proposal)
	}
}

func getMemoryProposal(service MemoryReviewService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		proposal, err := service.Get(r.Context(), r.PathValue("proposal_id"))
		if err != nil {
			writeError(w, http.StatusNotFound, request.RequestID, "resource_not_found", "The memory proposal was not found.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, proposal)
	}
}

func updateMemoryProposal(service MemoryReviewService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		var body review.UpdateCommand
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, request.RequestID, "invalid_request", "The proposal update is invalid.")
			return
		}
		proposal, err := service.Update(r.Context(), r.PathValue("proposal_id"), body)
		if err != nil {
			writeError(w, http.StatusConflict, request.RequestID, "proposal_not_reviewable", "The memory proposal is no longer reviewable.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, proposal)
	}
}

func acceptMemoryProposal(service MemoryReviewService) http.HandlerFunc {
	return reviewMemoryProposal(service, true)
}

func rejectMemoryProposal(service MemoryReviewService) http.HandlerFunc {
	return reviewMemoryProposal(service, false)
}

func reviewMemoryProposal(service MemoryReviewService, accept bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		var proposal review.Proposal
		var err error
		if accept {
			proposal, err = service.Accept(r.Context(), r.PathValue("proposal_id"))
		} else {
			proposal, err = service.Reject(r.Context(), r.PathValue("proposal_id"))
		}
		if err != nil {
			writeError(w, http.StatusConflict, request.RequestID, "proposal_not_reviewable", "The memory proposal is no longer reviewable.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, proposal)
	}
}

func listSources(service *sourceservice.CatalogService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		sources, err := service.List(r.Context(), r.URL.Query().Get("workspace_id"))
		if err != nil {
			writeError(w, http.StatusNotFound, request.RequestID, "resource_not_found", "The source collection was not found.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, sources)
	}
}

func listSourceStatuses(service *sourceservice.CatalogService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		statuses, err := service.ListStatuses(r.Context(), r.URL.Query().Get("workspace_id"))
		if err != nil {
			writeError(w, http.StatusNotFound, request.RequestID, "resource_not_found", "The source status collection was not found.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, statuses)
	}
}

func listProcessingTasks(service *sourceservice.CatalogService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		tasks, err := service.ListProcessingTasks(r.Context(), r.URL.Query().Get("workspace_id"))
		if err != nil {
			writeError(w, http.StatusNotFound, request.RequestID, "resource_not_found", "The processing task collection was not found.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, tasks)
	}
}

func getSource(service *sourceservice.CatalogService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		source, err := service.Get(r.Context(), r.PathValue("source_id"))
		if err != nil {
			writeError(w, http.StatusNotFound, request.RequestID, "resource_not_found", "The source was not found.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, source)
	}
}

func retrySource(service *sourceservice.CatalogService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		if err := service.Retry(r.Context(), r.PathValue("source_id")); err != nil {
			writeError(w, http.StatusNotFound, request.RequestID, "resource_not_found", "The source could not be retried.")
			return
		}
		source, err := service.Get(r.Context(), r.PathValue("source_id"))
		if err != nil {
			writeError(w, http.StatusNotFound, request.RequestID, "resource_not_found", "The source was not found.")
			return
		}
		writeSuccess(w, http.StatusAccepted, request.RequestID, source)
	}
}

func issueSourceUpload(service *sourceservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		var body sourceservice.GrantRequest
		if decodeJSON(r, &body) != nil {
			writeError(w, 400, request.RequestID, "invalid_request", "The upload request is invalid.")
			return
		}
		grant, err := service.Issue(r.Context(), body)
		if err != nil {
			status, code := http.StatusBadRequest, "invalid_request"
			if errors.Is(err, attestation.ErrAttestationRequired) {
				status, code = http.StatusPreconditionRequired, "attestation_required"
			}
			if errors.Is(err, auth.ErrTenantUnavailable) {
				status, code = http.StatusNotFound, "resource_not_found"
			}
			writeError(w, status, request.RequestID, code, "The upload grant could not be issued.")
			return
		}
		w.Header().Set("Location", grant.UploadPath)
		writeSuccess(w, http.StatusAccepted, request.RequestID, grant)
	}
}
func uploadSource(service *sourceservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mediaType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])
		if err := service.Upload(r.Context(), r.PathValue("grant_id"), r.URL.Query().Get("token"), mediaType, r.ContentLength, r.Body); err != nil {
			writeError(w, http.StatusNotFound, "upload", "resource_not_found", "The upload grant was not found.")
			return
		}
		writeSuccess(w, http.StatusOK, "upload", map[string]bool{"uploaded": true})
	}
}

func requestExport(service *exportservice.Service) http.HandlerFunc {
	type input struct {
		WorkspaceID string `json:"workspace_id"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		var body input
		if decodeJSON(r, &body) != nil {
			writeError(w, 400, request.RequestID, "invalid_request", "The request body is invalid.")
			return
		}
		operation, err := service.Request(r.Context(), body.WorkspaceID)
		if err != nil {
			writeError(w, 404, request.RequestID, "resource_not_found", "The export target was not found.")
			return
		}
		w.Header().Set("Location", "/v1/exports/"+operation.ID)
		writeSuccess(w, 202, request.RequestID, operation)
	}
}
func exportStatus(service *exportservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		operation, err := service.Status(r.Context(), r.PathValue("export_id"))
		if err != nil {
			writeError(w, 404, request.RequestID, "resource_not_found", "The export was not found.")
			return
		}
		writeSuccess(w, 200, request.RequestID, operation)
	}
}
func downloadExport(service *exportservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		body, operation, err := service.Download(r.Context(), r.PathValue("export_id"))
		if err != nil {
			writeError(w, 404, request.RequestID, "resource_not_found", "The export was not found.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="agent-memory-export-`+operation.ID+`.json"`)
		w.WriteHeader(200)
		_, _ = w.Write(body)
	}
}

func createNote(service *memory.WorkflowService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		var input core.CreateNoteInput
		if decodeJSON(r, &input) != nil {
			writeError(w, 400, request.RequestID, "invalid_request", "The request body is invalid.")
			return
		}
		note, duplicate, err := service.CreateNote(r.Context(), memory.NoteCreate{Input: input, IdempotencyKey: r.Header.Get("Idempotency-Key")})
		if err != nil {
			writeError(w, 400, request.RequestID, "invalid_request", "The note could not be created.")
			return
		}
		writeSuccess(w, 201, request.RequestID, map[string]any{"note": note, "duplicate": duplicate})
	}
}
func updateNote(service *memory.WorkflowService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		var input core.UpdateNoteInput
		if decodeJSON(r, &input) != nil {
			writeError(w, 400, request.RequestID, "invalid_request", "The request body is invalid.")
			return
		}
		input.NoteID = r.PathValue("note_id")
		note, duplicate, err := service.UpdateNote(r.Context(), memory.NoteUpdate{Input: input})
		if err != nil {
			writeError(w, 409, request.RequestID, "revision_conflict", "The note revision changed.")
			return
		}
		writeSuccess(w, 200, request.RequestID, map[string]any{"note": note, "duplicate": duplicate})
	}
}
func recordFeedback(service *memory.WorkflowService) http.HandlerFunc {
	type input struct {
		RequestID      string                 `json:"request_id"`
		Outcome        core.RetrievalFeedback `json:"outcome"`
		ReasonCategory string                 `json:"reason_category"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		var body input
		if decodeJSON(r, &body) != nil {
			writeError(w, 400, request.RequestID, "invalid_request", "The request body is invalid.")
			return
		}
		duplicate, err := service.Feedback(r.Context(), memory.FeedbackCommand{MemoryID: r.PathValue("memory_id"), RequestID: body.RequestID, Outcome: body.Outcome, ReasonCategory: body.ReasonCategory})
		if err != nil {
			writeError(w, 400, request.RequestID, "invalid_request", "Feedback could not be recorded.")
			return
		}
		writeSuccess(w, 200, request.RequestID, map[string]bool{"duplicate": duplicate})
	}
}
func endSession(service *memory.WorkflowService) http.HandlerFunc {
	type input struct {
		WorkspaceID string `json:"workspace_id"`
		Transcript  string `json:"transcript"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		var body input
		if decodeJSON(r, &body) != nil {
			writeError(w, 400, request.RequestID, "invalid_request", "The request body is invalid.")
			return
		}
		duplicate, err := service.EndSession(r.Context(), memory.SessionEndCommand{SessionID: r.PathValue("session_id"), WorkspaceID: body.WorkspaceID, Transcript: body.Transcript, IdempotencyKey: r.Header.Get("Idempotency-Key")})
		if err != nil {
			writeError(w, 400, request.RequestID, "invalid_request", "The session could not be completed.")
			return
		}
		writeSuccess(w, 200, request.RequestID, map[string]bool{"duplicate": duplicate})
	}
}

func createCredential(service *credential.Service) http.HandlerFunc {
	type input struct {
		Label     string    `json:"label"`
		Scopes    []string  `json:"scopes"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		var body input
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, request.RequestID, "invalid_request", "The request body is invalid.")
			return
		}
		issued, err := service.Create(r.Context(), body.Label, body.Scopes, body.ExpiresAt)
		if err != nil {
			writeError(w, http.StatusForbidden, request.RequestID, "credential_forbidden", "The credential could not be created.")
			return
		}
		writeSuccess(w, http.StatusCreated, request.RequestID, issued)
	}
}

func listCredentials(service *credential.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		values, err := service.List(r.Context())
		if err != nil {
			writeError(w, http.StatusForbidden, request.RequestID, "credential_forbidden", "Credentials are unavailable.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, values)
	}
}

func rotateCredential(service *credential.Service) http.HandlerFunc {
	type input struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		var body input
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, request.RequestID, "invalid_request", "The request body is invalid.")
			return
		}
		issued, err := service.Rotate(r.Context(), r.PathValue("credential_id"), body.ExpiresAt)
		if err != nil {
			writeError(w, http.StatusNotFound, request.RequestID, "resource_not_found", "The credential was not found.")
			return
		}
		writeSuccess(w, http.StatusCreated, request.RequestID, issued)
	}
}

func revokeCredential(service *credential.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		if err := service.Revoke(r.Context(), r.PathValue("credential_id")); err != nil {
			writeError(w, http.StatusNotFound, request.RequestID, "resource_not_found", "The credential was not found.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, map[string]bool{"revoked": true})
	}
}

func revokeCurrentCredential(service *credential.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		if err := service.RevokeSelf(r.Context()); err != nil {
			writeError(w, http.StatusForbidden, request.RequestID, "credential_forbidden", "The current credential could not be revoked.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, map[string]bool{"revoked": true})
	}
}

func probe(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"service": "api", "status": "ok"})
}

func readyProbe(check func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if check == nil || check(ctx) != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"service": "api", "status": "unavailable"})
			return
		}
		probe(w, r)
	}
}

func signup(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestID := requestID(r)
		token, ok := bearer(r.Header.Get("Authorization"))
		if !ok {
			writeError(w, http.StatusUnauthorized, requestID, "unauthenticated", "Authentication is required.")
			return
		}
		profile, err := deps.Profiles.Profile(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, requestID, "unauthenticated", "Authentication is required.")
			return
		}
		var input struct {
			InvitationToken string `json:"invitation_token"`
			AgeConfirmed    bool   `json:"age_confirmed"`
		}
		if r.Body != nil && r.ContentLength != 0 {
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				writeError(w, http.StatusBadRequest, requestID, "invalid_signup", "The signup request is invalid.")
				return
			}
		}
		country := strings.ToUpper(strings.TrimSpace(r.Header.Get("X-Agent-Memory-Country")))
		countryVerified := deps.CountryVerifier != nil && deps.CountryVerifier.Verify(country, r.Header.Get("X-Agent-Memory-Country-Timestamp"), r.Header.Get("X-Agent-Memory-Country-Signature"))
		account, err := deps.Signup.SignupWithContext(r.Context(), control.VerifiedIdentity{
			ExternalSubject: profile.SubjectID, Email: profile.Email, EmailVerified: true, DisplayName: profile.DisplayName,
		}, control.SignupContext{InvitationToken: input.InvitationToken, AgeConfirmed: input.AgeConfirmed, Country: country, CountryVerified: countryVerified, NetworkAddress: r.RemoteAddr})
		if err != nil {
			if errors.Is(err, launch.ErrSignupRateLimited) {
				writeError(w, http.StatusTooManyRequests, requestID, "signup_limited", "Signup is temporarily unavailable.")
				return
			}
			if errors.Is(err, launch.ErrSignupClosed) || errors.Is(err, launch.ErrInvitationRequired) || errors.Is(err, launch.ErrGeographyBlocked) || errors.Is(err, launch.ErrAgeRestricted) || errors.Is(err, launch.ErrAccountCapReached) {
				writeError(w, http.StatusForbidden, requestID, "signup_unavailable", "Signup is not available for this request.")
				return
			}
			writeError(w, http.StatusInternalServerError, requestID, "signup_failed", "The account could not be created.")
			return
		}
		writeSuccess(w, http.StatusCreated, requestID, account)
	}
}

func whoami(w http.ResponseWriter, r *http.Request) {
	request, _ := auth.FromContext(r.Context())
	writeSuccess(w, http.StatusOK, request.RequestID, map[string]any{
		"account_id": request.AccountID, "subject_id": request.SubjectID, "tenant_id": request.TenantID,
		"role": request.Role, "session_id": request.SessionID, "trace_id": request.TraceID,
	})
}

func attestationStatus(service *attestation.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		status, err := service.Status(r.Context(), request.AccountID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, request.RequestID, "attestation_failed", "Attestation status is unavailable.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, status)
	}
}

func attestationAccept(service *attestation.Service) http.HandlerFunc {
	type input struct {
		PolicyVersion        string   `json:"policy_version"`
		AcceptedStatementIDs []string `json:"accepted_statement_ids"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		var body input
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, request.RequestID, "invalid_request", "The request body is invalid.")
			return
		}
		status, err := service.Accept(r.Context(), request.AccountID, attestation.AcceptCommand{
			PolicyVersion: body.PolicyVersion, AcceptedStatementIDs: body.AcceptedStatementIDs,
			RequestID: request.RequestID, UserAgent: r.UserAgent(),
		})
		if err != nil {
			writeError(w, http.StatusConflict, request.RequestID, "attestation_conflict", "The current policy must be accepted completely.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, status)
	}
}

func writeMemory(service *memory.Service) http.HandlerFunc {
	type input struct {
		WorkspaceID string            `json:"workspace_id"`
		Type        core.MemoryType   `json:"type"`
		Content     string            `json:"content"`
		Source      core.MemorySource `json:"source"`
		Entities    []string          `json:"entities"`
		Tags        []string          `json:"tags"`
		Keywords    []string          `json:"keywords"`
		Outcome     *core.Outcome     `json:"outcome"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		var body input
		if err := decodeJSON(r, &body); err != nil || len(body.Keywords) > 3 {
			writeError(w, http.StatusBadRequest, request.RequestID, "invalid_request", "The request body is invalid.")
			return
		}
		terms := make([]core.MemoryTerm, 0, len(body.Keywords))
		for index, keyword := range body.Keywords {
			terms = append(terms, core.MemoryTerm{Term: keyword, Display: keyword, Source: core.TermSourceExplicit, Ordinal: index, NormalizationVersion: "v1", ExtractorVersion: "api-v1"})
		}
		entry, duplicate, err := service.Write(r.Context(), memory.Command{
			WorkspaceID: body.WorkspaceID, Type: body.Type, Content: body.Content, Source: body.Source,
			Entities: body.Entities, Tags: body.Tags, Keywords: terms, Outcome: body.Outcome,
			IdempotencyKey: r.Header.Get("Idempotency-Key"),
		})
		if err != nil {
			status, code := http.StatusBadRequest, "invalid_request"
			if errors.Is(err, memory.ErrIdempotencyConflict) {
				status, code = http.StatusConflict, "idempotency_conflict"
			} else if errors.Is(err, memory.ErrForbidden) || errors.Is(err, auth.ErrTenantUnavailable) {
				status, code = http.StatusNotFound, "resource_not_found"
			}
			writeError(w, status, request.RequestID, code, "The memory could not be written.")
			return
		}
		writeSuccess(w, http.StatusCreated, request.RequestID, map[string]any{"memory": entry, "duplicate": duplicate})
	}
}

func searchMemories(service MemorySearchService, auditor AuditRecorder) http.Handler {
	type input struct {
		WorkspaceID string `json:"workspace_id"`
		Query       string `json:"query"`
		Limit       int    `json:"limit"`
		Cursor      string `json:"cursor"`
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, _ := auth.FromContext(r.Context())
		var body input
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, request.RequestID, "invalid_request", "The request body is invalid.")
			return
		}
		result, err := service.Search(r.Context(), memory.SearchCommand{
			WorkspaceID: body.WorkspaceID, Query: body.Query, Limit: body.Limit, Cursor: body.Cursor,
		})
		if err != nil {
			status, code := http.StatusServiceUnavailable, "search_unavailable"
			if errors.Is(err, memory.ErrInvalidSearch) {
				status, code = http.StatusBadRequest, "invalid_request"
			} else if errors.Is(err, memory.ErrSearchForbidden) || errors.Is(err, auth.ErrTenantUnavailable) {
				status, code = http.StatusNotFound, "resource_not_found"
			}
			writeError(w, status, request.RequestID, code, "Memory search could not be completed.")
			return
		}
		if err := auditor.Record(
			r.Context(), request, "memory", "memory.search", "success", "workspace", body.WorkspaceID,
			"authorized", map[string]any{"result_count": len(result.Items), "has_next": result.NextCursor != ""},
		); err != nil {
			writeError(w, http.StatusServiceUnavailable, request.RequestID, "audit_unavailable", "Memory search could not be completed.")
			return
		}
		writeSuccess(w, http.StatusOK, request.RequestID, result)
	})
}

func bearer(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		return "", false
	}
	return strings.TrimSpace(parts[1]), true
}

func decodeJSON(r *http.Request, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func requestID(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Request-ID")); value != "" {
		return value
	}
	return "signup"
}

func writeSuccess(w http.ResponseWriter, status int, requestID string, data any) {
	writeJSONStatus(w, status, map[string]any{"ok": true, "version": "v1", "request_id": requestID, "data": data})
}

func writeError(w http.ResponseWriter, status int, requestID, code, message string) {
	writeJSONStatus(w, status, map[string]any{"ok": false, "version": "v1", "request_id": requestID, "error": map[string]any{
		"code": code, "message": message, "retryable": false,
	}})
}

func writeJSON(w http.ResponseWriter, status int, value any) { writeJSONStatus(w, status, value) }

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
