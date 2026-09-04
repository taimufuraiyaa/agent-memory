package retrieval

import (
	"fmt"
	"sort"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type GraphLocalSeed struct {
	CanonicalKind string  `json:"canonical_kind"`
	CanonicalID   string  `json:"canonical_id"`
	Score         float64 `json:"score"`
}

type GraphLocalNode struct {
	ID       string
	Trust    core.GraphTrustState
	Evidence []core.GraphEvidence
}

type GraphLocalEdge struct {
	ID       string
	SourceID string
	TargetID string
	Kind     core.GraphRelationshipKind
	Trust    core.GraphTrustState
	Weight   float64
	Evidence []core.GraphEvidence
}

type GraphLocalLimits struct {
	MaxSeeds    int
	MaxDepth    int
	MaxFanout   int
	MaxPaths    int
	MaxEvidence int
}

func DefaultGraphLocalLimits() GraphLocalLimits {
	return GraphLocalLimits{MaxSeeds: 8, MaxDepth: 2, MaxFanout: 8, MaxPaths: 32, MaxEvidence: 128}
}

type GraphLocalHop struct {
	EdgeID     string                     `json:"edge_id"`
	FromID     string                     `json:"from_entity_id"`
	ToID       string                     `json:"to_entity_id"`
	Kind       core.GraphRelationshipKind `json:"kind"`
	Trust      core.GraphTrustState       `json:"trust"`
	Direction  string                     `json:"direction"`
	ReasonCode string                     `json:"reason_code"`
	Influence  float64                    `json:"influence"`
	Evidence   []core.GraphEvidence       `json:"evidence"`
}

type GraphLocalPath struct {
	Seed       GraphLocalSeed       `json:"seed"`
	EntityIDs  []string             `json:"entity_ids"`
	Hops       []GraphLocalHop      `json:"hops"`
	Evidence   []core.GraphEvidence `json:"evidence"`
	PathScore  float64              `json:"path_score"`
	CanSupport bool                 `json:"can_support"`
}

type GraphLocalConflict struct {
	Seed GraphLocalSeed `json:"seed"`
	Hop  GraphLocalHop  `json:"hop"`
}

type GraphLocalResult struct {
	Paths     []GraphLocalPath     `json:"paths"`
	Conflicts []GraphLocalConflict `json:"conflicts"`
}

type GraphLocalRequest struct {
	Scope              core.GraphScope
	Seeds              []GraphLocalSeed
	Nodes              []GraphLocalNode
	Edges              []GraphLocalEdge
	AuthorizedEvidence map[string]struct{}
	Limits             GraphLocalLimits
}

func ExpandLocalGraph(request GraphLocalRequest) (GraphLocalResult, error) {
	if err := request.Scope.Validate(); err != nil {
		return GraphLocalResult{}, err
	}
	if err := validateGraphLocalLimits(request.Limits); err != nil {
		return GraphLocalResult{}, err
	}
	nodes := make(map[string]GraphLocalNode, len(request.Nodes))
	seedEntities := map[string][]string{}
	for _, node := range request.Nodes {
		if strings.TrimSpace(node.ID) == "" || !queryableGraphTrust(node.Trust) || !authorizedGraphEvidence(request.Scope, node.Evidence, request.AuthorizedEvidence) {
			continue
		}
		nodes[node.ID] = node
		for _, evidence := range node.Evidence {
			key := evidence.CanonicalKind + "\x00" + evidence.CanonicalID
			seedEntities[key] = append(seedEntities[key], node.ID)
		}
	}
	adjacency := map[string][]GraphLocalEdge{}
	for _, edge := range request.Edges {
		if strings.TrimSpace(edge.ID) == "" || !queryableGraphTrust(edge.Trust) || edge.Weight < 0 || edge.Weight > 1 || !authorizedGraphEvidence(request.Scope, edge.Evidence, request.AuthorizedEvidence) {
			continue
		}
		if _, ok := nodes[edge.SourceID]; !ok {
			continue
		}
		if _, ok := nodes[edge.TargetID]; !ok {
			continue
		}
		adjacency[edge.SourceID] = append(adjacency[edge.SourceID], edge)
		adjacency[edge.TargetID] = append(adjacency[edge.TargetID], edge)
	}
	for id := range adjacency {
		sort.Slice(adjacency[id], func(i, j int) bool {
			if adjacency[id][i].Weight != adjacency[id][j].Weight {
				return adjacency[id][i].Weight > adjacency[id][j].Weight
			}
			return adjacency[id][i].ID < adjacency[id][j].ID
		})
	}
	seeds := normalizedGraphSeeds(request.Seeds, request.Limits.MaxSeeds)
	result := GraphLocalResult{}
	perSeed := max(1, request.Limits.MaxPaths/max(1, len(seeds)))
	for _, seed := range seeds {
		roots := append([]string(nil), seedEntities[seed.CanonicalKind+"\x00"+seed.CanonicalID]...)
		sort.Strings(roots)
		seedPaths := 0
		for _, root := range roots {
			queue := []localTraversalState{{entityID: root, entityIDs: []string{root}, evidence: append([]core.GraphEvidence(nil), nodes[root].Evidence...), score: seed.Score, canSupport: true}}
			visited := map[string]int{root: 0}
			for len(queue) > 0 && seedPaths < perSeed && len(result.Paths) < request.Limits.MaxPaths {
				current := queue[0]
				queue = queue[1:]
				if len(current.hops) >= request.Limits.MaxDepth {
					continue
				}
				fanout := 0
				for _, edge := range adjacency[current.entityID] {
					if fanout >= request.Limits.MaxFanout || seedPaths >= perSeed || len(result.Paths) >= request.Limits.MaxPaths {
						break
					}
					to, direction := edge.TargetID, "outgoing"
					if current.entityID == edge.TargetID {
						to, direction = edge.SourceID, "incoming"
					}
					if priorDepth, seen := visited[to]; seen && priorDepth <= len(current.hops)+1 {
						continue
					}
					visited[to] = len(current.hops) + 1
					fanout++
					hop := graphLocalHop(edge, current.entityID, to, direction)
					if edge.Kind == core.GraphRelationshipContradicts || edge.Kind == core.GraphRelationshipChallenges {
						result.Conflicts = append(result.Conflicts, GraphLocalConflict{Seed: seed, Hop: hop})
						continue
					}
					pathScore := current.score * hop.Influence
					evidence := mergeAuthorizedGraphEvidence(current.evidence, nodes[to].Evidence, request.Limits.MaxEvidence)
					hops := append(append([]GraphLocalHop(nil), current.hops...), hop)
					entityIDs := append(append([]string(nil), current.entityIDs...), to)
					canSupport := current.canSupport && edge.Kind == core.GraphRelationshipSupports && edge.Trust != core.GraphTrustProposed
					path := GraphLocalPath{Seed: seed, EntityIDs: entityIDs, Hops: hops, Evidence: evidence, PathScore: pathScore, CanSupport: canSupport}
					result.Paths = append(result.Paths, path)
					seedPaths++
					queue = append(queue, localTraversalState{entityID: to, entityIDs: entityIDs, hops: hops, evidence: evidence, score: pathScore, canSupport: canSupport})
				}
			}
		}
	}
	sort.Slice(result.Paths, func(i, j int) bool {
		if result.Paths[i].PathScore != result.Paths[j].PathScore {
			return result.Paths[i].PathScore > result.Paths[j].PathScore
		}
		return graphPathIdentity(result.Paths[i]) < graphPathIdentity(result.Paths[j])
	})
	return result, nil
}

type localTraversalState struct {
	entityID   string
	entityIDs  []string
	hops       []GraphLocalHop
	evidence   []core.GraphEvidence
	score      float64
	canSupport bool
}

func validateGraphLocalLimits(limits GraphLocalLimits) error {
	if limits.MaxSeeds < 1 || limits.MaxSeeds > 32 || limits.MaxDepth < 1 || limits.MaxDepth > 4 || limits.MaxFanout < 1 || limits.MaxFanout > 64 || limits.MaxPaths < 1 || limits.MaxPaths > 256 || limits.MaxEvidence < 1 || limits.MaxEvidence > 1024 {
		return fmt.Errorf("local graph limits are outside policy")
	}
	return nil
}

func normalizedGraphSeeds(values []GraphLocalSeed, limit int) []GraphLocalSeed {
	byKey := map[string]GraphLocalSeed{}
	for _, value := range values {
		value.CanonicalKind, value.CanonicalID = strings.TrimSpace(value.CanonicalKind), strings.TrimSpace(value.CanonicalID)
		if value.CanonicalKind == "" || value.CanonicalID == "" || value.Score < 0 || value.Score > 1 {
			continue
		}
		key := value.CanonicalKind + "\x00" + value.CanonicalID
		if prior, ok := byKey[key]; !ok || value.Score > prior.Score {
			byKey[key] = value
		}
	}
	result := make([]GraphLocalSeed, 0, len(byKey))
	for _, value := range byKey {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		return result[i].CanonicalKind+"\x00"+result[i].CanonicalID < result[j].CanonicalKind+"\x00"+result[j].CanonicalID
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func queryableGraphTrust(trust core.GraphTrustState) bool {
	return trust == core.GraphTrustApproved || trust == core.GraphTrustReviewed || trust == core.GraphTrustProposed
}

func authorizedGraphEvidence(scope core.GraphScope, evidence []core.GraphEvidence, authorized map[string]struct{}) bool {
	if len(evidence) == 0 {
		return false
	}
	for _, item := range evidence {
		if item.Scope != scope {
			return false
		}
		if _, ok := authorized[GraphAuthorizationKey(item)]; !ok {
			return false
		}
	}
	return true
}

func GraphAuthorizationKey(evidence core.GraphEvidence) string {
	return evidence.Scope.TenantID + "\x00" + evidence.Scope.WorkspaceID + "\x00" + evidence.CanonicalKind + "\x00" + evidence.CanonicalID + "\x00" + evidence.CanonicalFingerprint
}

func graphLocalHop(edge GraphLocalEdge, from, to, direction string) GraphLocalHop {
	influence, reason := graphRelationshipInfluence(edge.Kind)
	influence *= edge.Weight
	if edge.Trust == core.GraphTrustReviewed {
		influence *= 0.85
	} else if edge.Trust == core.GraphTrustProposed {
		influence *= 0.35
	}
	return GraphLocalHop{EdgeID: edge.ID, FromID: from, ToID: to, Kind: edge.Kind, Trust: edge.Trust, Direction: direction, ReasonCode: reason, Influence: influence, Evidence: append([]core.GraphEvidence(nil), edge.Evidence...)}
}

func graphRelationshipInfluence(kind core.GraphRelationshipKind) (float64, string) {
	switch kind {
	case core.GraphRelationshipSupports:
		return 1, "typed_support"
	case core.GraphRelationshipMembership:
		return 0.8, "typed_membership"
	case core.GraphRelationshipCausal:
		return 0.75, "typed_causal"
	case core.GraphRelationshipTemporal:
		return 0.6, "typed_temporal"
	case core.GraphRelationshipSimilarity:
		return 0.35, "candidate_similarity"
	case core.GraphRelationshipContradicts:
		return 0, "conflict_contradiction"
	case core.GraphRelationshipChallenges:
		return 0, "conflict_challenge"
	case core.GraphRelationshipSupersedes:
		return 0.7, "typed_supersession"
	default:
		return 0.3, "external_relationship"
	}
}

func mergeAuthorizedGraphEvidence(existing, additional []core.GraphEvidence, limit int) []core.GraphEvidence {
	byKey := make(map[string]core.GraphEvidence, len(existing)+len(additional))
	for _, evidence := range append(append([]core.GraphEvidence(nil), existing...), additional...) {
		key := GraphAuthorizationKey(evidence)
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
	if len(keys) > limit {
		keys = keys[:limit]
	}
	result := make([]core.GraphEvidence, 0, len(keys))
	for _, key := range keys {
		result = append(result, byKey[key])
	}
	return result
}

func graphPathIdentity(path GraphLocalPath) string { return strings.Join(path.EntityIDs, "\x00") }
