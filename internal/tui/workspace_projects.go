package tui

import (
	"fmt"
	"math"
)

func projectMemoryCount(record map[string]any) (int, bool) {
	if status, present := record["count_status"]; present && status != "ready" {
		return 0, false
	}
	switch count := record["memory_count"].(type) {
	case int:
		return count, count >= 0
	case float64:
		if count >= 0 && count < float64(math.MaxInt) && count == math.Trunc(count) {
			return int(count), true
		}
	}
	return 0, false
}

func projectMemoryLabel(record map[string]any) string {
	count, known := projectMemoryCount(record)
	if !known {
		return "count unavailable"
	}
	if count == 1 {
		return "1 memory"
	}
	return fmt.Sprintf("%d memories", count)
}

func projectListSummary(projects []map[string]any) string {
	total, unavailable := 0, 0
	for _, project := range projects {
		if count, known := projectMemoryCount(project); known && count <= math.MaxInt-total {
			total += count
		} else {
			unavailable++
		}
	}
	label := fmt.Sprintf("All projects · %d registered · %d memories", len(projects), total)
	if unavailable > 0 {
		label += fmt.Sprintf(" · %d unavailable", unavailable)
	}
	return label
}

func projectRecordDocument(record map[string]any) []string {
	lines := []string{accentStyle.Render("Project: " + sanitizeTerminalText(recordLabel(record), false)), projectMemoryLabel(record), ""}
	if root, ok := record["workspace_root"].(string); ok && root != "" {
		lines = append(lines, "Root: "+sanitizeTerminalText(root, false), "")
	}
	return append(lines, "Enter Open project", "Search, Ask, Notes and Sources use that project's data.", "d Advanced details · r Refresh counts")
}
