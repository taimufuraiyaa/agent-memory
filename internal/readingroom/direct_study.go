package readingroom

import (
	"context"
	"errors"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/retrieval"
)

type DirectPassageSearcher interface {
	Search(context.Context, core.AuthorizationScope, string, int) ([]retrieval.PassageResult, error)
}

type Scholar interface {
	Draft(context.Context, string, []library.Passage) ([]Contribution, error)
}

type ContributionVerifier interface {
	Verify(context.Context, []Contribution, []library.Passage) ([]Contribution, error)
}

type StudyBudget struct {
	MaxPassages     int `json:"max_passages"`
	MaxOutputTokens int `json:"max_output_tokens"`
}

type DirectStudyRequest struct {
	Question string                  `json:"question"`
	Scope    core.AuthorizationScope `json:"scope"`
	Budget   StudyBudget             `json:"budget"`
}

type DirectStudyWorkflow struct {
	searcher DirectPassageSearcher
	scholar  Scholar
	verifier ContributionVerifier
	profile  AgentProfile
}

func NewDirectStudyWorkflow(searcher DirectPassageSearcher, scholar Scholar, verifier ContributionVerifier, profile AgentProfile) *DirectStudyWorkflow {
	return &DirectStudyWorkflow{searcher: searcher, scholar: scholar, verifier: verifier, profile: profile}
}

func (w *DirectStudyWorkflow) Run(ctx context.Context, request DirectStudyRequest) (GroundedAnswer, error) {
	if err := ctx.Err(); err != nil {
		return GroundedAnswer{}, err
	}
	if w == nil || w.searcher == nil || w.scholar == nil || w.verifier == nil || strings.TrimSpace(request.Question) == "" {
		return GroundedAnswer{}, errors.New("direct study requires workflow components and question")
	}
	if request.Scope.Validate() != nil || request.Budget.MaxPassages <= 0 || request.Budget.MaxOutputTokens <= 0 {
		return GroundedAnswer{}, errors.New("direct study requires valid scope and positive budget")
	}
	if err := w.profile.Validate(); err != nil {
		return GroundedAnswer{}, err
	}
	results, err := w.searcher.Search(ctx, request.Scope, request.Question, request.Budget.MaxPassages)
	if err != nil {
		return GroundedAnswer{}, err
	}
	passages := make([]library.Passage, len(results))
	for index, result := range results {
		passages[index] = result.Passage
	}
	if err := ctx.Err(); err != nil {
		return GroundedAnswer{}, err
	}
	drafts, err := w.scholar.Draft(ctx, request.Question, passages)
	if err != nil {
		return GroundedAnswer{}, err
	}
	verified, err := w.verifier.Verify(ctx, drafts, passages)
	if err != nil {
		return GroundedAnswer{}, err
	}
	statements := []AnswerStatement{}
	for _, contribution := range verified {
		if err := contribution.Validate(w.profile); err != nil {
			continue
		}
		state := EvidenceInterpretation
		if len(contribution.Verifications) > 0 {
			state = EvidenceSupported
		}
		statements = append(statements, AnswerStatement{
			ID: contribution.ID, Text: contribution.Statement, EvidenceState: state,
			Provenance: contribution.Provenance, Citations: contribution.Citations,
			Verifications: contribution.Verifications, Confidence: contribution.Confidence,
		})
	}
	answer := GroundedAnswer{Question: request.Question, Statements: statements}
	if err := answer.Validate(); err != nil {
		return GroundedAnswer{}, err
	}
	return answer, nil
}
