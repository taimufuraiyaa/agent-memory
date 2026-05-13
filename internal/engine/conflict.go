package engine

import (
	"context"
	"strings"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

type ConflictResult struct {
	WinnerID  string
	LoserID   string
	Ambiguous bool
	Reason    string
}

// ConflictEngine detects and resolves contradictory memories.
type ConflictEngine struct {
	store *sqlite.Store
}

func NewConflictEngine(store *sqlite.Store) *ConflictEngine { return &ConflictEngine{store: store} }

func (e *ConflictEngine) Resolve(ctx context.Context, workspace string) ([]ConflictResult, error) {
	memories, err := e.store.ListMemoriesByWorkspace(ctx, workspace)
	if err != nil {
		return nil, err
	}
	candidates := make([]core.MemoryEntry, 0)
	for _, m := range memories {
		if m.Type == core.SemanticMemory && m.SupersededBy == nil {
			candidates = append(candidates, m)
		}
	}

	results := make([]ConflictResult, 0)
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			a, b := candidates[i], candidates[j]
			if !potentialConflict(a.Content, b.Content) {
				continue
			}
			w, l, amb := pickWinner(a, b)
			_ = e.store.AddRelation(ctx, l.ID, w.ID, core.RelContradicts, 1, map[string]string{"ambiguous": boolStr(amb)})
			if !amb {
				if err := e.store.MarkSuperseded(ctx, []string{l.ID}, w.ID); err != nil {
					return nil, err
				}
			}
			results = append(results, ConflictResult{
				WinnerID:  w.ID,
				LoserID:   l.ID,
				Ambiguous: amb,
				Reason:    "negation conflict",
			})
		}
	}
	return results, nil
}

func potentialConflict(a, b string) bool {
	share := overlap(a, b) >= 0.4
	if !share {
		return false
	}
	return hasNegation(a) != hasNegation(b)
}

func hasNegation(s string) bool {
	low := strings.ToLower(s)
	markers := []string{" not ", " never ", " no ", " cannot ", " can't ", " disabled ", " does not "}
	for _, m := range markers {
		if strings.Contains(" "+low+" ", m) {
			return true
		}
	}
	return false
}

func pickWinner(a, b core.MemoryEntry) (winner, loser core.MemoryEntry, ambiguous bool) {
	if a.UpdatedAt.After(b.UpdatedAt) {
		return a, b, false
	}
	if b.UpdatedAt.After(a.UpdatedAt) {
		return b, a, false
	}
	if a.AccessCount > b.AccessCount {
		return a, b, false
	}
	if b.AccessCount > a.AccessCount {
		return b, a, false
	}
	return a, b, true
}

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

