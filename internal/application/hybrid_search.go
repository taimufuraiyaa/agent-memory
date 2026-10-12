package application

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	graphretrieval "github.com/taimufuraiyaa/agent-memory/internal/retrieval"
)

type GraphSearchOptions struct {
	Retrieval       engine.RetrievalOptions
	GraphMode       graphretrieval.GraphQueryMode
	GraphRequired   bool
	GraphAllowStale bool
}

type GraphSearchResult struct {
	*engine.RetrievalResult
	GraphRoute   graphretrieval.GraphRouteDecision `json:"graph_route"`
	GraphContext *RecallGraphContext               `json:"graph_context,omitempty"`
}

// SearchHybrid unions independent semantic and exact-term discoveries with
// bounded graph paths, then resolves and ranks current canonical records.
func (s *MemoryService) SearchHybrid(ctx context.Context, options GraphSearchOptions) (*GraphSearchResult, error) {
	opt := options.Retrieval
	if opt.TopK <= 0 {
		opt.TopK = 5
	}
	if opt.TopK > 1000 {
		return nil, fmt.Errorf("top-k must not exceed 1000")
	}
	if opt.Mode == "" {
		opt.Mode = engine.ModeSearch
	}
	mode := options.GraphMode
	if mode == "" {
		mode = graphretrieval.GraphQueryAuto
	}
	if opt.Mode != engine.ModeSearch {
		if options.GraphRequired || (mode != graphretrieval.GraphQueryAuto && mode != graphretrieval.GraphQueryBasic) {
			return nil, fmt.Errorf("%w: graph discovery requires search mode", graphretrieval.ErrGraphRouteInvalid)
		}
		mode = graphretrieval.GraphQueryBasic
	}
	availability := graphretrieval.GraphRouteAvailability{}
	var snapshot *contracts.GraphQuerySnapshot
	var readErr error
	// Direct auto queries need no graph snapshot or adapter invocation.
	if mode != graphretrieval.GraphQueryBasic && (mode != graphretrieval.GraphQueryAuto || graphretrieval.ClassifyGraphIntent(opt.Query) != graphretrieval.GraphIntentDirect) {
		loaded, err := s.store.LoadActiveGraphSnapshot(ctx, core.GraphScope{WorkspaceID: opt.Workspace}, 4096, 16384, 256)
		if err == nil {
			snapshot = &loaded
			availability = graphretrieval.GraphRouteAvailability{Readable: true, Fresh: loaded.Fresh, ActiveRevisionID: loaded.RevisionID}
			opt.GraphCacheIdentity = loaded.CacheIdentity
		} else if !errors.Is(err, sql.ErrNoRows) {
			readErr = err
		}
	}
	route, err := graphretrieval.NewGraphRouter().Route(graphretrieval.GraphRouteRequest{Mode: mode, Query: opt.Query, RequireGraph: options.GraphRequired,
		Policy: graphretrieval.GraphRoutePolicy{GraphEnabled: true, AllowLocal: true, AllowGlobal: true, AllowStale: options.GraphAllowStale}, Availability: availability})
	if err != nil {
		return nil, err
	}
	if readErr != nil {
		if options.GraphRequired {
			return nil, fmt.Errorf("%w: graph read failed", graphretrieval.ErrGraphRouteRequired)
		}
		route = degradedGraphRoute(route, graphretrieval.GraphReasonReadFailed)
	}
	direct, err := s.search(ctx, opt, false)
	if err != nil {
		return nil, err
	}
	result := &GraphSearchResult{RetrievalResult: direct, GraphRoute: route}
	defer func() {
		if s.store != nil {
			tokens := hitTokens(result.Hits)
			_ = s.store.AddTokenMetricV2(ctx, opt.Workspace, "search", tokens, tokens, engine.RunLabel(), engine.MemoryEnabled())
		}
	}()
	// Legacy relationship expansion keeps its existing contract.
	if opt.Mode != engine.ModeSearch {
		return result, nil
	}
	hits := append([]engine.RetrievalHit(nil), direct.Hits...)
	seen := map[string]bool{}
	directIDs := map[string]bool{}
	for _, hit := range hits {
		seen[hit.Memory.ID] = true
		directIDs[hit.Memory.ID] = true
	}
	if query := graphTermSeedQuery(opt.Query); query != "" {
		terms, termErr := s.SearchTerms(ctx, TermSearchOptions{Workspace: opt.Workspace, Query: query, Operator: TermOperatorOR, TopK: min(max(opt.TopK*3, 16), 128), Filters: opt.Filters})
		if termErr != nil {
			return nil, termErr
		}
		additional := []core.MemoryEntry{}
		for _, hit := range terms.Hits {
			if !seen[hit.Memory.ID] {
				seen[hit.Memory.ID] = true
				additional = append(additional, hit.Memory)
			}
		}
		scored, scoreErr := s.retrieval.ScoreCandidates(ctx, opt, additional)
		if scoreErr != nil {
			return nil, scoreErr
		}
		hits = append(hits, scored...)
	}
	// Lexical BM25 discovery is independent of embeddings and fail-open.
	lexicalRelative := map[string]float64{}
	if lexicalMemories, relative := s.lexicalCandidates(ctx, opt); len(lexicalMemories) > 0 {
		lexicalRelative = relative
		additional := make([]core.MemoryEntry, 0, len(lexicalMemories))
		for _, memory := range lexicalMemories {
			if !seen[memory.ID] {
				seen[memory.ID] = true
				additional = append(additional, memory)
			}
		}
		if scored, scoreErr := s.retrieval.ScoreCandidates(ctx, opt, additional); scoreErr == nil {
			hits = append(hits, scored...)
		}
		result.Weights.Lexical = engine.LexicalWeight()
	}
	// Cached direct hits are reauthorized against the current canonical rows.
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.Memory.ID)
	}
	current, err := s.store.GetMemoriesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	valid := hits[:0]
	for _, hit := range hits {
		memory, ok := current[hit.Memory.ID]
		if ok && core.FingerprintText(memory.Content) == core.FingerprintText(hit.Memory.Content) && engine.MatchCandidateFilters(memory, opt) {
			hit.Memory = memory
			valid = append(valid, hit)
		}
	}
	hits = valid
	if len(lexicalRelative) > 0 {
		weight := engine.LexicalWeight()
		for i := range hits {
			engine.ApplyLexicalSignal(&hits[i], lexicalRelative[hits[i].Memory.ID], weight)
		}
	}
	if snapshot != nil && (route.SelectedMode == graphretrieval.GraphQueryLocal || route.SelectedMode == graphretrieval.GraphQueryGlobal) {
		enriched, graphContext, enrichErr := s.enrichWithGraph(ctx, opt, hits, *snapshot, route.SelectedMode)
		if enrichErr != nil {
			if options.GraphRequired {
				return nil, fmt.Errorf("%w: graph enrichment failed", graphretrieval.ErrGraphRouteRequired)
			}
			result.GraphRoute = degradedGraphRoute(route, graphretrieval.GraphReasonReadFailed)
			result.GraphContext = &RecallGraphContext{RevisionID: snapshot.RevisionID, Fresh: snapshot.Fresh, DegradedReason: graphretrieval.GraphReasonReadFailed}
		} else {
			result.GraphContext = graphContext
			usable := graphContext.Local != nil && len(graphContext.Local.Paths) > 0 || graphContext.Global != nil && len(graphContext.Global.Communities) > 0
			if !usable {
				if options.GraphRequired {
					return nil, &graphretrieval.GraphRouteError{Mode: route.SelectedMode, Reason: graphretrieval.GraphReasonNoEvidence}
				}
				result.GraphRoute = degradedGraphRoute(route, graphretrieval.GraphReasonNoEvidence)
				result.GraphContext.DegradedReason = graphretrieval.GraphReasonNoEvidence
			} else {
				hits = enriched
			}
		}
	}
	if result.GraphContext == nil || result.GraphRoute.Fallback {
		sort.SliceStable(hits, func(i, j int) bool {
			if hits[i].Score == hits[j].Score {
				return hits[i].Memory.ID < hits[j].Memory.ID
			}
			return hits[i].Score > hits[j].Score
		})
	}
	if len(hits) > opt.TopK {
		hits = hits[:opt.TopK]
	}
	result.Hits = hits
	additionalIDs := []string{}
	for _, hit := range hits {
		if !directIDs[hit.Memory.ID] {
			additionalIDs = append(additionalIDs, hit.Memory.ID)
		}
	}
	if len(additionalIDs) > 0 {
		_ = s.store.MarkAccessed(ctx, additionalIDs, time.Now().UTC())
	}
	result.StrongHits = nil
	result.WeakHits = nil
	for _, hit := range hits {
		if hit.Band == engine.BandStrongRecall {
			result.StrongHits = append(result.StrongHits, hit)
		} else {
			result.WeakHits = append(result.WeakHits, hit)
		}
	}
	return result, nil
}
