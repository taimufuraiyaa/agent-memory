package readingroom

import (
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGroundedAnswerSeparatesSupportedClaimsAndInterpretation(t *testing.T) {
	citation := testAnswerCitation()
	verification := core.EvidenceVerification{
		ID: "verification-1", SubjectID: "statement-1", CitationID: citation.ID,
		Verdict: core.VerdictSupports, Method: core.VerificationEntailment,
		EvidenceFingerprint: citation.PassageFingerprint, SubjectFingerprint: core.FingerprintText("The author makes this claim."),
		VerifierID: "verifier", VerifierVersion: "v1", VerifiedAt: time.Now().UTC(),
	}
	citation.VerificationIDs = []string{verification.ID}
	answer := GroundedAnswer{
		Question: "What does this mean?",
		Statements: []AnswerStatement{
			{
				ID: "statement-1", Text: "The author makes this claim.", EvidenceState: EvidenceSupported,
				Provenance: core.KnowledgeProvenance{Attribution: core.Attribution{Kind: core.AttributionAuthor, SubjectID: "author-1"}, Form: core.KnowledgeClaim, Derivation: core.DerivationExtracted, CitationIDs: []string{citation.ID}},
				Citations:  []core.Citation{citation}, Verifications: []core.EvidenceVerification{verification},
			},
			{
				ID: "statement-2", Text: "I interpret this as a practical analogy.", EvidenceState: EvidenceInterpretation,
				Provenance: core.KnowledgeProvenance{Attribution: core.Attribution{Kind: core.AttributionAgent, SubjectID: "agent-1"}, Form: core.KnowledgeExplanation, Derivation: core.DerivationInterpreted},
			},
		},
	}
	if err := answer.Validate(); err != nil {
		t.Fatalf("expected grounded answer: %v", err)
	}

	answer.Statements[0].Verifications = nil
	if err := answer.Validate(); err == nil {
		t.Fatal("expected unsupported author claim to fail")
	}
}

func testAnswerCitation() core.Citation {
	return core.Citation{
		ID: "citation-1", EditionID: "edition-1", SourceAssetID: "asset-1", PassageID: "passage-1", StructuralNodeID: "node-1",
		Locator:            core.SourceLocator{Kind: core.LocatorPDF, Display: "p. 1", ParserVersion: "pdf-v1", NormalizationVersion: "text-v1", PDF: &core.PDFLocator{Page: 1}},
		PassageFingerprint: "sha256:evidence",
	}
}
