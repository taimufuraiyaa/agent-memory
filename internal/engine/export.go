package engine

import (
	"encoding/csv"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
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

// BuildCSVExport exports memories to CSV format.
//
// Columns: id, type, content, workspace, confidence, storage_tier, pinned,
// access_count, useful_count, created_at, updated_at
func BuildCSVExport(workspace string, memories []core.MemoryEntry) (string, error) {
	var b strings.Builder
	w := csv.NewWriter(&b)

	// Write header
	header := []string{
		"id",
		"type",
		"content",
		"workspace",
		"confidence",
		"storage_tier",
		"pinned",
		"access_count",
		"useful_count",
		"ignored_count",
		"rejected_count",
		"decay_score",
		"outcome_result",
		"outcome_approach",
		"created_at",
		"updated_at",
	}
	if err := w.Write(header); err != nil {
		return "", fmt.Errorf("failed to write CSV header: %w", err)
	}

	// Sort by updated_at (most recent first)
	sorted := make([]core.MemoryEntry, len(memories))
	copy(sorted, memories)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].UpdatedAt.After(sorted[j].UpdatedAt)
	})

	// Write rows
	for _, m := range sorted {
		outcomeResult := ""
		outcomeApproach := ""
		if m.Outcome != nil {
			outcomeResult = string(m.Outcome.Result)
			outcomeApproach = m.Outcome.Approach
		}

		row := []string{
			m.ID,
			string(m.Type),
			m.Content,
			m.Workspace,
			fmt.Sprintf("%.2f", m.Confidence),
			string(m.StorageTier),
			fmt.Sprintf("%t", m.Pinned),
			fmt.Sprintf("%d", m.AccessCount),
			fmt.Sprintf("%d", m.UsefulCount),
			fmt.Sprintf("%d", m.IgnoredCount),
			fmt.Sprintf("%d", m.RejectedCount),
			fmt.Sprintf("%.4f", m.DecayScore),
			outcomeResult,
			outcomeApproach,
			m.CreatedAt.Format(time.RFC3339),
			m.UpdatedAt.Format(time.RFC3339),
		}

		if err := w.Write(row); err != nil {
			return "", fmt.Errorf("failed to write CSV row: %w", err)
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return "", fmt.Errorf("CSV write error: %w", err)
	}

	return b.String(), nil
}
