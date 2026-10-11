package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func TestSkillMigrationReleaseGateRollbackDrill(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	allowMigrationCleanup(t, root)
	dir := filepath.Join(root, ".agents", "skills", "release")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: release\ndescription: release workflow\n---\n# Release v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := workspace.ImportExistingSkills(ctx, store, "ws", root, time.Now); err != nil {
		t.Fatal(err)
	}
	skills, _ := store.ListLogicalSkills(ctx, "ws", 10)
	revisions, _ := store.ListSkillRevisions(ctx, "ws", skills[0].ID, 10)
	first := revisions[0]
	content := []byte("---\nname: release\ndescription: release workflow\n---\n# Release v2\n")
	fileDigest := sha256.Sum256(content)
	file := core.SkillBundleFile{Path: "SKILL.md", Digest: "sha256:" + hex.EncodeToString(fileDigest[:]), SizeBytes: int64(len(content))}
	digest := migrationBundleDigest(file)
	second := core.SkillRevision{ID: "revision-2", Workspace: "ws", SkillID: skills[0].ID, Number: 2, State: core.SkillRevisionCanary, BundleDigest: digest, ManifestVersion: 1, Files: []core.SkillBundleFile{file}, ParentRevisionIDs: []string{first.ID}, RiskTier: core.SkillRiskLow, CreatedBy: "drill", CreatedAt: time.Now().UTC()}
	if err := store.CreateSkillRevision(ctx, second); err != nil {
		t.Fatal(err)
	}
	bundles, err := workspace.NewRevisionBundleStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundles.Publish(ctx, second, map[string][]byte{"SKILL.md": content}); err != nil {
		t.Fatal(err)
	}
	policy := core.SkillPromotionPolicy{ID: "policy", Workspace: "ws", Version: 1, RiskTier: core.SkillRiskLow, MinimumCanarySamples: 1, MinimumVerifiedSuccessRate: .9, MaximumFailureRate: .1, AllowAutomaticActivation: true, CreatedBy: "operator", CreatedAt: time.Now().UTC()}
	if err := store.CreateSkillPromotionPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	decision := core.SkillPolicyDecision{ID: "decision", Workspace: "ws", SkillID: skills[0].ID, RevisionID: second.ID, PolicyID: policy.ID, PolicyVersion: 1, EvaluationRunIDs: []string{"run"}, RiskTier: core.SkillRiskLow, Decision: core.SkillDecisionPromote, ReasonCodes: []string{"drill_passed"}, DecidedAt: time.Now().UTC()}
	if err := store.CreateSkillPolicyDecision(ctx, decision); err != nil {
		t.Fatal(err)
	}
	materializer, err := workspace.NewSkillMaterializer(root, bundles)
	if err != nil {
		t.Fatal(err)
	}
	activationService := application.NewSkillActivationService(store, materializer, time.Now)
	promoted, err := activationService.Activate(ctx, application.SkillActivationRequest{OperationID: "promote-drill", IdempotencyKey: "promote-drill", Workspace: "ws", Environment: "local", SkillID: skills[0].ID, TargetRevisionID: second.ID, ExpectedGeneration: 1, PolicyDecisionID: decision.ID, Actor: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if promoted.LastKnownGoodRevisionID != first.ID {
		t.Fatalf("promotion did not preserve rollback target: %+v", promoted)
	}
	rolledBack, err := activationService.Activate(ctx, application.SkillActivationRequest{OperationID: "rollback-drill", IdempotencyKey: "rollback-drill", Workspace: "ws", Environment: "local", SkillID: skills[0].ID, TargetRevisionID: first.ID, ExpectedGeneration: 2, PolicyDecisionID: "manual-rollback", Actor: "operator", Rollback: true, ReasonCode: "release_drill"})
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.ActiveRevisionID != first.ID || rolledBack.Generation != 3 {
		t.Fatalf("rollback drill = %+v", rolledBack)
	}
}

func migrationBundleDigest(file core.SkillBundleFile) string {
	hash := sha256.New()
	hash.Write([]byte(file.Path))
	hash.Write([]byte{0})
	hash.Write([]byte(file.Digest))
	hash.Write([]byte{0})
	hash.Write([]byte(strconv.FormatInt(file.SizeBytes, 10)))
	hash.Write([]byte{0})
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func allowMigrationCleanup(t *testing.T, root string) {
	t.Cleanup(func() {
		for _, relative := range []string{".agent-memory", ".agents"} {
			_ = filepath.Walk(filepath.Join(root, relative), func(path string, info os.FileInfo, err error) error {
				if err == nil {
					if info.IsDir() {
						_ = os.Chmod(path, 0o700)
					} else {
						_ = os.Chmod(path, 0o600)
					}
				}
				return nil
			})
		}
	})
}
