package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// DistillOptions holds parameters for packaging workspace memories into a skill.
type DistillOptions struct {
	Workspace   string
	SkillName   string
	Description string
	Force       bool
}

// DistillResult represents the metadata of the distilled skill.
type DistillResult struct {
	Workspace string `json:"workspace"`
	SkillName string `json:"skill_name"`
	SkillPath string `json:"skill_path"`
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

	if !opt.Force && fileExists(skillFile) {
		return nil, fmt.Errorf("%w: skill file already exists at %s (use --force to overwrite)", core.ErrAlreadyExists, skillFile)
	}

	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create skill directory: %w", err)
	}

	if err := os.WriteFile(skillFile, []byte(sb.String()), 0o644); err != nil {
		return nil, fmt.Errorf("failed to write skill file: %w", err)
	}

	return &DistillResult{
		Workspace: name,
		SkillName: opt.SkillName,
		SkillPath: skillFile,
	}, nil
}
