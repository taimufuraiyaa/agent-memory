package core

import (
	"errors"
	"strings"
	"time"
)

type KnowledgeEdgeKind string

const (
	EdgeSupports    KnowledgeEdgeKind = "supports"
	EdgeChallenges  KnowledgeEdgeKind = "challenges"
	EdgeContradicts KnowledgeEdgeKind = "contradicts"
	EdgeElaborates  KnowledgeEdgeKind = "elaborates"
	EdgeAppliesTo   KnowledgeEdgeKind = "applies_to"
	EdgeSimilarTo   KnowledgeEdgeKind = "similar_to"
	EdgeDerivedFrom KnowledgeEdgeKind = "derived_from"
)

type ReviewState string

const (
	ReviewProposed   ReviewState = "proposed"
	ReviewReviewed   ReviewState = "reviewed"
	ReviewApproved   ReviewState = "approved"
	ReviewRejected   ReviewState = "rejected"
	ReviewSuperseded ReviewState = "superseded"
)

type KnowledgeEdge struct {
	ID                  string            `json:"id"`
	FromID              string            `json:"from_id"`
	ToID                string            `json:"to_id"`
	Kind                KnowledgeEdgeKind `json:"kind"`
	EvidenceCitationIDs []string          `json:"evidence_citation_ids"`
	Creator             Principal         `json:"creator"`
	Confidence          float64           `json:"confidence"`
	ReviewState         ReviewState       `json:"review_state"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
}

func (e KnowledgeEdge) Validate() error {
	if strings.TrimSpace(e.ID) == "" || strings.TrimSpace(e.FromID) == "" || strings.TrimSpace(e.ToID) == "" || e.FromID == e.ToID || len(e.EvidenceCitationIDs) == 0 || e.CreatedAt.IsZero() || e.UpdatedAt.IsZero() {
		return errors.New("knowledge edge requires identity, distinct endpoints, evidence, and timestamps")
	}
	if err := e.Creator.Validate(); err != nil {
		return err
	}
	switch e.Kind {
	case EdgeSupports, EdgeChallenges, EdgeContradicts, EdgeElaborates, EdgeAppliesTo, EdgeSimilarTo, EdgeDerivedFrom:
	default:
		return errors.New("invalid knowledge edge kind")
	}
	switch e.ReviewState {
	case ReviewProposed, ReviewReviewed, ReviewApproved, ReviewRejected, ReviewSuperseded:
	default:
		return errors.New("invalid edge review state")
	}
	if e.Confidence < 0 || e.Confidence > 1 {
		return errors.New("edge confidence must be between zero and one")
	}
	return nil
}
