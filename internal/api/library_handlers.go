package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/ingestion"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/readingroom"
	"github.com/taimufuraiyaa/agent-memory/internal/retrieval"
)

type LibraryImportJob struct {
	ID        string                 `json:"id"`
	State     string                 `json:"state"`
	Result    ingestion.ImportResult `json:"result,omitempty"`
	Error     string                 `json:"error,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

func requireLibrary(w http.ResponseWriter) bool {
	if engine.LibraryEnabled() {
		return true
	}
	writeErr(w, http.StatusNotFound, "not_found", "route not enabled")
	return false
}

func libraryImportHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireLibrary(w) {
			return
		}
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req LibraryImportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if strings.TrimSpace(req.Workspace) == "" || strings.TrimSpace(req.LibraryID) == "" || strings.TrimSpace(req.PrincipalID) == "" {
			writeErr(w, http.StatusBadRequest, "validation", "workspace, library_id, and principal_id are required")
			return
		}
		assets, err := svc.resolve(r.Context(), req.Workspace)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		principal := core.Principal{ID: req.PrincipalID, Kind: core.PrincipalUser}
		libraryValue := library.Library{ID: req.LibraryID, Kind: library.LibraryPersonal, Owner: principal}
		visibility := core.VisibilityPrivate
		organizationID := ""
		if req.LibraryKind == string(library.LibraryOrganization) {
			organizationID = strings.TrimSpace(req.OrganizationID)
			libraryValue = library.Library{ID: req.LibraryID, Kind: library.LibraryOrganization, Owner: core.Principal{ID: organizationID, Kind: core.PrincipalOrganization}, OrganizationID: organizationID}
			visibility = core.VisibilityOrganization
		}
		if err := assets.Store.PutLibrary(r.Context(), libraryValue); err != nil {
			writeErr(w, http.StatusBadRequest, "validation", err.Error())
			return
		}
		if libraryValue.Kind == library.LibraryOrganization {
			capabilities := libraryScope(req.PrincipalID, []string{organizationID}).Capabilities
			if err := assets.Store.PutMembership(r.Context(), library.Membership{LibraryID: req.LibraryID, PrincipalID: req.PrincipalID, Capabilities: capabilities, Version: "v1", Active: true}); err != nil {
				writeErr(w, http.StatusBadRequest, "validation", err.Error())
				return
			}
		}
		policy := core.SourcePolicy{Retention: core.RetentionRetained, StoreOriginal: true, StoreNormalized: true, AllowSearch: true, AllowQuote: true, MaxQuoteRunes: 280}
		adapter := ingestion.MarkdownAdapter{ParserVersion: "markdown-v1", NormalizationVersion: "text-v1"}
		result, err := ingestion.NewMarkdownImporter(assets.Store, adapter).Import(r.Context(), ingestion.MarkdownImportInput{Title: req.Title, EditionLabel: req.EditionLabel, Language: req.Language, Source: []byte(req.Markdown), Policy: policy})
		job := LibraryImportJob{ID: libraryID("job", req.Workspace, req.LibraryID, req.Title, req.Markdown), State: "completed", Result: result, CreatedAt: time.Now().UTC()}
		if err != nil {
			job.State, job.Error = "failed", err.Error()
		} else {
			access := core.AccessPolicy{Version: "v1", Ownership: core.ResourceOwnership{Owner: libraryValue.Owner, Visibility: visibility, OrganizationID: organizationID}}
			for resourceType, resourceID := range map[library.ResourceType]string{library.ResourceEdition: result.EditionID, library.ResourceSource: result.AssetID} {
				if putErr := assets.Store.PutLibraryResourcePolicy(r.Context(), library.LibraryResourcePolicy{LibraryID: req.LibraryID, ResourceType: resourceType, ResourceID: resourceID, Policy: access}); putErr != nil {
					job.State, job.Error = "failed", putErr.Error()
					break
				}
			}
		}
		svc.mu.Lock()
		if svc.libraryJobs == nil {
			svc.libraryJobs = map[string]LibraryImportJob{}
		}
		svc.libraryJobs[job.ID] = job
		svc.mu.Unlock()
		if payload, marshalErr := json.Marshal(job); marshalErr == nil {
			_ = assets.Store.PutLibraryImportJob(r.Context(), job.ID, req.Workspace, job.State, string(payload), job.CreatedAt)
		}
		if job.State == "failed" {
			writeErr(w, http.StatusBadRequest, "import_failed", job.Error)
			return
		}
		writeOK(w, http.StatusAccepted, job)
	}
}

func libraryJobHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireLibrary(w) {
			return
		}
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		id := r.URL.Query().Get("id")
		svc.mu.RLock()
		job, ok := svc.libraryJobs[id]
		svc.mu.RUnlock()
		if !ok && r.URL.Query().Get("workspace") != "" {
			if assets, err := svc.resolve(r.Context(), r.URL.Query().Get("workspace")); err == nil {
				if payload, err := assets.Store.GetLibraryImportJob(r.Context(), id); err == nil && json.Unmarshal([]byte(payload), &job) == nil {
					ok = true
				}
			}
		}
		if !ok {
			writeErr(w, http.StatusNotFound, "not_found", "import job not found")
			return
		}
		writeOK(w, http.StatusOK, job)
	}
}

func libraryStructureHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireLibrary(w) {
			return
		}
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		assets, err := svc.resolve(r.Context(), r.URL.Query().Get("workspace"))
		if err != nil {
			writeErr(w, 500, "runtime", err.Error())
			return
		}
		scope := libraryScope(r.URL.Query().Get("principal_id"), nil)
		editionID := r.URL.Query().Get("edition_id")
		if _, err = library.NewAuthorizedRepository(assets.Store).GetEdition(r.Context(), scope, editionID); err != nil {
			writeErr(w, http.StatusNotFound, "not_found", "edition not found")
			return
		}
		nodes, err := assets.Store.ListStructuralNodes(r.Context(), editionID)
		if err != nil {
			writeErr(w, 500, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"edition_id": editionID, "nodes": nodes})
	}
}

func libraryQueryHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireLibrary(w) {
			return
		}
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req LibraryQueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, 400, "bad_request", err.Error())
			return
		}
		assets, err := svc.resolve(r.Context(), req.Workspace)
		if err != nil {
			writeErr(w, 500, "runtime", err.Error())
			return
		}
		results, err := retrieval.NewLexicalPassageSearch(assets.Store).Search(r.Context(), libraryScope(req.PrincipalID, req.OrganizationIDs), req.Question, req.Limit)
		if err != nil {
			writeErr(w, 400, "query_failed", err.Error())
			return
		}
		var proposal *core.BookMemoryProposal
		if req.ProposeMemory {
			if len(results) == 0 {
				writeErr(w, http.StatusUnprocessableEntity, "unanswerable", "no authorized evidence found")
				return
			}
			citation, citeErr := library.NewCitationService(assets.Store).CitePassage(r.Context(), results[0].Passage.ID, "")
			if citeErr != nil {
				writeErr(w, 500, "citation_failed", citeErr.Error())
				return
			}
			content := strings.TrimSpace(req.MemoryContent)
			if content == "" {
				content = results[0].Passage.Text
			}
			statement := readingroom.AnswerStatement{ID: libraryID("statement", req.PrincipalID, content), Text: content, EvidenceState: readingroom.EvidenceInterpretation, Provenance: core.KnowledgeProvenance{Attribution: core.Attribution{Kind: core.AttributionAgent, SubjectID: "direct-study"}, Form: core.KnowledgeInsight, Derivation: core.DerivationInterpreted, CitationIDs: []string{citation.ID}}, Citations: []core.Citation{citation}, Confidence: 0.7}
			created, proposalErr := engine.NewBookMemoryService(assets.Writer, assets.Store).Propose(r.Context(), engine.BookMemoryProposalInput{ID: libraryID("proposal", req.Workspace, req.PrincipalID, content), Workspace: req.Workspace, RequestedBy: core.Principal{ID: req.PrincipalID, Kind: core.PrincipalUser}, MemoryType: core.SemanticMemory, Statement: statement, CreatedAt: time.Now().UTC()})
			if proposalErr != nil {
				writeErr(w, 400, "proposal_failed", proposalErr.Error())
				return
			}
			proposal = &created
		}
		writeOK(w, http.StatusOK, map[string]any{"results": results, "proposal": proposal})
	}
}

func libraryMemoryReviewHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireLibrary(w) {
			return
		}
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req LibraryMemoryReviewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, 400, "bad_request", err.Error())
			return
		}
		assets, err := svc.resolve(r.Context(), req.Workspace)
		if err != nil {
			writeErr(w, 500, "runtime", err.Error())
			return
		}
		service := engine.NewBookMemoryService(assets.Writer, assets.Store)
		reviewer := core.Principal{ID: req.PrincipalID, Kind: core.PrincipalUser}
		var proposal core.BookMemoryProposal
		switch req.Decision {
		case "accept":
			proposal, err = service.Accept(r.Context(), req.ProposalID, reviewer)
		case "reject":
			proposal, err = service.Reject(r.Context(), req.ProposalID, reviewer)
		default:
			err = errors.New("decision must be accept or reject")
		}
		if err != nil {
			writeErr(w, 400, "review_failed", err.Error())
			return
		}
		writeOK(w, http.StatusOK, proposal)
	}
}

func libraryScope(principalID string, organizations []string) core.AuthorizationScope {
	return core.AuthorizationScope{Principal: core.Principal{ID: principalID, Kind: core.PrincipalUser}, OrganizationIDs: organizations, Capabilities: []core.Capability{core.CapabilityReadSource, core.CapabilitySearchSource, core.CapabilityQuoteSource, core.CapabilityProposeKnowledge, core.CapabilityApproveKnowledge}, PolicyVersion: "v1"}
}

func libraryID(prefix string, values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return prefix + "_" + hex.EncodeToString(sum[:12])
}
