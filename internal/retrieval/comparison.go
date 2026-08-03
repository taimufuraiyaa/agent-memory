package retrieval

import (
	"context"
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"sort"
	"strings"
)

type ComparisonStore interface {
	GetLibraryResourcePolicy(context.Context, library.ResourceType, string) (library.LibraryResourcePolicy, error)
	ListPassagesForEditions(context.Context, []string) ([]library.Passage, error)
}
type EditionEvidence struct {
	EditionID string          `json:"edition_id"`
	Results   []PassageResult `json:"results"`
	Missing   bool            `json:"missing"`
}
type ComparisonEvidence struct {
	Question string            `json:"question"`
	Editions []EditionEvidence `json:"editions"`
}
type CrossBookPlanner struct{ store ComparisonStore }

func NewCrossBookPlanner(store ComparisonStore) *CrossBookPlanner {
	return &CrossBookPlanner{store: store}
}
func (p *CrossBookPlanner) Plan(ctx context.Context, scope core.AuthorizationScope, question string, editionIDs []string, perEdition int) (ComparisonEvidence, error) {
	if p == nil || p.store == nil || scope.Validate() != nil || strings.TrimSpace(question) == "" || len(editionIDs) < 2 || perEdition <= 0 {
		return ComparisonEvidence{}, errors.New("comparison requires authorized scope, question, editions, and positive limit")
	}
	terms := uniqueTerms(question)
	out := ComparisonEvidence{Question: question, Editions: make([]EditionEvidence, 0, len(editionIDs))}
	for _, editionID := range editionIDs {
		item := EditionEvidence{EditionID: editionID, Results: []PassageResult{}}
		resource, err := p.store.GetLibraryResourcePolicy(ctx, library.ResourceEdition, editionID)
		if err != nil || !core.Authorize(scope, resource.Policy, core.CapabilitySearchSource).Allowed {
			item.Missing = true
			out.Editions = append(out.Editions, item)
			continue
		}
		passages, err := p.store.ListPassagesForEditions(ctx, []string{editionID})
		if err != nil {
			return ComparisonEvidence{}, err
		}
		for _, passage := range passages {
			score := 0
			text := strings.ToLower(passage.Text)
			for _, term := range terms {
				score += strings.Count(text, term)
			}
			if score > 0 {
				item.Results = append(item.Results, PassageResult{Passage: passage, Score: score})
			}
		}
		sort.Slice(item.Results, func(i, j int) bool {
			if item.Results[i].Score == item.Results[j].Score {
				return item.Results[i].Passage.ID < item.Results[j].Passage.ID
			}
			return item.Results[i].Score > item.Results[j].Score
		})
		if len(item.Results) > perEdition {
			item.Results = item.Results[:perEdition]
		}
		item.Missing = len(item.Results) == 0
		out.Editions = append(out.Editions, item)
	}
	return out, nil
}
