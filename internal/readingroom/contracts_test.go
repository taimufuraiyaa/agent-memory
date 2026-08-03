package readingroom

import (
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestDefaultProfilesCoverStudyRoles(t *testing.T) {
	profiles := DefaultProfiles()
	want := []Role{RoleLibrarian, RoleSummarizer, RoleCritic, RoleQuestioner, RoleDomainExpert, RoleConnector, RoleSynthesizer, RoleCitationVerifier}
	for _, role := range want {
		profile, ok := profiles[role]
		if !ok {
			t.Fatalf("missing default profile for %q", role)
		}
		if err := profile.Validate(); err != nil {
			t.Fatalf("profile %q is invalid: %v", role, err)
		}
	}
}

func TestContributionValidateEnforcesEpistemicProvenance(t *testing.T) {
	profile := DefaultProfiles()[RoleSummarizer]
	claim := Contribution{
		ID:             "contribution-1",
		Role:           RoleSummarizer,
		ProfileID:      profile.ID,
		ProfileVersion: profile.Version,
		Kind:           ContributionClaim,
		Statement:      "The author says deliberate practice targets weaknesses.",
		Confidence:     0.9,
		Provenance: core.KnowledgeProvenance{
			Attribution: core.Attribution{Kind: core.AttributionAuthor, SubjectID: "author-1"},
			Form:        core.KnowledgeClaim,
			Derivation:  core.DerivationExtracted,
		},
	}
	if err := claim.Validate(profile); err == nil {
		t.Fatal("expected an author claim without citations to be rejected")
	}

	claim.Citations = []core.Citation{{
		ID:            "citation-1",
		EditionID:     "edition-1",
		SourceAssetID: "asset-1",
		PassageID:     "passage-1",
		Locator: core.SourceLocator{
			Kind:                 core.LocatorEPUB,
			Display:              "Chapter 3",
			ParserVersion:        "epub-v1",
			NormalizationVersion: "text-v1",
			EPUB:                 &core.EPUBLocator{SpineItem: "chapter-3.xhtml", CFI: "epubcfi(/6/8)"},
		},
		PassageFingerprint: "sha256:passage",
	}}
	claim.Provenance.CitationIDs = []string{"citation-1"}
	if err := claim.Validate(profile); err == nil {
		t.Fatal("expected cited but unverified author claim to be rejected")
	}
	claim.Verifications = []core.EvidenceVerification{{
		ID:                  "verification-claim",
		SubjectID:           claim.ID,
		CitationID:          "citation-1",
		Verdict:             core.VerdictSupports,
		Method:              core.VerificationEntailment,
		EvidenceFingerprint: "sha256:passage",
		SubjectFingerprint:  core.FingerprintText(claim.Statement),
		VerifierID:          "verifier-1",
		VerifierVersion:     "v1",
		VerifiedAt:          time.Now().UTC(),
	}}
	claim.Citations[0].VerificationIDs = []string{"verification-claim"}
	if err := claim.Validate(profile); err != nil {
		t.Fatalf("expected verified author claim to be valid: %v", err)
	}
}

func TestContributionValidateRequiresVerifiedExactQuote(t *testing.T) {
	profile := DefaultProfiles()[RoleSummarizer]
	quote := Contribution{
		ID:             "contribution-quote",
		Role:           RoleSummarizer,
		ProfileID:      profile.ID,
		ProfileVersion: profile.Version,
		Kind:           ContributionQuote,
		Statement:      "Practice is not play.",
		Confidence:     1,
		Provenance: core.KnowledgeProvenance{
			Attribution: core.Attribution{Kind: core.AttributionAuthor, SubjectID: "author-1"},
			Form:        core.KnowledgeQuote,
			Derivation:  core.DerivationExtracted,
			CitationIDs: []string{"citation-quote"},
		},
		Citations: []core.Citation{{
			ID:            "citation-quote",
			EditionID:     "edition-1",
			SourceAssetID: "asset-1",
			PassageID:     "passage-quote",
			Locator: core.SourceLocator{
				Kind:                 core.LocatorPDF,
				Display:              "p. 61",
				ParserVersion:        "pdf-v1",
				NormalizationVersion: "text-v1",
				PDF:                  &core.PDFLocator{Page: 61},
			},
			ShortQuote:         "Practice is not play.",
			VerificationIDs:    []string{"verification-quote"},
			PassageFingerprint: "sha256:quote",
		}},
		Verifications: []core.EvidenceVerification{{
			ID:                  "verification-quote",
			SubjectID:           "contribution-quote",
			CitationID:          "citation-quote",
			Verdict:             core.VerdictSupports,
			Method:              core.VerificationExactMatch,
			EvidenceFingerprint: "sha256:quote",
			SubjectFingerprint:  core.FingerprintText("Practice is not play."),
			VerifierID:          "deterministic:quote-match",
			VerifierVersion:     "v1",
			VerifiedAt:          time.Now().UTC(),
		}},
	}
	if err := quote.Validate(profile); err != nil {
		t.Fatalf("expected exact verified quote to pass: %v", err)
	}

	quote.Citations[0].ShortQuote = "Practice is play."
	if err := quote.Validate(profile); err == nil {
		t.Fatal("expected quote text mismatch to be rejected")
	}
}

func TestSynthesisRequiresDerivationAndProfileOutputPermission(t *testing.T) {
	profile := DefaultProfiles()[RoleSynthesizer]
	synthesis := Contribution{
		ID:             "synthesis-1",
		Role:           RoleSynthesizer,
		ProfileID:      profile.ID,
		ProfileVersion: profile.Version,
		Kind:           ContributionSynthesis,
		Statement:      "The contributions agree on the method but dispute its scope.",
		Confidence:     0.8,
		Provenance: core.KnowledgeProvenance{
			Attribution: core.Attribution{Kind: core.AttributionAgent, SubjectID: "agent-synthesizer"},
			Form:        core.KnowledgeSynthesis,
			Derivation:  core.DerivationConsolidated,
		},
	}
	if err := synthesis.Validate(profile); err == nil {
		t.Fatal("expected synthesis without derivations to be rejected")
	}
	synthesis.Provenance.DerivedFrom = []string{"contribution-1", "contribution-2"}
	if err := synthesis.Validate(profile); err != nil {
		t.Fatalf("expected derived synthesis to pass: %v", err)
	}

	critic := DefaultProfiles()[RoleCritic]
	criticContribution := synthesis
	criticContribution.Role = RoleCritic
	criticContribution.ProfileID = critic.ID
	criticContribution.ProfileVersion = critic.Version
	if err := criticContribution.Validate(critic); err == nil {
		t.Fatal("critic profile must reject a final synthesis")
	}
}

func TestContributionRejectsChangedProfileVersion(t *testing.T) {
	profile := DefaultProfiles()[RoleQuestioner]
	contribution := Contribution{
		ID:             "question-1",
		Role:           RoleQuestioner,
		ProfileID:      profile.ID,
		ProfileVersion: "stale-version",
		Kind:           ContributionQuestion,
		Statement:      "What evidence would change your conclusion?",
		Confidence:     0.8,
		Provenance: core.KnowledgeProvenance{
			Attribution: core.Attribution{Kind: core.AttributionAgent, SubjectID: "agent-questioner"},
			Form:        core.KnowledgeQuestion,
			Derivation:  core.DerivationInterpreted,
		},
	}
	if err := contribution.Validate(profile); err == nil {
		t.Fatal("expected changed profile version to invalidate contribution")
	}
}
