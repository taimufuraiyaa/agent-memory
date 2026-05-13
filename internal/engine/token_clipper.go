package engine

import "strings"

// ClippingReason explains why an item was not included.
type ClippingReason string

const (
	ReasonBudgetExceeded ClippingReason = "budget_exceeded"
	ReasonItemTooLarge   ClippingReason = "item_too_large"
)

// ClippedItem describes a skipped hit.
type ClippedItem struct {
	ID     string         `json:"id"`
	Tokens int            `json:"tokens"`
	Reason ClippingReason `json:"reason"`
}

// ClipMetadata explains clipping behavior.
type ClipMetadata struct {
	Budget         int           `json:"budget"`
	UsedTokens     int           `json:"used_tokens"`
	IncludedCount  int           `json:"included_count"`
	ClippedCount   int           `json:"clipped_count"`
	IncludedIDs    []string      `json:"included_ids"`
	ClippedDetails []ClippedItem `json:"clipped_details"`
}

// TokenCounter abstracts token estimators.
type TokenCounter interface {
	Count(text string) int
}

// WhitespaceCounter is deterministic and cheap; used as current baseline.
type WhitespaceCounter struct{}

func (WhitespaceCounter) Count(text string) int {
	return len(strings.Fields(text))
}

// TokenClipper enforces hard token budgets.
type TokenClipper struct {
	counter TokenCounter
}

// NewTokenClipper creates a clipper.
func NewTokenClipper(counter TokenCounter) *TokenClipper {
	if counter == nil {
		counter = WhitespaceCounter{}
	}
	return &TokenClipper{counter: counter}
}

// Clip enforces budget and returns included hits + metadata.
func (c *TokenClipper) Clip(hits []RetrievalHit, budget int) ([]RetrievalHit, ClipMetadata) {
	if budget <= 0 {
		budget = 1
	}
	out := make([]RetrievalHit, 0, len(hits))
	meta := ClipMetadata{
		Budget:      budget,
		IncludedIDs: make([]string, 0, len(hits)),
	}
	for _, h := range hits {
		t := c.counter.Count(memoryTextForRecall(h.Memory))
		if t > budget && meta.UsedTokens == 0 {
			meta.ClippedDetails = append(meta.ClippedDetails, ClippedItem{
				ID:     h.Memory.ID,
				Tokens: t,
				Reason: ReasonItemTooLarge,
			})
			continue
		}
		if meta.UsedTokens+t > budget {
			meta.ClippedDetails = append(meta.ClippedDetails, ClippedItem{
				ID:     h.Memory.ID,
				Tokens: t,
				Reason: ReasonBudgetExceeded,
			})
			continue
		}
		out = append(out, h)
		meta.UsedTokens += t
		meta.IncludedIDs = append(meta.IncludedIDs, h.Memory.ID)
	}
	meta.IncludedCount = len(out)
	meta.ClippedCount = len(meta.ClippedDetails)
	return out, meta
}
