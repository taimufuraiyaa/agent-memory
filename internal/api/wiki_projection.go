package api

import (
	"context"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/readingroom"
	"net/http"
	"sync"
)

type WikiProjectionRepository interface {
	ListAcceptedBookMemoryProposals(context.Context, string, string) ([]core.BookMemoryProposal, error)
	GetLibraryResourcePolicy(context.Context, library.ResourceType, string) (library.LibraryResourcePolicy, error)
}
type WikiStatement struct {
	ID          string              `json:"id"`
	Text        string              `json:"text"`
	Attribution core.Attribution    `json:"attribution"`
	Evidence    []core.Citation     `json:"evidence"`
	Derivation  core.DerivationKind `json:"derivation"`
	DerivedFrom []string            `json:"derived_from,omitempty"`
	ReviewState string              `json:"review_state"`
}
type WikiPage struct {
	EditionID                string          `json:"edition_id"`
	AuthorizationFingerprint string          `json:"authorization_fingerprint"`
	Statements               []WikiStatement `json:"statements"`
}
type WikiProjector struct {
	repository WikiProjectionRepository
	mu         sync.RWMutex
	cache      map[string]WikiPage
}

func NewWikiProjector(repository WikiProjectionRepository) *WikiProjector {
	return &WikiProjector{repository: repository, cache: map[string]WikiPage{}}
}
func (p *WikiProjector) Project(ctx context.Context, workspace, editionID string, scope core.AuthorizationScope, regenerate bool) (WikiPage, error) {
	fingerprint := readingroom.AuthorizationFingerprint(scope)
	key := workspace + "\x00" + editionID + "\x00" + fingerprint
	if !regenerate {
		p.mu.RLock()
		cached, ok := p.cache[key]
		p.mu.RUnlock()
		if ok {
			return cached, nil
		}
	}
	resource, err := p.repository.GetLibraryResourcePolicy(ctx, library.ResourceEdition, editionID)
	if err != nil || !core.Authorize(scope, resource.Policy, core.CapabilityReadSource).Allowed {
		return WikiPage{EditionID: editionID, AuthorizationFingerprint: fingerprint, Statements: []WikiStatement{}}, nil
	}
	proposals, err := p.repository.ListAcceptedBookMemoryProposals(ctx, workspace, scope.Principal.ID)
	if err != nil {
		return WikiPage{}, err
	}
	page := WikiPage{EditionID: editionID, AuthorizationFingerprint: fingerprint, Statements: []WikiStatement{}}
	for _, proposal := range proposals {
		evidence := []core.Citation{}
		for _, citation := range proposal.Citations {
			if citation.EditionID == editionID {
				evidence = append(evidence, citation)
			}
		}
		if len(evidence) == 0 {
			continue
		}
		page.Statements = append(page.Statements, WikiStatement{ID: proposal.ID, Text: proposal.Content, Attribution: proposal.Provenance.Attribution, Evidence: evidence, Derivation: proposal.Provenance.Derivation, DerivedFrom: proposal.Provenance.DerivedFrom, ReviewState: string(proposal.Status)})
	}
	p.mu.Lock()
	p.cache[key] = page
	p.mu.Unlock()
	return page, nil
}
func wikiProjectionHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireLibrary(w) {
			return
		}
		if r.Method != http.MethodGet {
			writeErr(w, 405, "method_not_allowed", "method not allowed")
			return
		}
		workspace, principalID, editionID := r.URL.Query().Get("workspace"), r.URL.Query().Get("principal_id"), r.URL.Query().Get("edition_id")
		assets, err := svc.resolve(r.Context(), workspace)
		if err != nil {
			writeErr(w, 500, "runtime", err.Error())
			return
		}
		svc.mu.Lock()
		if svc.wikiProjectors == nil {
			svc.wikiProjectors = map[string]*WikiProjector{}
		}
		projector := svc.wikiProjectors[workspace]
		if projector == nil {
			projector = NewWikiProjector(assets.Store)
			svc.wikiProjectors[workspace] = projector
		}
		svc.mu.Unlock()
		page, err := projector.Project(r.Context(), workspace, editionID, libraryScope(principalID, nil), r.URL.Query().Get("regenerate") == "true")
		if err != nil {
			writeErr(w, 500, "runtime", err.Error())
			return
		}
		writeOK(w, 200, page)
	}
}
