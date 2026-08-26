package retrieval

import (
	"fmt"
	"sort"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type GraphGlobalCommunity struct {
	ID              string               `json:"id"`
	Level           int                  `json:"level"`
	Rank            float64              `json:"rank"`
	Trust           core.GraphTrustState `json:"trust"`
	Fresh           bool                 `json:"fresh"`
	SourceCount     int                  `json:"source_count"`
	UnresolvedCount int                  `json:"unresolved_count"`
	Title           string               `json:"title"`
	Summary         string               `json:"summary"`
	Findings        []string             `json:"findings,omitempty"`
	Evidence        []core.GraphEvidence `json:"evidence"`
}

type GraphGlobalLimits struct {
	MaxCommunities int
	MaxEvidence    int
}

func DefaultGraphGlobalLimits() GraphGlobalLimits {
	return GraphGlobalLimits{MaxCommunities: 12, MaxEvidence: 128}
}

type GraphGlobalRequest struct {
	Scope              core.GraphScope
	Query              string
	Candidates         []GraphGlobalCommunity
	AuthorizedEvidence map[string]struct{}
	Limits             GraphGlobalLimits
}

type GraphGlobalResult struct {
	Communities        []GraphGlobalCommunity `json:"communities"`
	Evidence           []core.GraphEvidence   `json:"evidence"`
	CoveredSources     int                    `json:"covered_sources"`
	UnresolvedEvidence int                    `json:"unresolved_evidence"`
}

func SelectGlobalCommunities(request GraphGlobalRequest) (GraphGlobalResult, error) {
	if err := request.Scope.Validate(); err != nil {
		return GraphGlobalResult{}, err
	}
	if request.Limits.MaxCommunities < 1 || request.Limits.MaxCommunities > 64 || request.Limits.MaxEvidence < 1 || request.Limits.MaxEvidence > 1024 {
		return GraphGlobalResult{}, fmt.Errorf("global graph limits are outside policy")
	}
	candidates := append([]GraphGlobalCommunity(nil), request.Candidates...)
	for index := range candidates {
		candidates[index].Evidence = filterAuthorizedGlobalEvidence(request.Scope, candidates[index].Evidence, request.AuthorizedEvidence)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		iScore := graphCommunityQueryScore(request.Query, candidates[i])
		jScore := graphCommunityQueryScore(request.Query, candidates[j])
		if iScore != jScore {
			return iScore > jScore
		}
		if candidates[i].Rank != candidates[j].Rank {
			return candidates[i].Rank > candidates[j].Rank
		}
		return candidates[i].ID < candidates[j].ID
	})
	result := GraphGlobalResult{}
	seenEvidence := map[string]struct{}{}
	seenLevels := map[int]struct{}{}
	appendCandidate := func(candidate GraphGlobalCommunity) bool {
		if len(result.Communities) >= request.Limits.MaxCommunities || strings.TrimSpace(candidate.ID) == "" || !candidate.Fresh || !queryableGraphTrust(candidate.Trust) || len(candidate.Evidence) == 0 {
			return false
		}
		newEvidence := false
		for _, item := range candidate.Evidence {
			if _, seen := seenEvidence[GraphAuthorizationKey(item)]; !seen {
				newEvidence = true
				break
			}
		}
		if !newEvidence {
			return false
		}
		result.Communities = append(result.Communities, candidate)
		result.CoveredSources += candidate.SourceCount
		result.UnresolvedEvidence += candidate.UnresolvedCount
		seenLevels[candidate.Level] = struct{}{}
		for _, item := range candidate.Evidence {
			key := GraphAuthorizationKey(item)
			if _, seen := seenEvidence[key]; seen || len(result.Evidence) >= request.Limits.MaxEvidence {
				continue
			}
			seenEvidence[key] = struct{}{}
			result.Evidence = append(result.Evidence, item)
		}
		return true
	}
	// First pass reserves diversity across hierarchy levels.
	for _, candidate := range candidates {
		if _, seen := seenLevels[candidate.Level]; !seen {
			appendCandidate(candidate)
		}
	}
	for _, candidate := range candidates {
		already := false
		for _, selected := range result.Communities {
			already = already || selected.ID == candidate.ID
		}
		if !already {
			appendCandidate(candidate)
		}
	}
	return result, nil
}

func graphCommunityQueryScore(query string, community GraphGlobalCommunity) float64 {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return community.Rank
	}
	text := strings.ToLower(strings.Join(append([]string{community.Title, community.Summary}, community.Findings...), " "))
	matched := 0
	for _, term := range terms {
		term = strings.Trim(term, " \t\r\n.,:;!?()[]{}\"'")
		if len(term) >= 3 && strings.Contains(text, term) {
			matched++
		}
	}
	return 0.65*(float64(matched)/float64(len(terms))) + 0.35*community.Rank
}

func filterAuthorizedGlobalEvidence(scope core.GraphScope, values []core.GraphEvidence, authorized map[string]struct{}) []core.GraphEvidence {
	result := make([]core.GraphEvidence, 0, len(values))
	seen := map[string]struct{}{}
	for _, item := range values {
		key := GraphAuthorizationKey(item)
		if item.Scope != scope {
			continue
		}
		if _, ok := authorized[key]; !ok {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return GraphAuthorizationKey(result[i]) < GraphAuthorizationKey(result[j]) })
	return result
}
