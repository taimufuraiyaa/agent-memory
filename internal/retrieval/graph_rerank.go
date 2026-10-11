package retrieval

import (
	"sort"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type GraphHybridCandidate struct {
	Memory        core.MemoryEntry     `json:"memory"`
	BaseScore     float64              `json:"base_score"`
	PathScore     float64              `json:"path_score,omitempty"`
	AdjustedScore float64              `json:"adjusted_score"`
	Trust         core.GraphTrustState `json:"trust,omitempty"`
	EvidenceCount int                  `json:"evidence_count,omitempty"`
	SourceKey     string               `json:"source_key,omitempty"`
	Direct        bool                 `json:"direct"`
	Conflict      bool                 `json:"conflict"`
}

type GraphRerankPolicy struct {
	MaxCandidates   int
	DiversityWindow int
}

func DefaultGraphRerankPolicy() GraphRerankPolicy {
	return GraphRerankPolicy{MaxCandidates: 64, DiversityWindow: 8}
}

func RerankGraphCandidates(input []GraphHybridCandidate, policy GraphRerankPolicy) []GraphHybridCandidate {
	if policy.MaxCandidates < 1 {
		policy.MaxCandidates = 64
	}
	if policy.DiversityWindow < 0 {
		policy.DiversityWindow = 0
	}
	byContent := map[string]GraphHybridCandidate{}
	for _, candidate := range input {
		if candidate.Memory.ID == "" || candidate.Memory.SupersededBy != nil {
			continue
		}
		candidate.AdjustedScore = graphAdjustedScore(candidate)
		key := strings.ToLower(strings.Join(strings.Fields(candidate.Memory.Content), " "))
		if key == "" {
			key = "id:" + candidate.Memory.ID
		}
		prior, exists := byContent[key]
		if !exists || preferGraphCandidate(candidate, prior) {
			byContent[key] = candidate
		}
	}
	values := make([]GraphHybridCandidate, 0, len(byContent))
	for _, candidate := range byContent {
		values = append(values, candidate)
	}
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].AdjustedScore != values[j].AdjustedScore {
			return values[i].AdjustedScore > values[j].AdjustedScore
		}
		if values[i].Direct != values[j].Direct {
			return values[i].Direct
		}
		return values[i].Memory.ID < values[j].Memory.ID
	})
	values = diversifyGraphSources(values, policy.DiversityWindow)
	if len(values) > policy.MaxCandidates {
		values = values[:policy.MaxCandidates]
	}
	return values
}

func graphAdjustedScore(candidate GraphHybridCandidate) float64 {
	base := clampGraphScore(candidate.BaseScore)
	if !candidate.Direct {
		base = 0.12*clampGraphScore(candidate.Memory.Confidence) + 0.08*clampGraphScore(candidate.Memory.DecayScore) + 0.05*clampGraphScore(candidate.Memory.SalienceScore)
		feedbackTotal := candidate.Memory.UsefulCount + candidate.Memory.IgnoredCount + candidate.Memory.RejectedCount + candidate.Memory.HarmfulCount
		if feedbackTotal > 0 {
			feedback := float64(candidate.Memory.UsefulCount-candidate.Memory.RejectedCount-2*candidate.Memory.HarmfulCount) / float64(feedbackTotal)
			base += 0.05 * feedback
		}
		base -= 0.15 * clampGraphScore(candidate.Memory.SuppressionScore)
		base = clampGraphScore(base)
	}
	if candidate.Conflict {
		return base
	}
	trust := map[core.GraphTrustState]float64{
		core.GraphTrustApproved: 1, core.GraphTrustReviewed: 0.8, core.GraphTrustProposed: 0.25,
	}[candidate.Trust]
	evidence := float64(candidate.EvidenceCount) / 3
	if evidence > 1 {
		evidence = 1
	}
	boost := 0.25 * clampGraphScore(candidate.PathScore) * trust * evidence
	return clampGraphScore(base + boost)
}

func preferGraphCandidate(candidate, prior GraphHybridCandidate) bool {
	if candidate.Direct != prior.Direct {
		return candidate.Direct
	}
	if candidate.Conflict != prior.Conflict {
		return !candidate.Conflict
	}
	if candidate.AdjustedScore != prior.AdjustedScore {
		return candidate.AdjustedScore > prior.AdjustedScore
	}
	return candidate.Memory.ID < prior.Memory.ID
}

func diversifyGraphSources(values []GraphHybridCandidate, window int) []GraphHybridCandidate {
	if window < 2 || len(values) < 2 {
		return values
	}
	if window > len(values) {
		window = len(values)
	}
	result := make([]GraphHybridCandidate, 0, len(values))
	used := make([]bool, len(values))
	seenSources := map[string]struct{}{}
	for len(result) < window {
		selected := -1
		for index, candidate := range values {
			if used[index] {
				continue
			}
			key := candidate.SourceKey
			if key == "" {
				key = "memory:" + candidate.Memory.ID
			}
			if _, seen := seenSources[key]; !seen {
				selected = index
				seenSources[key] = struct{}{}
				break
			}
		}
		if selected < 0 {
			break
		}
		used[selected] = true
		result = append(result, values[selected])
	}
	for index, candidate := range values {
		if !used[index] {
			result = append(result, candidate)
		}
	}
	return result
}

func clampGraphScore(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
