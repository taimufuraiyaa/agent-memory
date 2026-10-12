package application

import (
	"context"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// lexicalCandidates returns BM25-ranked canonical memories for the query and a
// relative score per memory ID (best hit = 1). It is fail-open: when the stream
// is off, the index is unavailable or the query errors, no candidates are
// returned and the caller proceeds with its other discoveries.
func (s *MemoryService) lexicalCandidates(ctx context.Context, opt engine.RetrievalOptions) ([]core.MemoryEntry, map[string]float64) {
	if s == nil || s.store == nil || engine.LexicalMode() != engine.LexicalOn {
		return nil, nil
	}
	limit := min(max(opt.TopK*3, 16), 128)
	matches, err := s.store.SearchMemoryLexical(ctx, sqlite.LexicalQuery{Workspace: opt.Workspace, Text: opt.Query, Limit: limit})
	if err != nil || len(matches) == 0 {
		return nil, nil
	}
	best := matches[0].Score
	if best <= 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(matches))
	relative := make(map[string]float64, len(matches))
	for _, match := range matches {
		ids = append(ids, match.MemoryID)
		relative[match.MemoryID] = match.Score / best
	}
	current, err := s.store.GetMemoriesByIDs(ctx, ids)
	if err != nil {
		return nil, nil
	}
	memories := make([]core.MemoryEntry, 0, len(ids))
	for _, id := range ids {
		if memory, ok := current[id]; ok {
			memories = append(memories, memory)
		}
	}
	return memories, relative
}
