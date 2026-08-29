package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
)

const (
	maxSkillRevisionDiffEntries = 256
	maxSkillDraftTotalBytes     = 32 * 1024 * 1024
	maxSkillDraftTextBytes      = 256 * 1024
)

type SkillRevisionBuildInput struct {
	Workspace, CandidateID, SkillName, Description, OwnerGroup, CreatedBy string
	ProposedFiles                                                         map[string][]byte
	RemovalReasons                                                        map[string]string
	Compatibility                                                         core.SkillCompatibility
	ProtectedSections                                                     []string
}

type SkillRevisionDiff struct {
	Added, Changed, Removed []string          `json:"added,omitempty"`
	RemovalReasons          map[string]string `json:"removal_reasons,omitempty"`
}

type SkillRevisionAdmission struct {
	Path        string                              `json:"path"`
	Disposition engine.SolutionAdmissionDisposition `json:"disposition"`
	Reason      engine.SolutionAdmissionReason      `json:"reason"`
}

type SkillRevisionBuildResult struct {
	Skill     core.LogicalSkill        `json:"skill"`
	Revision  core.SkillRevision       `json:"revision"`
	Diff      SkillRevisionDiff        `json:"diff"`
	Admission []SkillRevisionAdmission `json:"admission"`
	Bundle    SkillPublishedBundle     `json:"bundle"`
	Replayed  bool                     `json:"replayed"`
}

type SkillRevisionBuilderRepository interface {
	GetSkillCandidate(context.Context, string, string) (core.SkillCandidate, error)
	GetLogicalSkill(context.Context, string, string) (core.LogicalSkill, error)
	ListSkillRevisions(context.Context, string, string, int) ([]core.SkillRevision, error)
	GetSkillRevisionForCandidate(context.Context, string, string) (core.SkillRevision, error)
	CreateLogicalSkill(context.Context, core.LogicalSkill) error
	CreateSkillRevision(context.Context, core.SkillRevision) error
}

type SkillRevisionBundleStore interface {
	PublishRevision(context.Context, core.SkillRevision, map[string][]byte) (string, bool, error)
	ReadRevision(context.Context, core.SkillRevision) (map[string][]byte, error)
}

type SkillPublishedBundle struct {
	Digest    string `json:"digest"`
	Duplicate bool   `json:"duplicate"`
}

type SkillRevisionBuilder struct {
	repository SkillRevisionBuilderRepository
	bundles    SkillRevisionBundleStore
	admission  *engine.SolutionAdmissionPolicy
	now        func() time.Time
}

func NewSkillRevisionBuilder(repository SkillRevisionBuilderRepository, bundles SkillRevisionBundleStore) *SkillRevisionBuilder {
	return &SkillRevisionBuilder{repository: repository, bundles: bundles, admission: engine.NewSolutionAdmissionPolicy(), now: time.Now}
}

func (b *SkillRevisionBuilder) Build(ctx context.Context, input SkillRevisionBuildInput) (SkillRevisionBuildResult, error) {
	if b == nil || b.repository == nil || b.bundles == nil {
		return SkillRevisionBuildResult{}, errors.New("revision builder dependencies are required")
	}
	input.Workspace, input.CandidateID, input.SkillName, input.CreatedBy = strings.TrimSpace(input.Workspace), strings.TrimSpace(input.CandidateID), strings.TrimSpace(input.SkillName), strings.TrimSpace(input.CreatedBy)
	if input.Workspace == "" || input.CandidateID == "" || input.CreatedBy == "" {
		return SkillRevisionBuildResult{}, errors.New("workspace, candidate_id, and created_by are required")
	}
	candidate, err := b.repository.GetSkillCandidate(ctx, input.Workspace, input.CandidateID)
	if err != nil {
		return SkillRevisionBuildResult{}, err
	}
	if candidate.State != core.SkillCandidateProposed && candidate.State != core.SkillCandidateAccepted {
		return SkillRevisionBuildResult{}, errors.New("candidate is not buildable")
	}
	if existing, existingErr := b.repository.GetSkillRevisionForCandidate(ctx, input.Workspace, candidate.ID); existingErr == nil {
		skill, err := b.repository.GetLogicalSkill(ctx, input.Workspace, existing.SkillID)
		if err != nil {
			return SkillRevisionBuildResult{}, err
		}
		contents, err := b.bundles.ReadRevision(ctx, existing)
		if err != nil {
			return SkillRevisionBuildResult{}, err
		}
		digest, duplicate, err := b.bundles.PublishRevision(ctx, existing, contents)
		if err != nil {
			return SkillRevisionBuildResult{}, err
		}
		return SkillRevisionBuildResult{Skill: skill, Revision: existing, Bundle: SkillPublishedBundle{Digest: digest, Duplicate: duplicate}, Replayed: true}, nil
	}

	var skill core.LogicalSkill
	var parent *core.SkillRevision
	baseFiles := map[string][]byte{}
	if candidate.Kind == core.SkillCandidateRevise || candidate.Kind == core.SkillCandidateSplit {
		if len(candidate.TargetSkillIDs) != 1 {
			return SkillRevisionBuildResult{}, errors.New("revision candidate target is invalid")
		}
		skill, err = b.repository.GetLogicalSkill(ctx, input.Workspace, candidate.TargetSkillIDs[0])
		if err != nil {
			return SkillRevisionBuildResult{}, err
		}
		revisions, listErr := b.repository.ListSkillRevisions(ctx, input.Workspace, skill.ID, 1)
		if listErr != nil || len(revisions) == 0 {
			return SkillRevisionBuildResult{}, errors.New("active parent revision is required")
		}
		parent = &revisions[0]
		baseFiles, err = b.bundles.ReadRevision(ctx, *parent)
		if err != nil {
			return SkillRevisionBuildResult{}, err
		}
	} else {
		if input.SkillName == "" {
			return SkillRevisionBuildResult{}, errors.New("skill_name is required for create or merge candidates")
		}
		now := b.now().UTC()
		owner := strings.TrimSpace(input.OwnerGroup)
		if owner == "" {
			owner = "agent-memory"
		}
		description := strings.TrimSpace(input.Description)
		if description == "" {
			description = candidate.Summary
		}
		skill = core.LogicalSkill{ID: "skill-" + shortSkillHash(input.Workspace+"\x00"+input.SkillName), Workspace: input.Workspace, Name: input.SkillName,
			Description: description, TriggerConditions: []string{candidate.Summary}, Capabilities: []string{candidate.ExpectedBenefit}, RiskTier: candidate.RiskTier,
			OwnerGroup: owner, Status: core.SkillStatusActive, Generation: 1, CreatedAt: now, UpdatedAt: now}
	}
	files := cloneSkillFiles(input.ProposedFiles)
	if len(files) == 0 {
		files = cloneSkillFiles(baseFiles)
	}
	if len(files) == 0 || len(files) > core.MaxSkillBundleFiles {
		return SkillRevisionBuildResult{}, errors.New("proposed files are required and bounded")
	}
	if _, ok := files["SKILL.md"]; !ok {
		return SkillRevisionBuildResult{}, errors.New("draft revision cannot remove SKILL.md")
	}
	diff, err := buildSkillRevisionDiff(baseFiles, files, input.RemovalReasons)
	if err != nil {
		return SkillRevisionBuildResult{}, err
	}
	protected := sortedRecurrenceSet(append(append([]string(nil), input.ProtectedSections...), func() []string {
		if parent != nil {
			return parent.ProtectedSections
		}
		return nil
	}()...))
	if parent != nil {
		if err := preserveSkillSections(baseFiles["SKILL.md"], files["SKILL.md"], protected); err != nil {
			return SkillRevisionBuildResult{}, err
		}
	}
	admission, err := b.admitSkillFiles(ctx, input.Workspace, files)
	if err != nil {
		return SkillRevisionBuildResult{}, err
	}
	if err := validateSkillDraftDocument(files, skill.Name, parent == nil); err != nil {
		return SkillRevisionBuildResult{}, err
	}
	manifest, digest, err := buildSkillManifest(files)
	if err != nil {
		return SkillRevisionBuildResult{}, err
	}
	number, parents := int64(1), []string(nil)
	if parent != nil {
		if parent.BundleDigest == digest {
			return SkillRevisionBuildResult{}, errors.New("draft revision must change the parent bundle")
		}
		number, parents = parent.Number+1, []string{parent.ID}
	}
	now := b.now().UTC()
	revision := core.SkillRevision{ID: "revision-" + shortSkillHash(candidate.ID+"\x00"+digest), Workspace: input.Workspace, SkillID: skill.ID,
		Number: number, State: core.SkillRevisionDraft, BundleDigest: digest, ManifestVersion: 1, Files: manifest, ParentRevisionIDs: parents,
		CandidateID: candidate.ID, Compatibility: input.Compatibility, RiskTier: candidate.RiskTier, ProtectedSections: protected,
		SourceMemoryIDs: append([]string(nil), candidate.SourceMemoryIDs...), SourceToolLessonIDs: append([]string(nil), candidate.SourceToolLessonIDs...), SourceEpisodeIDs: append([]string(nil), candidate.SourceEpisodeIDs...), CreatedBy: input.CreatedBy, CreatedAt: now}
	if err := revision.Validate(); err != nil {
		return SkillRevisionBuildResult{}, err
	}
	publishedDigest, duplicate, err := b.bundles.PublishRevision(ctx, revision, files)
	if err != nil {
		return SkillRevisionBuildResult{}, err
	}
	if parent == nil {
		if err := skill.Validate(); err != nil {
			return SkillRevisionBuildResult{}, err
		}
		if err := b.repository.CreateLogicalSkill(ctx, skill); err != nil {
			return SkillRevisionBuildResult{}, err
		}
	}
	if err := b.repository.CreateSkillRevision(ctx, revision); err != nil {
		return SkillRevisionBuildResult{}, err
	}
	return SkillRevisionBuildResult{Skill: skill, Revision: revision, Diff: diff, Admission: admission, Bundle: SkillPublishedBundle{Digest: publishedDigest, Duplicate: duplicate}}, nil
}

func (b *SkillRevisionBuilder) admitSkillFiles(ctx context.Context, workspaceID string, files map[string][]byte) ([]SkillRevisionAdmission, error) {
	paths := make([]string, 0, len(files))
	for name := range files {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	results := make([]SkillRevisionAdmission, 0)
	for _, name := range paths {
		if !isSkillTextFile(name) {
			continue
		}
		raw := files[name]
		if name == "SKILL.md" && len(raw) > 12_000 {
			return nil, errors.New("SKILL.md exceeds 12000 bytes")
		}
		if len(raw) > maxSkillDraftTextBytes {
			return nil, fmt.Errorf("text asset %q exceeds admission bound", name)
		}
		for offset := 0; offset < len(raw); {
			end := offset + core.MaxSolutionStateItemBytes
			if end > len(raw) {
				end = len(raw)
			}
			decision := b.admission.Evaluate(ctx, engine.SolutionAdmissionInput{Workspace: workspaceID, Origin: engine.SolutionOriginAgent, Field: engine.SolutionFieldWorkingStateItem, Content: string(raw[offset:end])})
			if decision.Disposition != engine.SolutionAdmissionAllow {
				return nil, fmt.Errorf("text asset %q admission %s: %s", name, decision.Disposition, decision.Reason)
			}
			if end == len(raw) {
				break
			}
			offset = end - 128
		}
		results = append(results, SkillRevisionAdmission{Path: name, Disposition: engine.SolutionAdmissionAllow, Reason: engine.SolutionAdmissionAccepted})
	}
	return results, nil
}

func buildSkillRevisionDiff(base, proposed map[string][]byte, reasons map[string]string) (SkillRevisionDiff, error) {
	diff := SkillRevisionDiff{RemovalReasons: map[string]string{}}
	for name, content := range proposed {
		previous, exists := base[name]
		if !exists {
			diff.Added = append(diff.Added, name)
		} else if string(previous) != string(content) {
			diff.Changed = append(diff.Changed, name)
		}
	}
	for name := range base {
		if _, exists := proposed[name]; !exists {
			reason := strings.TrimSpace(reasons[name])
			if reason == "" {
				return SkillRevisionDiff{}, fmt.Errorf("removed asset %q requires an explanation", name)
			}
			diff.Removed = append(diff.Removed, name)
			diff.RemovalReasons[name] = reason
		}
	}
	sort.Strings(diff.Added)
	sort.Strings(diff.Changed)
	sort.Strings(diff.Removed)
	if len(diff.Added)+len(diff.Changed)+len(diff.Removed) > maxSkillRevisionDiffEntries {
		return SkillRevisionDiff{}, errors.New("revision diff exceeds bound")
	}
	return diff, nil
}

func preserveSkillSections(base, proposed []byte, protected []string) error {
	for _, heading := range protected {
		before, ok := markdownSkillSection(string(base), heading)
		if !ok {
			return fmt.Errorf("protected section %q is missing from parent", heading)
		}
		after, ok := markdownSkillSection(string(proposed), heading)
		if !ok || before != after {
			return fmt.Errorf("protected section %q must remain unchanged", heading)
		}
	}
	return nil
}
func markdownSkillSection(content, heading string) (string, bool) {
	lines := strings.Split(content, "\n")
	target := strings.TrimSpace(strings.TrimLeft(heading, "#"))
	start := -1
	level := 0
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		currentLevel := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
		title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		if start < 0 && title == target {
			start, level = index, currentLevel
			continue
		}
		if start >= 0 && currentLevel <= level {
			return strings.Join(lines[start:index], "\n"), true
		}
	}
	if start >= 0 {
		return strings.Join(lines[start:], "\n"), true
	}
	return "", false
}
func buildSkillManifest(files map[string][]byte) ([]core.SkillBundleFile, string, error) {
	if len(files) > core.MaxSkillBundleFiles {
		return nil, "", errors.New("skill bundle file count exceeds bound")
	}
	manifest := make([]core.SkillBundleFile, 0, len(files))
	totalBytes := 0
	for name, raw := range files {
		totalBytes += len(raw)
		if totalBytes > maxSkillDraftTotalBytes {
			return nil, "", errors.New("skill draft total size exceeds bound")
		}
		if path.Clean(name) != name || strings.HasPrefix(name, "../") || int64(len(raw)) > core.MaxSkillBundleFileBytes {
			return nil, "", fmt.Errorf("unsafe or oversized skill asset %q", name)
		}
		sum := sha256.Sum256(raw)
		manifest = append(manifest, core.SkillBundleFile{Path: name, Digest: "sha256:" + hex.EncodeToString(sum[:]), SizeBytes: int64(len(raw))})
	}
	sort.Slice(manifest, func(i, j int) bool { return manifest[i].Path < manifest[j].Path })
	hash := sha256.New()
	for _, file := range manifest {
		hash.Write([]byte(file.Path))
		hash.Write([]byte{0})
		hash.Write([]byte(file.Digest))
		hash.Write([]byte{0})
		hash.Write([]byte(strconv.FormatInt(file.SizeBytes, 10)))
		hash.Write([]byte{0})
	}
	return manifest, "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
func cloneSkillFiles(input map[string][]byte) map[string][]byte {
	result := make(map[string][]byte, len(input))
	for name, raw := range input {
		result[name] = append([]byte(nil), raw...)
	}
	return result
}
func isSkillTextFile(name string) bool {
	extension := strings.ToLower(path.Ext(name))
	return extension == ".md" || extension == ".txt" || extension == ".json" || extension == ".yaml" || extension == ".yml"
}

var skillDraftLinkPattern = regexp.MustCompile(`\]\(([^)]+)\)`)

func validateSkillDraftDocument(files map[string][]byte, skillName string, requireFrontmatter bool) error {
	raw := string(files["SKILL.md"])
	if strings.HasPrefix(raw, "---\n") {
		end := strings.Index(raw[4:], "\n---\n")
		if end < 0 {
			return errors.New("SKILL.md frontmatter is not terminated")
		}
		frontmatter := raw[4 : 4+end]
		if !strings.Contains(frontmatter, "name: "+skillName) || !strings.Contains(frontmatter, "description:") {
			return errors.New("SKILL.md frontmatter must bind name and description")
		}
	} else if requireFrontmatter {
		return errors.New("new skill draft requires SKILL.md frontmatter")
	}
	for _, match := range skillDraftLinkPattern.FindAllStringSubmatch(raw, -1) {
		target := strings.TrimSpace(strings.Split(match[1], "#")[0])
		if target == "" || strings.Contains(target, "://") {
			continue
		}
		target = path.Clean(target)
		if strings.HasPrefix(target, "../") {
			return errors.New("SKILL.md contains an unsafe relative reference")
		}
		if _, exists := files[target]; !exists {
			return fmt.Errorf("SKILL.md reference %q is missing from the bundle", target)
		}
	}
	return nil
}
func shortSkillHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}
