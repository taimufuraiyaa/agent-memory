package readingroom

import (
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSynthesisPreservesDerivationDisagreementAndQuestions(t *testing.T) {
	critic, questioner := DefaultProfiles()[RoleCritic], DefaultProfiles()[RoleQuestioner]
	inputs := []Contribution{
		{ID: "critique", Role: critic.Role, ProfileID: critic.ID, ProfileVersion: critic.Version, Kind: ContributionCritique, Statement: "The analogy does not establish scientific truth.", Provenance: core.KnowledgeProvenance{Attribution: core.Attribution{Kind: core.AttributionAgent, SubjectID: critic.ID}, Form: core.KnowledgeInsight, Derivation: core.DerivationInterpreted}, Confidence: .8, Verifications: []core.EvidenceVerification{{ID: "v", SubjectID: "critique", CitationID: "missing", Verdict: core.VerdictChallenges, Method: core.VerificationEntailment, EvidenceFingerprint: "fp", SubjectFingerprint: core.FingerprintText("The analogy does not establish scientific truth."), VerifierID: "v", VerifierVersion: "v1"}}},
		{ID: "question", Role: questioner.Role, ProfileID: questioner.ID, ProfileVersion: questioner.Version, Kind: ContributionQuestion, Statement: "What kind of invariance is intended?", Provenance: core.KnowledgeProvenance{Attribution: core.Attribution{Kind: core.AttributionAgent, SubjectID: questioner.ID}, Form: core.KnowledgeQuestion, Derivation: core.DerivationDiscussed}, Confidence: .7},
	}
	result, err := Synthesize("synthesis", "The proverb and astronomy statement express different kinds of invariance.", inputs, DefaultProfiles()[RoleSynthesizer])
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Contribution.Provenance.DerivedFrom) != 2 || len(result.Contradictions) != 1 || len(result.Unresolved) != 1 {
		t.Fatalf("synthesis collapsed inputs: %+v", result)
	}
}
