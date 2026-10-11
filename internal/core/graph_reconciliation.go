package core

import (
	"fmt"
	"strings"
)

// GraphEntityCandidate is a validated, revision-local entity awaiting assignment
// to an Agent Memory stable identity. External and revision IDs are never used
// as the stable public identity.
type GraphEntityCandidate struct {
	Scope                 GraphScope      `json:"scope"`
	RevisionID            string          `json:"revision_id"`
	ExternalID            string          `json:"external_id"`
	PriorStableEntityID   string          `json:"prior_stable_entity_id,omitempty"`
	ApprovedMergeEntityID string          `json:"approved_merge_entity_id,omitempty"`
	Name                  string          `json:"name"`
	EntityType            string          `json:"entity_type"`
	Description           string          `json:"description,omitempty"`
	Aliases               []string        `json:"aliases,omitempty"`
	OccurrenceCount       int             `json:"occurrence_count"`
	Degree                int             `json:"degree"`
	Evidence              []GraphEvidence `json:"evidence"`
}

func (c GraphEntityCandidate) Validate() error {
	if err := c.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(c.RevisionID) == "" || strings.TrimSpace(c.ExternalID) == "" ||
		strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.EntityType) == "" {
		return fmt.Errorf("%w: entity candidate identity, name, and type are required", ErrInvalidGraphRecord)
	}
	if len(c.Evidence) == 0 || c.OccurrenceCount < 0 || c.Degree < 0 {
		return fmt.Errorf("%w: entity candidate requires evidence and non-negative counts", ErrInvalidGraphRecord)
	}
	for _, evidence := range c.Evidence {
		if evidence.Scope != c.Scope || strings.TrimSpace(evidence.CanonicalKind) == "" ||
			strings.TrimSpace(evidence.CanonicalID) == "" || strings.TrimSpace(evidence.CanonicalFingerprint) == "" ||
			evidence.OccurrenceCount < 1 {
			return fmt.Errorf("%w: entity candidate evidence is invalid or cross-scope", ErrInvalidGraphRecord)
		}
	}
	return nil
}

type GraphEntityLineageKind string

const (
	GraphEntityLineageMerge GraphEntityLineageKind = "merge"
	GraphEntityLineageSplit GraphEntityLineageKind = "split"
)

// GraphEntityLineage records identity changes without erasing the earlier
// stable identity. It is intentionally revision-scoped and evidence-neutral.
type GraphEntityLineage struct {
	Scope        GraphScope             `json:"scope"`
	RevisionID   string                 `json:"revision_id"`
	Kind         GraphEntityLineageKind `json:"kind"`
	FromEntityID string                 `json:"from_entity_id"`
	ToEntityID   string                 `json:"to_entity_id"`
	ReasonCode   string                 `json:"reason_code"`
}

func (l GraphEntityLineage) Validate() error {
	if err := l.Scope.Validate(); err != nil {
		return err
	}
	if l.Kind != GraphEntityLineageMerge && l.Kind != GraphEntityLineageSplit {
		return fmt.Errorf("%w: unsupported entity lineage kind %q", ErrInvalidGraphRecord, l.Kind)
	}
	if strings.TrimSpace(l.RevisionID) == "" || strings.TrimSpace(l.FromEntityID) == "" ||
		strings.TrimSpace(l.ToEntityID) == "" || l.FromEntityID == l.ToEntityID || strings.TrimSpace(l.ReasonCode) == "" {
		return fmt.Errorf("%w: invalid entity lineage identity", ErrInvalidGraphRecord)
	}
	return nil
}
