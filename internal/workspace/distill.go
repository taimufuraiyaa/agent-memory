package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// DistillOptions holds parameters for packaging workspace memories into a skill.
type DistillOptions struct {
	Workspace           string
	SkillName           string
	Description         string
	Force               bool
	SourceMemoryIDs     []string
	SourceToolLessonIDs []string
}

// DistillResult represents the metadata of the distilled skill.
type DistillResult struct {
	Workspace      string `json:"workspace"`
	SkillName      string `json:"skill_name"`
	SkillPath      string `json:"skill_path"`
	ProvenancePath string `json:"provenance_path"`
}

type DistillProvenance struct {
	Workspace     string   `json:"workspace"`
	MemoryIDs     []string `json:"memory_ids"`
	ToolLessonIDs []string `json:"tool_lesson_ids"`
	EpisodeIDs    []string `json:"episode_ids"`
}

// Distill queries the workspace database and formats procedural, outcome, and semantic memories
// into an Antigravity-compatible Custom Skill file inside .agents/skills/<SkillName>/SKILL.md.
func (m *Manager) Distill(ctx context.Context, cwd string, opt DistillOptions) (*DistillResult, error) {
	name := strings.TrimSpace(opt.Workspace)
	if name == "" {
		if detected, _, ok := detectWorkspaceFromCWD(cwd); ok {
			name = detected
		}
	}
	if name == "" {
		name = filepath.Base(cwd)
	}
	name, err := ValidateProjectName(name)
	if err != nil {
		return nil, err
	}

	reg, err := m.readRegistry()
	if err != nil {
		return nil, err
	}
	p := findProject(reg, name)
	if p == nil {
		return nil, fmt.Errorf("project %s not found in registry (run init first)", name)
	}
	if !fileExists(p.DBPath) {
		return nil, fmt.Errorf("db file missing: %s", p.DBPath)
	}

	store, err := sqlite.Open(ctx, p.DBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	defer func() { _ = store.Close() }()

	memories, err := store.ListMemoriesByWorkspace(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to list memories: %w", err)
	}
	toolLessonIDs := uniqueDistillIDs(opt.SourceToolLessonIDs)
	episodeIDs := make([]string, 0)
	for _, lessonID := range toolLessonIDs {
		lesson, lessonErr := store.GetSolutionToolLesson(ctx, lessonID)
		if lessonErr != nil || lesson.Workspace != name || lesson.Validation != core.SolutionValidationVerified {
			return nil, fmt.Errorf("tool lesson %s is not a verified lesson in workspace %s", lessonID, name)
		}
		episodeIDs = append(episodeIDs, lesson.SourceEpisodeIDs...)
	}
	promotedMemoryIDs, err := store.ListPublishedToolLessonPromotionMemoryIDs(ctx, toolLessonIDs)
	if err != nil {
		return nil, err
	}
	selectedMemoryIDs := uniqueDistillIDs(append(append([]string(nil), opt.SourceMemoryIDs...), promotedMemoryIDs...))
	if len(toolLessonIDs) > 0 && len(promotedMemoryIDs) == 0 {
		return nil, fmt.Errorf("verified tool lessons must be promoted to procedural memory before distillation")
	}
	if len(selectedMemoryIDs) > 0 {
		selected := make(map[string]struct{}, len(selectedMemoryIDs))
		for _, id := range selectedMemoryIDs {
			selected[id] = struct{}{}
		}
		filtered := make([]core.MemoryEntry, 0, len(selectedMemoryIDs))
		found := make(map[string]struct{})
		for _, memory := range memories {
			if _, ok := selected[memory.ID]; ok {
				filtered = append(filtered, memory)
				found[memory.ID] = struct{}{}
			}
		}
		if len(found) != len(selected) {
			return nil, fmt.Errorf("one or more source memory ids were not found in workspace %s", name)
		}
		memories = filtered
	}

	var procedurals []core.MemoryEntry
	var semantics []core.MemoryEntry
	var outcomes []core.MemoryEntry

	for _, mem := range memories {
		switch mem.Type {
		case core.ProceduralMemory:
			procedurals = append(procedurals, mem)
		case core.SemanticMemory:
			semantics = append(semantics, mem)
		case core.OutcomeMemory:
			outcomes = append(outcomes, mem)
		}
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("name: %s\n", opt.SkillName))
	desc := strings.TrimSpace(opt.Description)
	if desc == "" {
		desc = fmt.Sprintf("Distilled agent skills from workspace %s", name)
	}
	sb.WriteString(fmt.Sprintf("description: %s\n", desc))
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("# %s\n\n", opt.SkillName))
	sb.WriteString(desc + "\n\n")

	if len(procedurals) > 0 {
		sb.WriteString("## Workflows & Checklists\n\n")
		for _, p := range procedurals {
			sb.WriteString(fmt.Sprintf("- %s\n", p.Content))
		}
		sb.WriteString("\n")
	}

	if len(outcomes) > 0 {
		sb.WriteString("## Attempt Outcomes & Learnings\n\n")
		for _, o := range outcomes {
			sb.WriteString(fmt.Sprintf("- %s\n", o.Content))
		}
		sb.WriteString("\n")
	}

	if len(semantics) > 0 {
		sb.WriteString("## System Constraints & Facts\n\n")
		for _, s := range semantics {
			sb.WriteString(fmt.Sprintf("- %s\n", s.Content))
		}
		sb.WriteString("\n")
	}

	projectRoot := FindProjectRoot(cwd)
	skillDir := filepath.Join(projectRoot, ".agents", "skills", opt.SkillName)
	skillFile := filepath.Join(skillDir, "SKILL.md")
	provenanceFile := filepath.Join(skillDir, ".agent-memory-provenance.json")

	if !opt.Force && fileExists(skillFile) {
		return nil, fmt.Errorf("%w: skill file already exists at %s (use --force to overwrite)", core.ErrAlreadyExists, skillFile)
	}

	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create skill directory: %w", err)
	}

	skillContent := boundDistilledSkill(sb.String(), 11999)
	if err := os.WriteFile(skillFile, []byte(skillContent), 0o644); err != nil {
		return nil, fmt.Errorf("failed to write skill file: %w", err)
	}
	provenance := DistillProvenance{Workspace: name, MemoryIDs: selectedMemoryIDs, ToolLessonIDs: toolLessonIDs, EpisodeIDs: uniqueDistillIDs(episodeIDs)}
	provenanceJSON, err := json.MarshalIndent(provenance, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(provenanceFile, append(provenanceJSON, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("failed to write skill provenance: %w", err)
	}
	if err := store.PutDistilledSkillMetadata(ctx, core.DistilledSkillMetadata{Workspace: name, Name: opt.SkillName, Path: skillFile,
		MemoryIDs: selectedMemoryIDs, ToolLessonIDs: toolLessonIDs, EpisodeIDs: provenance.EpisodeIDs}); err != nil {
		return nil, fmt.Errorf("failed to record skill provenance: %w", err)
	}

	return &DistillResult{
		Workspace:      name,
		SkillName:      opt.SkillName,
		SkillPath:      skillFile,
		ProvenancePath: provenanceFile,
	}, nil
}

func uniqueDistillIDs(values []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func boundDistilledSkill(content string, limit int) string {
	if len(content) < limit {
		return content
	}
	end := limit - 1
	for end > 0 && !utf8.ValidString(content[:end]) {
		end--
	}
	return strings.TrimRight(content[:end], " \t\r\n") + "\n"
}
