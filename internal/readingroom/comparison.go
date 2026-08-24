package readingroom

import (
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/retrieval"
)

type BookPosition struct {
	EditionID       string            `json:"edition_id"`
	Statements      []AnswerStatement `json:"statements"`
	MissingEvidence bool              `json:"missing_evidence"`
}
type CrossBookComparison struct {
	Question  string           `json:"question"`
	Positions []BookPosition   `json:"positions"`
	Synthesis *SynthesisResult `json:"synthesis,omitempty"`
}

func NewCrossBookComparison(evidence retrieval.ComparisonEvidence, positions []BookPosition) (CrossBookComparison, error) {
	if len(positions) != len(evidence.Editions) {
		return CrossBookComparison{}, errors.New("comparison must preserve one position per selected edition")
	}
	for i, item := range evidence.Editions {
		if positions[i].EditionID != item.EditionID || positions[i].MissingEvidence != item.Missing {
			return CrossBookComparison{}, errors.New("comparison positions do not match balanced evidence plan")
		}
	}
	return CrossBookComparison{Question: evidence.Question, Positions: positions}, nil
}
