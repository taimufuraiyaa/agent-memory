package workspace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillShadowSelection struct {
	TaskID           string `json:"task_id"`
	LegacySkillName  string `json:"legacy_skill_name"`
	LifecycleSkillID string `json:"lifecycle_skill_id"`
}

type SkillMigrationDiscrepancy struct {
	Kind, Skill, Detail string
}

type SkillMigrationGateReport struct {
	Workspace           string                      `json:"workspace"`
	Ready               bool                        `json:"ready"`
	Imported            int                         `json:"imported"`
	Skipped             int                         `json:"skipped"`
	VerifiedSkills      int                         `json:"verified_skills"`
	ShadowComparisons   int                         `json:"shadow_comparisons"`
	RollbackReadySkills int                         `json:"rollback_ready_skills"`
	Discrepancies       []SkillMigrationDiscrepancy `json:"discrepancies"`
	VerifiedAt          time.Time                   `json:"verified_at"`
}

type skillMigrationGateStore interface {
	existingSkillImportStore
	ListLogicalSkills(context.Context, string, int) ([]core.LogicalSkill, error)
	GetSkillRevision(context.Context, string, string) (core.SkillRevision, error)
	GetSkillActivation(context.Context, string, string, string) (core.SkillActivation, error)
}

func RunSkillMigrationReleaseGate(ctx context.Context, store skillMigrationGateStore, workspace, projectRoot string, shadow []SkillShadowSelection, now func() time.Time) (SkillMigrationGateReport, error) {
	workspace = strings.TrimSpace(workspace)
	if store == nil || workspace == "" || strings.TrimSpace(projectRoot) == "" || len(shadow) == 0 {
		return SkillMigrationGateReport{}, errors.New("migration gate requires store, workspace, project root, and representative shadow selections")
	}
	if now == nil {
		now = time.Now
	}
	resolvedRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return SkillMigrationGateReport{}, fmt.Errorf("resolve migration project root: %w", err)
	}
	projectRoot = resolvedRoot
	report := SkillMigrationGateReport{Workspace: workspace, Discrepancies: []SkillMigrationDiscrepancy{}, VerifiedAt: now().UTC()}
	imported, err := ImportExistingSkills(ctx, store, workspace, projectRoot, now)
	if err != nil {
		return report, err
	}
	report.Imported, report.Skipped = imported.Imported, imported.Skipped
	for _, issue := range imported.Issues {
		report.Discrepancies = append(report.Discrepancies, SkillMigrationDiscrepancy{Kind: "import", Skill: issue.Skill, Detail: issue.Reason})
	}
	skills, err := store.ListLogicalSkills(ctx, workspace, 200)
	if err != nil {
		return report, err
	}
	byID, byName := map[string]core.LogicalSkill{}, map[string]core.LogicalSkill{}
	bundles, err := NewRevisionBundleStore(projectRoot)
	if err != nil {
		return report, err
	}
	materializer, err := NewSkillMaterializer(projectRoot, bundles)
	if err != nil {
		return report, err
	}
	artifacts, err := NewSkillArtifactVerifier(bundles, materializer)
	if err != nil {
		return report, err
	}
	for _, skill := range skills {
		byID[skill.ID], byName[skill.Name] = skill, skill
		activation, loadErr := store.GetSkillActivation(ctx, workspace, SkillDefaultEnvironment, skill.ID)
		if loadErr != nil {
			report.add("activation", skill.Name, loadErr.Error())
			continue
		}
		active, loadErr := store.GetSkillRevision(ctx, workspace, activation.ActiveRevisionID)
		if loadErr != nil {
			report.add("revision", skill.Name, loadErr.Error())
			continue
		}
		rootBundle, _, inspectErr := inspectExistingSkillBundle(filepath.Clean(projectRoot), filepath.Join(projectRoot, ".agents", "skills", skill.Name))
		if inspectErr != nil {
			report.add("materialization", skill.Name, inspectErr.Error())
			continue
		}
		if rootBundle.Digest != activation.ActiveDigest || active.BundleDigest != activation.ActiveDigest || activation.Materialization != core.SkillMaterializationReady {
			report.add("digest", skill.Name, "active database, immutable revision, and root materialization do not agree")
			continue
		}
		if verifyErr := artifacts.VerifyActive(ctx, skill, active); verifyErr != nil {
			report.add("materialization", skill.Name, verifyErr.Error())
			continue
		}
		report.VerifiedSkills++
		if activation.LastKnownGoodRevisionID != "" {
			lastGood, lastGoodErr := store.GetSkillRevision(ctx, workspace, activation.LastKnownGoodRevisionID)
			if lastGoodErr != nil || lastGood.BundleDigest != activation.LastKnownGoodDigest {
				report.add("rollback", skill.Name, "last-known-good identity is invalid")
				continue
			}
			if verifyErr := artifacts.VerifyImmutable(ctx, lastGood); verifyErr != nil {
				report.add("rollback", skill.Name, verifyErr.Error())
				continue
			}
			report.RollbackReadySkills++
		}
	}
	for _, comparison := range shadow {
		if strings.TrimSpace(comparison.TaskID) == "" {
			report.add("shadow", comparison.LegacySkillName, "task id is required")
			continue
		}
		legacy, legacyOK := byName[comparison.LegacySkillName]
		lifecycle, lifecycleOK := byID[comparison.LifecycleSkillID]
		if !legacyOK || !lifecycleOK || legacy.ID != lifecycle.ID {
			report.add("shadow", comparison.LegacySkillName, fmt.Sprintf("legacy and lifecycle selection differ for task %s", comparison.TaskID))
			continue
		}
		report.ShadowComparisons++
	}
	report.Ready = len(report.Discrepancies) == 0 && report.VerifiedSkills == len(skills) && report.ShadowComparisons == len(shadow) && report.RollbackReadySkills == len(skills)
	return report, nil
}

func (r *SkillMigrationGateReport) add(kind, skill, detail string) {
	r.Discrepancies = append(r.Discrepancies, SkillMigrationDiscrepancy{Kind: kind, Skill: skill, Detail: detail})
}
