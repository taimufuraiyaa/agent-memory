package application

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type GraphCommunityImportRequest struct {
	Scope             core.GraphScope
	ConfigurationID   string
	RevisionID        string
	EntityIDs         map[string]struct{}
	EdgeIDs           map[string]struct{}
	Candidates        []core.GraphCommunityCandidate
	PreviousReports   map[string]core.GraphReport
	ModelRoute        string
	ModelFingerprint  string
	PromptFingerprint string
	ReviewVersion     int64
	Now               time.Time
}

type GraphCommunityImportResult struct {
	Communities []core.GraphCommunity
	Members     map[string][]contracts.GraphCommunityMember
	Reports     []core.GraphReport
}

type GraphCommunityImporter struct{}

func NewGraphCommunityImporter() *GraphCommunityImporter { return &GraphCommunityImporter{} }

func (i *GraphCommunityImporter) Import(request GraphCommunityImportRequest) (GraphCommunityImportResult, error) {
	if err := request.Scope.Validate(); err != nil {
		return GraphCommunityImportResult{}, err
	}
	for _, required := range []string{request.ConfigurationID, request.RevisionID, request.ModelRoute, request.ModelFingerprint, request.PromptFingerprint} {
		if strings.TrimSpace(required) == "" {
			return GraphCommunityImportResult{}, fmt.Errorf("graph community import identity is required")
		}
	}
	if request.Now.IsZero() || request.ReviewVersion < 0 {
		return GraphCommunityImportResult{}, fmt.Errorf("graph community import time or review version is invalid")
	}
	byExternal := make(map[string]core.GraphCommunityCandidate, len(request.Candidates))
	for _, candidate := range request.Candidates {
		if err := validateGraphCommunityCandidate(candidate, request); err != nil {
			return GraphCommunityImportResult{}, err
		}
		if _, duplicate := byExternal[candidate.ExternalID]; duplicate {
			return GraphCommunityImportResult{}, fmt.Errorf("duplicate graph community external identity")
		}
		byExternal[candidate.ExternalID] = candidate
	}
	levels := map[string]int{}
	visiting := map[string]bool{}
	var level func(string) (int, error)
	level = func(id string) (int, error) {
		if value, ok := levels[id]; ok {
			return value, nil
		}
		if visiting[id] {
			return 0, fmt.Errorf("graph community hierarchy is cyclic")
		}
		candidate, ok := byExternal[id]
		if !ok {
			return 0, fmt.Errorf("graph community parent is unresolved")
		}
		visiting[id] = true
		value := 0
		if candidate.ParentExternalID != "" {
			parentLevel, err := level(candidate.ParentExternalID)
			if err != nil {
				return 0, err
			}
			value = parentLevel + 1
		}
		visiting[id] = false
		levels[id] = value
		return value, nil
	}
	ids := make([]string, 0, len(byExternal))
	for id := range byExternal {
		if _, err := level(id); err != nil {
			return GraphCommunityImportResult{}, err
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool {
		if levels[ids[a]] != levels[ids[b]] {
			return levels[ids[a]] < levels[ids[b]]
		}
		return ids[a] < ids[b]
	})
	stableByExternal := map[string]string{}
	result := GraphCommunityImportResult{Members: make(map[string][]contracts.GraphCommunityMember)}
	for _, externalID := range ids {
		candidate := byExternal[externalID]
		membershipFingerprint := graphCommunityFingerprint("members", append(append([]string(nil), candidate.EntityIDs...), candidate.EdgeIDs...))
		evidenceFingerprint := graphCommunityFingerprint("evidence", append(append([]string(nil), candidate.EvidenceFingerprints...), fmt.Sprintf("sources:%d", candidate.SourceCount), fmt.Sprintf("unresolved:%d", candidate.UnresolvedCount)))
		stableID := candidate.PriorCommunityID
		if stableID == "" {
			stableID = deterministicGraphCommunityID(request.Scope, levels[externalID], membershipFingerprint)
		}
		parentID := ""
		if candidate.ParentExternalID != "" {
			parentID = stableByExternal[candidate.ParentExternalID]
		}
		community := core.GraphCommunity{
			ID: stableID, Scope: request.Scope, ConfigurationID: request.ConfigurationID, RevisionID: request.RevisionID,
			ExternalID: externalID, ParentID: parentID, Level: levels[externalID], EntityCount: len(candidate.EntityIDs), EdgeCount: len(candidate.EdgeIDs),
			SourceCount: candidate.SourceCount, UnresolvedCount: candidate.UnresolvedCount,
			MembershipFingerprint: membershipFingerprint, EvidenceFingerprint: evidenceFingerprint,
		}
		members := make([]contracts.GraphCommunityMember, 0, len(candidate.EntityIDs)+len(candidate.EdgeIDs))
		for _, entityID := range sortedUniqueGraphIDs(candidate.EntityIDs) {
			members = append(members, contracts.GraphCommunityMember{Kind: "entity", TargetID: entityID})
		}
		for _, edgeID := range sortedUniqueGraphIDs(candidate.EdgeIDs) {
			members = append(members, contracts.GraphCommunityMember{Kind: "edge", TargetID: edgeID})
		}
		freshness := core.GraphReportFreshness{
			MembershipFingerprint: membershipFingerprint, EvidenceFingerprint: evidenceFingerprint,
			ModelFingerprint: request.ModelFingerprint, PromptFingerprint: request.PromptFingerprint, ReviewVersion: request.ReviewVersion,
		}
		stale := false
		if prior, ok := request.PreviousReports[stableID]; ok {
			stale = prior.Freshness().StaleAgainst(freshness)
		}
		report := core.GraphReport{
			ID: deterministicGraphReportID(stableID, request.RevisionID), Scope: request.Scope, CommunityID: stableID, RevisionID: request.RevisionID,
			Title: strings.TrimSpace(candidate.Report.Title), Summary: strings.TrimSpace(candidate.Report.Summary), Findings: append([]string(nil), candidate.Report.Findings...),
			Rank: candidate.Report.Rank, Trust: core.GraphTrustProposed, AdmissionState: candidate.Report.AdmissionState, Stale: stale,
			EvidenceCount: candidate.SourceCount, UnresolvedCount: candidate.UnresolvedCount,
			ModelRoute: request.ModelRoute, ModelFingerprint: request.ModelFingerprint, PromptFingerprint: request.PromptFingerprint,
			MembershipFingerprint: membershipFingerprint, EvidenceFingerprint: evidenceFingerprint, ReviewVersion: request.ReviewVersion,
		}
		result.Communities = append(result.Communities, community)
		result.Members[stableID] = members
		result.Reports = append(result.Reports, report)
		stableByExternal[externalID] = stableID
	}
	return result, nil
}

func validateGraphCommunityCandidate(candidate core.GraphCommunityCandidate, request GraphCommunityImportRequest) error {
	if strings.TrimSpace(candidate.ExternalID) == "" || len(candidate.EntityIDs) == 0 || candidate.SourceCount < 0 || candidate.UnresolvedCount < 0 {
		return fmt.Errorf("invalid graph community candidate")
	}
	for _, entityID := range candidate.EntityIDs {
		if _, ok := request.EntityIDs[entityID]; !ok {
			return fmt.Errorf("graph community entity is unresolved")
		}
	}
	for _, edgeID := range candidate.EdgeIDs {
		if _, ok := request.EdgeIDs[edgeID]; !ok {
			return fmt.Errorf("graph community edge is unresolved")
		}
	}
	report := candidate.Report
	if strings.TrimSpace(report.ExternalID) == "" || strings.TrimSpace(report.Title) == "" || strings.TrimSpace(report.Summary) == "" || report.Rank < 0 || report.Rank > 1 ||
		(report.AdmissionState != core.GraphReportAdmitted && report.AdmissionState != core.GraphReportQuarantined && report.AdmissionState != core.GraphReportRejected) {
		return fmt.Errorf("invalid graph community report")
	}
	return nil
}

func graphCommunityFingerprint(domain string, values []string) string {
	values = sortedUniqueGraphIDs(values)
	hash := sha256.New()
	hash.Write([]byte(domain))
	hash.Write([]byte{0})
	for _, value := range values {
		hash.Write([]byte(value))
		hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func sortedUniqueGraphIDs(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			seen[trimmed] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func deterministicGraphCommunityID(scope core.GraphScope, level int, membershipFingerprint string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("community\x00"+graphStableDigest(scope.TenantID, scope.WorkspaceID, fmt.Sprintf("%d", level), membershipFingerprint))).String()
}

func deterministicGraphReportID(communityID, revisionID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("report\x00"+graphStableDigest(communityID, revisionID))).String()
}

func graphStableDigest(values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		hash.Write([]byte(value))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)[:16])
}
