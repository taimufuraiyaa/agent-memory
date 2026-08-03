package readingroom

import (
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"sort"
	"strings"
)

type SynthesisResult struct {
	Contribution   Contribution `json:"contribution"`
	Contradictions []string     `json:"contradictions,omitempty"`
	Unresolved     []string     `json:"unresolved,omitempty"`
}

func Synthesize(id, statement string, inputs []Contribution, profile AgentProfile) (SynthesisResult, error) {
	if profile.Role != RoleSynthesizer || len(inputs) == 0 || strings.TrimSpace(statement) == "" {
		return SynthesisResult{}, errors.New("synthesis requires synthesizer profile, inputs, and statement")
	}
	derived := make([]string, 0, len(inputs))
	citations := []core.Citation{}
	seen := map[string]bool{}
	contradictions := []string{}
	unresolved := []string{}
	confidence := 0.0
	for _, input := range inputs {
		derived = append(derived, input.ID)
		confidence += input.Confidence
		for _, citation := range input.Citations {
			if !seen[citation.ID] {
				citations = append(citations, citation)
				seen[citation.ID] = true
			}
		}
		if input.Kind == ContributionQuestion {
			unresolved = append(unresolved, input.ID)
		}
		for _, verification := range input.Verifications {
			if verification.Verdict == core.VerdictContradicts || verification.Verdict == core.VerdictChallenges {
				contradictions = append(contradictions, input.ID)
			}
		}
	}
	sort.Strings(derived)
	sort.Strings(contradictions)
	sort.Strings(unresolved)
	citationIDs := make([]string, len(citations))
	for i, c := range citations {
		citationIDs[i] = c.ID
	}
	result := SynthesisResult{Contribution: Contribution{ID: id, Role: RoleSynthesizer, ProfileID: profile.ID, ProfileVersion: profile.Version, Kind: ContributionSynthesis, Statement: statement, Provenance: core.KnowledgeProvenance{Attribution: core.Attribution{Kind: core.AttributionAgent, SubjectID: profile.ID}, Form: core.KnowledgeSynthesis, Derivation: core.DerivationConsolidated, CitationIDs: citationIDs, DerivedFrom: derived}, Citations: citations, Confidence: confidence / float64(len(inputs))}, Contradictions: contradictions, Unresolved: unresolved}
	if err := result.Contribution.Validate(profile); err != nil {
		return SynthesisResult{}, err
	}
	return result, nil
}
