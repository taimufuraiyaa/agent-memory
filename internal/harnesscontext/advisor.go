package harnesscontext

import (
	"context"
	"errors"
	"fmt"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

// Candidate is all an advisor ever learns about a chunk: an opaque ID, where it came
// from, its size and a coarse relevance. Never text, titles or paths, and never the
// existence of a chunk above the advisor's egress class.
type Candidate struct {
	ID        string
	Source    Source
	Tokens    int
	Relevance float64
}

// Advisor suggests which candidates deserve more room. It is advisory: the assembler
// validates the answer and falls back to the deterministic order on any problem.
type Advisor interface {
	Promote(ctx context.Context, candidates []Candidate) (ids []string, confidence float64, err error)
}

// SessionAdvisor asks a harness decision session. The question carries only the opaque
// IDs and sizes above; the answer is a subset of those IDs.
type SessionAdvisor struct {
	Session       *harness.Session
	Capability    harness.CapabilityID
	MaxReplyBytes int
}

func (s SessionAdvisor) Promote(ctx context.Context, candidates []Candidate) ([]string, float64, error) {
	if s.Session == nil {
		return nil, 0, errors.New("no decision session")
	}
	maxBytes := s.MaxReplyBytes
	if maxBytes <= 0 {
		maxBytes = 4096
	}
	envelope, err := s.Session.Envelope(s.Capability, maxBytes)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]string, len(candidates))
	evidence := make([]harness.EvidenceRef, len(candidates))
	for i, c := range candidates {
		ids[i] = c.ID
		evidence[i] = harness.EvidenceRef{ID: c.ID, Revision: fmt.Sprintf("t%d", c.Tokens), Class: string(c.Source)}
	}
	answer, err := s.Session.Decide(ctx, harness.DecisionQuestion{Envelope: envelope, Kind: "visibility", Candidates: ids, Evidence: evidence})
	if err != nil {
		return nil, 0, err
	}
	if answer.Outcome != harness.OutcomeOK {
		return nil, 0, fmt.Errorf("decision outcome %s", answer.Outcome)
	}
	return answer.Selected, answer.Confidence, nil
}
