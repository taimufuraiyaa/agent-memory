package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

const SkillDefaultEnvironment = "local"

type ExistingSkillImportIssue struct {
	Skill  string `json:"skill"`
	Reason string `json:"reason"`
}

type ExistingSkillImportResult struct {
	Imported int                        `json:"imported"`
	Skipped  int                        `json:"skipped"`
	Issues   []ExistingSkillImportIssue `json:"issues"`
}

type existingSkillImportStore interface {
	ListDistilledSkillMetadata(context.Context, string, int) ([]core.DistilledSkillMetadata, error)
	ImportSkillRevisionOne(context.Context, sqlite.SkillRevisionOneImport) (bool, error)
}

func ImportExistingSkills(ctx context.Context, store existingSkillImportStore, workspace, projectRoot string, now func() time.Time) (ExistingSkillImportResult, error) {
	result := ExistingSkillImportResult{Issues: []ExistingSkillImportIssue{}}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" || store == nil {
		return result, errors.New("workspace and store are required")
	}
	if now == nil {
		now = time.Now
	}
	root, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return result, fmt.Errorf("resolve project root: %w", err)
	}
	skillsPath := filepath.Join(root, ".agents", "skills")
	skillsRoot, err := filepath.EvalSymlinks(skillsPath)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil || !skillPathWithin(root, skillsRoot) {
		return result, errors.New("workspace skills directory is unavailable")
	}
	metadata, err := store.ListDistilledSkillMetadata(ctx, workspace, 100)
	if err != nil {
		return result, err
	}
	provenance := make(map[string]core.DistilledSkillMetadata, len(metadata))
	for _, item := range metadata {
		provenance[item.Name] = item
	}
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := entry.Name()
		bundle, description, bundleErr := inspectExistingSkillBundle(root, filepath.Join(skillsRoot, name))
		if bundleErr != nil {
			result.Skipped++
			result.Issues = append(result.Issues, ExistingSkillImportIssue{Skill: name, Reason: bundleErr.Error()})
			continue
		}
		at := now().UTC()
		skillID := deterministicSkillImportID("skill", workspace, name)
		revisionID := deterministicSkillImportID("revision", workspace, name, bundle.Digest)
		activationID := deterministicSkillImportID("activation", workspace, SkillDefaultEnvironment, name)
		prior := provenance[name]
		logical := core.LogicalSkill{
			ID: skillID, Workspace: workspace, Name: name, Description: description,
			RiskTier: core.SkillRiskMedium, OwnerGroup: "local-owner", Status: core.SkillStatusActive,
			Generation: 1, CreatedAt: at, UpdatedAt: at,
		}
		revision := core.SkillRevision{
			ID: revisionID, Workspace: workspace, SkillID: skillID, Number: 1, State: core.SkillRevisionActive,
			BundleDigest: bundle.Digest, ManifestVersion: 1, Files: bundle.Files,
			RiskTier: core.SkillRiskMedium, SourceMemoryIDs: prior.MemoryIDs, SourceToolLessonIDs: prior.ToolLessonIDs,
			SourceEpisodeIDs: prior.EpisodeIDs, CreatedBy: "existing-skill-import", CreatedAt: at,
		}
		activation := core.SkillActivation{
			ID: activationID, Workspace: workspace, Environment: SkillDefaultEnvironment, SkillID: skillID,
			ActiveRevisionID: revisionID, ActiveDigest: bundle.Digest,
			LastKnownGoodRevisionID: revisionID, LastKnownGoodDigest: bundle.Digest,
			Generation: 1, PolicyDecisionID: "existing-skill-import", Materialization: core.SkillMaterializationReady,
			ActivatedBy: "existing-skill-import", ActivatedAt: at, UpdatedAt: at,
		}
		deduplicated, importErr := store.ImportSkillRevisionOne(ctx, sqlite.SkillRevisionOneImport{Skill: logical, Revision: revision, Activation: activation})
		if importErr != nil {
			result.Skipped++
			result.Issues = append(result.Issues, ExistingSkillImportIssue{Skill: name, Reason: importErr.Error()})
			continue
		}
		if deduplicated {
			result.Skipped++
		} else {
			result.Imported++
		}
	}
	return result, nil
}

type inspectedSkillBundle struct {
	Digest string
	Files  []core.SkillBundleFile
}

func inspectExistingSkillBundle(root, skillDir string) (inspectedSkillBundle, string, error) {
	resolvedDir, err := filepath.EvalSymlinks(skillDir)
	if err != nil || !skillPathWithin(root, resolvedDir) {
		return inspectedSkillBundle{}, "", errors.New("skill directory escapes registered root")
	}
	files := make([]core.SkillBundleFile, 0)
	var description string
	err = filepath.WalkDir(resolvedDir, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(filePath)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("skill bundle contains a symlink")
		}
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("skill bundle contains a non-regular file")
		}
		if len(files) >= core.MaxSkillBundleFiles || info.Size() > core.MaxSkillBundleFileBytes {
			return errors.New("skill bundle exceeds file bounds")
		}
		resolved, err := filepath.EvalSymlinks(filePath)
		if err != nil || !skillPathWithin(resolvedDir, resolved) || !skillPathWithin(root, resolved) {
			return errors.New("skill bundle file escapes registered root")
		}
		content, err := os.ReadFile(resolved)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(resolvedDir, resolved)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == "SKILL.md" {
			if info.Size() > 12_000 {
				return errors.New("SKILL.md exceeds 12000 bytes")
			}
			description = parseExistingSkillDescription(string(content))
		}
		digest := sha256.Sum256(content)
		files = append(files, core.SkillBundleFile{Path: relative, Digest: "sha256:" + hex.EncodeToString(digest[:]), SizeBytes: info.Size()})
		return nil
	})
	if err != nil {
		return inspectedSkillBundle{}, "", err
	}
	sortSkillBundleFiles(files)
	if len(files) == 0 || files[0].Path == "" {
		return inspectedSkillBundle{}, "", errors.New("skill bundle is empty")
	}
	hasSkill := false
	for _, file := range files {
		if file.Path == "SKILL.md" {
			hasSkill = true
		}
	}
	if !hasSkill {
		return inspectedSkillBundle{}, "", errors.New("skill bundle is missing SKILL.md")
	}
	if strings.TrimSpace(description) == "" {
		description = "Imported workspace skill."
	}
	return inspectedSkillBundle{Digest: skillBundleDigest(files), Files: files}, description, nil
}

func parseExistingSkillDescription(raw string) string {
	if !strings.HasPrefix(raw, "---\n") && !strings.HasPrefix(raw, "---\r\n") {
		return ""
	}
	parts := strings.SplitN(raw, "---", 3)
	if len(parts) < 3 {
		return ""
	}
	for _, line := range strings.Split(parts[1], "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "description:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "description:")), `"'`)
		}
	}
	return ""
}

func deterministicSkillImportID(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return parts[0] + "-" + hex.EncodeToString(digest[:16])
}

func skillPathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
