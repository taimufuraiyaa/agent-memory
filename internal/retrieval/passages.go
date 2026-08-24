// Package retrieval implements authorization-scoped library retrieval.
package retrieval

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

type PassageStore interface {
	ListLibraryResourcePolicies(context.Context, library.ResourceType) ([]library.LibraryResourcePolicy, error)
	ListPassagesForEditions(context.Context, []string) ([]library.Passage, error)
}

type PassageResult struct {
	Passage library.Passage `json:"passage"`
	Score   int             `json:"score"`
}

type LexicalPassageSearch struct {
	store PassageStore
}

func NewLexicalPassageSearch(store PassageStore) *LexicalPassageSearch {
	return &LexicalPassageSearch{store: store}
}

func (s *LexicalPassageSearch) Search(ctx context.Context, scope core.AuthorizationScope, query string, limit int) ([]PassageResult, error) {
	if s == nil || s.store == nil || scope.Validate() != nil {
		return nil, errors.New("valid authorized passage store and scope are required")
	}
	terms := uniqueTerms(query)
	if len(terms) == 0 {
		return []PassageResult{}, nil
	}
	if limit <= 0 {
		limit = 10
	}
	resources, err := s.store.ListLibraryResourcePolicies(ctx, library.ResourceEdition)
	if err != nil {
		return nil, err
	}
	authorized := []string{}
	for _, resource := range resources {
		if core.Authorize(scope, resource.Policy, core.CapabilitySearchSource).Allowed {
			authorized = append(authorized, resource.ResourceID)
		}
	}
	sort.Strings(authorized)
	if len(authorized) == 0 {
		return []PassageResult{}, nil
	}
	passages, err := s.store.ListPassagesForEditions(ctx, authorized)
	if err != nil {
		return nil, err
	}
	results := make([]PassageResult, 0, len(passages))
	for _, passage := range passages {
		text := strings.ToLower(passage.Text)
		score := 0
		for _, term := range terms {
			score += strings.Count(text, term)
		}
		if score > 0 {
			results = append(results, PassageResult{Passage: passage, Score: score})
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Passage.ID < results[j].Passage.ID
		}
		return results[i].Score > results[j].Score
	})
	diverse := make([]PassageResult, 0, min(limit, len(results)))
	perNode := map[string]int{}
	for _, result := range results {
		if perNode[result.Passage.StructuralNodeID] >= 2 {
			continue
		}
		diverse = append(diverse, result)
		perNode[result.Passage.StructuralNodeID]++
		if len(diverse) == limit {
			break
		}
	}
	return diverse, nil
}

func uniqueTerms(query string) []string {
	seen := map[string]bool{}
	terms := []string{}
	for _, term := range strings.Fields(strings.ToLower(query)) {
		term = strings.Trim(term, ".,!?;:\"'()[]{}")
		if term != "" && !seen[term] {
			seen[term] = true
			terms = append(terms, term)
		}
	}
	return terms
}
