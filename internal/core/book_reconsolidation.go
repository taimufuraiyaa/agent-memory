package core

import "time"

type BookReconsolidation struct {
	ID               string                `json:"id"`
	PreviousMemoryID string                `json:"previous_memory_id"`
	NewMemoryID      string                `json:"new_memory_id"`
	Action           ReconsolidationAction `json:"action"`
	CitationIDs      []string              `json:"citation_ids"`
	CreatedAt        time.Time             `json:"created_at"`
}
