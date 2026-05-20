package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
)

const ExportVersion = "v1"

type ExportBundle struct {
	Version    string             `json:"version"`
	Workspace  string             `json:"workspace"`
	ExportedAt time.Time          `json:"exported_at"`
	Memories   []core.MemoryEntry `json:"memories"`
}

func BuildExportBundle(workspace string, memories []core.MemoryEntry) ExportBundle {
	return ExportBundle{
		Version:    ExportVersion,
		Workspace:  workspace,
		ExportedAt: time.Now().UTC(),
		Memories:   memories,
	}
}

func BuildMarkdownExport(workspace string, memories []core.MemoryEntry) string {
	var b strings.Builder
	b.WriteString("# Agent Memory Export\n\n")
	b.WriteString(fmt.Sprintf("Workspace: `%s`\n\n", workspace))
	sections := map[core.MemoryType][]core.MemoryEntry{
		core.ProceduralMemory: {},
		core.SemanticMemory:   {},
		core.OutcomeMemory:    {},
		core.EpisodicMemory:   {},
	}
	for _, m := range memories {
		sections[m.Type] = append(sections[m.Type], m)
	}
	order := []core.MemoryType{
		core.ProceduralMemory,
		core.SemanticMemory,
		core.OutcomeMemory,
		core.EpisodicMemory,
	}
	for _, t := range order {
		items := sections[t]
		if len(items) == 0 {
			continue
		}
		sort.Slice(items, func(i, j int) bool {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		})
		b.WriteString("## ")
		b.WriteString(strings.ToUpper(string(t[:1])))
		b.WriteString(string(t[1:]))
		b.WriteString("\n\n")
		for _, m := range items {
			b.WriteString("- ")
			b.WriteString(m.Content)
			if m.Outcome != nil {
				b.WriteString(fmt.Sprintf(" _(outcome: %s)_", m.Outcome.Result))
				if strings.TrimSpace(m.Outcome.Reason) != "" {
					b.WriteString(fmt.Sprintf(" reason: %s", m.Outcome.Reason))
				}
			}
			if m.Pinned {
				b.WriteString(" [pinned]")
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}
