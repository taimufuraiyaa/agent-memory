package readingroom

import (
	"context"
	"errors"
	"fmt"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"strings"
	"time"
)

type EntailmentEvaluator interface {
	Evaluate(context.Context, string, string) (core.VerificationVerdict, error)
}
type VerifierGate struct {
	ID, Version string
	evaluator   EntailmentEvaluator
}
type VerificationGateResult struct {
	Verified []Contribution    `json:"verified"`
	Rejected map[string]string `json:"rejected,omitempty"`
}

func NewVerifierGate(id, version string, e EntailmentEvaluator) *VerifierGate {
	return &VerifierGate{ID: id, Version: version, evaluator: e}
}
func (g *VerifierGate) Verify(ctx context.Context, contributions []Contribution, evidence []library.Passage) (VerificationGateResult, error) {
	if g == nil || strings.TrimSpace(g.ID) == "" || strings.TrimSpace(g.Version) == "" {
		return VerificationGateResult{}, errors.New("verifier identity is required")
	}
	passages := map[string]library.Passage{}
	for _, p := range evidence {
		passages[p.ID] = p
	}
	out := VerificationGateResult{Verified: []Contribution{}, Rejected: map[string]string{}}
	for _, draft := range contributions {
		candidate := draft
		candidate.Verifications = nil
		failed := ""
		for ci := range candidate.Citations {
			citation := &candidate.Citations[ci]
			passage, ok := passages[citation.PassageID]
			if !ok || passage.Fingerprint != citation.PassageFingerprint {
				failed = "citation evidence unavailable"
				break
			}
			method := core.VerificationEntailment
			verdict := core.VerdictSupports
			if candidate.Provenance.Form == core.KnowledgeQuote {
				method = core.VerificationExactMatch
				if !strings.Contains(passage.Text, candidate.Statement) {
					failed = "quote is not an exact source substring"
					break
				}
				citation.ShortQuote = candidate.Statement
			} else if g.evaluator != nil {
				var err error
				verdict, err = g.evaluator.Evaluate(ctx, candidate.Statement, passage.Text)
				if err != nil {
					return out, err
				}
			}
			if verdict == core.VerdictInsufficient {
				failed = "insufficient evidence"
				break
			}
			verification := core.EvidenceVerification{ID: fmt.Sprintf("verification_%s_%d", candidate.ID, ci), SubjectID: candidate.ID, CitationID: citation.ID, Verdict: verdict, Method: method, EvidenceFingerprint: passage.Fingerprint, SubjectFingerprint: core.FingerprintText(candidate.Statement), VerifierID: g.ID, VerifierVersion: g.Version, VerifiedAt: time.Now().UTC()}
			citation.VerificationIDs = append(citation.VerificationIDs, verification.ID)
			candidate.Verifications = append(candidate.Verifications, verification)
		}
		if failed != "" {
			out.Rejected[candidate.ID] = failed
			continue
		}
		out.Verified = append(out.Verified, candidate)
	}
	return out, nil
}
