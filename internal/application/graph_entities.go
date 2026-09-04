package application

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type GraphExistingEntity struct {
	Entity          core.GraphEntity
	Version         core.GraphEntityVersion
	Evidence        []core.GraphEvidence
	ApprovedAliases []string
}

type GraphEntityReconciliationRequest struct {
	Scope      core.GraphScope
	RevisionID string
	Existing   []GraphExistingEntity
	Candidates []core.GraphEntityCandidate
	Now        time.Time
}

type GraphEntityReconciliationDecision struct {
	ExternalID     string
	StableEntityID string
	ReasonCode     string
	AmbiguousIDs   []string
}

type GraphEntityReconciliationResult struct {
	Entities  []core.GraphEntity
	Versions  []core.GraphEntityVersion
	Evidence  map[string][]core.GraphEvidence
	Decisions []GraphEntityReconciliationDecision
	Lineage   []core.GraphEntityLineage
}

type GraphEntityReconciler struct{}

func NewGraphEntityReconciler() *GraphEntityReconciler { return &GraphEntityReconciler{} }

func (r *GraphEntityReconciler) Reconcile(request GraphEntityReconciliationRequest) (GraphEntityReconciliationResult, error) {
	if err := request.Scope.Validate(); err != nil {
		return GraphEntityReconciliationResult{}, err
	}
	if strings.TrimSpace(request.RevisionID) == "" || request.Now.IsZero() {
		return GraphEntityReconciliationResult{}, fmt.Errorf("graph reconciliation revision and time are required")
	}
	catalog := make(map[string]GraphExistingEntity, len(request.Existing))
	for _, existing := range request.Existing {
		if existing.Entity.Scope != request.Scope || existing.Entity.ID == "" || existing.Version.EntityID != existing.Entity.ID {
			return GraphEntityReconciliationResult{}, fmt.Errorf("invalid or cross-scope existing graph entity")
		}
		catalog[existing.Entity.ID] = existing
	}
	candidates := append([]core.GraphEntityCandidate(nil), request.Candidates...)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ExternalID < candidates[j].ExternalID })
	result := GraphEntityReconciliationResult{Evidence: make(map[string][]core.GraphEvidence)}
	emitted := make(map[string]int)
	for _, candidate := range candidates {
		if err := candidate.Validate(); err != nil {
			return GraphEntityReconciliationResult{}, err
		}
		if candidate.Scope != request.Scope || candidate.RevisionID != request.RevisionID {
			return GraphEntityReconciliationResult{}, fmt.Errorf("graph entity candidate scope or revision mismatch")
		}
		matched, reason, ambiguous := matchGraphEntity(candidate, catalog)
		stableID := matched
		if stableID == "" {
			stableID = deterministicGraphEntityID(candidate)
		}
		existing, exists := catalog[stableID]
		entity := core.GraphEntity{
			ID: stableID, Scope: request.Scope, Trust: core.GraphTrustProposed,
			FirstRevisionID: request.RevisionID, LastRevisionID: request.RevisionID,
			CreatedAt: request.Now.UTC(), UpdatedAt: request.Now.UTC(),
		}
		if exists {
			entity = existing.Entity
			entity.LastRevisionID = request.RevisionID
			entity.UpdatedAt = request.Now.UTC()
		}
		version := core.GraphEntityVersion{
			EntityID: stableID, RevisionID: request.RevisionID, ExternalID: candidate.ExternalID,
			Name: strings.TrimSpace(candidate.Name), EntityType: strings.TrimSpace(candidate.EntityType),
			Description: strings.TrimSpace(candidate.Description), Aliases: normalizedGraphAliases(candidate.Aliases),
			OccurrenceCount: max(1, candidate.OccurrenceCount), Degree: candidate.Degree,
		}
		if index, duplicate := emitted[stableID]; duplicate {
			result.Evidence[stableID] = mergeGraphEvidence(result.Evidence[stableID], candidate.Evidence)
			result.Versions[index].OccurrenceCount += version.OccurrenceCount
		} else {
			emitted[stableID] = len(result.Versions)
			result.Entities = append(result.Entities, entity)
			result.Versions = append(result.Versions, version)
			result.Evidence[stableID] = mergeGraphEvidence(nil, candidate.Evidence)
		}
		result.Decisions = append(result.Decisions, GraphEntityReconciliationDecision{
			ExternalID: candidate.ExternalID, StableEntityID: stableID, ReasonCode: reason, AmbiguousIDs: ambiguous,
		})
		if candidate.ApprovedMergeEntityID != "" && stableID == candidate.ApprovedMergeEntityID {
			fromID := deterministicGraphEntityID(candidate)
			if fromID != stableID {
				result.Lineage = append(result.Lineage, core.GraphEntityLineage{
					Scope: request.Scope, RevisionID: request.RevisionID, Kind: core.GraphEntityLineageMerge,
					FromEntityID: fromID, ToEntityID: stableID, ReasonCode: "approved_merge",
				})
			}
		}
		catalog[stableID] = GraphExistingEntity{Entity: entity, Version: version, Evidence: result.Evidence[stableID], ApprovedAliases: existing.ApprovedAliases}
	}
	return result, nil
}

func matchGraphEntity(candidate core.GraphEntityCandidate, catalog map[string]GraphExistingEntity) (string, string, []string) {
	if candidate.PriorStableEntityID != "" {
		if existing, ok := catalog[candidate.PriorStableEntityID]; ok && graphEntityTypeCompatible(candidate.EntityType, existing.Version.EntityType) {
			return existing.Entity.ID, "prior_stable_identity", nil
		}
	}
	if candidate.ApprovedMergeEntityID != "" {
		if existing, ok := catalog[candidate.ApprovedMergeEntityID]; ok && graphEntityTypeCompatible(candidate.EntityType, existing.Version.EntityType) && graphEvidenceCompatible(candidate.Evidence, existing.Evidence) {
			return existing.Entity.ID, "approved_merge", nil
		}
	}
	name := normalizeGraphIdentity(candidate.Name)
	var aliases, compatible, sameName []string
	for id, existing := range catalog {
		if !graphEntityTypeCompatible(candidate.EntityType, existing.Version.EntityType) {
			if normalizeGraphIdentity(existing.Version.Name) == name {
				sameName = append(sameName, id)
			}
			continue
		}
		if containsNormalizedGraphAlias(existing.ApprovedAliases, name) {
			aliases = append(aliases, id)
		}
		if normalizeGraphIdentity(existing.Version.Name) == name {
			if graphEvidenceCompatible(candidate.Evidence, existing.Evidence) {
				compatible = append(compatible, id)
			} else {
				sameName = append(sameName, id)
			}
		}
	}
	for _, ids := range [][]string{aliases, compatible, sameName} {
		sort.Strings(ids)
	}
	if len(aliases) == 1 {
		return aliases[0], "approved_alias", nil
	}
	if len(compatible) == 1 {
		return compatible[0], "compatible_evidence", nil
	}
	if len(aliases)+len(compatible) > 1 {
		ambiguous := append(append([]string(nil), aliases...), compatible...)
		sort.Strings(ambiguous)
		return "", "ambiguous_candidates", ambiguous
	}
	if len(sameName) > 0 {
		return "", "same_name_conflict", sameName
	}
	return "", "new_identity", nil
}

func graphEntityTypeCompatible(a, b string) bool {
	return normalizeGraphIdentity(a) == normalizeGraphIdentity(b)
}

func graphEvidenceCompatible(a, b []core.GraphEvidence) bool {
	for _, left := range a {
		for _, right := range b {
			if left.CanonicalKind == right.CanonicalKind && left.CanonicalID == right.CanonicalID {
				return true
			}
			if strings.TrimSpace(left.Locator) != "" && left.Locator == right.Locator {
				return true
			}
		}
	}
	return false
}

func deterministicGraphEntityID(candidate core.GraphEntityCandidate) string {
	evidence := append([]core.GraphEvidence(nil), candidate.Evidence...)
	sort.Slice(evidence, func(i, j int) bool {
		return evidence[i].CanonicalKind+"\x00"+evidence[i].CanonicalID+"\x00"+evidence[i].CanonicalFingerprint <
			evidence[j].CanonicalKind+"\x00"+evidence[j].CanonicalID+"\x00"+evidence[j].CanonicalFingerprint
	})
	hash := sha256.New()
	for _, value := range []string{candidate.Scope.TenantID, candidate.Scope.WorkspaceID, normalizeGraphIdentity(candidate.Name), normalizeGraphIdentity(candidate.EntityType)} {
		hash.Write([]byte(value))
		hash.Write([]byte{0})
	}
	for _, item := range evidence {
		hash.Write([]byte(item.CanonicalKind + "\x00" + item.CanonicalID + "\x00" + item.CanonicalFingerprint + "\x00" + item.Locator))
		hash.Write([]byte{0})
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, hash.Sum(nil)).String()
}

func normalizeGraphIdentity(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, strings.TrimSpace(value))
}

func normalizedGraphAliases(values []string) []string {
	seen := map[string]string{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if normalized := normalizeGraphIdentity(trimmed); normalized != "" {
			seen[normalized] = trimmed
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, seen[key])
	}
	return result
}

func containsNormalizedGraphAlias(values []string, normalized string) bool {
	for _, value := range values {
		if normalizeGraphIdentity(value) == normalized {
			return true
		}
	}
	return false
}

func mergeGraphEvidence(existing, additional []core.GraphEvidence) []core.GraphEvidence {
	byKey := make(map[string]core.GraphEvidence, len(existing)+len(additional))
	for _, evidence := range append(append([]core.GraphEvidence(nil), existing...), additional...) {
		key := evidence.CanonicalKind + "\x00" + evidence.CanonicalID + "\x00" + evidence.CanonicalFingerprint + "\x00" + evidence.Locator
		if prior, ok := byKey[key]; ok {
			prior.OccurrenceCount += evidence.OccurrenceCount
			byKey[key] = prior
		} else {
			byKey[key] = evidence
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]core.GraphEvidence, 0, len(keys))
	for _, key := range keys {
		result = append(result, byKey[key])
	}
	return result
}
