package application

import (
	"context"
	"sort"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	graphretrieval "github.com/taimufuraiyaa/agent-memory/internal/retrieval"
)

type RecallGraphContext struct {
	RevisionID     string                            `json:"revision_id"`
	Fresh          bool                              `json:"fresh"`
	CanonicalIDs   []string                          `json:"canonical_memory_ids,omitempty"`
	Local          *graphretrieval.GraphLocalResult  `json:"local,omitempty"`
	Global         *graphretrieval.GraphGlobalResult `json:"global,omitempty"`
	DegradedReason graphretrieval.GraphRouteReason   `json:"degraded_reason,omitempty"`
}

func (s *MemoryService) enrichRecallWithGraph(ctx context.Context, task string, direct []engine.RetrievalHit, snapshot contracts.GraphQuerySnapshot, mode graphretrieval.GraphQueryMode) ([]engine.RetrievalHit, *RecallGraphContext, error) {
	evidence := graphSnapshotEvidence(snapshot)
	memories, authorized, err := s.store.ResolveGraphCanonicalMemories(ctx, snapshot.Scope, evidence)
	if err != nil {
		return nil, nil, err
	}
	contextResult := &RecallGraphContext{RevisionID: snapshot.RevisionID, Fresh: snapshot.Fresh}
	pathScores := map[string]float64{}
	pathTrust := map[string]core.GraphTrustState{}
	evidenceCounts := map[string]int{}
	if mode == graphretrieval.GraphQueryLocal {
		additionalSeeds, seedErr := s.graphRecallAdditionalSeeds(ctx, task, snapshot)
		if seedErr != nil {
			return nil, nil, seedErr
		}
		local, expandErr := ExpandRecallLocalGraph(direct, additionalSeeds, snapshot, authorized)
		if expandErr != nil {
			return nil, nil, expandErr
		}
		contextResult.Local = &local
		for _, path := range local.Paths {
			trust := weakestGraphPathTrust(path)
			for _, item := range path.Evidence {
				if item.CanonicalKind != "memory" {
					continue
				}
				if path.PathScore > pathScores[item.CanonicalID] {
					pathScores[item.CanonicalID] = path.PathScore
					pathTrust[item.CanonicalID] = trust
				}
				evidenceCounts[item.CanonicalID]++
			}
		}
	} else {
		global, selectErr := SelectRecallGlobalGraph(task, snapshot, authorized)
		if selectErr != nil {
			return nil, nil, selectErr
		}
		contextResult.Global = &global
		for _, community := range global.Communities {
			for _, item := range community.Evidence {
				if item.CanonicalKind != "memory" {
					continue
				}
				if community.Rank > pathScores[item.CanonicalID] {
					pathScores[item.CanonicalID] = community.Rank
					pathTrust[item.CanonicalID] = community.Trust
				}
				evidenceCounts[item.CanonicalID]++
			}
		}
	}

	directByID := make(map[string]engine.RetrievalHit, len(direct))
	candidates := make([]graphretrieval.GraphHybridCandidate, 0, len(direct)+len(memories))
	for _, hit := range direct {
		directByID[hit.Memory.ID] = hit
		candidates = append(candidates, graphretrieval.GraphHybridCandidate{
			Memory: hit.Memory, BaseScore: hit.Score, PathScore: pathScores[hit.Memory.ID],
			Trust: pathTrust[hit.Memory.ID], EvidenceCount: evidenceCounts[hit.Memory.ID],
			SourceKey: graphMemorySourceKey(hit.Memory), Direct: true,
		})
	}
	for id, memory := range memories {
		if _, directHit := directByID[id]; directHit || pathScores[id] <= 0 {
			continue
		}
		candidates = append(candidates, graphretrieval.GraphHybridCandidate{
			Memory: memory, PathScore: pathScores[id], Trust: pathTrust[id], EvidenceCount: evidenceCounts[id], SourceKey: graphMemorySourceKey(memory),
		})
	}
	ranked := graphretrieval.RerankGraphCandidates(candidates, graphretrieval.DefaultGraphRerankPolicy())
	result := make([]engine.RetrievalHit, 0, len(ranked))
	for _, candidate := range ranked {
		hit, ok := directByID[candidate.Memory.ID]
		if !ok {
			hit = engine.RetrievalHit{Memory: candidate.Memory, Band: engine.BandWeakFamiliarity}
		}
		hit.Score = candidate.AdjustedScore
		hit.Breakdown.Total = candidate.AdjustedScore
		result = append(result, hit)
		contextResult.CanonicalIDs = append(contextResult.CanonicalIDs, candidate.Memory.ID)
	}
	sort.Strings(contextResult.CanonicalIDs)
	return result, contextResult, nil
}

func ExpandRecallLocalGraph(direct []engine.RetrievalHit, additionalSeeds []graphretrieval.GraphLocalSeed, snapshot contracts.GraphQuerySnapshot, authorized map[string]struct{}) (graphretrieval.GraphLocalResult, error) {
	seeds := make([]graphretrieval.GraphLocalSeed, 0, len(direct))
	for _, hit := range direct {
		score := hit.Score
		if score < 0 {
			score = 0
		}
		if score > 1 {
			score = 1
		}
		seeds = append(seeds, graphretrieval.GraphLocalSeed{CanonicalKind: "memory", CanonicalID: hit.Memory.ID, Score: score})
	}
	seeds = append(seeds, additionalSeeds...)
	nodes := make([]graphretrieval.GraphLocalNode, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodes = append(nodes, graphretrieval.GraphLocalNode{ID: node.Entity.ID, Trust: node.Entity.Trust, Evidence: node.Evidence})
	}
	edges := make([]graphretrieval.GraphLocalEdge, 0, len(snapshot.Edges))
	for _, edge := range snapshot.Edges {
		edges = append(edges, graphretrieval.GraphLocalEdge{ID: edge.Edge.ID, SourceID: edge.Edge.SourceEntityID, TargetID: edge.Edge.TargetEntityID, Kind: core.GraphRelationshipKind(edge.Edge.NormalizedKind), Trust: edge.Edge.Trust, Weight: edge.Version.Weight, Evidence: edge.Evidence})
	}
	return graphretrieval.ExpandLocalGraph(graphretrieval.GraphLocalRequest{Scope: snapshot.Scope, Seeds: seeds, Nodes: nodes, Edges: edges, AuthorizedEvidence: authorized, Limits: graphretrieval.DefaultGraphLocalLimits()})
}

func (s *MemoryService) graphRecallAdditionalSeeds(ctx context.Context, task string, snapshot contracts.GraphQuerySnapshot) ([]graphretrieval.GraphLocalSeed, error) {
	seeds := make([]graphretrieval.GraphLocalSeed, 0, 16)
	termQuery := graphTermSeedQuery(task)
	if termQuery != "" {
		terms, err := s.SearchTerms(ctx, TermSearchOptions{Workspace: snapshot.Scope.WorkspaceID, Query: termQuery, Operator: TermOperatorOR, TopK: 8})
		if err != nil {
			return nil, err
		}
		for index, hit := range terms.Hits {
			score := 0.75 - 0.03*float64(index)
			seeds = append(seeds, graphretrieval.GraphLocalSeed{CanonicalKind: "memory", CanonicalID: hit.Memory.ID, Score: score})
		}
	}
	queryTerms := normalizedGraphQueryTerms(task)
	for _, node := range snapshot.Nodes {
		if !graphEntityMatchesTerms(node.Version, queryTerms) {
			continue
		}
		for _, evidence := range node.Evidence {
			seeds = append(seeds, graphretrieval.GraphLocalSeed{CanonicalKind: evidence.CanonicalKind, CanonicalID: evidence.CanonicalID, Score: 0.65})
		}
	}
	return seeds, nil
}

func graphTermSeedQuery(query string) string {
	values := make([]string, 0, 3)
	seen := map[string]struct{}{}
	for _, value := range strings.Fields(strings.ToLower(query)) {
		value = strings.Trim(value, " \t\r\n.,:;!?()[]{}\"'")
		if len(value) < 3 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
		if len(values) == 3 {
			break
		}
	}
	return strings.Join(values, " ")
}

func normalizedGraphQueryTerms(query string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, value := range strings.Fields(strings.ToLower(query)) {
		value = strings.Trim(value, " \\t\\r\\n.,:;!?()[]{}\"'")
		if len(value) >= 3 {
			result[value] = struct{}{}
		}
	}
	return result
}

func graphEntityMatchesTerms(version core.GraphEntityVersion, queryTerms map[string]struct{}) bool {
	if len(queryTerms) == 0 {
		return false
	}
	values := append([]string{version.Name}, version.Aliases...)
	for _, value := range values {
		for _, term := range strings.Fields(strings.ToLower(value)) {
			term = strings.Trim(term, " \\t\\r\\n.,:;!?()[]{}\"'")
			if _, ok := queryTerms[term]; ok {
				return true
			}
		}
	}
	return false
}

func SelectRecallGlobalGraph(task string, snapshot contracts.GraphQuerySnapshot, authorized map[string]struct{}) (graphretrieval.GraphGlobalResult, error) {
	nodeEvidence := map[string][]core.GraphEvidence{}
	edgeEvidence := map[string][]core.GraphEvidence{}
	for _, node := range snapshot.Nodes {
		nodeEvidence[node.Entity.ID] = node.Evidence
	}
	for _, edge := range snapshot.Edges {
		edgeEvidence[edge.Edge.ID] = edge.Evidence
	}
	candidates := make([]graphretrieval.GraphGlobalCommunity, 0, len(snapshot.Communities))
	for _, community := range snapshot.Communities {
		var evidence []core.GraphEvidence
		for _, member := range community.Members {
			switch member.Kind {
			case "entity":
				evidence = append(evidence, nodeEvidence[member.TargetID]...)
			case "edge", "relationship":
				evidence = append(evidence, edgeEvidence[member.TargetID]...)
			}
		}
		candidates = append(candidates, graphretrieval.GraphGlobalCommunity{
			ID: community.Community.ID, Level: community.Community.Level, Rank: community.Report.Rank,
			Trust: community.Report.Trust, Fresh: !community.Report.Stale, SourceCount: community.Community.SourceCount,
			UnresolvedCount: community.Community.UnresolvedCount + community.Report.UnresolvedCount,
			Title:           community.Report.Title, Summary: community.Report.Summary, Findings: community.Report.Findings, Evidence: evidence,
		})
	}
	return graphretrieval.SelectGlobalCommunities(graphretrieval.GraphGlobalRequest{Scope: snapshot.Scope, Query: task, Candidates: candidates, AuthorizedEvidence: authorized, Limits: graphretrieval.DefaultGraphGlobalLimits()})
}

func graphSnapshotEvidence(snapshot contracts.GraphQuerySnapshot) []core.GraphEvidence {
	var result []core.GraphEvidence
	for _, node := range snapshot.Nodes {
		result = append(result, node.Evidence...)
	}
	for _, edge := range snapshot.Edges {
		result = append(result, edge.Evidence...)
	}
	return result
}

func weakestGraphPathTrust(path graphretrieval.GraphLocalPath) core.GraphTrustState {
	trust := core.GraphTrustApproved
	for _, hop := range path.Hops {
		if hop.Trust == core.GraphTrustProposed {
			return core.GraphTrustProposed
		}
		if hop.Trust == core.GraphTrustReviewed {
			trust = core.GraphTrustReviewed
		}
	}
	return trust
}

func graphMemorySourceKey(memory core.MemoryEntry) string {
	for _, value := range []string{memory.Source.NoteID, memory.Source.FilePath, memory.Source.SessionID} {
		if strings.TrimSpace(value) != "" {
			return string(memory.Source.Type) + ":" + strings.TrimSpace(value)
		}
	}
	return string(memory.Source.Type) + ":" + memory.ID
}

func degradedGraphRoute(route graphretrieval.GraphRouteDecision, reason graphretrieval.GraphRouteReason) graphretrieval.GraphRouteDecision {
	route.SelectedMode = graphretrieval.GraphQueryBasic
	route.ReasonCode = reason
	route.Fallback = true
	route.Degraded = true
	return route
}
