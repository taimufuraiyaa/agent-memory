package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
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
	Workspace            string                  `json:"workspace"`
	SkillName            string                  `json:"skill_name"`
	SkillPath            string                  `json:"skill_path"`
	ProvenancePath       string                  `json:"provenance_path"`
	CandidateID          string                  `json:"candidate_id"`
	RevisionID           string                  `json:"revision_id"`
	RevisionNumber       int64                   `json:"revision_number"`
	RevisionDigest       string                  `json:"revision_digest"`
	RevisionState        core.SkillRevisionState `json:"revision_state"`
	ActivePreserved      bool                    `json:"active_preserved"`
	CompatibilityMessage string                  `json:"compatibility_message"`
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
	if len(selectedMemoryIDs) == 0 {
		for _, memory := range memories {
			selectedMemoryIDs = append(selectedMemoryIDs, memory.ID)
		}
		selectedMemoryIDs = uniqueDistillIDs(selectedMemoryIDs)
	}
	if len(selectedMemoryIDs) == 0 && len(toolLessonIDs) == 0 {
		return nil, fmt.Errorf("distill requires focused reusable evidence")
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

	skillContent := boundDistilledSkill(sb.String(), 11999)
	provenance := DistillProvenance{Workspace: name, MemoryIDs: selectedMemoryIDs, ToolLessonIDs: toolLessonIDs, EpisodeIDs: uniqueDistillIDs(episodeIDs)}
	provenanceJSON, err := json.MarshalIndent(provenance, "", "  ")
	if err != nil {
		return nil, err
	}

	projectRoot := FindProjectRoot(cwd)
	if _, err := ImportExistingSkills(ctx, store, name, projectRoot, time.Now); err != nil {
		return nil, fmt.Errorf("import active skills before distill: %w", err)
	}
	logicalSkills, err := store.ListLogicalSkills(ctx, name, 200)
	if err != nil {
		return nil, err
	}
	var existing *core.LogicalSkill
	for index := range logicalSkills {
		if logicalSkills[index].Name == opt.SkillName {
			existing = &logicalSkills[index]
			break
		}
	}
	kind, targets, risk := core.SkillCandidateCreate, []string(nil), core.SkillRiskMedium
	proposedFiles := map[string][]byte{"SKILL.md": []byte(skillContent), ".agent-memory-provenance.json": append(provenanceJSON, '\n')}
	if existing != nil {
		kind, targets, risk = core.SkillCandidateRevise, []string{existing.ID}, existing.RiskTier
		revisions, listErr := store.ListSkillRevisions(ctx, name, existing.ID, 1)
		if listErr != nil || len(revisions) == 0 {
			return nil, errors.New("active skill revision is unavailable")
		}
		bundles, bundleErr := NewRevisionBundleStore(projectRoot)
		if bundleErr != nil {
			return nil, bundleErr
		}
		base, readErr := bundles.ReadRevision(ctx, revisions[0])
		if readErr != nil {
			base, readErr = readActiveDistillBundle(projectRoot, opt.SkillName, revisions[0])
			if readErr != nil {
				return nil, readErr
			}
			if _, _, readErr = bundles.PublishRevision(ctx, revisions[0], base); readErr != nil {
				return nil, readErr
			}
		}
		proposedFiles = cloneDistillFiles(base)
		proposedFiles["SKILL.md"] = []byte(boundDistilledSkill(string(base["SKILL.md"])+"\n\n## Proposed distilled learnings\n\n"+skillContent, 11999))
		proposedFiles[".agent-memory-provenance.json"] = append(provenanceJSON, '\n')
	}
	identityParts := append([]string{name, opt.SkillName, string(kind), string(skillContent)}, selectedMemoryIDs...)
	identityParts = append(identityParts, toolLessonIDs...)
	sum := sha256.Sum256([]byte(strings.Join(identityParts, "\x00")))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	now := time.Now().UTC()
	candidate := core.SkillCandidate{ID: "candidate-distill-" + hex.EncodeToString(sum[:12]), Workspace: name, Kind: kind, TargetSkillIDs: targets,
		Summary: "Focused distillation for " + opt.SkillName, ExpectedBenefit: "Package validated reusable workspace knowledge into a reviewable skill draft.",
		RiskTier: risk, Confidence: .9, State: core.SkillCandidateProposed, SourceMemoryIDs: selectedMemoryIDs, SourceToolLessonIDs: toolLessonIDs,
		SourceEpisodeIDs: provenance.EpisodeIDs, DeduplicationHash: digest, CreatedBy: "distill", CreatedAt: now, UpdatedAt: now}
	storedCandidate, _, err := store.PutSkillCandidate(ctx, candidate)
	if err != nil {
		return nil, err
	}
	bundleStore, err := NewRevisionBundleStore(projectRoot)
	if err != nil {
		return nil, err
	}
	built, err := application.NewSkillRevisionBuilder(store, bundleStore).Build(ctx, application.SkillRevisionBuildInput{Workspace: name, CandidateID: storedCandidate.ID,
		SkillName: opt.SkillName, Description: desc, OwnerGroup: "local-owner", CreatedBy: "distill", ProposedFiles: proposedFiles})
	if err != nil {
		return nil, err
	}
	objectRoot := filepath.Join(projectRoot, ".agent-memory", "skill-revisions", "objects", "sha256", strings.TrimPrefix(built.Revision.BundleDigest, "sha256:"), "bundle")
	skillFile, provenanceFile := filepath.Join(objectRoot, "SKILL.md"), filepath.Join(objectRoot, ".agent-memory-provenance.json")
	if err := store.PutDistilledSkillMetadata(ctx, core.DistilledSkillMetadata{Workspace: name, Name: opt.SkillName, Path: skillFile, MemoryIDs: selectedMemoryIDs, ToolLessonIDs: toolLessonIDs, EpisodeIDs: provenance.EpisodeIDs}); err != nil {
		return nil, fmt.Errorf("failed to record skill provenance: %w", err)
	}
	message := "Created immutable draft; active skill remains unchanged until evaluation, approval, canary, and promotion succeed."
	if opt.Force {
		message = "--force is compatibility-only; created or replayed an immutable draft and preserved the active skill."
	}
	return &DistillResult{Workspace: name, SkillName: opt.SkillName, SkillPath: skillFile, ProvenancePath: provenanceFile, CandidateID: storedCandidate.ID,
		RevisionID: built.Revision.ID, RevisionNumber: built.Revision.Number, RevisionDigest: built.Revision.BundleDigest, RevisionState: built.Revision.State, ActivePreserved: true, CompatibilityMessage: message}, nil
}

func cloneDistillFiles(input map[string][]byte) map[string][]byte {
	result := make(map[string][]byte, len(input))
	for name, raw := range input {
		result[name] = append([]byte(nil), raw...)
	}
	return result
}

func readActiveDistillBundle(projectRoot, skillName string, revision core.SkillRevision) (map[string][]byte, error) {
	root, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return nil, err
	}
	skillRoot, err := filepath.EvalSymlinks(filepath.Join(root, ".agents", "skills", skillName))
	if err != nil || !skillPathWithin(root, skillRoot) {
		return nil, errors.New("active skill bundle is unavailable")
	}
	contents := make(map[string][]byte, len(revision.Files))
	for _, declared := range revision.Files {
		candidate := filepath.Join(skillRoot, filepath.FromSlash(declared.Path))
		resolved, resolveErr := filepath.EvalSymlinks(candidate)
		if resolveErr != nil || !skillPathWithin(skillRoot, resolved) {
			return nil, fmt.Errorf("active skill asset %q escapes its bundle", declared.Path)
		}
		info, statErr := os.Lstat(candidate)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("active skill asset %q is not a regular file", declared.Path)
		}
		raw, readErr := os.ReadFile(candidate)
		if readErr != nil {
			return nil, readErr
		}
		contents[declared.Path] = raw
	}
	return contents, nil
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
