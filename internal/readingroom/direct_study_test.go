package readingroom

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/retrieval"
)

func TestDirectStudyProducesOnlyVerifiedGroundedAnswer(t *testing.T) {
	passage := library.Passage{
		ID: "passage-1", EditionID: "edition-1", SourceAssetID: "asset-1", StructuralNodeID: "node-1",
		Text: "Focused practice improves skill.", Fingerprint: "sha256:evidence",
		Locator: core.SourceLocator{Kind: core.LocatorPDF, Display: "p. 1", ParserVersion: "pdf-v1", NormalizationVersion: "text-v1", PDF: &core.PDFLocator{Page: 1}},
	}
	profile := DefaultProfiles()[RoleSummarizer]
	workflow := NewDirectStudyWorkflow(
		&fakeDirectSearcher{results: []retrieval.PassageResult{{Passage: passage, Score: 2}}},
		&fakeScholar{profile: profile},
		&fakeDirectVerifier{verify: true},
		profile,
	)
	answer, err := workflow.Run(context.Background(), DirectStudyRequest{
		Question: "What improves skill?", Scope: validStudyScope(), Budget: StudyBudget{MaxPassages: 5, MaxOutputTokens: 500},
	})
	if err != nil {
		t.Fatalf("run direct study: %v", err)
	}
	if err := answer.Validate(); err != nil || len(answer.Statements) != 1 || answer.Statements[0].EvidenceState != EvidenceSupported {
		t.Fatalf("expected verified answer: %+v err=%v", answer, err)
	}

	workflow.verifier = &fakeDirectVerifier{verify: false}
	if _, err := workflow.Run(context.Background(), DirectStudyRequest{Question: "What improves skill?", Scope: validStudyScope(), Budget: StudyBudget{MaxPassages: 5, MaxOutputTokens: 500}}); err == nil {
		t.Fatal("expected unsupported author claim to be excluded from final answer")
	}
}

func TestDirectStudyPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	workflow := NewDirectStudyWorkflow(&fakeDirectSearcher{}, &fakeScholar{}, &fakeDirectVerifier{}, DefaultProfiles()[RoleSummarizer])
	_, err := workflow.Run(ctx, DirectStudyRequest{Question: "question", Scope: validStudyScope(), Budget: StudyBudget{MaxPassages: 1, MaxOutputTokens: 1}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

type fakeDirectSearcher struct{ results []retrieval.PassageResult }

func (f *fakeDirectSearcher) Search(context.Context, core.AuthorizationScope, string, int) ([]retrieval.PassageResult, error) {
	return f.results, nil
}

type fakeScholar struct{ profile AgentProfile }

func (f *fakeScholar) Draft(_ context.Context, question string, passages []library.Passage) ([]Contribution, error) {
	if len(passages) == 0 {
		return nil, errors.New("no evidence")
	}
	citation := core.Citation{
		ID: "citation-1", EditionID: passages[0].EditionID, SourceAssetID: passages[0].SourceAssetID,
		PassageID: passages[0].ID, StructuralNodeID: passages[0].StructuralNodeID, Locator: passages[0].Locator,
		PassageFingerprint: passages[0].Fingerprint,
	}
	return []Contribution{{
		ID: "contribution-1", Role: RoleSummarizer, ProfileID: f.profile.ID, ProfileVersion: f.profile.Version,
		Kind: ContributionClaim, Statement: "Focused practice improves skill.", Confidence: 0.9,
		Provenance: core.KnowledgeProvenance{Attribution: core.Attribution{Kind: core.AttributionAuthor, SubjectID: "author-1"}, Form: core.KnowledgeClaim, Derivation: core.DerivationExtracted, CitationIDs: []string{citation.ID}},
		Citations:  []core.Citation{citation},
	}}, nil
}

type fakeDirectVerifier struct{ verify bool }

func (f *fakeDirectVerifier) Verify(_ context.Context, contributions []Contribution, _ []library.Passage) ([]Contribution, error) {
	if !f.verify {
		return contributions, nil
	}
	verified := append([]Contribution(nil), contributions...)
	v := core.EvidenceVerification{
		ID: "verification-1", SubjectID: verified[0].ID, CitationID: verified[0].Citations[0].ID,
		Verdict: core.VerdictSupports, Method: core.VerificationEntailment,
		EvidenceFingerprint: verified[0].Citations[0].PassageFingerprint, SubjectFingerprint: core.FingerprintText(verified[0].Statement),
		VerifierID: "verifier", VerifierVersion: "v1", VerifiedAt: time.Now().UTC(),
	}
	verified[0].Citations[0].VerificationIDs = []string{v.ID}
	verified[0].Verifications = []core.EvidenceVerification{v}
	return verified, nil
}

func validStudyScope() core.AuthorizationScope {
	return core.AuthorizationScope{
		Principal: core.Principal{ID: "user-1", Kind: core.PrincipalUser}, Capabilities: []core.Capability{core.CapabilitySearchSource}, PolicyVersion: "v1",
	}
}
