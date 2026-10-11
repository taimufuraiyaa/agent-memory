package retrieval

import "strings"

type GraphContextClipItem struct {
	ID     string `json:"id"`
	Tokens int    `json:"tokens"`
	Reason string `json:"reason"`
}

type GraphContextClipMetadata struct {
	Budget         int                    `json:"budget"`
	UsedTokens     int                    `json:"used_tokens"`
	IncludedCount  int                    `json:"included_count"`
	ClippedCount   int                    `json:"clipped_count"`
	ClippedDetails []GraphContextClipItem `json:"clipped_details,omitempty"`
}

func ClipGraphContext(candidates []GraphHybridCandidate, budget int) ([]GraphHybridCandidate, GraphContextClipMetadata) {
	if budget < 1 {
		budget = 1
	}
	metadata := GraphContextClipMetadata{Budget: budget}
	result := make([]GraphHybridCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		tokens := len(strings.Fields(candidate.Memory.Content))
		if tokens > budget && metadata.UsedTokens == 0 {
			metadata.ClippedDetails = append(metadata.ClippedDetails, GraphContextClipItem{ID: candidate.Memory.ID, Tokens: tokens, Reason: "item_too_large"})
			continue
		}
		if metadata.UsedTokens+tokens > budget {
			metadata.ClippedDetails = append(metadata.ClippedDetails, GraphContextClipItem{ID: candidate.Memory.ID, Tokens: tokens, Reason: "budget_exceeded"})
			continue
		}
		result = append(result, candidate)
		metadata.UsedTokens += tokens
	}
	metadata.IncludedCount = len(result)
	metadata.ClippedCount = len(metadata.ClippedDetails)
	return result, metadata
}
