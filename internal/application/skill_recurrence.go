package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const (
	defaultRecurrenceEvidenceLimit  = 200
	defaultRecurrenceClusterLimit   = 20
	defaultRecurrenceCandidateLimit = 20
)

type SkillRecurrenceEvidence struct {
	ID, Workspace, ToolLessonID, ToolName, Capability string
	EpisodeIDs                                        []string
	Validated, Authorized, Suppressed, TaskVerified   bool
	Confidence                                        float64
	OccurredAt                                        time.Time
}

type SkillRecurrencePolicy struct {
	MinimumDistinctEpisodes int
	MinimumConfidence       float64
	MatchThreshold          float64
	MaximumEvidence         int
	MaximumPerCluster       int
	MaximumCandidates       int
}

type SkillRecurrenceInput struct {
	Workspace, PrincipalID, CreatedBy string
	Evidence                          []SkillRecurrenceEvidence
}

type SkillCandidateResult struct {
	core.SkillCandidate
	Deduplicated bool `json:"deduplicated"`
}

type SkillRecurrenceResult struct {
	Candidates       []SkillCandidateResult `json:"candidates"`
	ScannedEvidence  int                    `json:"scanned_evidence"`
	EligibleEvidence int                    `json:"eligible_evidence"`
	Truncated        bool                   `json:"truncated"`
}

type SkillRecurrenceRepository interface {
	ListLogicalSkills(context.Context, string, int) ([]core.LogicalSkill, error)
	PutSkillCandidate(context.Context, core.SkillCandidate) (core.SkillCandidate, bool, error)
}

type SkillRecurrenceDetector struct {
	repository SkillRecurrenceRepository
	policy     SkillRecurrencePolicy
	now        func() time.Time
}

func NewSkillRecurrenceDetector(repository SkillRecurrenceRepository, policy SkillRecurrencePolicy) *SkillRecurrenceDetector {
	if policy.MinimumDistinctEpisodes <= 0 {
		policy.MinimumDistinctEpisodes = 2
	}
	if policy.MinimumConfidence <= 0 {
		policy.MinimumConfidence = .7
	}
	if policy.MatchThreshold <= 0 {
		policy.MatchThreshold = .35
	}
	if policy.MaximumEvidence <= 0 || policy.MaximumEvidence > defaultRecurrenceEvidenceLimit {
		policy.MaximumEvidence = defaultRecurrenceEvidenceLimit
	}
	if policy.MaximumPerCluster <= 0 || policy.MaximumPerCluster > defaultRecurrenceClusterLimit {
		policy.MaximumPerCluster = defaultRecurrenceClusterLimit
	}
	if policy.MaximumCandidates <= 0 || policy.MaximumCandidates > defaultRecurrenceCandidateLimit {
		policy.MaximumCandidates = defaultRecurrenceCandidateLimit
	}
	return &SkillRecurrenceDetector{repository: repository, policy: policy, now: time.Now}
}

func (d *SkillRecurrenceDetector) Detect(ctx context.Context, input SkillRecurrenceInput) (SkillRecurrenceResult, error) {
	if d == nil || d.repository == nil {
		return SkillRecurrenceResult{}, errors.New("skill recurrence repository is required")
	}
	input.Workspace, input.PrincipalID, input.CreatedBy = strings.TrimSpace(input.Workspace), strings.TrimSpace(input.PrincipalID), strings.TrimSpace(input.CreatedBy)
	if input.Workspace == "" || input.PrincipalID == "" || input.CreatedBy == "" {
		return SkillRecurrenceResult{}, errors.New("workspace, principal_id, and created_by are required")
	}
	evidence := append([]SkillRecurrenceEvidence(nil), input.Evidence...)
	sort.SliceStable(evidence, func(i, j int) bool {
		if evidence[i].OccurredAt.Equal(evidence[j].OccurredAt) {
			return evidence[i].ID < evidence[j].ID
		}
		return evidence[i].OccurredAt.Before(evidence[j].OccurredAt)
	})
	result := SkillRecurrenceResult{ScannedEvidence: len(evidence)}
	if len(evidence) > d.policy.MaximumEvidence {
		evidence, result.Truncated = evidence[:d.policy.MaximumEvidence], true
		result.ScannedEvidence = len(evidence)
	}
	clusters := map[string][]SkillRecurrenceEvidence{}
	for _, item := range evidence {
		if item.Workspace != input.Workspace || !item.Validated || !item.Authorized || item.Suppressed || !item.TaskVerified || item.Confidence < d.policy.MinimumConfidence || strings.TrimSpace(item.ToolLessonID) == "" {
			continue
		}
		key := normalizeRecurrenceText(item.ToolName) + "\x00" + normalizeRecurrenceText(item.Capability)
		if key == "\x00" || len(clusters[key]) >= d.policy.MaximumPerCluster {
			continue
		}
		clusters[key] = append(clusters[key], item)
		result.EligibleEvidence++
	}
	skills, err := d.repository.ListLogicalSkills(ctx, input.Workspace, 200)
	if err != nil {
		return SkillRecurrenceResult{}, err
	}
	type clusterMatch struct {
		key      string
		evidence []SkillRecurrenceEvidence
		targets  []string
	}
	matches := make([]clusterMatch, 0, len(clusters))
	for key, items := range clusters {
		if distinctRecurrenceEpisodes(items) < d.policy.MinimumDistinctEpisodes {
			continue
		}
		targets := matchRecurrenceSkills(items[0], skills, d.policy.MatchThreshold)
		matches = append(matches, clusterMatch{key: key, evidence: items, targets: targets})
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].key < matches[j].key })

	processed := map[int]bool{}
	for i := range matches {
		if processed[i] || len(result.Candidates) >= d.policy.MaximumCandidates {
			continue
		}
		match := matches[i]
		if len(match.targets) == 1 {
			combined := append([]SkillRecurrenceEvidence(nil), match.evidence...)
			indices := []int{i}
			for j := i + 1; j < len(matches); j++ {
				if len(matches[j].targets) == 1 && matches[j].targets[0] == match.targets[0] {
					combined = append(combined, matches[j].evidence...)
					indices = append(indices, j)
				}
			}
			if len(indices) > 1 {
				candidate, err := d.persistCandidate(ctx, input, core.SkillCandidateSplit, match.targets, combined)
				if err != nil {
					return SkillRecurrenceResult{}, err
				}
				result.Candidates = append(result.Candidates, candidate)
				for _, index := range indices {
					processed[index] = true
				}
				continue
			}
		}
		kind := core.SkillCandidateCreate
		if len(match.targets) == 1 {
			kind = core.SkillCandidateRevise
		} else if len(match.targets) > 1 {
			kind = core.SkillCandidateMerge
		}
		candidate, err := d.persistCandidate(ctx, input, kind, match.targets, match.evidence)
		if err != nil {
			return SkillRecurrenceResult{}, err
		}
		result.Candidates = append(result.Candidates, candidate)
		processed[i] = true
	}
	return result, nil
}

func (d *SkillRecurrenceDetector) persistCandidate(ctx context.Context, input SkillRecurrenceInput, kind core.SkillCandidateKind, targets []string, evidence []SkillRecurrenceEvidence) (SkillCandidateResult, error) {
	episodes, lessons := map[string]struct{}{}, map[string]struct{}{}
	confidence := 0.0
	for _, item := range evidence {
		lessons[item.ToolLessonID] = struct{}{}
		for _, id := range item.EpisodeIDs {
			if strings.TrimSpace(id) != "" {
				episodes[id] = struct{}{}
			}
		}
		confidence += item.Confidence
	}
	targets = sortedRecurrenceSet(targets)
	episodeIDs, lessonIDs := sortedRecurrenceMap(episodes), sortedRecurrenceMap(lessons)
	identity := strings.Join([]string{input.Workspace, string(kind), strings.Join(targets, ","), strings.Join(episodeIDs, ","), strings.Join(lessonIDs, ",")}, "\x00")
	sum := sha256.Sum256([]byte(identity))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	now := d.now().UTC()
	candidate := core.SkillCandidate{ID: "candidate-" + hex.EncodeToString(sum[:12]), Workspace: input.Workspace, Kind: kind, TargetSkillIDs: targets,
		Summary: "Recurring validated tool workflow: " + strings.TrimSpace(evidence[0].Capability), ExpectedBenefit: "Reduce repeated verified work while preserving review before activation.",
		RiskTier: core.SkillRiskLow, Confidence: confidence / float64(len(evidence)), State: core.SkillCandidateProposed,
		SourceEpisodeIDs: episodeIDs, SourceToolLessonIDs: lessonIDs, DeduplicationHash: digest, CreatedBy: input.CreatedBy, CreatedAt: now, UpdatedAt: now}
	stored, deduplicated, err := d.repository.PutSkillCandidate(ctx, candidate)
	return SkillCandidateResult{SkillCandidate: stored, Deduplicated: deduplicated}, err
}

func matchRecurrenceSkills(evidence SkillRecurrenceEvidence, skills []core.LogicalSkill, threshold float64) []string {
	query := recurrenceTokens(evidence.ToolName + " " + evidence.Capability)
	targets := make([]string, 0)
	for _, skill := range skills {
		if skill.Status != core.SkillStatusActive {
			continue
		}
		text := skill.Name + " " + skill.Description + " " + strings.Join(skill.Capabilities, " ") + " " + strings.Join(skill.TriggerConditions, " ")
		if recurrenceOverlap(query, recurrenceTokens(text)) >= threshold {
			targets = append(targets, skill.ID)
		}
	}
	return sortedRecurrenceSet(targets)
}

func distinctRecurrenceEpisodes(items []SkillRecurrenceEvidence) int {
	values := map[string]struct{}{}
	for _, item := range items {
		for _, id := range item.EpisodeIDs {
			if id = strings.TrimSpace(id); id != "" {
				values[id] = struct{}{}
			}
		}
	}
	return len(values)
}
func normalizeRecurrenceText(value string) string {
	return strings.Join(sortedRecurrenceMap(recurrenceTokens(value)), " ")
}
func recurrenceTokens(value string) map[string]struct{} {
	values := map[string]struct{}{}
	for _, field := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		if len(field) > 1 {
			values[field] = struct{}{}
		}
	}
	return values
}
func recurrenceOverlap(left, right map[string]struct{}) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	common := 0
	for token := range left {
		if _, ok := right[token]; ok {
			common++
		}
	}
	denominator := len(left)
	if len(right) < denominator {
		denominator = len(right)
	}
	return float64(common) / float64(denominator)
}
func sortedRecurrenceMap(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
func sortedRecurrenceSet(values []string) []string {
	set := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	return sortedRecurrenceMap(set)
}
