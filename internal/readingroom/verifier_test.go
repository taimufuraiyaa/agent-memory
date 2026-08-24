package readingroom

import (
	"context"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

func TestVerifierGateChecksQuoteAgainstSourceEvidence(t *testing.T) {
	passage := testPassage("quote")
	passage.Text = "All roads lead to Rome."
	passage.Fingerprint = core.FingerprintText(passage.Text)
	citation := core.Citation{ID: "citation", EditionID: passage.EditionID, SourceAssetID: passage.SourceAssetID, PassageID: passage.ID, StructuralNodeID: passage.StructuralNodeID, Locator: passage.Locator, PassageFingerprint: passage.Fingerprint, ShortQuote: "fabricated"}
	profile := DefaultProfiles()[RoleSummarizer]
	draft := Contribution{ID: "quote", Role: profile.Role, ProfileID: profile.ID, ProfileVersion: profile.Version, Kind: ContributionQuote, Statement: "All roads lead to Rome.", Provenance: core.KnowledgeProvenance{Attribution: core.Attribution{Kind: core.AttributionAuthor, SubjectID: "author"}, Form: core.KnowledgeQuote, Derivation: core.DerivationExtracted, CitationIDs: []string{citation.ID}}, Citations: []core.Citation{citation}, Confidence: .9}
	result, err := NewVerifierGate("verifier", "v1", nil).Verify(context.Background(), []Contribution{draft}, []library.Passage{passage})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Verified) != 1 || result.Verified[0].Citations[0].ShortQuote != draft.Statement {
		t.Fatalf("valid quote rejected: %+v", result)
	}
	if err := result.Verified[0].Validate(profile); err != nil {
		t.Fatal(err)
	}
	draft.ID, draft.Statement = "bad", "All roads lead to Paris."
	bad, err := NewVerifierGate("verifier", "v1", nil).Verify(context.Background(), []Contribution{draft}, []library.Passage{passage})
	if err != nil {
		t.Fatal(err)
	}
	if len(bad.Verified) != 0 || bad.Rejected["bad"] == "" {
		t.Fatalf("inaccurate quote passed gate: %+v", bad)
	}
}
