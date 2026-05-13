package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/time/timebooks/agent-memory/internal/core"
)

// AssembleRecallSections emits stable sectioned recall text for session start.
func AssembleRecallSections(task string, hits []RetrievalHit) string {
	var b strings.Builder
	b.WriteString("## Task\n")
	b.WriteString(strings.TrimSpace(task))
	b.WriteString("\n\n## Relevant Memories\n")
	if len(hits) == 0 {
		b.WriteString("- none\n")
		return b.String()
	}
	for i, h := range hits {
		fmt.Fprintf(&b, "%d. [%s]\n%s\n\n", i+1, h.Memory.Type, memoryTextForRecall(h.Memory))
	}
	return b.String()
}

// RebalanceRecallHits applies task-aware ordering and type quotas for recall.
func RebalanceRecallHits(task string, hits []RetrievalHit) []RetrievalHit {
	if len(hits) <= 1 {
		return hits
	}
	intent := detectTaskIntent(task)
	keywords := taskKeywords(task)

	type scored struct {
		hit      RetrievalHit
		adjusted float64
	}
	buckets := map[core.MemoryType][]scored{
		core.SemanticMemory:   {},
		core.ProceduralMemory: {},
		core.OutcomeMemory:    {},
		core.EpisodicMemory:   {},
	}
	for _, h := range hits {
		adj := h.Score + 0.08*keywordOverlap(keywords, h.Memory.Content)
		switch intent {
		case "procedural":
			if h.Memory.Type == core.ProceduralMemory {
				adj += 0.1
			}
		case "outcome":
			if h.Memory.Type == core.OutcomeMemory {
				adj += 0.1
			}
		default:
			if h.Memory.Type == core.SemanticMemory {
				adj += 0.06
			}
		}
		buckets[h.Memory.Type] = append(buckets[h.Memory.Type], scored{hit: h, adjusted: adj})
	}
	for mt := range buckets {
		sort.SliceStable(buckets[mt], func(i, j int) bool { return buckets[mt][i].adjusted > buckets[mt][j].adjusted })
	}

	plan := rebalancePlan(intent)
	out := make([]RetrievalHit, 0, len(hits))
	used := make(map[string]struct{}, len(hits))
	appendType := func(mt core.MemoryType) bool {
		items := buckets[mt]
		for len(items) > 0 {
			next := items[0]
			items = items[1:]
			buckets[mt] = items
			if _, ok := used[next.hit.Memory.ID]; ok {
				continue
			}
			used[next.hit.Memory.ID] = struct{}{}
			out = append(out, next.hit)
			return true
		}
		return false
	}
	for len(out) < len(hits) {
		progress := false
		for _, mt := range plan {
			if len(out) >= len(hits) {
				break
			}
			if appendType(mt) {
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	if len(out) == len(hits) {
		return out
	}
	// Fallback: preserve remaining original order if any item was left out.
	for _, h := range hits {
		if _, ok := used[h.Memory.ID]; ok {
			continue
		}
		out = append(out, h)
	}
	return out
}

func detectTaskIntent(task string) string {
	l := strings.ToLower(task)
	switch {
	case strings.Contains(l, "how"), strings.Contains(l, "step"), strings.Contains(l, "configure"), strings.Contains(l, "setup"), strings.Contains(l, "implement"):
		return "procedural"
	case strings.Contains(l, "why"), strings.Contains(l, "failed"), strings.Contains(l, "failure"), strings.Contains(l, "incident"), strings.Contains(l, "outcome"):
		return "outcome"
	default:
		return "general"
	}
}

func rebalancePlan(intent string) []core.MemoryType {
	switch intent {
	case "procedural":
		return []core.MemoryType{core.ProceduralMemory, core.SemanticMemory, core.OutcomeMemory, core.EpisodicMemory}
	case "outcome":
		return []core.MemoryType{core.OutcomeMemory, core.SemanticMemory, core.ProceduralMemory, core.EpisodicMemory}
	default:
		return []core.MemoryType{core.SemanticMemory, core.ProceduralMemory, core.OutcomeMemory, core.EpisodicMemory}
	}
}

func taskKeywords(task string) map[string]struct{} {
	parts := strings.Fields(strings.ToLower(task))
	out := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		p = strings.Trim(p, ".,:;!?()[]{}\"'`")
		if len(p) < 4 {
			continue
		}
		out[p] = struct{}{}
	}
	return out
}

func keywordOverlap(keywords map[string]struct{}, text string) float64 {
	if len(keywords) == 0 {
		return 0
	}
	parts := strings.Fields(strings.ToLower(text))
	if len(parts) == 0 {
		return 0
	}
	count := 0
	for _, p := range parts {
		p = strings.Trim(p, ".,:;!?()[]{}\"'`")
		if _, ok := keywords[p]; ok {
			count++
		}
	}
	return float64(count) / float64(len(parts))
}

func memoryTextForRecall(m core.MemoryEntry) string {
	base := strings.TrimSpace(m.Content)
	if m.Diagram == nil || strings.TrimSpace(m.Diagram.Code) == "" {
		return base
	}
	lang := strings.TrimSpace(m.Diagram.Lang)
	if lang == "" {
		lang = "mermaid"
	}
	if base == "" {
		base = "Diagram (" + lang + ")"
	}
	return base + "\n```" + lang + "\n" + strings.TrimRight(m.Diagram.Code, "\n") + "\n```"
}
