package core

import (
	"fmt"
	"strings"
)

type GraphRelationshipKind string

const (
	GraphRelationshipSupports    GraphRelationshipKind = "supports"
	GraphRelationshipContradicts GraphRelationshipKind = "contradicts"
	GraphRelationshipChallenges  GraphRelationshipKind = "challenges"
	GraphRelationshipSupersedes  GraphRelationshipKind = "supersedes"
	GraphRelationshipTemporal    GraphRelationshipKind = "temporal"
	GraphRelationshipCausal      GraphRelationshipKind = "causal"
	GraphRelationshipMembership  GraphRelationshipKind = "membership"
	GraphRelationshipSimilarity  GraphRelationshipKind = "similarity"
	GraphRelationshipExternal    GraphRelationshipKind = "external"
)

func NormalizeGraphRelationshipKind(value string) GraphRelationshipKind {
	normalized := strings.Join(strings.Fields(strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(strings.TrimSpace(value)))), " ")
	switch normalized {
	case "support", "supports", "supported by":
		return GraphRelationshipSupports
	case "contradicts", "contradicted by", "contradiction":
		return GraphRelationshipContradicts
	case "challenges", "challenged by", "challenge":
		return GraphRelationshipChallenges
	case "supersedes", "superseded by", "replaces":
		return GraphRelationshipSupersedes
	case "precedes", "follows", "before", "after", "temporal":
		return GraphRelationshipTemporal
	case "causes", "caused by", "causal":
		return GraphRelationshipCausal
	case "member of", "contains", "membership":
		return GraphRelationshipMembership
	case "similar to", "similarity", "related by similarity":
		return GraphRelationshipSimilarity
	default:
		return GraphRelationshipExternal
	}
}

// SupportsTraversal is intentionally narrow. Conflict, temporal, causal,
// membership, similarity, and opaque external edges retain their own semantics
// and cannot silently inflate a supporting path.
func (k GraphRelationshipKind) SupportsTraversal() bool { return k == GraphRelationshipSupports }

type GraphRelationshipOrigin string

const (
	GraphRelationshipOriginInferred      GraphRelationshipOrigin = "inferred"
	GraphRelationshipOriginDeterministic GraphRelationshipOrigin = "deterministic"
)

type GraphRelationshipCandidate struct {
	Scope              GraphScope              `json:"scope"`
	RevisionID         string                  `json:"revision_id"`
	ExternalID         string                  `json:"external_id"`
	SourceEntityID     string                  `json:"source_entity_id"`
	TargetEntityID     string                  `json:"target_entity_id"`
	ExternalKind       string                  `json:"external_kind"`
	Description        string                  `json:"description"`
	Weight             float64                 `json:"weight"`
	Origin             GraphRelationshipOrigin `json:"origin"`
	ProvenanceApproved bool                    `json:"provenance_approved"`
	Evidence           []GraphEvidence         `json:"evidence"`
}

func (c GraphRelationshipCandidate) Validate() error {
	if err := c.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(c.RevisionID) == "" || strings.TrimSpace(c.ExternalID) == "" ||
		strings.TrimSpace(c.SourceEntityID) == "" || strings.TrimSpace(c.TargetEntityID) == "" ||
		c.SourceEntityID == c.TargetEntityID || strings.TrimSpace(c.ExternalKind) == "" || len(c.Evidence) == 0 ||
		c.Weight < 0 || c.Weight > 1 || (c.Origin != GraphRelationshipOriginInferred && c.Origin != GraphRelationshipOriginDeterministic) {
		return fmt.Errorf("%w: invalid relationship candidate", ErrInvalidGraphRecord)
	}
	for _, evidence := range c.Evidence {
		if evidence.Scope != c.Scope || strings.TrimSpace(evidence.CanonicalKind) == "" || strings.TrimSpace(evidence.CanonicalID) == "" || strings.TrimSpace(evidence.CanonicalFingerprint) == "" || evidence.OccurrenceCount < 1 {
			return fmt.Errorf("%w: invalid or cross-scope relationship evidence", ErrInvalidGraphRecord)
		}
	}
	return nil
}
